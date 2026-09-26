#!/usr/bin/env bash
# descendant-pids.sh — enumerate a process's descendants (recursively), by
# PID. Extracted from test-api-e2e.sh (T011a review round 4, finding F3) so
# it has a test of its own (descendant-pids.test.sh) instead of living
# inline where only an end-to-end run could exercise it.
#
# Used by scripts/test-api-e2e.sh's cleanup() so it reaps exactly what THIS
# run started/spawned instead of pattern-matching every
# "mcpproxy"/"launcher-server" process on the machine. A blanket `pkill -f`
# would also kill the user's own tray-managed core, or another worktree's
# parallel test run, whenever their command line happens to match the same
# substring.
#
# Usage:
#   source scripts/descendant-pids.sh   # defines descendant_pids() for the caller
#   ./scripts/descendant-pids.sh <pid>  # CLI: print descendants of <pid>, one per line

descendant_pids() {
    local parent="$1"
    local children
    children=$(pgrep -P "$parent" 2>/dev/null) || true
    local pid
    for pid in $children; do
        echo "$pid"
        descendant_pids "$pid"
    done
}

# Only run the CLI wrapper when executed directly, not when sourced as a
# library (test-api-e2e.sh sources this file for the function alone).
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    if [ $# -ne 1 ]; then
        echo "usage: $0 <pid>" >&2
        exit 2
    fi
    descendant_pids "$1"
fi
