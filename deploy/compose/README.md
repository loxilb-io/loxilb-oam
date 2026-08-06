# LoxiLB management-plane bundle (Docker Compose, single node)

Runs the whole management plane on one host:

```
browser ──HTTP/HTTPS──▶ caddy (edge)
                          ├─ /            → static SPA (loxilb-ui)
                          └─ /api/oam/*   → oam-loxilb (API) ──▶ mysql
                                               └─ TLS ──▶ managed LoxiLB instances
```

Three long-running services — **caddy**, **oam-loxilb**, **mysql** — plus a
one-shot **ui-assets** job that publishes the SPA build into the volume Caddy
serves. **Full step-by-step operator guide:**
[`docs/deployment-compose.md`](../../docs/deployment-compose.md).

## Quick start

```bash
cp .env.example .env      # fill in the required secrets
# edit .env: MYSQL_ROOT_PASSWORD, DB_PASSWORD, OAM_JWT_SECRET,
#            OAM_DEFAULT_ADMIN_PASSWORD, SNAPSHOT_ENC_KEY
```

The bundle has **two first-class modes** — everything else is a variant
(see "Edge TLS modes" below).

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

## Configuration

One `.env` file drives everything; the same key names carry over to the k8s
ConfigMap/Secret. Full reference: `.env.example`. Highlights:

| Key | Purpose |
|-----|---------|
| `SITE_ADDRESS` / `EDGE_TLS` | edge listen address + TLS mode (see below) |
| `OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`, `SNAPSHOT_ENC_KEY` | OAM secrets |
| `MYSQL_ROOT_PASSWORD`, `DB_PASSWORD`, `DB_HOST` | database (set `DB_HOST` for an external DB) |
| `OAM_INSTANCE_CA_BUNDLE`, `OAM_INSTANCE_TLS_INSECURE` | TLS to managed LoxiLB instances |
| `OAM_TAG`, `UI_TAG` | pinned image versions (prod) |

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

> **Layout note:** this bundle lives in the `loxilb-oam` repo so the MySQL schema
> (`../../database/init`) and the future k8s overlays stay single-sourced. The
> dev overlay builds only the OAM image, from this repository (`OAM_SRC`,
> default `../..`); the console image is always pulled — set `UI_IMAGE` /
> `UI_TAG` in `.env` to run a build of your own.
