# Container Image Reference

Everything about the `loxilb-oam` container image: what is published, how to
verify it, how to run it standalone, and how to build your own.

> **Looking for a deployment guide, not an image reference?** Use
> [docs/deployment-compose.md](deployment-compose.md) (full management plane —
> UI + API + MySQL behind a TLS edge) or [DEPLOYMENT.md](../DEPLOYMENT.md)
> (single-service API stack). This document covers the image itself; those
> cover running it as a system.

## Table of contents

1. [What is published](#what-is-published)
2. [Image contents](#image-contents)
3. [Pull and verify](#pull-and-verify)
4. [Run the image](#run-the-image)
5. [Environment surface](#environment-surface)
6. [Build your own image](#build-your-own-image)
7. [Air-gapped and mirrored registries](#air-gapped-and-mirrored-registries)
8. [Operating a running container](#operating-a-running-container)
9. [Supply-chain pipeline](#supply-chain-pipeline)
10. [Known limitations](#known-limitations)

## What is published

| | |
|---|---|
| Registry | `ghcr.io/loxilb-io/loxilb-oam` |
| Platforms | `linux/amd64` only (no arm64 image is published — see [Known limitations](#known-limitations)) |
| Base | `alpine:3.21` |
| Published by | `.github/workflows/release.yml`, on a `v*` git tag |

### Tags

Image tags are the release version, which follows
[loxilb-io/loxilb](https://github.com/loxilb-io/loxilb): `vMAJOR.MINOR.PATCH`
with an optional fourth build component. loxilb-oam versions in lockstep with
loxilb — pair `loxilb-oam:v0.9.8.7` with `loxilb:v0.9.8.7`.

| Tag form | Meaning |
|----------|---------|
| `v0.9.8.7` | An immutable release. **Use this in production.** |
| `v0.9.8.7-rc.1`, `-alpha.`, `-beta.` | Prerelease. Published, but does **not** move `:latest`. |
| `latest` | The most recent *final* release. Convenient for a lab; untraceable for an upgrade — never pin production to it. |

Publishing is gated: the release job targets the `release` GitHub Environment,
so a pushed tag alone cannot ship an image — a required reviewer must approve
it, and a Trivy CRITICAL+fixable scan must pass *before* the push.

## Image contents

```
/app/loxilb-oam     the API server (static, CGO_ENABLED=0)
/app/reset_admin    admin-password recovery tool (same DB_* env surface)
/app/scripts/       helper scripts, incl. reset-admin.sh (run via docker exec)
```

| Aspect | Value |
|--------|-------|
| Workdir | `/app` |
| Exposed ports | `8080` (HTTP), `443` (HTTPS, only when the server is started with `-enable-https`) |
| Entrypoint | none — `CMD` is a `sh -c` wrapper that maps `DB_*`/`SERVER_PORT`/`TOKEN_EXPIRATION` env vars onto the binary's flags |
| Log file | `/var/log/loxioam.log` (the log-retrieval API reads `/var/log/`) |
| Healthcheck | not baked into the image; the Compose stacks probe `GET /oam/health` with `wget` (present via busybox) |
| CA trust | `ca-certificates` installed, for outbound TLS to managed LoxiLB instances |

The image carries OCI labels (`org.opencontainers.image.source`, `.version`,
`.licenses`, …) linking the GHCR package back to this repository.

To confirm which version a container actually runs — the release is compiled
into the binary, not just labelled:

```bash
docker run --rm ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 ./loxilb-oam -version
# loxilb-oam v0.9.8.7
```

An image built locally without a `VERSION` build-arg reports `dev`.

> **Persisting logs:** the app writes to `/var/log/loxioam.log` inside the
> container, so mount a volume at `/var/log` if you need logs to survive a
> container replacement — as the root `docker-compose.yml` does with its
> `oam_logs` volume. Docker seeds an empty named volume from the image's
> `/var/log` on first run, so the directory the image prepares survives the
> mount.

## Pull and verify

```bash
docker pull ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7
```

If the pull returns `unauthorized`, the GHCR package is not public from your
network. Authenticate with a token that has `read:packages`:

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <your-github-user> --password-stdin
```

Every published image is Cosign-signed (keyless/OIDC) and carries SLSA
build-provenance and SPDX SBOM attestations. Verify before deploying:

```bash
# 1. Signature — proves the image was built by this repo's release workflow.
cosign verify ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 \
  --certificate-identity-regexp '^https://github\.com/loxilb-io/loxilb-oam/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# 2. Build provenance (SLSA).
gh attestation verify oci://ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 --owner loxilb-io

# 3. SBOM (SPDX) — also attached to the GitHub Release as sbom.spdx.json.
gh attestation verify oci://ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 --owner loxilb-io \
  --predicate-type https://spdx.dev/Document
```

Deploy by digest (`...@sha256:…`) rather than tag when your policy requires the
verified artifact and the running artifact to be provably identical.

## Run the image

The service needs a MySQL 8.x+ database; the image does not contain one. On a
fresh database the schema in `database/init/` must have been applied — the
Compose stacks do this automatically via the MySQL entrypoint, so a standalone
`docker run` is for when you already have a prepared database (see
[docs/database-installation.md](database-installation.md)).

```bash
docker run -d --name loxilb-oam \
  -p 8080:8080 \
  -e OAM_JWT_SECRET="$(openssl rand -base64 48)" \
  -e OAM_DEFAULT_ADMIN_PASSWORD='Ch4nge-me!' \
  -e SNAPSHOT_ENC_KEY="$(openssl rand -base64 32)" \
  -e OAM_ALLOWED_ORIGINS='https://oam.example.com' \
  -e DB_HOST=10.0.0.5 -e DB_PORT=3306 \
  -e DB_USER=oamuser -e DB_PASSWORD='…' -e DB_NAME=loxioam \
  ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7
```

The server **fails fast** if `OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`, or
the database password is unset — the container exits rather than starting with
a default. Pass secrets from your secret manager or a `--env-file`, not on the
command line, in anything but a lab.

Verify it is up:

```bash
curl -fsS http://localhost:8080/oam/health
```

Trusting a private CA for managed-instance TLS means mounting the bundle and
pointing at the path *inside* the container:

```bash
  -v /etc/loxilb-oam/certs:/etc/loxilb-oam/certs:ro \
  -e OAM_INSTANCE_CA_BUNDLE=/etc/loxilb-oam/certs/instance-ca.pem \
  -e OAM_INSTANCE_TLS_INSECURE=false
```

## Environment surface

The image reads two distinct families — the difference matters when you write
manifests:

**Mapped to flags by the image's `CMD`** (container-only knobs):

| Variable | Default | Becomes |
|----------|---------|---------|
| `DB_USER` | `oamuser` | `-db-user` |
| `DB_PASSWORD` | *(required — container aborts if unset)* | `-db-password` |
| `DB_HOST` | `127.0.0.1` | `-db-host` |
| `DB_PORT` | `3306` | `-db-port` |
| `DB_NAME` | `loxioam` | `-db-name` |
| `SERVER_PORT` | `8080` | `-port` |
| `TOKEN_EXPIRATION` | *(unset)* | `-token-expiration` |

Note that `TOKEN_EXPIRATION` is the containerized spelling of the bare binary's
`OAM_TOKEN_TTL_MINUTES`.

**Read directly by the server** — `OAM_JWT_SECRET`, `OAM_DEFAULT_ADMIN_PASSWORD`,
`SNAPSHOT_ENC_KEY`, `OAM_ALLOWED_ORIGINS`, `OAM_INSTANCE_CA_BUNDLE`,
`OAM_INSTANCE_TLS_INSECURE`, `OAM_DOCKER_*`. These pass straight through and
behave identically to the bare binary.

Full semantics and defaults for every variable and flag:
[DEPLOYMENT.md → Configuration Reference](../DEPLOYMENT.md#configuration-reference).

Because the entrypoint maps `DB_*` onto flags, `reset_admin` — which reads the
same `DB_*` family — needs no extra arguments inside a container.

## Build your own image

Forks, air-gapped builds, and patched releases all build from the same
`Dockerfile`. The Makefile wraps it:

```bash
make docker-build VERSION=v0.9.8.7                     # ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7
make docker-build docker-push VERSION=v0.9.8.7         # build, then push (needs docker login)
make docker-build IMAGE_NAME=myorg/loxilb-oam REGISTRY=docker.io VERSION=v0.9.8.7
```

| Make variable | Default | Purpose |
|---------------|---------|---------|
| `REGISTRY` | `ghcr.io` | registry host |
| `IMAGE_NAME` | `loxilb-io/loxilb-oam` | repository path |
| `VERSION` | current release (`v0.9.8.7`) | release identifier — compiled into the binary and set as the OCI version label |
| `TAG` | `$(VERSION)` | image tag; override alone to name an image without changing the stamped version (e.g. `TAG=latest`) |

Plain Docker, if you would rather not use Make:

```bash
docker build --build-arg VERSION=v0.9.8.7 -t myorg/loxilb-oam:v0.9.8.7 .
```

What the build does: stage 1 (`golang:1.26-alpine`) downloads modules in their
own layer, runs `swag init` to generate the Swagger docs that get compiled into
the binary, then builds both binaries statically (`CGO_ENABLED=0`, `-trimpath`,
and `-ldflags="-s -w -X main.version=$VERSION"`). Stage 2 copies only the
binaries and `scripts/` onto Alpine — no toolchain, no source. `.dockerignore`
keeps `.git`, `.env`, `ssl/`, internal-only docs, tests, and markdown out of the
build context.

Building for a non-amd64 host:

```bash
docker buildx build --platform linux/arm64 -t myorg/loxilb-oam:v0.9.8.7-arm64 .
```

This is a supported *build* — it is simply not something the project publishes
or tests in CI.

## Air-gapped and mirrored registries

Move the image across the boundary by digest, keeping tags intact:

```bash
# On a connected host
docker pull ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7
docker save ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 | gzip > loxilb-oam-v0.9.8.7.tar.gz

# On the air-gapped host
gunzip -c loxilb-oam-v0.9.8.7.tar.gz | docker load
```

To serve it from an internal registry, retag and push, then override the image
name where the deployment reads it — `OAM_IMAGE` / `OAM_TAG` in the
[`deploy/compose/`](../deploy/compose/) bundle's `.env`:

```bash
docker tag ghcr.io/loxilb-io/loxilb-oam:v0.9.8.7 registry.internal/loxilb-oam:v0.9.8.7
docker push registry.internal/loxilb-oam:v0.9.8.7
```

Verify signatures on the connected side — Cosign and `gh attestation` both need
to reach the transparency log and GitHub.

Building images from source instead of mirroring them is covered in
[docs/deployment-compose.md §5.1](deployment-compose.md#51-pin-the-release-images).

## Operating a running container

```bash
docker logs -f loxilb-oam                        # startup + request logs
docker exec loxilb-oam wget -qO- http://localhost:8080/oam/health
docker exec -it loxilb-oam sh -c "./scripts/reset-admin.sh --confirm"   # recover admin access
docker exec -it loxilb-oam sh                    # busybox shell
```

The admin-reset flow is documented in
[docs/ADMIN_RESET_QUICK_GUIDE.md](ADMIN_RESET_QUICK_GUIDE.md).

Upgrading is a pull-and-replace: pull the new tag, recreate the container with
the same environment and volumes. Schema changes ship as numbered files in
`database/migrations/`; apply the ones newer than your database before starting
the new image. The container itself holds no state.

## Supply-chain pipeline

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `build-image.yml` | every PR and push to `main` | Builds the image (proving the Dockerfile works, not just `go build`). Runs an **advisory** Trivy scan for HIGH+CRITICAL fixable CVEs — reports, never blocks. No registry push. |
| `release.yml` | `v*` tag, or manual dispatch | Builds binaries + image, runs a **blocking** Trivy CRITICAL+fixable gate, pushes to GHCR, Cosign-signs the digest, attests SLSA provenance and an SPDX SBOM, and creates the GitHub Release with a tarball and `SHA256SUMS`. Requires reviewer approval via the `release` Environment. |

## Known limitations

- **`linux/amd64` only.** No multi-arch manifest is published. arm64 users must
  build their own (see above).
- **Runs as root.** The image declares no `USER`; add `--user` /
  `securityContext` at the deployment layer if your policy requires non-root.
  The binary needs no privileges beyond binding its listen port.
- **No baked-in healthcheck.** `HEALTHCHECK` is defined by the Compose files
  rather than the image, so a plain `docker run` reports no health status —
  probe `/oam/health` yourself.
- **No image-level TLS defaults.** HTTPS requires `-enable-https` plus mounted
  certificates; the recommended pattern is terminating TLS at an edge proxy, as
  the [`deploy/compose/`](../deploy/compose/) bundle does with Caddy.

## Related documents

- [DEPLOYMENT.md](../DEPLOYMENT.md) — configuration reference and the
  single-service Compose stack
- [docs/deployment-compose.md](deployment-compose.md) — full management-plane
  deployment guide
- [docs/database-installation.md](database-installation.md) — running against
  your own database
- [docs/instance-tls.md](instance-tls.md) — TLS to managed LoxiLB instances
