# Feature Specification: Structured Server State

**Feature Branch**: `013-structured-server-state`
**Created**: 2025-12-13
**Updated**: 2025-12-16
**Status**: Partially Complete
**Input**: User description: "Refactor server health and diagnostics to use structured state objects (OAuthState, ConnectionState), unify Doctor() to aggregate from server.Health, and fix broken web UI diagnostics Fix button"

## Implementation Status

**Completed** (merged to main via #192):
- ✅ `HealthStatus` struct with Level, AdminState, Summary, Detail, Action
- ✅ `health.CalculateHealth()` with priority logic (admin > connection > OAuth)
- ✅ `Health` field on `contracts.Server`
- ✅ Frontend `HealthStatus` TypeScript interface
- ✅ ServerCard.vue uses `server.health.action` for action buttons
- ✅ Dashboard "Servers Needing Attention" banner using `server.health`

**Remaining Work**:
- ❌ Structured state objects (`OAuthState`, `ConnectionState`)
- ❌ Doctor() aggregation from Health (still uses raw fields)
- ❌ Dashboard UI consolidation (still has separate diagnostics section)

## Problem Statement

The Dashboard currently has **two overlapping mechanisms** for displaying server health:

1. **"Servers Needing Attention" banner** - Uses unified `server.Health` with working action buttons
2. **System Diagnostics section** - Separate `/api/v1/diagnostics` endpoint with duplicate information

While the unified Health Status is now implemented, these systems still:
- Display the same issues through different UI components
- Use different data sources (`server.Health` vs `Doctor()` raw field access)
- Create user confusion with redundant displays

Additionally, server state uses flat fields rather than structured objects, making it harder to:
- Understand related state at a glance
- Extend with new fields without cluttering the struct
- Provide rich state information (retry counts, timestamps) to consumers

## User Scenarios & Testing *(mandatory)*

### User Story 1 - View Server Health Issues (Priority: P1)

As a developer using MCPProxy, I want to see a single, consistent view of server health issues across CLI and Web UI, so I can quickly identify and resolve problems.

**Why this priority**: Core user experience - users are currently confused by duplicate/inconsistent health displays

**Independent Test**: Can be fully tested by viewing Dashboard with unhealthy servers and verifying single consolidated health display with working action buttons

**Acceptance Scenarios**:

1. **Given** a server with an expired OAuth token, **When** I view the Dashboard, **Then** I see ONE health banner showing the issue with a working "Login" button
2. **Given** a server with a connection error, **When** I run `mcpproxy doctor`, **Then** I see the same error information as displayed in the Web UI
3. **Given** multiple servers with different issues, **When** I view any interface (CLI/Web UI/MCP tools), **Then** issues are categorized and actionable consistently

---

### User Story 2 - Access Rich Server State Information (Priority: P2)

As an API consumer (Web UI, CLI, MCP tools), I want server state organized into logical groups (OAuth, Connection), so I can build rich displays and make informed decisions.

**Why this priority**: Enables better tooling and UI while maintaining backwards compatibility

**Independent Test**: Can be tested by calling `/api/v1/servers` and verifying structured state objects are present alongside flat fields

**Acceptance Scenarios**:

1. **Given** a server with OAuth configured, **When** I fetch server details, **Then** I receive an `oauth_state` object with status, expiry, retry count, and last attempt time
2. **Given** a server in error state, **When** I fetch server details, **Then** I receive a `connection_state` object with status, error details, retry info, and connected duration
3. **Given** an existing API consumer using flat fields, **When** I fetch server details, **Then** flat fields (`Authenticated`, `OAuthStatus`, etc.) are still present and accurate

---

### User Story 3 - System-Level Health Checks (Priority: P2)

As an operator, I want to see system-level health issues (Docker daemon status) separately from per-server issues, so I can distinguish infrastructure problems from server configuration problems.

**Why this priority**: Clear separation of concerns helps with troubleshooting

**Independent Test**: Can be tested by running `mcpproxy doctor` with Docker isolation enabled but Docker daemon stopped

**Acceptance Scenarios**:

1. **Given** Docker isolation is enabled but Docker daemon is not running, **When** I run `mcpproxy doctor`, **Then** I see a system-level warning about Docker separately from per-server issues
2. **Given** a server fails because Docker is unavailable, **When** I view that server's health, **Then** the health status reflects the Docker-related failure

---

### User Story 4 - Backwards Compatible API (Priority: P3)

As an existing API consumer, I want my integrations to continue working without changes, so I don't have to update my code immediately.

**Why this priority**: Prevents breaking changes for existing users

**Independent Test**: Can be tested by running existing E2E tests without modification

**Acceptance Scenarios**:

1. **Given** an existing consumer reading `server.Authenticated`, **When** the refactor is deployed, **Then** the field returns the same value as before
2. **Given** an existing consumer reading `server.LastError`, **When** the refactor is deployed, **Then** the field returns the same value as before

---

### Edge Cases

- What happens when OAuth state changes mid-request? Health should reflect current state at time of request
- How does system handle servers with both OAuth AND connection errors? Prioritize by severity (connection > OAuth)
- What if Docker becomes available after initial check? Per-server health updates on next connection attempt
- What if a server has no OAuth configured? `oauth_state` should be null/omitted, not empty object

## Requirements *(mandatory)*

### Functional Requirements

#### Structured State Objects

- **FR-001**: System MUST provide an `OAuthState` object on servers with OAuth configured, containing: status, token_expires_at, last_attempt, retry_count, user_logged_out, has_refresh_token, error
- **FR-002**: System MUST provide a `ConnectionState` object on all servers, containing: status, connected_at, last_error, retry_count, last_retry_at, should_retry
- **FR-003**: ✅ DONE - System MUST continue to provide existing flat fields (Authenticated, OAuthStatus, Connected, etc.) for backwards compatibility
- **FR-004**: Flat fields and structured objects MUST contain consistent data (derived from same source)

#### Unified Health Calculation

- **FR-005**: ✅ DONE - System MUST calculate `Health` server-side from structured state objects
- **FR-006**: ✅ DONE - Health MUST include: level (healthy/degraded/unhealthy), admin_state (enabled/disabled/quarantined), summary (human-readable), detail (optional), action (suggested fix)
- **FR-007**: ✅ DONE - Health calculation MUST use priority: admin state > connection state > OAuth state > healthy
- **FR-008**: ✅ DONE - Health MUST be included in all server responses (API, MCP tools, CLI)

#### Doctor Aggregation

- **FR-009**: `Doctor()` MUST aggregate health issues from individual server Health objects (single source of truth)
- **FR-010**: `Doctor()` MUST NOT duplicate health detection logic that exists in Health calculation
- **FR-011**: ✅ DONE - `Doctor()` MUST include system-level checks: Docker daemon status (when isolation enabled)
- **FR-012**: `Doctor()` output MUST be consistent with what individual servers show in their Health

#### Web UI Consolidation

- **FR-013**: Dashboard MUST display ONE consolidated health banner (remove duplicate System Diagnostics banner)
- **FR-014**: ✅ DONE - Health banner MUST show actionable buttons (Login, Restart, Enable, Approve) that function correctly
- **FR-015**: Health banner MUST show aggregated counts (X errors, Y warnings) with ability to see details

### Key Entities

- **OAuthState**: Represents the OAuth authentication state for a server - status, token expiry, retry attempts, user logout flag
- **ConnectionState**: Represents the connection state for a server - connected/connecting/error status, uptime, error details, retry information
- **HealthStatus**: Calculated summary of server health - level, admin state, human-readable summary, suggested action
- **Diagnostics**: Aggregated system health - collection of server health issues plus system-level checks (Docker)

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users see exactly ONE health/diagnostics banner on Dashboard (down from two overlapping banners)
- **SC-002**: ✅ DONE - All action buttons (Login, Restart, Enable, Approve) in health displays function correctly
- **SC-003**: `mcpproxy doctor` output matches Web UI health information for same servers
- **SC-004**: ✅ DONE - Existing API consumers using flat fields experience zero breaking changes
- **SC-005**: Structured state objects provide at minimum: OAuth retry count, last attempt time, connection uptime, retry backoff status
- **SC-006**: ✅ DONE - Health calculation completes in under 10ms per server (no performance regression)

## Assumptions

- ✅ VALIDATED - The current `HealthStatus` structure (Level, AdminState, Summary, Detail, Action) is sufficient and does not need schema changes
- ✅ VALIDATED - Docker status is the only system-level check needed (disk space, index health are out of scope)
- Missing secrets detection can remain in Doctor() or be moved to per-server Health in a future iteration
- ✅ VALIDATED - The existing flat fields will be deprecated in a future major version but are not removed in this refactor

## Out of Scope

- Adding new health checks (disk space, index health, latency metrics)
- Removing deprecated flat fields
- Changes to the MCP protocol tool responses
- Mobile or alternative UI implementations

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
feat(health): add structured OAuthState to server response

Related #[issue-number]

Add OAuthState object to Server struct containing status, expiry,
retry count, and last attempt time. Flat fields maintained for
backwards compatibility.

## Changes
- Add OAuthState type to contracts/types.go
- Populate OAuthState in server serialization
- Add tests for OAuthState population

## Testing
- Unit tests pass
- E2E tests pass without modification (backwards compat verified)
```
