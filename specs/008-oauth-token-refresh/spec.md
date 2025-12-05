# Feature Specification: OAuth Token Refresh Bug Fixes and Logging Improvements

**Feature Branch**: `008-oauth-token-refresh`
**Created**: 2025-12-04
**Status**: Draft
**Input**: User description: "Need to fix bugs related with OAuth. Improve logging related with OAuth, use correlation_id to make OAuth flows traceable. In debug logs for oauth server client interaction save more relevant data, http headers, special resource responses etc. Mandatory to conduct comprehensive testing of OAuth mcpproxy implementation using oauth test server."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Automatic Token Refresh on Reconnection (Priority: P1)

As a user with an OAuth-protected MCP server configured, when my access token expires while mcpproxy is running, I want mcpproxy to automatically refresh my token using the stored refresh token and reconnect without requiring me to re-authenticate through the browser.

**Why this priority**: This is the core bug causing OAuth servers to become unusable after token expiration. Without this fix, users must manually re-authenticate every time tokens expire, making OAuth-protected servers impractical for real use.

**Independent Test**: Can be fully tested by configuring an OAuth server with short token TTL (30s), waiting for expiration, and verifying the server automatically reconnects using the refresh token.

**Acceptance Scenarios**:

1. **Given** mcpproxy is connected to an OAuth-protected server with a valid refresh token, **When** the access token expires and mcpproxy attempts to reconnect, **Then** mcpproxy automatically uses the refresh token to obtain a new access token and reconnects successfully.

2. **Given** mcpproxy has a persisted OAuth token from a previous session, **When** mcpproxy restarts and the access token is expired but refresh token is valid, **Then** mcpproxy loads the persisted token, refreshes it, and connects successfully without browser interaction.

3. **Given** both access token and refresh token are expired, **When** mcpproxy attempts to reconnect, **Then** mcpproxy triggers a new OAuth flow via browser and logs a clear message indicating re-authentication is required.

---

### User Story 2 - OAuth Flow Traceability with Correlation IDs (Priority: P2)

As a developer or administrator troubleshooting OAuth issues, I want each OAuth flow to have a unique correlation ID that appears in all related log entries, so I can trace the complete flow of a single authentication attempt through the logs.

**Why this priority**: Without correlation IDs, OAuth issues are extremely difficult to debug because log entries from concurrent flows are interleaved and cannot be distinguished. This is essential for diagnosing OAuth problems in production.

**Independent Test**: Can be tested by triggering an OAuth flow and verifying all related log entries share the same correlation ID, making it possible to filter logs for a single flow.

**Acceptance Scenarios**:

1. **Given** an OAuth flow is initiated, **When** any log entry related to that flow is written, **Then** the log entry includes a `correlation_id` field with a unique identifier for that flow.

2. **Given** multiple OAuth flows are running concurrently for different servers, **When** I filter logs by a specific correlation ID, **Then** I see only log entries from that single flow.

3. **Given** an OAuth flow fails at any stage, **When** I search logs for the correlation ID, **Then** I can trace the complete sequence of events leading to the failure.

---

### User Story 3 - Enhanced OAuth Debug Logging (Priority: P2)

As a developer troubleshooting OAuth issues, I want debug logs to capture comprehensive details about OAuth server interactions including HTTP headers, response bodies, token metadata, and timing information.

**Why this priority**: Current logs lack sufficient detail to diagnose OAuth failures. Enhanced logging is essential for understanding what the OAuth server is returning and why authentication might be failing.

**Independent Test**: Can be tested by enabling debug logging, triggering an OAuth flow, and verifying the logs contain HTTP headers, response details, and timing information.

**Acceptance Scenarios**:

1. **Given** debug logging is enabled, **When** mcpproxy makes an HTTP request to an OAuth authorization server, **Then** the request headers, URL, and relevant parameters are logged (with sensitive data redacted).

2. **Given** debug logging is enabled, **When** mcpproxy receives a response from an OAuth server, **Then** the response status, headers, and body structure are logged (with tokens redacted).

3. **Given** an OAuth token exchange occurs, **When** the exchange completes, **Then** the log includes token type, expiration time, scope, and timing information.

---

### User Story 4 - Coordinated OAuth Flow Execution (Priority: P3)

As a user, when mcpproxy attempts to reconnect to an OAuth-protected server, I want only one OAuth flow to run at a time per server, preventing race conditions that cause authentication failures.

**Why this priority**: The race condition bug causes OAuth flows to fail when multiple reconnection attempts trigger concurrent flows. While users can work around this by manually retrying, fixing this improves reliability.

**Independent Test**: Can be tested by triggering rapid reconnection attempts and verifying only one OAuth flow executes at a time, with subsequent attempts waiting for the active flow to complete.

**Acceptance Scenarios**:

1. **Given** an OAuth flow is in progress for a server, **When** another reconnection attempt triggers an OAuth flow for the same server, **Then** the second attempt waits for the first flow to complete rather than starting a new one.

2. **Given** an OAuth flow is in progress, **When** the flow completes successfully, **Then** waiting reconnection attempts use the newly obtained token.

3. **Given** an OAuth flow is in progress, **When** the flow fails, **Then** waiting reconnection attempts receive the failure and can decide whether to retry.

---

### User Story 5 - Comprehensive OAuth Testing (Priority: P1)

As a developer, I want comprehensive automated tests that verify all OAuth functionality works correctly, including token refresh, error handling, and edge cases.

