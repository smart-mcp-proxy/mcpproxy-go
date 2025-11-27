# Implementation Plan: OAuth Extra Parameters Support

**Status**: Proposed
**Created**: 2025-11-27
**Priority**: High (blocks Runlayer integration)
**Related**: docs/runlayer-oauth-investigation.md

## Problem Statement

MCPProxy cannot authenticate with OAuth providers that require additional query parameters beyond the standard OAuth 2.0 parameters. Specifically, Runlayer's OAuth implementation requires an RFC 8707 `resource` parameter that MCPProxy cannot currently provide.

**Current Authorization URL** (fails):
```
/oauth/authorize?client_id=X&code_challenge=Y&redirect_uri=Z&response_type=code&state=W
```

**Required Authorization URL** (works):
```
/oauth/authorize?client_id=X&resource=<MCP_ENDPOINT>&code_challenge=Y&redirect_uri=Z&response_type=code&state=W
```

## Goals

### Phase 0 (Immediate - Days 1-2)
1. ✅ **Fix `auth status` to show OAuth-configured servers**
2. ✅ **Provide clear error messages when OAuth fails due to missing parameters**
3. ✅ **Give actionable suggestions for fixing OAuth issues**
4. ✅ **Enable debugging OAuth problems without digging through logs**

### Full Implementation (Phases 1-5)
5. ✅ Add `extra_params` field to config.OAuthConfig
6. ✅ Pass extra parameters through to mcp-go OAuth client
7. ✅ Support both authorization and token endpoint extra parameters
8. ✅ Maintain backward compatibility with existing OAuth configs
9. ✅ Document usage for RFC 8707 resource indicators

## Non-Goals

- Modifying mcp-go library directly (upstream contribution is separate)
- Automatic detection of required extra parameters
- Validation of provider-specific parameter requirements

## Architecture

### Layer 1: Configuration (internal/config/config.go)

**Current**:
```go
type OAuthConfig struct {
    ClientID     string   `json:"client_id,omitempty"`
    ClientSecret string   `json:"client_secret,omitempty"`
    RedirectURI  string   `json:"redirect_uri,omitempty"`
    Scopes       []string `json:"scopes,omitempty"`
    PKCEEnabled  bool     `json:"pkce_enabled,omitempty"`
}
```

**Proposed**:
```go
type OAuthConfig struct {
    ClientID     string            `json:"client_id,omitempty" mapstructure:"client_id"`
    ClientSecret string            `json:"client_secret,omitempty" mapstructure:"client_secret"`
    RedirectURI  string            `json:"redirect_uri,omitempty" mapstructure:"redirect_uri"`
    Scopes       []string          `json:"scopes,omitempty" mapstructure:"scopes"`
    PKCEEnabled  bool              `json:"pkce_enabled,omitempty" mapstructure:"pkce_enabled"`
    ExtraParams  map[string]string `json:"extra_params,omitempty" mapstructure:"extra_params"` // NEW
}
```

**Example Configuration**:
```json
{
  "name": "slack",
  "protocol": "streamable-http",
  "url": "https://oauth.example.com/api/v1/proxy/00000000-0000-0000-0000-000000000000/mcp",
  "oauth": {
    "extra_params": {
      "resource": "https://oauth.example.com/api/v1/proxy/00000000-0000-0000-0000-000000000000/mcp"
    }
  }
}
```

### Layer 2: OAuth Config Creation (internal/oauth/config.go)

**Current Flow**:
```go
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig {
    // ... scope discovery ...

    oauthConfig := &client.OAuthConfig{
        ClientID:              clientID,
        ClientSecret:          clientSecret,
        RedirectURI:           callbackServer.RedirectURI,
        Scopes:                scopes,
        TokenStore:            tokenStore,
        PKCEEnabled:           true,
        AuthServerMetadataURL: authServerMetadataURL,
    }

    return oauthConfig
}
```

**Issue**: `client.OAuthConfig` (from mcp-go v0.42.0) doesn't have an `ExtraParams` field.

**Proposed Approach**:
Since we cannot modify mcp-go's `OAuthConfig` directly, we need to pass extra params through at the transport layer where the actual OAuth URLs are constructed.

### Layer 3: Transport Layer (internal/transport/http.go)

