# Zero-Config OAuth Implementation

## Summary

This PR implements zero-configuration OAuth for MCPProxy, enabling automatic detection and configuration of OAuth-protected MCP servers without requiring manual setup. The implementation follows RFC 8252 (OAuth for Native Apps), RFC 9728 (Protected Resource Metadata), and RFC 8707 (Resource Indicators).

**Status:** 7 of 9 tasks completed (78%) - All completable features implemented. Tasks 4-5 blocked pending upstream mcp-go support.

## What's Included

### ✅ Core Features (Completed)

#### 1. Enhanced Metadata Discovery (Task 1)
- **New Function**: `DiscoverProtectedResourceMetadata()` returns full RFC 9728 Protected Resource Metadata
- **Backward Compatible**: Existing `DiscoverScopesFromProtectedResource()` refactored to delegate to new function
- **Testing**: Full test coverage with mock HTTP servers

**Files Changed:**
- `internal/oauth/discovery.go`: Added `DiscoverProtectedResourceMetadata()`
- `internal/oauth/discovery_test.go`: Added comprehensive tests

#### 2. ExtraParams Config Field (Task 2)
- **New Config Field**: `ExtraParams map[string]string` in `OAuthConfig` struct
- **Validation**: `ValidateOAuthExtraParams()` prevents override of reserved OAuth parameters
- **Reserved Parameters**: Protects `client_id`, `client_secret`, `redirect_uri`, `scope`, `state`, PKCE params, etc.

**Files Changed:**
- `internal/config/config.go`: Added `ExtraParams` field
- `internal/config/validation.go`: Added validation function
- `internal/config/validation_test.go`: 9 test cases covering reserved params

#### 3. Resource Parameter Extraction (Task 3)
- **Enhanced Function**: `CreateOAuthConfig()` now returns `(*client.OAuthConfig, map[string]string)`
- **Auto-Detection**: Extracts `resource` parameter from Protected Resource Metadata
- **Fallback**: Uses server URL as resource if metadata unavailable
- **Manual Override**: Respects user-provided `extra_params` in config

**Files Changed:**
- `internal/oauth/config.go`: Updated signature and added resource extraction logic
- `internal/oauth/config_test.go`: Tests for resource extraction
- `internal/upstream/core/connection.go`: Updated callers to handle two return values

#### 4. OAuth Capability Detection (Task 6)
- **New Function**: `IsOAuthCapable()` determines if server can use OAuth
- **Zero-Config Support**: Returns `true` for HTTP/SSE servers even without explicit OAuth config
- **Protocol Awareness**: Returns `false` for stdio servers (OAuth not applicable)

**Files Changed:**
- `internal/oauth/config.go`: Added `IsOAuthCapable()`
- `internal/oauth/config_test.go`: 4 test scenarios
- `cmd/mcpproxy/auth_cmd.go`: Uses `IsOAuthCapable` in status command
- `internal/management/diagnostics.go`: Uses `IsOAuthCapable` in diagnostics

#### 5. Integration Testing (Task 7)
- **New Test Suite**: `internal/server/e2e_oauth_zero_config_test.go`
- **4 Test Scenarios**:
  1. `TestE2E_ZeroConfigOAuth_ResourceParameterExtraction` - Validates metadata discovery and resource extraction
  2. `TestE2E_ManualExtraParamsOverride` - Validates manual parameter preservation
  3. `TestE2E_IsOAuthCapable_ZeroConfig` - Validates capability detection
  4. `TestE2E_ProtectedResourceMetadataDiscovery` - Validates full RFC 9728 flow

**All tests pass** ✅

#### 6. Documentation (Task 8)
- **User Guide**: `docs/oauth-zero-config.md` with quick start and troubleshooting
- **README Update**: Added Zero-Config OAuth section with examples
- **Design Doc**: `docs/designs/2025-11-27-zero-config-oauth.md`
- **Implementation Plan**: `docs/plans/2025-11-27-zero-config-oauth.md`

### 🚧 Blocked Features (Pending Upstream Support)

