# Data Model: Activity Log Backend

**Feature**: 016-activity-log-backend
**Date**: 2025-12-26

## Entities

### ActivityRecord

Primary entity for storing all activity events.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier (ULID format) |
| `type` | ActivityType | Yes | Type of activity |
| `server_name` | string | No | Name of upstream MCP server (if applicable) |
| `tool_name` | string | No | Name of tool called (if applicable) |
| `arguments` | map[string]any | No | Tool call arguments (JSON) |
| `response` | string | No | Tool response (potentially truncated) |
| `response_truncated` | bool | No | True if response was truncated |
| `status` | string | Yes | Result status: "success", "error", "blocked" |
| `error_message` | string | No | Error details if status is "error" |
| `duration_ms` | int64 | No | Execution duration in milliseconds |
| `timestamp` | time.Time | Yes | When activity occurred |
| `session_id` | string | No | MCP session ID for correlation |
| `request_id` | string | No | HTTP request ID for correlation |
| `metadata` | map[string]any | No | Additional context-specific data |

**Storage Key**: `{timestamp_ns}_{id}` for reverse-chronological ordering

**Bucket**: `activity_records`

### ActivityType (Enumeration)

| Value | Description |
|-------|-------------|
| `tool_call` | Tool execution (start and completion) |
| `policy_decision` | Policy blocked a tool call |
| `quarantine_change` | Server quarantine state changed |
| `server_change` | Server added, removed, or configuration changed |

### ActivityFilter

Query parameters for filtering activity records.

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Filter by activity type |
| `server` | string | Filter by server name |
| `tool` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session |
| `status` | string | Filter by status (success/error/blocked) |
| `start_time` | time.Time | Activities after this time |
| `end_time` | time.Time | Activities before this time |
| `limit` | int | Max records to return (default 50, max 100) |
| `offset` | int | Pagination offset |

### ActivityEvent (SSE Payload)

Real-time event pushed to connected clients.

| Field | Type | Description |
|-------|------|-------------|
| `event_type` | string | SSE event name |
| `activity_id` | string | Reference to ActivityRecord |
| `timestamp` | int64 | Unix timestamp |
| `payload` | map[string]any | Event-specific data |

**Event Types**:
- `activity.tool_call.started` - Tool execution began
- `activity.tool_call.completed` - Tool execution finished
- `activity.policy_decision` - Policy blocked a call
- `activity.quarantine_change` - Quarantine state changed

## Relationships

```
┌─────────────────┐
│  MCPSession     │
│  (existing)     │
└────────┬────────┘
         │ 1:N
         ▼
┌─────────────────┐
│ ActivityRecord  │◄──────────────┐
└────────┬────────┘               │
         │                        │
         │ type="tool_call"       │ type="quarantine_change"
         ▼                        │
┌─────────────────┐      ┌────────┴────────┐
│  ToolCallRecord │      │  ServerConfig   │
│  (existing)     │      │  (existing)     │
└─────────────────┘      └─────────────────┘
```

## Validation Rules

### ActivityRecord

| Rule | Validation |
|------|------------|
| ID format | Must be valid ULID (26 chars, Crockford Base32) |
| Type | Must be one of defined ActivityType values |
| Status | Must be "success", "error", or "blocked" |
| Timestamp | Must not be zero, must not be in future |
| Server name | If type is `tool_call`, must not be empty |
| Tool name | If type is `tool_call`, must not be empty |

### ActivityFilter

| Rule | Validation |
|------|------------|
| Limit | 1-100, default 50 |
| Offset | >= 0 |
| Time range | start_time must be before end_time |

## State Transitions

### Tool Call Lifecycle

```
                    EmitActivity(started)
                           │
    ┌──────────────────────▼──────────────────────┐
    │                  STARTED                     │
    │  status: "pending"                           │
    │  timestamp: now                              │
    └──────────────────────┬───────────────────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
         ┌────────┐  ┌──────────┐  ┌─────────┐
         │SUCCESS │  │  ERROR   │  │ BLOCKED │
         │        │  │          │  │(policy) │
         └────────┘  └──────────┘  └─────────┘
              │            │            │
              └────────────┼────────────┘
                           │
                           ▼
                    EmitActivity(completed)
```

## Storage Schema

### BBolt Buckets

```go
const (
    ActivityRecordsBucket = "activity_records"  // Primary storage
)
```

### Key Format

```
Key:   {timestamp_ns}_{ulid}
       "17035123456789012345_01HQWX1Y2Z3A4B5C6D7E8F9G0H"

Value: JSON-encoded ActivityRecord
```

### Index Considerations

BBolt doesn't support secondary indexes. For efficient filtering:

1. **Time queries**: Natural key ordering supports efficient range scans
2. **Server/tool filters**: Post-fetch filtering (acceptable for <100K records)
3. **Session correlation**: Store session_id in record, filter in memory

For future scale (>100K records), consider:
- Separate bucket per server
- Secondary index buckets (server→activity_ids, session→activity_ids)

## Configuration Schema

New fields in `mcp_config.json`:

```json
{
    "activity_retention_days": 90,
    "activity_max_records": 100000,
    "activity_max_response_size": 65536,
    "activity_cleanup_interval_hours": 1
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `activity_retention_days` | int | 90 | Max age before pruning |
| `activity_max_records` | int | 100000 | Max records before pruning |
| `activity_max_response_size` | int | 65536 | Response truncation limit (bytes) |
| `activity_cleanup_interval_hours` | int | 1 | Background cleanup interval |
