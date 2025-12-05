# OAuth Observability Research

**Feature**: OAuth Extra Parameters Support
**Date**: 2025-11-30
**Purpose**: Research best practices for displaying OAuth information for debuggability

## Research Goal

Determine optimal locations to display OAuth configuration details (including RFC 8707 resource parameters and other extra_params) for maximum debuggability during authentication troubleshooting.

## Current OAuth Observability

### 1. Auth Status Command (`mcpproxy auth status`)

**Current Output** (auth_cmd.go:184-250):
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔐 OAuth Authentication Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Server: runlayer-slack
  Status: ✅ Authenticated
  Auth URL: https://provider.com/authorize
  Token URL: https://provider.com/token
```

**What's Missing**:
- Extra params configuration (e.g., RFC 8707 `resource`)
- Scopes configured/granted
- Token expiration time
- PKCE status (enabled/disabled)
- Redirect URI used
- OAuth discovery metadata URLs

---

### 2. Auth Login Command (`mcpproxy auth login`)

**Current Behavior** (auth_cmd.go:115-132):
- Opens browser with OAuth URL
- URL is not displayed to user (only visible in debug logs)
- No confirmation of which params are being sent

**What's Missing**:
- Authorization URL preview (with extra params visible)
- Confirmation that extra params are included
- Success message showing what was configured

---

### 3. Debug Logs

**Current Logging** (oauth/config.go:186-191):
```go
logger.Error("🚨 OAUTH CONFIG CREATION CALLED - THIS SHOULD APPEAR IN LOGS",
    zap.String("server", serverConfig.Name),
    zap.String("url", serverConfig.URL))

logger.Debug("Creating OAuth config for dynamic registration",
    zap.String("server", serverConfig.Name))
```

**What's Missing**:
- Extra params logging (when configured)
- Authorization URL with params (before browser opens)
- Token request body parameters (for debugging provider rejections)
- RFC 8707 compliance indicators

---

### 4. Doctor Command (`mcpproxy doctor`)

**Current Behavior**: General health checks

**What's Missing**:
- OAuth-specific diagnostics
- Extra params validation check
- RFC 8707 compliance check (resource param present when needed)
- Suggestions for missing extra params based on provider errors

---

## OAuth Observability Best Practices

### Industry Standards

**Source**: OAuth 2.0 Debugging Best Practices (RFC 6749 Section 10.16, OWASP OAuth Cheat Sheet)

**Recommendations**:
1. **Authorization URL Visibility**: Always show the full authorization URL being used
   - **Rationale**: Helps developers verify parameters are correctly included
   - **Security**: Mask sensitive query params (not resource URLs)

2. **Token Endpoint Logging**: Log token requests (without secrets)
   - **Rationale**: Debugging provider rejections requires seeing what was sent
   - **Security**: Never log `client_secret`, `refresh_token`, or access tokens

3. **Configuration Display**: Show active OAuth config in status commands
   - **Rationale**: Confirms configuration is loaded correctly
   - **Security**: Mask secrets, show public config (scopes, redirect_uri, extra_params)

4. **Error Context**: Include full error responses from OAuth provider
   - **Rationale**: Provider error messages often indicate missing params
   - **Security**: Safe to log (errors don't contain secrets)

---

### Comparison with Other Tools

#### GitHub CLI (`gh auth status`)
```
✓ Logged in to github.com as username (oauth_token)
✓ Token: *******************
✓ Scopes: repo, read:org, gist
```

**Lessons**:
- Shows token type (masked)
- Lists granted scopes
- Simple ✓/✗ status indicators

#### AWS CLI (`aws configure list`)
```
      Name                    Value             Type    Location
      ----                    -----             ----    --------
   profile                <not set>             None    None
