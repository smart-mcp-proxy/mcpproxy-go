---
description: "Tasks for Diagnostics & Error Taxonomy (spec 044)"
---

# Tasks: Diagnostics & Error Taxonomy

**Input**: Design documents from `/specs/044-diagnostics-taxonomy/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md
**Tests**: INCLUDED — spec §8 verification plan and constitution principle V (TDD) both mandate unit, integration, E2E, and UI tests.
**Organization**: Tasks grouped by user story (US1..US4) for independent delivery.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Parallelizable — different files, no in-story dependencies.
- **[Story]**: Maps task to user story (US1..US4). Setup/Foundational/Polish tasks have no story label.
- Include exact absolute file paths where applicable; within-worktree paths are rooted at `/Users/user/repos/mcpproxy-go-diagnostics-taxonomy/`.

## Path Conventions

- Go code: `internal/diagnostics/`, `internal/upstream/`, `internal/oauth/`, `internal/runtime/stateview/`, `internal/httpapi/`, `cmd/mcpproxy/`
- Frontend: `frontend/src/components/diagnostics/`, `frontend/src/views/`, `frontend/src/stores/`
- Tray: `native/macos/MCPProxy/`
- Docs: `docs/errors/`
- Scripts: `scripts/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Scaffold the new package, wire `go generate`, and verify build baseline.

- [ ] T001 Create package skeleton `internal/diagnostics/` with files: `types.go`, `codes.go`, `catalog.go`, `registry.go`, `classifier.go`, `fixers.go` (empty stubs, package declarations only)
- [ ] T002 [P] Add `docs/errors/` directory with an initial `README.md` placeholder noting it is auto-generated
- [ ] T003 [P] Add empty `scripts/check-errors-docs-links.sh` (stub that exits 0) and `scripts/test-diagnostics-e2e.sh` (stub), both executable
- [ ] T004 [P] Add `go generate` directive comment in `internal/diagnostics/registry.go` linking to future codegen for `docs/errors/README.md`
- [ ] T005 Verify baseline build passes: `cd /Users/user/repos/mcpproxy-go-diagnostics-taxonomy && go build ./...`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core types + registry + classifier framework that every user story depends on. MUST complete before any US phase starts.

**CRITICAL**: No user story work proceeds until this phase is green.

### Types & catalog framework

- [ ] T010 Implement core types in `internal/diagnostics/types.go`: `Code`, `Severity` (info/warn/error), `FixStepType`, `FixStep`, `CatalogEntry`, `DiagnosticError`, `FixRequest`, `FixResult`
- [ ] T011 [P] Implement `internal/diagnostics/catalog.go`: `Get(Code) (CatalogEntry, bool)`, `All() []CatalogEntry` (stable-sorted)
- [ ] T012 [P] Implement `internal/diagnostics/registry.go` with `var registry = map[Code]CatalogEntry{}` and `init()` scaffold
- [ ] T013 [P] Implement `internal/diagnostics/fixers.go`: `FixerFunc`, `fixers` map, `Register(key string, f FixerFunc)`, `Invoke(key, req)`
- [ ] T014 Implement `internal/diagnostics/classifier.go`: `Classify(err error, hints ClassifierHints) Code` with signature + fallback to `MCPX_UNKNOWN_UNCLASSIFIED`

### Error inventory (seed codes)

- [ ] T015 Grep codebase and produce initial catalog entries in `codes.go` + `registry.go` for all 7 domains (OAUTH, STDIO, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK) plus the `UNKNOWN` fallback. Target ≥ 25 codes total. Each entry MUST include code, severity, user_message, ≥1 fix_step, docs_url.

### Completeness tests (FR-003)

- [ ] T016 Write `internal/diagnostics/catalog_test.go` asserting: every registered code matches the naming regex; non-empty user_message; `len(fix_steps) >= 1`; docs_url present; no duplicate codes; `Deprecated` entries reference a live replacement
- [ ] T017 Run `go test ./internal/diagnostics/...` and iterate until green
- [ ] T018 [P] Write initial `internal/diagnostics/classifier_test.go` stubs (one table per domain) — all PENDING until per-domain tasks land

### Activity-log integration

- [ ] T019 Extend `internal/runtime/activity_service.go` with `RecordFixAttempt(attempt FixAttempt)` that writes to the existing `ActivityBucket`; update `internal/storage/models.go` only if a new activity type enum is needed (prefer reusing existing enum)

### Stateview snapshot extension

