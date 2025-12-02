# Tasks: OAuth E2E Testing & Observability

**Input**: Design documents from `/specs/007-oauth-e2e-testing/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: This feature IS the test infrastructure. Tests are the primary deliverable.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

Based on plan.md:
- Test server: `tests/oauthserver/`
- E2E scripts: `scripts/`
- Playwright tests: `e2e/playwright/`
- CLI enhancements: `cmd/mcpproxy/`
- Observability: `internal/oauth/`, `internal/management/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure for OAuth test harness

- [x] T001 Create `tests/oauthserver/` directory structure per plan.md
- [x] T002 [P] Add `github.com/golang-jwt/jwt/v5` dependency to go.mod
- [x] T003 [P] Create `tests/oauthserver/options.go` with Options, ErrorMode, DetectionMode types per data-model.md
- [x] T004 [P] Create `tests/oauthserver/types.go` with Client, AuthorizationCode, DeviceCode, TokenResponse types per data-model.md

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core OAuth test server components that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [x] T005 Implement KeyRing struct with AddKey, RotateTo, RemoveKey, GetJWKS, SignToken in `tests/oauthserver/jwks.go`
- [x] T006 Implement JWT token generation (access + refresh) with configurable claims in `tests/oauthserver/jwt.go`
- [x] T007 [P] Implement OAuthTestServer struct with server lifecycle (Start, Shutdown) in `tests/oauthserver/server.go`
- [x] T008 Implement ServerResult struct and Start(t, opts) entry point in `tests/oauthserver/server.go`
- [x] T009 [P] Create login.html template for browser-based auth in `tests/oauthserver/templates/login.html`
- [x] T010 Implement DiscoveryMetadata and `/.well-known/oauth-authorization-server` handler in `tests/oauthserver/discovery.go`
- [x] T011 [P] Implement `/.well-known/openid-configuration` alias in `tests/oauthserver/discovery.go`
- [x] T012 Implement `/jwks.json` endpoint serving public keys in `tests/oauthserver/jwks.go`

**Checkpoint**: Foundation ready - OAuth test server can start and serve discovery endpoints

---

## Phase 3: User Story 1 - Developer Runs OAuth E2E Tests Locally (Priority: P1)

**Goal**: Basic auth code + PKCE flow working with discovery, token exchange, and refresh

**Independent Test**: `go test ./tests/oauthserver/... -run TestAuthCodePKCE` completes auth code flow with PKCE

### Implementation for User Story 1

- [x] T013 [US1] Implement `/authorize` GET handler parsing client_id, redirect_uri, state, PKCE params in `tests/oauthserver/authorize.go`
- [x] T014 [US1] Implement login form rendering at `/authorize` when user interaction needed in `tests/oauthserver/authorize.go`
- [x] T015 [US1] Implement `/authorize` POST handler for login form submission in `tests/oauthserver/authorize.go`
- [x] T016 [US1] Implement authorization code generation and storage in `tests/oauthserver/authorize.go`
- [x] T017 [US1] Implement redirect to callback with code and state in `tests/oauthserver/authorize.go`
- [x] T018 [US1] Implement `/token` endpoint with `grant_type=authorization_code` in `tests/oauthserver/token.go`
- [x] T019 [US1] Implement PKCE verification (S256 code_verifier validation) in `tests/oauthserver/token.go`
- [x] T020 [US1] Implement `/token` endpoint with `grant_type=refresh_token` in `tests/oauthserver/token.go`
- [x] T021 [US1] Implement pre-registered test clients (confidential + public) in `tests/oauthserver/server.go`
- [x] T022 [US1] Write unit tests for auth code + PKCE flow in `tests/oauthserver/server_test.go`
- [x] T023 [US1] Write unit tests for token refresh flow in `tests/oauthserver/server_test.go`

**Checkpoint**: User Story 1 complete - Auth code + PKCE + refresh flows work end-to-end

---

