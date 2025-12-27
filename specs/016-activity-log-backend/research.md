# Research: Activity Log Backend

**Feature**: 016-activity-log-backend
**Date**: 2025-12-26

## Research Questions

Based on the spec and technical context, the following areas required investigation:

1. BBolt key design for time-ordered activity queries
2. Response truncation strategy for large payloads
3. Retention policy implementation (time + count based)
4. Export format best practices for streaming
5. Integration points for activity recording

---

## 1. BBolt Key Design for Activity Records

### Decision: Composite Key with Timestamp Prefix

Use `{timestamp_ns}_{activity_id}` format for natural reverse-chronological ordering.

### Rationale

BBolt stores keys in byte-sorted order. By using nanosecond timestamps as prefix:
- Cursor iteration from end gives newest-first ordering
- Range queries by time are efficient (seek to timestamp)
- Unique suffix prevents collisions for concurrent writes

### Implementation

```go
// Key format: 20-digit nanosecond timestamp + underscore + ULID
// Example: "17035123456789012345_01HQWX1Y2Z3A4B5C6D7E8F9G0H"
func activityKey(timestamp time.Time, id string) []byte {
    return []byte(fmt.Sprintf("%020d_%s", timestamp.UnixNano(), id))
}
```

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| ULID only | ULIDs are time-ordered but not easily range-queryable |
| UUID | Random order, poor for time-based queries |
| Auto-increment | Requires counter management, not timestamp-queryable |

---

## 2. Response Truncation Strategy

### Decision: Configurable Truncation with Indicator

Truncate response bodies exceeding configurable limit (default 64KB), store truncation indicator.

### Rationale

- Large LLM responses can exceed 100KB
- Storage efficiency matters at 100K records
- Users need to know if data was truncated
- Full response available via separate mechanism if needed

### Implementation

```go
type ActivityRecord struct {
    // ... other fields
    Response         string `json:"response"`          // Potentially truncated
    ResponseTruncated bool   `json:"response_truncated"` // True if truncated
}

const DefaultMaxResponseSize = 64 * 1024 // 64KB

func truncateResponse(response string, maxSize int) (string, bool) {
    if len(response) <= maxSize {
        return response, false
    }
    return response[:maxSize] + "...[truncated]", true
}
```

### Configuration

```json
{
    "activity_max_response_size": 65536
}
```

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| No truncation | Storage bloat, 100K records × 100KB = 10GB |
| Compress instead | CPU overhead, complexity |
| Store separately | Additional file management, complexity |

---

## 3. Retention Policy Implementation

### Decision: Dual-trigger Retention (Time + Count)

Delete records when either:
- Record age exceeds retention period (default 90 days), OR
- Total record count exceeds limit (default 100,000)

### Rationale

- Time-based ensures old data is purged for compliance
- Count-based ensures storage doesn't grow unbounded
- Background goroutine avoids blocking operations
- Uses existing `AsyncManager` pattern from storage

### Implementation

```go
type RetentionConfig struct {
    MaxAgeDays   int `json:"activity_retention_days"`   // Default: 90
    MaxRecords   int `json:"activity_max_records"`      // Default: 100000
    CleanupInterval time.Duration // Default: 1 hour
}

func (m *Manager) runActivityRetentionLoop(ctx context.Context) {
    ticker := time.NewTicker(m.retentionConfig.CleanupInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.pruneOldActivities()
            m.pruneExcessActivities()
        }
    }
}
```

### Cleanup Strategy

1. **Time-based pruning**: Delete all records older than `MaxAgeDays`
2. **Count-based pruning**: If count > `MaxRecords`, delete oldest until at 90% capacity

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| On-write pruning | Blocks tool calls, violates FR-019 |
| Manual cleanup only | Users forget, storage grows unbounded |
| Separate database | Complexity, another file to manage |

---

## 4. Export Format Implementation

### Decision: Streaming JSON Lines + CSV

Support both formats with streaming to handle large exports without memory issues.

### Rationale

