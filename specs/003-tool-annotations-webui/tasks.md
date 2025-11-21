# Tasks: Tool Annotations & MCP Sessions in WebUI

**Input**: Design documents from `/specs/003-tool-annotations-webui/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Following TDD principles per constitution - tests are included for core functionality.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/` at repository root (Go)
- **Frontend**: `frontend/src/` (Vue 3 + TypeScript)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add shared types and storage infrastructure needed by all user stories

- [x] T001 [P] Add ToolAnnotation struct to internal/contracts/types.go
- [x] T002 [P] Add MCPSession struct to internal/contracts/types.go
- [x] T003 [P] Add Annotations field to Tool struct in internal/contracts/types.go
- [x] T004 [P] Add Annotations field to ToolCallRecord struct in internal/contracts/types.go
- [x] T005 [P] Add GetSessionsResponse DTO to internal/contracts/types.go
- [x] T006 [P] Add GetSessionDetailResponse DTO to internal/contracts/types.go
- [x] T007 [P] Add ToolAnnotation interface to frontend/src/types/api.ts
- [x] T008 [P] Add MCPSession interface to frontend/src/types/api.ts
- [x] T009 [P] Add annotations field to Tool interface in frontend/src/types/api.ts
- [x] T010 [P] Add annotations field to ToolCallRecord interface in frontend/src/types/api.ts

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core storage and API infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [ ] T011 Create sessions BBolt bucket initialization in internal/storage/manager.go
- [ ] T012 Implement CreateSession storage method in internal/storage/manager.go
- [ ] T013 Implement CloseSession storage method in internal/storage/manager.go
- [ ] T014 Implement GetRecentSessions storage method in internal/storage/manager.go
- [ ] T015 Implement GetSessionByID storage method in internal/storage/manager.go
- [ ] T016 Implement UpdateSessionStats storage method in internal/storage/manager.go
- [ ] T017 Implement session retention cleanup (keep 100 most recent) in internal/storage/manager.go
- [ ] T018 [P] Write unit tests for session storage operations in internal/storage/session_test.go
- [ ] T019 Extend SessionStore to persist sessions to storage in internal/server/session_store.go
- [ ] T020 Add session lifecycle hooks (create on initialize, close on disconnect) in internal/server/mcp.go
- [ ] T021 [P] Add sessions API route registration in internal/httpapi/server.go
- [ ] T022 Implement handleGetSessions endpoint in internal/httpapi/server.go
- [ ] T023 Implement handleGetSessionDetail endpoint in internal/httpapi/server.go
- [ ] T024 [P] Write integration tests for session API endpoints in internal/httpapi/session_test.go

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - View Tool Annotations on Server Details Page (Priority: P1) 🎯 MVP

**Goal**: Display tool annotation badges (readOnly, destructive, idempotent, openWorld) on server details page

**Independent Test**: View any server's details page and verify annotation badges render correctly for tools with various annotation combinations

### Implementation for User Story 1

- [ ] T025 [P] [US1] Propagate tool annotations from mcp-go SDK through tool listing in internal/runtime/runtime.go
- [ ] T026 [P] [US1] Include annotations in tool API responses in internal/httpapi/server.go
- [ ] T027 [P] [US1] Create AnnotationBadges.vue component in frontend/src/components/AnnotationBadges.vue
- [ ] T028 [US1] Add annotation badges to tool cards in frontend/src/views/ServerDetail.vue
- [ ] T029 [US1] Style annotation badges with DaisyUI classes (info/error/neutral/secondary) in AnnotationBadges.vue
- [ ] T030 [US1] Handle tools without annotations gracefully (no badges displayed) in ServerDetail.vue

**Checkpoint**: User Story 1 complete - tool annotations visible on server details page

---

## Phase 4: User Story 2 - View Compact Tool Annotations in Tool Call History (Priority: P1)

**Goal**: Display compact annotation indicators with hover tooltips in tool call history list

