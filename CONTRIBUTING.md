# Contributing to loxilb-oam

Thanks for your interest in improving loxilb-oam. This document describes how to
set up a development environment and the conventions we follow.

## Getting started

1. Fork the repository and clone your fork.
2. Install Go 1.25+ and MySQL 8.x or later.
3. Copy `.env.example` to `.env` and fill in the required secrets.
4. Download dependencies and run the tests:

   ```bash
   make deps
   make test
   ```

   `make test` runs the same unit-test set as the CI gate. The integration
   suites under `tests/rest_api/` and `tests/e2e/` need a live server and
   database and are excluded; see `.github/workflows/ci.yml` for how CI boots
   them if you need to reproduce a failure locally.

## Development workflow

- Create a topic branch off `main` (e.g. `fix/token-refresh`, `feat/snapshot-ttl`).
- Keep changes focused; unrelated cleanups belong in separate PRs.
- Ensure the following pass before opening a PR:

  ```bash
  gofmt -l .          # must print nothing — CI fails on any unformatted file
  go build ./...
  go vet ./...
  make test           # unit tests; excludes the live-server integration suites
  golangci-lint run   # config in .golangci.yml
  ```

  If you change handler annotations, also regenerate the OpenAPI docs
  (`swag init`) — CI fails on drift between the annotations and `docs/`.

- Update or add tests for any behavior change.
- Update documentation (`README.md`, `docs/`, `DEPLOYMENT.md`, Swagger) when you
  change public behavior or configuration.

## Coding conventions

- Follow standard Go style; run `gofmt`/`goimports`.
- Prefer clear, idiomatic Go doc comments over verbose narration.
- Parameterize all SQL (no string concatenation of user input).
- Never commit secrets. Secrets come from environment variables; use `CHANGE_ME`
  placeholders in example manifests.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/), e.g.:

```
feat(snapshot): add scheduled retention pruning
fix(auth): reject tokens missing from the server-side store
docs(readme): document SNAPSHOT_ENC_KEY
```

## Sign your commits (DCO)

We require a [Developer Certificate of Origin (DCO)](https://developercertificate.org/) sign-off on
every commit. The sign-off certifies that you wrote the change or otherwise have the right to submit it
under the project's license.

Add a `Signed-off-by` line to each commit — it must match the git author name and email:

```
Signed-off-by: Your Name <your.name@example.com>
```

Git adds it automatically with the `-s` flag:

```bash
git commit -s -m "feat(snapshot): add scheduled retention pruning"
```

If you forgot on an unpushed commit, amend it with `git commit --amend -s`. PRs whose commits are not
signed off will be blocked by the DCO check.

## Pull request policy

All changes land through pull requests — direct pushes to `main` are disabled.

### Opening a PR
- Push your topic branch to your fork and open a PR against `loxilb-io/loxilb-oam` `main`.
- Fill in the PR template: describe the motivation and the change, and link any related issue
  (e.g. `Closes #123`).
- Give the PR a [Conventional Commits](https://www.conventionalcommits.org/) style title — it becomes
  the squash-merge commit message.
- Keep the PR focused and reasonably small; unrelated changes belong in separate PRs.

### Requirements to merge
- **At least one approving review from a loxilb maintainer is required.** Maintainers are the code
  owners in [.github/CODEOWNERS](.github/CODEOWNERS); GitHub requests their review automatically, and
  branch protection blocks the merge until a maintainer approves.
- All CI checks pass — build, `go vet`, `golangci-lint`, tests, the secret scan, and the DCO check.
- All commits are signed off (DCO) — see [Sign your commits](#sign-your-commits-dco).
- New or changed behavior is covered by tests, and docs are updated where relevant.
- The branch is up to date with `main` and all review threads are resolved.

Project roles and how maintainer decisions are made are described in [GOVERNANCE.md](GOVERNANCE.md).

### Review process
- A maintainer will review as soon as they can and may request changes; please be responsive.
- Address feedback with follow-up commits. Avoid force-pushing once a review has started so reviewers
  can follow incremental changes — the PR is squash-merged at the end, so intermediate commits don't
  matter.
- Authors cannot approve or merge their own PRs. Once it has a maintainer approval and green CI, a
  maintainer merges it via squash.
- A new approval is required after any further changes that a maintainer pushes back on (branch
  protection dismisses stale approvals on new commits).
- PRs with no author activity for an extended period may be marked stale; reopen when you're ready to
  continue.

## Security

Do **not** open public issues for security vulnerabilities. Follow the process in
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