## Phase 4: User Story 2 - Developer Tests OAuth Detection Methods (Priority: P1)

**Goal**: Test server supports all OAuth detection modes (WWW-Authenticate, discovery, explicit)

**Independent Test**: Tests configure harness in different DetectionModes and verify mcpproxy discovers endpoints correctly

### Implementation for User Story 2

- [x] T024 [US2] Implement DetectionMode enum and configuration in `tests/oauthserver/options.go`
- [x] T025 [US2] Implement `/protected` endpoint returning 401 with WWW-Authenticate header in `tests/oauthserver/protected.go`
- [x] T026 [US2] Implement WWW-Authenticate header format per RFC 9728 in `tests/oauthserver/protected.go`
- [x] T027 [US2] Add DetectionMode.Explicit support (disable discovery endpoints) in `tests/oauthserver/server.go`
- [x] T028 [US2] Add DetectionMode.Both support (discovery + WWW-Authenticate) in `tests/oauthserver/server.go`
- [x] T029 [US2] Write unit tests for WWW-Authenticate detection in `tests/oauthserver/server_test.go`
- [x] T030 [US2] Write unit tests for discovery-only mode in `tests/oauthserver/server_test.go`
- [x] T031 [US2] Write integration test verifying mcpproxy discovers OAuth from WWW-Authenticate in `tests/oauthserver/integration_test.go`

**Checkpoint**: User Story 2 complete - All detection methods work

---

## Phase 5: User Story 3 - Developer Tests Browser Login Workflow (Priority: P2)

**Goal**: Playwright tests drive the full browser login experience

**Independent Test**: `npx playwright test oauth-login.spec.ts` fills credentials, approves consent, verifies redirect

### Implementation for User Story 3

- [x] T032 [US3] Create `e2e/playwright/` directory structure
- [x] T033 [P] [US3] Create `e2e/playwright/playwright.config.ts` with headless Chromium config
- [x] T034 [P] [US3] Create `e2e/playwright/package.json` with playwright dependency
- [x] T035 [US3] Enhance login.html template with username, password, consent fields in `tests/oauthserver/templates/login.html`
- [x] T036 [US3] Implement error page rendering for invalid credentials in `tests/oauthserver/authorize.go`
- [x] T037 [US3] Implement consent denial (error=access_denied redirect) in `tests/oauthserver/authorize.go`
- [x] T038 [US3] Write Playwright test for happy path login in `e2e/playwright/oauth-login.spec.ts`
- [x] T039 [US3] Write Playwright test for invalid password in `e2e/playwright/oauth-login.spec.ts`
- [x] T040 [US3] Write Playwright test for consent denial in `e2e/playwright/oauth-login.spec.ts`

**Checkpoint**: User Story 3 complete - Browser login tests pass

---

## Phase 6: User Story 4 - Developer Tests RFC 8707 Resource Indicator (Priority: P2)

**Goal**: Resource indicator flows through auth and into JWT audience claim

**Independent Test**: Tests verify `resource` param in authorize/token and `aud` claim in JWT

### Implementation for User Story 4

- [x] T041 [US4] Parse `resource` parameter in `/authorize` handler in `tests/oauthserver/authorize.go`
- [x] T042 [US4] Store resource indicator with authorization code in `tests/oauthserver/authorize.go`
- [x] T043 [US4] Validate `resource` param on token exchange in `tests/oauthserver/token.go`
- [x] T044 [US4] Include resource as `aud` claim in JWT access token in `tests/oauthserver/jwt.go`
- [x] T045 [US4] Preserve resource on refresh token requests in `tests/oauthserver/token.go`
- [x] T046 [US4] Write unit tests for resource indicator flow in `tests/oauthserver/server_test.go` (TestResourceIndicator)

**Checkpoint**: User Story 4 complete - Resource indicators work

---

## Phase 7: User Story 5 - Developer Tests Dynamic Client Registration (Priority: P2)

**Goal**: DCR endpoint issues credentials, client can perform auth code flow

