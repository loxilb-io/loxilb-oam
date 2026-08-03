# OAM-LoxiLB Deployment Guide

This guide covers deploying OAM-LoxiLB with Docker Compose and Kubernetes.

> **Deploying the full management plane (UI + API + DB)?** Use the
> single-node Compose bundle instead — step-by-step operator guide:
> [docs/deployment-compose.md](docs/deployment-compose.md). This document
> remains the configuration reference for the OAM service itself.

## Table of Contents

1. [Configuration Reference](#configuration-reference)
2. [Docker Compose Deployment](#docker-compose-deployment)
3. [HTTPS/SSL Support](#httpsssl-support)
4. [Kubernetes Deployment](#kubernetes-deployment)
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
| `OAM_DB_PASSWORD`           | Database password. May instead be passed with the `-db-password` flag. |

### Strongly recommended

| Variable              | Description |
|-----------------------|-------------|
| `SNAPSHOT_ENC_KEY`    | Base64-encoded 32-byte AES-256 key for snapshot encryption at rest. **Without it, instance snapshots — which contain IPsec PSKs and certificate private keys — are stored UNENCRYPTED.** An invalid key aborts startup; an unset key logs a prominent warning. |
| `OAM_ALLOWED_ORIGINS` | Comma-separated CORS origin allowlist (e.g. `https://oam.example.com,http://localhost:3000`). If unset, CORS falls back to wildcard `*` without credentials — **development only**. |

### Optional

| Variable                                              | Description | Default |
|-------------------------------------------------------|-------------|---------|
| `OAM_TOKEN_TTL_MINUTES`                                | JWT / API-token lifetime in minutes. | `480` (8h) |
| `OAM_OAUTH_{GOOGLE,GITHUB,FACEBOOK}_CLIENT_{ID,SECRET}` | OAuth client credentials, per provider. Unset providers are disabled. OAuth is slated for removal — see [docs/oauth2.md](docs/oauth2.md). | — |

### CLI flags (server & database)

| Flag                 | Description | Default |
|----------------------|-------------|---------|
| `-db-user`           | Database username | `oamuser` |
| `-db-password`       | Database password (or `OAM_DB_PASSWORD`) | — |
| `-db-host`           | Database host | `127.0.0.1` |
| `-db-port`           | Database port | `3306` |
| `-db-name`           | Database name | `loxioam` |
| `-port`              | Server port | `8080` |
| `-token-expiration`  | Token lifetime in minutes (overrides `OAM_TOKEN_TTL_MINUTES`) | — |
| `-enable-https`      | Enable the HTTPS server | `false` |
| `-ssl-cert-file`     | Path to the server certificate | `./ssl/certs/server.crt` |
| `-ssl-key-file`      | Path to the server private key | `./ssl/certs/server.key` |
| `-ssl-option`        | Enable SSL for the database connection | `false` |
| `-ssl-ca-cert-file`, `-ssl-ca-client-cert-file`, `-ssl-ca-client-key-file` | Database TLS certificate paths | see `main.go` |
| `-google-redirect-url`, `-github-redirect-url`, `-facebook-redirect-url` | OAuth callback URLs (per deployment) | — |

> **Note:** OAuth redirect URLs are CLI flags, not environment variables. Only
> the OAuth **client credentials** (`OAM_OAUTH_*`) are read from the
> environment.

### Compose files

Two files describe the whole stack:

- **`docker-compose.yml`** — the default stack: MySQL + the OAM service over
  HTTP. It wires all required secrets from the environment (`.env`).
- **`docker-compose.https.yml`** — a small override that serves the API over TLS
  on `:443`; use it together with the base file.

Provide the required secrets (`OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`,
`DB_PASSWORD`, `MYSQL_ROOT_PASSWORD`, `MYSQL_PASSWORD`) and the recommended
`SNAPSHOT_ENC_KEY` via a `.env` file (see `.env.example`) or your secret manager.
The Kubernetes **production** overlay (`k8s/overlays/production/`) wires these
from Kubernetes Secrets.

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

#### Production

Use certificates from a trusted Certificate Authority (e.g. Let's Encrypt).

### HTTPS Docker Compose Deployment

```bash
# HTTPS with automatic dev-certificate generation
make deploy-docker-compose-https

# Or manually — the base file plus the HTTPS override
docker compose -f docker-compose.yml -f docker-compose.https.yml up -d
```

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

### Prerequisites

- Kubernetes cluster 1.20+
- `kubectl` configured
- Kustomize (recommended)

### Layout

```
k8s/
├── base/            # base manifests (MySQL + application, HTTP)
├── base-http/       # HTTP-only base
└── overlays/
    ├── development/
    └── production/  # wires SNAPSHOT_ENC_KEY via a Secret
```

### Quick Deployment

```bash
# Build and tag the image
docker build -t oam-loxilb:latest .

# Development
kubectl apply -k k8s/overlays/development

# Production (provides SNAPSHOT_ENC_KEY and other secrets)
kubectl apply -k k8s/overlays/production
```

### Accessing the Application

```bash
# Port forward (development)
kubectl port-forward svc/oam-loxilb-service 8080:8080 -n oam-loxilb
```

For production, configure your DNS and ingress to point at the OAM service.

## Database Initialization

On first startup the MySQL container runs `database/init/00-init-complete.sql`,
which creates all tables (users, instances, tokens, logs, alerts,
acknowledgments, login attempts, instance snapshots, and system config),
performance indexes, and seed rows. For existing databases, apply the numbered
files under `database/migrations/` in order. See
[docs/oam-db.md](docs/oam-db.md) for the schema reference.

### Reinitialize

#### Docker Compose
```bash
docker compose down -v
docker compose up -d
```

#### Kubernetes
```bash
kubectl delete pvc mysql-pvc -n oam-loxilb
kubectl apply -f k8s/base/mysql-pvc.yaml
kubectl rollout restart deployment/mysql -n oam-loxilb
```

## Health Checks

### Application

```bash
curl http://localhost:8080/oam/health
```

The `/oam/health` endpoint reports application and database connectivity.

### Database

#### Docker Compose
```bash
docker compose exec mysql mysqladmin ping -h localhost -u root -p
```

#### Kubernetes
```bash
kubectl exec -it deployment/mysql -n oam-loxilb -- mysqladmin ping -h localhost -u root -p
```

## Monitoring and Logs

### Docker Compose
```bash
docker compose logs -f
docker compose logs -f oam-loxilb
```

### Kubernetes
```bash
kubectl logs -f deployment/oam-loxilb -n oam-loxilb
kubectl get pods -n oam-loxilb
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
- **Database connection failed:** verify MySQL is healthy and the `-db-*` flags
  / `OAM_DB_PASSWORD` are correct.
  ```bash
  docker compose logs mysql
  kubectl logs -f deployment/mysql -n oam-loxilb
  ```

### Image pull errors in Kubernetes

```bash
# Build and load the image into your local cluster
docker build -t oam-loxilb:latest .
minikube image load oam-loxilb:latest    # minikube
kind load docker-image oam-loxilb:latest # kind
```

### Cleanup

#### Docker Compose
```bash
docker compose down      # keep data
docker compose down -v   # destroy data
```

#### Kubernetes
```bash
kubectl delete -k k8s/overlays/production
kubectl delete pvc mysql-pvc -n oam-loxilb
kubectl delete namespace oam-loxilb
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
