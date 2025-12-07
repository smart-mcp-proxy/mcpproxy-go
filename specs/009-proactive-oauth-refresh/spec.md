# Feature Specification: Proactive OAuth Token Refresh & UX Improvements

**Feature Branch**: `009-proactive-oauth-refresh`
**Created**: 2025-12-07
**Status**: Draft
**Input**: OAuth UX improvements with proactive token refresh, login/logout functionality, and proper auth status display. Add logout command into CLI. Logout must clear token and disconnect server. Features must be properly tested using unit tests, E2E tests, and OAuth test server. Verify Web UI works using Playwright and test CLI.

## Problem Statement

Current OAuth implementation has critical UX issues:

1. **Token refresh is lazy (on-demand)**: Tokens only refresh when making tool calls, causing failures when expired
2. **Login button hidden when connected**: Servers showing "Connected" but with expired tokens don't display the Login button
3. **No logout functionality**: Users cannot clear OAuth credentials to re-authenticate
4. **Misleading status display**: Server shows "Connected" even when OAuth tokens are expired and tool calls fail
5. **Race conditions**: Concurrent requests can trigger multiple simultaneous refresh attempts

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Proactive Token Refresh (Priority: P1)

As an MCPProxy user with OAuth-enabled servers, I want the system to automatically refresh tokens before they expire so that my tool calls never fail due to expired tokens.

**Why this priority**: This is the root cause of the user-facing issue. Tool calls fail with "authorization required" errors because tokens expire while the connection appears healthy.

**Independent Test**: Can be tested by configuring an OAuth server with short token lifetime (30s-2min), waiting for 80% of token lifetime, and verifying tokens are refreshed without user intervention.

**Acceptance Scenarios**:

1. **Given** a server with valid OAuth token expiring in 2 minutes, **When** 80% of the token lifetime elapses (1min 36s), **Then** the system automatically refreshes the token without user action

2. **Given** a background refresh is in progress, **When** a tool call is made, **Then** the tool call waits for refresh to complete rather than triggering a duplicate refresh

3. **Given** a proactive refresh fails, **When** the token actually expires, **Then** an auth error notification is shown to the user and the Login button becomes visible

4. **Given** multiple OAuth servers with different expiration times, **When** the refresh monitor runs, **Then** each server is refreshed independently at 80% of its respective token lifetime

---

### User Story 2 - CLI Logout Command (Priority: P1)

As an MCPProxy CLI user, I want to log out of an OAuth server to clear stored credentials and disconnect the server, so I can re-authenticate with different credentials or troubleshoot authentication issues.

**Why this priority**: Users currently have no way to clear OAuth tokens, forcing them to manually delete database files or restart the application.

**Independent Test**: Can be tested by running `mcpproxy auth logout --server=<name>` and verifying the token is cleared and server disconnected.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server, **When** I run `mcpproxy auth logout --server=sentry`, **Then** the OAuth token is cleared, the server is disconnected, and a success message is displayed

2. **Given** an OAuth server with expired token, **When** I run `mcpproxy auth logout --server=sentry`, **Then** the token is cleared even if expired

3. **Given** a non-existent server name, **When** I run `mcpproxy auth logout --server=nonexistent`, **Then** an error message indicates the server was not found

4. **Given** a non-OAuth server (stdio), **When** I run `mcpproxy auth logout --server=filesystem`, **Then** an error message indicates the server does not use OAuth

5. **Given** the daemon is not running, **When** I run `mcpproxy auth logout --server=sentry`, **Then** the command operates in standalone mode and clears the persisted token from storage

---

### User Story 3 - REST API Logout Endpoint (Priority: P1)

As a Web UI or tray application, I want to trigger logout via REST API so that users can log out of OAuth servers through the UI.

**Why this priority**: Required to support Web UI and tray menu logout functionality.

**Independent Test**: Can be tested by sending POST request to `/api/v1/servers/{id}/logout` and verifying the response and server state.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server, **When** POST `/api/v1/servers/sentry/logout` is called, **Then** response is 200 OK with action "logout" and the token is cleared

2. **Given** a non-OAuth server, **When** POST `/api/v1/servers/filesystem/logout` is called, **Then** response is 400 with appropriate error message

3. **Given** a non-existent server, **When** POST `/api/v1/servers/nonexistent/logout` is called, **Then** response is 404 with "server not found" error

---

### User Story 4 - Web UI Login Button Visibility Fix (Priority: P2)

