# Data Model: OAuth E2E Testing & Observability

**Feature**: 007-oauth-e2e-testing
**Date**: 2025-12-02

## Entities

### 1. OAuthTestServer

The main test server instance managing all OAuth endpoints.

| Field | Type | Description |
|-------|------|-------------|
| server | *http.Server | Underlying HTTP server |
| addr | string | Bound address (e.g., "127.0.0.1:12345") |
| issuerURL | string | Full issuer URL (e.g., "http://127.0.0.1:12345") |
| options | Options | Configuration for this server instance |
| keyRing | *KeyRing | RSA key management for JWT signing |
| clients | map[string]*Client | Registered OAuth clients (client_id -> Client) |
| authCodes | map[string]*AuthorizationCode | Pending authorization codes |
| deviceCodes | map[string]*DeviceCode | Pending device codes |
| mu | sync.RWMutex | Protects concurrent access |

**State Transitions**: None (stateless per request, state stored in maps)

### 2. Options

Configuration for the test OAuth server behavior.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| EnableAuthCode | bool | true | Enable authorization code flow |
| EnableDeviceCode | bool | true | Enable device code flow |
| EnableDCR | bool | true | Enable dynamic client registration |
| EnableClientCredentials | bool | true | Enable client credentials grant |
| EnableRefreshToken | bool | true | Issue refresh tokens |
| AccessTokenExpiry | time.Duration | 3600s | Access token lifetime |
| RefreshTokenExpiry | time.Duration | 86400s | Refresh token lifetime |
| AuthCodeExpiry | time.Duration | 600s | Authorization code lifetime |
| DeviceCodeExpiry | time.Duration | 300s | Device code lifetime |
| DeviceCodeInterval | int | 5 | Polling interval in seconds |
| DefaultScopes | []string | ["read"] | Scopes if none requested |
| SupportedScopes | []string | ["read","write","admin"] | All valid scopes |
| RequirePKCE | bool | true | Require PKCE for auth code flow |
| ErrorMode | ErrorMode | (empty) | Error injection configuration |
| DetectionMode | DetectionMode | Discovery | How OAuth is advertised |
| ValidUsers | map[string]string | {"testuser":"testpass"} | Valid username/password pairs |

### 3. ErrorMode

Configuration for injecting specific OAuth errors.

| Field | Type | Description |
|-------|------|-------------|
| TokenInvalidClient | bool | Return `invalid_client` on token requests |
| TokenInvalidGrant | bool | Return `invalid_grant` on token requests |
| TokenInvalidScope | bool | Return `invalid_scope` on token requests |
| TokenServerError | bool | Return HTTP 500 on token requests |
| TokenSlowResponse | time.Duration | Delay before token response |
| TokenUnsupportedGrant | bool | Return `unsupported_grant_type` |
| AuthAccessDenied | bool | Return `error=access_denied` on authorize |
| AuthInvalidRequest | bool | Return `error=invalid_request` on authorize |
| DCRInvalidRedirectURI | bool | Reject registration with bad redirect |
| DCRInvalidScope | bool | Reject registration with bad scope |
| DeviceSlowPoll | bool | Return `slow_down` on device polling |
| DeviceExpired | bool | Return `expired_token` on device polling |

### 4. DetectionMode

How the OAuth server advertises its capabilities.

| Value | Description |
|-------|-------------|
| Discovery | Serve `/.well-known/oauth-authorization-server` |
| WWWAuthenticate | Return 401 with WWW-Authenticate header on protected resource |
| Explicit | No discovery; endpoints must be configured explicitly |
| Both | Serve both discovery and WWW-Authenticate |

### 5. Client

A registered OAuth client (pre-configured or dynamically registered).

| Field | Type | Description |
|-------|------|-------------|
| ClientID | string | Unique client identifier |
| ClientSecret | string | Client secret (empty for public clients) |
| RedirectURIs | []string | Allowed callback URLs |
| GrantTypes | []string | Allowed grant types |
| ResponseTypes | []string | Allowed response types |
| Scopes | []string | Allowed scopes for this client |
| ClientName | string | Human-readable name |
| IsPublic | bool | True if public client (no secret) |
| CreatedAt | time.Time | Registration timestamp |

**Validation Rules**:
- `ClientID` must be non-empty and unique
- `RedirectURIs` must contain at least one valid URI
- URIs must not contain fragments (`#`)
- `localhost` and `127.0.0.1` allowed for test callbacks

### 6. AuthorizationCode

Ephemeral code issued during authorization flow.

| Field | Type | Description |
|-------|------|-------------|
| Code | string | The authorization code value |
| ClientID | string | Associated client |
| RedirectURI | string | Redirect URI used in request |
| Scopes | []string | Granted scopes |
| CodeChallenge | string | PKCE code challenge |
| CodeChallengeMethod | string | PKCE method (S256) |
| Resource | string | RFC 8707 resource indicator |
| State | string | Client state parameter |
| ExpiresAt | time.Time | Code expiration time |
| Used | bool | Whether code has been exchanged |

**State Transitions**:
- Created → Used (on token exchange)
- Created → Expired (on timeout)

### 7. DeviceCode

Device authorization code for device flow.

