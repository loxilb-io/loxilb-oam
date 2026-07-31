#!/usr/bin/env bash
#
# coverage-gate.sh — statement-coverage floor gate (blocking).
#
# Reads a Go coverage profile and fails if the total statement coverage is
# below MIN. This is a RATCHET, not a target: it exists to stop coverage from
# regressing. Raise the floor as tests land (direction of travel: 30%+ once the
# HTTP layer is more fully covered / integration coverage is folded in).
#
# Usage: coverage-gate.sh <profile> <min-percent>
#   profile      path to a `go test -coverprofile` output (default coverage.out)
#   min-percent  integer or float floor, e.g. 15 or 15.5 (default 0)
#
set -euo pipefail

PROFILE="${1:-coverage.out}"
MIN="${2:-0}"

if [ ! -f "$PROFILE" ]; then
  echo "coverage-gate: profile not found: $PROFILE" >&2
  exit 1
fi

# `go tool cover -func` prints a trailing "total: ... NN.N%" line.
total="$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/,"",$NF); print $NF}')"
if [ -z "$total" ]; then
  echo "coverage-gate: could not parse total coverage from $PROFILE" >&2
  exit 1
fi

echo "Total statement coverage: ${total}% (floor: ${MIN}%)"

below="$(awk -v t="$total" -v m="$MIN" 'BEGIN { print (t + 0 < m + 0) ? 1 : 0 }')"
if [ "$below" = "1" ]; then
  echo "::error::coverage ${total}% is below the required floor ${MIN}%"
  exit 1
fi

echo "coverage-gate: PASS"
