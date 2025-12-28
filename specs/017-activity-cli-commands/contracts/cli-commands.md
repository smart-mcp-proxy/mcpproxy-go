# CLI Contract: Activity Commands

**Date**: 2025-12-27
**Feature**: 017-activity-cli-commands

## Command Tree

```
mcpproxy activity
├── list          List activity records with filtering
├── watch         Watch activity stream in real-time
├── show <id>     Show activity details
├── summary       Show activity statistics
└── export        Export activity records
```

---

## Global Flags (inherited from root)

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `table` | Output format: table, json, yaml |
| `--json` | | | Shorthand for `-o json` |
| `--quiet` | `-q` | | Minimal output (errors only) |
| `--no-color` | | | Disable colored output |

---

## activity list

List activity records with filtering and pagination.

### Synopsis

```bash
mcpproxy activity list [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type: tool_call, policy_decision, quarantine_change, server_change |
| `--server` | `-s` | | Filter by server name |
| `--tool` | | | Filter by tool name |
| `--status` | | | Filter by status: success, error, blocked |
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

# List activity as JSON
mcpproxy activity list -o json

# List activity from today
mcpproxy activity list --start-time "$(date -u +%Y-%m-%dT00:00:00Z)"
```

### Output (table)

```
ID               TYPE         SERVER      TOOL           STATUS   DURATION   TIME
01JFXYZ123ABC    tool_call    github      create_issue   success  245ms      2 min ago
01JFXYZ123ABD    tool_call    filesystem  read_file      error    125ms      5 min ago
01JFXYZ123ABE    policy       private     get_secret     blocked  0ms        10 min ago

Showing 3 of 150 records (page 1)
```

### Output (json)

```json
{
  "activities": [
    {
      "id": "01JFXYZ123ABC",
      "type": "tool_call",
      "server_name": "github",
      "tool_name": "create_issue",
      "status": "success",
      "duration_ms": 245,
      "timestamp": "2025-12-27T10:30:00Z"
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

Watch activity stream in real-time.

### Synopsis

```bash
mcpproxy activity watch [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type |
| `--server` | `-s` | | Filter by server name |

### Examples

```bash
# Watch all activity
mcpproxy activity watch

# Watch only tool calls from github
mcpproxy activity watch --type tool_call --server github

# Watch with JSON output (one event per line)
mcpproxy activity watch -o json
```

### Output (table - streaming)

```
[10:30:45] github:create_issue ✓ 245ms
[10:30:46] filesystem:write_file ✗ 125ms permission denied
[10:30:47] private:get_data ⊘ BLOCKED policy:no-external
^C
Received interrupt, stopping...
```

### Output (json - NDJSON)

```json
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABC","server":"github","tool":"create_issue","status":"success","duration_ms":245,"timestamp":"2025-12-27T10:30:45Z"}
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABD","server":"filesystem","tool":"write_file","status":"error","duration_ms":125,"error":"permission denied","timestamp":"2025-12-27T10:30:46Z"}
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

### Synopsis

```bash
mcpproxy activity show <id> [flags]
```

### Arguments

| Arg | Description |
|-----|-------------|
| `id` | Activity record ID (ULID format) |

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--include-response` | | | Show full response (may be large) |

### Examples

```bash
# Show activity details
mcpproxy activity show 01JFXYZ123ABC

# Show with full response body
mcpproxy activity show 01JFXYZ123ABC --include-response

# Show as JSON
mcpproxy activity show 01JFXYZ123ABC -o json
```

### Output (table)

```
Activity Details
================

ID:           01JFXYZ123ABC
Type:         tool_call
Server:       github
Tool:         create_issue
Status:       success
Duration:     245ms
Timestamp:    2025-12-27T10:30:00Z
Session ID:   mcp-session-abc123

Arguments:
  title: "Bug report: Login fails"
  body: "When clicking login..."
  labels: ["bug", "priority-high"]

Response:
  Issue #123 created successfully
  URL: https://github.com/owner/repo/issues/123
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Activity not found |
| 2 | Invalid ID format |

---

## activity summary

Show activity statistics for a time period.

### Synopsis

```bash
mcpproxy activity summary [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--period` | `-p` | 24h | Time period: 1h, 24h, 7d, 30d |
| `--by` | | | Group by: server, tool, status |

### Examples

```bash
# Show 24-hour summary
mcpproxy activity summary

# Show weekly summary
mcpproxy activity summary --period 7d

# Show summary grouped by server
mcpproxy activity summary --by server

# Show summary as JSON
mcpproxy activity summary -o json
```

### Output (table)

```
Activity Summary (last 24h)
===========================

METRIC          VALUE
──────          ─────
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

### Output (json)

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

### Synopsis

```bash
mcpproxy activity export [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | | Output file path (stdout if not specified) |
| `--format` | `-f` | json | Export format: json, csv |
| `--include-bodies` | | | Include full request/response bodies |
| (filter flags) | | | Same as `activity list` |

### Examples

```bash
# Export all activity as JSON Lines to file
mcpproxy activity export --output activity.jsonl

# Export as CSV
mcpproxy activity export --format csv --output activity.csv

# Export with full bodies
mcpproxy activity export --include-bodies --output full-audit.jsonl

# Export specific time range
mcpproxy activity export \
  --start-time 2025-01-01T00:00:00Z \
  --end-time 2025-01-31T23:59:59Z \
  --output january-2025.jsonl

# Export to stdout (for piping)
mcpproxy activity export --format csv | gzip > activity.csv.gz
```

### Output (json - JSON Lines)

```json
{"id":"01JFXYZ123ABC","type":"tool_call","server_name":"github","tool_name":"create_issue","status":"success","duration_ms":245,"timestamp":"2025-12-27T10:30:00Z"}
{"id":"01JFXYZ123ABD","type":"tool_call","server_name":"filesystem","tool_name":"read_file","status":"error","duration_ms":125,"timestamp":"2025-12-27T10:30:01Z"}
```

### Output (csv)

```csv
id,type,server_name,tool_name,status,duration_ms,timestamp,error_message
01JFXYZ123ABC,tool_call,github,create_issue,success,245,2025-12-27T10:30:00Z,
01JFXYZ123ABD,tool_call,filesystem,read_file,error,125,2025-12-27T10:30:01Z,permission denied
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error exporting |
| 2 | Invalid file path or permissions |

---

## Error Messages

Standard error format for all commands:

### Table Format

```
Error: Activity not found: 01JFXYZ123ABC
Hint: Use 'mcpproxy activity list' to find valid activity IDs
```

### JSON Format

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
| ACTIVITY_NOT_FOUND | Activity ID does not exist |
| INVALID_ID_FORMAT | Activity ID is not valid ULID |
| INVALID_TYPE | Unknown activity type |
| INVALID_STATUS | Unknown status value |
| INVALID_TIME_FORMAT | Time not in RFC3339 format |
| INVALID_TIME_RANGE | End time before start time |
| CONNECTION_ERROR | Cannot connect to daemon |
| EXPORT_ERROR | Error writing export file |