**Current**:
```go
type HTTPTransportConfig struct {
    URL          string
    Headers      map[string]string
    UseOAuth     bool
    OAuthConfig  *client.OAuthConfig // mcp-go type
}

func CreateHTTPTransportConfig(serverConfig *config.ServerConfig, oauthConfig *client.OAuthConfig) *HTTPTransportConfig {
    return &HTTPTransportConfig{
        URL:         serverConfig.URL,
        Headers:     serverConfig.Headers,
        UseOAuth:    oauthConfig != nil,
        OAuthConfig: oauthConfig,
    }
}
```

**Proposed**:
```go
type HTTPTransportConfig struct {
    URL              string
    Headers          map[string]string
    UseOAuth         bool
    OAuthConfig      *client.OAuthConfig
    OAuthExtraParams map[string]string // NEW - bypass mcp-go limitation
}

func CreateHTTPTransportConfig(serverConfig *config.ServerConfig, oauthConfig *client.OAuthConfig) *HTTPTransportConfig {
    // Extract extra params from server config
    var extraParams map[string]string
    if serverConfig.OAuth != nil && serverConfig.OAuth.ExtraParams != nil {
        extraParams = serverConfig.OAuth.ExtraParams
    }

    return &HTTPTransportConfig{
        URL:              serverConfig.URL,
        Headers:          serverConfig.Headers,
        UseOAuth:        oauthConfig != nil,
        OAuthConfig:      oauthConfig,
        OAuthExtraParams: extraParams,
    }
}
```

### Layer 4: mcp-go Integration Strategy

Since `mcp-go v0.42.0` doesn't support extra parameters, we have **three options**:

#### Option A: Fork mcp-go (Short-term)
**Pros**:
- Full control over OAuth implementation
- Can add ExtraParams immediately
- Can contribute back to upstream

**Cons**:
- Maintenance burden of fork
- Need to sync with upstream updates
- Deployment complexity

#### Option B: Wrapper/Decorator Pattern (Recommended)
**Pros**:
- No mcp-go modifications needed
- Clean separation of concerns
- Easy to remove when mcp-go adds support

**Cons**:
- More complex integration code
- Need to intercept OAuth URL construction

**Implementation**:
```go
// internal/oauth/transport_wrapper.go

type OAuthTransportWrapper struct {
    inner       client.Transport     // mcp-go's OAuth transport
    extraParams map[string]string
}

func NewOAuthTransportWrapper(config *client.OAuthConfig, extraParams map[string]string) (*OAuthTransportWrapper, error) {
    // Create mcp-go OAuth client
    innerTransport, err := client.NewOAuthStreamableHttpClient(url, *config)
    if err != nil {
        return nil, err
    }

    return &OAuthTransportWrapper{
        inner:       innerTransport,
        extraParams: extraParams,
    }, nil
}

// Intercept methods that construct OAuth URLs
func (w *OAuthTransportWrapper) StartOAuthFlow(ctx context.Context) error {
    // Get the OAuth URL from inner transport
    authURL := w.inner.GetAuthorizationURL()

    // Add extra params
    if len(w.extraParams) > 0 {
        u, _ := url.Parse(authURL)
        q := u.Query()
        for k, v := range w.extraParams {
            q.Set(k, v)
        }
        u.RawQuery = q.Encode()
        authURL = u.String()
    }

    // Continue with modified URL
    return w.inner.StartOAuthFlowWithURL(ctx, authURL)
}
```

#### Option C: Contribute to mcp-go Upstream (Long-term)
**Pros**:
- Benefits entire mcp-go community
- No custom code in MCPProxy
- Standard solution

**Cons**:
- Takes time for PR review/merge
- Blocks Runlayer integration immediately
- Depends on upstream maintainer response

**Recommendation**: Use **Option B (Wrapper)** for immediate support, then pursue **Option C** as upstream contribution.

## Implementation Steps

### Phase 0: Diagnostics & Error Reporting (Week 1, Days 1-2) **[PRIORITY]**

**Rationale**: Before implementing the fix, users need clear visibility into OAuth status and actionable error messages. Currently `auth status` reports no OAuth servers despite OAuth being configured and failing.

**Tasks**:
1. Fix `auth status` to properly detect OAuth-configured servers
2. Enhance `auth status` to show OAuth failure reasons from runtime state
3. Add structured error messages for OAuth failures that include:
   - Missing required parameters (e.g., "OAuth failed: missing 'resource' parameter")
   - Authorization URL that was attempted
   - Suggestion to add `extra_params` to config
