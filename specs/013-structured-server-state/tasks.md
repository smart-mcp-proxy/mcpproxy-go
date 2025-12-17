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

- [ ] T001 [P] Add ActionSetSecret and ActionConfigure constants in internal/health/constants.go
- [ ] T002 [P] Add MissingSecret and OAuthConfigErr fields to HealthCalculatorInput in internal/health/calculator.go

**Checkpoint**: Constants and types ready for implementation

---

## Phase 2: Foundational (Health Detection Logic)

**Purpose**: Core health calculation that MUST be complete before user stories can be tested

**CRITICAL**: No UI/CLI changes make sense until Health correctly detects issues

- [ ] T003 Add missing secret detection check to CalculateHealth() in internal/health/calculator.go
- [ ] T004 Add OAuth config error detection check to CalculateHealth() in internal/health/calculator.go
- [ ] T005 Add helper functions to extract MissingSecret from connection errors in internal/upstream/manager.go
- [ ] T006 Add helper functions to extract OAuthConfigErr from connection errors in internal/upstream/manager.go
- [ ] T007 Populate MissingSecret and OAuthConfigErr in HealthCalculatorInput within internal/upstream/manager.go
- [ ] T008 [P] Add unit tests for set_secret action in internal/health/calculator_test.go
- [ ] T009 [P] Add unit tests for configure action in internal/health/calculator_test.go

**Checkpoint**: Health correctly detects and reports new action types

---

## Phase 3: User Story 1 - Fix Server Issues via Web UI (Priority: P1)

**Goal**: When user sees a health issue and clicks "Fix", they navigate to the correct location to resolve it

**Independent Test**:
1. Configure a server with missing secret reference (e.g., `${env:MISSING_TOKEN}`)
2. Start mcpproxy and verify Health shows `action: "set_secret"` with `detail: "MISSING_TOKEN"`
3. Click "Set Secret" button in Web UI and verify navigation to `/secrets`

### Implementation for User Story 1

- [ ] T010 [US1] Add set_secret action handler in frontend/src/components/ServerCard.vue
- [ ] T011 [US1] Add configure action handler in frontend/src/components/ServerCard.vue
- [ ] T012 [US1] Add button labels for new actions (set_secret, configure) in frontend/src/components/ServerCard.vue
- [ ] T013 [US1] Update TypeScript Server/Health types to include new action values in frontend/src/types/ if needed

**Checkpoint**: User Story 1 complete - Fix buttons navigate to correct pages

---

## Phase 4: User Story 2 - Consistent CLI and Web UI (Priority: P1)

**Goal**: `mcpproxy doctor` and Web UI show identical issues because both derive from Health

**Independent Test**:
1. Configure server with missing secret
2. Run `mcpproxy upstream list` and verify shows `set_secret` action hint
3. Run `mcpproxy doctor` and verify missing secret appears in diagnostics
4. Compare with Web UI Dashboard - should show same affected servers

### Implementation for User Story 2

- [ ] T014 [US2] Add set_secret action hint formatting in cmd/mcpproxy/upstream_cmd.go outputServers()
- [ ] T015 [US2] Add configure action hint formatting in cmd/mcpproxy/upstream_cmd.go outputServers()
- [ ] T016 [US2] Refactor Doctor() to aggregate from Health.Action instead of independent detection in internal/management/diagnostics.go
- [ ] T017 [US2] Implement set_secret aggregation by secret name (cross-cutting) in internal/management/diagnostics.go
- [ ] T018 [US2] Update diagnostics_test.go to verify aggregation from Health in internal/management/diagnostics_test.go

**Checkpoint**: User Story 2 complete - CLI and Web UI show identical issues

---

## Phase 5: User Story 3 - Single Health Banner (Priority: P2)

**Goal**: Dashboard displays ONE consolidated health section instead of duplicate banners

**Independent Test**:
1. Configure servers with various issues (connection error, missing secret, OAuth needed)
2. View Dashboard and verify only ONE "Servers Needing Attention" section appears
3. Verify all issues are represented with correct action buttons

### Implementation for User Story 3

- [ ] T019 [US3] Remove "System Diagnostics" banner from frontend/src/views/Dashboard.vue
- [ ] T020 [US3] Ensure "Servers Needing Attention" section handles all action types in frontend/src/views/Dashboard.vue
- [ ] T021 [US3] Verify action buttons display correctly for set_secret and configure in Dashboard

**Checkpoint**: User Story 3 complete - Single consolidated health display

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Verification and documentation

- [ ] T022 Run go test ./internal/health/... -v to verify all health tests pass
- [ ] T023 Run go test ./internal/management/... -v to verify diagnostics tests pass
- [ ] T024 Run ./scripts/test-api-e2e.sh for E2E verification
- [ ] T025 Run frontend build (cd frontend && npm run build) to verify no TypeScript errors
- [ ] T026 Update oas/swagger.yaml to document new action enum values (set_secret, configure)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-5)**: All depend on Foundational phase completion
  - US1 and US2 are both P1 priority but work on different layers (UI vs CLI/Backend)
  - US3 is P2 and depends on Doctor refactor from US2 for proper data flow
- **Polish (Phase 6)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational - Frontend-focused
- **User Story 2 (P1)**: Can start after Foundational - Backend/CLI-focused
- **User Story 3 (P2)**: Best after US2 since Dashboard needs proper Health-aggregated data

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

---

## Parallel Example: User Stories 1 and 2

```bash
# After Foundational phase completes, launch both user stories in parallel:

# User Story 1 - Frontend:
Task: "[US1] Add set_secret action handler in frontend/src/components/ServerCard.vue"
Task: "[US1] Add configure action handler in frontend/src/components/ServerCard.vue"

# User Story 2 - Backend/CLI:
Task: "[US2] Add set_secret action hint formatting in cmd/mcpproxy/upstream_cmd.go"
Task: "[US2] Refactor Doctor() to aggregate from Health in internal/management/diagnostics.go"
```

---

## Implementation Strategy

### MVP First (User Stories 1 & 2)

1. Complete Phase 1: Setup (constants and types)
2. Complete Phase 2: Foundational (detection logic)
3. Complete Phase 3: User Story 1 (Fix buttons navigate correctly)
4. Complete Phase 4: User Story 2 (CLI and Web UI consistent)
5. **STOP and VALIDATE**: Both P1 stories should work
6. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational -> Detection works
2. Add User Story 1 -> Fix buttons work -> Demo
3. Add User Story 2 -> CLI consistency -> Demo
4. Add User Story 3 -> Single banner -> Demo
5. Each story adds value without breaking previous stories

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1 and US2 are both P1 but target different layers (can be parallel)
- Foundational phase is critical - Health detection must work before anything else makes sense
- Verify existing tests still pass after each phase
