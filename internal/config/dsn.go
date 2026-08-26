package config

import (
	"fmt"
	"net"
	"net/url"
)

// SSL modes used when building a PostgreSQL DSN.
const (
	// SSLModeDisable connects in plaintext — the default for the bundled
	// deployments, where the database is reachable only on the internal
	// compose/cluster network.
	SSLModeDisable = "disable"
	// SSLModeVerifyFull requires TLS and verifies both the certificate chain
	// and the server hostname. Used with the -ssl-option flag, which also
	// supplies a private CA and client keypair.
	SSLModeVerifyFull = "verify-full"
)

// PostgresDSN builds a PostgreSQL connection URL from discrete connection
// settings.
//
// It exists so the server and the reset_admin tool cannot drift apart, and so
// credentials are escaped rather than concatenated: a password containing '@',
// '/', ':' or '?' silently produced an unparseable DSN under the old
// fmt.Sprintf construction, which surfaced as a confusing authentication
// failure rather than a configuration error.
func PostgresDSN(user, password, host, port, dbname, sslMode string) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + dbname,
	}
	q := url.Values{}
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// SSLModeFor maps the -ssl-option flag onto a PostgreSQL sslmode.
func SSLModeFor(sslEnabled bool) string {
	if sslEnabled {
		return SSLModeVerifyFull
	}
	return SSLModeDisable
}

// RedactDSN returns dsn with its password replaced, for logging. It never
// returns the original string on a parse failure — an unparseable DSN is
// exactly the case where the password is most likely to be malformed and
// leak into a log line.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "postgres://<unparseable DSN redacted>"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return fmt.Sprintf("%s", u)
}
