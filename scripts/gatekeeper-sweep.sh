#!/usr/bin/env bash
# gatekeeper-sweep.sh — auto-trigger for the Gatekeeper App (MCP-1249).
#
# Sweeps every OPEN PR in the repo and invokes gatekeeper-approve.sh for each.
# That call is idempotent and self-guarding: it approves a PR only when the
# Codex review verdict is ACCEPT for the *current* head and the Gatekeeper hasn't
# already approved that head; everything else is a no-op. So this script is safe
# to run unattended on a timer (launchd/cron) — it's the hands-off half of
# "full Model B": Codex ACCEPT → App approval → GitHub auto-merge, no admin.
#
# Reads creds from ~/.mcpproxy-gatekeeper/env (via gatekeeper-approve.sh).
# Designed for launchd: sets a sane PATH and logs each sweep with a timestamp.
#
# Usage: gatekeeper-sweep.sh [--dry-run]
# Env:   GATEKEEPER_REPO (default smart-mcp-proxy/mcpproxy-go)
set -euo pipefail

# launchd starts with a minimal PATH — make sure our tools resolve.
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${GATEKEEPER_REPO:-smart-mcp-proxy/mcpproxy-go}"
DRY=""; [[ "${1:-}" == "--dry-run" ]] && DRY="--dry-run"

ts() { date '+%Y-%m-%dT%H:%M:%S%z'; }
log() { echo "$(ts) [sweep] $*"; }

# Gatekeeper not configured → exit quietly (don't spam logs every interval).
[[ -f "${HOME}/.mcpproxy-gatekeeper/env" ]] || { log "not configured (~/.mcpproxy-gatekeeper/env missing) — skipping."; exit 0; }

# Paperclip (verdict source) must be reachable; otherwise every approve call
# would no-op as 'unknown' anyway — skip the sweep to keep logs clean.
PAPERCLIP_API_URL="${PAPERCLIP_API_URL:-http://localhost:3100}"
if ! curl -fsS -m 5 "${PAPERCLIP_API_URL}/api/health" >/dev/null 2>&1; then
  log "Paperclip not reachable at ${PAPERCLIP_API_URL} — skipping this sweep."
  exit 0
fi

# bash 3.2 (macOS /bin/bash) safe — no mapfile.
PRS=()
while IFS= read -r n; do [[ -n "$n" ]] && PRS+=("$n"); done \
  < <(gh pr list --repo "$REPO" --state open --json number -q '.[].number' 2>/dev/null || true)
log "open PRs: ${#PRS[@]}${PRS:+ (${PRS[*]})}"

approved=0
[[ ${#PRS[@]} -eq 0 ]] && { log "no open PRs — done."; exit 0; }
for pr in "${PRS[@]}"; do
  [[ -z "$pr" ]] && continue
  set +e
  out="$("$HERE/gatekeeper-approve.sh" --pr "$pr" $DRY 2>&1)"; rc=$?
  set -e
  case $rc in
    0)  if echo "$out" | grep -q 'approved.*as Gatekeeper'; then
          log "PR #$pr → APPROVED"; approved=$((approved+1))
        fi ;;  # other rc=0 = idempotent/closed no-op, stay quiet
    3)  : ;;                                   # not accept — quiet
    6)  log "PR #$pr → stale verdict (re-review needed)" ;;
    2)  log "PR #$pr → gatekeeper not configured"; break ;;
    *)  log "PR #$pr → error (rc=$rc): $(echo "$out" | tail -1)" ;;
  esac
done
log "sweep done — ${approved} newly approved."
