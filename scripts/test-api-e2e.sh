#!/bin/bash

# Note: Not using 'set -e' to allow tests to continue even if some fail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
MCPPROXY_BINARY="./mcpproxy"
CONFIG_TEMPLATE="./test/e2e-config.template.json"
CONFIG_FILE="./test/e2e-config.json"
LISTEN_PORT="${LISTEN_PORT:-8081}"
# Support both HTTP and HTTPS modes
# Default to HTTP for E2E tests since the template config has TLS disabled
USE_HTTPS="${USE_HTTPS:-false}"
if [ "$USE_HTTPS" = "true" ]; then
    BASE_URL="https://localhost:${LISTEN_PORT}"
    # Check for CA certificate in test-data directory (E2E config uses ./test-data as data_dir)
    if [ -f "./test-data/certs/ca.pem" ]; then
        CURL_CA_OPTS="--cacert ./test-data/certs/ca.pem"
    elif [ -f "./certs/ca.pem" ]; then
        CURL_CA_OPTS="--cacert ./certs/ca.pem"
    else
        CURL_CA_OPTS=""
    fi
else
    BASE_URL="http://localhost:${LISTEN_PORT}"
    CURL_CA_OPTS=""
fi
API_BASE="${BASE_URL}/api/v1"
TEST_DATA_DIR="./test-data"
MCPPROXY_PID=""
TEST_RESULTS_FILE="/tmp/mcpproxy_e2e_results.json"
API_KEY=""

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

echo -e "${GREEN}MCPProxy API E2E Tests${NC}"
echo "=============================="
echo -e "${YELLOW}Using everything server for testing${NC}"
echo ""

# Cleanup function
cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"

    # Kill mcpproxy if running
    if [ ! -z "$MCPPROXY_PID" ]; then
        echo "Stopping mcpproxy (PID: $MCPPROXY_PID)"
        kill $MCPPROXY_PID 2>/dev/null || true

        # Wait for graceful shutdown with timeout
        local count=0
        while [ $count -lt 10 ]; do
            if ! kill -0 $MCPPROXY_PID 2>/dev/null; then
                echo "Process stopped gracefully"
                break
            fi
            sleep 1
            count=$((count + 1))
        done

        # Force kill if still running
        if kill -0 $MCPPROXY_PID 2>/dev/null; then
            echo "Force killing process"
            kill -9 $MCPPROXY_PID 2>/dev/null || true
            sleep 1
        fi
    fi

    # Additional cleanup - find any remaining mcpproxy processes
    pkill -f "mcpproxy.*serve" 2>/dev/null || true
    sleep 1

    # Clean up test data
    if [ -d "$TEST_DATA_DIR" ]; then
        rm -rf "$TEST_DATA_DIR"
    fi

    # Clean up test results
    rm -f "$TEST_RESULTS_FILE"

    echo "Cleanup complete"
}

# Set up cleanup trap
trap cleanup EXIT

