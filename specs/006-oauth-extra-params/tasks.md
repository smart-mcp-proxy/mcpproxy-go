# Tasks: OAuth Extra Parameters Support

**Input**: Design documents from `/specs/006-oauth-extra-params/`
**Prerequisites**: plan.md, spec.md, data-model.md, contracts/, research.md, quickstart.md

**Tests**: Unit tests are included for all core functionality (validation, wrapper, masking). Integration tests included for OAuth flows.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

Go monorepo structure:
- `internal/`: Core application packages (config, oauth, transport, etc.)
- `cmd/mcpproxy/`: CLI application entry point
- Tests alongside source files: `*_test.go`

---

## Phase 1: Setup (Already Complete ✅)

**Purpose**: Project initialization and dependency upgrades

**Status**: This phase is already complete. The following tasks have been completed:

- ✅ T001 Upgrade mcp-go dependency from v0.42.0 to v0.43.1 in go.mod
- ✅ T002 Add ExtraParams field to OAuthConfig struct in internal/config/config.go
- ✅ T003 Create reserved OAuth parameters validation in internal/config/oauth_validation.go
- ✅ T004 [P] Write unit tests for validation in internal/config/oauth_validation_test.go (19 test cases)
- ✅ T005 Verify all unit tests pass with `go test ./internal/config -v`

---

## Phase 2: Foundational (Core Wrapper Implementation)

**Purpose**: Create OAuth transport wrapper to inject extra params into mcp-go OAuth flows

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### OAuth Transport Wrapper

- [X] T006 [P] Create OAuth parameter masking utilities in internal/oauth/masking.go
- [X] T007 [P] Write unit tests for masking functions in internal/oauth/masking_test.go
- [X] T008 Create OAuthTransportWrapper struct in internal/oauth/transport_wrapper.go
- [X] T009 Implement WrapAuthorizationURL method in internal/oauth/transport_wrapper.go
- [X] T010 Implement WrapTokenRequest method for token exchange in internal/oauth/transport_wrapper.go
- [X] T011 Implement WrapTokenRequest method for token refresh in internal/oauth/transport_wrapper.go
- [X] T012 [P] Write unit tests for WrapAuthorizationURL in internal/oauth/transport_wrapper_test.go
- [X] T013 [P] Write unit tests for WrapTokenRequest (exchange) in internal/oauth/transport_wrapper_test.go
- [X] T014 [P] Write unit tests for WrapTokenRequest (refresh) in internal/oauth/transport_wrapper_test.go

### OAuth Config Integration

- [X] T015 Update CreateOAuthConfig to return extra params in internal/oauth/config.go
- [X] T016 Add DEBUG logging for extra params in CreateOAuthConfig in internal/oauth/config.go
- [X] T017 Update CreateOAuthClient to use wrapper when extra_params present in internal/transport/http.go
- [X] T018 Add DEBUG logging for wrapper instantiation in internal/transport/http.go

### Integration Tests

- [X] T019 Create mock OAuth server requiring resource parameter in internal/server/oauth_extra_params_test.go
- [X] T020 Write integration test for authorization flow with extra params in internal/server/oauth_extra_params_test.go
- [X] T021 Write integration test for token exchange with extra params in internal/server/oauth_extra_params_test.go
- [X] T022 Write integration test for token refresh with extra params in internal/server/oauth_extra_params_test.go
- [X] T023 Write backward compatibility test (OAuth without extra_params) in internal/server/oauth_extra_params_test.go

**Checkpoint**: ✅ Foundation ready - OAuth wrapper can inject extra params into all OAuth flows

---

## Phase 3: User Story 1 - Authenticate with Runlayer MCP Servers (Priority: P1) 🎯 MVP

**Goal**: Enable developers to connect MCPProxy to Runlayer-hosted MCP servers which require RFC 8707 resource indicators in the OAuth flow

