# Tasks: Proactive OAuth Token Refresh & UX Improvements

**Input**: Design documents from `/specs/009-proactive-oauth-refresh/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Tests ARE requested per spec.md (unit tests, E2E tests, Playwright tests).

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/`, `cmd/mcpproxy/` at repository root
- **Frontend**: `frontend/src/`
- **Tests**: `internal/*_test.go`, `tests/`, `scripts/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and shared entities

- [ ] T001 Create OAuthStatus enum type in internal/oauth/status.go
- [ ] T002 [P] Add SSE event types oauth.token_refreshed and oauth.refresh_failed in internal/runtime/events.go
- [ ] T003 [P] Add event emission methods EmitOAuthTokenRefreshed and EmitOAuthRefreshFailed in internal/runtime/event_bus.go
- [ ] T004 [P] Define error types ErrServerNotOAuth and ErrRefreshFailed in internal/oauth/errors.go
- [ ] T005 [P] Add oauth_status and token_expires_at fields to Server struct in internal/contracts/server.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**CRITICAL**: No user story work can begin until this phase is complete

- [ ] T006 Extend Service interface with TriggerOAuthLogout method signature in internal/management/service.go
- [ ] T007 Extend RuntimeOperations interface with TriggerOAuthLogout and RefreshOAuthToken method signatures in internal/management/service.go
- [ ] T008 [P] Add triggerOAuthLogout function to frontend API client in frontend/src/services/api.ts
- [ ] T009 [P] Add triggerOAuthLogout action to servers store in frontend/src/stores/servers.ts
- [ ] T010 [P] Extend ServerResponse TypeScript interface with oauth_status and token_expires_at in frontend/src/types/contracts.ts
- [ ] T011 [P] Add SSE event types to TypeScript types in frontend/src/types/contracts.ts

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - Proactive Token Refresh (Priority: P1)

**Goal**: Automatically refresh OAuth tokens at 80% of their lifetime to prevent tool call failures

**Independent Test**: Configure OAuth server with 30s token lifetime, wait 24s, verify token refreshed automatically

### Tests for User Story 1

- [ ] T012 [P] [US1] Unit test for RefreshManager scheduleRefresh at 80% lifetime in internal/oauth/refresh_manager_test.go
- [ ] T013 [P] [US1] Unit test for RefreshManager retry with exponential backoff in internal/oauth/refresh_manager_test.go
- [ ] T014 [P] [US1] Unit test for RefreshManager stop on max retries in internal/oauth/refresh_manager_test.go
- [ ] T015 [P] [US1] Unit test for RefreshManager coordination with OAuthFlowCoordinator in internal/oauth/refresh_manager_test.go
- [ ] T016 [P] [US1] Unit test for RefreshManager OnTokenSaved and OnTokenCleared hooks in internal/oauth/refresh_manager_test.go

### Implementation for User Story 1

- [ ] T017 [US1] Create RefreshSchedule struct in internal/oauth/refresh_manager.go
- [ ] T018 [US1] Create RefreshManager struct with storage, coordinator, and timer management in internal/oauth/refresh_manager.go
- [ ] T019 [US1] Implement NewRefreshManager constructor in internal/oauth/refresh_manager.go
- [ ] T020 [US1] Implement RefreshManager.Start() to load existing tokens and schedule refreshes in internal/oauth/refresh_manager.go
- [ ] T021 [US1] Implement RefreshManager.Stop() to cancel all timers in internal/oauth/refresh_manager.go
- [ ] T022 [US1] Implement RefreshManager.scheduleRefresh() with 80% lifetime calculation in internal/oauth/refresh_manager.go
- [ ] T023 [US1] Implement RefreshManager.executeRefresh() with coordinator check in internal/oauth/refresh_manager.go
- [ ] T024 [US1] Implement RefreshManager.handleRefreshFailure() with exponential backoff retry in internal/oauth/refresh_manager.go
- [ ] T025 [US1] Implement RefreshManager.OnTokenSaved() hook to reschedule on token update in internal/oauth/refresh_manager.go
- [ ] T026 [US1] Implement RefreshManager.OnTokenCleared() hook to cancel schedule on logout in internal/oauth/refresh_manager.go
- [ ] T027 [US1] Implement Runtime.RefreshOAuthToken() method in internal/runtime/runtime.go
- [ ] T028 [US1] Integrate RefreshManager initialization into Runtime startup in internal/runtime/runtime.go
- [ ] T029 [US1] Integrate RefreshManager shutdown into Runtime cleanup in internal/runtime/runtime.go
- [ ] T030 [US1] Call RefreshManager.OnTokenSaved() from PersistentTokenStore.SaveToken() in internal/oauth/persistent_token_store.go

**Checkpoint**: Proactive token refresh should now work independently - tokens refresh at 80% lifetime

---

## Phase 4: User Story 2 - CLI Logout Command (Priority: P1)

**Goal**: Provide `mcpproxy auth logout --server=<name>` command to clear OAuth credentials

**Independent Test**: Run `mcpproxy auth logout --server=sentry`, verify token cleared and server disconnected

### Tests for User Story 2

- [ ] T031 [P] [US2] Unit test for TriggerOAuthLogout with valid server in internal/management/service_test.go
- [ ] T032 [P] [US2] Unit test for TriggerOAuthLogout with disable_management enabled in internal/management/service_test.go
- [ ] T033 [P] [US2] Unit test for TriggerOAuthLogout with non-OAuth server in internal/management/service_test.go
- [ ] T034 [P] [US2] Unit test for TriggerOAuthLogout with non-existent server in internal/management/service_test.go

### Implementation for User Story 2

- [ ] T035 [US2] Implement Runtime.TriggerOAuthLogout() method in internal/runtime/runtime.go
- [ ] T036 [US2] Implement service.TriggerOAuthLogout() in internal/management/service.go
- [ ] T037 [US2] Add TriggerOAuthLogout method to CLI client in internal/cliclient/client.go
- [ ] T038 [US2] Create authLogoutCmd cobra command in cmd/mcpproxy/auth_cmd.go
- [ ] T039 [US2] Implement runAuthLogout function with daemon socket support in cmd/mcpproxy/auth_cmd.go
- [ ] T040 [US2] Implement runLogoutStandalone function for standalone mode in cmd/mcpproxy/auth_cmd.go
- [ ] T041 [US2] Add --all flag support to logout from all OAuth servers in cmd/mcpproxy/auth_cmd.go
- [ ] T042 [US2] Register authLogoutCmd with authCmd in cmd/mcpproxy/auth_cmd.go

**Checkpoint**: CLI logout command should work independently via daemon or standalone mode

---

## Phase 5: User Story 3 - REST API Logout Endpoint (Priority: P1)

**Goal**: Provide POST /api/v1/servers/{id}/logout endpoint for Web UI and tray

**Independent Test**: POST to /api/v1/servers/sentry/logout, verify 200 OK and token cleared

### Tests for User Story 3

- [ ] T043 [P] [US3] Contract test for logout endpoint 200 response in internal/httpapi/contracts_test.go
- [ ] T044 [P] [US3] Contract test for logout endpoint 400 non-OAuth server in internal/httpapi/contracts_test.go
- [ ] T045 [P] [US3] Contract test for logout endpoint 404 not found in internal/httpapi/contracts_test.go

### Implementation for User Story 3

- [ ] T046 [US3] Add Swagger annotation for logout endpoint in internal/httpapi/server.go
- [ ] T047 [US3] Implement handleServerLogout handler in internal/httpapi/server.go
- [ ] T048 [US3] Register POST /api/v1/servers/{id}/logout route in internal/httpapi/server.go
- [ ] T049 [US3] Update mock controller TriggerOAuthLogout in internal/httpapi/contracts_test.go
- [ ] T050 [US3] Run make swagger to regenerate OpenAPI spec in oas/swagger.yaml

**Checkpoint**: REST logout endpoint should work independently - test with curl

---

## Phase 6: User Story 4 - Web UI Login Button Visibility Fix (Priority: P2)

**Goal**: Show Login button for OAuth servers with expired tokens even when connected

**Independent Test**: Load servers page with connected+expired OAuth server, verify Login button visible

### Tests for User Story 4

- [ ] T051 [P] [US4] Playwright test for Login button visible when connected+expired in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T052 [P] [US4] Playwright test for Login button hidden when connected+valid in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T053 [P] [US4] Playwright test for Login button visible when disconnected in tests/e2e/playwright/oauth-ux.spec.ts

### Implementation for User Story 4

- [ ] T054 [US4] Add oauthExpired computed property to ServerCard.vue in frontend/src/components/ServerCard.vue
- [ ] T055 [US4] Update Login button v-if condition to include oauthExpired in frontend/src/components/ServerCard.vue
- [ ] T056 [US4] Ensure Login button styling is consistent in frontend/src/components/ServerCard.vue

**Checkpoint**: Login button should now appear for connected servers with expired tokens

---

## Phase 7: User Story 5 - Web UI Logout Button (Priority: P2)

**Goal**: Add Logout button for authenticated OAuth servers with confirmation dialog

**Independent Test**: Click Logout button, confirm dialog, verify server disconnected

### Tests for User Story 5

- [ ] T057 [P] [US5] Playwright test for Logout button visible when authenticated in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T058 [P] [US5] Playwright test for Logout confirmation dialog in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T059 [P] [US5] Playwright test for Logout button hidden for non-OAuth server in tests/e2e/playwright/oauth-ux.spec.ts

### Implementation for User Story 5

- [ ] T060 [US5] Add isAuthenticated computed property to ServerCard.vue in frontend/src/components/ServerCard.vue
- [ ] T061 [US5] Add Logout button with v-if="isAuthenticated" in frontend/src/components/ServerCard.vue
- [ ] T062 [US5] Implement handleLogout method with confirmation dialog in frontend/src/components/ServerCard.vue
- [ ] T063 [US5] Connect handleLogout to serversStore.triggerOAuthLogout in frontend/src/components/ServerCard.vue

**Checkpoint**: Logout button should work with confirmation dialog

---

## Phase 8: User Story 6 - Auth Status Badge Display (Priority: P2)

**Goal**: Show "Token Expired" badge for connected servers with authentication issues

**Independent Test**: View servers list with expired token, verify badge appears

### Tests for User Story 6

- [ ] T064 [P] [US6] Playwright test for Token Expired badge visible when expired in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T065 [P] [US6] Playwright test for no badge when authenticated in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T066 [P] [US6] Playwright test for Auth Error badge when oauth_status is error in tests/e2e/playwright/oauth-ux.spec.ts

### Implementation for User Story 6

- [ ] T067 [US6] Add oauthError computed property to ServerCard.vue in frontend/src/components/ServerCard.vue
- [ ] T068 [US6] Add Token Expired badge element with v-if="oauthExpired" in frontend/src/components/ServerCard.vue
- [ ] T069 [US6] Add Auth Error badge element with v-if="oauthError" in frontend/src/components/ServerCard.vue
- [ ] T070 [US6] Style auth status badges with warning/error colors in frontend/src/components/ServerCard.vue

**Checkpoint**: Auth status badges should display correctly

---

## Phase 9: User Story 7 - Token Expiration Display (Priority: P3)

**Goal**: Show token expiration time in UI and CLI for debugging

**Independent Test**: View server details, verify expiration time displayed

### Tests for User Story 7

- [ ] T071 [P] [US7] Unit test for human-readable relative time formatting in internal/oauth/status_test.go
- [ ] T072 [P] [US7] Playwright test for expiration time displayed in server details in tests/e2e/playwright/oauth-ux.spec.ts
- [ ] T073 [P] [US7] Playwright test for EXPIRED indicator when token expired in tests/e2e/playwright/oauth-ux.spec.ts

### Implementation for User Story 7

- [ ] T074 [US7] Implement FormatRelativeTime helper function in internal/oauth/status.go
- [ ] T075 [US7] Update CLI auth status output to include expiration time in cmd/mcpproxy/auth_cmd.go
- [ ] T076 [US7] Add expiration time display to ServerCard details in frontend/src/components/ServerCard.vue
- [ ] T077 [US7] Add warning indicator for tokens expiring within 5 minutes in frontend/src/components/ServerCard.vue
- [ ] T078 [US7] Add EXPIRED text formatting for expired tokens in frontend/src/components/ServerCard.vue

**Checkpoint**: Token expiration should be visible in both UI and CLI

---

## Phase 10: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T079 [P] Create E2E test script scripts/test-oauth-refresh-e2e.sh for proactive refresh validation
- [ ] T080 [P] Create Playwright test file tests/e2e/playwright/oauth-ux.spec.ts with test infrastructure
- [ ] T081 [P] Update CLAUDE.md with new auth logout CLI command documentation
- [ ] T082 [P] Run scripts/verify-oas-coverage.sh to verify OpenAPI coverage
- [ ] T083 Run all tests: go test ./internal/... -v
- [ ] T084 Run API E2E tests: ./scripts/test-api-e2e.sh
- [ ] T085 Run OAuth E2E tests: ./scripts/run-oauth-e2e.sh
- [ ] T086 Run Playwright tests: npx playwright test oauth-ux.spec.ts
- [ ] T087 Code cleanup and linting: golangci-lint run ./...

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phases 3-9)**: All depend on Foundational phase completion
  - US1 (Proactive Refresh): Independent
  - US2 (CLI Logout): Depends on US1 TriggerOAuthLogout for RefreshManager notification
  - US3 (REST Logout): Depends on US2 management service method
  - US4-US6 (Web UI): Can run in parallel after US3
  - US7 (Expiration Display): Independent, low priority
- **Polish (Phase 10)**: Depends on all desired user stories being complete

### User Story Dependencies

```
Phase 2 (Foundational)
         │
         ▼
    ┌────┴────┐
    │         │
    ▼         ▼
   US1  ──► US2  ──► US3
   (P1)     (P1)     (P1)
                       │
         ┌─────────────┼─────────────┐
         ▼             ▼             ▼
        US4           US5           US6
        (P2)          (P2)          (P2)
                                     │
                                     ▼
                                    US7
                                    (P3)
