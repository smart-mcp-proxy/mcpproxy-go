# Feature Specification: Structured Server State

**Feature Branch**: `013-structured-server-state`
**Created**: 2025-12-13
**Updated**: 2025-12-16
**Status**: Ready for Implementation
**Depends On**: #192 (Unified Health Status) - merged to main

## Problem Statement

The system has two separate data sources for server health:

1. **Health Status** (`server.health`) - Per-server health with action buttons
2. **Diagnostics** (`/api/v1/diagnostics`) - System-wide aggregation with CLI hints

These systems detect the same issues independently, leading to:
- Duplicate banners on the Dashboard showing identical information
- Inconsistent data between CLI (`mcpproxy doctor`) and Web UI
- "Fix" buttons that show CLI hints instead of navigating to relevant pages

**Goal**: Health becomes the single source of truth for per-server issues. Diagnostics aggregates from Health for system-wide views.

## User Scenarios & Testing

### User Story 1 - Fix Server Issues via Web UI (Priority: P1)

As a developer, when I see a server health issue, I want the "Fix" button to navigate me to the right place to fix it.

**Acceptance Scenarios**:

1. **Given** a server with a missing secret, **When** I click Fix, **Then** I navigate to `/ui/secrets`
2. **Given** a server with OAuth config issue, **When** I click Fix, **Then** I navigate to server config tab
3. **Given** a server with connection error, **When** I click Restart, **Then** the server restarts

---

### User Story 2 - Consistent CLI and Web UI (Priority: P1)

As a developer, I want `mcpproxy doctor` and the Web UI to show the same issues.

**Acceptance Scenarios**:

1. **Given** a missing secret, **When** I run `mcpproxy doctor`, **Then** I see the same servers affected as in the Web UI
2. **Given** an OAuth issue, **When** I view Dashboard, **Then** I see the same information as `mcpproxy doctor --json`

---

### User Story 3 - Single Health Banner (Priority: P2)

As a developer, I want to see ONE consolidated health display on the Dashboard.

**Acceptance Scenarios**:

1. **Given** servers with issues, **When** I view Dashboard, **Then** I see one "Servers Needing Attention" section (not two separate banners)

---

## Requirements

### Health as Source of Truth

- **FR-001**: Health MUST detect missing secrets and set `action: "set_secret"` with secret name in `detail`
- **FR-002**: Health MUST detect OAuth config issues and set `action: "configure"` with error in `detail`
- **FR-003**: Health MUST be the single source of truth for all per-server issues

### Health Actions

- **FR-004**: Health actions MUST include: `login`, `restart`, `enable`, `approve`, `set_secret`, `configure`, `view_logs`, `""`
- **FR-005**: Each action MUST map to a UI navigation target or in-place action

| Action | UI Behavior |
|--------|-------------|
| `login` | Trigger OAuth flow |
| `restart` | Restart the server |
| `enable` | Enable the server |
| `approve` | Unquarantine the server |
| `set_secret` | Navigate to `/ui/secrets` |
| `configure` | Navigate to server config tab |
| `view_logs` | Navigate to server logs tab |
| `""` | No action needed |

### Diagnostics Aggregation

- **FR-006**: Diagnostics MUST aggregate from individual server Health objects
- **FR-007**: Diagnostics MUST NOT have independent detection logic for issues that Health already detects
- **FR-008**: Diagnostics MUST group `set_secret` issues by secret name (cross-cutting: one secret affects multiple servers)
- **FR-009**: Diagnostics MUST include system-level checks (Docker status) that aren't per-server

### Web UI

- **FR-010**: Dashboard MUST display ONE consolidated health section
- **FR-011**: Fix/action buttons MUST navigate to relevant pages, not show CLI hints

## Success Criteria

- **SC-001**: Users see exactly ONE health section on Dashboard
- **SC-002**: `mcpproxy doctor` output is derived from same data as Web UI
- **SC-003**: All action buttons navigate to appropriate fix locations
- **SC-004**: Missing secrets show which servers are affected (aggregated by secret name)

## Health Status Reference

### Levels
- `healthy` - No issues
- `degraded` - Minor issues (connecting, token expiring)
- `unhealthy` - Needs attention

### Admin States
- `enabled` - Normal operation
- `disabled` - User disabled
- `quarantined` - Pending approval

### Actions (Updated)

| Scenario | Level | Action | Detail | Navigation |
|----------|-------|--------|--------|------------|
| Healthy | `healthy` | `""` | - | - |
| Connecting | `degraded` | `""` | - | - |
| Token expiring | `degraded` | `login` | expiry time | OAuth flow |
| Connection error | `unhealthy` | `restart` | error message | Restarts server |
| OAuth needed | `unhealthy` | `login` | - | OAuth flow |
| Missing secret | `unhealthy` | `set_secret` | secret name | `/ui/secrets` |
| OAuth config issue | `unhealthy` | `configure` | error/param | Server config tab |
| Disabled | `healthy` | `enable` | - | Enables server |
| Quarantined | `healthy` | `approve` | - | Approves server |

## Out of Scope

- Adding new health checks (disk space, index health, latency metrics)
- Removing deprecated flat fields on Server struct
- Changes to the MCP protocol tool responses

## Commit Message Conventions

### Issue References
- Use: `Related #[issue-number]` - Links without auto-closing
- Do NOT use: `Fixes #`, `Closes #`, `Resolves #` - These auto-close on merge

### Co-Authorship
- Do NOT include AI attribution in commits
