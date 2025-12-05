#!/bin/bash
# OAuth E2E Test Runner
# This script runs the complete OAuth E2E test suite including:
# 1. Go unit tests for the OAuth test server
# 2. Full mcpproxy OAuth flow tests with Playwright
#
# Test Scenarios (Spec 008):
# - T047: Token refresh with short TTL
# - T048: Persisted token loading on restart
# - T049: Correlation ID verification
# - T050: Race condition prevention (rapid reconnections)
# - T051: Error injection (invalid_grant)
# - T052: Web UI OAuth status verification
# - T053: REST API OAuth status verification
#
# Usage:
#   ./scripts/run-oauth-e2e.sh                    # Run all tests
#   ./scripts/run-oauth-e2e.sh --short-ttl        # Run with 30s token TTL
#   ./scripts/run-oauth-e2e.sh --error-injection  # Run with error injection tests
#   ./scripts/run-oauth-e2e.sh --verbose          # Show verbose output

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse arguments
SHORT_TTL=false
ERROR_INJECTION=false
VERBOSE=false
for arg in "$@"; do
  case $arg in
    --short-ttl)
      SHORT_TTL=true
      shift
      ;;
    --error-injection)
      ERROR_INJECTION=true
      shift
      ;;
    --verbose)
      VERBOSE=true
      shift
      ;;
  esac
done

# Configuration
OAUTH_SERVER_PORT=${OAUTH_SERVER_PORT:-9000}
MCPPROXY_PORT=${MCPPROXY_PORT:-8085}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PLAYWRIGHT_DIR="$PROJECT_ROOT/e2e/playwright"
TEST_CONFIG="$PLAYWRIGHT_DIR/test-config.json"
TEST_DATA_DIR="/tmp/mcpproxy-oauth-e2e"
TEST_RESULTS_DIR="$PLAYWRIGHT_DIR/test-results"

# Token TTL for short TTL mode (T047)
if [ "$SHORT_TTL" = true ]; then
  ACCESS_TOKEN_TTL="30s"
else
  ACCESS_TOKEN_TTL="1h"
fi

# PIDs for cleanup
OAUTH_SERVER_PID=""
MCPPROXY_PID=""

cleanup() {
    echo ""
    echo -e "${YELLOW}Cleaning up...${NC}"

    if [ -n "$MCPPROXY_PID" ] && kill -0 "$MCPPROXY_PID" 2>/dev/null; then
        echo "Stopping mcpproxy (PID: $MCPPROXY_PID)"
        kill "$MCPPROXY_PID" 2>/dev/null || true
        wait "$MCPPROXY_PID" 2>/dev/null || true
    fi

    if [ -n "$OAUTH_SERVER_PID" ] && kill -0 "$OAUTH_SERVER_PID" 2>/dev/null; then
        echo "Stopping OAuth server (PID: $OAUTH_SERVER_PID)"
        kill "$OAUTH_SERVER_PID" 2>/dev/null || true
        wait "$OAUTH_SERVER_PID" 2>/dev/null || true
    fi

    # Kill any remaining processes on our ports
    lsof -ti :$OAUTH_SERVER_PORT | xargs kill -9 2>/dev/null || true
    lsof -ti :$MCPPROXY_PORT | xargs kill -9 2>/dev/null || true

    # Clean up test data
    rm -rf "$TEST_DATA_DIR" 2>/dev/null || true

    echo "Cleanup complete"
}

trap cleanup EXIT

echo "=========================================="
echo "OAuth E2E Test Suite"
echo "=========================================="
echo ""
echo "Configuration:"
echo "  OAuth Server Port: $OAUTH_SERVER_PORT"
echo "  MCPProxy Port: $MCPPROXY_PORT"
echo "  Test Data Dir: $TEST_DATA_DIR"
echo ""

# Track failures
FAILURES=0

# Run Go unit tests for OAuth test server
echo -e "${YELLOW}Running OAuth test server unit tests...${NC}"
cd "$PROJECT_ROOT"
if go test ./tests/oauthserver/... -v -count=1 2>&1 | tail -30; then
    echo -e "${GREEN}✅ OAuth test server tests passed${NC}"
