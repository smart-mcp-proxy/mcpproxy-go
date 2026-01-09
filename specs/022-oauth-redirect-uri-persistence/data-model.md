# Data Model: OAuth Redirect URI Port Persistence

## Entity Changes

### OAuthTokenRecord (Modified)

**Location**: `internal/storage/models.go:64-79`

```go
type OAuthTokenRecord struct {
    // Existing fields
    ServerName    string    `json:"server_name"`    // Storage key (serverName_hash format)
    DisplayName   string    `json:"display_name"`   // Actual server name
    AccessToken   string    `json:"access_token"`
    RefreshToken  string    `json:"refresh_token"`
    TokenType     string    `json:"token_type"`
    ExpiresAt     time.Time `json:"expires_at"`
    Scopes        []string  `json:"scopes"`
    Created       time.Time `json:"created"`
    Updated       time.Time `json:"updated"`
    ClientID      string    `json:"client_id"`      // DCR-obtained client ID
    ClientSecret  string    `json:"client_secret"`  // DCR-obtained client secret

    // NEW fields for port persistence
    CallbackPort  int       `json:"callback_port,omitempty"`  // Port used during DCR
    RedirectURI   string    `json:"redirect_uri,omitempty"`   // Full URI registered with DCR
}
```

**Field Descriptions**:

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| CallbackPort | int | TCP port used for OAuth callback during DCR | 0 = not set (use dynamic), 1024-65535 valid |
| RedirectURI | string | Full redirect URI registered during DCR | Format: `http://127.0.0.1:{port}/oauth/callback` |

**Validation Rules**:
- `CallbackPort` must be 0 (unset) or in range 1024-65535
- `RedirectURI` must match pattern `http://127.0.0.1:\d+/oauth/callback` if set
- Fields are optional for backward compatibility with existing records

**State Transitions**:

```
[No Credentials] --DCR Success--> [Credentials + Port Stored]
[Credentials + Port Stored] --Port Available--> [Reuse Port]
[Credentials + Port Stored] --Port Conflict--> [Clear Credentials] --> [No Credentials]
```

## Storage Interface Changes

### UpdateOAuthClientCredentials (Modified)

**Current Signature**:
```go
func (db *BoltDB) UpdateOAuthClientCredentials(serverKey, clientID, clientSecret string) error
```

**New Signature**:
```go
func (db *BoltDB) UpdateOAuthClientCredentials(serverKey, clientID, clientSecret string, callbackPort int) error
```

### GetOAuthClientCredentials (Modified)

**Current Signature**:
```go
func (db *BoltDB) GetOAuthClientCredentials(serverKey string) (clientID, clientSecret string, err error)
```

**New Signature**:
```go
func (db *BoltDB) GetOAuthClientCredentials(serverKey string) (clientID, clientSecret string, callbackPort int, err error)
```

### ClearOAuthClientCredentials (New)

**Signature**:
```go
func (db *BoltDB) ClearOAuthClientCredentials(serverKey string) error
```

**Behavior**:
- Clears only `ClientID`, `ClientSecret`, `CallbackPort`, `RedirectURI` fields
- Preserves token data if present
- Used when port conflict requires re-DCR

## CallbackServer Changes

### StartCallbackServer (Modified)

**Current Signature**:
```go
func (m *CallbackServerManager) StartCallbackServer(serverName string) (*CallbackServer, error)
```

**New Signature**:
```go
func (m *CallbackServerManager) StartCallbackServer(serverName string, preferredPort int) (*CallbackServer, error)
```

**Behavior**:
- If `preferredPort > 0`: Try to bind to that port first
- If binding fails or `preferredPort == 0`: Use dynamic allocation (`:0`)
- Return actual bound port in `CallbackServer.Port`

## Backward Compatibility

| Scenario | Behavior |
|----------|----------|
| Existing record without CallbackPort | `CallbackPort = 0`, use dynamic allocation |
| New DCR flow | Store port, attempt reuse on subsequent auth |
| Port conflict | Clear credentials, perform fresh DCR |

## Migration

**No migration required** - JSON deserialization handles missing fields:
- `CallbackPort` defaults to 0 (Go zero value for int)
- `RedirectURI` defaults to "" (Go zero value for string)
