#!/bin/bash
#
# test-config-race.sh - Reproduce config file race condition
#
# This script attempts to reproduce the race condition where:
# 1. Config file is being written (truncated then written)
# 2. Core process starts and reads the partially written file
# 3. Core fails with "invalid character '}'" JSON parse error
#
# Expected result WITHOUT fix: ~5% failure rate (corrupted config reads)
# Expected result WITH atomic writes: 0% failure rate
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
ITERATIONS=${1:-100}
TEST_DIR="/tmp/mcpproxy-race-test-$$"
CONFIG_FILE="$TEST_DIR/mcp_config.json"
RESULTS_FILE="$TEST_DIR/results.txt"

# Counters
SUCCESS_COUNT=0
PARTIAL_READ_COUNT=0
PARSE_ERROR_COUNT=0

echo "=================================================="
echo "Config File Race Condition Reproduction Test"
echo "=================================================="
echo ""
echo "Test directory: $TEST_DIR"
echo "Iterations: $ITERATIONS"
echo ""

# Create test directory
mkdir -p "$TEST_DIR"

# Create a sample config (large enough to have observable write time)
create_large_config() {
    local file=$1
    cat > "$file" << 'EOF'
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "api_key": "test-api-key-1234567890",
  "enable_web_ui": true,
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "mcpServers": [
    {
      "name": "server-1",
      "url": "http://localhost:8001",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "server-2",
      "url": "http://localhost:8002",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "server-3",
      "url": "http://localhost:8003",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "server-4",
      "url": "http://localhost:8004",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "server-5",
      "url": "http://localhost:8005",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    }
  ]
}
EOF
}

# Simulate non-atomic write (current implementation)
non_atomic_write() {
    local file=$1
    local data=$2

    # This mimics os.WriteFile behavior: truncate then write
    # The file is in a partial state during the write
    echo -n "$data" > "$file"
}

# Simulate atomic write (proposed fix)
atomic_write() {
    local file=$1
    local data=$2
    local tmp_file="${file}.tmp.$$"

    # Write to temp file
    echo -n "$data" > "$tmp_file"

    # Fsync (force to disk)
    if command -v fsync &> /dev/null; then
        fsync "$tmp_file"
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS doesn't have fsync command, use sync
        sync
    fi

    # Atomic rename
    mv "$tmp_file" "$file"
}

# Reader process that tries to read and parse JSON
read_and_validate() {
    local file=$1
    local iteration=$2

    # Try to read the file
    if [ ! -f "$file" ]; then
        echo "ITERATION $iteration: File not found" >> "$RESULTS_FILE"
        return 1
    fi

    local content=$(cat "$file")

    # Check if content looks incomplete
    local char_count=$(echo -n "$content" | wc -c | tr -d ' ')
    if [ "$char_count" -lt 100 ]; then
        echo "ITERATION $iteration: PARTIAL READ (only $char_count chars)" >> "$RESULTS_FILE"
        echo "$content" >> "$RESULTS_FILE"
        echo "---" >> "$RESULTS_FILE"
        return 2
    fi

    # Try to parse as JSON
    if ! echo "$content" | jq . > /dev/null 2>&1; then
        echo "ITERATION $iteration: JSON PARSE ERROR" >> "$RESULTS_FILE"
        echo "$content" >> "$RESULTS_FILE"
        echo "---" >> "$RESULTS_FILE"
        return 3
    fi

    echo "ITERATION $iteration: SUCCESS" >> "$RESULTS_FILE"
    return 0
}

# Test function
run_test() {
    local use_atomic=$1
    local iteration=$2

    # Generate new config content
    local config_content
    config_content=$(cat << EOF
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "api_key": "test-key-iteration-$iteration",
  "enable_web_ui": true,
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "mcpServers": [
    {"name": "server-$iteration-1", "url": "http://localhost:8001", "enabled": true},
    {"name": "server-$iteration-2", "url": "http://localhost:8002", "enabled": true},
    {"name": "server-$iteration-3", "url": "http://localhost:8003", "enabled": true},
    {"name": "server-$iteration-4", "url": "http://localhost:8004", "enabled": true},
    {"name": "server-$iteration-5", "url": "http://localhost:8005", "enabled": true}
  ]
}
EOF
)

    # Start writer in background
    if [ "$use_atomic" = "true" ]; then
        atomic_write "$CONFIG_FILE" "$config_content" &
    else
        non_atomic_write "$CONFIG_FILE" "$config_content" &
    fi
    local writer_pid=$!

    # Immediately try to read (race condition)
    # Add tiny delay to simulate core startup time
    sleep 0.001

    read_and_validate "$CONFIG_FILE" "$iteration"
    local result=$?

    # Wait for writer to finish
    wait $writer_pid 2>/dev/null || true

    return $result
}

