# Research: Tool Annotations & MCP Sessions in WebUI

**Feature Branch**: `003-tool-annotations-webui`
**Date**: 2025-11-19

## MCP Tool Annotations Structure

### Decision
Use the existing `ToolAnnotation` struct from mark3labs/mcp-go v0.42.0 SDK.

### Rationale
The MCP SDK already defines the complete annotation structure with all fields specified in the feature requirements. No custom implementation needed.

### Implementation Details

**SDK Type** (from `/Users/user/go/pkg/mod/github.com/mark3labs/mcp-go@v0.42.0/mcp/tools.go:656-667`):
```go
type ToolAnnotation struct {
    Title           string `json:"title,omitempty"`
    ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
    DestructiveHint *bool  `json:"destructiveHint,omitempty"`
    IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
    OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}
```

**Note**: Booleans are pointers to distinguish between `false` and `not set`.

### Alternatives Considered
- Custom annotation struct: Rejected - would duplicate SDK and risk drift
- Storing annotations as `map[string]interface{}`: Rejected - loses type safety

---

## Session Storage Strategy

### Decision
Create a new `sessions` BBolt bucket with session lifecycle tracking.

### Rationale
- Consistent with existing storage patterns (tool calls use per-server buckets)
- BBolt provides atomic writes and fast sequential reads
- Key format enables efficient "most recent" queries

### Implementation Details

**Bucket**: `sessions`
**Key Format**: `{start_timestamp_ns}_{session_id}` (enables reverse chronological iteration)
**Value**: JSON-serialized `MCPSession` struct

**Storage Operations**:
- `CreateSession(sessionID, clientName, clientVersion)` - On MCP initialize
- `UpdateSession(sessionID, toolCallCount, tokenSum)` - After each tool call
- `CloseSession(sessionID)` - On MCP transport close
- `GetRecentSessions(limit int)` - Iterate backwards from latest
- `GetSessionByID(sessionID)` - Direct key lookup

**Retention Policy**: Delete oldest sessions when count exceeds 100.

### Alternatives Considered
- In-memory only: Rejected - sessions lost on restart
- Per-session buckets: Rejected - overhead for simple lifecycle data
- SQLite: Rejected - adds dependency, BBolt sufficient for this scale

---

## Tool Call Record Extension

### Decision
Add `Annotations` field to existing `ToolCallRecord` struct.

### Rationale
- Preserves annotation snapshot at call time (tools can change)
- Enables annotation display in history without re-fetching tool definitions
- Backward compatible - existing records have nil annotations

### Implementation Details

**Updated ToolCallRecord** (in `internal/storage/server_identity.go`):
```go
type ToolCallRecord struct {
    // ... existing fields ...
    Annotations *ToolAnnotation `json:"annotations,omitempty"` // NEW
}
```

**Capture Point**: When `RecordToolCall` is invoked, lookup tool annotations from the server's tool list and attach to record.

### Alternatives Considered
- Store only annotation hashes: Rejected - requires separate lookup
- Denormalize into separate fields: Rejected - verbose, harder to extend

---

## Frontend Component Strategy

### Decision
Create reusable `AnnotationBadges.vue` component for both full and compact display modes.

### Rationale
- DRY: Same logic renders on Server Details and Tool Call History
- Prop-controlled: `compact` boolean switches between badge and icon-only modes
- Accessible: Tooltips provide full descriptions on hover

### Implementation Details

**Component Props**:
```typescript
interface Props {
  annotations: ToolAnnotation | null
  compact?: boolean  // default false
}
```

**Badge Styling** (DaisyUI classes):
- `readOnlyHint: true` → `badge-info` (blue)
- `destructiveHint: true` → `badge-error` (red)
- `idempotentHint: true` → `badge-neutral` (grey)
- `openWorldHint: true` → `badge-secondary` (purple)

**Compact Mode**: Icons only with `tooltip` on hover.

### Alternatives Considered
- Inline rendering in each view: Rejected - code duplication
- CSS-only badges: Rejected - need conditional logic for tooltips

---

## Session Filter Implementation

### Decision
URL query parameter `?sessionId={id}` on Tool Call History page.

### Rationale
- Shareable/bookmarkable URLs
- Works with browser back/forward
- Simple to implement with Vue Router