**Independent Test**: Test registers client via DCR, then completes auth code flow with issued credentials

### Implementation for User Story 5

- [x] T047 [US5] Implement `/registration` POST handler in `tests/oauthserver/dcr.go`
- [x] T048 [US5] Implement client_id and client_secret generation in `tests/oauthserver/dcr.go`
- [x] T049 [US5] Implement redirect_uri and scope validation in `tests/oauthserver/dcr.go`
- [x] T050 [US5] Store registered clients in server state in `tests/oauthserver/dcr.go`
- [x] T051 [US5] Implement DCR error responses (invalid_redirect_uri, etc.) in `tests/oauthserver/dcr.go`
- [x] T052 [US5] Implement RegisterClient() programmatic API in `tests/oauthserver/server.go`
- [x] T053 [US5] Write unit tests for DCR happy path in `tests/oauthserver/server_test.go` (TestDCR_*)
- [x] T054 [US5] Write unit tests for DCR error scenarios in `tests/oauthserver/server_test.go` (TestDCR_*)

**Checkpoint**: User Story 5 complete - DCR works

---

## Phase 8: User Story 6 - Developer Tests Device Code Flow (Priority: P2)

**Goal**: Device code flow works with polling, approval, and denial

**Independent Test**: Test initiates device flow, programmatically approves, polls for token

### Implementation for User Story 6

- [x] T055 [US6] Implement `/device_authorization` POST handler in `tests/oauthserver/device.go`
- [x] T056 [US6] Implement device_code, user_code generation in `tests/oauthserver/device.go`
- [x] T057 [US6] Implement DeviceCode state machine (pending/approved/denied/expired) in `tests/oauthserver/device.go`
- [x] T058 [US6] Implement `/device_verification` GET handler (form display) in `tests/oauthserver/device.go`
- [x] T059 [US6] Implement `/device_verification` POST handler (approve/deny) in `tests/oauthserver/device.go`
- [x] T060 [US6] Implement `/token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code` in `tests/oauthserver/device.go`
- [x] T061 [US6] Return authorization_pending for pending codes in `tests/oauthserver/device.go`
- [x] T062 [US6] Implement ApproveDeviceCode, DenyDeviceCode, ExpireDeviceCode APIs in `tests/oauthserver/device.go`
- [x] T063 [US6] Write unit tests for device code flow in `tests/oauthserver/server_test.go` (TestDeviceCode_*)
- [x] T064 [US6] Write unit tests for device code polling states in `tests/oauthserver/server_test.go` (TestDeviceCode_*)

**Checkpoint**: User Story 6 complete - Device code flow works

---

## Phase 9: User Story 7 - Developer Tests OAuth Error Handling (Priority: P2)

**Goal**: Error injection works, mcpproxy surfaces actionable errors

**Independent Test**: Configure ErrorMode, verify error responses and mcpproxy logging

### Implementation for User Story 7

- [x] T065 [US7] Implement ErrorMode configuration checking in token handlers in `tests/oauthserver/token.go`
- [x] T066 [US7] Implement invalid_client error injection in `tests/oauthserver/token.go`
- [x] T067 [US7] Implement invalid_grant error injection in `tests/oauthserver/token.go`
- [x] T068 [US7] Implement invalid_scope error injection in `tests/oauthserver/token.go`
- [x] T069 [US7] Implement server_error (500) injection in `tests/oauthserver/token.go`
- [x] T070 [US7] Implement slow_response delay injection in `tests/oauthserver/token.go`
- [x] T071 [US7] Implement unsupported_grant_type error injection in `tests/oauthserver/token.go`
- [x] T072 [US7] Implement SetErrorMode() runtime update API in `tests/oauthserver/server.go`
- [x] T073 [US7] Write unit tests for each error type in `tests/oauthserver/server_test.go` (TestErrorInjection_*)
- [ ] T074 [US7] Write integration test verifying mcpproxy error handling in `tests/oauthserver/integration_test.go`