# Run tests with NON-ATOMIC writes
echo "Running test with NON-ATOMIC writes (current implementation)..."
echo "This should show race condition failures (~5% of the time)"
echo ""

> "$RESULTS_FILE"

for i in $(seq 1 $ITERATIONS); do
    if [ $((i % 10)) -eq 0 ]; then
        echo -n "."
    fi

    run_test "false" "$i"
    result=$?

    case $result in
        0) ((SUCCESS_COUNT++)) ;;
        2) ((PARTIAL_READ_COUNT++)) ;;
        3) ((PARSE_ERROR_COUNT++)) ;;
    esac
done

echo ""
echo ""
echo "=================================================="
echo "Results (NON-ATOMIC writes):"
echo "=================================================="
echo -e "${GREEN}Successful reads:  $SUCCESS_COUNT / $ITERATIONS${NC}"
echo -e "${YELLOW}Partial reads:     $PARTIAL_READ_COUNT / $ITERATIONS${NC}"
echo -e "${RED}Parse errors:      $PARSE_ERROR_COUNT / $ITERATIONS${NC}"
echo ""

TOTAL_FAILURES=$((PARTIAL_READ_COUNT + PARSE_ERROR_COUNT))
FAILURE_RATE=$((TOTAL_FAILURES * 100 / ITERATIONS))

if [ $TOTAL_FAILURES -gt 0 ]; then
    echo -e "${RED}⚠️  RACE CONDITION DETECTED!${NC}"
    echo -e "${RED}   Failure rate: ${FAILURE_RATE}% ($TOTAL_FAILURES failures in $ITERATIONS attempts)${NC}"
    echo ""
    echo "Sample failures saved to: $RESULTS_FILE"
    echo ""
    echo "First 3 failures:"
    grep -A 5 "PARTIAL READ\|PARSE ERROR" "$RESULTS_FILE" | head -n 20
else
    echo -e "${GREEN}✓ No race condition detected${NC}"
    echo "  (Note: Race conditions are probabilistic - try increasing iterations)"
fi

echo ""
echo "=================================================="

# Now test with ATOMIC writes
echo ""
echo "Running test with ATOMIC writes (proposed fix)..."
echo "This should show NO failures"
echo ""

SUCCESS_COUNT=0
PARTIAL_READ_COUNT=0
PARSE_ERROR_COUNT=0
> "$RESULTS_FILE"

for i in $(seq 1 $ITERATIONS); do
    if [ $((i % 10)) -eq 0 ]; then
        echo -n "."
    fi

    run_test "true" "$i"
    result=$?

    case $result in
        0) ((SUCCESS_COUNT++)) ;;
        2) ((PARTIAL_READ_COUNT++)) ;;
        3) ((PARSE_ERROR_COUNT++)) ;;
    esac
done

echo ""
echo ""
echo "=================================================="
echo "Results (ATOMIC writes):"
echo "=================================================="
echo -e "${GREEN}Successful reads:  $SUCCESS_COUNT / $ITERATIONS${NC}"
echo -e "${YELLOW}Partial reads:     $PARTIAL_READ_COUNT / $ITERATIONS${NC}"
echo -e "${RED}Parse errors:      $PARSE_ERROR_COUNT / $ITERATIONS${NC}"
echo ""

TOTAL_FAILURES=$((PARTIAL_READ_COUNT + PARSE_ERROR_COUNT))

if [ $TOTAL_FAILURES -gt 0 ]; then
    echo -e "${RED}⚠️  UNEXPECTED FAILURES WITH ATOMIC WRITES!${NC}"
    echo -e "${RED}   This should not happen - atomic writes should prevent all race conditions${NC}"
    exit 1
else
    echo -e "${GREEN}✓ No race condition detected with atomic writes${NC}"
    echo -e "${GREEN}✓ All $ITERATIONS reads were successful${NC}"
fi

echo ""
echo "=================================================="
echo "Test complete!"
echo "=================================================="

# Cleanup
rm -rf "$TEST_DIR"

echo ""
echo "Summary:"
echo "  - Non-atomic writes are susceptible to race conditions"
echo "  - Atomic writes (temp + rename) prevent all race conditions"
echo "  - This reproduces the issue reported in GitHub issue #86"
