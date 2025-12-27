# Quickstart: Activity Log Backend

**Feature**: 016-activity-log-backend
**Date**: 2025-12-26

## Overview

This guide helps developers get started implementing the Activity Log Backend feature. It covers the key integration points and provides working examples.

## Prerequisites

- Go 1.24+
- Running mcpproxy instance
- API key for authentication

## Key Files to Modify

| File | Purpose |
|------|---------|
| `internal/storage/activity.go` | BBolt storage operations |
| `internal/storage/activity_models.go` | Data types |
| `internal/runtime/events.go` | Event type definitions |
| `internal/runtime/activity_service.go` | Recording service |
| `internal/httpapi/activity.go` | REST handlers |
| `internal/contracts/activity.go` | API types |
| `internal/server/mcp.go` | Emit events on tool calls |

## Implementation Order

### 1. Storage Layer (Start Here)

```go
// internal/storage/activity_models.go

type ActivityType string

const (
    ActivityTypeToolCall         ActivityType = "tool_call"
    ActivityTypePolicyDecision   ActivityType = "policy_decision"
    ActivityTypeQuarantineChange ActivityType = "quarantine_change"
    ActivityTypeServerChange     ActivityType = "server_change"
)

type ActivityRecord struct {
    ID                string                 `json:"id"`
    Type              ActivityType           `json:"type"`
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

### 2. Event Types

```go
// internal/runtime/events.go (extend existing)

const (
    EventTypeActivityToolCallStarted   EventType = "activity.tool_call.started"
    EventTypeActivityToolCallCompleted EventType = "activity.tool_call.completed"
    EventTypeActivityPolicyDecision    EventType = "activity.policy_decision"
    EventTypeActivityQuarantineChange  EventType = "activity.quarantine_change"
)
```

### 3. Recording Service

```go
// internal/runtime/activity_service.go

type ActivityService struct {
    storage *storage.Manager
    logger  *zap.Logger
}

func (s *ActivityService) Record(record *storage.ActivityRecord) error {
    return s.storage.SaveActivity(record)
}

func (s *ActivityService) HandleActivityEvent(evt Event) {
    // Convert event to record and store
    record := eventToActivityRecord(evt)
    if err := s.Record(record); err != nil {
        s.logger.Error("Failed to record activity", zap.Error(err))
    }
}
```

### 4. REST Handlers

```go
// internal/httpapi/activity.go

func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
    // Parse filters from query params
    filters := parseActivityFilters(r)

    // Query storage
    activities, total, err := s.controller.ListActivities(filters)
    if err != nil {
        s.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }

    // Return response
    s.writeSuccess(w, contracts.ActivityListResponse{
        Activities: activities,
        Total:      total,
        Limit:      filters.Limit,
        Offset:     filters.Offset,
    })
}
```

## API Usage Examples

### List Activities

```bash
# Get recent activities
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity"

# Filter by server
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity?server=github"

# Filter by type and status
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity?type=tool_call&status=error"

# Pagination
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity?limit=20&offset=40"
```

### Get Activity Detail

```bash
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity/01HQWX1Y2Z3A4B5C6D7E8F9G0H"
```

### Export Activities

```bash
# JSON Lines format
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity/export?format=json" \
  -o activity.jsonl

# CSV format
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity/export?format=csv" \
  -o activity.csv
```

### SSE Events

```bash
# Connect to SSE stream (activities appear as they happen)
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/events"

# Events you'll see:
# event: activity.tool_call.started
# data: {"activity_id":"01HQ...","server":"github","tool":"create_issue"}
#
# event: activity.tool_call.completed
# data: {"activity_id":"01HQ...","status":"success","duration_ms":234}
```

## Testing

### Unit Tests

```bash
# Run activity storage tests
go test -v ./internal/storage -run TestActivity

# Run activity API tests
go test -v ./internal/httpapi -run TestActivity
```

### E2E Test

```bash
# Start mcpproxy
./mcpproxy serve &

# Make a tool call
curl -X POST -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{"server":"everything","tool":"echo","arguments":{"message":"test"}}' \
  "http://127.0.0.1:8080/api/v1/call-tool"

# Verify activity was recorded
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/activity?limit=1"
```

## Configuration

Add to `~/.mcpproxy/mcp_config.json`:

```json
{
    "activity_retention_days": 90,
    "activity_max_records": 100000,
    "activity_max_response_size": 65536
}
```

## Common Issues

### Activities Not Recording

1. Check event bus subscription is active
2. Verify storage manager is initialized
3. Check logs for "Failed to record activity" errors

### SSE Events Not Appearing

1. Verify SSE connection is established (look for heartbeat)
2. Check that activity events are being emitted (debug log)
3. Ensure activity event types are in the SSE handler switch

### Export Timeout

1. Add filters to reduce dataset size
2. Check server timeout configuration
3. Consider streaming the response (already implemented)

## References

- [Feature Spec](./spec.md)
- [Data Model](./data-model.md)
- [API Contract](./contracts/activity-api.yaml)
- [RFC-003 Activity Log](../../docs/proposals/003-activity-log.md)
