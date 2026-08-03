package main

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT bearer token from POST /oam/login, sent as "Bearer <token>".

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/handlers"
	"github.com/loxilb-io/loxilb-oam/internal/routes"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

// requireSecrets aborts startup when a mandatory secret is unset. There are no
// built-in fallbacks, so a misconfigured deployment fails fast instead of
// silently running on a known credential.
func requireSecrets() {
	var missing []string
	if utils.JWTSecretMissing() {
		missing = append(missing, "OAM_JWT_SECRET")
	}
	if config.AdminPasswordMissing() {
		missing = append(missing, "OAM_DEFAULT_ADMIN_PASSWORD")
	}
	if len(missing) > 0 {
		utils.LogError("SECURITY: required secret(s) not set: " + strings.Join(missing, ", ") + ". Set them in the environment before starting.")
		os.Exit(1)
	}
	if config.CORSUsingWildcard() {
		utils.LogWarning("SECURITY: OAM_ALLOWED_ORIGINS is not set — CORS allows any origin (*). Set a comma-separated origin allowlist in production.")
	}
	if config.InstanceTLSInsecure() {
		utils.LogWarning("SECURITY: OAM_INSTANCE_TLS_INSECURE=true — TLS verification is DISABLED for connections to managed LoxiLB instances (proxy + snapshots). Prefer OAM_INSTANCE_CA_BUNDLE in production.")
	}
}

