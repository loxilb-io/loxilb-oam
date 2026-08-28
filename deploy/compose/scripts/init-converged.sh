#!/usr/bin/env bash
# Interactive bootstrap for the CONVERGED single-node deployment:
# shared state (PostgreSQL), management plane (OAM + console), and a local
# loxilb-inference-gateway data plane on the same host.
#
#   deploy/compose/scripts/init-converged.sh
#
# It writes .env, generates the secrets and certificates, creates the host
# paths, starts all three compose projects in the right order, verifies them, and
# optionally registers the local gateway in OAM.
#
# Safe to re-run: an existing .env is never overwritten without asking, and
# existing certificates are reused unless you say otherwise.
#
# Flags:
#   -y, --yes        accept every default, prompt only for what has no default
#       --no-start   set everything up but do not bring the stacks up
#   -h, --help       this text
#
# Full explanation of every choice here: docs/deployment-converged.md
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$COMPOSE_DIR/.env"
ASSUME_YES=0
DO_START=1

MGMT_FILES=(-f docker-compose.yml -f docker-compose.prod.yml -f docker-compose.converged.yml)
DATA_FILES=(-f docker-compose.dataplane.yml)
STATE_FILES=(-f docker-compose.database.yml)

# ── output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ]; then
  B=$'\033[1m'; DIM=$'\033[2m'; GRN=$'\033[32m'; YEL=$'\033[33m'; RED=$'\033[31m'; N=$'\033[0m'
else
  B=""; DIM=""; GRN=""; YEL=""; RED=""; N=""
fi
step()  { printf '\n%s==> %s%s\n' "$B" "$*" "$N"; }
ok()    { printf '  %s✓%s %s\n' "$GRN" "$N" "$*"; }
warn()  { printf '  %s!%s %s\n' "$YEL" "$N" "$*"; }
die()   { printf '\n%sERROR:%s %s\n\n' "$RED" "$N" "$*" >&2; exit 1; }
note()  { printf '  %s%s%s\n' "$DIM" "$*" "$N"; }
rand_alnum() { openssl rand -base64 "$1" | tr -dc 'A-Za-z0-9' | head -c "$1"; }

# ask VAR "prompt" "default"
ask() {
  local __var="$1" __prompt="$2" __default="${3:-}" __reply=""
  if [ "$ASSUME_YES" = 1 ] && [ -n "$__default" ]; then
    printf -v "$__var" '%s' "$__default"
    printf '  %s: %s %s(default)%s\n' "$__prompt" "$__default" "$DIM" "$N"
    return
  fi
  if [ -n "$__default" ]; then
    read -r -p "  $__prompt [$__default]: " __reply || true
    [ -z "$__reply" ] && __reply="$__default"
  else
    while [ -z "$__reply" ]; do read -r -p "  $__prompt: " __reply || true; done
  fi
  printf -v "$__var" '%s' "$__reply"
}

# confirm "prompt" [default y|n]
confirm() {
  local __prompt="$1" __default="${2:-y}" __reply=""
  if [ "$ASSUME_YES" = 1 ]; then [ "$__default" = y ]; return; fi
  local __hint="[Y/n]"; [ "$__default" = n ] && __hint="[y/N]"
  read -r -p "  $__prompt $__hint: " __reply || true
  __reply="${__reply:-$__default}"
  [[ "$__reply" =~ ^[Yy] ]]
}

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)    ASSUME_YES=1 ;;
    --no-start)  DO_START=0 ;;
    -h|--help)   sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)           die "unknown argument: $1 (try --help)" ;;
  esac
  shift
done

cd "$COMPOSE_DIR"

# ── 1. preflight ─────────────────────────────────────────────────────────────
step "Checking prerequisites"

[ "$(uname -s)" = "Linux" ] || die \
"Converged mode is Linux-only.

The gateway needs network_mode: host to attach eBPF to real NICs and to bind
VIPs. On Docker Desktop (macOS/Windows) host networking is a no-op: the stack
would come up reporting itself healthy while the gateway sees no host
interfaces at all. Run this on the target Linux node."

command -v docker  >/dev/null || die "docker not found in PATH."
command -v openssl >/dev/null || die "openssl not found in PATH (needed to generate secrets and certificates)."
docker info >/dev/null 2>&1   || die "cannot talk to the Docker daemon. Is it running, and is your user in the docker group?"
ok "docker present and the daemon is reachable"

docker compose version >/dev/null 2>&1 || die "'docker compose' (v2) not found. The Compose v1 'docker-compose' binary will not work here."
CV="$(docker compose version --short 2>/dev/null | tr -d 'v')"
CV_MAJ="${CV%%.*}"; CV_REST="${CV#*.}"; CV_MIN="${CV_REST%%.*}"
if [ "${CV_MAJ:-0}" -lt 2 ] || { [ "${CV_MAJ:-0}" -eq 2 ] && [ "${CV_MIN:-0}" -lt 24 ]; }; then
  die "Compose $CV is too old — 2.24+ is required for the '!override' tag.

Without it the converged overlay's port list MERGES with the base file's, so
the edge would publish 80 and 443 as well as its own port, re-creating exactly
the collision with the data plane that the overlay exists to prevent."
fi
ok "docker compose $CV (supports !override)"

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  if sudo -n true 2>/dev/null; then SUDO="sudo"
  elif command -v sudo >/dev/null; then SUDO="sudo"; warn "sudo may prompt for your password when creating the gateway's config directory"
  else warn "not root and no sudo — you may have to create the gateway config directory yourself"
  fi
fi

