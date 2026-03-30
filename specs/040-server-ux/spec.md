# Feature Specification: Add/Edit Server UX Improvements

**Feature Branch**: `040-server-ux`
**Created**: 2026-03-30
**Status**: Draft
**Input**: Improve Add Server, Edit Server, and Import Server UX in the MCPProxy macOS tray app

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Add a New Server with Validation Feedback (Priority: P1)

A user wants to add a new MCP server to MCPProxy. They open the Add Server sheet, fill in the form, and expect clear feedback on what's missing or wrong before submitting. After submitting, they want to see whether the connection succeeded or failed, with actionable error details.

**Why this priority**: Adding servers is the core onboarding action. Poor validation and no connection feedback are the two biggest UX complaints.

**Independent Test**: Open Add Server, leave fields empty (verify red validation labels), fill correctly, submit, verify connection feedback appears inline.

**Acceptance Scenarios**:

1. **Given** the Add Server sheet is open and Name field is empty, **When** user tabs away from Name, **Then** a red "Server name is required" label appears below the field.
2. **Given** "Remote URL (HTTP)" is selected and URL is empty, **When** user tabs away from URL, **Then** a red "URL is required" label appears.
3. **Given** a valid form is submitted, **When** the backend is connecting, **Then** an inline spinner with "Connecting to server..." text is shown.
4. **Given** a server connection fails, **When** the error is returned, **Then** the actual error message is shown in red with "Save Anyway" and "Retry" buttons.
5. **Given** a server connection succeeds, **Then** "Connected (N tools discovered)" appears in green and the sheet auto-closes after 2 seconds.
6. **Given** the protocol picker is shown, **Then** only two options appear: "Local Command (stdio)" and "Remote URL (HTTP)".
7. **Given** the form has many fields (stdio mode), **Then** the Add Server button is always visible without scrolling (pinned at bottom).

---

### User Story 2 - Edit an Existing Server Configuration (Priority: P1)

A user has an existing server with a typo in the URL or needs to change environment variables. They navigate to the server's detail view, click "Edit" on the Config tab, modify fields, and save.

**Why this priority**: Currently zero edit capability in the GUI. Users must delete and re-add servers or manually edit JSON config files.

**Independent Test**: Navigate to server detail view, click Edit on Config tab, modify a field, save, verify change persists.

**Acceptance Scenarios**:

1. **Given** a server's Config tab is shown, **When** user clicks "Edit", **Then** text labels become editable fields and toggle switches appear.
2. **Given** Config tab is in edit mode, **When** user modifies URL and clicks "Save", **Then** the configuration is updated and the view shows the new value.
3. **Given** Config tab is in edit mode, **When** user clicks "Cancel", **Then** all changes are discarded and view reverts to read-only.
4. **Given** Config tab is in edit mode, **Then** Server Name is not editable (displayed as label).
5. **Given** Config tab is in edit mode, **Then** toggles for Enabled, Quarantined, Docker Isolation, Skip Quarantine are visible.
6. **Given** user saves with empty required field, **Then** inline validation errors are shown and save is blocked.

---

### User Story 3 - Import Servers with Preview (Priority: P2)

A user wants to import servers from their Claude Code or Cursor configuration. They open the Import tab, select a config, preview what will be imported, and confirm.

**Why this priority**: Import is important for onboarding but used less frequently than add/edit.

**Independent Test**: Open Import tab, click Import for a config file, verify preview list with checkboxes, toggle servers, confirm import, check results.

**Acceptance Scenarios**:

1. **Given** Import tab shows a discovered config, **When** user clicks "Import", **Then** a preview list appears with checkboxes (checked by default) showing server name and protocol.
2. **Given** preview list, **When** a server already exists in MCPProxy, **Then** it is unchecked and marked "already exists".
3. **Given** preview list, **When** user clicks "Import Selected", **Then** only checked servers are imported.
4. **Given** import completes, **Then** per-server results show imported/skipped/failed with reasons.
5. **Given** Import tab, **When** user's config is not in auto-discovered list, **Then** "Browse Other File..." button opens a file picker.

---

### User Story 4 - View Connection Errors and Server Logs (Priority: P2)

A user has a server that won't connect and wants to understand why by viewing errors and logs from within the app.

**Why this priority**: Currently errors are invisible. Users see "Connecting..." indefinitely or generic timeout messages.

**Independent Test**: Configure server with invalid URL, observe error in server list tooltip and detail view, view logs in Logs tab.

**Acceptance Scenarios**:

1. **Given** a server has health.level == "unhealthy", **When** user hovers over status in server table, **Then** tooltip shows health.detail error.
2. **Given** Logs tab is open, **Then** logs auto-refresh every 3 seconds.
3. **Given** log lines with ERROR or WARN levels, **Then** they are color-coded red or yellow.
4. **Given** a server has a last_error, **Then** the Logs tab shows it prominently at top.

---

### User Story 5 - Keyboard Shortcuts and Polish (Priority: P3)

