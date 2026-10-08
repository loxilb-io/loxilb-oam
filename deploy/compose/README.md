# LoxiLB management-plane bundle (Docker Compose, single node)

Runs the whole management plane on one host:

```
browser ──HTTP/HTTPS──▶ caddy (edge)
                          ├─ /            → static SPA (loxilb-ui)
                          └─ /api/oam/*   → oam-loxilb (API) ──▶ postgres
                                               └─ TLS ──▶ managed LoxiLB instances
```

Normal mode has three long-running services — **caddy**, **oam-loxilb**,
**postgres** — plus a one-shot **ui-assets** job. Converged mode moves the same
single PostgreSQL service into an independent `loxilb-state` project and adds
the `loxilb-data` Gateway project. **Full step-by-step operator guide:**
[`docs/deployment-compose.md`](../../docs/deployment-compose.md).

## Quick start

```bash
cp .env.example .env      # fill in the required secrets
# edit .env: DB_PASSWORD, OAM_JWT_SECRET,
#            OAM_DEFAULT_ADMIN_PASSWORD, SNAPSHOT_ENC_KEY
```

The bundle has three main modes plus one developer-only converged variant.

**Mode 1 — Development, end-to-end HTTP.** Builds images from the local
`loxilb-oam` + `loxilb-ui` checkouts; edge on plain HTTP (`SITE_ADDRESS=:80`,
the shipped default); instance-cert verification off:
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

**Mode 2 — Production, end-to-end HTTPS.** Pinned pre-built images; HTTPS on
every wire that leaves the host (browser→edge, OAM→instances); DB isolated on
an internal network; only the Caddy edge is exposed:
```bash
# 1. pin OAM_TAG / UI_TAG in .env to released versions (never `latest`)
#    (registry not reachable yet? build the images from source and tag them —
#     see docs/deployment-compose.md §5.1 "Building the production images from source")
# 2. edge cert — generate self-signed (or drop your CA's files in certs/edge/):
scripts/generate-edge-certs.sh oam.example.internal
#    then set in .env:
#      SITE_ADDRESS=https://oam.example.internal
#      EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem
# 3. verified TLS to managed instances ("TLS to managed LoxiLB instances" below):
#      OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
#      OAM_INSTANCE_TLS_INSECURE=false
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

Open the UI at `http(s)://<host>/netlox/`. Edge liveness: `/healthz`.

**Mode 3 — Converged single node.** Everything in Mode 2, plus the
`loxilb-inference-gateway` data plane on the *same* host (host network,
privileged, eBPF on the host NIC). The Gateway and shared PostgreSQL state run
as **separate Compose projects** so a management-plane `down` cannot drop live
inference traffic or its API-key store.

The state container keeps its OAM-facing `database` network internal. A second,
state-only bridge exists only because Docker requires a gateway-capable network
to realize the Gateway-facing `127.0.0.1` port publication; no other service
joins it.

One interactive command does the whole thing — secrets (including a persistent
random OAM-to-Gateway service token), certificates, `.env`, all three projects,
and authentication-aware verification:
```bash
scripts/init-converged.sh          # -y for defaults, --no-start to set up only
```
By hand:
```bash
# shared state — one PostgreSQL server, one loxioam database
docker compose -f docker-compose.database.yml up -d postgres
docker compose -f docker-compose.database.yml run --rm gateway-db-bootstrap
# data plane — deployed once, upgraded deliberately
docker compose -f docker-compose.dataplane.yml up -d
# management plane — upgraded freely
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml up -d
```
Linux only, and it needs its own `.env` section (`GW_HOST`, `EDGE_BIND_IP`,
`OAM_RESERVED_ENDPOINTS`, `IGW_TAG`, `CONVERGED_PG_HOST_PORT`). The approved
Gateway image is `ghcr.io/loxilb-io/loxilb-inference-gateway:latest-u24`; record
the resolved digest for reproducible test evidence. Read
[`docs/deployment-converged.md`](../../docs/deployment-converged.md) first —
co-locating the data plane changes the port, privilege and upgrade story.

**Developer variant — converged backend with local UI, HTTP only.** Keep the
shared PostgreSQL, Gateway, and OAM on the remote Linux testbed, but disable the
bundled UI and Caddy. OAM is published directly over plain HTTP:

