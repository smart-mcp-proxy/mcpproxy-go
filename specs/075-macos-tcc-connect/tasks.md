# Tasks: macOS TCC-safe Connect wizard & App-Data denial diagnostics

**Input**: Design documents from `/specs/075-macos-tcc-connect/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/connect-status.md
**Tests**: INCLUDED — TDD is required (FR-012 + Constitution V). Each behavior gets a failing test first.

## Path Conventions

Single Go module. Connect logic in `internal/connect/`; doctor check in the diagnostics package (pinned in T004); REST mapping in `internal/httpapi/connect.go`.

---

## Phase 1: Setup (Shared Infrastructure)

- [x] T001 Confirm working on branch `075-macos-tcc-connect` and that `go build ./cmd/mcpproxy` + `go test ./internal/connect/...` are green as a baseline (capture current `GetAllStatus` behavior).
- [x] T002 [P] Record the canonical remediation string and bundle IDs (`com.smartmcpproxy.mcpproxy`, `com.smartmcpproxy.mcpproxy.dev`) from `native/macos/MCPProxy/MCPProxy/Info.plist` for reuse in code + tests.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The access-classification primitive every story depends on. MUST complete before US1–US3.

- [x] T003 [P] Add a content-read seam to `internal/connect`: a `Service`-held `readFile func(string) ([]byte, error)` (default `os.ReadFile`) and a `NewServiceWithReader`/test setter, mirroring the existing `homeDir` override, so tests can inject a permission-denied error. File: `internal/connect/connect.go`.
- [x] T004 Pin the diagnostics package/registration point for the doctor check. **Decision:** the runtime doctor is `internal/management` `service.Doctor()` (`diagnostics.go`), which builds `contracts.Diagnostics`; the macOS App-Data check appends an actionable warning string to its `RuntimeWarnings []string` (rendered by `mcpproxy doctor` as the "⚠️ Runtime Warnings" section and counted in `TotalIssues`). The static `internal/diagnostics` registry is an error-CODE catalog (classification metadata), not a runtime-check registry, so it is NOT used. The build-tagged check lives in `internal/management/tcc_appdata_{darwin,other}.go` with a pure, cross-platform translator in `tcc_appdata.go`.

---

## Phase 3: User Story 1 — Stat-only overall status, no privacy storm (Priority: P1) 🎯 MVP

**Goal**: `GetAllStatus` determines installed via `os.Stat` only and performs **zero** content reads; per-client content read moves to an on-demand path.

**Independent Test**: With several client configs present, `GetAllStatus` reads no file contents (injected reader asserts 0 calls); `GetStatus(id)` reads exactly one.

- [x] T005 [P] [US1] Write failing test `TestGetAllStatus_NoContentReads` in `internal/connect/connect_test.go`: inject a `readFile` that fails the test if called; create temp client configs via `NewServiceWithHome`; assert `GetAllStatus` sets `Exists` correctly and never invokes the reader.
- [x] T006 [P] [US1] Write failing test `TestGetStatus_ReadsSingleClientOnDemand` in `internal/connect/connect_test.go`: assert `GetStatus(id)` triggers exactly one content read for that client and resolves `Connected`/`AccessState`.
- [x] T007 [US1] Add `AccessState` (`json:"access_state"`) and `Remediation` (`json:"remediation,omitempty"`) fields to `ClientStatus` in `internal/connect/connect.go` (additive only; preserve all existing fields per FR-006).
- [x] T008 [US1] Refactor `GetAllStatus` (`internal/connect/connect.go:112`) to set `Exists` via `os.Stat` and leave `AccessState="unknown"` / `Connected=false` for installed clients — REMOVE the eager `findEntry` call at line ~135.
- [x] T009 [US1] Add `func (s *Service) GetStatus(clientID string) (ClientStatus, error)` that does the single-client content read via the seam, calls `findEntry`, and sets `Connected` + `AccessState=accessible|absent|malformed`. File: `internal/connect/connect.go`.
- [x] T010 [US1] Route `findEntry`/`findEntryJSON`/`findEntryTOML` (and `readOrCreate*`) through `s.readFile` instead of calling `os.ReadFile` directly, so the seam covers all content reads. File: `internal/connect/connect.go`.
- [x] T011 [US1] Update `GetConnectedCount`/`GetConnectedIDs` (connect.go:87/100) which currently rely on `GetAllStatus().Connected`: re-implement them to use per-client on-demand `GetStatus` (they legitimately need the connected truth for the wizard predicate) — document that this is the one internal caller allowed to content-read, and it does so lazily per client.
- [x] T012 [US1] Run `go test ./internal/connect/ -run 'Status' -race` → green. Verify T005/T006 pass.

**Checkpoint**: Overall status is content-read-free; connected detection is on-demand. SC-001/SC-002 satisfied.

---

## Phase 4: User Story 2 — Actionable message when macOS blocks access (Priority: P2)

**Goal**: Classify access into accessible/absent/denied/malformed and surface a distinct, actionable "blocked by macOS privacy" outcome on reads AND writes.

**Independent Test**: Injected `fs.ErrPermission` on a config access yields `AccessState=denied` + remediation; absent → `absent`; bad JSON → `malformed`; each distinct.

- [x] T013 [P] [US2] Write failing tests in new `internal/connect/access_test.go` for `classifyAccess(err)`: nil→accessible, `fs.ErrNotExist`→absent, `fs.ErrPermission`(wrap `syscall.EPERM` and `EACCES`)→denied, parse-error→malformed. Use `&fs.PathError{Err: syscall.EPERM}`.
- [x] T014 [P] [US2] Write failing test `TestGetStatus_DeniedSurfacesRemediation` in `internal/connect/connect_test.go`: inject reader returning EPERM; assert `GetStatus` returns `AccessState=denied`, non-empty `Remediation` containing `tccutil reset SystemPolicyAppData` and the bundle id, and that `Connected` stays false (not reported as plain "not connected").
- [x] T015 [P] [US2] Write failing test `TestConnectDenied_ReturnsAccessError` in `internal/connect/connect_test.go`: inject EPERM on the connect read/write path; assert a typed `*AccessError` (errors.As) carrying remediation is returned (distinct from unknown-client / already-exists errors).
- [x] T016 [US2] Implement `internal/connect/access.go`: `AccessOutcome` enum, `classifyAccess(err) AccessOutcome` (via `errors.Is(err, fs.ErrPermission)` / `fs.ErrNotExist`), `AccessError` type (Client/Path/Outcome/Remediation, `Error()` + `Unwrap`), and `remediationText(client)` building the canonical message from data-model.md.
- [x] T017 [US2] Wire `classifyAccess` into `GetStatus` (T009) so denied/malformed/absent set `AccessState` + `Remediation`.
- [x] T018 [US2] Wire `classifyAccess` into `Connect`/`Disconnect` (connect.go) and `backupFile` (`internal/connect/backup.go`): on `denied`, return an `*AccessError` with remediation; preserve existing error semantics otherwise.
- [x] T019 [US2] Run `go test ./internal/connect/ -run 'Access|Denied|Connect' -race` → green (T013/T014/T015).

**Checkpoint**: All four access outcomes are distinct and surfaced; denials are actionable. SC-003/SC-004 satisfied.

---

## Phase 5: User Story 3 — Doctor flags a persisted privacy denial (Priority: P3)

**Goal**: macOS-only diagnostics check that detects a persisted App-Data denial affecting Connect and reports remediation; no-op elsewhere.

**Independent Test**: On darwin with an injected denial → warn + remediation; darwin with access OK → pass; non-darwin → check absent/no-op.

- [x] T020 [P] [US3] Write failing test `internal/connect/...` or diagnostics test for a `Service` helper `DetectAppDataDenial() (denied bool, remediation string)`: inject reader returning EPERM on an existing (stat-true) client → denied=true + remediation; reader OK → denied=false; no installed clients → denied=false (no false positive).
- [x] T021 [P] [US3] Write failing test `tcc_appdata_test.go` (build-tagged darwin) asserting the diagnostics check returns warn when `DetectAppDataDenial` reports denied, and pass otherwise; plus a `!darwin` test asserting the check is not registered / is a no-op.
- [x] T022 [US3] Implement `Service.DetectAppDataDenial()` in `internal/connect/access.go`: iterate clients, for the first that `os.Stat`-exists, attempt a content read via the seam; if `classifyAccess`==denied return (true, remediation); else (false, "").
- [x] T023 [US3] Implement `internal/<diagnostics-pkg>/tcc_appdata_darwin.go` (`//go:build darwin`): a check that calls `DetectAppDataDenial` and emits warn+remediation; register it in the registry pinned in T004. Add `tcc_appdata_other.go` (`//go:build !darwin`) no-op stub.
- [x] T024 [US3] Run `go test ./internal/connect/... ./internal/<diagnostics-pkg>/... -run 'AppData|TCC|Denial' -race` → green. Build & smoke `./mcpproxy doctor` on darwin.

