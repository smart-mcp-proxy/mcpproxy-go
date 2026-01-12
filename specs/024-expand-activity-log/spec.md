# Feature Specification: Expand Activity Log

**Feature Branch**: `024-expand-activity-log`
**Created**: 2026-01-12
**Status**: Draft
**Input**: User description: "Required to expand Activity log. Need to log all key events e.g. mcpproxy stop, start, call of internal tools - retrieve_tools, call_tool* etc, need to log all config updates. Add remove servers. Add event types, on Web UI add filter by events, make it like dropdown with multiselect checkbox. Add column intent with text that LLM pass then calls tool. Make columns sortable, datetime by default, newest events first. On details of activity row show input params - colored formatted json. Show activity log menu item in Web UI. Extend CLI activity log command with event types."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Log System Lifecycle Events (Priority: P1)

As a system administrator, I want to see when MCPProxy starts and stops in the activity log so that I can correlate system availability with agent behavior and troubleshoot issues.

**Why this priority**: System lifecycle events are foundational for understanding the overall system state and debugging issues. Without these, users cannot correlate tool call failures with system restarts.

**Independent Test**: Can be fully tested by starting/stopping MCPProxy and verifying the events appear in the activity log via CLI or API.

**Acceptance Scenarios**:

1. **Given** MCPProxy is starting up, **When** the server initialization completes, **Then** a `system_start` activity record is created with server version, listen address, and timestamp
2. **Given** MCPProxy is running, **When** a graceful shutdown is initiated, **Then** a `system_stop` activity record is created with shutdown reason and timestamp
3. **Given** MCPProxy experiences an error during shutdown, **When** the forced shutdown occurs, **Then** a `system_stop` activity record is created with error details

---

### User Story 2 - Log Internal Tool Calls (Priority: P1)

As an AI agent developer, I want to see all internal MCPProxy tool calls (retrieve_tools, call_tool_read, call_tool_write, call_tool_destructive, code_execution) in the activity log so that I can audit agent behavior and debug tool discovery issues.

**Why this priority**: Internal tool calls are the primary interaction point between AI agents and MCPProxy. Logging these provides essential visibility into how agents discover and invoke tools.

**Independent Test**: Can be tested by making retrieve_tools and call_tool_* requests via MCP and verifying they appear in the activity log.

**Acceptance Scenarios**:

1. **Given** an AI agent calls `retrieve_tools`, **When** the tool completes, **Then** an activity record is created with type `internal_tool_call`, tool_name `retrieve_tools`, and the query parameters
2. **Given** an AI agent calls `call_tool_read`, **When** the tool completes, **Then** an activity record is created with the intent declaration, target server, and target tool name
3. **Given** an AI agent calls `call_tool_write` or `call_tool_destructive`, **When** the tool completes, **Then** an activity record is created with intent, target server, tool name, and execution duration
4. **Given** code_execution is enabled and used, **When** code execution completes, **Then** an activity record is created with the code executed, input data, and result summary

---

### User Story 3 - Log Configuration Changes (Priority: P1)

As a system administrator, I want to see all configuration changes in the activity log so that I can audit who changed what and when, and correlate configuration changes with system behavior.

**Why this priority**: Configuration changes directly impact system behavior. Logging these is essential for security auditing and troubleshooting unexpected behavior changes.

**Independent Test**: Can be tested by adding/removing/updating a server via CLI or API and verifying the config change appears in the activity log.

**Acceptance Scenarios**:

1. **Given** a user adds a new upstream server, **When** the server is added, **Then** a `config_change` activity record is created with action `server_added`, server name, and configuration summary
2. **Given** a user removes an upstream server, **When** the server is removed, **Then** a `config_change` activity record is created with action `server_removed` and server name
3. **Given** a user updates server settings, **When** the update is applied, **Then** a `config_change` activity record is created with action `server_updated`, server name, and changed fields
4. **Given** a user changes global settings, **When** the settings are saved, **Then** a `config_change` activity record is created with the changed settings