**Independent Test**: Configure a single MCP server with OAuth extra_params containing a `resource` parameter, trigger OAuth login flow with `mcpproxy auth login --server=runlayer-slack`, and verify successful authentication with the resource parameter visible in authorization URL

### Implementation for User Story 1

- [X] T024 [US1] Verify extra_params configuration loads correctly from mcp_config.json in existing config loader
- [X] T025 [US1] Verify validation rejects reserved parameter overrides at config load time
- [X] T026 [US1] Test end-to-end OAuth flow with Runlayer configuration from quickstart.md
- [X] T027 [US1] Verify authorization URL includes resource parameter via debug logs
- [X] T028 [US1] Verify token exchange request includes resource parameter via debug logs
- [X] T029 [US1] Verify token refresh request includes resource parameter via debug logs

**Checkpoint**: ✅ Runlayer authentication works end-to-end with RFC 8707 resource parameters

---

## Phase 4: User Story 2 - Configure Extra Parameters via Config File (Priority: P1)

**Goal**: Enable developers to add OAuth extra parameters to their server configuration without modifying application code

**Independent Test**: Add `extra_params` section to a server's OAuth config in `mcp_config.json`, reload configuration, and verify that configuration loads without errors

### Implementation for User Story 2

- [X] T030 [US2] Document extra_params configuration schema in CLAUDE.md with examples
- [X] T031 [US2] Add quickstart example for multiple extra parameters in CLAUDE.md
- [X] T032 [US2] Add quickstart example for multi-tenant OAuth providers in CLAUDE.md
- [X] T033 [US2] Add quickstart example for audience-restricted tokens in CLAUDE.md
- [X] T034 [US2] Test configuration with multiple extra params (resource + audience + tenant)
- [X] T035 [US2] Verify validation error messages for reserved parameter conflicts
- [X] T036 [US2] Test hot configuration reload with extra_params changes

**Checkpoint**: ✅ Users can configure arbitrary extra parameters via JSON without code changes

---

## Phase 5: User Story 3 - Debug OAuth Issues with Clear Diagnostics (Priority: P2)

**Goal**: Enable developers to debug OAuth authentication failures with clear error messages indicating missing parameters and actionable suggestions for fixing configuration

**Independent Test**: Configure a server with OAuth but without required extra_params, run `mcpproxy auth status`, verify that error messages clearly identify the missing parameter and suggest adding it to config

### Implementation for User Story 3

#### Auth Status Enhancements

- [ ] T037 [US3] Extend /api/v1/servers response to include OAuth config details in internal/httpapi/server.go
- [ ] T038 [US3] Update runAuthStatusClientMode to display OAuth configuration section in cmd/mcpproxy/auth_cmd.go
- [ ] T039 [US3] Add extra parameters display (with masking) to auth status output in cmd/mcpproxy/auth_cmd.go
- [ ] T040 [US3] Add scopes, PKCE status, redirect URI display to auth status output in cmd/mcpproxy/auth_cmd.go
- [ ] T041 [US3] Add authorization/token endpoints display to auth status output in cmd/mcpproxy/auth_cmd.go
- [ ] T042 [US3] Add token expiration and last refresh display to auth status output in cmd/mcpproxy/auth_cmd.go

#### Auth Login Enhancements

- [ ] T043 [P] [US3] Update runAuthLoginClientMode to display configuration preview in cmd/mcpproxy/auth_cmd.go
- [ ] T044 [P] [US3] Add pre-browser-open summary (provider, scopes, PKCE, extra_params) in cmd/mcpproxy/auth_cmd.go
- [ ] T045 [P] [US3] Add authorization URL display with all parameters visible in cmd/mcpproxy/auth_cmd.go
- [ ] T046 [P] [US3] Add post-success verification summary in cmd/mcpproxy/auth_cmd.go

#### Debug Logging

