# Feature Specification: OAuth Login Error Feedback

**Feature Branch**: `020-oauth-login-feedback`
**Created**: 2026-01-06
**Status**: Draft
**Input**: User description: "No need for quick fixes. Let's implement it properly. If something failed user should be able to see it as result of CLI login command"

## Problem Statement

The OAuth login flow (`POST /api/v1/servers/{id}/login`) currently provides minimal feedback to clients. When triggering OAuth:
- Clients don't know if the browser opened successfully
- Authorization URL is not returned for manual use
- Pre-flight validation errors aren't actionable
- Different clients (CLI, tray, Web UI) implement inconsistent behavior

**Current Behavior**:
1. Client triggers `POST /api/v1/servers/{id}/login`
2. Daemon attempts to open browser (may fail silently)
3. Response is generic success/failure with no details
4. Clients have no way to help users if browser didn't open

**Desired Behavior**:
1. Client triggers `POST /api/v1/servers/{id}/login`
2. Daemon validates server, attempts browser open
3. Response includes: `correlation_id`, `auth_url`, `browser_opened` status, and any errors
4. All clients (CLI, tray, Web UI) can display the auth URL when browser fails

## Multi-Client Model

This feature applies uniformly to all OAuth clients:

| Client | OAuth Trigger | Browser Failed Behavior |
|--------|---------------|------------------------|
| CLI | `mcpproxy auth login --server=X` | Print URL to terminal |
| Tray | Right-click menu → Login | Show notification with URL |
| Web UI | Server card → Login button | Display URL in modal |

All clients use the same `POST /api/v1/servers/{id}/login` endpoint and receive the same response payload. Client-specific UX decisions (how to display the URL) are out of scope for the daemon.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Pre-flight Validation (Priority: P1)

Before attempting OAuth, the daemon validates the server and returns actionable errors.

**Why this priority**: Fail fast with clear errors before wasting time on browser/OAuth flow.

**Independent Test**: Can be tested by calling login endpoint with invalid server name and verifying error response.

**Acceptance Scenarios**:

1. **Given** a non-existent server name, **When** client calls login endpoint, **Then** response includes `error_type: server_not_found` and list of available servers
2. **Given** a stdio-based server, **When** client calls login endpoint, **Then** response includes `error_type: oauth_not_supported` with reason
3. **Given** a disabled server, **When** client calls login endpoint, **Then** response includes `error_type: server_disabled` with enable instructions
4. **Given** a quarantined server, **When** client calls login endpoint, **Then** response includes `error_type: server_quarantined` with approval instructions

---

### User Story 2 - Browser Status in Response (Priority: P1)

The login response includes browser opening status so clients can decide whether to show the URL manually.

**Why this priority**: This is the core fix - clients need to know if the user can see the browser.

**Independent Test**: Can be tested by triggering login in headless environment and verifying `browser_opened: false` with `auth_url` in response.

**Acceptance Scenarios**:

1. **Given** browser opens successfully, **When** login endpoint is called, **Then** response includes `browser_opened: true` and `auth_url`
2. **Given** browser fails to open (headless, error), **When** login endpoint is called, **Then** response includes `browser_opened: false`, `auth_url`, and `browser_error` message
3. **Given** `HEADLESS=true` environment, **When** login endpoint is called, **Then** response includes `browser_opened: false` with `auth_url` for manual use

---

### User Story 3 - Correlation ID for Tracking (Priority: P1)

Each OAuth flow gets a unique correlation ID returned to the client for log correlation and status tracking.

**Why this priority**: Enables troubleshooting by correlating client requests with daemon logs.

**Independent Test**: Can be tested by triggering login and verifying UUID correlation_id in response.

**Acceptance Scenarios**:

1. **Given** valid server, **When** login endpoint is called, **Then** response includes unique `correlation_id` (UUID format)
2. **Given** correlation_id from login response, **When** checking daemon logs, **Then** OAuth flow entries include the same correlation_id

---

### User Story 4 - CLI Feedback (Priority: P2)

The CLI interprets the login response and displays appropriate user feedback.

**Why this priority**: CLI is the primary interface for developers; good feedback is essential.

**Independent Test**: Can be tested by running `auth login` and verifying CLI output matches response status.

**Acceptance Scenarios**:

1. **Given** browser opened successfully, **When** running `auth login`, **Then** CLI shows "Opening browser for authorization..."
2. **Given** browser failed to open, **When** running `auth login`, **Then** CLI shows "Could not open browser. Please open this URL manually: <url>"
3. **Given** validation error, **When** running `auth login`, **Then** CLI shows error message and exits with non-zero code
4. **Given** flow started successfully, **When** running `auth login`, **Then** CLI shows "OAuth flow started. Complete authorization in browser."