4. Update `doctor` command to detect OAuth parameter mismatches
5. Add logging to show when OAuth fails due to provider requirements

**Files Changed**:
- `cmd/mcpproxy/auth_cmd.go` (fix server detection in `auth status`)
- `internal/httpapi/server.go` (ensure `/api/v1/servers` serializes OAuth config)
- `internal/contracts/converters.go` (fix OAuth config conversion)
- `internal/upstream/core/connection.go` (capture OAuth error details)
- `internal/management/diagnostics.go` (add OAuth error diagnostics)

**Current Issue**:
```bash
$ ./mcpproxy auth status
ℹ️  No servers with OAuth configuration found.
   Configure OAuth in mcp_config.json to enable authentication.
```

Despite:
- Config has `"oauth": {}`
- Logs show OAuth setup happening
- OAuth flow is being attempted every 30 seconds

**Root Cause**:
The `/api/v1/servers` endpoint doesn't serialize the OAuth configuration, so `auth status` can't see it:

```json
{
  "authenticated": false,
  "name": "slack",
  "protocol": "",
  "oauth": null  // ← Should have OAuth config here
}
```

**Desired Output After Fix**:
```bash
$ ./mcpproxy auth status

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔐 OAuth Authentication Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Server: slack
  Status: ❌ Authentication Failed
  Error: OAuth provider requires 'resource' parameter (RFC 8707)
  Auth URL: https://oauth.example.com/.well-known/oauth-authorization-server
  Last Attempt: 2025-11-27 15:45:10

  💡 Suggestion:
     Add the following to your server configuration:

     "oauth": {
       "extra_params": {
         "resource": "https://your-mcp-endpoint/mcp"
       }
     }

     Note: extra_params support is coming in the next release.
```

**Implementation Details**:

1. **Fix OAuth Config Serialization** (contracts/converters.go):
```go
func ToServerContract(cfg *config.ServerConfig, status *upstream.ServerStatus) contracts.Server {
    var oauthConfig *contracts.OAuthConfig
    if cfg.OAuth != nil {
        oauthConfig = &contracts.OAuthConfig{
            AuthURL:  "", // TODO: Get from discovered metadata
            TokenURL: "", // TODO: Get from discovered metadata
            ClientID: cfg.OAuth.ClientID,
            Scopes:   cfg.OAuth.Scopes,
        }
    }

    return contracts.Server{
        // ... other fields ...
        OAuth:         oauthConfig,
        Authenticated: status.Authenticated,
        LastError:     status.LastError,
    }
}
```

2. **Capture OAuth Error Details** (upstream/core/connection.go):
```go
// When OAuth fails, parse error response
if strings.Contains(err.Error(), "Field required") {
    // Parse FastAPI validation error
    var validationErr struct {
        Detail []struct {
            Loc []string `json:"loc"`
            Msg string   `json:"msg"`
        } `json:"detail"`
    }

    if json.Unmarshal(errorBody, &validationErr) == nil {
        for _, detail := range validationErr.Detail {
            if len(detail.Loc) > 1 && detail.Loc[0] == "query" {
                missingParam := detail.Loc[1]
                return fmt.Errorf("OAuth provider requires '%s' parameter: %s",
                    missingParam, detail.Msg)
            }
        }
    }
}
```

3. **Enhanced Doctor Command** (management/diagnostics.go):
```go
// Add OAuth-specific diagnostics
type OAuthDiagnostic struct {
    ServerName     string
    ConfiguredAuth bool
    LastError      string
    MissingParams  []string
    Suggestion     string
}

func (s *service) checkOAuthIssues() []OAuthDiagnostic {
    // Detect missing resource parameters
    // Suggest extra_params configuration
}
```

**Success Criteria**:
- ✅ `auth status` shows slack server with OAuth configured
- ✅ Error message clearly states "missing 'resource' parameter"
- ✅ Suggestion includes example config with extra_params
- ✅ `doctor` command highlights OAuth configuration issues
- ✅ Logs include structured OAuth error information

**Why This Is Phase 0**:
Without proper diagnostics, users (and developers) can't:
- Verify OAuth is actually configured
- Understand why authentication fails
- Know what parameters are missing
- Get actionable guidance on fixes

This visibility is essential before implementing the fix itself.

### Phase 1: Config Layer (Week 1, Days 3-4)

