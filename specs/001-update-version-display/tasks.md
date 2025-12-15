# Tasks: Update Check Enhancement & Version Display

**Input**: Design documents from `/specs/001-update-version-display/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Tests ARE REQUIRED per FR-020, FR-021, FR-022 in spec.md. Unit tests and E2E tests must be included.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/`, `cmd/mcpproxy/`
- **Frontend**: `frontend/src/`
- **Documentation**: `docs/`
- **Tests**: `internal/*_test.go`, `scripts/test-api-e2e.sh`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the updatecheck package structure

- [x] T001 Create `internal/updatecheck/` directory structure
- [x] T002 [P] Create `internal/updatecheck/types.go` with VersionInfo, GitHubRelease, Asset structs from data-model.md
- [x] T003 [P] Create `internal/updatecheck/github.go` with GitHub API client (refactor from internal/tray/tray.go:1062-1106)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core update checker service that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [x] T004 Create `internal/updatecheck/checker.go` with Checker struct, New(), Start(), GetVersionInfo() methods
- [x] T005 Implement background ticker (4-hour interval) with context cancellation in `internal/updatecheck/checker.go`
- [x] T006 Implement check() method with semver comparison using golang.org/x/mod/semver in `internal/updatecheck/checker.go`
- [x] T007 Handle MCPPROXY_DISABLE_AUTO_UPDATE env var to disable background checks in `internal/updatecheck/checker.go`
- [x] T008 Handle MCPPROXY_ALLOW_PRERELEASE_UPDATES env var for prerelease comparison in `internal/updatecheck/checker.go`
- [x] T009 Handle "development" version (skip comparison) in `internal/updatecheck/checker.go`
- [x] T010 Integrate UpdateChecker with runtime startup in `internal/runtime/runtime.go`
- [x] T011 Extend handleGetInfo() to include update field in `internal/httpapi/server.go`

**Checkpoint**: Foundation ready - update checker runs, API exposes version info

---

## Phase 3: User Story 1 - Version Always Visible (Priority: P1) 🎯 MVP

**Goal**: Display current version in tray menu, Web Control Panel, and CLI doctor command

**Independent Test**: Open tray menu, Web Control Panel, or run `mcpproxy doctor` and verify version is visible

### Tests for User Story 1

- [ ] T012 [P] [US1] Write unit test for version display in tray in `internal/tray/tray_test.go`
- [ ] T013 [P] [US1] Write unit test for /api/v1/info version field in `internal/httpapi/server_test.go`
- [ ] T014 [P] [US1] Add E2E test for version in /api/v1/info response in `scripts/test-api-e2e.sh`

### Implementation for User Story 1

- [ ] T015 [US1] Add version menu item at top of tray menu in `internal/tray/tray.go` (setupMenu function)
- [ ] T016 [US1] Add UpdateInfo and InfoResponse types to `frontend/src/types/contracts.ts`
- [ ] T017 [US1] Display version in Web Control Panel sidebar/footer in `frontend/src/App.vue` or `frontend/src/components/SidebarNav.vue`
- [ ] T018 [US1] Add version output to doctor command in `cmd/mcpproxy/doctor.go`

**Checkpoint**: Version visible in tray, WebUI, and CLI - MVP complete

---

## Phase 4: User Story 2 - Background Update Detection by Core (Priority: P1)

**Goal**: Core server checks GitHub releases every 4 hours + on startup, exposes via API

**Independent Test**: Query /api/v1/info after startup, verify update field contains version info

### Tests for User Story 2

- [ ] T019 [P] [US2] Write unit test for startup check in `internal/updatecheck/checker_test.go`
- [ ] T020 [P] [US2] Write unit test for periodic check (4hr ticker) in `internal/updatecheck/checker_test.go`
- [ ] T021 [P] [US2] Write unit test for GitHub API success scenario in `internal/updatecheck/github_test.go`
- [ ] T022 [P] [US2] Write unit test for GitHub API failure scenario (graceful degradation) in `internal/updatecheck/github_test.go`
- [ ] T023 [P] [US2] Write unit test for semver comparison in `internal/updatecheck/checker_test.go`
- [ ] T024 [P] [US2] Add E2E test verifying update field in /api/v1/info in `scripts/test-api-e2e.sh`

### Implementation for User Story 2

