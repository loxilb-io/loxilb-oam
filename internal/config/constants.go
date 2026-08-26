package config

import "time"

const (
	MinPasswordLength = 9

	DefaultLogLimit     = 10 // Default limit for log pagination
	DefaultLogOffset    = 0  // Default offset for log pagination
	LogFilePath         = "/var/log/loxioam.log"
	MaxRetries          = 1
	RetryDelay          = 2 * time.Second
	DbRetryDelay        = 5 * time.Second
	DbMaxRetries        = 1
	DbRetryBackoff      = 2 * time.Second
	LoxilbContainerName = "loxilb"
	LoxilbImage         = "ghcr.io/loxilb-io/loxilb"
	LoxilbTag           = "latest"

	// Docker Engine API connection settings live in docker.go (env-driven).

	// TOKEN constants
	// (TokenExpirationMinutes moved to secrets.go — now env-driven)
	// Token validity is no longer cached: the store is authoritative on every
	// request so revocation takes effect immediately. See UserService.ValidateToken.

	// AlertType and Severity constants
	AlertTypeDBDisconnect   = "DB_DISCONNECT"
	AlertTypeAPIUnreachable = "API_UNREACHABLE"
	AlertTypeHighCPU        = "HIGH_CPU"
	AlertTypeMemoryLeak     = "MEMORY_LEAK"

	SeverityInfo     = "INFO"
	SeverityWarning  = "WARNING"
	SeverityCritical = "CRITICAL"

	// Polling Service
	PollingInterval = 10 * time.Second
	PollingRefresh  = 60 * time.Second

	// SSL Service
	CaCertFilePath       = "certs/ca.crt"
	CaClientCertFilePath = "certs/postgres.crt"
	CaClientKeyFilePath  = "certs/postgres.key"

	// Log Service
	DefaultLogLines       = 100
	DefaultLogLevel       = "ERROR"
	DefaultLogFilePath    = "/var/log/"
	DefaultOAMLogFile     = "loxioam"
	DefaultLogArchivePath = "/var/log/"

	// Pagination constants for alerts
	DefaultAlertPageSize = 20  // Default number of alerts per page
	MaxAlertPageSize     = 100 // Maximum number of alerts per page
	DefaultAlertPage     = 1   // Default page number

	// Login Lockout constants (exponential backoff)
	MaxFailedLoginAttempts = 5                // Maximum failed login attempts before lockout
	LoginLockoutBase       = 1 * time.Minute  // First lockout duration; doubles on each further failure
	LoginLockoutMax        = 15 * time.Minute // Cap for the exponential lockout
	LoginAttemptWindow     = 10 * time.Minute // Time window for failed attempt count reset

	// Rate limiting, keyed by client IP
	LoginRateLimitRPS   = 0.5 // sustained logins/sec (30 per minute); covers login + setup
	LoginRateLimitBurst = 10
	ProxyRateLimitRPS   = 50.0 // gateway proxy requests/sec
	ProxyRateLimitBurst = 100
)