- JSON Lines (JSONL) is easy to parse, one record per line
- CSV is universal for spreadsheets/compliance tools
- Streaming prevents OOM for 100K record exports
- Content-Disposition header triggers download

### Implementation

**JSON Lines Export**:
```go
func (s *Server) handleExportActivityJSONL(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/x-ndjson")
    w.Header().Set("Content-Disposition", "attachment; filename=activity.jsonl")

    encoder := json.NewEncoder(w)
    for record := range s.controller.StreamActivities(ctx, filters) {
        encoder.Encode(record)
        if flusher, ok := w.(http.Flusher); ok {
            flusher.Flush()
        }
    }
}
```

**CSV Export**:
```go
func (s *Server) handleExportActivityCSV(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/csv")
    w.Header().Set("Content-Disposition", "attachment; filename=activity.csv")

    writer := csv.NewWriter(w)
    writer.Write([]string{"id", "type", "server", "tool", "status", "timestamp"})

    for record := range s.controller.StreamActivities(ctx, filters) {
        writer.Write(recordToRow(record))
    }
    writer.Flush()
}
```

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| Full JSON array | Memory issues for large exports |
| Zip compression | Added complexity, streaming still possible |
| Background job | Overcomplicated for this use case |

---

## 5. Integration Points for Activity Recording

### Decision: Event-Driven Recording via Runtime

Record activities by:
1. Emitting events from tool call handler
2. Runtime receives and stores asynchronously
3. SSE automatically propagates to clients

### Rationale

- Non-blocking (FR-019) - tool calls don't wait for storage
- Decoupled - MCP handler doesn't know about storage
- Consistent with existing event bus pattern
- SSE "for free" via existing infrastructure

### Integration Points

**1. Tool Call Recording** (`internal/server/mcp.go`):
```go
func (p *MCPProxyServer) handleCallTool(...) {
    // Emit start event
    p.runtime.EmitActivity(ActivityToolCallStarted, map[string]any{
        "activity_id": activityID,
        "server":      serverName,
        "tool":        toolName,
        "arguments":   args,
    })

    // Execute tool call
    result, err := p.executeToolCall(...)

    // Emit completion event
    p.runtime.EmitActivity(ActivityToolCallCompleted, map[string]any{
        "activity_id": activityID,
        "status":      status,
        "duration_ms": duration,
        "response":    truncatedResponse,
    })
}
```

**2. Policy Decision Recording** (`internal/runtime/lifecycle.go`):
```go
func (r *Runtime) checkToolPolicy(serverName, toolName string) error {
    if blocked := r.policyEngine.Check(...); blocked {
        r.EmitActivity(ActivityPolicyDecision, map[string]any{
            "server": serverName,
            "tool":   toolName,
            "reason": "blocked by policy",
        })
        return ErrPolicyBlocked
    }
    return nil
}
```

**3. Quarantine Events** (already exists, extend):
```go
func (r *Runtime) QuarantineServer(serverName string, quarantined bool) error {
    // ... existing logic

    r.EmitActivity(ActivityQuarantineChange, map[string]any{
        "server":      serverName,
        "quarantined": quarantined,
    })
}
```

### Event Flow

```
Tool Call → MCP Handler → EmitActivity() → Event Bus → Runtime Subscriber
                                                            ↓
                                              ┌─────────────┴─────────────┐
                                              ↓                           ↓
                                        StorageManager              SSE Broadcast
                                        (async write)               (to clients)
```

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| Direct storage call | Blocks tool execution |
| Middleware approach | Doesn't capture policy decisions |
| Separate activity service | Unnecessary abstraction |

---

## Summary

All research questions resolved. Key decisions:

1. **BBolt Keys**: `{timestamp_ns}_{id}` for natural time ordering
2. **Truncation**: 64KB default with indicator flag
3. **Retention**: Dual-trigger (90 days OR 100K records) with background cleanup
4. **Export**: Streaming JSONL and CSV formats
5. **Integration**: Event-driven via existing event bus

No NEEDS CLARIFICATION items remain.
