# Feature Specification: OAuth Extra Parameters Support

**Feature Branch**: `006-oauth-extra-params`
**Created**: 2025-11-30
**Status**: Draft
**Input**: User description: "Required The full extra_params implementation (allowing custom OAuth parameters like RFC 8707 resource) is tracked separately and will follow the plan in docs/plans/2025-11-27-oauth-extra-params.md First check that it's possiable to implement with mcp-go dep that we are using"

**mcp-go Dependency Analysis**: Upgraded to `github.com/mark3labs/mcp-go v0.43.1`. This version does NOT natively support custom OAuth parameters (v0.43.1 focuses on session management improvements). The `OAuthConfig` struct and OAuth URL construction methods (`GetAuthorizationURL`, `ProcessAuthorizationResponse`) only support standard OAuth 2.0 parameters and provide no mechanism to add extra parameters like RFC 8707 `resource`. Implementation will require a wrapper/decorator pattern to inject extra parameters without modifying the mcp-go library directly.

**Implementation Decision**: Using wrapper pattern as outlined in `docs/plans/2025-11-27-oauth-extra-params.md`. See `sdk-comparison-analysis.md` for detailed comparison of all options (upgrade, wait for upstream, switch SDKs, wrapper). Wrapper pattern chosen for fastest time-to-value (2-3 weeks) with lowest risk.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Authenticate with Runlayer MCP Servers (Priority: P1)

A developer needs to connect MCPProxy to a Runlayer-hosted MCP server (e.g., Slack integration) which requires RFC 8707 resource indicators in the OAuth flow. Without this capability, authentication fails with "field required" errors.

**Why this priority**: This is the blocking issue for Runlayer integration. Runlayer's OAuth implementation requires the `resource` parameter to identify which MCP endpoint the token should grant access to. This is not a "nice to have" - authentication is completely non-functional without it.

**Independent Test**: Configure a single MCP server with OAuth extra_params containing a `resource` parameter, trigger OAuth login flow, and verify successful authentication. This delivers immediate value by enabling Runlayer integration.

**Acceptance Scenarios**:

1. **Given** a Runlayer MCP server configuration with OAuth enabled and `extra_params.resource` set to the MCP endpoint URL, **When** the user runs `mcpproxy auth login --server=slack`, **Then** the OAuth authorization URL includes the `resource` query parameter and authentication completes successfully
2. **Given** an OAuth provider that requires custom parameters, **When** the authorization flow is initiated, **Then** all configured extra_params are appended to the authorization URL without overriding standard OAuth 2.0 parameters
3. **Given** a successfully authenticated session with extra_params, **When** the access token expires and refresh is attempted, **Then** the extra_params are included in the token refresh request

---

### User Story 2 - Configure Extra Parameters via Config File (Priority: P1)

A developer needs to add OAuth extra parameters to their server configuration without modifying application code. They should be able to specify arbitrary key-value pairs that get passed to the OAuth provider.

**Why this priority**: Configuration is the primary interface for users. Without a clear config schema, users cannot leverage the feature even if it's technically working. This is equally critical as the authentication flow itself.

**Independent Test**: Add `extra_params` section to a server's OAuth config in `mcp_config.json`, reload configuration, and verify that `auth status` command displays the extra parameters. This can be tested without completing a full OAuth flow.

**Acceptance Scenarios**:

1. **Given** a server configuration file, **When** a user adds `"extra_params": {"resource": "https://example.com/mcp"}` under the `oauth` section, **Then** the configuration loads without errors and the parameters are available for OAuth flows
2. **Given** a configuration with extra_params containing a reserved OAuth parameter name like `client_id`, **When** the configuration is loaded, **Then** validation fails with a clear error message identifying the reserved parameter
3. **Given** a configuration with multiple extra parameters, **When** the configuration is loaded and displayed, **Then** all parameters are preserved in order and non-resource parameters are masked in logs for security

---

### User Story 3 - Debug OAuth Issues with Clear Diagnostics (Priority: P2)

A developer encounters OAuth authentication failures and needs to understand why. They should see clear error messages indicating missing parameters and receive actionable suggestions for fixing the configuration.

**Why this priority**: While not required for basic functionality, debugging OAuth issues is currently extremely difficult. Clear diagnostics significantly reduce time-to-resolution and improve developer experience. This is lower priority than P1 because users can eventually succeed through trial and error.

