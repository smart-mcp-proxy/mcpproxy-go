# Feature Specification: Activity CLI Commands

**Feature Branch**: `017-activity-cli-commands`
**Created**: 2025-12-26
**Status**: Draft
**Input**: User description: "Implement Activity CLI Commands for querying and monitoring activity log (RFC-001 + RFC-003)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - List Recent Activity via CLI (Priority: P1)

Users and AI agents need to query activity history from the command line to understand what tool calls have been made, debug issues, and monitor agent behavior without using the web UI.

**Why this priority**: CLI is the primary interface for AI agents and power users. They need to query activity without opening a browser.

**Independent Test**: Can be fully tested by running `mcpproxy activity list` after making tool calls and verifying the output shows the activity history.

**Acceptance Scenarios**:

1. **Given** 10 tool calls have been made, **When** a user runs `mcpproxy activity list`, **Then** a formatted table shows recent activities with timestamp, type, server, tool, and status
2. **Given** tool calls from multiple servers exist, **When** a user runs `mcpproxy activity list --server github`, **Then** only activities for the "github" server are shown
3. **Given** many activities exist, **When** a user runs `mcpproxy activity list --limit 5`, **Then** only the 5 most recent activities are shown
4. **Given** an AI agent needs machine-readable output, **When** it runs `mcpproxy activity list -o json`, **Then** valid JSON array of activities is returned

---

### User Story 2 - Watch Live Activity Stream (Priority: P1)

Users monitoring agent behavior in real-time need a `tail -f` style command that streams activity as it happens.

**Why this priority**: Real-time monitoring is essential for debugging and observing agent behavior during execution.

**Independent Test**: Can be tested by running `mcpproxy activity watch` in one terminal and making tool calls in another, verifying events stream live.

**Acceptance Scenarios**:

1. **Given** the watch command is running, **When** a tool call is made, **Then** the activity appears in the output immediately
2. **Given** the watch command is running with `--server github`, **When** tool calls are made to different servers, **Then** only github server activities are shown
3. **Given** the watch command is running, **When** the user presses Ctrl+C, **Then** the command exits cleanly

---

### User Story 3 - View Activity Details (Priority: P2)

Users investigating a specific tool call need to view its full details including request arguments and response data.

**Why this priority**: Detail view is important for debugging but less frequently used than list/watch.

**Independent Test**: Can be tested by running `mcpproxy activity show <id>` and verifying full details are displayed.

**Acceptance Scenarios**:

1. **Given** an activity ID from the list command, **When** a user runs `mcpproxy activity show act_abc123`, **Then** full details including arguments and response are displayed
2. **Given** an invalid activity ID, **When** a user runs `mcpproxy activity show unknown`, **Then** a clear error message is shown

---

### User Story 4 - Activity Summary Dashboard (Priority: P3)

Users need a quick overview of activity statistics for a time period to understand usage patterns and identify issues.

**Why this priority**: Summary is useful for high-level monitoring but not essential for core functionality.

**Independent Test**: Can be tested by running `mcpproxy activity summary` and verifying statistics are displayed.

**Acceptance Scenarios**:

1. **Given** activities exist for the past 24 hours, **When** a user runs `mcpproxy activity summary`, **Then** statistics showing total calls, success rate, and top servers are displayed
2. **Given** a user wants weekly summary, **When** they run `mcpproxy activity summary --period 7d`, **Then** the summary covers the last 7 days

---

### User Story 5 - Export Activity for Compliance (Priority: P4)

Enterprise users need to export activity logs to files for compliance and audit purposes.

**Why this priority**: Export is an enterprise feature, not needed for core CLI functionality.

**Independent Test**: Can be tested by running `mcpproxy activity export --output activity.json` and verifying the file is created.

**Acceptance Scenarios**:

1. **Given** activities exist, **When** a user runs `mcpproxy activity export --output activity.json`, **Then** a JSON file with all matching activities is created
2. **Given** activities exist, **When** a user runs `mcpproxy activity export -o csv --output activity.csv`, **Then** a CSV file is created

---

### Edge Cases

- What happens when activity list is empty? (Show "No activities found" message for table, empty array for JSON)
- What happens when watch command runs but daemon is not reachable? (Show connection error and retry logic)
- What happens when export file path is not writable? (Show clear permission error)
- How does watch handle very high activity volume? (Buffer and batch output to avoid overwhelming terminal)
- What happens when activity ID format is invalid? (Show format hint in error message)

