---
id: activity-commands
title: Activity Commands
sidebar_label: Activity Commands
sidebar_position: 3
description: CLI commands for querying and monitoring activity logs
keywords: [activity, logging, audit, cli, monitoring, observability]
---

# Activity Commands

The `mcpproxy activity` command group provides CLI access to activity logs for debugging, monitoring, and compliance.

## Overview

```
mcpproxy activity
├── list          List activity records with filtering
├── watch         Watch activity stream in real-time
├── show <id>     Show activity details
├── summary       Show activity statistics
└── export        Export activity records
```

## Output Formats

All activity commands support multiple output formats:

| Flag | Description |
|------|-------------|
| `--output table` | Human-readable table (default) |
| `--output json` or `--json` | JSON output for scripting |
| `--output yaml` | YAML output |

You can also set the default via environment variable:

```bash
export MCPPROXY_OUTPUT=json
```

---

## activity list

List activity records with filtering and pagination.

### Usage

```bash
mcpproxy activity list [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type (comma-separated for multiple): `tool_call`, `system_start`, `system_stop`, `internal_tool_call`, `config_change`, `policy_decision`, `quarantine_change`, `server_change` |
| `--server` | `-s` | | Filter by server name |
| `--tool` | | | Filter by tool name |
| `--status` | | | Filter by status: `success`, `error`, `blocked` |
| `--intent-type` | | | Filter by intent operation type: `read`, `write`, `destructive` |
| `--request-id` | | | Filter by HTTP request ID for log correlation |
| `--no-icons` | | | Disable emoji icons in output (use text instead) |
| `--session` | | | Filter by MCP session ID |
| `--start-time` | | | Filter records after this time (RFC3339) |
| `--end-time` | | | Filter records before this time (RFC3339) |
| `--limit` | `-n` | 50 | Max records to return (1-100) |
| `--offset` | | 0 | Pagination offset |

### Examples

```bash
# List recent activity
mcpproxy activity list

# List last 10 tool calls
mcpproxy activity list --type tool_call --limit 10

# List errors from github server
mcpproxy activity list --server github --status error

# List only read operations
mcpproxy activity list --intent-type read

# List destructive operations for audit
mcpproxy activity list --intent-type destructive --limit 100

# Find activity by request ID (useful for error correlation)
mcpproxy activity list --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

# List activity as JSON
mcpproxy activity list -o json

# List multiple event types (comma-separated)
mcpproxy activity list --type tool_call,config_change

# List system lifecycle events
mcpproxy activity list --type system_start,system_stop

# List internal tool calls (retrieve_tools, call_tool_*, upstream_servers, etc.)
mcpproxy activity list --type internal_tool_call

# List configuration changes
mcpproxy activity list --type config_change

# List activity from today
mcpproxy activity list --start-time "$(date -u +%Y-%m-%dT00:00:00Z)"

# Paginate through results
mcpproxy activity list --limit 20 --offset 40
```

### Output (Table)

```
ID               SRC  TYPE         SERVER      TOOL           INTENT  STATUS   DURATION   TIME
01JFXYZ123ABC    MCP  tool_call    github      create_issue   write   success  245ms      2 min ago
01JFXYZ123ABD    CLI  tool_call    filesystem  read_file      read    error    125ms      5 min ago
01JFXYZ123ABE    MCP  policy       private     get_secret     -       blocked  0ms        10 min ago

Showing 3 of 150 records (page 1)
```

**Intent Column**: Shows the declared operation type (`read`, `write`, `destructive`) or `-` if no intent was declared.

**Source Indicators:**
- `MCP` - AI agent call via MCP protocol
- `CLI` - Direct CLI command (`mcpproxy call tool`)
- `API` - REST API call

### Output (JSON)

```json
{
  "activities": [
    {
      "id": "01JFXYZ123ABC",
      "type": "tool_call",
      "source": "mcp",
      "server_name": "github",
      "tool_name": "create_issue",
      "status": "success",
      "duration_ms": 245,
      "timestamp": "2025-01-15T10:30:00Z",
      "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
    }
  ],
  "total": 150,
  "limit": 50,
  "offset": 0
}
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error fetching activity |
| 2 | Invalid filter parameters |

---

## activity watch

Watch activity stream in real-time via SSE.

### Usage

```bash
mcpproxy activity watch [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type (comma-separated for multiple): `tool_call`, `system_start`, `system_stop`, `internal_tool_call`, `config_change`, `policy_decision`, `quarantine_change`, `server_change` |
| `--server` | `-s` | | Filter by server name |

### Examples

```bash
# Watch all activity
mcpproxy activity watch

# Watch only tool calls from github
mcpproxy activity watch --type tool_call --server github

# Watch system and config events
mcpproxy activity watch --type system_start,system_stop,config_change

# Watch with JSON output (NDJSON)
mcpproxy activity watch -o json

