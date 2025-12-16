# Feature Specification: Structured Server State

**Feature Branch**: `013-structured-server-state`
**Created**: 2025-12-13
**Updated**: 2025-12-16
**Status**: Ready for Implementation
**Depends On**: #192 (Unified Health Status) - merged to main

## Problem Statement

The Dashboard currently has **two overlapping mechanisms** for displaying server health:

1. **"Servers Needing Attention" banner** - Uses unified `server.Health` with working action buttons
2. **System Diagnostics section** - Separate `/api/v1/diagnostics` endpoint with duplicate information

These systems:
- Display the same issues through different UI components
- Use different data sources (`server.Health` vs `Doctor()` raw field access)
- Create user confusion with redundant displays

Additionally, server state uses flat fields rather than structured objects, making it harder to:
- Understand related state at a glance
- Extend with new fields without cluttering the struct
- Provide rich state information (retry counts, timestamps) to consumers

## User Scenarios & Testing

### User Story 1 - Single Consolidated Health Display (Priority: P1)

As a developer using MCPProxy, I want to see a single, consistent view of server health issues across CLI and Web UI.

**Acceptance Scenarios**:

1. **Given** a server with an expired OAuth token, **When** I view the Dashboard, **Then** I see ONE health banner (not two)
2. **Given** a server with a connection error, **When** I run `mcpproxy doctor`, **Then** I see the same error information as displayed in the Web UI
3. **Given** multiple servers with different issues, **When** I view the Dashboard, **Then** issues are shown with aggregated counts (X errors, Y warnings)

---

### User Story 2 - Rich Server State Information (Priority: P2)

As an API consumer, I want server state organized into logical groups (OAuth, Connection) for building rich displays.

**Acceptance Scenarios**:

1. **Given** a server with OAuth configured, **When** I fetch server details, **Then** I receive an `oauth_state` object with status, expiry, retry count, and last attempt time
2. **Given** a server in error state, **When** I fetch server details, **Then** I receive a `connection_state` object with status, error details, retry info, and connected duration
3. **Given** an existing API consumer using flat fields, **When** I fetch server details, **Then** flat fields are still present and accurate

---

### Edge Cases

- What if a server has no OAuth configured? `oauth_state` should be null/omitted, not empty object
- How are flat fields and structured objects kept consistent? Derived from same source

## Requirements

### Structured State Objects

- **FR-001**: System MUST provide an `OAuthState` object on servers with OAuth configured, containing: status, token_expires_at, last_attempt, retry_count, user_logged_out, has_refresh_token, error
- **FR-002**: System MUST provide a `ConnectionState` object on all servers, containing: status, connected_at, last_error, retry_count, last_retry_at, should_retry
- **FR-003**: Flat fields and structured objects MUST contain consistent data (derived from same source)

### Doctor Aggregation

- **FR-004**: `Doctor()` MUST aggregate health issues from individual server Health objects (single source of truth)
- **FR-005**: `Doctor()` MUST NOT duplicate health detection logic that exists in Health calculation
- **FR-006**: `Doctor()` output MUST be consistent with what individual servers show in their Health

### Web UI Consolidation

- **FR-007**: Dashboard MUST display ONE consolidated health banner (remove duplicate System Diagnostics section)
- **FR-008**: Health banner MUST show aggregated counts (X errors, Y warnings) with ability to see details

### Key Entities

- **OAuthState**: OAuth authentication state - status, token expiry, retry attempts, user logout flag
- **ConnectionState**: Connection state - connected/connecting/error status, uptime, error details, retry information

## Success Criteria

- **SC-001**: Users see exactly ONE health/diagnostics banner on Dashboard (down from two)
- **SC-002**: `mcpproxy doctor` output matches Web UI health information for same servers
- **SC-003**: Structured state objects provide: OAuth retry count, last attempt time, connection uptime, retry backoff status

## Assumptions

- Missing secrets detection can remain in Doctor() or be moved to per-server Health in a future iteration

## Out of Scope

- Adding new health checks (disk space, index health, latency metrics)
- Removing deprecated flat fields
- Changes to the MCP protocol tool responses

## Commit Message Conventions

### Issue References
- Use: `Related #[issue-number]` - Links without auto-closing
- Do NOT use: `Fixes #`, `Closes #`, `Resolves #` - These auto-close on merge

### Co-Authorship
- Do NOT include AI attribution in commits

### Example Commit Message
```
feat(health): add structured OAuthState to server response

Related #[issue-number]

Add OAuthState object to Server struct containing status, expiry,
retry count, and last attempt time. Flat fields maintained for
backwards compatibility.
```
