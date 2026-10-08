package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
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
		return nil, fmt.Errorf("read database CA certificate: %w", err)
	}
	if !rootCertPool.AppendCertsFromPEM(pem) {
		return nil, errors.New("database CA file contains no certificates")
	}

	clientCert := make([]tls.Certificate, 1)
	clientCert[0], err = tls.LoadX509KeyPair(caClientCertFilePath, caClientKeyFilePath)
	if err != nil {
		return nil, fmt.Errorf("load database client certificate: %w", err)
	}

	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database DSN")
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

	return connectPostgres(connConfig, maxRetries, backoff)
}

// ConnectWithRetry establishes a connection to a PostgreSQL database using the given DSN. It
// retries up to maxRetries times, doubling backoff after each failed attempt, and returns the open
// connection.
func ConnectWithRetry(dsn string, maxRetries int, backoff time.Duration) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database DSN")
	}
	return connectPostgres(cfg, maxRetries, backoff)
}

// A failed Ping is the connection error; sql.OpenDB itself does no network I/O.
// Close failed pools and keep no global registered DSN containing credentials.
func connectPostgres(cfg *pgx.ConnConfig, maxRetries int, backoff time.Duration) (*sql.DB, error) {
	if maxRetries <= 0 || backoff < 0 {
		return nil, errors.New("invalid database retry policy")
	}
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		db := sql.OpenDB(stdlib.GetConnector(*cfg))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = db.PingContext(ctx)
		cancel()
		if lastErr == nil {
			return db, nil
		}
		db.Close()
		if i+1 < maxRetries {
			time.Sleep(backoff)
			// Saturate to avoid overflow on unusually large retry inputs.
			if backoff <= time.Duration(1<<62)-1 {
				backoff *= 2
			}
		}
	}
	return nil, fmt.Errorf("database connection failed after %d attempt(s): %w", maxRetries, lastErr)
}
