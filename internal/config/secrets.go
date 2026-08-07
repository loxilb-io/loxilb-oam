package config

import (
	"os"
	"strconv"
	"strings"
)

// Secrets are sourced entirely from the environment. There are no committed
// fallback values: the server refuses to start when a required secret is unset
// (see main.requireSecrets), so no usable credential ever ships in source.

// AllowedOrigins is the CORS origin allowlist, from the comma-separated
// OAM_ALLOWED_ORIGINS env var (e.g. "https://oam.example.com,http://localhost:3000").
// Empty = wildcard "*" without credentials (dev fallback, startup warning).
var AllowedOrigins = parseAllowedOrigins(os.Getenv("OAM_ALLOWED_ORIGINS"))

func parseAllowedOrigins(v string) []string {
	var out []string
	for _, o := range strings.Split(v, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// CORSUsingWildcard reports whether no origin allowlist is configured.
func CORSUsingWildcard() bool { return len(AllowedOrigins) == 0 }

// EnvOr returns the value of the first non-empty environment variable among
// keys, or def if none is set. It is used to default the -db-* connection flags
// from the canonical DB_* environment family (DB_USER/DB_PASSWORD/DB_HOST/
// DB_PORT/DB_NAME) that every bundled deployment sets, so the server and the
// reset_admin tool read an identical surface. An explicit flag still overrides
// the environment. OAM_DB_PASSWORD is accepted as a legacy alias for
// DB_PASSWORD.
func EnvOr(def string, keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return def
}

func getenvIntDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// TokenExpirationMinutes is the JWT / API-token lifetime in minutes.
// Default 480 (8 hours). Override with OAM_TOKEN_TTL_MINUTES; an explicit
// -token-expiration flag wins (see main.go).
var TokenExpirationMinutes = getenvIntDefault("OAM_TOKEN_TTL_MINUTES", 480)

// DefaultConfigPassword is the password assigned to the bootstrap admin and to
// users created by configuration import. Sourced from OAM_DEFAULT_ADMIN_PASSWORD;
// there is no built-in fallback (see main.requireSecrets).
var DefaultConfigPassword = os.Getenv("OAM_DEFAULT_ADMIN_PASSWORD")

// AdminPasswordMissing reports whether OAM_DEFAULT_ADMIN_PASSWORD is unset.
func AdminPasswordMissing() bool { return os.Getenv("OAM_DEFAULT_ADMIN_PASSWORD") == "" }

// TrustedProxies is the comma-separated OAM_TRUSTED_PROXIES allowlist of proxy
// IPs/CIDRs (e.g. "10.0.0.0/8,172.18.0.0/16") whose X-Forwarded-For header the
// server is willing to believe.
//
// This is a security control, not a convenience knob. gin's default is to trust
// every proxy, which makes ClientIP() equal to whatever X-Forwarded-For the
// caller sent — and ClientIP() keys both the per-IP rate limiter and the
// failed-login lockout. Trusting all proxies therefore lets an attacker defeat
// both by rotating one header. Empty (the default) trusts nothing: ClientIP()
// is the real socket peer, which cannot be spoofed.
//
// Set this only when OAM genuinely sits behind a reverse proxy you control, and
// list that proxy's address — otherwise every request appears to come from the
// proxy and all clients share one rate-limit bucket.
var TrustedProxies = parseTrustedProxies(os.Getenv("OAM_TRUSTED_PROXIES"))

func parseTrustedProxies(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