**Independent Test**: Configure a server with OAuth but without required extra_params, run `auth status` and `doctor` commands, verify that error messages clearly identify the missing parameter and suggest adding it to config. This tests diagnostic capabilities without requiring working OAuth.

**Acceptance Scenarios**:

1. **Given** a server with OAuth configured but missing required extra parameters, **When** the user runs `mcpproxy auth status`, **Then** the output shows the authentication failure reason and suggests adding the missing parameter to `extra_params` configuration
2. **Given** OAuth authentication errors logged during connection attempts, **When** the user runs `mcpproxy doctor`, **Then** the diagnostics output identifies servers with OAuth configuration issues and provides example configurations to fix them
3. **Given** an OAuth flow that includes extra_params, **When** verbose logging is enabled with `--log-level=debug`, **Then** logs show the complete authorization URL with all parameters (resource parameters visible, others masked) for debugging purposes
4. **Given** a server with OAuth and extra_params configured, **When** the user runs `mcpproxy auth status`, **Then** the output displays OAuth configuration details including extra_params (with resource URLs visible and other params masked), scopes, PKCE status, and token expiration time
5. **Given** an OAuth login flow initiated with `mcpproxy auth login --log-level=debug`, **When** the authorization browser opens, **Then** the console displays the complete authorization URL preview showing all extra_params before the browser opens
6. **Given** a token refresh operation with extra_params configured, **When** debug logging is enabled, **Then** logs show the token refresh request including extra_params (with appropriate masking for sensitive values)

---

### User Story 4 - Maintain Backward Compatibility (Priority: P2)

Existing users with working OAuth configurations (no extra_params) should continue to work without any changes. The feature should be purely additive with no breaking changes.

**Why this priority**: Backward compatibility is critical for production systems, but since this is a new feature being added, existing users won't be immediately affected. However, we must ensure zero regressions to maintain trust.

**Independent Test**: Run existing OAuth integration tests without extra_params configured, verify all tests pass unchanged. Can be tested independently through regression test suite without any new configuration.

**Acceptance Scenarios**:

1. **Given** an existing server configuration with OAuth but no `extra_params` field, **When** the configuration is loaded and OAuth flow initiated, **Then** authentication works identically to previous versions with no extra parameters added
2. **Given** a server configuration with `oauth: {}` (empty OAuth object), **When** OAuth flow is initiated, **Then** the system uses default OAuth behavior without attempting to add any extra parameters
3. **Given** existing unit and integration tests for OAuth, **When** tests are run against the new implementation, **Then** all existing tests pass without modification

---

### Edge Cases

- What happens when a user attempts to override a reserved OAuth parameter (e.g., `client_id`, `state`) via extra_params?
  - **Expected**: Configuration validation rejects the config at load time with a clear error message listing the reserved parameter name and explaining it cannot be overridden

- How does the system handle empty or null extra_params values?
  - **Expected**: Empty map `{}`, null, and omitted field all behave identically - no extra parameters are added to OAuth requests

- What happens if extra_params contains special characters or URL-unsafe values?
  - **Expected**: Values are properly URL-encoded when added to query strings to prevent malformed URLs

- How are extra parameters handled during token refresh vs initial authorization?
  - **Expected**: Extra parameters are included in both authorization requests AND token refresh requests for consistency with OAuth provider expectations

