# Feature Specification: Activity Log Backend

**Feature Branch**: `016-activity-log-backend`
**Created**: 2025-12-26
**Status**: Draft
**Input**: User description: "Implement Activity Log Backend with storage, REST API, and SSE events (RFC-003)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Query Tool Call History via REST API (Priority: P1)

Users and AI agents need to retrieve a history of tool calls made through mcpproxy to understand what actions have been performed, debug issues, and audit agent behavior.

**Why this priority**: This is the core value - visibility into what AI agents are doing. Without this, users have no insight into agent activity.

**Independent Test**: Can be fully tested by calling `GET /api/v1/activity` after making some tool calls and verifying the history is returned with correct details.

**Acceptance Scenarios**:

1. **Given** 10 tool calls have been made, **When** a client calls `GET /api/v1/activity`, **Then** the response contains the 10 activity records with tool name, server, timestamp, and status
2. **Given** tool calls from multiple servers exist, **When** a client calls `GET /api/v1/activity?server=github`, **Then** only activities for the "github" server are returned
3. **Given** both successful and failed tool calls exist, **When** a client calls `GET /api/v1/activity?status=error`, **Then** only failed activities are returned
4. **Given** 1000 activity records exist, **When** a client calls `GET /api/v1/activity?limit=50&offset=100`, **Then** records 101-150 are returned with pagination info

---

### User Story 2 - Real-time Activity Notifications via SSE (Priority: P1)

The tray application and web UI need to receive real-time notifications when tool calls are made so they can update their displays without polling.

**Why this priority**: Real-time visibility is essential for monitoring agent behavior as it happens.

**Independent Test**: Can be tested by connecting to SSE stream and verifying events arrive when tool calls are made.

**Acceptance Scenarios**:

1. **Given** a client is connected to the SSE stream, **When** a tool call starts, **Then** an `activity.tool_call.started` event is pushed with tool name and server
2. **Given** a client is connected to the SSE stream, **When** a tool call completes, **Then** an `activity.tool_call.completed` event is pushed with duration and status
3. **Given** a client is connected to the SSE stream, **When** a tool call is blocked by policy, **Then** an `activity.policy_decision` event is pushed with the reason

---

### User Story 3 - View Activity Details (Priority: P2)

Users investigating a specific tool call need to view its full details including request arguments and response data.

**Why this priority**: Detailed view is important for debugging but less frequently used than the list view.

**Independent Test**: Can be tested by calling `GET /api/v1/activity/{id}` and verifying full details are returned.

**Acceptance Scenarios**:

1. **Given** a tool call with ID "act_abc123" exists, **When** a client calls `GET /api/v1/activity/act_abc123`, **Then** full details including arguments and response are returned
2. **Given** a tool call does not exist, **When** a client calls `GET /api/v1/activity/unknown`, **Then** a 404 error is returned

---

### User Story 4 - Export Activity for Compliance (Priority: P3)

Enterprise users need to export activity logs for compliance and audit purposes in standard formats.

**Why this priority**: Compliance is important for enterprise but not needed for initial MVP.

**Independent Test**: Can be tested by calling `GET /api/v1/activity/export?format=json` and verifying downloadable file is returned.

**Acceptance Scenarios**:

1. **Given** activity records exist, **When** a client calls `GET /api/v1/activity/export?format=json`, **Then** a JSON file with all matching records is returned
2. **Given** activity records exist, **When** a client calls `GET /api/v1/activity/export?format=csv`, **Then** a CSV file with all matching records is returned

---

### Edge Cases

