# Runlayer OAuth Integration Investigation

**Date**: 2025-11-27
**Issue**: Runlayer Slack MCP server OAuth flow fails due to missing `resource` parameter
**Affected Component**: OAuth authentication for HTTP-based MCP servers

## Executive Summary

MCPProxy successfully detects OAuth requirements and initiates the OAuth flow for Runlayer's Slack MCP server, but the authorization fails because Runlayer's OAuth implementation requires a `resource` query parameter (per RFC 8707 - Resource Indicators) that MCPProxy cannot currently provide.

## Investigation Findings

### 1. Configuration Discovery ✅

**What Worked**:
- Empty `"oauth": {}` in config successfully signals OAuth requirement
- JSON parsing creates non-nil pointer: `serverConfig.OAuth != nil` returns `true`
- All OAuth detection points work correctly:
  - `IsOAuthConfigured()` (oauth/config.go:658)
  - `diagnostics.go` line 39: `hasOAuth := srvRaw["oauth"] != nil`
  - `auth status` command detection

**Configuration Used**:
```json
{
  "name": "slack",
  "protocol": "streamable-http",
  "enabled": true,
  "url": "https://oauth.example.com/api/v1/proxy/00000000-0000-0000-0000-000000000000/mcp",
  "oauth": {}
}
```

### 2. OAuth Discovery ✅

**What Worked**:
MCPProxy successfully discovered OAuth metadata from:
```
https://oauth.example.com/.well-known/oauth-authorization-server
```

**Discovered Metadata**:
```json
{
  "issuer": "https://oauth.example.com/api/v1/oauth",
  "authorization_endpoint": "https://oauth.example.com/api/v1/oauth/authorize",
  "token_endpoint": "https://oauth.example.com/api/v1/oauth/token",
  "registration_endpoint": "https://oauth.example.com/api/v1/oauth/register",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"]
}
```

**OAuth Config Created**:
- Scopes: `[]` (empty - none discovered)
- PKCE: Enabled (S256)
- Redirect URI: `http://127.0.0.1:50461/oauth/callback`
- Dynamic client registration: Attempted

### 3. Authorization Flow Failure ❌

**Generated Authorization URL**:
```
https://oauth.example.com/api/v1/oauth/authorize?
  client_id=client_abc123def456&
  code_challenge=PKCE_CHALLENGE_EXAMPLE_REDACTED&
  code_challenge_method=S256&
  redirect_uri=http%3A%2F%2F127.0.0.1%3A50461%2Foauth%2Fcallback&
  response_type=code&
  state=STATE_EXAMPLE_REDACTED
```

**Server Error Response**:
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

**Root Cause**: Missing `resource` query parameter required by Runlayer's OAuth implementation.

### 4. Current Architecture Limitations

**config.OAuthConfig (internal/config/config.go:155-161)**:
```go
type OAuthConfig struct {
    ClientID     string   `json:"client_id,omitempty"`
    ClientSecret string   `json:"client_secret,omitempty"`
    RedirectURI  string   `json:"redirect_uri,omitempty"`
    Scopes       []string `json:"scopes,omitempty"`
    PKCEEnabled  bool     `json:"pkce_enabled,omitempty"`
    // ❌ No ExtraParams field
}
```

**mcp-go client.OAuthConfig (v0.42.0)**:
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
    // ❌ No ExtraParams field
}
```

**contracts.OAuthConfig (internal/contracts/types.go:47-54)**:
```go
type OAuthConfig struct {
    AuthURL      string            `json:"auth_url"`
    TokenURL     string            `json:"token_url"`
    ClientID     string            `json:"client_id"`
    Scopes       []string          `json:"scopes,omitempty"`
    ExtraParams  map[string]string `json:"extra_params,omitempty"` // ✅ Exists but marked TODO
    RedirectPort int               `json:"redirect_port,omitempty"`
}
```

### 5. RFC 8707 - Resource Indicators

**What is the `resource` parameter?**

RFC 8707 defines the `resource` parameter as a way to specify which resource server(s) the access token should be valid for. This is particularly useful in multi-tenant or proxy scenarios like Runlayer.

**Expected Value**:
```
resource=https://oauth.example.com/api/v1/proxy/00000000-0000-0000-0000-000000000000/mcp
```

The resource parameter should be the MCP endpoint URL itself, telling the OAuth server which specific MCP server the token should grant access to.

**Authorization URL with Resource**:
```
https://oauth.example.com/api/v1/oauth/authorize?
  client_id=client_abc123def456&
  resource=https%3A%2F%2Foauth.example.com%2Fapi%2Fv1%2Fproxy%2F00000000-0000-0000-0000-000000000000%2Fmcp&
  code_challenge=PKCE_CHALLENGE_EXAMPLE_REDACTED&
  code_challenge_method=S256&
  redirect_uri=http%3A%2F%2F127.0.0.1%3A50461%2Foauth%2Fcallback&
  response_type=code&
  state=STATE_EXAMPLE_REDACTED