**Checkpoint**: User Story 7 complete - Error injection works

---

## Phase 10: User Story 8 - Developer Tests JWKS Rotation (Priority: P3)

**Goal**: Key rotation works, old tokens rejected after key removal

**Independent Test**: Issue token with kid-1, rotate to kid-2, verify old token fails

### Implementation for User Story 8

- [x] T075 [US8] Implement multiple key support in KeyRing in `tests/oauthserver/jwks.go`
- [x] T076 [US8] Implement RotateKey() that adds new key and makes it active in `tests/oauthserver/jwks.go`
- [x] T077 [US8] Implement RemoveKey() that removes old key from JWKS in `tests/oauthserver/jwks.go`
- [x] T078 [US8] Add kid (key ID) to JWT header in `tests/oauthserver/jwt.go`
- [x] T079 [US8] Write unit tests for JWKS rotation scenarios in `tests/oauthserver/server_test.go` (TestKeyRotation)

**Checkpoint**: User Story 8 complete - JWKS rotation works

---

## Phase 11: User Story 9 - Developer Verifies OAuth Observability (Priority: P2)

**Goal**: Enhanced CLI output, structured logs, doctor checks for OAuth

**Independent Test**: Run OAuth flow, verify `auth status` shows endpoints/expiry, `doctor` shows hints

### Implementation for User Story 9

- [ ] T080 [US9] Enhance `auth login` to print authorization URL preview in `cmd/mcpproxy/auth_cmd.go`
- [ ] T081 [US9] Enhance `auth status` to display endpoints, scopes, expiry, PKCE in `cmd/mcpproxy/auth_cmd.go`
- [ ] T082 [US9] Add secret masking to auth status output in `cmd/mcpproxy/auth_cmd.go`
- [ ] T083 [US9] Add structured logging fields for OAuth operations in `internal/oauth/config.go`
- [ ] T084 [US9] Add structured logging for provider URL, scopes, grant_type in `internal/upstream/core/connection.go`
- [ ] T085 [US9] Implement OAuth health check in doctor command in `internal/management/diagnostics.go`
- [ ] T086 [US9] Add discovery endpoint reachability check to doctor in `internal/management/diagnostics.go`
- [ ] T087 [US9] Add actionable hints for common OAuth errors to doctor in `internal/management/diagnostics.go`
- [ ] T088 [US9] Write tests verifying auth status output format in `cmd/mcpproxy/auth_cmd_test.go`
- [ ] T089 [US9] Write tests verifying doctor OAuth checks in `internal/management/diagnostics_test.go`

**Checkpoint**: User Story 9 complete - Observability enhanced

---

## Phase 12: User Story 10 - Developer Runs OAuth Suite in CI (Priority: P3)

**Goal**: E2E script orchestrates tests, runs in CI within 5 minutes

**Independent Test**: `./scripts/run-oauth-e2e.sh` completes with pass/fail status

### Implementation for User Story 10

- [x] T090 [US10] Create `scripts/run-oauth-e2e.sh` orchestration script
- [x] T091 [US10] Add OAuth test server startup logic to script in `scripts/run-oauth-e2e.sh`
- [x] T092 [US10] Add mcpproxy startup with test config to script in `scripts/run-oauth-e2e.sh`
- [x] T093 [US10] Add Go test invocation to script in `scripts/run-oauth-e2e.sh`
- [x] T094 [US10] Add Playwright test invocation to script in `scripts/run-oauth-e2e.sh`
- [x] T095 [US10] Add cleanup and error handling to script in `scripts/run-oauth-e2e.sh`
- [x] T096 [US10] Add CI workflow step for OAuth E2E tests in `.github/workflows/e2e-tests.yml`
- [ ] T097 [US10] Verify CI execution completes within 5 minute target

**Checkpoint**: User Story 10 complete - CI integration works

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, cleanup, final validation

