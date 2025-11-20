# Feature Specification: Tool Annotations & MCP Sessions in WebUI

**Feature Branch**: `003-tool-annotations-webui`
**Created**: 2025-11-19
**Status**: Draft
**Input**: User description: "We want to improve tool calls transparency for User in WebUI. Required to show for each tool the annotations object containing advisory hints (title, readOnlyHint, destructiveHint, idempotentHint, openWorldHint). Show annotations on server details page inside each tool card. Show tool annotations in compact form in tool calls history list. Add dashboard table with MCP sessions (10 last sessions, status, start time, duration, token sum). Improve filters on Tool Call History page to allow filtering by mcp-session."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - View Tool Annotations on Server Details Page (Priority: P1)

A user navigates to the server details page to understand the characteristics of available tools. They can see annotation badges and information for each tool, helping them understand which tools are read-only, destructive, idempotent, or interact with external systems.

**Why this priority**: This is the core feature that provides tool transparency. Without understanding tool behaviors, users may accidentally trigger destructive operations or misunderstand tool capabilities.

**Independent Test**: Can be fully tested by viewing any server's details page and verifying annotation displays render correctly for tools with various annotation combinations.

**Acceptance Scenarios**:

1. **Given** a server with tools that have annotations defined, **When** the user views the server details page, **Then** each tool card displays its annotations with appropriate visual indicators (badges/icons)
2. **Given** a tool with `title` annotation, **When** displayed in the tool card, **Then** the title appears as the primary tool name/heading
3. **Given** a tool with `destructiveHint: true`, **When** displayed, **Then** a visual warning indicator (e.g., red badge) is shown
4. **Given** a tool with `readOnlyHint: true`, **When** displayed, **Then** a visual indicator shows it's safe/read-only (e.g., blue badge)
5. **Given** a tool with no annotations, **When** displayed, **Then** no annotation badges appear (graceful fallback)

---

### User Story 2 - View Compact Tool Annotations in Tool Call History (Priority: P1)

A user reviews the tool call history to understand past operations. Each history entry shows compact annotation indicators with hover tooltips, allowing quick visual scanning of operation characteristics.

**Why this priority**: Equal priority with story 1 as it provides transparency for historical operations, enabling users to audit and understand what operations were performed.

**Independent Test**: Can be fully tested by viewing the tool call history page and verifying compact annotations appear with functional hover tooltips.

**Acceptance Scenarios**:

1. **Given** a tool call history entry for a tool with annotations, **When** displayed in the history list, **Then** compact icons/badges represent each annotation hint
2. **Given** a compact annotation icon, **When** the user hovers over it, **Then** a tooltip displays the full annotation description
3. **Given** a destructive tool call in history, **When** viewed, **Then** the destructive indicator is visually prominent (e.g., warning color)
4. **Given** multiple annotations on a tool call, **When** displayed compactly, **Then** all applicable icons appear without cluttering the UI

---

### User Story 3 - View MCP Sessions Dashboard Table (Priority: P2)

A user visits the dashboard to get an overview of recent MCP sessions. They see a table showing the 10 most recent sessions with status (active/closed), start time, duration, total token usage, client name (if available), and number of tool calls. Clicking on a session navigates to the tool call history filtered to that session.

**Why this priority**: Provides high-level visibility into system usage and session patterns. Important for monitoring but secondary to individual tool transparency.

**Independent Test**: Can be fully tested by viewing the dashboard and verifying the sessions table displays correct data for active and closed sessions, and that clicking navigates correctly.

**Acceptance Scenarios**:

1. **Given** the dashboard page, **When** loaded, **Then** a table displays up to 10 most recent MCP sessions
2. **Given** an active MCP session, **When** displayed in the table, **Then** status shows "active" with appropriate styling
3. **Given** a closed MCP session, **When** displayed, **Then** status shows "closed" with duration calculated from start to end time
4. **Given** an active session, **When** displayed, **Then** duration shows elapsed time from start to current time (updating or showing "ongoing")
5. **Given** sessions with tool calls, **When** displayed, **Then** total token count aggregates all tokens from that session's tool calls
6. **Given** a session with a known client name, **When** displayed, **Then** the client name is shown in the table
7. **Given** a session without a client name, **When** displayed, **Then** a placeholder or empty value is shown gracefully
8. **Given** any session, **When** displayed, **Then** the number of tool calls is shown
9. **Given** a session row in the table, **When** the user clicks on it, **Then** they are navigated to the Tool Call History page with the session filter pre-selected

---

### User Story 4 - Filter Tool Call History by MCP Session (Priority: P2)

A user wants to view all tool calls from a specific MCP session. They use a filter on the Tool Call History page to select a session and see only tool calls from that session.

**Why this priority**: Enhances the usability of tool call history for debugging and auditing specific sessions.

**Independent Test**: Can be fully tested by applying the session filter and verifying only tool calls from the selected session appear.

**Acceptance Scenarios**:

