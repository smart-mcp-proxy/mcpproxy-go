# Tasks: Structured Server State

**Input**: Design documents from `/specs/013-structured-server-state/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Not explicitly requested - test tasks included for critical areas only.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/` for Go packages, `cmd/mcpproxy/` for CLI
- **Frontend**: `frontend/src/` for Vue components

---

## Phase 1: Setup (Backend Constants & Types)

**Purpose**: Add new health action constants and extend calculator input

- [x] T001 [P] Add ActionSetSecret and ActionConfigure constants in internal/health/constants.go
- [x] T002 [P] Add MissingSecret and OAuthConfigErr fields to HealthCalculatorInput in internal/health/calculator.go

**Checkpoint**: Constants and types ready for implementation ✅

---

## Phase 2: Foundational (Health Detection Logic)

**Purpose**: Core health calculation that MUST be complete before user stories can be tested

**CRITICAL**: No UI/CLI changes make sense until Health correctly detects issues

- [x] T003 Add missing secret detection check to CalculateHealth() in internal/health/calculator.go
- [x] T004 Add OAuth config error detection check to CalculateHealth() in internal/health/calculator.go
- [x] T005 Add helper functions to extract MissingSecret from connection errors in internal/health/calculator.go
- [x] T006 Add helper functions to extract OAuthConfigErr from connection errors in internal/health/calculator.go
- [x] T007 Populate MissingSecret and OAuthConfigErr in HealthCalculatorInput within internal/runtime/runtime.go and internal/server/mcp.go
- [x] T008 [P] Add unit tests for set_secret action in internal/health/calculator_test.go
- [x] T009 [P] Add unit tests for configure action in internal/health/calculator_test.go

**Checkpoint**: Health correctly detects and reports new action types ✅

---

## Phase 3: User Story 1 - Fix Server Issues via Web UI (Priority: P1)

**Goal**: When user sees a health issue and clicks "Fix", they navigate to the correct location to resolve it

**Independent Test**:
1. Configure a server with missing secret reference (e.g., `${env:MISSING_TOKEN}`)
2. Start mcpproxy and verify Health shows `action: "set_secret"` with `detail: "MISSING_TOKEN"`
3. Click "Set Secret" button in Web UI and verify navigation to `/secrets`

### Implementation for User Story 1

- [x] T010 [US1] Add set_secret action handler in frontend/src/components/ServerCard.vue
- [x] T011 [US1] Add configure action handler in frontend/src/components/ServerCard.vue
- [x] T012 [US1] Add button labels for new actions (set_secret, configure) in frontend/src/components/ServerCard.vue
- [x] T013 [US1] Update TypeScript Server/Health types to include new action values in frontend/src/types/api.ts

**Checkpoint**: User Story 1 complete - Fix buttons navigate to correct pages ✅

---

## Phase 4: User Story 2 - Consistent CLI and Web UI (Priority: P1)

**Goal**: `mcpproxy doctor` and Web UI show identical issues because both derive from Health

**Independent Test**:
1. Configure server with missing secret
2. Run `mcpproxy upstream list` and verify shows `set_secret` action hint
3. Run `mcpproxy doctor` and verify missing secret appears in diagnostics
4. Compare with Web UI Dashboard - should show same affected servers

### Implementation for User Story 2

- [x] T014 [US2] Add set_secret action hint formatting in cmd/mcpproxy/upstream_cmd.go outputServers()
- [x] T015 [US2] Add configure action hint formatting in cmd/mcpproxy/upstream_cmd.go outputServers()
- [x] T016 [US2] Refactor Doctor() to aggregate from Health.Action instead of independent detection in internal/management/diagnostics.go
- [x] T017 [US2] Implement set_secret aggregation by secret name (cross-cutting) in internal/management/diagnostics.go
- [x] T018 [US2] Update diagnostics_test.go to verify aggregation from Health in internal/management/diagnostics_test.go

**Checkpoint**: User Story 2 complete - CLI and Web UI show identical issues ✅

---

## Phase 5: User Story 3 - Single Health Banner (Priority: P2)

**Goal**: Dashboard displays ONE consolidated health section instead of duplicate banners

**Independent Test**:
1. Configure servers with various issues (connection error, missing secret, OAuth needed)
2. View Dashboard and verify only ONE "Servers Needing Attention" section appears
3. Verify all issues are represented with correct action buttons

### Implementation for User Story 3

- [x] T019 [US3] Remove "System Diagnostics" banner from frontend/src/views/Dashboard.vue
- [x] T020 [US3] Ensure "Servers Needing Attention" section handles all action types in frontend/src/views/Dashboard.vue
- [x] T021 [US3] Verify action buttons display correctly for set_secret and configure in Dashboard

**Checkpoint**: User Story 3 complete - Single consolidated health display ✅

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Verification and documentation

