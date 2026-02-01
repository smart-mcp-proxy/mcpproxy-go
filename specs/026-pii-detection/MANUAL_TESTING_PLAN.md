# Sensitive Data Detection - Manual Testing Plan

## Prerequisites

1. **Build mcpproxy**:
   ```bash
   make build
   ```

2. **Start the server**:
   ```bash
   ./mcpproxy serve --config test/e2e-config.json
   ```

3. **Note the API key** from `test/e2e-config.json`:
   ```
   API_KEY=15152abefac37127746d2bb27a4157da95d13ff4a6036abb1f40be3a343dddaa
   ```

4. **Base URL**:
   ```
   BASE_URL=http://127.0.0.1:8081
   ```

---

## Part 1: curl Testing (REST API)

### 1.1 Add a test server with echo capability

```bash
# Add the "everything" server (already in config, but ensure it's active)
curl -X POST "$BASE_URL/api/v1/servers" \
  -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "everything",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-everything"],
    "protocol": "stdio",
    "enabled": true
  }'
```

### 1.2 Call tool with AWS access key (known example)

```bash
# Call the echo tool with an AWS access key
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"message\": \"My AWS key is AKIAIOSFODNN7EXAMPLE\"}"
      }
    }
  }'
```

### 1.3 Call tool with credit card number

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"message\": \"Card number: 4111111111111111\"}"
      }
    }
  }'
```

### 1.4 Call tool with GitHub token

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"message\": \"Token: ghp_1234567890abcdefghijABCDEFGHIJ123456\"}"
      }
    }
  }'
```

### 1.5 Call tool with private key

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"message\": \"-----BEGIN RSA PRIVATE KEY-----\\nMIIEpAIBAAKCAQEA...\\n-----END RSA PRIVATE KEY-----\"}"
      }
    }
  }'
```

### 1.6 Call tool with database connection string

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 5,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"message\": \"postgres://admin:secretpassword@db.example.com:5432/mydb\"}"
      }
    }
  }'
```

### 1.7 Call tool with sensitive file path

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 6,
    "method": "tools/call",
    "params": {
      "name": "call_tool_write",
      "arguments": {
        "name": "everything:echo",
        "args_json": "{\"path\": \"~/.ssh/id_rsa\"}"
      }
    }
  }'
```

### 1.8 Query activity log - all with sensitive data

```bash
# Wait 2 seconds for async detection, then query
sleep 2
curl "$BASE_URL/api/v1/activity?sensitive_data=true" \
  -H "X-API-Key: $API_KEY" | jq .
```

### 1.9 Query activity log - filter by detection type

```bash
curl "$BASE_URL/api/v1/activity?detection_type=aws_access_key" \
  -H "X-API-Key: $API_KEY" | jq .
```

### 1.10 Query activity log - filter by severity

```bash
curl "$BASE_URL/api/v1/activity?severity=critical" \
  -H "X-API-Key: $API_KEY" | jq .
```

### 1.11 Query activity log - combined filters

```bash
curl "$BASE_URL/api/v1/activity?sensitive_data=true&severity=critical" \
  -H "X-API-Key: $API_KEY" | jq .
```

### 1.12 Get activity detail

```bash
# Get the first activity ID from the list
ACTIVITY_ID=$(curl -s "$BASE_URL/api/v1/activity?sensitive_data=true&limit=1" \
  -H "X-API-Key: $API_KEY" | jq -r '.activities[0].id')

# Get detail
curl "$BASE_URL/api/v1/activity/$ACTIVITY_ID" \
  -H "X-API-Key: $API_KEY" | jq .
```

### Expected Response Fields

Check that each activity with sensitive data contains:
```json
{
  "has_sensitive_data": true,
  "detection_types": ["aws_access_key"],
  "max_severity": "critical",
  "metadata": {
    "sensitive_data_detection": {
      "detected": true,
      "detections": [
        {
          "type": "aws_access_key",
          "category": "cloud_credentials",
          "severity": "critical",
          "location": "arguments",
          "is_likely_example": true
        }
      ],
      "scan_duration_ms": 1
    }
  }
}
```

---

## Part 2: CLI Testing

### 2.1 List activities with sensitive data

```bash
./mcpproxy activity list --sensitive-data
```

**Expected**: Table shows SENSITIVE column with indicators like `☢️ 1` for critical detections.

### 2.2 Filter by detection type

```bash
./mcpproxy activity list --detection-type aws_access_key
```

**Expected**: Only activities with AWS access key detections shown.

### 2.3 Filter by severity

```bash
./mcpproxy activity list --severity critical
```

**Expected**: Only activities with critical severity shown.

### 2.4 Combined filters

```bash
./mcpproxy activity list --sensitive-data --severity high
```

### 2.5 Show activity detail

```bash
# Get an activity ID first
./mcpproxy activity list --sensitive-data -o json | jq -r '.[0].id'

