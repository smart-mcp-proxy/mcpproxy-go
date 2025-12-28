---
id: activity-log
title: Activity Log
sidebar_label: Activity Log
sidebar_position: 7
description: Track and audit AI agent tool calls with the Activity Log
keywords: [activity, logging, audit, observability, compliance]
---

# Activity Log

MCPProxy provides comprehensive activity logging to track AI agent tool calls, policy decisions, and system events. This enables debugging, auditing, and compliance monitoring.

## What Gets Logged

The activity log captures:

| Event Type | Description |
|------------|-------------|
| `tool_call` | Every tool call made through MCPProxy |
| `policy_decision` | Tool calls blocked by policy rules |
| `quarantine_change` | Server quarantine/unquarantine events |
| `server_change` | Server enable/disable/restart events |

### Tool Call Records

Each tool call record includes:

```json
{
  "id": "01JFXYZ123ABC",
  "type": "tool_call",
  "server_name": "github-server",
  "tool_name": "create_issue",
  "arguments": {"title": "Bug report", "body": "..."},
  "response": "Issue #123 created",
  "status": "success",
  "duration_ms": 245,
  "timestamp": "2025-01-15T10:30:00Z",
  "session_id": "mcp-session-abc123"
}
```

## CLI Commands

MCPProxy provides dedicated CLI commands for activity log access. See the full [Activity Commands Reference](/cli/activity-commands) for details.

### Quick Examples

```bash
# List recent activity
mcpproxy activity list

# List last 10 tool call errors
mcpproxy activity list --type tool_call --status error --limit 10

# Watch activity in real-time
mcpproxy activity watch

# Show activity statistics
mcpproxy activity summary --period 24h

# View specific activity details
mcpproxy activity show 01JFXYZ123ABC

# Export for compliance
mcpproxy activity export --output audit.jsonl
```

### Available Commands

| Command | Description |
|---------|-------------|
| `activity list` | List activity records with filtering and pagination |
| `activity watch` | Watch real-time activity stream via SSE |
| `activity show <id>` | Show full details of a specific activity |
| `activity summary` | Show aggregated statistics for a time period |
| `activity export` | Export activity records to file (JSON/CSV) |

All commands support `--output json`, `--output yaml`, or `--json` for machine-readable output.

---

## REST API

### List Activity

```bash
GET /api/v1/activity
```

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by type: `tool_call`, `policy_decision`, `quarantine_change`, `server_change` |
| `server` | string | Filter by server name |
| `tool` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session ID |
| `status` | string | Filter by status: `success`, `error`, `blocked` |
| `start_time` | string | Filter after this time (RFC3339) |
| `end_time` | string | Filter before this time (RFC3339) |
| `limit` | integer | Max records (1-100, default: 50) |
| `offset` | integer | Pagination offset (default: 0) |

**Example:**

```bash
# List recent tool calls
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity?type=tool_call&limit=10"

# Filter by server
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity?server=github-server"

# Filter by time range
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity?start_time=2025-01-15T00:00:00Z"
```

**Response:**

```json
{
  "success": true,
  "data": {
    "activities": [
      {
        "id": "01JFXYZ123ABC",
        "type": "tool_call",
        "server_name": "github-server",
        "tool_name": "create_issue",
        "status": "success",
        "duration_ms": 245,
        "timestamp": "2025-01-15T10:30:00Z"
      }
    ],
    "total": 150,
    "limit": 50,
    "offset": 0
  }
}
```

### Get Activity Detail

```bash
GET /api/v1/activity/{id}
```

Returns full details including request arguments and response data.

### Export Activity

```bash
GET /api/v1/activity/export
```

Export activity records for compliance and auditing.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `format` | string | Export format: `json` (JSON Lines) or `csv` |
| *(filters)* | | Same filters as list endpoint |

**Example:**

```bash
# Export as JSON Lines
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=json" > activity.jsonl

# Export as CSV
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=csv" > activity.csv

# Export specific time range
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?start_time=2025-01-01T00:00:00Z&end_time=2025-01-31T23:59:59Z"
```

## Real-time Events

Activity events are streamed via SSE for real-time monitoring:

```bash
curl -N "http://127.0.0.1:8080/events?apikey=$KEY"
```

**Events:**

| Event | Description |
|-------|-------------|
| `activity.tool_call.started` | Tool call initiated |
| `activity.tool_call.completed` | Tool call finished (success or error) |
| `activity.policy_decision` | Tool call blocked by policy |

**Example Event:**

```json
event: activity.tool_call.completed
data: {"id":"01JFXYZ123ABC","server":"github-server","tool":"create_issue","status":"success","duration_ms":245}
```

## Configuration

Activity logging is enabled by default. Configure via `mcp_config.json`:

```json
{
  "activity_retention_days": 90,
  "activity_max_records": 100000,
  "activity_max_response_size": 65536,
  "activity_cleanup_interval_min": 60
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `activity_retention_days` | 90 | Days to retain activity records |
| `activity_max_records` | 100000 | Maximum records before pruning oldest |
| `activity_max_response_size` | 65536 | Max response size stored (bytes) |
| `activity_cleanup_interval_min` | 60 | Background cleanup interval (minutes) |

## Use Cases

### Debugging Tool Calls

View recent tool calls to debug issues:

```bash
curl -H "X-API-Key: $KEY" \
  "http://127.0.0.1:8080/api/v1/activity?type=tool_call&status=error&limit=10"
```

### Compliance Auditing

Export activity for compliance review:

```bash
curl -H "X-API-Key: $KEY" \
  "http://127.0.0.1:8080/api/v1/activity/export?format=csv&start_time=2025-01-01T00:00:00Z" \
  > audit-q1-2025.csv
```

### Session Analysis

Track all activity for a specific AI session:

```bash
curl -H "X-API-Key: $KEY" \
  "http://127.0.0.1:8080/api/v1/activity?session_id=mcp-session-abc123"
```

### Real-time Monitoring

Monitor tool calls in real-time:

```bash
curl -N "http://127.0.0.1:8080/events?apikey=$KEY" | grep "activity.tool_call"
```

## Storage

Activity records are stored in BBolt database at `~/.mcpproxy/config.db`. The background cleanup process automatically prunes old records based on retention settings.

:::tip Performance
Activity logging is non-blocking and uses an event-driven architecture to minimize impact on tool call latency.
:::