**Independent Test**: View tool call history page and verify compact annotations appear with functional hover tooltips

### Implementation for User Story 2

- [ ] T031 [US2] Capture tool annotations when recording tool calls in internal/server/mcp.go
- [ ] T032 [US2] Lookup tool annotations from server's tool list in RecordToolCall function
- [ ] T033 [US2] Add compact mode prop to AnnotationBadges.vue for icon-only display
- [ ] T034 [US2] Add tooltip support to compact annotation icons in AnnotationBadges.vue
- [ ] T035 [US2] Integrate compact AnnotationBadges in tool call history rows in frontend/src/views/ToolCalls.vue
- [ ] T036 [US2] Ensure compact indicators don't exceed 30% of row width in ToolCalls.vue

**Checkpoint**: User Stories 1 AND 2 complete - annotations visible in both server details and history

---

## Phase 5: User Story 3 - View MCP Sessions Dashboard Table (Priority: P2)

**Goal**: Display table of 10 most recent sessions with status, start time, duration, client name, tool calls, tokens

**Independent Test**: View dashboard and verify sessions table displays correct data for active and closed sessions, clicking navigates to filtered history

### Implementation for User Story 3

- [ ] T037 [US3] Add getSessions API method to frontend/src/services/api.ts
- [ ] T038 [US3] Add getSession API method to frontend/src/services/api.ts
- [ ] T039 [P] [US3] Create SessionsTable.vue component in frontend/src/components/SessionsTable.vue
- [ ] T040 [US3] Implement session row display with all columns (status, client, start time, duration, calls, tokens)
- [ ] T041 [US3] Add duration calculation for active sessions (elapsed time) in SessionsTable.vue
- [ ] T042 [US3] Add duration calculation for closed sessions (end - start) in SessionsTable.vue
- [ ] T043 [US3] Format long durations appropriately (e.g., "2d 3h 15m") in SessionsTable.vue
- [ ] T044 [US3] Handle missing client name with graceful placeholder in SessionsTable.vue
- [ ] T045 [US3] Make session rows clickable with navigation to filtered history in SessionsTable.vue
- [ ] T046 [US3] Integrate SessionsTable component into frontend/src/views/Dashboard.vue
- [ ] T047 [US3] Implement 30-second polling for active session updates in Dashboard.vue

**Checkpoint**: User Story 3 complete - sessions dashboard table functional

---

## Phase 6: User Story 4 - Filter Tool Call History by MCP Session (Priority: P2)

**Goal**: Add session filter dropdown to Tool Call History page, pre-select via URL parameter

**Independent Test**: Apply session filter and verify only tool calls from selected session appear

### Implementation for User Story 4

- [ ] T048 [US4] Implement GetToolCallsBySession storage method in internal/storage/manager.go
- [ ] T049 [US4] Add session_id query parameter support to handleGetToolCalls in internal/httpapi/server.go
- [ ] T050 [US4] Add getToolCalls sessionId parameter to frontend/src/services/api.ts
- [ ] T051 [P] [US4] Create SessionFilter.vue dropdown component in frontend/src/components/SessionFilter.vue
- [ ] T052 [US4] Integrate SessionFilter component into frontend/src/views/ToolCalls.vue
- [ ] T053 [US4] Read sessionId from URL query parameter on page load in ToolCalls.vue
- [ ] T054 [US4] Update URL when session filter changes (shareable URLs) in ToolCalls.vue
- [ ] T055 [US4] Clear filter option to show all tool calls in SessionFilter.vue
- [ ] T056 [US4] Handle empty state when session has no tool calls in ToolCalls.vue
- [ ] T057 [US4] Persist filter selection during page navigation in ToolCalls.vue