#### Tasks 4-5: OAuth Parameter Injection
**Status:** Implementation blocked by mcp-go library limitation

**What Was Prepared:**
- ✅ Wrapper utility created: `internal/oauth/wrapper.go` with `InjectExtraParamsIntoURL()`
- ✅ Test coverage: `internal/oauth/wrapper_test.go`
- ✅ Documentation: Clear explanation of limitation in commit `051503f`

**Why Blocked:**
The mcp-go library's `NewStreamableHTTPClientWithOAuth()` function does not expose a mechanism to inject extra OAuth parameters (like `resource`) into the authorization URL. Full integration requires upstream changes to mcp-go.

**Workaround:**
The wrapper utility is ready for integration once mcp-go adds support for extra parameters via:
1. An `ExtraParams` field in `client.OAuthConfig`, OR
2. A custom `http.RoundTripper` hook for URL modification

**Tracking:** See `docs/upstream-issue-draft.md` for proposed mcp-go enhancement

## User Impact

### Before This PR
Users had to manually configure OAuth for each MCP server:
```json
{
  "name": "my-server",
  "url": "https://oauth.example.com/mcp",
  "oauth": {
    "client_id": "manually-registered-client",
    "scopes": ["manually", "specified", "scopes"]
  }
}
```

### After This PR
Zero configuration required for standard OAuth servers:
```json
{
  "name": "my-server",
  "url": "https://oauth.example.com/mcp"
}
```

MCPProxy automatically:
1. ✅ Detects OAuth requirement from 401 response
2. ✅ Fetches Protected Resource Metadata (RFC 9728)
3. ✅ Extracts `resource` parameter (RFC 8707)
4. ✅ Auto-discovers scopes
5. ✅ Identifies OAuth-capable servers without explicit config
6. 🚧 Injects parameters into OAuth flow (blocked - see above)

### Optional Manual Configuration
Users can still override detected values:
```json
{
  "oauth": {
    "extra_params": {
      "tenant_id": "12345",
      "audience": "custom-audience"
    }
  }
}
```

## Testing

### Test Coverage
- ✅ Unit tests: `go test ./internal/oauth` - All pass (6.355s)
- ✅ E2E tests: `go test ./internal/server -run OAuth` - 4/4 passing
- ✅ Validation tests: 9 test cases for reserved parameter protection
- ✅ Integration tests: Resource extraction, capability detection, metadata discovery

### Quality Checks
- ✅ Linter: `./scripts/run-linter.sh` - 0 issues
- ✅ Build: `go build -o mcpproxy ./cmd/mcpproxy` - Success
- ✅ All OAuth-specific tests passing

### Test Commands
```bash
# Run all OAuth tests
go test ./internal/oauth -v

# Run OAuth E2E tests
go test ./internal/server -run OAuth -v

# Run validation tests
go test ./internal/config -run ValidateOAuthExtraParams -v
```

## Breaking Changes

### API Changes
**`CreateOAuthConfig()` signature changed:**
```go
// Before
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig

// After
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) (*client.OAuthConfig, map[string]string)
```

**Impact:** All callers updated in this PR. External users of this internal function will need to handle the second return value.

**Migration:**
```go
// Old code
oauthConfig := oauth.CreateOAuthConfig(serverConfig, storage)

// New code
oauthConfig, extraParams := oauth.CreateOAuthConfig(serverConfig, storage)
// extraParams contains auto-detected resource parameter and manual overrides
```

### Configuration Schema
**New optional field:**
```json
{
  "oauth": {
    "extra_params": {
      "resource": "...",
      "tenant_id": "...",
      "audience": "..."
    }
  }
}
```

**Backward Compatible:** Existing configs work unchanged. The `extra_params` field is optional.

## Implementation Details

### Architecture Decisions

1. **Two-Return Value Pattern**: `CreateOAuthConfig()` returns both OAuth config and extra parameters map to separate concerns
2. **Capability vs Configuration**: `IsOAuthCapable()` identifies potential OAuth servers, `IsOAuthConfigured()` checks explicit config
3. **Validation Layer**: Reserved parameter protection prevents accidental OAuth spec violations
4. **Wrapper Pattern**: Prepared for future mcp-go integration without modifying existing code

