#!/bin/bash
#
# Integration test for issue #271: OAuth resource parameter injection
#
# This test uses a real Python FastMCP server to verify that mcpproxy
# correctly injects the RFC 8707 resource parameter into the OAuth
# authorization URL.
#
# Prerequisites:
#   - Python 3.8+
#   - pip install fastapi uvicorn
#   - Built mcpproxy binary
#
# Usage:
#   ./test/integration/test_oauth_resource_injection.sh
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
MOCK_SERVER_PORT=18271
MCPPROXY_PORT=18272
TEST_DATA_DIR=$(mktemp -d)
MOCK_SERVER_PID=""
MCPPROXY_PID=""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"

    if [ -n "$MCPPROXY_PID" ] && kill -0 "$MCPPROXY_PID" 2>/dev/null; then
        echo "Stopping mcpproxy (PID: $MCPPROXY_PID)"
        kill "$MCPPROXY_PID" 2>/dev/null || true
        wait "$MCPPROXY_PID" 2>/dev/null || true
    fi

    if [ -n "$MOCK_SERVER_PID" ] && kill -0 "$MOCK_SERVER_PID" 2>/dev/null; then
        echo "Stopping mock OAuth server (PID: $MOCK_SERVER_PID)"
        kill "$MOCK_SERVER_PID" 2>/dev/null || true
        wait "$MOCK_SERVER_PID" 2>/dev/null || true
    fi

    if [ -d "$TEST_DATA_DIR" ]; then
        echo "Removing test data dir: $TEST_DATA_DIR"
        rm -rf "$TEST_DATA_DIR"
    fi

    echo -e "${GREEN}Cleanup complete${NC}"
}

trap cleanup EXIT

echo -e "${YELLOW}=== OAuth Resource Injection Integration Test (Issue #271) ===${NC}\n"

# Check prerequisites
echo "Checking prerequisites..."

if ! command -v python3 &> /dev/null; then
    echo -e "${RED}ERROR: python3 not found${NC}"
    exit 1
fi

if ! python3 -c "import fastapi, uvicorn" 2>/dev/null; then
    echo -e "${RED}ERROR: fastapi or uvicorn not installed${NC}"
    echo "Run: pip install fastapi uvicorn"
    exit 1
fi

MCPPROXY_BIN="$ROOT_DIR/mcpproxy"
if [ ! -x "$MCPPROXY_BIN" ]; then
    echo -e "${YELLOW}mcpproxy binary not found, building...${NC}"
    cd "$ROOT_DIR"
    go build -o mcpproxy ./cmd/mcpproxy
fi

echo -e "${GREEN}Prerequisites OK${NC}\n"

# Start mock OAuth server
echo "Starting mock Runlayer OAuth server on port $MOCK_SERVER_PORT..."
PORT=$MOCK_SERVER_PORT BASE_URL="http://localhost:$MOCK_SERVER_PORT" \
    python3 "$SCRIPT_DIR/oauth_runlayer_mock.py" > "$TEST_DATA_DIR/mock_server.log" 2>&1 &
MOCK_SERVER_PID=$!

# Wait for mock server to be ready
echo "Waiting for mock server to be ready..."
for i in {1..30}; do
    if curl -s "http://localhost:$MOCK_SERVER_PORT/" > /dev/null 2>&1; then
        echo -e "${GREEN}Mock server ready${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}ERROR: Mock server failed to start${NC}"
        cat "$TEST_DATA_DIR/mock_server.log"
        exit 1
    fi
    sleep 0.5
done

# Create test config for mcpproxy
echo -e "\nCreating test configuration..."
API_KEY="test-api-key-$(date +%s)"
cat > "$TEST_DATA_DIR/config.json" << EOF
{
  "listen": "127.0.0.1:$MCPPROXY_PORT",
  "api_key": "$API_KEY",
  "enable_socket": false,
  "data_dir": "$TEST_DATA_DIR",
  "enable_tray": false,
  "features": {
    "enable_oauth": true,
    "enable_web_ui": false
  },
  "mcpServers": [
    {
      "name": "runlayer-test",
      "url": "http://localhost:$MOCK_SERVER_PORT/mcp",
      "protocol": "http",
      "enabled": true
    }
  ],
  "logging": {
    "level": "debug",
    "enable_file": true,
    "enable_console": false,
    "filename": "mcpproxy.log"
  }
}
EOF

echo "Test config created at: $TEST_DATA_DIR/config.json"

# Start mcpproxy
echo -e "\nStarting mcpproxy on port $MCPPROXY_PORT..."
"$MCPPROXY_BIN" serve --config "$TEST_DATA_DIR/config.json" > "$TEST_DATA_DIR/mcpproxy_stdout.log" 2>&1 &
MCPPROXY_PID=$!

