# loxilb-oam

[![CI](https://github.com/loxilb-io/loxilb-oam/actions/workflows/ci.yml/badge.svg)](https://github.com/loxilb-io/loxilb-oam/actions/workflows/ci.yml)
[![CodeQL](https://github.com/loxilb-io/loxilb-oam/actions/workflows/codeql.yml/badge.svg)](https://github.com/loxilb-io/loxilb-oam/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/loxilb-io/loxilb-oam)](https://goreportcard.com/report/github.com/loxilb-io/loxilb-oam)
[![Latest release](https://img.shields.io/github/v/release/loxilb-io/loxilb-oam)](https://github.com/loxilb-io/loxilb-oam/releases/latest)
[![License](https://img.shields.io/github/license/loxilb-io/loxilb-oam)](LICENSE)

Operations, Administration, and Management (OAM) service for
[LoxiLB](https://github.com/loxilb-io/loxilb). `loxilb-oam` is a Go REST API that
provides centralized authentication, role-based access control, user management,
instance lifecycle operations, configuration snapshots, and a management proxy
for one or more LoxiLB instances, backed by MySQL.

**Version:** `v0.9.8.7`. loxilb-oam versions in lockstep with
[loxilb](https://github.com/loxilb-io/loxilb) and uses the same
`vMAJOR.MINOR.PATCH[.BUILD]` scheme — run loxilb-oam `v0.9.8.7` against loxilb
`v0.9.8.7`. `loxilb-oam -version` reports the build's release identifier
(`dev` for an unstamped local build); see [CHANGELOG.md](CHANGELOG.md).

## Features

- **Authentication** — JWT-based username/password login against the local user
  store, with server-side token revocation and exponential-backoff login
  lockout.
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

- Go **1.25+** (the module targets `go 1.25`)
- MySQL **8.x** or later (the Compose stacks ship MySQL 9.2)
- (Optional) Docker Engine with the Compose plugin, for containerized deployment

## Quick start

```bash
git clone https://github.com/loxilb-io/loxilb-oam.git
cd loxilb-oam

# Configure required secrets (the server refuses to start without them)
cp .env.example .env
# edit .env and set OAM_JWT_SECRET, OAM_DEFAULT_ADMIN_PASSWORD, DB_PASSWORD, ...

# Run with Docker Compose (brings up MySQL + the service)
docker compose up -d
```

On a fresh database, a bootstrap `admin` account is created with the password
from `OAM_DEFAULT_ADMIN_PASSWORD`. **Change it on first login.**

### Build and run from source

```bash
make build                 # produces the loxilb-oam binary
export OAM_JWT_SECRET=... OAM_DEFAULT_ADMIN_PASSWORD=... DB_PASSWORD=...
# DB connection defaults from the DB_* env family; flags override it.
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
| `DB_PASSWORD` | Database password (or pass `-db-password`; legacy alias `OAM_DB_PASSWORD`) |

### Recommended

| Variable | Purpose |
|----------|---------|
| `SNAPSHOT_ENC_KEY` | base64 32-byte AES-256 key. **Without it, snapshots — which contain IPsec PSKs and certificate private keys — are stored unencrypted.** |
| `OAM_ALLOWED_ORIGINS` | Comma-separated CORS allowlist. Unset = wildcard (development only). |
| `OAM_TRUSTED_PROXIES` | Comma-separated proxy IPs/CIDRs whose `X-Forwarded-For` is trusted. **Required when OAM runs behind a reverse proxy**, otherwise rate limiting and login lockout see the proxy's IP for every client. Unset = header ignored, peer address used. |

### Optional

| Variable | Default | Purpose |
|----------|---------|---------|
| `OAM_TOKEN_TTL_MINUTES` | `480` | JWT / API-token lifetime in minutes |
| `OAM_INSTANCE_CA_BUNDLE` / `OAM_INSTANCE_TLS_INSECURE` | unset / `false` | Trust a private CA for managed-instance TLS, or (dev-only) skip verification |
| `OAM_DOCKER_TLS` / `OAM_DOCKER_PORT` / `OAM_DOCKER_CERT_PATH` | `false` / `2375` / unset | TLS + connection settings for the Docker Engine API on instance hosts |

### CLI flags

`-db-user` (default `oamuser`), `-db-password`, `-db-host` (`127.0.0.1`),
`-db-port` (`3306`), `-db-name` (`loxioam`), `-port` (`8080`),
`-token-expiration`, `-enable-https`, `-ssl-cert-file`, `-ssl-key-file`,
`-ssl-option` and the associated CA/client-cert flags.

The `-db-*` flags each default from the matching `DB_*` environment variable
(`DB_USER`/`DB_PASSWORD`/`DB_HOST`/`DB_PORT`/`DB_NAME`); an explicit flag wins.
The same surface is read by the `reset_admin` tool.

Generate strong secrets with, e.g., `openssl rand -base64 48` (keys) and
`openssl rand -base64 32` (`SNAPSHOT_ENC_KEY`).

## Deployment

- **Management plane (recommended for production)** — the single-node bundle in
  [`deploy/compose/`](deploy/compose/) runs the web console, this API, and MySQL
  behind a Caddy TLS edge. Step-by-step operator guide:
  [docs/deployment-compose.md](docs/deployment-compose.md).
- **API only** — `docker compose up -d` from the repository root runs MySQL plus
  the service over plain HTTP. See [DEPLOYMENT.md](DEPLOYMENT.md).
- **From source, against your own database** — see
  [docs/database-installation.md](docs/database-installation.md).

Released images are published to `ghcr.io/loxilb-io/loxilb-oam`, Cosign-signed
with SLSA provenance and an SBOM. Image tags, the container environment
surface, signature verification, building your own image and air-gapped
installs are covered in
[docs/container-image.md](docs/container-image.md).

[DEPLOYMENT.md](DEPLOYMENT.md) is also the full configuration reference: every
environment variable and CLI flag the service reads.

> The Kubernetes manifests under `k8s/` are **pre-release and not supported** for
> this release — as shipped they do not supply the mandatory secrets and will not
> start. A converged Kubernetes deployment is in development; until then, use
> Docker Compose. Details in [DEPLOYMENT.md](DEPLOYMENT.md#kubernetes-deployment).

## API

- Base path: `/oam`
- Health check: `GET /oam/health`
- Interactive API docs: `GET /oam/swagger/index.html`
- First-run setup: `GET /oam/setup/status`, `POST /oam/setup/update-admin`

## Development

```bash
make deps     # download the dependencies pinned in go.mod
make test     # run the unit tests (same set as the CI gate)
go vet ./...  # static checks
```

Integration suites under `tests/rest_api/` and `tests/e2e/` need a live server
and database, so they are excluded from `make test`; CI runs them against a
MySQL service container.

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
