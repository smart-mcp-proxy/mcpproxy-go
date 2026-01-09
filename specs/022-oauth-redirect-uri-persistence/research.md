# Research: OAuth Redirect URI Port Persistence

## 1. OAuth Credentials Storage

### Decision: Extend OAuthTokenRecord with CallbackPort and RedirectURI fields

**Rationale**: The existing `OAuthTokenRecord` struct already stores DCR credentials (`ClientID`, `ClientSecret`). Adding `CallbackPort` and `RedirectURI` fields is the cleanest extension.

**Current Structure** (`internal/storage/models.go:64-79`):
```go
type OAuthTokenRecord struct {
    ServerName    string    // Storage key (serverName_hash format)
    DisplayName   string    // Actual server name
    AccessToken   string
    RefreshToken  string
    TokenType     string
    ExpiresAt     time.Time
    Scopes        []string
    Created       time.Time
    Updated       time.Time
    ClientID      string    // DCR-obtained client ID
    ClientSecret  string    // DCR-obtained client secret
}
```

**Proposed Extension**:
```go
    CallbackPort  int       // NEW: Port used during DCR for redirect_uri
    RedirectURI   string    // NEW: Full redirect URI registered with DCR
```

**Alternatives Considered**:
- Separate storage bucket for port persistence: Rejected - adds complexity without benefit
- Store in config file: Rejected - OAuth state belongs in BBolt, not config

### Storage Functions to Update

| Function | File | Line | Change Needed |
|----------|------|------|---------------|
| `UpdateOAuthClientCredentials` | `internal/storage/bbolt.go` | 393-428 | Add `callbackPort` parameter |
| `GetOAuthClientCredentials` | `internal/storage/bbolt.go` | 430-448 | Return `callbackPort` |

---

## 2. Callback Server Implementation

### Decision: Add preferredPort parameter to StartCallbackServer

**Current Implementation** (`internal/oauth/config.go:719-819`):
- Always allocates dynamic port via `net.Listen("tcp", "127.0.0.1:0")`
- Port extracted from listener: `addr.Port`
- Constructs `redirectURI` from port

**Proposed Change**:
```go
func (m *CallbackServerManager) StartCallbackServer(serverName string, preferredPort int) (*CallbackServer, error) {
    // Try preferred port first if specified
    if preferredPort > 0 {
        listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
        if err == nil {
            return m.createCallbackServer(serverName, listener)
        }
        // Log warning, fall through to dynamic allocation
    }
    // Fall back to dynamic port allocation
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    // ...
}
```

**Rationale**:
- Non-breaking change - `preferredPort=0` gives existing behavior
- Clean fallback on port conflict
- No changes to callback handler logic

**Alternatives Considered**:
- Port range allocation (hash server name): Rejected - collision risk
- Fixed port per server type: Rejected - inflexible

---

## 3. DCR Flow Integration

### Decision: Pass callback port through DCR flow and store on success

**DCR Credential Storage** (`internal/upstream/core/connection.go:2183-2200`):
```go
// Current: Only stores client ID and secret
serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
c.storage.UpdateOAuthClientCredentials(serverKey, clientID, clientSecret)

// Proposed: Also store callback port
c.storage.UpdateOAuthClientCredentials(serverKey, clientID, clientSecret, callbackServer.Port)
```

**DCR Credential Loading** (`internal/oauth/config.go:664-683`):
```go
// Current: Loads client ID and secret
persistedClientID, persistedClientSecret, err := storage.GetOAuthClientCredentials(serverKey)

// Proposed: Also load callback port
persistedClientID, persistedClientSecret, persistedPort, err := storage.GetOAuthClientCredentials(serverKey)
if persistedPort > 0 {
    preferredPort = persistedPort
}
```

---

## 4. Port Conflict Handling

### Decision: Clear DCR credentials if port changes (re-DCR approach)

**Rationale**: If the stored port is unavailable, the stored DCR credentials become invalid because the registered `redirect_uri` won't match.

**Implementation Location**: `CreateOAuthConfigWithExtraParams` in `internal/oauth/config.go`

```go
// After starting callback server
actualPort := callbackServer.Port
if preferredPort > 0 && actualPort != preferredPort {
    // Port changed - DCR credentials are now invalid
    logger.Warn("Callback port changed, clearing DCR credentials for re-registration",
        zap.String("server", serverName),
        zap.Int("stored_port", preferredPort),
        zap.Int("new_port", actualPort))
    storage.ClearOAuthClientCredentials(serverKey)
    // Reset loaded credentials to force fresh DCR
    clientID = ""
    clientSecret = ""
}
```

**Alternatives Considered**:
- Keep trying port indefinitely: Rejected - could deadlock if port busy
- Return error to user: Rejected - poor UX, automatic recovery is better

---

## 5. Backward Compatibility

### Decision: Optional fields with zero-value fallback

**Rationale**: Existing stored credentials won't have `CallbackPort` field. Reading should handle this gracefully.

**Implementation**:
- `CallbackPort int` defaults to 0 in Go
- When loading: `if callbackPort == 0 { /* use dynamic allocation */ }`
- No migration script needed

---

## 6. Testing Strategy

### Unit Tests
1. `TestStartCallbackServerWithPreferredPort` - preferred port binding
2. `TestStartCallbackServerFallback` - fallback when preferred port busy
3. `TestUpdateOAuthClientCredentialsWithPort` - storage round-trip
4. `TestPortConflictClearsDCR` - credential clearing on port change

### E2E Tests
1. Fresh DCR flow - verify port stored
2. Re-auth with same port - verify port reuse
3. Re-auth with port conflict - verify re-DCR

---

## 7. Files to Modify

| File | Changes |
|------|---------|
| `internal/storage/models.go` | Add `CallbackPort`, `RedirectURI` to OAuthTokenRecord |
| `internal/storage/bbolt.go` | Update `UpdateOAuthClientCredentials`, `GetOAuthClientCredentials` |
| `internal/storage/manager.go` | Update wrapper functions if any |
| `internal/oauth/config.go` | Add `preferredPort` to `StartCallbackServer`, load/save port |
| `internal/upstream/core/connection.go` | Pass port to `UpdateOAuthClientCredentials` |
| `internal/storage/bbolt_test.go` | Add tests for port persistence |
| `internal/oauth/config_test.go` | Add tests for preferred port binding |
