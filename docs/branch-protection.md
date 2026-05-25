# Branch Protection Policy — `main`

This document describes the intended branch-protection rules for the `main` branch of `mcpproxy-go`.
**Applying or changing these rules is a human/admin action** — the repository owner must run the snippet below or configure the settings manually in the GitHub UI.

---

## Intended Rules

| Rule | Setting |
|------|---------|
| Require pull-request review before merging | ✅ enabled (`REVIEW_REQUIRED`) |
| Required approving reviews | 1 |
| Dismiss stale reviews on new push | ✅ enabled |
| Require status checks to pass before merging | ✅ enabled |
| Required status checks | `lint`, `unit-tests`, `e2e-tests`, `CodeQL`, `Scorecard` |
| Require branches to be up to date before merging | ✅ enabled |
| Require linear history (no merge commits) | ✅ enabled |
| Allow force-pushes | ❌ disabled |
| Allow deletions | ❌ disabled |
| Restrict who can push to matching branches | repository admins only |

---

## Applying / Verifying via `gh api`

Run the following as a repository admin.  Replace `<OWNER>` and `<REPO>` if needed (defaults: `smart-mcp-proxy` / `mcpproxy-go`).

```bash
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  /repos/smart-mcp-proxy/mcpproxy-go/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "lint",
      "unit-tests",
      "e2e-tests",
      "CodeQL",
      "Scorecard"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": 1
  },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

To **verify** the current settings without changing them:

```bash
gh api \
  -H "Accept: application/vnd.github+json" \
  /repos/smart-mcp-proxy/mcpproxy-go/branches/main/protection
```

---

## Status Check Names

The `contexts` array above must exactly match the job names (or check names) reported by GitHub Actions.
Confirm the current names with:

```bash
gh api /repos/smart-mcp-proxy/mcpproxy-go/commits/main/check-runs \
  --jq '.check_runs[].name' | sort -u
```

Adjust the list in the `PUT` payload if CI job names change.

---

## Notes

- `enforce_admins: false` is intentional so that emergency hotfix merges by admins are still possible; set to `true` for stricter enforcement.
- `restrictions: null` means no push restrictions beyond the PR requirement.
- Linear history prevents merge commits; contributors must rebase their branches before merging.
- The Scorecard check is produced by the `scorecard.yml` workflow added in Spec 053 (WP-B3).
- The CodeQL check is produced by `codeql.yml` (WP-B1).
