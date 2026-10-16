# External Keycloak and SIEM integration with optional deployment

[한국어](INTEGRATIONS.ko.md)

The default configuration uses Keycloak and SIEM systems already managed by the
operator. The existing `init-converged.sh` and management/state/data Compose
commands do not start these dependencies. Start the independent projects below
only when you choose to deploy them locally. Keycloak authenticates **inference
API Bearer requests**. OAM login accounts/JWTs and the OAM-to-Gateway service token
remain separate identities; this configuration does not enable console SSO.

| Choice | Compose file | Storage | Default host publication |
|---|---|---|---|
| Existing external systems (default) | No additional project | Managed externally | External endpoints |
| Keycloak only | `docker-compose.keycloak.yml` | Dedicated PostgreSQL external volume | `127.0.0.1:18443` HTTPS |
| Audit collector only | `docker-compose.siem.yml` | External receipt-log and collector-state volumes | `127.0.0.1:16514` TLS syslog |
| Both | Start the two files **separately** | As above | As above |

syslog-ng receives and stores audit logs for SIEM ingestion. It is a collector;
it does not supply a SIEM search interface, correlation engine, or alerting.
Skip this collector when your external SIEM accepts TLS syslog directly. HTTP
HEC, Beats, and plain TCP/UDP inputs are not directly compatible with the
Gateway's TLS syslog sink. Configure an RFC5424/RFC5425 input or a suitable
adapter for those products.

## 1. Connect an external Keycloak instance (default)

1. Obtain the realm issuer, discovery/JWKS URL, audience, tenant/model authorization
   claims, and server certificate chain from the IdP operator. For example:
   `https://id.example.internal/realms/inference`, audience `inference-api`.
2. Verify DNS, HTTPS reachability, and CA trust from the Gateway's execution
   environment. Opening the URL in a browser does not prove Gateway connectivity.
   `issuer` must exactly match the token's `iss`, including its port and path.
   If `jwks_url` is omitted, the Gateway uses OIDC discovery from the issuer.
   A publicly trusted CA normally requires no additional trust mount.
3. For a private CA, place **only public CA certificates** in a dedicated
   directory. During planned data-plane maintenance, append
   `docker-compose.integration-trust.yml` after the data-plane file, set
   `INTEGRATION_CA_DIR` to that directory, and recreate the Gateway. This step
   restarts the Gateway and can interrupt traffic; schedule it separately from
   a routine OAM upgrade. Preserve any existing extra trust directories in the
   overlay's `SSL_CERT_DIR`.
4. Select the instance in the UI and open **AI Gateway → JWT Auth Profiles**.
   As an administrator, save the complete profile, then refresh and check the
   stored values. Example API body:

   ```json
   {
     "name": "external-keycloak",
     "issuer": "https://id.example.internal/realms/inference",
     "jwks_url": "https://id.example.internal/realms/inference/protocol/openid-connect/certs",
     "audiences": ["inference-api"],
     "algs": ["RS256"],
     "tenant_claim": "tenant_id",
     "roles_claim": "realm_access.roles",
     "model_role_prefix": "model:",
     "model_authz": "claims-required"
   }
   ```

   Write: `POST /netlox/v1/config/ai/jwtauthprofile`; read back:
   `GET /netlox/v1/config/ai/jwtauthprofile`. Both require Gateway management
   authentication. Configure realm/client mappers and user/service-account
   permissions so that the actual access token contains `tenant_id`, the
   audience, and `model:<model-name>` roles. Empty audiences skip audience
   validation, so use an explicit audience for production. Never print tokens
   or client secrets in logs.
5. Under **Traffic → LB Rule**, set the target AI service's `api_key_auth` to
   `jwt` and `jwt_auth_profile` to the profile name above. `apikey-or-jwt`
   supports both API keys and Bearer tokens; a present API key takes precedence.
   `required` is API-key-only. Read the LB rule back after saving. JWT tenant/user
   policy checks also require the Gateway's AI-key/tenant PostgreSQL store:
   verify the converged DB connection and schema `aigw`. Providing only an IdP
   and profile without this store can cause even valid JWTs to receive 503.
   The consumed Bearer credential is not forwarded upstream by default.
