# Tasks: Deterministic Offline MCP Tool-Scanner v2

**Input**: Design documents from `/specs/076-deterministic-tool-scanner/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/detect-engine.md

**Tests**: REQUIRED. The repo constitution (Principle V) and CLAUDE.md mandate a failing `_test.go` before implementation. Every check ships with MUST-flag / MUST-NOT-flag contract tests.

**Organization**: Grouped by user story (US1–US4 from spec.md) so each is an independently testable increment.

## Path Conventions

Single Go module. New package `internal/security/detect/` (engine + `checks/`); modified `internal/security/scanner/`, `internal/security/patterns/`, `cmd/scan-eval/`, `specs/065-evaluation-foundation/datasets/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Scaffold the new package and the shared types every check depends on.

- [X] T001 Create the `internal/security/detect/` package with doc.go describing the offline, deterministic, recover-isolated contract (per `contracts/detect-engine.md`).
- [X] T002 [P] Add the import-guard test `internal/security/detect/imports_test.go` asserting the package imports no `net`, `os/exec`, filesystem, or HTTP/Docker client (enforces FR-001 offline guarantee). It will fail until the package exists; keep it as the standing offline gate.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core types, normalization, position classifier, engine skeleton, and the additive report fields. Everything below blocks all user stories.

- [X] T003 [P] Define core types in `internal/security/detect/signal.go`: `Tier` (TierHard/TierSoft), `Signal`, `Check` interface, `ToolView`, `RegistryView` (with `ToolsByName`/`ToolNames` indexes), per `data-model.md`. Include `Confidence` clamping and `Evidence` length cap helpers.
- [X] T004 Add additive fields `Confidence float64` and `Signals []string` to `ScanFinding` in `internal/security/scanner/types.go`; write `types_test.go` asserting JSON round-trip keeps them (omitempty) and existing consumers are unaffected.
- [X] T005 [P] Write `internal/security/detect/normalize_test.go` (TDD) covering NFKC, zero-width strip, lowercase, whitespace-collapse, light stemming, and the "don't disclose" vs "do not tell" equivalence; then implement `normalize.go`.
- [X] T006 [P] Write `internal/security/detect/position_test.go` (TDD) for the instruction-vs-example classifier (discount after "such as/e.g./example", inside quotes, in "detects/flags …" lists; keep imperative-position confidence); then implement `position.go`.
- [X] T007 Implement the engine skeleton in `internal/security/detect/engine.go`: registers checks, builds a `RegistryView` once per scan, runs each `Check.Inspect` under `recover()`, records `Coverage{ChecksRun,ChecksFailed,FailedCheckIDs}`; write `engine_test.go` asserting determinism + totality (a panicking fake check is isolated, scan still returns) per the contract guarantees.
- [X] T008 Implement `internal/security/detect/aggregate.go`: signals → `ScanFinding` with tier semantics (any hard → dangerous/quarantine; soft-only severity = distinct-CheckID count 1→low/2→medium/3+→high), combined confidence (independent signals add, cap 1.0), and `Signals` list; write `aggregate_test.go` for the severity ladder and consensus-raises-confidence (FR-005, FR-006, FR-010).

---

## Phase 3: User Story 1 — Catch structural attacks (Priority: P1) 🎯 MVP

**Goal**: The three HARD checks detect hidden-Unicode, cross-server shadowing, and decode-to-shell, auto-quarantining with near-zero FP.

**Independent test**: Offline fixtures per attack class produce a hard finding; clean equivalents produce none (`go test ./internal/security/detect/checks/...`).

- [x] T009 [P] [US1] Write `internal/security/detect/checks/unicode_hidden_test.go` (MUST-flag zero-width/bidi/tag-block/PUA; escalate ≥3 classes or decoded tag-message; MUST-NOT-flag plain ASCII and ordinary accented Unicode); then implement `unicode_hidden.go` running on RAW text (FR-007).
- [x] T010 [P] [US1] Write `internal/security/detect/checks/shadowing_test.go` (MUST-flag cross-server tool reference and same-name collision via `RegistryView`; MUST-NOT-flag a tool referencing its own name); then implement `shadowing.go`.
- [x] T011 [P] [US1] Write `internal/security/detect/checks/payload_decoded_test.go` (MUST-flag base64/hex that DECODES to `curl|sh`/`chmod`/`rm -rf`/raw IP:port with decoded evidence; MUST-NOT-flag base64 of benign data) per FR-008; then implement `payload_decoded.go`.
- [x] T012 [US1] Register the three hard checks in the engine and wire `internal/security/scanner/inprocess.go` so `tpa-descriptions` delegates to `detect.Engine` (feeding a `RegistryView`, rendering findings); keep all CLI/REST/MCP entry points unchanged (FR-015). Update `inprocess_test.go` / `e2e_tpa_smoke_test.go` expectations to the new finding shape.

**Checkpoint**: US1 is a usable MVP — structural attacks are caught and quarantined offline.

---

## Phase 4: User Story 2 — Stop false alarms on legit security tooling (Priority: P2)

**Goal**: The SOFT checks add recall on phrased attacks while the position classifier holds FP ≤ 5% on hard-negatives.

**Independent test**: Hard-negative corpus entries stay unflagged-as-dangerous; matching malicious entries are caught.