# Show details
./mcpproxy activity show <activity-id>
```

**Expected**: Shows "Sensitive Data Detection" section with:
- Detection count
- Severity levels with icons
- Detection types list
- Location (arguments/response)

### 2.6 JSON output

```bash
./mcpproxy activity list --sensitive-data -o json | jq .
```

**Expected**: JSON includes `has_sensitive_data`, `detection_types`, `max_severity` fields.

### 2.7 Export with sensitive data filter

```bash
./mcpproxy activity export --sensitive-data --output audit.jsonl
cat audit.jsonl | head -5
```

---

## Part 3: Chrome Extension / Web UI Testing

### 3.1 Open Web UI

```bash
open "http://127.0.0.1:8081/ui/?apikey=$API_KEY"
```

### 3.2 Navigate to Activity page

1. Click "Activity" in the sidebar
2. Verify the page loads with activity list

### 3.3 Test Sensitive Data Filter

1. Look for "Sensitive Data" dropdown in filters
2. Select "⚠️ Detected"
3. Verify only activities with sensitive data are shown
4. Select "Clean"
5. Verify only activities without sensitive data are shown

### 3.4 Test Severity Filter

1. Set "Sensitive Data" to "⚠️ Detected"
2. "Severity" dropdown should appear
3. Select "☢️ Critical"
4. Verify only critical severity activities shown

### 3.5 Verify Sensitive Column

In the activity table, check for "Sensitive" column showing:
- Badge with severity icon (☢️/⚠️/⚡/ℹ️)
- Detection count
- Tooltip showing detection types on hover

### 3.6 Test Detail Drawer

1. Click on an activity with sensitive data
2. Verify "Sensitive Data Detected" section appears
3. Check it shows:
   - Severity badge
   - Detection types as badges
   - Individual detections with type, severity, location
   - "example" badge for known test values

### 3.7 Active Filters Display

1. Apply multiple filters
2. Verify "Active filters" section shows badges for:
   - "Sensitive: ⚠️ Detected" or "Sensitive: Clean"
   - "Severity: critical" (when applicable)

---

## Part 4: MCP Client Testing (via mcpproxy tools)

### 4.1 Use retrieve_tools to find activity tools

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "retrieve_tools",
      "arguments": {
        "query": "activity sensitive"
      }
    }
  }'
```

### 4.2 Test via Claude Code MCP

If using Claude Code with mcpproxy as MCP server:

```
# In Claude Code conversation:
Search for activities with sensitive data detected
```

This should use the mcpproxy tools to query activities.

---

## Test Scenarios Summary

| Scenario | Detection Type | Severity | Test Value |
|----------|---------------|----------|------------|
| AWS Key | aws_access_key | critical | AKIAIOSFODNN7EXAMPLE |
| AWS Secret | aws_secret_key | critical | wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY |
| GitHub PAT | github_pat | critical | ghp_1234567890abcdefghijABCDEFGHIJ123456 |
| Visa Card | credit_card | high | 4111111111111111 |
| RSA Key | rsa_private_key | critical | -----BEGIN RSA PRIVATE KEY----- |
| PostgreSQL | postgresql_uri | critical | postgres://user:pass@host/db |
| SSH Path | ssh_private_key | high | ~/.ssh/id_rsa |
| .env Path | env_file | medium | .env.production |

---

## Validation Checklist

### API Response Validation
- [ ] `has_sensitive_data` field present and correct
- [ ] `detection_types` array contains expected types
- [ ] `max_severity` shows highest severity
- [ ] `metadata.sensitive_data_detection.detections` array populated
- [ ] `is_likely_example` true for known test values

### CLI Validation
- [ ] SENSITIVE column shows in table output
- [ ] `--sensitive-data` flag filters correctly
- [ ] `--detection-type` flag filters correctly
- [ ] `--severity` flag filters correctly
- [ ] `activity show` displays detection details
- [ ] JSON output includes all fields

### Web UI Validation
- [ ] Sensitive Data filter dropdown works
- [ ] Severity filter appears when sensitive filter active
- [ ] Sensitive column shows badge with count
- [ ] Tooltip shows detection types
- [ ] Detail drawer shows detection section
- [ ] Active filters display correctly

---

## Troubleshooting

### No detections appearing

1. Check if detection is enabled:
   ```bash
   curl "$BASE_URL/api/v1/config" -H "X-API-Key: $API_KEY" | jq '.sensitive_data_detection'
   ```

2. Wait for async detection (2 seconds after tool call)

3. Check server logs for detection errors:
   ```bash
   tail -f ~/.mcpproxy/logs/main.log | grep -i sensitive
   ```

### Detection not matching expected pattern

1. Check pattern definitions in `internal/security/patterns/`
2. Verify the test value matches the regex pattern
3. Check if category is enabled in config

### Web UI not showing filters

1. Hard refresh the page (Cmd+Shift+R)
2. Rebuild frontend: `cd frontend && npm run build`
3. Clear browser cache