---

### Edge Cases

- What happens when daemon is not running? CLI shows clear error: "Daemon not running. Start with: mcpproxy serve"
- What happens when server is quarantined? Response includes `server_quarantined` error with approval instructions
- What happens when server is disabled? Response includes `server_disabled` error with enable instructions
- What happens with concurrent login attempts for same server? Response indicates flow already in progress with existing correlation_id

---

### User Story 5 - OAuth Runtime Error Classification (Priority: P1)

OAuth flows can fail AFTER pre-flight validation passes but BEFORE the browser opens. These runtime errors need structured classification with actionable feedback.

**Why this priority**: Users currently see generic "internal error (panic recovered)" messages with no actionable information. This is the root cause of issue #155 comments about Smithery servers.

**Independent Test**: Can be tested by configuring a server with broken OAuth metadata and verifying structured error response.

**Acceptance Scenarios**:

1. **Given** OAuth metadata discovery fails (404), **When** client calls login endpoint, **Then** response includes `error_type: oauth_metadata_missing` with details of what URL was checked
2. **Given** OAuth server returns malformed metadata, **When** client calls login endpoint, **Then** response includes `error_type: oauth_metadata_invalid` with parse error
3. **Given** DCR returns 403 and no client_id configured, **When** client calls login endpoint, **Then** response includes `error_type: oauth_client_id_required` with configuration example
4. **Given** protected resource URL doesn't match MCP URL (RFC 9728), **When** client calls login endpoint, **Then** response includes `error_type: oauth_resource_mismatch` with both URLs
5. **Given** GetAuthorizationURL panics, **When** client calls login endpoint, **Then** response includes `error_type: oauth_flow_failed` with correlation_id for log lookup

**OAuth Runtime Error Types**:

| error_type | error_code | Condition | Suggestion |
|------------|------------|-----------|------------|
| `oauth_metadata_missing` | `OAUTH_NO_METADATA` | Auth server doesn't expose `/.well-known/oauth-authorization-server` | "OAuth server metadata not available. Contact server administrator." |
| `oauth_metadata_invalid` | `OAUTH_BAD_METADATA` | Metadata is malformed or missing required fields | "OAuth metadata is malformed. Check server configuration." |
| `oauth_resource_mismatch` | `OAUTH_RESOURCE_MISMATCH` | Protected resource != MCP server URL (RFC 9728 violation) | "OAuth resource URL mismatch. This is a server configuration issue." |
| `oauth_client_id_required` | `OAUTH_NO_CLIENT_ID` | DCR failed (403) and no static client_id | "Configure `oauth.client_id` in server config." |
| `oauth_dcr_failed` | `OAUTH_DCR_FAILED` | DCR failed with unexpected error | "Dynamic Client Registration failed. Try configuring static credentials." |
| `oauth_flow_failed` | `OAUTH_FLOW_FAILED` | Generic OAuth flow failure (panic recovered) | "OAuth flow failed unexpectedly. Check logs with correlation_id." |

**Error Response Structure** (extends OAuthValidationError):

