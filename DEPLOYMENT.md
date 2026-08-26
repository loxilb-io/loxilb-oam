# loxilb-oam Deployment Guide

This document is the **configuration reference** for the `loxilb-oam` service —
every environment variable and CLI flag it reads — plus the deployment guide for
the single-service Docker Compose stack at the repository root.

> **Deploying the full management plane (UI + API + DB)?** That is the
> single-node Compose bundle in [`deploy/compose/`](deploy/compose/), and it has
> its own step-by-step operator guide:
> [docs/deployment-compose.md](docs/deployment-compose.md). It is the
> recommended deployment for production.

## Table of Contents

1. [Configuration Reference](#configuration-reference)
2. [Docker Compose Deployment](#docker-compose-deployment)
3. [HTTPS/SSL Support](#httpsssl-support)
4. [Kubernetes Deployment](#kubernetes-deployment) — **pre-release, not supported**
5. [Database Initialization](#database-initialization)
6. [Health Checks](#health-checks)
7. [Troubleshooting](#troubleshooting)

## Configuration Reference

Runtime configuration comes from environment variables (secrets, CORS, token
lifetime) and CLI flags (database connection, port, TLS). The server **refuses
to start** if the required secrets below are unset.

### Required environment variables

| Variable                    | Description |
|-----------------------------|-------------|
| `OAM_JWT_SECRET`            | JWT signing key. |
| `OAM_DEFAULT_ADMIN_PASSWORD`| Bootstrap admin password. |
| `DB_PASSWORD`               | Database password. May instead be passed with the `-db-password` flag. Legacy alias: `OAM_DB_PASSWORD`. |

### Strongly recommended

| Variable              | Description |
|-----------------------|-------------|
| `SNAPSHOT_ENC_KEY`    | Base64-encoded 32-byte AES-256 key for snapshot encryption at rest. **Without it, instance snapshots — which contain IPsec PSKs and certificate private keys — are stored UNENCRYPTED.** An invalid key aborts startup; an unset key logs a prominent warning. |
| `OAM_ALLOWED_ORIGINS` | Comma-separated CORS origin allowlist (e.g. `https://oam.example.com,http://localhost:3000`). If unset, CORS falls back to wildcard `*` without credentials — **development only**. |
| `OAM_TRUSTED_PROXIES`  | Comma-separated IPs/CIDRs of reverse proxies whose `X-Forwarded-For` header OAM will believe (e.g. `172.16.0.0/12`). The resulting client IP keys the per-IP rate limiter and the failed-login lockout. Unset = the header is ignored and the real peer address is used, which is correct for direct access but collapses every client into one bucket when OAM sits behind a proxy. Never list an address range that untrusted callers can originate from — anyone inside it can forge the header and evade both controls. |

### Optional

| Variable                                              | Description | Default |
|-------------------------------------------------------|-------------|---------|
| `OAM_TOKEN_TTL_MINUTES`                                | JWT / API-token lifetime in minutes. Read by the bare binary. In the container images the equivalent knob is `TOKEN_EXPIRATION`, which the entrypoint maps to `-token-expiration`. | `480` (8h) |
| `OAM_INSTANCE_CA_BUNDLE`                               | PEM bundle trusted when connecting to managed LoxiLB instances (proxying and snapshots). Unset = system roots only. See [docs/instance-tls.md](docs/instance-tls.md). | — |
| `OAM_INSTANCE_TLS_INSECURE`                            | Skip certificate verification on connections to managed instances. **Development only**; logs a startup warning. | `false` |
| `OAM_DOCKER_TLS` / `OAM_DOCKER_PORT` / `OAM_DOCKER_CERT_PATH` | TLS and connection settings for the Docker Engine API on instance hosts. `OAM_DOCKER_CERT_PATH` must contain `ca.pem`, `cert.pem`, `key.pem`. | `false` / `2375` / — |

> **Removed:** OAuth login (`OAM_OAUTH_ENABLED`, `OAM_OAUTH_*_CLIENT_ID` /
> `_CLIENT_SECRET`, and the `-google/-github/-facebook-redirect-url` flags) was
> withdrawn before the public release — it was unfinished and untested against
> the real handlers. Those variables and flags no longer exist; remove them from
> any configuration carried over from an earlier version, and note that passing
> the removed **flags** will make the binary exit with `flag provided but not
> defined`. Authentication is username/password against the local user store.
> The archived implementation is on the `feature/oauth2` branch. Databases
> created before this release should apply
> `database/migrations/005_drop_oauth_columns.sql` to drop the now-unused
> `oauth_*` columns.

### CLI flags (server & database)

| Flag                 | Description | Default |
|----------------------|-------------|---------|
| `-db-user`           | Database username | `oamuser` |
| `-db-password`       | Database password (default: `DB_PASSWORD` env; legacy alias `OAM_DB_PASSWORD`) | — |
| `-db-host`           | Database host | `127.0.0.1` |
| `-db-port`           | Database port | `5432` |
| `-db-name`           | Database name | `loxioam` |
| `-port`              | Server port | `8080` |
| `-token-expiration`  | Token lifetime in minutes (overrides `OAM_TOKEN_TTL_MINUTES`) | — |
| `-enable-https`      | Enable the HTTPS server | `false` |
| `-ssl-cert-file`     | Path to the server certificate | `./ssl/certs/server.crt` |
| `-ssl-key-file`      | Path to the server private key | `./ssl/certs/server.key` |
| `-ssl-option`        | Enable SSL for the database connection | `false` |
| `-ssl-ca-cert-file`, `-ssl-ca-client-cert-file`, `-ssl-ca-client-key-file` | Database TLS certificate paths | see `main.go` |

Each `-db-*` flag defaults from the matching `DB_*` environment variable
(`DB_USER`/`DB_PASSWORD`/`DB_HOST`/`DB_PORT`/`DB_NAME`); an explicit flag wins.
The container image's entrypoint reads that same `DB_*` family (plus
`SERVER_PORT` and `TOKEN_EXPIRATION`) and passes the values on as flags, so the
`reset_admin` tool — which reads the same `DB_*` surface — needs no extra flags
inside a containerized deployment.

### Compose files

The repository ships two distinct Compose deployments — do not mix them:

| Path | What it runs | Use it for |
|------|--------------|------------|
| `docker-compose.yml` (repository root) | PostgreSQL + the OAM API over plain **HTTP**, built from local source | development, and API-only deployments |
| [`deploy/compose/`](deploy/compose/) | the full management plane — UI + API + PostgreSQL behind a **Caddy TLS edge** | **production** ([guide](docs/deployment-compose.md)) |

Running the published image directly — `docker run`, image tags, signature and
SBOM verification, building your own image, air-gapped installs — is covered in
[docs/container-image.md](docs/container-image.md).

Each has its own `.env.example`; the key names are shared, so a `.env` written
for one is largely portable to the other. Provide the required secrets
(`OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`, `DB_PASSWORD`,
and the recommended `SNAPSHOT_ENC_KEY` via that `.env`
file or your secret manager.

The rest of this section covers the root stack. For the management-plane bundle,
follow [docs/deployment-compose.md](docs/deployment-compose.md) instead.

## Docker Compose Deployment

### Prerequisites

- Docker Engine 20.10+
- Docker Compose v2.0+

### Quick Start

1. **Configure secrets:**
   ```bash
   cp .env.example .env    # then set the required secrets
   ```

2. **Build and start** (Compose builds the image on first run):
   ```bash
   docker compose up -d
   ```

3. **Check deployment status:**
   ```bash
   docker compose ps
   docker compose logs
   ```

4. **Access the application:**
   - Application: http://localhost:8080
   - Health check: http://localhost:8080/oam/health
   - API documentation: http://localhost:8080/oam/swagger/index.html

## HTTPS/SSL Support

OAM-LoxiLB supports HTTPS with certificate management for development and
production.

### SSL Certificate Generation

#### Development (self-signed)

```bash
make generate-ssl-certs
# or run the script directly:
./scripts/ssl/generate-dev-certs.sh
```

The certificates are written to `ssl/dev_certs/`. `make run-https` reads them
from `ssl/server_certs/`, so copy them there before starting the server:

```bash
mkdir -p ssl/server_certs
cp ssl/dev_certs/server.crt ssl/dev_certs/server.key ssl/server_certs/
```

#### Production

Use certificates from a trusted Certificate Authority (e.g. Let's Encrypt).

### HTTPS on Docker

The default `docker-compose.yml` serves plain HTTP. For an HTTPS deployment,
use the management-plane bundle in [`deploy/compose/`](deploy/compose/), where a
Caddy edge terminates TLS in front of the OAM API and the UI (see its README and
`.env.example` for the `SITE_ADDRESS` / `EDGE_TLS` knobs). The `make generate-ssl-certs`
certificates above are for the bare binary (`make run-https`) and the Kubernetes
HTTPS track.

### Trust self-signed certificates (development)

**macOS:**
```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ssl/dev_certs/ca.crt
```

**Linux:**
```bash
sudo cp ssl/dev_certs/ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates
```

## Kubernetes Deployment

> ### ⚠️ Pre-release — not supported for this release
>
> **The manifests under `k8s/` are incomplete and will not start the service as
> shipped.** None of them supply `OAM_JWT_SECRET` or
> `OAM_DEFAULT_ADMIN_PASSWORD`, both of which are mandatory (see
> [Required environment variables](#required-environment-variables)) — the
> container aborts at startup and the Pod enters `CrashLoopBackOff`. The
> production overlay also pins an image tag (`oam-loxilb:v0.9.8.7`) that is not
> published.
>
> They are retained as the starting point for a converged Kubernetes deployment
> (OAM + UI + PostgreSQL + cert-manager) that is being developed separately. **Do not
> deploy them.**
>
> **For a supported deployment, use Docker Compose** — either the full
> management-plane bundle in [`deploy/compose/`](deploy/compose/) (see
> [docs/deployment-compose.md](docs/deployment-compose.md)) or the
> single-service stack described above.

If you are working on those manifests, the current layout is:

```
k8s/
├── base/            # HTTPS base (PostgreSQL + application)
├── base-http/       # HTTP-only base
└── overlays/
    ├── development/
    └── production/  # wires SNAPSHOT_ENC_KEY via a Secret
```

At minimum, a working overlay must add `OAM_JWT_SECRET` and
`OAM_DEFAULT_ADMIN_PASSWORD` to `oam-loxilb-secret.yaml` and reference them from
the Deployment's `env:`. The container image translates the `DB_*` environment
family into `-db-*` flags at entrypoint, so the database wiring in the existing
manifests is correct as-is.

## Database Initialization

On first startup the PostgreSQL container runs `database/init/00-init-complete.sql`,
which creates all tables (users, instances, tokens, logs, alerts,
acknowledgments, login attempts, instance snapshots, and system config),
performance indexes, and seed rows. For existing databases, apply the numbered
files under `database/migrations/` in order. See
[docs/oam-db.md](docs/oam-db.md) for the schema reference.

The schema is applied only on first boot of an **empty** data volume. To
reinitialize, destroy the volume:

```bash
docker compose down -v
docker compose up -d
```

Running the service against a database you manage yourself (containerized or
external) is covered in
[docs/database-installation.md](docs/database-installation.md).

## Health Checks

```bash
# Application (reports application and database connectivity)
curl http://localhost:8080/oam/health

# Database
docker compose exec postgres sh -c 'exec pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

## Monitoring and Logs

```bash
docker compose logs -f
docker compose logs -f oam-loxilb
```

## Troubleshooting

### Application won't start

- **Missing secrets:** the server aborts when `OAM_JWT_SECRET`,
  `OAM_DEFAULT_ADMIN_PASSWORD`, or the database password is unset. Check the
  startup logs and provide the required values (see
  [Configuration Reference](#configuration-reference)).
- **Invalid `SNAPSHOT_ENC_KEY`:** a malformed key aborts startup. It must be a
  base64-encoded 32-byte value. Leaving it unset (not recommended) only logs a
  warning.
- **Database connection failed:** verify PostgreSQL is healthy and the `-db-*` flags
  / `DB_*` env vars are correct.
  ```bash
  docker compose logs postgres
  ```
- **Bootstrap admin not created / container restarting on a fresh install:**
  `OAM_DEFAULT_ADMIN_PASSWORD` violated the account password policy (≥9
  characters with upper, lower, digit and special; no character three times in a
  row; not equal to the username), so the initial admin could not be created and
  the API refuses to run. The log shows `failed to set up the initial admin
  account`. Set a compliant password and run `up -d` again.

### Cleanup

```bash
docker compose down      # keep data
docker compose down -v   # destroy data
```

## Security Considerations

1. Always set the required secrets; never rely on the development compose file
   in production.
2. Set `SNAPSHOT_ENC_KEY` so snapshots (which contain IPsec PSKs and certificate
   private keys) are encrypted at rest.
3. Set `OAM_ALLOWED_ORIGINS` to a real allowlist; the wildcard fallback is for
   development only.
4. Use a secret manager for all credentials; never commit them.
5. Enable TLS/HTTPS for production deployments.
6. Apply network policies and run non-root containers where possible.
