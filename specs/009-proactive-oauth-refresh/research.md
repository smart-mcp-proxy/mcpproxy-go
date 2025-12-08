# Research: Proactive OAuth Token Refresh & UX Improvements

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)
**Date**: 2025-12-07

## Summary

This document consolidates research findings for implementing proactive OAuth token refresh, CLI/REST logout functionality, and Web UI improvements. All technical decisions have been validated against the existing codebase patterns.

---

## Decision 1: Token Refresh Strategy

### Decision
Implement **proactive (scheduled) token refresh** at 80% of token lifetime with per-server coordination.

### Rationale
1. **Industry standard**: Major API gateways (Kong, Azure APIM, Envoy) use proactive refresh at 80-90% lifetime
2. **Race condition prevention**: Lazy refresh with single-use refresh tokens causes "refresh token already used" errors when concurrent requests race
3. **User experience**: Prevents tool call failures with "authorization required" errors when tokens expire during operations
4. **Existing pattern**: `internal/oauth/coordinator.go` already provides per-server mutex coordination for OAuth flows

### Alternatives Considered
| Alternative | Why Rejected |
|------------|--------------|
| Lazy refresh (current) | Causes tool call failures; race conditions with concurrent requests |
| Event-driven refresh | Over-complex; requires tracking all token usages |
| Fixed-interval refresh | Wastes resources on long-lived tokens; may miss short-lived ones |

### Implementation Pattern
```go
// RefreshManager coordinates background token refresh
type RefreshManager struct {
    storage    *storage.BoltDB
    coordinator *OAuthFlowCoordinator  // Reuse existing per-server mutex
    timers     map[string]*time.Timer  // Per-server scheduled refresh
    mu         sync.RWMutex
}

// Schedule refresh at 80% of token lifetime
func (m *RefreshManager) scheduleRefresh(serverName string, expiresAt time.Time) {
    lifetime := time.Until(expiresAt)
    refreshAt := time.Duration(float64(lifetime) * 0.8)
    // ...
}
```

---

## Decision 2: Refresh Coordination with Existing OAuth Flow

### Decision
Integrate refresh manager with existing `OAuthFlowCoordinator` to prevent race conditions between proactive refresh and manual login.

### Rationale
1. **Existing infrastructure**: `coordinator.go` provides `StartFlow()`, `EndFlow()`, `IsFlowActive()`, `WaitForFlow()` - all needed for coordination
2. **No duplication**: Reuse per-server mutexes from `flowLocks map[string]*sync.Mutex`
3. **Clean handoff**: Manual login can cancel scheduled refresh; refresh can detect active login and wait

### Key Interactions
```
Manual Login Triggered → Cancel pending scheduled refresh → Use existing flow
Proactive Refresh Triggered → Check IsFlowActive() → If active, WaitForFlow()
Refresh Succeeds → Update timer for new expiration
Refresh Fails (3 retries) → Emit oauth.refresh_failed → Show Login button
```

---

## Decision 3: Logout Implementation Approach

### Decision
Add `TriggerOAuthLogout()` method to `ManagementService` interface following the existing `TriggerOAuthLogin()` pattern.

### Rationale
1. **Consistency**: Mirrors existing `TriggerOAuthLogin()` signature and behavior
2. **DDD layering**: Management service → Runtime → PersistentTokenStore.ClearToken()
3. **Configuration gates**: Respects `disable_management` and `read_only` gates
4. **Event emission**: Emits `servers.changed` after logout completes

### Interface Extension
```go
// In internal/management/service.go
type Service interface {
    // ... existing methods ...

    // TriggerOAuthLogout clears OAuth token and disconnects a specific server.
    // Respects disable_management and read_only configuration gates.
    // Emits "servers.changed" event on successful logout.
    TriggerOAuthLogout(ctx context.Context, name string) error
}

type RuntimeOperations interface {
    // ... existing methods ...
    TriggerOAuthLogout(serverName string) error
}
```

---

## Decision 4: REST API Endpoint Design

