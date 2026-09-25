#!/bin/bash
#
# Process-hygiene check for scripts/test-api-e2e.sh's cleanup() trap.
#
# test-api-e2e.sh used to end with a blanket
#     pkill -f "mcpproxy.*serve"
#     pkill -f "launcher-server.*--port 39933"
# which killed EVERY matching process on the host: a developer's tray-managed
# core, or another worktree's E2E run (.claude/worktrees/*). This check pins
# both halves of the fix:
#
#   1. Decoys survive. Before running the suite it starts processes the old
#      pkill lines would have matched, from a scratch directory:
#        - a real `mcpproxy serve` on its own port, config, data dir and HOME
#          (stands in for a tray core / another worktree's core);
#        - a stand-in carrying the launcher fixture's argv signature
#          (`launcher-server --port 39933`). Not the real fixture: that would
#          bind :39933, which this run's own fixture needs.
#      After the suite exits both decoys must still be alive.
#   2. No leaks. While the suite runs, its process tree is sampled every
#      second; after it exits, no sampled process (its cores, stdio upstreams,
#      the launcher fixture) may still be alive.
#
# Modes (CHECK_MODES, default "complete abort"):
#   complete  let the suite run to its normal end (pass or fail);
#   abort     SIGTERM the suite once its core has the launcher fixture and the
#             npx-launched everything server up — a failed/interrupted run.
#
# The suite's own pass/fail is NOT asserted here, only process hygiene.
# Prereqs are the suite's: ./mcpproxy (and ./mcpproxy-server for its audit
# sub-test), jq, npx, and a free :39933. Never uses pkill.
#
# Usage: ./scripts/test-api-e2e-cleanup-check.sh
#   E2E_SCRIPT=path  suite to run (default scripts/test-api-e2e.sh)
#   KEEP_WORK=1      keep the scratch dir (suite logs); kept anyway on failure

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$REPO_ROOT" || exit 1

E2E_SCRIPT="${E2E_SCRIPT:-scripts/test-api-e2e.sh}"
CHECK_MODES="${CHECK_MODES:-complete abort}"
ABORT_AFTER=20       # abort mode: let the suite get into its tests first
ABORT_DEADLINE=180   # abort mode: SIGTERM by then even if the tree never filled
LAUNCHER_PORT=39933

WORK="$(mktemp -d "${TMPDIR:-/tmp}/mcpproxy_e2e_cleanup_check.XXXXXX")"
DECOY_DIR="$WORK/decoy"
DECOY_CORE_PID=""
DECOY_LAUNCHER_PID=""
FAILED=0

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; FAILED=1; }