```bash
# .env on the remote testbed
OAM_DEV_BIND_IP=0.0.0.0
OAM_DEV_HTTP_PORT=8080
LOCAL_UI_ORIGINS=http://localhost:3000,http://127.0.0.1:3000
LOCAL_UI_RESERVED_ENDPOINTS=:8080

# replace a running bundled management edge; state and data projects stay up
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml down
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml \
               -f docker-compose.converged-local-ui.yml \
               up -d --remove-orphans
```

Then set the local UI checkout's `.env.development` to the direct OAM route
(not the Caddy-only `/api/oam` alias) and start it:

```dotenv
REACT_APP_API_URL=http://<remote-testbed>:8080/oam
PORT=3000
HTTPS=false
```

This variant intentionally has no browser-to-OAM TLS. Credentials and JWTs
cross that link in clear text, so use it only on an access-controlled testbed,
VPN, or trusted development LAN. See the dedicated section in
[`docs/deployment-converged.md`](../../docs/deployment-converged.md).

## Configuration

One `.env` file drives everything; the same key names carry over to the k8s
ConfigMap/Secret. Full reference: `.env.example`. Highlights:

| Key | Purpose |
|-----|---------|
| `SITE_ADDRESS` / `EDGE_TLS` | edge listen address + TLS mode (see below) |
| `OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`, `SNAPSHOT_ENC_KEY` | OAM secrets |
| `DB_PASSWORD`, `DB_HOST` | database (set `DB_HOST` for an external DB) |
| `OAM_INSTANCE_CA_BUNDLE`, `OAM_INSTANCE_TLS_INSECURE` | TLS to managed LoxiLB instances |
| `OAM_TAG`, `UI_TAG` | pinned image versions (prod) |
| `CONVERGED_PG_HOST_PORT` | loopback-only PostgreSQL port used by the host-network Gateway |
| `AIGW_DB_PASSWORD_FILE` | mounted Gateway AI-store credential; never put its value in command arguments |
| `OAM_GATEWAY_AUTH_MODE` | `disabled` for generic/remote deployments; the converged overlay enforces `service-token` |
| `OAM_GATEWAY_SERVICE_TOKEN_FILE` | absolute in-container token path used by OAM; preferred over a raw environment value |
| `GATEWAY_SERVICE_TOKEN_FILE` | host-side token file mounted into both OAM and Gateway in converged mode |
| `OAM_DEV_BIND_IP`, `OAM_DEV_HTTP_PORT` | direct HTTP OAM publication for the local-UI developer variant |
| `LOCAL_UI_ORIGINS` | comma-separated local browser origins allowed by OAM CORS |

## Edge TLS modes

Set two knobs in `.env`. The ★ rows are the two first-class modes; the rest
are supported variants.

| Mode | `SITE_ADDRESS` | `EDGE_TLS` |
|------|----------------|-----------|
| **★ HTTP (dev mode)** | `:80` | *(empty)* |
| **★ Self-signed, your key (prod default)** | `https://your.host` | `tls /certs/edge/cert.pem /certs/edge/key.pem` |
| Self-signed, zero-file | `https://localhost` | `tls internal` |
| Automatic HTTPS (public DNS) | `your.domain` | *(empty)* |
| Official / commercial cert | `https://your.domain` | `tls /certs/edge/cert.pem /certs/edge/key.pem` |
| **No DNS name — reached by IP** | `https://203.0.113.10` | `tls /certs/edge/cert.pem /certs/edge/key.pem` (+ `EDGE_SNI_FALLBACK=default_sni 203.0.113.10`) |

Generate a self-signed edge cert:
```bash
scripts/generate-edge-certs.sh oam.local
# then set SITE_ADDRESS + EDGE_TLS as it prints, and trust cert.pem on clients
```

## TLS to managed LoxiLB instances

```bash
scripts/generate-instance-certs.sh 192.0.2.10 lb2.internal
```
Copy each `certs/instance-ca/<host>/server.{crt,key}` to that instance's
`/opt/loxilb/cert/`, start loxilb with `--tls`, then in `.env`:
```
OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
OAM_INSTANCE_TLS_INSECURE=false
```
Register each instance in the UI/API as `https://<host>:8091/netlox/v1`.