As a Web UI user, I want to see the Login button for OAuth servers with expired tokens even when they show as "Connected", so I can re-authenticate when needed.

**Why this priority**: Currently the Login button requires `notConnected`, so connected servers with expired tokens have no login option visible.

**Independent Test**: Can be tested with Playwright by loading the servers page with a connected server having expired token and verifying Login button is visible.

**Acceptance Scenarios**:

1. **Given** a connected OAuth server with expired token, **When** viewing the servers page, **Then** the Login button is visible

2. **Given** a connected OAuth server with valid token, **When** viewing the servers page, **Then** the Login button is NOT visible (authentication is working)

3. **Given** a disconnected OAuth server, **When** viewing the servers page, **Then** the Login button is visible (existing behavior maintained)

---

### User Story 5 - Web UI Logout Button (Priority: P2)

As a Web UI user, I want a Logout button for authenticated OAuth servers so I can clear credentials when needed.

**Why this priority**: Provides parity with the CLI logout command through the Web UI.

**Independent Test**: Can be tested with Playwright by clicking the Logout button and verifying the server is disconnected and token cleared.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server, **When** I click the Logout button, **Then** a confirmation dialog appears

2. **Given** I confirm the logout, **When** the logout completes, **Then** the server shows as disconnected and the Login button becomes visible

3. **Given** a non-OAuth server, **When** viewing the server card, **Then** no Logout button is displayed

---

### User Story 6 - Auth Status Badge Display (Priority: P2)

As a Web UI user, I want to see an "Auth Error" badge when a connected server has authentication issues, so I can quickly identify which servers need attention.

**Why this priority**: Visual feedback helps users understand why tool calls might be failing.

**Independent Test**: Can be tested by displaying a connected server with expired token and verifying the auth error badge appears.

**Acceptance Scenarios**:

1. **Given** a connected server with expired OAuth token, **When** viewing the servers list, **Then** an "Auth Error" or "Token Expired" badge is displayed alongside the "Connected" badge

2. **Given** a connected server with valid OAuth token, **When** viewing the servers list, **Then** no auth error badge is shown

3. **Given** a server with OAuth error in last_error, **When** viewing the servers list, **Then** the auth error badge is displayed

---

### User Story 7 - Token Expiration Display (Priority: P3)

As a user, I want to see when OAuth tokens will expire in the UI and CLI, so I can anticipate when re-authentication might be needed.

**Why this priority**: Informational improvement that helps with debugging but not critical for functionality.

**Independent Test**: Can be tested by viewing server details and verifying token expiration time is displayed.

**Acceptance Scenarios**:

1. **Given** an authenticated OAuth server, **When** viewing server details (UI or CLI), **Then** the token expiration time is displayed

2. **Given** a token expiring within 5 minutes, **When** viewing the expiration, **Then** a warning indicator is shown

3. **Given** an expired token, **When** viewing the expiration, **Then** "EXPIRED" is clearly indicated

---

### Edge Cases

- What happens when refresh fails due to revoked refresh token? System should notify user and show Login button
- What happens when logout is called during an active tool call? Tool call should fail gracefully with auth error
- What happens when multiple concurrent logouts are triggered? Only one logout should execute, others should be ignored
- What happens when proactive refresh races with manual login? Login should take precedence and cancel pending refresh
- What happens when server is disabled during refresh? Refresh should be cancelled
- What happens when network is unavailable during refresh? Retry with exponential backoff, max 3 attempts

## Requirements *(mandatory)*

### Functional Requirements

#### Proactive Token Refresh

- **FR-001**: System MUST refresh OAuth tokens when 80% of token lifetime has elapsed (configurable threshold)
- **FR-002**: System MUST use per-server mutex to prevent concurrent refresh attempts for the same server
- **FR-003**: System MUST persist refreshed tokens to storage immediately after successful refresh
- **FR-004**: System MUST emit SSE event `oauth.token_refreshed` when a token is successfully refreshed
- **FR-005**: System MUST emit SSE event `oauth.refresh_failed` when token refresh fails
- **FR-006**: System MUST retry failed refresh attempts up to 3 times with exponential backoff (1s, 2s, 4s)
- **FR-007**: System MUST trigger browser-based OAuth flow when refresh token is invalid or revoked
- **FR-008**: System MUST skip proactive refresh for disabled or quarantined servers

#### CLI Logout Command

