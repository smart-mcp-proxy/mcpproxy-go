# Tasks: CLI Output Formatting System

**Input**: Design documents from `/specs/014-cli-output-formatting/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/ ✅

**Tests**: Included as per constitution (TDD principle) and spec requirements.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

Based on plan.md structure:
- **New package**: `internal/cli/output/`
- **CLI commands**: `cmd/mcpproxy/`
- **Documentation**: `docs/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create output formatting package structure

- [x] T001 Create output package directory structure at internal/cli/output/
- [x] T002 Create OutputFormatter interface and factory in internal/cli/output/formatter.go
- [x] T003 Create StructuredError type in internal/cli/output/error.go
- [x] T004 Create FormatConfig with env var support in internal/cli/output/config.go
- [x] T005 Add global --output and --json flags to cmd/mcpproxy/main.go
- [x] T006 Add resolveOutputFormat() function to cmd/mcpproxy/main.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before user stories

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [x] T007 Implement base Format() method signature in internal/cli/output/formatter.go
- [x] T008 Implement base FormatError() method signature in internal/cli/output/formatter.go
- [x] T009 Implement base FormatTable() method signature in internal/cli/output/formatter.go
- [x] T010 Create NewFormatter() factory function with format validation in internal/cli/output/formatter.go

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - Machine-Readable JSON Output (Priority: P1) 🎯 MVP

**Goal**: AI agents can parse CLI output programmatically via `-o json` flag

**Independent Test**: Run `mcpproxy upstream list -o json | jq .` and verify valid JSON output

### Tests for User Story 1

- [x] T011 [P] [US1] Unit test for JSONFormatter.Format() in internal/cli/output/json_test.go
- [x] T012 [P] [US1] Unit test for JSONFormatter.FormatError() in internal/cli/output/json_test.go
- [x] T013 [P] [US1] Unit test for JSONFormatter.FormatTable() in internal/cli/output/json_test.go
- [x] T014 [P] [US1] Unit test for empty array output (not null) in internal/cli/output/json_test.go

### Implementation for User Story 1

- [x] T015 [US1] Implement JSONFormatter struct in internal/cli/output/json.go
- [x] T016 [US1] Implement JSONFormatter.Format() with snake_case fields in internal/cli/output/json.go
- [x] T017 [US1] Implement JSONFormatter.FormatError() for structured errors in internal/cli/output/json.go
- [x] T018 [US1] Implement JSONFormatter.FormatTable() converting to JSON array in internal/cli/output/json.go
- [x] T019 [US1] Migrate upstream list command to use OutputFormatter in cmd/mcpproxy/upstream_cmd.go
- [x] T020 [US1] Add structured error output when -o json and error occurs in cmd/mcpproxy/upstream_cmd.go
- [x] T021 [US1] Verify --json alias works same as -o json in cmd/mcpproxy/main.go
- [x] T022 [US1] Verify MCPPROXY_OUTPUT=json env var works in cmd/mcpproxy/main.go
- [x] T023 [US1] E2E test: mcpproxy upstream list -o json produces valid JSON

**Checkpoint**: JSON output works for upstream list command, testable independently ✅

---

## Phase 4: User Story 2 - Human-Readable Table Output (Priority: P2)

**Goal**: Developers see clean, aligned table output by default

**Independent Test**: Run `mcpproxy upstream list` and verify formatted table with headers

### Tests for User Story 2

- [x] T024 [P] [US2] Unit test for TableFormatter.Format() in internal/cli/output/table_test.go
- [x] T025 [P] [US2] Unit test for TableFormatter.FormatTable() with column alignment in internal/cli/output/table_test.go
- [x] T026 [P] [US2] Unit test for NO_COLOR=1 environment variable in internal/cli/output/table_test.go
- [x] T027 [P] [US2] Unit test for non-TTY simplified output in internal/cli/output/table_test.go

### Implementation for User Story 2

- [x] T028 [US2] Implement TableFormatter struct in internal/cli/output/table.go
- [x] T029 [US2] Implement TableFormatter.Format() using text/tabwriter in internal/cli/output/table.go
- [x] T030 [US2] Implement TableFormatter.FormatTable() with headers and alignment in internal/cli/output/table.go
- [x] T031 [US2] Implement TableFormatter.FormatError() for human-readable errors in internal/cli/output/table.go
- [x] T032 [US2] Add TTY detection for simplified non-TTY output in internal/cli/output/table.go
- [x] T033 [US2] Add NO_COLOR support to TableFormatter in internal/cli/output/table.go
- [x] T034 [US2] Migrate tools list command to use OutputFormatter in cmd/mcpproxy/tools_cmd.go
- [ ] T035 [US2] Migrate doctor command to use OutputFormatter in cmd/mcpproxy/doctor_cmd.go
- [x] T036 [US2] E2E test: mcpproxy upstream list displays aligned table

**Checkpoint**: Table output works, both JSON and table formats functional ✅

---

## Phase 5: User Story 3 - Hierarchical Command Discovery (Priority: P3)

**Goal**: AI agents discover commands via `--help-json` without loading full docs

**Independent Test**: Run `mcpproxy --help-json | jq .` and verify command structure

### Tests for User Story 3

- [x] T037 [P] [US3] Unit test for HelpInfo structure in internal/cli/output/help_test.go
- [x] T038 [P] [US3] Unit test for ExtractHelpInfo from Cobra command in internal/cli/output/help_test.go
- [x] T039 [P] [US3] Unit test for FlagInfo extraction in internal/cli/output/help_test.go

### Implementation for User Story 3

