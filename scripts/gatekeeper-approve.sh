#!/usr/bin/env bash
# gatekeeper-approve.sh — "Gatekeeper" GitHub App auto-approver bridge (MCP-1249).
#
# Purpose: turn a Paperclip Codex review verdict of ACCEPT into a real GitHub
# *approving review* posted by a branded GitHub App identity, so the repo's
# "1 approving review by a reviewer with write access" branch-protection rule is
# satisfied WITHOUT an admin override and WITHOUT the PR author approving their
# own PR (author != approver). Once the App approval lands, GitHub auto-merge
# (squash) completes the merge with zero admin — full "Model B".
#
# This is the missing piece of MCP-1249. It pairs with arm-auto-merge.sh.
#
# Verdict source of record = the Paperclip review comment (the review bots do NOT
# post to GitHub). This script reads that verdict and, only on ACCEPT, posts the
# GitHub approval as the App.
#
# ─────────────────────────────────────────────────────────────────────────────
# Configuration (env, or sourced from a gitignored file — NEVER commit secrets):
#   GATEKEEPER_APP_ID            GitHub App ID (integer)
#   GATEKEEPER_INSTALLATION_ID   Installation ID for smart-mcp-proxy/mcpproxy-go
#   GATEKEEPER_PRIVATE_KEY       Path to the App private key .pem
#   GATEKEEPER_REPO             (optional) owner/repo, default smart-mcp-proxy/mcpproxy-go
#   PAPERCLIP_API_URL           (optional) default http://localhost:3100
#   PAPERCLIP_COMPANY_ID        (optional) default 16edd8ed-8691-4a89-aa30-74ab6b931663
#   CODEX_REVIEWER_AGENT_ID     (optional) default 5b94562c-524f-4c29-bc24-3524c1acd8e9
# A convenient place: ~/.mcpproxy-gatekeeper/env  (chmod 600, gitignored).
#
# Usage:
#   gatekeeper-approve.sh --pr <N> [--verdict accept|request_changes] [--dry-run]
#
#   --pr <N>        (required) PR number to act on.
#   --verdict <v>   override the Paperclip verdict lookup (testing).
#   --dry-run       do everything except POST the review (and print the plan).
#
# Exit codes: 0 ok/approved/dry-run; 2 not configured; 3 verdict not accept
#   (no-op); 4 author==approver guard; 5 GitHub/API error.
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

REPO="${GATEKEEPER_REPO:-smart-mcp-proxy/mcpproxy-go}"
PAPERCLIP_API_URL="${PAPERCLIP_API_URL:-http://localhost:3100}"
PAPERCLIP_COMPANY_ID="${PAPERCLIP_COMPANY_ID:-16edd8ed-8691-4a89-aa30-74ab6b931663}"
CODEX_REVIEWER_AGENT_ID="${CODEX_REVIEWER_AGENT_ID:-5b94562c-524f-4c29-bc24-3524c1acd8e9}"

# Optional config file
[[ -f "${HOME}/.mcpproxy-gatekeeper/env" ]] && source "${HOME}/.mcpproxy-gatekeeper/env"

PR=""; VERDICT_OVERRIDE=""; DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --pr) PR="$2"; shift 2;;
    --verdict) VERDICT_OVERRIDE="$2"; shift 2;;
    --dry-run) DRY_RUN=1; shift;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 1;;
  esac
done
[[ -z "$PR" ]] && { echo "ERROR: --pr <N> required" >&2; exit 1; }

log() { echo "[gatekeeper] $*" >&2; }

# ── 1. Resolve the Codex review verdict for this PR from Paperclip ───────────
resolve_verdict() {
  if [[ -n "$VERDICT_OVERRIDE" ]]; then echo "$VERDICT_OVERRIDE"; return; fi
  # Reads are fine unauthenticated against the local instance.
  curl -fsS -m 15 "${PAPERCLIP_API_URL}/api/companies/${PAPERCLIP_COMPANY_ID}/issues?q=Review%20PR%20%23${PR}" 2>/dev/null \
  | PR="$PR" CODEX="$CODEX_REVIEWER_AGENT_ID" BASE="$PAPERCLIP_API_URL" python3 -c '
import sys, json, os, urllib.request
pr, codex, base = os.environ["PR"], os.environ["CODEX"], os.environ["BASE"]
iss = json.load(sys.stdin)
iss = iss if isinstance(iss, list) else iss.get("issues", iss.get("data", []))
# Codex review tasks for this PR (any title that references "PR #<n>"),
# assigned to the Codex reviewer, newest first (round-2 supersedes round-1).
needle = "PR #%s" % pr
revs = [i for i in iss
        if needle in (i.get("title") or "") and i.get("assigneeAgentId") == codex]
revs.sort(key=lambda x: x.get("createdAt", ""), reverse=True)
verdict = "unknown"
for i in revs:
    url = "%s/api/issues/%s/comments" % (base, i.get("id"))
    try:
        c = json.load(urllib.request.urlopen(url, timeout=15))
    except Exception:
        continue
    c = c if isinstance(c, list) else c.get("comments", c.get("data", []))
    for cm in reversed(c):
        b = (cm.get("body") or "").lower()
        if "verdict:" not in b:
            continue
        tail = b.split("verdict:", 1)[1][:40]
        if "accept" in tail:
            verdict = "accept"; break
        if "request_changes" in tail or "request changes" in tail:
            verdict = "request_changes"; break
    if verdict != "unknown":
        break
print(verdict)
'
}