**Checkpoint**: Doctor surfaces persisted denials with a one-command fix. SC-005 satisfied.

---

## Phase 6: Polish & Cross-Cutting

- [x] T025 [P] Map new `ClientStatus` fields (`access_state`, `remediation`) into the REST response in `internal/httpapi/connect.go`; ensure GET `/connect` payload is additive only. Confirm/append a per-client GET `/connect/{client}` on-demand status route per contracts (or document that connect/disconnect carry the denied error). DONE: GET `/connect` already serializes the additive fields (`unknown`); added `GET /connect/{client}` on-demand route (`handleGetConnectClientStatus` → `GetStatus`, 404 unknown, denied reported in-band) and mapped denied connect/disconnect `*AccessError` → `403` with remediation.
- [x] T026 [P] Run the existing Connect REST contract/integration tests and `./scripts/test-api-e2e.sh`; confirm no regression (SC-006). DONE: `test-api-e2e.sh` 65/65 PASS; new REST tests in `connect_test.go` green.
- [x] T027 [P] Run CI linter locally: `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml internal/connect/... internal/httpapi/...` → 0 issues (Constitution V; CI uses v2). DONE: 0 issues. (diagnostics-pkg doctor check is separate issue MCP-2831, not yet on main.)
- [x] T028 [P] Docs: add a short macOS "App Data privacy & Connect" note (cause + `tccutil reset` remediation) to the Connect/troubleshooting docs and update CLAUDE.md REST-payload notes for the new fields (Constitution VI). DONE: `docs/api/rest-api.md` Connect section + CLAUDE.md REST note.
- [x] T029 Full verification: `go build ./cmd/mcpproxy && go build -tags server ./cmd/mcpproxy && go test -race ./internal/connect/... ./internal/httpapi/...`. DONE: both editions build; race tests green; `make swagger` regenerated `oas/`.
- [x] T030 [P] (Optional, separable) Frontend: render the `unknown`/`denied` tri-state + remediation banner in the Connect view (`frontend/src/`), verified via the Playwright sweep in CLAUDE.md. Not required for backend MVP.

