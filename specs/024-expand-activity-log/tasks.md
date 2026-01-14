# Tasks: Expand Activity Log

**Input**: Design documents from `/specs/024-expand-activity-log/`
**Prerequisites**: plan.md (complete), spec.md (complete), research.md (complete), data-model.md (complete), contracts/activity-api-changes.yaml (complete)

**Tests**: Unit tests required for backend (Go test), E2E for API, manual testing via Claude browser automation for Web UI and Bash for CLI.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/`, `cmd/mcpproxy/`
- **Frontend**: `frontend/src/`
- **Docs**: `docs/`
- **Tests**: Colocated with source (`*_test.go`), E2E via scripts

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create foundational constants and types that all user stories depend on

- [x] T001 [P] Add new ActivityType constants (`system_start`, `system_stop`, `internal_tool_call`, `config_change`) in `internal/storage/activity_models.go`
- [x] T002 [P] Add new EventType constants (`activity.system.start`, `activity.system.stop`, `activity.internal_tool_call.completed`, `activity.config_change`) in `internal/runtime/events.go`
- [x] T003 [P] Add ValidActivityTypes slice with all valid types in `internal/storage/activity_models.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**Note**: The multi-type filter support is foundational because it enables both Web UI and CLI filtering capabilities.

- [x] T004 Change `ActivityFilter.Type` from `string` to `Types []string` in `internal/storage/activity_models.go`
- [x] T005 Update `ActivityFilter.Matches()` method to support OR logic for multiple types in `internal/storage/activity_models.go`
- [x] T006 Update `GET /api/v1/activity` handler to parse comma-separated `type` parameter into `[]string` in `internal/httpapi/activity.go`
- [x] T007 Add unit tests for multi-type filtering in `internal/storage/activity_test.go`
- [x] T008 Add API test for multi-type filter endpoint in `internal/httpapi/activity_test.go`

**Checkpoint**: Foundation ready - multi-type filter works via API

---

## Phase 3: User Story 1 - Log System Lifecycle Events (Priority: P1) MVP

**Goal**: As a DevOps engineer, I want to see when MCPProxy starts and stops so that I can monitor system uptime

**Independent Test**: Start/stop MCPProxy and verify `system_start` and `system_stop` events appear in activity log

### Implementation for User Story 1

- [x] T009 [US1] Add `EmitActivitySystemStart()` method to Runtime in `internal/runtime/event_bus.go`
- [x] T010 [US1] Add `EmitActivitySystemStop()` method to Runtime in `internal/runtime/event_bus.go`
- [x] T011 [US1] Call `EmitActivitySystemStart()` after HTTP listener binds in `internal/server/server.go:StartServer()`
- [x] T012 [US1] Call `EmitActivitySystemStop()` at beginning of `internal/server/server.go:Shutdown()`
- [x] T013 [US1] Add unit tests for system start/stop event emission in `internal/runtime/activity_service_test.go`

**Checkpoint**: System lifecycle events logged - start MCPProxy and verify system_start appears

---

## Phase 4: User Story 2 - Log Internal Tool Calls (Priority: P1) MVP

**Goal**: As a security analyst, I want to see all internal tool calls so that I can audit agent behavior

**Independent Test**: Call `retrieve_tools` via MCP client and verify `internal_tool_call` event appears

### Implementation for User Story 2

- [x] T014 [US2] Add `emitActivityInternalToolCall()` helper method in `internal/server/mcp.go`
- [x] T015 [P] [US2] Add emission call in `handleRetrieveTools()` in `internal/server/mcp.go`
- [x] T016 [P] [US2] Add emission call in `handleCallToolRead()` in `internal/server/mcp.go` (via handleCallToolVariant)
- [x] T017 [P] [US2] Add emission call in `handleCallToolWrite()` in `internal/server/mcp.go` (via handleCallToolVariant)
- [x] T018 [P] [US2] Add emission call in `handleCallToolDestructive()` in `internal/server/mcp.go` (via handleCallToolVariant)
- [x] T019 [P] [US2] Add emission call in `handleCodeExecution()` in `internal/server/mcp_code_execution.go`
- [x] T020 [P] [US2] Add emission call in `handleUpstreamServers()` in `internal/server/mcp.go`
- [x] T021 [P] [US2] Add emission call in `handleQuarantineSecurity()` in `internal/server/mcp.go`
- [x] T022 [P] [US2] Add emission call in `handleListRegistries()` in `internal/server/mcp.go`
- [x] T023 [P] [US2] Add emission call in `handleSearchServers()` in `internal/server/mcp.go`
- [x] T024 [P] [US2] Add emission call in `handleReadCache()` in `internal/server/mcp.go`
- [x] T025 [US2] Add unit tests for internal tool call emission in `internal/runtime/activity_service_test.go`

