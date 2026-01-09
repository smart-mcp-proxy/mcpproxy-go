# Feature Specification: Request ID Logging

**Feature Branch**: `021-request-id-logging`
**Created**: 2026-01-07
**Status**: Draft
**Input**: User description: "Create a request ID mechanism for all clients (CLI, tray, Web UI) to enable request-scoped logging and error tracking"

## Problem Statement

When errors occur in mcpproxy, users and developers have difficulty correlating client-side errors with server-side logs. Currently:
- No standard way to trace a request through the system
- Error messages don't include identifiers for log lookup
- OAuth flows use `correlation_id` but other requests have no tracking
- Users can't easily report issues with reproducible context

**Current Behavior**:
1. Client makes request to daemon
2. Error occurs and is returned to client
3. User sees error message but cannot find related logs
4. Support/debugging requires manual timestamp correlation

**Desired Behavior**:
1. Client sends `X-Request-Id` header (or server generates one)
2. All request-scoped logs include this `request_id`
3. Error responses include `request_id` in JSON payload
4. CLI prints "Request ID: <id>" on error with log lookup suggestion
5. Tray/Web UI display Request ID with "Copy ID" affordance

## Multi-Client Model

This feature applies uniformly to all clients:

| Client | Request ID Source | Error Display | Log Retrieval |
|--------|------------------|---------------|---------------|
| CLI | Optional `X-Request-Id` header; core generates if missing | Print to stderr | `mcpproxy logs --request-id <id>` |
| Tray | Optional `X-Request-Id` header; core generates if missing | Notification with copy button | Link to logs endpoint |
| Web UI | Optional `X-Request-Id` header; core generates if missing | Modal with copy button | Display logs inline or link |

**Request ID Generation**: Clients are NOT required to send `X-Request-Id`. If omitted, mcpproxy core generates a random UUID v4 and uses it for the request. The generated ID is returned in the `X-Request-Id` response header and included in error response bodies.

All clients use the same REST API endpoints. The server always returns `X-Request-Id` in response headers.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Request ID in Error Responses (Priority: P1)

Every error response includes a `request_id` field that users can use to find related logs.

**Why this priority**: Core value proposition - enables log correlation without any client changes.

**Independent Test**: Make any API call that returns an error and verify `request_id` in response body and `X-Request-Id` header.

**Acceptance Scenarios**:

1. **Given** a client makes a request without `X-Request-Id`, **When** an error occurs, **Then** response includes generated `request_id` in JSON body and `X-Request-Id` header
2. **Given** a client sends `X-Request-Id: abc123`, **When** an error occurs, **Then** response includes `request_id: "abc123"` in JSON body and `X-Request-Id: abc123` header
3. **Given** a client sends `X-Request-Id: abc123`, **When** request succeeds, **Then** response includes `X-Request-Id: abc123` header (body may or may not include it)

---

### User Story 2 - Request-Scoped Logging (Priority: P1)

All log entries for a request include the `request_id`, enabling filtering.

**Why this priority**: Without this, the request ID has no value - logs must be findable.

**Independent Test**: Make a request with `X-Request-Id`, check daemon logs contain the ID.

**Acceptance Scenarios**:

1. **Given** a request with `X-Request-Id: abc123`, **When** server processes it, **Then** all log entries include `request_id=abc123`
2. **Given** a request without `X-Request-Id`, **When** server generates `xyz789`, **Then** all log entries include `request_id=xyz789`
3. **Given** an OAuth flow with `correlation_id`, **When** started via request with `request_id`, **Then** logs include both `request_id` and `correlation_id`

---

### User Story 3 - CLI Error Display (Priority: P2)

CLI displays the request ID on errors and suggests how to retrieve logs.

**Why this priority**: Primary developer interface; immediate value for debugging.

**Independent Test**: Run a CLI command that fails and verify Request ID is printed with log suggestion.

**Acceptance Scenarios**:

1. **Given** `mcpproxy upstream list` fails with server error, **When** CLI receives error response, **Then** stderr shows `Request ID: <id>` and `Run 'mcpproxy logs --request-id <id>' to see detailed logs`
2. **Given** `mcpproxy auth login` fails validation, **When** CLI receives 400 error, **Then** stderr shows Request ID with log suggestion
3. **Given** request succeeds, **When** CLI receives success response, **Then** Request ID is NOT displayed (avoid noise)

---

### User Story 4 - Log Retrieval by Request ID (Priority: P2)

Users can retrieve logs filtered by request ID.

**Why this priority**: Completes the debugging workflow started by error display.

**Independent Test**: Make request, get request ID from error, retrieve logs using that ID.

**Acceptance Scenarios**:

1. **Given** a request ID from an error, **When** user runs `mcpproxy logs --request-id <id>`, **Then** only logs with that request ID are displayed
2. **Given** a request ID, **When** user calls `GET /api/v1/logs?request_id=<id>`, **Then** response contains filtered log entries
3. **Given** a non-existent request ID, **When** user queries logs, **Then** empty result is returned (not an error)

---

### User Story 5 - Tray/Web UI Error Display (Priority: P3)

Tray and Web UI display Request ID on errors with copy affordance.