# Press Ctrl+C to stop
```

### Output (Table - Streaming)

```
[10:30:45] [MCP] github:create_issue ✓ 245ms
[10:30:46] [CLI] filesystem:write_file ✗ 125ms permission denied
[10:30:47] [MCP] private:get_data ⊘ BLOCKED policy:no-external
^C
Received interrupt, stopping...
```

Source indicators (`[MCP]`, `[CLI]`, `[API]`) show how the tool call was triggered.

### Output (JSON - NDJSON)

```json
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABC","source":"mcp","server":"github","tool":"create_issue","status":"success","duration_ms":245}
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABD","source":"cli","server":"filesystem","tool":"write_file","status":"error","error":"permission denied"}
```

### Behavior

- Automatically reconnects on connection loss (exponential backoff)
- Exits cleanly on SIGINT (Ctrl+C) or SIGTERM
- Buffers high-volume events to prevent terminal flooding

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Normal exit (user interrupt) |
| 1 | Connection error (after retries exhausted) |

---

## activity show

Show full details of a specific activity record.

### Usage

```bash
mcpproxy activity show <id> [flags]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `id` | Activity record ID (ULID format) |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--include-response` | false | Show full response (may be large) |

### Examples

```bash
# Show activity details
mcpproxy activity show 01JFXYZ123ABC

# Show with full response body
mcpproxy activity show 01JFXYZ123ABC --include-response

# Show as JSON
mcpproxy activity show 01JFXYZ123ABC -o json
```

### Output (Table)

```
Activity Details
================

ID:           01JFXYZ123ABC
Type:         tool_call
Source:       MCP (AI agent via MCP protocol)
Server:       github
Tool:         create_issue
Status:       success
Duration:     245ms
Timestamp:    2025-01-15T10:30:00Z
Session ID:   mcp-session-abc123

Arguments:
  {
    "title": "Bug report: Login fails",
    "body": "When clicking login...",
    "labels": ["bug", "priority-high"]
  }

Response:
  Issue #123 created successfully
  URL: https://github.com/owner/repo/issues/123
