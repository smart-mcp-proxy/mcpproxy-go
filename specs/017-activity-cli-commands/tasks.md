# Tasks: Activity CLI Commands

**Input**: Design documents from `/specs/017-activity-cli-commands/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included per spec requirements (FR-025 TDD, constitution V).

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

```
cmd/mcpproxy/
├── activity_cmd.go      # Main implementation
├── activity_cmd_test.go # Unit tests
└── main.go              # Register command

internal/
├── cliclient/client.go  # Add activity API methods
└── httpapi/activity.go  # Add summary endpoint (backend extension)

scripts/
└── test-api-e2e.sh      # E2E test updates
```

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and scaffolding for activity commands

- [ ] T001 Create activity command file scaffolding in cmd/mcpproxy/activity_cmd.go
- [ ] T002 Register GetActivityCommand() in cmd/mcpproxy/main.go
- [ ] T003 [P] Add activity API client methods in internal/cliclient/client.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [ ] T004 Implement ActivityFilter struct and validation in cmd/mcpproxy/activity_cmd.go
- [ ] T005 [P] Implement formatRelativeTime() helper in cmd/mcpproxy/activity_cmd.go
- [ ] T006 [P] Implement buildActivityQueryParams() helper in cmd/mcpproxy/activity_cmd.go
- [ ] T007 [P] Implement outputActivityError() with structured error support in cmd/mcpproxy/activity_cmd.go
- [ ] T008 Add ListActivities() method to internal/cliclient/client.go
- [ ] T009 Add GetActivityDetail() method to internal/cliclient/client.go

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - List Recent Activity via CLI (Priority: P1) 🎯 MVP

**Goal**: Users can query activity history with filtering and pagination

**Independent Test**: Run `mcpproxy activity list` after making tool calls and verify formatted output

### Tests for User Story 1

- [ ] T010 [P] [US1] Unit test for activity list command parsing in cmd/mcpproxy/activity_cmd_test.go
- [ ] T011 [P] [US1] Unit test for filter validation in cmd/mcpproxy/activity_cmd_test.go
- [ ] T012 [P] [US1] Add activity list E2E test case in scripts/test-api-e2e.sh

### Implementation for User Story 1

- [ ] T013 [US1] Define activityListCmd cobra.Command with flags in cmd/mcpproxy/activity_cmd.go
- [ ] T014 [US1] Implement runActivityList() function in cmd/mcpproxy/activity_cmd.go
- [ ] T015 [US1] Implement table output formatting for list (headers: ID, TYPE, SERVER, TOOL, STATUS, DURATION, TIME) in cmd/mcpproxy/activity_cmd.go
- [ ] T016 [US1] Implement JSON/YAML output formatting for list in cmd/mcpproxy/activity_cmd.go
- [ ] T017 [US1] Add empty state handling ("No activities found") in cmd/mcpproxy/activity_cmd.go
- [ ] T018 [US1] Add pagination info display for table output in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: `mcpproxy activity list` works with all filter flags and output formats

---

## Phase 4: User Story 2 - Watch Live Activity Stream (Priority: P1)

**Goal**: Real-time `tail -f` style activity streaming via SSE

**Independent Test**: Run `mcpproxy activity watch` in one terminal, make tool calls in another

### Tests for User Story 2

- [ ] T019 [P] [US2] Unit test for SSE event parsing in cmd/mcpproxy/activity_cmd_test.go
- [ ] T020 [P] [US2] Unit test for watch event filtering logic in cmd/mcpproxy/activity_cmd_test.go

### Implementation for User Story 2

- [ ] T021 [US2] Define activityWatchCmd cobra.Command with flags in cmd/mcpproxy/activity_cmd.go
- [ ] T022 [US2] Implement watchActivityStream() SSE client using bufio.Scanner in cmd/mcpproxy/activity_cmd.go
- [ ] T023 [US2] Implement watchWithReconnect() with exponential backoff in cmd/mcpproxy/activity_cmd.go
- [ ] T024 [US2] Implement runActivityWatch() with signal handling (SIGINT/SIGTERM) in cmd/mcpproxy/activity_cmd.go
- [ ] T025 [US2] Implement table streaming output ([HH:MM:SS] server:tool status duration) in cmd/mcpproxy/activity_cmd.go
- [ ] T026 [US2] Implement NDJSON streaming output for watch in cmd/mcpproxy/activity_cmd.go
- [ ] T027 [US2] Add client-side event filtering (type, server) in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: `mcpproxy activity watch` streams live events with auto-reconnect

---

## Phase 5: User Story 3 - View Activity Details (Priority: P2)

**Goal**: View full details of a specific activity record

**Independent Test**: Run `mcpproxy activity show <id>` with ID from list command

### Tests for User Story 3

- [ ] T028 [P] [US3] Unit test for activity ID validation (ULID format) in cmd/mcpproxy/activity_cmd_test.go
- [ ] T029 [P] [US3] Unit test for show output formatting in cmd/mcpproxy/activity_cmd_test.go

### Implementation for User Story 3

- [ ] T030 [US3] Define activityShowCmd cobra.Command with id argument in cmd/mcpproxy/activity_cmd.go
- [ ] T031 [US3] Implement runActivityShow() function in cmd/mcpproxy/activity_cmd.go
- [ ] T032 [US3] Implement detailed table output (key-value pairs with Arguments/Response sections) in cmd/mcpproxy/activity_cmd.go
- [ ] T033 [US3] Implement JSON/YAML output for show in cmd/mcpproxy/activity_cmd.go
- [ ] T034 [US3] Add --include-response flag support in cmd/mcpproxy/activity_cmd.go
- [ ] T035 [US3] Add error handling for not found and invalid ID format in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: `mcpproxy activity show <id>` displays full activity details

---

## Phase 6: User Story 4 - Activity Summary Dashboard (Priority: P3)

**Goal**: Quick overview of activity statistics for a time period

**Independent Test**: Run `mcpproxy activity summary` and verify statistics

### Backend Extension (Required)

- [ ] T036 [US4] Add ActivitySummary struct to internal/contracts/activity.go
- [ ] T037 [US4] Add GetActivitySummary() storage method to internal/storage/activity.go
- [ ] T038 [US4] Add handleActivitySummary() endpoint (GET /api/v1/activity/summary) to internal/httpapi/activity.go
- [ ] T039 [US4] Add GetActivitySummary() client method to internal/cliclient/client.go

### Tests for User Story 4

- [ ] T040 [P] [US4] Unit test for summary period parsing (1h, 24h, 7d, 30d) in cmd/mcpproxy/activity_cmd_test.go
- [ ] T041 [P] [US4] Unit test for summary table formatting in cmd/mcpproxy/activity_cmd_test.go

### Implementation for User Story 4

- [ ] T042 [US4] Define activitySummaryCmd cobra.Command with flags in cmd/mcpproxy/activity_cmd.go
- [ ] T043 [US4] Implement runActivitySummary() function in cmd/mcpproxy/activity_cmd.go
- [ ] T044 [US4] Implement summary table output (metrics, top servers, top tools) in cmd/mcpproxy/activity_cmd.go
- [ ] T045 [US4] Implement JSON/YAML output for summary in cmd/mcpproxy/activity_cmd.go
- [ ] T046 [US4] Add --by flag support for grouping in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: `mcpproxy activity summary` shows usage statistics

---

## Phase 7: User Story 5 - Export Activity for Compliance (Priority: P4)

**Goal**: Export activity logs to files for compliance and auditing

**Independent Test**: Run `mcpproxy activity export --output file.jsonl` and verify file

### Tests for User Story 5

- [ ] T047 [P] [US5] Unit test for export file path validation in cmd/mcpproxy/activity_cmd_test.go
- [ ] T048 [P] [US5] Unit test for export format selection in cmd/mcpproxy/activity_cmd_test.go

### Implementation for User Story 5

- [ ] T049 [US5] Define activityExportCmd cobra.Command with flags in cmd/mcpproxy/activity_cmd.go
- [ ] T050 [US5] Implement runActivityExport() with streaming output in cmd/mcpproxy/activity_cmd.go
- [ ] T051 [US5] Implement file output (--output flag) in cmd/mcpproxy/activity_cmd.go
- [ ] T052 [US5] Implement stdout output for piping in cmd/mcpproxy/activity_cmd.go
- [ ] T053 [US5] Add --include-bodies flag support in cmd/mcpproxy/activity_cmd.go
- [ ] T054 [US5] Add permission error handling for unwritable paths in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: `mcpproxy activity export` creates valid JSON/CSV files

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, cleanup, and cross-story improvements

- [ ] T055 [P] Update docs/cli-management-commands.md with activity commands
- [ ] T056 [P] Update CLAUDE.md with activity command examples
- [ ] T057 [P] Add activity command examples to docs/features/activity-log.md
- [ ] T058 Run quickstart.md validation scenarios
- [ ] T059 Run full E2E test suite (scripts/test-api-e2e.sh)
- [ ] T060 Run linter (scripts/run-linter.sh)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-7)**: All depend on Foundational phase completion
  - US1 (list) and US2 (watch) can run in parallel (both P1)
  - US3 (show) can start after Foundational
  - US4 (summary) requires backend extension first (T036-T039)
  - US5 (export) can start after Foundational
- **Polish (Phase 8)**: Depends on all user stories being complete

### User Story Dependencies

```
Phase 1: Setup
    ↓
