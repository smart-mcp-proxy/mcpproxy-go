# Tasks: Management Service Refactoring & OpenAPI Generation

**Input**: Design documents from `/specs/004-management-health-refactor/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Tests are included based on SC-007 requirement (80% unit test coverage target) and constitution TDD principles.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

This is a Go backend refactoring project with CLI interface:
- **Core packages**: `internal/management/`, `internal/contracts/`, `internal/httpapi/`
- **CLI commands**: `cmd/mcpproxy/`
- **Client layer**: `internal/cliclient/`
- **Tests**: Alongside source files (`*_test.go`)
- **Documentation**: `docs/`, `CLAUDE.md`, `README.md`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Install swaggo/swag and prepare package structure

- [X] T001 Install swaggo/swag CLI tool: `go install github.com/swaggo/swag/cmd/swag@latest`
- [X] T002 [P] Add swaggo dependencies to go.mod: `github.com/swaggo/http-swagger` and `github.com/swaggo/files`
- [X] T003 [P] Create internal/management package directory structure
- [X] T004 [P] Create specs/004-management-health-refactor/contracts/ directory with example files (already exists)

**Checkpoint**: Dependencies installed, package structure ready

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T005 Create ManagementService interface in internal/management/service.go with all method signatures (ListServers, EnableServer, RestartServer, RestartAll, Doctor, GetServerLogs, AuthStatus)
- [X] T006 Create contracts.Diagnostics type in internal/contracts/diagnostics.go with all fields (TotalIssues, UpstreamErrors, OAuthRequired, MissingSecrets, RuntimeWarnings, DockerStatus, Timestamp)
- [X] T007 [P] Create contracts.UpstreamError type in internal/contracts/diagnostics.go
- [X] T008 [P] Create contracts.OAuthRequirement type in internal/contracts/diagnostics.go
- [X] T009 [P] Create contracts.MissingSecret type in internal/contracts/diagnostics.go
- [X] T010 [P] Create contracts.DockerStatus type in internal/contracts/diagnostics.go
- [X] T011 [P] Create contracts.AuthStatus type in internal/contracts/diagnostics.go
- [X] T012 Implement service struct with dependencies (manager, config, eventBus, logReader, secretResolver, logger) in internal/management/service.go
- [X] T013 Implement NewService constructor function in internal/management/service.go
- [X] T014 Add GetManagementService() method to Runtime in internal/runtime/runtime.go (expose service to controllers)
- [X] T015 Wire management service into Runtime.NewRuntime() in internal/runtime/runtime.go (instantiate and inject dependencies)
- [X] T016 Update ServerController interface in internal/httpapi/server.go to include GetManagementService() method

**Checkpoint**: Foundation ready - service interface exists, contracts defined, wired into runtime. User story implementation can now begin.

---

## Phase 3: User Story 1 - Unified Management Operations (Priority: P1) 🎯 MVP

**Goal**: Eliminate duplicate logic by centralizing server lifecycle operations (list, enable, disable, restart) in the management service. All CLI, REST, and MCP interfaces delegate to this service.

**Independent Test**: Execute the same operation (e.g., restart server) via all three interfaces (CLI `mcpproxy upstream restart`, REST `POST /api/v1/servers/{name}/restart`, MCP `upstream_servers`) and verify identical behavior, event emissions, and state changes.

### Tests for User Story 1

> **NOTE: Write these tests FIRST, ensure they FAIL before implementation (TDD)**

- [X] T017 [P] [US1] Unit test for checkWriteGates() in internal/management/service_test.go (verify disable_management and read_only enforcement)
- [X] T018 [P] [US1] Unit test for ListServers() in internal/management/service_test.go (verify server listing and stats calculation)
- [X] T019 [P] [US1] Unit test for EnableServer() in internal/management/service_test.go (verify gate checks, manager call, event emission)
- [X] T020 [P] [US1] Unit test for RestartServer() in internal/management/service_test.go (verify gate checks, manager call, event emission)
- [ ] T021 [P] [US1] Integration test for REST→Service delegation in internal/httpapi/server_test.go (verify POST /api/v1/servers/{name}/restart delegates correctly)
- [ ] T022 [P] [US1] E2E test for CLI→REST→Service flow in cmd/mcpproxy/upstream_cmd_test.go (verify `mcpproxy upstream restart` works)

### Implementation for User Story 1

- [X] T023 [P] [US1] Implement checkWriteGates() helper method in internal/management/service.go (check disable_management and read_only flags)
- [X] T024 [P] [US1] Implement ListServers() method in internal/management/service.go (call manager.GetAllServers(), compute stats)
- [X] T025 [US1] Implement EnableServer() method in internal/management/service.go (gate check→manager call→event emission) [depends on T023]
- [X] T026 [US1] Implement RestartServer() method in internal/management/service.go (gate check→manager call→event emission) [depends on T023]
- [X] T027 [US1] Update handleGetServers in internal/httpapi/server.go to delegate to managementService.ListServers()
- [X] T028 [US1] Update handleEnableServer in internal/httpapi/server.go to delegate to managementService.EnableServer()
- [X] T029 [US1] Update handleDisableServer in internal/httpapi/server.go to delegate to managementService.EnableServer(false)
- [X] T030 [US1] Update handleRestartServer in internal/httpapi/server.go to delegate to managementService.RestartServer()
- [X] T031 [US1] Update upstream_servers MCP tool handler in internal/server/mcp.go to delegate enable/disable/restart to management service
- [X] T032 [US1] Update runUpstreamRestart in cmd/mcpproxy/upstream_cmd.go to preserve existing flag behavior while calling REST client
- [X] T033 [US1] Update runUpstreamList in cmd/mcpproxy/upstream_cmd.go to preserve existing output format while calling REST client

**Checkpoint**: At this point, User Story 1 should be fully functional. Server lifecycle operations (list, enable, disable, restart) work identically across CLI, REST, and MCP interfaces. Gates are enforced. Events are emitted. Tests pass with 80%+ coverage.

---

## Phase 4: User Story 2 - Comprehensive Health Diagnostics (Priority: P1)

**Goal**: Provide a single `doctor` command that aggregates health diagnostics (connection errors, OAuth requirements, missing secrets, Docker status) across all servers.

**Independent Test**: Create known failure conditions (e.g., missing OAuth token, connection error, missing secret) and verify `mcpproxy doctor` detects and reports all issues with actionable guidance in both JSON and pretty formats.

### Tests for User Story 2

- [X] T034 [P] [US2] Unit test for Doctor() method in internal/management/diagnostics_test.go (verify aggregation logic with mock servers)
- [X] T035 [P] [US2] Unit test for OAuth requirements detection in internal/management/diagnostics_test.go
- [X] T036 [P] [US2] Unit test for missing secrets detection in internal/management/diagnostics_test.go
- [X] T037 [P] [US2] Unit test for Docker status check in internal/management/diagnostics_test.go
- [ ] T038 [P] [US2] Integration test for GET /api/v1/doctor endpoint in internal/httpapi/server_test.go
- [ ] T039 [P] [US2] E2E test for `mcpproxy doctor` CLI command in cmd/mcpproxy/doctor_cmd_test.go

### Implementation for User Story 2

- [X] T040 [P] [US2] Implement Doctor() method in internal/management/diagnostics.go (aggregate upstream errors from manager.GetAllServers())
- [X] T041 [P] [US2] Implement findServersUsingSecret() helper in internal/management/diagnostics.go (identify which servers reference a secret)
- [X] T042 [P] [US2] Implement checkDockerDaemon() helper in internal/management/diagnostics.go (check Docker availability)
- [X] T043 [US2] Add missing secrets detection to Doctor() in internal/management/diagnostics.go (use secretResolver.ListReferences()) [depends on T041]
- [X] T044 [US2] Add Docker status check to Doctor() in internal/management/diagnostics.go (conditionally if isolation enabled) [depends on T042]
- [X] T045 [US2] Add handleGetDiagnostics REST endpoint in internal/httpapi/server.go (call managementService.Doctor())
- [X] T046 [US2] Register GET /api/v1/doctor route in internal/httpapi/server.go setupRoutes()
- [X] T047 [US2] Add doctor MCP tool handler in internal/server/mcp.go (call managementService.Doctor(), return JSON)
- [X] T048 [US2] Add GetDiagnostics() method to internal/cliclient/client.go (call GET /api/v1/doctor)
- [X] T049 [US2] Update runDoctor in cmd/mcpproxy/doctor_cmd.go to call client.GetDiagnostics() and format output

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently. Doctor diagnostics provide comprehensive health visibility via CLI/REST/MCP. Output formats (JSON/pretty) both work.

---

## Phase 5: User Story 3 - Automatic OpenAPI Documentation (Priority: P2)

**Goal**: Generate OpenAPI 3.x specification from swag annotations automatically during build. Ensure API documentation stays synchronized with codebase for every release.

**Independent Test**: Run `make build`, verify oas/swagger.yaml exists and validates successfully with `swagger-cli validate`. Third-party OpenAPI tools can consume the spec without errors.

### Tests for User Story 3

- [ ] T050 [P] [US3] Validation test for generated OpenAPI spec in internal/httpapi/swagger_test.go (run swag init, validate schema)
- [ ] T051 [P] [US3] Integration test for Swagger UI endpoint in internal/httpapi/swagger_test.go (verify /swagger/ returns HTML)
- [ ] T052 [P] [US3] Build test in Makefile validation (verify `make build` regenerates spec successfully)

### Implementation for User Story 3

- [ ] T053 [US3] Add API metadata annotations to cmd/mcpproxy/main.go (@title, @version, @description, @host, @BasePath, @securityDefinitions)
- [ ] T054 [P] [US3] Add swag annotations to handleGetServers in internal/httpapi/server.go (@Summary, @Description, @Tags, @Produce, @Param, @Success, @Failure, @Router, @Security)
- [ ] T055 [P] [US3] Add swag annotations to handleRestartServer in internal/httpapi/server.go
- [ ] T056 [P] [US3] Add swag annotations to handleEnableServer and handleDisableServer in internal/httpapi/server.go
- [ ] T057 [P] [US3] Add swag annotations to handleGetServerLogs in internal/httpapi/server.go
- [ ] T058 [P] [US3] Add swag annotations to handleGetDiagnostics in internal/httpapi/server.go
- [ ] T059 [P] [US3] Add swag annotations to all remaining /api/v1 endpoints in internal/httpapi/server.go (servers, tools, docker, secrets, stats, sessions, config)
- [ ] T060 [US3] Create Swagger UI handler in internal/httpapi/swagger.go (mount httpSwagger.WrapHandler, import generated docs)
- [ ] T061 [US3] Register Swagger UI route in internal/httpapi/server.go setupRoutes() (mount at /swagger/)
- [ ] T062 [US3] Add swagger target to Makefile (run swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml)
- [ ] T063 [US3] Update build target in Makefile to depend on swagger target (build: swagger frontend-build...)
- [ ] T064 [US3] Add docs/ to .gitignore exceptions (ensure generated docs are tracked)

**Checkpoint**: At this point, User Stories 1, 2, AND 3 should all work independently. OpenAPI spec auto-generates during build, validates successfully, and Swagger UI serves documentation at /swagger/.

---

## Phase 6: User Story 4 - Bulk Server Management (Priority: P3)

**Goal**: Enable efficient bulk operations (enable-all, disable-all, restart-all) for managing multiple servers without individual commands.

**Independent Test**: Configure 5+ servers and execute `mcpproxy upstream restart --all`, verifying all servers restart and command returns accurate success/failure counts.

### Tests for User Story 4

- [ ] T065 [P] [US4] Unit test for RestartAll() in internal/management/service_test.go (verify sequential execution, partial failure handling)
- [ ] T066 [P] [US4] Unit test for EnableAll() in internal/management/service_test.go
- [ ] T067 [P] [US4] Unit test for DisableAll() in internal/management/service_test.go
- [ ] T068 [P] [US4] Integration test for POST /api/v1/servers/restart_all in internal/httpapi/server_test.go
- [ ] T069 [P] [US4] E2E test for `mcpproxy upstream restart --all` in cmd/mcpproxy/upstream_cmd_test.go

### Implementation for User Story 4

- [ ] T070 [P] [US4] Implement RestartAll() method in internal/management/service.go (gate check→iterate servers→call RestartServer()→return count and errors)
- [ ] T071 [P] [US4] Implement EnableAll() method in internal/management/service.go (gate check→iterate→call EnableServer(true)→return count)
- [ ] T072 [P] [US4] Implement DisableAll() method in internal/management/service.go (gate check→iterate→call EnableServer(false)→return count)
- [ ] T073 [P] [US4] Add handleRestartAll endpoint in internal/httpapi/server.go (call managementService.RestartAll(), return success/failure counts)
- [ ] T074 [P] [US4] Add handleEnableAll endpoint in internal/httpapi/server.go
- [ ] T075 [P] [US4] Add handleDisableAll endpoint in internal/httpapi/server.go
- [ ] T076 [US4] Register POST /api/v1/servers/restart_all route in internal/httpapi/server.go setupRoutes()
- [ ] T077 [US4] Register POST /api/v1/servers/enable_all and disable_all routes in internal/httpapi/server.go setupRoutes()
- [ ] T078 [US4] Add swag annotations to bulk operation endpoints in internal/httpapi/server.go (handleRestartAll, handleEnableAll, handleDisableAll)
- [ ] T079 [US4] Add RestartAll() method to internal/cliclient/client.go (call POST /api/v1/servers/restart_all)
- [ ] T080 [US4] Add EnableAll() and DisableAll() methods to internal/cliclient/client.go
- [ ] T081 [US4] Update runUpstreamRestart in cmd/mcpproxy/upstream_cmd.go to support --all flag (call client.RestartAll())
- [ ] T082 [US4] Update runUpstreamEnable and runUpstreamDisable in cmd/mcpproxy/upstream_cmd.go to support --all flag

**Checkpoint**: All user stories (1, 2, 3, 4) should now be independently functional. Bulk operations work across CLI/REST/MCP with partial failure reporting.

---

## Phase 7: Logging Enhancements (Optional - Can Be Done in Parallel or Later)

**Purpose**: Implement comprehensive logging requirements (FR-008b, FR-008c, FR-008d) - NOT blocking for management service

**Note**: These tasks enhance what gets logged to `server-{name}.log` files. Management service already exposes logs via GetServerLogs(). These enhancements can be implemented incrementally.

- [ ] T083 [P] Add HTTP request logging to internal/upstream/core/client.go (logHTTPRequest method with sanitized headers)
- [ ] T084 [P] Add OAuth token exchange logging to internal/oauth/ (log token requests with redacted tokens)
- [ ] T085 [P] Update Docker log streaming in internal/upstream/core/monitoring.go monitorDockerLogsWithContext (stream stderr only)
- [ ] T086 [P] Add token redaction helper functions to internal/logs/sanitizer.go (redactToken, sanitizeAuthHeader)
- [ ] T087 [P] Add unit tests for sanitization functions in internal/logs/sanitizer_test.go
- [ ] T088 Validate logs include HTTP/OAuth/Docker details per FR-008a-f requirements

**Checkpoint**: Logs now include comprehensive operational details (HTTP requests, OAuth flows, Docker stderr) with automatic secret sanitization.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories, documentation, and final validation

- [ ] T089 [P] Update CLAUDE.md with management service architecture section (CLI → Service ← REST ← MCP diagram)
- [ ] T090 [P] Update README.md with new `/api/v1/doctor` and bulk operation endpoints
- [ ] T091 [P] Update docs/cli-management-commands.md with complete endpoint mapping table
- [ ] T092 [P] Add architecture diagram to specs/004-management-health-refactor/plan.md showing service layer flow
- [ ] T093 Run `make build` and verify OpenAPI spec generates without errors
- [ ] T094 Validate OpenAPI spec with `swagger-cli validate oas/swagger.yaml`
- [ ] T095 Run `./scripts/run-linter.sh` and fix any linting issues
- [ ] T096 Run `./scripts/test-api-e2e.sh` and verify all E2E tests pass
- [ ] T097 Run `./scripts/run-all-tests.sh` and verify full test suite passes
- [ ] T098 Verify unit test coverage reaches 80%+ for internal/management/ (SC-007)
- [ ] T099 Measure code duplication reduction across CLI/REST/MCP implementations (target 40%+ reduction per SC-006)
- [ ] T100 Performance test: Verify doctor diagnostics complete in <3s for 20 servers (SC-002)
- [ ] T101 Backward compatibility test: Run existing CLI scripts and verify no breaking changes (SC-005)

**Checkpoint**: All success criteria (SC-001 through SC-008) validated. Feature complete and ready for merge.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phases 3-6)**: All depend on Foundational phase completion
  - US1 (P1): Can start after Foundational - RECOMMENDED MVP (highest value)
  - US2 (P1): Can start after Foundational - Can work in parallel with US1
  - US3 (P2): Can start after Foundational - Can work in parallel with US1/US2
  - US4 (P3): Can start after Foundational - Can work in parallel with US1/US2/US3
- **Logging Enhancements (Phase 7)**: Independent - Can start anytime, no blocking dependencies
- **Polish (Phase 8)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Independent - No dependencies on other stories
- **User Story 2 (P1)**: Independent - No dependencies on other stories (uses same service interface)
- **User Story 3 (P2)**: Independent - No dependencies on other stories (adds annotations to existing handlers)
- **User Story 4 (P3)**: Soft dependency on US1 (reuses service methods), but independently testable

### Within Each User Story

- Tests MUST be written and FAIL before implementation (TDD)
- Unit tests before implementation
- Service methods before REST handlers
- REST handlers before CLI updates
- Story complete and tested before moving to next priority

### Parallel Opportunities

**Setup Phase**:
- T002, T003, T004 can run in parallel (different files)

**Foundational Phase**:
- T007, T008, T009, T010, T011 can run in parallel (different contract types)

**User Story 1 Tests**:
- T017, T018, T019, T020, T021, T022 can run in parallel (different test files)

**User Story 1 Implementation**:
- T023, T024 can run in parallel (different methods in same file, but simple enough)

**User Story 2 Tests**:
- T034, T035, T036, T037, T038, T039 can run in parallel (different test scenarios)

**User Story 2 Implementation**:
- T040, T041, T042 can run in parallel (different helper methods)

**User Story 3 Implementation**:
- T054-T059 can run in parallel (different handler annotations)

**User Story 4 Tests**:
- T065, T066, T067, T068, T069 can run in parallel (different test scenarios)

**User Story 4 Implementation**:
- T070, T071, T072 can run in parallel (similar bulk operation methods)
- T073, T074, T075 can run in parallel (different REST handlers)

**Logging Enhancements**:
- T083, T084, T085, T086, T087 can all run in parallel (different packages)

**Polish Phase**:
- T089, T090, T091, T092 can run in parallel (different documentation files)

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together (TDD - write first):
Task: "Unit test for checkWriteGates() in internal/management/service_test.go"
Task: "Unit test for ListServers() in internal/management/service_test.go"
Task: "Unit test for EnableServer() in internal/management/service_test.go"
Task: "Unit test for RestartServer() in internal/management/service_test.go"
Task: "Integration test for REST→Service in internal/httpapi/server_test.go"
Task: "E2E test for CLI→REST→Service in cmd/mcpproxy/upstream_cmd_test.go"

# Verify all tests FAIL (red phase)

# Launch core implementation together:
Task: "Implement checkWriteGates() in internal/management/service.go"
Task: "Implement ListServers() in internal/management/service.go"

# Then sequential (dependencies):
Task: "Implement EnableServer() in internal/management/service.go" (needs checkWriteGates)
Task: "Implement RestartServer() in internal/management/service.go" (needs checkWriteGates)

# Launch handler updates in parallel:
Task: "Update handleGetServers in internal/httpapi/server.go"
Task: "Update handleEnableServer in internal/httpapi/server.go"
Task: "Update handleDisableServer in internal/httpapi/server.go"
Task: "Update handleRestartServer in internal/httpapi/server.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 + User Story 2)

1. Complete Phase 1: Setup (T001-T004)
2. Complete Phase 2: Foundational (T005-T016) - CRITICAL
3. Complete Phase 3: User Story 1 (T017-T033)
4. **CHECKPOINT**: Test US1 independently - server lifecycle operations work across CLI/REST/MCP
5. Complete Phase 4: User Story 2 (T034-T049)
6. **CHECKPOINT**: Test US2 independently - doctor diagnostics work across CLI/REST/MCP
7. Deploy/demo MVP (US1 + US2 deliver core management functionality)

**Why US1+US2 as MVP**: Both are P1 priority, provide immediate operational value (manage servers + diagnose issues), and represent the core refactoring goal (unified service layer).

### Incremental Delivery

1. **Foundation** (Phases 1-2) → Service interface exists, wired into runtime
2. **MVP** (US1+US2) → Core management + diagnostics → Test → Deploy
3. **Enhanced** (US3) → OpenAPI docs → Test → Deploy
4. **Complete** (US4) → Bulk operations → Test → Deploy
5. **Polished** (Logging + Polish) → Comprehensive logs + validation → Deploy

Each increment adds value without breaking previous functionality.

### Parallel Team Strategy

With multiple developers after Foundational phase:

1. **Team completes Setup + Foundational together** (Phases 1-2)
2. **Once T016 completes, parallelize**:
   - Developer A: User Story 1 (T017-T033)
   - Developer B: User Story 2 (T034-T049)
   - Developer C: User Story 3 (T050-T064)
   - Developer D: User Story 4 (T065-T082)
3. Stories integrate via shared service interface (already defined in Phase 2)
4. Each developer tests their story independently before integration

---

## Task Summary

- **Total Tasks**: 101 tasks
- **Setup**: 4 tasks
- **Foundational**: 12 tasks (BLOCKING)
- **User Story 1**: 17 tasks (6 tests + 11 implementation)
- **User Story 2**: 16 tasks (6 tests + 10 implementation)
- **User Story 3**: 15 tasks (3 tests + 12 implementation)
- **User Story 4**: 18 tasks (5 tests + 13 implementation)
- **Logging Enhancements**: 6 tasks (optional, can be done anytime)
- **Polish**: 13 tasks

**Parallel Opportunities**: 47 tasks marked [P] can run in parallel within their phase
**Test Coverage**: 20 test tasks ensure 80%+ coverage (SC-007)
**Independent Stories**: All 4 user stories can be tested and deployed independently

---

## Notes

- [P] tasks = different files or independent logic, no sequential dependencies
- [Story] label maps task to specific user story for traceability and incremental delivery
- Each user story independently testable per acceptance scenarios in spec.md
- TDD approach: Write tests first (T017-T022 before T023-T033, etc.)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Logging enhancements (Phase 7) enhance visibility but don't block management service refactoring
- All existing CLI commands maintain backward compatibility (FR-022, FR-023, FR-024)
- Constitution compliance: TDD (Principle V), Documentation updates (Principle VI), Tests pass (Development Workflow)