## Operations

```bash
docker compose ... ps                 # status
docker compose ... logs -f caddy      # edge logs
docker compose ... down               # stop (keep data)
docker compose ... down -v            # stop + destroy DB/volumes
```

In converged mode, run those commands against the intended project file. Never
use `down -v` for `docker-compose.database.yml`; its volume contains OAM state
plus Gateway API keys and quotas.

The converged token file is generated once at
`secrets/gateway_service_token` with mode `0600` and is never printed. Re-running
the initializer preserves it. Rotate it only as a coordinated data-plane and
management-plane restart: replace the file atomically, then recreate both
projects so neither side temporarily uses a different value.

> **Layout note:** this bundle lives in the `loxilb-oam` repo so the PostgreSQL schema
> (`../../database/init`) and the future k8s overlays stay single-sourced. The
> dev overlay builds only the OAM image, from this repository (`OAM_SRC`,
> default `../..`); the console image is always pulled — set `UI_IMAGE` /
> `UI_TAG` in `.env` to run a build of your own.

### Opt-in OAM runtime role separate from the database owner

The default database project's `POSTGRES_USER` is the PostgreSQL bootstrap
superuser. Do not copy that credential into a production OAM runtime as a
least-privilege claim. Keep the state project's owner credentials and existing
roles unchanged, migrate the schema as its owner, then provision a **new,
dedicated** OAM application role. The application starts in read-only schema
check mode; migrations remain a separately authorized owner operation.

From an authorized `psql` owner session against the migrated OAM database,
set `OAM_APP_DB_USER` and `OAM_APP_DB_PASSWORD` through a private environment
file or your secret manager. The password must not appear in shell history or
`psql -v` arguments. Run the following command from this directory:

```bash
psql -v ON_ERROR_STOP=1 -f database/oam-app-db-bootstrap.sql
```

The script reads those two environment variables. It grants DML on the current
named OAM tables and USAGE/SELECT on their owned sequences, plus SELECT on
`schema_migrations`. It grants no table ownership, schema CREATE, DBA role
membership or access to `aigw`/`aigw_mgmt`. Missing inputs, existing elevated or
owner/member roles, inherited public CREATE, and inherited migration/Gateway
access cause an error and transaction rollback. It does not demote existing
roles or revoke shared PUBLIC grants. Check grants before reusing an existing
application role; it must have no unrelated direct table/function privileges.

Keep the role/password in a separate mode0600 management environment file
(for example `private/oam-app.env`). Do not modify the state `.env` or run its
bootstrap jobs with the application credential. Add the overlay **last**:

```bash
docker compose --env-file .env --env-file private/oam-app.env \
  -f docker-compose.yml -f docker-compose.prod.yml \
  -f docker-compose.converged.yml -f docker-compose.oam-app-db.yml \
  up -d --no-deps --force-recreate oam-loxilb
```

Retain any deployment-specific overlays before the application-role overlay.
Require OAM readiness, same-session authenticated reads, login and an authorized
write. Verify the role flags and grants through an application connection:

```sql
SELECT current_user, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls
FROM pg_roles WHERE rolname = current_user;
SELECT has_schema_privilege(current_user, 'public', 'CREATE');
SELECT has_table_privilege(current_user, 'public.schema_migrations', 'UPDATE');
```

All five elevated flags and both privilege checks must be false. In a dedicated
test database, additionally require permission denials for CREATE ROLE,
CREATE DATABASE, a public-table CREATE, Gateway schema reads and migration
history UPDATE. A service health200 alone is insufficient. PostgreSQL transport
TLS remains independently configured; this overlay does not enable it.

For an upgrade, preserve backups, stop the OAM writer, apply reviewed migrations
with the owner credential, rerun the grant script for new named tables if the
release requires them, and restart in `check` mode. Do not select `off` to bypass
a pending schema. Roll back the runtime credential by omitting only this final
overlay and private environment file, retaining the other deployment overlays;
verify the same-session/configuration/traffic and log-volume boundaries again.
Do not delete roles, volumes or migration records as rollback.
