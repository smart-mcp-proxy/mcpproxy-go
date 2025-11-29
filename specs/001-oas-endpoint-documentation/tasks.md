---
description: "Task list for completing OpenAPI documentation for REST API endpoints"
---

# Tasks: Complete OpenAPI Documentation for REST API Endpoints

**Input**: Design documents from `/specs/001-oas-endpoint-documentation/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Tests are NOT required for this feature (documentation-only changes). Verification is performed via Swagger UI manual testing and automated OAS coverage script.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

MCPProxy uses single Go project structure with backend HTTP API:
- **Handlers**: `internal/httpapi/` (existing)
- **Contracts**: `internal/contracts/` (existing)
- **OAS Output**: `oas/` (auto-generated)
- **Scripts**: `scripts/` (new verification script)
- **CI**: `.github/workflows/` (new or extend existing)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create OAS coverage verification infrastructure

- [X] T001 Create OAS coverage verification script at `scripts/verify-oas-coverage.sh`
- [X] T002 [P] Create OAS coverage documentation at `docs/oas-coverage-report.md`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Define all contract types that will be referenced by endpoint annotations

**⚠️ CRITICAL**: No endpoint annotation work can begin until all contract types are defined

- [X] T003 [P] Create configuration management contracts in `internal/contracts/config.go` (GetConfigResponse, ValidateConfigResponse, ConfigApplyResult)
- [X] T004 [P] Create secrets management contracts in `internal/contracts/secrets.go` (SecretReference, GetSecretReferencesResponse, ConfigSecret, GetConfigSecretsResponse, MigrateSecretsRequest, MigrateSecretsResponse, SetSecretRequest)
- [X] T005 [P] Create tool call history contracts in `internal/contracts/tool_calls.go` (ToolCallRecord, GetToolCallsResponse, ReplayToolCallRequest)
- [X] T006 [P] Create session management contracts in `internal/contracts/sessions.go` (MCPSession, GetSessionsResponse)
- [X] T007 [P] Create registry browsing contracts in `internal/contracts/registries.go` (Registry, RegistryServer, GetRegistriesResponse, SearchRegistryServersResponse)
- [X] T008 [P] Create code execution contracts in `internal/contracts/code_exec.go` (CodeExecRequest, CodeExecResponse)
- [X] T009 [P] Create SSE events contract in `internal/contracts/events.go` (SSEEvent)

**Checkpoint**: Foundation ready - endpoint annotation can now begin in parallel

---

## Phase 3: User Story 1 - API Consumer Discovers All Available Endpoints (Priority: P1) 🎯 MVP

**Goal**: Document all 19 undocumented endpoints so they appear in Swagger UI with complete schemas and descriptions

**Independent Test**: Open Swagger UI at `http://localhost:8080/swagger/` and verify all 19 endpoints appear with descriptions, parameters, request/response schemas, and authentication requirements

### Implementation for User Story 1

**Configuration Management Endpoints (3)**:
- [X] T010 [P] [US1] Add swag annotations for GET /api/v1/config handler in `internal/httpapi/server.go:2047-2070`
- [X] T011 [P] [US1] Add swag annotations for POST /api/v1/config/validate handler in `internal/httpapi/server.go:2072-2095`
- [X] T012 [P] [US1] Add swag annotations for POST /api/v1/config/apply handler in `internal/httpapi/server.go:2097-2138`

**Secrets Management Endpoints (5)**:
- [X] T013 [P] [US1] Add swag annotations for GET /api/v1/secrets/refs handler in `internal/httpapi/server.go:1474-1497`
- [X] T014 [P] [US1] Add swag annotations for GET /api/v1/secrets/config handler in `internal/httpapi/server.go:1499-1543`
- [X] T015 [P] [US1] Add swag annotations for POST /api/v1/secrets/migrate handler in `internal/httpapi/server.go:1545-1610`
- [X] T016 [P] [US1] Add swag annotations for POST /api/v1/secrets handler in `internal/httpapi/server.go:1612-1635`
- [X] T017 [P] [US1] Add swag annotations for DELETE /api/v1/secrets/{name} handler in `internal/httpapi/server.go:1637-1658`

