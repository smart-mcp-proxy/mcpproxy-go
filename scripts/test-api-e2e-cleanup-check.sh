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

DECOY_PORT="${DECOY_PORT:-18099}"
DECOY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mcpproxy-e2e-cleanup-check.XXXXXX")"
DECOY_CONFIG="$DECOY_DIR/config.json"
DECOY_LOG="$DECOY_DIR/decoy.log"
DECOY_PID=""

cleanup_decoy() {
    if [ -n "$DECOY_PID" ] && kill -0 "$DECOY_PID" 2>/dev/null; then
        kill "$DECOY_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$DECOY_PID" 2>/dev/null || true
    fi
    rm -rf "$DECOY_DIR"
}
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
    if curl -s -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
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

if kill -0 "$DECOY_PID" 2>/dev/null && curl -s -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
    echo -e "${GREEN}PASS: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) survived the E2E run's cleanup trap.${NC}"
    exit 0
else
    echo -e "${RED}FAIL: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) did not survive the E2E run's cleanup trap.${NC}" >&2
    exit 1
fi