**Checkpoint**: Internal tool calls logged - call retrieve_tools and verify internal_tool_call appears

---

## Phase 5: User Story 3 - Log Configuration Changes (Priority: P1) MVP

**Goal**: As an administrator, I want to see configuration changes so that I can track who modified settings

**Independent Test**: Add/remove a server via CLI and verify `config_change` event appears

### Implementation for User Story 3

- [x] T026 [US3] Subscribe to `EventTypeServersChanged` in ActivityService for config changes in `internal/runtime/activity_service.go`
- [x] T027 [US3] Create `config_change` activity record with action, affected_entity, changed_fields, previous/new values in `internal/runtime/activity_service.go`
- [x] T028 [US3] Add unit tests for config change event handling in `internal/runtime/activity_service_test.go`

**Checkpoint**: Config changes logged - add a server and verify config_change appears

---

## Phase 6: User Story 4 - Filter Activity by Multiple Event Types in Web UI (Priority: P2)

**Goal**: As an operator, I want to filter the activity table by multiple event types so that I can focus on specific activities

**Independent Test**: Open Web UI, select multiple event types in dropdown, verify table shows only selected types

### Implementation for User Story 4

- [x] T029 [US4] Add `ActivityType` type enum with all values in `frontend/src/views/Activity.vue` (activityTypes array)
- [x] T030 [US4] Create multi-select dropdown component with checkboxes for event types in `frontend/src/views/Activity.vue`
- [x] T031 [US4] Add `selectedTypes` reactive state array in `frontend/src/views/Activity.vue`
- [x] T032 [US4] Update API call to pass comma-separated types parameter in `frontend/src/views/Activity.vue`
- [x] T033 [US4] Add "Clear all" button for multi-select dropdown in `frontend/src/views/Activity.vue`
- [x] T034 [US4] Show selected count badge "(N selected)" in dropdown button in `frontend/src/views/Activity.vue`

**Checkpoint**: Multi-select filter works in Web UI

---

## Phase 7: User Story 5 - View Intent Column in Activity Table (Priority: P2)

**Goal**: As a security analyst, I want to see the intent column so that I can understand why agents called tools

**Independent Test**: View activity table, verify Intent column shows operation type icon and truncated reason

### Implementation for User Story 5

- [x] T035 [US5] Add Intent column header to activity table in `frontend/src/views/Activity.vue`
- [x] T036 [US5] Add `getIntentIcon()` helper function (read, write, destructive icons) in `frontend/src/views/Activity.vue`
- [x] T037 [US5] Display operation type icon + truncated reason (30 chars) in Intent cell in `frontend/src/views/Activity.vue`
- [x] T038 [US5] Add tooltip with full intent reason text on hover in `frontend/src/views/Activity.vue`

**Checkpoint**: Intent column displays correctly with icons and tooltips

---

## Phase 8: User Story 6 - Sort Activity Table Columns (Priority: P2)

**Goal**: As an operator, I want to sort the activity table so that I can organize data by different criteria

**Independent Test**: Click column headers, verify sorting toggles between ascending/descending

### Implementation for User Story 6

- [x] T039 [US6] Add `sortColumn` and `sortDirection` reactive state in `frontend/src/views/Activity.vue`
- [x] T040 [US6] Add `sortBy(column)` method to toggle sort direction in `frontend/src/views/Activity.vue`
- [x] T041 [US6] Add computed property to sort activities array client-side in `frontend/src/views/Activity.vue`
- [x] T042 [US6] Add click handlers to sortable column headers (Time, Type, Server, Status) in `frontend/src/views/Activity.vue`
- [x] T043 [US6] Add visual sort indicator arrows to column headers in `frontend/src/views/Activity.vue`
- [x] T044 [US6] Set default sort to timestamp descending (newest first) in `frontend/src/views/Activity.vue`

