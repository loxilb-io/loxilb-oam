#!/usr/bin/env bash
#
# apply-org-governance.sh — synchronized repository-management and
# branch-protection baseline for the loxilb-io open-source repositories.
#
# loxilb-oam is the reference posture. Running this script converges every
# listed repository onto the same scheme:
#
#   Repository settings
#     - squash merges only (no merge commits, no rebase merges)
#     - head branches deleted automatically on merge
#
#   Branch protection on `main`
#     - PRs only; direct pushes disabled for everyone (enforce_admins=true)
#     - 1 approving review, required from Code Owners (.github/CODEOWNERS)
#     - stale approvals dismissed on new pushes; approval of the last push
#       required
#     - required status checks (per-repo list below), strict mode (branch
#       must be up to date with main before merging)
#     - conversation resolution required; force pushes and deletions blocked
#
# Requires: gh CLI authenticated with admin permission on the target repos.
#
# Usage:
#   .github/scripts/apply-org-governance.sh                  # dry-run: print every change
#   .github/scripts/apply-org-governance.sh --apply          # apply to all repos
#   .github/scripts/apply-org-governance.sh --apply --repo loxilb-io/loxilb-oam
#
# Notes:
#   - Required status-check contexts must match check-run names EXACTLY as
#     they appear on a real PR head. When a repo's CI jobs are renamed or
#     added, update the list here and re-run.
#   - Every required check must run on EVERY pull_request to main with no
#     path filters, otherwise a skipped check blocks unrelated PRs forever.
#   - Advisory jobs (continue-on-error) are deliberately NOT required.
#   - loxilb-inference-gateway is managed by its own scripts/apply-governance.sh
#     with a softer, upstream-loxilb-mirroring posture (enforce_admins=false);
#     it is intentionally not listed here.

set -euo pipefail

REPOS=(
  "loxilb-io/loxilb-oam"
  "loxilb-io/loxicmd-inference-gateway"
  "loxilb-io/loxilbdocs-inference-gateway"
  "loxilb-io/loxitui-inference-gateway"
)

# Required status checks per repo, as JSON arrays of check-run context names.
required_checks() {
  case "$1" in
    loxilb-io/loxilb-oam)
      echo '["Build & Test", "golangci-lint", "gitleaks", "Integration (MySQL)", "Swagger drift", "govulncheck", "hygiene gate"]' ;;
    loxilb-io/loxicmd-inference-gateway)
      echo '["loxicmd-inference-gateway-build-ci", "leak-scan"]' ;;
    loxilb-io/loxilbdocs-inference-gateway)
      # "Prose lint" is continue-on-error (advisory) — not required.
      echo '["Repository hygiene", "Strict docs build", "Link check"]' ;;
    loxilb-io/loxitui-inference-gateway)
      echo '["Lint & Typecheck", "Tests & Coverage", "Generated Schema Drift", "Build Binaries", "Gitleaks (full history)", "Repository hygiene gate", "Signed-off-by check", "E2E (live gateway container)"]' ;;
    *)
      echo "unknown repo: $1" >&2; return 1 ;;
  esac
}

APPLY=0
ONLY_REPO=""
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --repo)  ONLY_REPO="$2"; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
  shift
done

run_api() {
  echo "+ gh api $*"
  if [ "$APPLY" = "1" ]; then gh api "$@" > /dev/null; fi
}

merge_settings() {
  local repo="$1"
  echo ""
  echo "── $repo: repository merge settings ──"
  run_api -X PATCH "repos/$repo" \
    -F allow_squash_merge=true \
    -F allow_merge_commit=false \
    -F allow_rebase_merge=false \
    -F allow_auto_merge=false \
    -F delete_branch_on_merge=true
}

protect_main() {
  local repo="$1" checks
  checks="$(required_checks "$repo")"
  echo ""
  echo "── $repo: branch protection on main ──"
  echo "+ gh api -X PUT repos/$repo/branches/main/protection  (checks: $checks)"
  if [ "$APPLY" = "1" ]; then
    gh api -X PUT "repos/$repo/branches/main/protection" --input - > /dev/null <<EOF
{
  "required_status_checks": { "strict": true, "contexts": $checks },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": true,
    "require_last_push_approval": true,
    "required_approving_review_count": 1
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_linear_history": false,
  "required_conversation_resolution": true
}
EOF
  fi
}

audit() {
  local repo="$1"
  echo ""
  echo "── $repo: current state ──"
  gh api "repos/$repo" --jq \
    '"  squash_only: \(.allow_squash_merge and (.allow_merge_commit or .allow_rebase_merge | not))  delete_branch_on_merge: \(.delete_branch_on_merge)"'
  gh api "repos/$repo/branches/main" --jq '"  main protected: \(.protected)"'
  local prot
  if prot="$(gh api "repos/$repo/branches/main/protection" --jq \
    '"  enforce_admins: \(.enforce_admins.enabled)  reviews: \(.required_pull_request_reviews.required_approving_review_count) (code_owners=\(.required_pull_request_reviews.require_code_owner_reviews))  strict_checks: \(.required_status_checks.strict) [\(.required_status_checks.contexts | join(", "))]"' \
    2>/dev/null)"; then
    echo "$prot"
  else
    echo "  (no branch protection)"
  fi
}

for repo in "${REPOS[@]}"; do
  if [ -n "$ONLY_REPO" ] && [ "$repo" != "$ONLY_REPO" ]; then continue; fi
  audit "$repo"
  merge_settings "$repo"
  protect_main "$repo"
done

echo ""
if [ "$APPLY" = "1" ]; then
  echo "Applied. Re-run without --apply to audit the converged state."
else
  echo "Dry run only — re-run with --apply to make the changes."
fi