---

## Dependencies & Execution Order

- **Setup (T001–T002)** → **Foundational (T003–T004)** → user stories.
- **US1 (P1, T005–T012)**: depends only on Foundational. This is the MVP — kills the prompt storm.
- **US2 (P2, T013–T019)**: depends on US1 (`GetStatus` + seam). Adds classification/surfacing.
- **US3 (P3, T020–T024)**: depends on US2 (`classifyAccess`, `DetectAppDataDenial`).
- **Polish (T025–T030)**: after the stories it touches; T025/T026 need US1+US2.

```
Setup → Foundational → US1 → US2 → US3 → Polish
                         │      │
                         └─MVP──┘ (US1 alone delivers SC-001/SC-002)
```

## Parallel Opportunities

- T002 ∥ within setup.
- All test-authoring tasks marked [P] within a phase (T005/T006; T013/T014/T015; T020/T021) — different test funcs, write first, watch fail.
- Polish T025/T026/T027/T028 ∥ (different files), then T029 gate.

## Implementation Strategy

1. **MVP = US1** (T001–T012): stat-only status removes the prompt storm — ship-able alone, addresses the root cause for issue #696's sibling TCC report.
2. **US2** makes denials diagnosable; **US3** adds the doctor convenience.
3. Each phase ends green under `-race` + linter before the next. Frontend (T030) ships separately.

## Notes

- Issue link: `Related #696` (per spec commit conventions). Do NOT use auto-closing keywords; no Claude co-author trailer.
- Out of scope (do not touch): Docker well-known probe (`shellwrap`/`secureenv`), entitlements, signing/notarization.