### Decision
Add `POST /api/v1/servers/{id}/logout` endpoint following existing action endpoint patterns.

### Rationale
1. **Consistency**: Mirrors existing `/login`, `/enable`, `/restart` endpoints
2. **HTTP semantics**: POST for state-changing operation (not DELETE - token is not a resource)
3. **Response format**: Returns `{"action": "logout", "success": true}` like other action endpoints

### Existing Pattern Reference
```go
// From internal/httpapi/server.go - /login endpoint
r.Post("/api/v1/servers/{id}/login", s.handleServerLogin)

// Response format from existing endpoints
s.writeSuccess(w, map[string]interface{}{
    "action": "login",
    "server": serverID,
})
```

---

## Decision 5: CLI Command Structure

### Decision
Add `mcpproxy auth logout --server=<name>` command with `--all` flag support.

### Rationale
1. **Consistency**: Parallels existing `mcpproxy auth login --server=<name>` command
2. **Daemon support**: Uses existing socket communication pattern from `auth_cmd.go`
3. **Standalone mode**: Works without daemon by directly calling storage methods

### Existing Pattern Reference
```go
// From cmd/mcpproxy/auth_cmd.go - login command
authLoginCmd := &cobra.Command{
    Use:   "login",
    Short: "Authenticate with an OAuth-enabled MCP server",
    // ...
}

// Daemon socket communication
if err := client.TriggerOAuthLogin(ctx, serverName); err != nil {
    // handle error
}
```

---

## Decision 6: Web UI Login Button Visibility Fix

### Decision
Modify `ServerCard.vue` to show Login button when server has expired OAuth token, regardless of connection status.

### Rationale
1. **Root cause**: Current condition `notConnected && needsOAuth` hides button for connected servers with expired tokens
2. **User scenario**: Server shows "Connected" but tool calls fail with auth errors - user needs Login button
3. **Minimal change**: Add computed property `needsReauthentication` that checks `oauth_status === 'expired'`

### Existing Code Reference
```vue
<!-- From frontend/src/components/ServerCard.vue -->
<button v-if="needsOAuth && notConnected" @click="handleLogin">
  Login
</button>

<!-- Fix: Show Login when authenticated but token expired -->
<button v-if="needsOAuth && (notConnected || oauthExpired)" @click="handleLogin">
  Login
</button>
```

---

## Decision 7: SSE Event Types for Token Refresh

### Decision
Add new SSE event types `oauth.token_refreshed` and `oauth.refresh_failed` following existing event patterns.

### Rationale
1. **Real-time updates**: Web UI needs to update auth status badge immediately after refresh
2. **Existing pattern**: `servers.changed` and `config.reloaded` events already in use
3. **Event payload**: Include `server_name`, `expires_at`, and `error` fields

### Event Structure
```go
// From internal/runtime/events.go pattern
const (
    EventTypeOAuthTokenRefreshed EventType = "oauth.token_refreshed"
    EventTypeOAuthRefreshFailed  EventType = "oauth.refresh_failed"
)

// Event payload
type OAuthRefreshEvent struct {
    ServerName string    `json:"server_name"`
    ExpiresAt  time.Time `json:"expires_at,omitempty"` // Only for success
    Error      string    `json:"error,omitempty"`      // Only for failure
}
```

---

## Decision 8: Token Expiration Storage Schema

### Decision
Use existing `OAuthTokenRecord.ExpiresAt` field for refresh scheduling - no schema changes needed.

### Rationale
1. **Existing field**: `internal/storage/models.go` already has `ExpiresAt time.Time` in `OAuthTokenRecord`
2. **ListOAuthTokens**: `internal/storage/bbolt.go:ListOAuthTokens()` returns all tokens for refresh manager initialization
3. **No migration**: No BBolt schema migration required

### Existing Schema Reference
```go
// From internal/storage/models.go
type OAuthTokenRecord struct {
    ServerName   string    `json:"server_name"`
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    TokenType    string    `json:"token_type"`
    ExpiresAt    time.Time `json:"expires_at"`  // Used for refresh scheduling
    Scopes       []string  `json:"scopes,omitempty"`
    Created      time.Time `json:"created"`
    Updated      time.Time `json:"updated"`
    // ...
}
```

