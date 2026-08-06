# Kubernetes manifests — pre-release, not supported

> ## ⚠️ Do not deploy these
>
> **The manifests in this directory will not start `loxilb-oam` as they stand.**

## Why

None of the overlays supply `OAM_JWT_SECRET` or `OAM_DEFAULT_ADMIN_PASSWORD`.
Both are mandatory — the server calls `requireSecrets()` at startup and exits
when either is unset — so the container aborts immediately and the Pod settles
into `CrashLoopBackOff`.

Two smaller problems come with it:

- `k8s/overlays/production` pins `oam-loxilb:v0.9.8.7`, an image tag that has
  never been published.

## What to use instead

Docker Compose is the supported deployment path for this release:

| Deployment | Path | Guide |
|------------|------|-------|
| Full management plane (UI + API + MySQL behind a TLS edge) — **recommended** | [`deploy/compose/`](../deploy/compose/) | [docs/deployment-compose.md](../docs/deployment-compose.md) |
| API + MySQL over HTTP | [`docker-compose.yml`](../docker-compose.yml) | [DEPLOYMENT.md](../DEPLOYMENT.md) |

## Status

These manifests are kept as the starting point for a converged Kubernetes
deployment (OAM + UI + MySQL + cert-manager) that is in development. The
certificate model it will use for the OAM→gateway hop is already defined and
shared with the Compose path — see
[docs/instance-tls.md](../docs/instance-tls.md).

## If you are working on them

The minimum to make an overlay boot:

1. Add `OAM_JWT_SECRET` and `OAM_DEFAULT_ADMIN_PASSWORD` to
   `oam-loxilb-secret.yaml` and reference both from the Deployment's `env:`.
   `OAM_DEFAULT_ADMIN_PASSWORD` must satisfy the account password policy (≥9
   characters with upper, lower, digit and special; no character three times in
   a row) or the initial admin cannot be created and the API still refuses to
   run.
2. Build and load an image that actually exists — `make build-image` produces
   `oam-loxilb:latest`; adjust the overlay's `images:` tag to match.
3. Add the OAM service's remaining configuration as needed —
   `SNAPSHOT_ENC_KEY` (only the production overlay wires it today) and
   `OAM_ALLOWED_ORIGINS`.

The database wiring is already correct: the image entrypoint translates the
`DB_*` environment family (plus `SERVER_PORT` and `TOKEN_EXPIRATION`) into
`-db-*` flags, so no `args:` override is needed for it.

Populate every `CHANGE_ME` placeholder out of band. Never commit real secrets.

## Layout

```
k8s/
├── base/            # HTTPS base (MySQL + application)
├── base-http/       # HTTP-only base
└── overlays/
    ├── development/
    └── production/  # additionally wires SNAPSHOT_ENC_KEY from a Secret
```