Phase 2: Foundational (BLOCKS ALL)
    ↓
    ├── Phase 3: US1 - List (P1) 🎯 MVP
    │       ↓ (parallel with US2)
    ├── Phase 4: US2 - Watch (P1)
    │       ↓
    ├── Phase 5: US3 - Show (P2)
    │       ↓
    ├── Phase 6: US4 - Summary (P3) ← requires backend extension
    │       ↓
    └── Phase 7: US5 - Export (P4)
            ↓
      Phase 8: Polish
```

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Command definition before run function
- Run function before output formatting
- Error handling after happy path

### Parallel Opportunities

- T003 (client methods) can run in parallel with T001-T002
- T005, T006, T007 (helpers) can run in parallel
- T008, T009 (client methods) can run in parallel
- All test tasks marked [P] can run in parallel within their story
- US1 and US2 can run in parallel (both P1 priority)

---

## Parallel Example: User Story 1

```bash
# Launch all tests in parallel:
Task: "T010 [P] [US1] Unit test for activity list command parsing"
Task: "T011 [P] [US1] Unit test for filter validation"
Task: "T012 [P] [US1] Add activity list E2E test case"

# After tests fail (TDD), implement sequentially:
Task: "T013 [US1] Define activityListCmd cobra.Command"
Task: "T014 [US1] Implement runActivityList()"
# ... etc
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL)
3. Complete Phase 3: User Story 1 - List
4. **STOP and VALIDATE**: Test `mcpproxy activity list` independently
5. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. Add US1 (List) → Test independently → MVP!
3. Add US2 (Watch) → Test independently → Real-time monitoring
4. Add US3 (Show) → Test independently → Detail inspection
5. Add US4 (Summary) → Test independently → Dashboard
6. Add US5 (Export) → Test independently → Compliance
7. Polish phase → Documentation complete

### Two-Developer Strategy

With two developers after Foundational:
- Developer A: US1 (List) → US3 (Show) → US5 (Export)
- Developer B: US2 (Watch) → US4 (Summary) → Polish

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story
- Each user story is independently completable and testable
- Verify tests fail before implementing
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- US4 (Summary) requires backend extension - coordinate with spec 016
