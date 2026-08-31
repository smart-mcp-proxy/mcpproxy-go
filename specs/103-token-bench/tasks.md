# Tasks: Token-efficiency benchmark — measured savings, published results

**Input**: Design documents from `/specs/103-token-bench/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: INCLUDED. The project constitution (Principle V) requires tests before implementation
for all features, so test tasks are not optional here.

**Organization**: Grouped by user story. US1 alone is a shippable MVP.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: US1–US5, mapping to spec.md's user stories

## Path Conventions

Single Go project. The feature lives in the existing `bench/` tree, with two small production
changes in `internal/`. Paths below are repo-relative and exact.

---

## Phase 1: Setup

**Purpose**: Make the deterministic half reproducible before anything depends on it.

- [x] T001 Set `TIKTOKEN_CACHE_DIR` in `.github/workflows/bench.yml` AND add a cache
      restore/save step so the vocabulary is present offline — setting the variable alone only
      names a cache, it does not fill one (research.md "Cross-cutting: reproducibility hazards")
- [x] T002 [P] Add a PR-triggered required workflow step asserting no report is tracked:
      `test -z "$(git ls-files bench/results)"`. It must NOT live in `.github/workflows/bench.yml`,
      which triggers only on `v*` tags and `workflow_dispatch` and so cannot block a PR (SC-011)
- [x] T003 [P] Document the replay + agent-loop entry points in `bench/README.md`, including the
      mandatory fleet input and the bodies-off/bodies-on distinction (Principle VI)

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: Contract and report groundwork every story depends on. **No user story can start
until this phase completes.**

**Production changes (both small, both required before US1 can be honest). Tests first:**

- [x] T004 Write a FAILING test asserting the export projection carries both byte fields, in
      `internal/httpapi/activity_test.go` (TDD, Principle V — must precede the DTO change below)
- [x] T005 Write a FAILING test asserting a truncated `retrieve_tools` call produces an activity
      record flagged truncated. Create `internal/server/mcp_activity_truncation_test.go` — no
      such file exists today (must precede the truncation-propagation task below)
- [x] T006 Add `RequestBytes` / `ResponseBytes` to the export DTO in
      `internal/contracts/activity.go` and copy them in `storageToContractActivityForExport`
      in `internal/httpapi/activity.go`. They exist on the storage record as pre-truncation
      measurements but never reach the export (contracts/replay-input.md). Turns the export-projection test green
- [x] T007 Propagate the already-computed `wasTruncated` into the internal tool-call activity
      emit: extend `emitActivityInternalToolCall` in `internal/server/mcp.go:810` and pass the
      value the `retrieve_tools` handler already holds at `internal/server/mcp.go:2019-2035`.
      **Without this a truncated `retrieve_tools` record cannot be identified and the loader
      would silently overstate agent cost.** If rejected, record the blanket-exclusion fallback
      decision in `bench/README.md` — it must be explicit, never a default. Turns the truncation test green

**Report envelope (additive, no `report_version` bump). Tests first:**

- [x] T008 Write FAILING tests in `bench/reportv2_test.go` asserting: the new blocks validate
      against the schema; every emitted block carries a populated `accounting_source`; and each
      new row type carries a `provenance` value from the closed enum. A field that exists but is
      never set does not satisfy the contract
- [x] T009 Add a `provenance` field to every new row type and constrain it to
      `measured|computed|estimated` in `bench/reportv2.go`. Section-level provenance cannot
      express FR-013, which lets measured and estimated figures coexist in one block
- [x] T010 Add a per-block `accounting_source` field in `bench/reportv2.go`. **Do NOT narrow
      the existing document-level `Tokenizer` field** — that would be a meaning change requiring
      a version bump (contracts/report-v2-additions.md). Same file as the provenance task, so
      not parallel
- [x] T011 Declare the new optional blocks (`replay`, `agent_loop`, `payload_decomposition`) in
      `specs/083-discovery-profiler/contracts/report-v2.schema.json`. The schema has no
      `additionalProperties: false`, so an undeclared block would validate silently — declare
      them anyway, the schema file is the reviewed contract

**Mode matrix:**

- [x] T012 Write FAILING tests for the matrix in `bench/modematrix_test.go`: exactly 5 distinct
      cells; the 7 redundant combinations each produce a skip row naming the cell it collapses
      onto; `code_execution` + `enable_code_execution:false` yields reason `degenerate`
- [x] T013 Write FAILING tests in `bench/modematrix_test.go` for the FR-016 capability
      conditions — batching, stored scripts and validate-before-dispatch as BINARY conditions
      over the cells where each is available, with the report enumerating applicable rows.
      Same file as the task above, so not parallel
- [x] T014 Implement `bench/modematrix.go` per `contracts/mode-matrix.md`, including the FR-016
      capability conditions. **Compose existing pieces through structural interfaces — do not
      re-derive any serialization.** Reuse mode constants and proxy catalogs
      (`bench/tokens.go`), full rendering (`bench/arms/baseline.go`), compact measurement
      (`bench/flipgate.go`), deferred-direct rendering (`bench/arms/directdeferred.go`), MCP
      transport (`bench/mcpcaller.go`). Reuse the existing skip shape
      (`ArmResult.Skipped`/`SkipReason`, `SkippedArmResult`)
- [x] T015 Map a cell to its endpoint URL in `bench/modematrix.go`. The routing-mode axis needs
      no config change — all three routing-mode servers are mounted at startup
      (`internal/server/server.go:2514-2536`). The two serialization axes need config but both
      hot-reload, so the matrix crosses on one long-lived instance with a config apply between
      serialization cells

**Checkpoint**: Contracts, report envelope and matrix ready — user stories can begin.

---

## Phase 3: User Story 1 — Recompute real workload cost (Priority: P1) 🎯 MVP

**Goal**: Given exported activity plus a fleet input, report per-cell menu cost and the
direct-cell cross-mode delta for a real workload shape. No model, deterministic.

**Independent Test**: Supply an export and a frozen corpus; confirm per-cell menu cost and the
`direct_full` vs `direct_deferred` delta appear, each badged `measured`, with an exclusion
report beside them — and that two runs are identical modulo `generated_at`.

### Tests for User Story 1 ⚠️ (write first, confirm they fail)

- [x] T016 [P] [US1] Loader tests in `bench/replaycorpus/load_test.go`: JSONL decoding; CSV
      rejected; grouping by `work_session_id`; `parent_id` ↔ `request_id` join for
      code-execution sub-calls
- [x] T017 [P] [US1] Usability-flag tests in `bench/replaycorpus/flags_test.go`: truncated,
      bodies-missing, sensitive and unreplayable are each detected, and every exclusion is
      counted — **assert a truncated record never contributes silently** (FR-002, SC-008)
- [x] T018 [P] [US1] Privacy tests in `bench/replaycorpus/privacy_test.go`: no body text escapes
      the package; tokenization happens inside the loader and only counts cross the boundary;
      **bodies-off is the DEFAULT**; bodies-on requires an explicit opt-in and emits a warning
      (FR-006, SC-009, contracts/replay-input.md)
- [x] T019 [US1] In `bench/replay_test.go`: a recording-only invocation (no fleet input) is a
      hard error, not a degraded run
- [x] T020 [US1] In `bench/replay_test.go`: no cell reports an absolute complete-workload cost
      bodies-off, and only the `direct_full` vs `direct_deferred` delta is produced. Same file
      as the task above, so not parallel
- [x] T021 [US1] In `bench/replay_test.go`: two runs over the same inputs are byte-identical
      after `generated_at` is excluded (SC-002). Same file as the task above, so not parallel

### Implementation for User Story 1

- [x] T022 [P] [US1] Create `bench/replaycorpus/load.go` — decode `contracts.ActivityRecord`
      JSONL from `mcpproxy activity export --format json`. **This package MUST import nothing
      from `bench`**, or `bench/replay.go` cannot import it without a cycle (`bench/corpusio`
      imports `bench`, which is why mirroring it would fail)
- [x] T023 [P] [US1] Create `bench/replaycorpus/group.go` — group by `work_session_id`, join
      code-execution sub-calls via `parent_id` ↔ `request_id` so they are neither
      double-counted nor orphaned
- [x] T024 [US1] Create `bench/replaycorpus/flags.go` — compute usability flags once at load.
      Handle the two byte-coverage gaps: internal `retrieve_tools` records carry no byte counts,
      and code-execution sub-calls emit both as zero
      (`internal/server/mcp_code_execution.go:811-818`); both fall to exclusion accounting
- [x] T025 [US1] Enforce the privacy posture in `bench/replaycorpus/load.go`: bodies-off by
      DEFAULT, bodies-on only via explicit opt-in that prints a warning, and refuse an input
      path inside the repository working tree (replay inputs live outside it and are never
      committed). Turns the privacy test green
- [x] T026 [US1] In `bench/replaycorpus/tokenize.go`, tokenize inside the loader for bodies-on
      runs and emit counts only, so no text crosses the boundary. Exclude (or re-apply
      truncation to) truncated `retrieve_tools` records — the log stores the FULL pre-truncation
      response while the agent consumed truncated text, so tokenizing it as-is would OVERSTATE
      cost
- [x] T027 [US1] Create `bench/replay.go` — per-cell menu cost from the fleet input, plus the
      direct-cell delta. Reuse `arms.CanonicalToolText` / `CountToolWithSchema` for the tool
      surface and the `bench/respcost.go` span-partition technique for responses, so components
      sum exactly. Populate the `replay` block's `accounting_source` as the deterministic
      tokenizer
- [x] T028 [US1] Add the `-replay` flag plus a mandatory fleet input to `bench/cmd/bench/main.go`
      as a top-level branch beside the profiler check, emitting a `replay` block into `ReportV2`
      (not an `OfflineSection` — replay crosses the matrix, it is not one more corpus)
- [x] T029 [US1] Exclude or pin `generated_at` for replay reports in `bench/replay.go` so
      SC-002's determinism check is meaningful
- [x] T030 [US1] Write a FAILING assertion in `bench/replay_test.go`: no replay figure may be
      emitted without the counterfactual marker (FR-004)
- [x] T031 [US1] Label every replay figure as a COUNTERFACTUAL over recorded traffic, not
      observed agent behaviour, in `bench/replay.go` — FR-004 forbids presenting it as observed,
      and the Replay Boundary is the spec's most load-bearing constraint. Turns the assertion
      above green
- [x] T032 [US1] Render the `replay` block in the dashboard template in `bench/report.go`,
      showing the exclusion report, the counterfactual label, and that figures are scored against
      **today's** fleet, not the fleet as recorded
- [x] T033 [US1] Document in `bench/README.md` how to delete a replay input when finished
      (contracts/replay-input.md privacy rule 6)

**Checkpoint**: US1 is independently shippable and delivers real-workload numbers with no model spend.

---

## Phase 4: User Story 2 — Measure whether a mode helps the agent succeed (Priority: P1)

**Goal**: Tokens per *completed* task, completion rate, first-attempt success and retry rates
per cell, from a live agent loop under a pinned model.

**Independent Test**: Run a fixed task set under at least two cells; confirm all four figures
appear per cell, badged `measured`, averaged over k≥4 runs with spread.

**⚠️ Blocked on an operator decision**: pinned model + spend ceiling. US1 does not depend on this.

### Tests for User Story 2 ⚠️

- [ ] T034 [US2] Classification tests in `bench/agentloop_test.go` for the binding Definitions:
      first-attempt success, corrective vs infrastructure retry, unit of work, task completion.
      **The same rule must apply to the baseline arm and the proxy arms**, or the comparison is
      biased toward whichever carries richer error signal
- [ ] T035 [US2] In `bench/agentloop_test.go`: `completion: no-signal` excludes a record from
      completion-dependent figures rather than counting it as success or failure. Same file as
      the task above, so not parallel
- [ ] T036 [US2] In `bench/agentloop_test.go`: a model-dependent figure with `runs < 4` is
      refused as a headline (FR-021)
- [ ] T037 [US2] In `bench/agentloop_test.go`: a mode with lower token cost but lower completion
      is flagged a regression by the completion threshold and is NOT reported as a saving
      (SC-007) — drive it with a deliberately degraded mode
- [ ] T038 [US2] In `bench/agentloop_test.go`: a cross-accounting-source aggregate is WITHHELD
      with a stated reason, never computed, mirroring the existing
      `AuthoritativeHeadline`/`withholdHeadline` pattern in `bench/live_report.go`

### Implementation for User Story 2

- [ ] T039 [US2] Create `bench/agentloop.go` — driver parameterised over a function type so the
      arithmetic is unit-testable with no suite running, following the `bench/flipgate.go`
      precedent
- [ ] T040 [US2] Implement the FR-020 BASELINE ARM in `bench/agentloop.go`: the same agent
      running the same tasks with every upstream tool loaded directly, **bypassing mcpproxy
      entirely**. This is the denominator every published percentage is measured against, and
      without it no saving can be quoted. It is NOT the deterministic `baseline` renderer used
      by the offline arms
- [ ] T041 [US2] Ingest the suite's per-task `meta.json` (`execution_result.success`,
      `token_usage`, `turn_count`) and `messages.json` trajectories for retry classification
- [ ] T042 [US2] Capture provider-reported `usage` in the driver for the input/output/cache-read
      split. **The suite's own output has no cache-read field**, so that axis must come from the
      driver or be explicitly declared out of reach (research.md)
- [ ] T043 [US2] Emit the `agent_loop` block from `bench/agentloop.go` with its own populated
      `accounting_source` (provider + pinned model), never summed with tokenizer-sourced figures
- [ ] T044 [US2] Write a FAILING test in `bench/session_test.go`: where a measured retry rate
      exists it supersedes the assumed default, and where none exists the row stays badged
      `estimated`. `RetryRateForArm` returns `0.0` for unknown arms (`bench/session.go:49-62`),
      indistinguishable from a measured `0.0` — this test is what prevents the silent mix
- [ ] T045 [US2] Implement FR-013 in `bench/session.go`: measured success and retry rates
      supersede the literature-derived `armRetryRates` defaults wherever a measurement exists,
      and add a per-row provenance field to session-cost rows so measured and estimated rows are
      distinguishable inside one table
- [ ] T046 [US2] Implement the FR-023 COST-VERSUS-OUTCOME VIEW in `bench/report.go`: cost
      plotted against completion outcome per cell, so a reader sees which modes are worth their
      savings rather than only which are cheapest. Completion rate must sit beside cost at equal
      prominence (FR-018)
- [ ] T047 [US2] Patch the pinned suite's single MCP factory — `src/agents/mcpmark_agent.py`
      `_create_mcp_server()` in the external MCPMark clone, NOT a file in this repo — with an
      env-gated branch returning an HTTP MCP server at mcpproxy's URL with the API-key header.
      Pin by commit SHA and record the SHA in `bench/README.md` (FR-028)
- [ ] T048 [US2] In the benchmark's mcpproxy config (a scratch `mcp_config.json`, never
      committed), configure ALL suite services simultaneously while running one service's tasks
      — a single server's toolset is too small a fleet to show the asymptote, and the full fleet
      is also the honest FR-020 baseline
- [ ] T049 [US2] RUN the public suite against mcpproxy across at least two mode cells plus the
      baseline arm, at k>=4 runs each, and record comparable results (SC-010). Start with the
      credential-free core (filesystem + postgres, 51 local tasks); any service performing real
      writes must be run so it cannot damage real data (FR-027). Record the executed scope and
      the pinned model in `bench/README.md`

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 4 — Explain the 29.7% shortfall (Priority: P2)

**Goal**: Attribute tool-definition payload to names, descriptions, annotations and schemas
across fleet shapes, and confirm or correct spec 102's conclusion.

**Independent Test**: Produce a decomposition for at least two fleet shapes with a ceiling
recomputed per corpus and an explicit `confirmed`/`corrected` verdict.

### Tests for User Story 4 ⚠️

- [x] T050 [US4] In `bench/payloaddecomp_test.go`: the four shares sum to the whole payload
- [x] T051 [US4] In `bench/payloaddecomp_test.go`: the achievable ceiling is recomputed per
      corpus and never carried forward as a constant — that carry-forward is precisely the error
      spec 102 made. Same file as the task above, so not parallel

### Implementation for User Story 4

- [x] T052 [US4] Implement the decomposition in `bench/payloaddecomp.go` over at least two fleet
      shapes (the 45-tool reference corpus and the 527-tool snapshot)
- [ ] T053 [US4] Emit the `payload_decomposition` block from `bench/payloaddecomp.go` with a
      populated `accounting_source` and an explicit `spec102_verdict` (`confirmed` or
      `corrected`, with the delta)

**Checkpoint**: The shortfall has an evidenced answer.

---

## Phase 6: User Story 3 — An outsider reproduces the numbers (Priority: P2)

**Goal**: Every published figure is reproducible from a documented, pinned procedure.

**Independent Test**: Someone with no prior context follows the procedure and reproduces the
deterministic figures exactly, the model-dependent ones within the stated tolerance.

- [ ] T054 [US3] Write FAILING tests in `bench/reportv2_test.go` for the US3 report behaviours:
      a private-session-derived figure is marked not-independently-reproducible; a partial run is
      marked partial and refused for publication
- [ ] T055 [US3] Measure the tokenizer's divergence from the pinned model with the
      provider's token-counting endpoint (no inference spend) and record it in `bench/README.md`
      as FR-022's numeric tolerance. **The repo currently states ~60% while the provider's
      guidance says ~15–20%** — neither is sourced, and they differ threefold
- [ ] T056 [US3] In `bench/reportv2.go`, mark figures derived from private recorded sessions as
      NOT independently reproducible; they must never be the sole support for a published claim
      (FR-030)
- [ ] T057 [US3] In `bench/replay.go` and `bench/agentloop.go`, retain raw per-run records under
      the gitignored `bench/results/` and reference them from the report by RUN-LOCAL path, with
      an explicit note that they are not durable across a results cleanup. A report reference
      must degrade to "records not retained" rather than dangling (FR-029, and consistent with
      SC-011's never-committed rule)
- [ ] T058 [US3] In `bench/reportv2.go`, mark partial or interrupted runs as partial and block
      them from publication (FR-032)
- [ ] T059 [US3] Write the reproduction procedure into `bench/README.md`, including warming the
      tokenizer cache and supplying a fleet input

**Checkpoint**: The numbers survive outside scrutiny.

---

## Phase 7: User Story 5 — Publish (Priority: P3)

**Goal**: Results reach developers deciding whether to adopt.

- [ ] T060 [US5] Draft the write-up for `mcpproxy.app/blog` (separate repository): measured
      figures, the fleet shapes they hold for, known limitations, the reproduction procedure,
      and **plainly where savings do not materialise** (FR-031, SC-012). Extend the existing
      2026-03-19 "BM25 vs Embeddings vs Lua" post rather than duplicating it

---

## Phase 8: Polish & Cross-Cutting

- [ ] T061 [P] Compare the Web-UI/status savings figure (`internal/server/tokens/savings.go`,
      surfaced via `internal/runtime/runtime.go`) against the bench headline over one live
      fleet. They are independently implemented over the same data and have never been compared;
      the spec's own Assumptions say a contradiction is a finding to investigate
- [ ] T062 [P] Verify no generated report became tracked: `git ls-files bench/results` is empty
- [ ] T063 Full gates: `go build ./cmd/mcpproxy` and `-tags server`;
      `go test -race -count=1 ./internal/... ./bench/...`;
      `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`;
      `./scripts/test-api-e2e.sh`; `make swagger` diff-clean
- [ ] T064 Cross-model review of the full diff (opencode `gpt-5.6-sol`) staged into
      `.review-tmp/`, ≤10 rounds per the standing cap; verify each finding against the tree
      before fixing

---

## Dependencies

```text
Phase 1 (Setup) ──► Phase 2 (Foundational) ──┬──► Phase 3 US1 (P1) ──► Phase 5 US4 (P2)
                                             │                    └──► Phase 6 US3 (P2)
                                             └──► Phase 4 US2 (P1) ──► Phase 6 US3 (P2)
                                                                        │
                                                                        └──► Phase 7 US5 (P3)
                                                                                 │
                                                                        Phase 8 Polish