| Field | Type | Description |
|-------|------|-------------|
| DeviceCode | string | Secret device code (for polling) |
| UserCode | string | User-facing code (e.g., "ABCD-1234") |
| ClientID | string | Associated client |
| Scopes | []string | Requested scopes |
| Resource | string | RFC 8707 resource indicator |
| VerificationURI | string | URL for user to visit |
| VerificationURIComplete | string | URL with user_code pre-filled |
| ExpiresAt | time.Time | Code expiration time |
| Interval | int | Minimum polling interval (seconds) |
| Status | DeviceCodeStatus | Current status |
| ApprovedScopes | []string | Scopes approved by user (if approved) |

**State Transitions**:
```
pending → approved (user approves)
pending → denied (user denies)
pending → expired (timeout)
```

### 8. DeviceCodeStatus

| Value | Description |
|-------|-------------|
| Pending | Awaiting user action |
| Approved | User approved the request |
| Denied | User denied the request |
| Expired | Code has expired |

### 9. TokenResponse

Response from token endpoint (success case).

| Field | Type | Description |
|-------|------|-------------|
| AccessToken | string | JWT access token |
| TokenType | string | Always "Bearer" |
| ExpiresIn | int | Seconds until expiry |
| RefreshToken | string | Refresh token (if enabled) |
| Scope | string | Space-separated granted scopes |

### 10. TokenErrorResponse

Response from token endpoint (error case).

| Field | Type | Description |
|-------|------|-------------|
| Error | string | Error code (e.g., "invalid_client") |
| ErrorDescription | string | Human-readable description |
| ErrorURI | string | Optional URI for more info |

### 11. KeyRing

Manages RSA key pairs for JWT signing with rotation support.

| Field | Type | Description |
|-------|------|-------------|
| keys | map[string]*rsa.PrivateKey | Key ID to private key mapping |
| activeKid | string | Currently active key for signing |
| mu | sync.RWMutex | Protects concurrent access |

**Operations**:
- `AddKey(kid string, key *rsa.PrivateKey)`: Add a new key
- `RotateTo(kid string)`: Switch active signing key
- `RemoveKey(kid string)`: Remove a key (for rotation testing)
- `GetJWKS()`: Return public keys in JWK Set format
- `SignToken(claims)`: Sign JWT with active key

### 12. ServerResult

Return value from `Start()` function.

| Field | Type | Description |
|-------|------|-------------|
| IssuerURL | string | Full issuer URL for configuration |
| ClientID | string | Pre-registered test client ID |
| ClientSecret | string | Pre-registered test client secret |
| PublicClientID | string | Pre-registered public client ID (PKCE) |
| JWKSURL | string | URL to fetch JWKS |
| AuthorizationEndpoint | string | /authorize URL |
| TokenEndpoint | string | /token URL |
| RegistrationEndpoint | string | /registration URL (if DCR enabled) |
| DeviceAuthorizationEndpoint | string | /device_authorization URL (if enabled) |
| Shutdown | func() error | Function to stop server |

### 13. DiscoveryMetadata

OAuth 2.0 Authorization Server Metadata (RFC 8414).

| Field | JSON Key | Type | Description |
|-------|----------|------|-------------|
| Issuer | issuer | string | Issuer identifier URL |
| AuthorizationEndpoint | authorization_endpoint | string | Authorization endpoint URL |
| TokenEndpoint | token_endpoint | string | Token endpoint URL |
| JWKSURI | jwks_uri | string | JWKS endpoint URL |
| RegistrationEndpoint | registration_endpoint | string | DCR endpoint URL |
| DeviceAuthorizationEndpoint | device_authorization_endpoint | string | Device authz URL |
| ScopesSupported | scopes_supported | []string | Supported scopes |
| ResponseTypesSupported | response_types_supported | []string | Supported response types |
| GrantTypesSupported | grant_types_supported | []string | Supported grant types |
| CodeChallengeMethodsSupported | code_challenge_methods_supported | []string | PKCE methods |
| TokenEndpointAuthMethodsSupported | token_endpoint_auth_methods_supported | []string | Client auth methods |

## Relationships

```
OAuthTestServer
    ├── Options (1:1)
    ├── KeyRing (1:1)
    ├── Clients (1:N)
    │   └── Client
    ├── AuthorizationCodes (1:N)
    │   └── AuthorizationCode
    │       └── Client (N:1)
    └── DeviceCodes (1:N)
        └── DeviceCode
            └── Client (N:1)
```

## JWT Claims Structure

Access tokens issued by the test server:

```json
{
  "iss": "http://127.0.0.1:12345",
  "sub": "testuser",
  "aud": "https://api.example.com",  // From resource indicator
  "client_id": "test-client-id",
  "scope": "read write",
  "exp": 1733123456,
  "iat": 1733119856,
  "jti": "unique-token-id"
}
```

## Storage Notes

The test OAuth server is **in-memory only**:
- All state (clients, codes, tokens) stored in Go maps
- State is cleared when server shuts down
- No persistence needed for test scenarios
- Thread-safe access via `sync.RWMutex`

mcpproxy's token storage (unchanged):
- Uses existing `internal/storage/` BBolt implementation
- `PersistentTokenStore` in `internal/oauth/persistent_token_store.go`
- Tests verify tokens are persisted correctly