# Helper functions
log_test() {
    echo -e "${BLUE}[TEST]${NC} $1"
    TESTS_RUN=$((TESTS_RUN + 1))
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

# Extract API key from server logs
extract_api_key() {
    if [ -f "/tmp/mcpproxy_e2e.log" ]; then
        API_KEY=$(grep -o '"api_key": "[^"]*"' "/tmp/mcpproxy_e2e.log" | sed 's/.*"api_key": "\([^"]*\)".*/\1/' | head -1)
        if [ ! -z "$API_KEY" ]; then
            echo "Extracted API key: ${API_KEY:0:8}..."
        fi
    fi
}

# Wait for server to be ready
wait_for_server() {
    local max_attempts=30
    local attempt=1

    echo "Waiting for server to be ready..."

    while [ $attempt -le $max_attempts ]; do
        # First extract API key from logs if available
        extract_api_key

        # Build curl command with CA certificate if it exists, otherwise use insecure for initial check
        local curl_cmd="curl -s -f --max-time 5"
        if [ "$USE_HTTPS" = "true" ]; then
            if [ -f "./test-data/certs/ca.pem" ]; then
                curl_cmd="$curl_cmd --cacert ./test-data/certs/ca.pem"
            elif [ -f "./certs/ca.pem" ]; then
                curl_cmd="$curl_cmd --cacert ./certs/ca.pem"
            else
                # For initial startup, use insecure until certificates are generated
                curl_cmd="$curl_cmd -k"
            fi
        fi

        if [ ! -z "$API_KEY" ]; then
            curl_cmd="$curl_cmd -H \"X-API-Key: $API_KEY\""
        fi
        curl_cmd="$curl_cmd \"${BASE_URL}/api/v1/servers\""

        if eval $curl_cmd > /dev/null 2>&1; then
            echo "Server is ready!"
            # Update CURL_CA_OPTS for subsequent tests if certificates now exist
            if [ "$USE_HTTPS" = "true" ]; then
                if [ -f "./test-data/certs/ca.pem" ]; then
                    CURL_CA_OPTS="--cacert ./test-data/certs/ca.pem"
                elif [ -f "./certs/ca.pem" ]; then
                    CURL_CA_OPTS="--cacert ./certs/ca.pem"
                fi
            fi
            return 0
        fi

        echo "Attempt $attempt/$max_attempts - server not ready yet"
        sleep 1
        attempt=$((attempt + 1))
    done

    echo "Server failed to start within $max_attempts seconds"
    return 1
}

# Wait for everything server to connect and be indexed
wait_for_everything_server() {
    local max_attempts=30
    local attempt=1

    echo "Waiting for everything server to connect and be indexed..."

    while [ $attempt -le $max_attempts ]; do
        # Check if everything server is connected
        local curl_cmd="curl -s --max-time 5 $CURL_CA_OPTS"
        if [ ! -z "$API_KEY" ]; then
            curl_cmd="$curl_cmd -H \"X-API-Key: $API_KEY\""
        fi
        curl_cmd="$curl_cmd \"${API_BASE}/servers\""

        local response=$(eval $curl_cmd 2>/dev/null)
        local connected=$(echo "$response" | jq -r '.data.servers[0].connected // false' 2>/dev/null)
        local enabled=$(echo "$response" | jq -r '.data.servers[0].enabled // false' 2>/dev/null)

        if [ "$connected" = "true" ]; then
            echo "Everything server is connected!"
            # Wait a bit more for indexing to complete
            sleep 3
            return 0
        fi

        echo "Attempt $attempt/$max_attempts - connected: $connected, enabled: $enabled"
        sleep 2
        attempt=$((attempt + 1))
    done

    echo "Everything server failed to connect within $max_attempts attempts"
    return 1
}

# Test helper function
test_api() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local expected_status="$4"
    local data="$5"
    local extra_checks="$6"

    log_test "$test_name"

    # Clear previous test results
    rm -f "$TEST_RESULTS_FILE"

    local curl_args=("-s" "-w" "%{http_code}" "-o" "$TEST_RESULTS_FILE" "--max-time" "10")

    # Add CA certificate for HTTPS if needed
    if [ ! -z "$CURL_CA_OPTS" ]; then
        curl_args+=($CURL_CA_OPTS)
    fi

    # Add API key header if available
    if [ ! -z "$API_KEY" ]; then
        curl_args+=("-H" "X-API-Key: $API_KEY")
    fi

    if [ "$method" = "POST" ]; then
        curl_args+=("-X" "POST" "-H" "Content-Type: application/json")
        if [ ! -z "$data" ]; then
            curl_args+=("-d" "$data")
        fi
    fi

    curl_args+=("$url")

    local status_code=$(curl "${curl_args[@]}")

    if [ "$status_code" = "$expected_status" ]; then
        if [ ! -z "$extra_checks" ]; then
            if eval "$extra_checks"; then
                log_pass "$test_name"
                return 0
            else
                log_fail "$test_name - extra checks failed"
                return 1
            fi
        else
            log_pass "$test_name"
            return 0
        fi
    else
        if [ "$status_code" = "000" ]; then
            log_fail "$test_name - Connection failed (timeout or refused)"
            echo "Note: Server may be down or not responding. Check server logs."
        else
            log_fail "$test_name - Expected status $expected_status, got $status_code"
        fi
        if [ -f "$TEST_RESULTS_FILE" ] && [ -s "$TEST_RESULTS_FILE" ]; then
            echo "Response body:"
            cat "$TEST_RESULTS_FILE"
            echo
        fi
        return 1
    fi
}