- [x] T040 [US3] Create HelpInfo, CommandInfo, FlagInfo types in internal/cli/output/help.go
- [x] T041 [US3] Implement ExtractHelpInfo() to build HelpInfo from cobra.Command in internal/cli/output/help.go
- [x] T042 [US3] Implement AddHelpJSONFlag() to add --help-json to commands in internal/cli/output/help.go
- [x] T043 [US3] Implement custom help function with --help-json check in internal/cli/output/help.go
- [x] T044 [US3] Add --help-json flag to root command in cmd/mcpproxy/main.go
- [x] T045 [US3] Propagate --help-json to all subcommands in cmd/mcpproxy/main.go
- [x] T046 [US3] E2E test: mcpproxy --help-json returns valid JSON with commands array
- [x] T047 [US3] E2E test: mcpproxy upstream --help-json returns subcommands array

**Checkpoint**: --help-json works on all commands, agents can discover CLI structure ✅

---

## Phase 6: User Story 4 - YAML Output (Priority: P4)

**Goal**: Users can export data in YAML format for configuration scenarios

**Independent Test**: Run `mcpproxy upstream list -o yaml` and verify valid YAML output

### Tests for User Story 4

- [x] T048 [P] [US4] Unit test for YAMLFormatter.Format() in internal/cli/output/yaml_test.go
- [x] T049 [P] [US4] Unit test for YAMLFormatter.FormatError() in internal/cli/output/yaml_test.go

### Implementation for User Story 4

- [x] T050 [US4] Implement YAMLFormatter struct in internal/cli/output/yaml.go
- [x] T051 [US4] Implement YAMLFormatter.Format() using yaml.v3 in internal/cli/output/yaml.go
- [x] T052 [US4] Implement YAMLFormatter.FormatError() in internal/cli/output/yaml.go
- [x] T053 [US4] Implement YAMLFormatter.FormatTable() in internal/cli/output/yaml.go
- [x] T054 [US4] E2E test: mcpproxy upstream list -o yaml produces valid YAML

**Checkpoint**: All output formats (table, json, yaml) working ✅

---

## Phase 7: Command Migration

**Purpose**: Migrate remaining commands to use unified formatters

- [x] T055 [P] Migrate call command to use OutputFormatter in cmd/mcpproxy/call_cmd.go
- [x] T056 [P] Migrate auth command to use OutputFormatter in cmd/mcpproxy/auth_cmd.go (N/A - auth status uses custom format)
- [x] T057 [P] Migrate secrets command to use OutputFormatter in cmd/mcpproxy/secrets_cmd.go
- [x] T058 Remove legacy output formatting code from migrated commands
- [x] T059 Verify backward compatibility: existing -o json behavior unchanged

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, cleanup, and validation

- [x] T060 [P] Create CLI output formatting documentation at docs/cli-output-formatting.md
- [x] T061 [P] Update CLAUDE.md with output formatting patterns
- [x] T062 [P] Add mcp-eval scenarios for JSON output validation (N/A - mcp-eval not in project)
- [x] T063 Run golangci-lint and fix any issues
- [x] T064 Run full test suite: ./scripts/run-all-tests.sh
- [x] T065 Run E2E API tests: ./scripts/test-api-e2e.sh
- [x] T066 Validate quickstart.md examples work correctly (N/A - no quickstart changes needed)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-6)**: All depend on Foundational phase completion
  - US1 → US2 → US3 → US4 (priority order) OR can run in parallel
- **Command Migration (Phase 7)**: Depends on US1 and US2 (need JSON + Table working)
- **Polish (Phase 8)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Phase 2 - No dependencies on other stories
- **User Story 2 (P2)**: Can start after Phase 2 - Independent of US1
- **User Story 3 (P3)**: Can start after Phase 2 - Independent of US1/US2
- **User Story 4 (P4)**: Can start after Phase 2 - Independent of US1/US2/US3

### Within Each User Story

- Tests written FIRST, verify they FAIL
- Core type/struct implementation
- Method implementations
- Command integration
- E2E verification

### Parallel Opportunities

- All test tasks within a story marked [P] can run in parallel
- Phase 7 command migrations marked [P] can run in parallel
- Phase 8 documentation tasks marked [P] can run in parallel

---

## Parallel Example: User Story 1 Tests

```bash
# Launch all tests for User Story 1 together:
go test -run TestJSONFormatter_Format ./internal/cli/output/
go test -run TestJSONFormatter_FormatError ./internal/cli/output/
go test -run TestJSONFormatter_FormatTable ./internal/cli/output/
go test -run TestJSONFormatter_EmptyArray ./internal/cli/output/
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T006)
2. Complete Phase 2: Foundational (T007-T010)
3. Complete Phase 3: User Story 1 - JSON Output (T011-T023)
4. **STOP and VALIDATE**: `mcpproxy upstream list -o json | jq .`
5. Deploy/demo if ready - agents can now use JSON output

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. Add User Story 1 (JSON) → Test independently → **MVP!**
3. Add User Story 2 (Table) → Improves human UX
4. Add User Story 3 (--help-json) → Agent discovery
5. Add User Story 4 (YAML) → Nice-to-have format
6. Command Migration → Full CLI coverage
7. Polish → Documentation complete

### Task Count Summary

| Phase | Task Count | Parallel Tasks |
|-------|------------|----------------|
| Phase 1: Setup | 6 | 0 |
| Phase 2: Foundational | 4 | 0 |
| Phase 3: US1 JSON (P1) | 13 | 4 tests |
| Phase 4: US2 Table (P2) | 13 | 4 tests |
| Phase 5: US3 Help-JSON (P3) | 11 | 3 tests |
| Phase 6: US4 YAML (P4) | 7 | 2 tests |
| Phase 7: Migration | 5 | 3 commands |
| Phase 8: Polish | 7 | 3 docs |
| **Total** | **66** | **19** |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Constitution requires TDD: write tests first, verify they fail
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