# ── 2. existing .env ─────────────────────────────────────────────────────────
REUSE_ENV=0
if [ -f "$ENV_FILE" ]; then
  step "An .env already exists"
  note "$ENV_FILE"
  if confirm "Keep it and only do the remaining setup (certs, dirs, start)?" y; then
    REUSE_ENV=1
    ok "keeping the existing .env"
  else
    BK="$ENV_FILE.bak.$(date +%Y%m%d-%H%M%S)"
    cp "$ENV_FILE" "$BK"; ok "backed up to $(basename "$BK")"
    # Needed if a PostgreSQL volume from this .env still exists — see "Database" below.
    OLD_DB_PW="$(grep -m1 '^DB_PASSWORD=' "$BK" | cut -d= -f2- || true)"
    # Everything else becomes the DEFAULT for its prompt. Re-running and pressing
    # Enter must reproduce the deployment you already have — silently drifting to
    # a computed default (a different GW_HOST, say) invalidates the certificate
    # the gateway serves and every instance already registered against it.
    envget() { grep -m1 "^$2=" "$1" 2>/dev/null | cut -d= -f2- | sed 's/[[:space:]]*#.*$//' | sed 's/[[:space:]]*$//' || true; }
    PREV_GW_HOST="$(envget "$BK" GW_HOST)"
    PREV_EDGE_BIND_IP="$(envget "$BK" EDGE_BIND_IP)"
    PREV_EDGE_PORT="$(envget "$BK" EDGE_HTTPS_PORT)"
    PREV_IGW_TAG="$(envget "$BK" IGW_TAG)"
    PREV_IGW_CONFIG_DIR="$(envget "$BK" IGW_CONFIG_DIR)"
    PREV_PG_HOST_PORT="$(envget "$BK" CONVERGED_PG_HOST_PORT)"
    PREV_PG_VOLUME="$(envget "$BK" CONVERGED_PG_VOLUME)"
    PREV_OAM_TAG="$(envget "$BK" OAM_TAG)"
    PREV_UI_TAG="$(envget "$BK" UI_TAG)"
    # SITE_ADDRESS is "https://name:port[, https://other:port]" — take the first host.
    PREV_EDGE_NAME="$(envget "$BK" SITE_ADDRESS | sed 's/,.*//; s#^https\?://##; s/:[0-9]*$//')"
  fi
fi

if [ "$REUSE_ENV" = 1 ]; then
  # shellcheck disable=SC2046
  get() { grep -m1 "^$1=" "$ENV_FILE" 2>/dev/null | cut -d= -f2- | sed 's/[[:space:]]*#.*$//' | xargs 2>/dev/null || true; }
  GW_HOST="$(get GW_HOST)";                 EDGE_BIND_IP="$(get EDGE_BIND_IP)"
  EDGE_HTTPS_PORT="$(get EDGE_HTTPS_PORT)"; IGW_CONFIG_DIR="$(get IGW_CONFIG_DIR)"
  SITE_ADDRESS="$(get SITE_ADDRESS)";       IGW_TAG="$(get IGW_TAG)"
  DB_PASSWORD="$(get DB_PASSWORD)"
  CONVERGED_PG_HOST_PORT="$(get CONVERGED_PG_HOST_PORT)"
  CONVERGED_PG_VOLUME="$(get CONVERGED_PG_VOLUME)"
  EDGE_NAME="$(printf '%s' "$SITE_ADDRESS" | sed 's/,.*//; s#^https\?://##; s/:[0-9]*$//')"
  EDGE_TLS_MODE="reuse"
  : "${EDGE_HTTPS_PORT:=8443}"; : "${IGW_CONFIG_DIR:=/opt/loxilb/config}"
  : "${EDGE_BIND_IP:=0.0.0.0}"; : "${IGW_TAG:=latest-u24}"
  : "${CONVERGED_PG_HOST_PORT:=5432}"
  : "${CONVERGED_PG_VOLUME:=loxilb-state-postgres-data}"
  : "${SITE_ADDRESS:=https://${EDGE_BIND_IP}:${EDGE_HTTPS_PORT}}"
  [ -n "$GW_HOST" ] || die "the existing .env has no GW_HOST — it is not a converged .env. Re-run and choose to recreate it."

  # An .env created by the former two-project layout has no state-volume key,
  # while its durable data still lives under the management project's volume
  # name. Adopt that exact volume even on the "keep existing .env" path; using
  # the new default here would silently initialise an empty database beside it.
  if ! docker volume inspect "$CONVERGED_PG_VOLUME" >/dev/null 2>&1 \
     && docker volume inspect loxilb-mgmt_postgres_data >/dev/null 2>&1; then
    CONVERGED_PG_VOLUME=loxilb-mgmt_postgres_data
    if grep -q '^CONVERGED_PG_VOLUME=' "$ENV_FILE"; then
      sed -i "s/^CONVERGED_PG_VOLUME=.*/CONVERGED_PG_VOLUME=$CONVERGED_PG_VOLUME/" "$ENV_FILE"
    else
      printf '\nCONVERGED_PG_VOLUME=%s\n' "$CONVERGED_PG_VOLUME" >> "$ENV_FILE"
    fi
    chmod 600 "$ENV_FILE"
    warn "adopting legacy management database volume as shared state: $CONVERGED_PG_VOLUME"
  fi
  ok "GW_HOST=$GW_HOST  edge=${EDGE_BIND_IP:-0.0.0.0}:$EDGE_HTTPS_PORT"
fi

if [ "$REUSE_ENV" = 0 ]; then

# ── 3. network ───────────────────────────────────────────────────────────────
step "Network layout"

# Docker's own interfaces carry global-scope addresses too. Counting them would
# make a single-NIC host look multi-homed and hand the console :443 — the port
# the data plane wants. Same exclusion the gateway's --blacklist uses.
mapfile -t ADDRS < <(ip -o -4 addr show scope global 2>/dev/null \
  | awk '{split($4,a,"/"); print $2" "a[1]}' \
  | grep -Ev '^(docker[0-9]+|br-[a-f0-9]+|veth[a-z0-9]+|virbr[0-9]+|cni[0-9]+|flannel|llb[0-9]+|vlan[0-9]+) ')