- [ ] T020 Extend `internal/runtime/stateview/stateview.go` with per-server `DiagnosticError` field (optional); update copy-on-write snapshot logic; extend `stateview_test.go` with round-trip test

### REST infrastructure

- [ ] T021 Add `internal/httpapi/diagnostics_per_server.go` with handler stub `handleGetServerDiagnostics` and route registration under existing `/api/v1/servers/{name}/diagnostics` (per `contracts/diagnostics-openapi.yaml`)
- [ ] T022 Add `internal/httpapi/diagnostics_fix.go` with handler stub `handleInvokeFix` + rate-limiter middleware (token-bucket keyed by `(server, code)`, 1/s per tuple), return 429 with Retry-After on breach
- [ ] T023 Register both new routes in `internal/httpapi/server.go` inside the existing `/api/v1` group

**Checkpoint**: Foundation ready — catalog exists with ≥ 25 codes, tests green, stateview carries DiagnosticError, REST routes wired but behaviour is story-dependent.

---

## Phase 3: User Story 1 — Stable error code per failure (Priority: P1) MVP

**Goal**: Every terminal failure produces a registered `MCPX_*` code visible on `GET /api/v1/servers/{name}/diagnostics` with user_message + fix_steps + docs_url. (No UI, no tray, no CLI fix yet.)

**Independent Test**: Configure a broken stdio server (`command: /nonexistent`). Call the REST endpoint. Assert `error_code == "MCPX_STDIO_SPAWN_ENOENT"`, non-empty user_message, ≥1 fix_step, and docs_url.

### Tests for US1 (write first, expect failure, then implement)

- [ ] T030 [P] [US1] Classifier golden test in `internal/diagnostics/classifier_test.go` for STDIO: `exec.Error{Name:"/nonexistent", Err: syscall.ENOENT}` → `MCPX_STDIO_SPAWN_ENOENT`
- [ ] T031 [P] [US1] Classifier golden test for STDIO: non-zero exit → `MCPX_STDIO_EXIT_NONZERO`
- [ ] T032 [P] [US1] Classifier golden test for STDIO: initial handshake timeout → `MCPX_STDIO_HANDSHAKE_TIMEOUT`
- [ ] T033 [P] [US1] HTTP handler test in `internal/httpapi/diagnostics_per_server_test.go`: healthy server returns empty `error_code`; failing server returns populated fields
- [ ] T034 [US1] E2E test `scripts/test-diagnostics-e2e.sh`: start mcpproxy with broken stdio server; curl `/api/v1/servers/broken/diagnostics`; assert `MCPX_STDIO_SPAWN_ENOENT`

### Implementation for US1

- [ ] T040 [US1] Implement STDIO classifier branches in `internal/diagnostics/classifier.go` using `errors.As(*exec.Error)` + `errors.Is(syscall.ENOENT)` + timeout/exit detection
- [ ] T041 [US1] Wrap terminal connection errors in `internal/upstream/manager.go` (or `internal/upstream/managed/*.go`): catch the raw error at the terminal return path, call `diagnostics.Classify(err, hints)`, attach as `DiagnosticError` on stateview snapshot
- [ ] T042 [US1] Implement `handleGetServerDiagnostics` in `internal/httpapi/diagnostics_per_server.go`: read stateview, include existing fields + additive `error_code`, `severity`, `user_message`, `fix_steps`, `docs_url`, `detected_at`, `cause`
- [ ] T043 [US1] Populate catalog entries for 3 STDIO codes: `MCPX_STDIO_SPAWN_ENOENT`, `MCPX_STDIO_EXIT_NONZERO`, `MCPX_STDIO_HANDSHAKE_TIMEOUT`
- [ ] T044 [US1] Author docs pages `docs/errors/MCPX_STDIO_SPAWN_ENOENT.md`, `docs/errors/MCPX_STDIO_EXIT_NONZERO.md`, `docs/errors/MCPX_STDIO_HANDSHAKE_TIMEOUT.md`
- [ ] T045 [US1] Implement `scripts/check-errors-docs-links.sh`: assert bidirectional code ↔ file linkage; add invocation to `scripts/run-all-tests.sh`
- [ ] T046 [US1] Update `oas/swagger.yaml` (or `internal/httpapi/swagger.go`) with the extended per-server diagnostics schema from `contracts/diagnostics-openapi.yaml`
- [ ] T047 [US1] Run `go test -race ./internal/...` and `./scripts/test-diagnostics-e2e.sh`; iterate until green

