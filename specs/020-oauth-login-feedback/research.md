# Research: OAuth Login Error Feedback

**Feature**: 020-oauth-login-feedback
**Date**: 2026-01-06
**Status**: Complete

## Research Summary

This document captures design decisions for improving OAuth login feedback across all clients (CLI, tray, Web UI). The approach is non-blocking (async) with enhanced response payloads.

---

## Decision 1: Non-Blocking (Async) API

**Decision**: Keep `POST /api/v1/servers/{id}/login` as async (non-blocking)

**Rationale**:
- Current behavior returns immediately after triggering OAuth - this is correct
- OAuth completion happens asynchronously (user interacts with browser)
- Clients can monitor completion via existing `servers.changed` SSE events or polling `GET /servers`
- Simpler implementation - no need for new blocking/waiting logic

**Alternatives Considered**:
1. **Synchronous blocking endpoint**: Would require timeout handling, SSE subscription for progress - overly complex
2. **New polling endpoint**: Unnecessary - existing server status endpoints suffice
3. **WebSocket bidirectional**: Overkill for this use case

**Implementation**: Enhance existing endpoint response, not change its async nature.

---

## Decision 2: Browser Status in Response

**Decision**: Return `browser_opened` boolean and `browser_error` string in login response

**Rationale**:
- The core problem is clients don't know if the browser opened
- Without this info, clients can't decide whether to show the manual URL
- Simple boolean + error string covers all cases

**Response Fields**:
```json
{
  "browser_opened": true,   // Did xdg-open/open/rundll32 succeed?
  "browser_error": null     // Error message if browser_opened is false
}
```

**Browser Open Detection**:
- On success: `exec.Command().Start()` returns nil
- On failure: Capture error message (e.g., "xdg-open not found")
- On HEADLESS: Skip browser attempt, return `browser_opened: false`

**Alternatives Considered**:
1. **Only return URL on failure**: Would require daemon to determine "failure" - but browser may succeed yet user doesn't see it
2. **Always return URL**: Good, but still need browser status for UX decisions

---

## Decision 3: Auth URL in Response

**Decision**: Always return `auth_url` in successful OAuth start response

**Rationale**:
- Clients need the URL to display when browser fails
- URL is useful for debugging/logging regardless
- No downside to including it

**Security Note**: The auth_url contains OAuth state parameters - safe to expose to the same client that triggered the flow.

**Alternatives Considered**:
1. **Only return URL on browser failure**: Would require conditional logic; simpler to always include
2. **Return URL via separate endpoint**: Adds complexity for no benefit

---

## Decision 4: Correlation ID

**Decision**: Return `correlation_id` (UUID) for every OAuth start request

**Rationale**:
- Enables log correlation between client and daemon
- Useful for debugging "OAuth didn't work" issues
- Follows existing correlation patterns in the codebase

**Implementation**: Generate UUID in `StartManualOAuth`, include in response and all log messages.

**Alternatives Considered**:
1. **No correlation ID**: Makes debugging harder
2. **Use server name as correlation**: Not unique across multiple attempts

---

## Decision 5: Multi-Client Behavior Model

**Decision**: All clients (CLI, tray, Web UI) use the same endpoint and response format

**Rationale**:
- Consistent behavior across all interfaces
- Single place to maintain validation and response logic
- Clients only differ in UI presentation

**Shared Behaviors**:

| Behavior | All Clients |
|----------|-------------|
| Pre-flight validation | Same validation, same error responses |
| Browser status | Same `browser_opened` field |
| Auth URL | Same `auth_url` field |
| Correlation ID | Same `correlation_id` field |

**Client-Specific UX** (out of scope for daemon):

| Client | Browser Failed | Success |
|--------|----------------|---------|
| CLI | Print URL to stderr | Print success message |
| Tray | Show notification with URL | Show success notification |
| Web UI | Show URL in modal | Show success toast |

**Alternatives Considered**:
1. **Separate endpoints per client**: Inconsistent behavior, maintenance burden
2. **Client-type parameter**: Unnecessary - response is the same

---

## Decision 6: Validation Error Structure

**Decision**: Return structured validation errors with `error_type`, `message`, `suggestion`

**Rationale**:
- Clients need machine-readable error types for branching logic
- Human-readable messages for display
- Suggestions for actionable remediation

**Error Types**:
- `server_not_found` - Include `available_servers` list
- `oauth_not_supported` - Include reason (stdio protocol)
- `server_disabled` - Include enable command
- `server_quarantined` - Include approval instructions
- `flow_in_progress` - Include existing `correlation_id`

**Response Format**:
```json
{
  "success": false,
  "error_type": "server_not_found",
  "message": "Server 'google-drvie' not found",
  "suggestion": "Check spelling. Available servers: google-drive, github-server",
  "available_servers": ["google-drive", "github-server"]
}
```

**Alternatives Considered**:
1. **Single error string**: Not machine-readable
2. **Error codes (integers)**: Less descriptive than string types

---

## Decision 7: No New SSE Events

**Decision**: Do not add new OAuth-specific SSE event types

**Rationale**:
- Existing `servers.changed` event already fires when OAuth completes
- Clients can detect OAuth success by checking server's OAuth status after event
- Simpler implementation - no new event infrastructure

**How Clients Detect Completion**:
1. Subscribe to `/events` SSE stream
2. Watch for `servers.changed` event with matching server name
3. Fetch server details to check OAuth status

**Alternatives Considered**:
1. **New oauth.flow.* events**: Adds complexity, requires new event handling in all clients
2. **Polling endpoint**: Works but less efficient than existing SSE

---

## Technical Dependencies

| Dependency | Purpose | Version |
|------------|---------|---------|
| Cobra | CLI framework | Existing |
| Chi | HTTP router | Existing |
| Zap | Structured logging | Existing |

No new external dependencies required.

---

## Implementation Notes

### Browser Opening Status Detection

```go
// In openBrowser function:
func (c *Client) openBrowser(authURL string) (bool, error) {
    if os.Getenv("HEADLESS") == "true" {
        return false, nil  // Don't attempt browser in headless mode
    }

    cmd := exec.Command(browserCmd, args...)
    err := cmd.Start()
    if err != nil {
        return false, err  // Browser failed to open
    }
    return true, nil  // Browser opened (process started)
}
```

### Response Structure

```go
type OAuthStartResponse struct {
    Success       bool   `json:"success"`
    ServerName    string `json:"server_name"`
    CorrelationID string `json:"correlation_id"`
    AuthURL       string `json:"auth_url,omitempty"`
    BrowserOpened bool   `json:"browser_opened"`
    BrowserError  string `json:"browser_error,omitempty"`
    Message       string `json:"message"`
}
```

---

## Risk Assessment

| Risk | Mitigation |
|------|------------|
| Browser reports success but doesn't appear | Include auth_url for all cases; user can always use manual URL |
| Clients ignore browser_opened field | Document expected client behavior; CLI will demonstrate pattern |
| Race condition on concurrent logins | Return existing correlation_id if flow in progress |

---

## Next Steps

1. Update data-model.md with OAuthStartResponse and OAuthValidationError
2. Update oauth-api.yaml with response schema
3. Update quickstart.md with async examples
4. Update plan.md to reflect simplified scope
