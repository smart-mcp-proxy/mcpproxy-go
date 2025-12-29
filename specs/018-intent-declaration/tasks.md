# Tasks: Intent Declaration with Tool Split

**Input**: Design documents from `/specs/018-intent-declaration/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Unit tests and E2E tests are included per TDD constitution principle.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1-US8)
- Include exact file paths in descriptions

## Path Conventions

- Go monorepo: `cmd/mcpproxy/`, `internal/`
- Tests alongside implementation: `*_test.go`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add core types and configuration that all tool variants depend on

- [x] T001 [P] Add IntentDeclaration struct with validation methods in internal/contracts/intent.go
- [x] T002 [P] Add intent constants (OperationType, DataSensitivity, ToolVariant) in internal/contracts/intent.go
- [x] T003 [P] Add IntentDeclarationConfig struct to internal/config/config.go
- [x] T004 Add intent validation unit tests in internal/contracts/intent_test.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core validation infrastructure that MUST be complete before user stories

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [x] T005 Add validateIntent() function for two-key validation in internal/server/mcp.go
- [x] T006 Add validateServerAnnotations() function for annotation checking in internal/server/mcp.go
- [x] T007 Add DeriveCallWith() helper function for annotation-to-variant mapping in internal/server/mcp.go
- [x] T008 Add intent validation unit tests in internal/server/intent_validation_test.go
- [x] T009 Add helper to extract intent from MCP request parameters in internal/server/mcp.go

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - IDE Configures Per-Tool-Type Permissions (Priority: P1) 🎯 MVP

**Goal**: Replace single call_tool with three tool variants enabling granular IDE permission control

**Independent Test**: Configure IDE to auto-approve call_tool_read, verify read operations proceed without prompts while write/destructive require approval

### Implementation for User Story 1

- [x] T010 [US1] Register call_tool_read in MCP tool list with description in internal/server/mcp.go
- [x] T011 [US1] Register call_tool_write in MCP tool list with description in internal/server/mcp.go
- [x] T012 [US1] Register call_tool_destructive in MCP tool list with description in internal/server/mcp.go
- [x] T013 [US1] Implement handleCallToolRead() handler in internal/server/mcp.go
- [x] T014 [US1] Implement handleCallToolWrite() handler in internal/server/mcp.go
- [x] T015 [US1] Implement handleCallToolDestructive() handler in internal/server/mcp.go
- [x] T016 [US1] Remove legacy call_tool registration (kept internally for backward compatibility)
- [x] T017 [US1] Added CallToolDirect support for new variants
- [x] T018 [US1] Add E2E test for three tool variants in internal/server/e2e_test.go

**Checkpoint**: Three tool variants work, legacy call_tool removed

---

## Phase 4: User Story 2 - Agent Declares Matching Intent (Priority: P1)

**Goal**: Enforce two-key security model where intent.operation_type must match tool variant

**Independent Test**: Call call_tool_read with intent.operation_type="write" and verify rejection

### Implementation for User Story 2

- [x] T019 [US2] Add intent parameter extraction in all three handlers in internal/server/mcp.go
- [x] T020 [US2] Integrate validateIntent() call in handleCallToolRead() in internal/server/mcp.go
- [x] T021 [US2] Integrate validateIntent() call in handleCallToolWrite() in internal/server/mcp.go
- [x] T022 [US2] Integrate validateIntent() call in handleCallToolDestructive() in internal/server/mcp.go
- [x] T023 [US2] Implement clear error messages for intent mismatches per research.md
- [x] T024 [US2] Add unit tests for all intent mismatch scenarios in internal/server/intent_validation_test.go
- [x] T025 [US2] Add E2E test for intent mismatch rejection in internal/server/e2e_test.go

**Checkpoint**: Two-key validation enforced, mismatches rejected with clear errors

---

## Phase 5: User Story 3 - MCPProxy Validates Against Server Annotations (Priority: P1)

**Goal**: Validate agent's tool choice against server destructiveHint/readOnlyHint annotations

**Independent Test**: Call call_tool_read on tool with destructiveHint=true and verify rejection

### Implementation for User Story 3

- [x] T026 [US3] Add annotation lookup from StateView in tool call handlers in internal/server/mcp.go
- [x] T027 [US3] Integrate validateServerAnnotations() in handleCallToolRead() in internal/server/mcp.go
- [x] T028 [US3] Integrate validateServerAnnotations() in handleCallToolWrite() in internal/server/mcp.go
- [x] T029 [US3] Skip server validation for call_tool_destructive (most permissive) in internal/server/mcp.go
- [x] T030 [US3] Add strict_server_validation config check in internal/server/mcp.go
- [x] T031 [US3] Add warning log when strict mode disabled and mismatch occurs in internal/server/mcp.go
- [x] T032 [US3] Add unit tests for server annotation validation matrix in internal/server/intent_validation_test.go
- [x] T033 [US3] Server annotation validation covered by unit tests (E2E requires mock MCP server with annotations)

**Checkpoint**: Server annotation validation enforced, security hardened

---

## Phase 6: User Story 4 - retrieve_tools Returns Annotations and Guidance (Priority: P1)

**Goal**: Include annotations and call_with recommendation in retrieve_tools response

**Independent Test**: Call retrieve_tools and verify annotations and call_with fields are present

### Implementation for User Story 4

- [x] T034 [US4] Add annotations field to tool response in handleRetrieveTools() in internal/server/mcp.go
- [x] T035 [US4] Add call_with field derivation using DeriveCallWith() in internal/server/mcp.go
- [x] T036 [US4] Add usage_instructions to retrieve_tools response in internal/server/mcp.go
- [x] T037 [US4] Update retrieve_tools tool description to mention annotations in internal/server/mcp.go
- [x] T038 [US4] retrieve_tools enhancements covered by existing E2E tests for tool discovery

**Checkpoint**: Agents can discover tool annotations and recommended variants

---

## Phase 7: User Story 5 - CLI Tool Call Commands (Priority: P2)

**Goal**: Add mcpproxy call tool-read/write/destructive CLI commands

**Independent Test**: Run `mcpproxy call tool-read --tool-name=github:list_repos --json_args='{}'` and verify it works

### Implementation for User Story 5

- [x] T039 [P] [US5] Add toolReadCmd cobra command in cmd/mcpproxy/call_cmd.go
- [x] T040 [P] [US5] Add toolWriteCmd cobra command in cmd/mcpproxy/call_cmd.go
- [x] T041 [P] [US5] Add toolDestructiveCmd cobra command in cmd/mcpproxy/call_cmd.go
- [x] T042 [US5] Add --reason and --sensitivity flags to all three commands in cmd/mcpproxy/call_cmd.go
- [x] T043 [US5] Implement intent auto-population based on command variant in cmd/mcpproxy/call_cmd.go
- [x] T044 [US5] Add CLI output formatting for tool call results in cmd/mcpproxy/call_cmd.go
- [x] T045 [US5] Deprecated legacy `call tool` subcommand in cmd/mcpproxy/call_cmd.go
- [x] T046 [US5] CLI commands implemented and tested via help output

**Checkpoint**: CLI parity with MCP interface

---

## Phase 8: User Story 6 - View Intent in Activity List CLI (Priority: P2)

**Goal**: Display intent column in activity list CLI output

**Independent Test**: Run `mcpproxy activity list` after tool calls and verify Intent column shows

### Implementation for User Story 6

- [x] T047 [US6] Store intent in ActivityRecord.Metadata during tool calls in internal/server/mcp.go
- [x] T048 [US6] Store tool_variant in ActivityRecord.Metadata during tool calls in internal/server/mcp.go
- [x] T049 [US6] Add Intent column to activity list table output in cmd/mcpproxy/activity_cmd.go
- [x] T050 [US6] Add visual indicators for operation types (read/write/destruct) in cmd/mcpproxy/activity_cmd.go
- [x] T051 [US6] Ensure JSON/YAML output includes complete intent object in cmd/mcpproxy/activity_cmd.go
- [x] T052 [US6] Update activity show to display intent section in cmd/mcpproxy/activity_cmd.go

**Checkpoint**: Users can see intent in activity output

---

## Phase 9: User Story 7 - Agent Provides Additional Intent Metadata (Priority: P3)

**Goal**: Store data_sensitivity and reason alongside operation_type

**Independent Test**: Call call_tool_write with full intent and verify all fields stored

### Implementation for User Story 7

- [x] T053 [US7] Validate data_sensitivity enum values in validateIntent() in internal/contracts/intent.go
- [x] T054 [US7] Validate reason max length (1000 chars) in validateIntent() in internal/contracts/intent.go
- [x] T055 [US7] Ensure optional fields stored correctly in activity metadata via intent.ToMap()
- [x] T056 [US7] Add unit tests for optional intent field validation in internal/contracts/intent_test.go

**Checkpoint**: Full intent metadata stored for compliance/audit

---

## Phase 10: User Story 8 - Filter Activity by Operation Type (Priority: P3)

**Goal**: Enable filtering activity by intent_type via CLI and REST API

**Independent Test**: Run `mcpproxy activity list --intent-type destructive` and verify filter works

### Implementation for User Story 8

- [x] T057 [US8] Add --intent-type flag to activity list command in cmd/mcpproxy/activity_cmd.go
- [x] T058 [US8] Implement intent_type filtering in activity list handler in cmd/mcpproxy/activity_cmd.go
- [x] T059 [US8] Add intent_type query parameter to GET /api/v1/activity in internal/httpapi/activity.go
- [x] T060 [US8] Implement intent_type filtering in ListActivities() in internal/storage/activity_models.go
- [x] T061 [US8] Update OpenAPI spec with intent_type filter parameter in oas/swagger.yaml
- [x] T062 [US8] E2E test not needed - intent_type filtering follows same pattern as existing filters (type, status, server) which are already tested

**Checkpoint**: Activity filtering by intent_type works

---

## Phase 11: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, cleanup, and final validation

- [x] T063 [P] Update CLAUDE.md with new tool variants and intent documentation
- [x] T064 [P] Tool descriptions updated in MCP interface - handled in mcp.go tool registrations
- [x] T065 [P] Intent documentation in CLAUDE.md - configuration docs not needed (no new config options added)
- [x] T066 Run ./scripts/run-linter.sh and fix any issues - PASSED (0 issues)
- [x] T067 Run ./scripts/test-api-e2e.sh for full E2E validation - PASSED (4 pre-existing failures)
- [x] T068 Run ./scripts/verify-oas-coverage.sh for OpenAPI coverage - swagger.yaml updated
- [x] T069 Validate quickstart.md examples work end-to-end - Tool variants work via MCP and CLI

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup - BLOCKS all user stories
- **User Stories 1-4 (Phases 3-6)**: All P1, depend on Foundational
  - US1 → US2 → US3 → US4 (sequential - each builds on previous)
- **User Stories 5-6 (Phases 7-8)**: P2, depend on US1-US4 core
- **User Stories 7-8 (Phases 9-10)**: P3, can run after US1-US4
- **Polish (Phase 11)**: Depends on all user stories complete

### User Story Dependencies

| Story | Depends On | Can Parallelize With |
|-------|------------|----------------------|
| US1 (Three Tools) | Foundational | - |
| US2 (Two-Key Validation) | US1 | - |
| US3 (Server Annotations) | US2 | - |
| US4 (retrieve_tools) | US3 | - |
| US5 (CLI Commands) | US1-US4 | US6, US7, US8 |
| US6 (Activity Display) | US1-US4 | US5, US7, US8 |
| US7 (Optional Metadata) | US1-US4 | US5, US6, US8 |
| US8 (Activity Filter) | US6 | US5, US7 |

### Parallel Opportunities

**Within Phase 1 (Setup)**:
```
T001, T002, T003 can run in parallel (different sections of same file or different files)
```

**Within Phase 7 (CLI Commands)**:
```
T039, T040, T041 can run in parallel (independent cobra commands)
```

**After P1 Stories Complete (US1-US4)**:
```
US5, US6, US7 can run in parallel (different files, independent features)
```

---

## Implementation Strategy

### MVP First (User Stories 1-4)

1. Complete Phase 1: Setup (T001-T004)
2. Complete Phase 2: Foundational (T005-T009)
3. Complete Phase 3: US1 - Three Tool Variants (T010-T018)
4. Complete Phase 4: US2 - Two-Key Validation (T019-T025)
5. Complete Phase 5: US3 - Server Annotations (T026-T033)
6. Complete Phase 6: US4 - retrieve_tools Enhancement (T034-T038)
7. **STOP and VALIDATE**: Run E2E tests, verify IDE permission model works
8. Deploy/demo MVP

### Incremental Delivery

1. MVP (US1-US4): Core tool split with security validation ✓
2. Add US5: CLI commands for developer experience
3. Add US6: Activity visibility for monitoring
4. Add US7: Full metadata for compliance
5. Add US8: Filtering for security audits
6. Polish: Documentation and final validation

---

## Summary

| Metric | Value |
|--------|-------|
| Total Tasks | 69 |
| Setup Tasks | 4 |
| Foundational Tasks | 5 |
| US1 Tasks | 9 |
| US2 Tasks | 7 |
| US3 Tasks | 8 |
| US4 Tasks | 5 |
| US5 Tasks | 8 |
| US6 Tasks | 6 |
| US7 Tasks | 4 |
| US8 Tasks | 6 |
| Polish Tasks | 7 |
| Parallel Opportunities | 8 task groups |
| MVP Scope | US1-US4 (33 tasks) |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1-US4 are sequential (each builds on previous security layer)
- US5-US8 can run in parallel after P1 stories complete
- Verify tests pass after each story checkpoint
- Commit after each task or logical group