## Requirements *(mandatory)*

### Functional Requirements

**Activity List Command**:
- **FR-001**: System MUST provide `mcpproxy activity list` command
- **FR-002**: List command MUST support `--type` filter (tool_call, policy_decision, quarantine)
- **FR-003**: List command MUST support `--server` filter
- **FR-004**: List command MUST support `--status` filter (success, error, blocked)
- **FR-005**: List command MUST support `--limit` and `--offset` for pagination
- **FR-006**: List command MUST support `--start-time` and `--end-time` filters
- **FR-007**: List command MUST use output formatter from spec 014 (table, json, yaml)

**Activity Watch Command**:
- **FR-008**: System MUST provide `mcpproxy activity watch` command
- **FR-009**: Watch command MUST stream activities in real-time via SSE connection
- **FR-010**: Watch command MUST support `--type` and `--server` filters
- **FR-011**: Watch command MUST handle disconnection with automatic reconnection
- **FR-012**: Watch command MUST exit cleanly on SIGINT/SIGTERM

**Activity Show Command**:
- **FR-013**: System MUST provide `mcpproxy activity show <id>` command
- **FR-014**: Show command MUST display full activity details including arguments and response
- **FR-015**: Show command MUST support `--include-response` flag for very large responses

**Activity Summary Command**:
- **FR-016**: System MUST provide `mcpproxy activity summary` command
- **FR-017**: Summary MUST show: total count, success/error/blocked counts, top servers, top tools
- **FR-018**: Summary MUST support `--period` flag (1h, 24h, 7d, 30d)
- **FR-019**: Summary MUST support `--by` flag for grouping (server, tool, status)

**Activity Export Command**:
- **FR-020**: System MUST provide `mcpproxy activity export` command
- **FR-021**: Export MUST support `--output` flag for file path
- **FR-022**: Export MUST support `-o` format flag (json, csv)
- **FR-023**: Export MUST support same filters as list command
- **FR-024**: Export MUST support `--include-bodies` flag for full request/response

**CLI Integration**:
- **FR-025**: All commands MUST use REST API backend (not direct storage access)
- **FR-026**: All commands MUST respect `--quiet` flag for minimal output
- **FR-027**: All commands MUST work with both running daemon and direct mode

### Key Entities

- **ActivityListOptions**: CLI options for list command including filters, pagination, and output format.

- **ActivityWatchOptions**: CLI options for watch command including filters and reconnection settings.

- **ActivitySummary**: Aggregated statistics for activity summary including counts and groupings.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can list activities in under 1 second for up to 100 records (verifiable by timing command)
- **SC-002**: Watch command displays new activities within 200ms of occurrence (verifiable by timing)
- **SC-003**: AI agents using mcp-eval scenarios can query activity with 95%+ success rate (verifiable by running mcp-eval)
- **SC-004**: All CLI output is consistent with spec 014 formatting (verifiable by comparing output formats)
- **SC-005**: Export command creates valid, parseable files (verifiable by parsing exported files)
- **SC-006**: Commands provide helpful error messages for common mistakes (verifiable by triggering errors)

## Assumptions

- Spec 014 (CLI Output Formatting) is implemented and available
- Spec 016 (Activity Log Backend) REST API is implemented and available
- The SSE endpoint for watch is available at `/events`
- CLI can connect to daemon via socket or HTTP

## Dependencies

- **Spec 014**: CLI Output Formatting (for table/json/yaml output)
- **Spec 016**: Activity Log Backend (REST API endpoints)
- **Existing Components**:
  - `cmd/mcpproxy/`: CLI command structure
  - REST API client for daemon communication

## Out of Scope

- Web UI for activity (existing web UI to be extended separately)
- Risk scoring display (future RFC-004)
- PII detection display (future RFC-004)
- Activity modification/deletion commands
- Interactive activity explorer

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
feat(cli): add activity commands for querying and monitoring

Related #[issue-number]

Implement CLI commands for activity log as per RFC-001 and RFC-003.
Provides list, watch, show, summary, and export commands.

## Changes
- Add mcpproxy activity list command with filtering
- Add mcpproxy activity watch for real-time streaming
- Add mcpproxy activity show for detail view
- Add mcpproxy activity summary for statistics
- Add mcpproxy activity export for compliance export
- Integrate with output formatter from spec 014

## Testing
- Unit tests for command parsing
- E2E tests for all activity commands
- mcp-eval scenarios for AI agent usage
```