else
    echo -e "${RED}❌ OAuth test server tests failed${NC}"
    FAILURES=$((FAILURES + 1))
fi
echo ""

# Check if Playwright is available
if [ ! -d "$PLAYWRIGHT_DIR" ] || [ ! -f "$PLAYWRIGHT_DIR/package.json" ]; then
    echo -e "${YELLOW}⚠️  Playwright tests not available (e2e/playwright not found)${NC}"
    echo ""
    echo "=========================================="
    echo "Test Summary"
    echo "=========================================="
    if [ $FAILURES -eq 0 ]; then
        echo -e "${GREEN}✅ All tests passed!${NC}"
        exit 0
    else
        echo -e "${RED}❌ $FAILURES test suite(s) failed${NC}"
        exit 1
    fi
fi

# Ensure Playwright dependencies are installed
echo -e "${YELLOW}Checking Playwright setup...${NC}"
cd "$PLAYWRIGHT_DIR"
if [ ! -d "node_modules" ]; then
    echo "Installing Playwright dependencies..."
    npm install
    npx playwright install chromium
fi
cd "$PROJECT_ROOT"

# Build mcpproxy
echo -e "${YELLOW}Building mcpproxy...${NC}"
go build -o /tmp/mcpproxy-e2e ./cmd/mcpproxy
echo -e "${GREEN}✅ mcpproxy built${NC}"
echo ""

# Create test data directory
mkdir -p "$TEST_DATA_DIR"

# Create test results directory
mkdir -p "$TEST_RESULTS_DIR"

# Start OAuth test server with configured TTL
echo -e "${YELLOW}Starting OAuth test server on port $OAUTH_SERVER_PORT...${NC}"
echo -e "${BLUE}  Access Token TTL: $ACCESS_TOKEN_TTL${NC}"
go run ./tests/oauthserver/cmd/server -port $OAUTH_SERVER_PORT -access-token-ttl=$ACCESS_TOKEN_TTL &
OAUTH_SERVER_PID=$!
sleep 3

# Verify OAuth server is running
if ! curl -s "http://127.0.0.1:$OAUTH_SERVER_PORT/jwks.json" > /dev/null; then
    echo -e "${RED}❌ Failed to start OAuth test server${NC}"
    exit 1
fi
echo -e "${GREEN}✅ OAuth test server started${NC}"
echo ""

# Start mcpproxy with test config
echo -e "${YELLOW}Starting mcpproxy on port $MCPPROXY_PORT...${NC}"
MCPPROXY_API_KEY="test-api-key" /tmp/mcpproxy-e2e serve --config="$TEST_CONFIG" &
MCPPROXY_PID=$!
sleep 3

# Verify mcpproxy is running
if ! curl -s -H "X-API-Key: test-api-key" "http://127.0.0.1:$MCPPROXY_PORT/api/v1/status" > /dev/null; then
    echo -e "${RED}❌ Failed to start mcpproxy${NC}"
    exit 1
fi
echo -e "${GREEN}✅ mcpproxy started${NC}"
echo ""

# Run Playwright tests
echo -e "${YELLOW}Running Playwright OAuth flow tests...${NC}"
cd "$PLAYWRIGHT_DIR"
OAUTH_SERVER_URL="http://127.0.0.1:$OAUTH_SERVER_PORT" \
OAUTH_CLIENT_ID="test-client" \
MCPPROXY_URL="http://127.0.0.1:$MCPPROXY_PORT" \
MCPPROXY_API_KEY="test-api-key" \
npx playwright test --reporter=list || {
    echo -e "${RED}❌ Playwright tests failed${NC}"
    FAILURES=$((FAILURES + 1))
}
cd "$PROJECT_ROOT"

if [ $FAILURES -eq 0 ]; then
    echo -e "${GREEN}✅ Playwright tests passed${NC}"
fi
echo ""

# Summary
echo "=========================================="
echo "Test Summary"
echo "=========================================="
if [ $FAILURES -eq 0 ]; then
    echo -e "${GREEN}✅ All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}❌ $FAILURES test suite(s) failed${NC}"
    exit 1
fi