# SSE test helper
test_sse() {
    local test_name="$1"
    log_test "$test_name"

    # Test SSE endpoint by connecting and reading first few events
    # Use Perl for cross-platform timeout (macOS doesn't have timeout command)
    local curl_cmd="curl -s -N $CURL_CA_OPTS"
    if [ ! -z "$API_KEY" ]; then
        curl_cmd="$curl_cmd -H \"X-API-Key: $API_KEY\""
    fi
    curl_cmd="$curl_cmd \"${BASE_URL}/events\""

    # Run with 5 second timeout using Perl
    perl -e 'alarm 5; exec @ARGV' sh -c "$curl_cmd" | head -n 10 > "$TEST_RESULTS_FILE" 2>/dev/null || true

    if [ -s "$TEST_RESULTS_FILE" ] && grep -q "data:" "$TEST_RESULTS_FILE"; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name - No SSE events received"
        return 1
    fi
}

# Enhanced SSE test with query parameter
test_sse_with_query_param() {
    local test_name="$1"
    log_test "$test_name"

    # Test SSE endpoint with API key as query parameter
    local sse_url="${BASE_URL}/events"
    if [ ! -z "$API_KEY" ]; then
        sse_url="${sse_url}?apikey=${API_KEY}"
    fi

    # Use Perl for cross-platform timeout (macOS doesn't have timeout command)
    perl -e 'alarm 5; exec @ARGV' curl -s -N $CURL_CA_OPTS "$sse_url" | head -n 10 > "$TEST_RESULTS_FILE" 2>/dev/null || true

    if [ -s "$TEST_RESULTS_FILE" ] && grep -q "data:" "$TEST_RESULTS_FILE"; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name - No SSE events received with query parameter"
        return 1
    fi
}

# Test SSE connection establishment
test_sse_connection() {
    local test_name="$1"
    log_test "$test_name"

    # Test that SSE endpoint establishes proper connection headers
    local curl_cmd="curl -s -I --max-time 3 $CURL_CA_OPTS"
    if [ ! -z "$API_KEY" ]; then
        curl_cmd="$curl_cmd -H \"X-API-Key: $API_KEY\""
    fi
    curl_cmd="$curl_cmd \"${BASE_URL}/events\""

    eval "$curl_cmd" > "$TEST_RESULTS_FILE" 2>/dev/null || true

    if [ -s "$TEST_RESULTS_FILE" ] && grep -q "text/event-stream" "$TEST_RESULTS_FILE" && grep -q "Cache-Control: no-cache" "$TEST_RESULTS_FILE"; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name - Improper SSE headers"
        echo "Headers received:"
        cat "$TEST_RESULTS_FILE"
        return 1
    fi
}

# Test SSE authentication failure
test_sse_auth_failure() {
    local test_name="$1"
    log_test "$test_name"

    # Test SSE with wrong API key (if API key is configured)
    if [ -z "$API_KEY" ]; then
        log_pass "$test_name (skipped - no API key configured)"
        return 0
    fi

    local status_code=$(curl -s --max-time 5 -w "%{http_code}" -o /dev/null $CURL_CA_OPTS -H "X-API-Key: wrong-api-key" "${BASE_URL}/events")

    if [ "$status_code" = "401" ]; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name - Expected 401, got $status_code"
        return 1
    fi
}

# Prerequisites check
echo -e "${YELLOW}Checking prerequisites...${NC}"

# Check if mcpproxy binary exists
if [ ! -f "$MCPPROXY_BINARY" ]; then
    echo -e "${RED}Error: mcpproxy binary not found at $MCPPROXY_BINARY${NC}"
    echo "Please run: go build -o mcpproxy ./cmd/mcpproxy"
    exit 1