---

### User Story 4 - Filter Activity by Multiple Event Types (Priority: P2)

As a user, I want to filter activity records by multiple event types simultaneously using a multi-select dropdown in the Web UI so that I can focus on specific types of events while excluding others.

**Why this priority**: Multi-select filtering significantly improves usability when investigating issues that involve multiple related event types. Essential for effective log analysis.

**Independent Test**: Can be tested by selecting multiple event types in the dropdown and verifying only those types appear in the results.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** I click the event type filter, **Then** I see a dropdown with checkboxes for each event type
2. **Given** the event type dropdown is open, **When** I check multiple event types, **Then** the dropdown shows the count of selected types
3. **Given** I have selected multiple event types, **When** I apply the filter, **Then** only activities matching any of the selected types are displayed
4. **Given** I have filters applied, **When** I view the filter summary, **Then** I see all selected event types listed

---

### User Story 5 - View Intent Column in Activity Table (Priority: P2)

As an AI agent developer, I want to see the LLM's declared intent in a dedicated column in the activity table so that I can quickly understand why each tool call was made without opening the detail view.

**Why this priority**: Intent visibility at a glance improves debugging efficiency. The intent explains the "why" behind each tool call, which is crucial for auditing agent behavior.

**Independent Test**: Can be tested by making tool calls with intent declarations and verifying the intent appears in the table column.

**Acceptance Scenarios**:

1. **Given** the Activity Log table is displayed, **When** I view it, **Then** I see an "Intent" column between "Details" and "Status"
2. **Given** an activity has an intent reason, **When** the row is displayed, **Then** the Intent column shows the operation type icon and truncated reason text
3. **Given** an activity has no intent, **When** the row is displayed, **Then** the Intent column shows a dash or empty state
4. **Given** the intent text is long, **When** I hover over the Intent column, **Then** I see the full intent text in a tooltip

---

### User Story 6 - Sort Activity Table Columns (Priority: P2)

As a user, I want to sort the activity table by clicking column headers so that I can organize data by time, type, server, or status based on my current analysis needs.

**Why this priority**: Sortable columns are a standard UX expectation for data tables. Sorting by different columns helps users find patterns and anomalies quickly.

**Independent Test**: Can be tested by clicking column headers and verifying the table re-sorts accordingly.

**Acceptance Scenarios**:

1. **Given** I am on the Activity Log page, **When** the page loads, **Then** activities are sorted by datetime descending (newest first)
2. **Given** the table is displayed, **When** I click a column header, **Then** the table sorts by that column in ascending order
3. **Given** a column is sorted ascending, **When** I click the same column header again, **Then** the sort order toggles to descending
4. **Given** a column is sorted, **When** I view the column header, **Then** I see a sort indicator showing the current direction
5. **Given** I have filters applied, **When** I sort by a column, **Then** the sorting applies to the filtered results

---

### User Story 7 - View Colored JSON in Activity Details (Priority: P2)

As a user, I want to see input parameters and response data displayed as syntax-highlighted, colored JSON in the activity detail panel so that I can easily read and understand the data structure.

**Why this priority**: Syntax highlighting significantly improves readability of JSON data, making it faster to identify keys, values, and structure. This is essential for debugging complex tool calls.

**Independent Test**: Can be tested by opening an activity detail and verifying JSON is displayed with colored syntax highlighting.

**Acceptance Scenarios**:

1. **Given** I click on an activity row, **When** the detail panel opens, **Then** I see request arguments displayed with syntax-highlighted JSON (colored keys, strings, numbers, booleans)
2. **Given** the detail panel shows JSON data, **When** I view it, **Then** keys are colored distinctly from values, strings from numbers
3. **Given** the JSON is nested, **When** I view it, **Then** proper indentation is maintained with collapsible sections for nested objects
4. **Given** the JSON viewer displays data, **When** I click a copy button, **Then** the raw JSON is copied to my clipboard

---

### User Story 8 - Access Activity Log from Web UI Menu (Priority: P2)

