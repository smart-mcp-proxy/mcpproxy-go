# Activity Log Backend - Evaluation Scenario

This document defines evaluation scenarios to verify the Activity Log Backend implementation (Spec 016).

## Prerequisites

1. mcpproxy running with API key configured
2. At least one upstream MCP server configured (e.g., `everything` server)
3. curl or equivalent HTTP client

## Scenario 1: Activity Recording on Tool Call

**Objective**: Verify that tool calls are recorded in the activity log.

### Steps

```bash
# 1. Get current activity count
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=1" | jq '.data.total'

# 2. Make a tool call via MCP
curl -s -X POST -H "Content-Type: application/json" \
  "http://localhost:8080/mcp" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"everything:echo","arguments":{"message":"test"}}}'

# 3. Verify activity was recorded
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=1" | jq '.data.activities[0]'
```

### Expected Results

- Activity count increases by 1
- New activity has:
  - `type`: "tool_call"
  - `server_name`: "everything"
  - `tool_name`: "echo"
  - `status`: "success"

---

## Scenario 2: Activity Filtering

**Objective**: Verify that activity list supports filtering.

### Steps

```bash
# Filter by type
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?type=tool_call" | jq '.data.total'

# Filter by server
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?server=everything" | jq '.data.total'

# Filter by status
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?status=success" | jq '.data.total'

# Combined filters
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?type=tool_call&server=everything&status=success" | jq '.data.total'
```

### Expected Results

- Each filter returns subset of total activities
- Combined filters return intersection of individual filters

---

## Scenario 3: Activity Detail View

**Objective**: Verify that individual activity records can be retrieved.

### Steps

```bash
# 1. Get an activity ID
ACTIVITY_ID=$(curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=1" | jq -r '.data.activities[0].id')

# 2. Fetch activity detail
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity/$ACTIVITY_ID" | jq '.data.activity'

# 3. Verify 404 for non-existent ID
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity/nonexistent123" | jq '.success'
```

### Expected Results

- Detail endpoint returns full activity record with all fields
- Non-existent ID returns `success: false` with 404 status

---

## Scenario 4: Activity Export

**Objective**: Verify that activities can be exported in JSON Lines and CSV formats.

### Steps

```bash
# Export as JSON Lines (default)
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity/export" | head -3

# Export as CSV
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity/export?format=csv" | head -3

# Export with filters
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity/export?type=tool_call&format=csv" | wc -l
```

### Expected Results

- JSON export returns one JSON object per line (JSONL format)
- CSV export includes header row followed by data rows
- Filters reduce exported record count

---

## Scenario 5: SSE Activity Events

**Objective**: Verify that activity events are streamed via SSE.

### Steps

```bash
# 1. Start SSE listener in background
curl -s -N -H "X-API-Key: $API_KEY" "http://localhost:8080/events" > /tmp/sse_events.txt &
SSE_PID=$!

# 2. Wait for connection
sleep 2

# 3. Make a tool call
curl -s -X POST -H "Content-Type: application/json" \
  "http://localhost:8080/mcp" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"everything:echo","arguments":{"message":"sse-test"}}}'

# 4. Wait for event propagation
sleep 1

# 5. Stop SSE listener and check events
kill $SSE_PID 2>/dev/null
grep "activity.tool_call" /tmp/sse_events.txt
```

### Expected Results

- SSE stream receives `activity.tool_call.started` event
- SSE stream receives `activity.tool_call.completed` event
- Events contain server_name, tool_name, and status

---

## Scenario 6: Quarantine Activity Recording

**Objective**: Verify that quarantine changes are recorded.

### Steps

```bash
# 1. Quarantine a server
curl -s -X POST -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/api/v1/servers/everything/quarantine" \
  -d '{"quarantined": true}'

# 2. Check for quarantine_change activity
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?type=quarantine_change&limit=1" | jq '.data.activities[0]'

# 3. Unquarantine the server
curl -s -X POST -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/api/v1/servers/everything/quarantine" \
  -d '{"quarantined": false}'
```

### Expected Results

- Quarantine change creates activity with `type: quarantine_change`
- Activity metadata includes `quarantined: true/false` and `reason`