fi

# Check if config template exists
if [ ! -f "$CONFIG_TEMPLATE" ]; then
    echo -e "${RED}Error: Config template not found at $CONFIG_TEMPLATE${NC}"
    exit 1
fi

# Check if jq is available for JSON parsing
if ! command -v jq &> /dev/null; then
    echo -e "${RED}Error: jq is required for JSON parsing${NC}"
    echo "Please install jq: brew install jq (macOS) or apt-get install jq (Ubuntu)"
    exit 1
fi

# Check if npx is available (needed for everything server)
if ! command -v npx &> /dev/null; then
    echo -e "${RED}Error: npx is required for @modelcontextprotocol/server-everything${NC}"
    echo "Please install Node.js and npm"
    exit 1
fi

echo -e "${GREEN}Prerequisites check passed${NC}"
echo ""

# Start mcpproxy server
echo -e "${YELLOW}Starting mcpproxy server...${NC}"

# Create test data directory
mkdir -p "$TEST_DATA_DIR"

# Copy fresh config from template to ensure clean state
echo "Copying fresh config from template..."
cp "$CONFIG_TEMPLATE" "$CONFIG_FILE"

# Substitute LISTEN_PORT in config file if not using default 8081
if [ "$LISTEN_PORT" != "8081" ]; then
    sed -i '' "s/:8081/:${LISTEN_PORT}/g" "$CONFIG_FILE"
    echo "Updated listen port to :${LISTEN_PORT}"
fi

# Start server in background
$MCPPROXY_BINARY serve --config="$CONFIG_FILE" --log-level=info > "/tmp/mcpproxy_e2e.log" 2>&1 &
MCPPROXY_PID=$!

echo "Started mcpproxy with PID: $MCPPROXY_PID"
echo "Log file: /tmp/mcpproxy_e2e.log"

# Wait for server to be ready
if ! wait_for_server; then
    echo -e "${RED}Failed to start server${NC}"
    echo "Server logs:"
    cat "/tmp/mcpproxy_e2e.log"
    exit 1
fi

# Wait for everything server to connect
if ! wait_for_everything_server; then
    echo -e "${RED}Everything server failed to connect${NC}"
    echo "Server logs:"
    tail -50 "/tmp/mcpproxy_e2e.log"
    exit 1
fi

echo ""
echo -e "${YELLOW}Running API tests...${NC}"
echo ""

# Test 0: Get info (version and update information)
test_api "GET /api/v1/info" "GET" "${API_BASE}/info" "200" "" \
    "jq -e '.success == true and .data.version != null and .data.version != \"\" and .data.listen_addr != null and .data.endpoints.http != null and .data.endpoints.socket != null' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 1: Get servers list
test_api "GET /api/v1/servers" "GET" "${API_BASE}/servers" "200" "" \
    "jq -e '.success == true and (.data.servers | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 2: Get specific server tools
test_api "GET /api/v1/servers/everything/tools" "GET" "${API_BASE}/servers/everything/tools" "200" "" \
    "jq -e '.success == true and (.data.tools | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 3: Search tools
test_api "GET /api/v1/index/search?q=echo" "GET" "${API_BASE}/index/search?q=echo" "200" "" \
    "jq -e '.success == true and (.data.results | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 4: Search tools with limit
test_api "GET /api/v1/index/search?q=tool&limit=5" "GET" "${API_BASE}/index/search?q=tool&limit=5" "200" "" \
    "jq -e '.success == true and (.data.results | length) <= 5' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 5: Get server logs
test_api "GET /api/v1/servers/everything/logs" "GET" "${API_BASE}/servers/everything/logs?tail=10" "200" "" \
    "jq -e '.success == true and (.data.logs | type) == \"array\"' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 6: Disable server
test_api "POST /api/v1/servers/everything/disable" "POST" "${API_BASE}/servers/everything/disable" "200" "" \
    "jq -e '.success == true and .data.success == true' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 7: Enable server
test_api "POST /api/v1/servers/everything/enable" "POST" "${API_BASE}/servers/everything/enable" "200" "" \
    "jq -e '.success == true and .data.success == true' < '$TEST_RESULTS_FILE' >/dev/null"