A user expects Cmd+N to work from the main window, contextual tab defaults, accessibility labels, and proper empty states.

**Why this priority**: Polish items that improve experience but don't block core workflows.

**Independent Test**: Press Cmd+N from main window (verify Add Server opens), click Dashboard import button (verify Import tab pre-selected), check accessibility labels.

**Acceptance Scenarios**:

1. **Given** main window is focused, **When** user presses Cmd+N, **Then** Add Server sheet opens with Manual tab.
2. **Given** Dashboard import button is clicked, **Then** Add Server sheet opens with Import tab pre-selected.
3. **Given** empty server list, **Then** onboarding empty state view with Add Server and Import buttons is shown.
4. **Given** Dashboard action buttons, **Then** each has an accessibilityLabel.

---

### Edge Cases

- Duplicate server name on Add: show "Server name already exists" error inline.
- PATCH with empty body: return 400 "No fields to update".
- Import preview returns 0 servers: show "No servers found in this config file".
- Connection test timeout (>10s): show timeout error with "Save Anyway" to save config.
- Edit while server reconnects: warn changes may require restart, offer restart after save.
- NSOpenPanel cancelled: return to Import tab unchanged.
- Config tab "Command: N/A" for disabled stdio servers: read from config, not runtime state.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Add Server sheet MUST be 560x560 with submit button pinned outside ScrollView, always visible.
- **FR-002**: Protocol picker MUST show "Local Command (stdio)" and "Remote URL (HTTP)". Backend auto-detects transport variant.
- **FR-003**: Form MUST show inline red validation text below empty required fields on field blur.
- **FR-004**: Submit MUST show phased progress: "Saving..." then "Connecting..." then success/failure with actual error.
- **FR-005**: Failure state MUST show "Save Anyway" (saves without connection) and "Retry" buttons.
- **FR-006**: Cmd+N MUST open Add Server sheet from the main window.
- **FR-007**: ServerDetailView Config tab MUST have "Edit" button toggling editable mode.
- **FR-008**: Edit mode MUST support: URL/Command, Args, Working Dir, Env Vars, Enabled, Quarantined, Docker Isolation, Skip Quarantine.
- **FR-009**: Edit mode MUST have Save (calls PATCH API) and Cancel buttons.
- **FR-010**: Server Name MUST NOT be editable in edit mode.
- **FR-011**: Backend MUST provide PATCH /api/v1/servers/{name} accepting partial updates.
- **FR-012**: Import MUST show preview with checkboxes before committing.
- **FR-013**: Import results MUST show per-server status with reasons.
- **FR-014**: Import tab MUST include "Browse Other File..." button using NSOpenPanel.
- **FR-015**: Import timeout MUST be 120 seconds.
- **FR-016**: Server table MUST show tooltip with health.detail on unhealthy server status hover.
- **FR-017**: Logs tab MUST auto-refresh every 3s and color-code ERROR/WARN lines.
- **FR-018**: Empty server list MUST show onboarding empty state.
- **FR-019**: Dashboard buttons MUST have accessibility labels.
- **FR-020**: Default tab: Manual for Cmd+N/Add Server, Import for Dashboard import button.
- **FR-021**: Config tab MUST show command from config (not runtime) so disabled servers display correctly.

### Key Entities

- **Server Configuration**: Name (immutable key), protocol (stdio/http), URL or command+args, env vars, working directory, enabled, quarantined, docker isolation, skip quarantine.
- **Import Preview**: List of servers from config file — name, protocol, exists-in-MCPProxy flag, selected-for-import flag.
- **Connection Test Result**: Status (connecting/success/failure), error message, tools discovered count.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users see connection success/failure feedback within 10 seconds of clicking Add Server.
- **SC-002**: Users can edit any server configuration field without deleting and re-adding.
- **SC-003**: All required fields show inline validation errors when empty — zero silent rejections.
- **SC-004**: Import preview shows all discovered servers before changes, with deselect capability.
- **SC-005**: Connection errors display actual backend error, not generic timeout text.
- **SC-006**: Cmd+N opens Add Server from main window on first attempt.
- **SC-007**: Empty server list guides new users to add or import their first server.
- **SC-008**: Unhealthy server status shows error detail on hover.

## Assumptions

- Backend auto-detection of HTTP transport variants is already implemented.
- Existing AddServerRequest struct shape works for PATCH updates.
- Import preview uses existing ?preview=true query parameter.
- NSOpenPanel presentable from SwiftUI via AppKit bridge.
- Server name is immutable primary key.
- Sheet remains the correct Apple HIG pattern for Add Server; inline editing in detail view for Edit.
- "Security Scan: soon" placeholder will be removed.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]`
- Do NOT use: `Fixes`, `Closes`, `Resolves` (auto-close)

### Example Commit Message
```
feat(040): [brief description]

Related #[issue-number]

## Changes
- [key changes]

## Testing
- [test results]
```
