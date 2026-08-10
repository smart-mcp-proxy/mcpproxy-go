# Tasks: retrieve_tools Filter Diagnostics

**Input**: Design documents from `/specs/094-filter-diagnostics/` (spec.md + plan.md Codex-APPROVED)
**Tests**: REQUIRED — constitution principle V (TDD). Every RED task lands (and is observed failing) before its GREEN task. The branch still has pre-feature behavior until T008 — Phase 3 exploits that to capture frozen response goldens.

## Phase 1: Setup

*(none — existing packages only, no new deps, no scaffolding)*

## Phase 2: Foundational — attribution seam (blocking)

- [ ] T001 Write the frozen-oracle parity test in `internal/server/mcp_annotations_test.go`: copy the CURRENT `shouldExclude` body verbatim into the test as `legacyShouldExcludeOracle`; table-drive all 224 cases (27 non-nil hint combos + nil-annotations, × 8 filter combos) asserting `excludeReason` returns (`excluded` == oracle, correct first-failure `filterKey` per read-only→destructive→open-world order incl. the read-only shortcut, correct `explicit` class per data-model.md). Observe RED (does not compile: `excludeReason` absent).
- [ ] T002 Implement `excludeReason(...)` in `internal/server/mcp_annotations.go` and reimplement `shouldExclude` as a delegation to it. T001 + all existing annotation tests GREEN.

## Phase 3: RED wall — all behavior tests written against pre-feature code

Every task in this phase edits test files only, must FAIL (or fail-to-compile) when written, and is sequential within `mcp_filter_diagnostics_test.go` (T004–T007 share that file — no [P]).