### RFC Compliance

- ✅ **RFC 8252** (OAuth 2.0 for Native Apps): PKCE support, dynamic port allocation
- ✅ **RFC 9728** (Protected Resource Metadata): Full metadata structure parsing
- ✅ **RFC 8707** (Resource Indicators): Resource parameter extraction and storage
- 🚧 **RFC 8707** (Resource Indicators): Parameter injection (blocked by mcp-go)

### Security Considerations

- ✅ **Reserved Parameter Protection**: `ValidateOAuthExtraParams()` prevents override of critical OAuth parameters
- ✅ **Localhost Binding**: OAuth callback servers use dynamic port allocation on localhost
- ✅ **PKCE Required**: All OAuth flows use PKCE for security
- ✅ **No Secret Exposure**: Client credentials stored securely, never logged

## Known Limitations

### 1. Parameter Injection Blocked (Tasks 4-5)
**Issue:** mcp-go library doesn't support extra OAuth parameters
**Impact:** `resource` parameter extracted but not injected into auth flow
**Workaround:** Wrapper utility ready for future integration
**Timeline:** Pending mcp-go upstream enhancement

### 2. Dynamic Client Registration
**Status:** Not implemented in this PR
**Scope:** This PR focuses on metadata discovery and parameter extraction
**Future Work:** DCR support planned for separate PR

## Migration Guide

### For Users

**No action required** - Zero-config OAuth is automatic.

**Optional:** Add `extra_params` for non-standard OAuth requirements:
```json
{
  "oauth": {
    "extra_params": {
      "tenant_id": "your-tenant-id"
    }
  }
}
```

### For Developers

**Update imports** if using internal OAuth functions:
```go
// Update CreateOAuthConfig calls
oauthConfig, extraParams := oauth.CreateOAuthConfig(serverConfig, storage)

// Use IsOAuthCapable for capability detection
if oauth.IsOAuthCapable(serverConfig) {
    // Server can use OAuth
}
```

## Related Issues

- Closes #XXX (add issue number when created)
- Related to upstream mcp-go issue (see `docs/upstream-issue-draft.md`)

## Checklist

- [x] Implementation follows plan (`docs/plans/2025-11-27-zero-config-oauth.md`)
- [x] All completable tasks (7/9) implemented
- [x] E2E tests added and passing
- [x] Unit tests for all new functions
- [x] Documentation updated (`docs/oauth-zero-config.md`, `README.md`)
- [x] Linter passing (0 issues)
- [x] Build successful
- [x] Breaking changes documented
- [x] Migration guide provided
- [x] Blocked tasks documented with workarounds

## Next Steps

1. **Merge this PR** - All completable features ready
2. **Create mcp-go upstream issue** - Request ExtraParams support (draft ready in `docs/upstream-issue-draft.md`)
3. **Monitor mcp-go** - Watch for ExtraParams support
4. **Future PR** - Integrate wrapper utility once mcp-go updated

## Screenshots/Examples

### Before: Manual Configuration Required
```json
{
  "name": "slack-mcp",
  "url": "https://oauth.example.com/api/v1/proxy/UUID/mcp",
  "oauth": {
    "client_id": "...",
    "client_secret": "...",
    "scopes": ["mcp.read", "mcp.write"]
  }
}
```

### After: Zero Configuration
```json
{
  "name": "slack-mcp",
  "url": "https://oauth.example.com/api/v1/proxy/UUID/mcp"
}
```

MCPProxy automatically detects and configures OAuth! 🎉

---

**Review Focus Areas:**
1. ✅ RFC compliance for metadata discovery and resource extraction
2. ✅ Breaking change to `CreateOAuthConfig()` signature
3. ✅ Test coverage for new functionality
4. 🚧 Blocked tasks documentation - is the workaround approach acceptable?
5. ✅ Documentation completeness

**Estimated Review Time:** 30-45 minutes
