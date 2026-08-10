# Tasks: retrieve_tools Filter Diagnostics

**Input**: Design documents from `/specs/094-filter-diagnostics/` (spec.md + plan.md Codex-APPROVED)
**Tests**: REQUIRED — constitution principle V (TDD) + CLAUDE.md test-driven progress. Test tasks precede implementation within each phase.

**Organization**: Foundational attribution seam first (everything depends on it), then user stories in priority order. The block's normative shape (FR-003) spans US1/US2, so the struct lands in Foundational and each story adds its observable behavior + tests.

## Phase 1: Setup

*(none — existing packages only, no new deps, no scaffolding)*

## Phase 2: Foundational (blocking)

- [ ] T001 Write the frozen-oracle parity test in `internal/server/mcp_annotations_test.go`: copy the CURRENT `shouldExclude` body verbatim into the test as `legacyShouldExcludeOracle`; table-drive all 224 cases (27 non-nil hint combos + nil-annotations, × 8 filter combos) asserting `excludeReason` returns (`excluded` == oracle, correct first-failure `filterKey` per read-only→destructive→open-world order incl. the read-only shortcut, correct `explicit` class per data-model.md). Test MUST fail to compile/pass until T002. (RED)
- [ ] T002 Implement `excludeReason(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (filterKey string, explicit, excluded bool)` in `internal/server/mcp_annotations.go` and reimplement `shouldExclude` as a delegation to it. All existing annotation tests + T001 green. (GREEN)
- [ ] T003 Add `reasonCounts` + `filterDiagnostics` structs and the two suggestion `const` templates (data-model.md shapes, JSON tags exact) in `internal/server/mcp_annotations.go`, plus `filterByAnnotationsWithDiagnostics(...) (kept []annotatedSearchResult, diag *filterDiagnostics)` building counts via `excludeReason` (map entry inserted only on first omission for that filter) and selecting the suggestion by missing-cause precedence (FR-006). Keep `filterByAnnotations` as a thin wrapper.

## Phase 3: US1 — Agent learns why matched tools were withheld (P1)

**Goal**: `filter_diagnostics` present exactly when an active filter omitted ≥1 candidate; counts correct. **Independent test**: quickstart.md live scenario (echo-rugpull fixture, `read_only_only=true`).

- [ ] T004 [US1] Write presence/absence + shape tests in NEW `internal/server/mcp_filter_diagnostics_test.go` (RED): (a) no filters → raw response bytes contain no `filter_diagnostics` key, full AND compact modes; (b) filters active, zero omissions → key absent, both modes; (c) filters active with omissions → key present, and deleting only that key from the serialized response reproduces the pre-feature filtered bytes; (d) exact raw-JSON shape test of a fixture block — alphabetical `omitted_by_filter` ordering, zero-count active filters absent, both reason fields serialized; (e) invariants `omitted_total == Σ counts` and `matched_before_filters == omitted_total + total`.
- [ ] T005 [US1] Wire diagnostics into `handleRetrieveTools` in `internal/server/mcp.go`: replace the `filterByAnnotations` call in the `annotationFilterActive` branch with `filterByAnnotationsWithDiagnostics`; attach `response["filter_diagnostics"] = diag` iff `diag.OmittedTotal >= 1`. T004 green. (GREEN)

## Phase 4: US2 — Missing-annotation vs explicitly-unsafe + actionable suggestion (P2)

**Goal**: reason split and suggestion selection observable per FR-004/FR-006. **Independent test**: mixed-reason fixture yields split counts and the annotations-suggestion; explicit-only fixture yields the inspect-suggestion.

- [ ] T006 [P] [US2] Write reason-split + suggestion tests in `internal/server/mcp_filter_diagnostics_test.go` (RED): mixed missing/explicit fixtures split correctly per filter; read-only shortcut visible in counts (explicit `readOnlyHint=true` tool never attributed to `exclude_destructive`); suggestion precedence (any missing → annotations template; all explicit → inspect template); all 7 non-empty filter subsets × both templates assert ≤200 chars, charset `[a-zA-Z0-9 .,:;()'_-]`, and no unrelated filter name.
- [ ] T007 [US2] Adjust suggestion construction in `internal/server/mcp_annotations.go` until T006 is green (templates are compile-time constants; filter names joined once, alphabetical). (GREEN)

## Phase 5: US3 — Zero results no longer reads as broken (P3)

**Goal**: total=0 + diagnostics coexist with the locked-tools flow without double counting.

- [ ] T008 [US3] Write zero-result + coexistence tests in `internal/server/mcp_filter_diagnostics_test.go` (RED→GREEN with no prod change expected; fix if red): (a) every match omitted → `total: 0`, `matched_before_filters == omitted_total`; (b) locked-only hits → NO diagnostics; (c) locked hit + annotation-filtered callable hit → both `notice` and `filter_diagnostics` present and consistent; repeat with `include_disabled=true` → `disabled`/`remediation` coexist; (d) profile/agent-scope-excluded tools do NOT contribute to `matched_before_filters`.

## Phase 6: Polish & cross-cutting (FR-007/009/010, SC-003/004)

- [ ] T009 Update `internal/server/mcp_menu_surface_test.go` FIRST (controlled delta, RED): keep the pre-feature golden frozen; widen assertions to permit exactly the three default filter params + description changes on all three registrations; add a deep-compare test that helper-produced filter schemas are identical across the three registrations and every description mentions `filter_diagnostics` + the candidate-window caveat.
- [ ] T010 Implement `retrieveToolsAnnotationFilterOptions()` in `internal/server/mcp.go` (beside `retrieveToolsDetailOption`); use it in the default registration and BOTH routing builders in `internal/server/mcp_routing.go` (replacing their duplicated definitions); update all three descriptions. T009 green. (GREEN)
- [ ] T011 [P] Add a diagnostics-active case (filters + omissions) to `internal/server/toon_surface_isolation_test.go` so the new block is inside the TOON byte-comparison.
- [ ] T012 [P] Add code-execution-surface test in `internal/server/mcp_filter_diagnostics_test.go`: diagnostics via `handleRetrieveToolsForMode(RoutingModeCodeExecution)` with forced-full output; plus full-vs-compact identical-block assertion on the default surface.
- [ ] T013 [P] Add SC-003 size test in `internal/server/mcp_filter_diagnostics_test.go`: maximal reachable fixture (matched=100, omitted=100, three nonzero filters, 200-char suggestion) serializes compact ≤500 bytes; assert the real suggestion constants conform to the FR-006 charset.
- [ ] T014 Run gates: `go test -race ./internal/server/...`, full `go test -race ./internal/...`, and `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`. Fix findings.
- [ ] T015 Execute quickstart.md live scenario end-to-end on an isolated instance (port 18972, scratch dirs, PID cleanup) and record the four observed responses in the PR description. Check whether any `docs/` page documents retrieve_tools parameters (grep `read_only_only` in docs/) and update it if so.

## Dependencies

- T001→T002→T003 strictly sequential (Foundational blocks everything).
- US1 (T004→T005) after T003. US2 (T006→T007) after T003; T006 parallel with T004. US3 (T008) after T005.
- Polish: T009→T010 after T005; T011/T012/T013 parallel after T005/T007; T014→T015 last.

## Implementation strategy

MVP = Foundational + US1 (block present with counts). US2/US3 are additional observable guarantees on the same block; Polish carries the registration gap + cross-cutting gates. Single PR — the feature is one cohesive ~small diff; phases order the TDD loop, not separate deliveries.
