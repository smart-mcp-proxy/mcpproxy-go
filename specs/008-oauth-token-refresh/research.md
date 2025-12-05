# Research: OAuth Token Refresh Bug Fixes and Logging Improvements

**Feature**: 008-oauth-token-refresh
**Date**: 2025-12-04

## Bug Analysis

### Bug 1: Expired Token Not Refreshed on Reconnection

**Root Cause Analysis**:

The issue is in `internal/upstream/core/connection.go` at lines 990-999 and 1303-1310. The code checks `tokenManager.HasTokenStore(c.config.Name)` which checks the **in-memory** `TokenStoreManager`, but:

1. The `TokenStoreManager.stores` map only contains stores created during the current session
2. On reconnection or restart, a **new** `PersistentTokenStore` is created via `oauth.CreateOAuthConfig()`
3. The `HasTokenStore()` check returns `false` because it checks the wrong map
4. The persisted token exists in BBolt database but isn't being loaded into the OAuth transport

**Code Flow on Reconnection**:
```
1. Connection lost → reconnectWithBackoff()
2. trySSEOAuthStrategy() or tryHTTPOAuthStrategy() called
3. tokenManager.HasTokenStore(serverName) → false (checks in-memory map, not BBolt)
4. oauth.CreateOAuthConfig() creates new PersistentTokenStore
5. PersistentTokenStore.GetToken() loads token from BBolt
6. BUT: The token's ExpiresAt is adjusted by TokenRefreshGracePeriod
7. mcp-go sees token as "expired" and tries to refresh
8. Refresh fails because mcp-go doesn't properly invoke refresh flow
```

**Key Insight**: The `PersistentTokenStore` correctly loads tokens from BBolt (line 340-358 in config.go), but the in-memory `TokenStoreManager.HasTokenStore()` check is misleading and doesn't reflect the actual token availability.

**Decision**: Remove reliance on `TokenStoreManager.HasTokenStore()` for connection decisions. The `PersistentTokenStore` already handles token loading correctly. The issue is that when tokens need refresh, the mcp-go library's automatic refresh isn't being triggered properly.

**Rationale**: The current code architecture is correct - `PersistentTokenStore.GetToken()` returns tokens with adjusted expiration. The bug is that:
1. Log message is misleading (shows `has_existing_token_store: false`)
2. When token refresh is needed, the OAuth flow restarts from scratch instead of using refresh_token

**Alternatives Considered**:
- Pre-loading tokens into memory store: Rejected - creates duplicate state management
- Manual refresh endpoint: Rejected - mcp-go should handle this automatically

### Bug 2: OAuth State Race Condition

**Root Cause Analysis**:

The race condition occurs because multiple reconnection attempts can trigger concurrent OAuth flows. Looking at `connection.go`:

1. `markOAuthInProgress()` sets a flag
2. Multiple goroutines can call `trySSEOAuthStrategy()` / `tryHTTPOAuthStrategy()` simultaneously
3. Each clears the previous OAuth state with "clearing stale state and retrying"
4. Neither flow completes successfully

**Evidence from logs** (from bug doc):
```log
18:24:02.262 | ⚠️ OAuth is already in progress, clearing stale state and retrying
18:24:02.266 | ❌ MCP initialization failed after OAuth setup
18:24:02.309 | ⚠️ OAuth is already in progress, clearing stale state and retrying
```

**Decision**: Implement per-server OAuth flow coordination using a map of sync.Mutex or channels to ensure only one OAuth flow runs per server at a time. Subsequent attempts should wait for the active flow to complete.

**Rationale**: The existing `oauthMu` mutex in connection.go is per-connection, not per-server. Multiple connections to the same server can still trigger concurrent OAuth flows.

**Alternatives Considered**:
- Global OAuth mutex: Rejected - would serialize all OAuth flows unnecessarily
- Semaphore pattern: Rejected - more complex than needed for this use case

### Bug 3: Browser Rate Limiting Prevents OAuth Completion

**Root Cause Analysis**:

The browser rate limiter in `connection.go` prevents opening multiple browser windows in quick succession (5-minute window). Combined with Bug 2, this causes:

1. First OAuth flow opens browser
2. Race condition triggers second flow
3. Second flow rate-limited ("Browser opening rate limited, skipping")
4. First flow's state was cleared by second flow's attempt
5. Neither flow completes

**Decision**: Track OAuth flows per-server in the rate limiter rather than globally rate-limiting all browser opens. If an OAuth flow is already in progress for a server, don't rate-limit browser opens for that specific server.