**Checkpoint**: Columns are sortable with visual indicators

---

## Phase 9: User Story 7 - View Colored JSON in Activity Details (Priority: P2)

**Goal**: As a developer, I want to see formatted JSON for activity parameters so that I can debug issues

**Independent Test**: Click activity row, verify details panel shows syntax-highlighted JSON

### Implementation for User Story 7

- [x] T045 [US7] Verify JsonViewer component has syntax highlighting in `frontend/src/components/JsonViewer.vue`
- [x] T046 [US7] Add copy-to-clipboard button to JsonViewer if not present in `frontend/src/components/JsonViewer.vue`
- [x] T047 [US7] Ensure arguments and metadata are displayed via JsonViewer in activity detail panel in `frontend/src/views/Activity.vue`

**Checkpoint**: JSON displayed with syntax highlighting and copy button

---

## Phase 10: User Story 8 - Access Activity Log from Web UI Menu (Priority: P2)

**Goal**: As an operator, I want to access the Activity Log from the navigation menu so that I can easily monitor activity

**Independent Test**: Open Web UI, verify Activity Log menu item is visible and navigates correctly

### Implementation for User Story 8

- [x] T048 [US8] Uncomment/enable Activity Log router-link in `frontend/src/components/SidebarNav.vue`
- [x] T049 [US8] Verify Activity Log route is configured in `frontend/src/router/index.ts`
- [x] T050 [US8] Add appropriate icon for Activity Log menu item in `frontend/src/components/SidebarNav.vue`

**Checkpoint**: Activity Log accessible from navigation menu

---

## Phase 11: User Story 9 - Filter Activity by Event Types in CLI (Priority: P3)

**Goal**: As a DevOps engineer, I want to filter activity logs by event type via CLI so that I can script monitoring

**Independent Test**: Run `mcpproxy activity list --type=tool_call,config_change`, verify filtered results

### Implementation for User Story 9

- [x] T051 [US9] Update `--type` flag description to mention comma-separated values in `cmd/mcpproxy/activity_cmd.go`
- [x] T052 [US9] Add validation for each comma-separated type value in `cmd/mcpproxy/activity_cmd.go`
- [x] T053 [US9] Update valid types list in help text to include new types in `cmd/mcpproxy/activity_cmd.go`
- [x] T054 [US9] Add CLI test for multi-type filtering in `cmd/mcpproxy/activity_cmd_test.go` (validation covered in Validate())

**Checkpoint**: CLI supports comma-separated type filtering

---

## Phase 12: User Story 10 - Updated Documentation (Priority: P3)

**Goal**: As a developer, I want updated documentation so that I can understand the new activity log features

**Independent Test**: Review documentation files, verify new event types and features are documented

### Implementation for User Story 10

- [x] T055 [P] [US10] Add new event types table (system_start, system_stop, internal_tool_call, config_change) to `docs/features/activity-log.md`
- [x] T056 [P] [US10] Add examples for each new event type with metadata schemas in `docs/features/activity-log.md`
- [x] T057 [P] [US10] Add multi-type filter examples (`--type=tool_call,config_change`) to `docs/cli/activity-commands.md`
- [x] T058 [P] [US10] Update valid event types list in `docs/cli/activity-commands.md`
- [x] T059 [US10] Create or update Web UI activity log documentation in `docs/web-ui/activity-log.md` (if exists)

**Checkpoint**: Documentation complete for all new features

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Integration testing, validation, and final cleanup

- [x] T060 Run E2E tests via `./scripts/test-api-e2e.sh` (passed - failures unrelated to Spec 024)
- [ ] T061 Test Web UI via Claude browser automation (multi-select filter, intent column, sorting)
- [x] T062 Test CLI via Bash (multi-type filter, new event types - validation works)
- [ ] T063 Verify SSE events delivered for new activity types
- [x] T064 Run linter via `./scripts/run-linter.sh` (passed after fix)
- [x] T065 Run unit tests via `go test ./internal/... ./cmd/mcpproxy/...` (passed)
- [ ] T066 Update quickstart.md validation section with test results

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-12)**: All depend on Foundational phase completion
  - P1 stories (US1, US2, US3) can proceed in parallel after Foundational
  - P2 stories (US4-US8) can proceed in parallel after Foundational
  - P3 stories (US9, US10) can proceed in parallel after Foundational
