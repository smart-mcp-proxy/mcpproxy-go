#!/usr/bin/env bash
# gatekeeper-approve.test.sh — fail-closed SHA-guard regression tests (MCP-1249).
#
# Proves the security invariant Codex flagged: gatekeeper-approve.sh must REFUSE
# to post an approving review unless it can resolve a reviewed SHA that equals
# the PR's current head. A missing reviewed SHA must fail CLOSED (never approve
# blind), so a post-review force-push of unreviewed code cannot inherit an old
# ACCEPT. The manual --verdict accept override is held to the same requirement.
#
# Hermetic: stubs `gh` on PATH and uses --verdict/--dry-run so no network, no
# Paperclip, and no real GitHub approval is ever posted.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APPROVE="$SCRIPT_DIR/gatekeeper-approve.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

HEAD_SHA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
OTHER_SHA="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

# Stub gh: PR head is OPEN at HEAD_SHA, no existing reviews.
cat > "$TMP/gh" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == "pr" && "\$2" == "view" ]]; then echo "$HEAD_SHA author-x OPEN"; exit 0; fi
if [[ "\$1" == "api" ]]; then echo "[]"; exit 0; fi
exit 0
EOF
chmod +x "$TMP/gh"

# Config gate must pass so a *matching* SHA can reach the (dry-run) approve step.
touch "$TMP/key.pem"
export GATEKEEPER_APP_ID=123 GATEKEEPER_INSTALLATION_ID=456 GATEKEEPER_PRIVATE_KEY="$TMP/key.pem"
export PATH="$TMP:$PATH"

pass=0; fail=0
check() { # desc expected actual
  if [[ "$2" == "$3" ]]; then echo "ok   - $1 (exit $3)"; pass=$((pass+1));
  else echo "FAIL - $1 (expected exit $2, got $3)"; fail=$((fail+1)); fi
}
refused() { echo "$1" | grep -qiE "refus|stale" ; }

# 1. accept, NO reviewed SHA -> fail-closed refuse, must NOT approve.
out="$("$APPROVE" --pr 999 --verdict accept --dry-run 2>&1)"; rc=$?
check "no reviewed SHA -> refuse (fail-closed)" "7" "$rc"
if refused "$out"; then echo "     msg: refusal text present"; else echo "     FAIL: no refusal text"; fail=$((fail+1)); fi
if echo "$out" | grep -qi "DRY-RUN: would"; then echo "     FAIL: reached approve with no SHA!"; fail=$((fail+1)); fi

# 2. accept, reviewed SHA != head -> stale refuse.
out="$("$APPROVE" --pr 999 --verdict accept --reviewed-sha "$OTHER_SHA" --dry-run 2>&1)"; rc=$?
check "reviewed SHA != head -> stale refuse" "6" "$rc"
if echo "$out" | grep -qi "DRY-RUN: would"; then echo "     FAIL: reached approve while stale!"; fail=$((fail+1)); fi

# 3. accept, reviewed SHA == head -> reaches approve (dry-run, exit 0).
out="$("$APPROVE" --pr 999 --verdict accept --reviewed-sha "$HEAD_SHA" --dry-run 2>&1)"; rc=$?
check "reviewed SHA == head -> approves (dry-run)" "0" "$rc"
if echo "$out" | grep -qi "DRY-RUN: would"; then echo "     reached approve step as expected"; else echo "     FAIL: did not reach approve"; fail=$((fail+1)); fi

echo "----"
echo "pass=$pass fail=$fail"
[[ "$fail" == "0" ]]