- [x] T098 [P] Update CLAUDE.md with OAuth test commands in `CLAUDE.md`
- [ ] T099 [P] Create MANUAL_TESTING.md section for OAuth in `docs/MANUAL_TESTING.md`
- [x] T100 [P] Add package documentation to `tests/oauthserver/doc.go`
- [ ] T101 Run full test suite and verify 90% coverage target on OAuth modules
- [ ] T102 Run quickstart.md validation - verify all examples work
- [x] T103 Code cleanup: run linter, fix any issues (0 issues)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-12)**: All depend on Foundational phase completion
  - US1 + US2 are P1 priority - complete first (can be parallel)
  - US3-US7, US9 are P2 priority - complete second (can be parallel)
  - US8, US10 are P3 priority - complete last
- **Polish (Phase 13)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Foundational only - no other story dependencies
- **User Story 2 (P1)**: Foundational only - independent of US1
- **User Story 3 (P2)**: Depends on US1 (auth code flow needed for browser tests)
- **User Story 4 (P2)**: Depends on US1 (builds on auth code flow)
- **User Story 5 (P2)**: Foundational only - DCR independent
- **User Story 6 (P2)**: Foundational + token endpoint from US1
- **User Story 7 (P2)**: Depends on US1 (error injection on existing flows)
- **User Story 8 (P3)**: Foundational only - JWKS independent
- **User Story 9 (P2)**: Depends on US1 (observability for auth flows)
- **User Story 10 (P3)**: Depends on ALL other stories (CI runs everything)

### Within Each User Story

- Implementation tasks before test tasks (this feature IS tests)
- Core handlers before advanced features
- Unit tests before integration tests

### Parallel Opportunities

- T002, T003, T004 can run in parallel (different files)
- T007, T009, T010, T011, T012 can run in parallel after T005, T006
- Within US1: T013-T017 (authorize) can run parallel to T018-T020 (token) after T016
- US1 and US2 can run in parallel (both P1, no dependencies)
- US3, US4, US5, US6, US7, US9 can all run in parallel (after US1)
- US8 can run any time after Foundational
- T098, T099, T100 can run in parallel (documentation)

---

## Parallel Example: Phase 2 Foundational

```bash
# After T005, T006 complete, launch these in parallel:
Task: "Implement OAuthTestServer struct in tests/oauthserver/server.go"
Task: "Create login.html template in tests/oauthserver/templates/login.html"
Task: "Implement DiscoveryMetadata in tests/oauthserver/discovery.go"
Task: "Implement /.well-known/openid-configuration in tests/oauthserver/discovery.go"
Task: "Implement /jwks.json endpoint in tests/oauthserver/jwks.go"
```

## Parallel Example: User Story 1

```bash
# After authorize endpoints complete (T013-T017):
Task: "Implement /token with grant_type=authorization_code in tests/oauthserver/token.go"
Task: "Implement PKCE verification in tests/oauthserver/token.go"
Task: "Implement /token with grant_type=refresh_token in tests/oauthserver/token.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational
3. Complete Phase 3: User Story 1 (Auth Code + PKCE)
4. **STOP and VALIDATE**: Run `go test ./tests/oauthserver/... -run TestAuthCodePKCE`
5. This delivers immediate value - basic OAuth testing works

### Incremental Delivery

1. Setup + Foundational → OAuth test server boots
2. Add User Story 1 → Auth code + PKCE tests work (MVP!)
3. Add User Story 2 → Detection tests work
4. Add User Story 3 → Browser tests work
5. Add User Stories 4-9 → All flows + observability
6. Add User Story 10 → CI integration complete

### Suggested MVP Scope

**Minimum Viable Product**: User Story 1 + User Story 2

This provides:
- Working OAuth test server with discovery
- Auth code + PKCE flow
- Token refresh
- Detection mode testing

Total tasks for MVP: T001-T031 (31 tasks)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story
- Each user story is independently completable and testable
- This feature IS the test infrastructure - tests are primary deliverables
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
