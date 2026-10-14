# Converged Compose persistence, upgrade and rollback

The `loxilb-state`, `loxilb-data` and `loxilb-mgmt` projects remain independent.
Gateway config (including managed certificate material and `snapshot-node.secret`),
PostgreSQL, Audit, service token, database credential files, Gateway TLS leaf
material, OAM public CA bundle and edge TLS material now use explicitly named
external volumes. Runtime mounts use `nocopy`; missing volumes stop deployment
rather than selecting a new store implicitly. Certificate/token consumers mount
those stores read-only; Gateway config and Audit remain writable.

`.env` still supplies the supported environment-only inputs, including OAM JWT
and Snapshot encryption keys and the PostgreSQL/OAM database password. Preserve
and back up this protected file (0600) too. Mounting a file is not a substitute
for an environment contract the application does not implement.

## First provisioning or adoption of existing host binds

1. Preserve `.env`, all current Compose manifests, image digests, `docker inspect`
   records, a logical PostgreSQL backup, Gateway Snapshot and a separate config
   directory backup. Snapshot `cert` entries contain IDs/digests, not CA/key files.
2. Set the stable volume identities in `.env.example`. Keep the existing
   `CONVERGED_PG_VOLUME` and Audit volume name; never initialize another database
   store accidentally. `IGW_CONFIG_DIR` is now a seed/backup source only.
3. Prepare protected seed credentials and leaf certificates through
   `scripts/init-converged.sh --no-start`, or the existing certificate scripts.
   Existing-volume adoption requires a maintenance window: stop the service
   writing the source config bind before it is copied. The preparation tool
   refuses a running source bind consumer or a running target volume consumer.
   It does not stop any service for you.
4. Run the volume preparation tool. It creates missing named volumes, seeds
   empty stores, preserves existing config, and compares existing credentials
   and certificate files against the seed inputs. A mismatch stops the tool;
   it never silently rotates a token or certificate. CA signing keys are refused
   from the service certificate seed tree. OAM receives only its public CA bundle.

```bash
./scripts/prepare-converged-volumes.py
```

5. Use the existing three-project startup sequence in README.md. The state
   bootstrap reads role-specific read-only credential volumes. The shared token
   volume is mounted by Gateway and OAM at the same `/run/secrets` directory.
   Config, TLS and token material is not stored in a container writable layer.

The initializer calls `--restore-credentials` before generating seed credentials:
if a host seed file was lost but its volume exists, the original value is recovered
without printing it. If both exist and differ, provisioning stops. Empty stores
left by interrupted first preparation can be retried; a populated store missing
its expected credential is refused. The original
source files are retained; nothing is pruned or deleted.

Use distinct volume names for every role and run one provisioning operation at
a time. Python 3 and a Docker Compose version supporting the existing
`!override`/`!reset` overlay tags are required. Preparation is a maintenance
operation, not a transactional online rotation.

## Upgrade only the Gateway

Pin an approved immutable reference with `IGW_IMAGE_REF` (a complete image/digest
reference). The older `IGW_IMAGE`/`IGW_TAG` inputs remain the fallback.

1. Check live requests and Audit receipt. Record the current resolved image,
   volume **names, destinations and read/write modes**, commands, environment,
   ports, network and privilege settings. Protect unredacted `inspect/config`
   output because it can contain environment credentials.
2. Persist the Gateway configuration, create/pin a UI Snapshot, and make a
   separate pre-upgrade config-volume archive while its writer is stopped.
   Preserve DB backup, TLS/token volume archives and `.env`.
   A live filesystem copy is not a consistent PostgreSQL backup.
3. Set the new `IGW_IMAGE_REF`; keep every volume identity and internal path.
   Validate the rendered Compose model privately, then recreate only Gateway:

```bash
docker compose -f docker-compose.dataplane.yml config --quiet
```

```bash
docker compose -f docker-compose.dataplane.yml up -d --no-deps gateway
```

4. Check strict readiness, schema/desired-state readback, certificates and their
   digests, mTLS client/server verification, service token and original inference
   key. Test normal HTTP/SSE, missing/wrong frontend client certificate, key/model
   denial, independent backend receipt and independent Audit receiver hashes.
   Container liveness or a UI Healthy badge alone does not finish the upgrade.
5. Keep pre-upgrade files and images until rollback is no longer required. Do not
   run two Gateway versions writing the same config/Audit volumes concurrently.
   Application DB schema changes need their own compatibility/rollback decision.

Management-only image changes use the management project and `--no-deps`; they
must not restart Gateway or PostgreSQL. PostgreSQL major upgrades require their
own `pg_upgrade` or dump/restore procedure; volume retention alone is insufficient.

## Rollback

A volume persists **new writes too**. Switching only to the previous image does
not recover the previous schema. In the UC-5 candidate test, schema 1.11 was
refused by the previous schema-1.9 Gateway, which quarantined the snapshot,
reported not-ready, and did not forward requests.

1. Stop the failed Gateway service; preserve its current config/archive, logs,
   quarantined snapshot and Audit. Leave database/management projects running
   unless their independent rollback plan requires otherwise.
2. If the old image cannot read the new config, restore the protected pre-upgrade
   config archive into the **same owned config volume**, while Gateway is stopped.
   This includes managed certificate material and `snapshot-node.secret`.
   Do not overwrite the Audit volume: its records/cursors are retained separately.
3. Restore the old image reference and pre-upgrade deployment settings. Recreate
   only Gateway; repeat readiness, mTLS, token/key, requests and Audit checks.
4. If a DB migration is incompatible, follow the separate tested DB restore plan.
   Do not run two PostgreSQL servers on one data volume.

Ordinary `down` retains external volumes; never use destructive volume cleanup
as an upgrade/rollback step. The deployment tool has no prune/delete operation.

## Explicit certificate/token refresh

A seed-file change does not automatically update an authoritative volume. Back
up the existing material and stop **all** consumers of the affected role, then
refresh only that role. The tool keeps a protected host backup before writing.

```bash
./scripts/prepare-converged-volumes.py --refresh gateway-tls
```

Other roles: `instance-ca`, `edge-tls`, `gateway-token`, `aigw-password`,
`mgmt-password`. Restart the affected consumers afterward and verify actual
connections. Token contents surviving in a volume do not override server-side
session expiry/revocation. A database-role password change needs a coordinated
DB/application procedure; refreshing its file alone does not change PostgreSQL.

## Validation boundary

UC-5 isolated bridge-network Compose verification covered candidate c70ac070 →
69125b3e, same-volume rollback using pre-upgrade config, PostgreSQL 18.6
same-image recreation, OAM recreation, frontend/backend mTLS, preserved token
and certificate bytes, actual backend receipts and Audit receipt. It did not
qualify arbitrary releases, PostgreSQL major upgrades, zero downtime or the
production host-network/NIC topology. Production Compose keeps its existing
host-network and three-project contracts.

The source checks, isolated Docker checks and request/Audit results are recorded
in [VALIDATION.md](VALIDATION.md).
