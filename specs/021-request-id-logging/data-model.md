# Data Model: Request ID Logging

**Feature**: 021-request-id-logging
**Date**: 2026-01-07

## Overview

This feature introduces request-scoped context and standardized error responses with request IDs. No database schema changes are required - these are runtime/API structures.

---

## Entity: RequestContext

**Purpose**: Request-scoped context carrying the request ID through the handler chain.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| request_id | string | Unique identifier for this request (UUID or client-provided) |
| logger | *zap.Logger | Logger instance with request_id field pre-set |
| start_time | time.Time | When the request was received |

**Constraints**:
- `request_id` follows pattern `^[a-zA-Z0-9_-]{1,256}$`
- Created in middleware at start of request
- Propagated via Go context.Context

**Go Implementation**:
```go
type RequestContext struct {
    RequestID string
    Logger    *zap.Logger
    StartTime time.Time
}

// Context keys
type contextKey string
const RequestContextKey contextKey = "request_context"

// Helper to get from context
func GetRequestContext(ctx context.Context) *RequestContext {
    if rc, ok := ctx.Value(RequestContextKey).(*RequestContext); ok {
        return rc
    }
    return nil
}

func GetRequestID(ctx context.Context) string {
    if rc := GetRequestContext(ctx); rc != nil {
        return rc.RequestID
    }
    return ""
}
```

---

## Entity: ErrorResponse

**Purpose**: Standard JSON structure for all API error responses.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| error | string | Machine-readable error code |
| message | string | Human-readable error description |
| request_id | string | Request ID for log correlation |
| suggestion | string (optional) | Actionable remediation hint |
| details | object (optional) | Additional error-specific data |

**Constraints**:
- `request_id` is always present
- `error` uses snake_case codes
- `suggestion` is populated for actionable errors

**Example (validation error)**:
```json
{
  "error": "server_not_found",
  "message": "Server 'google-drvie' not found",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "suggestion": "Check server name spelling. Did you mean 'google-drive'?",
  "details": {
    "available_servers": ["google-drive", "github-server"]
  }
}
```

**Example (internal error)**:
```json
{
  "error": "internal_error",
  "message": "An unexpected error occurred",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

**Go Implementation**:
```go
type ErrorResponse struct {
    Error     string                 `json:"error"`
    Message   string                 `json:"message"`
    RequestID string                 `json:"request_id"`
    Suggestion string                `json:"suggestion,omitempty"`
    Details   map[string]interface{} `json:"details,omitempty"`
}

func NewErrorResponse(ctx context.Context, code, message string) *ErrorResponse {
    return &ErrorResponse{
        Error:     code,
        Message:   message,
        RequestID: GetRequestID(ctx),
    }
}
```

---

## Entity: LogEntry

**Purpose**: Extended structured log entry with request ID field.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| level | string | Log level (debug, info, warn, error) |
| msg | string | Log message |
| timestamp | string | ISO 8601 timestamp |
| request_id | string (optional) | Request ID if in request context |
| correlation_id | string (optional) | OAuth flow correlation ID |
| server | string (optional) | Server name if applicable |
| ... | various | Other contextual fields |

**Example (request-scoped log)**:
```json
{
  "level": "info",
  "msg": "handling server list request",
  "timestamp": "2026-01-07T10:30:00Z",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "path": "/api/v1/servers",
  "method": "GET"
}
```

**Example (OAuth flow log with both IDs)**:
```json
{
  "level": "info",
  "msg": "OAuth callback received",
  "timestamp": "2026-01-07T10:30:05Z",
  "request_id": "req-abc123",
  "correlation_id": "oauth-def456",
  "server": "google-drive",
  "state": "authenticating"
}
```

**Note**: LogEntry is not a Go struct but represents the JSON structure produced by Zap logger.

---

## Entity: LogQueryParams

**Purpose**: Parameters for log retrieval filtering.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| request_id | string (optional) | Filter by request ID |
| correlation_id | string (optional) | Filter by OAuth correlation ID |
| server | string (optional) | Filter by server name |
| level | string (optional) | Filter by log level |
| since | time.Time (optional) | Start time filter |
| until | time.Time (optional) | End time filter |
| limit | int | Maximum entries to return (default 100) |

**Example API Request**:
```
GET /api/v1/logs?request_id=abc123&limit=50
```

**Go Implementation**:
```go
type LogQueryParams struct {
    RequestID     string    `json:"request_id,omitempty"`
    CorrelationID string    `json:"correlation_id,omitempty"`
    Server        string    `json:"server,omitempty"`
    Level         string    `json:"level,omitempty"`
    Since         time.Time `json:"since,omitempty"`
    Until         time.Time `json:"until,omitempty"`
    Limit         int       `json:"limit,omitempty"`
}
```

---

## HTTP Headers

### Request Header

| Header | Direction | Description |
|--------|-----------|-------------|
| `X-Request-Id` | Client → Server | Optional client-provided request ID |

**Validation**:
- Pattern: `^[a-zA-Z0-9_-]{1,256}$`
- If invalid or missing: server generates UUID v4

### Response Header

| Header | Direction | Description |
|--------|-----------|-------------|
| `X-Request-Id` | Server → Client | Request ID (client-provided or generated) |

**Behavior**:
- Always present in ALL responses (success and error)
- Set in middleware before handler execution
- Matches `request_id` in error response body

---

## Multi-Client Response Handling

All clients receive the same `X-Request-Id` header and error response structure:

| Client | On Error | Action |
|--------|----------|--------|
| CLI | Print request_id + suggestion | `mcpproxy logs --request-id <id>` |
| Tray | Notification with ID | Copy button, link to logs |
| Web UI | Modal with ID | Copy button, inline logs link |

---

## Relationships

```
┌──────────────────────┐
│   HTTP Request       │
│   X-Request-Id: ?    │
└──────────┬───────────┘
           │
           ▼
┌──────────────────────┐
│   RequestID          │
│   Middleware         │
│   (generate/validate)│
└──────────┬───────────┘
           │
           ▼
┌──────────────────────┐
│   RequestContext     │
│   + Logger w/ ID     │
└──────────┬───────────┘
           │
     ┌─────┴─────┐
     │           │
     ▼           ▼
┌─────────┐  ┌──────────────────┐
│ Handler │  │ Logs with        │
│ Logic   │  │ request_id field │
└────┬────┘  └──────────────────┘
     │
     ▼
┌──────────────────────┐
│   Response           │
│   X-Request-Id: xxx  │
│   + request_id body  │
└──────────────────────┘
```

---

## Storage Notes

- **No database changes**: Request IDs are transient per-request
- **Log storage**: Activity log (BBolt) extended with request_id field
- **Log retention**: Follows existing activity log retention policy
- **Index**: Activity log already indexed; request_id filtering via existing mechanisms

---

## Integration with Spec 020 (OAuth Login Feedback)

When OAuth login endpoint is called:

```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "oauth-abc123",
  "request_id": "req-xyz789",
  "auth_url": "https://...",
  "browser_opened": true
}
```

Both IDs appear in logs:
- `request_id`: Find logs for this specific HTTP request
- `correlation_id`: Find logs for entire OAuth flow (including callbacks)