- [ ] T047 [P] [US3] Add DEBUG logging for authorization URL construction in internal/oauth/transport_wrapper.go
- [ ] T048 [P] [US3] Add DEBUG logging for token exchange requests in internal/oauth/transport_wrapper.go
- [ ] T049 [P] [US3] Add DEBUG logging for token refresh requests in internal/oauth/transport_wrapper.go
- [ ] T050 [P] [US3] Add DEBUG logging for extra params injection in internal/transport/http.go

#### Doctor Command Enhancements

- [ ] T051 [US3] Add OAuth health check section to doctor command in cmd/mcpproxy/doctor_cmd.go
- [ ] T052 [US3] Implement RFC 8707 compliance detection in doctor diagnostics in cmd/mcpproxy/doctor_cmd.go
- [ ] T053 [US3] Add missing extra_params suggestions to doctor output in cmd/mcpproxy/doctor_cmd.go
- [ ] T054 [US3] Add example config snippets for common OAuth issues in cmd/mcpproxy/doctor_cmd.go

**Checkpoint**: All diagnostics should now provide clear, actionable guidance for OAuth troubleshooting

---

## Phase 6: User Story 4 - Maintain Backward Compatibility (Priority: P2)

**Goal**: Ensure existing users with working OAuth configurations (no extra_params) continue to work without any changes

**Independent Test**: Run existing OAuth integration tests without extra_params configured, verify all tests pass unchanged

### Implementation for User Story 4

- [ ] T055 [US4] Run full OAuth test suite without extra_params configuration
- [ ] T056 [US4] Verify existing OAuth flows work identically to previous versions
- [ ] T057 [US4] Verify empty OAuth object `oauth: {}` works without extra params
- [ ] T058 [US4] Verify nil extra_params and omitted field behave identically
- [ ] T059 [US4] Run regression tests for all existing OAuth integration tests
- [ ] T060 [US4] Verify no performance degradation for OAuth flows without extra_params

**Checkpoint**: All existing OAuth configurations should work without modification

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T061 [P] Update CLAUDE.md with comprehensive OAuth extra_params examples
- [ ] T062 [P] Update quickstart.md with step-by-step Runlayer integration guide
- [ ] T063 [P] Add troubleshooting section to CLAUDE.md for common OAuth issues
- [ ] T064 Run full test suite (unit + integration + E2E) with `./scripts/run-all-tests.sh`
- [ ] T065 Run linter with `./scripts/run-linter.sh` and fix any issues
- [ ] T066 [P] Add code comments explaining wrapper pattern in internal/oauth/transport_wrapper.go
- [ ] T067 [P] Add security documentation for masking strategy in internal/oauth/masking.go
- [ ] T068 Verify quickstart.md 30-second test scenario works end-to-end
- [ ] T069 Performance benchmark: Verify wrapper overhead is <1ms for URL manipulation
- [ ] T070 Performance benchmark: Verify no impact on non-OAuth servers

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: ✅ Already complete - all foundational config/validation work done
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-6)**: All depend on Foundational phase completion
  - User Story 1 (P1): Can start after Foundational - No dependencies on other stories
  - User Story 2 (P1): Can start after Foundational - No dependencies on other stories
  - User Story 3 (P2): Depends on User Story 1 (needs working OAuth to test diagnostics)
  - User Story 4 (P2): Can start after Foundational - No dependencies on other stories
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1) - Runlayer Authentication**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P1) - Configuration**: Can start after Foundational (Phase 2) - Can run in parallel with US1
- **User Story 3 (P2) - Diagnostics**: Depends on US1 completion (needs working OAuth flows to test diagnostics)
- **User Story 4 (P2) - Backward Compatibility**: Can start after Foundational (Phase 2) - Can run in parallel with US1/US2

### Within Each User Story

- Tests before implementation (wrapper tests before wrapper code)
- Core implementation before integration
- Logging/diagnostics after core functionality works
- Story complete before moving to next priority

### Parallel Opportunities

- **Phase 2 Foundational**:
  - T006 (masking.go) || T008-T011 (transport_wrapper.go implementation)
  - T007 (masking_test.go) || T012-T014 (transport_wrapper_test.go)