**Tasks**:
1. Add `ExtraParams map[string]string` to `config.OAuthConfig` (config.go:155-161)
2. Add validation for extra params (no reserved OAuth 2.0 keywords)
3. Update config tests to cover extra params parsing
4. Update example configurations in docs/

**Files Changed**:
- `internal/config/config.go`
- `internal/config/validation.go`
- `internal/config/config_test.go`

**Validation Rules**:
```go
// Reserved OAuth 2.0 parameters that cannot be overridden
var reservedOAuthParams = map[string]bool{
    "client_id":             true,
    "client_secret":         true,
    "redirect_uri":          true,
    "response_type":         true,
    "scope":                 true,
    "state":                 true,
    "code_challenge":        true,
    "code_challenge_method": true,
    "grant_type":            true,
    "code":                  true,
    "refresh_token":         true,
}

func ValidateOAuthExtraParams(params map[string]string) error {
    for key := range params {
        if reservedOAuthParams[strings.ToLower(key)] {
            return fmt.Errorf("extra_params cannot override reserved OAuth parameter: %s", key)
        }
    }
    return nil
}
```

### Phase 2: OAuth Wrapper (Week 1-2)

**Tasks**:
1. Create `internal/oauth/transport_wrapper.go`
2. Implement wrapper for streamable-http OAuth client
3. Implement wrapper for SSE OAuth client
4. Handle both authorization and token endpoint parameters
5. Add comprehensive tests for wrapper behavior

**Files Created**:
- `internal/oauth/transport_wrapper.go`
- `internal/oauth/transport_wrapper_test.go`

**Wrapper Interface**:
```go
type TransportWrapper interface {
    // Wrap existing mcp-go OAuth transport with extra params support
    WrapTransport(inner client.Transport, extraParams map[string]string) (client.Transport, error)

    // Intercept authorization URL construction
    ModifyAuthorizationURL(baseURL string, extraParams map[string]string) (string, error)

    // Intercept token request construction
    ModifyTokenRequest(req *http.Request, extraParams map[string]string) error
}
```

### Phase 3: Integration (Week 2)

**Tasks**:
1. Update `internal/oauth/config.go` to pass extra params to wrapper
2. Update `internal/transport/http.go` to use wrapper when extra params present
3. Update `internal/upstream/core/connection.go` OAuth flow to use wrapper
4. Add logging for extra params being applied
5. Update existing OAuth tests

**Files Changed**:
- `internal/oauth/config.go`
- `internal/transport/http.go`
- `internal/upstream/core/connection.go`
- `internal/upstream/core/connection_test.go`

**CreateOAuthConfig Changes**:
```go
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) (*client.OAuthConfig, map[string]string) {
    // ... existing scope discovery ...

    oauthConfig := &client.OAuthConfig{
        ClientID:              clientID,
        ClientSecret:          clientSecret,
        RedirectURI:           callbackServer.RedirectURI,
        Scopes:                scopes,
        TokenStore:            tokenStore,
        PKCEEnabled:           true,
        AuthServerMetadataURL: authServerMetadataURL,
    }

    // Extract extra params
    var extraParams map[string]string
    if serverConfig.OAuth != nil && serverConfig.OAuth.ExtraParams != nil {
        extraParams = make(map[string]string)
        for k, v := range serverConfig.OAuth.ExtraParams {
            extraParams[k] = v
        }

        logger.Info("OAuth extra parameters configured",
            zap.String("server", serverConfig.Name),
            zap.Any("params", maskSensitiveParams(extraParams)))
    }

    return oauthConfig, extraParams
}

// Mask sensitive parameter values in logs
func maskSensitiveParams(params map[string]string) map[string]string {
    masked := make(map[string]string)
    for k, v := range params {
        // Don't mask resource URLs (not sensitive)
        if strings.HasPrefix(strings.ToLower(k), "resource") {
            masked[k] = v
        } else {
            masked[k] = "***MASKED***"
        }
    }
    return masked
}
```

### Phase 4: Testing (Week 2-3)

**Unit Tests**:
1. Config parsing with extra_params
2. Validation of reserved parameter names
3. Wrapper authorization URL modification
4. Wrapper token request modification
5. Backward compatibility (no extra_params)

**Integration Tests**:
1. Mock OAuth server requiring `resource` parameter
2. Full OAuth flow with extra params
3. Error handling for malformed extra params
4. Multiple extra params simultaneously

**E2E Tests**:
1. Runlayer Slack MCP server authentication (live test)
2. Regular OAuth flow without extra params (regression)