6. Verify a valid token returns 200 with an actual backend receipt. Missing,
   expired, bad-signature, wrong-issuer, and wrong-audience tokens should return
   401; missing model permission should return 403. Every denied request must
   leave the backend receipt count unchanged. During JWKS refresh outages,
   previously usable keys can remain available; an IdP outage does not imply
   immediate rejection of all requests. A new profile without usable keys must
   fail closed with 503.

The navigation and labels below were checked against source. If the deployed
UI lacks a menu, check the UI/Gateway versions and capabilities first. Record
menu availability separately from actual browser acceptance testing.

Main JWT profile inputs (English UI):

| UI field | Required/optional | Representative value and meaning |
|---|---|---|
| Name | Required | `external-keycloak`; LB reference, maximum 63 bytes |
| Issuer | Required | Realm URL; exact match for token `iss` |
| JWKS URL | Optional | Public-key URL; blank uses issuer discovery |
| Audiences | Optional; set explicitly in production | `inference-api`; comma-separated values |
| Signature algorithms | Optional | `RS256`; match the realm signing algorithm |
| Model authorization | Optional | Default `claims-required`; refuse tokens without model permission |
| Tenant claim | Optional | Default `tenant_id`; match the actual token claim |
| Roles claim / Model role prefix | Optional | `realm_access.roles` / `model:` |
| Models claim | Optional | Dot-path to an allowed-models array; blank derives permissions from roles |
| Default tenant | Optional | Blank by default; tokens without a tenant claim receive 401 |

After saving, check the list and `Keyset health`. A new profile or changed key
configuration can return 503 until its first JWKS fetch succeeds. Re-saving an
identical configuration does not necessarily discard existing usable keys.
A configuration row alone does not prove keyset readiness.

## 2. Connect an external SIEM (default)

1. Obtain the TLS syslog address (`siem.example.internal:6514`), certificate SAN,
   trusted CA, and, if required, a Gateway mTLS client certificate/key. The
   receiver must support RFC5424 and RFC5425 octet-count framing.
2. Place CA/client files at **paths readable inside the Gateway container**.
   Do not enter paths on the OAM host or browser workstation. Use the existing
   Gateway configuration volume or a reviewed read-only bind. Do not deploy the
   CA private key to the Gateway.
3. Configure **Maintenance → Audit Trail → Compliance sink**. Administrators can
   write; operators can read; viewers have restricted access to these settings.

   ```json
   {
     "enabled": true,
     "address": "siem.example.internal:6514",
     "ca_bundle_path": "/etc/loxilb/siem-ca.crt",
     "server_name": "siem.example.internal",
     "client_cert_path": "",
     "client_key_path": ""
   }
   ```

   Write/read: `POST`/`GET /netlox/v1/audit/sink`. A successful write returns
   HTTP 204 without a body; a successful read returns HTTP 200. For mTLS,
   supply both client certificate and key paths. Certificate verification
   cannot be disabled. Named sinks use `/audit/sinks/{name}` and require an
   enterprise number assigned to your organization. Do not use documentation
   number `32473` as your production number.
4. Check `GET /audit/status`, sink readback, and the new event's **receiver file,
   record identity, and hash** together. `connected`, transmission counters, or
   successful TCP writes do not prove SIEM persistence. Match original Gateway
   JSONL and received JSON MSG by `boot_id`/`seq` (and `segment_uuid` if needed),
   then compare SHA-256 over the original bytes excluding the trailing LF.
   Do not assume the record contains a hash field.
5. Verify rejection of wrong CA/SAN, local recording during receiver downtime,
   backlog delivery after recovery, preservation across Gateway/receiver
   recreation, and cessation of new exports after disabling the sink. TLS
   syslog has no application ACK: account separately for duplicates, replay,
   and possible loss at the receiving end. Records already removed by Gateway
   retention cannot be recovered automatically.

Use `Configure` initially or `Change` for an existing compliance sink, enter the
settings, select `Save`, then `Refresh` to read back. `Stop` removes the sink
configuration; it does not pause the sink while retaining its settings.

