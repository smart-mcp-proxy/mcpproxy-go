# Draft GitHub Issue for mcp-go Repository

**Repository**: https://github.com/mark3labs/mcp-go
**Title**: Add support for extra OAuth parameters (RFC 8707 Resource Indicators)

---

## Issue Description

### Problem Statement

The `client.OAuthConfig` struct currently does not support passing additional query parameters to OAuth authorization and token endpoints beyond the standard OAuth 2.0 parameters. This prevents integration with OAuth providers that implement RFC 8707 (Resource Indicators) or other OAuth extensions that require custom parameters.

### Use Case

We're implementing an MCP proxy (MCPProxy) that needs to authenticate with MCP servers hosted behind OAuth gateways that require RFC 8707 resource indicators. These gateways require a `resource` query parameter in the authorization request to specify which resource server the access token should be valid for.

**Current authorization URL** (fails with validation error):
```
https://oauth.example.com/authorize?
  client_id=client_abc123&
  code_challenge=xyz...&
  code_challenge_method=S256&
  redirect_uri=http://127.0.0.1:50461/oauth/callback&
  response_type=code&
  state=abc123
```

**Required authorization URL** (works):
```
https://oauth.example.com/authorize?
  client_id=client_abc123&
  resource=https://api.example.com/mcp&
  code_challenge=xyz...&
  code_challenge_method=S256&
  redirect_uri=http://127.0.0.1:50461/oauth/callback&
  response_type=code&
  state=abc123
```

**Error response from OAuth provider**:
```json
{
  "detail": [
    {
      "type": "missing",
      "loc": ["query", "resource"],
      "msg": "Field required",
      "input": null
    }
  ]
}
```

### Current Workarounds

There is currently no way to add the `resource` parameter without modifying mcp-go source code. The `OAuthConfig` struct only supports standard OAuth 2.0 fields.

## Proposed Solution

Add an `ExtraParams map[string]string` field to the `OAuthConfig` struct to allow passing arbitrary query parameters to OAuth endpoints.

### Implementation

**Before** (`client/transport/oauth.go`):
```go
type OAuthConfig struct {
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	Scopes                []string
	TokenStore            TokenStore
	AuthServerMetadataURL string
	PKCEEnabled           bool
	HTTPClient            *http.Client
}
```

**After**:
```go
type OAuthConfig struct {
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	Scopes                []string
	TokenStore            TokenStore
	AuthServerMetadataURL string
	PKCEEnabled           bool
	HTTPClient            *http.Client
	ExtraParams           map[string]string // NEW
}
```

### Usage Example

**RFC 8707 Resource Indicators**:
```go
config := &client.OAuthConfig{
    ClientID:    "client_abc123",
    RedirectURI: "http://127.0.0.1:8080/oauth/callback",
    Scopes:      []string{"read", "write"},
    PKCEEnabled: true,
    ExtraParams: map[string]string{
        "resource": "https://api.example.com/mcp",
    },
}

client, err := client.NewOAuthStreamableHttpClient(serverURL, *config)
```

**Multiple Extra Parameters**:
```go
config := &client.OAuthConfig{
    ClientID:    "client_abc123",
    RedirectURI: "http://127.0.0.1:8080/oauth/callback",
    PKCEEnabled: true,
    ExtraParams: map[string]string{
        "resource": "https://api.example.com/mcp",
        "audience": "mcp-api",
        "prompt":   "consent",
    },
}
```

### Implementation Details

The extra parameters should be:
1. **Added to authorization URL**: Appended to the query string when constructing the authorization URL
2. **Added to token request**: Included in the token exchange request body
3. **Validated**: Should not allow overriding reserved OAuth 2.0 parameters (client_id, redirect_uri, etc.)
4. **Optional**: Empty/nil map should work exactly as current behavior

### Reserved Parameters (Should Not Be Overridable)

The implementation should reject or ignore attempts to override these standard OAuth parameters:
- `client_id`
- `client_secret`
- `redirect_uri`
- `response_type`
- `scope`
- `state`
- `code_challenge`
- `code_challenge_method`
- `grant_type`
- `code`
- `refresh_token`

## Benefits

