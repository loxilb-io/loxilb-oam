#!/usr/bin/env bash
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SNAPSHOT="$COMPOSE_DIR/database/aigw-db-bootstrap.sql"
GATEWAY_ROOT="${1:-$COMPOSE_DIR/../../../loxilb-inference-gateway}"
CANONICAL="$GATEWAY_ROOT/scripts/aigw-db-bootstrap.sql"
EXPECTED_SHA256="569d940f0fdd995061b13a51e23434b428266e162e8cf162e5ba74cd1457a2f5"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SHA256="$(sha256sum "$SNAPSHOT" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL_SHA256="$(shasum -a 256 "$SNAPSHOT" | awk '{print $1}')"
else
  printf 'ERROR: sha256sum or shasum is required\n' >&2
  exit 1
fi

if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
  printf 'ERROR: bundled bootstrap checksum drifted\n' >&2
  printf 'expected: %s\nactual:   %s\n' "$EXPECTED_SHA256" "$ACTUAL_SHA256" >&2
  exit 1
fi

if [ ! -f "$CANONICAL" ]; then
  printf 'ERROR: Gateway bootstrap not found: %s\n' "$CANONICAL" >&2
  printf 'Pass the loxilb-inference-gateway checkout path as argument 1.\n' >&2
  exit 1
fi

if ! cmp -s "$SNAPSHOT" "$CANONICAL"; then
  printf 'ERROR: bundled bootstrap differs from Gateway canonical source\n' >&2
  diff -u "$SNAPSHOT" "$CANONICAL" || true
  exit 1
fi

printf 'PASS: bundled bootstrap matches %s\n' "$CANONICAL"
