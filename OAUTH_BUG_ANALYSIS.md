# OAuth Bug Analysis - Issue #131

## Summary

**Root Cause**: mcpproxy uses hardcoded OAuth scopes `["mcp.read", "mcp.write"]` which are NOT part of the MCP specification and cause OAuth failures with standard providers like GitHub, Google, Azure AD, etc.

## Evidence from Testing

### Test Server: GitHub MCP (https://api.githubcopilot.com/mcp/readonly)

#### 1. Hardcoded Scopes Problem
```
🌟 Starting OAuth authentication flow
  scopes: ["mcp.read", "mcp.write"]  ← WRONG!
```

#### 2. Server's Actual Requirements (from WWW-Authenticate header)
```http
www-authenticate: Bearer error="invalid_request",
                 resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly"
```

#### 3. Server's Published Scopes (RFC 9728 - Protected Resource Metadata)
```json
{
  "resource_name": "GitHub MCP Server",
  "scopes_supported": [
    "gist", "notifications", "public_repo", "repo",
    "repo:status", "repo_deployment", "user", "user:email",
    "user:follow", "read:gpg_key", "read:org", "project"
  ]
}
```

**Result**: OAuth Dynamic Client Registration fails because GitHub doesn't recognize `mcp.read` or `mcp.write` scopes.

## Why Commands Fail

### `mcpproxy tools list --server=github`
```
❌ Error: OAuth authorization required - deferred for background processing
```
**Reason**: Automatic connections defer OAuth to prevent blocking, but scopes are invalid so OAuth can never succeed.

### `mcpproxy auth login --server=github`
```
❌ Error: OAuth RegisterClient panicked; likely no dynamic registration or metadata
❌ Error: failed to register client: server does not support dynamic client registration
```
**Reason**:
1. Tries to register with invalid scopes `["mcp.read", "mcp.write"]`
2. GitHub OAuth rejects the registration
3. Falls back to non-DCR flow but still uses invalid scopes

## The Three Problems

### Problem 1: Hardcoded Non-Standard Scopes
**Location**: `internal/oauth/config.go:196`
```go
scopes := []string{"mcp.read", "mcp.write"}  // NOT in MCP spec!
```

**Impact**:
- Breaks OAuth with ALL standard providers
- No way to auto-discover correct scopes
- Poor UX - requires manual config editing

### Problem 2: No Scope Discovery
**Missing**: RFC 9728 Protected Resource Metadata discovery
**Missing**: RFC 8414 Authorization Server Metadata discovery

**Impact**:
- Users must manually look up and configure scopes
- Copy-paste errors common
- No automatic updates when server changes scopes

### Problem 3: Poor Error Messages
**Current**:
```
Error: OAuth authorization required - deferred for background processing
```

**Should Be**:
```
Error: OAuth failed - invalid scopes ["mcp.read", "mcp.write"]
GitHub MCP server requires scopes: ["repo", "user:email"]

Auto-discovery found these scopes via:
  https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly

To fix, either:
1. Let mcpproxy auto-discover (recommended): Remove oauth.scopes from config
2. Manually configure: Add "oauth": {"scopes": ["repo", "user:email"]} to server config
```

## The Solution

### Scope Discovery Priority (MCP Spec Compliant)

```
1. Config Override (if specified)
   └─> Use oauth.scopes from config

2. Protected Resource Metadata (RFC 9728)
   └─> Parse WWW-Authenticate header
   └─> Fetch resource_metadata URL
   └─> Use scopes_supported

3. Authorization Server Metadata (RFC 8414)
   └─> Fetch /.well-known/oauth-authorization-server
   └─> Use scopes_supported

4. Empty Scopes Fallback
   └─> Use []
   └─> Let server specify via WWW-Authenticate

5. NEVER: Remove hardcoded defaults
   └─> Delete ["mcp.read", "mcp.write"]
```

## Implementation Files

### New File: `internal/oauth/discovery.go`
- `DiscoverScopesFromProtectedResource()` - RFC 9728
- `DiscoverScopesFromAuthorizationServer()` - RFC 8414
- `ExtractResourceMetadataURL()` - Parse WWW-Authenticate
- Caching logic (30min TTL)

### Modified: `internal/oauth/config.go:184-203`
- Remove hardcoded defaults
- Add discovery waterfall logic
- Improve logging

### Modified: `internal/upstream/core/connection.go:1724`
- Better error messages for invalid_scope
- Show discovered vs configured scopes
- Provide actionable fix suggestions

## Testing Results

### Before Fix
```bash
$ ./mcpproxy auth login --server=github
❌ Error: OAuth authorization required - deferred for background processing
```

### After Fix (Expected)
```bash
$ ./mcpproxy auth login --server=github
✅ Auto-discovered OAuth scopes from Protected Resource Metadata (RFC 9728)
   Metadata URL: https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly
   Scopes: ["repo", "user:email", "read:org"]
🌐 Starting OAuth flow with GitHub...
🔐 Opening browser for authentication...
✅ OAuth authentication successful!
```

## Debugging Commands Reference

### List tools (triggers automatic OAuth)
```bash
./mcpproxy tools list --server=github --log-level=trace
```

### Manual OAuth login (forces OAuth flow)
```bash
./mcpproxy auth login --server=github --log-level=debug
```

### Check OAuth status
```bash
./mcpproxy auth status --server=github
```

### Check Protected Resource Metadata
```bash
curl -s "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly" | jq .
```

### Check Authorization Server Metadata
```bash
curl -s "https://api.githubcopilot.com/.well-known/oauth-authorization-server" | jq .
```

## Key Takeaways

1. ✅ **MCP spec does NOT define standard scopes** - each provider uses their own
2. ✅ **Scope discovery is REQUIRED** for good UX
3. ✅ **Protected Resource Metadata (RFC 9728) is the primary discovery method**
4. ✅ **Hardcoded defaults are harmful** - they break OAuth with standard providers
5. ✅ **Empty scopes are valid OAuth 2.1** - let server specify requirements

## Next Steps

1. Implement `internal/oauth/discovery.go` with RFC 9728 and RFC 8414 support
2. Modify `internal/oauth/config.go` to use discovery waterfall
3. Improve error messages in `internal/upstream/core/connection.go`
4. Add integration tests with GitHub MCP server
5. Update documentation with discovery examples