- What happens when storage is full? (Implement retention policy, delete oldest records first)
- What happens when activity record is created during system shutdown? (Ensure graceful handling, don't block shutdown)
- How are very large tool responses handled? (Truncate response body, store full response separately if needed)
- What happens when SSE client disconnects and reconnects? (Client can request missed events using last event ID)
- How is activity data protected? (Same API key authentication as other endpoints)

## Requirements *(mandatory)*

### Functional Requirements

**Activity Recording**:
- **FR-001**: System MUST record all tool calls with: id, server_name, tool_name, arguments, response, status, duration, timestamp
- **FR-002**: System MUST record policy decisions (tool calls blocked by policy rules)
- **FR-003**: System MUST record quarantine events (server quarantine/unquarantine)
- **FR-004**: Activity records MUST be persisted to durable storage
- **FR-005**: System MUST generate unique IDs for each activity record

**REST API Endpoints**:
- **FR-006**: System MUST provide `GET /api/v1/activity` endpoint with filtering and pagination
- **FR-007**: System MUST support filters: type, server, session, status, start_time, end_time
- **FR-008**: System MUST support pagination with limit and offset parameters
- **FR-009**: System MUST provide `GET /api/v1/activity/{id}` for single record details
- **FR-010**: System MUST provide `GET /api/v1/activity/export` for bulk export
- **FR-011**: Export endpoint MUST support format parameter (json, csv)

**SSE Events**:
- **FR-012**: System MUST emit `activity.tool_call.started` event when tool call begins
- **FR-013**: System MUST emit `activity.tool_call.completed` event when tool call finishes
- **FR-014**: System MUST emit `activity.policy_decision` event when policy blocks a call
- **FR-015**: SSE events MUST include activity ID for correlation

**Data Management**:
- **FR-016**: System MUST implement configurable retention policy (default 90 days)
- **FR-017**: System MUST limit stored records (default 100,000 records)
- **FR-018**: System MUST automatically prune old records based on retention policy

**Architecture Requirements**:
- **FR-019**: Activity recording MUST be non-blocking (use event-driven approach)
- **FR-020**: Activity service MUST be a singleton shared across all interfaces
- **FR-021**: Activity service MUST use internal event bus for decoupling
- **FR-022**: Storage operations MUST minimize lock contention

### Key Entities

- **ActivityRecord**: A single recorded activity with id, type (tool_call, policy_decision, quarantine), server_name, tool_name, arguments, response, status, duration_ms, timestamp, and optional session_id.

- **ActivityType**: Enumeration of activity types - tool_call, policy_decision, quarantine, server_change.

- **ActivityFilter**: Query parameters for filtering activity records - type, server, session, status, start_time, end_time.

- **ActivityEvent**: SSE event payload for real-time notifications including event type, activity ID, and relevant details.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Activity records are queryable within 100ms for up to 10,000 records (verifiable by load testing API)
- **SC-002**: SSE events are delivered to connected clients within 50ms of activity occurrence (verifiable by timing tests)
- **SC-003**: System handles 100 concurrent tool calls without data loss (verifiable by stress testing)
- **SC-004**: Storage cleanup runs without blocking normal operations (verifiable by monitoring during cleanup)
- **SC-005**: Export of 10,000 records completes within 10 seconds (verifiable by timing export operation)
- **SC-006**: All activity endpoints are authenticated with existing API key mechanism (verifiable by testing without auth)

## Assumptions

- Existing BBolt database is suitable for activity storage (already used for tool call history)
- The existing SSE infrastructure (`/events` endpoint) can be extended for activity events
- Activity recording does not need to survive server restarts (in-flight activities are lost)
- Response bodies may be truncated for storage efficiency (configurable limit)

## Dependencies

- **Existing Components**:
  - `internal/storage/`: BBolt database layer to extend
  - `internal/server/sse.go`: SSE event infrastructure
  - `internal/runtime/event_bus.go`: Internal event bus
  - `internal/httpapi/`: REST API handlers

- **Existing Models**:
  - Tool call recording (partial - to be extended)
  - Session tracking (to be integrated)

## Out of Scope

- CLI commands for activity (separate spec 017)
- Web UI for activity (to be added to existing web UI)
- Intent declaration (Phase 2 of RFC-003)
- PII detection (future RFC-004)
- Risk scoring (future RFC-004)
- OpenTelemetry export (future enhancement)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]`

**Rationale**: Issues should only be closed manually after verification and testing in production.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors.

### Example Commit Message
```
feat(activity): add activity log backend with REST API and SSE

Related #[issue-number]

Implement activity recording and query system as per RFC-003.
Provides visibility into AI agent tool calls for monitoring and audit.

## Changes
- Add ActivityRecord schema to storage layer
- Add GET /api/v1/activity endpoint with filtering
- Add GET /api/v1/activity/{id} for detail view
- Add GET /api/v1/activity/export for bulk export
- Emit SSE events for real-time activity updates
- Implement retention policy for storage management

## Testing
- Unit tests for activity storage
- E2E tests for REST endpoints
- SSE event delivery tests
```
