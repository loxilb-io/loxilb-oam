#!/usr/bin/env bash
# One Go toolchain for every build. Fails when:
#   - go.mod has no `toolchain goX.Y.Z` line,
#   - the Dockerfile build stage is not golang:X.Y.Z-alpine@sha256:<digest> of that same release,
#   - a workflow selects Go by `go-version:` instead of `go-version-file: go.mod`.
set -euo pipefail
cd "$(dirname "$0")/../.."

want="$(sed -n 's/^toolchain go\([0-9][0-9.]*\)$/\1/p' go.mod)"
[ -n "$want" ] || { echo "go.mod: no 'toolchain goX.Y.Z' line"; exit 1; }

fail=0
from="$(grep -m1 -E '^FROM golang:' Dockerfile || true)"
if ! printf '%s\n' "$from" | grep -Eq "^FROM golang:${want//./\\.}-alpine@sha256:[0-9a-f]{64}( |$)"; then
  echo "Dockerfile: build stage must be 'FROM golang:${want}-alpine@sha256:<digest>', found: ${from:-<none>}"
  fail=1
fi
if grep -nE '^\s*go-version:' .github/workflows/*.yml; then
  echo "workflows: select Go with 'go-version-file: go.mod', not go-version"
  fail=1
fi
[ "$fail" = 0 ] && echo "check-go-pin: OK - go${want} in go.mod, Dockerfile and workflows"
exit "$fail"