1. **RFC 8707 Compliance**: Enables integration with OAuth providers requiring resource indicators
2. **OAuth Extensions**: Supports custom OAuth extensions without code changes
3. **Multi-tenant Auth**: Enables authentication with multi-tenant OAuth gateways
4. **Future-proof**: Allows adopting new OAuth specifications without library updates
5. **Backward Compatible**: Existing code continues to work unchanged

## Related Standards

- **RFC 8707**: Resource Indicators for OAuth 2.0 - https://www.rfc-editor.org/rfc/rfc8707.html
  - Defines the `resource` parameter for specifying target resource servers
  - Used by multi-tenant OAuth providers and API gateways

- **OAuth 2.0 Multiple Response Types**: Some providers use custom `response_type` combinations

- **Custom Parameters**: Various OAuth providers (Azure AD, Okta, Auth0) support provider-specific parameters

## Alternative Approaches Considered

### 1. Hardcode Resource Parameter
❌ Not flexible enough for other use cases

### 2. Modify Authorization URL After Creation
❌ No access to URL construction internals

### 3. Fork mcp-go
❌ Maintenance burden, misses community benefits

### 4. Add ExtraParams (Proposed)
✅ Clean, flexible, backward compatible

## Testing Considerations

Example test cases to include:

```go
func TestOAuthConfig_ExtraParams(t *testing.T) {
    tests := []struct {
        name        string
        extraParams map[string]string
        wantInURL   map[string]string
    }{
        {
            name: "RFC 8707 resource indicator",
            extraParams: map[string]string{
                "resource": "https://api.example.com",
            },
            wantInURL: map[string]string{
                "resource": "https://api.example.com",
            },
        },
        {
            name: "multiple extra parameters",
            extraParams: map[string]string{
                "resource": "https://api.example.com",
                "audience": "api",
            },
            wantInURL: map[string]string{
                "resource": "https://api.example.com",
                "audience": "api",
            },
        },
        {
            name:        "empty extra params",
            extraParams: nil,
            wantInURL:   nil,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            config := &OAuthConfig{
                ClientID:    "test",
                ExtraParams: tt.extraParams,
            }

            // Test that extra params appear in authorization URL
            authURL := config.BuildAuthorizationURL()
            for key, expectedValue := range tt.wantInURL {
                actualValue := extractQueryParam(authURL, key)
                assert.Equal(t, expectedValue, actualValue)
            }
        })
    }
}
```

## Impact Assessment

### Breaking Changes
**None** - This is a purely additive change:
- New optional field defaults to nil/empty
- Existing code works unchanged
- No changes to function signatures

### Affected Components
- `client/transport/oauth.go`: OAuthConfig struct
- OAuth URL construction logic
- OAuth token request construction

### Migration Path
Existing code requires **no changes**:

```go
// Before (still works)
config := &client.OAuthConfig{
    ClientID:    "client_abc123",
    PKCEEnabled: true,
}

// After (with extra params)
config := &client.OAuthConfig{
    ClientID:    "client_abc123",
    PKCEEnabled: true,
    ExtraParams: map[string]string{
        "resource": "https://api.example.com",
    },
}
```

## Implementation Checklist

- [ ] Add `ExtraParams map[string]string` field to `OAuthConfig`
- [ ] Update authorization URL construction to include extra params
- [ ] Update token request construction to include extra params
- [ ] Add validation to prevent overriding reserved parameters
- [ ] Add unit tests for extra params behavior
- [ ] Add integration tests with mock OAuth server
- [ ] Update documentation and examples
- [ ] Verify backward compatibility with existing tests

## Timeline

We're happy to contribute this implementation via pull request. Estimated timeline:
- Implementation: 1-2 days
- Tests: 1 day
- Documentation: 1 day
- Total: 3-4 days

## Questions for Maintainers

1. Is this approach acceptable, or would you prefer a different design?
2. Should we add extra params to both authorization and token endpoints, or just authorization?
3. Should validation reject reserved parameter names, or just log a warning?
4. Any specific test coverage requirements?

## Context

We're building MCPProxy (smart-mcp-proxy/mcpproxy-go), a proxy server for MCP that needs to integrate with various OAuth providers. This feature would enable us (and other mcp-go users) to support a wider range of OAuth implementations without forking the library.

## Related Issues

_(Will search for any existing issues related to OAuth customization)_

---

**Labels**: enhancement, oauth, rfc-8707
**Priority**: Medium (blocks integration with certain OAuth providers)
