# Tasks: Discovery Effectiveness Profiler (mcp-discovery-bench)

**Input**: Design documents from `/specs/083-discovery-profiler/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md
**Tests**: Included — the constitution mandates TDD (write the failing test first within each task pair).

## Phase 1: Setup

- [ ] T001 Add Go dependency `github.com/toon-format/toon-go@v0.0.0-20251202084852-7ca0e27c4e8c` to go.mod (bench-only import) and verify `go list -deps ./cmd/mcpproxy | grep -c toon` returns 0 (dependency must not enter production binaries)
- [ ] T002 [P] Create pinned TSCG shim package: bench/tscg/package.json (`@tscg/core@1.4.3`), bench/tscg/package-lock.json (via `npm i --package-lock-only`), bench/tscg/shim.mjs implementing JSONL protocol `{"tool_id","tool"}` in → `{"tool_id","encoded"}`/`{"tool_id","error"}` out per research.md D3
- [ ] T003 [P] Add `bench-discovery` Makefile target: `npm ci --prefix bench/tscg` + offline arm run per quickstart.md (SC-008); CI will call this target
- [ ] T004 [P] Add `bench/results/cache/` to .gitignore (ToolRet cache must never be committable, FR-013)

## Phase 2: Foundational (blocking prerequisites)

- [ ] T005 Write failing tests for canonical baseline renderer in bench/arms/baseline_test.go: determinism (two runs byte-equal), canonical JSON key order, parity with existing `CountToolWithSchema` text shape (research D7b)
- [ ] T006 Implement Arm interface + registry in bench/arms/arm.go per contracts/arm-interface.md: `Name()`, `IndexAltering()`, `EncodeTool`, `EncodeListing`, `EncodeIndexMetadata`, `ErrArmUnavailable`, `LowerBound` self-report; registry rejects duplicate names
- [ ] T007 Implement `baseline_json` arm in bench/arms/baseline.go (canonical full-definition renderer: name + description + canonical-JSON schema) making T005 pass; export the renderer for naive-menu/break-even reuse
- [ ] T008 [P] Define ReportV2 Go types in bench/reportv2.go mirroring contracts/report-v2.schema.json (flat RetrievalScore DTO + mapping from existing `RetrievalMetrics`; provenance map; arms/corpora/response_cost/break_even/session_estimates/lap/subset sections) with a schema-validation test in bench/reportv2_test.go validating a sample report against the contract file
- [x] T009 [P] Create corpus_v2 generator script scripts/gen-corpus-v2.sh (boot snapshot proxy per quickstart, export GET /api/v1/tools with full input schemas, canonicalize ordering, write specs/083-discovery-profiler/datasets/corpus_v2.tools.json) and run it once; commit the generated corpus with tool count recorded in datasets/README.md
- [ ] T010 Extend bench/tokens.go to load corpus_v2 (schema-bearing) alongside corpus_v1, with validation test in bench/tokens_test.go (every corpus_v2 tool has non-empty schema; count matches datasets/README.md)

## Phase 3: User Story 1 — Measure live retrieve_tools response cost (P1) 🎯 MVP

**Goal**: per-query response tokens over real MCP protocol + component breakdown + break-even.
**Independent test**: live run against snapshot proxy produces report with per-query tokens, p50/p95/max, component sums == totals, break-even count.

- [ ] T011 [P] [US1] Write failing tests for span-based component attribution in bench/respcost_test.go: fixture retrieve_tools response → components sum EXACTLY to total tokens; known spans land in correct buckets (input_schemas/descriptions/usage_instructions/metadata/other)
- [ ] T012 [US1] Implement span-based attribution in bench/respcost.go: partition canonical response text into labeled byte spans, tokenize once, attribute each token to span owning its starting byte (research D7b); plus percentile aggregation (reuse existing percentile helper from bench/live_report.go)
- [ ] T013 [P] [US1] Write failing tests for break-even math in bench/breakeven_test.go: formula `(naive − proxy_menu) / mean_response`, no-break-even case (numerator ≤ 0), inputs echoed
- [ ] T014 [US1] Implement bench/breakeven.go making T013 pass
- [ ] T015 [US1] Implement real MCP retrieve_tools invocation in bench/mcpcall.go using mark3labs/mcp-go client against the proxy `/mcp` endpoint (initialize session, call retrieve_tools per golden query, capture full text content + client latency); integration test bench/mcpcall_test.go behind a `-live` guard
- [ ] T016 [US1] Wire response-cost measurement into RunLive in bench/live_report.go: per-golden-query MCP calls → DiscoveryResponseMeasurement rows → ResponseCostSummary + BreakEvenAnalysis in ReportV2; record proxy version/tool count/tools_limit/routing_mode (FR-021)

**Checkpoint**: `go run ./bench/cmd/bench -live ...` answers SC-001 (a)(b)(c).

## Phase 4: User Story 2 — Compare encoding arms (P1)

**Goal**: all mandatory arms measured on corpus_v2 with savings % + arm-aware retrieval quality.
**Independent test**: offline run with `-arms all` produces per-arm rows; baseline arm reproduces recall@5 = 0.68 ± 0.05 on golden set (SC-003).

- [ ] T017 [P] [US2] Write failing golden-output determinism tests for compact_sig arm in bench/arms/compact_test.go (first 3 corpus_v2 tools → committed expected bytes in bench/arms/testdata/; required/optional distinction; description preserved)
- [ ] T018 [US2] Implement `compact_sig` arm in bench/arms/compact.go: `name(param:type, opt?:type)|description` per contracts/arm-interface.md, `EncodeIndexMetadata` mapping params text to ParamsJSON replacement
- [ ] T019 [P] [US2] Implement `tscg` arm in bench/arms/tscg.go: spawn `node bench/tscg/shim.mjs`, JSONL exchange keyed by tool_id, `ErrArmUnavailable` when node/`node_modules` absent; determinism test with committed golden output in bench/arms/tscg_test.go (skips locally when node absent, never in CI)
- [ ] T020 [P] [US2] Implement `toon_listing` arm in bench/arms/toon.go using toon-go with golden-output test bench/arms/toon_test.go
- [ ] T021 [P] [US2] Implement optional `tron_dedup` arm in bench/arms/tron.go (named-class schema dedup: identical schema shapes declared once, referenced by class name; amortized in EncodeListing) with golden-output test; if infeasible within budget, register as not-yet-measured row per FR-006
- [ ] T022 [US2] Implement two-sided IndexAltering contract test in bench/arms/contract_test.go: diff `EncodeIndexMetadata` output vs baseline on corpus_v2 for every registered arm (under-declaration AND over-declaration fail) per contracts/arm-interface.md
- [ ] T023 [US2] Implement arm index builder in bench/armindex.go: temp-dir index via production `internal/index.Manager.BatchIndexTools` fed from `EncodeIndexMetadata`, returning a SearchFunc for `ScoreRetrieval`; parity test bench/armindex_test.go proving baseline arm reproduces golden-set baseline recall@5 = 0.68 ± 0.05 (SC-003 gate)
- [ ] T024 [US2] Implement arm runner + ArmResult assembly in bench/armrun.go: per-arm token stats (total/mean/p95), savings vs baseline, skip counting with examples, heaviest-tools top-N, degenerate-description count (rules per FR-020), quality attach for index-altering arms; unit tests in bench/armrun_test.go
- [ ] T025 [US2] Add `-arms`, `-corpus-v2` flags to bench/cmd/bench/main.go wiring offline arm runs into ReportV2 output

**Checkpoint**: SC-002 verifiable offline; compact-sig sanity check vs ~92% renders with explanation field.

## Phase 5: User Story 3 — Public corpora (P2)

**Goal**: ToolRet retrieval-quality end-to-end; LiveMCPTool token/scale.
**Independent test**: fetch → load → subset → score ToolRet; load committed LiveMCPTool snapshot → arm tokens.

- [x] T026 [P] [US3] Create scripts/fetch-toolret.sh: `uv run --with huggingface_hub,pyarrow` download of mangopy/ToolRet-Tools + mangopy/ToolRet-Queries at pinned `--revision` (default recorded in script), parquet→JSON into bench/results/cache/toolret/<revision>/ with actionable errors (FR-013, research D5)
- [ ] T027 [US3] Write failing loader tests in bench/corpusio/toolret_test.go on a small committed synthetic fixture matching ToolRet's field shape (NOT real ToolRet data): per-record validation errors, stable-ID sort, seeded subset determinism (same revision+seed+size ⇒ same subset), missing-ID failure
- [ ] T028 [US3] Implement bench/corpusio/toolret.go making T027 pass, mapping tools→Corpus and queries→GoldenSet with corpus/version stamping (FR-011/012/014)
- [ ] T029 [P] [US3] Create LiveMCPTool committed snapshot under specs/083-discovery-profiler/datasets/livemcptool_snapshot/ with ATTRIBUTION.md (Apache 2.0, ICIP/LiveMCPBench, arXiv:2508.01780) via a documented one-time download; implement + test loader bench/corpusio/livemcptool.go (Corpus for token/scale; relevance-label derivation from task annotations as stretch — if labels not derivable, record explicit absence per FR-011)
- [ ] T030 [US3] Add `-toolret`, `-livemcptool`, `-subset`, `-seed` flags to bench/cmd/bench/main.go; ToolRet subset retrieval scoring through armindex path; corpus rows (with license/attribution/committed fields) into ReportV2

**Checkpoint**: SC-004 satisfiable; no ToolRet bytes in git (verify with `git status --porcelain` in test).

## Phase 6: User Story 4 — LAP independent verdict (P2)

**Goal**: pinned LAP run in CI, artifact archived, divergence check non-blocking.
**Independent test**: booted proxy + `uvx --from lap-score==0.8.0 lap lint --json` → lap.json parsed, divergence computed, warning path exercised.

- [ ] T031 [P] [US4] Write failing tests for LAP verdict parsing + divergence in bench/lapcheck_test.go: fixture lap.json → LapVerdict; divergence > ±15% ⇒ warning flag; missing file ⇒ Executed=false with skip reason (FR-015/016, SC-006)
- [ ] T032 [US4] Implement bench/lapcheck.go making T031 pass and wire `-lap-json <path>` flag into bench/cmd/bench/main.go, merging LapVerdict into ReportV2

## Phase 7: User Story 5 — Public-ready report (P3)

**Goal**: self-contained dashboard + estimator with provenance everywhere.
**Independent test**: generate dashboard from a run; every headline number carries provenance badge + tokenizer caveat; no external requests.

- [ ] T033 [P] [US5] Write failing tests for session estimator in bench/session_test.go: `session_cost = proxy_menu + calls × mean_response(arm) × (1 + retry_rate(arm))` for calls ∈ {1,3,5,10}, per-arm documented retry defaults (research D8), provenance=estimated
- [ ] T034 [US5] Implement bench/session.go making T033 pass; SessionCostEstimate rows into ReportV2
- [ ] T035 [US5] Extend dashboard template in bench/report.go: arms table, corpora table, response-cost percentiles, break-even, session estimates, LAP row, provenance badges (measured/computed/estimated), tokenizer caveat banner; assert self-containment in bench/report_test.go (rendered HTML contains no `http://`/`https://` resource loads)
- [ ] T036 [US5] Report-validator test in bench/reportv2_test.go: full ReportV2 from a real offline run validates against contracts/report-v2.schema.json including conditional rules (non-skipped requires tokens; results rows require fixture fields; index-altering requires quality key)