| UI field | Required/optional | Optional collector example and meaning |
|---|---|---|
| Receiver address (host:port) | Required | `127.0.0.1:16514`; reachable from the Gateway |
| CA bundle path | Required | `/run/integration-ca/integration-ca.crt`; Gateway container path |
| Server name in the certificate | Optional | `localhost`; blank verifies against the address host |
| Client certificate path / Client key path | Optional pair | Supply both only when using mTLS |
| Syslog facility | Optional | Blank selects default 13 |
| Largest message (bytes) | Optional | Blank means unlimited; a small limit can shorten records and affect byte comparisons |

After saving, inspect `Sink the gateway holds now`, `Session established`, and
`Last error`. `Records written to the socket` is not a count of records persisted
by the collector: inspect the receiver files separately. Keyset health and audit
sink health are independent.

## 3. Prepare optional deployment

Run from `deploy/compose` on Linux. Docker Compose, Python 3, and OpenSSL are
required. The OPTIONAL integrations variables in `.env.example` do not change
the base deployment. When customizing ports or volume names, use the **same
values** in `.env` and the preparation command environment. The preparation
script does not load `.env`; pass custom volume names explicitly:

```bash
# Keycloak only
python3 scripts/prepare-integrations.py --keycloak
# Or collector only
python3 scripts/prepare-integrations.py --siem
# Or both
python3 scripts/prepare-integrations.py --keycloak --siem

# Custom example: set KEYCLOAK_DB_VOLUME to the same name in .env
KEYCLOAK_DB_VOLUME=my-keycloak-db python3 scripts/prepare-integrations.py --keycloak
```

The script creates a shared CA, 90-day certificates for the selected services,
and random initial administrator/DB passwords once under
`integrations/private/` (0700). Password and TLS key files use 0444 so container
UID 1000 can read their individual read-only mounts; the parent directory on the
host remains 0700. Do not expose that parent or mount the entire private tree
into a service. `integrations/trust/` contains only public CA certificates.
Both directories are excluded from Git.

The collector configuration is also prepared from `integrations/syslog-ng.conf`
as a protected 0444 runtime copy, allowing a capability-free container to read
it even under umask 077. Re-running preserves existing values and refuses
configuration/CA mismatches. To change collector configuration, review the
source, explicitly update the runtime copy with
`install -m 0444 integrations/syslog-ng.conf integrations/private/syslog-ng.conf`,
and recreate only that service. Preparation does not start services or change
existing OAM/Gateway configuration. If preparation fails partway through,
recover against a protected backup; do not delete/reissue files in a way that
replaces an existing database password.

The default Keycloak URL is `https://localhost:18443`. Use it on the Gateway's
host; from an administrator workstation, open the tunnel
`ssh -L 18443:127.0.0.1:18443 operator@oam.example.internal` and browse to
`https://localhost:18443/admin/`. For a different host/port, specify
`--keycloak-host id.example.internal --keycloak-port 18443` **on first
preparation**, and align DNS, `KEYCLOAK_BIND_IP`, and `KEYCLOAK_HTTPS_PORT`.
If another process owns a default port, change the port instead of stopping
that process. A remote Gateway cannot use this host's loopback address; provide
a reachable address/firewall configuration and matching certificate SAN.

The generated CA is intended for evaluation or controlled private environments.
Use your organization's CA or a publicly trusted certificate, protected key
custody, expiry monitoring, and backup/recovery policies for production. The
syslog receiver does not require client certificates by default. Before exposing
it beyond loopback, configure access controls and
`peer-verify(required-trusted)` with a client CA.

## 4. Start and connect

```bash
# Start each project independently. Do not combine these files in one -f chain.
docker compose -f docker-compose.keycloak.yml config --quiet
docker compose -f docker-compose.keycloak.yml up -d
docker compose -f docker-compose.keycloak.yml ps

docker compose -f docker-compose.siem.yml config --quiet
docker compose -f docker-compose.siem.yml up -d
docker compose -f docker-compose.siem.yml ps
```

Keycloak starts after PostgreSQL becomes healthy, but JVM initialization takes
additional time.

```bash
# Verify HTTPS with the CA; do not bypass verification with -k.
curl --fail --cacert integrations/trust/integration-ca.crt \
  https://localhost:18443/realms/master/.well-known/openid-configuration
# Inspect TLS syslog certificate verification (bounded to five seconds).
timeout 5 openssl s_client -connect 127.0.0.1:16514 -servername localhost \
  -CAfile integrations/trust/integration-ca.crt -verify_hostname localhost -verify_return_error </dev/null
```

