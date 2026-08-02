# TLS Between the Management Plane and LoxiLB Gateway Instances

This guide sets up verified TLS on the control channel between **loxilb-oam**
(with **loxilb-ui** in front of it) and every managed
**loxilb-inference-gateway** instance, using a **self-signed private CA**. It
covers the Docker/bare-metal deployment today and defines the mechanism the
Kubernetes deployment (Phase B, with cert-manager) will reuse — the two
platforms share one model, so nothing you set up here is thrown away.

```
loxilb-ui ──▶ caddy ──▶ oam-loxilb ══ TLS (verified) ══▶ gateway instance A :8091
                            ║                            gateway instance B :8091
                            ║  trusts: management CA     (each presents a server
                            ╚═ OAM_INSTANCE_CA_BUNDLE     cert signed by the CA)
```

> The browser↔UI edge has its own TLS story (see
> [deployment-compose.md §5](deployment-compose.md)); this document is only
> about the OAM → gateway hop, which always crosses the network between
> machines and therefore **must** be verified TLS in production.

## 1. The model (identical on Docker and Kubernetes)

| Piece | What it is | Docker / bare metal | Kubernetes (Phase B) |
|-------|-----------|---------------------|----------------------|
| **Management CA** | one private CA per deployment | created once by `generate-instance-certs.sh` | the **same CA** imported into a cert-manager `CA Issuer` (or a fresh one) |
| **Server certificate** | per gateway instance, SAN = its host/IP/DNS name | issued by the script, copied to the instance | issued by a cert-manager `Certificate`, delivered as a Secret |
| **Gateway TLS listener** | `--tls` on port `8091` | flags or `TLS*` env vars on the container/process | same env vars on the Pod |
| **OAM trust anchor** | `OAM_INSTANCE_CA_BUNDLE` → the CA cert | file mounted at `/etc/loxilb-oam/certs/instance-ca.pem` | CA Secret mounted at the **same path**, same env var |
| **Registration** | instance endpoint in OAM | `https://<host>:8091/netlox/v1` | `https://<service-dns>:8091/netlox/v1` |

**The invariant:** env keys (`OAM_INSTANCE_CA_BUNDLE`,
`OAM_INSTANCE_TLS_INSECURE`, gateway `TLS*`), the in-container CA path, and
the `https://` registration never change across platforms. Moving to k8s
replaces only *how certificates are issued and delivered* (shell script →
cert-manager), not how they are used.

## 2. Docker / bare-metal setup

All paths are relative to `loxilb-oam/deploy/compose/` (see the
[deployment guide](deployment-compose.md)).

### 2.1 Create the CA and per-instance certificates

One command creates the CA (first run only) and a server certificate for each
gateway host — use the exact host, IP, or DNS name that OAM will connect to
(it must match, this becomes the certificate SAN):

```bash
scripts/generate-instance-certs.sh 192.0.2.10 gw2.example.internal
```

Produces:

```
certs/
├── instance-ca.pem                  # CA certificate — what OAM trusts
└── instance-ca/
    ├── ca.crt / ca.key              # the CA itself — keep ca.key OFFLINE
    ├── 192.0.2.10/server.{crt,key}
    └── gw2.example.internal/server.{crt,key}
```

### 2.2 Enable TLS on each gateway instance

Copy that instance's `server.crt` + `server.key` to the gateway host, then
start the gateway with TLS. **As a container** (the usual case):

```bash
docker run -d --name loxilb-gw --privileged \
  -v /etc/loxilb/cert:/opt/loxilb/cert:ro \
  ghcr.io/loxilb-io/loxilb-inference-gateway:<tag> \
  --tls --tls-port=8091
```

(`--tls-certificate` / `--tls-key` default to
`/opt/loxilb/cert/server.{crt,key}`, so mounting the certs there is enough.
The equivalent environment variables — `TLS=true`, `TLS_PORT`,
`TLS_CERTIFICATE`, `TLS_PRIVATE_KEY` — work identically and are what the k8s
manifests use.)

**Bare-metal process:** same flags on the `loxilb` command line.

> `--tls` adds the HTTPS listener; the plaintext API on `:11111` still exists.
> In production, firewall `:11111` (or bind it to localhost) so the verified
> TLS port is the only management path into the gateway.

### 2.3 Point OAM at the CA and register the instances

In the bundle's `.env` (the `certs/` directory is already mounted into the
OAM container):

```bash
OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem
OAM_INSTANCE_TLS_INSECURE=false
```

Apply with `docker compose ... up -d`, then register each instance in the UI
(or via the API) with the endpoint:

```
https://<host-or-ip>:8091/netlox/v1
```

The registered name must be one of the names in the certificate SAN —
register by IP if you issued for the IP, by DNS name if you issued for the
name.

### 2.4 Verify

```bash
# The gateway presents your CA-signed cert (issuer CN = LoxiLB Management CA):
echo | openssl s_client -connect <host>:8091 -servername <host> 2>/dev/null \
  | openssl x509 -noout -subject -issuer -ext subjectAltName

# Verification passes against the CA:
curl --cacert certs/instance-ca.pem https://<host>:8091/netlox/v1/config/loadbalancer/all
```