func main() {
	// Define command-line flags for the database connection details
	dbUser := flag.String("db-user", "oamuser", "Database username")
	dbPassword := flag.String("db-password", "", "Database password (or set OAM_DB_PASSWORD)")
	dbHost := flag.String("db-host", "127.0.0.1", "Database host")
	dbPort := flag.String("db-port", "3306", "Database port")
	dbName := flag.String("db-name", "loxioam", "Database name")
	googleRedirectURL := flag.String("google-redirect-url", "", "Google OAuth Redirect URL")
	githubRedirectURL := flag.String("github-redirect-url", "", "GitHub OAuth Redirect URL")
	facebookRedirectURL := flag.String("facebook-redirect-url", "", "Facebook OAuth Redirect URL")
	sslOption := flag.String("ssl-option", "false", "Enable SSL connection")
	sslCaCertFilePath := flag.String("ssl-ca-cert-file", "./ssl/certs/root-ca.pem", "SSL CA certificate file path")
	sslCaClientCertFilePath := flag.String("ssl-ca-client-cert-file", "./ssl/certs/client-cert.pem", "SSL client certificate file path")
	sslCaClientKeyFilePath := flag.String("ssl-ca-client-key-file", "./ssl/certs/client-key.pem", "SSL client key file path")

	// Define a command-line flag for the token expiration time in minutes.
	// Empty = use OAM_TOKEN_TTL_MINUTES (default 480 = 8h, see config.TokenExpirationMinutes).
	expirationMinutesFlag := flag.String("token-expiration", "", "Token expiration time in minutes (default: OAM_TOKEN_TTL_MINUTES env or 480)")

	// Define a command-line flag for the server port
	serverPort := flag.String("port", "8080", "Server port")

	// SSL/TLS Settings
	enableHTTPS := flag.Bool("enable-https", false, "Enable HTTPS service")
	sslCertFile := flag.String("ssl-cert-file", "./ssl/certs/server.crt", "Path to SSL certificate")
	sslKeyFile := flag.String("ssl-key-file", "./ssl/certs/server.key", "Path to SSL private key")

	flag.Parse()

	// Abort startup if any mandatory secret is unset.
	requireSecrets()

	// The database password has no built-in default: require it via flag or env.
	dbPass := *dbPassword
	if dbPass == "" {
		dbPass = os.Getenv("OAM_DB_PASSWORD")
	}
	if dbPass == "" {
		utils.LogError("SECURITY: no database password provided. Set OAM_DB_PASSWORD or pass -db-password.")
		os.Exit(1)
	}

	// OAuth is opt-in (disabled by default). Only initialize it when enabled.
	if config.OAuthEnabled() {
		config.InitOAuthConfigs(*googleRedirectURL, *githubRedirectURL, *facebookRedirectURL)
		utils.LogInfo("OAuth login is ENABLED (OAM_OAUTH_ENABLED=true).")
	}

	// Connect to the MySQL database with retry logic. If the SSL option is enabled, connect securely
	var db *sql.DB
	var err error

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", *dbUser, dbPass, *dbHost, *dbPort, *dbName)

	// Create the DSN (Data Source Name) from the command-line flags
	if *sslOption == "true" {
		db, err = services.ConnectWithSecureTLS(dsn, config.DbMaxRetries, config.DbRetryBackoff, *sslCaCertFilePath, *sslCaClientCertFilePath, *sslCaClientKeyFilePath)
	} else {
		db, err = services.ConnectWithRetry(dsn, config.DbMaxRetries, config.DbRetryBackoff)
	}
	if err != nil {
		utils.LogError(fmt.Sprintf("Database connection failed (host=%s port=%s db=%s): %v", *dbHost, *dbPort, *dbName, err))
		return
	}
	defer db.Close()

	// Configure the connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Initialize the services
	userService := services.NewUserService(db)
	loxiLBService := services.NewLoxiLBService(db)
	logService := services.NewLogService(db)
	alertService := services.NewAlertService(db)
	proxyService := services.NewProxyService(loxiLBService)
	snapshotService, err := services.NewSnapshotService(db, loxiLBService)
	if err != nil {
		// A set-but-invalid SNAPSHOT_ENC_KEY must fail boot loudly rather
		// than silently storing secret-bearing snapshots unencrypted.
		utils.LogError(fmt.Sprintf("Failed to initialize snapshot service: %s", err))
		return
	}
	// pollingService := polling.NewPollingService(alertService, loxiLBService)

	// -token-expiration (when passed) overrides OAM_TOKEN_TTL_MINUTES.
	// config.TokenExpirationMinutes is also what SaveToken uses for the DB row
	// expiry, so JWT and stored-token lifetimes stay in sync.
	if *expirationMinutesFlag != "" {
		expirationMinutes, err := strconv.Atoi(*expirationMinutesFlag)
		if err != nil || expirationMinutes <= 0 {
			utils.LogError(fmt.Sprintf("Invalid token expiration time: %s", *expirationMinutesFlag))
			return
		}
		config.TokenExpirationMinutes = expirationMinutes
	}
	handler := handlers.NewHandler(userService, loxiLBService, logService, alertService, proxyService, snapshotService, config.TokenExpirationMinutes)

	// First-time setup: create the bootstrap admin if needed. A fresh
	// installation without an admin account is unusable (every login fails),
	// so abort startup instead of running in that state — most commonly hit
	// when OAM_DEFAULT_ADMIN_PASSWORD does not meet the password policy.
	if err := setupInitialAdminIfNeeded(userService); err != nil {
		utils.LogError(fmt.Sprintf("SECURITY: failed to set up the initial admin account: %s. Fix the configuration (typically OAM_DEFAULT_ADMIN_PASSWORD — it must meet the account password policy) and restart.", err))
		os.Exit(1)
	}

	// Start polling in a separate goroutine
	// go pollingService.StartPolling(config.PollingInterval, config.PollingRefresh)

	// Snapshot scheduler: scheduled snapshots, retention, and daily integrity sweep.
	snapshotScheduler := services.NewSnapshotScheduler(snapshotService)
	go snapshotScheduler.Start()

	// Graceful shutdown handling
	ctx, cancel := context.WithCancel(context.Background())
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Create a mutex for synchronizing access to the db pointer
	var mu sync.Mutex

	// Start a goroutine to periodically check the database connection health
	go func() {
		for {
			select {
			case <-ctx.Done():
				utils.LogInfo("Stopping database health check...")
				return
			default:
				time.Sleep(config.DbRetryDelay)
				mu.Lock()
				if err := db.Ping(); err != nil {
					utils.LogError(fmt.Sprintf("Database connection lost: %s", err))
					if *sslOption == "true" {
						newDB, err := services.ConnectWithSecureTLS(dsn, config.DbMaxRetries, config.DbRetryBackoff, *sslCaCertFilePath, *sslCaClientCertFilePath, *sslCaClientKeyFilePath)
						if err != nil {
							utils.LogError(fmt.Sprintf("Reconnection failed: %s", err))
						} else {
							db = newDB
							utils.LogInfo("Database SSL connection re-established")
						}
					} else {
						newDB, err := services.ConnectWithRetry(dsn, config.DbMaxRetries, config.DbRetryBackoff)
						if err != nil {
							utils.LogError(fmt.Sprintf("Reconnection failed: %s", err))
						} else {
							db.Close()
							db = newDB
							utils.LogInfo("Database connection re-established")
						}
					}
				}
				mu.Unlock()
			}
		}
	}()

	router := gin.Default()
	routes.SetupRoutes(router, db, dsn, *sslOption, *sslCaCertFilePath, *sslCaClientCertFilePath, *sslCaClientKeyFilePath, handler, userService, &mu, alertService)

	// Create HTTP/HTTPS server
	port := fmt.Sprintf(":%s", *serverPort)

	httpServer := &http.Server{
		Addr:    port,
		Handler: router,
	}
	// Start HTTP or HTTPS based on the flag
	go func() {
		if *enableHTTPS {
			utils.LogInfo(fmt.Sprintf("Starting HTTPS server on :%s", port))
			if err := httpServer.ListenAndServeTLS(*sslCertFile, *sslKeyFile); err != nil && err != http.ErrServerClosed {
				utils.LogError(fmt.Sprintf("[%s] Could not start HTTPS server: %s", time.Now().Format(time.RFC3339), err))
			}
		} else {
			utils.LogInfo(fmt.Sprintf("Starting HTTP server on :%s", *serverPort))
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				utils.LogError(fmt.Sprintf("[%s] Could not start HTTP server: %s", time.Now().Format(time.RFC3339), err))
			}
		}
	}()

	// Handle graceful shutdown
	<-signalChan
	utils.LogInfo("Shutdown signal received, shutting down server...")

	cancel()
	httpServer.Shutdown(ctx)
	db.Close()
	utils.LogInfo("Server gracefully stopped")
}