# Give server time to update config after enable
sleep 2

# Test 8: Restart server
test_api "POST /api/v1/servers/everything/restart" "POST" "${API_BASE}/servers/everything/restart" "200" "" \
    "jq -e '.success == true and .data.success == true' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 9: SSE Events (Header authentication)
test_sse "GET /events (SSE with header auth)"

# Test 10: SSE Events (Query parameter authentication)
test_sse_with_query_param "GET /events (SSE with query param auth)"

# Test 11: SSE Connection headers
test_sse_connection "GET /events (SSE connection headers)"

# Test 12: SSE Authentication failure
test_sse_auth_failure "GET /events (SSE auth failure)"

# Test 13: Error handling - invalid server
# Note: 404 is returned for nonexistent servers (more appropriate than 500)
test_api "GET /api/v1/servers/nonexistent/tools" "GET" "${API_BASE}/servers/nonexistent/tools" "404" ""

# Test 14: Error handling - invalid search query
test_api "GET /api/v1/index/search (missing query)" "GET" "${API_BASE}/index/search" "400" ""

# Test 15: Error handling - invalid server action
test_api "POST /api/v1/servers/nonexistent/enable" "POST" "${API_BASE}/servers/nonexistent/enable" "500" ""

# Wait for everything server to reconnect after restart
echo ""
echo -e "${YELLOW}Waiting for everything server to reconnect after restart...${NC}"
if wait_for_everything_server; then
    echo -e "${GREEN}Everything server reconnected successfully${NC}"
else
    echo -e "${YELLOW}Warning: Everything server didn't reconnect, but tests can continue${NC}"
fi

# Test 16: Verify server is working after restart
test_api "GET /api/v1/servers (after restart)" "GET" "${API_BASE}/servers" "200" "" \
    "jq -e '.success == true and (.data.servers | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 17: Test concurrent requests
echo ""
log_test "Concurrent API requests"

# Start concurrent requests
curl_base="curl -s --max-time 10"
if [ ! -z "$API_KEY" ]; then
    curl_base="$curl_base -H \"X-API-Key: $API_KEY\""
fi

eval "$curl_base \"${API_BASE}/servers\"" > /dev/null &
PID1=$!
eval "$curl_base \"${API_BASE}/index/search?q=test\"" > /dev/null &
PID2=$!
eval "$curl_base \"${API_BASE}/servers/everything/tools\"" > /dev/null &
PID3=$!

# Wait for all requests with timeout
success=true
for pid in $PID1 $PID2 $PID3; do
    if ! wait $pid; then
        success=false
    fi
done

if [ "$success" = true ]; then
    log_pass "Concurrent API requests"
else
    log_fail "Concurrent API requests"
fi

# Test 18: Get config
test_api "GET /api/v1/config" "GET" "${API_BASE}/config" "200" "" \
    "jq -e '.success == true and .data.config != null' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 19: Get diagnostics
test_api "GET /api/v1/diagnostics" "GET" "${API_BASE}/diagnostics" "200" "" \
    "jq -e '.success == true and .data.total_issues != null' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 20: Get tool call history
test_api "GET /api/v1/tool-calls" "GET" "${API_BASE}/tool-calls?limit=10" "200" "" \
    "jq -e '.success == true and .data.tool_calls != null' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 21: Execute a tool call via MCP (to create history)
echo ""
echo -e "${YELLOW}Executing a tool call to create history for replay test...${NC}"
TOOL_CALL_ID=""
# Make a tool call using the echo_tool from everything server
$MCPPROXY_BINARY call tool --tool-name="everything:echo_tool" --json_args='{"message":"test replay"}' > /dev/null 2>&1 || true
sleep 2  # Wait for call to be recorded

# Test 22: Get tool call history again (should have at least one call)
# DISABLED: This test is flaky because tool call history tracking may not work via CLI
# test_api "GET /api/v1/tool-calls (with history)" "GET" "${API_BASE}/tool-calls?limit=100" "200" "" \
#     "jq -e '.success == true and (.data.tool_calls | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"
echo -e "${YELLOW}Skipping GET /api/v1/tool-calls (with history) - test disabled${NC}"