---

## Scenario 7: Policy Decision Recording

**Objective**: Verify that blocked tool calls are recorded as policy decisions.

### Steps

```bash
# 1. Quarantine a server
curl -s -X POST -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/api/v1/servers/everything/quarantine" \
  -d '{"quarantined": true}'

# 2. Attempt tool call to quarantined server
curl -s -X POST -H "Content-Type: application/json" \
  "http://localhost:8080/mcp" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"everything:echo","arguments":{"message":"blocked"}}}'

# 3. Check for policy_decision activity
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?type=policy_decision&limit=1" | jq '.data.activities[0]'

# 4. Cleanup - unquarantine
curl -s -X POST -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/api/v1/servers/everything/quarantine" \
  -d '{"quarantined": false}'
```

### Expected Results

- Blocked tool call creates activity with `type: policy_decision`
- Activity has `status: blocked` and includes reason in metadata

---

## Scenario 8: Pagination

**Objective**: Verify pagination works correctly.

### Steps

```bash
# Get total count
TOTAL=$(curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=1" | jq '.data.total')

# Get first page
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=5&offset=0" | jq '.data.activities | length'

# Get second page
curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=5&offset=5" | jq '.data.activities | length'

# Verify no overlap (first item IDs should differ)
PAGE1_FIRST=$(curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=5&offset=0" | jq -r '.data.activities[0].id')
PAGE2_FIRST=$(curl -s -H "X-API-Key: $API_KEY" "http://localhost:8080/api/v1/activity?limit=5&offset=5" | jq -r '.data.activities[0].id')
[ "$PAGE1_FIRST" != "$PAGE2_FIRST" ] && echo "PASS: Pages have different items"
```

### Expected Results

- Total reflects all matching records
- Limit controls page size
- Offset skips records
- Pages contain different records

---

## Automated Test Script

```bash
#!/bin/bash
# eval-activity-log.sh - Run all evaluation scenarios

API_KEY="${MCPPROXY_API_KEY:-your-api-key}"
BASE_URL="${MCPPROXY_URL:-http://localhost:8080}"

pass=0
fail=0

run_test() {
    local name="$1"
    local cmd="$2"
    local expected="$3"

    result=$(eval "$cmd" 2>/dev/null)
    if [[ "$result" == *"$expected"* ]]; then
        echo "PASS: $name"
        ((pass++))
    else
        echo "FAIL: $name (expected: $expected, got: $result)"
        ((fail++))
    fi
}

echo "=== Activity Log Evaluation ==="

# Test 1: List endpoint returns valid response
run_test "List activities" \
    "curl -s -H 'X-API-Key: $API_KEY' '$BASE_URL/api/v1/activity' | jq -r '.success'" \
    "true"

# Test 2: Detail endpoint handles 404
run_test "Activity 404" \
    "curl -s -H 'X-API-Key: $API_KEY' '$BASE_URL/api/v1/activity/nonexistent' | jq -r '.success'" \
    "false"

# Test 3: Export JSON format
run_test "Export JSON" \
    "curl -s -H 'X-API-Key: $API_KEY' '$BASE_URL/api/v1/activity/export?format=json' -o /dev/null -w '%{content_type}'" \
    "application/x-ndjson"

# Test 4: Export CSV format
run_test "Export CSV" \
    "curl -s -H 'X-API-Key: $API_KEY' '$BASE_URL/api/v1/activity/export?format=csv' -o /dev/null -w '%{content_type}'" \
    "text/csv"

# Test 5: Filtering works
run_test "Filter by type" \
    "curl -s -H 'X-API-Key: $API_KEY' '$BASE_URL/api/v1/activity?type=tool_call' | jq -r '.success'" \
    "true"

echo ""
echo "=== Results: $pass passed, $fail failed ==="
```

---

## References

- [MCPEval: Automatic MCP-based Deep Evaluation](https://arxiv.org/html/2507.12806v1)
- [mcpx-eval: Open-ended tool use evaluation](https://docs.mcp.run/blog/2025/03/03/introducing-mcpx-eval/)
- [MCP Evals on Hugging Face](https://huggingface.co/blog/mclenhard/mcp-evals)