// setupInitialAdminIfNeeded creates the bootstrap admin account on a fresh
// installation (no admin and no users yet). It is a no-op once any user exists.
func setupInitialAdminIfNeeded(userService *services.UserService) error {
	adminCount, err := userService.GetAdminCount()
	if err != nil {
		return fmt.Errorf("failed to check admin count: %w", err)
	}

	userCount, err := userService.GetTotalUserCount()
	if err != nil {
		return fmt.Errorf("failed to check user count: %w", err)
	}

	if adminCount > 0 || userCount > 0 {
		// Existing installation — nothing to bootstrap.
		return nil
	}

	// Fresh installation — create the bootstrap admin. Bootstrap password comes
	// from OAM_DEFAULT_ADMIN_PASSWORD (see config.DefaultConfigPassword);
	// operators are prompted to change it on first login.
	utils.LogInfo("Fresh installation detected. Creating initial admin account...")
	adminUserID, err := userService.CreateUserWithRole(
		"admin",
		"admin@oam-loxilb.local",
		config.DefaultConfigPassword,
		"admin",
	)
	if err != nil {
		return fmt.Errorf("failed to create initial admin: %w", err)
	}

	utils.LogInfo("Initial admin account created.")
	utils.LogInfo("Username: admin")
	utils.LogInfo("Password: (as configured in OAM_DEFAULT_ADMIN_PASSWORD)")
	utils.LogInfo(fmt.Sprintf("User ID: %d", adminUserID))
	utils.LogInfo("Please change the admin password after first login.")

	return nil
}
