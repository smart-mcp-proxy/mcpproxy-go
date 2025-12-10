# Tasks: Auto-Detect RFC 8707 Resource Parameter

**Input**: Design documents from `/specs/011-resource-auto-detect/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, quickstart.md

**Tests**: Included per constitution requirement (V. Test-Driven Development)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

- **Go project**: `internal/` for packages, `tests/` for test infrastructure
- Paths follow existing MCPProxy structure per plan.md

---

## Phase 1: Setup

**Purpose**: No new project setup needed - working within existing codebase

- [x] T001 Verify branch `011-resource-auto-detect` is up to date with main

**Checkpoint**: Ready to begin foundational changes

---

## Phase 2: Foundational (Discovery Layer)

**Purpose**: Add `DiscoverProtectedResourceMetadata()` that returns full RFC 9728 metadata struct. This is required by ALL user stories.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Tests for Discovery Layer

- [x] T002 [P] Add unit test `TestDiscoverProtectedResourceMetadata_ReturnsFullStruct` in internal/oauth/discovery_test.go
- [x] T003 [P] Add unit test `TestDiscoverProtectedResourceMetadata_HandlesError` in internal/oauth/discovery_test.go

### Implementation for Discovery Layer

- [x] T004 Add `DiscoverProtectedResourceMetadata()` function in internal/oauth/discovery.go
- [x] T005 Refactor `DiscoverScopesFromProtectedResource()` to delegate to new function in internal/oauth/discovery.go
- [x] T006 Run tests: `go test ./internal/oauth/... -v -run TestDiscoverProtectedResourceMetadata`

**Checkpoint**: Discovery layer ready - `DiscoverProtectedResourceMetadata()` returns full metadata struct including `resource` field

---

## Phase 3: User Story 1 - Zero-Config Runlayer OAuth (Priority: P1) 🎯 MVP

**Goal**: Auto-detect `resource` parameter from Protected Resource Metadata and inject into authorization URL

**Independent Test**: Configure a server with only `name` and `url`, initiate OAuth, verify authorization URL contains `?resource=<detected-value>`

### Tests for User Story 1

- [ ] T007 [P] [US1] Add unit test `TestCreateOAuthConfig_AutoDetectsResource` in internal/oauth/config_test.go
- [ ] T008 [P] [US1] Add unit test `TestCreateOAuthConfig_FallsBackToServerURL` in internal/oauth/config_test.go
- [ ] T009 [P] [US1] Add unit test `TestHandleOAuthAuthorization_InjectsExtraParams` in internal/upstream/core/connection_test.go

### Implementation for User Story 1

- [ ] T010 [US1] Change `CreateOAuthConfig()` signature to return `(*client.OAuthConfig, map[string]string)` in internal/oauth/config.go
- [ ] T011 [US1] Add resource auto-detection logic in `CreateOAuthConfig()` in internal/oauth/config.go
- [ ] T012 [US1] Add fallback to server URL when metadata lacks resource field in internal/oauth/config.go
- [ ] T013 [US1] Build `extraParams` map with auto-detected resource in internal/oauth/config.go
- [ ] T014 [US1] Update `handleOAuthAuthorization()` signature to accept `extraParams` in internal/upstream/core/connection.go
- [ ] T015 [US1] Add URL injection logic after `GetAuthorizationURL()` in internal/upstream/core/connection.go
- [ ] T016 [US1] Update call site in `tryOAuthAuth()` (~line 1108) in internal/upstream/core/connection.go
- [ ] T017 [US1] Update call site in `trySSEOAuthAuth()` (~line 1557) in internal/upstream/core/connection.go
- [ ] T018 [US1] Update call site in `forceHTTPOAuthFlow()` (~line 2436) in internal/upstream/core/connection.go
- [ ] T019 [US1] Update call site in `forceSSEOAuthFlow()` (~line 2495) in internal/upstream/core/connection.go
- [ ] T020 [US1] Add INFO logging for detected resource parameter in internal/oauth/config.go
- [ ] T021 [US1] Run unit tests: `go test ./internal/oauth/... ./internal/upstream/core/... -v`

**Checkpoint**: User Story 1 complete - OAuth flows auto-detect and inject resource parameter into authorization URL

---

## Phase 4: User Story 2 - Manual Override of Auto-Detected Resource (Priority: P2)

**Goal**: Allow `extra_params.resource` in config to override auto-detected value

**Independent Test**: Configure server with both auto-detectable metadata AND manual `extra_params.resource`, verify manual value is used

### Tests for User Story 2

- [ ] T022 [P] [US2] Add unit test `TestCreateOAuthConfig_ManualOverride` in internal/oauth/config_test.go
- [ ] T023 [P] [US2] Add unit test `TestCreateOAuthConfig_MergesExtraParams` in internal/oauth/config_test.go

### Implementation for User Story 2

- [ ] T024 [US2] Add merge logic for manual `extra_params` in `CreateOAuthConfig()` in internal/oauth/config.go
- [ ] T025 [US2] Ensure manual values override auto-detected values in internal/oauth/config.go
- [ ] T026 [US2] Add INFO logging when manual override is applied in internal/oauth/config.go
- [ ] T027 [US2] Run unit tests: `go test ./internal/oauth/... -v -run TestCreateOAuthConfig`

**Checkpoint**: User Story 2 complete - Manual `extra_params` override auto-detected values

---

## Phase 5: User Story 3 - Token Request Resource Injection (Priority: P2)

**Goal**: Include `resource` parameter in token exchange and refresh requests

**Independent Test**: Complete OAuth flow, verify token exchange request body contains `resource` parameter

### Tests for User Story 3

- [ ] T028 [P] [US3] Add E2E test for token exchange with resource parameter in tests/oauthserver/

### Implementation for User Story 3

- [ ] T029 [US3] Verify `OAuthTransportWrapper` injects `resource` into token requests in internal/oauth/transport_wrapper.go
- [ ] T030 [US3] Ensure `extraParams` are passed to transport wrapper in internal/oauth/config.go
- [ ] T031 [US3] Run E2E test: `go test ./tests/oauthserver/... -v`

**Checkpoint**: User Story 3 complete - Resource parameter included in token exchange/refresh

---

## Phase 6: User Story 4 - Diagnostic Visibility (Priority: P3)

**Goal**: Show auto-detected `resource` parameter in diagnostic commands

**Independent Test**: Run `mcpproxy auth status --server=<name>` and verify resource parameter is displayed

### Implementation for User Story 4

- [ ] T032 [US4] Add `resource` field to auth status output in cmd/mcpproxy/auth_cmd.go
- [ ] T033 [US4] Add resource parameter to doctor diagnostics in cmd/mcpproxy/doctor_cmd.go
- [ ] T034 [US4] Test manually: `go build && ./mcpproxy auth status --server=test`

**Checkpoint**: User Story 4 complete - Diagnostic commands show detected resource parameter

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Final validation and documentation

- [ ] T035 [P] Run full test suite: `./scripts/run-all-tests.sh`
- [ ] T036 [P] Run linter: `./scripts/run-linter.sh`
- [ ] T037 Update CLAUDE.md OAuth section if needed
- [ ] T038 Run quickstart.md validation steps manually
- [ ] T039 [P] Add E2E test with mock OAuth server requiring resource in internal/server/e2e_oauth_test.go

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup - BLOCKS all user stories
- **User Stories (Phase 3-6)**: All depend on Foundational phase completion
  - US1 (P1): Can start after Phase 2
  - US2 (P2): Can start after Phase 2 (independent of US1)
  - US3 (P2): Can start after Phase 2 (independent of US1/US2)
  - US4 (P3): Can start after Phase 2 (independent of others)
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Depends only on Foundational (Phase 2)
- **User Story 2 (P2)**: Depends only on Foundational (Phase 2) - builds on US1 implementation but independently testable
- **User Story 3 (P2)**: Depends only on Foundational (Phase 2) - uses existing transport wrapper
- **User Story 4 (P3)**: Depends only on Foundational (Phase 2) - CLI changes independent of OAuth flow

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Core logic before integration
- Story complete before moving to next priority

### Parallel Opportunities

- T002, T003 can run in parallel (different test functions)
- T007, T008, T009 can run in parallel (different test files)
- T022, T023 can run in parallel (same file but different tests)
- T028 can run in parallel with other US3 work
- T035, T036, T039 can run in parallel (different scripts/files)

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together:
Task: "Add unit test TestCreateOAuthConfig_AutoDetectsResource in internal/oauth/config_test.go"
Task: "Add unit test TestCreateOAuthConfig_FallsBackToServerURL in internal/oauth/config_test.go"
Task: "Add unit test TestHandleOAuthAuthorization_InjectsExtraParams in internal/upstream/core/connection_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (trivial)
2. Complete Phase 2: Foundational (discovery layer)
3. Complete Phase 3: User Story 1 (core auto-detection)
4. **STOP and VALIDATE**: Test with real Runlayer server
5. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational → Discovery layer ready
2. Add User Story 1 → Test auto-detection → Deploy (MVP!)
3. Add User Story 2 → Test manual override → Deploy
4. Add User Story 3 → Test token injection → Deploy
5. Add User Story 4 → Test diagnostics → Deploy
6. Each story adds value without breaking previous stories

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Related PR: #188, Related issue: #165
