# Feature Specification: Auto-Detect RFC 8707 Resource Parameter for OAuth Flows

**Feature Branch**: `011-resource-auto-detect`
**Created**: 2025-12-10
**Status**: Draft
**Input**: User description: "Auto-detect RFC 8707 resource parameter for OAuth flows"

## Overview

OAuth providers like Runlayer require the RFC 8707 `resource` parameter in authorization requests. Currently, users must manually configure this parameter via `extra_params` in their server configuration. This feature enables automatic detection and injection of the `resource` parameter, achieving true zero-configuration OAuth for compliant servers.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Zero-Config Runlayer OAuth (Priority: P1)

A user adds a Runlayer MCP server to their configuration with only the server URL. When they attempt to connect, MCPProxy automatically detects the required `resource` parameter from the server's OAuth metadata and completes the OAuth flow without any manual configuration.

**Why this priority**: This is the core use case driving the feature. Without this, users must manually configure `extra_params.resource` which defeats the "zero-config" OAuth promise and requires knowledge of RFC 8707.

**Independent Test**: Can be tested by configuring a Runlayer server with only `name` and `url` fields, initiating connection, and verifying the OAuth flow completes successfully.

**Acceptance Scenarios**:

1. **Given** a server configured with only `name` and `url` (no `oauth` block), **When** the server requires RFC 8707 `resource` parameter, **Then** MCPProxy automatically extracts the `resource` from Protected Resource Metadata and includes it in the authorization URL.

2. **Given** a server configured with only `name` and `url`, **When** Protected Resource Metadata does not contain a `resource` field, **Then** MCPProxy falls back to using the server URL as the `resource` parameter.

3. **Given** a server configured with only `name` and `url`, **When** the OAuth authorization request is made, **Then** the authorization URL contains `?resource=<detected-value>` query parameter.

---

### User Story 2 - Manual Override of Auto-Detected Resource (Priority: P2)

A user configures a server with an explicit `extra_params.resource` value. When OAuth is initiated, the manually configured value takes precedence over any auto-detected value, allowing users to override incorrect auto-detection.

**Why this priority**: Provides escape hatch when auto-detection produces incorrect results or when server configuration requires a non-standard resource value.

**Independent Test**: Can be tested by configuring both auto-detectable metadata and manual `extra_params.resource`, then verifying the manual value is used in the authorization URL.

**Acceptance Scenarios**:

1. **Given** a server with `oauth.extra_params.resource` configured manually, **When** OAuth flow is initiated, **Then** the manual value is used in the authorization URL instead of the auto-detected value.

2. **Given** a server with manual `extra_params` for other parameters (e.g., `tenant_id`), **When** OAuth flow is initiated, **Then** both auto-detected `resource` and manual parameters are included in the authorization URL.

---

### User Story 3 - Token Request Resource Injection (Priority: P2)

When exchanging an authorization code for tokens, or refreshing tokens, the `resource` parameter is included in the request body. This ensures token requests comply with RFC 8707 requirements.

**Why this priority**: Some OAuth providers require `resource` in both authorization and token requests. Without this, token exchange may fail even if authorization succeeded.

**Independent Test**: Can be tested by completing an OAuth flow and verifying the token exchange request body contains the `resource` parameter.

**Acceptance Scenarios**:

1. **Given** a successful OAuth authorization with auto-detected `resource`, **When** token exchange is performed, **Then** the token request body includes the `resource` parameter.

2. **Given** a stored refresh token, **When** token refresh is performed, **Then** the refresh request body includes the same `resource` parameter.

---

### User Story 4 - Diagnostic Visibility (Priority: P3)

Users can see the auto-detected `resource` parameter in diagnostic output (`mcpproxy doctor`, `mcpproxy auth status`) to verify correct configuration and troubleshoot OAuth issues.

**Why this priority**: Observability is important for troubleshooting but is not critical for the OAuth flow itself.

