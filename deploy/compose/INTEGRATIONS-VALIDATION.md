# Optional integration validation — 2026-10-16

[한국어](INTEGRATIONS-VALIDATION.ko.md)

This record covers optional dependency deployment in independent Compose
projects while keeping external Keycloak/SIEM integration as the default. Raw
audit logs, tokens, passwords, and certificate private keys remain in protected
testbed evidence storage and are not included here.

## Source and runtime scope

- OAM base: `f7b7525c2f2bc56536b6ff67ac666e1a5f242e38`, confirmed equal to main.
- Gateway contract: local `1d513937` compared with fetched main
  `40cdb72b8dbb57ec834436eac759a04939d5061b`; no differences in the core JWT/audit
  contracts reviewed. Runtime image provenance is recorded separately below.
- UI source: JWT/Audit navigation, inputs, and connectors at
  `6816f28d2985c63e956d6787cce916cd304198a9`. Existing user changes in the UI
  checkout were not modified.
- Linux x86_64 testbed, Compose v5.5.1. Clean replay used separate projects,
  ports, a new CA, new passwords, and new volumes; existing management/data/state
  projects were not modified.
- The final test Gateway used `--proxyonlymode`, `privileged=false`,
  `CapDrop=[ALL]`, and `no-new-privileges`. It did not share the production host
  network namespace.

| Image | Validated digest |
|---|---|
| Keycloak 26.8.0 | `sha256:27b3230fbabb8c9ff3c66c864cf09dcd8a41aca82e5100679d109ef0eeb05741` |
| PostgreSQL 18.6 | `sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336` |
| syslog-ng 4.8.3 | `sha256:0956c0f2a94bc5ad4ff88e36ac1f3f59d5bae29c906555139f6633045f213ddf` |
| Gateway (existing testbed image, reused in isolation) | image ID `sha256:ee119fcdb3665b13b02786b7348dce6c6eafc27f5e312269a13743ce83a1181b` |

The optional Compose files and `.env.example` pin the dependency digests above.
The Gateway image ID is not a source-commit attestation or qualification of a
new Gateway release.

## Results

| Check | Result and evidence level |
|---|---|
| Bilingual publication | Full English/Korean operator manuals and validation reports; command/JSON block, digest, section coverage, and local-link parity checked |
| Rehearsal source identity | Five Compose/template/preparation/test files in the PR matched the final Linux rehearsal files byte for byte |
| Repository gates before PR | With Go 1.26.7: gofmt, build, vet, make test, and golangci-lint 2.12.2 passed; repository-configured Gitleaks and Compose/Python checks passed |
| Default deployment | `config --services` lists only postgres, oam-loxilb, ui-assets, caddy; optional services do not start automatically |
| Independent deployment | Keycloak/DB and syslog-ng started as separate projects with loopback publication and external named volumes |
| Compose contracts | Independent Keycloak/SIEM configurations and dataplane+trust overlay passed configuration checks |
| Seed custody | Six unittests passed on macOS and Linux: explicit opt-in, invalid-host rejection, replay/incremental selection preserving secrets, CA/incomplete-pair rejection, explicit configuration-update requirement |
| Strict umask | New seeds generated with umask 077; public CA directory 0755, private directory 0700, individual read-only runtime files 0444; capability-free containers read them successfully |
| Keycloak TLS/issuance | CA-verified discovery, actual client_credentials token issuance, wrong client secret rejected with 401 |
| Gateway management authentication | Dedicated management token accepted, anonymous access 401; JWT profile save/readback matched |
| Successful inference | Valid JWT returned 200 and increased actual backend receipts by one |
| Denied inference | Missing, malformed, bad-signature, expired, wrong-issuer/audience tokens returned 401; missing model permission returned 403; every denial had zero backend receipt increase |
| Recovery | Restored profile accepted a valid JWT with 200 and one backend receipt |
| IdP outage/recovery | While Keycloak was paused, a **new** profile without cached keys and a temporary rule returned 503 with zero backend receipts; after unpause, the same new profile returned 200/+1; temporary rule deletion confirmed |
| Synthetic TLS collection | RFC5425 octet-count framing received; RAWMSG matched original RFC5424/JSON bytes; wrong CA/SAN and plaintext rejected |
| Actual Gateway collection | Durable management intent/result pairs matched by boot_id/seq, including active JSONL and compressed sealed segments |
| Actual byte integrity | At final acceptance: 186 unique receiver records / 186 local records; every JSON MSG matched original bytes and SHA-256 |
| Gateway sink TLS rejection/recovery | Wrong server_name and a CA bundle lacking the private issuer failed TLS; restoring trusted settings resumed connection and receipt |
| Collector outage/recovery | New management intent/result pairs remained local during downtime and arrived as backlog after collector restart |
| Disable/reconnect | New management records did not arrive while the sink was disabled; new records arrived after reconnecting |
| Persistence | Separate Keycloak/DB and collector down/up cycles replaced container IDs while preserving realm/client secret/token issuance and existing receipts |
| Rehearsal cleanup | Ownership labels checked; test Gateway configuration/logs archived privately; only test containers/projects removed; external volumes, seeds, and evidence retained |
| Existing production services | Caddy, OAM, Gateway, and state PostgreSQL retained their 2026-10-14 start times; all remained running and OAM healthy |

Receiver records were read before local records to avoid a concurrent-append
race. Gateway `submitted` counters and successful socket writes were not used as
proof of receiver persistence.

## Negative evidence and repairs

Initial failure logs were retained with the protected evidence bundle.

- A valid JWT received 503 when the test environment lacked the AI tenant policy
  DB. A dedicated test policy DB was provisioned and the dependency documented.
- Restricted public-CA directory access caused JWKS verification failure. Public
  CA mount-root read/search permissions were corrected while retaining private
  directory protection, then verified.
- Under a fresh umask 077 environment, syslog-ng could not read its configuration
  bind. A protected 0444 runtime copy fixed this; actual receipt and regression
  tests passed under the same conditions.
- An initial byte comparison omitted compressed sealed segments. The comparison
  was extended to all original segments and actual management intent/result pairs.
- Gateway management HTTP readiness precedes AI policy DB/keyset readiness.
  Startup checks bounded not-ready 503/zero-receipt behavior to 15 seconds and
  verified ready 200/+1 separately.

## Repeatable repository checks

```bash
python3 deploy/compose/scripts/test_prepare_integrations.py

docker compose --env-file /dev/null \
  -f deploy/compose/docker-compose.keycloak.yml config --quiet
docker compose --env-file /dev/null \
  -f deploy/compose/docker-compose.siem.yml config --quiet

git diff --check
```

Follow [INTEGRATIONS.md](INTEGRATIONS.md) for the runtime rehearsal sequence:
preparation → independent startup → TLS/claims → Gateway configuration/readback →
inference/backend receipts → original/received audit comparison → outage/recovery →
down/up persistence. The dedicated runtime harnesses and per-stage logs remain
in the protected testbed evidence bundle.

## Qualification limits

- UI navigation and labels were reviewed against source. This change does not
  claim actual browser creation/rejection/recovery or screenshot validation.
- Full Linux eBPF/host-network dataplane, performance, Kubernetes/HA, external
  commercial SIEM indexing/analytics/alerting, mTLS client authentication,
  organizational CA operations, and release CI remain separate qualification.
- The test policy DB was a separate database in an isolated rehearsal PostgreSQL
  instance. Production Gateway uses the existing converged AI-key-store
  role/schema; test DB owner privileges do not establish production least privilege.
- TLS syslog has no application ACK. Observed byte equality and backlog recovery
  do not guarantee loss-free or duplicate-free delivery under every failure.
