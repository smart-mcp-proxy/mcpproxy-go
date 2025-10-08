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
COVERAGE_FILE="coverage.out"
COVERAGE_HTML="coverage.html"

export MCPPROXY_BINARY_PATH="$(pwd)/mcpproxy"
export MCPPROXY_BINARY="$MCPPROXY_BINARY_PATH"

# Test results tracking
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

echo -e "${GREEN}MCPProxy Complete Test Suite${NC}"
echo "================================"
echo ""

# Helper functions
run_test_stage() {
    local stage_name="$1"
    local command="$2"
    local required="$3"  # "required" or "optional"

    echo -e "${BLUE}[STAGE]${NC} $stage_name"
    echo "Command: $command"
    echo ""

    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    if eval "$command"; then
        echo -e "${GREEN}✓ $stage_name PASSED${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        echo ""
        return 0
    else
        echo -e "${RED}✗ $stage_name FAILED${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        echo ""

        if [ "$required" = "required" ]; then
            echo -e "${RED}Required stage failed. Stopping test suite.${NC}"
            exit 1
        else
            echo -e "${YELLOW}Optional stage failed. Continuing...${NC}"
            return 1
        fi
    fi
}

cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"

    # Kill any remaining mcpproxy processes
    pkill -f "mcpproxy.*serve" 2>/dev/null || true

    # Clean up any test data directories
    rm -rf ./test-data 2>/dev/null || true

    # Clean up temporary log files
    rm -f /tmp/mcpproxy_*.log 2>/dev/null || true
}

# Set up cleanup trap
trap cleanup EXIT

# Print environment info
echo -e "${YELLOW}Environment Information${NC}"
echo "======================="
echo -e "Go version: ${BLUE}$(go version)${NC}"
echo -e "Node.js version: ${BLUE}$(node --version 2>/dev/null || echo 'Not installed')${NC}"
echo -e "npm version: ${BLUE}$(npm --version 2>/dev/null || echo 'Not installed')${NC}"
echo -e "jq version: ${BLUE}$(jq --version 2>/dev/null || echo 'Not installed')${NC}"
echo ""

# Check prerequisites
echo -e "${YELLOW}Checking Prerequisites${NC}"
echo "====================="

missing_deps=0

if ! command -v go &> /dev/null; then
    echo -e "${RED}✗ Go is not installed${NC}"
    missing_deps=1
fi

if ! command -v node &> /dev/null; then
    echo -e "${RED}✗ Node.js is not installed (required for everything server)${NC}"
    missing_deps=1
fi

if ! command -v npm &> /dev/null; then
    echo -e "${RED}✗ npm is not installed (required for everything server)${NC}"
    missing_deps=1
fi

if ! command -v jq &> /dev/null; then
    echo -e "${RED}✗ jq is not installed (required for E2E tests)${NC}"
    missing_deps=1
fi

if [ $missing_deps -eq 1 ]; then
    echo ""
    echo -e "${RED}Missing required dependencies. Please install them and try again.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ All prerequisites satisfied${NC}"
echo ""

# Stage 1: Build
run_test_stage "Build mcpproxy binary" \
    "go build -o mcpproxy ./cmd/mcpproxy" \
    "required"

# Stage 2: Unit Tests (exclude E2E tests that run in dedicated stages)
run_test_stage "Unit tests" \
    "go test ./internal/... -v -race -timeout=5m -skip '^Test(E2E_|Binary|MCPProtocol)'" \
    "required"

# Stage 3: Unit Tests with Coverage (exclude E2E tests that run in dedicated stages)
run_test_stage "Unit tests with coverage" \
    "go test -coverprofile=$COVERAGE_FILE -covermode=atomic ./internal/... -timeout=5m -skip '^Test(E2E_|Binary|MCPProtocol)'" \
    "optional"

# Generate coverage report if coverage file exists
if [ -f "$COVERAGE_FILE" ]; then
    echo -e "${YELLOW}Generating coverage report...${NC}"
    go tool cover -html="$COVERAGE_FILE" -o "$COVERAGE_HTML"
    echo -e "${GREEN}Coverage report generated: $COVERAGE_HTML${NC}"

    # Show coverage summary
    echo -e "${YELLOW}Coverage Summary:${NC}"
    go tool cover -func="$COVERAGE_FILE" | tail -1
    echo ""
fi

# Stage 4: Linting
run_test_stage "Code linting" \
    "./scripts/run-linter.sh" \
    "optional"

# Stage 5: Original E2E Tests (internal mocks)
run_test_stage "Original E2E tests (mocked)" \
    "./scripts/run-e2e-tests.sh" \
    "required"

# Stage 6: API E2E Tests (with everything server)
run_test_stage "API E2E tests (with everything server)" \
    "./scripts/test-api-e2e.sh" \
    "required"

# Stage 7: Binary E2E Tests
run_test_stage "Binary E2E tests" \
    "go test ./internal/server -run TestBinary -v -timeout=10m" \
    "required"

# Stage 8: MCP Protocol E2E Tests
run_test_stage "MCP Protocol E2E tests" \
    "go test ./internal/server -run TestMCP -v -timeout=10m" \
    "required"

# Stage 9: Performance/Load Tests (optional)
run_test_stage "Performance tests" \
    "go test ./internal/server -run TestBinaryPerformance -v -timeout=5m" \
    "optional"

# Final cleanup
cleanup

# Results Summary
echo ""
echo -e "${YELLOW}Test Suite Summary${NC}"
echo "=================="
echo -e "Total test stages: ${BLUE}$TOTAL_TESTS${NC}"
echo -e "Passed stages: ${GREEN}$PASSED_TESTS${NC}"
echo -e "Failed stages: ${RED}$FAILED_TESTS${NC}"

if [ $FAILED_TESTS -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 ALL TESTS PASSED! 🎉${NC}"
    echo -e "${GREEN}The code is ready for commit/deployment.${NC}"

    if [ -f "$COVERAGE_FILE" ]; then
        echo ""
        echo -e "${YELLOW}Coverage report available at: $COVERAGE_HTML${NC}"
    fi

    exit 0
else
    echo ""
    echo -e "${RED}⚠️  $FAILED_TESTS stage(s) failed${NC}"

    # Check if any required stages failed
    required_failed=false
    if [ $FAILED_TESTS -gt 0 ]; then
        # Since we exit on required failures, any failures here are optional
        echo -e "${YELLOW}All failures were in optional stages.${NC}"
        echo -e "${YELLOW}Core functionality is working, but some optimizations may be needed.${NC}"
    fi

    exit 1
fi