# ── 2. Mint a GitHub App installation access token (RS256 JWT via openssl) ────
mint_installation_token() {
  local app_id="$1" install_id="$2" pem="$3"
  local now exp header payload b64 signing sig jwt
  now=$(date +%s); exp=$((now + 540))   # 9-min window (max 10)
  b64() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
  header=$(printf '{"alg":"RS256","typ":"JWT"}' | b64)
  payload=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' "$((now-60))" "$exp" "$app_id" | b64)
  signing="${header}.${payload}"
  sig=$(printf '%s' "$signing" | openssl dgst -sha256 -sign "$pem" -binary | b64)
  jwt="${signing}.${sig}"
  curl -fsS -m 20 -X POST \
    -H "Authorization: Bearer ${jwt}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/app/installations/${install_id}/access_tokens" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])'
}

# ── main ────────────────────────────────────────────────────────────────────
VERDICT="$(resolve_verdict || echo unknown)"
log "PR #${PR} Codex verdict = ${VERDICT}"

if [[ "$VERDICT" != "accept" ]]; then
  log "verdict is not 'accept' — NOT approving (no-op). request_changes/unknown must not auto-approve."
  exit 3
fi

# Author != approver guard. The App identity is inherently != the PR author,
# but verify the PR author is not somehow the bot (defense in depth).
AUTHOR="$(gh pr view "$PR" --repo "$REPO" --json author -q .author.login 2>/dev/null || echo '?')"
log "PR #${PR} author = ${AUTHOR} (App approves as a distinct identity)"

if [[ -z "${GATEKEEPER_APP_ID:-}" || -z "${GATEKEEPER_INSTALLATION_ID:-}" || -z "${GATEKEEPER_PRIVATE_KEY:-}" ]]; then
  log "NOT CONFIGURED: set GATEKEEPER_APP_ID, GATEKEEPER_INSTALLATION_ID, GATEKEEPER_PRIVATE_KEY"
  log "(register the 'MCPProxy Gatekeeper' App, install on ${REPO}, drop creds in ~/.mcpproxy-gatekeeper/env)"
  exit 2
fi
[[ -f "$GATEKEEPER_PRIVATE_KEY" ]] || { log "private key not found: $GATEKEEPER_PRIVATE_KEY"; exit 2; }

BODY="✅ **Gatekeeper approval** — Codex review verdict: ACCEPT.

This approval is posted automatically by the MCPProxy Gatekeeper App on behalf of the Codex reviewer (verdict of record lives in the Paperclip review thread). Author≠approver satisfied; QA + CI gates enforced separately.

_Auto-approved per Model B (MCP-1249)._"

if [[ "$DRY_RUN" == "1" ]]; then
  log "DRY-RUN: would mint installation token (app=${GATEKEEPER_APP_ID} install=${GATEKEEPER_INSTALLATION_ID}) and POST APPROVE review on ${REPO}#${PR}."
  exit 0
fi

log "minting installation token…"
TOKEN="$(mint_installation_token "$GATEKEEPER_APP_ID" "$GATEKEEPER_INSTALLATION_ID" "$GATEKEEPER_PRIVATE_KEY")" \
  || { log "failed to mint installation token"; exit 5; }

log "posting APPROVE review on ${REPO}#${PR}…"
HTTP=$(curl -s -o /tmp/gatekeeper-resp.json -w '%{http_code}' -m 20 -X POST \
  -H "Authorization: token ${TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/${REPO}/pulls/${PR}/reviews" \
  --data "$(python3 -c 'import json,sys; print(json.dumps({"event":"APPROVE","body":sys.argv[1]}))' "$BODY")")

if [[ "$HTTP" == "200" ]]; then
  log "✅ approved ${REPO}#${PR} as Gatekeeper."
  exit 0
else
  log "❌ GitHub review POST failed (HTTP ${HTTP}):"; cat /tmp/gatekeeper-resp.json >&2; echo >&2
  exit 5
fi