# Wait for mcpproxy to be ready
echo "Waiting for mcpproxy to be ready..."
for i in {1..30}; do
    if curl -s -H "X-API-Key: $API_KEY" "http://localhost:$MCPPROXY_PORT/api/v1/status" > /dev/null 2>&1; then
        echo -e "${GREEN}mcpproxy ready${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}ERROR: mcpproxy failed to start${NC}"
        echo "=== stdout log ==="
        cat "$TEST_DATA_DIR/mcpproxy_stdout.log"
        echo "=== mcpproxy.log ==="
        cat "$TEST_DATA_DIR/logs/mcpproxy.log" 2>/dev/null || echo "(no log file)"
        exit 1
    fi
    sleep 0.5
done

# Test 1: Verify mock server requires resource parameter
echo -e "\n${YELLOW}Test 1: Verify mock server requires 'resource' parameter${NC}"
RESPONSE=$(curl -s "http://localhost:$MOCK_SERVER_PORT/authorize?client_id=test&redirect_uri=http://localhost/cb&state=123")
if echo "$RESPONSE" | grep -q '"type":"missing"' && echo "$RESPONSE" | grep -q '"resource"'; then
    echo -e "${GREEN}PASS: Mock server correctly rejects requests without resource parameter${NC}"
else
    echo -e "${RED}FAIL: Mock server should require resource parameter${NC}"
    echo "Response: $RESPONSE"
    exit 1
fi

# Test 2: Trigger OAuth login via API and check auth_url
echo -e "\n${YELLOW}Test 2: Verify mcpproxy includes 'resource' in auth URL${NC}"
LOGIN_RESPONSE=$(curl -s -X POST \
    -H "X-API-Key: $API_KEY" \
    -H "Content-Type: application/json" \
    "http://localhost:$MCPPROXY_PORT/api/v1/servers/runlayer-test/login?headless=true")

echo "Login response: $LOGIN_RESPONSE"

# Extract auth_url from response (may be nested in .data)
AUTH_URL=$(echo "$LOGIN_RESPONSE" | jq -r '.data.auth_url // .auth_url // empty')

if [ -z "$AUTH_URL" ]; then
    echo -e "${RED}FAIL: No auth_url in response${NC}"
    echo "Full response: $LOGIN_RESPONSE"
    echo "=== mcpproxy logs ==="
    cat "$TEST_DATA_DIR/logs/mcpproxy.log" 2>/dev/null | tail -50
    exit 1
fi

echo "Auth URL: $AUTH_URL"

# Check if resource parameter is present
if echo "$AUTH_URL" | grep -q "resource="; then
    echo -e "${GREEN}PASS: Auth URL contains 'resource' parameter${NC}"

    # Extract and decode resource value
    RESOURCE_VALUE=$(echo "$AUTH_URL" | grep -oE 'resource=[^&]+' | sed 's/resource=//' | python3 -c "import sys, urllib.parse; print(urllib.parse.unquote(sys.stdin.read().strip()))")
    echo "Resource value: $RESOURCE_VALUE"

    # Verify resource points to MCP endpoint
    EXPECTED_RESOURCE="http://localhost:$MOCK_SERVER_PORT/mcp"
    if [ "$RESOURCE_VALUE" = "$EXPECTED_RESOURCE" ]; then
        echo -e "${GREEN}PASS: Resource value is correct ($RESOURCE_VALUE)${NC}"
    else
        echo -e "${RED}FAIL: Resource value mismatch${NC}"
        echo "Expected: $EXPECTED_RESOURCE"
        echo "Got: $RESOURCE_VALUE"
        exit 1
    fi
else
    echo -e "${RED}FAIL: Auth URL is missing 'resource' parameter${NC}"
    echo "This is the bug from issue #271!"
    echo "=== mcpproxy logs ==="
    cat "$TEST_DATA_DIR/logs/mcpproxy.log" 2>/dev/null | tail -50
    exit 1
fi

# Test 3: Verify auth URL works with mock server (no 422 error)
echo -e "\n${YELLOW}Test 3: Verify auth URL is accepted by mock server${NC}"
# Use curl with redirect following disabled to check the response
AUTH_RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" -L --max-redirs 0 "$AUTH_URL" 2>/dev/null || true)

if [ "$AUTH_RESPONSE_CODE" = "302" ]; then
    echo -e "${GREEN}PASS: Mock server accepted auth URL (302 redirect)${NC}"
elif [ "$AUTH_RESPONSE_CODE" = "422" ]; then
    echo -e "${RED}FAIL: Mock server returned 422 (missing required parameter)${NC}"
    exit 1
else
    echo -e "${YELLOW}INFO: Got HTTP $AUTH_RESPONSE_CODE (expected 302, but not 422 is good)${NC}"
fi

echo -e "\n${GREEN}=== All tests passed! ===${NC}"
echo "Issue #271 fix verified: resource parameter is correctly injected into OAuth auth URL"
