# Public cloud / Floating IP bootstrap

[한국어](CONVERGED-CLOUD.ko.md) · [Converged architecture](../../docs/deployment-converged.md)

Use the Linux converged initializer for one host running PostgreSQL, Gateway,
OAM and UI. Optional Keycloak/syslog-ng remain separate opt-in projects; see
[Integrations](INTEGRATIONS.md). This procedure does not install a model backend.

## 1. Host and public addresses

Example: the VM NIC has `10.0.0.14`; a cloud Floating IP maps `203.0.113.10` to
that VM. Replace these documentation addresses with your actual values.

| Setting | Example | Purpose |
|---|---|---|
| `EDGE_BIND_IP` | `10.0.0.14` | Address actually assigned to the host NIC |
| `EDGE_HTTPS_PORT` | `8443` | UI/OAM HTTPS listener |
| `GW_HOST` | `gw.example.internal` | Gateway TLS identity and registration hostname |
| `GW_CONNECT_IP` | `10.0.0.14` | OAM's internal destination for `GW_HOST` |
| `EDGE_PUBLIC_ADDRESSES` | `203.0.113.10` | Public browser identities, not NIC configuration |

Do not assign the Floating IP to a NIC or use it as `EDGE_BIND_IP` or
`GW_CONNECT_IP`. Do not register the Gateway by Floating IP. Its registered
hostname continues to match the existing Gateway certificate. Internal traffic
uses the host NIC address through the container bridge, without public NAT.

Requirements: Linux, Docker Engine, Docker Compose 2.24+ (including support for
`!override`), Python 3, OpenSSL, `ip` (iproute2), `curl`, and `nsenter`
(util-linux). Run as root or with working sudo; namespace verification needs
root. Use approved image versions/digests for production.

In the cloud console, confirm the Floating IP attachment and permit inbound TCP
8443 from administrator networks. Host-side probes cannot prove this external
path. Keep database ports, plaintext Gateway management and optional IdP/audit
listeners private. The initializer does not modify cloud firewalls or NICs.

## 2. New installation

```bash
sudo -i
cd /opt/loxilb-stack/loxilb-oam/deploy/compose
bash scripts/init-converged.sh
```

Choose the actual NIC address for the management listener. Enter the hostname/IP
used in the browser and any other internal names. In the separate
`Public cloud / NAT / Floating IP access` step, enter the public IP or DNS names
as a comma-separated list, or `-` when none apply. The installer does not guess
an inbound Floating IP from outbound-IP services or cloud metadata.

The explicit command-line equivalent is:

```bash
bash scripts/init-converged.sh --public-address 203.0.113.10
```

The script adds the public URL to `SITE_ADDRESS` and `OAM_ALLOWED_ORIGINS`, adds
an `IP:` SAN for an address or `DNS:` SAN for a hostname, and sets the SNI
fallback needed for IP-literal access. It selects an actual host NIC for
`GW_CONNECT_IP`; wildcard edge binding uses the default-route NIC, or the sole
eligible NIC. Ambiguous selection fails instead of guessing. Docker bridges,
veth, loopback and virtual dataplane interfaces are excluded.

Retain the generated `.env`, seed files and named volumes. After login, change
the initial administrator password. A self-signed certificate needs client trust;
adding a correct SAN does not establish issuer trust.

## 3. Repair / extend an existing installation

Keep the existing `.env` when prompted. The public-address step still runs and
preserves database credentials, encryption secrets and Gateway service identity.
Addresses are added to existing origins; `-` does not remove previous addresses.

```bash
cd /opt/loxilb-stack/loxilb-oam/deploy/compose
bash scripts/init-converged.sh --public-address 203.0.113.10
```

If the existing self-signed certificate lacks a configured identity, the script
offers to regenerate it, backs up the old seed, stops only Caddy for certificate
volume refresh, and starts the management project with the updated configuration.
No manual volume copy or Compose edit is required. `--yes` accepts that default;
review the displayed identities before using unattended mode.

To explicitly renew an otherwise valid self-signed certificate:

