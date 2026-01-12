# Tasks: OAuth Token Refresh Reliability

**Input**: Design documents from `/specs/023-oauth-state-persistence/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Not explicitly requested in spec. Tests included for critical functionality only.

**Organization**: Tasks grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Go module**: `internal/` for packages, root for `go.mod`
- **Tests**: `*_test.go` alongside implementation files

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Constants, types, and metrics infrastructure needed by all user stories

- [X] T001 Add RefreshState type and constants in internal/oauth/refresh_manager.go
- [X] T002 [P] Add RetryBackoffBase (10s) and MaxRetryBackoff (5min) constants in internal/oauth/refresh_manager.go
- [X] T003 [P] Extend RefreshSchedule struct with RetryBackoff, MaxBackoff, LastAttempt, RefreshState fields in internal/oauth/refresh_manager.go
- [X] T004 [P] Add OAuth refresh metrics (mcpproxy_oauth_refresh_total counter, mcpproxy_oauth_refresh_duration_seconds histogram) in internal/observability/metrics.go
- [X] T005 [P] Add RecordOAuthRefresh() and RecordOAuthRefreshDuration() methods to MetricsManager in internal/observability/metrics.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core backoff algorithm and rate limiting that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T006 Implement calculateBackoff(retryCount int) method with exponential backoff and 5min cap in internal/oauth/refresh_manager.go
- [X] T007 Add rate limiting check (min 10s between attempts per server) in internal/oauth/refresh_manager.go
- [X] T008 [P] Fix misleading LogTokenRefreshSuccess() function - rename to LogClientConnectionSuccess() in internal/oauth/logging.go
- [X] T009 [P] Fix misleading success log at line ~1239 in internal/upstream/core/connection.go (HTTP connection)
- [X] T010 [P] Fix misleading success log at line ~1696 in internal/upstream/core/connection.go (SSE connection)
- [X] T011 Add accurate LogTokenRefreshAttempt() and LogTokenRefreshResult() functions in internal/oauth/logging.go

**Checkpoint**: Backoff algorithm and accurate logging ready - user story implementation can begin

---

## Phase 3: User Story 1 - Survive Laptop Sleep/Wake (Priority: P1)

**Goal**: OAuth servers automatically reconnect after laptop wakes from sleep or mcpproxy restarts, using stored refresh tokens

**Independent Test**: Authenticate OAuth server, restart mcpproxy, verify server reconnects within 60s without browser auth

### Implementation for User Story 1

- [X] T012 [US1] Modify RefreshManager.Start() to detect expired tokens at startup in internal/oauth/refresh_manager.go
- [X] T013 [US1] Add executeImmediateRefresh() method for expired tokens with valid refresh tokens in internal/oauth/refresh_manager.go
- [X] T014 [US1] Emit metrics on refresh attempt (success/failure with result label) in internal/oauth/refresh_manager.go
- [X] T015 [US1] Add structured logging for startup refresh attempts with server name, token age, result in internal/oauth/refresh_manager.go
- [X] T016 [US1] Update RefreshSchedule state to RefreshStateRetrying on failure in internal/oauth/refresh_manager.go
- [X] T017 [US1] Add unit test for startup refresh with expired access token in internal/oauth/refresh_manager_test.go

**Checkpoint**: OAuth servers survive restart - verify with manual test per quickstart.md

---

## Phase 4: User Story 2 - Proactive Token Refresh (Priority: P2)

**Goal**: Tokens are refreshed automatically at 80% lifetime before expiration, with exponential backoff retry on failure

**Independent Test**: Authenticate OAuth server, monitor token expiration, verify new token obtained before original expires

### Implementation for User Story 2

- [X] T018 [US2] Modify executeRefresh() to use new exponential backoff on failure in internal/oauth/refresh_manager.go
- [X] T019 [US2] Implement rescheduleWithBackoff() using calculateBackoff() in internal/oauth/refresh_manager.go
- [X] T020 [US2] Continue retries until token expiration (not limited retry count) in internal/oauth/refresh_manager.go
- [X] T021 [US2] Update RefreshSchedule.RefreshState transitions (Scheduled -> Retrying -> Failed) in internal/oauth/refresh_manager.go
- [X] T022 [US2] Emit refresh duration metric on each attempt in internal/oauth/refresh_manager.go
- [ ] T023 [US2] Add unit test for exponential backoff sequence (10s, 20s, 40s, 80s, 160s, 300s cap) in internal/oauth/refresh_manager_test.go
- [ ] T024 [US2] Add unit test for unlimited retries until token expiration in internal/oauth/refresh_manager_test.go

**Checkpoint**: Proactive refresh works with backoff - verify token refresh before expiration

---

## Phase 5: User Story 3 - Clear Refresh Failure Feedback (Priority: P3)

**Goal**: Users see specific failure reasons in health status (network error vs expired refresh token vs provider error)

**Independent Test**: Simulate refresh failures, verify distinct error messages in `mcpproxy upstream list` and web UI

### Implementation for User Story 3

- [X] T025 [US3] Add RefreshState, RefreshRetryCount, RefreshLastError, RefreshNextAttempt fields to HealthCalculatorInput in internal/health/calculator.go
- [X] T026 [US3] Implement health calculation for RefreshStateRetrying -> degraded level in internal/health/calculator.go
- [X] T027 [US3] Implement health calculation for RefreshStateFailed -> unhealthy level in internal/health/calculator.go
- [X] T028 [US3] Set appropriate health detail messages per refresh state in internal/health/calculator.go
- [X] T029 [US3] Set appropriate health action per state (view_logs for retrying, login for failed) in internal/health/calculator.go
- [X] T030 [US3] Add error classification for invalid_grant (permanent) vs network errors (retryable) in internal/oauth/refresh_manager.go
- [X] T031 [US3] Expose RefreshSchedule state via RefreshManager.GetRefreshState(serverName) method in internal/oauth/refresh_manager.go
- [X] T032 [US3] Wire RefreshManager state into health calculation flow in internal/health/calculator.go
- [ ] T033 [US3] Add unit test for health status output per refresh state in internal/health/calculator_test.go

**Checkpoint**: Health status shows specific refresh failure reasons

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Final validation, cleanup, and documentation

- [X] T034 [P] Verify all logging follows naming convention (OAuth token refresh *) in internal/oauth/logging.go
- [X] T035 [P] Run existing tests to ensure no regressions: go test ./internal/...
- [ ] T036 [P] Run E2E tests: ./scripts/test-api-e2e.sh
- [ ] T037 Verify metrics endpoint shows new OAuth metrics: curl http://localhost:8080/metrics | grep oauth_refresh
- [ ] T038 Manual verification per quickstart.md success criteria checklist
- [ ] T039 Update CLAUDE.md if any architecture patterns changed

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-5)**: All depend on Foundational phase completion
  - User stories can proceed in priority order (P1 -> P2 -> P3)
  - US2 depends on backoff from Foundational; US3 depends on state tracking from US1/US2
- **Polish (Phase 6)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - startup refresh
- **User Story 2 (P2)**: Builds on US1 - adds exponential backoff to existing refresh
- **User Story 3 (P3)**: Builds on US1/US2 - surfaces refresh state in health status

### Within Each User Story

- Implementation tasks in order (no parallel within story due to file dependencies)
- Unit tests after implementation of the feature they test
- Story complete before moving to next priority

### Parallel Opportunities

- Setup tasks T002, T003, T004, T005 can run in parallel (different files/functions)
- Foundational tasks T008, T009, T010 can run in parallel (different files)
- Polish tasks T034, T035, T036 can run in parallel

---

## Parallel Example: Setup Phase

```bash
# Launch all setup tasks in parallel (different files):
Task: "Add RetryBackoffBase and MaxRetryBackoff constants" [T002]
Task: "Extend RefreshSchedule struct" [T003]
Task: "Add OAuth refresh metrics" [T004]
Task: "Add MetricsManager methods" [T005]
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T005)
2. Complete Phase 2: Foundational (T006-T011)
3. Complete Phase 3: User Story 1 (T012-T017)
4. **STOP and VALIDATE**: Restart mcpproxy with OAuth server, verify auto-reconnect
5. This alone delivers SC-001: "OAuth servers automatically reconnect within 60s"

### Incremental Delivery

1. Complete Setup + Foundational -> Backoff and metrics ready
2. Add User Story 1 -> Startup recovery works -> Deploy/Demo (MVP!)
3. Add User Story 2 -> Proactive refresh with backoff -> Deploy/Demo
4. Add User Story 3 -> Clear failure feedback -> Deploy/Demo
5. Each story adds value without breaking previous stories

### Key Files Modified

| File | Tasks | User Stories |
|------|-------|--------------|
| `internal/oauth/refresh_manager.go` | T001-T003, T006-T007, T012-T016, T018-T022, T030-T031 | Setup, US1, US2, US3 |
| `internal/oauth/logging.go` | T008, T011, T034 | Foundational, Polish |
| `internal/upstream/core/connection.go` | T009, T010 | Foundational |
| `internal/observability/metrics.go` | T004, T005 | Setup |
| `internal/health/calculator.go` | T025-T029, T032 | US3 |
| `internal/oauth/refresh_manager_test.go` | T017, T023, T024 | US1, US2 |
| `internal/health/calculator_test.go` | T033 | US3 |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story delivers incremental value
- Verify with quickstart.md manual testing steps after each story
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
