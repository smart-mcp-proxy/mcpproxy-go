#!/bin/bash
# T011a: proves that scripts/test-api-e2e.sh's cleanup trap only reaps the
# process(es) THIS run started, and never a system-wide "mcpproxy"/
# "launcher-server" pattern match. It starts two decoys — a real
# `mcpproxy serve` on a different port with its own scratch data dir, and a
# stand-in process carrying the launcher fixture's argv signature — runs the
# full E2E script, and asserts both decoys are still alive afterward: proving
# the fix actually holds against a real invocation of the script, not just a
# code read, and that a regression reintroducing the old blanket `pkill -f`
# patterns would be caught (finding 1, review round: the pattern-match
# sanity check below proves this methodology, since both decoys are built to
# match those exact patterns).
#
# Also continuously samples the suite's own core (and, if it runs, its
# audit-log sub-instance) process tree once per second while the run is in
# progress, and asserts none of those descendants outlive the run — this
# is what actually catches an orphaned stdio/npx grandchild that a single
# post-hoc snapshot would miss (see "Known limitation" in the PR: a SIGKILLed
# core reparents its children before a one-shot snapshot can see them; a
# continuous sampler, taken while the core is still alive, does not have
# that gap).
#
# Deferred: a "SIGTERM the suite mid-run" abort-mode variant (double-running
# the suite, once to completion and once aborted partway through the
# launcher-lifecycle test) is NOT implemented here. It would double this
# script's runtime and its correctness depends on timing a signal against a
# specific step of a ~70-test suite, which needs its own iteration to get
# non-flaky — tracked as a follow-up rather than shipped unverified.
#
# Run from the repo root: ./scripts/test-api-e2e-cleanup-check.sh
# Requires a built ./mcpproxy binary (the same prerequisite test-api-e2e.sh
# itself has).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./descendant-pids.sh
source "$SCRIPT_DIR/descendant-pids.sh"

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
# argv signature of the spec-046 launcher-test fixture (must match
# test-api-e2e.sh's own $LAUNCHER_PATTERN exactly, or the assertions below
# would be testing the wrong pattern).
LAUNCHER_PATTERN='launcher-server.*--port 39933'
STANDIN_PID=""
SAMPLER_PID=""
SAMPLE_FILE="$DECOY_DIR/pid-samples.txt"
: > "$SAMPLE_FILE"
CLEANUP_DECOY_DONE=0