[ "${#ADDRS[@]}" -gt 0 ] || die "no global IPv4 address found on this host."
DEFAULT_IP="$(ip -o -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')"
[ -n "$DEFAULT_IP" ] || DEFAULT_IP="$(echo "${ADDRS[0]}" | awk '{print $2}')"

for a in "${ADDRS[@]}"; do note "$a"; done
if [ "${#ADDRS[@]}" -eq 1 ]; then
  warn "one usable address — the console and the data plane must share it"
  note "The edge will take a port of its own and 80/443 stay free for VIPs."
  note "loxilb VIPs are floating addresses: adding a second address to this NIC"
  note "later lets you move the console back to :443. See the guide, section 4."
else
  ok "${#ADDRS[@]} addresses — you can give the console one and leave the rest for VIPs"
fi

ask EDGE_BIND_IP "Address the management console should listen on" "${PREV_EDGE_BIND_IP:-$DEFAULT_IP}"

DEF_PORT=8443
[ "${#ADDRS[@]}" -gt 1 ] && DEF_PORT=443
ask EDGE_HTTPS_PORT "Port for the management console (80/443 belong to the data plane on a single-address host)" "${PREV_EDGE_PORT:-$DEF_PORT}"
if [ "$EDGE_HTTPS_PORT" = 443 ] || [ "$EDGE_HTTPS_PORT" = 80 ]; then
  warn "the console will own :$EDGE_HTTPS_PORT on $EDGE_BIND_IP — no VIP may use that address:port pair"
fi

# ── 4. how operators reach the console ───────────────────────────────────────
step "Console address and TLS"
DEF_NAME="$(hostname -f 2>/dev/null || hostname)"
[ -n "$DEF_NAME" ] && [ "$DEF_NAME" != "localhost" ] || DEF_NAME="$EDGE_BIND_IP"
note "Whatever you enter here must be what operators actually type in the browser:"
note "it becomes the certificate's identity and Caddy matches the Host header on it."
ask EDGE_NAME "Hostname or IP operators will use" "${PREV_EDGE_NAME:-$DEF_NAME}"

IS_IP=0
[[ "$EDGE_NAME" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && IS_IP=1

echo
note "Operators usually reach the console by more than one address — a NATed"
note "public IP, the LAN address, a second DNS name. Every one of them has to be"
note "in the certificate, or that client gets a hard TLS failure. An address the"
note "host cannot see itself (a public IP in front of NAT) must be typed here."
ask EDGE_ALT "Other names/addresses, comma-separated ('-' for none)" "$EDGE_BIND_IP"

SAN_LIST=""; SITE_LIST=""; HAS_IP=0
add_san() {
  local e; e="$(printf '%s' "$1" | tr -d '[:space:]')"
  [ -z "$e" ] && return 0
  case ",$SITE_LIST," in *"https://${e}:${EDGE_HTTPS_PORT},"*) return 0 ;; esac
  if [[ "$e" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    SAN_LIST="${SAN_LIST}${SAN_LIST:+,}IP:$e"; HAS_IP=1
  else
    SAN_LIST="${SAN_LIST}${SAN_LIST:+,}DNS:$e"
  fi
  SITE_LIST="${SITE_LIST}${SITE_LIST:+, }https://${e}:${EDGE_HTTPS_PORT}"
}
add_san "$EDGE_NAME"
if [ "$EDGE_ALT" != "-" ]; then
  IFS=',' read -r -a __alts <<< "$EDGE_ALT"
  for __a in "${__alts[@]}"; do add_san "$__a"; done
fi
SAN_LIST="${SAN_LIST},DNS:localhost,IP:127.0.0.1"
ok "certificate will cover: ${SAN_LIST}"

echo
note "TLS for the console:"
note "  1) self-signed, generated now   (works anywhere; browsers warn until trusted)"
note "  2) certificate you provide      (drop cert.pem + key.pem in certs/edge/)"
if [ "$IS_IP" = 0 ] && { [ "$EDGE_HTTPS_PORT" = 443 ] || [ "$EDGE_HTTPS_PORT" = 80 ]; }; then
  note "  3) automatic HTTPS via ACME     (needs public DNS for $EDGE_NAME)"
  ACME_OK=1
else
  ACME_OK=0
  if [ "$IS_IP" = 1 ]; then note "  ${DIM}(ACME not offered: no CA issues certificates for a bare address)${N}"
  else note "  ${DIM}(ACME not offered: it needs :80 or :443, which the data plane has)${N}"; fi
fi
while :; do
  ask EDGE_TLS_MODE "Choose 1-$(( ACME_OK ? 3 : 2 ))" "1"
  case "$EDGE_TLS_MODE" in
    1|2) break ;;
    3) if [ "$ACME_OK" = 1 ]; then break; else warn "option 3 is not available with this address/port"; fi ;;
    *) warn "enter 1, 2$([ "$ACME_OK" = 1 ] && echo " or 3")" ;;
  esac
done

# ── 5. gateway ───────────────────────────────────────────────────────────────
step "Local gateway"
note "The gateway runs in the host network namespace, so it has no compose DNS"
note "name. GW_HOST is the name OAM registers it under AND its certificate's"
note "SAN; the overlay pins that name to the Docker host-gateway address."
DEF_GW="gw.$(hostname -s 2>/dev/null || echo local)"
ask GW_HOST "Name for the local gateway" "${PREV_GW_HOST:-$DEF_GW}"
if [ -n "${PREV_GW_HOST:-}" ] && [ "$GW_HOST" != "$PREV_GW_HOST" ]; then
  warn "GW_HOST changes from '$PREV_GW_HOST' to '$GW_HOST'."
  note "The gateway will serve a certificate for the NEW name, and OAM will pin"
  note "only the new name — so any instance already registered as"
  note "https://$PREV_GW_HOST:8091/netlox/v1 stops resolving and reports Down."
  note "The registration step at the end re-points it for you, but only if it can"
  note "log in. Keep the old name unless you mean to change it."
  confirm "Continue with '$GW_HOST'?" n || GW_HOST="$PREV_GW_HOST"
  ok "using GW_HOST=$GW_HOST"
