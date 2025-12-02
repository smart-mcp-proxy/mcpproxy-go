# OAuth Test Server Go API Contract

**Package**: `tests/oauthserver`

## Entry Point

```go
// Start creates and starts a new OAuth test server.
// The server listens on an ephemeral port on localhost.
// Returns ServerResult containing URLs and credentials for testing.
// Call result.Shutdown() to stop the server after tests complete.
func Start(t *testing.T, opts Options) *ServerResult
```

## Options Struct

```go
// Options configures the OAuth test server behavior.
type Options struct {
    // Flow toggles (all true by default)
    EnableAuthCode         bool
    EnableDeviceCode       bool
    EnableDCR              bool
    EnableClientCredentials bool
    EnableRefreshToken     bool

    // Token lifetimes
    AccessTokenExpiry  time.Duration // Default: 1 hour
    RefreshTokenExpiry time.Duration // Default: 24 hours
    AuthCodeExpiry     time.Duration // Default: 10 minutes
    DeviceCodeExpiry   time.Duration // Default: 5 minutes
    DeviceCodeInterval int           // Default: 5 seconds

    // Scopes
    DefaultScopes    []string // Default: ["read"]
    SupportedScopes  []string // Default: ["read", "write", "admin"]

    // Security
    RequirePKCE bool // Default: true

    // Error injection
    ErrorMode ErrorMode

    // Detection mode
    DetectionMode DetectionMode // Default: Discovery

    // Test credentials
    ValidUsers map[string]string // Default: {"testuser": "testpass"}

    // Pre-registered clients (in addition to auto-generated test client)
    Clients []ClientConfig
}
```

## ErrorMode Struct

```go
// ErrorMode configures error injection for testing error handling.
type ErrorMode struct {
    // Token endpoint errors
    TokenInvalidClient    bool
    TokenInvalidGrant     bool
    TokenInvalidScope     bool
    TokenServerError      bool
    TokenSlowResponse     time.Duration
    TokenUnsupportedGrant bool

    // Authorization endpoint errors
    AuthAccessDenied   bool
    AuthInvalidRequest bool

    // DCR endpoint errors
    DCRInvalidRedirectURI bool
    DCRInvalidScope       bool

    // Device code errors
    DeviceSlowPoll bool
    DeviceExpired  bool
}
```

## DetectionMode Type

```go
// DetectionMode controls how OAuth is advertised to clients.
type DetectionMode int

const (
    // Discovery serves /.well-known/oauth-authorization-server
    Discovery DetectionMode = iota

    // WWWAuthenticate returns 401 with WWW-Authenticate header on /protected
    WWWAuthenticate

    // Explicit provides no discovery; client must configure endpoints manually
    Explicit

    // Both serves discovery AND returns WWW-Authenticate
    Both
)
```

## ServerResult Struct

```go
// ServerResult contains everything needed to configure a test client.
type ServerResult struct {
    // Server URLs
    IssuerURL                    string
    AuthorizationEndpoint        string
    TokenEndpoint                string
    JWKSURL                      string
    RegistrationEndpoint         string // Empty if DCR disabled
    DeviceAuthorizationEndpoint  string // Empty if device code disabled
    ProtectedResourceURL         string // For WWW-Authenticate detection tests

    // Pre-registered test client (confidential)
    ClientID     string
    ClientSecret string

    // Pre-registered public client (for PKCE flows)
    PublicClientID string

    // Shutdown function - must be called after tests
    Shutdown func() error

    // Internal server reference for advanced testing
    Server *OAuthTestServer
}
```

## ClientConfig Struct

```go
// ClientConfig defines a pre-registered OAuth client.
type ClientConfig struct {
    ClientID       string
    ClientSecret   string   // Empty for public clients
    RedirectURIs   []string
    GrantTypes     []string // Default: ["authorization_code", "refresh_token"]
    ResponseTypes  []string // Default: ["code"]
    Scopes         []string // Default: options.SupportedScopes
    ClientName     string
}
```

## Server Methods (Advanced Usage)

