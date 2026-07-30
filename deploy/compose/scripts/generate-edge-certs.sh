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
SAN="${2:-DNS:${CN},DNS:localhost,IP:127.0.0.1}"
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
  EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem

To silence browser warnings, trust cert.pem on clients:
  macOS: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$DIR/cert.pem"
  Linux: sudo cp "$DIR/cert.pem" /usr/local/share/ca-certificates/loxilb-edge.crt && sudo update-ca-certificates
EOF