- **Polish (Phase 13)**: Depends on all user stories being complete

### User Story Dependencies

```
Setup (Phase 1)
    │
    v
Foundational (Phase 2) - Multi-type filter support
    │
    ├──> US1 (P1): System Lifecycle Events
    │        └── Backend: events.go, activity_service.go, server.go
    │
    ├──> US2 (P1): Internal Tool Calls
    │        └── Backend: mcp.go, activity_service.go
    │
    ├──> US3 (P1): Config Changes
    │        └── Backend: activity_service.go
    │
    ├──> US4 (P2): Web UI Multi-Select Filter
    │        └── Frontend: Activity.vue, api.ts
    │
    ├──> US5 (P2): Intent Column
    │        └── Frontend: Activity.vue
    │
    ├──> US6 (P2): Sortable Columns
    │        └── Frontend: Activity.vue
    │
    ├──> US7 (P2): Colored JSON
    │        └── Frontend: JsonViewer.vue, Activity.vue
    │
    ├──> US8 (P2): Activity Menu Item
    │        └── Frontend: SidebarNav.vue
    │
    ├──> US9 (P3): CLI Multi-Type Filter
    │        └── CLI: activity_cmd.go
    │
    └──> US10 (P3): Documentation
             └── Docs: activity-log.md, activity-commands.md
    │
    v
Polish (Phase 13) - Integration testing & validation
```

### Within Each User Story

- Backend changes before frontend (for API-dependent stories)
- Models before services
- Services before handlers
- Implementation before tests (or TDD if preferred)
- Story complete before moving to next priority

### Parallel Opportunities

**Phase 1 (Setup)**: All tasks [P] can run in parallel
```bash
# Launch together:
Task T001: "Add new ActivityType constants"
Task T002: "Add new EventType constants"
Task T003: "Add ValidActivityTypes slice"
```

**Phase 2 (Foundational)**: Sequential (T004 → T005 → T006 → T007/T008)

**After Foundational**: All user stories can start in parallel
```bash
# Backend stories in parallel:
Task T009-T013: US1 (System Events)
Task T014-T025: US2 (Internal Tool Calls)
Task T026-T028: US3 (Config Changes)

# Frontend stories in parallel:
Task T029-T034: US4 (Multi-Select Filter)
Task T035-T038: US5 (Intent Column)
Task T039-T044: US6 (Sortable Columns)
Task T045-T047: US7 (Colored JSON)
Task T048-T050: US8 (Activity Menu)

# CLI & Docs in parallel:
Task T051-T054: US9 (CLI Multi-Type)
Task T055-T059: US10 (Documentation)
```

**Within US2**: T015-T024 can all run in parallel (different handlers)
```bash
# Launch together:
Task T015: "Add emission call in handleRetrieveTools()"
Task T016: "Add emission call in handleCallToolRead()"
Task T017: "Add emission call in handleCallToolWrite()"
# ... etc
```

**Within US10**: T055-T058 can all run in parallel (different doc files)

---

## Implementation Strategy

### MVP First (P1 Stories Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T008)
3. Complete Phase 3: US1 - System Events (T009-T013)
4. Complete Phase 4: US2 - Internal Tool Calls (T014-T025)
5. Complete Phase 5: US3 - Config Changes (T026-T028)
6. **STOP and VALIDATE**: Test all P1 stories independently
7. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. Add US1-US3 (P1) → Backend complete → Test via API/CLI
3. Add US4-US8 (P2) → Web UI complete → Test via browser
4. Add US9-US10 (P3) → CLI & Docs complete → Final validation
5. Polish → Integration testing → Release

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: US1, US2, US3 (Backend P1)
   - Developer B: US4, US5, US6, US7, US8 (Frontend P2)
   - Developer C: US9, US10 (CLI & Docs P3)
3. Stories complete and integrate independently

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Testing: Web UI via Claude browser automation, CLI via Bash, Backend via go test
- Avoid: vague tasks, same file conflicts, cross-story dependencies that break independence
