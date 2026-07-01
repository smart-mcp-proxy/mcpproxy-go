---
description: "Task list for Spec 077 — Scanner Simplification"
---

# Tasks: Scanner Simplification — Deterministic Default, Opt-In Deep Scan

**Input**: Design documents from `/specs/077-scanner-simplification/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/
**Tests**: INCLUDED — the constitution (Principle V) mandates TDD; write each test first and confirm it fails before implementing.

**Organization**: By user story (US1 P1 → US4 P3). US1 is the MVP. US2/US3/US4 build on the US1 baseline refactor but are each independently testable once Foundational + US1 land.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: US1–US4 for story-phase tasks; Setup/Foundational/Polish carry no story label

## Path Conventions

Web app: Go core in `internal/` + `cmd/`, Vue frontend in `frontend/src/`. Paths are absolute-from-repo-root.

---

## Phase 1: Setup (Shared)

**Purpose**: Establish a green regression baseline before refactoring the scanner.

- [ ] T001 Confirm branch `077-scanner-simplification` builds and the existing scanner suite passes: `go build ./cmd/mcpproxy && go test ./internal/security/... ` — record as the pre-refactor reference.
- [ ] T002 [P] Capture the current eval baseline: `go run ./cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` and note per-category recall/FP as the no-regression reference for T006/T015.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared type changes that US1–US4 all build on. ⚠️ No user story work begins until this is done.

- [ ] T003 Add `Tier` (`hard`|`soft`) and `Sources` (`[]string`) fields to the `ScanFinding` type in `internal/security/scanner/types.go` (JSON `omitempty`, back-compat); update `detectFindingToScanFinding` in `internal/security/scanner/inprocess.go` to set `Tier`/`Sources` from detect output.
- [ ] T004 Add a `DeepScanDescriptor` type (`Enabled`, `Ran`, `Available`, `ScannersFailed []{ID,Reason}`) and a `DeepScan` field on the scan report/summary type in `internal/security/scanner/service.go` (unpopulated placeholder for now).
- [ ] T005 [P] Copy the two contract schemas from `specs/077-scanner-simplification/contracts/` into `internal/security/scanner/testdata/` for report/config validation tests.

**Checkpoint**: Shared types compile; scan output unchanged in behavior.

---

## Phase 3: User Story 1 — Reliable offline scan, no Docker (Priority: P1) 🎯 MVP

**Goal**: The deterministic detect engine is the always-on, offline, zero-Docker baseline; duplicate legacy rules are gone; blocking posture preserved via a curated hard check.

**Independent Test**: With Docker stopped, scan several servers (incl. one poisoned tool) → every server gets a deterministic `clean`/`warning`/`dangerous` verdict, poisoned tool blocked, benign near-miss not blocked, no degraded/failed state.

### Tests for User Story 1 (write first, must fail)

- [ ] T006 [P] [US1] Failing test: no detection-coverage loss after legacy-rule deletion (every corpus attack still detected) in `internal/security/scanner/inprocess_test.go`.
- [ ] T007 [P] [US1] Failing test: `phrase_injection` hard-tier recall on curated positives AND zero hard-block on benign near-misses in `internal/security/detect/checks/phrase_injection_test.go`.
- [ ] T008 [P] [US1] Failing test: determinism (same tool set → identical findings/verdict across two runs) and baseline runs with no Docker in `internal/security/scanner/service_test.go`.

### Implementation for User Story 1

- [ ] T009 [US1] Create the curated hard-tier check in `internal/security/detect/checks/phrase_injection.go` (high-confidence injection/exfiltration patterns; tier=hard; positions/thresholds to avoid benign FP).
- [ ] T010 [US1] Register `phrase_injection` in the detect wiring `Checks` slice in `internal/security/scanner/inprocess.go` (check imports detect; engine never imports checks — no cycle).
- [ ] T011 [US1] Delete legacy `tpaRules` + `matchAnyPhrase` and their append in `internal/security/scanner/inprocess.go`.
- [ ] T012 [US1] Delete the legacy embedded-secret path (`security.NewDetector(nil)` append) in `internal/security/scanner/inprocess.go`; rely on detect's `EmbeddedSecret` check.
- [ ] T013 [US1] Derive the baseline verdict (`clean`/`warning`/`dangerous`) from baseline hard/soft tiers only in `internal/security/scanner/service.go` (a `dangerous` status requires ≥1 hard baseline finding).
- [ ] T014 [US1] Default all bundled Docker scanners to `enabled:false` and keep `tpa-descriptions` `enabled:true` in `internal/security/scanner/registry_bundled.go`.
- [ ] T015 [US1] Add `phrase_injection` to `gateChecks()` in `cmd/scan-eval/gate.go`.
- [ ] T016 [P] [US1] Extend `detect_corpus_v1.json` with curated `phrase_injection` positives and benign near-misses.
- [ ] T017 [US1] Frontend: gate the Approve modal on baseline `dangerous` findings only in `frontend/src/views/ServerDetail.vue`.

**Checkpoint**: MVP — offline deterministic baseline replaces the legacy stack; `scan-eval --gate` green with the new check.

---

## Phase 4: User Story 2 — One unified, readable report (Priority: P2)

**Goal**: A single deduplicated report where cross-scanner agreement raises confidence.

**Independent Test**: With two sources flagging the same tool/location → one finding, `sources` lists both, `confidence` higher than either alone; every finding shows a severity.

### Tests for User Story 2 (write first, must fail)

- [ ] T018 [P] [US2] Failing test: dedup by `(rule_id, location)` yields exactly one finding in `internal/security/scanner/sarif_test.go`.
- [ ] T019 [P] [US2] Failing test: two independent sources agreeing on `(location, threat_type)` boosts confidence above single-source in `internal/security/scanner/sarif_test.go`.

### Implementation for User Story 2

- [ ] T020 [US2] Extend `consensusWeight`/`CalculateRiskScore` so matched external findings on `(location, threat_type)` add to consensus, not flatten to weight 1, in `internal/security/scanner/sarif.go`.
- [ ] T021 [US2] Populate `Finding.Sources` (all contributing scanner ids) during merge in `AggregateReports` in `internal/security/scanner/engine.go`.
- [ ] T022 [US2] Ensure every finding carries a severity via `ClassifyThreat` backfill for external/legacy SARIF findings in `internal/security/scanner/sarif.go`.
- [ ] T023 [US2] Frontend: render the single merged finding list with severity + source attribution in `frontend/src/views/ScanReport.vue`.

**Checkpoint**: US1 + US2 — one trustworthy merged report.

---

## Phase 5: User Story 3 — Opt-in deep scan that never hurts the baseline (Priority: P2)

**Goal**: Docker scanners + source extraction are opt-in (off by default), best-effort, and can never change the baseline verdict; deprecated config migrates.

**Independent Test**: Enable deep scan (Docker present) → deep findings merge; then kill Docker → baseline verdict identical to deep-off, `deep_scan.available=false` with a note; old config keys load unchanged.

### Tests for User Story 3 (write first, must fail)

- [ ] T024 [P] [US3] Failing test: deep scan off by default → only baseline runs, no Docker invoked, in `internal/security/scanner/service_test.go`.
- [ ] T025 [P] [US3] Failing test: deep-scan failure/unavailable → baseline verdict unchanged AND descriptor populated, in `internal/security/scanner/engine_test.go`.
- [ ] T026 [P] [US3] Failing test: config migration round-trip — `scanner_fetch_package_source`/`scanner_disable_no_new_privileges` map into `deep_scan.*`, `auto_scan_quarantined` ignored — in `internal/config/config_test.go`.

### Implementation for User Story 3

- [ ] T027 [US3] Add the `security.deep_scan` config struct (`Enabled`, `FetchPackageSource *bool`, `DisableNoNewPrivileges`, `Scanners []string`; `swaggertype` tags) and remove the orphaned `auto_scan_quarantined` in `internal/config/config.go`.
- [ ] T028 [US3] Migrate deprecated top-level keys into `deep_scan.*` on load with back-compat aliases in the config loader/`normalizeServerQuarantineFlags` path in `internal/config/config.go`.
- [ ] T029 [US3] Gate deep-scan execution on `deep_scan.enabled` (and the per-scanner list) in `resolveScanners`/`executeScan` in `internal/security/scanner/engine.go`.
- [ ] T030 [US3] Populate `DeepScanDescriptor` and REMOVE the `degradeIfIncompleteCoverage` downgrade-to-`degraded` when only deep-scan plugins fail, in `internal/security/scanner/service.go`.
- [ ] T031 [US3] Point `scanner_fetch_package_source` / `scanner_disable_no_new_privileges` consumers at `deep_scan.*` in `internal/security/scanner/engine.go` and `docker.go`.
- [ ] T032 [US3] Regenerate OpenAPI: `make swagger` (config surface changed) and verify `make swagger-verify`.
- [ ] T033 [US3] Frontend: show deep scan as an opt-in affordance and render deep-scan failures as info (not error) in `frontend/src/views/Security.vue` + `frontend/src/views/ScanReport.vue`.

**Checkpoint**: US1–US3 — deep scan is safely optional; the baseline is untouchable.

---

## Phase 6: User Story 4 — Quiet, trustworthy notifications (Priority: P3)

**Goal**: At most one settled scan notification per server, even under reconnect storms.

**Independent Test**: Restart several servers at once → each emits a single settled scan event, not a flood of per-scanner lifecycle messages.

### Tests for User Story 4 (write first, must fail)

- [ ] T034 [P] [US4] Failing test: a reconnect storm across N servers yields ≤ N settled scan events in `internal/runtime/scan_notify_test.go`.

### Implementation for User Story 4

- [ ] T035 [US4] Replace per-scanner `security.scan_started/progress/completed/failed` emissions with one debounced `scan.settled` event per server per scan in the scan-notification emit path in `internal/runtime/`.
- [ ] T036 [US4] Frontend: consume the settled event (drop per-scanner lifecycle handling) in `frontend/src/composables/useSecurityScannerStatus.ts`.

**Checkpoint**: All four stories independently functional.

---

## Phase 7: Polish & Cross-Cutting

- [ ] T037 [P] Update `docs/features/tool-scanner.md`: remove the legacy-coexistence caveat; document the baseline/deep-scan split, the `phrase_injection` hard check, and the two-tier model now governing behavior.
- [ ] T038 [P] Update `docs/features/security-scanner-plugins.md`: deep scan is opt-in/off-by-default, config migration table, no-Docker default behavior.
- [ ] T039 [P] Update `docs/configuration.md` for the `security.deep_scan` block and the removed `auto_scan_quarantined`.
- [ ] T040 Run `specs/077-scanner-simplification/quickstart.md` scenarios 1–7 and record results.
- [ ] T041 `golangci-lint run --config .github/.golangci.yml ./...` clean + `go test -race ./internal/... ` green.
- [ ] T042 `./scripts/test-api-e2e.sh` green (unified report shape via REST).

---

## Dependencies & Execution Order

- **Setup (P1)** → **Foundational (P2)** blocks all stories.
- **US1 (P3 phase)** is the MVP and the base refactor; **US2/US3/US4 depend on US1** landing (they build on the baseline-only report and the deleted legacy path).
- After US1: **US2, US3, US4 are largely independent** and can proceed in parallel (US2 = `sarif.go`/`engine.go`; US3 = `config.go`/`engine.go`/`service.go`; US4 = `internal/runtime` + a different frontend file). US3 and US2 both touch `engine.go` — sequence those two if worked concurrently.
- **Polish (P7)** after the desired stories.

### Within each story

- Tests first and failing → implementation → frontend → checkpoint.
- Go: models/types before services before wiring. `gofmt`/`goimports` on every file.

### Parallel opportunities

- T006/T007/T008 (US1 tests) in parallel.
- T018/T019 (US2 tests) in parallel.
- T024/T025/T026 (US3 tests) in parallel.
- Docs T037/T038/T039 in parallel.

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → **STOP & VALIDATE**: run quickstart scenarios 1–2 (offline deterministic baseline, poisoned-blocked/benign-not). This alone is shippable value: a reliable no-Docker scanner.

### Incremental delivery

US1 (MVP) → US2 (unified report) → US3 (opt-in deep scan) → US4 (quiet notifications). Each is a demoable increment that doesn't break the prior.

---

## Notes

- No new third-party dependency (constitution + spec constraint).
- Quarantine state machine (`internal/runtime/tool_quarantine.go`) is OUT OF SCOPE — do not modify.
- The one deliberate posture change (some legacy-blocked phrases → review-only unless curated) is bounded by T007/T016 and the `scan-eval --gate`.
- Commit after each task or logical group; use `Related #<issue>` (never `Fixes/Closes`), no AI co-author trailer (per spec Commit Conventions).
