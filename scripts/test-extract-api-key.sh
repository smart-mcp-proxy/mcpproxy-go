#!/bin/bash
#
# Unit test for extract_api_key() in scripts/test-api-e2e.sh (MCP-2404).
#
# The server log can contain NUL bytes and ANSI color codes. Without `grep -a`,
# grep treats the log as binary and emits "Binary file ... matches" instead of
# the match, so API_KEY becomes garbage and every authed curl is rejected.
#
# This test extracts the *real* extract_api_key() function from test-api-e2e.sh
# (so it stays in lockstep with the code under test) and runs it against a
# fixture log that mixes ANSI escapes, a NUL byte, and the api_key line.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="$SCRIPT_DIR/test-api-e2e.sh"

if [ ! -f "$TARGET" ]; then
    echo "FAIL: cannot find $TARGET"
    exit 1
fi

# Pull just the extract_api_key() function out of the real script and load it,
# without executing the rest of the (top-level) integration script.
FUNC_SRC="$(awk '/^extract_api_key\(\) \{/,/^\}/' "$TARGET")"
if [ -z "$FUNC_SRC" ]; then
    echo "FAIL: could not extract extract_api_key() from $TARGET"
    exit 1
fi
eval "$FUNC_SRC"

EXPECTED_KEY="4197c426deadbeef0123456789abcdef"
FIXTURE="$(mktemp -t mcpproxy_e2e_fixture.XXXXXX)"
trap 'rm -f "$FIXTURE"' EXIT

# Build a fixture log that reproduces the real-world failure: ANSI color codes
# plus an embedded NUL byte, then the api_key line.
{
    printf '\033[0;34m2026-06-14T12:00:00\033[0m starting server\n'
    printf 'some binary noise: \x00\x00 end\n'
    printf '{"level":"info","api_key": "%s","listen":"127.0.0.1:8081"}\n' "$EXPECTED_KEY"
} > "$FIXTURE"

# Sanity check: the fixture really does contain a NUL byte (the trigger).
if ! grep -qa $'\x00' "$FIXTURE"; then
    echo "FAIL: fixture is missing the NUL byte that triggers the bug"
    exit 1
fi

API_KEY=""
extract_api_key "$FIXTURE" > /dev/null

if [ "$API_KEY" = "$EXPECTED_KEY" ]; then
    echo "PASS: extract_api_key() returned the key from a NUL/ANSI log ($EXPECTED_KEY)"
    exit 0
else
    echo "FAIL: expected '$EXPECTED_KEY' but got '$API_KEY'"
    exit 1
fi
