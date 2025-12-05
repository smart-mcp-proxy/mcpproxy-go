# OAuth Token Management Bugs in mcpproxy

This document describes OAuth token management bugs discovered during OAuth E2E testing and their fixes.

## Bug 1: Expired Token Not Refreshed on Reconnection ✅ FIXED

### Status: Fixed in spec 008-oauth-token-refresh

### Original Symptoms

After a successful OAuth login, if the access token expires and mcpproxy attempts to reconnect, the server remains in "disconnected" status with error:

```
failed to connect: all authentication strategies failed, last error: OAuth authorization required - deferred for background processing
```

### Root Cause

When mcpproxy reconnects to an OAuth-protected server:

1. It detects there's a persisted token (with `token_expires_at` in the past)
2. It creates a **NEW** OAuth config with an empty token store
3. The existing persisted token is NOT loaded into the OAuth transport
4. When MCP initialize is called, the transport has no token available
5. The request fails with "no valid token available, authorization required"

### Fix Applied

1. **Added `HasPersistedToken()` function** in `internal/oauth/config.go`:
   - Checks BBolt storage for persisted tokens
   - Returns token status including `hasRefreshToken` and `isExpired` flags

2. **Added token refresh retry logic** in `internal/upstream/core/connection.go`:
   - Before triggering browser OAuth, checks if persisted token has refresh_token
   - Retries token refresh with exponential backoff (up to 3 attempts)
   - Only falls back to browser OAuth if refresh fails

3. **Enhanced logging** to distinguish between token scenarios:
   - Logs both in-memory and persisted token status
   - Logs refresh attempts and outcomes

### Verification

Run OAuth E2E tests with short token TTL:
```bash
go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=30s
# Token should auto-refresh without browser re-authentication
```

---

## Bug 2: OAuth State Race Condition ✅ FIXED

### Status: Fixed in spec 008-oauth-token-refresh

### Original Symptoms

Multiple OAuth flows may run concurrently, causing state corruption:

```log
WARN | ⚠️ OAuth is already in progress, clearing stale state and retrying
INFO | 🧹 Clearing OAuth state | {"was_in_progress": true, "was_completed": false}
```

### Root Cause

The OAuth state machine doesn't properly coordinate multiple reconnection attempts:

1. Background reconnection triggers OAuth flow
2. Before flow completes, another reconnection attempt starts
3. Second attempt clears the first flow's state
4. Neither flow completes successfully

### Fix Applied

1. **Implemented `OAuthFlowCoordinator`** in `internal/oauth/coordinator.go`:
   - Per-server mutex coordination prevents concurrent flows
   - `StartFlow()` returns `ErrFlowInProgress` if flow already active
   - `WaitForFlow()` allows goroutines to wait for completion
   - `EndFlow()` notifies all waiters of completion

2. **Integrated coordinator into OAuth strategies**:
   - `tryOAuthAuth()` and `trySSEOAuthAuth()` now use coordinator
   - Second goroutine waits for first flow instead of starting new one
   - Proper cleanup via defer to handle both success and failure

3. **Added correlation IDs** for flow traceability:
   - Each OAuth flow gets a unique UUID
   - All related logs include the correlation ID
   - Makes debugging concurrent flows much easier

### Verification

Trigger rapid reconnections and verify single OAuth flow:
```bash
# Multiple reconnection attempts should result in single OAuth flow
# Second goroutine should log "waiting for OAuth flow to complete"
```

---

## Bug 3: Browser Rate Limiting Prevents OAuth Completion ✅ FIXED

### Status: Fixed in spec 008-oauth-token-refresh

### Original Symptoms

Browser opening is rate-limited, preventing OAuth flow from completing:

```log
WARN | Browser opening rate limited, skipping
```

### Root Cause

The browser rate limiter prevents opening multiple browser windows in quick succession. However, when combined with Bug 2 (race condition), this can prevent any OAuth flow from completing.

### Fix Applied

1. **Browser rate limiting is already per-server**:
   - Each `Client` instance has its own `lastOAuthTimestamp`
   - Rate limiting applies per-server, not globally

2. **Combined with Bug 2 fix**:
   - With flow coordinator preventing concurrent flows, rate limiting is less problematic
   - Only one flow runs at a time per server, so rate limiting works correctly

3. **Manual OAuth flows bypass rate limiting**:
   - `mcpproxy auth login` sets context flag to bypass rate limit
   - User-initiated flows always open browser

### Verification

Browser should open for each unique server's OAuth flow without interference.

---

## Summary of Changes

### New Files Created

- `internal/oauth/coordinator.go` - OAuth flow coordinator for race condition prevention
- `internal/oauth/correlation.go` - Correlation ID generation and context propagation
- `internal/oauth/logging.go` - Enhanced OAuth logging with token redaction

### Modified Files

- `internal/upstream/core/connection.go` - Token refresh retry, flow coordinator integration
- `internal/oauth/config.go` - `HasPersistedToken()`, `GetPersistedRefreshToken()`
- `internal/oauth/persistent_token_store.go` - Enhanced token metadata logging
- `internal/oauth/discovery.go` - Consistent request/response logging

### Testing

All fixes are covered by unit tests:
- `internal/oauth/coordinator_test.go`
- `internal/oauth/correlation_test.go`
- `internal/oauth/logging_test.go`

Run verification:
```bash
# Unit tests
go test -race ./internal/oauth/... -v

# E2E tests
./scripts/run-oauth-e2e.sh
```

## Related Files

- `internal/upstream/core/connection.go` - OAuth authentication logic
- `internal/upstream/managed/client.go` - Managed client reconnection
- `internal/oauth/` - OAuth configuration and flow management
- `tests/oauthserver/` - OAuth test server for reproduction
- `specs/008-oauth-token-refresh/` - Feature specification
