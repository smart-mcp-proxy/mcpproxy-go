# Tasks: Subscribe to notifications/tools/list_changed for Automatic Tool Re-indexing

**Input**: Design documents from `/specs/013-tool-change-notifications/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/
**Issue**: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/209

**Tests**: Tests ARE requested (per spec.md Testing Requirements section and Constitution V. TDD)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

This is a Go project with the following structure:
- **Source**: `internal/upstream/core/`, `internal/upstream/managed/`, `internal/upstream/`
- **Tests**: `internal/upstream/core/*_test.go`, `internal/upstream/managed/*_test.go`
- **Docs**: `docs/features/`, `docs/api/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify prerequisites and create feature branch

- [X] T001 Create feature branch `013-tool-change-notifications` from `main`
- [X] T002 Verify mcp-go v0.43.1 has `OnNotification` API by reviewing go.mod and library

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core callback infrastructure that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 Add `onToolsChanged func(serverName string)` field to Client struct in `internal/upstream/core/client.go`
- [X] T004 Implement `SetOnToolsChangedCallback(callback func(serverName string))` method in `internal/upstream/core/client.go`
- [X] T005 Add `toolDiscoveryCallback func(ctx context.Context, serverName string) error` field to Client struct in `internal/upstream/managed/client.go`
- [X] T006 Implement `SetToolDiscoveryCallback(callback func(ctx context.Context, serverName string) error)` method in `internal/upstream/managed/client.go`

**Checkpoint**: Foundation ready - callback fields and setters exist, user story implementation can now begin

---

## Phase 3: User Story 1 - Automatic Tool Discovery on Server Change (Priority: P1)

**Goal**: When an upstream MCP server sends `notifications/tools/list_changed`, MCPProxy automatically re-indexes tools within 5 seconds

**Independent Test**: Connect to MCP server with `capabilities.tools.listChanged: true`, trigger tool change, verify index updates within 5 seconds

### Tests for User Story 1

> **NOTE: Write these tests FIRST, ensure they FAIL before implementation**

- [ ] T007 [P] [US1] Unit test: Verify OnNotification handler is registered after Start() in `internal/upstream/core/notification_test.go`
- [ ] T008 [P] [US1] Unit test: Verify callback is invoked when tools/list_changed notification received in `internal/upstream/core/notification_test.go`
- [ ] T009 [P] [US1] Unit test: Verify only tools/list_changed triggers callback (filter other notifications) in `internal/upstream/core/notification_test.go`
- [ ] T010 [P] [US1] Unit test: Verify managed client forwards notification to discovery callback in `internal/upstream/managed/notification_test.go`

### Implementation for User Story 1

- [X] T011 [US1] Register `OnNotification` handler after `client.Start()` in `connectStdio()` in `internal/upstream/core/connection.go`
- [X] T012 [US1] Register `OnNotification` handler after `client.Start()` in `connectHTTP()` in `internal/upstream/core/connection.go`
- [X] T013 [US1] Register `OnNotification` handler after `client.Start()` in `connectSSE()` in `internal/upstream/core/connection.go`
- [X] T014 [US1] Filter notifications for `mcp.MethodNotificationToolsListChanged` and invoke `onToolsChanged` callback in `internal/upstream/core/connection.go`
- [X] T015 [US1] Wire core's `onToolsChanged` to trigger `toolDiscoveryCallback` in managed client constructor in `internal/upstream/managed/client.go`
- [X] T016 [US1] Set `toolDiscoveryCallback` to call `runtime.DiscoverAndIndexToolsForServer()` in manager's AddServer in `internal/upstream/manager.go`
- [ ] T017 [US1] Run unit tests and verify all pass: `go test ./internal/upstream/... -v -run Notification`

**Checkpoint**: User Story 1 complete - notifications trigger automatic tool re-indexing

---

## Phase 4: User Story 2 - Resilient Notification Handling (Priority: P2)

**Goal**: MCPProxy gracefully handles edge cases: duplicate notifications, rapid succession, disconnection during processing

**Independent Test**: Simulate rapid successive notifications and verify deduplication via logs showing "already in progress"

### Tests for User Story 2

- [ ] T018 [P] [US2] Unit test: Verify rapid notifications are deduplicated (discovery skipped if in progress) in `internal/upstream/core/notification_test.go`
- [ ] T019 [P] [US2] Unit test: Verify discovery errors are logged but don't crash in `internal/upstream/managed/notification_test.go`
- [ ] T020 [P] [US2] Unit test: Verify notification during disconnection is handled gracefully in `internal/upstream/managed/notification_test.go`

### Implementation for User Story 2

- [X] T021 [US2] Add debug log when notification skipped due to `discoveryInProgress` check in `internal/runtime/lifecycle.go`
- [X] T022 [US2] Wrap discovery callback invocation in goroutine with timeout (30s) in `internal/upstream/managed/client.go`
- [X] T023 [US2] Add error logging for discovery failures without crashing in `internal/upstream/managed/client.go`
- [X] T024 [US2] Handle nil callback gracefully (no-op) in core notification handler in `internal/upstream/core/connection.go`
- [ ] T025 [US2] Run unit tests and verify all pass: `go test ./internal/upstream/... -v -run Notification`

**Checkpoint**: User Story 2 complete - notification handling is resilient to edge cases

---

## Phase 5: User Story 3 - Logging and Observability (Priority: P3)

**Goal**: Clear log entries for notification receipt and processing to enable debugging

**Independent Test**: Enable debug logging, trigger notification, verify log entries contain server name and discovery results

### Tests for User Story 3

- [ ] T026 [P] [US3] Unit test: Verify INFO log on notification receipt includes server name in `internal/upstream/core/notification_test.go`
- [ ] T027 [P] [US3] Unit test: Verify DEBUG log includes capability status in `internal/upstream/core/notification_test.go`

### Implementation for User Story 3

- [X] T028 [US3] Add INFO log "Received tools/list_changed notification" with server name in `internal/upstream/core/connection.go`
- [X] T029 [US3] Add DEBUG log for server capability status after initialize() in `internal/upstream/core/connection.go`
- [X] T030 [US3] Add WARN log if notification received from server without listChanged capability in `internal/upstream/core/connection.go`
- [X] T031 [US3] Verify existing `DiscoverAndIndexToolsForServer` logs added/modified/removed counts (no changes needed if already present) in `internal/runtime/lifecycle.go`
- [ ] T032 [US3] Run unit tests and verify all pass: `go test ./internal/upstream/... -v -run Notification`

**Checkpoint**: User Story 3 complete - full observability for notification processing

---

## Phase 6: Integration Testing

**Purpose**: End-to-end verification of full notification flow

- [ ] T033 Create test MCP server that supports `capabilities.tools.listChanged: true` in `tests/notification-server/main.go`
- [ ] T034 Add API endpoint to test server for dynamically adding/removing tools in `tests/notification-server/main.go`
- [ ] T035 Implement notification sending when tools change in test server in `tests/notification-server/main.go`
- [ ] T036 Write E2E test: connect mcpproxy to test server, change tools, verify index updates in `internal/server/e2e_notification_test.go`
- [ ] T037 Write E2E test: verify backward compatibility with server not supporting notifications in `internal/server/e2e_notification_test.go`
- [ ] T038 Run full test suite: `./scripts/run-all-tests.sh`

---

## Phase 7: Documentation & Polish

**Purpose**: Update documentation and finalize

- [X] T039 [P] Add "Automatic Tool Discovery" section to `docs/features/search-discovery.md`
- [X] T040 [P] Document notification handling in `docs/api/mcp-protocol.md`
- [X] T041 [P] Add notification subscription to Key Implementation Details in `CLAUDE.md`
- [X] T042 Run linter: `./scripts/run-linter.sh`
- [X] T043 Run E2E tests: `./scripts/test-api-e2e.sh`
- [X] T044 Verify all tests pass: `go test ./internal/... -v`
- [ ] T045 Create PR with commit message following spec conventions

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup - BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational - Core functionality
- **User Story 2 (Phase 4)**: Depends on User Story 1 - Adds resilience to existing handling
- **User Story 3 (Phase 5)**: Depends on User Story 1 - Adds logging to existing handling
- **Integration (Phase 6)**: Depends on all user stories
- **Polish (Phase 7)**: Depends on Integration

### User Story Dependencies

```
Foundational (Phase 2)
         │
         ▼
User Story 1 (P1) ──────────────────┐
         │                          │
         ├──────────────┐           │
         ▼              ▼           ▼
User Story 2 (P2)  User Story 3 (P3)
         │              │
         └──────┬───────┘
                ▼
      Integration Tests
                │
                ▼
      Documentation
```

### Within Each User Story

1. Tests FIRST (fail before implementation)
2. Core layer changes
3. Managed layer changes
4. Manager layer changes
5. Verify tests pass

### Parallel Opportunities

**Phase 2 (Foundational)**:
- T003 + T005 can run in parallel (different files)
- T004 + T006 can run in parallel (different files)

**Phase 3 (User Story 1) - Tests**:
- T007, T008, T009, T010 can ALL run in parallel (different test functions)

**Phase 3 (User Story 1) - Implementation**:
- T011, T012, T013 can run in parallel (same file but independent functions)

**Phase 4, 5 - Tests**:
- All tests within each phase marked [P] can run in parallel

**Phase 7 (Documentation)**:
- T039, T040, T041 can ALL run in parallel (different files)

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together:
Task: "Unit test: Verify OnNotification handler is registered after Start() in internal/upstream/core/notification_test.go"
Task: "Unit test: Verify callback is invoked when tools/list_changed notification received in internal/upstream/core/notification_test.go"
Task: "Unit test: Verify only tools/list_changed triggers callback in internal/upstream/core/notification_test.go"
Task: "Unit test: Verify managed client forwards notification to discovery callback in internal/upstream/managed/notification_test.go"

# After tests exist and fail, launch connection handlers in parallel:
Task: "Register OnNotification handler in connectStdio() in internal/upstream/core/connection.go"
Task: "Register OnNotification handler in connectHTTP() in internal/upstream/core/connection.go"
Task: "Register OnNotification handler in connectSSE() in internal/upstream/core/connection.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: Manually test with an MCP server that sends notifications
5. Deploy/demo if ready - core functionality works!

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test → **MVP COMPLETE** (core issue #209 fixed)
3. Add User Story 2 → Test → Resilient handling
4. Add User Story 3 → Test → Full observability
5. Integration tests → Confidence
6. Documentation → User-facing completeness

### Single Developer Strategy

Execute phases sequentially in priority order:
1. Phase 1 → Phase 2 → Phase 3 → VALIDATE MVP
2. Continue with Phase 4 → Phase 5 → Phase 6 → Phase 7

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Tests fail before implementing (TDD per Constitution V)
- Commit after each phase using spec commit message conventions
- Stop at any checkpoint to validate story independently

---

## Summary

| Phase | Task Count | Description |
|-------|------------|-------------|
| Phase 1: Setup | 2 | Create branch, verify prerequisites |
| Phase 2: Foundational | 4 | Callback fields and setters |
| Phase 3: User Story 1 (P1) | 11 | Core notification → discovery flow |
| Phase 4: User Story 2 (P2) | 8 | Resilient handling |
| Phase 5: User Story 3 (P3) | 7 | Logging and observability |
| Phase 6: Integration | 6 | E2E tests |
| Phase 7: Documentation | 7 | Docs and final validation |
| **TOTAL** | **45** | |

**MVP Scope**: Phases 1-3 (17 tasks) → Core issue #209 fixed
**Full Scope**: All phases (45 tasks) → Complete feature with resilience, logging, tests, docs
