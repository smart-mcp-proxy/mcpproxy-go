# Tasks: OAuth Token Refresh Bug Fixes and Logging Improvements

**Input**: Design documents from `/specs/008-oauth-token-refresh/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Tests ARE requested - comprehensive OAuth E2E testing is mandatory per spec.md

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4, US5)
- Include exact file paths in descriptions

## User Story Mapping

| Story | Priority | Title | Key Files |
|-------|----------|-------|-----------|
| US1 | P1 | Automatic Token Refresh on Reconnection | connection.go, persistent_token_store.go |
| US2 | P2 | OAuth Flow Traceability with Correlation IDs | correlation.go (new) |
| US3 | P2 | Enhanced OAuth Debug Logging | logging.go (new), discovery.go |
| US4 | P3 | Coordinated OAuth Flow Execution | coordinator.go (new), connection.go |
| US5 | P1 | Comprehensive OAuth Testing | oauth.spec.ts, test scripts |

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add required dependency and create new file skeletons

- [x] T001 Add google/uuid dependency via `go get github.com/google/uuid`
- [x] T002 [P] Create skeleton file internal/oauth/correlation.go with package declaration
- [x] T003 [P] Create skeleton file internal/oauth/logging.go with package declaration
- [x] T004 [P] Create skeleton file internal/oauth/coordinator.go with package declaration

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before user story implementation

**⚠️ CRITICAL**: Correlation ID infrastructure is needed by multiple stories

- [x] T005 Implement OAuthFlowContext struct in internal/oauth/correlation.go per data-model.md
- [x] T006 Implement OAuthFlowState enum (FlowInitiated, FlowAuthenticating, FlowTokenExchange, FlowCompleted, FlowFailed) in internal/oauth/correlation.go
- [x] T007 Implement NewCorrelationID() using google/uuid in internal/oauth/correlation.go
- [x] T008 Implement context key type and WithCorrelationID/GetCorrelationID functions in internal/oauth/correlation.go
- [x] T009 Implement CorrelationLogger() wrapper function in internal/oauth/correlation.go
- [x] T010 Implement token redaction utility RedactSensitiveData() in internal/oauth/logging.go
- [x] T011 Implement header redaction utility RedactHeaders() in internal/oauth/logging.go

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - Automatic Token Refresh on Reconnection (Priority: P1) 🎯 MVP

**Goal**: Fix Bug 1 - expired tokens should be refreshed automatically using refresh_token without browser re-authentication

**Independent Test**: Configure OAuth test server with 30s TTL, wait for expiration, verify auto-refresh works

### Implementation for User Story 1

- [x] T012 [US1] Update PersistentTokenStore.GetToken() to return token with refresh_token properly populated in internal/oauth/persistent_token_store.go
- [x] T013 [US1] Fix misleading has_existing_token_store log message in internal/upstream/core/connection.go lines 990-994 and 1303-1307
- [x] T014 [US1] Add token refresh retry logic (max 3 attempts with exponential backoff) before triggering new OAuth flow in internal/upstream/core/connection.go
- [x] T015 [US1] Update trySSEOAuthStrategy() to properly check if persisted token has valid refresh_token in internal/upstream/core/connection.go
- [x] T016 [US1] Update tryHTTPOAuthStrategy() to properly check if persisted token has valid refresh_token in internal/upstream/core/connection.go
- [x] T017 [US1] Add explicit refresh token flow handling when access token is expired but refresh token is valid in internal/upstream/core/connection.go
- [x] T018 [US1] Add logging for token refresh attempts and outcomes in internal/oauth/persistent_token_store.go
- [x] T019 [US1] Update managed client to detect token refresh scenarios vs full re-auth in internal/upstream/managed/client.go

**Checkpoint**: Token refresh should now work automatically - US1 is independently testable

---

## Phase 4: User Story 2 - OAuth Flow Traceability with Correlation IDs (Priority: P2)

**Goal**: Add unique correlation_id to all OAuth log entries for traceability

**Independent Test**: Trigger OAuth flow, verify all related logs share same correlation_id

### Implementation for User Story 2

- [ ] T020 [US2] Update CreateOAuthConfig() to accept context with correlation_id in internal/oauth/config.go
- [ ] T021 [US2] Add correlation_id parameter to OAuth callback handler logging in internal/oauth/config.go (handleCallback function)
- [ ] T022 [US2] Update scope discovery functions to use correlation logger in internal/oauth/discovery.go
- [ ] T023 [US2] Add correlation_id to token save/load logging in internal/oauth/persistent_token_store.go
- [x] T024 [US2] Update trySSEOAuthStrategy() to generate and propagate correlation_id in internal/upstream/core/connection.go
- [x] T025 [US2] Update tryHTTPOAuthStrategy() to generate and propagate correlation_id in internal/upstream/core/connection.go
- [ ] T026 [US2] Add correlation_id to handleOAuthAuthorization() logging in internal/upstream/core/connection.go
- [ ] T027 [US2] Add correlation_id to managed client OAuth-related logging in internal/upstream/managed/client.go

**Checkpoint**: All OAuth logs should include correlation_id - US2 is independently testable

---

## Phase 5: User Story 3 - Enhanced OAuth Debug Logging (Priority: P2)

**Goal**: Add comprehensive HTTP request/response logging with token redaction

**Independent Test**: Enable debug logging, trigger OAuth flow, verify HTTP details captured with tokens redacted

### Implementation for User Story 3

- [x] T028 [P] [US3] Implement LogOAuthRequest() with method, URL, redacted headers in internal/oauth/logging.go
- [x] T029 [P] [US3] Implement LogOAuthResponse() with status, redacted headers, timing in internal/oauth/logging.go
- [x] T030 [P] [US3] Implement LogTokenMetadata() with type, expiration, scope (no actual tokens) in internal/oauth/logging.go
- [x] T031 [US3] Add request logging to discovery HTTP calls in internal/oauth/discovery.go
- [x] T032 [US3] Add response logging to discovery HTTP calls in internal/oauth/discovery.go
- [x] T033 [US3] Add timing information to OAuth operations in internal/oauth/config.go
- [x] T034 [US3] Add token metadata logging on token save in internal/oauth/persistent_token_store.go
- [x] T035 [US3] Ensure Authorization header is always redacted (show "Bearer ***") in internal/oauth/logging.go

**Checkpoint**: Debug logs should show comprehensive OAuth details - US3 is independently testable

---

## Phase 6: User Story 4 - Coordinated OAuth Flow Execution (Priority: P3)

**Goal**: Fix Bug 2 & Bug 3 - prevent race conditions with per-server OAuth flow coordination

**Independent Test**: Trigger rapid reconnections, verify single OAuth flow executes per server

### Implementation for User Story 4

- [x] T036 [US4] Implement OAuthFlowCoordinator struct with activeFlows and flowLocks maps in internal/oauth/coordinator.go
- [x] T037 [US4] Implement NewOAuthFlowCoordinator() constructor in internal/oauth/coordinator.go
- [x] T038 [US4] Implement StartFlow(serverName) method with per-server mutex in internal/oauth/coordinator.go
- [x] T039 [US4] Implement EndFlow(serverName, success) method with goroutine notification in internal/oauth/coordinator.go
- [x] T040 [US4] Implement IsFlowActive(serverName) method in internal/oauth/coordinator.go
- [x] T041 [US4] Implement WaitForFlow(serverName, timeout) method with 5-minute timeout in internal/oauth/coordinator.go
- [x] T042 [US4] Add global OAuthFlowCoordinator instance in internal/oauth/coordinator.go
- [x] T043 [US4] Integrate OAuthFlowCoordinator into trySSEOAuthStrategy() in internal/upstream/core/connection.go
- [x] T044 [US4] Integrate OAuthFlowCoordinator into tryHTTPOAuthStrategy() in internal/upstream/core/connection.go
- [x] T045 [US4] Update browser rate limiting to be per-server instead of global in internal/upstream/core/connection.go
- [x] T046 [US4] Remove "clearing stale state and retrying" behavior - wait for active flow instead in internal/upstream/core/connection.go

**Checkpoint**: Only one OAuth flow per server - US4 is independently testable

---

## Phase 7: User Story 5 - Comprehensive OAuth Testing (Priority: P1)

**Goal**: Verify all OAuth functionality with automated E2E tests

**Independent Test**: Run OAuth E2E test suite with various scenarios (short TTL, error injection, etc.)

### Test Implementation for User Story 5

- [x] T047 [P] [US5] Add token refresh test scenario (30s TTL) to e2e/playwright/oauth-advanced.spec.ts
- [x] T048 [P] [US5] Add persisted token loading test scenario to e2e/playwright/oauth-advanced.spec.ts
- [x] T049 [P] [US5] Add correlation ID verification test to e2e/playwright/oauth-advanced.spec.ts
- [x] T050 [P] [US5] Add race condition prevention test (rapid reconnections) to e2e/playwright/oauth-advanced.spec.ts
- [x] T051 [P] [US5] Add error injection test scenario (invalid_grant) to e2e/playwright/oauth-advanced.spec.ts
- [x] T052 [US5] Add Web UI OAuth status verification test using Playwright in e2e/playwright/oauth-advanced.spec.ts
- [x] T053 [US5] Add REST API OAuth status verification test in e2e/playwright/oauth-advanced.spec.ts
- [x] T054 [US5] Update scripts/run-oauth-e2e.sh to include all new test scenarios
- [x] T055 [US5] Add unit tests for OAuthFlowCoordinator in internal/oauth/coordinator_test.go
- [x] T056 [US5] Add unit tests for correlation ID functions in internal/oauth/correlation_test.go
- [x] T057 [US5] Add unit tests for logging utilities in internal/oauth/logging_test.go

**Checkpoint**: All OAuth E2E tests pass - US5 validates all other stories

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Documentation updates and final validation

- [x] T058 [P] Update docs/oauth_mcpproxy_bug.md to mark bugs as fixed with fix details
- [x] T059 [P] Update CLAUDE.md if any architecture patterns changed
- [x] T060 Run ./scripts/run-linter.sh and fix any issues
- [x] T061 Run ./scripts/test-api-e2e.sh and verify all tests pass
- [x] T062 Run ./scripts/run-oauth-e2e.sh with 10 consecutive runs to verify 100% pass rate
- [x] T063 Manually validate quickstart.md scenarios work as documented
- [ ] T064 Verify SC-001: OAuth servers stay connected 24h with 3-min TTL tokens (long-running test)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **US1 (Phase 3)**: Depends on Foundational - highest priority bug fix
- **US2 (Phase 4)**: Depends on Foundational - can run parallel with US1
- **US3 (Phase 5)**: Depends on Foundational - can run parallel with US1, US2
- **US4 (Phase 6)**: Depends on Foundational - can run parallel with US1, US2, US3
- **US5 (Phase 7)**: Depends on US1, US2, US3, US4 completion - validates all fixes
- **Polish (Phase 8)**: Depends on all user stories being complete

### User Story Dependencies

- **US1 (Token Refresh)**: Foundation only - no dependencies on other stories
- **US2 (Correlation IDs)**: Foundation only - enhances logging across all code
- **US3 (Debug Logging)**: Foundation only - builds on US2 for correlation
- **US4 (Flow Coordination)**: Foundation only - works alongside US1, US2, US3
- **US5 (Testing)**: All other stories must be complete to test them

### Parallel Opportunities

**Phase 1 (Setup)** - All tasks can run in parallel:
```
T002, T003, T004 → parallel (different files)
```

**Phase 7 (US5 Testing)** - Test files can be created in parallel:
```
T047, T048, T049, T050, T051 → parallel (different test scenarios)
T055, T056, T057 → parallel (different test files)
```

**Phase 8 (Polish)** - Documentation can run in parallel:
```
T058, T059 → parallel (different files)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational
3. Complete Phase 3: User Story 1 (Token Refresh Bug Fix)
4. **STOP and VALIDATE**: Test token refresh independently with 30s TTL server
5. Deploy/demo if ready - core bug is fixed

### Recommended Implementation Order

Given the P1 priorities and bug criticality:

1. **Foundation + US1** (Token Refresh) - Core bug fix
2. **US4** (Flow Coordination) - Second critical bug fix
3. **US2 + US3** (Logging) - Can run in parallel with US4
4. **US5** (Testing) - Validates all fixes
5. **Polish** - Documentation and final validation

### Parallel Team Strategy

With multiple developers after Foundational completes:

- **Developer A**: US1 (Token Refresh) - Most critical
- **Developer B**: US4 (Flow Coordination) - Second critical
- **Developer C**: US2 + US3 (Logging) - Enhances debugging
- **All**: US5 (Testing) after implementation complete

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing (TDD approach for US5)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Run `./scripts/run-linter.sh` frequently during development