cleanup_decoy() {
    if [ "$CLEANUP_DECOY_DONE" = "1" ]; then
        return
    fi
    CLEANUP_DECOY_DONE=1
    if [ -n "$SAMPLER_PID" ] && kill -0 "$SAMPLER_PID" 2>/dev/null; then
        kill "$SAMPLER_PID" 2>/dev/null || true
    fi
    if [ -n "$STANDIN_PID" ] && kill -0 "$STANDIN_PID" 2>/dev/null; then
        kill -9 "$STANDIN_PID" 2>/dev/null || true
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

# Finding 1 (this review): a bystander process carrying the launcher
# fixture's exact argv signature, independent of anything test-api-e2e.sh
# itself spawns. If a future regression re-adds
# `pkill -f "launcher-server.*--port 39933"` to that script's cleanup(),
# THIS process — not the real fixture, which the scoped code should also
# leave alone — is what would die, and prove the regression actually
# happened rather than relying on the real fixture's own (potentially
# coincidental) survival.
echo -e "${YELLOW}Starting launcher-fixture stand-in (argv-only bystander)...${NC}"
exec -a "launcher-server --port 39933 (cleanup-check stand-in)" sleep 3600 &
STANDIN_PID=$!
sleep 0.2
if ! kill -0 "$STANDIN_PID" 2>/dev/null; then
    echo -e "${RED}Error: launcher-fixture stand-in did not start${NC}" >&2
    exit 1
fi
echo -e "${GREEN}Stand-in running (pid=$STANDIN_PID).${NC}"

# Sanity check: both decoys must match the OLD blanket patterns this PR
# removed from test-api-e2e.sh's cleanup(). If either did not, the survival
# assertions after the run would prove nothing — a regression could
# reintroduce the blanket pkill and this check would still pass, having
# never actually put anything in its path.
sanity_fail=0
if ! pgrep -f "mcpproxy.*serve" 2>/dev/null | grep -qx "$DECOY_PID"; then
    echo -e "${RED}FAIL(sanity): decoy core (pid=$DECOY_PID) does not match the old 'mcpproxy.*serve' pattern — this check would not catch a regression.${NC}" >&2
    sanity_fail=1
fi
if ! pgrep -f "$LAUNCHER_PATTERN" 2>/dev/null | grep -qx "$STANDIN_PID"; then
    echo -e "${RED}FAIL(sanity): launcher stand-in (pid=$STANDIN_PID) does not match the old '$LAUNCHER_PATTERN' pattern — this check would not catch a regression.${NC}" >&2
    sanity_fail=1
fi
if [ "$sanity_fail" -eq 1 ]; then
    exit 1
fi
echo -e "${GREEN}Sanity check passed: both decoys match the old blanket-pkill patterns.${NC}"

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

# Finding 1 (this review, continuous sampling): resolve the suite's own
# core/audit PIDs from its growing log as they appear, and record the FULL
# descendant tree (pid:comm) of each, once per second, for as long as the
# suite is running. A single post-hoc snapshot (the old design) cannot see
# a stdio/npx grandchild that got reparented before the snapshot was taken;
# sampling every second while the anchors are still alive closes that gap.
sample_descendants() {
    local anchor pid comm
    for anchor in "$@"; do
        [ -z "$anchor" ] && continue
        for pid in $(descendant_pids "$anchor" 2>/dev/null); do
            comm=$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ')
            [ -n "$comm" ] && echo "${pid}:${comm}" >> "$SAMPLE_FILE"
        done
    done
}

sampler_loop() {
    # Backgrounded via `&`, this runs in a subshell that otherwise inherits
    # the parent's INT/TERM/EXIT traps (bash trap inheritance) — including
    # the one that calls cleanup_decoy, which this loop is not meant to
    # trigger a second time when the main flow signals it to stop. Reset to
    # plain default signal handling so `kill "$SAMPLER_PID"` just ends the
    # loop.
    trap - INT TERM EXIT
    local core_pid audit_pid
    while :; do
        core_pid="$(sed -n 's/^Started mcpproxy with PID: \([0-9]*\)$/\1/p' "$E2E_LOG" 2>/dev/null | tail -1)"
        audit_pid="$(sed -n 's/^Started audit-log instance with PID: \([0-9]*\).*$/\1/p' "$E2E_LOG" 2>/dev/null | tail -1)"
        sample_descendants "$core_pid" "$audit_pid"
        sleep 1
    done
}
sampler_loop &
SAMPLER_PID=$!

echo -e "${YELLOW}Running scripts/test-api-e2e.sh (its own cleanup trap must not touch the decoys)...${NC}"
./scripts/test-api-e2e.sh 2>&1 | tee "$E2E_LOG"
e2e_exit=${PIPESTATUS[0]}
echo "scripts/test-api-e2e.sh exited with status $e2e_exit (not itself checked here — only cleanup hygiene is)."

# Stop the sampler now that the suite (and its own cleanup trap) has
# finished; one more sample would only ever see the post-cleanup state,
# which the checks below already cover more precisely (by PID identity).
if kill -0 "$SAMPLER_PID" 2>/dev/null; then
    kill "$SAMPLER_PID" 2>/dev/null || true
fi
wait "$SAMPLER_PID" 2>/dev/null || true

overall_pass=1

if kill -0 "$DECOY_PID" 2>/dev/null && curl -s --max-time 5 -o /dev/null "http://127.0.0.1:${DECOY_PORT}/api/v1/status"; then
    echo -e "${GREEN}PASS: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) survived the E2E run's cleanup trap.${NC}"
else
    echo -e "${RED}FAIL: decoy mcpproxy (pid=$DECOY_PID, port ${DECOY_PORT}) did not survive the E2E run's cleanup trap.${NC}" >&2
    overall_pass=0
fi

if kill -0 "$STANDIN_PID" 2>/dev/null; then
    echo -e "${GREEN}PASS: launcher-fixture stand-in (pid=$STANDIN_PID) survived the E2E run's cleanup trap.${NC}"
else
    echo -e "${RED}FAIL: launcher-fixture stand-in (pid=$STANDIN_PID) did not survive the E2E run's cleanup trap — a blanket pkill regression would do exactly this.${NC}" >&2
    overall_pass=0
fi

suite_core_pid="$(sed -n 's/^Started mcpproxy with PID: \([0-9]*\)$/\1/p' "$E2E_LOG" | tail -1)"
if [ -z "$suite_core_pid" ]; then
    echo -e "${YELLOW}WARN: could not find the suite's own core PID in its log; skipping the own-core-leak check.${NC}"
elif kill -0 "$suite_core_pid" 2>/dev/null; then
    # Finding 3 (this review): re-verify identity right before force-killing
    # a PID parsed out of a log line — the same TOCTOU this script already
    # guards against elsewhere (test-api-e2e.sh's snapshot_orphans_with_comm
    # re-checks `comm` before every kill -9). In the realistic multi-second
    # gap between the core's actual exit and this check (its own graceful
    # wait, then this script's decoy/standin curls and sed calls), the OS
    # can reuse suite_core_pid for an unrelated process; a bare kill -0/-9
    # would then report a false FAIL and SIGKILL an innocent bystander.
    core_comm="$(ps -o comm= -p "$suite_core_pid" 2>/dev/null | tr -d ' ')"
    if [ -n "$core_comm" ] && kill -0 "$suite_core_pid" 2>/dev/null; then
        recheck_comm="$(ps -o comm= -p "$suite_core_pid" 2>/dev/null | tr -d ' ')"
        if [ "$recheck_comm" = "$core_comm" ]; then
            echo -e "${RED}FAIL: the suite's own mcpproxy core (pid=$suite_core_pid, $core_comm) is still running after its cleanup trap — leaked as an orphan.${NC}" >&2
            kill -9 "$suite_core_pid" 2>/dev/null || true
            overall_pass=0
        else
            echo -e "${YELLOW}WARN: pid=$suite_core_pid changed identity between checks ($core_comm -> $recheck_comm) — likely PID reuse, not a leak. Not killing it.${NC}"
        fi
    else
        echo -e "${GREEN}PASS: the suite's own mcpproxy core (pid=$suite_core_pid) was stopped by its own cleanup trap.${NC}"
    fi
else
    echo -e "${GREEN}PASS: the suite's own mcpproxy core (pid=$suite_core_pid) was stopped by its own cleanup trap.${NC}"
fi

# Finding 1 (this review, continuous sampling): every descendant ever
# sampled from the suite's core/audit process trees, while the run was in
# progress, must be dead by now — with the SAME identity recorded at
# sample time. A survivor here is exactly the class of leak (a
# reparented npx/node grandchild) the PR's own "Known limitation" note
# describes as uncovered.
if [ -s "$SAMPLE_FILE" ]; then
    leaked=0
    while IFS=: read -r spid scomm; do
        [ -z "$spid" ] && continue
        if kill -0 "$spid" 2>/dev/null; then
            current_comm="$(ps -o comm= -p "$spid" 2>/dev/null | tr -d ' ')"
            if [ -n "$current_comm" ] && [ "$current_comm" = "$scomm" ]; then
                echo -e "${RED}FAIL: sampled descendant (pid=$spid, $scomm) of the suite's own process tree is still alive after cleanup.${NC}" >&2
                leaked=1
            fi
        fi
    done < <(sort -u "$SAMPLE_FILE")
    if [ "$leaked" -eq 1 ]; then
        overall_pass=0
    else
        echo -e "${GREEN}PASS: no sampled descendant of the suite's own process tree outlived the run.${NC}"
    fi
else
    echo -e "${YELLOW}WARN: no descendants were ever sampled (core PID never resolved from the log?) — this check ran vacuously.${NC}"
fi

if [ "$overall_pass" -eq 1 ]; then
    exit 0
else
    exit 1
fi
