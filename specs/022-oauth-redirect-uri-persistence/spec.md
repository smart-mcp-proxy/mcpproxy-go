# Spec 022: OAuth Redirect URI Port Persistence

## Status
Implemented

## Problem Statement

When OAuth tokens expire and users attempt to re-authenticate, the OAuth flow fails with "Invalid redirect URI" errors from OAuth servers (e.g., Sentry, Cloudflare). This occurs because:

1. **Dynamic Client Registration (DCR)** registers a client with a specific `redirect_uri` including a dynamically allocated port (e.g., `http://127.0.0.1:60156/oauth/callback`)
2. When tokens expire and re-authentication is needed, mcpproxy allocates a **new dynamic port** (e.g., `http://127.0.0.1:60289/oauth/callback`)
3. OAuth servers reject the request because the `redirect_uri` doesn't match what was registered during DCR

### Affected Servers
- Cloudflare MCP servers (autorag-cf, bindings-cloudflare, cloudflare-logs)
- Sentry MCP server
- Any OAuth server using DCR with strict redirect URI validation

### User Impact
- Users cannot re-authenticate after token expiration
- Error messages like "Invalid redirect URI" or "Internal Error" from OAuth servers
- Requires manual `auth logout` + `auth login` to clear DCR and re-register

## Root Cause Analysis

In `internal/oauth/config.go`, the callback server always allocates a fresh port:

```go
// Line 738-741
addr := listener.Addr().(*net.TCPAddr)
port := addr.Port
redirectURI := fmt.Sprintf("%s:%d%s", DefaultRedirectURIBase, port, DefaultRedirectPath)
```

The port used during DCR is not stored or reused for subsequent authentications.

## Proposed Solution

### Option A: Port Persistence (Recommended)

Store the callback port used during successful DCR and attempt to reuse it:

1. **Store port with DCR credentials**: When DCR succeeds, persist the port alongside `client_id` and `client_secret`
2. **Attempt port reuse**: When starting OAuth flow, try to bind to the stored port first
3. **Fallback with re-registration**: If stored port unavailable, clear DCR credentials and perform fresh DCR with new port

### Option B: Fixed Port Range

Configure a fixed port or small range per server:

1. Hash server name to determine port (e.g., `50000 + hash(serverName) % 1000`)
2. Deterministic port allocation ensures same port across restarts
3. Risk: Port conflicts if multiple servers hash to same port

### Option C: Re-DCR on Port Change

Detect redirect_uri mismatch and automatically re-register:

1. Before OAuth flow, check if stored redirect_uri matches current callback server port
2. If mismatch, clear DCR credentials and force fresh registration
3. Simpler than port persistence but requires extra DCR call

## Recommended Implementation (Option A)

### Data Model Changes

Extend `OAuthClientCredentials` in storage:

```go
type OAuthClientCredentials struct {
    ClientID     string `json:"client_id"`
    ClientSecret string `json:"client_secret"`
    RedirectURI  string `json:"redirect_uri"`   // NEW: Store the registered redirect URI
    CallbackPort int    `json:"callback_port"`  // NEW: Store the port for reuse
    RegisteredAt time.Time `json:"registered_at"`
}
```

### Code Changes

#### 1. Update `StartCallbackServer` in `internal/oauth/config.go`

```go
func (m *CallbackServerManager) StartCallbackServer(serverName string, preferredPort int) (*CallbackServer, error) {
    // Try preferred port first if specified
    if preferredPort > 0 {
        listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
        if err == nil {
            // Success - use preferred port
            return m.createCallbackServer(serverName, listener)
        }
        m.logger.Warn("Preferred port unavailable, using dynamic allocation",
            zap.String("server", serverName),
            zap.Int("preferred_port", preferredPort),
            zap.Error(err))
    }

    // Fall back to dynamic port allocation
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return nil, err
    }
    return m.createCallbackServer(serverName, listener)
}
```

#### 2. Update `CreateOAuthConfigWithExtraParams`

```go
func CreateOAuthConfigWithExtraParams(...) (*client.OAuthConfig, map[string]string) {
    // Check for stored callback port from previous DCR
    var preferredPort int
    if storage != nil {
        creds, _ := storage.GetOAuthClientCredentials(serverKey)
        if creds != nil && creds.CallbackPort > 0 {
            preferredPort = creds.CallbackPort
        }
    }

    // Start callback server with preferred port
    callbackServer, err := globalCallbackManager.StartCallbackServer(serverConfig.Name, preferredPort)
    // ...
}
```

#### 3. Update DCR credential storage

After successful DCR, store the port:

```go
if c.storage != nil && clientID != "" {
    serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
    _ = c.storage.UpdateOAuthClientCredentials(serverKey, clientID, clientSecret, callbackServer.Port)
}
```

#### 4. Handle port conflict with re-DCR

If preferred port unavailable AND we have stored DCR credentials, clear them to force fresh DCR:

```go
if preferredPort > 0 && actualPort != preferredPort {
    // Port changed - DCR credentials are now invalid
    logger.Warn("Callback port changed, clearing DCR credentials for re-registration",
        zap.String("server", serverName),
        zap.Int("stored_port", preferredPort),
        zap.Int("new_port", actualPort))
    storage.ClearOAuthClientCredentials(serverKey)
}
```

## Testing

### Unit Tests
- Test port reuse when stored port available
- Test fallback to dynamic port when stored port busy
- Test DCR credential clearing on port change

### E2E Tests
1. Fresh DCR flow - verify port stored
2. Re-auth with same port - verify success
3. Re-auth with port conflict - verify re-DCR triggered
4. Multiple servers - verify independent port management

### Manual Testing
```bash
# Test 1: Fresh authentication
mcpproxy auth logout --server=sentry
mcpproxy auth login --server=sentry
# Verify: Should succeed, port stored

# Test 2: Re-authentication (simulate token expiry)
# Wait for token to expire or manually clear token
mcpproxy auth login --server=sentry
# Verify: Should reuse same port and succeed

# Test 3: Port conflict
# Start another process on the stored port
mcpproxy auth login --server=sentry
# Verify: Should detect conflict, re-DCR, and succeed with new port
```

## Migration

No migration needed - new fields are optional and backward compatible:
- Existing credentials without `callback_port` will use dynamic allocation
- Next successful DCR will populate the new fields

## Security Considerations

- Port persistence is local-only (no sensitive data exposed)
- Re-DCR on port change maintains security guarantees
- No changes to token storage or transmission

## Acceptance Criteria

1. Re-authentication after token expiry works without "Invalid redirect URI" errors
2. Stored callback port is reused when available
3. Port conflicts trigger automatic re-DCR
4. No manual intervention required for normal token refresh flows
5. Backward compatible with existing stored credentials

## References

- OAuth 2.0 Dynamic Client Registration (RFC 7591)
- OAuth 2.0 for Native Apps (RFC 8252) - Loopback redirect considerations
- Related: Spec 020 (OAuth Login Feedback)