## Phase 8: TOON-results arm (US2 remainder, needs fixtures)

- [ ] T037 [P] [US2] Capture datasets/result_fixtures_v1.json once from reference servers (filesystem list, git log, sqlite query, time now, fetch of committed HTML fixture) with tabular/non-tabular classification and fixture snapshot ID; document capture procedure in datasets/README.md (FR-007, research D10)
- [ ] T038 [US2] Implement `toon_results` arm rows in bench/arms/toon.go over the fixture set (payload_class=results, fixture_id, tabular/non-tabular split vs compact-JSON baseline of same payloads) with tests in bench/arms/toon_results_test.go

## Phase 9: Polish & CI

- [x] T039 Extend .github/workflows/bench.yml: setup-node + `npm ci --prefix bench/tscg`, run `make bench-discovery`, live run with response-cost measurement, `uvx --from lap-score==0.8.0 lap lint --json` step (pinned; failure → skip with reason), upload lap.json + report.json + dashboard.html artifacts; job stays `continue-on-error: true` (FR-022); add optional `workflow_dispatch` ToolRet-subset job (< 30 min, SC-007)
- [x] T040 [P] Update bench/README.md: new modes/flags, arm descriptions, dataset provenance + license notes (ToolRet unstated-license rationale), tokenizer caveat, quickstart parity with specs/083-discovery-profiler/quickstart.md
- [ ] T041 Run full verification: `go test ./bench/... -race`, `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./bench/...`, `./scripts/test-api-e2e.sh`, `make bench-discovery` wall-clock < 15 min budget check (SC-007/SC-008)

## Dependencies

- Phase 2 blocks everything (T006/T007 are the arm substrate; T008 the report substrate; T009/T010 the corpus substrate).
- US1 (T011–T016) and US2 (T017–T025) are independent of each other after Phase 2; both are P1.
- US3 (T026–T030) needs T023/T024 (armindex + armrun) for scoring; loaders themselves independent.
- US4 (T031–T032) independent after T008.
- US5 (T033–T036) needs data producers (US1/US2 minimally) for a real-run validation.
- Phase 8 needs T020 (toon.go) + T037 fixtures.
- T039 (CI) last-but-one; T041 final gate.

## Parallel execution examples

- After Phase 2: {T011,T013} ∥ {T017,T019,T020,T021} ∥ T026 ∥ T031 ∥ T033 (different files, no shared state).
- Within US2: compact/tscg/toon/tron arms (T018–T021) are separate files — parallelizable across agents; T022–T024 join them.

## Implementation strategy

MVP = Phase 1 + 2 + US1 (T001–T016): answers the headline questions (response cost, break-even) on the live proxy. Then US2 delivers the experiment engine, US3 external validity, US4 independence, US5 the publishable artifact. Each checkpoint is independently demoable.