**Test Files**:
- `internal/config/config_test.go` (extra params parsing)
- `internal/oauth/transport_wrapper_test.go` (wrapper behavior)
- `internal/transport/http_test.go` (integration)
- `internal/server/e2e_oauth_test.go` (E2E)

### Phase 5: Documentation (Week 3)

**Tasks**:
1. Update main README with extra_params example
2. Create docs/oauth-extra-parameters.md guide
3. Document RFC 8707 resource indicator usage
4. Add example for common OAuth providers
5. Update API documentation (OpenAPI spec)

**Documentation Structure**:
```markdown
# OAuth Extra Parameters Guide

## Overview
Support for custom OAuth 2.0 authorization parameters...

## Configuration
### Basic Example
### RFC 8707 Resource Indicators
### Multiple Extra Parameters

## Supported Parameters
### Authorization Endpoint
### Token Endpoint

## Common Use Cases
### Runlayer/Anysource Integration
### Multi-tenant OAuth Providers
### Custom OAuth Extensions

## Security Considerations
### Parameter Validation
### Reserved Parameter Names
### Logging and Debugging

## Troubleshooting
```

### Phase 6: Upstream Contribution (Parallel)

**Tasks**:
1. Create fork of mark3labs/mcp-go
2. Add `ExtraParams map[string]string` to `client.OAuthConfig`
3. Update OAuth URL construction to include extra params
4. Add tests for extra params in mcp-go
5. Create PR to upstream with RFC 8707 use case
6. Monitor PR and respond to feedback

**PR Description**:
```markdown
# Add ExtraParams support to OAuthConfig

## Problem
OAuth providers may require additional parameters beyond the standard OAuth 2.0
specification. For example, RFC 8707 Resource Indicators require a `resource`
parameter to specify the target resource server.

## Solution
Add `ExtraParams map[string]string` field to `OAuthConfig` to allow passing
arbitrary query parameters to authorization and token endpoints.

## Use Case
Runlayer (https://anysource.io) requires RFC 8707 resource indicators for
multi-tenant OAuth authentication to MCP servers.

## Backward Compatibility
Fully backward compatible - `ExtraParams` is optional and defaults to nil/empty.

## Testing
- Unit tests for parameter injection
- Integration tests with mock OAuth server
- RFC 8707 resource indicator example
```

## Testing Strategy

### Unit Tests

**config_test.go**:
```go
func TestOAuthConfig_ExtraParams(t *testing.T) {
    tests := []struct {
        name        string
        config      string
        wantParams  map[string]string
        wantErr     bool
    }{
        {
            name: "valid extra params",
            config: `{
                "oauth": {
                    "extra_params": {
                        "resource": "https://example.com/mcp",
                        "audience": "mcp-api"
                    }
                }
            }`,
            wantParams: map[string]string{
                "resource": "https://example.com/mcp",
                "audience": "mcp-api",
            },
        },
        {
            name: "empty extra params",
            config: `{"oauth": {}}`,
            wantParams: nil,
        },
        {
            name: "reserved parameter rejected",
            config: `{
                "oauth": {
                    "extra_params": {
                        "client_id": "override"
                    }
                }
            }`,
            wantErr: true,
        },
    }
    // ... test implementation ...
}
```

**transport_wrapper_test.go**:
```go
func TestOAuthWrapper_AuthorizationURL(t *testing.T) {
    baseURL := "https://oauth.example.com/authorize?client_id=abc&state=xyz"
    extraParams := map[string]string{
        "resource": "https://api.example.com",
        "audience": "api",
    }

    wrapper := NewOAuthTransportWrapper(nil, extraParams)
    modifiedURL := wrapper.ModifyAuthorizationURL(baseURL)

    u, _ := url.Parse(modifiedURL)
    assert.Equal(t, "https://api.example.com", u.Query().Get("resource"))
    assert.Equal(t, "api", u.Query().Get("audience"))
    assert.Equal(t, "abc", u.Query().Get("client_id")) // Original preserved
}
```

### Integration Tests

**e2e_oauth_test.go**:
```go
func TestOAuth_WithResourceParameter(t *testing.T) {
    // Mock OAuth server that requires resource parameter
    mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/authorize" {
            resource := r.URL.Query().Get("resource")
            if resource == "" {
                w.WriteHeader(http.StatusBadRequest)
                json.NewEncoder(w).Encode(map[string]string{
                    "error": "resource parameter required",
                })
                return
            }
            // ... complete OAuth flow ...
        }
    }))
    defer mockServer.Close()

    // Test MCPProxy with extra_params config
    config := fmt.Sprintf(`{
        "oauth": {
            "extra_params": {
                "resource": "%s/mcp"
            }
        }
    }`, mockServer.URL)

    // ... test OAuth flow completes successfully ...
}
```