```go
// OAuthTestServer provides additional methods for advanced test scenarios.
type OAuthTestServer struct {
    // ... internal fields
}

// RotateKey adds a new signing key and makes it active.
// The old key remains valid for verification.
// Returns the new key ID.
func (s *OAuthTestServer) RotateKey() (string, error)

// RemoveKey removes a key from the JWKS.
// Tokens signed with this key will fail verification.
func (s *OAuthTestServer) RemoveKey(kid string) error

// ApproveDeviceCode marks a device code as approved.
// Use for programmatic device flow testing without UI interaction.
func (s *OAuthTestServer) ApproveDeviceCode(userCode string) error

// DenyDeviceCode marks a device code as denied.
func (s *OAuthTestServer) DenyDeviceCode(userCode string) error

// ExpireDeviceCode marks a device code as expired.
func (s *OAuthTestServer) ExpireDeviceCode(userCode string) error

// GetIssuedTokens returns all tokens issued (for verification in tests).
func (s *OAuthTestServer) GetIssuedTokens() []TokenInfo

// SetErrorMode updates error injection at runtime.
func (s *OAuthTestServer) SetErrorMode(mode ErrorMode)

// GetAuthorizationCodes returns pending authorization codes (for debugging).
func (s *OAuthTestServer) GetAuthorizationCodes() []AuthCodeInfo

// RegisterClient programmatically registers a client.
// Returns the registered client with generated credentials.
func (s *OAuthTestServer) RegisterClient(cfg ClientConfig) (*Client, error)
```

## Usage Examples

### Basic Auth Code + PKCE Test

```go
func TestAuthCodePKCE(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{})
    defer server.Shutdown()

    // Configure mcpproxy to use test server
    cfg := &config.Config{
        MCPServers: []config.MCPServerConfig{{
            Name: "test-server",
            URL:  "http://example.com/mcp",
            OAuth: &config.OAuthConfig{
                ClientID:    server.PublicClientID,
                IssuerURL:   server.IssuerURL,
            },
        }},
    }

    // ... test OAuth flow
}
```

### Error Injection Test

```go
func TestInvalidClient(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        ErrorMode: oauthserver.ErrorMode{
            TokenInvalidClient: true,
        },
    })
    defer server.Shutdown()

    // ... test error handling
}
```

### JWKS Rotation Test

```go
func TestJWKSRotation(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{})
    defer server.Shutdown()

    // Get a token with original key
    token1 := getToken(server)

    // Rotate to new key
    newKid, _ := server.Server.RotateKey()

    // Remove old key
    server.Server.RemoveKey("initial-kid")

    // Old token should fail verification
    // New tokens should work
}
```

### Device Code Flow Test

```go
func TestDeviceCodeFlow(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        DeviceCodeInterval: 1, // Fast polling for tests
    })
    defer server.Shutdown()

    // Initiate device code flow
    resp := initiateDeviceCode(server)

    // Programmatically approve (no UI)
    server.Server.ApproveDeviceCode(resp.UserCode)

    // Poll for token
    token := pollForToken(server, resp.DeviceCode)

    assert.NotEmpty(t, token.AccessToken)
}
```

### WWW-Authenticate Detection Test

```go
func TestWWWAuthenticateDetection(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        DetectionMode: oauthserver.WWWAuthenticate,
    })
    defer server.Shutdown()

    // Hit protected resource
    resp, _ := http.Get(server.ProtectedResourceURL)
    assert.Equal(t, 401, resp.StatusCode)

    wwwAuth := resp.Header.Get("WWW-Authenticate")
    assert.Contains(t, wwwAuth, "authorization_uri")
}
```

### Dynamic Client Registration Test

```go
func TestDCR(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        EnableDCR: true,
    })
    defer server.Shutdown()

    // Register new client
    regReq := ClientRegistrationRequest{
        RedirectURIs: []string{"http://localhost:8080/callback"},
        ClientName:   "Test App",
    }

    client := registerClient(server.RegistrationEndpoint, regReq)
    assert.NotEmpty(t, client.ClientID)

    // Use registered client for auth code flow
    // ...
}
```
