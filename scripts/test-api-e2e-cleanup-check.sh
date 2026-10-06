#!/bin/bash
# T011a: proves that scripts/test-api-e2e.sh's cleanup trap only reaps the
# process(es) THIS run started, and never a system-wide "mcpproxy"/
# "launcher-server" pattern match. It starts a decoy `mcpproxy serve` on a
# different port with its own scratch data dir and config, runs the full
# E2E script, and asserts the decoy is still alive afterward — proving the
# fix actually holds against a real invocation of the script, not just a
# code read.
#
# Run from the repo root: ./scripts/test-api-e2e-cleanup-check.sh [--abort [WAIT_SECS]]
#
# --abort (issue #1388): instead of letting the suite run to completion, start
# it in the background in its own process group, wait up to WAIT_SECS (default
# 120) for the suite to touch $E2E_ABORT_MARKER (it does so mid-run, from
# test_launcher_lifecycle, once the core, launcher fixture and npx child are
# up), snapshot every descendant PID of the suite, SIGTERM the suite, and
# assert: the decoy survives, no new launcher-server --port 39933 process
# remains, and none of the snapshotted descendants survive. Exits non-zero
# listing the leaked PIDs. This exercises test-api-e2e.sh's INT/TERM trap
# mid-run, which a run-to-completion check never does.
# Requires a built ./mcpproxy binary (the same prerequisite test-api-e2e.sh
# itself has).

set -uo pipefail

ABORT_MODE=0
ABORT_WAIT=120
if [ "${1:-}" = "--abort" ]; then
    ABORT_MODE=1
    ABORT_WAIT="${2:-120}"
