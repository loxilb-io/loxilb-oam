#!/usr/bin/env bash
# Provision TLS for OAM → managed LoxiLB instances (plan §5).
#
# Creates a management CA once, then issues one server cert per instance signed
# by it. OAM trusts the CA via OAM_INSTANCE_CA_BUNDLE; each server cert/key is
# installed on its LoxiLB instance, which is started with `--tls`.
#
# Usage:
#   scripts/generate-instance-certs.sh <host-or-ip> [<host-or-ip> ...]
#   DAYS=825 scripts/generate-instance-certs.sh 192.0.2.10 lb2.internal
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <instance-host-or-ip> [more...]" >&2
  exit 1
fi

CERTS="$(cd "$(dirname "$0")/.." && pwd)/certs"
CA_DIR="$CERTS/instance-ca"
DAYS="${DAYS:-825}"
mkdir -p "$CA_DIR"

# ── Management CA (create once, reuse thereafter) ────────────────────────────
if [[ ! -f "$CA_DIR/ca.key" ]]; then
  echo "==> Creating management CA"
  openssl genrsa -out "$CA_DIR/ca.key" 4096
  openssl req -x509 -new -nodes -key "$CA_DIR/ca.key" -sha256 -days 3650 \
    -subj "/CN=LoxiLB Management CA" -out "$CA_DIR/ca.crt"
fi

# Publish the bundle OAM trusts (this path is what OAM_INSTANCE_CA_BUNDLE points at).
cp "$CA_DIR/ca.crt" "$CERTS/instance-ca.pem"

# ── Per-instance server certs ────────────────────────────────────────────────
for HOST in "$@"; do
  echo "==> Issuing server cert for ${HOST}"
  OUT="$CA_DIR/${HOST}"
  mkdir -p "$OUT"

  if [[ "$HOST" =~ ^[0-9.]+$ || "$HOST" == *:* ]]; then
    SAN="IP:${HOST}"
  else
    SAN="DNS:${HOST}"
  fi

  openssl genrsa -out "$OUT/server.key" 2048
  openssl req -new -key "$OUT/server.key" -subj "/CN=${HOST}" -out "$OUT/server.csr"
  openssl x509 -req -in "$OUT/server.csr" \
    -CA "$CA_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" -CAcreateserial \
    -days "$DAYS" -sha256 \
    -extfile <(printf 'subjectAltName=%s\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n' "$SAN") \
    -out "$OUT/server.crt"
  rm -f "$OUT/server.csr"
  chmod 600 "$OUT/server.key"

  echo "    server.crt / server.key -> $OUT"
done

cat <<EOF

Done.

On each LoxiLB instance:
  1. Copy that host's server.crt + server.key to /opt/loxilb/cert/
  2. Start loxilb with:
       loxilb --tls --tls-host=0.0.0.0 --tls-port=8091 \\
         --tls-certificate=/opt/loxilb/cert/server.crt \\
         --tls-key=/opt/loxilb/cert/server.key

In OAM (.env), trust the CA and turn verification on:
  OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
  OAM_INSTANCE_TLS_INSECURE=false

Then register each instance in the UI/API with endpoint:
  https://<host>:8091/netlox/v1
EOF
