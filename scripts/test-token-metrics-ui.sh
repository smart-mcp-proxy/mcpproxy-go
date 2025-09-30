#!/bin/bash
set -e

echo "🧪 Testing Token Metrics UI with Playwright"
echo "==========================================="

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Check if mcpproxy binary exists
if [ ! -f "./mcpproxy" ]; then
    echo -e "${YELLOW}Building mcpproxy...${NC}"
    CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
fi

# Set API key for tests
export MCPPROXY_API_KEY="test-api-key-12345"

# Kill any existing mcpproxy processes
echo -e "${YELLOW}Cleaning up existing mcpproxy processes...${NC}"
pkill -f "mcpproxy serve" || true
sleep 1

# Start mcpproxy with test config
echo -e "${YELLOW}Starting mcpproxy server...${NC}"
./mcpproxy serve --listen=127.0.0.1:8080 --log-level=info &
MCPPROXY_PID=$!

# Function to cleanup on exit
cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"
    kill $MCPPROXY_PID 2>/dev/null || true
    pkill -f "mcpproxy serve" || true
}
trap cleanup EXIT

# Wait for server to start
echo -e "${YELLOW}Waiting for server to start...${NC}"
for i in {1..30}; do
    if curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/api/v1/servers > /dev/null 2>&1; then
        echo -e "${GREEN}✓ Server is ready${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}✗ Server failed to start${NC}"
        exit 1
    fi
    sleep 1
done

# Give server a moment to fully initialize
sleep 2

echo -e "${YELLOW}Checking if Playwright browsers are installed...${NC}"
npx playwright install chromium --with-deps 2>&1 | grep -v "Downloading" || true

# Run Playwright tests
echo -e "${GREEN}Running Playwright tests...${NC}"
echo "==========================================="

# Run all token metrics tests
echo -e "${YELLOW}Testing token metrics features...${NC}"
npx playwright test tests/token-metrics.spec.ts --reporter=list

echo -e "\n${YELLOW}Testing tool calls fixes...${NC}"
npx playwright test tests/tool-calls-fixes.spec.ts --reporter=list

# Check test results
if [ $? -eq 0 ]; then
    echo -e "\n${GREEN}✓ All tests passed!${NC}"
    echo -e "${YELLOW}Note: Token metrics will only appear for NEW tool calls made after the update${NC}"
    echo -e "${YELLOW}Old tool calls will show '—' in the Tokens column (this is expected)${NC}"
    exit 0
else
    echo -e "\n${RED}✗ Some tests failed${NC}"
    echo -e "${YELLOW}View detailed report with: npx playwright show-report${NC}"
    exit 1
fi