fi
[[ "$GW_HOST" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && die \
"GW_HOST must be a name, not an address.

Go resolves an IP literal without consulting /etc/hosts, so the host-gateway
pin would be ignored and OAM's calls would hairpin out through the host NIC.
Any name works — it never needs to resolve in public DNS."

ask IGW_TAG "loxilb-inference-gateway image tag" "${PREV_IGW_TAG:-latest-u24}"
ask IGW_CONFIG_DIR "Host directory for the gateway config snapshot" "${PREV_IGW_CONFIG_DIR:-/opt/loxilb/config}"
ask CONVERGED_PG_HOST_PORT "Loopback port for the shared PostgreSQL service" "${PREV_PG_HOST_PORT:-5432}"
CONVERGED_PG_VOLUME="${PREV_PG_VOLUME:-loxilb-state-postgres-data}"

step "Management plane images"
warn "pin these to released versions in production; 'latest' is not reproducible"
ask OAM_TAG "loxilb-oam image tag" "${PREV_OAM_TAG:-latest}"
ask UI_TAG  "loxilb-ui image tag"  "${PREV_UI_TAG:-latest}"

# ── 6. secrets ───────────────────────────────────────────────────────────────
step "Generating secrets"

# Mirrors internal/services.validatePassword so a bad password fails here rather
# than aborting the server on first boot.
valid_admin_pw() {
  local p="$1"
  [ "${#p}" -ge 9 ] || return 1
  [ "$p" != "admin" ] || return 1
  [[ "$p" == *[[:upper:]]* ]] || return 1
  [[ "$p" == *[[:lower:]]* ]] || return 1
  [[ "$p" == *[[:digit:]]* ]] || return 1
  case "$p" in *['!%*+,-.:=?@^_~']*) ;; *) return 1 ;; esac
  # no character three times in a row
  local i c prev="" run=1
  for ((i=0; i<${#p}; i++)); do
    c="${p:i:1}"
    if [ "$c" = "$prev" ]; then run=$((run+1)); [ "$run" -ge 3 ] && return 1; else run=1; fi
    prev="$c"
  done
  return 0
}

gen_admin_pw() {
  local p
  for _ in $(seq 1 40); do
    p="$(rand_alnum 6)$(printf '%s' '!%*+,-.:=?@^_~' | fold -w1 | shuf | head -c2)$(rand_alnum 4)"
    if valid_admin_pw "$p"; then printf '%s' "$p"; return 0; fi
  done
  printf 'Loxi%%Adm1n%s' "$(rand_alnum 4)"
}

# PostgreSQL applies POSTGRES_PASSWORD only when it initialises an EMPTY data
# directory. Against a volume that already exists, freshly generated credentials
# are simply wrong: PostgreSQL comes up fine and reports healthy (pg_isready
# checks that the server accepts connections, not that a password matches),
# while OAM loops on "Database connection failed" and never goes healthy.
# Decide this before writing .env.
KEEP_DB=0
PG_VOL="$(docker volume ls -q --filter "name=^${CONVERGED_PG_VOLUME}$" 2>/dev/null | head -1 || true)"
if [ -z "$PG_VOL" ]; then
  # Adopt the pre-state-project converged volume in place. Naming it in .env
  # lets the state project use the existing data without a copy or destructive
  # rename; Docker volumes are independent of their original Compose project.
  LEGACY_PG_VOL="$(docker volume ls -q --filter name=loxilb-mgmt_postgres_data 2>/dev/null | head -1 || true)"
  if [ -n "$LEGACY_PG_VOL" ]; then
    PG_VOL="$LEGACY_PG_VOL"
    CONVERGED_PG_VOLUME="$LEGACY_PG_VOL"
    warn "adopting legacy management database volume as shared state: $LEGACY_PG_VOL"
  fi
fi
if [ -n "$PG_VOL" ]; then
  warn "an existing database volume was found: $PG_VOL"
  note "New database secrets would not apply to it — PostgreSQL keeps the password"
  note "it was first initialised with, and OAM would fail to authenticate."
  if [ -n "${OLD_DB_PW:-}" ]; then
    if confirm "Keep that database and reuse its credentials (preserves users, instances, snapshots)?" y; then
      KEEP_DB=1
    fi
  else
    note "There is no previous .env to recover the credentials from."
  fi
  if [ "$KEEP_DB" = 0 ]; then
    warn "starting clean DESTROYS that database: OAM users/instances/snapshots and Gateway API keys/quotas"
    if confirm "Delete $PG_VOL and start with an empty database?" n; then
      docker compose "${MGMT_FILES[@]}" down >/dev/null 2>&1 || true
      docker compose "${DATA_FILES[@]}" down >/dev/null 2>&1 || true
      docker compose "${STATE_FILES[@]}" down >/dev/null 2>&1 || true
      docker volume rm "$PG_VOL" >/dev/null 2>&1 \
        || die "could not remove $PG_VOL — stop anything still using it and re-run."
      ok "removed $PG_VOL — a fresh database will be initialised"
    else
      die "Nothing has been changed yet.

Re-run and choose to keep the existing .env, so its database credentials keep
matching the volume. To recover credentials by hand, they are in one of:
  $(find "$(dirname "$ENV_FILE")" -maxdepth 1 -type f -name "$(basename "$ENV_FILE").bak.*" -print 2>/dev/null | sort | tail -3 | tr '\n' ' ')"
    fi
  fi
fi

if [ "$KEEP_DB" = 1 ]; then
  DB_PASSWORD="$OLD_DB_PW"
  ok "database credentials carried over from the previous .env"
  note "The admin password below applies only to a FRESH database; this one keeps"
  note "the password it already has. The script offers to reset it at the end."
else
  DB_PASSWORD="$(rand_alnum 28)"
fi
OAM_JWT_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
SNAPSHOT_ENC_KEY="$(openssl rand -base64 32 | tr -d '\n')"
ok "OAM database, JWT and snapshot-encryption secrets generated"

ADMIN_PW=""
if confirm "Generate the bootstrap admin password too?" y; then
  ADMIN_PW="$(gen_admin_pw)"; ok "admin password generated (shown at the end)"
else
  while :; do
    read -r -s -p "  Admin password (>=9 chars, upper+lower+digit+special): " ADMIN_PW; echo
    if valid_admin_pw "$ADMIN_PW"; then break; fi
    warn "does not meet the policy (>=9 chars, upper, lower, digit, special, no character 3x in a row)"
  done
fi

# ── 7. compose the .env ──────────────────────────────────────────────────────
step "Writing .env"

# Caddy matches the Host header, so every address operators use needs its own
# site address — otherwise that client reaches a listener with no site and gets
# nothing back.
SITE_ADDRESS="$SITE_LIST"
case "$EDGE_TLS_MODE" in
  1|2) EDGE_TLS_LINE="tls /certs/edge/cert.pem /certs/edge/key.pem" ;;
  3)   EDGE_TLS_LINE=""
       # ACME cannot issue for a bare address, and listing one here would make
       # Caddy keep trying. Name-only site, and say so rather than silently
       # dropping the addresses the operator just asked for.
       SITE_ADDRESS="https://${EDGE_NAME}:${EDGE_HTTPS_PORT}"
       [ "$HAS_IP" = 1 ] && warn "ACME mode: the addresses you listed are dropped — no CA issues for an IP. Reach the console by name."
       ;;
esac
# A browser opened at an address sends NO SNI at all, so Caddy has no site to
# select and the handshake dies with ERR_SSL_PROTOCOL_ERROR before a byte of
# HTTP is spoken — which looks like a broken port, not a TLS problem.
SNI_LINE=""
if [ "$HAS_IP" = 1 ] && [ "$EDGE_TLS_MODE" != 3 ]; then
  SNI_LINE="default_sni ${EDGE_NAME}"
fi
# Reserve exactly what the edge binds, so no LB rule can take it over.
RESERVED="${EDGE_BIND_IP}:${EDGE_HTTPS_PORT}"

umask 077
cat > "$ENV_FILE" <<ENVEOF
# Generated by scripts/init-converged.sh on $(date -Is)
# Converged single-node deployment. Guide: docs/deployment-converged.md

# ── Required secrets ─────────────────────────────────────────────────────────
DB_PASSWORD=$DB_PASSWORD
OAM_JWT_SECRET=$OAM_JWT_SECRET
OAM_DEFAULT_ADMIN_PASSWORD=$ADMIN_PW
SNAPSHOT_ENC_KEY=$SNAPSHOT_ENC_KEY

# ── Access control ───────────────────────────────────────────────────────────
OAM_ALLOWED_ORIGINS=$SITE_ADDRESS
OAM_TRUSTED_PROXIES=172.16.0.0/12

# ── Edge ─────────────────────────────────────────────────────────────────────
SITE_ADDRESS=$SITE_ADDRESS
EDGE_TLS=$EDGE_TLS_LINE
EDGE_SNI_FALLBACK=$SNI_LINE
EDGE_BIND_IP=$EDGE_BIND_IP
EDGE_HTTPS_PORT=$EDGE_HTTPS_PORT
OAM_UPSTREAM=http://oam-loxilb:8080

# ── Converged single-node ────────────────────────────────────────────────────
GW_HOST=$GW_HOST
OAM_RESERVED_ENDPOINTS=$RESERVED
IGW_IMAGE=ghcr.io/loxilb-io/loxilb-inference-gateway
IGW_TAG=$IGW_TAG
IGW_CONFIG_DIR=$IGW_CONFIG_DIR
IGW_TLS_HOST=0.0.0.0

# ── Shared PostgreSQL state ──────────────────────────────────────────────────
CONVERGED_PG_HOST_PORT=$CONVERGED_PG_HOST_PORT
CONVERGED_DB_NETWORK=loxilb-converged-db
CONVERGED_PG_VOLUME=$CONVERGED_PG_VOLUME
AIGW_DB_USER=aigwuser
AIGW_DB_PASSWORD_FILE=./secrets/aigw_db_password
AIGW_MGMT_DB_PASSWORD_FILE=./secrets/aigw_mgmt_db_password

# ── TLS to the gateway (verified) ────────────────────────────────────────────
OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
OAM_INSTANCE_TLS_INSECURE=false

# ── Database ─────────────────────────────────────────────────────────────────
DB_USER=oamuser
DB_HOST=postgres
DB_PORT=5432
DB_NAME=loxioam
TOKEN_EXPIRATION=480

# ── Images ───────────────────────────────────────────────────────────────────
OAM_IMAGE=ghcr.io/loxilb-io/loxilb-oam
UI_IMAGE=ghcr.io/loxilb-io/loxilb-ui
OAM_TAG=$OAM_TAG
UI_TAG=$UI_TAG
ENVEOF
umask 022
chmod 600 "$ENV_FILE"
ok ".env written (0600) — it holds every secret for this node, keep it that way"

fi  # end of "$REUSE_ENV" = 0

# Runtime Gateway DB credentials live in files, not .env or the process command
# line. Preserve existing files on every re-run; bootstrap rotates the database
# roles to the file values, so deleting and regenerating one unintentionally
# would be an availability event.
step "Gateway database credential files"
SECRETS_DIR="$COMPOSE_DIR/secrets"
mkdir -p "$SECRETS_DIR"
chmod 700 "$SECRETS_DIR"
ensure_secret_file() {
  local path="$1" label="$2"
  if [ -s "$path" ]; then
    chmod 600 "$path"
    ok "$label preserved"
  else
    umask 077
    rand_alnum 32 > "$path"
    chmod 600 "$path"
    umask 022
    ok "$label generated"
  fi
}
ensure_secret_file "$SECRETS_DIR/aigw_db_password" "Gateway AI-store credential"
ensure_secret_file "$SECRETS_DIR/aigw_mgmt_db_password" "Gateway management-store credential (provisioned, dormant)"

# ── 8. host paths and certificates ───────────────────────────────────────────
step "Host directory for the gateway snapshot"
if [ -d "$IGW_CONFIG_DIR" ]; then
  ok "$IGW_CONFIG_DIR exists"
else
  $SUDO mkdir -p "$IGW_CONFIG_DIR" || die "could not create $IGW_CONFIG_DIR"
  ok "created $IGW_CONFIG_DIR"
fi
note "Without this bind mount the gateway's configuration survives a restart but"
note "is lost on image upgrade, which recreates the container."

step "Certificates"
chmod +x "$COMPOSE_DIR"/scripts/*.sh 2>/dev/null || true

if [ -f "$COMPOSE_DIR/certs/instance-ca/$GW_HOST/server.crt" ]; then
  ok "gateway certificate for $GW_HOST already present"
else
  if ! CERTOUT="$(./scripts/generate-instance-certs.sh "$GW_HOST" 2>&1)"; then
    printf '%s\n' "$CERTOUT" >&2; die "could not generate the gateway certificate"
  fi
  ok "management CA + gateway certificate for $GW_HOST"
fi
note "certs/instance-ca/$GW_HOST/ mounts straight into the gateway — nothing is copied"

if [ "${EDGE_TLS_MODE:-reuse}" = 1 ]; then
  if [ -f "$COMPOSE_DIR/certs/edge/cert.pem" ] && ! confirm "An edge certificate already exists — regenerate it?" n; then
    ok "keeping the existing edge certificate"
  else
    CERTOUT="$(./scripts/generate-edge-certs.sh "$EDGE_NAME" "$SAN_LIST" 2>&1)" \
      || { printf '%s\n' "$CERTOUT" >&2; die "could not generate the edge certificate"; }
    ok "edge certificate for $SAN_LIST"
    note "self-signed: browsers warn until certs/edge/cert.pem is trusted on each client"
  fi
elif [ "${EDGE_TLS_MODE:-reuse}" = 2 ]; then
  [ -f "$COMPOSE_DIR/certs/edge/cert.pem" ] && [ -f "$COMPOSE_DIR/certs/edge/key.pem" ] \
    || die "you chose to supply the edge certificate, but certs/edge/cert.pem and key.pem are not both there yet.
Drop them in (leaf + intermediates concatenated in cert.pem) and re-run."
  ok "using the edge certificate you provided"
fi

if [ "$DO_START" = 0 ]; then
  step "Set up, not started (--no-start)"
  note "Shared state:     docker compose ${STATE_FILES[*]} up -d postgres"
  note "DB bootstrap:     docker compose ${STATE_FILES[*]} run --rm gateway-db-bootstrap"
  note "Data plane:       docker compose ${DATA_FILES[*]} up -d"
  note "Management plane: docker compose ${MGMT_FILES[*]} up -d"
  exit 0
fi

# ── 9. start ─────────────────────────────────────────────────────────────────
# PostgreSQL owns an independent lifecycle. Bring it up and prove the canonical
# Gateway bootstrap before either application is allowed to start.
step "Starting shared PostgreSQL state (project: loxilb-state)"
docker compose "${STATE_FILES[@]}" up -d postgres \
  || die "shared PostgreSQL did not start. 'docker compose ${STATE_FILES[*]} logs postgres' will say why."
ok "shared PostgreSQL container up"

step "Provisioning Gateway schemas and roles"
docker compose "${STATE_FILES[@]}" run --rm gateway-db-bootstrap \
  || die "Gateway database bootstrap failed. No application containers were started."
ok "aigw and aigw_mgmt bootstrap complete"

# Data plane next and as its OWN compose project: a management-plane `down`
# must never tear down the eBPF datapath or its PostgreSQL dependency.
step "Starting the data plane (project: loxilb-data)"
docker compose "${DATA_FILES[@]}" up -d \
  || die "the gateway did not start. 'docker compose ${DATA_FILES[*]} logs' will say why."
ok "gateway container up"

step "Starting the management plane (project: loxilb-mgmt)"
docker compose "${MGMT_FILES[@]}" up -d \
  || die "the management plane did not start. 'docker compose ${MGMT_FILES[*]} logs' will say why."
ok "management plane up"

# ── 10. verify ───────────────────────────────────────────────────────────────
step "Verifying"
FAIL=0

# Authenticated SQL readiness plus the schema/privilege contract. pg_isready by
# itself does not prove the password works and would miss an old-volume mismatch.
DB_CHECK="$(docker compose "${STATE_FILES[@]}" exec -T postgres sh -ec \
  'PGPASSWORD="$POSTGRES_PASSWORD" psql -qAt -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT array_to_string(array_agg(nspname ORDER BY nspname), chr(44)) FROM pg_namespace WHERE nspname IN ('\''aigw'\'','\''aigw_mgmt'\'','\''public'\'')"' 2>/dev/null || true)"
if [ "$DB_CHECK" = "aigw,aigw_mgmt,public" ]; then
  ok "one database contains public, aigw and aigw_mgmt schemas"
else
  warn "shared database schema contract failed (observed: ${DB_CHECK:-none})"; FAIL=1
fi

DB_ISOLATION="$(docker compose "${STATE_FILES[@]}" exec -T postgres sh -ec \
  'PGPASSWORD="$POSTGRES_PASSWORD" psql -qAt -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT has_schema_privilege('\''aigwuser'\'','\''aigw'\'','\''CREATE'\'') AND NOT has_schema_privilege('\''aigwuser'\'','\''aigw_mgmt'\'','\''USAGE'\'') AND NOT has_schema_privilege('\''aigw_mgmt_user'\'','\''aigw'\'','\''USAGE'\'')"' 2>/dev/null || true)"
if [ "$DB_ISOLATION" = t ]; then
  ok "Gateway database roles are schema-isolated"
else
  warn "Gateway database role isolation check failed"; FAIL=1
fi

if ss -lntH 2>/dev/null | grep -q "127.0.0.1:${CONVERGED_PG_HOST_PORT}\b"; then
  ok "PostgreSQL published on loopback only (${CONVERGED_PG_HOST_PORT})"
else
  warn "PostgreSQL loopback listener not found on ${CONVERGED_PG_HOST_PORT}"; FAIL=1
fi

printf '  waiting for OAM to become healthy '
for _ in $(seq 1 30); do
  st="$(docker inspect -f '{{.State.Health.Status}}' loxilb-mgmt-oam-loxilb-1 2>/dev/null || echo none)"
  [ "$st" = healthy ] && break
  printf '.'; sleep 2
done
echo
if [ "${st:-none}" = healthy ]; then
  ok "OAM healthy"
else
  warn "OAM did not become healthy. Last lines from it:"
  docker logs loxilb-mgmt-oam-loxilb-1 2>&1 | tail -6 | sed 's/^/      /'
  if docker logs loxilb-mgmt-oam-loxilb-1 2>&1 | tail -20 | grep -qi "database connection failed"; then
    warn "OAM cannot reach the database with the credentials in .env."
    note "Almost always: a PostgreSQL volume initialised with DIFFERENT credentials."
    note "Either restore DB_PASSWORD from an .env backup, or"
    note "remove the volume to start clean:  docker volume rm ${PG_VOL:-$CONVERGED_PG_VOLUME}"
  fi
  FAIL=1
fi

# eBPF attachment can take several seconds after the container is running. Wait
# for both API listeners before evaluating the binding and TLS checks below.
printf '  waiting for Gateway APIs '
for _ in $(seq 1 30); do
  if ss -lntH 2>/dev/null | grep -q '127.0.0.1:11111' \
     && ss -lntH 2>/dev/null | grep -q ':8091'; then
    break
  fi
  printf '.'; sleep 2
done
echo

# The plaintext gateway API must be loopback-only; the TLS one must be reachable.
if ss -lntH 2>/dev/null | grep -q '127.0.0.1:11111'; then
  ok "gateway plaintext API bound to 127.0.0.1 only"
else
  warn "gateway :11111 is NOT loopback-only — check HOST=127.0.0.1 in docker-compose.dataplane.yml"; FAIL=1
fi
if ss -lntH 2>/dev/null | grep -q ':8091'; then
  ok "gateway TLS API listening on :8091"
else
  warn "gateway :8091 not listening — is TLS=true set?"; FAIL=1
fi

# The edge must own exactly one address:port, or the port policy has slipped.
EDGE_LISTEN="$(ss -lntH 2>/dev/null | grep -c ":${EDGE_HTTPS_PORT}\b" || true)"
if [ "${EDGE_LISTEN:-0}" -ge 1 ]; then
  ok "console listening on ${EDGE_BIND_IP}:${EDGE_HTTPS_PORT}"
else
  warn "nothing listening on ${EDGE_HTTPS_PORT}"; FAIL=1
fi
if ss -lntH 2>/dev/null | grep -qE ':(80|443)\b.*docker-proxy'; then
  warn "something is publishing :80/:443 — those belong to the data plane in converged mode"
fi

# The datapath must not have attached to Docker's own interfaces.
TAKEN="$(docker exec loxilb-gateway loxicmd get port 2>/dev/null | grep -coE '\| (docker0|br-[a-f0-9]+|veth[a-z0-9]+) ' || true)"
if [ "${TAKEN:-0}" -eq 0 ]; then
  ok "datapath is off docker0/br-*/veth*"
else
  warn "datapath attached to $TAKEN Docker interface(s) — check --blacklist"; FAIL=1
fi

# Metrics should be live from first boot.
if docker exec loxilb-gateway sh -c 'command -v curl >/dev/null' 2>/dev/null; then
  if docker exec loxilb-gateway curl -sf -o /dev/null http://127.0.0.1:11111/netlox/v1/metrics 2>/dev/null; then
    ok "gateway metrics enabled"
  else
    warn "metrics not answering (is --prometheus set?)"
  fi
else
  note "skipped metrics probe (no curl in the gateway image)"
fi

# Exactly what OAM will do: chain the gateway certificate to the management CA
# and match the hostname against GW_HOST. A failure here is the difference
# between "registered" and "instance unreachable" in the console.
if echo | openssl s_client -connect "127.0.0.1:8091" -servername "$GW_HOST" \
     -CAfile "$COMPOSE_DIR/certs/instance-ca.pem" -verify_hostname "$GW_HOST" \
     -verify_return_error >/dev/null 2>&1; then
  ok "gateway certificate verifies against the management CA for $GW_HOST"
else
  warn "gateway certificate does NOT verify for $GW_HOST — OAM will report the instance unreachable"
  note "SAN must contain $GW_HOST: openssl x509 -in certs/instance-ca/$GW_HOST/server.crt -noout -ext subjectAltName"
  FAIL=1
fi

# ── 11. register the gateway ─────────────────────────────────────────────────
# Registration has to happen NOW, while OAM_DEFAULT_ADMIN_PASSWORD is still the
# admin password. The moment an operator logs in and changes it — which the
# summary below tells them to do — this script can no longer authenticate.
: "${EDGE_NAME:=$EDGE_BIND_IP}"
RESOLVE_IP="$EDGE_BIND_IP"; [ "$RESOLVE_IP" = "0.0.0.0" ] && RESOLVE_IP=127.0.0.1
API="https://${EDGE_NAME}:${EDGE_HTTPS_PORT}/api/oam"
CURL=(curl -sk --max-time 20 --resolve "${EDGE_NAME}:${EDGE_HTTPS_PORT}:${RESOLVE_IP}")
GW_NAME="local-gateway"
GW_BODY="{\"name\":\"$GW_NAME\",\"host\":\"$GW_HOST\",\"port\":\"8091\",\"protocol\":\"https\",\"version\":\"v1\",\"description\":\"co-located converged gateway\",\"cimage\":\"ghcr.io/loxilb-io/loxilb-inference-gateway\",\"ctag\":\"$IGW_TAG\"}"

# Pull "<id> <host>" for GW_NAME out of the instance list. jq if present,
# python3 otherwise; both are optional, so degrade to instructions if neither is.
gw_lookup() {
  local json="$1"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$json" | jq -r --arg n "$GW_NAME" \
      'if type=="array" then . else (.data // []) end | map(select(.name==$n)) | .[0] // empty | "\(.id) \(.host)"' 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    printf '%s' "$json" | python3 -c 'import sys,json
try: d=json.load(sys.stdin)
except Exception: sys.exit(0)
if isinstance(d,dict): d=d.get("data") or []
for i in d if isinstance(d,list) else []:
    if i.get("name")==sys.argv[1]: print(i.get("id"),i.get("host")); break' "$GW_NAME" 2>/dev/null
  fi
}

if command -v curl >/dev/null && confirm "Register the local gateway in OAM now?" y; then
  ADMIN_IN_ENV="$(grep -m1 '^OAM_DEFAULT_ADMIN_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
  TOKEN="$("${CURL[@]}" -X POST "$API/login" -H 'Content-Type: application/json' \
      -d "{\"username\":\"admin\",\"password\":\"$ADMIN_IN_ENV\"}" 2>/dev/null \
      | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)"

  if [ -n "$TOKEN" ]; then
    AUTH=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')
    EXISTING="$(gw_lookup "$("${CURL[@]}" "${AUTH[@]}" "$API/loxilbs" 2>/dev/null || true)")"
    EX_ID="${EXISTING%% *}"; EX_HOST="${EXISTING#* }"

    if [ -z "$EXISTING" ]; then
      CODE="$("${CURL[@]}" "${AUTH[@]}" -o /dev/null -w '%{http_code}' -X POST "$API/loxilbs" -d "$GW_BODY" 2>/dev/null || true)"
      case "$CODE" in
        2*) ok "registered '$GW_NAME' → https://$GW_HOST:8091/netlox/v1" ;;
        *)  warn "could not register the gateway (HTTP $CODE) — add it in the console"
            note "  host $GW_HOST, port 8091, protocol https, version v1" ;;
      esac
    elif [ "$EX_HOST" = "$GW_HOST" ]; then
      ok "'$GW_NAME' already registered at https://$GW_HOST:8091/netlox/v1"
    else
      # A stale host here is the classic symptom: the console shows the instance
      # as Down because the old name no longer resolves and the gateway now
      # serves a certificate for the new one.
      CODE="$("${CURL[@]}" "${AUTH[@]}" -o /dev/null -w '%{http_code}' -X PUT "$API/loxilbs/$EX_ID" -d "$GW_BODY" 2>/dev/null || true)"
      case "$CODE" in
        2*) ok "re-pointed '$GW_NAME' from $EX_HOST to $GW_HOST" ;;
        *)  warn "'$GW_NAME' still points at $EX_HOST and could not be updated (HTTP $CODE)"
            note "Edit instance $EX_ID in the console and set Host to $GW_HOST" ;;
      esac
    fi
  else
    note "could not log in as admin, so the gateway was not registered."
    note "OAM_DEFAULT_ADMIN_PASSWORD only works against a FRESH database, and only"
    note "until someone changes the admin password."
    if confirm "Reset the admin password to the one in .env and retry?" n; then
      if docker exec loxilb-mgmt-oam-loxilb-1 ./reset_admin --confirm >/dev/null 2>&1; then
        ok "admin password reset — re-run this script to finish registering"
      else
        warn "reset_admin failed — run: docker exec loxilb-mgmt-oam-loxilb-1 ./reset_admin --confirm"
      fi
    fi
    note "Or add it in the console: host $GW_HOST, port 8091, protocol https, version v1"
  fi