- [ ] T025 [US2] Implement initial check on startup (non-blocking goroutine) in `internal/updatecheck/checker.go`
- [ ] T026 [US2] Ensure 4-hour periodic check preserves last known state on failure in `internal/updatecheck/checker.go`
- [ ] T027 [US2] Add debug-level logging for GitHub API errors in `internal/updatecheck/github.go`

**Checkpoint**: Background update detection working, API returns update status

---

## Phase 5: User Story 3 - Update Available Menu Item in Tray (Priority: P2)

**Goal**: Show "New version available (vX.Y.Z)" menu item when update detected, remove "Check for Updates..."

**Independent Test**: Run tray with core that has detected update, verify menu item appears

### Tests for User Story 3

- [ ] T028 [P] [US3] Write unit test for update menu item visibility in `internal/tray/tray_test.go`
- [ ] T029 [P] [US3] Write unit test for "Check for Updates..." removal in `internal/tray/tray_test.go`

### Implementation for User Story 3

- [ ] T030 [US3] Remove existing "Check for Updates..." menu item from `internal/tray/tray.go`
- [ ] T031 [US3] Add hidden update menu item that shows when update available in `internal/tray/tray.go`
- [ ] T032 [US3] Poll /api/v1/info periodically in tray to check for updates in `internal/tray/tray.go`
- [ ] T033 [US3] Implement click handler to open GitHub releases URL in `internal/tray/tray.go`
- [ ] T034 [US3] Detect Homebrew installation and show "brew upgrade" message in `internal/tray/tray.go`

**Checkpoint**: Tray shows update notification when available

---

## Phase 6: User Story 4 - Update Notification in Web Control Panel (Priority: P2)

**Goal**: Display dismissible update banner in WebUI when update available

**Independent Test**: Open WebUI connected to core with update available, verify banner appears

### Tests for User Story 4

- [ ] T035 [P] [US4] Write component test for UpdateBanner.vue (if Playwright/Vitest configured)

### Implementation for User Story 4

- [ ] T036 [US4] Create `frontend/src/components/UpdateBanner.vue` with dismissible alert
- [ ] T037 [US4] Integrate UpdateBanner in `frontend/src/App.vue` or layout component
- [ ] T038 [US4] Fetch update info from /api/v1/info on WebUI load
- [ ] T039 [US4] Implement session-based dismiss (sessionStorage)
- [ ] T040 [US4] Add link to GitHub releases page in banner

**Checkpoint**: WebUI shows dismissible update banner

---

## Phase 7: User Story 5 - Update Info in CLI Doctor Command (Priority: P2)

**Goal**: Show version and update status in `mcpproxy doctor` output

**Independent Test**: Run `mcpproxy doctor` when update available, verify output shows update info

### Implementation for User Story 5

- [ ] T041 [US5] Query /api/v1/info from doctor command (if server running) in `cmd/mcpproxy/doctor.go`
- [ ] T042 [US5] Display "Version: vX.Y.Z (latest)" when up-to-date in `cmd/mcpproxy/doctor.go`
- [ ] T043 [US5] Display "Version: vX.Y.Z (update available: vX.Y.Z)" with URL when update available in `cmd/mcpproxy/doctor.go`
- [ ] T044 [US5] Handle case when server not running (show version only) in `cmd/mcpproxy/doctor.go`

**Checkpoint**: CLI doctor shows version and update status

---

## Phase 8: User Story 6 - Download New Version via GitHub Releases (Priority: P3)

**Goal**: Clicking update notification opens GitHub releases page

**Independent Test**: Click update notification/menu item, verify browser opens correct URL

### Implementation for User Story 6

- [ ] T045 [US6] Ensure tray update menu opens correct release URL (already in T033)
- [ ] T046 [US6] Ensure WebUI banner link opens correct release URL (already in T040)
- [ ] T047 [US6] Ensure doctor output includes correct release URL (already in T043)

**Checkpoint**: All interfaces link to correct GitHub releases page

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, OpenAPI spec, and final validation