```

**Source Values:**
- `MCP (AI agent via MCP protocol)` - Tool called by AI agent
- `CLI (CLI command)` - Tool called via `mcpproxy call tool` CLI command
- `API (REST API)` - Tool called via REST API directly

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Activity not found |
| 2 | Invalid ID format |

---

## activity summary

Show aggregated activity statistics for a time period.

### Usage

```bash
mcpproxy activity summary [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--period` | `-p` | 24h | Time period: `1h`, `24h`, `7d`, `30d` |
| `--by` | | | Group by: `server`, `tool`, `status` |

### Examples

```bash
# Show 24-hour summary
mcpproxy activity summary

# Show weekly summary
mcpproxy activity summary --period 7d

# Show summary grouped by server
mcpproxy activity summary --by server

# Show as JSON
mcpproxy activity summary -o json
```

### Output (Table)

```
Activity Summary (last 24h)
===========================

METRIC          VALUE
───────         ─────
Total Calls     150
Successful      142 (94.7%)
Errors          5 (3.3%)
Blocked         3 (2.0%)

TOP SERVERS
───────────
github          75 calls
filesystem      45 calls
database        20 calls

TOP TOOLS
─────────
github:create_issue      30 calls
filesystem:read_file     25 calls
database:query           15 calls
```

### Output (JSON)

```json
{
  "period": "24h",
  "total_count": 150,
  "success_count": 142,
  "error_count": 5,
  "blocked_count": 3,
  "success_rate": 0.947,
  "top_servers": [
    {"name": "github", "count": 75},
    {"name": "filesystem", "count": 45}
  ],
  "top_tools": [
    {"server": "github", "tool": "create_issue", "count": 30},
    {"server": "filesystem", "tool": "read_file", "count": 25}
  ]
}
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error fetching summary |
| 2 | Invalid period format |

---

## activity export

Export activity records for compliance and auditing.

### Usage

```bash
mcpproxy activity export [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | | | Output file path (stdout if not specified) |
| `--format` | `-f` | json | Export format: `json`, `csv` |
| `--include-bodies` | | false | Include full request/response bodies |
| *(filter flags)* | | | Same filters as `activity list` |

### Examples

```bash
# Export all activity as JSON Lines to file
mcpproxy activity export --output activity.jsonl

# Export as CSV
mcpproxy activity export --format csv --output activity.csv

# Export with full bodies for compliance
mcpproxy activity export --include-bodies --output full-audit.jsonl

# Export specific time range
mcpproxy activity export \
  --start-time 2025-01-01T00:00:00Z \
  --end-time 2025-01-31T23:59:59Z \
  --output january-2025.jsonl

# Export to stdout for piping
mcpproxy activity export --format csv | gzip > activity.csv.gz

# Export errors only
mcpproxy activity export --status error --output errors.jsonl

# Export specific event types
mcpproxy activity export --type tool_call,internal_tool_call --output tool-calls.jsonl

# Export config changes for audit
mcpproxy activity export --type config_change --output config-audit.jsonl
```

### Output (JSON - JSON Lines)

```json
{"id":"01JFXYZ123ABC","type":"tool_call","source":"mcp","server_name":"github","tool_name":"create_issue","status":"success","duration_ms":245,"timestamp":"2025-01-15T10:30:00Z"}
{"id":"01JFXYZ123ABD","type":"tool_call","source":"cli","server_name":"filesystem","tool_name":"read_file","status":"error","duration_ms":125,"timestamp":"2025-01-15T10:30:01Z"}
```

### Output (CSV)

```csv
id,type,source,server_name,tool_name,status,duration_ms,timestamp,error_message
01JFXYZ123ABC,tool_call,mcp,github,create_issue,success,245,2025-01-15T10:30:00Z,
01JFXYZ123ABD,tool_call,cli,filesystem,read_file,error,125,2025-01-15T10:30:01Z,permission denied
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error exporting |
| 2 | Invalid file path or permissions |

---

## Common Workflows

### Debug a Failed Tool Call

```bash
# 1. Find recent errors
mcpproxy activity list --status error --limit 5

# 2. Get details for specific error
mcpproxy activity show <id-from-step-1> --include-response
```

### Monitor Agent Behavior

```bash
# Watch what an AI agent is doing in real-time
mcpproxy activity watch

# Or filter to specific server
mcpproxy activity watch --server github
```

### Compliance Export

```bash
# Monthly audit export
mcpproxy activity export \
  --start-time "$(date -u -v-1m +%Y-%m-01T00:00:00Z)" \
  --include-bodies \
  --output audit-$(date +%Y-%m).jsonl
```

### Session Analysis

```bash
# Track all activity for a specific AI session
mcpproxy activity list --session <session-id> -o json | jq '.'
```

### Quick Statistics

```bash
# Quick overview of today's activity
mcpproxy activity summary --period 24h

# Weekly report as JSON
mcpproxy activity summary --period 7d -o json
```

---

## Error Handling

### Error Format (Table)

```
Error: Activity not found: 01JFXYZ123ABC
Hint: Use 'mcpproxy activity list' to find valid activity IDs
```

### Error Format (JSON)

```json
{
  "error": {
    "code": "ACTIVITY_NOT_FOUND",
    "message": "Activity not found: 01JFXYZ123ABC",
    "guidance": "Use 'mcpproxy activity list' to find valid activity IDs",
    "recovery_command": "mcpproxy activity list --limit 10"
  }
}
```

### Error Codes

| Code | Description |
|------|-------------|
| `ACTIVITY_NOT_FOUND` | Activity ID does not exist |
| `INVALID_ID_FORMAT` | Activity ID is not valid ULID |
| `INVALID_TYPE` | Unknown activity type |
| `INVALID_STATUS` | Unknown status value |
| `INVALID_TIME_FORMAT` | Time not in RFC3339 format |
| `INVALID_TIME_RANGE` | End time before start time |
| `CONNECTION_ERROR` | Cannot connect to daemon |
| `EXPORT_ERROR` | Error writing export file |

---

## Event Types Reference

The activity log captures the following event types:

| Type | Description |
|------|-------------|
| `tool_call` | Every tool call made through MCPProxy to upstream servers |
| `system_start` | MCPProxy server startup events |
| `system_stop` | MCPProxy server shutdown events |
| `internal_tool_call` | Internal proxy tool calls (`retrieve_tools`, `call_tool_*`, `code_execution`, `upstream_servers`, etc.) |
| `config_change` | Configuration changes (server added/removed/updated) |
| `policy_decision` | Tool calls blocked by policy rules |
| `quarantine_change` | Server quarantine/unquarantine events |
| `server_change` | Server enable/disable/restart events |

### Multi-Type Filtering

You can filter by multiple types using comma-separated values:

```bash
# Filter by multiple types
mcpproxy activity list --type tool_call,internal_tool_call

# System lifecycle events
mcpproxy activity list --type system_start,system_stop

# All config-related events
mcpproxy activity list --type config_change,quarantine_change,server_change
```

---

## Tips

- Use `--json` output for piping to `jq` for complex filtering
- The watch command automatically reconnects on network issues
- Export streams directly to file without loading all records in memory
- Time filters use RFC3339 format: `2025-01-15T10:30:00Z`
- Combine with `grep` and `jq` for advanced filtering

```bash
# Find all tool calls with "github" in JSON output
mcpproxy activity list -o json | jq '.activities[] | select(.server_name | contains("github"))'

# Count errors by server
mcpproxy activity list --status error -o json | jq -r '.activities[].server_name' | sort | uniq -c
```
