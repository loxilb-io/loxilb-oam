# Cloud bootstrap validation — 2026-10-19

[한국어](CONVERGED-CLOUD-VALIDATION.ko.md) · [Operator guide](CONVERGED-CLOUD.md)

## Scope and environment

Validated on the designated Ubuntu 24.04.5 LTS cloud testbed using a real private
NIC and a public Floating IP. This was a fresh application installation on an
existing OS, followed by recovery of the original application state; the VM OS
was not reinstalled. Gateway ran with the standard privileged/host-network
Compose definition. Inference traffic and eBPF forwarding were not qualified.

Base source: `46acdf7ffb74f205161674b6ebc23a008eb1d08f` plus this change.
Linux Python 3.12.3, OpenSSL 3.0.13, Docker Engine 29.1.3, Docker Compose
2.40.3. Existing runtime images were reused, not rebuilt or upgraded:

| Component | Local image ID |
|---|---|
| Gateway | `sha256:49d6581c4a5c2e62ad97b25ef7f8591efb44bcd3737efb5789225805de8a8a33` |
| OAM | `sha256:3bfbdcc49470fb9ee3cea808be4323bbf18e83e32fea94f87e587a723fa64ead` |
| Caddy | `sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f` |
| PostgreSQL | `sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336` |

These image IDs identify the tested runtime, not a source-to-image attestation
or a general release/security qualification. The browser displayed UI v0.9.8.9.

## Results

| Check | Result and boundary |
|---|---|
| Original failure | OAM mapped `GW_HOST` to a missing default-bridge address; actual OAM network probe timed out while host loopback worked. RED evidence retained. |
| Automatic repair | Updated initializer selected the real NIC, preserved `GW_HOST`, renewed self-signed edge material, refreshed the external certificate volume and recreated management containers without a manual Compose edit. Exit 0. |
| Real OAM path | Mounted public CA + hostname validation + mounted service token; anonymous LB-management 401, authenticated version/ports 200. No `-k`. |
| Floating IP identity | Included in site matching, allowed origins, certificate IP SAN and SNI fallback; every configured identity returned UI/OAM-health 200 in local verified-TLS probes. |
| External ingress | A workstation outside the VM/NAT accessed the Floating IP UI: HTTP 200, TLS verify result 0 with the public certificate. |
| Fresh installation | Separate empty PostgreSQL, config, audit, token and TLS volumes; no existing `.env` or certificates. Normal initializer defaults plus public-address input completed with exit 0 and automatically registered one `local-gateway`. |
| Original-state recovery | All three fresh projects stopped without `-v`; original initializer completed with exit 0. Original user-password hashes/instance rows and certificate/secret seed bytes matched pre-rehearsal receipts. Both original and rehearsal volumes retained. |
| Replay | Repeated initializer without renewal preserved `.env`, edge certificate/key and three service/DB secret files byte-for-byte. |
| Invalid input | A scheme-bearing public address was rejected with nonzero exit; receipts confirmed unchanged `.env`, edge certificate/key and secret bytes. |
| Browser | Existing authenticated Chrome session, Instances → Check Health: `local-gateway` displayed **Healthy / Active**. Dashboard displayed live system/connection data. No user credentials were reset. |
| Regression checks | 30 Python tests passed on macOS and Ubuntu, including bad addresses, missing bridge, public SAN rejection/recovery, CA-issued/expired certificate classification, actual namespace/CA/token use, anonymous-acceptance rejection, timeout rejection and edge TLS failure. |
| Static/hygiene | Bash syntax, ShellCheck warning-level checks, EN/KO command parity/local links, diff whitespace and configured Gitleaks directory scan passed. |

## Retained failures and limitations

The first Linux regression run exposed different OpenSSL behavior: on Ubuntu
3.0, `x509 -checkip/-checkhost` did not reliably fail by exit status for an
identity mismatch. The implementation now uses `openssl verify` and the same
negative fixture passes on both Ubuntu 3.0 and macOS 3.6. Failure logs were kept.

The initializer validates local access identities but cannot certify cloud
firewall policy or Floating IP attachment from inside the VM. The separate
external-workstation check is the ingress evidence. Public addresses remain
operator-supplied; no third-party IP discovery or cloud API mutation was used.

Not covered: a freshly provisioned/reinstalled OS, multihomed runtime/failover,
IPv6 deployment, live CA-issued/ACME renewal, browser negative/recovery campaigns,
inference correctness/performance, eBPF forwarding, external IdP/SIEM end-to-end,
HA/Kubernetes, or release qualification. CI results belong to the PR checks;
local tests are not CI evidence. The new Ubuntu 24.04 CI job runs the Python
bootstrap/seed regressions; it does not run this live cloud rehearsal.

Raw logs, DB backup, seed backups and browser screenshot are retained privately.
No passwords, private keys, filled `.env`, database dumps, cloud IPs, internal
host identities or raw runtime logs are included in this public report.
