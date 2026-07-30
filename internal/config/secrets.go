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
