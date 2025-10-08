#!/bin/bash
#
# MCP Interface Test Script
#
# Tests the MCP protocol implementation using a real MCP client.
# This ensures tool calls, discovery, and history tracking work correctly.
#
# Usage:
#   ./scripts/test-mcp.sh [simple|full]
#
# Arguments:
#   simple - Run basic connectivity test (default)
#   full   - Run comprehensive test suite
#

set -e

# Configuration
MCPPROXY_PORT="${MCPPROXY_PORT:-18080}"
MCPPROXY_URL="http://127.0.0.1:${MCPPROXY_PORT}"
MCPPROXY_API_KEY="test-mcp-key-12345"
DATA_DIR="/tmp/mcpproxy-mcp-test"
CONFIG_FILE="$(mktemp /tmp/mcpproxy-test-config.XXXXXX.json)"

# Determine test mode
TEST_MODE="${1:-simple}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

# Cleanup function
cleanup() {
    log_info "Cleaning up..."
    pkill -f "mcpproxy serve" 2>/dev/null || true
    rm -f "$CONFIG_FILE"
    rm -rf "$DATA_DIR"
}

trap cleanup EXIT

# Check dependencies
log_info "Checking dependencies..."
if ! command -v node &> /dev/null; then
    log_error "Node.js is not installed"
    exit 1
fi

if ! command -v npx &> /dev/null; then
    log_error "npx is not available"
    exit 1
fi

# Check if MCP SDK is installed, install if needed
if ! node -e "import('@modelcontextprotocol/sdk/client/index.js')" 2>/dev/null; then
    log_info "Installing MCP SDK dependencies..."
    if [ ! -f "package.json" ]; then
        npm init -y > /dev/null 2>&1
    fi
    npm install @modelcontextprotocol/sdk --save-dev > /dev/null 2>&1
    log_success "MCP SDK installed"
fi

# Create test configuration
log_info "Creating test configuration..."
cat > "$CONFIG_FILE" <<EOF
{
  "listen": "127.0.0.1:${MCPPROXY_PORT}",
  "data_dir": "${DATA_DIR}",
  "enable_tray": false,
  "mcpServers": [
    {
      "name": "everything",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "protocol": "stdio",
      "enabled": true,
      "quarantined": false
    }
  ],
  "logging": {
    "level": "info",
    "enable_file": false,
    "enable_console": true
  },
  "docker_isolation": {
    "enabled": false
  }
}
EOF

# Build mcpproxy if needed
if [ ! -f "./mcpproxy" ]; then
    log_info "Building mcpproxy..."
    CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
    log_success "Build complete"
fi

# Clean up old data
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

# Start mcpproxy in background
log_info "Starting mcpproxy server..."
MCPPROXY_API_KEY="$MCPPROXY_API_KEY" \
    ./mcpproxy serve --config="$CONFIG_FILE" > /tmp/mcpproxy-mcp-test.log 2>&1 &
MCPPROXY_PID=$!

# Wait for server to be ready
log_info "Waiting for server to be ready..."
for i in {1..30}; do
    if curl -s -f "${MCPPROXY_URL}/healthz" > /dev/null 2>&1; then
        log_success "Server is ready"
        break
    fi
    if [ $i -eq 30 ]; then
        log_error "Server failed to start"
        cat /tmp/mcpproxy-mcp-test.log
        exit 1
    fi
    sleep 0.5
done

# Give it a moment for everything server to connect
sleep 3

# Run tests
log_info "Running MCP tests (mode: $TEST_MODE)..."
export MCPPROXY_URL
export MCPPROXY_API_KEY

if [ "$TEST_MODE" = "full" ]; then
    # Run comprehensive test suite
    if [ ! -f "tests/mcp/test-suite.mjs" ]; then
        log_error "Test suite not found: tests/mcp/test-suite.mjs"
        exit 1
    fi
    node tests/mcp/test-suite.mjs
else
    # Run simple connectivity test
    if [ ! -f "test-mcp-simple.mjs" ]; then
        log_error "Test script not found: test-mcp-simple.mjs"
        exit 1
    fi
    node test-mcp-simple.mjs
fi

# Check exit code
if [ $? -eq 0 ]; then
    log_success "All tests passed!"
    exit 0
else
    log_error "Tests failed"
    exit 1
fi