In the UI, the instance should show as reachable; proxied views (dashboard
stats, LB lists) and snapshots then run over verified TLS. Failures appear in
`docker compose ... logs oam-loxilb` — see §4.

### 2.5 Rotation

- **Server certs** (default validity 825 days): re-run
  `generate-instance-certs.sh <host>`, copy the new pair to the instance,
  restart the gateway. OAM needs no change.
- **CA** (10 years): to roll it, generate the new CA, then concatenate old +
  new CA certs into `instance-ca.pem` (OAM accepts a multi-certificate
  bundle), reissue server certs against the new CA instance by instance, and
  drop the old CA from the bundle when the last instance has moved.

## 3. Kubernetes (Phase B) — same model, cert-manager issuance

This section is the design contract for the k8s phase; the mechanism is
chosen so everything from §2 carries over.

1. **Bring the same CA** (or start fresh — both work): import it as a Secret
   and make it a cert-manager CA `Issuer`. Reusing the Docker-phase CA means
   a mixed fleet (some gateways on Compose hosts, some on k8s) trusts one
   authority during migration:

   ```bash
   kubectl create secret tls loxilb-management-ca \
     --cert=certs/instance-ca/ca.crt --key=certs/instance-ca/ca.key -n loxilb
   ```
   ```yaml
   apiVersion: cert-manager.io/v1
   kind: Issuer
   metadata: { name: loxilb-management-ca, namespace: loxilb }
   spec: { ca: { secretName: loxilb-management-ca } }
   ```

2. **Issue per-gateway certificates** with a `Certificate` whose `dnsNames`
   is the gateway Service DNS (replaces `generate-instance-certs.sh`; renewal
   becomes automatic):

   ```yaml
   apiVersion: cert-manager.io/v1
   kind: Certificate
   metadata: { name: gw-a-tls, namespace: loxilb }
   spec:
     secretName: gw-a-tls
     issuerRef: { name: loxilb-management-ca, kind: Issuer }
     dnsNames: [ gw-a.loxilb.svc.cluster.local ]
   ```

3. **Mount into the gateway Pod** and enable TLS via the same knobs as §2.2 —
   cert-manager Secrets use `tls.crt`/`tls.key` key names, which the env vars
   absorb without any renaming:

   ```yaml
   env:
     - { name: TLS, value: "true" }
     - { name: TLS_CERTIFICATE, value: /opt/loxilb/cert/tls.crt }
     - { name: TLS_PRIVATE_KEY, value: /opt/loxilb/cert/tls.key }
   volumeMounts: [ { name: tls, mountPath: /opt/loxilb/cert, readOnly: true } ]
   volumes: [ { name: tls, secret: { secretName: gw-a-tls } } ]
   ```

   > The gateway does not hot-reload certificates; pair renewals with a
   > rollout restart (e.g. Reloader/Wave, or cert-manager's ~30-day-early
   > renewal plus a scheduled restart).

4. **OAM trusts the same CA at the same path** — mount the CA Secret and keep
   the identical env value:

   ```yaml
   env:
     - { name: OAM_INSTANCE_CA_BUNDLE, value: /etc/loxilb-oam/certs/instance-ca.pem }
   volumeMounts: [ { name: instance-ca, mountPath: /etc/loxilb-oam/certs, readOnly: true } ]
   volumes:
     - name: instance-ca
       secret:
         secretName: loxilb-management-ca
         items: [ { key: tls.crt, path: instance-ca.pem } ]
   ```

5. **Register** instances as `https://<service-dns>:8091/netlox/v1` — the
   only per-platform difference is the hostname in the URL.

## 4. Troubleshooting

| Symptom (in `oam-loxilb` logs) | Cause / fix |
|-------------------------------|-------------|
| `x509: certificate signed by unknown authority` | OAM isn't trusting your CA — `OAM_INSTANCE_CA_BUNDLE` unset, pointing at the wrong file, or the instance cert was signed by a different/regenerated CA. Check the mounted file matches `certs/instance-ca/ca.crt`. |
| `x509: certificate is valid for X, not Y` | SAN mismatch — the instance was registered by a name/IP not present in its certificate. Re-register with a SAN name, or reissue the cert for the right name (§2.1). |
| `connection refused` on `:8091` | The gateway isn't running with `--tls` / `TLS=true`, or a firewall blocks 8091. `openssl s_client -connect <host>:8091` from the OAM host is the quickest probe. |
| Works with `OAM_INSTANCE_TLS_INSECURE=true`, fails without | Confirms a certificate problem (trust or SAN, rows above) rather than connectivity. Never ship with the insecure flag on. |
| `tls: failed to verify certificate` right after cert renewal | The gateway is still serving the old cert (no hot reload) — restart the gateway container/Pod. |

## Related

- [deployment-compose.md](deployment-compose.md) — the full bundle deployment
  guide (this document details its §6).
- `deploy/compose/scripts/generate-instance-certs.sh` — CA + server-cert
  helper used in §2.