- [x] T022 Run go test ./internal/health/... -v to verify all health tests pass
- [x] T023 Run go test ./internal/management/... -v to verify diagnostics tests pass
- [x] T024 Run ./scripts/test-api-e2e.sh for E2E verification
- [x] T025 Run frontend build (cd frontend && npm run build) to verify no TypeScript errors
- [x] T026 Update oas/swagger.yaml to document new action enum values (set_secret, configure)

**Checkpoint**: All verification passed ✅

---

## Phase 7: Follow-up Fixes (Identified Gaps)

**Purpose**: Address gaps discovered during output review vs spec/plan

### Gap 1: `mcpproxy doctor` Shows OAuth Issues as Generic Connection Errors

**Problem**: Servers needing OAuth login (health action=`login`) appear under "Upstream Server Connection Errors" with generic remediation hints, not under "OAuth Authentication Required" with specific `auth login` hints.

**Root Cause**: The `doctor_cmd.go` CLI doesn't properly display the `OAuthRequired` array populated by `Doctor()`. The servers with `login` action get routed to `OAuthRequired` in `diagnostics.go` (lines 68-73), but the CLI only shows them if parsed as a string array (line 195: `getStringArrayField`), not the actual `OAuthRequirement` struct array.

- [x] T027 [US2] Fix doctor_cmd.go to display OAuthRequired array as objects (server_name, message) not string array
- [x] T028 [US2] Update doctor remediation for OAuth to show server-specific auth login commands

### Gap 2: `upstream list` ACTION Column Shows Incomplete Command

**Problem**: The ACTION column shows `auth login --server=gcal` but this isn't a runnable command - users need to know which binary to run (e.g., `./mcpproxy auth login --server=gcal` or `mcpproxy auth login --server=gcal`).

**Spec Reference**: The spec table shows "CLI Hint" as `auth login --server=X`, but users expect copy-paste-able commands.

- [x] T029 [US2] Update upstream_cmd.go to show full runnable command (matches spec format: `auth login --server=X`)

### Gap 3: `mcpproxy doctor` Remediation Not Action-Specific

**Problem**: The "Remediation" section shows generic hints for all upstream errors:
```
💡 Remediation:
  • Check server configuration in mcp_config.json
  • View detailed logs: mcpproxy upstream logs <server-name>
  • Restart server: mcpproxy upstream restart <server-name>
```

But spec says remediation should be derived from Health.Action. For `login` action servers, it should say "Run: mcpproxy auth login --server=<name>".

- [x] T030 [US2] Fixed by properly routing servers to correct diagnostic categories (OAuth→oauth_required, errors→upstream_errors)

**Checkpoint**: All gaps addressed ✅

---

## Phase 8: User Story 4 - Tray Shows Correct Actions (Priority: P1)

**Goal**: Tray menu uses `health.*` fields as single source of truth, not legacy fields or URL heuristics

**Independent Test**:
1. Configure a stdio OAuth server (e.g., `npx @anthropic/mcp-gcal`) that needs login
2. View tray menu and verify "⚠️ Login Required" appears (not just generic options)
3. Verify tray connected count matches Web UI count

### Implementation for User Story 4

**Tray Status Display (FR-013, FR-016, FR-017)**:
- [x] T031 [US4] Remove `serverSupportsOAuth()` URL heuristic function from internal/tray/managers.go
- [x] T032 [US4] Update `getServerStatusDisplay()` to use `health.level` instead of legacy `connected` field in internal/tray/managers.go
- [x] T033 [US4] Update `getServerStatusDisplay()` to use `health.summary` for status text in internal/tray/managers.go
- [x] T034 [US4] Update tooltip generation to use `health.detail` instead of `last_error` in internal/tray/managers.go

**Tray Action Menus (FR-014, FR-015)**:
- [x] T035 [US4] Update `createServerActionSubmenus()` to show actions based on `health.action` in internal/tray/managers.go
- [x] T036 [US4] Add "⚠️ Login Required" menu item when `health.action == "login"` (no URL check) in internal/tray/managers.go
- [x] T037 [US4] Add "⚠️ Set Secret" menu item when `health.action == "set_secret"` in internal/tray/managers.go
- [x] T038 [US4] Add "⚠️ Configure" menu item when `health.action == "configure"` in internal/tray/managers.go

**Tray Connected Count (FR-013)**:
- [x] T039 [US4] Update connected server count logic to use `health.level == "healthy"` only in internal/tray/managers.go
- [x] T040 [US4] Remove fallback to legacy `connected` field in connected count calculation in internal/tray/managers.go

**Tests**:
- [x] T041 [US4] [P] Add unit tests for tray menu showing login action for stdio servers in internal/tray/managers_test.go
- [x] T042 [US4] [P] Add unit tests for tray connected count using health.level in internal/tray/managers_test.go

