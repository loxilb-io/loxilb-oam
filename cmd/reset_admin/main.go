package main

import (
	"database/sql"
	"flag"
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// Define command-line flags for the database connection details
	dbUser := flag.String("db-user", "oamuser", "Database username")
	dbPassword := flag.String("db-password", "", "Database password (or set OAM_DB_PASSWORD)")
	dbHost := flag.String("db-host", "127.0.0.1", "Database host")
	dbPort := flag.String("db-port", "3306", "Database port")
	dbName := flag.String("db-name", "loxioam", "Database name")
	sslOption := flag.String("ssl-option", "false", "Enable SSL connection")
	sslCaCertFilePath := flag.String("ssl-ca-cert-file", "./ssl/certs/root-ca.pem", "SSL CA certificate file path")
	sslCaClientCertFilePath := flag.String("ssl-ca-client-cert-file", "./ssl/certs/client-cert.pem", "SSL client certificate file path")
	sslCaClientKeyFilePath := flag.String("ssl-ca-client-key-file", "./ssl/certs/client-key.pem", "SSL client key file path")
	confirmFlag := flag.Bool("confirm", false, "Confirm the admin reset operation (required)")

	flag.Parse()

	// Safety check - require explicit confirmation
	if !*confirmFlag {
		fmt.Println("ADMIN RESET TOOL")
		fmt.Println("=====================================")
		fmt.Println("This tool resets the admin account to its default credentials:")
		fmt.Println("  Username: admin")
		fmt.Println("  Password: the configured default admin password (OAM_DEFAULT_ADMIN_PASSWORD)")
		fmt.Println("  Email: admin@oam-loxilb.local")
		fmt.Println("")
		fmt.Println("WARNING: This will:")
		fmt.Println("  - Reset admin username, password, and email to defaults")
		fmt.Println("  - Invalidate all existing admin sessions")
		fmt.Println("  - Mark credentials as not updated (will require change after login)")
		fmt.Println("")
		fmt.Println("To proceed, run this command with the --confirm flag:")
		fmt.Printf("  %s --confirm\n", os.Args[0])
		fmt.Println("")
		os.Exit(1)
	}

	// The database password has no built-in default: require it via flag or env.
	dbPass := *dbPassword
	if dbPass == "" {
		dbPass = os.Getenv("OAM_DB_PASSWORD")
	}
	if dbPass == "" {
		fmt.Println("SECURITY: no database password provided. Set OAM_DB_PASSWORD or pass -db-password.")
		os.Exit(1)
	}

	// Create the DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", *dbUser, dbPass, *dbHost, *dbPort, *dbName)

	// Connect to the database
	var db *sql.DB
	var err error

	fmt.Println("Connecting to database...")
	if *sslOption == "true" {
		db, err = services.ConnectWithSecureTLS(dsn, config.DbMaxRetries, config.DbRetryBackoff, *sslCaCertFilePath, *sslCaClientCertFilePath, *sslCaClientKeyFilePath)
	} else {
		db, err = services.ConnectWithRetry(dsn, config.DbMaxRetries, config.DbRetryBackoff)
	}
	if err != nil {
		fmt.Printf("Database connection failed: %s\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("Database connected")

	// Create user service
	userService := services.NewUserService(db)

	// Perform the admin reset
	fmt.Println("")
	fmt.Println("Resetting admin account to default credentials...")
	fmt.Println("")

	adminUserID, err := userService.ResetAdminToDefault()
	if err != nil {
		fmt.Printf("Failed to reset admin account: %s\n", err)
		os.Exit(1)
	}

	// Success message
	fmt.Println("")
	fmt.Println("=====================================")
	fmt.Println("ADMIN RESET SUCCESSFUL")
	fmt.Println("=====================================")
	fmt.Println("")
	fmt.Printf("Admin User ID: %d\n", adminUserID)
	fmt.Println("Username: admin")
	fmt.Println("Password: the configured default admin password (OAM_DEFAULT_ADMIN_PASSWORD)")
	fmt.Println("Email: admin@oam-loxilb.local")
	fmt.Println("")
	fmt.Println("IMPORTANT SECURITY REMINDER:")
	fmt.Println("  1. Log in with the default credentials above")
	fmt.Println("  2. Change the password to a secure one immediately")
	fmt.Println("  3. Consider changing the username and email as well")
	fmt.Println("")
	fmt.Println("All previous admin sessions have been invalidated.")
	fmt.Println("")

	utils.LogInfo(fmt.Sprintf("Admin account reset completed (User ID: %d)", adminUserID))
}
