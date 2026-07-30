# loxilb-oam

Operations, Administration, and Management (OAM) service for
[LoxiLB](https://github.com/loxilb-io/loxilb). `loxilb-oam` is a Go REST API that
provides centralized authentication, role-based access control, user management,
instance lifecycle operations, configuration snapshots, and a management proxy
for one or more LoxiLB instances, backed by MySQL.

## Features

- **Authentication** — JWT-based login with server-side token revocation and
  exponential-backoff login lockout.
- **RBAC** — three-role model (`admin`, `operator`, `viewer`) resolved from the
  database on every request, with a capability-gated management proxy.
- **User management** — CRUD, password policy enforcement, and admin bootstrap.
- **Instance management** — register and operate LoxiLB instances; proxy
  management calls through to each instance's API.
- **Instance snapshots** — capture and restore instance configuration, with
  optional AES-256-GCM encryption at rest and integrity checksums.
- **Observability** — logs, alerts, and acknowledgements.
- **OpenAPI/Swagger** UI served at `/oam/swagger/`.

## Prerequisites

- Go **1.23+**
- MySQL **8.x**
- (Optional) Docker / Docker Compose or Kubernetes for containerized deployment

## Quick start

```bash
git clone https://github.com/loxilb-io/loxilb-oam.git
cd loxilb-oam

# Configure required secrets (the server refuses to start without them)
cp .env.example .env
# edit .env and set OAM_JWT_SECRET, OAM_DEFAULT_ADMIN_PASSWORD, OAM_DB_PASSWORD, ...

# Run with Docker Compose (brings up MySQL + the service)
docker compose up -d
```

On a fresh database, a bootstrap `admin` account is created with the password
from `OAM_DEFAULT_ADMIN_PASSWORD`. **Change it on first login.**

### Build and run from source

```bash
make build                 # produces the loxilb-oam binary
export OAM_JWT_SECRET=... OAM_DEFAULT_ADMIN_PASSWORD=... OAM_DB_PASSWORD=...
./loxilb-oam -db-user=oamuser -db-host=127.0.0.1 -db-port=3306 -db-name=loxioam -port=8080

# or
make run
```

## Configuration

Secrets are supplied via environment variables; the server **fails fast** at
startup if a required one is unset (there are no built-in fallback values).

### Required

| Variable | Purpose |
|----------|---------|
| `OAM_JWT_SECRET` | JWT signing key (use a long random value) |
| `OAM_DEFAULT_ADMIN_PASSWORD` | Bootstrap admin password (set on first run) |
| `OAM_DB_PASSWORD` | Database password (or pass `-db-password`) |

### Recommended

| Variable | Purpose |
|----------|---------|
| `SNAPSHOT_ENC_KEY` | base64 32-byte AES-256 key. **Without it, snapshots — which contain IPsec PSKs and certificate private keys — are stored unencrypted.** |
| `OAM_ALLOWED_ORIGINS` | Comma-separated CORS allowlist. Unset = wildcard (development only). |

### Optional

| Variable | Default | Purpose |
|----------|---------|---------|
| `OAM_TOKEN_TTL_MINUTES` | `480` | JWT / API-token lifetime in minutes |
| `OAM_OAUTH_ENABLED` | `false` | Enable OAuth login (experimental, opt-in). Routes are not registered unless `true`. |
| `OAM_OAUTH_{GOOGLE,GITHUB,FACEBOOK}_CLIENT_ID` / `_CLIENT_SECRET` | unset | OAuth provider credentials (used only when OAuth is enabled) |
| `OAM_INSTANCE_CA_BUNDLE` / `OAM_INSTANCE_TLS_INSECURE` | unset / `false` | Trust a private CA for managed-instance TLS, or (dev-only) skip verification |
| `OAM_DOCKER_TLS` / `OAM_DOCKER_PORT` / `OAM_DOCKER_CERT_PATH` | `false` / `2375` / unset | TLS + connection settings for the Docker Engine API on instance hosts |

### CLI flags

`-db-user` (default `oamuser`), `-db-password`, `-db-host` (`127.0.0.1`),
`-db-port` (`3306`), `-db-name` (`loxioam`), `-port` (`8080`),
`-token-expiration`, `-enable-https`, `-ssl-cert-file`, `-ssl-key-file`,
`-ssl-option` and the associated CA/client-cert flags.

Generate strong secrets with, e.g., `openssl rand -base64 48` (keys) and
`openssl rand -base64 32` (`SNAPSHOT_ENC_KEY`).

## Deployment

Multiple deployment paths are provided; see [DEPLOYMENT.md](DEPLOYMENT.md) for details.

- **Docker Compose** — `docker compose up -d` runs the default HTTP stack
  (MySQL + service). For TLS, add the override:
  `docker compose -f docker-compose.yml -f docker-compose.https.yml up -d`.
- **Kubernetes (Kustomize)** — `k8s/base`, `k8s/base-http`, and
  `k8s/overlays/{development,production}`. Populate the `CHANGE_ME` placeholders
  in the Secret manifests out-of-band (never commit real secrets).

## API

- Base path: `/oam`
- Health check: `GET /oam/health`
- Interactive API docs: `GET /oam/swagger/index.html`
- First-run setup: `GET /oam/setup/status`, `POST /oam/setup/update-admin`

## Development

```bash
make deps     # download dependencies
make test     # run unit tests
go vet ./...  # static checks
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines and
[SECURITY.md](SECURITY.md) for vulnerability reporting.

## Community & Governance

Contributions are welcome via pull request. Every PR needs at least one approving review from a
maintainer and passing CI before it can merge.

- [CONTRIBUTING.md](CONTRIBUTING.md) — how to build, test, and submit changes (incl. DCO sign-off)
- [GOVERNANCE.md](GOVERNANCE.md) — project governance and decision-making
- [MAINTAINERS.md](MAINTAINERS.md) — current maintainers
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — community standards

## License

Licensed under the [Apache License 2.0](LICENSE).
