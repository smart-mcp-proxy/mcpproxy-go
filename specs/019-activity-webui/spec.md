# Feature Specification: Activity Log Web UI

**Feature Branch**: `019-activity-webui`
**Created**: 2025-12-29
**Status**: Draft
**Input**: Implement Web UI updates for Activity Log as defined in RFC-003 section 1.4

## User Scenarios & Testing *(mandatory)*

### User Story 1 - View Activity Log Page (Priority: P1)

As a user, I want to view a dedicated Activity Log page in the Web UI so that I can see all agent activity in one place with full details.

**Why this priority**: This is the core functionality - without the Activity Log page, users have no web-based way to view activity records. This is the foundation for all other features.

**Independent Test**: Can be fully tested by navigating to `/ui/activity` and verifying the table displays activity records with columns for time, type, server, details, status, and duration.

**Acceptance Scenarios**:

1. **Given** I am on the Web UI, **When** I navigate to the Activity Log page, **Then** I see a table displaying recent activity records with columns: Time, Type, Server, Details, Status, Duration
2. **Given** the Activity Log page is open, **When** new activity records exist, **Then** the table displays them in chronological order (most recent first)
3. **Given** activity records exist, **When** I view the table, **Then** each row shows appropriate status indicators (success, error, blocked)

---

### User Story 2 - Real-time Activity Updates (Priority: P1)

As a user, I want to see activity updates in real-time so that I can monitor what AI agents are doing as it happens.

**Why this priority**: Real-time visibility is essential for understanding agent behavior and catching issues immediately. This is a core value proposition of the Activity Log.

**Independent Test**: Can be tested by triggering a tool call and observing the activity appear in the table without manual refresh.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** a tool call is made, **Then** the new activity appears in the table automatically via SSE
2. **Given** I am viewing the Activity Log, **When** a tool call completes, **Then** the status and duration update in the existing row automatically
3. **Given** I am on the Activity Log page, **When** a policy decision blocks a tool call, **Then** the blocked activity appears with appropriate status

---

### User Story 3 - Filter Activity Records (Priority: P2)

As a user, I want to filter activity records by type, server, status, and time range so that I can focus on specific activities of interest.

**Why this priority**: Filtering enables users to find specific activities in potentially large datasets. Important for debugging and monitoring but secondary to basic viewing.

**Independent Test**: Can be tested by selecting filter values and verifying only matching records are displayed.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** I select a type filter (e.g., "tool_call"), **Then** only activities of that type are displayed
2. **Given** I am on the Activity Log page, **When** I select a server filter, **Then** only activities from that server are displayed
3. **Given** I am on the Activity Log page, **When** I select a status filter (e.g., "error"), **Then** only activities with that status are displayed
4. **Given** I am on the Activity Log page, **When** I select a date range, **Then** only activities within that range are displayed
5. **Given** I have multiple filters active, **When** I apply them, **Then** all filters are combined (AND logic)

---

### User Story 4 - View Activity Details (Priority: P2)

As a user, I want to click on an activity row to see full details including request arguments and response data so that I can understand exactly what happened.

**Why this priority**: Detailed view is necessary for debugging and understanding specific tool calls. Important but depends on the basic table view.

**Independent Test**: Can be tested by clicking a row and verifying the detail panel shows complete information.

**Acceptance Scenarios**:

1. **Given** I am viewing the Activity Log table, **When** I click on a row, **Then** a detail panel opens showing full activity information
2. **Given** the detail panel is open, **When** I view it, **Then** I see: activity ID, type, timestamp, server name, tool name, status, duration
3. **Given** the detail panel is open for a tool_call, **When** I view it, **Then** I see the full request arguments displayed in a syntax-highlighted JSON viewer with colored keys, strings, numbers, and booleans
4. **Given** the detail panel is open for a completed tool_call, **When** I view it, **Then** I see the full response data displayed in a syntax-highlighted JSON viewer with copy-to-clipboard functionality
5. **Given** the detail panel is open for an error, **When** I view it, **Then** I see the error message

---

### User Story 5 - Dashboard Activity Widget (Priority: P2)

As a user, I want to see an activity summary widget on the dashboard so that I can quickly understand recent agent activity at a glance.

**Why this priority**: The dashboard widget provides quick overview without navigating to the full Activity Log. Useful for at-a-glance monitoring.

**Independent Test**: Can be tested by viewing the dashboard and verifying the widget shows correct counts and recent activities.

**Acceptance Scenarios**:

1. **Given** I am on the main dashboard, **When** the page loads, **Then** I see a Tool Call Activity widget
2. **Given** the activity widget is displayed, **When** I view it, **Then** I see today's total call count, success count, and warning/error count
3. **Given** the activity widget is displayed, **When** I view it, **Then** I see the 3 most recent activities with server, tool, time, and status
4. **Given** the activity widget is displayed, **When** I click "View All", **Then** I am navigated to the Activity Log page

---

### User Story 6 - Export Activity Records (Priority: P3)

As a user, I want to export activity records to JSON or CSV format so that I can use them for compliance audits or external analysis.

