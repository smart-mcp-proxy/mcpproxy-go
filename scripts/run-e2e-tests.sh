#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
TEST_TIMEOUT=${TEST_TIMEOUT:-"5m"}
VERBOSE=${VERBOSE:-"true"}
RACE=${RACE:-"true"}
COVER=${COVER:-"false"}

echo -e "${GREEN}Running MCP Proxy E2E Tests${NC}"
echo "=================================="

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed${NC}"
    exit 1
fi

echo -e "${YELLOW}Go version:${NC} $(go version)"
echo -e "${YELLOW}Test timeout:${NC} $TEST_TIMEOUT"
echo -e "${YELLOW}Race detection:${NC} $RACE"
echo -e "${YELLOW}Coverage:${NC} $COVER"
echo ""

# Build the binary first to ensure everything compiles
echo -e "${YELLOW}Building mcpproxy binary...${NC}"
go build -o mcpproxy ./cmd/mcpproxy
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Build successful${NC}"
    export MCPPROXY_BINARY_PATH="$(pwd)/mcpproxy"
    export MCPPROXY_BINARY="$MCPPROXY_BINARY_PATH"
else
    echo -e "${RED}✗ Build failed${NC}"
    exit 1
fi

# Prepare test arguments
TEST_ARGS="-v -timeout $TEST_TIMEOUT"
if [ "$RACE" = "true" ]; then
    TEST_ARGS="$TEST_ARGS -race"
fi
if [ "$COVER" = "true" ]; then
    TEST_ARGS="$TEST_ARGS -coverprofile=coverage.out -covermode=atomic"
fi

# Run unit tests first
echo -e "${YELLOW}Running unit tests...${NC}"
go test $TEST_ARGS ./internal/... -run "^Test[^E2E]"
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Unit tests passed${NC}"
else
    echo -e "${RED}✗ Unit tests failed${NC}"
    exit 1
fi

echo ""

# Run E2E tests
echo -e "${YELLOW}Running E2E tests...${NC}"
go test $TEST_ARGS ./internal/server -run TestE2E
E2E_EXIT_CODE=$?

if [ $E2E_EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}✓ E2E tests passed${NC}"
else
    echo -e "${RED}✗ E2E tests failed${NC}"
fi

# Generate coverage report if enabled
if [ "$COVER" = "true" ] && [ -f coverage.out ]; then
    echo -e "${YELLOW}Generating coverage report...${NC}"
    go tool cover -html=coverage.out -o coverage.html
    echo -e "${GREEN}Coverage report generated: coverage.html${NC}"
    
    # Show coverage summary
    go tool cover -func=coverage.out | tail -1
fi

# Cleanup
echo -e "${YELLOW}Cleaning up...${NC}"
rm -f mcpproxy
rm -f coverage.out

if [ $E2E_EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}All tests completed successfully!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed${NC}"
    exit 1
fi 