```

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Core structs before methods
- Internal implementation before public API
- Story complete before moving to next priority

### Parallel Opportunities

**Setup Phase**:
- T002, T003, T004, T005 can all run in parallel

**Foundational Phase**:
- T008, T009, T010, T011 (frontend) can run in parallel

**US1 Tests**:
- T012, T013, T014, T015, T016 can all run in parallel

**US2 Tests**:
- T031, T032, T033, T034 can all run in parallel

**US3 Tests**:
- T043, T044, T045 can all run in parallel

**US4/US5/US6 can run in parallel** after US3 completes

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together:
Task: "Unit test for RefreshManager scheduleRefresh at 80% lifetime in internal/oauth/refresh_manager_test.go"
Task: "Unit test for RefreshManager retry with exponential backoff in internal/oauth/refresh_manager_test.go"
Task: "Unit test for RefreshManager stop on max retries in internal/oauth/refresh_manager_test.go"
Task: "Unit test for RefreshManager coordination with OAuthFlowCoordinator in internal/oauth/refresh_manager_test.go"
Task: "Unit test for RefreshManager OnTokenSaved and OnTokenCleared hooks in internal/oauth/refresh_manager_test.go"
```

---

## Implementation Strategy

### MVP First (User Stories 1-3 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1 (Proactive Refresh)
4. Complete Phase 4: User Story 2 (CLI Logout)
5. Complete Phase 5: User Story 3 (REST Logout)
6. **STOP and VALIDATE**: Test proactive refresh and logout independently
7. Deploy/demo if ready - core functionality complete

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test proactive refresh → Core value delivered
3. Add User Story 2-3 → Full logout capability
4. Add User Stories 4-6 → Complete Web UI experience
5. Add User Story 7 → Enhanced debugging capability

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Story 1 (Proactive Refresh)
   - Developer B: User Story 2-3 (Logout functionality)
3. After US3 completes:
   - Developer A: User Story 4 (Login Button Fix)
   - Developer B: User Story 5-6 (Logout Button + Badge)
4. Developer C: User Story 7 (Expiration Display)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Frontend tasks require `npm run build` to validate
- Backend tasks require `go test ./...` to validate
