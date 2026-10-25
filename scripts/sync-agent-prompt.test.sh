#!/usr/bin/env bash
# Tests for scripts/sync-agent-prompt.sh (Spec 121 T040).
set -euo pipefail
cd "$(dirname "$0")/.."
S=scripts/sync-agent-prompt.sh
fail() { echo "FAIL: $*" >&2; exit 1; }
d=$(mktemp -d); trap 'rm -rf "$d"' EXIT

cat >"$d/src.md" <<'EOF'
intro
<!-- agent-install-prompt:start -->
```text
step one
step two $VAR "quoted" \ backslash
```
<!-- agent-install-prompt:end -->
outro
EOF
cat >"$d/dst.md" <<'EOF'
# readme
before
<!-- agent-install-prompt:start -->
old text
<!-- agent-install-prompt:end -->
after
EOF
export AGENT_PROMPT_SRC="$d/src.md" AGENT_PROMPT_DST="$d/dst.md"

$S --check >/dev/null 2>&1 && fail "drift not detected"
$S >/dev/null
$S --check >/dev/null || fail "not in sync after sync"
grep -q 'step two \$VAR "quoted" \\ backslash' "$d/dst.md" || fail "special characters mangled"
grep -q '^before$' "$d/dst.md" && grep -q '^after$' "$d/dst.md" || fail "surrounding text lost"
grep -q 'old text' "$d/dst.md" && fail "old block survived"

# the repository's own files are in sync
unset AGENT_PROMPT_SRC AGENT_PROMPT_DST
$S --check >/dev/null || fail "README.md has drifted from docs/getting-started/agent-install.md"

# missing markers are an error, not a silent pass
echo "no markers" >"$d/none.md"
AGENT_PROMPT_SRC="$d/none.md" AGENT_PROMPT_DST="$d/dst.md" $S --check >/dev/null 2>&1 && fail "accepted a source without markers"
echo "ok sync-agent-prompt"