---

## Decision 9: OAuth Status Display in Server Response

### Decision
Add `oauth_status` and `token_expires_at` fields to server status response.

### Rationale
1. **Web UI needs**: Display accurate auth status badge ("Authenticated", "Token Expired", "Auth Error")
2. **CLI needs**: Show human-readable expiration in `auth status` command
3. **Computed at runtime**: Calculate from `OAuthTokenRecord` in `ListServers()` response

### Implementation Location
```go
// In internal/contracts/server.go or similar
type Server struct {
    // ... existing fields ...
    OAuthStatus    string     `json:"oauth_status,omitempty"`     // "authenticated", "expired", "error", "none"
    TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"` // ISO 8601 when authenticated
}
```

---

## Decision 10: Retry Strategy for Failed Refresh

### Decision
Implement exponential backoff with 3 retries (1s, 2s, 4s) before falling back to browser re-authentication.

### Rationale
1. **Transient errors**: Network timeouts, rate limits may resolve quickly
2. **Industry practice**: 3 retries with exponential backoff is standard for OAuth
3. **Existing pattern**: `internal/upstream/core/connection.go:1043-1057` has similar retry logic

### Existing Pattern Reference
```go
// From internal/upstream/core/connection.go
const maxTokenRefreshRetries = 3

for attempt := 1; attempt <= maxTokenRefreshRetries; attempt++ {
    // Exponential backoff: 1s, 2s, 4s
    delay := time.Duration(1<<uint(attempt-1)) * time.Second
    // ...
}
```

---

## Codebase References

### Existing OAuth Infrastructure
- `internal/oauth/persistent_token_store.go` - Token storage with grace period
- `internal/oauth/coordinator.go` - Per-server OAuth flow coordination
- `internal/oauth/logging.go` - Token metadata logging
- `internal/storage/models.go` - `OAuthTokenRecord` schema

### Management Service Pattern
- `internal/management/service.go` - Service interface with `TriggerOAuthLogin()`
- `internal/management/service.go:666` - `TriggerOAuthLogin()` implementation

### HTTP API Pattern
- `internal/httpapi/server.go:1063` - `/login` endpoint handler
- `internal/httpapi/server.go` - Swagger annotations for OpenAPI

### CLI Command Pattern
- `cmd/mcpproxy/auth_cmd.go` - Existing `auth login` and `auth status` commands
- `internal/cliclient/client.go:614` - `TriggerOAuthLogin()` client method

### Event System
- `internal/runtime/events.go` - Event type definitions
- `internal/runtime/event_bus.go` - Event broadcasting

### Web UI
- `frontend/src/components/ServerCard.vue` - Server card with Login button
- `frontend/src/services/api.ts` - API client
- `frontend/src/stores/servers.ts` - Server state management

---

## Testing Considerations

### OAuth Test Server Configuration
The existing OAuth test server from spec 007 supports configurable token lifetimes:
- Location: `tests/oauthserver/`
- Configure short lifetime (30-60s) via environment variables for refresh testing

### E2E Test Strategy
1. Configure OAuth test server with 30-second token lifetime
2. Start mcpproxy with OAuth server configured
3. Verify refresh happens at ~24 seconds (80% of 30s)
4. Verify SSE event emitted
5. Verify tool calls succeed after refresh

---

## Constitution Compliance

All decisions verified against `.specify/memory/constitution.md`:

| Principle | Compliance |
|-----------|------------|
| I. Performance at Scale | ✅ Background refresh doesn't block API requests |
| II. Actor-Based Concurrency | ✅ Reuses existing OAuthFlowCoordinator |
| III. Configuration-Driven | ✅ Refresh threshold can be configurable |
| IV. Security by Default | ✅ Tokens stored in BBolt, cleared on logout |
| V. TDD | ✅ Unit tests specified for all new components |
| VI. Documentation Hygiene | ✅ CLAUDE.md update for new CLI command |
