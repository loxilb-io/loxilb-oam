#!/usr/bin/env bash
# Regenerate the Kubernetes database-init ConfigMaps from the canonical schema.
#
# The schema lives in exactly one place (database/init/00-init-complete.sql).
# Kustomize cannot read a file outside its own directory under the default load
# restrictor, so each overlay needs its own committed copy — this script keeps
# those copies identical instead of letting them drift, which is how the
# production copy previously lost the system_config table.
set -euo pipefail
cd "$(dirname "$0")/.."

SCHEMA=database/init/00-init-complete.sql
[ -f "$SCHEMA" ] || { echo "missing $SCHEMA" >&2; exit 1; }

for d in k8s/base k8s/base-http k8s/overlays/development k8s/overlays/production; do
  out="$d/postgres-init-configmap.yaml"
  {
    cat <<'HEADER'
# GENERATED FILE — do not edit by hand.
# Regenerate with: make k8s-init-configmap
# The schema is single-sourced from database/init/00-init-complete.sql; the four
# hand-maintained copies this replaces had drifted badly (the production one had
# lost the system_config table the service requires).
apiVersion: v1
kind: ConfigMap
metadata:
  name: postgres-init-config
  namespace: oam-loxilb
  labels:
    app: postgres
    component: database-init
data:
  00-init-complete.sql: |
HEADER
    # Indent four spaces under the block scalar; keep blank lines truly blank so
    # trailing whitespace does not creep into the manifest.
    sed -e 's/^/    /' -e 's/^ *$//' "$SCHEMA"
  } > "$out"
  echo "regenerated $out"
done
