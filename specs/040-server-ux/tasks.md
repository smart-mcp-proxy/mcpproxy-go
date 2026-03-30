# Tasks: Add/Edit Server UX Improvements

**Input**: Design documents from `/specs/040-server-ux/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Organization**: Tasks grouped by user story for independent implementation.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story (US1-US5)
- Exact file paths included

---

## Phase 1: Setup

**Purpose**: Backend PATCH endpoint (foundation for Edit Server)

- [ ] T001 Add PATCH /api/v1/servers/{name} handler in internal/httpapi/server.go
- [ ] T002 Write Go test for PATCH endpoint in internal/httpapi/server_test.go
- [ ] T003 Add updateServer() method to Swift APIClient in native/macos/MCPProxy/MCPProxy/API/APIClient.swift

**Checkpoint**: PATCH API working, Swift client can call it

---

## Phase 2: User Story 1 - Add Server with Validation (Priority: P1) MVP

**Goal**: Improved Add Server sheet with proper size, validation, protocol simplification, and connection feedback.

**Independent Test**: Open Add Server, leave fields empty (verify red labels), fill correctly, submit (verify connection feedback).

- [ ] T004 [US1] Increase sheet size to 560x560 and pin submit button outside ScrollView in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T005 [US1] Simplify protocol picker to ["Local Command (stdio)", "Remote URL (HTTP)"] in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T006 [US1] Add @FocusState tracking and inline red validation text below required fields in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T007 [US1] Implement phased connection test feedback (Saving → Connecting → Success/Failure) in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T008 [US1] Add "Save Anyway" and "Retry" buttons for connection failure state in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T009 [US1] Build Swift tray app binary and verify Add Server flow with mcpproxy-ui-test tool

**Checkpoint**: Add Server sheet fully functional with validation and connection feedback

---

## Phase 3: User Story 2 - Edit Server Configuration (Priority: P1)

**Goal**: Editable Config tab in ServerDetailView with Save/Cancel, field editing, and toggle switches.

**Independent Test**: Navigate to server detail, click Edit, modify field, save, verify change persists.

- [ ] T010 [US2] Add "Edit" button and edit mode state to Config tab in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T011 [US2] Convert Config tab read-only labels to editable TextFields and Toggles in edit mode in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T012 [US2] Add Save (calls PATCH API) and Cancel buttons to Config tab edit mode in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T013 [US2] Add inline validation for required fields in edit mode in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T014 [US2] Fix "Command: N/A" bug — read command from config, not runtime state in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T015 [US2] Build and verify Edit Server flow with mcpproxy-ui-test tool

**Checkpoint**: Edit Server fully functional, changes persist via PATCH API

---

## Phase 4: User Story 3 - Import with Preview (Priority: P2)

**Goal**: Import shows preview with checkboxes, per-server results, browse button, and longer timeout.

**Independent Test**: Open Import tab, click Import, verify preview list with checkboxes, confirm, check results.

- [ ] T016 [US3] Add preview step — call import API with preview=true, show server list with checkboxes in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T017 [US3] Show per-server import results (imported/skipped/failed with reasons) in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T018 [US3] Add "Browse Other File..." button with NSOpenPanel in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift
- [ ] T019 [US3] Increase URLSession timeout for import requests to 120s in native/macos/MCPProxy/MCPProxy/API/APIClient.swift
- [ ] T020 [US3] Build and verify Import flow with mcpproxy-ui-test tool

**Checkpoint**: Import flow with preview, results, and file browser working

---

## Phase 5: User Story 4 - Connection Errors and Logs (Priority: P2)

**Goal**: Error visibility via tooltips, color-coded logs, auto-refresh.

**Independent Test**: Configure invalid server URL, observe error tooltip in server list, view colored logs.

- [ ] T021 [P] [US4] Add tooltip showing health.detail on hover over unhealthy server status in native/macos/MCPProxy/MCPProxy/Views/ServersView.swift
- [ ] T022 [P] [US4] Add log auto-refresh (3s interval) to Logs tab in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T023 [US4] Color-code ERROR (red) and WARN (yellow) log lines in Logs tab in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T024 [US4] Show last_error prominently at top of Logs tab when server is unhealthy in native/macos/MCPProxy/MCPProxy/Views/ServerDetailView.swift
- [ ] T025 [US4] Build and verify error display with mcpproxy-ui-test tool

**Checkpoint**: Connection errors visible in table and detail view

---

## Phase 6: User Story 5 - Keyboard Shortcuts and Polish (Priority: P3)

**Goal**: Cmd+N from main window, contextual tab defaults, empty state, accessibility.

**Independent Test**: Cmd+N opens Add Server, Dashboard import opens Import tab, empty state visible.

- [ ] T026 [US5] Add Cmd+N keyboard shortcut via SwiftUI .commands in native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift
- [ ] T027 [P] [US5] Add default tab parameter (manual vs import) to AddServerView and wire from Dashboard import button in native/macos/MCPProxy/MCPProxy/Views/AddServerView.swift and DashboardView.swift
- [ ] T028 [P] [US5] Add empty state view for server list in native/macos/MCPProxy/MCPProxy/Views/ServersView.swift
- [ ] T029 [P] [US5] Add .accessibilityLabel() to Dashboard action buttons in native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift
- [ ] T030 [US5] Remove "Security Scan: soon" placeholder from Dashboard in native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift
- [ ] T031 [US5] Build and verify all polish items with mcpproxy-ui-test tool

**Checkpoint**: All polish items complete

---

## Phase 7: Polish & Cross-Cutting

**Purpose**: Final verification and documentation

- [ ] T032 Full regression build — compile Swift tray app + Go core binary
- [ ] T033 Run full mcpproxy-ui-test verification of all 5 user stories
- [ ] T034 Update CLAUDE.md if API endpoints changed
- [ ] T035 Commit all changes with descriptive commit message

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (US1 Add Server)**: Can start after T003 (Swift APIClient)
- **Phase 3 (US2 Edit Server)**: Depends on Phase 1 (PATCH endpoint + Swift client)
- **Phase 4 (US3 Import)**: Independent — can start after Phase 1
- **Phase 5 (US4 Errors/Logs)**: Independent — no API dependencies
- **Phase 6 (US5 Polish)**: Independent — can start anytime
- **Phase 7 (Polish)**: After all stories complete

### Parallel Opportunities

- US1 (Add Server) and US4 (Errors/Logs) can run in parallel (different files)
- US5 (Polish) tasks are all parallelizable with other work
- T021-T024 within US4 can run in parallel (different views)

---

## Implementation Strategy

### MVP (User Story 1 + 2 Only)

1. Phase 1: PATCH endpoint + Swift client (T001-T003)
2. Phase 2: Add Server improvements (T004-T009)
3. Phase 3: Edit Server (T010-T015)
4. **STOP and VALIDATE**: Test Add + Edit independently
5. Ship as MVP — users can now add AND edit servers with proper UX

### Full Delivery

6. Phase 4: Import preview (T016-T020)
7. Phase 5: Error visibility (T021-T025)
8. Phase 6: Polish (T026-T031)
9. Phase 7: Final verification (T032-T035)

---

## Notes

- All Swift changes go through the same build command (swiftc)
- All Go changes in single file (server.go)
- mcpproxy-ui-test verification after each phase
- Commit after each phase checkpoint
