# Tasks: Status Command

**Input**: Design documents from `/specs/027-status-command/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Included per TDD constitution principle (V).

**Organization**: Tasks grouped by user story for independent implementation.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story (US1, US2, US3, US4)
- Exact file paths included

## Phase 1: Setup

**Purpose**: Register command and add client method

- [x] T001 Add `GetStatus()` method to `cliclient.Client` in `internal/cliclient/client.go` - call `/api/v1/status`, return `map[string]interface{}`
- [x] T002 Create `cmd/mcpproxy/status_cmd.go` with Cobra command skeleton: `statusCmd` with `--show-key`, `--web-url`, `--reset-key` flags, register in `cmd/mcpproxy/main.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core status data collection logic shared by all user stories

- [x] T003 Implement `StatusInfo` struct and `collectStatusFromDaemon()` in `cmd/mcpproxy/status_cmd.go` - detect socket via `socket.IsSocketAvailable()`, call `cliclient.GetStatus()` and `cliclient.GetInfo()`, merge into StatusInfo
- [x] T004 Implement `collectStatusFromConfig()` in `cmd/mcpproxy/status_cmd.go` - load config via `config.Load()`, populate StatusInfo with configured listen address, API key, constructed Web UI URL, config path
- [x] T005 Implement `maskAPIKey()` in `cmd/mcpproxy/status_cmd.go` - first4+****+last4 masking (reuse logic from `main.go:81-87`)
- [x] T006 Implement `buildWebUIURL()` helper in `cmd/mcpproxy/status_cmd.go` - construct `http://{addr}/ui/?apikey={key}`, handle `:port` prefix case

**Checkpoint**: Core data collection ready - user story implementation can begin

---

## Phase 3: User Story 1 - Quick Status Check (Priority: P1) MVP

**Goal**: `mcpproxy status` displays proxy state with masked API key and Web UI URL in both daemon and config-only modes

**Independent Test**: Run `mcpproxy status` with and without daemon, verify all fields present

### Tests for User Story 1

- [x] T007 [P] [US1] Unit test `TestCollectStatusFromConfig` in `cmd/mcpproxy/status_cmd_test.go` - verify config-only mode populates StatusInfo correctly with "Not running" state, masked key, config path
- [x] T008 [P] [US1] Unit test `TestMaskAPIKey` in `cmd/mcpproxy/status_cmd_test.go` - verify masking: normal key (first4+****+last4), short key (****), empty key
- [x] T009 [P] [US1] Unit test `TestBuildWebUIURL` in `cmd/mcpproxy/status_cmd_test.go` - verify URL construction: normal addr, `:port` prefix, with API key
- [x] T010 [P] [US1] Unit test `TestFormatStatusTable` in `cmd/mcpproxy/status_cmd_test.go` - verify table output contains all expected fields for both running and not-running states

### Implementation for User Story 1

- [x] T011 [US1] Implement `formatStatusTable()` in `cmd/mcpproxy/status_cmd.go` - render StatusInfo as aligned key-value table output (State, Listen, Uptime, API Key, Web UI, Servers, Socket/Config)
- [x] T012 [US1] Implement `formatStatusJSON()` in `cmd/mcpproxy/status_cmd.go` - render StatusInfo as JSON matching `contracts/status-response.json` schema, support YAML via standard formatter
- [x] T013 [US1] Wire up `runStatus()` in `cmd/mcpproxy/status_cmd.go` - detect daemon mode, collect status, format with resolved output format, print to stdout

**Checkpoint**: `mcpproxy status` works in both modes with masked key

---

## Phase 4: User Story 2 - Copy API Key for Scripting (Priority: P1)

**Goal**: `--show-key` flag reveals full unmasked API key

**Independent Test**: Run `mcpproxy status --show-key` and verify full 64-char key in output

### Tests for User Story 2

- [x] T014 [P] [US2] Unit test `TestShowKeyFlag` in `cmd/mcpproxy/status_cmd_test.go` - verify --show-key produces full unmasked key in both table and JSON output

### Implementation for User Story 2

- [x] T015 [US2] Add `--show-key` logic to `runStatus()` in `cmd/mcpproxy/status_cmd.go` - when flag set, skip masking and include full API key in StatusInfo

**Checkpoint**: `mcpproxy status --show-key` reveals full key

---

## Phase 5: User Story 3 - Open Web UI Quickly (Priority: P2)

**Goal**: `--web-url` outputs only the URL for piping

**Independent Test**: Run `mcpproxy status --web-url` and verify output is URL-only (no labels)

### Tests for User Story 3