**Checkpoint**: MVP delivered. A deliberately-broken stdio server produces a stable code + user guidance via REST. No UI/tray/fix behaviour yet.

Commit: `feat(diagnostics): STDIO domain with classifier + REST surfacing (US1)`

---

## Phase 4: User Story 2 — Web UI + macOS tray surfacing (Priority: P2)

**Goal**: Users see diagnostics in the web UI ErrorPanel and in the macOS tray badge + "Fix Issues" menu without needing the CLI.

**Independent Test**: With a broken server configured, open the web UI → ErrorPanel shows code + fix buttons. macOS tray shows red badge + "Fix Issues (1)" menu group. Clicking an entry opens the web UI to the right server.

### Tests for US2

- [ ] T050 [P] [US2] Vue component test `frontend/src/components/diagnostics/ErrorPanel.test.ts`: renders code, message, fix steps; buttons call the right action
- [ ] T051 [P] [US2] Swift test in `native/macos/MCPProxy/Tests/StatusBarControllerTests.swift`: badge colour selection given severity mix
- [ ] T052 [US2] Browser verification script (manual, via `claude-in-chrome`): screenshot ErrorPanel, assert DOM contains code + fix button
- [ ] T053 [US2] Tray verification script (manual, via `mcpproxy-ui-test`): `screenshot_status_bar_menu` + `list_menu_items` show "Fix Issues" group

### Implementation for US2 — Frontend

- [ ] T060 [P] [US2] Create `frontend/src/components/diagnostics/FixStep.vue` (renders one step: link/command/button)
- [ ] T061 [P] [US2] Create `frontend/src/components/diagnostics/ErrorPanel.vue` consuming `{code, severity, user_message, fix_steps, docs_url}`
- [ ] T062 [US2] Extend `frontend/src/stores/servers.ts` (or equivalent) to expose the additive diagnostics fields from the snapshot
- [ ] T063 [US2] Wire `ErrorPanel` from `frontend/src/views/ServerDetail.vue` (visible when `error_code` non-empty)
- [ ] T064 [US2] Add toast handling for fix-button responses (reuse existing toast system)
- [ ] T065 [US2] `cd frontend && npm run build` and verify the built asset is picked up by `make build`

### Implementation for US2 — macOS tray

- [ ] T070 [P] [US2] Create `native/macos/MCPProxy/API/DiagnosticsDecoder.swift`: decode `error_code`/`severity` from server snapshot
- [ ] T071 [P] [US2] Create `native/macos/MCPProxy/Menu/FixIssuesMenu.swift`: "Fix Issues (N)" submenu populated from failing servers; each entry opens web UI at server's detail page
- [ ] T072 [US2] Modify `native/macos/MCPProxy/StatusBar/StatusBarController.swift`: compute worst severity across servers and set badge (red=error, orange=warn, none=healthy)
- [ ] T073 [US2] Rebuild tray per `CLAUDE.md` tray-build instructions; replace `/tmp/MCPProxy.app/Contents/MacOS/MCPProxy`
- [ ] T074 [US2] Execute T052 + T053 visual verifications; attach screenshots paths to the PR description

**Checkpoint**: US1 and US2 both work. UI renders errors and tray badges reflect state. Fix buttons call the existing REST endpoint.

Commit: `feat(diagnostics): web UI ErrorPanel + macOS tray badge (US2)`

---

## Phase 5: User Story 3 — CLI doctor fix + dry-run safety (Priority: P2)

**Goal**: Power users diagnose and fix from the terminal with dry-run default for destructive actions.

**Independent Test**: `mcpproxy doctor --server broken-server` prints code + fix_steps. `mcpproxy doctor fix MCPX_STDIO_SPAWN_ENOENT --server broken-server` runs dry-run by default. `mcpproxy doctor list-codes` prints the full catalog.

### Tests for US3

- [ ] T080 [P] [US3] Cobra CLI test for `doctor --server` in `cmd/mcpproxy/doctor_test.go`: invokes REST, formats output correctly in table/JSON/YAML
- [ ] T081 [P] [US3] Cobra CLI test for `doctor fix` in `cmd/mcpproxy/doctor_fix_test.go`: dry-run default for destructive; `--execute` flag required
- [ ] T082 [P] [US3] Cobra CLI test for `doctor list-codes` in `cmd/mcpproxy/doctor_list_codes_test.go`: prints all catalog entries in both table and JSON formats
- [ ] T083 [US3] Extend `scripts/test-diagnostics-e2e.sh`: run `doctor fix` dry-run against broken server, assert preview includes "dry-run"; assert 429 on immediate second attempt (rate-limit)