Read the initial administrator password from
`integrations/private/admin_password` in an authorized host terminal. Keep it
out of screenshots, shell arguments, and logs. In the admin console, create the
application realm, client, mappers, users/service accounts, and model roles,
then connect using section 1. Production applications should use Authorization
Code+PKCE or a confidential service client; do not adopt example password grants
as the production application flow. Retire the initial bootstrap administrator
after creating and testing a regular administrator account. Editing a password
seed does not change an account already stored in the database.

For a Gateway on the same host, use profile issuer
`https://localhost:18443/realms/<application-realm>`. Trust the public CA through
the trust overlay during a planned Gateway recreation. Profile/LB rule creation
is deliberately manual so existing policies are not overwritten.

For the collector sink, use `address=127.0.0.1:16514`, `server_name=localhost`,
and `ca_bundle_path=/run/integration-ca/integration-ca.crt`. For an external
Gateway without the trust overlay, deploy the same public CA to a suitable
Gateway path and adjust the path accordingly.

```bash
# Inspect actual receipts. Treat operating logs as sensitive; do not paste them into public logs/PRs.
docker compose -f docker-compose.siem.yml exec -T syslog-ng \
  sh -c 'ls -l /var/log/siem; wc -l /var/log/siem/receipts-*.log'
```

Receipt files are created per UTC day and retain raw RFC5424 messages and their
JSON MSG. Original Gateway JSONL remains authoritative. This example has no
automatic deletion policy: monitor disk space and stalled ingestion, and apply
your organization's archive/deletion policy. For external SIEM ingestion,
configure its agent with a dedicated read-only receipt-file mount, the correct
parser and checkpoints, and verify matching record identities and original-byte
SHA-256 in the final SIEM index. The default collector does not automatically
forward to an external product.

## 5. Upgrade, stop, and recover

- Default dependency images are pinned to validated versions and digests.
  For an upgrade, set each image variable to an approved `repo@sha256:...` and
  record OAM/UI/Gateway and dependency image digests together.
- Keycloak has a dedicated PostgreSQL store; it does not share the existing
  OAM/aigw database. Back it up with `pg_dump` or organizational backup tooling,
  and protect the backup alongside private seeds and certificates.
- Before upgrading, retain the DB dump, volume names, image digests,
  sink/profile readbacks, and last receiver record. A Keycloak schema upgrade
  may prevent an image-only downgrade: rehearse restoration of the older
  database as well.
- Stop each project using `docker compose -f <selected-file> down`. External
  volumes remain. Do not use `down -v`, pruning, or volume deletion for
  upgrade/recovery.
- Before stopping a collector for an extended period, protect a backup of the
  sink configuration and disable or redirect the affected sink. Disable the
  compliance sink with `POST /audit/sink`, body `{"enabled":false}`; remove a
  named sink with `DELETE /audit/sinks/{name}`. Check backfill, duplicates, and
  retention gaps after reconnecting.
- Before removing Keycloak, deliberately change JWT rules that depend on it.
  Profile deletion is refused while rules reference the profile. Stopping the
  IdP does not immediately revoke existing tokens.
- Renew server certificate/key pairs while retaining the existing CA and DB
  password, then recreate only the affected service. CA rotation requires an
  overlap period with both old and new CAs and verification of Gateway trust
  reload/restart. The preparation script is not an automatic renewal tool.

## 6. Acceptance scope

Record static configuration checks, actual TLS/token behavior, Gateway
readback, inference admission/denial with backend receipts, original/received
audit hashes, outages/recovery, recreation/deletion, and reconnection separately.
Only report UI PASS after actual browser creation, refresh, rejection, and
recovery flows have been checked. See
[INTEGRATIONS-VALIDATION.md](INTEGRATIONS-VALIDATION.md) for results and limits.

References: [Keycloak containers](https://www.keycloak.org/server/containers),
[Keycloak hostname](https://www.keycloak.org/server/hostname),
[syslog-ng syslog source](https://syslog-ng.github.io/admin-guide/060_Sources/170_Syslog/000_syslog_source_options).
