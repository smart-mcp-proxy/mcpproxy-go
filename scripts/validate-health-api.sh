#!/usr/bin/env bash
# Validation script for Spec 013 health field propagation
# Tests each stage of the data flow to identify where health is lost

set -e

CONFIG_FILE="$HOME/.mcpproxy/mcp_config.json"
API_BASE="http://127.0.0.1:8080"
# Known API key from config - set MCPPROXY_API_KEY env var to override
API_KEY="${MCPPROXY_API_KEY:-8d58be58479630e8c15adec02bfc8729e5c9a684c7c4e1afd4a598e1d538fd5a}"

echo "=== Spec 013 Health Field Validation ==="
echo ""
echo "Using API key: ${API_KEY:0:10}..."

# Step 2: Check if server is running
if ! curl -s -o /dev/null -w "%{http_code}" "$API_BASE/api/v1/status" -H "X-API-Key: $API_KEY" | grep -q "200"; then
    echo "ERROR: Server not responding at $API_BASE"
    exit 1
fi
echo "✓ Server is running"

# Step 3: Get servers and check for health field
echo ""
echo "=== Checking /api/v1/servers response ==="

RESPONSE=$(curl -s -H "X-API-Key: $API_KEY" "$API_BASE/api/v1/servers")

# Debug: Show response structure
echo "Response keys:"
echo "$RESPONSE" | jq 'keys' 2>/dev/null || echo "$RESPONSE" | head -c 500

# The API might return {servers: [...]} or {data: {servers: [...]}} or just [...]
# Try different formats
if echo "$RESPONSE" | jq -e '.servers' > /dev/null 2>&1; then
    SERVERS_PATH=".servers"
elif echo "$RESPONSE" | jq -e '.data.servers' > /dev/null 2>&1; then
    SERVERS_PATH=".data.servers"
elif echo "$RESPONSE" | jq -e '.[0]' > /dev/null 2>&1; then
    SERVERS_PATH="."
else
    echo "ERROR: Unknown response format"
    echo "$RESPONSE" | head -c 1000
    exit 1
fi
echo "Using servers path: $SERVERS_PATH"

# Check if any server has health field
SERVERS_WITH_HEALTH=$(echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.health != null)] | length")
TOTAL_SERVERS=$(echo "$RESPONSE" | jq "$SERVERS_PATH | length")

echo "Total servers: $TOTAL_SERVERS"
echo "Servers with health field: $SERVERS_WITH_HEALTH"

if [ "$SERVERS_WITH_HEALTH" = "0" ]; then
    echo ""
    echo "❌ PROBLEM FOUND: HTTP API is NOT returning health field"
    echo ""
    echo "First server in response:"
    echo "$RESPONSE" | jq '.servers[0] | keys'
    echo ""
    echo "This means the fix in internal/server/server.go or internal/httpapi/handlers.go is not working."
    exit 1
else
    echo "✓ HTTP API is returning health field"
fi

# Step 4: Show health status breakdown
echo ""
echo "=== Health Status Breakdown ==="
echo -n "Healthy servers: "
echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.health.level == \"healthy\")] | length"

echo -n "Unhealthy servers: "
echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.health.level == \"unhealthy\")] | length"

echo -n "Degraded servers: "
echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.health.level == \"degraded\")] | length"

echo ""
echo "=== Sample Server Data (first 3) ==="
echo "$RESPONSE" | jq "$SERVERS_PATH[:3] | .[] | {name: .name, connected: .connected, health_level: .health.level, health_summary: .health.summary}"

# Step 5: Compare health.level=healthy vs connected=true
echo ""
echo "=== Consistency Check: health.level vs connected ==="
HEALTHY_COUNT=$(echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.health.level == \"healthy\")] | length")
CONNECTED_COUNT=$(echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.connected == true)] | length")
ENABLED_HEALTHY=$(echo "$RESPONSE" | jq "[$SERVERS_PATH[] | select(.enabled == true and .health.level == \"healthy\")] | length")

echo "health.level=healthy: $HEALTHY_COUNT"
echo "connected=true: $CONNECTED_COUNT"
echo "enabled + healthy: $ENABLED_HEALTHY"

if [ "$HEALTHY_COUNT" != "$CONNECTED_COUNT" ]; then
    echo ""
    echo "⚠️  MISMATCH: health.level and connected field differ"
    echo "   This is expected - health.level is the source of truth (Spec 013)"
    echo ""
    echo "Servers where health.level differs from connected:"
    echo "$RESPONSE" | jq "$SERVERS_PATH[] | select((.health.level == \"healthy\") != .connected) | {name: .name, connected: .connected, health_level: .health.level}"
fi

echo ""
echo "=== Validation Complete ==="
echo ""
echo "If the tray still shows wrong count after this validation passes,"
echo "the issue is in the tray's API client or adapter layer."
echo ""
echo "Next steps:"
echo "1. Check tray logs for 'Connected count calculated' message"
echo "2. Verify healthy count > 0 in logs"
echo "3. If healthy=0, the issue is in client.go GetServers() health extraction"
