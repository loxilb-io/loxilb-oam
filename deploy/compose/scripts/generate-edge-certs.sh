#!/usr/bin/env bash
# Generate a self-signed edge certificate for the Caddy edge (the ★ default
# TLS path). Writes certs/edge/{cert,key}.pem, which EDGE_TLS points at.
#
# Usage:
#   scripts/generate-edge-certs.sh [CN] [SAN] [DAYS]
#     CN   common name / primary hostname   (default: oam.local)
#     SAN  subjectAltName list              (default: DNS:<CN>,DNS:localhost,IP:127.0.0.1)
#     DAYS validity in days                 (default: 825)
#
# For an OFFICIAL / commercial cert instead, skip this script and drop your CA's
# cert.pem (leaf + intermediates concatenated) and key.pem into certs/edge/.
set -euo pipefail

CN="${1:-oam.local}"

# An IP has to appear in the SAN list as IP:, never DNS: — a DNS entry holding a
# dotted quad matches nothing and the browser rejects the certificate outright.
# Deployments reached only by address (no DNS name at all) are common, so detect
# that here rather than leaving the caller to discover it from a TLS error.
if [[ "$CN" =~ ^[0-9]+(\.[0-9]+){3}$ || "$CN" == *:*[0-9a-fA-F]* ]]; then
  DEFAULT_SAN="IP:${CN},DNS:localhost,IP:127.0.0.1"
  CN_IS_IP=1
else
  DEFAULT_SAN="DNS:${CN},DNS:localhost,IP:127.0.0.1"
  CN_IS_IP=0
fi

SAN="${2:-$DEFAULT_SAN}"
DAYS="${3:-825}"

DIR="$(cd "$(dirname "$0")/.." && pwd)/certs/edge"
mkdir -p "$DIR"

openssl req -x509 -newkey rsa:2048 -nodes -days "$DAYS" \
  -keyout "$DIR/key.pem" -out "$DIR/cert.pem" \
  -subj "/CN=${CN}" -addext "subjectAltName=${SAN}"

chmod 600 "$DIR/key.pem"
chmod 644 "$DIR/cert.pem"

cat <<EOF

Wrote:
  $DIR/cert.pem
  $DIR/key.pem
  (CN=${CN}, SAN=${SAN}, ${DAYS} days)

Set in .env:
  SITE_ADDRESS=https://${CN}
  EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem$(
  if [ "$CN_IS_IP" = 1 ]; then printf '\n  EDGE_SNI_FALLBACK=default_sni %s' "$CN"; fi)$(
  if [ "$CN_IS_IP" = 1 ]; then printf '\n\nReached by address, so clients send no SNI — the EDGE_SNI_FALLBACK line above\nis required, not optional. No public CA will issue for a private address, so\ntrust cert.pem on each client (or issue the edge cert from your own CA).'; fi)

To silence browser warnings, trust cert.pem on clients:
  macOS: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$DIR/cert.pem"
  Linux: sudo cp "$DIR/cert.pem" /usr/local/share/ca-certificates/loxilb-edge.crt && sudo update-ca-certificates
EOF