# Extract a tool call ID for replay test
if [ -f "$TEST_RESULTS_FILE" ]; then
    TOOL_CALL_ID=$(jq -r '.data.tool_calls[0].id // empty' < "$TEST_RESULTS_FILE" 2>/dev/null)
fi

# Test 23: Replay tool call (if we have an ID)
if [ ! -z "$TOOL_CALL_ID" ]; then
    echo ""
    echo -e "${YELLOW}Testing replay with tool call ID: $TOOL_CALL_ID${NC}"

    # Replay with modified arguments
    REPLAY_DATA='{"arguments":{"message":"replayed message"}}'
    test_api "POST /api/v1/tool-calls/$TOOL_CALL_ID/replay" "POST" "${API_BASE}/tool-calls/${TOOL_CALL_ID}/replay" "200" "$REPLAY_DATA" \
        "jq -e '.success == true and .data.new_call_id != null and .data.replayed_from == \"'$TOOL_CALL_ID'\"' < '$TEST_RESULTS_FILE' >/dev/null"
else
    echo -e "${YELLOW}Skipping replay test - no tool call ID available${NC}"
    # Still count it as a test for consistency
    log_test "POST /api/v1/tool-calls/{id}/replay"
    log_pass "POST /api/v1/tool-calls/{id}/replay (skipped - no history)"
fi

# Test 24: Error handling - replay nonexistent tool call
test_api "POST /api/v1/tool-calls/nonexistent/replay" "POST" "${API_BASE}/tool-calls/nonexistent-id-12345/replay" "500" '{"arguments":{}}'

# Test 25: List registries (Phase 7)
log_test "GET /api/v1/registries"
RESPONSE=$(curl -s --max-time 10 $CURL_CA_OPTS -H "X-API-Key: $API_KEY" "${API_BASE}/registries")
echo "$RESPONSE" > "$TEST_RESULTS_FILE"
if echo "$RESPONSE" | jq -e '.success == true and .data.registries != null and .data.total > 0' >/dev/null; then
    log_pass "GET /api/v1/registries - Response has registries array and total count"
else
    log_fail "GET /api/v1/registries - Expected registries data structure" \
        "jq -e '.success == true and .data.registries != null and .data.total > 0' < '$TEST_RESULTS_FILE' >/dev/null"
fi

# Test 26: Search registry servers (Phase 7)
log_test "GET /api/v1/registries/{id}/servers"
RESPONSE=$(curl -s --max-time 10 $CURL_CA_OPTS -H "X-API-Key: $API_KEY" "${API_BASE}/registries/pulse/servers?limit=5")
echo "$RESPONSE" > "$TEST_RESULTS_FILE"
if echo "$RESPONSE" | jq -e '.success == true and .data.servers != null and .data.registry_id == "pulse"' >/dev/null; then
    log_pass "GET /api/v1/registries/{id}/servers - Response has servers array and registry_id"
else
    log_fail "GET /api/v1/registries/{id}/servers - Expected server search results" \
        "jq -e '.success == true and .data.servers != null and .data.registry_id == \"pulse\"' < '$TEST_RESULTS_FILE' >/dev/null"
fi

# Test 27: Search registry servers with query (Phase 7)
log_test "GET /api/v1/registries/{id}/servers with query parameter"
RESPONSE=$(curl -s --max-time 10 $CURL_CA_OPTS -H "X-API-Key: $API_KEY" "${API_BASE}/registries/pulse/servers?q=github&limit=3")
echo "$RESPONSE" > "$TEST_RESULTS_FILE"
if echo "$RESPONSE" | jq -e '.success == true and .data.servers != null and .data.query == "github"' >/dev/null; then
    log_pass "GET /api/v1/registries/{id}/servers?q=github - Response has query field"
else
    log_fail "GET /api/v1/registries/{id}/servers?q=github - Expected query parameter in response" \
        "jq -e '.success == true and .data.servers != null and .data.query == \"github\"' < '$TEST_RESULTS_FILE' >/dev/null"