access_key     ****************ABCD shared-credentials-file
```

**Lessons**:
- Masks credentials (shows last 4 chars)
- Shows configuration source
- Tabular format for multiple items

#### Heroku CLI (`heroku authorizations:info`)
```
ID:          12345678-abcd-1234-abcd-123456789012
Description: heroku-cli
Scope:       global
Created at:  2023-01-15T14:30:00Z
Updated at:  2023-11-30T10:15:00Z
```

**Lessons**:
- Shows timestamp information
- Displays scope details
- Clear key-value format

---

## Proposed OAuth Observability Design

### 1. Enhanced `auth status` Output

**New Format**:
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔐 OAuth Authentication Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Server: runlayer-slack
  Status: ✅ Authenticated

  Configuration:
    Client ID: abc***789 (masked)
    Redirect URI: http://127.0.0.1:45123/oauth/callback
    Scopes: read, write, admin
    PKCE: ✅ Enabled (S256)

  Extra Parameters (RFC 8707):
    resource: https://oauth.runlayer.com/api/v1/proxy/UUID/mcp
    audience: mcp-api

  Endpoints:
    Authorization: https://provider.com/authorize
    Token: https://provider.com/token

  Token Status:
    Expires: 2025-12-01 14:30:00 UTC (in 23h 15m)
    Last Refreshed: 2025-11-30 09:00:00 UTC
```

**Implementation Location**: `cmd/mcpproxy/auth_cmd.go:runAuthStatusClientMode()`

**Data Source**: Extend `/api/v1/servers` response to include OAuth config details

---

### 2. Enhanced `auth login` Output

**Before Opening Browser**:
```
🔐 Starting OAuth authentication for server: runlayer-slack

Configuration Preview:
  Provider: https://oauth.runlayer.com
  Scopes: read, write
  PKCE: ✅ Enabled
  Extra Params: resource=https://oauth.runlayer.com/api/v1/proxy/UUID/mcp

Opening authorization URL in browser...
URL: https://provider.com/authorize?response_type=code&client_id=abc123&...&resource=https%3A%2F%2Foauth.runlayer.com%2F...

⏳ Waiting for authorization callback...
```

**After Successful Auth**:
```
✅ OAuth login successful!

Token Details:
  Access Token: ey***xyz (masked, expires in 1h)
  Refresh Token: ✅ Available
  Scopes Granted: read, write, admin

Configuration Verified:
  ✅ PKCE code verifier validated
  ✅ State parameter matched
  ✅ Extra params accepted by provider
```

**Implementation Location**: `cmd/mcpproxy/auth_cmd.go:runAuthLoginClientMode()` and standalone mode

---

### 3. Debug Logging Enhancements

**Authorization Request** (DEBUG level):
```
DEBUG OAuth authorization request
  server=runlayer-slack
  auth_url=https://provider.com/authorize?response_type=code&client_id=abc123&state=xyz&resource=https%3A%2F%2Foauth.runlayer.com%2F...
  extra_params={"resource":"https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"}
  pkce=true
  scopes=[read,write]
```

**Token Exchange Request** (DEBUG level):
```
DEBUG OAuth token exchange request
  server=runlayer-slack
  token_url=https://provider.com/token
  grant_type=authorization_code
  extra_params={"resource":"https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"}
  pkce_verifier=***masked***
```

**Token Refresh** (DEBUG level):
```
DEBUG OAuth token refresh
  server=runlayer-slack
  token_url=https://provider.com/token
  extra_params={"resource":"https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"}
  expires_in=3600s
```

**Implementation Locations**:
- `internal/oauth/transport_wrapper.go` (wrapper logging)
- `internal/transport/http.go` (OAuth client creation)

---

### 4. Doctor Command OAuth Diagnostics

**New Section** in `mcpproxy doctor`:
```
🔐 OAuth Configuration Health

✅ runlayer-slack
  Status: Authenticated (token expires in 23h)
  Extra Params: ✅ Configured (RFC 8707 resource present)
  PKCE: ✅ Enabled

⚠️  github-server
  Status: Authentication Failed
  Error: "invalid_request: Required parameter 'resource' is missing"

  💡 Suggested Fix:
     Add RFC 8707 resource parameter to OAuth config:

     "oauth": {
       "client_id": "your-client-id",
       "extra_params": {
         "resource": "https://example.com/mcp"
       }
     }
```

