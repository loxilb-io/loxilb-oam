# Named-volume provisioning validation

Runtime exercise on 2026-10-14; PR review/source checks on 2026-10-14~15 KST. These are author-operated checks, not independent QA,
release qualification, or a production deployment approval. Protected host logs,
credentials, dumps, certificates and receiver records remain outside public Git.

## Current-main review and source checks

The implementation was reviewed against `main` at
`910596b99fd18a3e23331bdaa6672f912cd38e5b`. Changes are confined to this
Compose directory. Existing state/data/management project separation,
host-network Gateway, audit permission initialization, TLS verification and
service-token wiring are retained. The OAM application and database schema are
unchanged.

- Shell syntax and Python compilation checks passed.
- Ten standard-library regression tests passed. They cover missing/empty TLS
  pairs, automatic edge TLS without a supplied pair, nested signing-key
  rejection, public-anchor policy, symlinks, empty credentials, aliased volumes,
  and changed nested/hidden certificate files with shell-sensitive names.
- `gofmt -l .` produced no output; `go build ./...`, `go vet ./...`, `make test`
  and `golangci-lint run --timeout=5m ./...` passed with Go 1.26.9 and
  golangci-lint 2.12.2 built using that toolchain. Initial attempts with a
  Go 1.25 override could not run against the existing Go 1.26 module; the
  matching toolchain was selected without changing the user's global tools.

Run the deployment regressions without Docker:

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/compose/scripts -p test_prepare_converged_volumes.py -v
```

## Reviewed provisioning source: isolated Docker checks

Four Compose combinations rendered successfully: data, state,
base + prod + converged, and base + prod + converged + local-ui. The reviewed
preparation tool ran against newly named test volumes, without starting any
production project.

- Initial seed and second-run preservation: unchanged file fingerprints.
- Mismatched token seed: refused, persistent contents unchanged.
- Missing host token seed: recovered from the authoritative volume at 0600.
- Refresh with a running consumer: refused, persistent contents unchanged.
- Refresh after stopping the owned consumer: only the selected token volume
  changed; protected backups were created; the original test token was restored.
- Partially initialized empty credential stores: recovery continues without
  inventing an authoritative credential. A nonempty store missing its expected
  credential still refuses recovery.
- Changed nested/hidden CA anchor: refused; restoring the source allows a
  matching rerun. Config/database volume aliasing is refused before writes.

All old test resources and evidence were retained. New test consumers were
stopped and retained after refresh tests; no volume deletion/pruning was used.

## Separate Compose upgrade/rollback exercise

The preceding isolated bridge-network exercise used candidate Gateway sources
`c70ac070` and `69125b3e`, independent named stores and a copied database.
It did not start the production host-network manifests in this directory.

| Phase | Request probes | Independently received Audit records |
|---|---:|---:|
| Previous image, schema 1.9 | 6 | 6 |
| New candidate, schema 1.11 | 6 | 6 |
| Backend client CA refusal | 1 | 1 |
| CA recovery | 6 | 6 |
| Previous image with incompatible new config | 1 | 0 |
| Previous image with pre-upgrade config restored | 6 | 6 |
| PostgreSQL/OAM recreation with the same stores | 6 | 6 |
| Final new-candidate reapplication | 6 | 6 |
| Total | 38 | 37 |

A separate final recheck passed six probes and six Audit receipts. The counts
are separate from the 38/37 exercise. Normal and SSE requests each produced two
Audit records; HTTP key/model denials produced one each. Frontend handshake
refusals occurred before HTTP and produced no request Audit. Backend TLS refusal
returned 502, delivered no backend request, and produced one received Audit
record. Receiver MSG hashes matched the local records; backend receipt and
verified client identity were checked independently.

Gateway, PostgreSQL and OAM container IDs changed while volume identities,
mount paths/modes, protected file bytes, desired certificate IDs/digests and
authentication DB fingerprints remained equal. Existing OAM login and outbound
linkage still worked. Original unrelated container identities/configuration and
running states were preserved.

## Upgrade and rollback limits

- The old candidate rejected schema 1.11: readiness 503, no restored rules and
  no inference forwarding. Successful rollback required **both** the old image
  and its pre-upgrade config archive, including managed certificate files and
  `snapshot-node.secret`. Failure/quarantine evidence and Audit were retained.
- A volume retains new writes; it is not a backup or an automatic schema
  rollback. DB migrations require a separate compatibility/restore decision.
- PostgreSQL 18.6 **same-image recreation** was exercised. Major version upgrades
  need a tested `pg_upgrade` or dump/restore procedure.
- `.env` remains necessary for supported environment-only inputs, including
  JWT/vault encryption keys. Volume retention does not restore missing env
  keys, reverse token expiry/revocation, or rotate a database role password.
- Refresh is a stopped-consumer operation with a backup, not an atomic online
  certificate/token rotation. Run provisioning serially, preserve extra files,
  and verify required filenames/UIDs/paths when selecting a new image.
- Local volumes do not replicate between hosts. No zero-downtime, arbitrary
  release compatibility, production host NIC/eBPF topology, or real LLM/GPU
  qualification is claimed.

See [PERSISTENT-UPGRADE.md](PERSISTENT-UPGRADE.md) for the operator procedure.
