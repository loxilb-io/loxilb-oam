# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Versioning

loxilb-oam versions **in lockstep with
[loxilb-inference-gateway](https://github.com/loxilb-io/loxilb-inference-gateway)
and [loxilb-ui](https://github.com/loxilb-io/loxilb-ui)** and uses the same
`vMAJOR.MINOR.PATCH[.BUILD]` scheme (e.g. `v0.9.9.1`). A given loxilb-oam release
targets the gateway and console releases of the same version: run
`loxilb-oam v0.9.9.1` with `loxilb-inference-gateway v0.9.9.1` and
`loxilb-ui v0.9.9.1`. Version numbers therefore track that release train rather
than carrying independent semantic-versioning meaning about this repository's own
API compatibility. <!-- OWNER REVIEW PENDING -->

The version is stamped into the binary at build time (`loxilb-oam -version`, the
startup log, and the served OpenAPI spec all report it) and onto the container
image's `org.opencontainers.image.version` label.

## [Unreleased]

## [v0.9.9.1] - 2026-11-02

### Added
- Failures OAM itself produces on the instance proxy and the snapshot routes
  (refusals, the rate limit, an instance that could not be reached) carry
  `X-Loxi-Error-Origin: oam`. A client no longer has to read a missing marker
  as "OAM".
- A snapshot restore can be limited to chosen domains.
  `POST /oam/snapshots/{sid}/restore` takes `components`, a list of snapshot
  domain names (for example `["auditsink"]`); the gateway replaces those
  domains and leaves the rest alone. Without it the whole document is restored,
  as before. An empty list, a malformed or repeated name, or a domain the
  document does not cover is refused with `400` before the gateway is called.
  The selection is returned in the restore outcome and kept with the snapshot
  as `last_restore_components` (schema version 4, applied at startup).
- Appliance operations can be authorized, submitted, followed and cancelled
  (**alpha**, `appliance-ops/v1alpha1`; the only host adapter is still the
  fixture, which executes nothing). `POST …/operations/{id}/authorize`
  verifies the caller's password — under the same lockout and rate limit as
  login — and returns a one-use challenge bound to the operation, its plan,
  the user and the session. `POST …/operations/{id}/submit` consumes it and
  hands the operation to the host adapter; a backup needs no challenge.
  `POST …/operations/{id}/cancel` and `…/reconcile` complete the set. Only one
  operation can be active per installation. OAM follows the adapter's journal
  every 2 seconds, delivers a submission the adapter missed (including across
  an OAM restart) exactly once, and marks an operation the journal lost as
  `RECOVERY_REQUIRED` instead of running it again. Every step is recorded in
  `appliance_audit`. Adds schema migration `0003` (`appliance_challenges`,
  `appliance_audit`, one-active-operation index). See
  [docs/appliance-operations.md](docs/appliance-operations.md).
- Appliance operations can be planned and read (**alpha**,
  `appliance-ops/v1alpha1`). `POST /oam/v1/appliance/operations` validates a
  backup, restore, update, rollback or reset request with the host adapter and
  records the plan — what it would touch, the artifacts involved, and the
  point past which it could not be cancelled — without executing anything.
  The call is idempotent on a required `Idempotency-Key` header. Plans expire
  after 15 minutes. `GET /oam/v1/appliance/operations` and
  `…/operations/{id}` return them to every role, with the plan withheld from
  roles that could not run that operation type. Adds schema migration `0002`
  (`appliance_operations`), the first migration after the baseline.
- Appliance status and capability discovery (**alpha**, contract
  `appliance-ops/v1alpha1`, subject to change). `GET /oam/v1/appliance/capabilities`
  reports, for each whole-Appliance action (backup, restore, update, rollback,
  reset, diagnostics), three independent facts: whether the host adapter
  supports it, whether it can run now and if not why, and whether the caller's
  role may request it. `GET /oam/v1/appliance/status` reports product identity,
  per-component liveness and readiness, and the database schema version; a
  component that cannot be observed is `unknown`, never ready. No action can
  be executed yet. A deployment without a host adapter — every deployment
  today — answers with `HOST_NOT_CONFIGURED`. See
  [docs/appliance-operations.md](docs/appliance-operations.md).
- Seven capabilities for the Appliance lifecycle: `appliance_read` (every
  role) and `appliance_backup`, `appliance_restore`, `appliance_update`,
  `appliance_rollback`, `appliance_reset`, `appliance_diagnostics` (admin
  only). None is implied by `config_write` or any existing capability.
- `OAM_APPLIANCE_HOST_SOCKET` and `OAM_APPLIANCE_HOST_KEY_FILE` connect OAM to
  an Appliance host adapter over a Unix socket with HMAC-signed requests.
  `cmd/appliance-host-fixture` is a stand-in adapter for development; it
  executes nothing and labels everything it returns as a fixture.
- Versioned database migrations. The binary now carries the schema — the
  baseline `database/init/00-init-complete.sql` as version 1, plus numbered
  files under `database/migrations/postgres/` — and brings the database to it
  at startup, recording progress in a new `schema_migrations` table. An empty
  database is initialized by the server itself; a database created from the
  baseline by an earlier release is adopted as version 1 without being
  modified. The server refuses to start against a schema newer than the
  binary (a downgrade) or one whose applied migrations were altered.
  `OAM_DB_MIGRATE` selects `auto` (default), `check` (apply nothing, refuse to
  start if a migration is pending) or `off`; `loxilb-oam -migrate` applies
  pending migrations and exits. No migration beyond the baseline ships in this
  release — the only change to an existing database is the new table.
- Converged single-node deployment now uses one independent `loxilb-state`
  PostgreSQL service and one `loxioam` database for OAM plus the Gateway's
  isolated `aigw` and dormant `aigw_mgmt` schemas. The bundle includes the
  idempotent Gateway bootstrap, file-backed Gateway database credentials,
  source-drift validation, existing-volume adoption, and database → data →
  management startup/verification automation.
- The approved converged integration image defaults to
  `ghcr.io/loxilb-io/loxilb-inference-gateway:v0.9.9.1-u24`; validation records
  the resolved digest before promotion to an immutable release identity. <!-- OWNER REVIEW PENDING -->
- Optional local Keycloak (with its own PostgreSQL) and syslog-ng audit collector
  Compose projects, selected independently and pinned by digest, for
  installations without an existing Keycloak or TLS-syslog SIEM. The default
  startup is unchanged. Keycloak protects inference Bearer requests; OAM login
  stays separate. <!-- OWNER REVIEW PENDING -->
- An opt-in restricted runtime database role for OAM
  (`docker-compose.oam-app-db.yml`, `database/oam-app-db-bootstrap.sql`), so the
  running service no longer has to use the database owner's credential. <!-- OWNER REVIEW PENDING -->
- `init-converged.sh` asks for a public, NAT or Floating IP address on fresh and
  existing installs and updates the site addresses, allowed origins and
  certificate SANs. Operator and CA-issued certificates and existing credentials
  are preserved. <!-- OWNER REVIEW PENDING -->
- A dedicated `docker-compose.converged-local-ui.yml` developer overlay keeps
  converged PostgreSQL, Gateway, and OAM on a remote testbed while disabling
  the bundled UI and Caddy. It exposes OAM directly over HTTP with an explicit
  local-development CORS allowlist and reserved-port protection.
- `OAM_PROXY_TIMEOUT` bounds a single proxied request to a managed instance
  (default `10s`). Raise it if an instance carries a large enough configuration
  for a management call to run long, so a healthy instance is not reported as
  timing out. An unparseable or non-positive value falls back to the default and
  is reported at startup, rather than silently disabling the bound.
- `OAM_PROXY_DISABLE_KEEPALIVES=true` restores a fresh connection per proxied
  request. An escape hatch for a deployment that hits a connection-reuse problem
  the invalidation points do not cover; it logs a startup warning because it
  costs a handshake on every request.

### Changed — BREAKING
- Existing converged deployments must prepare and adopt the external named
  volumes before startup: Gateway configuration, TLS, service credentials and
  edge TLS now live in named volumes that survive container and image
  replacement, and protected host seeds are no longer runtime mounts. See
  `deploy/compose/PERSISTENT-UPGRADE.md`. <!-- OWNER REVIEW PENDING -->
- The instance proxy (`/oam/loxilbs/{id}/netlox/*`) now authorizes by Gateway
  path as well as by method. Day-to-day configuration is unchanged: every role
  reads it, `admin` and `operator` change it. What changes:
  - `/auth/*` is not available through the proxy to any role.
  - `/config/snapshot`, `/config/export`, `/config/restore`, `/config/import`
    and `/config/persist` are `admin` only, reads included, matching the
    instance snapshot routes.
  - `/audit/*` changes are `admin` only; reading the audit policy and sinks
    needs `admin` or `operator`. `/audit/status` stays readable by every role.
  - `/logs` and `/log-archives` need `admin` or `operator`.
  - A Gateway path OAM does not list is `admin` only.
  - A path with `.` or `..` segments, empty segments, an encoded `/` or `\`,
    `?`, `#` or control characters is refused with `400`; methods other than
    `GET`, `HEAD`, `POST`, `PUT`, `PATCH` and `DELETE` get `405`.

  The full table is in `docs/proxy-functionality.md`.
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
- **A proxied request that times out now answers `504`, where it previously
  answered `502`.** A client that keys on `502` to mean "any proxy failure" must
  also handle `504`. The two are different claims: `502` says the instance could
  not be reached, `504` says it did not answer within `OAM_PROXY_TIMEOUT` — which
  does not establish that it is down. Error responses additionally carry a
  `detail` field naming the cause; `error` keeps its existing shape, and its
  wording for the `502` case is unchanged.

### Changed
- Compose image defaults name this release instead of `latest`: `OAM_TAG` and
  `UI_TAG` default to `v0.9.9.1`, and `IGW_TAG` to `v0.9.9.1-u24`, in
  `docker-compose.yml`, `docker-compose.dataplane.yml`, `.env.example` and the
  `init-converged.sh` prompts. An upgrade now requires changing the tags. Set
  them to `latest` / `latest-u24` to follow the newest final release. <!-- OWNER REVIEW PENDING -->
- A PostgreSQL outage no longer ends a live session: authenticated requests and
  login answer `503` with `authentication_unavailable` and `Retry-After: 5`, and
  the same token works once the database recovers. Missing, expired and revoked
  tokens still answer `401`. <!-- OWNER REVIEW PENDING -->
- Release images are signed with Cosign v3, which stores the signature as a
  Sigstore bundle instead of the legacy `.sig` tag. Verify with Cosign v3 or
  newer; Cosign v2 needs `--new-bundle-format`, otherwise it reports
  `no signatures found`. See `docs/container-image.md`.
- Connections to managed instances are reused instead of dialled afresh on every
  proxied request. Reuse was previously disabled outright to avoid handing a
  request a connection to an endpoint that had since been replaced — a real
  hazard, but one that arises only when an endpoint changes, while the cost was
  paid on every request forever. A small proxied response measured roughly twice
  the latency of the equivalent direct call. The pool is now invalidated where
  the hazard actually occurs: instance update, instance delete, and firmware
  update/start/stop, each of which can move the endpoint or recreate the
  container behind it. Snapshot take/restore deliberately keeps reuse off, since
  a handshake is not measurable against a 60s take or a 5-minute restore.

### Fixed
- Fixed: leaving `OAM_JWT_SECRET` or `OAM_DEFAULT_ADMIN_PASSWORD` blank in a `.env`
  copied from `deploy/compose/.env.example` did not stop the stack, and the
  converged stack failed with `invalid IP address` — Compose read the inline
  comment after a blank value as the value, so the JWT secret became a publicly
  known string — `.env.example` has no inline comments, a blank secret stops
  Compose, and OAM refuses to start on a placeholder or comment in place of a
  secret. <!-- OWNER REVIEW PENDING -->
- Fixed: a user's old sessions kept working after an administrator changed their
  password — the update left issued tokens in place — the password change and
  token revocation now commit together. <!-- OWNER REVIEW PENDING -->
- Fixed: sessions from before initial administrator setup kept working after it
  — setup did not revoke them — setup now revokes them in the same transaction. <!-- OWNER REVIEW PENDING -->
- Fixed: a deleted user's token could still read protected routes — validation
  checked only that the token existed and had not expired — a token now needs a
  live user. <!-- OWNER REVIEW PENDING -->
- Fixed: the only administrator could be demoted or removed, including by
  concurrent requests, leaving nobody able to manage users — the check ran
  outside the change — removing the last administrator is now refused with
  `403`. <!-- OWNER REVIEW PENDING -->
- Fixed: a login in flight during a password change could produce a usable
  session afterwards — issuing the session was a separate step from checking the
  password — issuance is now bound to the current credentials, and a concurrent
  password replacement is never overwritten. <!-- OWNER REVIEW PENDING -->
- Fixed: an operator could acknowledge an alert as another user, and a failed
  write could leave it half recorded — the body's `user_id` was trusted and the
  two writes were separate — a mismatched ID is refused with `403` and both
  writes commit together. An invalid alert type or severity now answers `400`
  instead of `500`. <!-- OWNER REVIEW PENDING -->
- Fixed: OAM exited `0` when it could not reach its database, hiding the cause
  (wrong CA or SAN, rejected credentials) — the connection error was discarded —
  it now exits `1` and logs the real cause. <!-- OWNER REVIEW PENDING -->
- Fixed: OAM exited `0` on invalid Gateway identity, snapshot key or token
  lifetime settings, and kept running without serving when its HTTP listener or
  HTTPS keypair failed — the errors did not reach the main process — both now
  exit `1`. <!-- OWNER REVIEW PENDING -->
- Fixed: Gateway audit records and OAM logs were lost when a container was
  recreated — they lived in the container's writable layer — they now live in
  persistent volumes. <!-- OWNER REVIEW PENDING -->
- Fixed: a fresh converged install answered read requests but returned `503
  audit_unavailable` on audited changes — the new audit volume started at mode
  `0755` and the Gateway requires `0700` — a one-shot job now sets the mode
  before the Gateway starts. <!-- OWNER REVIEW PENDING -->
- Fixed: on cloud hosts `local-gateway` registered successfully but OAM calls to
  it returned `504` — its hostname resolved to a nonexistent Docker bridge, and
  host-loopback checks could not see that — setup now selects a real host
  interface and verifies the connection from inside OAM's network namespace. <!-- OWNER REVIEW PENDING -->
- Fixed: console pages served by the bundled Caddy edge lacked the security
  headers of the standalone UI container, including the CSP — Caddy serves those
  assets directly, bypassing the container's nginx policy — the edge now applies
  the same policies. <!-- OWNER REVIEW PENDING -->
- Fixed: every fresh deployment showed a `LoxiLB-Instance-1` reporting Down —
  database init seeded a placeholder row pointing at a host that does not exist
  — a new database starts with no instances. <!-- OWNER REVIEW PENDING -->
- Fixed: the dev overlay failed for anyone who cloned only this repository — it
  built the console from a sibling `loxilb-ui` checkout — it now uses the
  `UI_IMAGE` / `UI_TAG` image in every mode. <!-- OWNER REVIEW PENDING -->
- A console served from another origin could not read `Retry-After`,
  `X-Request-Id`, `X-Correlation-Id`, `X-Snapshot-Checksum`,
  `X-Content-Checksum` or `Content-Disposition`, because CORS exposed only the
  error-origin marker; and could not send `If-Match`, `If-None-Match`,
  `X-Request-Id` or `X-Correlation-Id`, which the instance proxy forwards.
  The exposed list now includes the former and the allowed list every header
  the proxy forwards. Same-origin deployments were not affected.
- The instance proxy did not pass the caller's request context to the
  instance, so a client that disconnected left the call running until the
  proxy timeout. The call now stops with the caller. Snapshot take and restore
  are deliberately not tied to the caller.
- A gateway's `Retry-After` was dropped on snapshot take and restore. It is
  relayed on a refused take, and on a restore as the response's `Retry-After`
  header and `gateway_retry_after`.
- Snapshot restore answered `500` when OAM's gateway service identity was
  unavailable, where the instance proxy answers `503` for the same condition;
  it now answers `503`. An invalid restore `mode` was reported as "invalid
  snapshot document"; it is now reported as an invalid restore request.
- Two logins by the same user within the same second no longer fail. The JWT
  carried no unique identifier, so both signed to the identical string and the
  second was rejected by the token store's unique key (`500 Could not save
  token`). Every token now carries a random `jti` and an `iat`; concurrent
  sessions are distinct and are revoked independently. Tokens issued before
  this change stay valid and revocable until they expire.
- `GET /oam/loxilbs` returns `[]`, not `null`, when no instance is registered.
  A failed query is still an error, never an empty success.
- `GET /oam/loxilbs` no longer returns the database driver's error text in its
  `500` body; the cause is logged and the response carries a fixed message.
- A Gateway failure relayed by the proxy, or by taking a snapshot, now
  carries `X-Loxi-Error-Origin: gateway`, exposed to cross-origin consoles through
  `Access-Control-Expose-Headers`. Without it the console could not tell a
  Gateway refusing OAM's management credential (401) from the operator's own
  session expiring, and signed the operator out a second after every login.
  OAM sets the marker itself; a Gateway's own value is never relayed.
- The proxy no longer relays the Gateway's `Access-Control-*` response
  headers. They overwrote OAM's CORS policy, so every pass-through answered
  `Access-Control-Allow-Origin: *` regardless of `OAM_ALLOWED_ORIGINS`.
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
- Every failure of a proxied instance request was reported to the operator as
  `502 "LoxiLB instance unreachable"`, including a timeout — a positive assertion
  that the instance is down, which a timeout does not establish. The proxy
  flattened every transport failure into one string and discarded the cause, and
  the handler then classified on that string; since no error it could return
  contained the text `timeout`, the `504` branch was unreachable code. Failures
  are now classified from the error itself, so a timeout, a refused connection, a
  DNS failure, a TLS rejection and a reset are each reported distinctly. See
  [docs/instance-proxy.md](docs/instance-proxy.md) for the full status contract.

### Security
- OAM reaches the Gateway with its own service identity, separate from the
  signed-in user, and strips browser `Authorization`, cookies and API keys
  before proxying. Tokens can be supplied as a value or a file; the converged
  bundle generates one and requires it. <!-- OWNER REVIEW PENDING -->
- Refreshed `golang.org/x/crypto`, `golang.org/x/net` and related Go modules,
  moved the module to Go 1.26, and upgraded Alpine runtime packages in the
  image. <!-- OWNER REVIEW PENDING -->
- The instance proxy decided on the HTTP method alone, while it reaches the
  Gateway with OAM's own identity. It now decides on the Gateway path too, on
  one canonical form of the path that is also the form forwarded, and resolves
  the caller from the database for reads as well as writes. See the breaking
  change above for what each role may reach.
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

[Unreleased]: https://github.com/loxilb-io/loxilb-oam/compare/v0.9.9.1...HEAD
[v0.9.9.1]: https://github.com/loxilb-io/loxilb-oam/compare/v0.9.8.7...v0.9.9.1
[v0.9.8.7]: https://github.com/loxilb-io/loxilb-oam/releases/tag/v0.9.8.7