### Implementation for US3

- [ ] T090 [US3] Implement fixer `Register("stdio_show_last_logs", ...)` in `internal/diagnostics/fixers.go` (non-destructive): tails the per-server log and returns it in FixResult.Preview
- [ ] T091 [US3] Implement fixer `Register("oauth_reauth", ...)`: calls existing `internal/oauth/coordinator.InitiateLogin(ctx, server)`; destructive: mutates token store
- [ ] T092 [US3] Implement `handleInvokeFix` body in `internal/httpapi/diagnostics_fix.go`: parse FixRequest, check destructive + mode, enforce rate-limit, call `fixers.Invoke`, record FixAttempt via `activity_service.RecordFixAttempt`
- [ ] T093 [P] [US3] Add `cmd/mcpproxy/doctor.go` modifications: support `--server <name>` flag; print code + user_message + fix_steps
- [ ] T094 [P] [US3] Add `cmd/mcpproxy/doctor_fix.go` subcommand with flags: `<CODE>`, `--server`, `--execute` (default false), `--fixer-key` (default auto-select destructive-safe)
- [ ] T095 [P] [US3] Add `cmd/mcpproxy/doctor_list_codes.go` subcommand using `internal/cli/output` formatter (table/JSON/YAML)
- [ ] T096 [US3] Update `docs/cli-management-commands.md` with `doctor fix` and `doctor list-codes` examples

**Checkpoint**: CLI matches the spec. Destructive fixes require `--execute`.

Commit: `feat(diagnostics): CLI doctor fix + list-codes (US3)`

---

## Phase 6: Remaining domains (extension of US1 classifier)

**Purpose**: Cover OAUTH, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK domains. Each sub-phase parallels the STDIO pattern from US1 (classifier branches + catalog entries + docs pages). Technically extensions of US1 but broken out for PR-per-domain delivery (design doc §9).

### OAUTH

- [ ] T100 [P] Classifier tests for OAUTH codes in `internal/diagnostics/classifier_test.go`: `MCPX_OAUTH_REFRESH_EXPIRED`, `MCPX_OAUTH_REFRESH_403`, `MCPX_OAUTH_DISCOVERY_FAILED`, `MCPX_OAUTH_CALLBACK_TIMEOUT`
- [ ] T101 Implement OAUTH classifier branches using typed errors from `internal/oauth/*.go`
- [ ] T102 Wrap OAuth terminal errors in `internal/oauth/*.go` so they surface via stateview
- [ ] T103 [P] Author docs pages under `docs/errors/MCPX_OAUTH_*.md`
- [ ] T104 Commit: `feat(diagnostics): OAUTH domain classifier`

### HTTP

- [ ] T110 [P] Classifier tests for `MCPX_HTTP_DNS_FAILED`, `MCPX_HTTP_TLS_FAILED`, `MCPX_HTTP_401`, `MCPX_HTTP_404`, `MCPX_HTTP_5XX`
- [ ] T111 Implement HTTP classifier using `*net.DNSError`, `*tls.CertificateVerificationError`, HTTP status code inspection
- [ ] T112 [P] Author docs pages `docs/errors/MCPX_HTTP_*.md`
- [ ] T113 Commit: `feat(diagnostics): HTTP domain classifier`

### DOCKER

- [ ] T120 [P] Classifier tests for `MCPX_DOCKER_DAEMON_DOWN`, `MCPX_DOCKER_IMAGE_PULL_FAILED`, `MCPX_DOCKER_NO_PERMISSION`, `MCPX_DOCKER_SNAP_APPARMOR`
- [ ] T121 Implement DOCKER classifier in `internal/diagnostics/classifier.go`; for `MCPX_DOCKER_SNAP_APPARMOR` consult memory note `~/.claude/projects/-Users-user-repos-mcpproxy-go/memory/project_scanner_snap_docker.md`
- [ ] T122 Wrap Docker errors at their terminal return paths (search under `internal/upstream/` isolation code and `internal/security/` scanner code)
- [ ] T123 Implement dual fix steps for `MCPX_DOCKER_SNAP_APPARMOR` per research.md R2: link to docs + destructive button to set `skip_scanner: true` on the server's config (dry-run default)
- [ ] T124 [P] Author docs pages `docs/errors/MCPX_DOCKER_*.md`
- [ ] T125 Commit: `feat(diagnostics): DOCKER domain classifier + snap-apparmor handling`

