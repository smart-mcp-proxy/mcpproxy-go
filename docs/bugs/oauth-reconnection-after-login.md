# Bug: Server doesn't connect immediately after OAuth login

## Summary
After successful OAuth authentication via browser flow, the server remains in "Disconnected" status instead of automatically connecting. Users must manually restart the server to establish a connection.

## Expected Behavior
After OAuth login completes successfully:
1. Token is saved to persistent storage
2. Server automatically attempts connection using the new token
3. Server status updates to "Connected" in UI

## Actual Behavior
After OAuth login completes:
1. Token is saved to persistent storage ✅
2. `RetryConnection` is called but fails with "OAuth authentication required"
3. Server remains "Disconnected" despite having a valid token

## Root Cause Analysis

The issue is in `internal/upstream/core/connection.go` lines 1028-1033:

```go
// Check if we have a recently completed OAuth flow that should have tokens
if oauth.GetTokenStoreManager().HasRecentOAuthCompletion(c.serverName) {
    c.logger.Info("OAuth flow recently completed, tokens should be available",
        zap.String("server", c.serverName))
    // Skip the browser flow since tokens should be available
}
```

**Problem**: The code logs "Skip the browser flow since tokens should be available" but doesn't actually change the control flow. The browser OAuth flow is still triggered, which fails because OAuth is already complete.

### Why this happens:

1. **Managed client calls `RetryConnection`** (`internal/upstream/manager.go:1326-1355`)
2. **Core client's `Connect()` is called** which goes through `connectWithOAuth()`
3. **`connectWithOAuth()` checks `HasRecentOAuthCompletion()`** but only logs - doesn't skip OAuth
4. **OAuth flow coordinator returns "flow already active" or times out**
5. **Connection fails** even though valid token exists in storage

### The architectural issue:

The core client (`internal/upstream/core/`) doesn't directly read tokens from the persistent token store during connection. Instead:
- Tokens are managed by `PersistentTokenStore` in `internal/oauth/persistent_token_store.go`
- Core client relies on OAuth flow to provide tokens
- When OAuth flow is skipped/already complete, no mechanism exists to retrieve existing tokens

## Proposed Fix

Option A: **Skip OAuth flow when recent completion detected**
```go
if oauth.GetTokenStoreManager().HasRecentOAuthCompletion(c.serverName) {
    c.logger.Info("OAuth flow recently completed, using existing token")
    // Actually skip the OAuth flow - just attempt connection with existing token
    return c.connectWithExistingToken(ctx)
}
```

Option B: **Add token retrieval to core client**
- Add method to retrieve token from persistent store
- Use token directly in HTTP transport without triggering OAuth flow

Option C: **Fix RetryConnection to reinitialize transport with token**
- When `RetryConnection` is called after OAuth completion
- Reinitialize the HTTP transport with the token from storage
- Skip the full OAuth flow

## Affected Files
- `internal/upstream/core/connection.go` - Main fix location
- `internal/upstream/core/client.go` - May need token retrieval method
- `internal/upstream/manager.go` - RetryConnection logic

## Reproduction Steps
1. Configure an OAuth-enabled server (e.g., cloudflare-logs)
2. Start mcpproxy - server shows as "Disconnected"
3. Click "Login" button or run `mcpproxy auth login --server=<name>`
4. Complete OAuth flow in browser
5. Observe: Server still shows "Disconnected" despite successful login
6. Click "Restart" to manually connect

## Impact
- User experience degradation - requires manual intervention after OAuth
- Confusion about whether OAuth succeeded
- Extra clicks/commands to get server connected

## Priority
Medium - Workaround exists (manual restart), but UX is poor

## Related
- Feature 009: Proactive OAuth Token Refresh (this bug was discovered during testing)
- PR #177: fix: clear OAuth error on successful reconnection
- PR #178: fix: make Login button consistent with other server action buttons
