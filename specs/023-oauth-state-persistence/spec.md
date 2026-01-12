# Feature Specification: OAuth Token Refresh Reliability

**Feature Branch**: `023-oauth-state-persistence`
**Created**: 2026-01-12
**Status**: Draft
**Input**: Fix token refresh so OAuth servers survive restarts and proactively refresh before expiration

## Problem Statement

OAuth-enabled MCP servers require manual re-authentication after any mcpproxy downtime (restart, laptop sleep, weekend). Despite having refresh tokens stored in the database, the automatic token refresh is not working reliably.

**Root causes identified through investigation:**

1. **Misleading logging**: Current "OAuth token refresh successful" logs are incorrect - they log when `client.Start()` returns nil, not when an actual token refresh occurs. The logged duration (1 nanosecond) is impossible for a real HTTP refresh call.

2. **Refresh errors are swallowed**: The mcp-go library's `getValidToken()` function attempts refresh but swallows the error, making it impossible to diagnose why refresh failed (expired refresh token, network error, provider rejection, etc.).

3. **No startup recovery**: When mcpproxy starts with already-expired tokens, there is no mechanism to attempt refresh before connecting.

4. **Access tokens are short-lived**: Tokens expire in approximately 2 hours, requiring reliable proactive refresh to maintain connectivity.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Survive Laptop Sleep/Wake (Priority: P1)

As a developer using mcpproxy, I want OAuth servers to automatically reconnect after my laptop wakes from sleep, so I don't have to manually re-authenticate every morning.

**Why this priority**: This is the most common disruption - every developer experiences this daily. It's the primary pain point driving this feature.

**Independent Test**: Can be fully tested by authenticating an OAuth server, sleeping the laptop for 3+ hours (allowing tokens to expire), waking it, and verifying the server automatically reconnects without user intervention.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server with a valid refresh token, **When** the laptop sleeps for 3 hours and wakes, **Then** the server automatically refreshes the token and reconnects within 30 seconds.
2. **Given** an authenticated OAuth server, **When** mcpproxy is restarted, **Then** the server automatically reconnects using the stored refresh token without requiring browser authentication.

---

### User Story 2 - Proactive Token Refresh (Priority: P2)

As a developer using mcpproxy during a long work session, I want tokens to be refreshed automatically before they expire, so my MCP tools never fail mid-task due to token expiration.

**Why this priority**: Prevents disruption during active work. Without this, tokens could expire during tool calls causing unexpected failures.

**Independent Test**: Can be tested by authenticating an OAuth server, monitoring token expiration time, and verifying that a new token is obtained before the original expires.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server with a token expiring in 2 hours, **When** 80% of the token lifetime elapses (approximately 96 minutes), **Then** the system automatically refreshes the token without user action.
2. **Given** a proactive refresh is scheduled, **When** the refresh succeeds, **Then** a new refresh is scheduled for 80% of the new token's lifetime.
3. **Given** a proactive refresh is scheduled, **When** the refresh fails, **Then** the user is notified with the specific error reason.

---

### User Story 3 - Clear Refresh Failure Feedback (Priority: P3)

As a developer, when automatic token refresh fails, I want to understand why it failed, so I can take appropriate action (re-authenticate vs. wait for network vs. contact provider).

**Why this priority**: Improves debuggability and user experience. Currently, all refresh failures show the same generic "Authentication required" message.

**Independent Test**: Can be tested by simulating various refresh failure scenarios and verifying distinct, actionable error messages appear in health status and logs.

**Acceptance Scenarios**:

1. **Given** a refresh token that has been revoked by the provider, **When** refresh is attempted, **Then** the health status shows "Refresh token expired - re-authentication required" with the `login` action.
2. **Given** a network connectivity issue, **When** refresh is attempted, **Then** the health status shows "Refresh failed - network error" with the `retry` action.
3. **Given** any refresh failure, **When** viewing logs, **Then** the actual OAuth error (e.g., `invalid_grant`, `server_error`) is logged with full context.

---

### Edge Cases

- What happens when the refresh token itself has expired on the provider side?
  - System should detect this (e.g., `invalid_grant` error) and prompt for re-authentication rather than retrying indefinitely.

- How does the system handle rapid successive refresh attempts?
  - System should implement rate limiting to prevent overwhelming the OAuth provider (minimum 10 seconds between refresh attempts per server).

- What happens if refresh succeeds but the new token is immediately invalid?
  - System should detect this on the subsequent connection attempt and report the specific error rather than entering a retry loop.

- What happens during a network partition?
  - System should use exponential backoff for refresh retries, with clear status indicating retry attempts.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST attempt to refresh expired tokens at startup before attempting to connect OAuth servers.
- **FR-002**: System MUST use the mcp-go library's `RefreshToken()` method directly to obtain actual refresh errors (not swallowed errors).
- **FR-003**: System MUST log accurate token refresh results, including actual duration and success/failure status with error details.
- **FR-004**: System MUST schedule proactive token refresh at 80% of token lifetime to prevent expiration during use.
- **FR-005**: System MUST persist newly refreshed tokens to the database immediately after successful refresh.
- **FR-006**: System MUST display distinct health status messages for different refresh failure types (expired refresh token, network error, provider error).
- **FR-007**: System MUST remove or correct the current misleading "OAuth token refresh successful" logging that reports success when `Start()` returns nil.
- **FR-008**: System MUST rate-limit refresh attempts to no more than one per 10 seconds per server.

### Key Entities

- **OAuth Token**: Access token, refresh token, expiration timestamp, token type, scope. Stored in database with server identifier.
- **Refresh Schedule**: Server name, scheduled refresh time, retry count, last error. Managed by RefreshManager.
- **Health Status**: Level (healthy/degraded/unhealthy), summary, detail (including refresh error), suggested action.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: OAuth servers automatically reconnect within 60 seconds after mcpproxy restart, without requiring user re-authentication (when refresh tokens are valid).
- **SC-002**: Tokens are refreshed before expiration 99% of the time during continuous operation, preventing mid-session authentication failures.
- **SC-003**: When refresh fails, users see the specific failure reason (not generic "Authentication required") within 5 seconds of the failure.
- **SC-004**: Log messages accurately reflect refresh attempt outcomes - no false "successful" logs for failed or unattempted refreshes.
- **SC-005**: After implementation, manual re-authentication is required only when refresh tokens have genuinely expired on the provider side (not due to system bugs).

## Assumptions

- The mcp-go library v0.44.0-beta.2 (or later stable version) will be used, which provides the `GetOAuthHandler()` method and improved OAuth error detection.
- OAuth providers honor standard RFC 6749 refresh token semantics.
- Refresh tokens remain valid for at least 24 hours on most providers (provider-specific expiration is outside our control).
- Network connectivity is generally available; brief outages should be handled with retry logic.

## Future Work

- **offline_access scope**: Investigate adding `offline_access` to scope requests for providers that support it (Atlassian, Google) to potentially obtain longer-lived refresh tokens.
- **Token expiration warnings**: Add health status indicators when tokens are approaching expiration without scheduled refresh.
- **Refresh token rotation handling**: Some providers issue new refresh tokens on each use; ensure proper handling of token rotation.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- **Do NOT include**: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(oauth): implement startup token refresh

Related #XXX

Adds automatic token refresh at startup for OAuth servers with expired
access tokens but valid refresh tokens.

## Changes
- Add startup refresh logic in RefreshManager
- Call mcp-go RefreshToken() directly for error visibility
- Update health status to show refresh failure reasons

## Testing
- Verified with expired Google OAuth tokens
- Confirmed proper error surfacing for invalid_grant
```
