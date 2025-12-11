# Tasks: Unified Health Status

**Input**: Design documents from `/specs/012-unified-health-status/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, quickstart.md

**Tests**: Not explicitly requested in feature specification; test tasks included only in Polish phase for regression testing.

**Organization**: Tasks grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/`, `cmd/mcpproxy/`
- **Frontend**: `frontend/src/`
- **Tray**: `cmd/mcpproxy-tray/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the health package and core types

- [X] T001 Create internal/health/ directory structure
- [X] T002 Add HealthStatus struct to internal/contracts/types.go
- [X] T003 [P] Create health level, admin state, and action constants in internal/health/constants.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core health calculator that ALL interfaces depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T004 Create HealthCalculatorInput struct in internal/health/calculator.go
- [X] T005 Create HealthCalculatorConfig struct with ExpiryWarningDuration in internal/health/calculator.go
- [X] T006 Implement CalculateHealth() function in internal/health/calculator.go
- [X] T007 Add Health field to contracts.Server struct in internal/contracts/types.go
- [X] T008 Integrate CalculateHealth() into runtime.GetAllServers() in internal/runtime/runtime.go
- [X] T009 Add oauth_expiry_warning_hours config option to internal/config/config.go

**Checkpoint**: Backend health calculation complete - all interfaces can now use server.Health field

---

## Phase 3: User Story 1 - Consistent Status Across Interfaces (Priority: P1)

**Goal**: All four interfaces (CLI, tray, web UI, MCP tools) display identical health status for any server

**Independent Test**: Check any server's status in all four interfaces and verify they show identical health level and summary

### Implementation for User Story 1

- [X] T010 [US1] Update CLI upstream list display to use health.level for status emoji in cmd/mcpproxy/upstream_cmd.go
- [X] T011 [US1] Update CLI upstream list to show health.summary instead of calculating status in cmd/mcpproxy/upstream_cmd.go
- [ ] T012 [P] [US1] Update tray server menu to use health.level for status indicator in cmd/mcpproxy-tray/
- [ ] T013 [P] [US1] Update web UI ServerCard.vue to use health.level for badge color in frontend/src/components/ServerCard.vue
- [ ] T014 [US1] Update web UI ServerCard.vue to display health.summary as status text in frontend/src/components/ServerCard.vue

**Checkpoint**: All four interfaces now display the same health level and summary for any given server

---

## Phase 4: User Story 2 - Actionable Guidance for Issues (Priority: P1)

**Goal**: When a server has an issue, users see what action to take to fix it

**Independent Test**: Create various error conditions and verify each displays an appropriate action

### Implementation for User Story 2

- [X] T015 [US2] Add action hints column to CLI upstream list in cmd/mcpproxy/upstream_cmd.go
- [X] T016 [US2] Display CLI-appropriate action commands (e.g., "auth login --server=X") based on health.action in cmd/mcpproxy/upstream_cmd.go
- [ ] T017 [P] [US2] Add clickable action buttons to tray menu based on health.action in cmd/mcpproxy-tray/
- [ ] T018 [P] [US2] Add action button component to ServerCard.vue based on health.action in frontend/src/components/ServerCard.vue
- [ ] T019 [US2] Implement action button handlers (login, restart, enable, approve) in frontend/src/components/ServerCard.vue

**Checkpoint**: All interfaces show appropriate actionable guidance when servers have issues

---

## Phase 5: User Story 3 - OAuth Token Visibility in Tray/Web (Priority: P2)

**Goal**: OAuth token issues (expired, expiring soon) visible in tray and web UI, not just CLI

**Independent Test**: Let an OAuth token expire and verify tray and web UI both indicate the issue

### Implementation for User Story 3

- [ ] T020 [US3] Ensure tray displays degraded status (yellow indicator) for token expiring soon in cmd/mcpproxy-tray/
- [ ] T021 [US3] Ensure tray displays unhealthy status (red indicator) for expired token in cmd/mcpproxy-tray/
- [ ] T022 [P] [US3] Ensure web UI ServerCard shows degraded badge for expiring token in frontend/src/components/ServerCard.vue
- [ ] T023 [P] [US3] Ensure web UI ServerCard shows unhealthy badge for expired token in frontend/src/components/ServerCard.vue
- [ ] T024 [US3] Add "Token expiring" / "Token expired" message display in web UI in frontend/src/components/ServerCard.vue

**Checkpoint**: OAuth token status now visible across all interfaces, not just CLI

---

## Phase 6: User Story 4 - Admin State Separate from Health (Priority: P2)

**Goal**: Disabled and quarantined servers show admin state clearly distinct from health issues

**Independent Test**: Disable a server and verify it shows "Disabled" state, not an error

### Implementation for User Story 4

- [ ] T025 [US4] Add gray styling for disabled servers in frontend/src/components/ServerCard.vue
- [ ] T026 [US4] Add purple styling for quarantined servers in frontend/src/components/ServerCard.vue
- [ ] T027 [P] [US4] Display admin_state badge instead of level badge when server is disabled/quarantined in frontend/src/components/ServerCard.vue
- [X] T028 [P] [US4] Update CLI upstream list to show distinct indicators for disabled/quarantined in cmd/mcpproxy/upstream_cmd.go
- [ ] T029 [US4] Update tray to show distinct indicators for disabled/quarantined servers in cmd/mcpproxy-tray/

**Checkpoint**: Admin states are visually distinct from health issues in all interfaces

---

## Phase 7: User Story 5 - Dashboard Shows Servers Needing Attention (Priority: P3)

**Goal**: Web dashboard highlights servers that need attention (degraded or unhealthy)

**Independent Test**: Have a mix of healthy and unhealthy servers and verify dashboard shows correct count/list

### Implementation for User Story 5

- [ ] T030 [US5] Add computed property to filter servers needing attention in frontend/src/views/Dashboard.vue
- [ ] T031 [US5] Create "X servers need attention" banner component in frontend/src/views/Dashboard.vue
- [ ] T032 [US5] Show quick-fix buttons for each server needing attention in frontend/src/views/Dashboard.vue
- [ ] T033 [US5] Hide banner when all servers are healthy in frontend/src/views/Dashboard.vue

**Checkpoint**: Dashboard now shows servers needing attention with quick-fix actions

---

## Phase 8: User Story 6 - MCP Tools Return Unified Health Status (Priority: P2)

**Goal**: LLMs can understand server health from MCP tools without interpreting raw fields

**Independent Test**: Call upstream_servers with operation=list via MCP and verify each server includes health field

### Implementation for User Story 6

- [X] T034 [US6] Add health field to handleListUpstreams() response in internal/server/mcp.go
- [X] T035 [US6] Ensure health field uses same HealthStatus structure as REST API in internal/server/mcp.go
- [X] T036 [US6] Update MCP tool schema to document health field in response in internal/server/mcp.go

**Checkpoint**: LLMs can now get unified health status from MCP tools

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, testing, and cleanup

- [X] T037 [P] Add HealthStatus schema to oas/swagger.yaml
- [ ] T038 [P] Update CLAUDE.md with new health fields documentation
- [X] T039 [P] Create unit tests for CalculateHealth() in internal/health/calculator_test.go
- [X] T039a [P] Add test case verifying FR-016: token with working auto-refresh returns healthy in internal/health/calculator_test.go
- [ ] T040 Run quickstart.md validation scenarios
- [ ] T041 Run full test suite (./scripts/run-all-tests.sh)
- [X] T042 Run API E2E tests (./scripts/test-api-e2e.sh)
- [ ] T043 [P] Verify OpenAPI endpoint coverage (./scripts/verify-oas-coverage.sh)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-8)**: All depend on Foundational phase completion
  - US1 and US2 are both P1 priority - do them first
  - US3, US4 are P2 priority - must run SEQUENTIALLY (both modify ServerCard.vue)
  - US6 is P2 priority - can run in parallel with US3/US4 (MCP is independent of UI)
  - US5 is P3 priority - do after P2 stories
- **Polish (Phase 9)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Depends only on Foundational - core consistency
- **User Story 2 (P1)**: Depends only on Foundational - can run in parallel with US1
- **User Story 3 (P2)**: Depends on US1 (needs health display infrastructure) - modifies ServerCard.vue
- **User Story 4 (P2)**: Depends on US3 (sequential - both modify ServerCard.vue)
- **User Story 5 (P3)**: Depends on US1 (needs filtering by health.level)
- **User Story 6 (P2)**: Depends only on Foundational (MCP is independent of UI)

### Within Each User Story

- Implementation before integration tasks
- Core changes before UI polish
- Backend changes propagate through all interfaces via server.Health field

### Parallel Opportunities

- T002 and T003 can run in parallel (different files)
- T012, T013 can run in parallel (tray and web UI)
- T017, T018 can run in parallel (tray and web UI)
- T020/T021 and T022/T023 can run in parallel (tray and web UI)
- T025, T026, T027, T028 partially parallel (T27 || T28)
- T037, T038, T039, T043 all parallel (documentation and tests)

---

## Parallel Example: Foundational Phase

```bash
# After T004-T005, these can run in parallel:
Task: "Create health constants in internal/health/constants.go"
Task: "Add Health field to contracts.Server in internal/contracts/types.go"
```

## Parallel Example: User Story 1

```bash
# T012 and T013 can run in parallel:
Task: "Update tray server menu in cmd/mcpproxy-tray/"
Task: "Update ServerCard.vue badge color in frontend/src/components/ServerCard.vue"
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1 (consistent status)
4. Complete Phase 4: User Story 2 (actionable guidance)
5. **STOP and VALIDATE**: All interfaces show identical status with actions
6. Deploy/demo if ready - this is the core value

### Incremental Delivery

1. Complete Setup + Foundational → Backend health calculation ready
2. Add US1 + US2 → Core consistency and actions (MVP!)
3. Add US3 → OAuth visibility in tray/web
4. Add US4 → Admin state clarity
5. Add US6 → MCP tools (LLM support)
6. Add US5 → Dashboard attention banner
7. Polish → Documentation and testing

### Single Developer Strategy

Priority order: P1 stories first (US1 → US2), then P2 (US3 → US4 → US6), then P3 (US5)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently testable after completion
- Backend changes (Phase 2) automatically propagate to all interfaces
- No database schema changes required - health is calculated at runtime
- Commit after each phase completion for easy rollback
