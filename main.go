package main

// @title LoxiLB OAM API
// @description Operations, Administration and Management API for LoxiLB instances —
// @description authentication, RBAC, user and instance management, configuration
// @description snapshots, and a management proxy.
// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html
// (No @BasePath: the generated path keys already carry the /oam prefix.)
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT bearer token from POST /oam/login, sent as "Bearer <token>".

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	swaggerdocs "github.com/loxilb-io/loxilb-oam/docs"
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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

// version is the release identifier, stamped at link time by the build:
//
//	go build -ldflags "-X main.version=v0.9.8.7"
//
// It follows loxilb-io/loxilb's vMAJOR.MINOR.PATCH.BUILD scheme, in lockstep
// with the loxilb release this build targets. `make build` stamps the Makefile's
// VERSION, the Dockerfile stamps its VERSION build-arg, and the release workflow
// stamps the git tag. A plain `go build` leaves it as "dev", which is the honest
// answer for an untagged local build.
var version = "dev"

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
	// Define command-line flags for the database connection details. Each flag
	// defaults from the canonical DB_* environment family (an explicit flag
	// still wins); OAM_DB_PASSWORD is accepted as a legacy alias for DB_PASSWORD.
	dbUser := flag.String("db-user", config.EnvOr("oamuser", "DB_USER"), "Database username (default: DB_USER env)")
	dbPassword := flag.String("db-password", config.EnvOr("", "DB_PASSWORD", "OAM_DB_PASSWORD"), "Database password (default: DB_PASSWORD env)")
	dbHost := flag.String("db-host", config.EnvOr("127.0.0.1", "DB_HOST"), "Database host (default: DB_HOST env)")
	dbPort := flag.String("db-port", config.EnvOr("3306", "DB_PORT"), "Database port (default: DB_PORT env)")
	dbName := flag.String("db-name", config.EnvOr("loxioam", "DB_NAME"), "Database name (default: DB_NAME env)")
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

	showVersion := flag.Bool("version", false, "Print the version and exit")

	flag.Parse()

	// Answer -version before anything else: it must work without a database,
	// secrets, or any other configuration.
	if *showVersion {
		fmt.Println("loxilb-oam " + version)
		return
	}

	// Serve the running version in the OpenAPI spec rather than baking it into
	// the generated docs — that keeps `swag init` output stable across releases
	// (the Swagger drift CI gate diffs it) while the UI still shows the truth.
	swaggerdocs.SwaggerInfo.Version = version

	utils.LogInfo("Starting loxilb-oam " + version)

	// Abort startup if any mandatory secret is unset.
	requireSecrets()

	// The database password has no built-in default: require it via the
	// -db-password flag or the DB_PASSWORD env var (OAM_DB_PASSWORD alias).
	dbPass := *dbPassword
	if dbPass == "" {
		utils.LogError("SECURITY: no database password provided. Set DB_PASSWORD or pass -db-password.")
		os.Exit(1)
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

	// Periodically report database reachability.
	//
	// This deliberately only observes. *sql.DB is a pool, not a connection: it
	// reconnects on its own, and a failed Ping means "no connection was free
	// and healthy just now", not "this pool is finished". The previous version
	// closed the pool and swapped in a new one, but every service above
	// captured the original *sql.DB at construction and was never updated — so
	// one transient blip permanently left every handler querying a closed pool
	// ("sql: database is closed") until the process restarted. Observing and
	// letting database/sql recover is both simpler and correct.
	go func() {
		wasDown := false
		for {
			select {
			case <-ctx.Done():
				utils.LogInfo("Stopping database health check...")
				return
			default:
				time.Sleep(config.DbRetryDelay)
				if err := db.Ping(); err != nil {
					if !wasDown {
						utils.LogError(fmt.Sprintf("Database unreachable: %s (the pool will keep retrying)", err))
						wasDown = true
					}
				} else if wasDown {
					utils.LogInfo("Database connection re-established")
					wasDown = false
				}
			}
		}
	}()

	router := gin.Default()

	// Decide which proxies may set X-Forwarded-For. ClientIP() keys the rate
	// limiter and the login lockout, so gin's trust-everything default would
	// let a caller forge a new identity per request and evade both.
	if err := router.SetTrustedProxies(config.TrustedProxies); err != nil {
		utils.LogError(fmt.Sprintf("Invalid OAM_TRUSTED_PROXIES value: %s", err))
		os.Exit(1)
	}
	if len(config.TrustedProxies) == 0 {
		utils.LogInfo("OAM_TRUSTED_PROXIES is not set — X-Forwarded-For is ignored and the peer address is used as the client IP. If OAM runs behind a reverse proxy, set it to that proxy's address so rate limiting and login lockout see real client IPs.")
	}

	routes.SetupRoutes(router, db, handler, userService, alertService)

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