- [x] T016 [P] [US3] Unit test `TestWebURLFlag` in `cmd/mcpproxy/status_cmd_test.go` - verify --web-url outputs only URL string with no formatting, labels, or extra newlines

### Implementation for User Story 3

- [x] T017 [US3] Add `--web-url` early-return logic to `runStatus()` in `cmd/mcpproxy/status_cmd.go` - when flag set, print only the Web UI URL and exit (bypass all other formatting)

**Checkpoint**: `open $(mcpproxy status --web-url)` opens Web UI

---

## Phase 6: User Story 4 - Reset Compromised API Key (Priority: P3)

**Goal**: `--reset-key` generates new key, saves to config, warns about HTTP clients

**Independent Test**: Run `mcpproxy status --reset-key`, verify new key saved and warning displayed

### Tests for User Story 4

- [x] T018 [P] [US4] Unit test `TestResetKey` in `cmd/mcpproxy/status_cmd_test.go` - verify new key is generated (different from old), config file updated, warning message contains "HTTP clients"
- [x] T019 [P] [US4] Unit test `TestResetKeyWithEnvVar` in `cmd/mcpproxy/status_cmd_test.go` - verify env var override warning is shown when `MCPPROXY_API_KEY` is set

### Implementation for User Story 4

- [x] T020 [US4] Implement `resetAPIKey()` in `cmd/mcpproxy/status_cmd.go` - generate new key via `crypto/rand`, save via `config.SaveConfig()`, return new key
- [x] T021 [US4] Add `--reset-key` logic to `runStatus()` in `cmd/mcpproxy/status_cmd.go` - call resetAPIKey(), print warning about HTTP client disconnection, check for env var override, display new key in full (implicit --show-key), then show updated status

**Checkpoint**: API key rotation works with proper warnings

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Documentation and integration

- [x] T022 [P] Create Docusaurus documentation in `docs/cli/status-command.md` - follow pattern from `docs/cli/activity-commands.md`: frontmatter, overview, usage, flags table, examples (daemon/config modes), output examples (table/JSON), edge cases, exit codes
- [x] T023 [P] Register status-command in `website/sidebars.js` under CLI category
- [x] T024 Run `go test ./cmd/mcpproxy/ -run TestStatus -v -race` and `./scripts/run-linter.sh` to verify all tests pass and code is lint-clean
- [x] T025 Run quickstart.md validation: build binary, test all flag combinations in both daemon and config-only modes

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 (T001, T002)
- **US1 (Phase 3)**: Depends on Phase 2 (T003-T006)
- **US2 (Phase 4)**: Depends on Phase 3 (builds on status output)
- **US3 (Phase 5)**: Depends on Phase 2 only (URL construction is foundational)
- **US4 (Phase 6)**: Depends on Phase 2 only (reset is independent of display)
- **Polish (Phase 7)**: Depends on all user stories complete

### User Story Dependencies

- **US1 (P1)**: Core status display - must complete first as base
- **US2 (P1)**: Extends US1 with --show-key flag
- **US3 (P2)**: Independent of US1/US2 (uses only URL construction from foundational)
- **US4 (P3)**: Independent of US1/US2/US3 (config write + display)

### Parallel Opportunities

- T007, T008, T009, T010 can all run in parallel (different test functions)
- T014, T016, T018, T019 can all run in parallel (different test functions)
- T022, T023 can run in parallel (different files)
- US3 and US4 can be developed in parallel after foundational phase

---

## Parallel Example: User Story 1

```bash
# Launch all tests for US1 together:
Task: "Unit test TestCollectStatusFromConfig in cmd/mcpproxy/status_cmd_test.go"
Task: "Unit test TestMaskAPIKey in cmd/mcpproxy/status_cmd_test.go"
Task: "Unit test TestBuildWebUIURL in cmd/mcpproxy/status_cmd_test.go"
Task: "Unit test TestFormatStatusTable in cmd/mcpproxy/status_cmd_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T002)
2. Complete Phase 2: Foundational (T003-T006)
3. Complete Phase 3: US1 - Quick Status Check (T007-T013)
4. **STOP and VALIDATE**: `mcpproxy status` works in both modes
5. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational -> Core ready
2. US1 -> `mcpproxy status` works (MVP)
3. US2 -> `--show-key` added
4. US3 -> `--web-url` added
5. US4 -> `--reset-key` added
6. Polish -> docs, lint, final validation

---

## Notes

- All code in single file `cmd/mcpproxy/status_cmd.go` + test file - minimal blast radius
- `maskAPIKey()` already exists in `main.go` - can reuse or duplicate (same package)
- `cliclient.GetInfo()` already exists - only `GetStatus()` needs adding
- Config hot-reload handles key changes - no daemon restart needed after reset
