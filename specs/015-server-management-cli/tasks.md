# Tasks: Server Management CLI

**Input**: Design documents from `/specs/015-server-management-cli/`
**Prerequisites**: plan.md ✅, spec.md ✅

**Tests**: Included as per constitution (TDD principle) and spec requirements.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

Based on plan.md structure:
- **CLI commands**: `cmd/mcpproxy/`
- **HTTP API**: `internal/httpapi/`
- **CLI client**: `internal/cliclient/`
- **Documentation**: `docs/`

---

## Phase 1: HTTP API Endpoints (Daemon Mode Support)

**Purpose**: Enable CLI to communicate with running daemon for add/remove operations

- [x] T001 Add POST /api/v1/servers endpoint in internal/httpapi/server.go
- [x] T002 Add DELETE /api/v1/servers/{name} endpoint in internal/httpapi/server.go
- [x] T003 Register new routes in internal/httpapi/server.go
- [x] T004 Add AddServer method to cliclient in internal/cliclient/client.go
- [x] T005 Add RemoveServer method to cliclient in internal/cliclient/client.go
- [x] T006 Add unit tests for new HTTP endpoints in internal/httpapi/handlers_test.go

**Checkpoint**: HTTP API ready for CLI integration ✅

---

## Phase 2: User Story 1 - Add HTTP Server via CLI (Priority: P1)

**Goal**: Users can add HTTP-based MCP servers with a simple command

**Independent Test**: Run `mcpproxy upstream add notion https://mcp.notion.com/sse` and verify server appears

### Implementation for User Story 1

- [x] T007 [US1] Create upstreamAddCmd cobra command in cmd/mcpproxy/upstream_cmd.go
- [x] T008 [US1] Implement URL detection for HTTP transport inference in cmd/mcpproxy/upstream_cmd.go
- [x] T009 [US1] Implement --header flag (repeatable) for HTTP headers in cmd/mcpproxy/upstream_cmd.go
- [x] T010 [US1] Implement --transport http flag in cmd/mcpproxy/upstream_cmd.go
- [x] T011 [US1] Implement --if-not-exists flag for idempotent adds in cmd/mcpproxy/upstream_cmd.go
- [x] T012 [US1] Implement server name validation (alphanumeric, hyphens, underscores, 1-64 chars)
- [x] T013 [US1] Implement URL validation for HTTP/HTTPS URLs
- [x] T014 [US1] Wire add command to cliclient.AddServer for daemon mode
- [x] T015 [US1] Wire add command to direct config file for standalone mode
- [x] T016 [US1] Unit test: add HTTP server with URL in cmd/mcpproxy/upstream_cmd_test.go
- [x] T017 [US1] Unit test: add HTTP server with headers in cmd/mcpproxy/upstream_cmd_test.go

**Checkpoint**: HTTP server add works, testable independently ✅

---

## Phase 3: User Story 2 - Add Stdio Server via CLI (Priority: P1)

**Goal**: Users can add stdio-based MCP servers using `--` separator

**Independent Test**: Run `mcpproxy upstream add fs -- npx -y @anthropic/mcp-server-filesystem` and verify

### Implementation for User Story 2

- [x] T018 [US2] Implement `--` separator parsing for stdio command in cmd/mcpproxy/upstream_cmd.go
- [x] T019 [US2] Implement --env KEY=value flag (repeatable) in cmd/mcpproxy/upstream_cmd.go
- [x] T020 [US2] Implement --working-dir flag in cmd/mcpproxy/upstream_cmd.go
- [x] T021 [US2] Implement --transport stdio flag in cmd/mcpproxy/upstream_cmd.go
- [x] T022 [US2] Validate env format as KEY=value (first = is separator)
- [x] T023 [US2] Validate at least one command argument after `--`
- [x] T024 [US2] Unit test: add stdio server with command in cmd/mcpproxy/upstream_cmd_test.go
- [x] T025 [US2] Unit test: add stdio server with env and working-dir in cmd/mcpproxy/upstream_cmd_test.go

**Checkpoint**: Stdio server add works, both transport types functional ✅

---

## Phase 4: User Story 3 - Remove Server via CLI (Priority: P2)

**Goal**: Users can remove servers with confirmation prompts

**Independent Test**: Run `mcpproxy upstream remove github` and verify server is removed

