# Research: OAuth E2E Testing & Observability

**Feature**: 007-oauth-e2e-testing
**Date**: 2025-12-02

## Research Tasks

### 1. JWT Library Selection for Test Server

**Decision**: Use `github.com/golang-jwt/jwt/v5`

**Rationale**:
- Industry standard Go JWT library (successor to dgrijalva/jwt-go)
- Already a transitive dependency in many Go OAuth implementations
- Supports RS256, ES256, and other algorithms
- Well-documented API for custom claims

**Alternatives Considered**:
- `gopkg.in/square/go-jose.v2`: More complex, designed for full JOSE suite (not just JWT)
- `github.com/lestrrat-go/jwx`: Feature-rich but heavier dependency
- Manual JWT creation: Too error-prone and maintenance burden

### 2. Test Server Architecture Pattern

**Decision**: Single HTTP server with handler registration pattern

**Rationale**:
- Mirrors existing mcpproxy test patterns (`internal/server/e2e_test.go`)
- Ephemeral port allocation via `net.Listen(":0")` avoids conflicts
- `http.ServeMux` provides clean endpoint routing
- Shutdown via `httptest.Server` or manual `http.Server.Shutdown()`

**Alternatives Considered**:
- Multiple servers per endpoint: Unnecessary complexity
- Docker-based test OAuth server: Slower, harder to configure dynamically
- External OAuth provider mocks (WireMock): Additional dependency, less Go-native

### 3. PKCE Implementation

**Decision**: Standard SHA-256 code challenge method (S256)

**Rationale**:
- RFC 7636 mandates S256 as the preferred method
- mcpproxy's existing OAuth implementation uses S256
- `crypto/sha256` in stdlib for verification
- Plain method should be rejected in tests (security best practice)

**Implementation Pattern**:
```go
// Generate verifier (client-side, 43-128 chars)
verifier := base64.RawURLEncoding.EncodeToString(randomBytes(32))

// Generate challenge (client-side)
hash := sha256.Sum256([]byte(verifier))
challenge := base64.RawURLEncoding.EncodeToString(hash[:])

// Verify (server-side)
expectedHash := sha256.Sum256([]byte(receivedVerifier))
expectedChallenge := base64.RawURLEncoding.EncodeToString(expectedHash[:])
if expectedChallenge != storedChallenge { return error }
```

### 4. Device Code Flow Implementation

**Decision**: Implement full RFC 8628 device authorization grant

**Rationale**:
- mcpproxy needs headless/CLI OAuth support
- Device code flow is the standard for CLI tools
- Configurable polling interval and timeout for fast tests

**Endpoints Required**:
- `POST /device_authorization`: Returns `device_code`, `user_code`, `verification_uri`, `interval`
- `GET /device_verification`: Shows form for entering user_code
- `POST /device_verification`: Approves/denies device code
- `POST /token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code`: Polls for token

**Test Server State Machine**:
- `pending`: Device code issued, awaiting user action
- `approved`: User entered code and approved
- `denied`: User explicitly denied
- `expired`: Timeout exceeded

### 5. Dynamic Client Registration (DCR)

**Decision**: Implement RFC 7591 with minimal required fields

**Rationale**:
- mcpproxy supports DCR for zero-configuration OAuth
- Test server needs to issue client credentials dynamically
- Minimal implementation sufficient for testing

**Required Fields (Request)**:
- `redirect_uris`: Array of callback URLs
- `grant_types`: Optional, defaults to `["authorization_code"]`
- `response_types`: Optional, defaults to `["code"]`
- `client_name`: Optional for identification

**Response Fields**:
- `client_id`: Generated UUID
- `client_secret`: Generated random string (for confidential clients)
- `client_id_issued_at`: Unix timestamp
- `client_secret_expires_at`: 0 for non-expiring

### 6. Login UI for Browser Tests

**Decision**: Simple HTML template with Go's `html/template`

**Rationale**:
- Playwright can interact with standard HTML forms
- No JavaScript framework needed for test UI
- Template can be embedded via `embed` directive

**Required Form Elements**:
- `<input name="username">`: Test username
- `<input name="password" type="password">`: Test password
- `<input name="consent" type="checkbox">`: Consent checkbox
- `<button type="submit">`: Submit button
- Error message display area

**Test Credentials**:
- Valid: `testuser` / `testpass`
- Invalid password triggers error page
- Unchecked consent triggers `error=access_denied` redirect

### 7. Error Injection Patterns

**Decision**: Options struct with toggles for each error type

**Rationale**:
- Test-specific configuration without global state
- Clear mapping from option to expected error behavior
- Supports per-test customization

