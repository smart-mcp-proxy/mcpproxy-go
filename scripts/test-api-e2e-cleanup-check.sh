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
if pgrep -f 'launcher-server.*--port 39933' > /dev/null 2>&1; then
    echo -e "${RED}FAIL: a launcher-server fixture process from the E2E run is still alive after cleanup.${NC}" >&2
    overall_pass=0
else
    echo -e "${GREEN}PASS: the E2E run's own launcher-server fixture process was reaped.${NC}"
fi

if [ "$overall_pass" -eq 1 ]; then
    exit 0
else
    exit 1
fi