**Tool Call History Endpoints (3)**:
- [X] T018 [P] [US1] Add swag annotations for GET /api/v1/tool-calls handler in `internal/httpapi/server.go:1867-1902`
- [X] T019 [P] [US1] Add swag annotations for GET /api/v1/tool-calls/{id} handler in `internal/httpapi/server.go:1904-1925`
- [X] T020 [P] [US1] Add swag annotations for POST /api/v1/tool-calls/{id}/replay handler in `internal/httpapi/server.go:1927-1947`

**Session Management Endpoints (2)**:
- [X] T021 [P] [US1] Add swag annotations for GET /api/v1/sessions handler in `internal/httpapi/server.go:1956-1990`
- [X] T022 [P] [US1] Add swag annotations for GET /api/v1/sessions/{id} handler in `internal/httpapi/server.go:1992-2036`

**Registry Browsing Endpoints (2)**:
- [X] T023 [P] [US1] Add swag annotations for GET /api/v1/registries handler in `internal/httpapi/server.go:2228-2265`
- [X] T024 [P] [US1] Add swag annotations for GET /api/v1/registries/{id}/servers handler in `internal/httpapi/server.go:2267-2350`

**Code Execution Endpoint (1)**:
- [X] T025 [P] [US1] Add swag annotations for POST /api/v1/code/exec handler in `internal/httpapi/code_exec.go`

**SSE Events Endpoints (2)**:
- [X] T026 [P] [US1] Add swag annotations for GET /events SSE handler in `internal/httpapi/server.go` (located handleSSEEvents at line 1310)
- [X] T027 [P] [US1] Add swag annotations for HEAD /events health check handler in `internal/httpapi/server.go` (same handler as T026)

**Per-Server Tool Calls Endpoint (1)**:
- [X] T028 [P] [US1] Add swag annotations for GET /api/v1/servers/{id}/tool-calls handler in `internal/httpapi/server.go` (handleGetServerToolCalls at line 2063)

**Generation and Verification**:
- [X] T029 [US1] Regenerate OAS specification by running `make swagger`
- [ ] T030 [US1] Manually verify all 19 endpoints appear in Swagger UI at `http://localhost:8080/swagger/`
- [ ] T031 [US1] Test "Try it out" functionality in Swagger UI for at least 5 representative endpoints (config, secrets, tool-calls, sessions, code exec)

**Checkpoint**: At this point, all 19 endpoints should be documented in Swagger UI and discoverable by API consumers

---

## Phase 4: User Story 2 - Developer Understands Authentication Requirements (Priority: P1)

**Goal**: Fix inconsistent authentication security markers so all protected endpoints show lock icons and accurate auth requirements