- **FR-010**: CLI MUST provide `mcpproxy auth logout --server=<name>` command
- **FR-011**: Logout command MUST clear OAuth token from persistent storage
- **FR-012**: Logout command MUST disconnect the server after clearing token
- **FR-013**: Logout command MUST work via daemon socket when daemon is running
- **FR-014**: Logout command MUST work in standalone mode when daemon is not running
- **FR-015**: Logout command MUST return appropriate error for non-OAuth servers
- **FR-016**: Logout command MUST support `--all` flag to logout all OAuth servers

#### REST API Logout Endpoint

- **FR-020**: REST API MUST provide `POST /api/v1/servers/{id}/logout` endpoint
- **FR-021**: Logout endpoint MUST return 200 OK with `{"action": "logout", "success": true}` on success
- **FR-022**: Logout endpoint MUST return 400 Bad Request for non-OAuth servers
- **FR-023**: Logout endpoint MUST return 404 Not Found for non-existent servers
- **FR-024**: Logout endpoint MUST be documented in OpenAPI specification

#### Web UI Improvements

- **FR-030**: Web UI MUST show Login button for OAuth servers with expired tokens regardless of connection status
- **FR-031**: Web UI MUST show Logout button for authenticated OAuth servers
- **FR-032**: Web UI MUST display confirmation dialog before executing logout
- **FR-033**: Web UI MUST show "Token Expired" badge for connected servers with expired OAuth tokens
- **FR-034**: Web UI MUST update server state in real-time via SSE after logout

#### Status Display

- **FR-040**: Server status MUST include `oauth_status` field with values: "authenticated", "expired", "error", "none"
- **FR-041**: Server status MUST include `token_expires_at` field with ISO 8601 timestamp when authenticated
- **FR-042**: CLI `auth status` command MUST display token expiration with human-readable relative time

### Key Entities

- **OAuth Token**: Represents stored OAuth credentials including access_token, refresh_token, expires_at, and server association
- **Refresh Attempt**: Represents a token refresh operation with status, retry count, and last error
- **OAuth Status**: Enum representing current authentication state (authenticated, expired, error, none)

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Tool calls to OAuth servers succeed without "authorization required" errors when tokens are within validity period (100% success rate during valid token lifetime)
- **SC-002**: Users can complete logout operation in under 3 seconds through CLI, Web UI, or API
- **SC-003**: Token refresh occurs automatically at 80% of token lifetime (within 5 second tolerance)
- **SC-004**: All OAuth servers display accurate authentication status (expired, valid, error) in real-time
- **SC-005**: Login button is visible whenever OAuth authentication is required, regardless of connection status
- **SC-006**: E2E tests pass with OAuth test server configured for short token lifetimes (30s-2min)
- **SC-007**: Playwright tests verify Web UI login/logout button visibility and functionality
- **SC-008**: CLI logout command completes successfully for valid OAuth servers
- **SC-009**: No race conditions occur during concurrent refresh/logout operations (verified by unit tests)

## Testing Requirements

### Unit Tests

- Test proactive refresh trigger at 80% lifetime threshold
- Test per-server mutex prevents concurrent refresh
- Test refresh retry logic with exponential backoff
- Test logout clears token and disconnects server
- Test OAuth status calculation (authenticated/expired/error)

### E2E Tests

- Test full proactive refresh cycle with OAuth test server
- Test CLI logout command via daemon socket
- Test CLI logout in standalone mode
- Test REST API logout endpoint
- Test SSE events for token refresh and logout

### Playwright Tests

- Test Login button visibility for connected+expired servers
- Test Logout button visibility for authenticated servers
- Test logout confirmation dialog flow
- Test auth status badge display
- Test real-time UI updates after logout

### OAuth Test Server

- Configure short token lifetime (30-60 seconds) for refresh testing
- Configure refresh token to validate refresh flow
- Test revoked refresh token scenario

## Assumptions

- Token refresh threshold of 80% is appropriate for most OAuth providers (can be made configurable if needed)
- Exponential backoff with max 3 retries is sufficient for transient network errors
- Users expect logout to require confirmation in Web UI but not in CLI
- OAuth test server from spec 007 is available for E2E testing

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add proactive OAuth token refresh

Related #[issue-number]

Implement background token refresh at 80% of token lifetime to prevent
tool call failures due to expired tokens.

## Changes
- Add TokenRefreshManager with per-server mutex coordination
- Add background goroutine to monitor token expiration
- Emit SSE events for refresh success/failure
- Add unit tests for refresh timing and coordination

## Testing
- Unit tests pass for refresh logic
- E2E tests verify refresh with OAuth test server
```