As a user, I want to access the Activity Log from the main navigation menu in the Web UI so that I can easily navigate to it without knowing the direct URL.

**Why this priority**: Navigation accessibility is fundamental UX. Users expect all features to be discoverable from the main menu.

**Independent Test**: Can be tested by clicking the Activity Log menu item and verifying navigation to the Activity Log page.

**Acceptance Scenarios**:

1. **Given** I am on any page in the Web UI, **When** I look at the sidebar navigation, **Then** I see "Activity Log" as a menu item
2. **Given** the Activity Log menu item is visible, **When** I click it, **Then** I am navigated to the Activity Log page at `/activity`
3. **Given** I am on the Activity Log page, **When** I view the navigation, **Then** the Activity Log menu item is highlighted as active

---

### User Story 9 - Filter Activity by Event Types in CLI (Priority: P3)

As a CLI user, I want to filter activity records by event types using the `mcpproxy activity list` command so that I can focus on specific event types when scripting or debugging from the terminal.

**Why this priority**: CLI parity with Web UI is important for automation and users who prefer terminal workflows. Event type filtering is a core filter capability.

**Independent Test**: Can be tested by running `mcpproxy activity list --type=internal_tool_call,config_change` and verifying only those types are returned.

**Acceptance Scenarios**:

1. **Given** I run `mcpproxy activity list --type=internal_tool_call`, **When** results are returned, **Then** only internal_tool_call activities are shown
2. **Given** I run `mcpproxy activity list --type=system_start,system_stop`, **When** results are returned, **Then** only system lifecycle activities are shown
3. **Given** I run `mcpproxy activity list --type=invalid_type`, **When** the command executes, **Then** I see an error message listing valid event types

---

### User Story 10 - Updated Documentation (Priority: P3)

As a user or developer, I want to read updated documentation about the expanded Activity Log features so that I can understand and use all new event types, filters, and UI enhancements.

**Why this priority**: Documentation ensures users can discover and use new features effectively. Essential for adoption but can follow implementation.

**Independent Test**: Can be tested by visiting the Docusaurus documentation site and verifying all new features are documented.

**Acceptance Scenarios**:

1. **Given** the documentation site is deployed, **When** I navigate to the Activity Log section, **Then** I see documentation for all new event types (system_start, system_stop, internal_tool_call, config_change)
2. **Given** I am reading the CLI documentation, **When** I view the activity command reference, **Then** I see examples of filtering by multiple event types
3. **Given** I am reading the Web UI documentation, **When** I view the Activity Log section, **Then** I see documentation for the multi-select filter, Intent column, and column sorting

---

### Edge Cases

- What happens when the activity log storage is full? System should apply retention policy, removing oldest records first
- What happens if MCPProxy crashes before logging system_stop? The next startup should detect unclean shutdown and log a `system_start` with a flag indicating the previous session ended abnormally
- How are internal tool calls with very large arguments handled? Truncate arguments in storage, provide indicator in UI
- What happens when sorting a column that has null/missing values? Null values should sort to the end regardless of sort direction
- What happens when filtering by multiple event types with no matching records? Show empty state with current filter information

## Requirements *(mandatory)*

### Functional Requirements

**New Event Types**:
- **FR-001**: System MUST record `system_start` activity when MCPProxy server initialization completes, including version, listen address, and startup duration
- **FR-002**: System MUST record `system_stop` activity when MCPProxy begins graceful shutdown, including reason (signal received, manual stop, error)
- **FR-003**: System MUST record `internal_tool_call` activity for all built-in MCP tools: retrieve_tools, call_tool_read, call_tool_write, call_tool_destructive, code_execution, upstream_servers, quarantine_security
- **FR-004**: System MUST record `config_change` activity when upstream servers are added, removed, or updated, including the action performed and affected server name
- **FR-005**: System MUST record `config_change` activity when global settings are modified, including changed setting names

