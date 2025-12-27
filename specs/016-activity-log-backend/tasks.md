# Tasks: Activity Log Backend

**Input**: Design documents from `/specs/016-activity-log-backend/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included per constitution (TDD requirement - V. Test-Driven Development)

**Organization**: Tasks grouped by user story for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

Following Go project structure from plan.md:
- Storage: `internal/storage/`
- Runtime: `internal/runtime/`
- HTTP API: `internal/httpapi/`
- Contracts: `internal/contracts/`
- MCP Server: `internal/server/`

---

## Phase 1: Setup

**Purpose**: Project scaffolding and type definitions

- [ ] T001 [P] Define ActivityType enum and ActivityRecord struct in internal/storage/activity_models.go
- [ ] T002 [P] Define ActivityFilter struct in internal/storage/activity_models.go
- [ ] T003 [P] Add activity event types to internal/runtime/events.go (activity.tool_call.started, activity.tool_call.completed, activity.policy_decision)
- [ ] T004 [P] Define API request/response types in internal/contracts/activity.go (ActivityListResponse, ActivityDetailResponse)
- [ ] T005 Add activity configuration fields to internal/config/config.go (retention_days, max_records, max_response_size)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core storage infrastructure that ALL user stories depend on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [ ] T006 Add ActivityRecordsBucket constant to internal/storage/models.go
- [ ] T007 Implement activityKey() function in internal/storage/activity.go (timestamp_ns + ULID format)
- [ ] T008 Implement SaveActivity() in internal/storage/activity.go (BBolt write transaction)
- [ ] T009 Implement GetActivity() in internal/storage/activity.go (BBolt read by ID)
- [ ] T010 Implement ListActivities() with pagination in internal/storage/activity.go (cursor iteration)
- [ ] T011 Implement DeleteActivity() in internal/storage/activity.go
- [ ] T012 Add activity bucket initialization in internal/storage/bbolt.go initBuckets()
- [ ] T013 Add activity storage methods to Manager interface in internal/storage/manager.go
- [ ] T014 Implement truncateResponse() helper in internal/storage/activity.go
- [ ] T015 Write unit tests for activity storage in internal/storage/activity_test.go

**Checkpoint**: Activity storage layer complete - user story implementation can begin

---

## Phase 3: User Story 1 - Query Tool Call History via REST API (Priority: P1) 🎯 MVP

**Goal**: Users can retrieve paginated, filtered history of tool calls via REST API

**Independent Test**: Call `GET /api/v1/activity` after tool calls and verify history returns with correct details

### Tests for User Story 1

- [ ] T016 [P] [US1] Write test for handleListActivity in internal/httpapi/activity_test.go (pagination, filters)
- [ ] T017 [P] [US1] Write E2E test for GET /api/v1/activity in internal/server/activity_e2e_test.go

### Implementation for User Story 1

- [ ] T018 [US1] Implement parseActivityFilters() in internal/httpapi/activity.go
- [ ] T019 [US1] Implement handleListActivity() handler in internal/httpapi/activity.go
- [ ] T020 [US1] Register GET /api/v1/activity route in internal/httpapi/server.go setupRoutes()
- [ ] T021 [US1] Add ListActivities() method to ServerController interface in internal/httpapi/server.go
- [ ] T022 [US1] Implement ListActivities() in Runtime for controller interface in internal/runtime/runtime.go
- [ ] T023 [US1] Emit activity events from handleCallTool in internal/server/mcp.go (started + completed)
- [ ] T024 [US1] Subscribe to activity events and persist in internal/runtime/activity_service.go
- [ ] T025 [US1] Initialize activity service in Runtime.New() in internal/runtime/runtime.go

**Checkpoint**: User Story 1 complete - activity list API functional and tool calls recorded

---

## Phase 4: User Story 2 - Real-time Activity Notifications via SSE (Priority: P1)

**Goal**: Tray/web UI receives real-time notifications when tool calls occur via SSE

**Independent Test**: Connect to SSE stream, make tool call, verify event arrives within 50ms

### Tests for User Story 2

- [ ] T026 [P] [US2] Write test for activity SSE events in internal/httpapi/sse_test.go

### Implementation for User Story 2

- [ ] T027 [US2] Add activity event types to SSE handler switch in internal/httpapi/server.go handleSSEEvents()
- [ ] T028 [US2] Emit activity.policy_decision event from policy check in internal/runtime/lifecycle.go
- [ ] T029 [US2] Emit activity.quarantine_change event from QuarantineServer in internal/runtime/lifecycle.go

**Checkpoint**: User Story 2 complete - real-time SSE events delivered for all activity types

---

## Phase 5: User Story 3 - View Activity Details (Priority: P2)

**Goal**: Users can view full details of a specific activity record including arguments and response

**Independent Test**: Call `GET /api/v1/activity/{id}` and verify full details returned (or 404 for unknown)

### Tests for User Story 3

- [ ] T030 [P] [US3] Write test for handleGetActivityDetail in internal/httpapi/activity_test.go (success + 404)

### Implementation for User Story 3

- [ ] T031 [US3] Implement handleGetActivityDetail() handler in internal/httpapi/activity.go
- [ ] T032 [US3] Register GET /api/v1/activity/{id} route in internal/httpapi/server.go setupRoutes()
- [ ] T033 [US3] Add GetActivity() method to ServerController interface in internal/httpapi/server.go
- [ ] T034 [US3] Implement GetActivity() in Runtime for controller interface in internal/runtime/runtime.go

**Checkpoint**: User Story 3 complete - activity detail view functional

---

## Phase 6: User Story 4 - Export Activity for Compliance (Priority: P3)

**Goal**: Enterprise users can export activity logs in JSON Lines or CSV format for compliance

**Independent Test**: Call `GET /api/v1/activity/export?format=json` and verify downloadable file returned

### Tests for User Story 4

- [ ] T035 [P] [US4] Write test for handleExportActivity in internal/httpapi/activity_test.go (json + csv formats)

### Implementation for User Story 4

- [ ] T036 [US4] Implement StreamActivities() channel-based iterator in internal/storage/activity.go
- [ ] T037 [US4] Implement handleExportActivity() handler with streaming in internal/httpapi/activity.go
- [ ] T038 [US4] Implement activityToCSVRow() helper in internal/httpapi/activity.go
- [ ] T039 [US4] Register GET /api/v1/activity/export route in internal/httpapi/server.go setupRoutes()

**Checkpoint**: User Story 4 complete - compliance export functional

---

## Phase 7: Data Management & Retention

**Purpose**: Automatic cleanup to prevent unbounded storage growth

- [ ] T040 Implement pruneOldActivities() in internal/storage/activity.go (time-based deletion)
- [ ] T041 Implement pruneExcessActivities() in internal/storage/activity.go (count-based deletion)
- [ ] T042 Implement runActivityRetentionLoop() background goroutine in internal/runtime/activity_service.go
- [ ] T043 Start retention loop in Runtime startup in internal/runtime/lifecycle.go
- [ ] T044 Write test for retention cleanup in internal/storage/activity_test.go

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, validation, and hardening

- [ ] T045 [P] Update CLAUDE.md with activity API endpoints documentation
- [ ] T046 [P] Add activity endpoints to oas/swagger.yaml
- [ ] T047 [P] Run ./scripts/verify-oas-coverage.sh to validate OpenAPI coverage
- [ ] T048 Run ./scripts/test-api-e2e.sh to verify full integration
- [ ] T049 Run ./scripts/run-linter.sh to verify code quality
- [ ] T050 Validate quickstart.md examples work correctly

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup - BLOCKS all user stories
- **User Stories (Phases 3-6)**: All depend on Foundational phase completion
  - US1 and US2 are both P1 and can proceed in parallel
  - US3 depends on US1 (needs list endpoint pattern)
  - US4 depends on storage layer from Phase 2
- **Data Management (Phase 7)**: Can start after Foundational, parallel to user stories
- **Polish (Phase 8)**: Depends on all user stories being complete

### User Story Dependencies

| Story | Depends On | Can Parallel With |
|-------|------------|-------------------|
| US1 (P1) | Foundational | US2 |
| US2 (P1) | Foundational | US1 |
| US3 (P2) | Foundational | US1, US2, US4 |
| US4 (P3) | Foundational | US1, US2, US3 |

### Within Each User Story

1. Tests written FIRST (TDD per constitution)
2. Handlers depend on storage methods
3. Routes depend on handlers
4. Integration depends on runtime wiring

### Parallel Opportunities

**Phase 1 (all parallel):**
```
T001, T002, T003, T004 can run simultaneously
```

**Phase 2 (sequential with some parallel):**
```
T006 → T007 → T008, T009, T010, T011 (parallel) → T012, T013 → T014, T015
```

**User Stories (parallel between stories):**
```
After Phase 2 completes:
  Team A: US1 (T016-T025)
  Team B: US2 (T026-T029)

