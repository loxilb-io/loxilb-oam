package services

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	// registers the "pgx" driver with database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ConnectWithSecureTLS establishes a secure TLS connection to a PostgreSQL database using the
// given DSN and the CA, client certificate, and client key at the provided file paths. It retries
// up to maxRetries times, doubling backoff after each failed attempt, and returns the open
// connection.
//
// The TLS settings are applied to the parsed connection config directly rather than through DSN
// parameters, because pgx only reads certificate paths from a DSN in some sslmode combinations.
// Any plaintext fallbacks pgx may have derived from the DSN's sslmode are dropped: a connection
// asked for over TLS must never silently downgrade.
func ConnectWithSecureTLS(dsn string, maxRetries int, backoff time.Duration, caCertFilePath, caClientCertFilePath, caClientKeyFilePath string) (*sql.DB, error) {
	rootCertPool := x509.NewCertPool()
	pem, err := os.ReadFile(caCertFilePath)
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}
	rootCertPool.AppendCertsFromPEM(pem)

	clientCert := make([]tls.Certificate, 1)
	clientCert[0], err = tls.LoadX509KeyPair(caClientCertFilePath, caClientKeyFilePath)
	if err != nil {
		log.Fatalf("Failed to load client certificate: %v", err)
	}

	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid database DSN: %w", err)
	}

	connConfig.TLSConfig = &tls.Config{
		RootCAs:      rootCertPool,
		Certificates: clientCert,
		// pgx derives ServerName itself only when it builds the TLS config from
		// sslmode; supplying our own means we have to set it, or verification
		// would fail against every certificate.
		ServerName: connConfig.Host,
		MinVersion: tls.VersionTLS12,
	}
	// sslmode=prefer/allow leave a non-TLS fallback in place. Drop them.
	connConfig.Fallbacks = nil

	// Registered once: each call returns a new DSN string keyed to the config,
	// so registering inside the retry loop would leak an entry per attempt.
	secureDSN := stdlib.RegisterConnConfig(connConfig)

	var db *sql.DB
	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("pgx", secureDSN)
		if err == nil && db.Ping() == nil {
			return db, nil
		}
		log.Printf("Retrying database connection (%d/%d)...", i+1, maxRetries)
		time.Sleep(backoff)
		backoff *= 2
	}
	return nil, fmt.Errorf("could not connect securely after %d retries: %w", maxRetries, err)
}

// ConnectWithRetry establishes a connection to a PostgreSQL database using the given DSN. It
// retries up to maxRetries times, doubling backoff after each failed attempt, and returns the open
// connection.
func ConnectWithRetry(dsn string, maxRetries int, backoff time.Duration) (*sql.DB, error) {
	var db *sql.DB
	var err error
	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("pgx", dsn)
		if err == nil && db.Ping() == nil {
			return db, nil
		}
		log.Printf("Database connection failed, retrying in %v... (%d/%d)", backoff, i+1, maxRetries)
		time.Sleep(backoff)
		backoff *= 2
	}
	return nil, fmt.Errorf("could not connect to database after %d retries: %w", maxRetries, err)
}
