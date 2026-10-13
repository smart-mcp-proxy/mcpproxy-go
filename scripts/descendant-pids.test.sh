#!/usr/bin/env bash
# descendant-pids.test.sh — Spec 109 PR-a review round 4, finding F3:
# descendant_pids (T011a) had no test of its own — only an end-to-end run
# (test-api-e2e-cleanup-check.sh) ever exercised it, and that script only
# proves the "don't kill unrelated processes" half, never that a multi-level
# tree is walked correctly. This proves the recursive walk reaches a
# grandchild and great-grandchild, not just direct children, and that a leaf
# process (no children) returns empty.
#
# Hermetic: spawns only `sleep`/`bash` processes of its own, no mcpproxy
# binary needed. Run directly: ./scripts/descendant-pids.test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./descendant-pids.sh
source "$SCRIPT_DIR/descendant-pids.sh"

pass=0
fail=0
check() { # desc, exit-status-of-previous-test (0 = pass)
    if [ "$2" -eq 0 ]; then
        echo "ok   - $1"
        pass=$((pass + 1))
    else
        echo "FAIL - $1"
        fail=$((fail + 1))
    fi
}

# Build a real 3-level tree: OUTER (bash, direct child of this script) ->
# MID (bash, its child) -> LEAF (sleep, MID's child). Each level uses `cmd &
# wait` rather than a single simple command, so bash does not exec-optimize
# the intermediate shells away (a single `bash -c "sleep N"` collapses into
# the sleep process itself, losing the intermediate level).
bash -c 'bash -c "sleep 25 & wait" & wait' &
OUTER=$!

# Wait for the tree to actually appear (avoids a fixed sleep racing process
# creation on a loaded machine).
MID=""
for _ in $(seq 1 20); do
    MID=$(pgrep -P "$OUTER" 2>/dev/null | head -1)
    [ -n "$MID" ] && break
    sleep 0.1
done

LEAF=""
for _ in $(seq 1 20); do
    LEAF=$(pgrep -P "$MID" 2>/dev/null | head -1)
    [ -n "$LEAF" ] && break
    sleep 0.1
done

cleanup_tree() {
    kill -9 "$LEAF" "$MID" "$OUTER" 2>/dev/null || true
}
trap cleanup_tree EXIT

[ -n "$MID" ]
check "test fixture: MID (grandchild-to-be) actually spawned" $?
[ -n "$LEAF" ]
check "test fixture: LEAF (great-grandchild-to-be) actually spawned" $?

result=$(descendant_pids "$$")

echo "$result" | grep -qx "$OUTER"
check "direct child (OUTER) is present" $?

echo "$result" | grep -qx "$MID"
check "grandchild (MID) is present" $?

echo "$result" | grep -qx "$LEAF"
check "great-grandchild (LEAF) is present" $?

leaf_result=$(descendant_pids "$LEAF")
[ -z "$leaf_result" ]
check "leaf process (no children) returns empty" $?

no_such_pid_result=$(descendant_pids "999999999")
[ -z "$no_such_pid_result" ]
check "nonexistent PID returns empty (no pgrep error leaks into output)" $?

echo ""
echo "Passed: $pass, Failed: $fail"
[ "$fail" -eq 0 ]
