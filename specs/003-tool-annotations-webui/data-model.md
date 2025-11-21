# Data Model: Tool Annotations & MCP Sessions

**Feature Branch**: `003-tool-annotations-webui`
**Date**: 2025-11-19

## Entities

### ToolAnnotation

Advisory metadata about a tool's behavior characteristics.

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| title | string | No | Human-readable title for UI display |
| readOnlyHint | *bool | No | If true, tool does not modify environment |
| destructiveHint | *bool | No | If true, tool may perform destructive updates |
| idempotentHint | *bool | No | If true, repeated calls have same effect |
| openWorldHint | *bool | No | If true, tool interacts with external systems |

**Notes**:
- Booleans are pointers to distinguish `false` from `not set`
- All fields optional per MCP specification
- Sourced from upstream MCP server tool definitions

---

### MCPSession

A connection session between a client and MCPProxy.

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| id | string | Yes | UUID, unique session identifier |
| clientName | string | No | MCP client application name |
| clientVersion | string | No | MCP client version string |
| status | string | Yes | "active" or "closed" |
| startTime | time.Time | Yes | Session start timestamp |
| endTime | *time.Time | No | Session end timestamp (nil if active) |
| toolCallCount | int | Yes | Number of tool calls in session |
| totalTokens | int | Yes | Aggregate tokens across all calls |

**State Transitions**:
```
[Created] → status: "active", endTime: nil
    │
    ▼
[Closed] → status: "closed", endTime: now()
```

**Validation Rules**:
- `id` must be valid UUID
- `status` must be "active" or "closed"
- `toolCallCount` >= 0
- `totalTokens` >= 0
- `endTime` must be nil when status is "active"
- `endTime` must be set when status is "closed"

**Retention**:
- Maximum 100 sessions stored
- Oldest sessions auto-deleted when limit exceeded

---

### Tool (Extended)

Existing tool entity with annotations added.

**New Field**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| annotations | ToolAnnotation | No | Tool behavior hints |

**Full Structure**:
```go
type Tool struct {
    Name        string                 `json:"name"`
    ServerName  string                 `json:"server_name"`
    Description string                 `json:"description"`
    Schema      map[string]interface{} `json:"schema,omitempty"`
    Usage       int                    `json:"usage"`
    LastUsed    *time.Time             `json:"last_used,omitempty"`
    Annotations *ToolAnnotation        `json:"annotations,omitempty"` // NEW
}
```

---

### ToolCallRecord (Extended)

Existing tool call record with annotations and session reference.

**New Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| annotations | ToolAnnotation | No | Snapshot of tool annotations at call time |

**Existing Session Fields** (already present):
| Field | Type | Description |
|-------|------|-------------|
| mcp_session_id | string | Session this call belongs to |
| mcp_client_name | string | Client name from session |
| mcp_client_version | string | Client version from session |

**Full Structure**:
```go
type ToolCallRecord struct {
    ID               string                 `json:"id"`
    ServerID         string                 `json:"server_id"`
    ServerName       string                 `json:"server_name"`
    ToolName         string                 `json:"tool_name"`
    Arguments        map[string]interface{} `json:"arguments"`
    Response         interface{}            `json:"response"`
    Error            string                 `json:"error"`
    Duration         int64                  `json:"duration"`
    Timestamp        time.Time              `json:"timestamp"`
    ConfigPath       string                 `json:"config_path"`
    RequestID        string                 `json:"request_id"`
    Metrics          *TokenMetrics          `json:"metrics,omitempty"`
    ParentCallID     string                 `json:"parent_call_id,omitempty"`
    ExecutionType    string                 `json:"execution_type,omitempty"`
    MCPSessionID     string                 `json:"mcp_session_id,omitempty"`
    MCPClientName    string                 `json:"mcp_client_name,omitempty"`
    MCPClientVersion string                 `json:"mcp_client_version,omitempty"`
    Annotations      *ToolAnnotation        `json:"annotations,omitempty"` // NEW
}
```

---

## Relationships

```
MCPSession (1) ←──────→ (N) ToolCallRecord
    │                           │
    │ mcp_session_id            │ annotations
    │                           │
    ▼                           ▼
  Sessions                ToolAnnotation
   bucket                   (embedded)
```

**MCPSession ↔ ToolCallRecord**:
- One session has many tool calls
- Tool calls reference session via `mcp_session_id`
- Session aggregates calculated from tool calls (count, tokens)

**Tool ↔ ToolAnnotation**:
- One tool has one annotation object
- Annotations embedded in tool definition
- Annotations snapshotted into tool call records

---

## Storage Schema

### BBolt Buckets

**Existing Buckets** (unchanged):
- `server_{serverID}_tool_calls` - Tool call records per server

**New Bucket**:
- `sessions` - MCP session lifecycle records

### Session Bucket Key Format

**Pattern**: `{start_timestamp_ns}_{session_id}`

**Example**: `1700000000000000000_550e8400-e29b-41d4-a716-446655440000`

**Benefits**:
- Natural reverse chronological order (newest first on reverse iteration)
- Efficient "most recent N" queries
- Direct lookup by session ID still possible

### Indexes

**Session Lookup Index** (in-memory):
```go
sessionIndex map[string][]byte  // session_id → bucket_key
```

**Purpose**: O(1) session lookup without full bucket scan.

---

## TypeScript Interfaces

### Frontend Types

```typescript
interface ToolAnnotation {
  title?: string
  readOnlyHint?: boolean
  destructiveHint?: boolean
  idempotentHint?: boolean
  openWorldHint?: boolean
}

interface MCPSession {
  id: string
  client_name?: string
  client_version?: string
  status: 'active' | 'closed'
  start_time: string  // ISO 8601
  end_time?: string   // ISO 8601, null if active
  tool_call_count: number
  total_tokens: number
}

interface Tool {
  name: string
  server_name: string
  description: string
  schema?: Record<string, any>
  usage: number
  last_used?: string
  annotations?: ToolAnnotation  // NEW
}

interface ToolCallRecord {
  // ... existing fields ...
  annotations?: ToolAnnotation  // NEW
}
```

---

## Query Patterns

### Get Recent Sessions
```go
// Returns 10 most recent sessions for dashboard
sessions, err := storage.GetRecentSessions(10)
```

### Filter Tool Calls by Session
```go
// Returns tool calls for specific session
calls, err := storage.GetToolCallsBySession(sessionID, limit, offset)
```

### Get Session Aggregates
```go
// Returns session with calculated tool_call_count and total_tokens
session, err := storage.GetSessionWithAggregates(sessionID)
```

### Update Session on Tool Call
```go
// Called after each tool call to update session aggregates
err := storage.IncrementSessionStats(sessionID, tokenCount)
```

---

## Migration Notes

### Backward Compatibility

**Existing ToolCallRecords**:
- `annotations` field will be nil for existing records
- Frontend handles nil annotations gracefully (no badges displayed)

**Existing Sessions**:
- No existing session records (new bucket)
- Session tracking begins after deployment

### Data Migration

No migration required:
- New `sessions` bucket created on first access
- Existing tool call records work without modification
- Annotations populated for new tool calls only