After US1:
  Continue: US3 (T030-T034)

After US3:
  Continue: US4 (T035-T039)
```

---

## Parallel Example: Phase 1 Setup

```bash
# Launch all setup tasks together:
Task: "Define ActivityType enum and ActivityRecord struct in internal/storage/activity_models.go"
Task: "Define ActivityFilter struct in internal/storage/activity_models.go"
Task: "Add activity event types to internal/runtime/events.go"
Task: "Define API request/response types in internal/contracts/activity.go"
```

## Parallel Example: User Story 1 Tests

```bash
# Launch US1 tests together:
Task: "Write test for handleListActivity in internal/httpapi/activity_test.go"
Task: "Write E2E test for GET /api/v1/activity in internal/server/activity_e2e_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T005)
2. Complete Phase 2: Foundational (T006-T015)
3. Complete Phase 3: User Story 1 (T016-T025)
4. **STOP and VALIDATE**: Test activity list API independently
5. Deploy/demo if ready - users can now query tool call history!

### Incremental Delivery

1. Setup + Foundational → Storage layer ready
2. Add US1 → Activity list API functional → **MVP Demo**
3. Add US2 → Real-time SSE events → Enhanced monitoring
4. Add US3 → Detail view → Debugging capability
5. Add US4 → Export → Compliance ready
6. Add Data Management → Production ready
7. Polish → Documentation complete

### Suggested MVP Scope

**MVP = Phase 1 + Phase 2 + User Story 1**
- Total: 25 tasks (T001-T025)
- Deliverable: REST API to query tool call history with filtering and pagination
- Value: Core visibility into AI agent activity

---

## Notes

- [P] tasks = different files, no dependencies, can run in parallel
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Tests follow TDD - write and verify they FAIL before implementation
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Constitution requires tests (V. TDD) and docs update (VI. Documentation Hygiene)