fi

# ── 12. summary ──────────────────────────────────────────────────────────────
step "Done"
# SITE_ADDRESS may list several addresses; give each its own usable URL rather
# than gluing the path onto the end of the joined string.
__first=1
IFS=',' read -r -a __sites <<< "$SITE_ADDRESS"
for __s in "${__sites[@]}"; do
  __s="$(printf '%s' "$__s" | xargs)"
  [ -z "$__s" ] && continue
  if [ "$__first" = 1 ]; then
    printf '\n  %sConsole%s   %s/netlox/\n' "$B" "$N" "$__s"; __first=0
  else
    printf '            %s/netlox/\n' "$__s"
  fi
done
printf '  %sUser%s      admin\n' "$B" "$N"
printf '  %sPassword%s  %s\n' "$B" "$N" "$(grep -m1 '^OAM_DEFAULT_ADMIN_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
printf '\n'
note "Change the admin password after first login. Every other secret lives in"
note ".env (0600) — back it up somewhere safe; SNAPSHOT_ENC_KEY cannot be"
note "recovered, and without it stored snapshots cannot be decrypted."
printf '\n  %sUpgrading later%s\n' "$B" "$N"
note "management only, traffic untouched:  docker compose ${MGMT_FILES[*]} up -d --no-deps oam-loxilb"
note "gateway (a real traffic event):      docker compose ${DATA_FILES[*]} pull && docker compose ${DATA_FILES[*]} up -d"
note "database (explicit maintenance only): docker compose ${STATE_FILES[*]} up -d postgres"
if [ "$FAIL" -ne 0 ]; then
  printf '\n  %sSome checks did not pass%s — see the warnings above and\n' "$YEL" "$N"
  note "docs/deployment-converged.md section 9 (Troubleshooting)."
  exit 1
fi