```json
{
  "success": false,
  "error_type": "oauth_metadata_missing",
  "error_code": "OAUTH_NO_METADATA",
  "server_name": "googledrive-smithery",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "request_id": "req-xyz-123",
  "message": "OAuth authorization server metadata not available",
  "details": {
    "server_url": "https://server.smithery.ai/googledrive",
    "protected_resource_metadata": {
      "found": true,
      "url_checked": "https://server.smithery.ai/.well-known/oauth-protected-resource/googledrive",
      "authorization_servers": ["https://auth.smithery.ai/googledrive"]
    },
    "authorization_server_metadata": {
      "found": false,
      "url_checked": "https://auth.smithery.ai/googledrive/.well-known/oauth-authorization-server",
      "error": "HTTP 404 Not Found"
    }
  },
  "suggestion": "The OAuth authorization server is not properly configured. Contact the server administrator or try a different server.",
  "debug_hint": "For logs: mcpproxy upstream logs googledrive-smithery | grep a1b2c3d4"
}
```

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Daemon MUST validate server existence before attempting OAuth and return `server_not_found` error with available server list
- **FR-002**: Daemon MUST validate OAuth applicability (protocol) and return `oauth_not_supported` error with reason
- **FR-003**: Daemon MUST check server state (enabled, not quarantined) and return appropriate error with remediation steps
- **FR-004**: Daemon MUST return `correlation_id` (UUID) for every OAuth start request
- **FR-005**: Daemon MUST return `auth_url` for every successful OAuth start (regardless of browser status)
- **FR-006**: Daemon MUST return `browser_opened` boolean indicating if browser launch succeeded
- **FR-007**: Daemon MUST return `browser_error` message when browser launch fails
- **FR-008**: CLI MUST display auth_url when `browser_opened` is false
- **FR-009**: CLI MUST exit with non-zero code on validation errors
- **FR-010**: All clients (CLI, tray, Web UI) MUST use the same login endpoint and interpret the same response format
- **FR-011**: Daemon MUST classify OAuth runtime errors with structured `error_type` and `error_code`
- **FR-012**: Daemon MUST include `details` object with metadata discovery status for OAuth errors
- **FR-013**: Daemon MUST include `request_id` (from PR #237) in all OAuth error responses
- **FR-014**: Daemon MUST include `debug_hint` with log grep command for debugging
- **FR-015**: CLI MUST display rich error output including details and debug_hint for OAuth runtime errors

### Key Entities

- **OAuthStartResponse**: Response from login endpoint with `correlation_id`, `auth_url`, `browser_opened`, `browser_error`, `message`
- **OAuthValidationError**: Pre-flight validation error with `error_type`, `message`, `suggestion`, `available_servers`
- **OAuthFlowError**: OAuth runtime error with `error_type`, `error_code`, `details`, `request_id`, `debug_hint`

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Clients receive auth_url in 100% of successful OAuth start requests
- **SC-002**: Browser opening failures are detectable via `browser_opened: false` response field
- **SC-003**: All validation errors include `error_type` and actionable `suggestion`
- **SC-004**: Correlation IDs appear in both API responses and daemon logs for all OAuth flows
- **SC-005**: CLI displays auth_url to stderr when browser fails to open
- **SC-006**: CLI exits with non-zero code for all validation errors
- **SC-007**: OAuth metadata discovery failures return structured error with `details.authorization_server_metadata`
- **SC-008**: All OAuth runtime errors include `request_id` for log correlation (requires PR #237)
- **SC-009**: CLI displays `debug_hint` for all OAuth runtime errors
- **SC-010**: Zero "internal error (panic recovered)" messages visible to users

## Known Issues (Post-Implementation)

### BUG-001: browser_opened Hardcoded to True (Fixed in PR #XXX)

**Problem**: The initial implementation at `internal/httpapi/server.go:1393` hardcoded `BrowserOpened: true` instead of using the actual browser status. This happened because:

1. `TriggerOAuthLogin()` called `StartManualOAuth()` which runs asynchronously
2. `StartManualOAuth()` returns `nil` immediately before the browser is opened
3. HTTP handler assumed `browser_opened: true` with comment "Assumed true since errors are returned above"

**Root Cause**: The OAuth flow was fully async, but the API needed synchronous feedback about browser status.

**Fix**: Created `StartManualOAuthQuick()` which:
1. Gets authorization URL synchronously
2. Checks HEADLESS environment variable
3. Attempts browser open and captures result
4. Returns `OAuthStartResult` with actual `BrowserOpened`, `AuthURL`, `BrowserError`
5. Continues OAuth callback handling in goroutine

**Files Changed**:
- `internal/upstream/core/connection.go` - Added `StartOAuthFlowQuick()`
- `internal/upstream/manager.go` - Added `StartManualOAuthQuick()`
- `internal/runtime/runtime.go` - Updated return type
- `internal/management/service.go` - Updated return type
- `internal/httpapi/server.go` - Use actual result

## Assumptions

- The daemon already has OAuth flow infrastructure (callback server, token handling)
- Browser opening logic exists but doesn't report success/failure
- OAuth completion is tracked via existing `servers.changed` events (no new events needed)

## Out of Scope

- Synchronous waiting for OAuth completion (keep current async model)
- New SSE event types for OAuth progress
- Changes to OAuth flow internals (token exchange, refresh)
- Client-specific UI implementations (tray notifications, Web UI modals)
- OAuth status polling endpoint (use `servers.changed` events or `GET /servers`)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #155` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #155`, `Closes #155`, `Resolves #155` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(api): add browser status and auth_url to OAuth login response

Related #155

Enhances OAuth login endpoint to return browser_opened status and
auth_url, enabling all clients (CLI, tray, Web UI) to display
manual authorization URL when browser fails to open.

## Changes
- Add correlation_id, auth_url, browser_opened to login response
- Add pre-flight validation with actionable error responses
- Update CLI to display auth_url when browser_opened is false

## Testing
- Verified browser_opened detection in headless environment
- Tested all validation error scenarios
- Confirmed correlation_id in logs matches response
```
