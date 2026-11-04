# Compose bundle validation

Author-operated checks of this Compose bundle. They are not independent QA, a
security qualification, or a production deployment approval. Protected host
logs, credentials, dumps, certificates and screenshots remain outside public Git.

## v0.9.9.1 release smoke — 2026-11-04

The final cross-repository check of the v0.9.9.1 release train: the published,
signed images, deployed with this bundle exactly as released. Nothing was
built from source.

### Scope and environment

- Bundle: `deploy/compose` at tag `v0.9.9.1` (`d84fb31`), installed with
  `scripts/init-converged.sh -y` (converged single node: shared state,
  management plane and the Gateway data plane). The persistent volume names
  were given a test prefix through the documented environment overrides.
- Image set: the pinned defaults from `.env.example`
  (`OAM_TAG=UI_TAG=v0.9.9.1`, `IGW_TAG=v0.9.9.1-u24`). No `:latest` run; the
  Gateway release already showed `:latest` resolves to the same images.
- Host: a single-NIC Ubuntu 22.04.5 VM (kernel 5.15), Docker Engine 29.7.2,
  Docker Compose 5.5.0. The Gateway ran with the standard privileged,
  host-network definition.

Image digests the running containers resolved to:

| Component | Image | Digest |
|---|---|---|
| OAM | `ghcr.io/loxilb-io/loxilb-oam:v0.9.9.1` | `sha256:aaf91a8010a391a7824c97c797c2d92ebd59ff0905e8146425a6d68f06baea6d` |
| UI | `ghcr.io/loxilb-io/loxilb-ui:v0.9.9.1` | `sha256:0c19ae56d2c7022cddc7bff1d67676e0704fe0f7663723a79a3974963f849359` |
| Gateway | `ghcr.io/loxilb-io/loxilb-inference-gateway:v0.9.9.1-u24` | `sha256:b77dc1e3c2e1c0e2545e3cec2c64b0b22b80082fa35eeeab558880e73bbd12b6` |
| PostgreSQL | `postgres:18.6` | `sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280` |
| Edge | `caddy:2-alpine` (floating tag) | `sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f` |

The OAM, UI and Gateway digests are the published manifest lists. The OAM
image's `:v0.9.9.1` and `:latest` resolved to the same digest, and Cosign v3
verified its signature. `go version -m` reported go1.26.10 for the image
binaries and go1.27.3 for the release tarball binaries (`vcs.revision`
`d84fb31`, `SHA256SUMS` OK).

### Results

| Check | Result |
|---|---|
| Initializer | Exit 0, all built-in checks passed: one database with isolated Gateway roles, PostgreSQL on loopback only, OAM healthy in service-token mode, Gateway plaintext API on loopback only, TLS API on `:8091`, anonymous management request 401, datapath kept off Docker interfaces, Gateway certificate verified against the management CA, OAM to Gateway anonymous 401 / authenticated 200, every console identity serving UI and OAM health, `local-gateway` registered. |
| Edge health | `/healthz`, `/api/oam/health` and `/netlox/` returned 200 with certificate verification against the edge certificate. |
| Versions | `loxilb-oam -version`: `v0.9.9.1`. UI login page and header: `0.9.9.1`. Gateway `/version`: `v0.9.9.1`. `loxicmd version` inside the Gateway: `v0.9.9.2`, build `1d9e946`. |
| Console, end to end | Headless Chromium (Playwright) against the deployed edge: the fresh install's admin-credential update, sign-in, the instance list showing `local-gateway`, its dashboard, then an LB rule (`203.0.113.91:18091` to `198.51.100.91`) created through the console form (HTTP 200), shown in the table, read back from the Gateway API with matching fields, deleted through the console by name, and gone from both the table and the Gateway. No firewall rules were left behind and the browser logged no console errors. |
| Placeholder-secret guard | An extra OAM container started from the same image with `OAM_JWT_SECRET=CHANGE_ME` exited with status 1, and `docker logs` showed `main.requireSecrets: SECURITY: secret(s) hold a placeholder or comment instead of a real value: OAM_JWT_SECRET`. The running OAM stayed healthy. |
| Teardown | All three projects were brought down, and the test volumes and host directory removed. Host addresses, routes, policy rules, the bpffs tree, volumes and listening ports matched the pre-test snapshot. |

### Observations and limits

- The installer's fixed Compose project names (`loxilb-state`, `loxilb-data`,
  `loxilb-mgmt`) collide with any other converged stack on the same host. The
  host held a stopped stack under those names; its volumes were backed up and
  restored byte for byte, and its container recreated from its own definition.
  A second stack on one host is not supported until this is addressed
  ([#108](https://github.com/loxilb-io/loxilb-oam/issues/108)).
- eBPF programs stay attached to the host NIC after the Gateway project is
  brought down. They were present before this test from an earlier run, and
  identical programs were attached afterwards. This is Gateway behaviour.
- `caddy:2-alpine` is the only third-party image still referenced by a
  floating tag.
- Not covered: inference traffic and eBPF forwarding, HA, IPv6, upgrade from a
  previous release, external IdP/SIEM, and access from outside the host.

## Named-volume provisioning validation — 2026-10-14

Runtime exercise on 2026-10-14; PR review/source checks on 2026-10-14~15 KST. These are author-operated checks, not independent QA,
release qualification, or a production deployment approval. Protected host logs,
credentials, dumps, certificates and receiver records remain outside public Git.

### Current-main review and source checks

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

### Reviewed provisioning source: isolated Docker checks

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

### Separate Compose upgrade/rollback exercise

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

### Upgrade and rollback limits

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