1. **Given** the Tool Call History page, **When** loaded, **Then** a session filter dropdown/selector is available
2. **Given** multiple sessions exist, **When** the user selects a session from the filter, **Then** only tool calls belonging to that session are displayed
3. **Given** a session filter is applied, **When** the user clears the filter, **Then** all tool calls are displayed again
4. **Given** a session with no tool calls, **When** selected in the filter, **Then** an empty state message is displayed

---

### Edge Cases

- What happens when a tool has no annotations defined? (Display tool normally without annotation badges)
- How does the system handle MCP servers that don't provide annotations in their tool definitions? (Gracefully omit annotation displays)
- What happens when there are fewer than 10 sessions? (Display all available sessions without padding)
- What happens when an active session has no tool calls yet? (Show session with 0 tokens and 0 tool calls)
- How does the system handle very long session durations? (Format appropriately, e.g., "2d 3h 15m")
- What happens when filtering by a session that was deleted? (Clear filter and show all results with notification)
- What happens when a session has no client name? (Display placeholder like "Unknown" or leave empty with appropriate styling)
- What happens when clicking a session that has since been deleted? (Show error message and redirect back to dashboard)

## Requirements *(mandatory)*

### Functional Requirements

#### Tool Annotations Display

- **FR-001**: System MUST display tool annotations on the server details page within each tool card
- **FR-002**: System MUST support displaying these annotation fields: `title`, `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`
- **FR-003**: System MUST display the `title` annotation as the human-readable tool name when present
- **FR-004**: System MUST display boolean hint annotations as visual badges/icons with distinct styling:
  - `readOnlyHint`: Safe/read-only indicator (blue/info style)
  - `destructiveHint`: Warning/danger indicator (red/warning style)
  - `idempotentHint`: Neutral indicator (grey/neutral style)
  - `openWorldHint`: External system indicator (purple/special style)
- **FR-005**: System MUST gracefully handle tools without annotations by displaying them without annotation badges

#### Tool Call History Annotations

- **FR-006**: System MUST display compact annotation indicators in the tool call history list
- **FR-007**: System MUST show tooltips with full annotation descriptions when user hovers over compact indicators
- **FR-008**: System MUST preserve annotation information when recording tool calls to history

#### MCP Sessions Dashboard

- **FR-009**: System MUST display a table on the dashboard showing the 10 most recent MCP sessions
- **FR-010**: System MUST show session status as "active" or "closed"
- **FR-011**: System MUST show session start time in a human-readable format
- **FR-012**: System MUST calculate and display session duration (elapsed time for active, total time for closed)
- **FR-013**: System MUST aggregate and display total token count for each session's tool calls
- **FR-014**: System MUST update active session information every 30 seconds via polling
- **FR-015**: System MUST display MCP client name when available, with graceful handling when not provided
- **FR-016**: System MUST display the number of tool calls for each session
- **FR-017**: System MUST make session rows clickable, navigating to Tool Call History page with session filter pre-selected
- **FR-022**: System MUST retain the 100 most recent sessions and automatically delete older sessions

#### Session Filtering

- **FR-018**: System MUST provide a session filter on the Tool Call History page
- **FR-019**: System MUST filter tool call history to show only calls from the selected session
- **FR-020**: System MUST allow clearing the session filter to show all tool calls
- **FR-021**: System MUST persist filter selection during page navigation within the session

### Key Entities

- **Tool Annotation**: Advisory metadata about a tool's behavior characteristics (title, readOnlyHint, destructiveHint, idempotentHint, openWorldHint)
- **MCP Session**: A connection session between a client and MCPProxy, with lifecycle (active/closed), start/end times, client name (optional), tool call count, and associated tool calls
- **Tool Call Record**: A historical record of a tool execution, now enhanced with session ID and annotation snapshot
- **Session Token Aggregate**: Calculated sum of all input/output tokens used across a session's tool calls

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can identify tool behavior characteristics (read-only, destructive, etc.) within 2 seconds of viewing a tool card
- **SC-002**: Users can understand annotation meanings through tooltips without consulting documentation
- **SC-003**: Dashboard displays session information within 1 second of page load
- **SC-004**: Users can filter tool call history by session with a single click/selection
- **SC-005**: 100% of tool annotations from upstream MCP servers are accurately displayed
- **SC-006**: Session token counts accurately reflect the sum of all tool call tokens in that session
- **SC-007**: Compact annotation indicators occupy no more than 30% of tool call history row width

## Assumptions

- MCP servers provide tool annotations in the standard format (as defined by MCP specification)
- Token counts are already being tracked per tool call in the existing system
- Session tracking infrastructure exists or will be implemented as part of this feature
- The WebUI framework supports tooltips/hover interactions

## Clarifications

### Session 2025-11-19

- Q: How long should session data be retained? → A: Store last 100 sessions, auto-delete older
- Q: How often should active session information update? → A: Update every 30 seconds via polling

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
