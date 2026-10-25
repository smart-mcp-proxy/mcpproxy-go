#!/usr/bin/env bash
# Keep the README "Paste this into your agent" block identical to the prompt in
# docs/getting-started/agent-install.md (Spec 121 FR-021). The docs page is the
# single source of truth.
#
#   scripts/sync-agent-prompt.sh           # rewrite the README block from the docs page
#   scripts/sync-agent-prompt.sh --check   # exit 1 if they differ (CI)
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${AGENT_PROMPT_SRC:-docs/getting-started/agent-install.md}"
DST="${AGENT_PROMPT_DST:-README.md}"
START='<!-- agent-install-prompt:start -->'
END='<!-- agent-install-prompt:end -->'

extract() { # <file>: print the marked region including markers
  awk -v s="$START" -v e="$END" '$0==s{p=1} p{print} $0==e{p=0}' "$1"
}

want="$(extract "$SRC")"
[[ -n "$want" ]] || { echo "sync-agent-prompt: no marked prompt in $SRC" >&2; exit 2; }
have="$(extract "$DST")"
[[ -n "$have" ]] || { echo "sync-agent-prompt: no marked block in $DST (add the start/end markers)" >&2; exit 2; }

if [[ "${1:-}" == "--check" ]]; then
  if [[ "$want" != "$have" ]]; then
    echo "sync-agent-prompt: $DST has drifted from $SRC; run scripts/sync-agent-prompt.sh" >&2
    diff <(printf '%s\n' "$want") <(printf '%s\n' "$have") >&2 || true
    exit 1
  fi
  echo "ok: agent prompt in sync"
  exit 0
fi

tmp="$(mktemp)"; wantf="$(mktemp)"
trap 'rm -f "$tmp" "$wantf"' EXIT
printf '%s\n' "$want" >"$wantf"
awk -v s="$START" -v e="$END" -v f="$wantf" '
  $0==s {while ((getline l < f) > 0) print l; skip=1; next}
  $0==e {skip=0; next}
  !skip {print}
' "$DST" >"$tmp"
cat "$tmp" >"$DST"
echo "updated $DST"