**Implementation Location**: `cmd/mcpproxy/doctor_cmd.go` (new OAuth health check section)

---

## Logging Levels

### TRACE (Most Verbose)
- Full OAuth URLs (including state, code_challenge)
- Full token request bodies (secrets masked)
- Raw HTTP requests/responses

### DEBUG (Recommended for Troubleshooting)
- Authorization URL with extra params
- Token request with params (secrets masked)
- Configuration details
- Extra params injection points

### INFO (Default)
- OAuth flow start/complete
- Token expiration warnings
- Configuration load confirmation

### WARN
- Missing recommended params (e.g., PKCE not enabled)
- Token near expiration
- Extra params config issues (reserved param override)

### ERROR
- OAuth failures with provider error details
- Configuration validation errors
- Token refresh failures

---

## Security Considerations

### What to Mask in Logs

**MUST Mask** (Security Risk):
- `client_secret`
- `code_verifier` (PKCE)
- `access_token`
- `refresh_token`
- `authorization_code`
- Any extra param named `api_key`, `token`, `secret`, `password`

**CAN Show** (Public Information):
- `client_id` (can show last 4 chars: `abc***789`)
- `resource` parameter (public endpoint URL)
- `audience` parameter (public identifier)
- `scope` values
- `redirect_uri`
- Authorization/token endpoint URLs
- `state` parameter (random, no sensitive data)

### Masking Strategy

```go
func maskOAuthSecret(secret string) string {
    if len(secret) <= 8 {
        return "***"
    }
    // Show first 3 and last 4 chars
    return secret[:3] + "***" + secret[len(secret)-4:]
}

func maskExtraParams(params map[string]string) map[string]string {
    masked := make(map[string]string)
    for k, v := range params {
        key := strings.ToLower(k)
        // Show resource URLs (public endpoints)
        if strings.HasPrefix(key, "resource") || key == "audience" {
            masked[k] = v
        } else if strings.Contains(key, "key") || strings.Contains(key, "secret") || strings.Contains(key, "token") {
            masked[k] = "***" // Likely sensitive
        } else {
            masked[k] = maskOAuthSecret(v) // Default: partial masking
        }
    }
    return masked
}
```

---

## Implementation Checklist

### Phase 1: Auth Status Enhancements
- [ ] Add extra_params to `/api/v1/servers` response
- [ ] Update `auth status` output format (new sections)
- [ ] Add OAuth config display (scopes, PKCE, extra params)
- [ ] Add token status display (expiration, last refresh)
- [ ] Add masking for sensitive fields

### Phase 2: Auth Login Enhancements
- [ ] Display configuration preview before browser opens
- [ ] Show authorization URL with extra params
- [ ] Add success message with token details
- [ ] Add verification summary (PKCE, state, extra params)

### Phase 3: Debug Logging
- [ ] Add DEBUG logs for authorization URL construction
- [ ] Add DEBUG logs for extra params injection
- [ ] Add DEBUG logs for token requests (exchange & refresh)
- [ ] Implement selective masking (resource visible, secrets masked)

### Phase 4: Doctor Command
- [ ] Add OAuth health check section
- [ ] Check for extra params when provider requires them
- [ ] Provide RFC 8707 configuration suggestions
- [ ] Detect common OAuth misconfigurations

---

## References

- **OAuth 2.0 Security Best Practices**: https://datatracker.ietf.org/doc/html/draft-ietf-oauth-security-topics
- **RFC 8707 (Resource Indicators)**: https://www.rfc-editor.org/rfc/rfc8707.html
- **OWASP OAuth Cheat Sheet**: https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html
- **GitHub CLI Source**: https://github.com/cli/cli (auth commands)
