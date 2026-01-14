# Data Model: Expand Activity Log

**Date**: 2026-01-12
**Feature**: 024-expand-activity-log

## Entity Changes

### 1. ActivityType Enumeration (Extended)

**Location**: `internal/storage/activity_models.go`

| Constant | Value | Description |
|----------|-------|-------------|
| `ActivityTypeToolCall` | `"tool_call"` | Upstream tool execution (existing) |
| `ActivityTypePolicyDecision` | `"policy_decision"` | Policy blocking a tool call (existing) |
| `ActivityTypeQuarantineChange` | `"quarantine_change"` | Server quarantine state change (existing) |
| `ActivityTypeServerChange` | `"server_change"` | Server enable/disable/restart (existing) |
| `ActivityTypeSystemStart` | `"system_start"` | **NEW**: MCPProxy server started |
| `ActivityTypeSystemStop` | `"system_stop"` | **NEW**: MCPProxy server stopped |
| `ActivityTypeInternalToolCall` | `"internal_tool_call"` | **NEW**: Internal MCP tool call |
| `ActivityTypeConfigChange` | `"config_change"` | **NEW**: Configuration change |

### 2. ActivityRecord (Extended Metadata)

**Location**: `internal/storage/activity_models.go`

The `ActivityRecord` struct remains unchanged. New event types use the existing `Metadata` field to store type-specific data.

```go
type ActivityRecord struct {
    ID                string                 `json:"id"`
    Type              ActivityType           `json:"type"`
    Source            ActivitySource         `json:"source,omitempty"`
    ServerName        string                 `json:"server_name,omitempty"`
    ToolName          string                 `json:"tool_name,omitempty"`
    Arguments         map[string]interface{} `json:"arguments,omitempty"`
    Response          string                 `json:"response,omitempty"`
    ResponseTruncated bool                   `json:"response_truncated,omitempty"`
    Status            string                 `json:"status"`
    ErrorMessage      string                 `json:"error_message,omitempty"`
    DurationMs        int64                  `json:"duration_ms,omitempty"`
    Timestamp         time.Time              `json:"timestamp"`
    SessionID         string                 `json:"session_id,omitempty"`
    RequestID         string                 `json:"request_id,omitempty"`
    Metadata          map[string]interface{} `json:"metadata,omitempty"`
}
```

### 3. Type-Specific Metadata Schemas

#### 3.1 SystemStartMetadata

For `system_start` events:

```json
{
  "version": "v1.2.3",
  "listen_address": "127.0.0.1:8080",
  "startup_duration_ms": 1250,
  "config_path": "/Users/user/.mcpproxy/mcp_config.json",
  "unclean_previous_shutdown": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | MCPProxy version |
| `listen_address` | string | HTTP listener address |
| `startup_duration_ms` | int64 | Time from command start to ready |
| `config_path` | string | Path to config file used |
| `unclean_previous_shutdown` | bool | True if previous session didn't log system_stop |

#### 3.2 SystemStopMetadata

For `system_stop` events:

```json
{
  "reason": "signal",
  "signal": "SIGTERM",
  "uptime_seconds": 3600,
  "error_message": ""
}
```

| Field | Type | Description |
|-------|------|-------------|
| `reason` | string | Shutdown reason: `"signal"`, `"manual"`, `"error"` |
| `signal` | string | Signal name if reason is signal |
| `uptime_seconds` | int64 | Total server uptime |
| `error_message` | string | Error details if reason is error |

#### 3.3 InternalToolCallMetadata

For `internal_tool_call` events:

```json
{
  "internal_tool_name": "retrieve_tools",
  "target_server": "github",
  "target_tool": "create_issue",
  "tool_variant": "call_tool_write",
  "intent": {
    "operation_type": "write",
    "data_sensitivity": "internal",
    "reason": "Creating issue per user request"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `internal_tool_name` | string | Name of internal tool called |
| `target_server` | string | Target upstream server (for call_tool_*) |
| `target_tool` | string | Target upstream tool (for call_tool_*) |
| `tool_variant` | string | Tool variant used (call_tool_read/write/destructive) |
| `intent` | object | Intent declaration (from Spec 018) |

#### 3.4 ConfigChangeMetadata

For `config_change` events:

```json
{
  "action": "server_added",
  "affected_entity": "github-server",
  "changed_fields": ["enabled", "quarantined"],
  "previous_values": {"enabled": false},
  "new_values": {"enabled": true}
}
```

| Field | Type | Description |
|-------|------|-------------|
| `action` | string | `"server_added"`, `"server_removed"`, `"server_updated"`, `"settings_changed"` |
| `affected_entity` | string | Name of affected server or "global" |
| `changed_fields` | []string | List of fields that changed |
| `previous_values` | object | Previous values for changed fields |
| `new_values` | object | New values for changed fields |

### 4. ActivityFilter (Extended)

**Location**: `internal/storage/activity_models.go`

```go
type ActivityFilter struct {
    Types      []string  // Changed from single Type string - supports multi-type OR filter
    Server     string
    Tool       string
    SessionID  string
    Status     string
    StartTime  time.Time
    EndTime    time.Time
    Limit      int
    Offset     int
    IntentType string
    RequestID  string
}
```

**Changes**:
- `Type string` → `Types []string`
- `Matches()` method updated for OR logic on types

### 5. EventType Constants (Extended)

**Location**: `internal/runtime/events.go`

| Constant | Value | Description |
|----------|-------|-------------|
| `EventTypeActivitySystemStart` | `"activity.system.start"` | **NEW**: System started |
| `EventTypeActivitySystemStop` | `"activity.system.stop"` | **NEW**: System stopped |
| `EventTypeActivityInternalToolCall` | `"activity.internal_tool_call.completed"` | **NEW**: Internal tool call completed |
| `EventTypeActivityConfigChange` | `"activity.config_change"` | **NEW**: Config changed |

## Validation Rules

### ActivityType Validation

Valid activity types for filtering:

```go
var ValidActivityTypes = []string{
    "tool_call",
    "policy_decision",
    "quarantine_change",
    "server_change",
    "system_start",
    "system_stop",
    "internal_tool_call",
    "config_change",
}
```

### ConfigChange Action Validation

Valid config change actions:

```go
var ValidConfigActions = []string{
    "server_added",
    "server_removed",
    "server_updated",
    "settings_changed",
}
```

## State Transitions

### Server Lifecycle

```
[Not Running] → system_start → [Running] → system_stop → [Not Running]
```

### Config Change Flow

```
[User Action] → [API/CLI Handler] → [ServerManager] → [Storage]
                                          ↓
                              [EventBus: servers.changed]
                                          ↓
                              [ActivityService] → config_change record
```

## Storage Considerations

- **No schema migration required**: New activity types use existing BBolt bucket
- **Metadata flexibility**: Type-specific data stored in `Metadata` map
- **Retention unchanged**: Existing retention policy applies to all activity types
- **Index unchanged**: No new BBolt indexes needed (filter by type uses iteration)

## Backwards Compatibility

- Existing `Type` filter API parameter continues to work (treated as single-element array)
- Existing activity records remain valid
- Clients that don't understand new types will see them as unknown strings
- SSE events for new types will be ignored by old clients