**Why this priority**: Export is a compliance feature that adds value but is not essential for core monitoring functionality.

**Independent Test**: Can be tested by clicking export and verifying a file downloads in the selected format.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** I click the export button, **Then** I see options to export as JSON or CSV
2. **Given** I have filters active, **When** I export, **Then** only the filtered records are exported
3. **Given** I select JSON format, **When** I export, **Then** a valid JSON file is downloaded with activity records
4. **Given** I select CSV format, **When** I export, **Then** a valid CSV file is downloaded with appropriate columns

---

### User Story 7 - Paginate Activity Records (Priority: P3)

As a user, I want to navigate through pages of activity records so that I can view historical activity without performance issues.

**Why this priority**: Pagination is necessary for handling large datasets but not critical for initial monitoring functionality.

**Independent Test**: Can be tested by viewing more records than fit on one page and navigating between pages.

**Acceptance Scenarios**:

1. **Given** more than 100 activity records exist, **When** I view the Activity Log page, **Then** I see the first page with pagination controls
2. **Given** I am on page 1, **When** I click "Next", **Then** I see the next page of records
3. **Given** pagination is displayed, **When** I view it, **Then** I see the current page, total pages, and record count

---

### User Story 8 - Toggle Auto-refresh (Priority: P3)

As a user, I want to toggle auto-refresh so that I can choose between live updates and a static view.

**Why this priority**: This is a UX enhancement that gives users control over update behavior but real-time updates should work by default.

**Independent Test**: Can be tested by toggling auto-refresh off and verifying new activities don't appear until manual refresh.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** I disable auto-refresh, **Then** new activities do not automatically appear
2. **Given** auto-refresh is disabled, **When** I manually refresh or enable auto-refresh, **Then** the table updates with new records
3. **Given** auto-refresh is enabled, **When** the page is in the background, **Then** updates resume when I return to the page

---

### Edge Cases

- What happens when no activity records exist? Display empty state with helpful message
- What happens when SSE connection is lost? Show connection status indicator, attempt reconnection
- What happens when activity records have very large arguments/responses? Truncate in table view, show full content in detail panel with scrolling
- What happens when many activities occur rapidly? Batch SSE updates to prevent UI thrashing
- What happens when filter returns no results? Show empty state with current filter information
- What happens when export is requested with too many records? Apply reasonable limits or background processing

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST display an Activity Log page accessible from the Web UI navigation
- **FR-002**: System MUST show activity records in a table with columns: Time, Type, Server, Details, Status, Duration
- **FR-003**: System MUST update the table in real-time via SSE events (activity.tool_call.started, activity.tool_call.completed, activity.policy_decision)
- **FR-004**: System MUST provide filter dropdowns for: type, server, status
- **FR-005**: System MUST provide a date range picker for time-based filtering
- **FR-006**: System MUST fetch filter options dynamically from the `/api/v1/activity/filter-options/{filter}` endpoint
- **FR-007**: System MUST display a detail panel when a row is clicked, showing full request/response data with syntax-highlighted JSON viewer
- **FR-008**: System MUST provide pagination controls with configurable page size (default 100 records)
- **FR-009**: System MUST provide export functionality supporting JSON and CSV formats
- **FR-010**: System MUST display a Dashboard widget showing: total calls today, success count, warning count, and 3 most recent activities
- **FR-011**: System MUST provide an auto-refresh toggle that controls SSE subscription
- **FR-012**: System MUST display appropriate status indicators (icons or colors) for success, error, and blocked statuses
- **FR-013**: System MUST show a summary section displaying total records and filter state
- **FR-014**: System MUST handle SSE connection loss gracefully with visual feedback and automatic reconnection

### Key Entities

- **ActivityRecord**: Represents a single activity entry - contains ID, type, timestamp, server_name, tool_name, arguments, response, status, duration_ms, error
- **FilterOptions**: Available values for filter dropdowns - fetched from API for server_name, tool_name, status, type
- **ExportRequest**: Parameters for exporting activity - includes format (json/csv), time range, and current filters

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can view the Activity Log page within 2 seconds of navigation
- **SC-002**: Real-time activity updates appear in the table within 500ms of the SSE event
- **SC-003**: Users can apply filters and see results within 1 second
- **SC-004**: Detail panel opens within 500ms of clicking a row
- **SC-005**: Dashboard widget loads with accurate counts on page load
- **SC-006**: Export of up to 10,000 records completes within 30 seconds
- **SC-007**: Pagination allows navigation through any volume of records without page freezing
- **SC-008**: 95% of users can find and apply filters without documentation

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

## Assumptions

- The REST API endpoints defined in RFC-003 section 1.2 are already implemented (specs 016-activity-log-backend)
- The SSE event types (activity.tool_call.started, activity.tool_call.completed, activity.policy_decision) are already being emitted
- The existing Web UI framework and component library will be extended
- Authentication for the Web UI is already handled (API key in URL query parameter)
- The `/api/v1/activity/filter-options/{filter}` endpoint is available for dynamic filter population