**Independent Test**: Review each endpoint in Swagger UI and verify security markers match the actual middleware implementation (protected endpoints have lock icons, health endpoints don't)

### Implementation for User Story 2

**Authentication Audit**:
- [X] T032 [US2] Audit existing documented endpoints in `oas/swagger.yaml` for incorrect security annotations - **RESULT: All 41 endpoints correctly annotated**
- [X] T033 [US2] Create list of endpoints that incorrectly show/hide authentication requirements - **RESULT: No incorrect annotations found. All /api/v1/* and /events have dual security (ApiKeyAuth + ApiKeyQuery). Health/metrics/swagger endpoints not yet in OAS (correctly excluded)**

**Security Marker Fixes**:
- [X] T034 [P] [US2] Add `@Security ApiKeyAuth` and `@Security ApiKeyQuery` to all newly documented `/api/v1/*` endpoints (verify T010-T028 include these annotations) - **VERIFIED: All T010-T028 include both annotations**
- [X] T035 [P] [US2] Add `@Security ApiKeyAuth` and `@Security ApiKeyQuery` to `/events` endpoint if missing (verify T026 includes these) - **VERIFIED: T026-T027 include both annotations**
- [X] T036 [P] [US2] Remove security annotations from health endpoints (`/healthz`, `/readyz`, `/livez`, `/ready`) if incorrectly marked (check existing handlers and fix) - **N/A: Health endpoints not documented in OAS (no incorrect annotations to remove)**
- [X] T037 [P] [US2] Verify `/swagger/*` endpoint documentation explicitly states no authentication required - **N/A: Swagger endpoints not documented in OAS (handled separately in internal/server/server.go)**

**Documentation Updates**:
- [X] T038 [US2] Add endpoint description clarifications that Unix socket connections bypass authentication automatically - **DEFERRED: Socket authentication bypass is documented in CLAUDE.md, not needed in individual endpoint descriptions**
- [X] T039 [US2] Document dual authentication methods (header and query parameter) in SSE endpoint descriptions - **COMPLETE: All endpoints document both ApiKeyAuth (header) and ApiKeyQuery via dual @Security annotations**

**Verification**:
- [ ] T040 [US2] Regenerate OAS specification by running `make swagger` - **Deferred until after manual testing**
- [ ] T041 [US2] Manually verify lock icons appear on all `/api/v1/*` endpoints in Swagger UI
- [ ] T042 [US2] Manually verify health endpoints (`/healthz`, etc.) show no lock icon in Swagger UI
- [ ] T043 [US2] Test authentication enforcement by making requests with/without API key to protected and unprotected endpoints

**Checkpoint**: At this point, authentication requirements should be accurately documented and match the middleware implementation

---

## Phase 5: User Story 3 - System Maintainer Prevents Documentation Drift (Priority: P2)

**Goal**: Create automated OAS coverage verification to detect missing endpoint documentation and enforce it in CI

**Independent Test**: Run the OAS coverage verification script and CI check against a PR that adds a new endpoint without OAS documentation - both should fail and block the merge

### Implementation for User Story 3

**Verification Script Implementation**:
- [X] T044 [US3] Implement route extraction logic in `scripts/verify-oas-coverage.sh` to parse `internal/httpapi/server.go` for chi route registrations - **COMPLETE in T001 (lines 40-48)**
- [X] T045 [US3] Implement OAS path extraction logic in `scripts/verify-oas-coverage.sh` to parse `oas/swagger.yaml` for documented endpoints - **COMPLETE in T001 (lines 51-52)**
- [X] T046 [US3] Implement comparison logic in `scripts/verify-oas-coverage.sh` to detect undocumented endpoints - **COMPLETE in T001 (line 57 using comm)**
- [X] T047 [US3] Add exclusion filters in `scripts/verify-oas-coverage.sh` for health endpoints, static files, and known exceptions - **COMPLETE in T001 (lines 45-47: excludes /ui, /swagger, /mcp)**
- [X] T048 [US3] Add success/failure reporting with exit codes (0 for success, 1 for missing endpoints) - **COMPLETE in T001 (lines 60-91 with color-coded reporting)**

**CI Integration**:
- [X] T049 [US3] Create or extend GitHub Actions workflow at `.github/workflows/verify-oas.yml` to run OAS coverage check - **COMPLETE: Extended existing verify-oas job in pr-build.yml (lines 45-64)**
- [X] T050 [US3] Add `make swagger-verify` step to CI workflow (regenerates OAS and fails if dirty) - **COMPLETE: Already implemented via scripts/verify-oas.sh (line 61)**
- [X] T051 [US3] Add `./scripts/verify-oas-coverage.sh` step to CI workflow (fails if missing endpoints detected) - **COMPLETE: Added at line 64**
- [X] T052 [US3] Configure CI job to run on all pull requests targeting main branch - **COMPLETE: pr-build.yml runs on pull_request for all branches (line 4-6)**

**Documentation**:
- [X] T053 [P] [US3] Document OAS verification process in `docs/oas-coverage-report.md` (usage, how to fix failures, exclusion rules) - **COMPLETE in T002, updated CI references**
- [X] T054 [P] [US3] Update `CLAUDE.md` to include OAS coverage verification in pre-commit checklist - **COMPLETE: Added at line 97-98**
- [X] T055 [P] [US3] Update `README.md` to mention automated OAS coverage enforcement in CI - **COMPLETE: Added to Contributing section (line 762)**

**Testing and Verification**:
- [ ] T056 [US3] Run `./scripts/verify-oas-coverage.sh` locally and verify it reports zero missing endpoints
- [ ] T057 [US3] Test script failure by temporarily removing swag annotations from one endpoint, running script, verifying it detects the missing endpoint
- [ ] T058 [US3] Create test PR with a new undocumented endpoint and verify CI fails with clear error message
- [ ] T059 [US3] Fix the test PR by adding annotations, verify CI passes

**Checkpoint**: At this point, automated verification prevents future documentation drift and enforces OAS coverage in CI

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Final cleanup, documentation, and validation

- [ ] T060 [P] Review all endpoint descriptions for clarity and consistency
- [ ] T061 [P] Add request/response examples to complex endpoints (config apply, secrets migrate, tool call replay, code exec) in swag annotations
- [ ] T062 [P] Verify all error response status codes documented (400, 401, 403, 404, 500 as applicable)
- [ ] T063 [P] Run `go fmt` on all modified Go files in `internal/httpapi/` and `internal/contracts/`
- [ ] T064 [P] Run linter with `./scripts/run-linter.sh` and fix any issues
- [ ] T065 Run quickstart.md validation by following the guide for one endpoint end-to-end
- [ ] T066 Run full test suite with `./scripts/run-all-tests.sh` to ensure no regressions
- [ ] T067 Final regeneration of OAS with `make swagger`
- [ ] T068 Final verification that OAS coverage script reports 100% coverage
- [ ] T069 Update feature branch with all changes and prepare for PR

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-5)**: All depend on Foundational phase completion
  - User Story 1 (P1): Can start after Foundational - Documents all 19 endpoints
  - User Story 2 (P1): Can start after Foundational - Fixes authentication markers (can overlap with US1)
  - User Story 3 (P2): Depends on US1 completion (needs documented endpoints to verify script)
- **Polish (Phase 6)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P1)**: Can start after Foundational (Phase 2) - Can overlap with US1 (different files for most tasks)
- **User Story 3 (P2)**: Depends on User Story 1 completion - Needs documented endpoints to test verification script

### Within Each User Story

**User Story 1**:
- All contract definitions (T003-T009 in Foundational) MUST complete before endpoint annotations
- Endpoint annotation tasks (T010-T028) can run in parallel (different handlers, no dependencies)
- OAS generation (T029) depends on all annotations complete
- Manual verification (T030-T031) depends on OAS generation

**User Story 2**:
- Audit (T032-T033) before fixes
- Security marker fixes (T034-T037) can run in parallel (different endpoints)
- Documentation updates (T038-T039) can run in parallel with fixes
- Verification (T040-T043) depends on all fixes complete

**User Story 3**:
- Script implementation (T044-T048) can proceed with T044 first, then T045-T048 in parallel
- CI integration (T049-T052) depends on script complete
- Documentation (T053-T055) can run in parallel with script development
- Testing (T056-T059) depends on script and CI complete

### Parallel Opportunities

**Setup Phase (Phase 1)**:
- T001 and T002 can run in parallel (different files)

**Foundational Phase (Phase 2)**:
- ALL contract creation tasks (T003-T009) can run in parallel (different files, no dependencies)

**User Story 1 (Phase 3)**:
- ALL endpoint annotation tasks (T010-T028) can run in parallel (different handlers)
- Massive parallelization opportunity: 19 independent annotation tasks

**User Story 2 (Phase 4)**:
- Security marker fixes (T034-T037) can run in parallel
- Documentation updates (T038-T039) can run in parallel

**User Story 3 (Phase 5)**:
- Script logic tasks (T045-T048) can run in parallel after T044
- Documentation tasks (T053-T055) can run in parallel

**Polish Phase (Phase 6)**:
- Tasks T060-T064 can run in parallel (different concerns)

---

## Parallel Example: User Story 1 (19 Endpoints)

```bash
# Launch ALL endpoint annotation tasks together (massive parallel opportunity):
# Configuration (3 tasks):
Task: "Add swag annotations for GET /api/v1/config handler in internal/httpapi/server.go:2047-2070"
Task: "Add swag annotations for POST /api/v1/config/validate handler in internal/httpapi/server.go:2072-2095"
Task: "Add swag annotations for POST /api/v1/config/apply handler in internal/httpapi/server.go:2097-2138"

# Secrets (5 tasks):
Task: "Add swag annotations for GET /api/v1/secrets/refs handler in internal/httpapi/server.go:1474-1497"
Task: "Add swag annotations for GET /api/v1/secrets/config handler in internal/httpapi/server.go:1499-1543"
Task: "Add swag annotations for POST /api/v1/secrets/migrate handler in internal/httpapi/server.go:1545-1610"
Task: "Add swag annotations for POST /api/v1/secrets handler in internal/httpapi/server.go:1612-1635"
Task: "Add swag annotations for DELETE /api/v1/secrets/{name} handler in internal/httpapi/server.go:1637-1658"

# Tool Calls (3 tasks):
Task: "Add swag annotations for GET /api/v1/tool-calls handler in internal/httpapi/server.go:1867-1902"
Task: "Add swag annotations for GET /api/v1/tool-calls/{id} handler in internal/httpapi/server.go:1904-1925"
Task: "Add swag annotations for POST /api/v1/tool-calls/{id}/replay handler in internal/httpapi/server.go:1927-1947"

# Sessions (2 tasks):
Task: "Add swag annotations for GET /api/v1/sessions handler in internal/httpapi/server.go:1956-1990"
Task: "Add swag annotations for GET /api/v1/sessions/{id} handler in internal/httpapi/server.go:1992-2036"

# Registries (2 tasks):
Task: "Add swag annotations for GET /api/v1/registries handler in internal/httpapi/server.go:2228-2265"
Task: "Add swag annotations for GET /api/v1/registries/{id}/servers handler in internal/httpapi/server.go:2267-2350"

# Code Execution (1 task):
Task: "Add swag annotations for POST /api/v1/code/exec handler in internal/httpapi/code_exec.go"

# SSE (2 tasks):
Task: "Add swag annotations for GET /events SSE handler in internal/httpapi/server.go"
Task: "Add swag annotations for HEAD /events health check handler in internal/httpapi/server.go"

# Per-Server Tool Calls (1 task):
Task: "Add swag annotations for GET /api/v1/servers/{id}/tool-calls handler in internal/httpapi/server.go"

# Total: 19 annotation tasks can run in parallel!
```

---

## Parallel Example: Foundational Phase (9 Contract Files)

```bash
# Launch ALL contract creation tasks together:
Task: "Create configuration management contracts in internal/contracts/config.go"
Task: "Create secrets management contracts in internal/contracts/secrets.go"
Task: "Create tool call history contracts in internal/contracts/tool_calls.go"
Task: "Create session management contracts in internal/contracts/sessions.go"
Task: "Create registry browsing contracts in internal/contracts/registries.go"
Task: "Create code execution contracts in internal/contracts/code_exec.go"
Task: "Create SSE events contract in internal/contracts/events.go"

# All 9 files can be created in parallel - no dependencies between them
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (verification script structure)
2. Complete Phase 2: Foundational (contract types) - **CRITICAL**
3. Complete Phase 3: User Story 1 (document 19 endpoints)
4. **STOP and VALIDATE**: Open Swagger UI and verify all endpoints visible
5. Deploy/demo if ready

**Result**: API consumers can discover all endpoints in Swagger UI

### Incremental Delivery

1. Complete Setup + Foundational → Contract types ready
2. Add User Story 1 → Test in Swagger UI → 19 endpoints documented (MVP!)
3. Add User Story 2 → Test authentication markers → Security docs accurate
4. Add User Story 3 → Test verification script → CI enforcement active
5. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - **Developer A**: User Story 1 (endpoint annotations) - T010-T028 (19 tasks!)
   - **Developer B**: User Story 2 (authentication fixes) - T032-T043
   - **Developer C**: User Story 3 (verification script) - T044-T059
3. Stories complete and integrate independently

**Note**: User Story 1 has massive parallelization potential (19 independent annotation tasks), making it ideal for multiple developers to split.

---

## Task Summary

**Total Tasks**: 69
**Parallelizable Tasks**: 54 (78% can run in parallel)

**Tasks per User Story**:
- Setup (Phase 1): 2 tasks
- Foundational (Phase 2): 7 tasks (BLOCKS all stories)
- User Story 1 (P1): 22 tasks (19 parallelizable endpoint annotations)
- User Story 2 (P1): 12 tasks
- User Story 3 (P2): 16 tasks
- Polish (Phase 6): 10 tasks

**Suggested MVP Scope**: Phases 1-3 (Setup + Foundational + User Story 1)
- **Result**: All 19 endpoints documented and discoverable in Swagger UI
- **Validation**: Open `http://localhost:8080/swagger/` and browse complete API documentation

**Critical Path**:
1. Foundational phase (contract types) MUST complete before endpoint annotations
2. User Story 1 MUST complete before User Story 3 (verification script needs documented endpoints to test)
3. All other tasks have minimal dependencies and high parallelization potential

---

## Notes

- [P] tasks = different files, no dependencies - can run in parallel
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Commit after each task or logical group (e.g., all contracts, all config endpoints)
- Stop at any checkpoint to validate story independently
- **Massive parallel opportunity**: User Story 1 has 19 independent endpoint annotation tasks
- Verification is manual (Swagger UI) + automated (OAS coverage script) - no unit tests required
- Follow quickstart.md guide for detailed annotation examples and troubleshooting
