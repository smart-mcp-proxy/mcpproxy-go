# Feature Specification: Unified Health Status

**Feature Branch**: `012-unified-health-status`
**Created**: 2025-12-11
**Status**: Draft
**Input**: User description: "from docs/designs/2025-12-10-unified-health-status.md"
**Design Document**: [docs/designs/2025-12-10-unified-health-status.md](../../docs/designs/2025-12-10-unified-health-status.md)

## Problem Statement

MCPProxy currently displays inconsistent server health status across its three interfaces:

1. **CLI** reads `oauth_status` and shows "Token Expired"
2. **Tray** only checks HTTP connectivity and shows "Healthy"
3. **Web UI** may show different status based on its own interpretation

This leads to user confusion when the same server shows different states in different interfaces. Additionally, when servers have issues, users often don't know what action to take to resolve them.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Consistent Status Across Interfaces (Priority: P1)

As a user, I want to see the same health status for a server regardless of whether I'm using the CLI, tray, or web UI, so I can trust the information and not be confused by conflicting reports.

**Why this priority**: This is the core problem - inconsistent status erodes trust and causes confusion. Without this, all other improvements are undermined.

**Independent Test**: Can be tested by checking any server's status in all three interfaces and verifying they show identical health level and summary.

**Acceptance Scenarios**:

1. **Given** a server with an expired OAuth token, **When** I check status in CLI, tray, and web UI, **Then** all three show "unhealthy" status with the same summary message.
2. **Given** a healthy connected server, **When** I check status in CLI, tray, and web UI, **Then** all three show "healthy" status with matching tool counts.
3. **Given** a disabled server, **When** I check status in all interfaces, **Then** all three show "disabled" admin state consistently.

---

### User Story 2 - Actionable Guidance for Issues (Priority: P1)

As a user, when a server has an issue, I want to see what action I should take to fix it, so I don't have to guess or search documentation.

**Why this priority**: Equally critical to consistency - users need to know HOW to fix problems, not just that problems exist.

**Independent Test**: Can be tested by creating various error conditions and verifying each displays an appropriate action.

**Acceptance Scenarios**:

1. **Given** a server with expired OAuth token, **When** I view its status, **Then** I see an action suggesting to login (CLI shows command, tray/web show button).
2. **Given** a server with connection refused error, **When** I view its status, **Then** I see an action suggesting to restart.
3. **Given** a healthy server, **When** I view its status, **Then** no action is shown (none needed).

---

### User Story 3 - OAuth Token Visibility in Tray/Web (Priority: P2)

As a user, I want to see OAuth token issues (expired, expiring soon) in the tray and web UI, not just the CLI, so I'm aware of authentication problems across all interfaces.

**Why this priority**: Addresses a specific gap where OAuth status was only visible in CLI, which many users don't use regularly.

**Independent Test**: Can be tested by letting an OAuth token expire and verifying tray and web UI both indicate the issue.

**Acceptance Scenarios**:

1. **Given** a server with OAuth token expiring in 30 minutes (and no refresh token), **When** I view the tray menu, **Then** I see a yellow/degraded status indicator with "Token expiring" message.
2. **Given** a server with expired OAuth token, **When** I view the web dashboard, **Then** I see the server listed as needing attention with a Login action.

---

### User Story 4 - Admin State Separate from Health (Priority: P2)

As a user, I want disabled and quarantined servers to show their admin state clearly distinct from health status, so I understand they're intentionally inactive rather than broken.

**Why this priority**: Prevents confusion between "server is off" and "server is broken".

**Independent Test**: Can be tested by disabling a server and verifying it shows disabled state, not an error.

**Acceptance Scenarios**:

1. **Given** a disabled server, **When** I view its status, **Then** I see "Disabled" admin state (not "unhealthy" or "error").
2. **Given** a quarantined server, **When** I view its status, **Then** I see "Quarantined" admin state with an "approve" action.

---

### User Story 5 - Dashboard Shows Servers Needing Attention (Priority: P3)

As a user, I want the web dashboard to highlight servers that need attention (degraded or unhealthy), so I can quickly identify and fix issues.

**Why this priority**: Quality-of-life improvement that builds on the core health status feature.

**Independent Test**: Can be tested by having a mix of healthy and unhealthy servers and verifying dashboard shows the right count/list.

**Acceptance Scenarios**:

1. **Given** 3 healthy servers and 2 unhealthy servers, **When** I view the dashboard, **Then** I see "2 servers need attention" with quick-fix buttons.
2. **Given** all servers healthy, **When** I view the dashboard, **Then** I see no "needs attention" banner.

---

### Edge Cases

- What happens when a server is both disabled AND has an expired token? Admin state takes precedence - show "Disabled".
- How does system handle servers that are connecting but not yet ready? Show "degraded" with no action required.
- What if OAuth auto-refresh is working but token is about to expire? Show "healthy" - auto-refresh handles it automatically.
- What if token has no expiration time set? Assume valid if no explicit expiration.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST calculate a single unified health status in the backend for each server
- **FR-002**: System MUST include health level (healthy/degraded/unhealthy) in the status
- **FR-003**: System MUST include admin state (enabled/disabled/quarantined) separate from health
- **FR-004**: System MUST include a human-readable summary message in the status
- **FR-005**: System MUST include an action type (login/restart/enable/approve/view_logs) when applicable
- **FR-006**: CLI MUST display health status with appropriate emoji indicators
- **FR-007**: CLI MUST display action as a command hint (e.g., "auth login --server=X")
- **FR-008**: Tray MUST display health status with consistent emoji indicators matching CLI
- **FR-009**: Tray MUST provide clickable actions that resolve the issue (open web UI or trigger API)
- **FR-010**: Web UI MUST display health status with colored badges
- **FR-011**: Web UI MUST display action buttons appropriate to each issue type
- **FR-012**: Dashboard MUST show count of servers needing attention
- **FR-013**: Admin state MUST take precedence over health when server is not enabled
- **FR-014**: OAuth token expiration MUST be considered unhealthy (not degraded)
- **FR-015**: OAuth token expiring soon with no refresh token MUST be considered degraded
- **FR-016**: OAuth token with working auto-refresh MUST be considered healthy regardless of expiration time

### Key Entities

- **HealthStatus**: Represents the unified health of a server
  - Level: healthy, degraded, or unhealthy
  - AdminState: enabled, disabled, or quarantined
  - Summary: Human-readable status message
  - Detail: Optional longer explanation
  - Action: Suggested fix action type

- **Server**: Existing entity extended with Health field
  - All existing fields preserved
  - New Health field containing HealthStatus

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All three interfaces (CLI, tray, web) display identical health level for any given server
- **SC-002**: 100% of unhealthy/degraded states include an appropriate action suggestion
- **SC-003**: Users can identify and fix server issues without consulting documentation
- **SC-004**: OAuth token expiration is visible in tray and web UI (not just CLI)
- **SC-005**: Admin state (disabled/quarantined) is visually distinct from health issues in all interfaces

## Assumptions

- All clients (CLI, tray, web) are deployed together, so no backward compatibility is needed
- The existing `/api/v1/servers` endpoint will be extended to include the health field
- Token expiration threshold for "expiring soon" warning is configurable (default: 1 hour)
- Auto-refresh working means the system will handle token renewal automatically

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