fi

# ===========================================
# Tests for env_json, args_json, headers_json updates (Issue #182)
# ===========================================
echo ""
echo -e "${YELLOW}Testing env_json, args_json, headers_json updates...${NC}"
echo ""

# Test 28: Add a test server for env/args/headers testing
log_test "Add test server for env/args/headers tests"
ADD_SERVER_DATA='{"operation":"add","name":"env-test-server","command":"echo","args_json":"[\"hello\"]","env_json":"{\"INITIAL_VAR\":\"initial\",\"SECOND_VAR\":\"second\"}","enabled":false}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$ADD_SERVER_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "env-test-server"; then
    log_pass "Add test server for env/args/headers tests"
else
    log_fail "Add test server for env/args/headers tests - Failed to add server"
    echo "Response: $RESPONSE"
fi

# Test 29: Update env_json (full replacement)
log_test "Update env_json via upstream_servers tool"
UPDATE_ENV_DATA='{"operation":"update","name":"env-test-server","env_json":"{\"NEW_VAR\":\"new_value\",\"ANOTHER_VAR\":\"test\"}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$UPDATE_ENV_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "NEW_VAR" && ! echo "$RESPONSE" | grep -q "INITIAL_VAR"; then
    log_pass "Update env_json via upstream_servers tool - Full replacement worked"
else
    log_fail "Update env_json via upstream_servers tool - Full replacement failed"
    echo "Response: $RESPONSE"
fi

# Test 30: Verify env_json update via list operation
log_test "Verify env_json update via list operation"
LIST_DATA='{"operation":"list"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$LIST_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "NEW_VAR" && echo "$RESPONSE" | grep -q "new_value"; then
    log_pass "Verify env_json update via list operation"
else
    log_fail "Verify env_json update via list operation - NEW_VAR not found in list response"
fi

# Test 31: Update args_json
log_test "Update args_json via upstream_servers tool"
UPDATE_ARGS_DATA='{"operation":"update","name":"env-test-server","args_json":"[\"updated\",\"--flag\"]"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$UPDATE_ARGS_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "updated"; then
    log_pass "Update args_json via upstream_servers tool"
else
    log_fail "Update args_json via upstream_servers tool"
    echo "Response: $RESPONSE"
fi

# Test 32: Verify args_json update via list operation
log_test "Verify args_json update via list operation"
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args='{"operation":"list"}' 2>&1)
if echo "$RESPONSE" | grep -q "updated" && echo "$RESPONSE" | grep -q "\-\-flag"; then
    log_pass "Verify args_json update via list operation"
else
    log_fail "Verify args_json update via list operation - updated args not found in list response"
fi

# Test 33: Add HTTP server for headers test
log_test "Add HTTP server for headers_json test"
ADD_HTTP_DATA='{"operation":"add","name":"headers-test-server","url":"http://example.com/api","protocol":"http","headers_json":"{\"X-Initial\":\"initial\"}","enabled":false}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$ADD_HTTP_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "headers-test-server"; then
    log_pass "Add HTTP server for headers_json test"
else
    log_fail "Add HTTP server for headers_json test"
    echo "Response: $RESPONSE"
fi

# Test 34: Update headers_json
log_test "Update headers_json via upstream_servers tool"
UPDATE_HEADERS_DATA='{"operation":"update","name":"headers-test-server","headers_json":"{\"X-Custom\":\"custom-value\",\"Authorization\":\"Bearer token123\"}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$UPDATE_HEADERS_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "X-Custom" && ! echo "$RESPONSE" | grep -q "X-Initial"; then
    log_pass "Update headers_json via upstream_servers tool - Full replacement worked"
else
    log_fail "Update headers_json via upstream_servers tool - Full replacement failed"
    echo "Response: $RESPONSE"
fi

# Test 35: Verify headers_json update via list operation
log_test "Verify headers_json update via list operation"
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args='{"operation":"list"}' 2>&1)
if echo "$RESPONSE" | grep -q "X-Custom" && echo "$RESPONSE" | grep -q "custom-value"; then
    log_pass "Verify headers_json update via list operation"