port_in_use() {
    lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

free_port() {
    local p="$1"
    while port_in_use "$p"; do
        p=$((p + 1))
    done
    echo "$p"
}

# tree_of PID: every transitive child of PID, one per line. Deliberately a
# separate implementation from the suite's descendant_pids — this is the oracle.
tree_of() {
    ps -A -o pid= -o ppid= 2>/dev/null | awk -v root="$1" '
        { parent[$1] = $2 }
        END {
            keep[root] = 1
            changed = 1
            while (changed) {
                changed = 0
                for (p in parent) {
                    if (!(p in keep) && (parent[p] in keep)) { keep[p] = 1; print p; changed = 1 }
                }
            }
        }'
}

# proc_id PID: start time + executable — stable identity, so a PID the OS
# reused for an unrelated process is never mistaken for a leak.
proc_id() {
    ps -o lstart= -o comm= -p "$1" 2>/dev/null | sed 's/^ *//; s/ *$//'
}

# shellcheck disable=SC2329 # invoked via the EXIT trap
cleanup_check() {
    local pid
    for pid in $DECOY_CORE_PID $DECOY_LAUNCHER_PID; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 1
    for pid in $DECOY_CORE_PID $DECOY_LAUNCHER_PID; do
        kill -9 "$pid" 2>/dev/null || true
    done
    if [ "${KEEP_WORK:-0}" = "1" ] || [ "$FAILED" -ne 0 ]; then
        echo "Scratch dir (suite logs) kept: $WORK"
    else
        rm -rf "$WORK"
    fi
}
trap cleanup_check EXIT

# --- Preconditions -----------------------------------------------------------

if [ ! -x ./mcpproxy ]; then
    echo "FAIL: ./mcpproxy not found — go build -o mcpproxy ./cmd/mcpproxy"
    exit 1
fi
if [ ! -f "$E2E_SCRIPT" ]; then
    echo "FAIL: suite not found at $E2E_SCRIPT"
    exit 1
fi
for tool in jq lsof; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "FAIL: $tool is required"
        exit 1
    fi
done
if port_in_use "$LAUNCHER_PORT"; then
    echo "FAIL: :$LAUNCHER_PORT is in use (another E2E run?); the suite's launcher fixture needs it"
    exit 2
fi

E2E_PORT="$(free_port 18471)"
AUDIT_PORT="$(free_port $((E2E_PORT + 1)))"
DECOY_PORT="$(free_port $((AUDIT_PORT + 1)))"

# --- Decoys ------------------------------------------------------------------

mkdir -p "$DECOY_DIR/data" "$DECOY_DIR/home" "$DECOY_DIR/launcher-server"
jq -n --arg listen "127.0.0.1:${DECOY_PORT}" --arg dir "$DECOY_DIR/data" \
    '{listen: $listen, data_dir: $dir, enable_tray: false, enable_socket: false, mcpServers: []}' \
    > "$DECOY_DIR/config.json"

(cd "$DECOY_DIR" && HOME="$DECOY_DIR/home" MCPPROXY_TELEMETRY=false \
    exec "$REPO_ROOT/mcpproxy" serve --config="$DECOY_DIR/config.json" --log-level=warn) \
    > "$DECOY_DIR/core.log" 2>&1 &
DECOY_CORE_PID=$!

cat > "$DECOY_DIR/launcher-server/launcher-server" <<'SH'
#!/bin/sh
# Decoy: carries the launcher fixture's argv signature, binds nothing.
trap 'exit 0' TERM INT
while :; do sleep 1; done
SH
chmod +x "$DECOY_DIR/launcher-server/launcher-server"
(cd "$DECOY_DIR" && exec ./launcher-server/launcher-server --port "$LAUNCHER_PORT" --quiet) \
    > /dev/null 2>&1 &
DECOY_LAUNCHER_PID=$!

waited=0
while ! port_in_use "$DECOY_PORT"; do
    if [ $waited -ge 30 ] || ! kill -0 "$DECOY_CORE_PID" 2>/dev/null; then
        echo "FAIL: decoy core did not start listening on :$DECOY_PORT"
        cat "$DECOY_DIR/core.log"
        exit 1
    fi
    sleep 1
    waited=$((waited + 1))
done
echo "Decoy core PID $DECOY_CORE_PID on :$DECOY_PORT; decoy launcher PID $DECOY_LAUNCHER_PID"

# The check only bites if the decoys are exactly what the old pkill lines hit.
if pgrep -f "mcpproxy.*serve" | grep -qx "$DECOY_CORE_PID"; then
    pass "decoy core matches the old blanket pattern \"mcpproxy.*serve\""
else
    fail "decoy core does not match \"mcpproxy.*serve\" — this check would not bite"
fi
if pgrep -f "launcher-server.*--port $LAUNCHER_PORT" | grep -qx "$DECOY_LAUNCHER_PID"; then
    pass "decoy launcher matches the old blanket pattern \"launcher-server.*--port $LAUNCHER_PORT\""
else
    fail "decoy launcher does not match \"launcher-server.*--port $LAUNCHER_PORT\" — this check would not bite"
fi
[ $FAILED -eq 0 ] || exit 1

# --- Suite runs --------------------------------------------------------------

# run_suite MODE: run the suite, sampling its process tree; then assert the
# decoys survived and nothing it spawned outlived it.
run_suite() {
    local mode="$1"
    local log="$WORK/e2e-$mode.log"
    local seen="$WORK/seen-$mode.txt"
    : > "$seen"

    echo ""
    echo "=== Suite run: $mode (log: $log) ==="
    LISTEN_PORT="$E2E_PORT" AUDIT_LISTEN_PORT="$AUDIT_PORT" bash "$E2E_SCRIPT" > "$log" 2>&1 &
    local suite_pid=$!
    local elapsed=0 aborted=false pid ident tree

    while kill -0 "$suite_pid" 2>/dev/null; do
        tree="$(tree_of "$suite_pid")"
        for pid in $tree; do
            ident="$(proc_id "$pid")"
            [ -n "$ident" ] && echo "$pid $ident" >> "$seen"
        done
        if [ "$mode" = "abort" ] && [ "$aborted" = false ]; then
            local cmds=""
            [ -n "$tree" ] && cmds="$(ps -o command= -p "$(echo "$tree" | paste -sd, -)" 2>/dev/null)"
            if { [ $elapsed -ge $ABORT_AFTER ] \
                    && echo "$cmds" | grep -q "launcher-server.*--port $LAUNCHER_PORT" \
                    && echo "$cmds" | grep -q "server-everything"; } \
                || [ $elapsed -ge $ABORT_DEADLINE ]; then
                echo "Aborting suite (SIGTERM to PID $suite_pid) after ${elapsed}s with a live process tree"
                kill -TERM "$suite_pid" 2>/dev/null || true
                aborted=true
            fi
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    wait "$suite_pid"
    local rc=$?
    echo "Suite exited with status $rc after ~${elapsed}s"
    tail -3 "$log" | sed 's/^/  | /'
    if [ "$mode" = "abort" ] && [ "$aborted" = false ]; then
        fail "[$mode] suite ended before it could be aborted"
    fi
    if ! grep -q "Cleanup complete" "$log"; then
        fail "[$mode] cleanup() trap did not run to completion"
    fi

    # Decoys must have survived the cleanup trap.
    if kill -0 "$DECOY_CORE_PID" 2>/dev/null && port_in_use "$DECOY_PORT"; then
        pass "[$mode] decoy mcpproxy core survived (PID $DECOY_CORE_PID, :$DECOY_PORT)"
    else
        fail "[$mode] decoy mcpproxy core was killed by the suite's cleanup"
    fi
    if kill -0 "$DECOY_LAUNCHER_PID" 2>/dev/null; then
        pass "[$mode] decoy launcher-server survived (PID $DECOY_LAUNCHER_PID)"
    else
        fail "[$mode] decoy launcher-server was killed by the suite's cleanup"
    fi

    # Nothing the suite spawned may outlive it. Allow a short grace for
    # transient children (a `sleep` the SIGTERM interrupted) to exit on their own;
    # the processes that matter here (cores, node, the fixture) are long-lived.
    local leaked="" grace=0 recorded
    while :; do
        leaked=""
        while read -r pid recorded; do
            [ "$(proc_id "$pid")" = "$recorded" ] && leaked="$leaked $pid"
        done < <(sort -u "$seen")
        if [ -z "$leaked" ] || [ $grace -ge 10 ]; then
            break
        fi
        sleep 1
        grace=$((grace + 1))
    done
    if [ -z "$leaked" ]; then
        pass "[$mode] no leaked processes ($(sort -u "$seen" | wc -l | tr -d ' ') sampled from the suite's tree)"
    else
        fail "[$mode] processes spawned by the suite outlived it:"
        ps -o pid=,ppid=,command= -p "$(echo "$leaked" | xargs | tr ' ' ',')" 2>/dev/null | sed 's/^/  /'
        # They are ours: reap them so this check leaves the host clean.
        for pid in $leaked; do kill -9 "$pid" 2>/dev/null || true; done
    fi
    if port_in_use "$LAUNCHER_PORT" || port_in_use "$E2E_PORT"; then
        fail "[$mode] :$LAUNCHER_PORT or :$E2E_PORT still has a listener after the suite exited"
    fi
}

for mode in $CHECK_MODES; do
    case "$mode" in
        complete|abort) run_suite "$mode" ;;
        *) fail "unknown mode '$mode' (want complete|abort)" ;;
    esac
done

echo ""
if [ $FAILED -eq 0 ]; then
    echo "All cleanup checks passed."
    exit 0
fi
echo "Cleanup check FAILED."
exit 1