## Migration Path

### Backward Compatibility

**No breaking changes**:
- `extra_params` is optional field
- Existing configs work unchanged
- OAuth flow unchanged when no extra params

**Config Evolution**:
```json
// Phase 1: No OAuth
{
  "name": "slack",
  "url": "https://example.com/mcp"
}

// Phase 2: Basic OAuth (current)
{
  "name": "slack",
  "url": "https://example.com/mcp",
  "oauth": {}
}

// Phase 3: OAuth with extra params (proposed)
{
  "name": "slack",
  "url": "https://example.com/mcp",
  "oauth": {
    "extra_params": {
      "resource": "https://example.com/mcp"
    }
  }
}
```

## Deployment Strategy

### Feature Flag
```go
// internal/config/features.go
type FeatureFlags struct {
    // ... existing flags ...
    EnableOAuthExtraParams bool `json:"enable_oauth_extra_params"`
}
```

Initial deployment with feature flag disabled by default:
```json
{
  "features": {
    "enable_oauth_extra_params": false
  }
}
```

### Rollout Phases

1. **Alpha (v1.x-alpha)**: Feature flag enabled, internal testing
2. **Beta (v1.x-beta)**: Feature flag enabled by default, community testing
3. **GA (v1.x)**: Feature flag removed, always enabled

## Success Criteria

- ✅ Runlayer Slack MCP server authenticates successfully
- ✅ Existing OAuth flows unchanged (regression tests pass)
- ✅ Config validation prevents reserved parameter overrides
- ✅ Extra params logged appropriately (masked if sensitive)
- ✅ Documentation covers common use cases
- ✅ Unit test coverage >90% for new code
- ✅ Integration tests cover RFC 8707 scenario

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| mcp-go doesn't accept upstream PR | Medium | Maintain wrapper indefinitely |
| Breaking changes in mcp-go OAuth | Medium | Pin mcp-go version, test upgrades |
| Users override reserved params | Low | Validation rejects at config load |
| Sensitive params logged | Medium | Implement masking for non-resource params |
| Wrapper complexity | Low | Comprehensive tests, clear docs |

## Future Enhancements

1. **Automatic Resource Detection**: Automatically set `resource` param to server URL if not specified
2. **Provider Presets**: Pre-configured extra_params for known providers (Runlayer, etc.)
3. **Parameter Templates**: Support variable substitution in extra_params (e.g., `${SERVER_URL}`)
4. **Token Endpoint Params**: Separate extra_params for token endpoint vs authorization endpoint

## Timeline

| Phase | Duration | Deliverable |
|-------|----------|-------------|
| **Phase 0: Diagnostics** | **2 days** | **OAuth status visibility + error reporting** |
| Phase 1: Config | 2 days | Config parsing + validation |
| Phase 2: Wrapper | 3 days | OAuth transport wrapper |
| Phase 3: Integration | 3 days | End-to-end OAuth flow |
| Phase 4: Testing | 4 days | Comprehensive test suite |
| Phase 5: Documentation | 2 days | User guides + API docs |
| Phase 6: Upstream PR | Parallel | mcp-go contribution |
| **Total** | **2-3 weeks** | Production-ready feature |

**Note**: Phase 0 is a prerequisite that provides immediate value by making OAuth issues visible and actionable before implementing the full fix.

## Open Questions

1. Should `extra_params` support token endpoint parameters separately?
   - **Decision**: Start with authorization endpoint only, add token support if needed

2. Should we auto-populate `resource` from server URL if not specified?
   - **Decision**: No auto-population initially, explicit is better than implicit

3. Should extra_params support environment variable substitution?
   - **Decision**: Not in MVP, consider for future enhancement

4. How to handle parameter conflicts with discovered metadata?
   - **Decision**: User-specified extra_params take precedence (explicit override)

## References

- RFC 8707: Resource Indicators - https://www.rfc-editor.org/rfc/rfc8707.html
- mcp-go repository - https://github.com/mark3labs/mcp-go
- OAuth 2.1 spec - https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-09
