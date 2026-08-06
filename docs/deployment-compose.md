# LoxiLB Management Plane — Docker Compose Deployment Guide

This guide walks an operator through deploying the complete LoxiLB management
plane — the **loxilb-ui** web console, the **loxilb-oam** API, and its MySQL
database — on a single host with Docker Compose, behind a Caddy edge that
serves the UI, proxies the API, and terminates TLS.

```
browser ──HTTP/HTTPS──▶ caddy (edge, the only exposed service)
                          ├─ /            → static SPA (loxilb-ui)
                          └─ /api/oam/*   → oam-loxilb (API) ──▶ mysql
                                               └─ TLS ──▶ managed LoxiLB instances
```

The bundle supports **two first-class modes**:

| | Mode 1 — Development | Mode 2 — Production |
|---|---|---|
| Transport | end-to-end **HTTP** | end-to-end **HTTPS** (every wire that leaves the host) |
| Images | built from local source | pinned, signed release images from GHCR |
| DB / API exposure | host ports published for debugging | **none** — DB network is internal; only the edge is reachable |
| Instance TLS | verification off | verification **on** against your management CA |

Everything else (Caddy's internal CA, Let's Encrypt, commercial certificates)
is a supported variant of Mode 2, covered in [Edge TLS variants](#edge-tls-variants).

**Reference environment** (used for validation): Ubuntu 24.04 LTS, x86_64,
Docker Engine 24+ with the Compose plugin, single node.

---

## 1. Prerequisites

- A Linux host (x86_64) with **Docker Engine 24 or newer** and the
  **Docker Compose plugin** (`docker compose version` must work).
- Ports **80** and **443** free on the host (configurable via
  `HTTP_PORT`/`HTTPS_PORT`).
- At least **2 GB RAM** and **10 GB free disk** (Mode 1 source builds need
  more — roughly 5 GB extra for build layers).
- Outbound HTTPS to `ghcr.io` (Mode 2 image pulls) — not required in Mode 1.
- For **Mode 1** only: local checkouts of both repositories, side by side:
  ```
  <workdir>/
  ├── loxilb-oam/     # this repository
  └── loxilb-ui/      # the web console
  ```
  (A different layout works — set `OAM_SRC`/`UI_SRC` in `.env`.)

## 2. Get the bundle

```bash
git clone https://github.com/loxilb-io/loxilb-oam.git
# Mode 1 also needs the UI source as a sibling:
git clone https://github.com/loxilb-io/loxilb-ui.git

cd loxilb-oam/deploy/compose
```

All commands below run from `loxilb-oam/deploy/compose/`.

## 3. Configure `.env`

```bash
cp .env.example .env
```

Generate the required secrets and put them in `.env` — **the stack refuses to
start while any of these is empty**:

```bash
# JWT signing key
echo "OAM_JWT_SECRET=$(openssl rand -base64 48)"
# Snapshot encryption key (AES-256). Without it, instance snapshots —
# which contain IPsec PSKs and certificate private keys — are stored UNENCRYPTED.
echo "SNAPSHOT_ENC_KEY=$(openssl rand -base64 32)"
# Database passwords
echo "MYSQL_ROOT_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=')"
echo "DB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=')"
```

Set `OAM_DEFAULT_ADMIN_PASSWORD` to a strong bootstrap password for the
`admin` account — you will change it after first login (§7). It must satisfy
the account password policy: **at least 9 characters, containing an uppercase
letter, a lowercase letter, a digit and a special character; no character
three times in a row; not equal to the username**. For example:

```bash
echo "OAM_DEFAULT_ADMIN_PASSWORD=$(openssl rand -base64 12 | tr -d '/+=')A1!"
```

> **Important:** if the bootstrap password violates the policy, the API
> **refuses to start** on a fresh installation (the `oam-loxilb` container
> exits and restarts, logging `failed to set up the initial admin account`).
> Fix the password in `.env` and run `docker compose ... up -d` again; the
> admin account is created on the next start.

Key reference (full comments in `.env.example`):

| Key | Purpose |
|-----|---------|
| `OAM_JWT_SECRET` | JWT signing key (required) |
| `OAM_DEFAULT_ADMIN_PASSWORD` | bootstrap admin password (required) |
| `MYSQL_ROOT_PASSWORD`, `DB_PASSWORD` | database credentials (required) |
| `SNAPSHOT_ENC_KEY` | snapshot encryption at rest (strongly recommended) |
| `OAM_ALLOWED_ORIGINS` | CORS allowlist, e.g. `https://oam.example.internal` (production) |
| `SITE_ADDRESS`, `EDGE_TLS` | edge listen address + TLS mode (§4/§5) |
| `EDGE_SNI_FALLBACK` | site to serve clients that send no SNI — required only when the edge is reached by IP (see Troubleshooting) |
| `OAM_INSTANCE_CA_BUNDLE`, `OAM_INSTANCE_TLS_INSECURE` | TLS to managed LoxiLB instances (§6) |
| `OAM_TAG`, `UI_TAG` | pinned image versions (Mode 2) |
| `DB_HOST` | `mysql` = bundled DB; set a hostname to use an external database |

> Never commit your filled-in `.env`. It is gitignored by the bundle.

## 4. Mode 1 — Development (end-to-end HTTP)

Builds both images from your local checkouts and serves everything over plain
HTTP. `SITE_ADDRESS=:80` and empty `EDGE_TLS` are the shipped defaults, so no
edge configuration is needed.

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build -d
```

The first build takes several minutes (the UI is a full Node build). Then:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml ps
```

Expected steady state: `caddy`, `oam-loxilb`, `mysql` running (`oam-loxilb`
and `mysql` **healthy**), and `ui-assets` exited with code **0** (it is a
one-shot job that publishes the SPA build, then stops).

**Verify** (replace `localhost` with the host's address as needed):

```bash
curl -s  http://localhost/healthz                  # → ok            (edge alive)
curl -sI http://localhost/ | head -1               # → HTTP/1.1 302  (redirect to /netlox/)
curl -s  http://localhost/api/oam/health           # → {"status":"healthy"}
```

Open `http://<host>/netlox/` in a browser and log in as `admin` with
`OAM_DEFAULT_ADMIN_PASSWORD`.

Dev conveniences (dev overlay only): the API is also reachable directly at
`http://<host>:8080` and MySQL at `<host>:3306`; instance-certificate
verification is off (`OAM_INSTANCE_TLS_INSECURE=true`).

## 5. Mode 2 — Production (end-to-end HTTPS)

Uses pinned, pre-built release images; publishes only ports 80/443; places
MySQL on an isolated internal network; encrypts every wire that leaves the
host. Three steps: pin images → edge certificate → bring up.

### 5.1 Pin the release images

In `.env`, set explicit release versions (the prod overlay fails fast when
they are unset; always pin immutable versions — never use `latest` in
production, or upgrades become untraceable):

```bash
OAM_TAG=v0.1.0        # use the latest release of each repo
UI_TAG=v0.9.0
```

Releases are published to `ghcr.io/loxilb-io/loxilb-oam` and
`ghcr.io/loxilb-io/loxilb-ui`, Cosign-signed with SLSA provenance and SBOM
attestations. If your host cannot pull them anonymously (registry not public
yet, or an air-gapped mirror), either authenticate —
`docker login ghcr.io` — or load the images out-of-band and keep the same
tags.

### 5.2 Edge certificate (browser → UI/API)

**Default path — self-signed, your own key.** Works on private networks with
no public DNS:

```bash
scripts/generate-edge-certs.sh oam.example.internal
```

This writes `certs/edge/{cert,key}.pem` with the SANs
`DNS:oam.example.internal, DNS:localhost, IP:127.0.0.1` (pass a second
argument to customize — every name/IP clients will type **must** be in the
SAN list, or browsers reject the certificate). Then set in `.env`:

```bash
SITE_ADDRESS=https://oam.example.internal
EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem
```

To silence browser warnings, import `certs/edge/cert.pem` into each client's
trust store:

- **macOS**: `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain cert.pem`
- **Linux**: `sudo cp cert.pem /usr/local/share/ca-certificates/loxilb-edge.crt && sudo update-ca-certificates`
- **Windows**: `certutil -addstore -f Root cert.pem` (administrator prompt)

For a CA-issued certificate instead, skip the script and drop your CA's files
into `certs/edge/` (concatenate leaf + intermediates into `cert.pem`); the
same two `.env` values apply. Other variants: [Edge TLS variants](#edge-tls-variants).

### 5.3 Bring up and verify

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
```

**Verification checklist** (all should hold before you hand the system to
users; `-k` is needed while clients haven't trusted a self-signed cert yet,
`--resolve` substitutes for DNS if the hostname isn't resolvable yet):

```bash
H=oam.example.internal; IP=<host-ip>

# 1. HTTPS serves the console and API
curl -sk  --resolve $H:443:$IP https://$H/healthz               # → ok
curl -skI --resolve $H:443:$IP https://$H/ | head -1            # → 302 (to /netlox/)
curl -sk  --resolve $H:443:$IP https://$H/api/oam/health        # → {"status":"healthy"}

# 2. Plain HTTP is not served — it redirects to HTTPS
curl -sI  --resolve $H:80:$IP  http://$H/ | head -1             # → HTTP/1.1 308

# 3. The edge presents YOUR certificate
openssl s_client -connect $IP:443 -servername $H </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -ext subjectAltName

# 4. Neither the API nor the database is reachable from outside
curl -s -m 3 http://$IP:8080/ || echo "8080 closed (expected)"
nc -z -w 3 $IP 3306          || echo "3306 closed (expected)"
```

Log in at `https://<hostname>/netlox/` and proceed to §6 and §7.

## 6. TLS to managed LoxiLB instances

The OAM service talks to every LoxiLB gateway instance it manages (API
proxying and configuration snapshots). In production this traffic crosses the
network and **must** be TLS with verification on. The full guide — including
the certificate model, rotation, gateway container/env configuration, the
Kubernetes / cert-manager mapping for the next phase, and troubleshooting —
is **[instance-tls.md](instance-tls.md)**; the essential steps are below.
The bundle ships a helper that creates a private management CA and
per-instance server certificates:

```bash
scripts/generate-instance-certs.sh 192.0.2.10 lb2.example.internal
```

For each instance:

1. Copy `certs/instance-ca/<host>/server.crt` and `server.key` to the
   instance's `/opt/loxilb/cert/` directory.
2. Start loxilb with TLS enabled:
   ```bash
   loxilb --tls --tls-host=0.0.0.0 --tls-port=8091 \
          --tls-certificate=/opt/loxilb/cert/server.crt \
          --tls-key=/opt/loxilb/cert/server.key
   ```
3. Register the instance in the UI (or API) with the endpoint
   `https://<host>:8091/netlox/v1`.

Then point OAM at the CA in `.env` (the `certs/` directory is already mounted
into the OAM container) and re-run `up -d`:

```bash
OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
OAM_INSTANCE_TLS_INSECURE=false
```

Certificates from a public CA also work with **no** `OAM_INSTANCE_CA_BUNDLE`
(system roots are trusted by default). Keep `ca.key` (under
`certs/instance-ca/`) offline and out of the deployment host if you can.

## 7. First login and hardening

1. Log in as `admin` / `OAM_DEFAULT_ADMIN_PASSWORD`.
2. **Change the admin password immediately** (Users → admin).
3. Create per-operator accounts with appropriate roles; don't share `admin`.
4. Set `OAM_ALLOWED_ORIGINS` in `.env` to the exact origin operators use
   (e.g. `https://oam.example.internal`) — unset falls back to a wildcard,
   which is acceptable only in Mode 1.
5. Confirm `SNAPSHOT_ENC_KEY` is set (§3) before taking instance snapshots.

## 8. Operations

All commands take the same `-f docker-compose.yml -f docker-compose.<mode>.yml`
pair used at `up` time; it is omitted below for brevity.

```bash
docker compose ... ps                      # service status
docker compose ... logs -f caddy           # edge access/error log
docker compose ... logs -f oam-loxilb      # API log
docker compose ... down                    # stop, KEEP data
docker compose ... down -v                 # stop and DESTROY database + volumes
```

**Upgrade (Mode 2).** Edit `OAM_TAG`/`UI_TAG` in `.env` to the new release,
then:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

The `ui-assets` one-shot republishes the new SPA build automatically on every
`up`. Database schema is initialized only on first boot of an empty volume;
release notes will call out migrations when they exist.

**Backup.** The database is the only stateful component that matters
(`caddy_data` only caches ACME material, `ui_build` is rebuilt on every `up`):

```bash
docker compose ... exec mysql sh -c \
  'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction loxioam' \
  > oam-backup-$(date +%F).sql
```

Also keep safe copies of `.env` and the `certs/` directory (they are exactly
what you need to rebuild the host from scratch).

**Restore** (into a fresh stack after `down -v` + `up -d`):

```bash
docker compose ... exec -T mysql sh -c \
  'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" loxioam' < oam-backup-YYYY-MM-DD.sql
```

**Certificate renewal (self-signed / BYO).** Replace the two files in
`certs/edge/` and restart the edge:
`docker compose ... up -d --force-recreate caddy`. (ACME mode renews itself.)

## Edge TLS variants

All variants are the same two `.env` knobs; only the values differ.

| Variant | `SITE_ADDRESS` | `EDGE_TLS` | Notes |
|---------|----------------|-----------|-------|
| Caddy internal CA (zero-file dev HTTPS) | `https://localhost` | `tls internal` | Caddy generates its own local CA |
| Let's Encrypt / ACME | `oam.example.com` | *(empty)* | needs public DNS + inbound 80/443; certs persist in the `caddy_data` volume and renew automatically |
| Commercial / organizational CA | `https://oam.example.com` | `tls /certs/edge/cert.pem /certs/edge/key.pem` | identical mechanics to self-signed — only the signer differs |

## Troubleshooting

| Symptom | Cause / fix |
|---------|-------------|
| Browser shows a certificate warning | Self-signed cert not yet trusted on the client — import `certs/edge/cert.pem` (§5.2), and check the URL's hostname is in the cert SANs. |
| HTTPS by IP fails outright (`curl` exit 35, browser "cannot establish a secure connection") while the hostname works | SNI cannot carry an IP literal, so those clients send none, and Caddy selects its TLS policy by SNI — an SNI-less handshake matches no site and is rejected with alert 80 before any HTTP. Listing the IP in `SITE_ADDRESS` does **not** help. Set `EDGE_SNI_FALLBACK=default_sni <your.host>` in `.env` and restart the edge (`docker compose restart caddy`). |
| `up` fails with `set OAM_JWT_SECRET in .env` (or similar) | A required secret is empty in `.env` — see §3. This is fail-fast by design. |
| `ui-assets` exits non-zero / UI shows Caddy 404 | The SPA was not published. Check `docker compose ... logs ui-assets`; re-run `up -d`. Ensure the bundle is at a version that includes the ui-assets `entrypoint` override. |
| `Error response from daemon: … unauthorized` pulling images | The GHCR package isn't public from your network — `docker login ghcr.io` with a token that has `read:packages`, or mirror the images. |
| Port 80/443 already in use | Another web server on the host — stop it, or set `HTTP_PORT`/`HTTPS_PORT` in `.env` and re-run `up -d`. |
| `mysql` unhealthy right after first `up` | First-boot initialization can take ~40 s (`start_period`); `oam-loxilb` waits for it. Only investigate if it stays unhealthy after a minute: `docker compose ... logs mysql`. |
| `oam-loxilb` exits / keeps restarting on a fresh install | `OAM_DEFAULT_ADMIN_PASSWORD` violated the password policy, so the initial admin account could not be created and the API refuses to run — the log shows `failed to set up the initial admin account`. Set a compliant password (§3) and run `up -d` again. (Older releases instead started with **no** admin account, making every login return 401 — same cause, same fix.) |
| Login returns HTTP 500 immediately after a previous login | Known upstream issue: two logins by the same user within the same minute can collide on token storage. Wait a minute and retry; the first token is valid. |
| Instance shows unreachable after enabling TLS | On the instance: loxilb running with `--tls` and the cert/key in place? Endpoint registered as `https://<host>:8091/netlox/v1`? On OAM: `OAM_INSTANCE_CA_BUNDLE` pointing at the CA that signed the instance cert, cert SAN matching the registered host? Errors appear in `docker compose ... logs oam-loxilb`. |
| Need to reset the admin password | See [ADMIN_RESET_QUICK_GUIDE.md](ADMIN_RESET_QUICK_GUIDE.md). |

## Related documents

- [`deploy/compose/README.md`](../deploy/compose/README.md) — quick start and
  file layout of the bundle.
- [`DEPLOYMENT.md`](../DEPLOYMENT.md) — OAM configuration reference (all
  environment variables and CLI flags) and standalone/Kubernetes deployment.
- [`SECURITY.md`](../SECURITY.md) — vulnerability reporting.
