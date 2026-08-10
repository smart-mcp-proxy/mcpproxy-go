# Tasks: retrieve_tools Filter Diagnostics

**Input**: Design documents from `/specs/094-filter-diagnostics/` (spec.md + plan.md Codex-APPROVED)
**Tests**: REQUIRED — constitution principle V (TDD). Every RED task lands (and is observed failing) before its GREEN task. The branch still has pre-feature behavior until T010 — Phase 3 exploits that to capture frozen response goldens.

## Phase 1: Setup

*(none — existing packages only, no new deps, no scaffolding)*

## Phase 2: Foundational — attribution seam (blocking)

- [x] T001 Write the frozen-oracle parity test in `internal/server/mcp_annotations_test.go`: copy the CURRENT `shouldExclude` body verbatim into the test as `legacyShouldExcludeOracle`; table-drive all 224 cases (27 non-nil hint combos + nil-annotations, × 8 filter combos) asserting `excludeReason` returns (`excluded` == oracle, correct first-failure `filterKey` per read-only→destructive→open-world order incl. the read-only shortcut, correct `explicit` class per data-model.md). Observe RED (does not compile: `excludeReason` absent).
- [x] T002 Implement `excludeReason(...)` in `internal/server/mcp_annotations.go` and reimplement `shouldExclude` as a delegation to it. T001 + all existing annotation tests GREEN.

## Phase 3a: Characterization (expected GREEN) — freeze pre-feature behavior

- [x] T003 Capture frozen PRE-FEATURE response goldens while the branch still has pre-feature behavior: in NEW `internal/server/mcp_filter_diagnostics_test.go`, build handler fixtures for the three conditions — (a) no filters, (b) filters active/zero omissions, (c) filters active with omissions — in BOTH full and compact modes, and serialize each response to a golden file under `internal/server/testdata/spec094/` (6 goldens). Commit the goldens. Normal test runs ONLY read the committed fixtures; regeneration requires an explicit update mode (`-run TestSpec094Goldens -args -update` or an `UPDATE_SPEC094_GOLDENS=1` env guard). Add the SC-002 assertions: post-feature responses for (a) and (b) must be BYTE-IDENTICAL to their goldens in both modes, and for (c) deleting only the `filter_diagnostics` key must reproduce the (c) golden bytes in both modes. This task is EXPECTED GREEN — it characterizes current behavior; it becomes the SC-002 gate once T010 lands.

## Phase 3b: RED wall — all behavior tests written against pre-feature code