**Why this priority**: Important for non-CLI users but lower priority than CLI workflow.

**Independent Test**: Trigger an error via tray menu or Web UI and verify Request ID is displayed with copy button.

**Acceptance Scenarios**:

1. **Given** tray triggers login that fails, **When** error notification appears, **Then** notification includes Request ID with "Copy ID" action
2. **Given** Web UI makes request that fails, **When** error modal appears, **Then** modal includes Request ID with copy button and link to logs
3. **Given** successful operation in tray/Web UI, **When** success is displayed, **Then** Request ID is NOT shown (reduce clutter)

---

### Edge Cases

- What happens when `X-Request-Id` header contains invalid characters? Server sanitizes or rejects with 400
- What happens when `X-Request-Id` is extremely long (>256 chars)? Server truncates to 256 chars
- What happens when multiple requests have same `X-Request-Id`? Logs share the ID (caller's responsibility)
- What happens when daemon restarts during request? Request ID is lost; logs from new process won't have it
- What happens with WebSocket/SSE connections? Request ID applies to initial connection request

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: mcpproxy core MUST generate a random UUID v4 `request_id` for requests without `X-Request-Id` header (clients are NOT required to provide one)
- **FR-002**: mcpproxy core MUST use client-provided `X-Request-Id` value when present and valid
- **FR-003**: mcpproxy core MUST return `X-Request-Id` header in ALL responses (success and error)
- **FR-004**: mcpproxy core MUST include `request_id` field in ALL error JSON responses
- **FR-005**: mcpproxy core MUST include `request_id` in all log entries for request-scoped operations
- **FR-006**: mcpproxy core MUST validate `X-Request-Id` header (alphanumeric, dashes, underscores, max 256 chars)
- **FR-007**: CLI MUST display Request ID on error responses with log retrieval suggestion
- **FR-008**: CLI MUST NOT display Request ID on successful responses
- **FR-009**: Server MUST provide endpoint or CLI command to retrieve logs by `request_id`
- **FR-010**: OAuth flows MUST include both `request_id` (from triggering request) and `correlation_id` in logs
- **FR-011**: Request ID MUST NOT contain sensitive information (safe to display to users)

### Key Entities

- **RequestContext**: Request-scoped context containing `request_id`, propagated through handlers
- **LogEntry**: Extended to include optional `request_id` field for request-scoped logs
- **ErrorResponse**: Standard error response structure including `request_id`

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of API error responses include `request_id` field in JSON body
- **SC-002**: 100% of API responses include `X-Request-Id` header
- **SC-003**: CLI displays Request ID for all error cases with log lookup suggestion
- **SC-004**: Logs can be filtered by `request_id` returning only matching entries
- **SC-005**: OAuth flow logs are findable by either `request_id` or `correlation_id`
- **SC-006**: Request ID generation adds less than 1ms latency to requests

## Security and Privacy Considerations

### Request ID Safety

Request IDs are designed to be **safe for display to end users**:

- Generated IDs are random UUIDs with no embedded information
- Client-provided IDs are validated (alphanumeric, dashes, underscores only)
- IDs do not contain secrets, tokens, or PII
- IDs are not used for authentication or authorization
- IDs can be safely shared in bug reports, support tickets, and logs

### Privacy

- Request IDs themselves do not identify users
- Log retrieval by request ID does not bypass access controls
- API key authentication still required for log retrieval endpoints

### Abuse Prevention

- Maximum ID length (256 chars) prevents memory exhaustion
- Character validation prevents injection attacks
- Rate limiting applies to log retrieval endpoints (existing limits)

## Assumptions

- Daemon already has structured logging via Zap
- Activity log infrastructure exists (spec 016) and can be extended
- CLI already handles error responses and exit codes
- Web UI has error handling infrastructure
- Tray shows notifications for errors

## Out of Scope

- Distributed tracing (spans, parent IDs) - request ID is single-hop only
- Request ID persistence across daemon restarts
- Automatic log cleanup based on request ID
- Request ID in MCP protocol messages (only REST API)
- Tray/Web UI implementation (they receive response, implement their own UX)

## Integration with OAuth Login Feedback (Spec 020)

The `correlation_id` in OAuth responses is complementary to `request_id`:

| ID Type | Scope | Purpose |
|---------|-------|---------|
| `request_id` | Single HTTP request | Correlate request with immediate logs |
| `correlation_id` | OAuth flow (may span multiple requests) | Track entire OAuth flow across callbacks |

When `POST /api/v1/servers/{id}/login` is called:
- Response includes both `request_id` (for this request) and `correlation_id` (for OAuth flow)
- Logs include both IDs
- User can search logs by either ID

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(api): add request ID to all API responses and error payloads

Related #XXX

Implements request-scoped logging with X-Request-Id header support.
Server generates UUID if client doesn't provide one. All error
responses include request_id for log correlation.

## Changes
- Add X-Request-Id header processing middleware
- Include request_id in all error JSON responses
- Add request_id to structured log context
- Add --request-id flag to logs command

## Testing
- Verified request ID in error responses
- Tested client-provided vs server-generated IDs
- Confirmed logs filterable by request_id
```