elif [ -n "${1:-}" ]; then
    echo "usage: $0 [--abort [WAIT_SECS]]" >&2
    exit 2
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Review round 4 (finding F6): a fixed default port collided between two
# concurrent runs of this script (only DECOY_DIR was made unique via
# mktemp), causing a flaky/misleading failure rather than a safety issue.
# Ask the OS for an ephemeral free port instead, same trick used elsewhere
# for scratch test instances (see memory reference_isolated_dev_instance).
find_free_port() {
    python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

DECOY_PORT="${DECOY_PORT:-$(find_free_port)}"
DECOY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mcpproxy-e2e-cleanup-check.XXXXXX")"
DECOY_CONFIG="$DECOY_DIR/config.json"
DECOY_LOG="$DECOY_DIR/decoy.log"
DECOY_PID=""
SUITE_PID=""
CLEANUP_DECOY_DONE=0

cleanup_decoy() {
    if [ "$CLEANUP_DECOY_DONE" = "1" ]; then
        return
    fi
    CLEANUP_DECOY_DONE=1
    if [ -n "${SUITE_PID:-}" ] && kill -0 "$SUITE_PID" 2>/dev/null; then
        kill -TERM "$SUITE_PID" 2>/dev/null || true
    fi
    if [ -n "$DECOY_PID" ] && kill -0 "$DECOY_PID" 2>/dev/null; then
        kill "$DECOY_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$DECOY_PID" 2>/dev/null || true
    fi
    rm -rf "$DECOY_DIR"
}
# See scripts/test-api-e2e.sh's cleanup trap for why INT/TERM are trapped
# explicitly alongside EXIT (review round 4, finding F5).
trap 'trap - EXIT; cleanup_decoy; exit 130' INT
trap 'trap - EXIT; cleanup_decoy; exit 143' TERM
trap cleanup_decoy EXIT

if [ ! -f "./mcpproxy" ]; then
    echo -e "${RED}Error: ./mcpproxy binary not found — build it first (go build -o mcpproxy ./cmd/mcpproxy)${NC}" >&2
    exit 1
fi

if [ ! -f "./scripts/test-api-e2e.sh" ]; then
    echo -e "${RED}Error: run this from the repo root (./scripts/test-api-e2e.sh not found)${NC}" >&2
    exit 1
fi

cat > "$DECOY_CONFIG" <<EOF
{
  "listen": "127.0.0.1:${DECOY_PORT}",
  "data_dir": "${DECOY_DIR}/data",
  "enable_tray": false,
  "enable_web_ui": false,
  "api_key": "",
  "mcpServers": []
}
EOF

echo -e "${YELLOW}Starting decoy mcpproxy serve on :${DECOY_PORT} (scratch data dir ${DECOY_DIR})...${NC}"
./mcpproxy serve --config="$DECOY_CONFIG" --log-level=error > "$DECOY_LOG" 2>&1 &
DECOY_PID=$!

# Wait for the decoy to come up.
ready=0
for _ in $(seq 1 30); do
    if ! kill -0 "$DECOY_PID" 2>/dev/null; then
        echo -e "${RED}Error: decoy mcpproxy exited early. Log:${NC}" >&2
        cat "$DECOY_LOG" >&2
        exit 1
    fi
    # Review round 4 (finding F7): every curl call in the sibling
    # test-api-e2e.sh uses --max-time; a wedged decoy (e.g. left over from a
    # prior leaked run holding the same port) could otherwise hang this
    # script indefinitely.
    if curl -s --max-time 5 -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
        ready=1
        break
    fi
    sleep 0.5
done
if [ "$ready" -ne 1 ]; then
    echo -e "${RED}Error: decoy mcpproxy never became ready. Log:${NC}" >&2
    cat "$DECOY_LOG" >&2
    exit 1
fi
echo -e "${GREEN}Decoy running (pid=$DECOY_PID).${NC}"

# Review round 6 (finding 5): snapshot PIDs matching the launcher-server
# fixture's port BEFORE running test-api-e2e.sh. Without this baseline, the
# T011a proof below fails on ANY matching process — including one leaked by
# an earlier crashed/kill -9'd run (SIGKILL bypasses that run's own cleanup
# trap) or a parallel worktree's own concurrent E2E run — even when THIS
# run's cleanup behaved perfectly, and keeps failing until someone manually
# reaps the stale process.
baseline_launcher_pids="$(pgrep -f 'launcher-server.*--port 39933' 2>/dev/null | sort)"

# descendants PID: print every recursive child PID of PID.
# shellcheck source=scripts/descendant-pids.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/descendant-pids.sh"
descendants() { descendant_pids "$@"; }

run_abort_mode() {
    local marker="$DECOY_DIR/abort.marker"
    local suite_log="$DECOY_DIR/suite.log"
    local suite_port
    suite_port="$(find_free_port)"

    echo -e "${YELLOW}--abort: starting scripts/test-api-e2e.sh in its own process group (LISTEN_PORT=${suite_port})...${NC}"
    # set -m gives the background job its own process group, so the SIGTERM
    # below (sent to the suite PID only) cannot reach this script.
    set -m
    E2E_ABORT_MARKER="$marker" LISTEN_PORT="$suite_port" ./scripts/test-api-e2e.sh > "$suite_log" 2>&1 &
    SUITE_PID=$!
    set +m

    local waited=0
    while [ ! -e "$marker" ]; do
        if ! kill -0 "$SUITE_PID" 2>/dev/null; then
            echo -e "${RED}FAIL: suite exited before reaching the abort marker. Log tail:${NC}" >&2
            tail -30 "$suite_log" >&2
            return 1
        fi
        if [ "$waited" -ge "$ABORT_WAIT" ]; then
            echo -e "${RED}FAIL: abort marker not seen within ${ABORT_WAIT}s.${NC}" >&2
            return 1
        fi
        sleep 1
        waited=$((waited + 1))
    done

    local snap_pids
    snap_pids="$(descendants "$SUITE_PID" | sort -u | sed '/^$/d')"
    # Record "pid:comm" so a recycled PID is not misreported as a leak.
    local snap="" pid
    for pid in $snap_pids; do
        snap="$snap $pid:$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ')"
    done
    echo "Marker seen after ${waited}s; snapshotted descendants:${snap:- (none)}"
    if [ -z "$snap_pids" ]; then
        echo -e "${RED}FAIL: no descendants snapshotted — the proof would be vacuous.${NC}" >&2
        return 1
    fi

    echo -e "${YELLOW}Sending SIGTERM to suite pid=$SUITE_PID...${NC}"
    kill -TERM "$SUITE_PID" 2>/dev/null || true
    waited=0
    while kill -0 "$SUITE_PID" 2>/dev/null && [ "$waited" -lt 90 ]; do
        sleep 1
        waited=$((waited + 1))
    done
    local ok=1
    if kill -0 "$SUITE_PID" 2>/dev/null; then
        echo -e "${RED}FAIL: suite still alive 90s after SIGTERM.${NC}" >&2
        kill -9 "$SUITE_PID" 2>/dev/null || true
        ok=0
    fi
    sleep 2

    local leaked="" entry comm cur
    for entry in $snap; do
        pid="${entry%%:*}"; comm="${entry#*:}"
        if kill -0 "$pid" 2>/dev/null; then
            cur="$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ')"
            if [ "$cur" = "$comm" ]; then
                leaked="$leaked $pid($comm)"
            fi
        fi
    done
    local new_launcher
    new_launcher="$(comm -13 <(printf '%s\n' "$baseline_launcher_pids") <(pgrep -f 'launcher-server.*--port 39933' 2>/dev/null | sort) | sed '/^$/d' | tr '\n' ' ')"

    if [ -n "$leaked" ]; then
        echo -e "${RED}FAIL: descendants survived SIGTERM:${leaked}${NC}" >&2
        ok=0
    fi
    if [ -n "$new_launcher" ]; then
        echo -e "${RED}FAIL: launcher-server --port 39933 still alive (pid(s): ${new_launcher})${NC}" >&2
        ok=0
    fi
    if kill -0 "$DECOY_PID" 2>/dev/null && curl -s --max-time 5 -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
        echo -e "${GREEN}PASS: decoy survived the aborted run.${NC}"
    else
        echo -e "${RED}FAIL: decoy did not survive the aborted run.${NC}" >&2
        ok=0
    fi
    if [ "$ok" -ne 1 ]; then
        echo "--- suite log tail ---" >&2
        tail -25 "$suite_log" >&2
    fi
    [ "$ok" -eq 1 ] && echo -e "${GREEN}PASS: SIGTERM mid-run left no leaked descendants.${NC}"
    [ "$ok" -eq 1 ]
}

if [ "$ABORT_MODE" -eq 1 ]; then
    run_abort_mode
    exit $?
fi

echo -e "${YELLOW}Running scripts/test-api-e2e.sh (its own cleanup trap must not touch the decoy)...${NC}"
./scripts/test-api-e2e.sh
e2e_exit=$?
echo "scripts/test-api-e2e.sh exited with status $e2e_exit (not itself checked here — only decoy survival is)."

overall_pass=1

if kill -0 "$DECOY_PID" 2>/dev/null && curl -s --max-time 5 -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
    echo -e "${GREEN}PASS: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) survived the E2E run's cleanup trap.${NC}"
else
    echo -e "${RED}FAIL: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) did not survive the E2E run's cleanup trap.${NC}" >&2
    overall_pass=0
fi

# Review round 4 (finding F3): the decoy-survival check above only proves
# the "don't kill unrelated processes" half. This proves the other half — a
# REAL orphan of THIS run (the launcher-test fixture's child process,
# test-api-e2e.sh's own test_launcher_lifecycle leaves it running again by
# design, see its Step 5) is actually gone once the run's cleanup trap has
# fired, regardless of whether mcpproxy's own graceful shutdown or the
# T011a orphan-reap fallback is what reaped it.
#
# Diffed against the baseline snapshot above (review round 6, finding 5): a
# PID present both before and after this run is a pre-existing stray, not
# something this run's cleanup failed to reap, and must not fail the proof —
# only a PID that is NEW since the baseline counts as this run's own orphan.
after_launcher_pids="$(pgrep -f 'launcher-server.*--port 39933' 2>/dev/null | sort)"
new_launcher_pids="$(comm -13 <(printf '%s\n' "$baseline_launcher_pids") <(printf '%s\n' "$after_launcher_pids") | sed '/^$/d')"

if [ -n "$new_launcher_pids" ]; then
    echo -e "${RED}FAIL: a NEW launcher-server fixture process from this E2E run is still alive after cleanup (pid(s): $(echo "$new_launcher_pids" | tr '\n' ' ')).${NC}" >&2
    overall_pass=0
elif [ -n "$baseline_launcher_pids" ]; then
    echo -e "${YELLOW}WARN: a launcher-server fixture process matching this pattern (pid(s): $(echo "$baseline_launcher_pids" | tr '\n' ' ')) already existed BEFORE this run — a pre-existing stray from an earlier run, not counted against this run's cleanup. Consider reaping it manually.${NC}"
    echo -e "${GREEN}PASS: this run did not leak a NEW launcher-server fixture process.${NC}"
else
    echo -e "${GREEN}PASS: the E2E run's own launcher-server fixture process was reaped.${NC}"
fi

if [ "$overall_pass" -eq 1 ]; then
    exit 0
else
    exit 1
fi
