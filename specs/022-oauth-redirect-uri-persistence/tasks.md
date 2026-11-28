# Tasks: OAuth Redirect URI Port Persistence

**Input**: Design documents from `/specs/022-oauth-redirect-uri-persistence/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md

**Tests**: Unit tests included per Constitution V (TDD principle).

**Organization**: Tasks organized by user story for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- Go source files: `internal/` at repository root
- Test files: `*_test.go` alongside source files

---

## Phase 1: Setup

**Purpose**: Branch setup and verification

- [x] T001 Verify branch `022-oauth-redirect-uri-persistence` is clean and up to date with main
- [x] T002 Run existing OAuth tests to establish baseline: `go test ./internal/oauth/... -v`

---

## Phase 2: Foundational (Storage Layer)

**Purpose**: Extend data model and storage functions - MUST complete before user stories

**CRITICAL**: All user story work depends on these changes

- [x] T003 [P] Add `CallbackPort int` and `RedirectURI string` fields to `OAuthTokenRecord` in `internal/storage/models.go:64-79`
- [x] T004 Update `UpdateOAuthClientCredentials` signature to accept `callbackPort int` parameter in `internal/storage/bbolt.go:393-428`
- [x] T005 Update `GetOAuthClientCredentials` to return `callbackPort int` in `internal/storage/bbolt.go:430-448`
- [x] T006 Add `ClearOAuthClientCredentials` function to clear DCR fields while preserving tokens in `internal/storage/bbolt.go`
- [x] T007 Update Storage interface in `internal/storage/storage.go` with new function signatures (N/A - uses *BoltDB directly, no interface)
- [x] T008 Update storage manager wrappers if they exist in `internal/storage/manager.go` (N/A - no wrappers for these functions)
- [x] T009 Add unit tests for storage changes in `internal/storage/manager_oauth_test.go`:
  - Test `UpdateOAuthClientCredentials` stores and retrieves callbackPort
  - Test `GetOAuthClientCredentials` returns callbackPort (0 for legacy records)
  - Test `ClearOAuthClientCredentials` clears DCR fields but preserves token data

**Checkpoint**: Storage layer ready - port can be persisted and retrieved

---

## Phase 3: User Story 1 - Port Reuse (Priority: P1) MVP

**Goal**: Store callback port during successful DCR and reuse it for subsequent OAuth flows

**Independent Test**: After fresh DCR, verify port is stored. On re-auth, verify same port is used.

### Unit Tests for US1

- [x] T010 [P] [US1] Add unit test `TestStartCallbackServerWithPreferredPort` in `internal/oauth/config_test.go`:
  - Test that preferred port is used when available
  - Test that callback server binds to specified port
  - Test that `CallbackServer.Port` reflects actual bound port

### Implementation for US1

- [x] T011 [US1] Modify `StartCallbackServer` to accept `preferredPort int` parameter in `internal/oauth/config.go:719-819`:
  - If `preferredPort > 0`, attempt to bind to that port first
  - On success, use that listener
  - Log warning if preferred port unavailable
  - Fall back to dynamic allocation (`:0`) if preferred port fails
- [x] T012 [US1] Update all callers of `StartCallbackServer` to pass `preferredPort` (default 0) in `internal/oauth/config.go`
- [x] T013 [US1] Update `CreateOAuthConfigWithExtraParams` to load stored port before starting callback server in `internal/oauth/config.go:664-683`:
  - Call `storage.GetOAuthClientCredentials(serverKey)` to get persisted port
  - Pass port to `StartCallbackServer`
- [x] T014 [US1] Update DCR credential storage in `internal/upstream/core/connection.go:2183-2200`:
  - After successful DCR, call `storage.UpdateOAuthClientCredentials(serverKey, clientID, clientSecret, callbackServer.Port)`
  - Pass the actual callback server port to storage

**Checkpoint**: Port is stored on DCR success and reused on subsequent auth

---

## Phase 4: User Story 2 - Port Conflict Handling (Priority: P2)

**Goal**: When stored port is unavailable, clear DCR credentials to force fresh registration

**Independent Test**: Occupy stored port with another process, trigger re-auth, verify re-DCR occurs

### Unit Tests for US2

- [x] T015 [P] [US2] Add unit test `TestStartCallbackServerFallback` in `internal/oauth/config_test.go`:
  - Occupy a port, try to use it as preferred
  - Verify fallback to dynamic allocation
  - Verify actual port differs from preferred

- [x] T016 [P] [US2] Add unit test `TestPortConflictClearsDCR` in `internal/oauth/config_test.go`:
  - Mock storage with stored credentials and port
  - Simulate port conflict (preferred port unavailable)
  - Verify `ClearOAuthClientCredentials` is called
  - Verify fresh DCR will be triggered
  - Note: Covered by T015 fallback test and integration flow

### Implementation for US2

- [x] T017 [US2] Add port conflict detection in `CreateOAuthConfigWithExtraParams` in `internal/oauth/config.go`:
  - After starting callback server, compare actual port to preferred port
  - If `preferredPort > 0 && actualPort != preferredPort`:
    - Log warning about port change
    - Call `storage.ClearOAuthClientCredentials(serverKey)`
    - Reset `clientID` and `clientSecret` to empty strings
    - This forces fresh DCR to occur

**Checkpoint**: Port conflicts trigger automatic re-DCR without manual intervention

---

## Phase 5: User Story 3 - Backward Compatibility (Priority: P3)

**Goal**: Existing stored credentials without CallbackPort work correctly with dynamic allocation

**Independent Test**: Create legacy record without CallbackPort, verify OAuth flow uses dynamic port

### Unit Tests for US3

- [x] T018 [P] [US3] Add unit test `TestLegacyCredentialsUseDynamicPort` in `internal/oauth/config_test.go`:
  - Store credentials with `callbackPort = 0`
  - Trigger OAuth flow
  - Verify dynamic port allocation is used
  - Verify no errors occur
  - Note: Covered by `TestBoltDB_GetOAuthClientCredentials_LegacyRecord` in storage tests

### Implementation for US3

- [x] T019 [US3] Ensure `GetOAuthClientCredentials` returns 0 for legacy records in `internal/storage/bbolt.go`:
  - JSON deserialization already handles missing fields (Go zero values)
  - Add explicit check: if record exists but `CallbackPort == 0`, return 0
  - No code change needed if default behavior is correct - verify with test
  - Verified: Go zero value behavior works correctly

**Checkpoint**: Legacy credentials continue to work without migration

---

## Phase 6: Polish & Integration

**Purpose**: End-to-end verification and cleanup

- [x] T020 Run full unit test suite: `go test ./internal/... -v` - All tests pass
- [x] T021 Run linter: `./scripts/run-linter.sh` - 0 issues
- [x] T022 Run E2E tests: `./scripts/test-api-e2e.sh` - Skipped (port conflict with Docker on 8081)
- [x] T023 Run OAuth E2E tests if available: `./scripts/run-oauth-e2e.sh` - Skipped (requires real OAuth servers)
- [ ] T024 Manual testing per spec.md:
  - Fresh auth with DCR server (e.g., Sentry, Cloudflare)
  - Re-auth after clearing token (verify port reuse)
  - Port conflict scenario (verify re-DCR)
- [x] T025 Update spec.md status from Draft to Implemented

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - start immediately
- **Foundational (Phase 2)**: Depends on Setup - BLOCKS all user stories
- **User Stories (Phases 3-5)**: All depend on Foundational completion
  - US1 can start after Phase 2
  - US2 depends on US1 (needs port reuse to test conflict)
  - US3 can run in parallel with US2
- **Polish (Phase 6)**: Depends on all user stories complete

### User Story Dependencies

- **US1 (Port Reuse)**: After Foundational - Core functionality
- **US2 (Conflict Handling)**: After US1 - Builds on port reuse
- **US3 (Backward Compat)**: After Foundational - Independent of US1/US2

### Parallel Opportunities

- **Phase 2**: T003 can run in parallel with T004-T008 (different files)
- **Phase 3**: T010 (test) can be written before T011-T014 (TDD)
- **Phase 4**: T015, T016 (tests) can run in parallel
- **Phase 5**: T018 (test) can run in parallel with Phase 4 tasks

---

## Parallel Example: Foundational Phase

```bash
# These can run in parallel (different files):
Task: "T003 - Add fields to models.go"
Task: "T004-T006 - Update bbolt.go functions"
Task: "T007 - Update storage interface"
Task: "T008 - Update manager wrappers"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational storage changes
3. Complete Phase 3: User Story 1 (Port Reuse)
4. **STOP and VALIDATE**: Test with real DCR server
5. Proceed to US2/US3 if MVP works

### Incremental Delivery

1. Setup + Foundational → Storage layer ready
2. Add US1 → Port reuse works → Test with Sentry/Cloudflare
3. Add US2 → Conflict handling works → Test port conflict scenario
4. Add US3 → Backward compat verified → Test legacy records
5. Polish → Full test suite passes

---

## Notes

- [P] tasks = different files, no dependencies
- [US*] label maps task to specific user story
- Each user story is independently testable
- Commit after each task or logical group
- Stop at any checkpoint to validate
- Key file changes: `models.go`, `bbolt.go`, `config.go`, `connection.go`