### CONFIG

- [ ] T130 [P] Classifier tests for `MCPX_CONFIG_DEPRECATED_FIELD`, `MCPX_CONFIG_PARSE_ERROR`, `MCPX_CONFIG_MISSING_SECRET`
- [ ] T131 Implement CONFIG classifier; wrap errors in `internal/config/*.go`
- [ ] T132 [P] Author docs pages `docs/errors/MCPX_CONFIG_*.md`
- [ ] T133 Commit: `feat(diagnostics): CONFIG domain classifier`

### QUARANTINE

- [ ] T140 [P] Classifier tests for `MCPX_QUARANTINE_PENDING_APPROVAL`, `MCPX_QUARANTINE_TOOL_CHANGED`
- [ ] T141 Implement QUARANTINE classifier; wire into the existing quarantine flow in `internal/runtime/tool_quarantine.go`
- [ ] T142 [P] Author docs pages `docs/errors/MCPX_QUARANTINE_*.md`
- [ ] T143 Commit: `feat(diagnostics): QUARANTINE domain classifier`

### NETWORK

- [ ] T150 [P] Classifier tests for `MCPX_NETWORK_PROXY_MISCONFIG`, `MCPX_NETWORK_OFFLINE`
- [ ] T151 Implement NETWORK classifier (detect missing default route / unreachable hosts / HTTP_PROXY env issues)
- [ ] T152 [P] Author docs pages `docs/errors/MCPX_NETWORK_*.md`
- [ ] T153 Commit: `feat(diagnostics): NETWORK domain classifier`

**Checkpoint**: All 7 domains covered. Every registered code has message + fix_step + docs page. `scripts/check-errors-docs-links.sh` passes.

---

## Phase 7: User Story 4 — Telemetry v3 `diagnostics` sub-object (Priority: P3)

**Goal**: Emit anonymous counters for error_code occurrences and fix attempts in the v3 telemetry payload.

**Decision gate**: If spec 042's v3 client is not merged when this phase begins, DEFER this phase to a follow-up PR and document the deferral in the final agent-report.

**Independent Test**: Trigger multiple error codes, run `doctor fix` dry-run, inspect next telemetry heartbeat payload via `GET /api/v1/telemetry/payload`; assert `diagnostics` object contains the documented counters.

### Tests for US4

- [ ] T160 [P] [US4] Payload test in `internal/telemetry/*_test.go`: registering error codes populates `error_code_counts_24h`
- [ ] T161 [P] [US4] Payload test: fix-attempt counters increment; top-20 cap enforced
- [ ] T162 [US4] HTTP handler test in `internal/httpapi/telemetry_payload_test.go`: v3 payload includes `diagnostics` sub-object

### Implementation for US4

- [ ] T170 [US4] Extend `internal/telemetry/` counter registry with `RecordErrorCode(Code)`, `RecordFixAttempt(code, outcome)` (in-memory only)
- [ ] T171 [US4] Wire `RecordErrorCode` from the stateview snapshot extension (T020)
- [ ] T172 [US4] Wire `RecordFixAttempt` from `handleInvokeFix` (T092)
- [ ] T173 [US4] Extend v3 payload serialization with `diagnostics` sub-object: top-20 `error_code_counts_24h`, `fix_attempted_24h`, `fix_succeeded_24h`, `unique_codes_ever`
- [ ] T174 [US4] Respect telemetry-disabled config (reuse existing `TelemetryEnabled()` check — no new gate)
- [ ] T175 [US4] Commit: `feat(diagnostics): telemetry v3 diagnostics sub-object (US4)`

**Checkpoint**: Telemetry carries diagnostics counters when enabled; zero data when disabled.

---

## Phase 8: Polish & Cross-Cutting

