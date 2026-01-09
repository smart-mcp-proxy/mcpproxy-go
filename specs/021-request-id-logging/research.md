# Research: Request ID Logging

**Feature**: 021-request-id-logging
**Date**: 2026-01-07
**Status**: Complete

## Research Summary

This document captures design decisions for implementing request ID logging across all clients (CLI, tray, Web UI). The approach uses standard HTTP headers with server-side generation fallback.

---

## Decision 1: Header Name

**Decision**: Use `X-Request-Id` header

**Rationale**:
- Industry standard header name (used by Heroku, AWS, nginx, etc.)
- Well-documented behavior and expectations
- Existing tooling support (log parsers, proxy passthrough)
- `X-` prefix indicates non-standard but widely adopted

**Alternatives Considered**:
1. **`X-Correlation-Id`**: Already used for OAuth flows; would cause confusion
2. **`Request-Id`** (no X- prefix): Less widely recognized
3. **`Trace-Id`**: Implies distributed tracing which is out of scope
4. **Custom header**: No benefit over standard name

---

## Decision 2: ID Format

**Decision**: Use UUID v4 for server-generated IDs; accept alphanumeric with dashes/underscores from clients

**Rationale**:
- UUID v4 provides sufficient uniqueness without coordination
- Alphanumeric validation prevents injection attacks
- Dashes and underscores allow readable client-provided IDs
- 256 character limit prevents abuse

**ID Validation Rules**:
```
Pattern: ^[a-zA-Z0-9_-]{1,256}$
```

**Alternatives Considered**:
1. **UUID only**: Too restrictive for clients wanting readable IDs
2. **Any string**: Security risk (injection, memory exhaustion)
3. **Shorter IDs (ULID, nanoid)**: UUID is universally understood

---

## Decision 3: Generation Location

**Decision**: Generate in HTTP middleware (earliest point in request lifecycle)

**Rationale**:
- Ensures ALL requests have an ID, including errors in routing
- Single point of generation/validation
- ID available for entire request lifecycle
- Consistent behavior across all endpoints

