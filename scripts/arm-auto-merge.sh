#!/usr/bin/env bash
#
# Model B "merge without --admin" (MCP-1248): arm auto-merge for a code PR after
# the Paperclip review verdicts are ACCEPT and `qa-gate` is success at the PR's
# CURRENT head SHA. A bot identity posts a GitHub *approving review* (reflecting
# the Paperclip verdict — bots do not post to GitHub by default) and then arms
# GitHub auto-merge (squash). GitHub merges only once ALL required checks are
# green.
#
#   Agents ARM auto-merge. Agents NEVER bypass required checks.
#   This script must NOT be used with `gh pr merge --admin` or any
#   bypass_pull_request_allowances path. The whole point is to land PRs through
#   the gate, not around it.
#
# spec-075 invariant: a PASS is valid only while PR head == qa_head_sha. This
# script re-verifies the head SHA you were blessed at still matches the live PR
# head before approving, and refuses if the PR was pushed since.
#
# Identity / credentials:
#   GH_TOKEN must be a repo-scoped fine-grained PAT or GitHub App installation
#   token with: Contents RW, Pull requests RW, Commit statuses RW. Store it with
#   the agent secrets (searcher/agents/.env pattern, gitignored) and inject it
#   as GH_TOKEN. Do NOT use the human owner's default gh login (that can
#   --admin-bypass and muddies the audit trail).
#
# Usage:
#   GH_TOKEN=<bot-token> scripts/arm-auto-merge.sh <pr-number|pr-url> <expected-head-sha> ["approval body"]
#
# Exit codes: 0 armed; 2 usage; 3 head SHA drifted; 4 qa-gate not success.
set -euo pipefail

REPO="${MCPPROXY_REPO:-smart-mcp-proxy/mcpproxy-go}"
PR="${1:-}"
EXPECTED_SHA="${2:-}"
BODY="${3:-Approved (Model B): Paperclip review verdicts = ACCEPT and qa-gate green at this head SHA. Arming auto-merge; GitHub merges when all required checks pass.}"

if [[ -z "$PR" || -z "$EXPECTED_SHA" ]]; then
  echo "usage: $0 <pr-number|pr-url> <expected-head-sha> [approval body]" >&2
  exit 2
fi
if [[ -z "${GH_TOKEN:-}" ]]; then
  echo "error: GH_TOKEN must be set to a repo-scoped bot PAT / App token (not a human --admin login)." >&2
  exit 2
fi

# 1. spec-075: the live PR head must still equal the SHA we were blessed at.
LIVE_SHA="$(gh pr view "$PR" --repo "$REPO" --json headRefOid -q .headRefOid)"
if [[ "$LIVE_SHA" != "$EXPECTED_SHA" ]]; then
  echo "head SHA drifted: blessed=$EXPECTED_SHA live=$LIVE_SHA — re-run QA/review on the new head; refusing to approve." >&2
  exit 3
fi

# 2. qa-gate must be success at this exact SHA (the required quality bar).
QA_STATE="$(gh api "repos/${REPO}/commits/${LIVE_SHA}/status" \
  -q '.statuses[] | select(.context=="qa-gate") | .state' 2>/dev/null | head -1 || true)"
if [[ "$QA_STATE" != "success" ]]; then
  echo "qa-gate is '${QA_STATE:-missing}' at ${LIVE_SHA} (need success) — refusing to approve." >&2
  exit 4
fi

# 3. Post the approving review (reflects the Paperclip verdict; counts toward
#    required_approving_review_count) and arm auto-merge.
gh pr review --approve "$PR" --repo "$REPO" --body "$BODY"
gh pr merge --auto --squash "$PR" --repo "$REPO"
echo "armed auto-merge for PR $PR at $LIVE_SHA (will merge when all required checks are green)"