### Implementation Details

**Route**: `/tool-calls?sessionId=abc123`

**API Endpoint**: Extend `GET /api/v1/tool-calls` with optional `session_id` query param.

**UI**: Dropdown selector populated from recent sessions list.

**Navigation**: Clicking session row in dashboard opens `/tool-calls?sessionId={id}`.

### Alternatives Considered
- Local state only: Rejected - loses filter on refresh
- POST body filter: Rejected - not RESTful for read operations

---

## API Endpoint Design

### Decision
Add new session endpoints under `/api/v1/sessions`.

### Rationale
- Consistent with existing `/api/v1/tool-calls`, `/api/v1/servers` patterns
- Follows RESTful resource naming conventions

### New Endpoints

```
GET /api/v1/sessions                    # List recent sessions (limit, offset)
    Response: { sessions: MCPSession[], total: number }

GET /api/v1/sessions/{id}               # Get single session with aggregates
    Response: MCPSession

GET /api/v1/sessions/{id}/tool-calls    # Get tool calls for session
    Response: { tool_calls: ToolCallRecord[], total: number }
```

**Modified Endpoints**:
- `GET /api/v1/tool-calls` - Add `?session_id=` filter
- `GET /api/v1/tools` - Include annotations in Tool response

### Alternatives Considered
- Nest under `/api/v1/tool-calls/sessions`: Rejected - sessions are independent resource
- GraphQL: Rejected - overkill for this use case, REST sufficient

---

## Real-time Updates Strategy

### Decision
Emit SSE events for session lifecycle changes.

### Rationale
- Dashboard auto-updates when sessions open/close
- Consistent with existing `servers.changed` event pattern

### Implementation Details

**New Events**:
- `sessions.created` - New session started
- `sessions.updated` - Tool call added to session
- `sessions.closed` - Session ended

**Event Payload**:
```json
{
  "type": "sessions.updated",
  "data": { "session_id": "...", "tool_call_count": 5, "total_tokens": 1200 },
  "timestamp": "..."
}
```

**Dashboard Polling**: As fallback, poll every 30s for active session updates (per spec clarification).

### Alternatives Considered
- Polling only: Rejected - less responsive, more server load
- WebSocket: Rejected - SSE already established, lower complexity

---

## Token Aggregation Strategy

### Decision
Aggregate tokens on-the-fly when querying sessions.

### Rationale
- Avoids storing redundant aggregates that can drift
- Tool calls already store token metrics
- BBolt iteration is fast for bounded session sizes

### Implementation Details

**Aggregation Query**:
```go
func (m *Manager) GetSessionTokenSum(sessionID string) (int, error) {
    // Iterate tool calls with matching session_id
    // Sum metrics.TotalTokens
    return totalTokens, nil
}
```

**Caching**: Store `last_token_sum` in session record, update after each tool call for O(1) dashboard reads.

### Alternatives Considered
- Pre-aggregate in separate bucket: Rejected - sync complexity
- Compute only on dashboard load: Rejected - slow for many tool calls

---

## Tool Annotation Propagation

### Decision
Propagate annotations through the existing tool flow: Upstream → Index → API → Frontend.

### Rationale
- Annotations are part of the MCP tool definition
- Already have tool metadata flowing through these layers
- Minimal new plumbing required

### Implementation Details

**Flow**:
1. **Upstream**: mcp-go SDK provides `Tool.Annotations` from server
2. **Index**: Include annotations in BM25 document for search results
3. **Contracts**: Add `Annotations` field to `contracts.Tool`
4. **API**: Return annotations in tool list and search endpoints
5. **Frontend**: TypeScript interfaces match Go structs

**No changes to**:
- Storage of tool definitions (in-memory via stateview)
- Tool indexing logic (annotations are metadata, not searchable)

### Alternatives Considered
- Separate annotations endpoint: Rejected - unnecessary API complexity
- Store annotations separately: Rejected - couples tightly with tool identity

---

## Summary

All technical decisions align with existing MCPProxy patterns:
- BBolt storage with key ordering for efficient queries
- REST API following existing conventions
- SSE for real-time updates
- Vue components with DaisyUI styling
- Type-safe contracts between Go and TypeScript

No new dependencies required. Implementation can proceed using existing infrastructure.