**Activity Record Extensions**:
- **FR-006**: System MUST store intent declaration (operation_type, data_sensitivity, reason) in activity records for call_tool_* calls
- **FR-007**: System MUST store input parameters for internal tool calls in the activity record arguments field
- **FR-008**: System MUST support the following activity types: tool_call, policy_decision, quarantine_change, server_change, system_start, system_stop, internal_tool_call, config_change

**Web UI - Event Type Filter**:
- **FR-009**: System MUST display a multi-select dropdown for event type filtering with checkboxes for each type
- **FR-010**: System MUST show the count of selected event types when multiple are selected
- **FR-011**: System MUST apply OR logic when multiple event types are selected (show activities matching any selected type)

**Web UI - Intent Column**:
- **FR-012**: System MUST display an "Intent" column in the activity table showing operation type and truncated reason
- **FR-013**: System MUST show a tooltip with full intent text on hover when text is truncated
- **FR-014**: System MUST show appropriate placeholder (dash or empty) when intent is not present

**Web UI - Column Sorting**:
- **FR-015**: System MUST enable sorting by clicking column headers for: Time, Type, Server, Status, Duration
- **FR-016**: System MUST default to sorting by Time descending (newest first)
- **FR-017**: System MUST toggle sort direction (asc/desc) when clicking the same column header
- **FR-018**: System MUST display visual sort indicators (arrows) on sorted columns

**Web UI - JSON Viewer**:
- **FR-019**: System MUST display request arguments with syntax-highlighted JSON (distinct colors for keys, strings, numbers, booleans, null)
- **FR-020**: System MUST support collapsible sections for nested JSON objects and arrays
- **FR-021**: System MUST provide copy-to-clipboard functionality for JSON data

**Web UI - Navigation**:
- **FR-022**: System MUST display "Activity Log" in the sidebar navigation menu
- **FR-023**: System MUST highlight the Activity Log menu item when on the Activity Log page

**CLI - Event Type Filter**:
- **FR-024**: `mcpproxy activity list` MUST support `--type` flag accepting comma-separated event types
- **FR-025**: System MUST validate event type values and show error with valid options for invalid types
- **FR-026**: System MUST support filtering by multiple types (OR logic) matching Web UI behavior

**Documentation (Docusaurus)**:
- **FR-027**: Documentation MUST describe all new activity types (system_start, system_stop, internal_tool_call, config_change) with examples
- **FR-028**: Documentation MUST include CLI examples showing multi-type filtering with `--type` flag
- **FR-029**: Documentation MUST describe Web UI Activity Log features including multi-select filter, Intent column, and sortable columns
- **FR-030**: Documentation MUST be updated in the `docs/` Docusaurus site under the appropriate sections

### Key Entities

- **ActivityType (Extended)**: Enumeration extended to include: tool_call, policy_decision, quarantine_change, server_change, system_start, system_stop, internal_tool_call, config_change

- **SystemStartMetadata**: Additional fields for system_start events: version, listen_address, startup_duration_ms, config_path, unclean_previous_shutdown

- **SystemStopMetadata**: Additional fields for system_stop events: reason (signal, manual, error), uptime_seconds, error_message (if applicable)

- **InternalToolCallMetadata**: Additional fields for internal_tool_call events: tool_name (retrieve_tools, call_tool_read, etc.), target_server (if applicable), target_tool (if applicable), intent_declaration

- **ConfigChangeMetadata**: Additional fields for config_change events: action (server_added, server_removed, server_updated, settings_changed), affected_entity, changed_fields

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All system start and stop events are captured in the activity log with complete metadata
- **SC-002**: All internal tool calls (retrieve_tools, call_tool_*) appear in the activity log within 100ms of completion
- **SC-003**: Configuration changes are logged before the operation completes (synchronous logging)
- **SC-004**: Multi-select event type filter returns results within 1 second for up to 10,000 records
- **SC-005**: Column sorting reorders the table within 500ms for up to 1,000 visible records
- **SC-006**: JSON syntax highlighting renders within 100ms for documents up to 100KB
- **SC-007**: Activity Log is accessible from the sidebar menu on all pages within the Web UI
- **SC-008**: CLI `--type` flag correctly filters by multiple event types with consistent behavior to Web UI
- **SC-009**: All documentation pages build successfully and are accessible on the Docusaurus site

