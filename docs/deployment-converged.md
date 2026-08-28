# Converged single-node deployment

Shared state (one PostgreSQL server), management plane (OAM + console), and
data plane (`loxilb-inference-gateway`) on **one host**.

```
                       host network namespace — privileged, eBPF on the host NIC
 inference clients ──▶ loxilb-inference-gateway
   (:80/:443, VIPs)      L7 fullproxy binds VIPs · L4 modes run in eBPF at TC ingress
                         :11111 plaintext API  → bound to 127.0.0.1
                         :8091  verified-TLS API → OAM's only way in
                                ▲                         │
                                │ https, CA-verified      │ PostgreSQL over
                                │ over the Docker bridge  │ 127.0.0.1 only
  browser ──:8443──▶ caddy ──▶ oam-loxilb ──┘
                       │  frontend (bridge)  │
                       └ SPA volume          └─▶ postgres ◀─┘
                                                database network (internal)
```

PostgreSQL also joins a state-project-only bridge so Docker can implement the
loopback host publication used by the host-network Gateway. Docker Engine does
not activate published ports for a container attached only to an internal
network. No application joins that auxiliary bridge, and the published address
remains `127.0.0.1`.

**Use this when** one node runs both the AI gateway and its own management
console. **Do not use it** when that OAM instance also manages *other* gateways
— see [§5](#5-what-you-are-accepting).

Linux only. `network_mode: host` is a no-op on Docker Desktop: the stack comes
up "healthy" with a gateway that sees no host NICs.

---

## 1. Three projects, one host, one PostgreSQL database

Converged means **co-located, not co-managed**. PostgreSQL, the gateway, and
the management plane each have an independent Compose lifecycle:

```
deploy/compose/
├── docker-compose.yml            # base (unchanged)
├── docker-compose.prod.yml       # prod overlay (unchanged)
├── docker-compose.converged.yml  # management-side wiring for a local gateway
├── docker-compose.converged-local-ui.yml # dev: direct HTTP OAM, no bundled UI/edge
├── docker-compose.database.yml   # shared state (project: loxilb-state)
├── docker-compose.dataplane.yml  # the gateway (project: loxilb-data)
├── database/
│   └── aigw-db-bootstrap.sql     # reviewed Gateway bootstrap snapshot
├── secrets/                      # ignored 0600 Gateway DB password files
└── .env                          # shared by all three projects
```

The gateway's compose file deliberately sits **beside** `.env`, not in a
subdirectory. Compose resolves `.env` relative to the compose file's own
directory, so a subdirectory copy needs `--env-file ../.env` on every single
invocation — and the one people forget is `down`, which then fails with
`required variable GW_HOST is missing a value` exactly when they want it to
work. What separates the projects is each file's `name:` value, not the
directory it lives in: management is `loxilb-mgmt`, data is `loxilb-data`, and
shared state is `loxilb-state`.

Compose has no way to exempt a service from `down`. A routine management
upgrade must not tear down either the eBPF datapath or the database both planes
use. Splitting the projects provides that boundary:

```bash
cd deploy/compose

# shared state — start first; never use `down -v` during routine maintenance
docker compose -f docker-compose.database.yml up -d postgres
docker compose -f docker-compose.database.yml run --rm gateway-db-bootstrap

# data plane — starts after database bootstrap; upgraded deliberately
docker compose -f docker-compose.dataplane.yml up -d

# management plane — upgraded freely, never touches traffic
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml up -d
```

### Developer variant: remote converged backend, local UI over HTTP

Use `docker-compose.converged-local-ui.yml` when the React development server
runs on a developer workstation while PostgreSQL, the Gateway, and OAM remain
on the Linux testbed:

```
browser ── http://localhost:3000 ──▶ local loxilb-ui dev server
                  │
                  └── HTTP + CORS ──▶ remote-host:8080/oam ──▶ OAM
                                                              ├─▶ PostgreSQL
                                                              └─▶ local Gateway
```

The final overlay publishes `oam-loxilb:8080` directly and puts inherited
`ui-assets` and `caddy` services behind inactive profiles. No Caddy container,
edge certificate, `/netlox/` route, or `/api/oam` rewrite exists in this mode.

Set these values in the remote `deploy/compose/.env`:

```dotenv
OAM_DEV_BIND_IP=0.0.0.0
OAM_DEV_HTTP_PORT=8080
LOCAL_UI_ORIGINS=http://localhost:3000,http://127.0.0.1:3000
LOCAL_UI_RESERVED_ENDPOINTS=:8080
```

`LOCAL_UI_ORIGINS` contains browser origins, not URLs: do not add `/netlox`,
`/oam`, or a trailing slash. Add the workstation's actual origin if it uses a
different hostname or port. `:8080` reserves the direct management port on
every inference VIP; it can be narrowed to comma-separated
`address:port[/protocol]` entries if required.

Switch only the management project; the independent state and data projects
remain running:

```bash
cd deploy/compose
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml down
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml \
               -f docker-compose.converged-local-ui.yml \
               up -d --remove-orphans
```

In the local `loxilb-ui` checkout, development mode calls OAM directly. The URL
must end in `/oam`; `/api/oam` exists only behind the omitted Caddy edge:

```dotenv
REACT_APP_API_URL=http://<remote-testbed>:8080/oam
REACT_APP_ENV=local
REACT_APP_PUBLIC_URL=/netlox
PORT=3000
HTTPS=false
```

Run `npm start`, open `http://localhost:3000/netlox/`, and verify the remote
contract before login:

```bash
curl http://<remote-testbed>:8080/oam/health
curl -i -X OPTIONS \
  -H 'Origin: http://localhost:3000' \
  -H 'Access-Control-Request-Method: GET' \
  http://<remote-testbed>:8080/oam/setup/status
```

The preflight response must include
`Access-Control-Allow-Origin: http://localhost:3000`. A different origin gets
no allow-origin header.

> **Development only:** this path deliberately has no transport encryption.
> Login credentials, JWTs, and API responses are visible to anyone able to
> observe the link. Restrict port 8080 to the developer workstation with the
> testbed firewall/security group or use a trusted VPN. Use the normal
> Caddy/TLS converged deployment for shared, staging, or production systems.

## 2. Setup

### The quick way

```bash
deploy/compose/scripts/init-converged.sh
```

Interactive. It checks the prerequisites (Linux, Compose 2.24+ for `!override`),
shows the host's real addresses and picks the port profile from how many there
are, generates every secret and certificate, creates the snapshot directory,
writes a `0600` `.env`, starts all three projects in database → data →
management order, verifies the schema/role/network/runtime contracts, and
optionally registers the local gateway.

Re-running is safe: it offers to keep an existing `.env` and reuses existing
certificates. `-y` accepts every default; `--no-start` sets up without bringing
anything up.

It refuses to guess about the decisions that matter — which address the console
binds, what operators type in the browser, and **every other name or address
they may also use** — because those set the certificate identity, the site
matching and the reserved endpoint. List a NATed public address there too: the
host cannot discover it, and a client arriving on an address missing from the
certificate gets a hard TLS failure.

It also refuses to generate fresh OAM credentials when a PostgreSQL volume
already exists, offering to reuse the old credentials or explicitly delete the
volume — see "existing database" in the troubleshooting table. A legacy
`loxilb-mgmt_postgres_data` volume is adopted in place as the shared-state
volume; it is not copied or silently deleted.

**Re-running keeps your answers.** Every prompt defaults to the value already in
`.env`, so pressing Enter reproduces the deployment you have. This matters most
for `GW_HOST`: it is the gateway certificate's SAN *and* the name OAM pins and
registers, so changing it makes the gateway serve a certificate for the new name
while any existing registration still points at the old one — which shows up as
**Health Status: Down** in the console. The script warns before letting it
change, and re-points the registration afterwards.

**The gateway is registered during bootstrap, not after.** Registration is
create-or-update: it adds `local-gateway` if absent, and if it exists with a
stale host it re-points it. This has to happen while the script runs, because
`OAM_DEFAULT_ADMIN_PASSWORD` is valid only against a fresh database and only
until an operator changes the admin password — which the closing summary tells
them to do. If login fails, the script offers to run `reset_admin` and says
exactly what to add by hand. The rest of this section is
what the script does, for when you would rather do it by hand.

### By hand

**1. Database credentials.** The gateway reads its AI-key-store password from a
Compose secret file; the management-store role is provisioned for future use
but deliberately not enabled in converged mode. Keep both files out of Git:

```bash
install -d -m 0700 secrets
openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > secrets/aigw_db_password
openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > secrets/aigw_mgmt_db_password
chmod 0600 secrets/aigw_db_password secrets/aigw_mgmt_db_password
```

**2. Host paths.** The gateway's config snapshot must survive a container
recreate, or configuration is lost on every image upgrade:

```bash
sudo mkdir -p /opt/loxilb/config
```

**3. Certificates.** One command issues the management CA and the gateway's
server certificate. On a converged node the output directory mounts straight
into the gateway — nothing is copied between hosts:

```bash
scripts/generate-instance-certs.sh gw.example.internal   # = GW_HOST
# → certs/instance-ca.pem                       (what OAM trusts)
# → certs/instance-ca/gw.example.internal/server.{crt,key}  (mounted at /opt/loxilb/cert)
```

**4. `.env`.** See the "Converged single-node mode" section of `.env.example`.
Minimum:

```bash
GW_HOST=gw.example.internal
EDGE_BIND_IP=192.168.0.8          # the management address
EDGE_HTTPS_PORT=8443
OAM_RESERVED_ENDPOINTS=192.168.0.8:8443
IGW_TAG=latest-u24                # approved integration image; record its digest
CONVERGED_PG_HOST_PORT=5432       # published on 127.0.0.1 only
CONVERGED_DB_NETWORK=loxilb-converged-db
CONVERGED_PG_VOLUME=loxilb-state-postgres-data
AIGW_DB_PASSWORD_FILE=./secrets/aigw_db_password
AIGW_MGMT_DB_PASSWORD_FILE=./secrets/aigw_mgmt_db_password
SITE_ADDRESS=https://gw.example.internal:8443   # MUST carry the port
EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem   # see "ACME renewal" below
OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
OAM_INSTANCE_TLS_INSECURE=false
```

`latest-u24` is the approved image for this integration cycle. Record the
resolved repository digest in test evidence; replace it with an immutable
release tag or digest for production promotion.

**5. Start the database and bootstrap Gateway roles before applications.** One
PostgreSQL database (default `loxioam`) contains three isolated schemas:
`public` for OAM, `aigw` for AI API keys and tenant quotas, and dormant
`aigw_mgmt` for Gateway users/session tokens.

```bash
docker compose -f docker-compose.database.yml up -d postgres
docker compose -f docker-compose.database.yml run --rm gateway-db-bootstrap
docker compose -f docker-compose.dataplane.yml up -d
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
               -f docker-compose.converged.yml up -d
```

The bootstrap is idempotent and is the password-rotation path. The Gateway uses
only `aigw`; do **not** add `--userservice` merely because `aigw_mgmt` exists.
OAM forwards its own JWT in `Authorization`, which is a different identity
plane from Gateway management tokens.

**6. Register the gateway** in the console as
`https://${GW_HOST}:8091/netlox/v1` — host `gw.example.internal`, port `8091`,
protocol `https`.

## 3. Addressing: why `GW_HOST` and not a container name

The gateway is in the host network namespace, so it has **no name on any compose
network** — `https://loxilb-gw:8091` can never resolve. The overlay instead pins
`GW_HOST` to the Docker host-gateway address:

```yaml
extra_hosts:
  - "${GW_HOST}:host-gateway"      # → 172.17.0.1 gw.example.internal in /etc/hosts
```

The instance is therefore registered exactly as a *remote* gateway would be, the
certificate needs no Docker-specific SAN, and the call never leaves the bridge.
Verified on a live node: from a container on the compose bridge,
`https://gw.example.internal:8091` returns **200 with `ssl_verify_result 0`**,
and the same call **fails** without `--cacert` — verification is genuinely on.

> Do not use a bare IP for `GW_HOST`. Go resolves an IP literal without
> consulting `/etc/hosts`, so the pin is ignored and the call hairpins out
> through the host NIC.

## 4. Port ownership — the part that bites

On a converged node the data plane wants `:80`/`:443` for inference VIPs, so the
management edge moves aside. The overlay publishes **HTTPS only, on one
address**:

```yaml
ports: !override
  - "${EDGE_BIND_IP}:${EDGE_HTTPS_PORT}:${EDGE_HTTPS_PORT}"
```

Two things about that line are load-bearing:

- **`!override` is required.** Compose *merges* `ports` across overlays. Without
  it the base file's `80:80` and `443:443` are published *in addition*,
  recreating the collision this overlay exists to prevent.
- **Host and container port are identical** (no `8443:443` remap) so
  `SITE_ADDRESS` can name the port Caddy actually listens on. If `SITE_ADDRESS`
  omits the port, Caddy's site block will not match the `Host` header the
  browser sends.

Verified on a live node: the only listener is
`LISTEN 192.168.0.8:8443` — `:80` and `:443` are free on every address.

### Converged port-split breaks ACME renewal

Giving `:80`/`:443` to the data plane also takes away the ports Caddy needs to
answer an ACME challenge. HTTP-01 needs `:80`; TLS-ALPN-01 needs `:443`. Neither
is published in converged mode, so **a Let's Encrypt certificate will serve
until it expires and then fail to renew** — silently, months after the change.

An existing cached certificate keeps working, which is exactly what makes this
easy to miss: the console is fine today and breaks one renewal cycle later.
Pick one deliberately:

| Option | Cost |
|---|---|
| **Operator-supplied cert** (`EDGE_TLS=tls /certs/edge/cert.pem ...`) | You own renewal. Certificate SANs must list every name and IP operators use — a name missing from the SAN is a hard browser error, not a warning. |
| **Keep `:443` for the edge**, VIPs on a secondary address | ACME keeps working. Needs a spare IP (see below) — the recommended profile where one is available. |
| **DNS-01 challenge** | Works with no inbound ports, but the stock `caddy:2-alpine` image has no DNS provider plugin; you must build a custom Caddy. |

If you are on a self-signed edge certificate, regenerate it with every name and
address in the SAN list:

```bash
scripts/generate-edge-certs.sh oam.example.internal \
  "DNS:oam.example.internal,DNS:localhost,IP:127.0.0.1,IP:192.168.0.8"
```

### When the node has no DNS name

Reaching the console by address only — no DNS anywhere — is supported, and on an
air-gapped or on-prem node it is the normal case. Three things must line up, and
missing any one of them presents as a bare TLS failure:

```bash
scripts/generate-edge-certs.sh 203.0.113.10       # 1. cert with an IP: SAN
# .env:
SITE_ADDRESS=https://203.0.113.10:8443
EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem   # 2. not ACME
EDGE_SNI_FALLBACK=default_sni 203.0.113.10              # 3. no SNI for an IP
```

1. **`IP:` SAN, never `DNS:`.** A dotted quad in a `DNS:` entry matches nothing
   and the browser rejects the certificate. `generate-edge-certs.sh` now emits
   `IP:` automatically when its first argument is an address.
2. **A certificate file, not ACME.** No public CA issues for a private address,
   and the converged port-split has already removed the challenge ports anyway.
3. **`EDGE_SNI_FALLBACK` is mandatory, not optional.** TLS SNI has no
   representation for an IP literal, so these clients send none; without a
   fallback the handshake dies with alert 80 before any HTTP is spoken — which
   presents as "the port is closed", not as a certificate problem. An IP is a
   valid `default_sni` value (verified on a live node).

Verified end-to-end against a live converged node reached only by address:
SNI-less handshake accepted, `ssl_verify_result 0` against the IP SAN, and
`/netlox/` plus `/api/oam/*` both `200`.

**Browsers will still warn**, because the certificate is self-signed — the IP
SAN makes the *name* correct, not the *issuer* trusted. Fix it once, per client:

- trust `certs/edge/cert.pem` on each admin machine (the script prints the
  macOS/Linux commands), or
- issue the edge certificate from the same private CA that
  `generate-instance-certs.sh` creates, and distribute that one root instead —
  then every node's console is trusted, IP-only nodes included.

> One knob, one policy: the Caddyfile applies a single `EDGE_TLS` to every
> address in `SITE_ADDRESS`. You cannot serve an ACME certificate for a hostname
> *and* a self-signed one for an IP from the same edge. Pick the one the
> operators actually type.

### Single NIC vs. single IP

"Single NIC" is not "single IP". loxilb VIPs are normally floating addresses, so
on a one-NIC host you add a secondary address for VIPs and keep the primary for
management. Only if the host truly has one usable address do you need the
port-split above — and there the data plane should win `:443`.

### Why a collision is dangerous rather than merely annoying

It does **not** fail cleanly, and the two LB modes fail differently:

| Mode | What happens on a collision |
|---|---|
| L7 fullproxy (`mode: 4`) | The gateway binds a real socket. Whether it collides depends on whether both listeners set `SO_REUSEADDR` — do not build on it. |
| L4 (`mode: 0/1/2`) | **No bind at all.** The packet is handled in eBPF at TC ingress, ahead of netfilter and Docker's DNAT. |

The L4 case was reproduced on a live node. With an L4 rule on the edge's own
`address:port`:

- the gateway accepted the rule with **HTTP 200** — no conflict reported;
- the console became **completely unreachable from off-host** (3/3 probes `000`);
- **Caddy kept running, still bound, and logged nothing**;
- deleting the rule restored the console immediately, with no restart.

**A probe from the host itself still returned 200 the whole time.** Host-
originated traffic to the host's own address never enters through the NIC's TC
ingress hook, so it bypasses the interception entirely. An operator testing
`curl localhost` from the box will conclude everything is fine while every real
user is locked out. Always verify from off-host.

### The guard

`OAM_RESERVED_ENDPOINTS` makes OAM refuse such a rule with **409 Conflict**
before it reaches the gateway:

```bash
OAM_RESERVED_ENDPOINTS=192.168.0.8:8443        # ip:port[/proto], comma-separated
```

Set it to whatever the edge binds. Details:

- An omitted or wildcard address (`:8443`, `0.0.0.0:8443`) reserves the port on
  every address; an omitted protocol reserves both TCP and UDP.
- A rule with a **wildcard VIP** (`0.0.0.0`) is refused against *any*
  reservation on that port — it would capture the reserved address too.
- Both `externalIP` and the L7 `host` field are checked, so a rule cannot slip
  through by naming the edge in only one of them.
- A malformed value **aborts startup**. A guard that silently failed to parse is
  worse than no guard, because the operator believes the console is protected.

**Its limit:** it covers rules created *through OAM*, which is the path the
console and every scripted client use. A caller with direct network access to
the gateway's own REST API can still program the rule. That is why the gateway's
plaintext `:11111` is pinned to loopback (below) — keep `:8091` reachable only
by OAM and this is closed.

#### Open follow-up: gateway-side enforcement

Pinning `:11111` to loopback and restricting `:8091` closes the bypass by
*network reachability*, not by *admission*. Anything that can reach the
gateway's REST API — a host-local process, an operator running `loxicmd` on the
box, a future component given access to `:8091` — can still program a rule that
takes over the management endpoint, and OAM will never see the request.

Closing it properly requires the check to live in the gateway, in
`loxilb-inference-gateway`. The request, stated so it can be picked up
directly:

> Add a `--reserved-endpoints` flag (env `RESERVED_ENDPOINTS`) accepting a
> comma-separated `ip:port[/proto]` list — the same syntax as OAM's
> `OAM_RESERVED_ENDPOINTS`, so one value can be configured from one place.
> Validate it in the load-balancer create/update handler and reject a rule
> whose `serviceArguments.externalIP` **or** `host`, together with `port` and
> `protocol`, matches a reserved entry. Return `400` naming the offending
> endpoint. Treat a wildcard on either side as a match: a reservation with no
> address covers its port on every address, and a rule with a `0.0.0.0` VIP
> captures the reserved address too. A malformed value should abort startup
> rather than leave the check silently inert.

Two smaller asks worth bundling with it:

- **`--blacklist` should default to excluding Docker's interfaces.** Today it
  defaults to `"none"`, and `NlpIsBlackListedIntf` excludes only `lo`, so an
  unconfigured gateway attaches to `docker0`, every `br-*` and every `veth*`
  (see [§6](#6-keeping-the-datapath-off-dockers-interfaces)). Every containerised
  deployment has to know to override it.
- **Check whether `--privileged` can be dropped** in favour of
  `NET_ADMIN + SYS_ADMIN + BPF + PERFMON + SYS_RESOURCE`. If it holds, that is
  the single highest-value hardening available to a converged node, where the
  gateway's privileges and the management credentials share a host
  ([§5](#5-what-you-are-accepting)).

Until the gateway enforces this, treat the OAM guard as covering operator
mistakes rather than a determined caller, and keep `:8091` reachable only from
the OAM bridge.

## 5. What you are accepting

The gateway runs `--privileged` with `SYS_ADMIN` in the host network namespace.
That is root on the host. It is also the internet-facing component parsing
untrusted HTTP/SSE. **A gateway compromise is a host compromise, and the host
holds the management credentials** — `OAM_JWT_SECRET`, the admin password,
`DB_PASSWORD`, `SNAPSHOT_ENC_KEY`.

On a **single-node** deployment this is a bounded trade: OAM's authority does not
extend past the node the attacker already owns. **It stops being bounded the
moment that OAM also manages remote gateways** — one data-plane compromise then
reaches the whole fleet. Converged mode assumes local-only management.

Reduce what is left:

- Keep `.env` `0600` and root-owned.
- Do not reuse `OAM_JWT_SECRET` / `DB_PASSWORD` on any other node.
- Keep the console off the public interface where you can (bind `EDGE_BIND_IP`
  to a management address, or reach it over VPN).

Hardening still worth testing on your kernel: whether `--privileged` can be
replaced by `NET_ADMIN + SYS_ADMIN + BPF + PERFMON + SYS_RESOURCE`.

## 6. Keeping the datapath off Docker's interfaces

`NlpIsBlackListedIntf` (`api/loxinlp/nlp.go`) excludes only `lo` by default, so
an unconfigured gateway attaches to every interface it discovers. The dataplane
compose file therefore ships:

```yaml
command: ["--blacklist=^docker0$$|^br-|^veth"]   # $$ escapes compose interpolation
```

Measured on a live converged node with both compose bridges and four veths up:

| | interfaces loxilb took | tc filters |
|---|---|---|
| **without** `--blacklist` | `enp0s3`, `docker0`, both `br-*`, **all 4 `veth*`** | **2 per veth** |
| **with** `--blacklist` | `enp0s3` + loxilb's own `llb0`/`vlan*` only | 0 everywhere |

Unconfigured, the datapath really does sit on the wires carrying Caddy→OAM,
OAM→PostgreSQL and OAM→gateway traffic.

> **Verify on the veths, not the bridge.** The filters attach to the veth, which
> is the actual packet path — `tc filter show dev docker0` reports nothing even
> when the datapath *is* attached, and is falsely reassuring:
> ```bash
> for i in $(ip -br link | grep -oE '^veth[a-z0-9]+'); do
>   echo "$i: $(sudo tc filter show dev $i ingress | wc -l)"
> done
> ```

## 7. Metrics

The dataplane compose passes `--prometheus` **by default**. Without it
`/netlox/v1/metrics` answers `503 Prometheus option is disabled`, and turning it
on afterwards costs a data-plane restart — so it is on from the first boot.
Confirm with:

```bash
curl -s http://127.0.0.1:11111/netlox/v1/config/metrics    # {"prometheus":true}
curl -s http://127.0.0.1:11111/netlox/v1/metrics | head    # ~95 series
```

`POST /netlox/v1/config/metrics` toggles collection at runtime, but that state
does **not** survive a container recreate. The flag does; prefer it.

> **Scrape from the host, not from a bridge.** Converged mode pins the gateway's
> plaintext API to `127.0.0.1` (§2), so `:11111/netlox/v1/metrics` is reachable
> only from the host network namespace. The gateway repo's own
> `deploy/monitoring/` stack already runs Prometheus with `network_mode: host`,
> so it works unchanged — but a Prometheus on a compose bridge will not reach
> it. The alternative is scraping through OAM's authenticated proxy
> (`/oam/loxilbs/{id}/netlox/v1/metrics`), which requires a bearer token.

Verified on the live converged node, through OAM's verified-TLS proxy:
`/config/metrics` → `{"prometheus":true}`, `/metrics` → 200 with 95 series.

## 8. Operations

```bash
cd deploy/compose
S="-f docker-compose.database.yml"
D="-f docker-compose.dataplane.yml"
M="-f docker-compose.yml -f docker-compose.prod.yml -f docker-compose.converged.yml"

docker compose $M up -d --no-deps oam-loxilb   # upgrade OAM only — traffic untouched
docker compose $M up -d --no-deps caddy        # upgrade the edge only
docker compose $M down                         # SAFE: gateway and DB are separate projects

# gateway upgrade — a real traffic event, schedule it
curl -s http://127.0.0.1:11111/netlox/v1/config/snapshot > snapshot-$(date +%F).json
docker compose $D pull && docker compose $D up -d

# database maintenance — affects both planes; schedule and back up first
docker compose $S exec -T postgres pg_dump -U oamuser -d loxioam -Fc > loxioam-$(date +%F).dump
docker compose $S up -d postgres
docker compose $S run --rm gateway-db-bootstrap
```

The gateway boot-restores from `/opt/loxilb/config/snapshot.json`. Snapshots
contain IPsec PSKs and certificate private keys — treat them as credentials.
The PostgreSQL volume is the single durable state source for both applications.
Never run `docker compose $S down -v` as part of an OAM or Gateway upgrade.

## 9. Troubleshooting

| Symptom | Check |
|---|---|
| Console unreachable from outside but fine from the host | An LB rule is hijacking the edge. `curl -s http://127.0.0.1:11111/netlox/v1/config/loadbalancer/all`, and set `OAM_RESERVED_ENDPOINTS`. |
| Both `:443` and `:8443` are published | `!override` missing from the overlay's `ports`. |
| Browser gets a TLS error or 404 at the edge | `SITE_ADDRESS` must include `:${EDGE_HTTPS_PORT}`. |
| `ERR_SSL_PROTOCOL_ERROR` when reaching the console **by address** (by name it works) | No `EDGE_SNI_FALLBACK`. An address sends no SNI, so Caddy selects no site and drops the handshake before any HTTP. Set `EDGE_SNI_FALLBACK=default_sni <primary-name>`, add the address to `SITE_ADDRESS`, and put it in the certificate as an `IP:` SAN. |
| OAM loops on `Database connection failed`, never healthy, while PostgreSQL reports healthy | The PostgreSQL volume was initialised with **different** credentials. `DB_PASSWORD` applies only to an empty data directory. Restore the old value from an `.env.bak.*`. Starting clean requires stopping the state project and explicitly removing `CONVERGED_PG_VOLUME`; this destroys OAM users, instances, snapshots, AI keys, and quotas. |
| Gateway logs an AI-key database connection or preflight error | Confirm `127.0.0.1:${CONVERGED_PG_HOST_PORT}` is listening, the `aigw` schema exists, and `secrets/aigw_db_password` is the value last applied by `gateway-db-bootstrap`. Re-run the idempotent bootstrap, then restart the Gateway. |
| `gateway-db-bootstrap` succeeds but Gateway management requests return 401/403 | Do not enable `--userservice` in this topology. OAM JWT and Gateway management tokens are separate authentication planes; only the AI-key store is enabled. |
| `docker compose $M down` leaves PostgreSQL and the Gateway running | Expected. They are independent `loxilb-state` and `loxilb-data` projects. Stop either only during an explicitly scheduled state/data maintenance event. |
| Instance shows **Down** right after re-running the init script | `GW_HOST` changed, so the gateway now serves a certificate for the new name and OAM pins only that name — the registered host no longer resolves. Set the instance's Host to the current `GW_HOST` (`grep GW_HOST .env`), or re-run and keep the previous name. |
| Browser shows a certificate-name error (`curl` exit 60) but `curl -k` works | The edge certificate's SAN list does not contain the name being used. Check it: `echo \| openssl s_client -connect <host>:8443 -servername <name> \| openssl x509 -noout -ext subjectAltName`. |
| Certificate valid today, expired later, nothing changed | ACME renewal cannot run without `:80`/`:443` — see §4. |
| Instance unreachable, `x509` in the OAM log | `GW_HOST` must equal the certificate SAN. Probe it: `openssl s_client -connect ${GW_HOST}:8091`. |
| Instance unreachable, connection refused | `TLS=true` set on the gateway? `sudo ss -lntp \| grep 8091` should show `*:8091`. |
| Intermittent, unexplained management-plane packet loss | Datapath attached to the veths — see §6. |
| Gateway config lost after an upgrade | `IGW_CONFIG_DIR` was not bind-mounted. |
| `/netlox/v1/metrics` returns `503 Prometheus option is disabled` | `--prometheus` missing from the gateway's `command:`. |
| Prometheus cannot scrape the gateway | `:11111` is loopback-only by design — run Prometheus with `network_mode: host` (see §7). |
| Everything "healthy" but no traffic is handled | Docker Desktop. `network_mode: host` is Linux-only. |