- [ ] T003 Capture frozen PRE-FEATURE response goldens while the branch still has pre-feature behavior: in NEW `internal/server/mcp_filter_diagnostics_test.go`, build handler fixtures for the three conditions — (a) no filters, (b) filters active/zero omissions, (c) filters active with omissions — in BOTH full and compact modes, and serialize each response to a golden file under `internal/server/testdata/spec094/` (6 goldens). Commit the goldens; add the SC-002 assertions: post-feature responses for (a) and (b) must be BYTE-IDENTICAL to their goldens in both modes, and for (c) deleting only the `filter_diagnostics` key must reproduce the (c) golden bytes in both modes. (These assertions pass trivially now; they become the SC-002 gate once T008 lands. The goldens are the frozen oracle Codex required.)
- [ ] T004 [US1] Presence/absence + shape RED tests in `internal/server/mcp_filter_diagnostics_test.go`: (a/b) key absent in both modes; (c) key present in both modes with identical block content; (d) exact raw-JSON shape of a fixture block — alphabetical `omitted_by_filter` ordering, zero-count active filters absent, both reason fields serialized; (e) invariants `omitted_total == Σ counts` and `matched_before_filters == omitted_total + total`. Observe RED.
- [ ] T005 [US1] Candidate-window semantics RED tests (FR-002, spec Edge Cases) in the same file: caller `limit=0` and `limit=-5` → window normalized to 20 by the index layer and `matched_before_filters` reflects THAT window (an implementation deriving it from the caller's raw limit fails); no backfill — visibility-removed hits shrink the window and never appear in `matched_before_filters`; annotation-lookup failure (nil from `lookupToolAnnotations`) → classified `missing_annotation`. Observe RED.
- [ ] T006 [US2] Reason-split + suggestion RED tests in the same file: mixed missing/explicit fixtures split correctly per filter; read-only shortcut visible in counts (explicit `readOnlyHint=true` tool never attributed to `exclude_destructive`); suggestion precedence (any missing → annotations template; all explicit → inspect template); all 7 non-empty filter subsets × both templates assert ≤200 chars, charset `[a-zA-Z0-9 .,:;()'_-]`, EVERY responsible filter name appears literally in the string, and no unrelated filter name appears. Observe RED.
- [ ] T007 [US3] Zero-result + coexistence RED tests in the same file: (a) every match omitted → `total: 0`, `matched_before_filters == omitted_total`; (b) locked-only hits — meaning the normal disabled/name-only-quarantine path where the tool is absent from the index or dropped by visibility (NOT the stale-index case, which is expressly allowed to enter counts per spec Edge Cases) — produce NO diagnostics; (c) locked hit + annotation-filtered callable hit → both `notice` and `filter_diagnostics` present and consistent; repeat with `include_disabled=true` → `disabled`/`remediation` coexist; (d) SC-003 size test: maximal reachable fixture (matched=100, omitted=100, three nonzero filters, 200-char suggestion) serializes compact ≤500 bytes, and the real suggestion constants conform to the FR-006 charset. Observe RED.

## Phase 4: GREEN — implementation

- [ ] T008 Implement in `internal/server/mcp_annotations.go`: `reasonCounts` + `filterDiagnostics` structs (data-model.md JSON tags exact), the two suggestion `const` templates, and `filterByAnnotationsWithDiagnostics(...)` (map entry inserted only on a filter's first omission; suggestion by missing-cause precedence with responsible names joined once, alphabetical). Then wire the handler in `internal/server/mcp.go`: in `handleRetrieveToolsWithMode`'s `annotationFilterActive` branch, call `filterByAnnotationsWithDiagnostics`, retain `diag` in a local, and attach `response["filter_diagnostics"] = diag` AFTER the response map is constructed, iff `diag.OmittedTotal >= 1`. T003–T007 all GREEN; goldens byte-identical.

## Phase 5: Registrations & cross-surface (FR-007/009/010)

- [ ] T009 Update `internal/server/mcp_menu_surface_test.go` FIRST (controlled delta, RED): keep the pre-feature registration golden frozen; widen assertions to permit exactly the three default filter params + description changes on all three registrations; add a deep-compare test that the helper-produced filter parameter schemas are identical across the three registrations; assert every registration description contains the literal caveat sentence adopted for FR-009 — fix it here as: `Filter diagnostics describe this call's candidate window, not the whole catalog.` — and the token `filter_diagnostics`. Observe RED.
- [ ] T010 Implement `retrieveToolsAnnotationFilterOptions()` in `internal/server/mcp.go` (beside `retrieveToolsDetailOption`); use it in the default registration and BOTH routing builders in `internal/server/mcp_routing.go` (replacing their duplicated definitions); update all three descriptions with the FR-009 mention + the exact caveat sentence from T009. T009 GREEN.
- [ ] T011 Cross-surface tests (may be written RED before or observed GREEN after — same file rule: sequential in `mcp_filter_diagnostics_test.go`): diagnostics via the code-execution surface (`handleRetrieveToolsForMode` with `config.RoutingModeCodeExecution`, forced-full output) match the default surface's block; full-vs-compact identical-block assertion on the default and call-tool surfaces.
- [ ] T012 Add a diagnostics-active case (filters + omissions) to `internal/server/toon_surface_isolation_test.go` so the new block is inside the TOON byte-comparison.

## Phase 6: Gates & live verification

- [ ] T013 Execute quickstart.md live scenario end-to-end on an isolated instance (port 18972, scratch dirs, PID cleanup) and record the four observed responses for the PR description. Grep `read_only_only` under `docs/` — if any page documents retrieve_tools parameters, update it to mention `filter_diagnostics`.
- [ ] T014 FINAL gate (after every prior task, including any T013 doc edits): `go test -race ./internal/server/...`, full `go test -race ./internal/...`, and `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`. Fix findings; re-run until clean.

## Dependencies

Strictly sequential except where noted: T001→T002 → T003→T004→T005→T006→T007 (same test file — no parallelism) → T008 → T009→T010 → T011→T012 → T013 → T014. T012 may run in parallel with T011 (different files). No other [P] pairs — T004/T006 and T011 share `mcp_filter_diagnostics_test.go`.

## Implementation strategy

Single PR; one cohesive small diff. The RED wall (Phase 3) is written entirely against pre-feature behavior so every assertion is observed failing for the right reason before T008 exists; the pre-feature goldens captured in T003 make SC-002 a byte-level guarantee rather than a key-absence check.