- What happens if an OAuth provider's metadata discovery returns endpoints that conflict with extra_params?
  - **Expected**: User-specified extra_params take precedence (explicit configuration overrides discovery)

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST allow users to specify arbitrary OAuth extra parameters via `extra_params` field in server OAuth configuration as a map of string key-value pairs
- **FR-002**: System MUST validate extra_params at configuration load time and reject configurations that attempt to override reserved OAuth 2.0 parameters (client_id, client_secret, redirect_uri, response_type, scope, state, code_challenge, code_challenge_method, grant_type, code, refresh_token)
- **FR-003**: System MUST append all configured extra_params to OAuth authorization URLs when initiating the OAuth flow
- **FR-004**: System MUST include all configured extra_params in token exchange requests when exchanging authorization codes for access tokens
- **FR-005**: System MUST include all configured extra_params in token refresh requests when refreshing expired access tokens
- **FR-006**: System MUST properly URL-encode extra parameter values when constructing OAuth URLs to handle special characters
- **FR-007**: System MUST preserve existing OAuth behavior when no extra_params are configured (backward compatibility)
- **FR-008**: System MUST mask sensitive extra parameter values in logs, displaying full values only for parameters identified as resource URLs (parameters starting with "resource")
- **FR-009**: OAuth status command (`auth status`) MUST display configured extra parameters for servers with OAuth enabled
- **FR-010**: Diagnostics command (`doctor`) MUST detect OAuth servers with authentication failures and suggest adding extra_params when provider errors indicate missing required parameters
- **FR-011**: System MUST load extra_params from JSON configuration file using the mapstructure library to support flexible field name mapping
- **FR-012**: System MUST maintain all existing OAuth configuration fields (client_id, client_secret, scopes, etc.) without any breaking changes to existing config schemas
- **FR-013**: Auth status command (`auth status`) MUST display comprehensive OAuth configuration details including: extra_params (with selective masking), configured scopes, PKCE status, redirect URI, authorization/token endpoints, and token expiration time (when authenticated)
- **FR-014**: Auth login command (`auth login`) MUST display configuration preview before opening browser, including: provider URL, configured scopes, PKCE status, extra_params summary, and the complete authorization URL with all parameters visible for debugging
- **FR-015**: System MUST log OAuth authorization requests at DEBUG level showing: authorization URL with extra_params visible, PKCE status, and configured scopes
- **FR-016**: System MUST log OAuth token requests (exchange and refresh) at DEBUG level showing: token endpoint URL, grant type, extra_params included, and request outcome (without logging access tokens, refresh tokens, or client secrets)
- **FR-017**: System MUST implement selective masking for OAuth parameters in logs and command output: resource URLs and audience parameters displayed in full, all other extra_params and sensitive OAuth fields (client_secret, tokens, code_verifier) masked with "***"

### Key Entities *(include if feature involves data)*

- **OAuthConfig**: Represents OAuth configuration for a server, containing standard OAuth fields plus the new `ExtraParams` map. Attributes include ClientID, ClientSecret, RedirectURI, Scopes, PKCEEnabled, and ExtraParams (map[string]string). The ExtraParams field is optional and nil/empty by default.

- **OAuthExtraParams**: A map structure containing arbitrary key-value string pairs that represent additional OAuth parameters. Keys must not conflict with reserved OAuth 2.0 parameter names. Values may include resource URLs, audience identifiers, or other provider-specific parameters.

- **ServerConfig**: The server configuration entity that contains OAuth settings. When OAuth is enabled, it includes an OAuthConfig object which may contain ExtraParams.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Developers can successfully authenticate with Runlayer MCP servers that require RFC 8707 resource parameters within 5 minutes of adding extra_params to their configuration
- **SC-002**: Configuration validation catches 100% of attempts to override reserved OAuth parameters and provides clear error messages before runtime
- **SC-003**: Existing OAuth flows without extra_params continue to work with zero regressions, verified by 100% pass rate on existing OAuth integration test suite
- **SC-004**: OAuth diagnostic commands (`auth status` and `doctor`) provide actionable error messages that reduce OAuth troubleshooting time from hours to minutes
- **SC-005**: Extra parameters are successfully delivered to all OAuth endpoints (authorization, token exchange, token refresh) without data loss or corruption
- **SC-006**: Security-sensitive parameter values are masked in logs while resource URLs remain visible for debugging, verified through log output inspection
- **SC-007**: Auth status and auth login commands display all relevant OAuth configuration details (extra_params, scopes, PKCE, endpoints, token status) enabling developers to verify correct configuration in under 30 seconds without inspecting log files or config JSON

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add OAuth extra_params support for RFC 8707 resource indicators

Related #[issue-number]

Add ExtraParams field to OAuthConfig to support arbitrary OAuth parameters
required by providers like Runlayer. Parameters are validated against reserved
OAuth 2.0 keywords and appended to authorization and token requests.

## Changes
- Add ExtraParams map[string]string to config.OAuthConfig
- Implement validation to prevent reserved parameter overrides
- Create OAuth transport wrapper to inject extra params into mcp-go flows
- Update auth status and doctor commands to display/diagnose extra_params
- Add comprehensive test coverage for extra params scenarios

## Testing
- Unit tests: config parsing, validation, wrapper URL modification
- Integration tests: mock OAuth server with resource parameter requirement
- Regression tests: existing OAuth flows pass without extra_params
- E2E test: Runlayer Slack MCP server authentication with resource param
```