**Checkpoint**: User Story 4 complete - Tray uses health data as single source of truth ✅

---

## Phase 9: User Story 5 - Web UI Shows Clean Error State (Priority: P2)

**Goal**: Web UI suppresses redundant `last_error` display when `health.action` already conveys the issue

**Independent Test**:
1. Configure a server that needs OAuth login
2. View server in Web UI and verify only "Login" button appears, not verbose error message
3. Configure a server with missing secret, verify only "Set Secret" button appears

### Implementation for User Story 5

**Error Display Suppression (FR-018, FR-019)**:
- [x] T043 [US5] Update ServerCard.vue to suppress `last_error` display when `health.action` is `login` in frontend/src/components/ServerCard.vue
- [x] T044 [US5] Update ServerCard.vue to suppress `last_error` display when `health.action` is `set_secret` in frontend/src/components/ServerCard.vue
- [x] T045 [US5] Update ServerCard.vue to suppress `last_error` display when `health.action` is `configure` in frontend/src/components/ServerCard.vue
- [x] T046 [US5] Add computed property `shouldShowError` that checks if error is redundant with health action in frontend/src/components/ServerCard.vue

**Checkpoint**: User Story 5 complete - Web UI shows clean, non-redundant error states ✅

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-5)**: All depend on Foundational phase completion
  - US1 and US2 are both P1 priority but work on different layers (UI vs CLI/Backend)
  - US3 is P2 and depends on Doctor refactor from US2 for proper data flow
- **Polish (Phase 6)**: Depends on all user stories being complete
- **User Story 4 (Phase 8)**: Depends on Foundational - Tray-focused, can run in parallel with US1/US2
- **User Story 5 (Phase 9)**: Depends on Foundational - Frontend-focused, can run in parallel with US4

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational - Frontend-focused
- **User Story 2 (P1)**: Can start after Foundational - Backend/CLI-focused
- **User Story 3 (P2)**: Best after US2 since Dashboard needs proper Health-aggregated data
- **User Story 4 (P1)**: Can start after Foundational - Tray-focused (internal/tray/)
- **User Story 5 (P2)**: Can start after Foundational - Frontend-focused (ServerCard.vue)

### Within Each User Story

- Health detection must work (Foundational) before UI/CLI can surface actions
- Backend changes before frontend changes that depend on them
- Story complete before moving to next priority

### Parallel Opportunities

**Phase 1** (all parallel):
- T001 and T002 can run in parallel (different files)

**Phase 2** (sequential core, parallel tests):
- T003-T007 are sequential (dependency chain)
- T008 and T009 can run in parallel (different test cases)

**User Stories** (can overlap):
- US1 (T010-T013) and US2 (T014-T018) can run in parallel (different files)
- US3 (T019-T021) should wait for US2 for best results
- US4 (T031-T042) can run in parallel with US1/US2 (different directory: internal/tray/)
- US5 (T043-T046) can run in parallel with US4 (same file as US1 but different section)

---

## Parallel Example: User Stories 1, 2, and 4

```bash
# After Foundational phase completes, launch user stories in parallel:

# User Story 1 - Frontend (ServerCard actions):
Task: "[US1] Add set_secret action handler in frontend/src/components/ServerCard.vue"
Task: "[US1] Add configure action handler in frontend/src/components/ServerCard.vue"

# User Story 2 - Backend/CLI:
Task: "[US2] Add set_secret action hint formatting in cmd/mcpproxy/upstream_cmd.go"
Task: "[US2] Refactor Doctor() to aggregate from Health in internal/management/diagnostics.go"

# User Story 4 - Tray (can run in parallel - different directory):
Task: "[US4] Remove serverSupportsOAuth() URL heuristic function from internal/tray/managers.go"
Task: "[US4] Update createServerActionSubmenus() to show actions based on health.action"
```

---

## Implementation Strategy

### MVP First (User Stories 1, 2 & 4)

1. Complete Phase 1: Setup (constants and types)
2. Complete Phase 2: Foundational (detection logic)
3. Complete Phase 3: User Story 1 (Fix buttons navigate correctly)
4. Complete Phase 4: User Story 2 (CLI and Web UI consistent)
5. Complete Phase 8: User Story 4 (Tray uses health data)
6. **STOP and VALIDATE**: All P1 stories should work
7. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational -> Detection works
2. Add User Story 1 -> Fix buttons work -> Demo
3. Add User Story 2 -> CLI consistency -> Demo
4. Add User Story 4 -> Tray consistency -> Demo
5. Add User Story 3 -> Single banner -> Demo
6. Add User Story 5 -> Clean error states -> Demo
7. Each story adds value without breaking previous stories

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1 and US2 are both P1 but target different layers (can be parallel)
- Foundational phase is critical - Health detection must work before anything else makes sense
- Verify existing tests still pass after each phase