- **User Stories 1 & 2**: Can run completely in parallel after Foundational phase
  - US1 (T024-T029) || US2 (T030-T036)

- **User Story 3**: Within story, many tasks can parallelize:
  - Auth status tasks (T037-T042) || Auth login tasks (T043-T046) || Debug logging (T047-T050)

- **Phase 7 Polish**:
  - All documentation tasks (T061-T063, T066-T067) can run in parallel
  - Benchmarks (T069-T070) can run in parallel

---

## Parallel Example: Foundational Phase

```bash
# Launch masking utilities and wrapper implementation in parallel:
Task: "Create OAuth parameter masking utilities in internal/oauth/masking.go" (T006)
Task: "Create OAuthTransportWrapper struct in internal/oauth/transport_wrapper.go" (T008)
Task: "Implement WrapAuthorizationURL method in internal/oauth/transport_wrapper.go" (T009)

# Launch all test files in parallel:
Task: "Write unit tests for masking functions in internal/oauth/masking_test.go" (T007)
Task: "Write unit tests for WrapAuthorizationURL in internal/oauth/transport_wrapper_test.go" (T012)
Task: "Write unit tests for WrapTokenRequest (exchange) in internal/oauth/transport_wrapper_test.go" (T013)
Task: "Write unit tests for WrapTokenRequest (refresh) in internal/oauth/transport_wrapper_test.go" (T014)
```

---

## Parallel Example: User Story 3 (Diagnostics)

```bash
# Launch all auth command enhancements in parallel:
Task: "Update runAuthStatusClientMode to display OAuth configuration section in cmd/mcpproxy/auth_cmd.go" (T038)
Task: "Update runAuthLoginClientMode to display configuration preview in cmd/mcpproxy/auth_cmd.go" (T043)

# Launch all debug logging tasks in parallel:
Task: "Add DEBUG logging for authorization URL construction in internal/oauth/transport_wrapper.go" (T047)
Task: "Add DEBUG logging for token exchange requests in internal/oauth/transport_wrapper.go" (T048)
Task: "Add DEBUG logging for token refresh requests in internal/oauth/transport_wrapper.go" (T049)
Task: "Add DEBUG logging for extra params injection in internal/transport/http.go" (T050)
```

---

## Implementation Strategy

### MVP First (User Stories 1 & 2 Only)

1. ✅ Complete Phase 1: Setup (already done)
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1 (Runlayer authentication)
4. Complete Phase 4: User Story 2 (Configuration)
5. **STOP and VALIDATE**: Test US1 + US2 independently with Runlayer
6. Deploy/demo if ready

### Incremental Delivery

1. ✅ Setup complete → Config validation working
2. Add Foundational → Wrapper can inject params into OAuth flows
3. Add User Story 1 → Test Runlayer authentication independently → Deploy/Demo (MVP!)
4. Add User Story 2 → Test configuration flexibility independently → Deploy/Demo
5. Add User Story 3 → Test diagnostics independently → Deploy/Demo
6. Add User Story 4 → Test backward compatibility → Deploy/Demo
7. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Foundational together (Phase 2)
2. Once Foundational is done:
   - Developer A: User Story 1 (Runlayer auth)
   - Developer B: User Story 2 (Configuration)
   - Developer C: User Story 4 (Backward compatibility)
3. After US1 complete:
   - Developer D: User Story 3 (Diagnostics - depends on US1)
4. Stories complete and integrate independently

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests pass before implementing next feature
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Phase 1 already complete (✅) - config validation and go.mod upgrade done
- Phase 2 is the critical path - wrapper implementation blocks all user stories
- User Stories 1 & 2 (both P1) can run in parallel after Foundational phase
- User Story 3 diagnostics depend on User Story 1 working OAuth flows
- User Story 4 backward compatibility can run in parallel with US1/US2