- [ ] T200 [P] Update `CLAUDE.md` Active Technologies section confirming spec 044 entries
- [ ] T201 [P] Update `docs/architecture.md` with a short "Diagnostics" section pointing to `internal/diagnostics/`
- [ ] T202 [P] Update `docs/api/rest-api.md` with the two new endpoints
- [ ] T203 [P] Regenerate `docs/errors/README.md` via `go generate ./internal/diagnostics/...`
- [ ] T204 Run full `./scripts/run-all-tests.sh` and capture output in the agent report
- [ ] T205 Run `./scripts/check-errors-docs-links.sh` and `./scripts/test-diagnostics-e2e.sh`
- [ ] T206 Run `./scripts/run-linter.sh` and resolve any new findings
- [ ] T207 Build personal edition: `go build -o mcpproxy ./cmd/mcpproxy` — smoke test `./mcpproxy doctor list-codes`
- [ ] T208 Build server edition: `go build -tags server ./cmd/mcpproxy` — confirm it still compiles
- [ ] T209 Frontend full build: `cd frontend && npm run build`
- [ ] T210 Re-run macOS tray verification via `mcpproxy-ui-test` and capture screenshots under `docs/screenshots/tray-macos/`
- [ ] T211 Open PR with title `feat(diagnostics): stable error-code catalog with tray/web/CLI surfacing`; include the per-phase commit list, verification outputs, and screenshots

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: no deps; starts immediately.
- **Phase 2 (Foundational)**: depends on Phase 1.
- **Phase 3 (US1 — MVP)**: depends on Phase 2.
- **Phase 4 (US2)**: depends on Phase 3 (needs `error_code` populated in REST).
- **Phase 5 (US3 — CLI)**: depends on Phase 2; may run parallel with Phase 4.
- **Phase 6 (remaining domains)**: each sub-phase depends on Phase 2; each domain parallelizable with other domains.
- **Phase 7 (US4 — telemetry)**: depends on Phase 2 + Phase 5 (fix-attempt recording).
- **Phase 8 (Polish)**: depends on all prior phases.

### Parallel Opportunities

- Phase 1: T002, T003, T004 in parallel.
- Phase 2: T011, T012, T013 parallel after T010. T018 parallel with T015.
- Phase 3: T030, T031, T032, T033 parallel; then implementation tasks T040/T043/T044 parallel; T041 must follow T040 (same file path in upstream manager).
- Phase 4: Frontend tasks T060/T061/T070/T071 parallel.
- Phase 5: CLI tasks T093, T094, T095 parallel (distinct files).
- Phase 6: OAUTH, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK sub-phases all parallelizable across agents.

### Within Each Story

- Tests before implementation (TDD per constitution V).
- Types before handlers.
- Handlers before tests green.
- Docs alongside code (not after).

---

## Parallel Example: User Story 1

```bash
# Classifier tests (write first, must fail):
Task: "T030 STDIO ENOENT classifier test in internal/diagnostics/classifier_test.go"
Task: "T031 STDIO non-zero exit classifier test"
Task: "T032 STDIO handshake timeout classifier test"

# HTTP + E2E tests in parallel:
Task: "T033 handleGetServerDiagnostics handler test"
Task: "T034 scripts/test-diagnostics-e2e.sh"

# Implementation tasks in parallel (different files):
Task: "T040 classifier STDIO branches in classifier.go"
Task: "T043 STDIO catalog entries in registry.go"
Task: "T044 STDIO docs pages under docs/errors/"
```

---

## Implementation Strategy

### MVP First (US1 only)

1. Phase 1 — Setup
2. Phase 2 — Foundational (≥ 25 codes + completeness tests)
3. Phase 3 — US1 (STDIO end-to-end)
4. STOP. Verify MVP via `scripts/test-diagnostics-e2e.sh`. Ship if ready.

### Incremental Delivery

1. MVP (US1) → ship.
2. Phase 4 (US2 UI+tray) + Phase 5 (US3 CLI) in parallel → ship.
3. Phase 6 per-domain PRs → ship each.
4. Phase 7 (US4 telemetry) once spec 042 v3 is merged → ship.
5. Phase 8 polish → final PR.

### Parallel Team Strategy

- Dev A: Phase 3 (US1 MVP, STDIO).
- Dev B: Phase 4 (US2 — web UI + tray) after Dev A.
- Dev C: Phase 5 (US3 — CLI) after Phase 2.
- Dev D/E/F: Phase 6 domains in parallel after Phase 2.
- Final: single PR title `feat(diagnostics): stable error-code catalog with tray/web/CLI surfacing` aggregating all commits.

---

## Notes

- [P] = different file, no in-story dependency.
- Stable code names mandated by FR-004 — do not rename codes once committed.
- Every fix respects FR-021/022: no auto-invocation, destructive dry-run default.
- Commit after each checkpoint; do not mix phases in a single commit.
- Verification outputs (curl, screenshots, test logs) go into the agent report at `~/repos/mcpproxy-go/tmp-agent-report-spec2.md`.