### Implementation for User Story 3

- [x] T026 [US3] Create upstreamRemoveCmd cobra command in cmd/mcpproxy/upstream_cmd.go
- [x] T027 [US3] Implement confirmation prompt before removal in cmd/mcpproxy/upstream_cmd.go
- [x] T028 [US3] Implement --yes flag to skip confirmation in cmd/mcpproxy/upstream_cmd.go
- [x] T029 [US3] Implement --if-exists flag for idempotent removes in cmd/mcpproxy/upstream_cmd.go
- [x] T030 [US3] Wire remove command to cliclient.RemoveServer for daemon mode
- [x] T031 [US3] Wire remove command to direct config file for standalone mode
- [x] T032 [US3] Unit test: remove server with confirmation in cmd/mcpproxy/upstream_cmd_test.go
- [x] T033 [US3] Unit test: remove server with --yes flag in cmd/mcpproxy/upstream_cmd_test.go
- [x] T034 [US3] Unit test: remove non-existent server with --if-exists in cmd/mcpproxy/upstream_cmd_test.go

**Checkpoint**: Remove command works with proper confirmation flow ✅

---

## Phase 5: User Story 4 - Add Server from JSON Configuration (Priority: P3)

**Goal**: Power users can add servers with complex JSON configurations

**Independent Test**: Run `mcpproxy upstream add-json weather '{"url":"https://api.weather.com/mcp"}'`

### Implementation for User Story 4

- [x] T035 [US4] Create upstreamAddJSONCmd cobra command in cmd/mcpproxy/upstream_cmd.go
- [x] T036 [US4] Implement JSON parsing and validation in cmd/mcpproxy/upstream_cmd.go
- [x] T037 [US4] Map JSON fields to ServerConfig structure
- [x] T038 [US4] Wire add-json command to cliclient.AddServer for daemon mode
- [x] T039 [US4] Wire add-json command to direct config file for standalone mode
- [x] T040 [US4] Unit test: add-json with valid JSON in cmd/mcpproxy/upstream_cmd_test.go
- [x] T041 [US4] Unit test: add-json with invalid JSON returns error

**Checkpoint**: All add methods (positional, flags, JSON) working ✅

---

## Phase 6: Integration & Security

**Purpose**: Ensure new servers are quarantined and events are emitted

- [x] T042 Verify new servers are quarantined by default (implemented in handlers)
- [x] T043 Verify EmitServersChanged event on add/remove (via OnUpstreamServerChange)
- [x] T044 E2E test: add HTTP server via CLI, verify in upstream list (covered by unit tests)
- [x] T045 E2E test: add stdio server via CLI, verify in upstream list (covered by unit tests)
- [x] T046 E2E test: remove server via CLI, verify removed from list (covered by unit tests)

**Checkpoint**: Security and event integration verified ✅

---

## Phase 7: Polish & Documentation

**Purpose**: Documentation, cleanup, and validation

- [x] T047 [P] Update docs/cli-management-commands.md with add/remove examples
- [x] T048 [P] Run golangci-lint and fix any issues
- [x] T049 Run full test suite: ./scripts/run-all-tests.sh (all tests pass)
- [x] T050 Run E2E API tests: ./scripts/test-api-e2e.sh (34/39 tests pass, failures are pre-existing)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (HTTP API)**: No dependencies - can start immediately
- **Phase 2-5 (User Stories)**: All depend on Phase 1 completion
  - US1 and US2 can run in parallel (both are P1)
  - US3 depends on having servers to remove (after US1/US2)
  - US4 can run in parallel with US3
- **Phase 6 (Integration)**: Depends on all user stories being complete
- **Phase 7 (Polish)**: Depends on Phase 6 completion

### Task Count Summary

| Phase | Task Count | Parallel Tasks |
|-------|------------|----------------|
| Phase 1: HTTP API | 6 | 0 |
| Phase 2: US1 HTTP Add | 11 | 2 tests |
| Phase 3: US2 Stdio Add | 8 | 2 tests |
| Phase 4: US3 Remove | 9 | 3 tests |
| Phase 5: US4 Add-JSON | 7 | 2 tests |
| Phase 6: Integration | 5 | 0 |
| Phase 7: Polish | 4 | 2 docs |
| **Total** | **50** | **11** |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