```

- **US1 and US2 are independent of each other** and can proceed in parallel once Phase 2 lands.
- **US4 depends on US1** only for the fleet-input plumbing.
- **US3 depends on both P1 stories** having produced figures worth reproducing.
- **US5 depends on US3** — publishing before reproducibility is what this feature exists to avoid.

## Parallel opportunities

`[P]` means **different files**. Tasks editing the same file are deliberately NOT marked
parallel, even when logically independent — several markers were removed on review for exactly
that reason.

- **Phase 1**: the SC-011 gate and the README documentation task run together.
- **Phase 2**: the two failing backend tests run together (different test files). The two
  `bench/reportv2.go` tasks and the two `bench/modematrix_test.go` tasks are sequential.
- **US1**: the three `bench/replaycorpus/*_test.go` test tasks run together; everything touching
  `bench/replay_test.go` is sequential. The two loader implementation files run together.
- **US2**: everything in `bench/agentloop_test.go` is sequential.
- **US4**: both `bench/payloaddecomp_test.go` tasks are sequential.
- **US3**: nothing is parallel — the tokenizer-divergence task and the reproduction-procedure
  task both write `bench/README.md`.
- **Polish**: the savings-comparison and tracked-report checks run together.

## Implementation strategy

**MVP = Phase 1 + Phase 2 + Phase 3 (US1).** That yields real-workload menu costs and the
direct-cell delta with **zero model spend**, and it is independently useful: it replaces the
frozen-corpus numbers with numbers from real fleets.

**Increment 2 = US2**, gated on the pinned-model decision. This is where the feature's actual
claim — tokens per *completed* task — becomes measurable.

**Increment 3 = US4, then US3, then US5.**

## Warnings carried from the design

1. **Never let a truncated record contribute silently.** It understates cost *in the project's
   favour*, which is the failure this whole feature exists to prevent.
2. **Never sum across accounting sources.** Withhold with a stated reason instead.
3. **Never publish a percentage without its fleet shape.**
4. **Never present a bodies-off figure as an absolute complete-workload cost.** Bodies-off
   yields menu costs and one cross-mode delta.
5. **A recording-only run is an error**, not a degraded run — without a fleet input there is no
   menu to cost.
