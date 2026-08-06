# Configuration Constants (`internal/config/constants.go`)

`constants.go` holds compile-time constants used across the application:
retry/backoff behavior, logging, alerting, polling, SSL file paths, LoxiLB
instance defaults, login-lockout and rate-limiting parameters.

Runtime secrets and tunables (JWT/token lifetime, admin password, database
password, CORS allowlist) are **not** here — they are environment-driven and
live in `internal/config/secrets.go`. See [DEPLOYMENT.md](../DEPLOYMENT.md) for
the full environment-variable reference.

## General Configuration

| Constant Name       | Description                      | Value |
|---------------------|----------------------------------|-------|
| `MinPasswordLength` | Minimum required password length | `9`   |

## Logging Configuration

| Constant Name           | Description                        | Value |
|-------------------------|------------------------------------|-------|
| `DefaultLogLimit`       | Default logs returned per page     | `10` |
| `DefaultLogOffset`      | Default offset for log pagination  | `0` |
| `LogFilePath`           | Path to the main log file          | `"/var/log/loxioam.log"` |
| `DefaultLogLines`       | Default number of log lines        | `100` |
| `DefaultLogLevel`       | Default logging level              | `"ERROR"` |
| `DefaultLogFilePath`    | Default directory for log files    | `"/var/log/"` |
| `DefaultOAMLogFile`     | Default OAM log file name          | `"loxioam"` |
| `DefaultLogArchivePath` | Directory for archived log files   | `"/var/log/"` |

## Retry & Backoff Configuration

| Constant Name     | Description                                   | Value |
|-------------------|-----------------------------------------------|-------|
| `MaxRetries`      | Maximum retry attempts                        | `1` |
| `RetryDelay`      | Delay between retries                          | `2s` |
| `DbRetryDelay`    | Delay between database connection retries      | `5s` |
| `DbMaxRetries`    | Maximum database connection retries            | `1` |
| `DbRetryBackoff`  | Backoff time for database retries              | `2s` |

## Token & Cache Configuration

| Constant Name          | Description                          | Value |
|------------------------|--------------------------------------|-------|
| `CacheExpirationTime`  | Cache expiration time (minutes)      | `5` |
| `CacheCleanupInterval` | Cache cleanup interval (minutes)     | `10` |

> The JWT / API-token lifetime is **not** a constant. It is
> `config.TokenExpirationMinutes` in `secrets.go`, defaulting to `480` (8h) and
> overridable with the `OAM_TOKEN_TTL_MINUTES` env var or the `-token-expiration`
> flag.

## Alerting Configuration

| Constant Name             | Description                     | Value |
|---------------------------|---------------------------------|-------|
| `AlertTypeDBDisconnect`   | Alert for database disconnection | `"DB_DISCONNECT"` |
| `AlertTypeAPIUnreachable` | Alert when API is unreachable    | `"API_UNREACHABLE"` |
| `AlertTypeHighCPU`        | Alert for high CPU usage         | `"HIGH_CPU"` |
| `AlertTypeMemoryLeak`     | Alert for memory-leak detection  | `"MEMORY_LEAK"` |
| `SeverityInfo`            | Severity level: Informational    | `"INFO"` |
| `SeverityWarning`         | Severity level: Warning          | `"WARNING"` |
| `SeverityCritical`        | Severity level: Critical         | `"CRITICAL"` |

### Alert Pagination

| Constant Name          | Description                       | Value |
|------------------------|-----------------------------------|-------|
| `DefaultAlertPageSize` | Default number of alerts per page | `20` |
| `MaxAlertPageSize`     | Maximum number of alerts per page | `100` |
| `DefaultAlertPage`     | Default page number               | `1` |

## Polling Configuration

| Constant Name     | Description                            | Value |
|-------------------|----------------------------------------|-------|
| `PollingInterval` | Interval for the polling service       | `10s` |
| `PollingRefresh`  | Interval for polling refresh           | `60s` |

## SSL Configuration

| Constant Name          | Description                      | Value |
|------------------------|----------------------------------|-------|
| `CaCertFilePath`       | Path to CA certificate file      | `"certs/ca.crt"` |
| `CaClientCertFilePath` | Path to client certificate file  | `"certs/mysql.crt"` |
| `CaClientKeyFilePath`  | Path to client key file          | `"certs/mysql.key"` |

## LoxiLB Instance Configuration

| Constant Name         | Description                          | Value |
|-----------------------|--------------------------------------|-------|
| `LoxilbContainerName` | Name of the LoxiLB Docker container  | `"loxilb"` |
| `LoxilbImage`         | LoxiLB Docker image repository       | `"ghcr.io/loxilb-io/loxilb"` |
| `LoxilbTag`           | Default tag/version for LoxiLB image | `"latest"` |
| `DockerPort`          | Docker daemon port                   | `2375` |
| `DockerHost`          | Docker host protocol                 | `"http"` |

## Login Lockout & Rate Limiting

Failed-login lockout uses exponential backoff, keyed by username and client IP.

| Constant Name            | Description                                        | Value |
|--------------------------|----------------------------------------------------|-------|
| `MaxFailedLoginAttempts` | Failed attempts before lockout                     | `5` |
| `LoginLockoutBase`       | First lockout duration (doubles on each failure)   | `1m` |
| `LoginLockoutMax`        | Cap for the exponential lockout                    | `15m` |
| `LoginAttemptWindow`     | Window after which the failed-attempt count resets | `10m` |

Request rate limiting is keyed by client IP.

| Constant Name         | Description                          | Value |
|-----------------------|--------------------------------------|-------|
| `LoginRateLimitRPS`   | Sustained login requests per second  | `0.5` |
| `LoginRateLimitBurst` | Login request burst allowance        | `10` |
| `ProxyRateLimitRPS`   | Gateway proxy requests per second    | `50.0` |
| `ProxyRateLimitBurst` | Gateway proxy burst allowance        | `100` |
