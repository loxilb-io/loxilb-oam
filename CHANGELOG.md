# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial public release of loxilb-oam: OAM management API for LoxiLB instances.
- Authentication, RBAC (admin/operator/viewer), and server-side token revocation.
- Instance snapshot orchestration with at-rest AES-256-GCM encryption.
- Docker Compose deployments: a single-node management-plane bundle
  (`deploy/compose/`, UI + API + MySQL behind a Caddy TLS edge) and an API-only
  HTTP stack at the repository root.
- CI (build, vet, golangci-lint, unit tests, govulncheck) and secret scanning.

### Removed
- OAuth login (Google/GitHub/Facebook). The implementation was unfinished and
  had no coverage against the real handlers, so it was withdrawn rather than
  published. This drops the `OAM_OAUTH_ENABLED` and `OAM_OAUTH_*_CLIENT_{ID,
  SECRET}` variables, the `-google/-github/-facebook-redirect-url` flags, the
  `/oam/oauth/*` endpoints, and the `golang.org/x/oauth2` dependency.
  Authentication is username/password against the local user store. The
  `users` table keeps its `oauth_*` columns, so no migration is needed and a
  future implementation can reuse them; the withdrawn code is archived on the
  `feature/oauth2` branch.

### Known limitations
- The Kubernetes manifests under `k8s/` are **pre-release and unsupported**:
  they do not supply the mandatory `OAM_JWT_SECRET` /
  `OAM_DEFAULT_ADMIN_PASSWORD` and will not start. Use Docker Compose. See
  [k8s/README.md](k8s/README.md).

[Unreleased]: https://github.com/loxilb-io/loxilb-oam/commits/main