**Independent Test**: Can be tested by running diagnostic commands and verifying the auto-detected `resource` parameter is displayed.

**Acceptance Scenarios**:

1. **Given** a server with auto-detected `resource`, **When** user runs `mcpproxy auth status --server=<name>`, **Then** the output displays the detected `resource` parameter.

2. **Given** a server with auto-detected `resource`, **When** user runs `mcpproxy doctor`, **Then** OAuth diagnostics include the detected `resource` parameter.

---

### Edge Cases

- What happens when the server URL changes after initial OAuth? The `resource` parameter should be re-detected on next connection attempt.
- How does the system handle malformed Protected Resource Metadata? It should fall back to using the server URL as `resource`.
- What happens when both authorization and token endpoints require different `resource` values? The same `resource` value is used for both (per RFC 8707 design).
- What happens during offline/network failure during metadata discovery? Fall back to server URL as `resource`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST extract the `resource` field from RFC 9728 Protected Resource Metadata when available.
- **FR-002**: System MUST fall back to using the server URL as the `resource` parameter when metadata does not contain a `resource` field.
- **FR-003**: System MUST inject the `resource` parameter into the OAuth authorization URL query string.
- **FR-004**: System MUST inject the `resource` parameter into OAuth token exchange request bodies.
- **FR-005**: System MUST inject the `resource` parameter into OAuth token refresh request bodies.
- **FR-006**: System MUST allow manual `extra_params.resource` configuration to override auto-detected values.
- **FR-007**: System MUST merge manual `extra_params` with auto-detected `resource` parameter.
- **FR-008**: System MUST log the detected/used `resource` parameter at INFO level for observability.
- **FR-009**: System MUST handle metadata discovery failures gracefully without blocking OAuth flow.
- **FR-010**: System MUST maintain backward compatibility with servers that do not require `resource` parameter.

### Key Entities

- **Protected Resource Metadata**: RFC 9728 metadata document containing `resource`, `scopes_supported`, and `authorization_servers` fields.
- **OAuth Extra Parameters**: Additional query/body parameters to include in OAuth requests beyond standard OAuth 2.0/2.1 fields.
- **Server Configuration**: User-provided server settings including optional `oauth.extra_params` overrides.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can connect to Runlayer OAuth servers with zero OAuth configuration (only `name` and `url` required).
- **SC-002**: OAuth authorization requests to RFC 8707-compliant servers succeed without manual `extra_params` configuration.
- **SC-003**: Existing configurations with manual `extra_params.resource` continue to work unchanged.
- **SC-004**: Users can verify the auto-detected `resource` parameter through diagnostic commands.
- **SC-005**: OAuth flows complete within the same time as before (no significant latency increase from metadata discovery).

## Assumptions

- RFC 9728 Protected Resource Metadata is accessible at the URL indicated in the `WWW-Authenticate` header's `resource_metadata` parameter.
- The `resource` field in Protected Resource Metadata, when present, is the correct value for RFC 8707 compliance.
- Servers requiring `resource` parameter will fail with a clear error (like Runlayer's "Field required") when it's missing.
- The metadata discovery request (already performed for scope detection) can be reused for `resource` extraction without additional network calls.

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
feat: auto-detect RFC 8707 resource parameter for OAuth flows

Related #165

Implement automatic detection and injection of the RFC 8707 resource
parameter for OAuth authorization requests. This enables zero-config
OAuth for providers like Runlayer that require the resource parameter.

## Changes
- Add DiscoverProtectedResourceMetadata() returning full RFC 9728 metadata
- Update CreateOAuthConfig() to return extraParams with auto-detected resource
- Inject resource parameter into authorization URL after mcp-go constructs it
- Add fallback to server URL when metadata lacks resource field

## Testing
- Unit tests for metadata discovery and resource extraction
- E2E test with mock OAuth server requiring resource parameter
- Manual verification with Runlayer server
```
