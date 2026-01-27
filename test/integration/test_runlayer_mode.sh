#!/bin/bash
#
# Integration test for Go OAuth server Runlayer mode
#
# This test verifies that the Go OAuth test server correctly implements
# Runlayer-style strict validation with Pydantic 422 error responses.
#
# Usage:
#   ./test/integration/test_runlayer_mode.sh
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
OAUTH_SERVER_PORT=18277
OAUTH_SERVER_PID=""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"

    if [ -n "$OAUTH_SERVER_PID" ] && kill -0 "$OAUTH_SERVER_PID" 2>/dev/null; then
        echo "Stopping OAuth server (PID: $OAUTH_SERVER_PID)"
        kill "$OAUTH_SERVER_PID" 2>/dev/null || true
        wait "$OAUTH_SERVER_PID" 2>/dev/null || true
    fi

    # Kill any remaining processes on our port
    lsof -ti :$OAUTH_SERVER_PORT 2>/dev/null | xargs kill -9 2>/dev/null || true

    echo -e "${GREEN}Cleanup complete${NC}"
}

trap cleanup EXIT

echo -e "${YELLOW}=== Go OAuth Server Runlayer Mode Integration Test ===${NC}\n"

# Start OAuth test server in Runlayer mode
echo "Starting Go OAuth test server in Runlayer mode on port $OAUTH_SERVER_PORT..."
cd "$ROOT_DIR"
go run ./tests/oauthserver/cmd/server -port $OAUTH_SERVER_PORT -runlayer-mode > /dev/null 2>&1 &
OAUTH_SERVER_PID=$!

# Wait for server to be ready
echo "Waiting for server to be ready..."
for i in {1..30}; do
    if curl -s "http://localhost:$OAUTH_SERVER_PORT/.well-known/oauth-authorization-server" > /dev/null 2>&1; then
        echo -e "${GREEN}OAuth server ready${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}ERROR: OAuth server failed to start${NC}"
        exit 1
    fi
    sleep 0.5
done

# Test 1: Verify 422 Pydantic error when resource is missing
echo -e "\n${YELLOW}Test 1: Verify Pydantic 422 error when resource is missing${NC}"
RESPONSE=$(curl -s "http://localhost:$OAUTH_SERVER_PORT/authorize?client_id=test-client&redirect_uri=http://127.0.0.1/callback&response_type=code&state=test123&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256")
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$OAUTH_SERVER_PORT/authorize?client_id=test-client&redirect_uri=http://127.0.0.1/callback&response_type=code&state=test123&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256")

# Verify HTTP status is 422
if [ "$HTTP_CODE" != "422" ]; then
    echo -e "${RED}FAIL: Expected HTTP 422, got $HTTP_CODE${NC}"
    exit 1
fi
echo "HTTP Status: $HTTP_CODE (expected: 422) ✓"

# Verify response contains Pydantic error format
if ! echo "$RESPONSE" | grep -q '"detail"'; then
    echo -e "${RED}FAIL: Response missing 'detail' field${NC}"
    echo "Response: $RESPONSE"
    exit 1
fi
echo "Response has 'detail' field ✓"

if ! echo "$RESPONSE" | grep -q '"type":"missing"'; then
    echo -e "${RED}FAIL: Response missing type=missing${NC}"
    echo "Response: $RESPONSE"
    exit 1
fi
echo "Response has type=missing ✓"

if ! echo "$RESPONSE" | grep -q '"loc":\["query","resource"\]'; then
    echo -e "${RED}FAIL: Response missing correct loc field${NC}"
    echo "Response: $RESPONSE"
    exit 1
fi
echo "Response has loc=[query,resource] ✓"

if ! echo "$RESPONSE" | grep -q '"msg":"Field required"'; then
    echo -e "${RED}FAIL: Response missing correct msg field${NC}"
    echo "Response: $RESPONSE"
    exit 1
fi
echo "Response has msg=Field required ✓"

echo -e "${GREEN}PASS: Pydantic 422 error format is correct${NC}"

# Test 2: Verify successful request when resource is provided
echo -e "\n${YELLOW}Test 2: Verify success when resource is provided${NC}"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$OAUTH_SERVER_PORT/authorize?client_id=test-client&redirect_uri=http://127.0.0.1/callback&response_type=code&state=test123&resource=http://example.com/mcp&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256")

if [ "$HTTP_CODE" != "200" ]; then
    echo -e "${RED}FAIL: Expected HTTP 200, got $HTTP_CODE${NC}"
    exit 1
fi
echo -e "${GREEN}PASS: Request with resource parameter returns 200${NC}"

# Test 3: Verify discovery endpoint works
echo -e "\n${YELLOW}Test 3: Verify OAuth discovery endpoint${NC}"
DISCOVERY=$(curl -s "http://localhost:$OAUTH_SERVER_PORT/.well-known/oauth-authorization-server")

if ! echo "$DISCOVERY" | grep -q '"authorization_endpoint"'; then
    echo -e "${RED}FAIL: Discovery missing authorization_endpoint${NC}"
    exit 1
fi
echo -e "${GREEN}PASS: Discovery endpoint working${NC}"

echo -e "\n${GREEN}=== All Runlayer mode tests passed! ===${NC}"