## Testing Approach

All features MUST be tested using Claude Code with available tools:

### Web UI Testing (Claude Extension)

Use Claude browser automation (mcp__claude-in-chrome__* or mcp__playwriter__*) to test:

- **Navigation**: Verify Activity Log appears in sidebar and navigates correctly
- **Multi-select Filter**: Open dropdown, select multiple event types, verify checkbox behavior and filter results
- **Intent Column**: Verify column displays, shows operation type icons, and truncated text with tooltips
- **Column Sorting**: Click headers, verify sort direction changes, check sort indicators
- **JSON Viewer**: Open activity details, verify syntax highlighting with distinct colors for keys/values
- **Real-time Updates**: Trigger tool calls and verify activities appear via SSE

### CLI Testing (Shell)

Use Bash tool to test CLI commands:

- **Event Type Filtering**: Run `mcpproxy activity list --type=internal_tool_call` and verify output
- **Multi-type Filtering**: Run `mcpproxy activity list --type=system_start,system_stop` and verify OR logic
- **Invalid Type Handling**: Run `mcpproxy activity list --type=invalid` and verify error message lists valid types
- **JSON Output**: Run `mcpproxy activity list -o json` and verify new event types appear correctly

### Backend Testing

Use Bash tool and API calls to verify:

- **System Events**: Start/stop MCPProxy and verify system_start/system_stop records via API
- **Internal Tool Calls**: Use retrieve_tools via MCP and verify internal_tool_call records appear
- **Config Changes**: Add/remove servers via CLI and verify config_change records
- **Intent Storage**: Make call_tool_* requests with intent and verify intent is stored in activity record

## Assumptions

- The existing BBolt storage layer can accommodate the new activity types without schema migration
- The existing JsonViewer component supports syntax highlighting (based on current implementation)
- Performance requirements are met within current pagination limits (100 records per page)
- Event type filter state is not persisted across sessions (resets on page load)

## Dependencies

- **Existing Components**:
  - `internal/storage/activity_models.go`: ActivityType enumeration to extend
  - `internal/runtime/activity_service.go`: Activity recording service to extend
  - `internal/runtime/events.go`: Event types for SSE to extend
  - `frontend/src/views/Activity.vue`: Activity Log page to enhance
  - `frontend/src/components/SidebarNav.vue`: Navigation menu to update
  - `cmd/mcpproxy/activity_cmd.go`: CLI activity command to extend
  - `docs/`: Docusaurus documentation site to update

- **Related Specs**:
  - Spec 016 (Activity Log Backend): Base activity recording infrastructure
  - Spec 018 (Intent Declaration): Intent metadata structure
  - Spec 019 (Activity Web UI): Existing Activity page implementation

## Out of Scope

- Real-time filtering (filters apply on explicit action, not as-you-type)
- Saved/persistent filter presets
- Custom event types defined by users
- Activity log export enhancements (covered by existing export functionality)
- Advanced search/query language (beyond type/server/status filters)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]`

**Rationale**: Issues should only be closed manually after verification and testing in production.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors.

### Example Commit Message
```
feat(activity): expand activity log with new event types and UI enhancements

Related #[issue-number]

Add system lifecycle, internal tool call, and config change events to activity log.
Enhance Web UI with multi-select type filter, intent column, and sortable columns.

## Changes
- Add system_start, system_stop, internal_tool_call, config_change activity types
- Extend activity recording for internal MCP tools
- Add multi-select dropdown for event type filtering in Web UI
- Add Intent column to activity table
- Enable column sorting with visual indicators
- Enable Activity Log in sidebar navigation
- Extend CLI --type flag to support comma-separated event types

## Testing
- Unit tests for new activity types
- E2E tests for event recording
- UI tests for filtering and sorting
```
