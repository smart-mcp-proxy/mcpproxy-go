#!/bin/bash
#
# Unit test for extract_api_key() in scripts/test-api-e2e.sh.
#
# SEC-01: the auto-generated API key is no longer written to the server log (it
# is the root credential and the log is a plaintext file at rest), so the E2E
# script reads it from the CONFIG FILE mcpproxy persists it to. This test pins
# that contract, including the two states the polling loop has to tolerate:
# a config file that does not exist yet, and one whose api_key is still empty.
#
# It extracts the *real* extract_api_key() function from test-api-e2e.sh so it
# stays in lockstep with the code under test.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="$SCRIPT_DIR/test-api-e2e.sh"

if [ ! -f "$TARGET" ]; then
    echo "FAIL: cannot find $TARGET"
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "FAIL: jq is required by extract_api_key()"
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

# Guard against a silent regression to log scraping: the function must not read
# a log file any more.
if echo "$FUNC_SRC" | grep -q 'log_file\|mcpproxy_e2e.log'; then
    echo "FAIL: extract_api_key() still reads the server log; the key is no longer logged (SEC-01)"
    exit 1
fi

EXPECTED_KEY="4197c426deadbeef0123456789abcdef"
FIXTURE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mcpproxy_key_fixture.XXXXXX")"
trap 'rm -rf "$FIXTURE_DIR"' EXIT

FAILED=0

check() {
    local label="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo "PASS: $label"
    else
        echo "FAIL: $label — expected '$expected' but got '$actual'"
        FAILED=1
    fi
}

# 1. A persisted config file carries the key.
POPULATED="$FIXTURE_DIR/populated.json"
cat > "$POPULATED" <<JSON
{
  "listen": "127.0.0.1:8081",
  "api_key": "$EXPECTED_KEY",
  "mcpServers": []
}
JSON
API_KEY=""
extract_api_key "$POPULATED" > /dev/null
check "reads api_key from the config file" "$EXPECTED_KEY" "$API_KEY"

# 2. The server has not written the key back yet — the polling loop must see an
#    empty value rather than a parse error or the literal "null".
EMPTY="$FIXTURE_DIR/empty.json"
cat > "$EMPTY" <<'JSON'
{
  "listen": "127.0.0.1:8081",
  "api_key": "",
  "mcpServers": []
}
JSON
API_KEY=""
extract_api_key "$EMPTY" > /dev/null
check "empty api_key yields an empty result" "" "$API_KEY"

# 3. Config file missing entirely (first poll, before the server writes it).
API_KEY=""
extract_api_key "$FIXTURE_DIR/does-not-exist.json" > /dev/null
check "missing config file yields an empty result" "" "$API_KEY"

# 4. Half-written config (atomic replace not finished) must not abort the loop.
PARTIAL="$FIXTURE_DIR/partial.json"
printf '{ "listen": "127.0.0.1:8081", "api_k' > "$PARTIAL"
API_KEY=""
extract_api_key "$PARTIAL" > /dev/null
check "truncated config file yields an empty result" "" "$API_KEY"

exit "$FAILED"
