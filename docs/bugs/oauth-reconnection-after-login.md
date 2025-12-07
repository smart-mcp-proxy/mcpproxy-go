# Bug: Server doesn't connect immediately after OAuth login

## Status: FIXED

**Fixed in:** PR #181 (fix/oauth-reconnection-after-login branch)
**Fix Date:** 2025-12-07

## Summary
After successful OAuth authentication via browser flow, the server remains in "Disconnected" status instead of automatically connecting. Users must manually restart the server to establish a connection.

## Expected Behavior
After OAuth login completes successfully:
1. Token is saved to persistent storage
2. Server automatically attempts connection using the new token
3. Server status updates to "Connected" in UI

## Actual Behavior (BEFORE FIX)
After OAuth login completes:
1. Token is saved to persistent storage ✅
2. `RetryConnection` is called but fails with "OAuth authentication required"
3. Server remains "Disconnected" despite having a valid token

## Root Cause Analysis

The issue was in `internal/upstream/core/connection.go`:

1. **Cross-process OAuth issue**: When CLI completed OAuth, the daemon's `HasRecentOAuthCompletion()` check returned false because it only checks in-memory state (per-process)
2. **Missing token pre-check**: The code didn't directly check if a valid persisted token existed before triggering browser OAuth flow
3. **No flow skip logic**: Even when a valid token was detected, the code only logged the finding but didn't actually skip the browser OAuth flow

### Why this happened:

1. **Managed client calls `RetryConnection`** (`internal/upstream/manager.go`)
2. **Core client's `Connect()` is called** which goes through `tryOAuthAuth()`
3. **`tryOAuthAuth()` checks `HasRecentOAuthCompletion()`** - but this is per-process, so cross-process OAuth (CLI → daemon) wasn't detected
4. **OAuth flow coordinator triggers browser flow** even though valid token exists in storage
5. **Connection fails** unnecessarily

## The Fix

The fix adds three key improvements to `internal/upstream/core/connection.go`:

### 1. Direct Persisted Token Check (lines ~1032-1045)
```go
// First check for valid persisted token directly (handles cross-process OAuth)
hasTokenPrecheck, hasRefreshPrecheck, tokenExpiredPrecheck := oauth.HasPersistedToken(c.config.Name, c.config.URL, c.storage)
if hasTokenPrecheck && !tokenExpiredPrecheck {
    logger.Info("🔄 Valid OAuth token found in persistent storage - will skip browser flow if OAuth error occurs",
        zap.String("server", c.config.Name),
        zap.Bool("has_refresh_token", hasRefreshPrecheck))
    skipBrowserFlow = true
} else if tokenManager.HasRecentOAuthCompletion(c.config.Name) {
    // Also check in-memory completion flag (same-process OAuth)
    ...
}
```

### 2. Skip Browser Flow When Token Exists (lines ~1217-1230)
```go
if client.IsOAuthAuthorizationRequiredError(lastErr) {
    // CRITICAL FIX: If we have a valid persisted token (e.g., from CLI OAuth),
    // skip browser flow and return retriable error. The token exists but
    // the mcp-go client needs to pick it up on retry.
    if skipBrowserFlow {
        c.logger.Info("🔄 OAuth authorization error but valid token exists - skipping browser flow",
            zap.String("server", c.config.Name),
            zap.String("tip", "Token should be used on next connection attempt"))

        c.clearOAuthState()
        return fmt.Errorf("OAuth token exists in storage, retry connection to use it: %w", lastErr)
    }
    // ... proceed with browser flow only if no valid token exists
}
```

### 3. Same Fix Applied to SSE OAuth Flow (lines ~1465-1683)
The same `skipBrowserFlow` logic was applied to the SSE OAuth flow to handle SSE-based OAuth servers.

## Affected Files
- `internal/upstream/core/connection.go` - Main fix location
  - HTTP OAuth flow: lines ~1026-1350
  - SSE OAuth flow: lines ~1465-1710

## Test Results
- ✅ Core unit tests pass
- ✅ OAuth unit tests pass
- ✅ OAuth E2E tests pass (29 tests)
- ✅ API E2E tests pass (except 1 unrelated test)

## Reproduction Steps (BEFORE FIX)
1. Configure an OAuth-enabled server (e.g., cloudflare-logs)
2. Start mcpproxy - server shows as "Disconnected"
3. Click "Login" button or run `mcpproxy auth login --server=<name>`
4. Complete OAuth flow in browser
5. Observe: Server still shows "Disconnected" despite successful login
6. Click "Restart" to manually connect

## Expected Behavior (AFTER FIX)
1. Configure an OAuth-enabled server
2. Start mcpproxy - server shows as "Disconnected"
3. Complete OAuth login via CLI or Web UI
4. Server automatically retries connection with existing token
5. Server status updates to "Connected" (or returns retriable error for managed client to retry)

## Impact
- ✅ Better user experience - no manual intervention needed after OAuth
- ✅ Clear feedback about OAuth status
- ✅ Reduced confusion about whether OAuth succeeded

## Related
- Feature 009: Proactive OAuth Token Refresh (this bug was discovered during testing)
- PR #177: fix: clear OAuth error on successful reconnection
- PR #178: fix: make Login button consistent with other server action buttons