- [ ] T048 [P] Create `docs/features/version-updates.md` with user-facing documentation
- [ ] T049 [P] Update `oas/swagger.yaml` with /api/v1/info response extension (update field)
- [ ] T050 [P] Update `AUTOUPDATE.md` to reference new centralized approach
- [ ] T051 Run `./scripts/run-linter.sh` and fix any issues
- [ ] T052 Run `go test ./internal/updatecheck/... -v` to verify all unit tests pass
- [ ] T053 Run `./scripts/test-api-e2e.sh` to verify E2E tests pass
- [ ] T054 Manual test: Verify tray menu shows version on macOS
- [ ] T055 Manual test: Verify tray menu shows version on Windows
- [ ] T056 Manual test: Verify WebUI shows version and update banner
- [ ] T057 Manual test: Verify `mcpproxy doctor` shows version
- [ ] T058 Run quickstart.md validation checklist

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-8)**: All depend on Foundational phase completion
  - US1 and US2 are both P1 priority and form the MVP
  - US3, US4, US5 are P2 and can proceed in parallel after US1/US2
  - US6 is P3 and depends on US3/US4/US5 implementation
- **Polish (Phase 9)**: Depends on all desired user stories being complete

### User Story Dependencies

| Story | Priority | Dependencies | Can Start After |
|-------|----------|--------------|-----------------|
| US1 - Version Visible | P1 | Foundational | Phase 2 |
| US2 - Background Detection | P1 | Foundational | Phase 2 |
| US3 - Tray Update Menu | P2 | US2 (needs API) | Phase 4 |
| US4 - WebUI Banner | P2 | US2 (needs API) | Phase 4 |
| US5 - CLI Doctor | P2 | US2 (needs API) | Phase 4 |
| US6 - GitHub Releases | P3 | US3, US4, US5 | Phase 7 |

### Parallel Opportunities

**Within Setup (Phase 1)**:
```
T002 (types.go) || T003 (github.go)
```

**Within User Story 1**:
```
T012 (tray test) || T013 (api test) || T014 (e2e test)
T016 (frontend types) || T017 (webui version)
```

**Within User Story 2**:
```
T019 || T020 || T021 || T022 || T023 || T024 (all test tasks)
```

**Across User Stories (after Phase 4)**:
```
US3 (tray) || US4 (webui) || US5 (cli) - can run in parallel
```

---

## Parallel Example: User Story 2 Tests

```bash
# Launch all US2 tests in parallel:
Task: "Write unit test for startup check in internal/updatecheck/checker_test.go"
Task: "Write unit test for periodic check in internal/updatecheck/checker_test.go"
Task: "Write unit test for GitHub API success in internal/updatecheck/github_test.go"
Task: "Write unit test for GitHub API failure in internal/updatecheck/github_test.go"
Task: "Write unit test for semver comparison in internal/updatecheck/checker_test.go"
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T011)
3. Complete Phase 3: User Story 1 (T012-T018)
4. Complete Phase 4: User Story 2 (T019-T027)
5. **STOP and VALIDATE**: Test version display + background detection
6. Deploy/demo if ready - users can see version everywhere

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. Add US1 + US2 → MVP: Version visible + update detection working
3. Add US3 → Tray shows update notifications
4. Add US4 → WebUI shows update notifications
5. Add US5 → CLI shows update notifications
6. Add US6 → All links work correctly
7. Polish → Documentation + final validation

---

## Summary

| Metric | Count |
|--------|-------|
| **Total Tasks** | 58 |
| **Phase 1 (Setup)** | 3 |
| **Phase 2 (Foundational)** | 8 |
| **Phase 3 (US1 - Version Visible)** | 7 |
| **Phase 4 (US2 - Background Detection)** | 9 |
| **Phase 5 (US3 - Tray Update Menu)** | 7 |
| **Phase 6 (US4 - WebUI Banner)** | 6 |
| **Phase 7 (US5 - CLI Doctor)** | 4 |
| **Phase 8 (US6 - GitHub Releases)** | 3 |
| **Phase 9 (Polish)** | 11 |
| **Parallel Opportunities** | 25 tasks marked [P] |

### Suggested MVP Scope

**MVP = US1 + US2** (Phases 1-4, Tasks T001-T027)
- Version visible in tray, WebUI, CLI
- Background update detection working
- API exposes update information

This provides core value and validates the architecture before adding notification UIs.

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Tests are required per FR-020, FR-021, FR-022 - write tests before implementation
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- The existing `checkForUpdates()` code in `internal/tray/tray.go` should be refactored to use the new centralized checker
