# Feature Specification: OAuth Scope Auto-Discovery

**Feature Branch**: `001-oauth-scope-discovery`
**Created**: 2025-11-08
**Status**: Draft
**Input**: User description: "Implement OAuth scope auto-discovery using RFC 9728 and RFC 8414"
**Related Issue**: [#131 - OAuth scope configuration problems](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/131)

## User Scenarios & Testing *(mandatory)*

<!--
  IMPORTANT: User stories should be PRIORITIZED as user journeys ordered by importance.
  Each user story/journey must be INDEPENDENTLY TESTABLE - meaning if you implement just ONE of them,
  you should still have a viable MVP (Minimum Viable Product) that delivers value.

  Assign priorities (P1, P2, P3, etc.) to each story, where P1 is the most critical.
  Think of each story as a standalone slice of functionality that can be:
  - Developed independently
  - Tested independently
  - Deployed independently
  - Demonstrated to users independently
-->

### User Story 1 - Automatic OAuth Scope Discovery (Priority: P1)

As a user configuring OAuth MCP servers (like GitHub MCP, Google MCP, Azure MCP), I want mcpproxy to automatically discover the correct OAuth scopes without manual configuration, so that I can connect to OAuth servers with zero configuration effort.

**Why this priority**: This is the core fix for issue #131. Without this, users cannot use OAuth MCP servers without manual scope configuration and trial-and-error. This delivers immediate value by enabling OAuth servers to "just work" out of the box.

**Independent Test**: Can be fully tested by adding a GitHub MCP server with no `oauth.scopes` configuration, running `mcpproxy auth login --server=github`, and verifying that OAuth succeeds using auto-discovered scopes from RFC 9728 Protected Resource Metadata.

**Acceptance Scenarios**:

1. **Given** a user adds GitHub MCP server with no `oauth.scopes` config, **When** OAuth flow starts, **Then** mcpproxy automatically discovers scopes from `https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly` and uses `["repo", "user:email", ...]`
2. **Given** an OAuth server returns `401` with `WWW-Authenticate` header containing `resource_metadata` URL, **When** mcpproxy attempts authentication, **Then** it fetches the metadata URL and extracts `scopes_supported` array
3. **Given** an OAuth server supports RFC 8414 Authorization Server Metadata, **When** Protected Resource Metadata is unavailable, **Then** mcpproxy falls back to fetching `/.well-known/oauth-authorization-server` and uses its `scopes_supported`
4. **Given** an OAuth server has no metadata endpoints, **When** scope discovery fails, **Then** mcpproxy uses empty scopes `[]` and lets the server specify requirements via `WWW-Authenticate` header

---

### User Story 2 - Manual Scope Override (Priority: P2)

As an advanced user or developer, I want to manually specify OAuth scopes in the server configuration to override auto-discovery, so that I can use specific scopes for testing or when I need finer-grained permissions than the server's default.

**Why this priority**: This preserves backwards compatibility with existing configurations and provides power users with control. It's lower priority than P1 because it's already partially working (config-specified scopes are respected), but needs better validation and logging.

**Independent Test**: Can be fully tested by adding `"oauth": {"scopes": ["repo", "user"]}` to server config, running `mcpproxy auth login --server=github`, and verifying that the manually specified scopes are used instead of auto-discovered ones.

**Acceptance Scenarios**:

1. **Given** a user specifies `oauth.scopes` in server config, **When** OAuth flow starts, **Then** mcpproxy uses the config-specified scopes and logs "Using config-specified OAuth scopes"
2. **Given** a user specifies empty `oauth.scopes: []` in config, **When** OAuth flow starts, **Then** mcpproxy respects the empty array and doesn't perform discovery
3. **Given** a user specifies invalid scopes in config, **When** OAuth server rejects them, **Then** mcpproxy provides clear error message suggesting scope discovery or checking server metadata

---

### User Story 3 - Clear Error Messages for Scope Failures (Priority: P3)

As a user troubleshooting OAuth issues, I want clear error messages that explain exactly why OAuth failed and how to fix it, so that I don't waste time debugging obscure authentication errors.

**Why this priority**: This enhances user experience but isn't blocking. Users can still succeed with P1/P2, but might need to look at logs. Better error messages reduce support burden and improve UX.

**Independent Test**: Can be fully tested by intentionally configuring invalid scopes, running `mcpproxy auth login`, and verifying that the error message clearly explains the problem and suggests solutions (auto-discovery, manual config, or checking metadata URLs).

**Acceptance Scenarios**:

1. **Given** OAuth fails with `invalid_scope` error, **When** error is reported to user, **Then** error message includes: discovered scopes, configured scopes, metadata URL used, and actionable fix suggestions
2. **Given** scope discovery fails due to network error, **When** OAuth continues with empty scopes, **Then** logs clearly explain discovery failure and fallback behavior
3. **Given** Dynamic Client Registration fails due to scope mismatch, **When** OAuth flow retries without DCR, **Then** logs explain the fallback and suggest checking server metadata

---

### User Story 4 - Scope Discovery Caching (Priority: P4)

As a user with multiple servers or frequent OAuth flows, I want scope discovery results to be cached, so that I don't experience delays from repeated metadata fetches during normal operation.

**Why this priority**: This is a performance optimization, not a functional requirement. P1-P3 deliver full functionality. Caching improves UX by reducing latency but isn't critical for MVP.

**Independent Test**: Can be fully tested by enabling trace logging, running `mcpproxy auth login` twice in succession, and verifying that the second run uses cached scopes without fetching metadata URLs (confirmed by absence of HTTP requests in logs).

**Acceptance Scenarios**:

1. **Given** scope discovery succeeds for a server, **When** OAuth is triggered again within 30 minutes, **Then** cached scopes are used without refetching metadata
2. **Given** cached scopes exist but cache TTL has expired, **When** OAuth flow starts, **Then** metadata is refetched and cache is updated
3. **Given** server configuration changes (name, URL, or oauth settings), **When** OAuth flow starts, **Then** cache is invalidated and discovery runs fresh

---

### Edge Cases

- **What happens when Protected Resource Metadata URL returns 404?** System falls back to RFC 8414 Authorization Server Metadata, then empty scopes if that also fails.
- **What happens when metadata endpoint returns malformed JSON?** Discovery fails with logged error, system falls back to next discovery method in waterfall.
- **What happens when server requires scopes not in metadata?** OAuth will fail with `invalid_scope`, error message suggests manually configuring correct scopes in config file.
- **What happens when metadata returns empty `scopes_supported` array?** Treated as successful discovery with empty scopes `[]`, which is valid OAuth 2.1.
- **What happens when metadata fetch times out (>5 seconds)?** Discovery times out, logs error, falls back to next method in waterfall.
- **What happens when WWW-Authenticate header is missing `resource_metadata` field?** RFC 9728 discovery is skipped, system tries RFC 8414, then empty scopes.
- **What happens when server URL has no base URL (e.g., custom scheme)?** Discovery is skipped (cannot construct `/.well-known/` URLs), falls back to empty scopes.
- **What happens when cache is corrupted?** Cache read fails safely, triggers fresh discovery attempt.
- **What happens when OAuth server changes scopes between cached fetch and actual use?** Server rejects with `invalid_scope`, user gets clear error. Manual config override or cache invalidation (restart/wait 30min) required.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST implement RFC 9728 Protected Resource Metadata scope discovery by parsing `WWW-Authenticate` header's `resource_metadata` field and fetching the metadata URL
- **FR-002**: System MUST implement RFC 8414 OAuth Authorization Server Metadata scope discovery by fetching `{baseURL}/.well-known/oauth-authorization-server`
- **FR-003**: System MUST use a priority waterfall for scope selection: (1) Config-specified scopes, (2) RFC 9728 discovery, (3) RFC 8414 discovery, (4) Empty scopes `[]`
- **FR-004**: System MUST remove hardcoded default scopes `["mcp.read", "mcp.write"]` from `internal/oauth/config.go:196`
- **FR-005**: System MUST cache discovered scopes with 30-minute TTL to prevent repeated metadata fetches
- **FR-006**: System MUST provide clear error messages when OAuth fails due to scope issues, including discovered vs configured scopes and actionable fix suggestions
- **FR-007**: System MUST support empty scopes `[]` as valid OAuth 2.1 configuration (server specifies requirements via `WWW-Authenticate`)
- **FR-008**: System MUST respect user-configured `oauth.scopes` in server config and skip discovery when explicitly set
- **FR-009**: System MUST log which scope discovery method was used (config override, RFC 9728, RFC 8414, or empty fallback) at INFO level
- **FR-010**: System MUST handle metadata fetch failures gracefully with timeouts (5 seconds) and fallback to next discovery method
- **FR-011**: System MUST invalidate scope cache when server configuration changes (name, URL, or oauth settings modified)
- **FR-012**: System MUST maintain backwards compatibility with existing configurations that specify `oauth.scopes`

### Key Entities *(include if feature involves data)*

- **ProtectedResourceMetadata**: RFC 9728 metadata structure containing `resource`, `resource_name`, `authorization_servers`, `bearer_methods_supported`, and `scopes_supported`
- **OAuthServerMetadata**: RFC 8414 metadata structure containing `issuer`, `authorization_endpoint`, `token_endpoint`, `scopes_supported`, `response_types_supported`, `grant_types_supported`
- **ScopeCache**: In-memory cache mapping metadata URLs to discovered scopes with timestamp for TTL expiry
- **ServerConfig**: Existing configuration structure, `oauth.scopes` field takes precedence over discovery

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can connect to GitHub MCP server (`https://api.githubcopilot.com/mcp/readonly`) without manually configuring OAuth scopes
- **SC-002**: Running `mcpproxy tools list --server=github` succeeds and lists tools instead of "OAuth authorization required - deferred"
- **SC-003**: Running `mcpproxy auth login --server=github` completes OAuth flow successfully using auto-discovered scopes
- **SC-004**: Logs clearly show which scope discovery method was used (e.g., "Auto-discovered OAuth scopes from Protected Resource Metadata (RFC 9728)")
- **SC-005**: OAuth failures include actionable error messages with discovered scopes, metadata URLs, and fix suggestions
- **SC-006**: Scope discovery completes within 5 seconds or times out and falls back gracefully
- **SC-007**: Second OAuth attempt within 30 minutes uses cached scopes without refetching metadata (measurable via trace logs)
- **SC-008**: All existing server configurations with `oauth.scopes` continue to work without changes (backwards compatibility)
- **SC-009**: Test suite passes with GitHub MCP server integration test using real OAuth flow
- **SC-010**: No hardcoded `["mcp.read", "mcp.write"]` scopes exist in codebase (verified via grep)

## Implementation Plan

### Phase 1: Core Scope Discovery (P1 - MVP)

**New File**: `internal/oauth/discovery.go`

Implement:
- `ProtectedResourceMetadata` and `OAuthServerMetadata` structs
- `DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error)`
- `DiscoverScopesFromAuthorizationServer(baseURL string, timeout time.Duration) ([]string, error)`
- `ExtractResourceMetadataURL(wwwAuthHeader string) string`

**Modified File**: `internal/oauth/config.go:184-203`

Replace hardcoded scopes logic with:
1. Check `serverConfig.OAuth.Scopes` first (config override)
2. Try RFC 9728 discovery (preflight HEAD request, parse WWW-Authenticate, fetch metadata)
3. Fall back to RFC 8414 discovery
4. Final fallback to empty scopes `[]`
5. Remove `scopes := []string{"mcp.read", "mcp.write"}` line entirely

**Testing**:
- Unit tests for `ExtractResourceMetadataURL()` with various header formats
- Unit tests for metadata parsing with valid/invalid JSON
- Integration test with GitHub MCP server (`https://api.githubcopilot.com/mcp/readonly`)
- Test with mock OAuth server supporting RFC 9728
- Test with mock OAuth server supporting RFC 8414 only
- Test with server supporting neither (empty scopes fallback)

### Phase 2: Error Reporting (P3)

**Modified File**: `internal/upstream/core/connection.go:1724`

Enhance error messages:
- Detect `invalid_scope` errors
- Include discovered scopes in error message
- Include metadata URL used for discovery
- Provide actionable fix suggestions (manual config, check metadata, etc.)

**Testing**:
- Test with intentionally wrong scopes in config
- Test with server that rejects discovered scopes
- Verify error messages include all required information

### Phase 3: Caching (P4)

**Modified File**: `internal/oauth/discovery.go`

Add:
- `scopeCache map[string]cachedScopes` with `sync.RWMutex`
- `cachedScopes struct { scopes []string; timestamp time.Time }`
- `scopeCacheTTL = 30 * time.Minute`
- `DiscoverScopesFromProtectedResourceCached()` wrapper function
- Cache invalidation logic when config changes

**Testing**:
- Test cache hit within TTL window
- Test cache miss after TTL expiry
- Test cache invalidation on config change
- Test concurrent access to cache (race detector)

### Phase 4: Documentation

- Update CLAUDE.md with scope discovery details
- Add section to README.md explaining OAuth auto-discovery
- Document manual override with `oauth.scopes` examples
- Add troubleshooting guide for OAuth scope issues

## Test Plan

### Unit Tests

1. **`internal/oauth/discovery_test.go`**:
   - `TestExtractResourceMetadataURL` - various WWW-Authenticate header formats
   - `TestDiscoverScopesFromProtectedResource` - mock HTTP server with valid metadata
   - `TestDiscoverScopesFromProtectedResource_InvalidJSON` - malformed metadata
   - `TestDiscoverScopesFromProtectedResource_Timeout` - slow server (>5s)
   - `TestDiscoverScopesFromProtectedResource_404` - missing metadata endpoint
   - `TestDiscoverScopesFromAuthorizationServer` - RFC 8414 metadata parsing
   - `TestScopeCaching` - cache hit/miss/TTL/invalidation

2. **`internal/oauth/config_test.go`**:
   - `TestCreateOAuthConfig_ConfigOverride` - manual scopes take precedence
   - `TestCreateOAuthConfig_RFC9728Discovery` - Protected Resource Metadata
   - `TestCreateOAuthConfig_RFC8414Fallback` - Authorization Server Metadata
   - `TestCreateOAuthConfig_EmptyFallback` - no metadata available
   - `TestCreateOAuthConfig_NoHardcodedDefaults` - verify no `mcp.read`/`mcp.write`

### Integration Tests

3. **`internal/server/oauth_e2e_test.go`**:
   - `TestGitHubMCPOAuth` - real OAuth flow with GitHub MCP server
   - `TestScopeDiscoveryWaterfall` - mock server testing all fallback levels
   - `TestConfigOverridesDiscovery` - verify manual config takes precedence
   - `TestEmptyScopesFallback` - verify empty scopes work

### Manual Testing

4. **GitHub MCP Server**:
   ```bash
   # Remove OAuth config
   jq 'del(.mcpServers[] | select(.name == "github") | .oauth)' ~/.mcpproxy/mcp_config.json > /tmp/config.json
   mv /tmp/config.json ~/.mcpproxy/mcp_config.json

   # Test auto-discovery
   ./mcpproxy auth login --server=github --log-level=debug

   # Expected: Discovers scopes from RFC 9728 metadata
   # Expected: OAuth flow succeeds

   # Verify tools list works
   ./mcpproxy tools list --server=github

   # Expected: Lists GitHub MCP tools without errors
   ```

5. **Manual Config Override**:
   ```bash
   # Add manual scopes to config
   jq '.mcpServers[] | select(.name == "github") | .oauth.scopes = ["repo", "user"]' ~/.mcpproxy/mcp_config.json

   # Test manual override
   ./mcpproxy auth login --server=github --log-level=debug

   # Expected: Uses ["repo", "user"] instead of discovered scopes
   # Expected: Logs "Using config-specified OAuth scopes"
   ```

## Non-Goals

- **Scope validation**: We do NOT validate that discovered scopes are actually required. If the server's metadata is wrong, OAuth will fail and the user must manually configure.
- **Scope selection logic**: We do NOT implement smart selection of subset of scopes. We use all `scopes_supported` from metadata.
- **OAuth 1.0 support**: This feature only applies to OAuth 2.x servers.
- **Persistent scope cache**: Cache is in-memory only, cleared on restart.
- **UI for scope management**: Configuration is via JSON file only (consistent with existing mcpproxy design).

## Dependencies

- No new external dependencies required
- Uses existing `net/http` for metadata fetching
- Uses existing `encoding/json` for metadata parsing
- Uses existing `zap` logger for structured logging

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Server metadata is incorrect or stale | Medium | High | Provide manual config override, clear error messages suggesting checking metadata URL |
| Metadata fetch fails due to network issues | Medium | Medium | 5-second timeout, fallback to empty scopes, log clear error |
| Server changes scopes between discovery and use | Low | Medium | OAuth fails with clear error, cache invalidation on restart/TTL |
| Breaking existing configs | Low | High | Maintain config priority (manual scopes always override discovery) |
| Performance regression from metadata fetching | Low | Low | 30-minute cache, 5-second timeout, async discovery during connection phase |

## Rollout Plan

1. **Phase 1 (MVP)**: Implement core discovery (FR-001 to FR-004), deploy to `next` branch for prerelease testing
2. **Phase 2 (Polish)**: Add error reporting improvements (FR-006), gather user feedback
3. **Phase 3 (Optimization)**: Add caching (FR-005, FR-011), monitor performance
4. **Phase 4 (Documentation)**: Update docs, write migration guide for users with manual scope configs
5. **Release**: Merge to `main` after successful testing with GitHub MCP server and user validation

## Commit Message Conventions

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #131` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #131`, `Closes #131`, `Resolves #131` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: implement OAuth scope auto-discovery (RFC 9728 & RFC 8414)

Related #131

This commit implements automatic OAuth scope discovery to eliminate
the need for manual scope configuration when using OAuth MCP servers.

## Changes
- Add RFC 9728 Protected Resource Metadata discovery
- Add RFC 8414 Authorization Server Metadata discovery
- Remove hardcoded ["mcp.read", "mcp.write"] scopes
- Implement 4-level scope discovery waterfall

## Testing
- Unit tests: 16/16 passed
- Integration tests: All passed
- Manual testing: GitHub MCP server verified
```

## Open Questions

None at this time. The RFC 9728 and RFC 8414 specifications provide clear guidance for implementation.