**Why this priority**: The OAuth bugs were discovered through testing but proper regression tests are needed to prevent reintroduction. Testing is mandatory to validate the bug fixes.

**Independent Test**: Can be tested by running the OAuth E2E test suite with the OAuth test server configured for various scenarios (short TTL, error injection, etc.).

**Acceptance Scenarios**:

1. **Given** the OAuth test server is running with short token TTL, **When** the E2E test suite runs, **Then** tests verify token refresh functionality works correctly.

2. **Given** the OAuth test server is configured to inject errors, **When** tests run, **Then** error handling paths are verified.

3. **Given** OAuth E2E tests complete, **When** I review the test results, **Then** logs with correlation IDs are available for debugging any failures.

---

### Edge Cases

- What happens when the refresh token endpoint returns an error (invalid_grant, server_error)?
- How does the system handle network timeouts during token refresh?
- What happens if the token store file is corrupted or unreadable?
- How does the system behave when the OAuth server's JWKS endpoint is unreachable?
- What happens if a token refresh succeeds but the new token is immediately expired?
- How does the system handle concurrent OAuth flows across a mcpproxy restart?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST load persisted OAuth tokens into the token store when creating OAuth transport for reconnection attempts.
- **FR-002**: System MUST attempt token refresh using stored refresh token before triggering a new browser-based OAuth flow.
- **FR-003**: System MUST only prompt for re-authentication when both access token and refresh token are invalid or expired.
- **FR-004**: System MUST generate a unique correlation ID at the start of each OAuth flow and include it in all related log entries.
- **FR-005**: System MUST log OAuth HTTP requests with method, URL, and non-sensitive headers at debug level.
- **FR-006**: System MUST log OAuth HTTP responses with status code, headers, and response structure at debug level.
- **FR-007**: System MUST redact sensitive data (tokens, secrets, passwords) from all log entries.
- **FR-008**: System MUST use proper locking to ensure only one OAuth flow runs per server at a time.
- **FR-009**: Subsequent OAuth requests for the same server MUST wait for any in-progress flow to complete.
- **FR-010**: System MUST log token metadata (type, expiration, scope) when tokens are obtained or refreshed.
- **FR-011**: System MUST include timing information for OAuth operations in debug logs.
- **FR-012**: System MUST update browser rate limiting to track OAuth flows per-server rather than globally.

### Key Entities

- **OAuth Token**: Represents an OAuth access/refresh token pair with expiration metadata and the server it belongs to.
- **OAuth Flow**: Represents a single authentication attempt with a unique correlation ID, state, and outcome.
- **Token Store**: Persists and retrieves OAuth tokens for configured servers.
- **Correlation ID**: A unique identifier (UUID) that links all log entries related to a single OAuth flow.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: OAuth-protected servers remain connected continuously for at least 24 hours with tokens that have 3-minute TTL (verified by automated testing).
- **SC-002**: 100% of OAuth-related log entries include a correlation ID field when an OAuth flow is active.
- **SC-003**: Debug logs contain sufficient information to diagnose OAuth failures without requiring additional logging changes (verified by successfully debugging test-injected errors).
- **SC-004**: Zero concurrent OAuth flow conflicts observed during stress testing with rapid reconnection attempts.
- **SC-005**: All OAuth E2E tests pass consistently (100% pass rate over 10 consecutive runs).
- **SC-006**: Token refresh occurs automatically without user intervention when access tokens expire but refresh tokens are valid.

## Testing Requirements

### Test Scenarios Using OAuth Test Server

The following test scenarios MUST be executed using the OAuth test server (`tests/oauthserver/`):

1. **Token Refresh Flow**: Configure server with 30-second access token TTL, verify automatic refresh works.
2. **Persisted Token Loading**: Restart mcpproxy with expired access token, verify refresh occurs.
3. **Correlation ID Tracing**: Trigger OAuth flow, verify all logs share same correlation ID.
4. **Debug Log Content**: Enable debug logging, verify HTTP details are captured.
5. **Race Condition Prevention**: Trigger rapid reconnections, verify single OAuth flow executes.
6. **Error Handling**: Use error injection flags to test failure scenarios.
7. **Web UI Verification**: Use Playwright to verify OAuth status is correctly displayed in Web UI.
8. **REST API Verification**: Verify OAuth status is correctly returned in API responses.

### Testing Tools

- OAuth Test Server: `go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=30s`
- Playwright E2E Tests: `npx playwright test tests/e2e/oauth.spec.ts`
- Log Analysis: Filter by correlation_id to trace OAuth flows
- REST API: Verify `/api/v1/servers` returns correct OAuth status

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
feat(oauth): add correlation ID tracking for OAuth flows

Related #XXX

Added unique correlation IDs to OAuth flow logging to enable tracing
of authentication attempts through the log files.

## Changes
- Generate UUID at OAuth flow start
- Add correlation_id field to all OAuth-related log entries
- Include timing information in debug logs

## Testing
- Verified correlation IDs appear in all OAuth logs
- Tested with concurrent flows to ensure IDs are unique per flow
```

## Assumptions

- The OAuth test server (`tests/oauthserver/`) is available and functional for testing.
- Existing token persistence mechanism in mcpproxy correctly stores tokens to disk.
- The refresh token grant type is supported by OAuth servers that mcpproxy connects to.
- Debug logging can be enabled via `--log-level=debug` flag.
- Playwright and Node.js are available for E2E testing.
- The Web UI is accessible at the configured port for UI testing.
