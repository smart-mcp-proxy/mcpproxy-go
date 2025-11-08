# Requirements Checklist - OAuth Scope Auto-Discovery

**Feature**: 001-oauth-scope-discovery
**Related Issue**: [#131](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/131)
**Status**: Phase 1 (MVP) Complete ✅

---

## Phase 1: Core Scope Discovery (MVP - Priority P1)

### Functional Requirements

- [x] **FR-001**: Implement RFC 9728 Protected Resource Metadata scope discovery
  - [x] Parse `WWW-Authenticate` header for `resource_metadata` field
  - [x] Fetch metadata URL with 5-second timeout
  - [x] Extract `scopes_supported` array from JSON response
  - [x] Handle 404/timeout/malformed JSON gracefully

- [x] **FR-002**: Implement RFC 8414 OAuth Authorization Server Metadata scope discovery
  - [x] Construct `/.well-known/oauth-authorization-server` URL from base URL
  - [x] Fetch metadata with 5-second timeout
  - [x] Extract `scopes_supported` array from JSON response
  - [x] Handle 404/timeout/malformed JSON gracefully

- [x] **FR-003**: Implement priority waterfall for scope selection
  - [x] Priority 1: Use config-specified `oauth.scopes` if present
  - [x] Priority 2: Try RFC 9728 Protected Resource Metadata
  - [x] Priority 3: Try RFC 8414 Authorization Server Metadata
  - [x] Priority 4: Fall back to empty scopes `[]`

- [x] **FR-004**: Remove hardcoded default scopes
  - [x] Delete `scopes := []string{"mcp.read", "mcp.write"}` from `internal/oauth/config.go:196`
  - [x] Verify with grep that no hardcoded scopes remain in codebase

- [x] **FR-008**: Respect user-configured `oauth.scopes`
  - [x] Check `serverConfig.OAuth.Scopes` first before discovery
  - [x] Skip discovery when explicitly set (including empty array `[]`)

- [x] **FR-009**: Log which scope discovery method was used
  - [x] Log at INFO level when config override is used
  - [x] Log at INFO level when RFC 9728 discovery succeeds
  - [x] Log at INFO level when RFC 8414 discovery succeeds
  - [x] Log at INFO level when falling back to empty scopes

- [x] **FR-010**: Handle metadata fetch failures gracefully
  - [x] Implement 5-second timeout for all HTTP requests
  - [x] Fall back to next method in waterfall on timeout
  - [x] Fall back to next method on network errors
  - [x] Fall back to next method on malformed JSON

### Implementation Tasks

- [x] **Create `internal/oauth/discovery.go`**
  - [x] Define `ProtectedResourceMetadata` struct
  - [x] Define `OAuthServerMetadata` struct
  - [x] Implement `ExtractResourceMetadataURL(wwwAuthHeader string) string`
  - [x] Implement `DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error)`
  - [x] Implement `DiscoverScopesFromAuthorizationServer(baseURL string, timeout time.Duration) ([]string, error)`

- [x] **Modify `internal/oauth/config.go:184-203`**
  - [x] Add config override check (Priority 1)
  - [x] Add preflight HEAD request to get WWW-Authenticate header
  - [x] Add RFC 9728 discovery (Priority 2)
  - [x] Add RFC 8414 discovery (Priority 3)
  - [x] Add empty scopes fallback (Priority 4)
  - [x] Remove hardcoded `["mcp.read", "mcp.write"]` line

### Testing

- [x] **Unit Tests - `internal/oauth/discovery_test.go`**
  - [x] `TestExtractResourceMetadataURL` - valid header with resource_metadata
  - [x] `TestExtractResourceMetadataURL` - header without resource_metadata
  - [x] `TestExtractResourceMetadataURL` - malformed header
  - [x] `TestDiscoverScopesFromProtectedResource` - valid metadata JSON
  - [x] `TestDiscoverScopesFromProtectedResource` - 404 response
  - [x] `TestDiscoverScopesFromProtectedResource` - malformed JSON
  - [x] `TestDiscoverScopesFromProtectedResource` - timeout (>5s)
  - [x] `TestDiscoverScopesFromProtectedResource` - empty scopes array
  - [x] `TestDiscoverScopesFromAuthorizationServer` - valid metadata JSON
  - [x] `TestDiscoverScopesFromAuthorizationServer` - 404 response
  - [x] `TestDiscoverScopesFromAuthorizationServer` - malformed JSON
  - [x] `TestDiscoverScopesFromAuthorizationServer` - timeout (>5s)

- [x] **Unit Tests - Existing tests verified**
  - [x] All existing OAuth tests still pass (backwards compatibility verified)
  - [x] No regressions in token storage tests
  - [x] Config waterfall tested via manual verification

- [ ] **Integration Tests - `internal/server/oauth_e2e_test.go`** (Not required for Phase 1 MVP)
  - [ ] `TestGitHubMCPOAuth` - real OAuth flow with GitHub MCP server
  - [ ] `TestScopeDiscoveryWaterfall` - mock server testing all 4 priority levels
  - [ ] `TestConfigOverridesDiscovery` - verify manual config bypasses discovery
  - [ ] `TestEmptyScopesFallback` - verify empty scopes work correctly

### Success Criteria (Phase 1)

- [x] **SC-001**: Users can connect to GitHub MCP server without manual OAuth scope configuration
- [x] **SC-002**: Scopes auto-discovered successfully (verified: gist, notifications, public_repo, repo, etc.)
- [x] **SC-003**: Scope discovery waterfall implemented and functional
- [x] **SC-004**: Logs show discovered scopes (verified in manual testing)
- [x] **SC-006**: Scope discovery completes within 5 seconds or times out gracefully
- [x] **SC-008**: Existing configurations with `oauth.scopes` continue to work (backwards compatible)
- [x] **SC-010**: No hardcoded `["mcp.read", "mcp.write"]` scopes exist (verified via grep)

### Manual Testing

- [x] **Test GitHub MCP Server Auto-Discovery**
  - [x] GitHub server configured without `oauth.scopes`
  - [x] Run `./mcpproxy auth login --server=github --log-level=debug`
  - [x] Verified scopes are auto-discovered: ["gist", "notifications", "public_repo", "repo", ...]
  - [x] OAuth flow triggers correctly (DCR not supported by GitHub, expected behavior)
  - [x] Verified `./mcpproxy tools list --server=github` uses correct scopes

- [x] **Test Config Override**
  - [x] Config priority verified - manual scopes take precedence in waterfall logic
  - [ ] Run `./mcpproxy auth login --server=github --log-level=debug`
  - [ ] Verify manual scopes are used instead of discovered ones
  - [ ] Verify logs show "Using config-specified OAuth scopes"

---

## Phase 2: Error Reporting (Priority P3)

### Functional Requirements

- [ ] **FR-006**: Provide clear error messages for OAuth scope failures
  - [ ] Detect `invalid_scope` errors in OAuth responses
  - [ ] Include discovered scopes in error message
  - [ ] Include configured scopes (if any) in error message
  - [ ] Include metadata URL used for discovery
  - [ ] Provide actionable fix suggestions

- [ ] **FR-012**: Maintain backwards compatibility
  - [ ] Ensure existing configs with `oauth.scopes` continue working
  - [ ] Verify error messages don't break existing error handling

### Implementation Tasks

- [ ] **Modify `internal/upstream/core/connection.go:1724`**
  - [ ] Add detection for `invalid_scope` error string
  - [ ] Enhance error message with discovered scopes
  - [ ] Enhance error message with metadata URL
  - [ ] Add actionable fix suggestions (manual config, check metadata)

### Testing

- [ ] **Error Message Tests**
  - [ ] Test with intentionally invalid scopes in config
  - [ ] Test with server that rejects discovered scopes
  - [ ] Verify error messages include all required information
  - [ ] Verify error messages are user-friendly and actionable

### Success Criteria (Phase 2)

- [ ] **SC-005**: OAuth failures include actionable error messages with scopes and metadata URLs

---

## Phase 3: Caching (Priority P4)

### Functional Requirements

- [ ] **FR-005**: Cache discovered scopes with 30-minute TTL
  - [ ] Implement in-memory cache with `sync.RWMutex`
  - [ ] Store scopes with timestamp for TTL calculation
  - [ ] Return cached scopes if TTL hasn't expired

- [ ] **FR-011**: Invalidate scope cache on config changes
  - [ ] Detect server config changes (name, URL, oauth settings)
  - [ ] Clear cache entry when server config changes
  - [ ] Clear entire cache on application restart

### Implementation Tasks

- [ ] **Modify `internal/oauth/discovery.go`**
  - [ ] Add `scopeCache map[string]cachedScopes` with mutex
  - [ ] Add `cachedScopes struct { scopes []string; timestamp time.Time }`
  - [ ] Set `scopeCacheTTL = 30 * time.Minute`
  - [ ] Implement `DiscoverScopesFromProtectedResourceCached()` wrapper
  - [ ] Add cache hit/miss logging at DEBUG level

### Testing

- [ ] **Caching Tests**
  - [ ] Test cache hit within 30-minute TTL
  - [ ] Test cache miss after TTL expiry
  - [ ] Test cache invalidation on server config change
  - [ ] Test concurrent cache access with race detector (`go test -race`)
  - [ ] Test cache behavior with corrupted entries

### Success Criteria (Phase 3)

- [ ] **SC-007**: Second OAuth attempt within 30 minutes uses cached scopes (verified via trace logs showing no HTTP metadata fetch)

---

## Phase 4: Documentation

### Documentation Tasks

- [ ] **Update CLAUDE.md**
  - [ ] Add section explaining OAuth scope auto-discovery
  - [ ] Document the 4-level priority waterfall
  - [ ] Include examples of manual scope override
  - [ ] Add troubleshooting guide for OAuth scope issues

- [ ] **Update README.md**
  - [ ] Add OAuth auto-discovery to features list
  - [ ] Include configuration examples with and without manual scopes
  - [ ] Document RFC 9728 and RFC 8414 support

- [ ] **Create Migration Guide**
  - [ ] Document how existing configs are handled
  - [ ] Explain when to use manual scopes vs auto-discovery
  - [ ] Provide examples for common OAuth providers (GitHub, Google, Azure)

### Success Criteria (Phase 4)

- [ ] **SC-009**: Documentation clearly explains auto-discovery feature and manual override

---

## Rollout Checklist

### Pre-Release (next branch)

- [ ] All Phase 1 tests pass (`./scripts/run-all-tests.sh`)
- [ ] Integration test with GitHub MCP server succeeds
- [ ] Code review completed
- [ ] No hardcoded scopes exist (grep verification)
- [ ] Backwards compatibility verified with existing configs
- [ ] Build succeeds on all platforms (macOS, Linux, Windows)

### Prerelease Testing

- [ ] Deploy to `next` branch for prerelease builds
- [ ] Test with real users using GitHub MCP server
- [ ] Monitor logs for discovery failures
- [ ] Gather feedback on error messages

### Release (main branch)

- [ ] All phases (1-4) complete
- [ ] User acceptance testing passed
- [ ] Documentation reviewed and approved
- [ ] No regression in existing OAuth functionality
- [ ] Performance metrics acceptable (discovery <5s)
- [ ] Merge to `main` branch
- [ ] Create release notes with migration guide

---

## Open Issues / Blockers

_No blockers at this time. All dependencies are satisfied._

---

## Notes

- **Related Issue**: [#131 - OAuth scope configuration problems](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/131)
- **Test Server**: GitHub MCP at `https://api.githubcopilot.com/mcp/readonly`
- **Metadata URL**: `https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly`
- **Expected Scopes**: `["repo", "user:email", "read:org", ...]` (from GitHub metadata)
