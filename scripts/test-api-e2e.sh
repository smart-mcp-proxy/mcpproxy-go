#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
MCPPROXY_BINARY="./mcpproxy"
CONFIG_FILE="./test/e2e-config.json"
LISTEN_PORT="8081"
BASE_URL="http://localhost:${LISTEN_PORT}"
API_BASE="${BASE_URL}/api/v1"
TEST_DATA_DIR="./test-data"
MCPPROXY_PID=""
TEST_RESULTS_FILE="/tmp/mcpproxy_e2e_results.json"

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

# Wait for server to be ready
wait_for_server() {
    local max_attempts=30
    local attempt=1

    echo "Waiting for server to be ready..."

    while [ $attempt -le $max_attempts ]; do
        if curl -s -f "${BASE_URL}/api/v1/servers" > /dev/null 2>&1; then
            echo "Server is ready!"
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
        local connected=$(curl -s "${API_BASE}/servers" | jq -r '.data.servers[0].connected // false' 2>/dev/null)

        if [ "$connected" = "true" ]; then
            echo "Everything server is connected!"
            # Wait a bit more for indexing to complete
            sleep 3
            return 0
        fi

        echo "Attempt $attempt/$max_attempts - everything server connected: $connected"
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

    local curl_args=("-s" "-w" "%{http_code}" "-o" "$TEST_RESULTS_FILE")

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
        log_fail "$test_name - Expected status $expected_status, got $status_code"
        if [ -f "$TEST_RESULTS_FILE" ]; then
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
    timeout 5s curl -s -N "${BASE_URL}/events" | head -n 10 > "$TEST_RESULTS_FILE" 2>/dev/null || true

    if [ -s "$TEST_RESULTS_FILE" ] && grep -q "data:" "$TEST_RESULTS_FILE"; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name - No SSE events received"
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

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo -e "${RED}Error: Config file not found at $CONFIG_FILE${NC}"
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
    "jq -e '.success == true and .data.enabled == false' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 7: Enable server
test_api "POST /api/v1/servers/everything/enable" "POST" "${API_BASE}/servers/everything/enable" "200" "" \
    "jq -e '.success == true and .data.enabled == true' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 8: Restart server
test_api "POST /api/v1/servers/everything/restart" "POST" "${API_BASE}/servers/everything/restart" "200" "" \
    "jq -e '.success == true and .data.restarted == true' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 9: SSE Events
test_sse "GET /events (SSE)"

# Test 10: Error handling - invalid server
test_api "GET /api/v1/servers/nonexistent/tools" "GET" "${API_BASE}/servers/nonexistent/tools" "500" ""

# Test 11: Error handling - invalid search query
test_api "GET /api/v1/index/search (missing query)" "GET" "${API_BASE}/index/search" "400" ""

# Test 12: Error handling - invalid server action
test_api "POST /api/v1/servers/nonexistent/enable" "POST" "${API_BASE}/servers/nonexistent/enable" "500" ""

# Wait for everything server to reconnect after restart
echo ""
echo -e "${YELLOW}Waiting for everything server to reconnect after restart...${NC}"
if wait_for_everything_server; then
    echo -e "${GREEN}Everything server reconnected successfully${NC}"
else
    echo -e "${YELLOW}Warning: Everything server didn't reconnect, but tests can continue${NC}"
fi

# Test 13: Verify server is working after restart
test_api "GET /api/v1/servers (after restart)" "GET" "${API_BASE}/servers" "200" "" \
    "jq -e '.success == true and (.data.servers | length) > 0' < '$TEST_RESULTS_FILE' >/dev/null"

# Test 14: Test concurrent requests
echo ""
log_test "Concurrent API requests"

# Start concurrent requests
curl -s --max-time 10 "${API_BASE}/servers" > /dev/null &
PID1=$!
curl -s --max-time 10 "${API_BASE}/index/search?q=test" > /dev/null &
PID2=$!
curl -s --max-time 10 "${API_BASE}/servers/everything/tools" > /dev/null &
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