Every task in this phase edits test files only and MUST be observed failing (or failing to compile) when written. T004–T009 are sequential where they share `mcp_filter_diagnostics_test.go`; T009 touches only `toon_surface_isolation_test.go` and may run in parallel with T008.
- [x] T004 [US1] Presence/absence + shape RED tests in `internal/server/mcp_filter_diagnostics_test.go`: (a/b) key absent in both modes; (c) key present in both modes with identical block content; (d) exact raw-JSON shape of a fixture block — alphabetical `omitted_by_filter` ordering, zero-count active filters absent, both reason fields serialized; (e) invariants `omitted_total == Σ counts` and `matched_before_filters == omitted_total + total`. Observe RED.
- [x] T005 [US1] Candidate-window semantics RED tests (FR-002, spec Edge Cases) in the same file: caller `limit=0` and `limit=-5` → window normalized to 20 by the index layer and `matched_before_filters` reflects THAT window (an implementation deriving it from the caller's raw limit fails); no backfill — visibility-removed hits shrink the window and never appear in `matched_before_filters`; annotation-lookup failure (nil from `lookupToolAnnotations`) → classified `missing_annotation`. Observe RED.
- [x] T006 [US2] Reason-split + suggestion RED tests in the same file: mixed missing/explicit fixtures split correctly per filter; read-only shortcut visible in counts (explicit `readOnlyHint=true` tool never attributed to `exclude_destructive`); suggestion precedence (any missing → annotations template; all explicit → inspect template); all 7 non-empty filter subsets × both templates assert ≤200 chars, charset `[a-zA-Z0-9 .,:;()'_-]`, EVERY responsible filter name appears literally in the string, and no unrelated filter name appears. Observe RED.
- [x] T007 [US3] Zero-result + coexistence RED tests in the same file: (a) every match omitted → `total: 0`, `matched_before_filters == omitted_total`; (b) locked-only hits — meaning the normal disabled/name-only-quarantine path where the tool is absent from the index or dropped by visibility (NOT the stale-index case, which is expressly allowed to enter counts per spec Edge Cases) — produce NO diagnostics; (c) locked hit + annotation-filtered callable hit → both `notice` and `filter_diagnostics` present and consistent; repeat with `include_disabled=true` → `disabled`/`remediation` coexist; (d) SC-003 size test: maximal reachable fixture (matched=100, omitted=100, three nonzero filters, 200-char suggestion) serializes compact ≤500 bytes, and the real suggestion constants conform to the FR-006 charset. Observe RED.
- [x] T008 Cross-surface RED tests, sequential in `internal/server/mcp_filter_diagnostics_test.go`: diagnostics via the code-execution surface (`handleRetrieveToolsForMode` with `config.RoutingModeCodeExecution`, forced-full output) — assert the block IS PRESENT and matches the default surface's block (presence assertion fails pre-feature); full-vs-compact identical-block assertion on the default and call-tool surfaces. Observe RED.
- [x] T009 [P] Add a diagnostics-active case (filters + omissions) to `internal/server/toon_surface_isolation_test.go` asserting the `filter_diagnostics` key is present in both toon modes and byte-identical across them (presence fails pre-feature). Observe RED.

## Phase 4: GREEN — implementation

- [x] T010 Implement in `internal/server/mcp_annotations.go`: `reasonCounts` + `filterDiagnostics` structs (data-model.md JSON tags exact), the two suggestion `const` templates, and `filterByAnnotationsWithDiagnostics(...)` (map entry inserted only on a filter's first omission; suggestion by missing-cause precedence with responsible names joined once, alphabetical). Then wire the handler in `internal/server/mcp.go`: in `handleRetrieveToolsWithMode`'s `annotationFilterActive` branch, call `filterByAnnotationsWithDiagnostics`, retain `diag` in a local, and attach `response["filter_diagnostics"] = diag` AFTER the response map is constructed, iff `diag.OmittedTotal >= 1`. T003–T009 all GREEN; goldens byte-identical.

## Phase 5: Registrations (FR-009)

- [x] T011 Update `internal/server/mcp_menu_surface_test.go` FIRST (controlled delta, RED): keep the pre-feature registration golden frozen; widen assertions to permit exactly the three default filter params + description changes on all three registrations; add a deep-compare test that the helper-produced filter parameter schemas are identical across the three registrations; assert every registration description contains the literal caveat sentence adopted for FR-009 — fixed here as: `Filter diagnostics describe this call's candidate window, not the whole catalog.` — and the token `filter_diagnostics`. Observe RED.
- [x] T012 Implement `retrieveToolsAnnotationFilterOptions()` in `internal/server/mcp.go` (beside `retrieveToolsDetailOption`); use it in the default registration and BOTH routing builders in `internal/server/mcp_routing.go` (replacing their duplicated definitions); update all three descriptions with the FR-009 mention + the exact caveat sentence from T011. T011 GREEN.

## Phase 6: Gates & live verification

- [x] T013 Execute quickstart.md live scenario end-to-end on an isolated instance (port 18972, scratch dirs, PID cleanup) and record the four observed responses for the PR description. Grep `read_only_only` under `docs/` — if any page documents retrieve_tools parameters, update it to mention `filter_diagnostics`.
- [ ] T014 FINAL gate (after every prior task, including any T013 doc edits): `go test -race ./internal/server/...`, full `go test -race ./internal/...`, and `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`. Fix findings; re-run until clean.

## Dependencies

`T001→T002 → T003 → T004→T005→T006→T007→T008 (sequential — shared test file) → T010`, with `T009 ∥ T008` (different files, both before T010). Then `T010 → T011→T012 → T013 → T014`. No other parallelism.

## Implementation strategy

Single PR; one cohesive small diff. Phase 3a is expected-GREEN characterization (frozen pre-feature goldens, read-only fixtures, explicit update mode); the RED wall (Phase 3b, T004–T009) is written entirely against pre-feature behavior so every assertion is observed failing for the right reason before T010 exists. The goldens make SC-002 a byte-level guarantee rather than a key-absence check.
