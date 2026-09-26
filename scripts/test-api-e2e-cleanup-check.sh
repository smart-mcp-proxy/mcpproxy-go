#!/bin/bash
# T011a: proves that scripts/test-api-e2e.sh's cleanup trap only reaps the
# process(es) THIS run started, and never a system-wide "mcpproxy"/
# "launcher-server" pattern match. It starts a decoy `mcpproxy serve` on a
# different port with its own scratch data dir and config, runs the full
# E2E script, and asserts the decoy is still alive afterward — proving the
# fix actually holds against a real invocation of the script, not just a
# code read.
#
# Run from the repo root: ./scripts/test-api-e2e-cleanup-check.sh
# Requires a built ./mcpproxy binary (the same prerequisite test-api-e2e.sh
# itself has).

set -uo pipefail

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
CLEANUP_DECOY_DONE=0

cleanup_decoy() {
    if [ "$CLEANUP_DECOY_DONE" = "1" ]; then
        return
    fi
    CLEANUP_DECOY_DONE=1
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

# Live QA regression (fixed): cleanup() used to leak the suite's OWN main
# core as an orphan, because its identity (MCPPROXY_PID_ID) was captured
# with `ps -o comm=` immediately after backgrounding the serve command,
# racing the child's own execve() — `comm` briefly still names the shell,
# so the identity check at cleanup time (once the child had definitely
# exec'd into mcpproxy) never matched, and the suite's own core was treated
# as a foreign/reused PID and never stopped. Capture its PID from the run's
# own log (tee'd below) so we can assert, after the run, that it did NOT
# survive its own cleanup trap.
E2E_LOG="$DECOY_DIR/e2e-run.log"

echo -e "${YELLOW}Running scripts/test-api-e2e.sh (its own cleanup trap must not touch the decoy)...${NC}"
./scripts/test-api-e2e.sh 2>&1 | tee "$E2E_LOG"
e2e_exit=${PIPESTATUS[0]}
echo "scripts/test-api-e2e.sh exited with status $e2e_exit (not itself checked here — only cleanup hygiene is)."

overall_pass=1

if kill -0 "$DECOY_PID" 2>/dev/null && curl -s --max-time 5 -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
    echo -e "${GREEN}PASS: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) survived the E2E run's cleanup trap.${NC}"
else
    echo -e "${RED}FAIL: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) did not survive the E2E run's cleanup trap.${NC}" >&2
    overall_pass=0
fi

suite_core_pid="$(sed -n 's/^Started mcpproxy with PID: \([0-9]*\)$/\1/p' "$E2E_LOG" | tail -1)"
if [ -z "$suite_core_pid" ]; then
    echo -e "${YELLOW}WARN: could not find the suite's own core PID in its log; skipping the own-core-leak check.${NC}"
elif kill -0 "$suite_core_pid" 2>/dev/null; then
    echo -e "${RED}FAIL: the suite's own mcpproxy core (pid=$suite_core_pid) is still running after its cleanup trap — leaked as an orphan.${NC}" >&2
    kill -9 "$suite_core_pid" 2>/dev/null || true
    overall_pass=0
else
    echo -e "${GREEN}PASS: the suite's own mcpproxy core (pid=$suite_core_pid) was stopped by its own cleanup trap.${NC}"
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