- [ ] T013 [P] [US2] Write `internal/security/detect/checks/directive_imperative_test.go` (MUST-flag `<IMPORTANT>`/"before using this tool"/"do not tell the user"/"ignore previous instructions" and variants over NORMALIZED text; MUST-NOT-flag example-position usage) per FR-009; then implement `directive_imperative.go` using regex families + the position classifier.
- [ ] T014 [P] [US2] Write `internal/security/detect/checks/capability_mismatch_test.go` (MUST-flag a math/string tool that reads `~/.ssh` or has an unexplained data-sink param like "sidenote"; MUST-NOT-flag a file tool that legitimately reads files); then implement `capability_mismatch.go` (declared-vs-implied + unused-param heuristic).
- [ ] T015 [P] [US2] Add a per-match confidence to `internal/security/patterns/` matchers (validated card/Luhn → high; entropy-only → low) without changing existing call sites' behavior; update the patterns tests.
- [ ] T016 [US2] Write `internal/security/detect/checks/embedded_secret_test.go`; then implement `embedded_secret.go` wrapping `patterns/` with confidence, register all three soft checks in the engine.

**Checkpoint**: US1 + US2 — full six-check detector with FP discrimination.

---

## Phase 5: User Story 3 — Make "reliable" a CI-gated number (Priority: P2)

**Goal**: Corpus eval gate fails the build on recall/FP regression.

**Independent test**: `scan-eval --gate` exits non-zero when recall < 0.90 or hard-negative FP > 5%.

- [ ] T017 [P] [US3] Expand the labeled corpus in `specs/065-evaluation-foundation/datasets/` with new categories (unicode_smuggling, decoded_payload, capability_mismatch, shadowing) and additional hard-negatives; author original equivalents where external licensing is unclear (FR-014). Update the dataset README + counts.
- [ ] T018 [US3] Add `--gate --min-recall --max-fp` mode to `cmd/scan-eval/` that runs the new `detect.Engine` over the corpus, prints per-category recall/precision/FP/F1 JSON, and exits non-zero on breach; write `cmd/scan-eval` test for the gate exit logic.
- [ ] T019 [US3] Wire the gate into the existing CI test workflow (`.github/workflows/…`) as a blocking step `scan-eval --gate --min-recall 0.90 --max-fp 0.05` (FR-013, SC-006).

**Checkpoint**: reliability is enforced; recall ≥ 0.90 / FP ≤ 5% proven by the gate.

---

## Phase 6: User Story 4 — Transparent, consensus-aware findings (Priority: P3)

**Goal**: Findings expose confidence + contributing checks; risk score reflects agreement instead of dedup-collapsing it.

**Independent test**: a multi-signal tool yields a finding listing each check, carrying confidence, with severity rising by signal count.

- [ ] T020 [US4] Update the risk-score aggregation in `internal/security/scanner/` (types.go / sarif.go scoring) so independent signals on a tool ADD to the score rather than dedup by `(rule_id+location)`; write a scoring test proving consensus raises the score (FR-006, SC-007).
- [ ] T021 [P] [US4] Surface `confidence` + `signals` in the CLI report (`cmd/mcpproxy/security_cmd.go` printReportTable) and confirm they serialize in the REST scan report; add/update the report-rendering test.

**Checkpoint**: operator can see why a tool was flagged and how strongly.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T022 [P] Document the six checks, the two-tier model, and the eval gate in `docs/features/` (extend security-quarantine.md / sensitive-data-detection.md or add tool-scanner.md); note offline/no-egress guarantee.
- [ ] T023 [P] Run `gofmt`/`goimports` and `golangci-lint run --config .github/.golangci.yml ./internal/security/... ./cmd/scan-eval/...`; fix findings.
- [ ] T024 Full verification: `go test -race ./internal/security/... ./cmd/scan-eval/...`, `./scripts/test-api-e2e.sh`, and the corpus gate; confirm SC-001…SC-007 and update the spec checklist.

---

## Dependencies & Execution Order

- **Setup (T001–T002)** → **Foundational (T003–T008)** block everything.
- **US1 (T009–T012)** depends only on Foundational → this is the MVP; ship first.
- **US2 (T013–T016)** depends on Foundational; independent of US1 except the shared engine registration (T012 before T016 wiring is cleanest, but checks themselves are parallel).
- **US3 (T017–T019)** depends on the engine + at least US1 checks existing to measure; corpus expansion (T017) can start in parallel with US1/US2.
- **US4 (T020–T021)** depends on Foundational aggregation; independent of US2/US3.
- **Polish (T022–T024)** last.

### Parallel opportunities

- T002 + T003 + T005 + T006 (different files) early.
- All six check test+impl pairs (T009, T010, T011, T013, T014, T016) are `[P]` — different files, one check each.
- T017 (corpus) parallel with check implementation.
- T022 + T023 parallel in polish.

## Implementation Strategy

**MVP = Phase 1 + 2 + US1 (T001–T012)** — offline detector catching the three structural attack classes, delegated into the live scanner. Then US2 (FP discrimination), US3 (CI gate proving the numbers), US4 (transparency), Polish.

## Summary

- **Total tasks**: 24
- **Per story**: Setup 2, Foundational 6, US1 4, US2 4, US3 3, US4 2, Polish 3
- **Parallel-marked**: 12 tasks `[P]`
- **MVP scope**: T001–T012 (US1)
