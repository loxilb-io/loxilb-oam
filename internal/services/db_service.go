package services

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
)

// ConnectWithSecureTLS establishes a secure TLS connection to a MySQL database using the given
// DSN and the CA, client certificate, and client key at the provided file paths. It retries up to
// maxRetries times, doubling backoff after each failed attempt, and returns the open connection.
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

	tlsConfig := &tls.Config{
		RootCAs:      rootCertPool,
		Certificates: clientCert,
	}

	err = mysql.RegisterTLSConfig("custom", tlsConfig)
	if err != nil {
		log.Fatalf("Failed to register TLS config: %v", err)
	}

	secureDSN := fmt.Sprintf("%s&tls=custom", dsn)

	var db *sql.DB
	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("mysql", secureDSN)
		if err == nil && db.Ping() == nil {
			return db, nil
		}
		log.Printf("Retrying database connection (%d/%d)...", i+1, maxRetries)
		time.Sleep(backoff)
		backoff *= 2
	}
	return nil, fmt.Errorf("could not connect securely after %d retries: %w", maxRetries, err)
}

// ConnectWithRetry establishes a connection to a MySQL database using the given DSN. It retries up
// to maxRetries times, doubling backoff after each failed attempt, and returns the open connection.
func ConnectWithRetry(dsn string, maxRetries int, backoff time.Duration) (*sql.DB, error) {
	var db *sql.DB
	var err error
	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil && db.Ping() == nil {
			return db, nil
		}
		log.Printf("Database connection failed, retrying in %v... (%d/%d)", backoff, i+1, maxRetries)
		time.Sleep(backoff)
		backoff *= 2
	}
	return nil, fmt.Errorf("could not connect to database after %d retries: %w", maxRetries, err)
}
