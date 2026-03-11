# Tasks: Expand Secret/Env Refs in All Config String Fields

**Input**: Design documents from `/specs/034-expand-secret-refs/`
**Branch**: `034-expand-secret-refs`
**Issue**: [#333](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/333)

**Tests**: TDD is required by the feature constitution (Principle V). Test tasks are included and MUST precede implementation tasks within each phase.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)

---

## Phase 1: Setup (Baseline)

**Purpose**: Establish a green baseline before any changes. All tests must pass before implementation begins.

- [x] T001 Run baseline tests and confirm green: `go test ./internal/secret/... ./internal/upstream/core/... ./internal/config/... -race -v`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Add `ExpandStructSecretsCollectErrors` and export `CopyServerConfig` — both are required by US1 and US2 before any server-config expansion can be implemented.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

> **TDD**: Write tests first (T002), confirm they FAIL, then implement (T003–T004).

- [x] T002 Write failing tests for `ExpandStructSecretsCollectErrors` in `internal/secret/resolver_test.go` (7 cases: HappyPath, PartialFailure, NilPointer, NestedStruct, SliceField, MapField, NoRefs) — tests must FAIL before T003
- [x] T003 Add `SecretExpansionError` struct type to `internal/secret/resolver.go`
- [x] T004 Implement `ExpandStructSecretsCollectErrors` and internal `expandValueCollectErrors` helper in `internal/secret/resolver.go` (mirrors `expandValue` with path tracking + collect-errors semantics)
- [x] T005 Export `copyServerConfig` → `CopyServerConfig` in `internal/config/merge.go` and update 3 internal call sites in the same file
- [x] T006 Verify foundational phase: `go test ./internal/secret/... ./internal/config/... -race` — all T002 tests must now pass

**Checkpoint**: Foundation ready — `ExpandStructSecretsCollectErrors` and `CopyServerConfig` available for user story implementation.

---

## Phase 3: User Stories 1 & 2 — ServerConfig Expansion (Priority: P1) 🎯 MVP

**Goal (US1)**: `${env:...}` and `${keyring:...}` refs in `ServerConfig.WorkingDir` resolve to the actual path when the server starts.

**Goal (US2)**: ALL string fields in `ServerConfig` (including nested `IsolationConfig` and `OAuthConfig`) are automatically expanded — current and future fields alike, without manual allowlisting.

**Independent Test**: Configure a server with `"working_dir": "${env:HOME}/test"`, start it, confirm the stdio child process runs in the resolved directory. Also confirm `URL`, `Command`, `Isolation.WorkingDir`, and `Isolation.ExtraArgs` resolve when set to `${env:...}` refs.

> **TDD**: Write tests first (T007–T008), confirm they FAIL, then implement (T009).

- [x] T007 [US1] [US2] Write failing tests for `NewClientWithOptions` expansion in `internal/upstream/core/client_secret_test.go` (new file): ExpandsWorkingDir, ExpandsIsolationWorkingDir, ExpandsURL, PreservesExistingEnvArgsHeaders, DoesNotMutateOriginal — tests must FAIL before T009
- [x] T008 [P] [US2] Write reflection regression test `TestNewClientWithOptions_ReflectionRegressionTest` in `internal/upstream/core/client_secret_test.go`: walks all string fields of resolved config via reflection and asserts none match `IsSecretRef()` (SC-004) — must FAIL before T009
- [x] T009 [US1] [US2] Replace manual expansion block (lines 105–182) in `internal/upstream/core/client.go` with `config.CopyServerConfig(serverConfig)` + `secretResolver.ExpandStructSecretsCollectErrors(ctx, resolvedServerConfig)` + error logging loop
- [x] T010 [US1] [US2] Verify: `go test ./internal/upstream/core/... -race` — all T007 and T008 tests must now pass

**Checkpoint**: US1 and US2 complete. WorkingDir and all other `ServerConfig` string fields expand refs. Existing `Env`/`Args`/`Headers` behavior preserved (FR-008). Original config not mutated (FR-004).

---

## Phase 4: User Story 3 — DataDir Expansion (Priority: P2)

**Goal**: `${env:...}` refs in the top-level `data_dir` config field resolve before directory validation runs.