**Implementation**:
```go
func RequestIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        requestID := r.Header.Get("X-Request-Id")
        if requestID == "" || !isValidRequestID(requestID) {
            requestID = uuid.New().String()
        }
        ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
        w.Header().Set("X-Request-Id", requestID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**Alternatives Considered**:
1. **Generate in handler**: Misses early errors
2. **Generate in client**: Requires all clients to implement
3. **Generate in router**: Still misses some early errors

---

## Decision 4: Response Header Behavior

**Decision**: Always return `X-Request-Id` in response header (success and error)

**Rationale**:
- Clients can always correlate request with response
- Works even when response body parsing fails
- Standard behavior matching industry practice
- No conditional logic needed

**Header Timing**:
- Set header in middleware BEFORE calling next handler
- Ensures header present even if handler panics

**Alternatives Considered**:
1. **Only on errors**: Inconsistent; clients can't predict when ID available
2. **Only when client provides**: Breaks server-generated ID flow

---

## Decision 5: Error Response Body

**Decision**: Include `request_id` field in ALL error JSON responses

**Rationale**:
- Redundant with header but more visible in error messages
- Easier for users to copy from JSON than inspect headers
- Aligns with existing error response patterns
- Enables direct display in UI without header parsing

**Error Response Structure**:
```json
{
  "error": "server_not_found",
  "message": "Server 'foo' not found",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

**Alternatives Considered**:
1. **Header only**: Harder for users to find
2. **Success responses too**: Unnecessary noise; header sufficient

---

## Decision 6: Logging Integration

**Decision**: Add `request_id` field to Zap logger context at middleware level

**Rationale**:
- Single place to add context field
- All downstream logs automatically include ID
- No changes needed to individual log calls
- Zap's structured logging makes filtering easy

**Implementation**:
```go
// In middleware
logger := zap.L().With(zap.String("request_id", requestID))
ctx := context.WithValue(ctx, LoggerKey, logger)
```

**Log Output**:
```json
{"level":"info","msg":"handling request","request_id":"abc123","path":"/api/v1/servers"}
```

**Alternatives Considered**:
1. **Manual logging**: Error-prone, inconsistent
2. **Separate log stream**: Over-engineered for this use case
3. **Log correlation service**: Adds external dependency

---

## Decision 7: CLI Error Display

**Decision**: Print Request ID to stderr on errors with log lookup suggestion

**Rationale**:
- stderr is appropriate for error information
- Suggestion provides actionable next step
- Does not clutter stdout (data output)
- Only shown on errors to reduce noise

**CLI Output Format**:
```
Error: Server 'foo' not found

Request ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
Run 'mcpproxy logs --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890' to see detailed logs
```

**Alternatives Considered**:
1. **Always show ID**: Too noisy for successful operations
2. **Show ID without suggestion**: Less actionable
3. **Hide ID entirely**: Loses debugging value

---

## Decision 8: Log Retrieval Endpoint

**Decision**: Add `--request-id` flag to existing `logs` command; add `request_id` query param to API

**Rationale**:
- Extends existing infrastructure (no new commands/endpoints)
- Consistent with existing filtering patterns
- Works for both CLI and API clients
- Activity log infrastructure (spec 016) already supports filtering

**CLI Usage**:
```bash
mcpproxy logs --request-id abc123
mcpproxy logs --request-id abc123 --tail 50
```

**API Usage**:
```bash
GET /api/v1/logs?request_id=abc123
```

**Alternatives Considered**:
1. **New endpoint**: Unnecessary; filtering is the same pattern
2. **Grep server-side**: Activity log already has structured data
3. **Client-side filtering**: Inefficient for large logs

---

## Decision 9: OAuth Integration

**Decision**: OAuth flows include both `request_id` and `correlation_id` in logs

**Rationale**:
- `request_id` tracks the initiating HTTP request
- `correlation_id` tracks the entire OAuth flow (multiple callbacks)
- Both IDs useful for different debugging scenarios
- No conflict; they serve different purposes

**Log Entry Example**:
```json
{
  "msg": "OAuth callback received",
  "request_id": "abc123",
  "correlation_id": "def456",
  "server": "google-drive"
}
```

**Lookup Behavior**:
- `--request-id abc123`: Finds the login request logs
- `--correlation-id def456`: Finds all OAuth flow logs

**Alternatives Considered**:
1. **Single ID**: Loses ability to trace specific request vs flow
2. **Merge IDs**: Confusing; they have different scopes

---

## Decision 10: Tray/Web UI Display

**Decision**: Error modals/notifications include Request ID with copy affordance

**Rationale**:
- Users need to copy ID for support/debugging
- Copy button is more user-friendly than selecting text
- Link to logs provides immediate access
- Consistent experience across clients

**Web UI Example**:
```html
<div class="error-modal">
  <h3>Error</h3>
  <p>Server 'foo' not found</p>
  <div class="request-id">
    <span>Request ID: abc123</span>
    <button onclick="copyToClipboard('abc123')">Copy</button>
  </div>
  <a href="/logs?request_id=abc123">View Logs</a>
</div>
```

**Tray Notification**:
- Shows abbreviated ID in notification body
- "Copy ID" action button
- Click notification opens logs in browser

**Alternatives Considered**:
1. **Hide ID from users**: Loses debugging value
2. **Show full ID always**: Too long for notifications

---

## Technical Dependencies

| Dependency | Purpose | Version |
|------------|---------|---------|
| google/uuid | UUID generation | Existing |
| uber-go/zap | Structured logging | Existing |
| Chi router | HTTP middleware | Existing |

No new external dependencies required.

---

## Implementation Notes

### Middleware Order

```go
router.Use(RequestIDMiddleware)  // First - generates ID
router.Use(LoggingMiddleware)    // Second - uses ID for logging
router.Use(AuthMiddleware)       // Third - may log auth errors with ID
```

### Context Keys

```go
type contextKey string

const (
    RequestIDKey contextKey = "request_id"
    LoggerKey    contextKey = "logger"
)
```

### Response Writer Wrapper

To ensure header is set even on panic:
```go
func (m *requestIDMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    requestID := getOrGenerateRequestID(r)
    w.Header().Set("X-Request-Id", requestID)  // Set before calling next
    // ... rest of middleware
}
```

---

## Risk Assessment

| Risk | Mitigation |
|------|------------|
| Performance impact of UUID generation | UUID v4 is fast (~100ns); negligible |
| Log storage increase | request_id is 36 chars; minimal impact |
| Client forgetting to display ID | Server always includes in header/body |
| ID collision | UUID v4 collision probability is negligible |

---

## Next Steps

1. Create data-model.md with RequestContext, ErrorResponse entities
2. Update plan.md to reflect implementation approach
3. Generate tasks.md via /speckit.tasks
