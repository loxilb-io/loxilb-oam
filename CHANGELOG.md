# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Versioning

loxilb-oam versions **in lockstep with [loxilb-io/loxilb](https://github.com/loxilb-io/loxilb)**
and uses the same `vMAJOR.MINOR.PATCH[.BUILD]` scheme (e.g. `v0.9.8.7`). A given
loxilb-oam release targets the loxilb release of the same version: run
`loxilb-oam v0.9.8.7` against `loxilb v0.9.8.7`. Version numbers therefore track
the loxilb release train rather than carrying independent semantic-versioning
meaning about this repository's own API compatibility.

The version is stamped into the binary at build time (`loxilb-oam -version`, the
startup log, and the served OpenAPI spec all report it) and onto the container
image's `org.opencontainers.image.version` label.

## [Unreleased]

### Added
- Converged single-node deployment now uses one independent `loxilb-state`
  PostgreSQL service and one `loxioam` database for OAM plus the Gateway's
  isolated `aigw` and dormant `aigw_mgmt` schemas. The bundle includes the
  idempotent Gateway bootstrap, file-backed Gateway database credentials,
  source-drift validation, existing-volume adoption, and database → data →
  management startup/verification automation.
- The approved converged integration image defaults to
  `ghcr.io/loxilb-io/loxilb-inference-gateway:latest-u24`; validation records
  the resolved digest before promotion to an immutable release identity.
- A dedicated `docker-compose.converged-local-ui.yml` developer overlay keeps
  converged PostgreSQL, Gateway, and OAM on a remote testbed while disabling
  the bundled UI and Caddy. It exposes OAM directly over HTTP with an explicit
  local-development CORS allowlist and reserved-port protection.

### Changed — BREAKING
- **The datastore is now PostgreSQL 18; MySQL is no longer supported.** There is
  no in-place upgrade: a MySQL deployment cannot be pointed at this release.
  Stand the stack up against an empty PostgreSQL database, then move any data
  across separately (the `MEDIUMBLOB` → `BYTEA` snapshot column and the former
  `ENUM` columns need explicit type handling if you do).
  - `MYSQL_ROOT_PASSWORD` is **removed**. PostgreSQL's `POSTGRES_USER` owns the
    database, so `DB_PASSWORD` is now the only database secret. Remove the key
    from your `.env`.
  - `DB_PORT` defaults to `5432` (was `3306`), and `DB_HOST` defaults to the
    `postgres` service (was `mysql`) in the bundled Compose stacks.
  - The bundled database service, its volume, and the Kubernetes manifests are
    renamed `mysql*` → `postgres*` (`postgres-secret` now holds `postgres-user`
    / `postgres-password` / `postgres-database`).
  - `database/config/my.cnf` is gone. The settings that mattered are passed as
    server flags instead, because pointing PostgreSQL's `config_file` outside
    `PGDATA` also moves where it looks for `pg_hba.conf`. MySQL's
    `general_log = 1` is deliberately **not** carried over: the equivalent
    (`log_statement = all`) would write credentials and tokens to the log.
  - `-ssl-option=true` now means `sslmode=verify-full`, so the server
    certificate must be valid for the hostname in `DB_HOST`. The default client
    key/cert paths moved from `certs/mysql.*` to `certs/postgres.*`.
  - rsyslog log shipping uses `ompgsql` (package `rsyslog-pgsql`) instead of
    `ommysql`.
  - The MySQL-era migrations are retained unconverted under
    `database/migrations/legacy-mysql/` for historical reference only.
    `database/init/00-init-complete.sql` is the entire PostgreSQL schema.

### Fixed
- The log-retrieval query could never succeed: it selected ten columns —
  `message` twice, plus a `create_at` column that does not exist — into a
  nine-column scan. It now selects the nine columns the reader actually expects.
- The Kubernetes database liveness/readiness probes authenticated with the
  literal string `CHANGE_ME`, so they failed against any real secret. They now
  read the credentials from the pod environment.
- The four Kubernetes database-init ConfigMaps had each drifted from the real
  schema — the production copy was missing the `system_config` table the service
  requires at startup. All four are now generated from the single canonical
  schema file.
- `isDuplicateEntryError` matched the driver error with a bare type assertion,
  so a wrapped error was not recognised as a duplicate. It now unwraps, and
  shares one implementation with `IsDuplicateKeyError`.
- Database credentials are URL-escaped when building the connection string. A
  password containing `@`, `/`, `:` or `?` previously produced an unparseable
  DSN that surfaced as a confusing authentication failure.

### Security
- The break-glass admin reset now actually revokes the account's tokens. It
  deleted from a `user_tokens` table that does not exist, so a stolen bearer
  token kept working for its full TTL while the CLI reported that all sessions
  had been invalidated. Tokens are deleted from `api_tokens`, and a failure now
  aborts the reset rather than being logged as a warning.
- Token validity is no longer cached in the server process. A positive
  five-minute cache meant a logged-out token kept authenticating until the entry
  expired, and — because the break-glass reset runs as a separate binary — a
  reset could never evict it, so a revoked token stayed valid indefinitely. The
  `api_tokens` store is now read on every request, making logout and reset take
  effect immediately. Removes the `github.com/patrickmn/go-cache` dependency and
  the `CacheExpirationTime` / `CacheCleanupInterval` constants.
- The bootstrap admin password and logged-out bearer tokens are no longer
  written to the server log, which is served over the API.
- `GET /oam/logs` and the log-archive endpoints now require the `admin` role
  (new `log_read` capability). They were reachable by any authenticated user,
  including `viewer`.
- Added `OAM_TRUSTED_PROXIES`. The server previously trusted every proxy
  (gin's default), so `X-Forwarded-For` — which keys the per-IP rate limiter and
  the failed-login lockout — could be forged to evade both. Unset, the header is
  now ignored in favour of the peer address; the Compose bundle defaults it to
  Docker's bridge pool so the Caddy edge's client IP is still honoured.
- **Upgrade note for operators:** the credential-leak fixes stop *new* secrets
  from reaching the server log, but they do not scrub what earlier builds
  already wrote. An existing `/var/log/loxioam.log` may still contain the
  bootstrap admin password and bearer tokens. After upgrading, truncate (or
  delete) the old log file and rotate the admin password.

### Fixed
- A transient database outage no longer disables the server permanently. The
  health check closed the connection pool and replaced it, but every service
  held the original pool, leaving all handlers on a closed one
  (`sql: database is closed`) until restart. The pool is now left to
  `database/sql`, which redials on its own.
- `docs/container-image.md` documented a `make docker-build docker-push
  VERSION=…` command that the Makefile's release-tag guard rejects, so the
  build-your-own-image instructions failed as written.

## [v0.9.8.7]

### Added
- Initial public release of loxilb-oam: OAM management API for LoxiLB instances,
  versioned in lockstep with loxilb v0.9.8.7.
- Authentication, RBAC (admin/operator/viewer), and server-side token revocation.
- Instance snapshot orchestration with at-rest AES-256-GCM encryption.
- Docker Compose deployments: a single-node management-plane bundle
  (`deploy/compose/`, UI + API + MySQL behind a Caddy TLS edge) and an API-only
  HTTP stack at the repository root.
- CI (build, vet, golangci-lint, unit tests, govulncheck) and secret scanning.

### Removed
- OAuth login (Google/GitHub/Facebook). The implementation was unfinished and
  had no coverage against the real handlers, so it was withdrawn rather than
  published. This drops the `OAM_OAUTH_ENABLED` and `OAM_OAUTH_*_CLIENT_{ID,
  SECRET}` variables, the `-google/-github/-facebook-redirect-url` flags, the
  `/oam/oauth/*` endpoints, the `golang.org/x/oauth2` dependency, and the
  `users.oauth_provider` / `oauth_id` / `oauth_token` columns (the last of
  which stored provider access tokens in plaintext). Authentication is
  username/password against the local user store. Databases created before
  this release should apply
  `database/migrations/005_drop_oauth_columns.sql`; the withdrawn code is
  archived on the `feature/oauth2` branch.

### Known limitations
- The Kubernetes manifests under `k8s/` are **pre-release and unsupported**:
  they do not supply the mandatory `OAM_JWT_SECRET` /
  `OAM_DEFAULT_ADMIN_PASSWORD` and will not start. Use Docker Compose. See
  [k8s/README.md](k8s/README.md).

[Unreleased]: https://github.com/loxilb-io/loxilb-oam/compare/v0.9.8.7...HEAD
[v0.9.8.7]: https://github.com/loxilb-io/loxilb-oam/releases/tag/v0.9.8.7