**Independent Test**: Set `"data_dir": "${env:HOME}/.mcpproxy-test"` in config, start the proxy, confirm the database opens at the resolved path (not the literal `${env:HOME}/.mcpproxy-test`).

> **TDD**: Write tests first (T011), confirm they FAIL, then implement (T012).

- [x] T011 [US3] Write failing tests for DataDir expansion in `internal/config/config_test.go`: TestLoadConfig_ExpandsDataDir (env var resolves before Validate), TestLoadConfig_DataDirExpandFailure (missing var → warn + Validate fails on dir not found) — tests must FAIL before T012
- [x] T012 [US3] Implement DataDir expansion in `internal/config/loader.go` at both `cfg.Validate()` call sites (lines ~50 and ~143): `secret.NewResolver().ExpandSecretRefs(ctx, cfg.DataDir)` with WARN on failure
- [x] T013 [US3] Verify: `go test ./internal/config/... -race` — all T011 tests must now pass

**Checkpoint**: US3 complete. `data_dir` refs expand before validation. All three user stories are independently functional.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [x] T014 Run `.specify/scripts/bash/update-agent-context.sh claude` to add `ExpandStructSecretsCollectErrors` to CLAUDE.md Active Technologies section
- [x] T015 [P] Full regression: `go test ./internal/... -race` — zero test failures
- [x] T016 [P] E2E sanity: `./scripts/test-api-e2e.sh` — passes clean
- [x] T017 Manual smoke test: add server with `"working_dir": "${env:HOME}/test"` to config and verify it starts with resolved path

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — run immediately
- **Foundational (Phase 2)**: Depends on Phase 1 green baseline — **BLOCKS US1, US2, US3**
- **US1+US2 (Phase 3)**: Depends on Phase 2 (`ExpandStructSecretsCollectErrors` + `CopyServerConfig` available)
- **US3 (Phase 4)**: Depends on Phase 2 (`secret.NewResolver()` already available — no new dep on Phase 3, can run in parallel with Phase 3 if desired)
- **Polish (Phase 5)**: Depends on Phases 3 and 4 both complete

### User Story Dependencies

- **US1+US2 (Phase 3)**: After Phase 2 — no dependency on US3
- **US3 (Phase 4)**: After Phase 2 — no dependency on US1/US2 (different files: `loader.go`, `config_test.go`)

### Within Each Phase

- Tests (T002, T007, T008, T011) MUST be written and confirmed FAILING before their implementation tasks
- T003 (type declaration) before T004 (method implementation)
- T005 (export CopyServerConfig) before T009 (call it from client.go)

### Parallel Opportunities

- T007 and T008 can be written in parallel (both in `client_secret_test.go` — same file, so sequential is safer)
- Phase 3 and Phase 4 can proceed in parallel after Phase 2 (different files: `client.go` vs `loader.go`)
- T015 and T016 (regression + E2E) can run in parallel

---

## Parallel Example: Phases 3 and 4 (after Phase 2 complete)

```bash
# Once foundational phase is done, these can proceed in parallel:

# Agent A: Phase 3 (US1+US2)
# Write tests → internal/upstream/core/client_secret_test.go
# Implement  → internal/upstream/core/client.go

# Agent B: Phase 4 (US3) — independent, different package
# Write tests → internal/config/config_test.go
# Implement  → internal/config/loader.go
```

---

## Implementation Strategy

### MVP First (US1 only — the reported bug fix)

1. Complete Phase 1: Baseline
2. Complete Phase 2: Foundational (required)
3. Complete Phase 3 T007 + T009 only (skip T008 regression test for MVP speed)
4. **STOP and VALIDATE**: `go test ./internal/upstream/core/... -race`
5. Confirm `working_dir` refs resolve in manual smoke test

### Full Delivery (all 3 user stories)

1. Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5
2. Each phase independently verifiable before proceeding
3. Total: 17 tasks, ~4–6 implementation files touched

---

## Notes

- [P] tasks operate on different files with no inter-dependencies
- Each user story phase is independently verifiable via its checkpoint
- TDD order is strictly: WRITE TEST → CONFIRM FAIL → IMPLEMENT → CONFIRM PASS
- Commit after each phase checkpoint (T006, T010, T013, T017)
- `ExpandStructSecretsCollectErrors` must never log resolved values — only reference patterns (FR-003, IV. Security)