**Checkpoint**: User Story 4 complete - session filtering functional

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T058 [P] Add SSE events for session lifecycle changes (sessions.created, sessions.updated, sessions.closed) in internal/httpapi/server.go
- [ ] T059 [P] Update Dashboard.vue to listen for session SSE events for real-time updates
- [ ] T060 [P] Add error handling for deleted sessions (redirect back to dashboard) in ToolCalls.vue
- [ ] T061 [P] Update CLAUDE.md with new API endpoints documentation
- [ ] T062 Run full E2E test suite with ./scripts/test-api-e2e.sh
- [ ] T063 Run linter with ./scripts/run-linter.sh and fix issues
- [ ] T064 Validate implementation against quickstart.md manual testing checklist

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-6)**: All depend on Foundational phase completion
  - US1 and US2 can run in parallel (different files, no dependencies)
  - US3 and US4 can start after Foundational (session storage available)
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational - No dependencies on other stories
- **User Story 2 (P1)**: Can start after Foundational - No dependencies on other stories
- **User Story 3 (P2)**: Can start after Foundational - Depends on session storage from Phase 2
- **User Story 4 (P2)**: Can start after Foundational - Depends on session storage from Phase 2

### Within Each User Story

- Backend changes before frontend changes
- Models/storage before API endpoints
- API endpoints before frontend components
- Core implementation before integration

### Parallel Opportunities

**Setup Phase (all parallel)**:
- T001-T010 can all run in parallel (different files)

**Foundational Phase**:
- T018 (tests) and T021 (route registration) and T024 (API tests) can run in parallel

**User Story 1**:
- T025 (runtime), T026 (httpapi), T027 (component) can run in parallel

**User Story 3**:
- T039 (SessionsTable) can run in parallel with backend tasks

**User Story 4**:
- T051 (SessionFilter) can run in parallel with backend tasks

---

## Parallel Example: Setup Phase

```bash
# Launch all type definition tasks together:
Task: "Add ToolAnnotation struct to internal/contracts/types.go"
Task: "Add MCPSession struct to internal/contracts/types.go"
Task: "Add Annotations field to Tool struct in internal/contracts/types.go"
Task: "Add Annotations field to ToolCallRecord struct in internal/contracts/types.go"
Task: "Add GetSessionsResponse DTO to internal/contracts/types.go"
Task: "Add GetSessionDetailResponse DTO to internal/contracts/types.go"

# Launch all frontend type tasks together:
Task: "Add ToolAnnotation interface to frontend/src/types/api.ts"
Task: "Add MCPSession interface to frontend/src/types/api.ts"
Task: "Add annotations field to Tool interface in frontend/src/types/api.ts"
Task: "Add annotations field to ToolCallRecord interface in frontend/src/types/api.ts"
```

## Parallel Example: User Story 1

```bash
# Launch backend and frontend work in parallel:
Task: "Propagate tool annotations from mcp-go SDK through tool listing in internal/runtime/runtime.go"
Task: "Include annotations in tool API responses in internal/httpapi/server.go"
Task: "Create AnnotationBadges.vue component in frontend/src/components/AnnotationBadges.vue"
```

---

## Implementation Strategy

### MVP First (User Stories 1 & 2 Only)

1. Complete Phase 1: Setup (all type definitions)
2. Complete Phase 2: Foundational (session storage + API)
3. Complete Phase 3: User Story 1 (annotations on server details)
4. Complete Phase 4: User Story 2 (compact annotations in history)
5. **STOP and VALIDATE**: Test both annotation stories independently
6. Deploy/demo if ready - annotations are fully functional

### Full Feature Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test independently → Annotations on details page
3. Add User Story 2 → Test independently → Compact annotations in history
4. Add User Story 3 → Test independently → Sessions dashboard table
5. Add User Story 4 → Test independently → Session filtering
6. Complete Polish phase → Full feature complete

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Stories 1 & 2 (annotations - both P1)
   - Developer B: User Stories 3 & 4 (sessions - both P2)
3. Stories complete and integrate independently

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Constitution requires tests for core functionality (storage, API)
- Run linter before committing: `./scripts/run-linter.sh`
