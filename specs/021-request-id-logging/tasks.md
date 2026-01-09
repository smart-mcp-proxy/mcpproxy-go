# Tasks: Request ID Logging

**Input**: Design documents from `/specs/021-request-id-logging/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Tests are included as this feature touches critical error handling paths.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **CLI**: `cmd/mcpproxy/`
- **HTTP API**: `internal/httpapi/`
- **Server**: `internal/server/`
- **Request Context**: `internal/reqcontext/`
- **Contracts**: `internal/contracts/`
- **Tests**: `internal/server/e2e_test.go`, `internal/httpapi/*_test.go`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Extend existing request context infrastructure for request IDs

- [x] T001 [P] Add RequestID constants and helpers to `internal/reqcontext/correlation.go`
- [x] T002 [P] Add request ID validation helper function in `internal/reqcontext/requestid.go` (new file)
- [x] T003 Add unit tests for request ID validation in `internal/reqcontext/requestid_test.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core middleware and error response infrastructure that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [x] T004 Create RequestID middleware in `internal/httpapi/middleware.go` (new file)
  - Generate UUID v4 if `X-Request-Id` header missing or invalid
  - Validate client-provided IDs against pattern `^[a-zA-Z0-9_-]{1,256}$`
  - Set `X-Request-Id` response header BEFORE calling next handler
  - Add request ID to context using `reqcontext` package
- [x] T005 [P] Extend `contracts.ErrorResponse` in `internal/contracts/types.go` to include `request_id` field
- [x] T006 [P] Update `contracts.NewErrorResponse()` in `internal/contracts/converters.go` to accept and include request_id
- [x] T007 Update `Server.writeError()` in `internal/httpapi/server.go` to extract request ID from context and include in response
- [x] T008 Register RequestID middleware FIRST in router chain in `internal/server/server.go`
- [x] T009 Add request ID to Zap logger context in middleware for request-scoped logging

**Checkpoint**: Foundation ready - all API responses now include X-Request-Id header and error responses include request_id in body

---

## Phase 3: User Story 1 - Request ID in Error Responses (Priority: P1) MVP

**Goal**: Every error response includes a `request_id` field that users can use to find related logs.

**Independent Test**: Make any API call that returns an error and verify `request_id` in response body and `X-Request-Id` header.

### Tests for User Story 1

- [x] T010 [P] [US1] Add E2E test: verify X-Request-Id header in ALL responses in `internal/server/e2e_test.go`
- [x] T011 [P] [US1] Add E2E test: verify request_id in error JSON body in `internal/server/e2e_test.go`
- [x] T012 [P] [US1] Add E2E test: verify client-provided X-Request-Id is echoed back in `internal/server/e2e_test.go`
- [x] T013 [P] [US1] Add E2E test: verify invalid X-Request-Id is replaced with generated UUID in `internal/server/e2e_test.go`

### Implementation for User Story 1

- [x] T014 [US1] Update all `writeError()` calls in `internal/httpapi/server.go` to use new signature (audit all ~100 calls)
- [x] T015 [US1] Update `writeError()` in `internal/httpapi/activity.go` to use new error response pattern
- [x] T016 [US1] Update `writeError()` in `internal/httpapi/code_exec.go` to use new error response pattern

**Checkpoint**: At this point, User Story 1 should be fully functional - all API errors include request_id

---

## Phase 4: User Story 2 - Request-Scoped Logging (Priority: P1)

**Goal**: All log entries for a request include the `request_id`, enabling filtering.

**Independent Test**: Make a request with `X-Request-Id`, check daemon logs contain the ID.

### Tests for User Story 2

- [x] T017 [P] [US2] Add test: verify logs include request_id field when in request context in `internal/httpapi/handlers_test.go`

### Implementation for User Story 2

- [x] T018 [US2] Create logger-with-request-id helper in `internal/httpapi/middleware.go` (Done in Phase 2)
- [x] T019 [US2] Update request handlers to use context-aware logger in `internal/httpapi/server.go` (Added getRequestLogger helper)
- [x] T020 [US2] Ensure OAuth flows log both request_id and correlation_id in `internal/httpapi/middleware.go` (Already implemented)

**Checkpoint**: At this point, all request-scoped logs include request_id for filtering

---

## Phase 5: User Story 3 - CLI Error Display (Priority: P2)

**Goal**: CLI displays the request ID on errors and suggests how to retrieve logs.

**Independent Test**: Run a CLI command that fails and verify Request ID is printed with log suggestion.

### Tests for User Story 3

- [ ] T021 [P] [US3] Add test: CLI prints request_id on error in `cmd/mcpproxy/upstream_cmd_test.go`
- [ ] T022 [P] [US3] Add test: CLI does NOT print request_id on success in `cmd/mcpproxy/upstream_cmd_test.go`

### Implementation for User Story 3

- [ ] T023 [US3] Create CLI error display helper that extracts and prints request_id in `cmd/mcpproxy/cmd_helpers.go`
- [ ] T024 [US3] Update `upstream` command error handling to display request ID in `cmd/mcpproxy/upstream_cmd.go`
- [ ] T025 [US3] Update `auth` command error handling to display request ID in `cmd/mcpproxy/auth_cmd.go`
- [ ] T026 [US3] Update `activity` command error handling to display request ID in `cmd/mcpproxy/activity_cmd.go`
- [ ] T027 [US3] Update `tools` command error handling to display request ID in `cmd/mcpproxy/tools_cmd.go`
- [ ] T028 [US3] Update `call` command error handling to display request ID in `cmd/mcpproxy/call_cmd.go`
- [ ] T029 [US3] Update `code` command error handling to display request ID in `cmd/mcpproxy/code_cmd.go`

**Checkpoint**: At this point, CLI users see request_id on errors with log retrieval suggestion

---

## Phase 6: User Story 4 - Log Retrieval by Request ID (Priority: P2)

**Goal**: Users can retrieve logs filtered by request ID.

**Independent Test**: Make request, get request ID from error, retrieve logs using that ID.

### Tests for User Story 4

- [ ] T030 [P] [US4] Add E2E test: `--request-id` flag filters activity logs in `internal/server/e2e_test.go`
- [ ] T031 [P] [US4] Add E2E test: API query param `request_id` filters logs in `internal/server/e2e_test.go`

### Implementation for User Story 4

- [ ] T032 [US4] Extend ActivityRecord to include request_id field in `internal/contracts/activity.go`
- [ ] T033 [US4] Update activity recording to capture request_id from context in `internal/storage/activity.go`
- [ ] T034 [US4] Add `--request-id` flag to `activity list` command in `cmd/mcpproxy/activity_cmd.go`
- [ ] T035 [US4] Add `request_id` query parameter to activity API handler in `internal/httpapi/activity.go`
- [ ] T036 [US4] Update activity storage query to filter by request_id in `internal/storage/activity.go`

**Checkpoint**: At this point, users can retrieve logs filtered by request_id via CLI and API

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, validation, and cleanup

- [ ] T037 [P] Update OpenAPI spec (`oas/swagger.yaml`) with X-Request-Id header and error response schema
- [ ] T038 [P] Update CLAUDE.md error handling documentation
- [ ] T039 Run `./scripts/test-api-e2e.sh` to verify all E2E tests pass
- [ ] T040 Run `./scripts/verify-oas-coverage.sh` to ensure OpenAPI coverage
- [ ] T041 Run quickstart.md validation - test all documented commands work
- [ ] T042 Run `./scripts/run-linter.sh` to ensure code quality

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-6)**: All depend on Foundational phase completion
  - US1 (Phase 3) and US2 (Phase 4) can run in parallel
  - US3 (Phase 5) depends on US1 (needs error response with request_id)
  - US4 (Phase 6) can start after Foundational but benefits from US1/US2
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 3 (P2)**: Depends on US1 (error responses must include request_id first)
- **User Story 4 (P2)**: Can start after Foundational (Phase 2) - Integrates with US1/US2

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Infrastructure changes before handler updates
- Core implementation before integration
- Story complete before moving to next priority

### Parallel Opportunities

**Phase 1 (Setup)**:
```bash
# Can run in parallel:
T001: Add RequestID constants to reqcontext/correlation.go
T002: Add validation helper in reqcontext/requestid.go
```

**Phase 2 (Foundational)**:
```bash
# Can run in parallel:
T005: Extend ErrorResponse in contracts/types.go
T006: Update NewErrorResponse in contracts/converters.go
```

**Phase 3 (US1 Tests)**:
```bash
# All tests can run in parallel:
T010: E2E test for X-Request-Id header
T011: E2E test for request_id in error body
T012: E2E test for client-provided ID echo
T013: E2E test for invalid ID replacement
```

**Phase 5 (US3 Implementation)**:
```bash
# CLI command updates can run in parallel after T023:
T024: upstream_cmd.go
T025: auth_cmd.go
T026: activity_cmd.go
T027: tools_cmd.go
T028: call_cmd.go
T029: code_cmd.go
```

---

## Parallel Example: User Story 1 Implementation

```bash
# After Foundational phase completes:

# Launch all US1 tests together (should FAIL initially):
Task T010: "E2E test: verify X-Request-Id header in ALL responses"
Task T011: "E2E test: verify request_id in error JSON body"
Task T012: "E2E test: verify client-provided X-Request-Id is echoed"
Task T013: "E2E test: verify invalid X-Request-Id is replaced"

# Then implement sequentially:
Task T014: "Update writeError() calls in httpapi/server.go"
Task T015: "Update writeError() in httpapi/activity.go"
Task T016: "Update writeError() in httpapi/code_exec.go"

# Verify tests now PASS
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T009)
3. Complete Phase 3: User Story 1 (T010-T016)
4. **STOP and VALIDATE**: All API errors include request_id
5. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational Foundation ready
2. Add User Story 1 Test independently Deploy/Demo (MVP!)
3. Add User Story 2 Request-scoped logging works
4. Add User Story 3 CLI shows request_id on errors
5. Add User Story 4 Log retrieval by request_id works
6. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Story 1 (error responses)
   - Developer B: User Story 2 (logging)
3. After US1 complete:
   - Developer A: User Story 3 (CLI display)
   - Developer B: User Story 4 (log retrieval)
4. Stories complete and integrate independently

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- User Story 5 (Tray/Web UI) is OUT OF SCOPE per spec - only server-side + CLI changes

## Key Files Summary

| File | Changes |
|------|---------|
| `internal/reqcontext/correlation.go` | Add RequestIDKey constant |
| `internal/reqcontext/requestid.go` | NEW: Validation helper |
| `internal/httpapi/middleware.go` | NEW: RequestID middleware |
| `internal/contracts/types.go` | Extend ErrorResponse |
| `internal/contracts/converters.go` | Update NewErrorResponse |
| `internal/httpapi/server.go` | Update writeError, register middleware |
| `internal/httpapi/activity.go` | Update error responses, add filter |
| `internal/httpapi/code_exec.go` | Update error responses |
| `internal/server/server.go` | Register middleware in router |
| `cmd/mcpproxy/cmd_helpers.go` | CLI error display helper |
| `cmd/mcpproxy/*_cmd.go` | Update error handling |
| `internal/contracts/activity.go` | Add request_id field |
| `internal/storage/activity.go` | Store/query request_id |
| `oas/swagger.yaml` | Document X-Request-Id header |