**Error Types**:
```go
type ErrorMode struct {
    TokenEndpoint struct {
        InvalidClient     bool          // Return invalid_client
        InvalidGrant      bool          // Return invalid_grant
        InvalidScope      bool          // Return invalid_scope
        ServerError       bool          // Return 500
        SlowResponse      time.Duration // Delay before response
        UnsupportedGrant  bool          // Return unsupported_grant_type
    }
    AuthorizeEndpoint struct {
        AccessDenied      bool          // Return error=access_denied
        InvalidRequest    bool          // Return error=invalid_request
    }
}
```

### 8. JWKS Rotation Testing

**Decision**: Test server maintains key ring with key ID (kid) tracking

**Rationale**:
- Production OAuth providers rotate keys periodically
- mcpproxy must handle key rotation gracefully
- Test server needs ability to swap keys mid-test

**Implementation**:
```go
type KeyRing struct {
    keys     map[string]*rsa.PrivateKey  // kid -> private key
    activeKid string                      // Current signing key
}

func (kr *KeyRing) RotateTo(newKid string) error
func (kr *KeyRing) GetJWKS() *jose.JSONWebKeySet
func (kr *KeyRing) SignToken(claims jwt.MapClaims) (string, error)
```

### 9. Resource Indicator (RFC 8707) Support

**Decision**: Echo `resource` parameter into JWT `aud` claim

**Rationale**:
- RFC 8707 specifies resource indicators for audience-restricted tokens
- mcpproxy passes `resource` on authorize and token requests
- Test server must validate and echo back

**Flow**:
1. Authorization request includes `resource=https://api.example.com`
2. Store resource with authorization code
3. Token request includes `resource=https://api.example.com`
4. Validate resource matches stored value
5. Issue JWT with `aud: "https://api.example.com"`

### 10. Existing mcpproxy OAuth Code Integration Points

**Decision**: Tests will exercise existing code paths without modification to OAuth core

**Key Integration Points Identified**:
- `internal/oauth/config.go`: `CreateOAuthConfig()` - main entry point
- `internal/oauth/discovery.go`: `DetectOAuthAvailability()`, `DiscoverScopesFromProtectedResource()`
- `internal/upstream/core/connection.go`: `tryOAuthAuth()`, `handleOAuthAuthorization()`
- `internal/upstream/cli/client.go`: `TriggerManualOAuth()`, `GetOAuthStatus()`
- `cmd/mcpproxy/auth_cmd.go`: `runAuthLogin()`, `runAuthStatus()`

**Test Approach**:
- Create mcpproxy config pointing to test OAuth server
- Start mcpproxy with this config
- Trigger OAuth flows via CLI commands or API
- Assert tokens are stored and status reflects success

### 11. Playwright Test Strategy

**Decision**: Use `npx playwright test` with TypeScript specs

**Rationale**:
- Playwright provides cross-browser testing (Chromium by default)
- TypeScript specs align with existing frontend conventions
- `npx` avoids global installation requirements
- Headless mode for CI compatibility

**Test Flow**:
1. Start OAuth test server
2. Start mcpproxy with test config
3. Playwright navigates to authorization URL
4. Fill credentials form, submit
5. Assert redirect to mcpproxy callback
6. Assert `auth status` shows authenticated

### 12. CI Integration

**Decision**: Separate CI job with OAuth tests behind flag

**Rationale**:
- OAuth E2E tests are slower than unit tests
- Can be skipped for quick PR checks if needed
- Playwright requires browser installation

**CI Configuration**:
```yaml
- name: Run OAuth E2E Tests
  if: github.event_name == 'push' || contains(github.event.pull_request.labels.*.name, 'test-oauth')
  run: ./scripts/run-oauth-e2e.sh
```

## Resolved Unknowns

All technical context items from the plan are resolved:

| Item | Resolution |
|------|------------|
| JWT library | `github.com/golang-jwt/jwt/v5` |
| Test server pattern | Single HTTP server with `http.ServeMux` |
| PKCE verification | SHA-256 (S256 method) |
| Device code flow | Full RFC 8628 implementation |
| DCR | Minimal RFC 7591 implementation |
| Login UI | Go `html/template` embedded template |
| Error injection | Options struct with toggles |
| JWKS rotation | Key ring with kid tracking |
| Resource indicator | Echo to JWT `aud` claim |
| Playwright | `npx playwright test` with TypeScript |
| CI | Separate job with conditional execution |

## References

- RFC 6749: OAuth 2.0 Authorization Framework
- RFC 7636: PKCE for OAuth Public Clients
- RFC 7591: OAuth 2.0 Dynamic Client Registration
- RFC 8414: OAuth 2.0 Authorization Server Metadata
- RFC 8628: OAuth 2.0 Device Authorization Grant
- RFC 8707: Resource Indicators for OAuth 2.0
- RFC 9728: OAuth 2.0 Protected Resource Metadata
- go-sdk `oauthex` package for reference patterns