```

## Behavioral Observations

### Background Retry Loop
Every ~30 seconds, the slack server:
1. Attempts to connect via `streamable-http` protocol
2. Tries MCP initialize without token → gets `401 Unauthorized`
3. Detects OAuth requirement → calls `CreateOAuthConfig()`
4. Sets up OAuth client successfully
5. Attempts MCP initialize with OAuth → fails with `"no valid token available, authorization required"`
6. **Defers OAuth flow** to prevent blocking: `"⏳ Deferring OAuth to prevent tray UI blocking"`
7. Transitions to `Error` state
8. Waits 30 seconds and retries

**Key Log Messages**:
```
INFO | 🌟 Starting OAuth authentication flow | {"scopes": [], "pkce_enabled": true}
INFO | 💡 OAuth login available via system tray menu
INFO | 🎯 OAuth authorization required during MCP init - deferring OAuth for background processing
WARN | Connection error, will attempt automatic reconnection | {"retry_count": 101}
```

### Manual Auth Trigger
Running `./mcpproxy auth login --server=slack`:
- Successfully launches browser
- Generates correct OAuth URL (except missing `resource`)
- Opens Runlayer's authorization page
- Fails with validation error for missing `resource` parameter

## Test Verification

### Test 1: JSON Parsing of Empty OAuth Object
```go
// Config: {"oauth": {}}
var cfg ServerConfig
json.Unmarshal([]byte(jsonEmpty), &cfg)
// Result: cfg.OAuth == nil → false ✅
// Result: cfg.OAuth → &{ClientID: ClientSecret: RedirectURI: Scopes:[] PKCEEnabled:false}
```

### Test 2: Upstream List Output
```bash
$ ./mcpproxy upstream list --output json | jq '.[] | select(.name == "slack")'
{
  "authenticated": false,
  "connected": false,
  "enabled": true,
  "name": "slack",
  "protocol": "",
  "quarantined": false,
  "reconnect_count": 101,
  "status": "connecting",
  "tool_count": 0
}
```

Note: No `oauth` field in output (API serialization issue?)

### Test 3: Auth Status Output
```bash
$ ./mcpproxy auth status
ℹ️  No servers with OAuth configuration found.
   Configure OAuth in mcp_config.json to enable authentication.
```

**Issue**: `auth status` doesn't detect the slack server as OAuth-enabled despite:
- Config file having `"oauth": {}`
- Logs showing OAuth setup happening
- `IsOAuthConfigured()` returning true in code

This suggests the API's `/api/v1/servers` endpoint isn't properly serializing OAuth config to the format expected by `auth status`.

## Dependencies

**mcp-go Library**:
- Version: v0.42.0
- Repository: github.com/mark3labs/mcp-go
- OAuth implementation: `client/transport/oauth.go`
- No `ExtraParams` support in current version

## References

- **RFC 8707**: Resource Indicators for OAuth 2.0 - https://www.rfc-editor.org/rfc/rfc8707.html
- **RFC 9728**: Protected Resource Metadata - https://www.rfc-editor.org/rfc/rfc9728.html
- **RFC 8414**: OAuth 2.0 Authorization Server Metadata - https://www.rfc-editor.org/rfc/rfc8414.html
- **OAuth 2.1 Draft**: https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-09

## Impact Assessment

**Affected Use Cases**:
1. Any MCP server hosted behind Runlayer's proxy
2. OAuth providers requiring RFC 8707 resource indicators
3. Multi-tenant OAuth scenarios where resource scoping is required

**Current Workarounds**:
None available without code changes. The OAuth flow cannot complete without the `resource` parameter.

## Next Steps

See `docs/plans/2025-11-27-oauth-extra-params.md` for implementation plan.