**Rationale**: The rate limiter should prevent spam, but shouldn't prevent legitimate re-opens during an active OAuth flow for the same server.

**Alternatives Considered**:
- Disable rate limiting entirely: Rejected - would allow browser spam
- Longer rate limit window: Rejected - doesn't address the root cause

## Correlation ID Implementation

**Decision**: Use `github.com/google/uuid` to generate correlation IDs at OAuth flow start. Propagate via Go context using a custom key.

**Implementation Pattern**:
```go
// In internal/oauth/correlation.go
type contextKey string
const correlationIDKey contextKey = "oauth_correlation_id"

func NewCorrelationID() string {
    return uuid.New().String()
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, correlationIDKey, id)
}

func GetCorrelationID(ctx context.Context) string {
    if id, ok := ctx.Value(correlationIDKey).(string); ok {
        return id
    }
    return ""
}

// Logger wrapper that adds correlation_id field
func CorrelationLogger(ctx context.Context, logger *zap.Logger) *zap.Logger {
    if id := GetCorrelationID(ctx); id != "" {
        return logger.With(zap.String("correlation_id", id))
    }
    return logger
}
```

**Rationale**: Standard Go pattern for context-based propagation. Compatible with existing zap logging.

**Alternatives Considered**:
- Request-scoped logger: Rejected - requires more invasive changes
- Global correlation ID: Rejected - doesn't support concurrent flows

## Enhanced Debug Logging

**Decision**: Add structured fields to existing log calls for OAuth HTTP interactions:

1. **Request logging** (at debug level):
   - Method, URL, non-sensitive headers
   - Request timing (start time)
   - correlation_id

2. **Response logging** (at debug level):
   - Status code, response headers
   - Response timing (duration)
   - Body structure (not full body to avoid token leakage)
   - correlation_id

3. **Token metadata logging** (at info level):
   - Token type, expiration time, scope
   - Has refresh token (bool, not the actual token)
   - correlation_id

**Rationale**: Provides sufficient detail for debugging without exposing sensitive data.

**Security Considerations**:
- Never log actual access_token or refresh_token values
- Redact Authorization headers (show "Bearer ***" instead)
- Redact client_secret in request bodies

## mcp-go Library Token Refresh Behavior

**Research Finding**: The mcp-go library checks `token.IsExpired()` before making requests and should automatically refresh tokens if a refresh_token is available. However, the automatic refresh may not be working correctly.

**Decision**: Investigate mcp-go library's refresh implementation and potentially implement manual refresh as a fallback if library refresh fails.

**Key Files to Check**:
- `github.com/mark3labs/mcp-go/client/transport/oauth.go`
- Check how `RefreshToken` grant type is handled

**Fallback Strategy**:
If mcp-go library refresh fails:
1. Detect "token expired" error
2. Manually call token endpoint with refresh_token grant
3. Save new token to PersistentTokenStore
4. Retry original request

## Testing Strategy

**Decision**: Use OAuth test server with short TTL for comprehensive testing.

**Test Scenarios**:

1. **Token Refresh Test** (30s TTL):
   ```bash
   go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=30s
   ```
   - Wait for token expiration
   - Verify automatic refresh works
   - Check logs for correlation IDs

2. **Persisted Token Test**:
   - Authenticate, then restart mcpproxy
   - Verify token is loaded from BBolt
   - Verify refresh works after restart

3. **Race Condition Test**:
   - Trigger rapid reconnections
   - Verify single OAuth flow per server
   - Check for race condition warnings

4. **Error Injection Test**:
   ```bash
   go run ./tests/oauthserver/cmd/server -port 9000 -token-error=invalid_grant
   ```
   - Verify error handling and logging
   - Check correlation IDs trace through errors

5. **Playwright E2E Tests**:
   - Verify Web UI shows correct OAuth status
   - Test OAuth login flow via browser automation
   - Verify status updates in real-time

## Summary of Decisions

| Topic | Decision | Key File(s) |
|-------|----------|-------------|
| Token Refresh Bug | Fix refresh token flow in connection.go | internal/upstream/core/connection.go |
| Race Condition | Per-server OAuth flow coordination | internal/oauth/config.go |
| Browser Rate Limit | Per-server rate limiting | internal/upstream/core/connection.go |
| Correlation IDs | Context-based UUID propagation | internal/oauth/correlation.go (new) |
| Debug Logging | Structured zap fields with redaction | internal/oauth/logging.go (new) |
| Testing | OAuth test server with short TTL | tests/oauthserver/ |