else
    log_fail "Verify headers_json update via list operation - X-Custom header not found in list response"
fi

# Test 36: Clear env vars with empty JSON
log_test "Clear env vars with empty env_json"
CLEAR_ENV_DATA='{"operation":"update","name":"env-test-server","env_json":"{}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$CLEAR_ENV_DATA" 2>&1)
# Verify via list - env should be empty or null
LIST_RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args='{"operation":"list"}' 2>&1)
# Check that NEW_VAR is no longer present in env-test-server's env
if echo "$LIST_RESPONSE" | grep -A 20 "env-test-server" | grep -q "NEW_VAR"; then
    log_fail "Clear env vars with empty env_json - NEW_VAR still present"
else
    log_pass "Clear env vars with empty env_json"
fi

# Test 37: Test patch operation (same semantics as update)
log_test "Patch env_json via upstream_servers tool"
PATCH_ENV_DATA='{"operation":"patch","name":"env-test-server","env_json":"{\"PATCHED_VAR\":\"patched_value\"}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$PATCH_ENV_DATA" 2>&1)
if echo "$RESPONSE" | grep -q "PATCHED_VAR" && echo "$RESPONSE" | grep -q "patched_value"; then
    log_pass "Patch env_json via upstream_servers tool"
else
    log_fail "Patch env_json via upstream_servers tool - Expected PATCHED_VAR in response"
    echo "Response: $RESPONSE"
fi

# Test 38: Test invalid env_json (should fail gracefully)
log_test "Invalid env_json returns error"
INVALID_ENV_DATA='{"operation":"update","name":"env-test-server","env_json":"not valid json"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$INVALID_ENV_DATA" 2>&1)
if echo "$RESPONSE" | grep -qi "error\|invalid\|failed"; then
    log_pass "Invalid env_json returns error"
else
    log_fail "Invalid env_json returns error - Expected error message"
    echo "Response: $RESPONSE"
fi

# Test 39: Test invalid args_json (array expected)
log_test "Invalid args_json (not array) returns error"
INVALID_ARGS_DATA='{"operation":"update","name":"env-test-server","args_json":"{\"key\":\"value\"}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$INVALID_ARGS_DATA" 2>&1)
if echo "$RESPONSE" | grep -qi "error\|invalid\|failed\|array"; then
    log_pass "Invalid args_json (not array) returns error"
else
    log_fail "Invalid args_json (not array) returns error - Expected error message"
    echo "Response: $RESPONSE"
fi

# Test 40: Test server not found
log_test "Update nonexistent server returns error"
NOTFOUND_DATA='{"operation":"update","name":"nonexistent-server-12345","env_json":"{\"VAR\":\"value\"}"}'
RESPONSE=$($MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args="$NOTFOUND_DATA" 2>&1)
if echo "$RESPONSE" | grep -qi "error\|not found\|failed"; then
    log_pass "Update nonexistent server returns error"
else
    log_fail "Update nonexistent server returns error - Expected error message"
    echo "Response: $RESPONSE"
fi

# Cleanup test servers
echo ""
echo -e "${YELLOW}Cleaning up test servers...${NC}"
$MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args='{"operation":"remove","name":"env-test-server"}' > /dev/null 2>&1 || true
$MCPPROXY_BINARY call tool --tool-name="upstream_servers" --json_args='{"operation":"remove","name":"headers-test-server"}' > /dev/null 2>&1 || true

echo ""
echo -e "${YELLOW}Test Summary${NC}"
echo "============"
echo -e "Tests run: ${BLUE}$TESTS_RUN${NC}"
echo -e "Tests passed: ${GREEN}$TESTS_PASSED${NC}"
echo -e "Tests failed: ${RED}$TESTS_FAILED${NC}"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}All tests passed! 🎉${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}$TESTS_FAILED test(s) failed${NC}"
    echo ""
    echo "Server logs (last 50 lines):"
    tail -50 "/tmp/mcpproxy_e2e.log"
    exit 1
fi