```bash
bash scripts/init-converged.sh --yes --public-address 203.0.113.10 --refresh-edge-cert
```

Operator/CA-issued certificates are never silently replaced with self-signed
ones. Supply a certificate/key covering all configured names/IPs if that check
fails. This installer's existing ACME mode accepts DNS identities only. Existing
custom `EDGE_TLS_SEED_DIR` is honored. Backups under `backups/` are protected and
ignored by Git. New `.env` network changes have a protected original copy under
`backups/network/`; certificate seed and volume refresh backups are independent.

On a multihomed host, explicitly choose another assigned IPv4 address if needed:

```bash
bash scripts/init-converged.sh --gateway-connect-ip 10.0.0.14 --public-address 203.0.113.10
```

Missing/nonlocal addresses are refused. `GW_HOST` is unchanged. A correct existing
`local-gateway` registration is reused without resetting the administrator's
current credentials. Management containers can be recreated, briefly interrupting
the console. The full initializer also reconciles state/data projects; an image
or configuration change can restart those services. Schedule deliberate upgrades.

`--no-start` prepares configuration/volumes without starting services; it cannot
complete a live certificate-volume renewal or runtime qualification. Never use
`down -v` or broad Docker prune to repair this issue.

## 4. Acceptance and failure handling

The initializer now verifies:

1. Existing database/schema/listener and Gateway checks.
2. Actual OAM network namespace and effective `/etc/hosts` mapping.
3. TLS with OAM's mounted public CA and the `GW_HOST` identity.
4. Anonymous load-balancer management returns 401; service-token version and
   port queries return 200. Tokens are passed through curl stdin, never argv.
5. Every console identity serves `/netlox/` and `/api/oam/health` over verified
   TLS, using the local NIC destination. This is a local identity test.

A failed check yields a nonzero exit and `Installation verification FAILED`;
it is not a successful installation. No certificate-validation bypass is used
in the new probes. Ubuntu OpenSSL 3.0 and newer releases use `openssl verify`
for identity checks because `x509 -checkip/-checkhost` exit behavior differs.

Read-only verification can also be run separately:

```bash
sudo python3 scripts/converged-network.py probe
python3 scripts/converged-network.py edge-probe
docker exec loxilb-mgmt-oam-loxilb-1 cat /etc/hosts
```

From an administrator machine outside the VM/NAT, securely obtain only the
public `certs/edge/cert.pem` and verify the external path:

```bash
curl --cacert ./cert.pem --connect-timeout 5 --max-time 10 \
  -o /dev/null -sS -w 'HTTP %{http_code}, TLS verify %{ssl_verify_result}\n' \
  https://203.0.113.10:8443/netlox/
```

Expect `HTTP 200, TLS verify 0`. Then open the same `/netlox/` URL, trust the
certificate according to your workstation policy, log in, refresh the instance
view and verify `local-gateway` health and version. CLI/API checks alone are not
browser acceptance. Renewing a self-signed certificate can require updating the
workstation's trust/exception. Never copy `key.pem` to the workstation.

| Failure | Action |
|---|---|
| Host probes pass, external TCP times out | Check cloud ingress, ACLs and Floating IP mapping |
| OAM maps Gateway to a missing `172.17.0.1` | Rerun the updated initializer; check `GW_CONNECT_IP` |
| TLS identity/SAN mismatch | Confirm public identities; regenerate self-signed or supply matching CA certificate |
| Anonymous management is accepted | Investigate Gateway management authentication; do not mark installation successful |
| Authenticated management returns 401/403 | Compare service-token custody and effective authentication mode without printing tokens |

For rollback, retain the network `.env` backup and previous certificate seed.
Stop Caddy, restore the matching `.env` and edge seed, run
`python3 scripts/prepare-converged-volumes.py --refresh edge-tls`, then recreate
OAM/Caddy using the same three management Compose files. Preserve Gateway/DB
volumes and unrelated secrets. Restoring the old broken host mapping restores
the old Down symptom as well; use rollback only for an introduced regression.
