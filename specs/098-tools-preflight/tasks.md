# Tasks: Required-Tools Preflight

**Input**: Design documents from `/specs/098-tools-preflight/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/preflight-api.yaml, quickstart.md
**Convention**: TDD — each implementation task lands with (or after) its failing test. Revised 2026-08-15 after cross-model plan review (opencode/gpt-5.6-sol): activity via storage+runtime seam (synchronous), profiles before route, no ForProfile use, ProfilePin propagation, swag-annotation OAS path, generator-source contracts.ts, dedicated wait semaphore, goldens from merge-base, dispatch primitives across all four dispatch paths.

## Phase 1: Setup

- [x] T001 Create `internal/preflight` package skeleton (`reasons.go`, `evaluator.go`, doc comment stating zero-I/O invariant and the error-not-fabricated-reason rule) per plan.md structure
- [x] T002 [P] Capture tools/list golden snapshots for all three routing modes **from merge-base (origin/main)** into `internal/server/testdata/toolslist_goldens/` + snapshot test `internal/server/toolslist_snapshot_test.go` comparing the feature branch byte-for-byte (FR-015; goldens must predate any dispatch refactor)

## Phase 2: Foundational (blocking all stories)

- [x] T003 Define the 15-code reason enum, classes, retryable defaults, action mapping (action omitted = ""), precedence chain, set-verdict + exit-code mapping in `internal/preflight/reasons.go` as single source of truth (data-model tables), with table-driven test `internal/preflight/reasons_test.go` asserting the full FR-003 table
- [x] T004 Define narrow read interfaces + `EvalContext`/`Result` types in `internal/preflight/evaluator.go` (`IndexReader`, `ApprovalReader`, `StateReader`, `ConfigPolicy`, tier, profile scope, filters, pins); `Evaluate` returns `([]Result, error)` — infra read errors are errors, never reason codes
- [x] T005 Extract the spec 094 annotation classifier (`excludeReason` logic) from `internal/server/mcp_annotations.go` into a shared lower-level package (`internal/toolannotations`) with `internal/server` delegating; unit tests moved/extended (unexported-symbol cycle fix, review finding 14)
- [x] T006 Implement `ClassifyTool` shared classification in `internal/preflight` (quarantine-enabled/skip flags honored; `changed` distinct from `pending`; `auto_approve_tool_changes` ⇒ ready) with unit tests covering the documented divergences (research D2)
- [x] T007 Implement `Evaluate` walking the FR-004 precedence chain, incl. PendingAuth→`oauth_required` explicit map, server-level `server_initializing`, pin check (`sha256/v{N}:{hex}`, schema-version aware), annotation slot via `internal/toolannotations`; table-driven tests: every enum cell + co-occurrence pairs per adjacent precedence pair in `internal/preflight/evaluator_test.go`
- [x] T008 Implement scope/tier disclosure: agent-token tier out-of-scope ⇒ byte-indistinguishable `not_found` (serialized-bytes comparison test); operator tier ⇒ `server_not_in_scope` with profile-session detail; profile semantics = shared index existence + profile scope filter, **no `ForProfile` calls** (FR-010/FR-013, review findings 9/10/13)
- [x] T009 [P] Implement `did_you_mean` helper (prefix + Levenshtein ≤2, ≤3 suggestions, caller-visible names only, quarantined-server names excluded) in `internal/preflight/suggest.go` + tests
- [x] T010 Propagate agent-token `ProfilePin` through REST auth into the evaluation context (`internal/auth/agent_token.go`, `internal/httpapi/server.go`); evaluation scope = token scope ∩ token pin ∩ requested profile; tests for each intersection case (review finding 11)
- [x] T011 Mirror DTOs + reason constants into `internal/contracts/types.go` per data-model.md; add the new types to the `cmd/generate-types` **generator source** and regen `frontend/src/types/contracts.ts`; anti-drift unit test (preflight enum ≡ contracts constants)
- [x] T012 Glue: `internal/server/preflight_glue.go` — build EvalContext from index manager, storage, stateview snapshot, config, resolved profile scope; expose `RunPreflight` via ServerController (`internal/httpapi/server.go`); explicitly no `serverToolNames` (live-ListTools fallback); test with instrumented transport asserting zero upstream calls **plus before/after snapshots of runtime/index/config/approval state proving no mutation** (FR-006, review finding 17)
- [x] T013 Dispatch consolidation onto shared primitives across all four paths: `handleCallToolVariant` (internal/server/mcp.go), direct-mode callability (`internal/server/mcp_direct_callability.go`), code_execution dispatch, stored-script dispatch; refactor `classifyServerToolStatus` + `describeGateReason` to delegate; FR-002 contract tests: two-way equivalence for shared policy gates, one-way (refusal ⇒ non-ready) for fail-open existence gates, covering `auto_approve_tool_changes` and quarantine-skip cases (review findings 1/2/3)
- [x] T014 Activity seam: add activity type `preflight` to `internal/storage/activity_models.go` allowlist with Metadata payload `{verdict, reasons{code:count}, per_tool[{id,status,reason?}]}` (RequestID first-class, existing status vocabulary); add synchronous durable `RecordPreflight` to `internal/runtime/activity_service.go` (bypasses the bounded async channel); storage + service unit tests incl. write-failure propagation (FR-014, review findings 4/5/15)

## Phase 3: User Story 1 — CLI preflight for cron/CI (P1) 🎯 MVP

**Goal**: `mcpproxy tools preflight` exits 0/10/11/12 with per-tool verdicts; endpoint + activity land together.
**Independent test**: quickstart §2–§3 CLI cells against isolated instance.

- [x] T015 [US1] REST handler `internal/httpapi/preflight.go` with swag annotations (standard `APIResponse{data}` envelope, existing security schemes): validation (empty list 400, raw >100 entries 400, conflicting duplicate pins 400, unknown profile 400, wait_ms range 400, runtime-unavailable + evaluator infra error 503), dedup preserving first-occurrence order, tier detection (API key/socket/pipe vs agent token), **synchronous `RecordPreflight` before every 200**; route registration; handler tests for every 400/503 rule and the no-record-on-reject rule (FR-008/FR-014; review findings 6/17/18/20)
- [x] T016 [US1] wait_ms poll loop in handler (floor 250 ms, cap 10 s, early-terminate on non-retryable, always resolves at deadline) bounded by a dedicated preflight-wait semaphore (exhausted ⇒ immediate resolve, `waited_ms: 0`); tests incl. deadline, early-termination, semaphore-exhausted degrade (FR-012, review finding 8)
- [x] T017 [US1] Typed preflight exit-code error + central classification in `cmd/mcpproxy/exit_codes.go` + `main.go` error mapping; `cliclient` Preflight method; tests (review finding 21)
- [x] T018 [US1] CLI `tools preflight` subcommand in `cmd/mcpproxy/tools_cmd.go`: args = tool IDs, flags `--profile`, `--pin id=hash` (repeatable), `--read-only-only`, `--exclude-destructive`, `--exclude-open-world`, `--wait`, `-o json|yaml|table` + `MCPPROXY_OUTPUT`; exit codes 0/10/11/12 worst-class-wins, transport errors exit 1; `--help-json` metadata; unit tests for exit-code precedence (12>11>10), env output, formats (FR-009)
- [x] T019 [US1] Benchmarks: evaluator micro-benchmark in `internal/preflight/bench_test.go` + normative handler-level benchmark (incl. response encoding + activity-record build) in `internal/httpapi/preflight_bench_test.go`; SC-002 asserted as committed benchmark with generous CI threshold, not a brittle wall-clock gate (review findings 16/25)

## Phase 4: User Story 2 — REST harness extras (P1)

- [x] T020 [US2] Hash-pin authoring surface: expose approval `CurrentHash` + `HashSchemaVersion` as `sha256/v{N}:{hex}` on the operator-tier per-tool REST payload and `tools list -o json` (explicit contract change; generator + swagger + disclosure tests — never exposed to agent-token tier) (FR-011, review finding 22)
- [x] T021 [US2] OAS: `make swagger` regen from annotations, `scripts/verify-oas.sh` + `make swagger-verify` clean; server-edition gates: `go build -tags server ./cmd/mcpproxy`, `go test -tags server ./internal/serveredition/... -race`, `golangci-lint --build-tags server` (review findings 6/14-oas)

## Phase 5: User Story 3 — Activity browsability (P2)

- [x] T022 [US3] CLI `activity list` renders type `preflight` (allowlist + verdict summary from Metadata) in `cmd/mcpproxy/activity_cmd.go`; tests (review finding 16)
- [x] T023 [P] [US3] Frontend: extend activity type union/filter menu in `frontend/src/types/api.ts` + activity view rendering of preflight verdict; Playwright web-ui verification per docs/development/web-ui-verification.md (review finding 16)

## Phase 6: User Story 4 — Non-regression (P3)

- [ ] T024 [US4] Golden snapshot test from T002 green on the finished branch (byte-identical tools/list across all three modes) (FR-015)
- [ ] T025 [US4] E2E: run a `code_execution` script and a stored script (spec 097) against the isolated instance; assert unchanged behavior and that dispatch decisions for sabotaged tools agree with preflight verdicts on all four dispatch paths (no-skew live check)

## Phase 7: Sabotage E2E matrix (acceptance gate)

- [x] T026 Committed scenario-keyed matrix (`internal/server/testdata/preflight_sabotage_matrix.json`: scenario → expected {reason, retryable, action}) + E2E `internal/server/preflight_e2e_test.go` driving ctl-server fixtures (DESC_FILE rug-pull): quarantine flip, tool-definition drift, tool block, config denial, server disable, SIGSTOP/kill, mid-indexing, missing/explicit annotation per each of the three filters, unknown ID, unknown server, hash mismatch + schema-version-bump variant, PendingAuth, profile out-of-scope at both tiers; independent assertions per row PLUS reflection check that every enum code appears in ≥1 row; after each cell, `activity list --request-id` lookup asserts the preflight record (FR-016, SC-005; review findings 23/22-activity)
- [ ] T027 Scripted incident-diagnosis scenario (SC-006): tool quarantined between runs → preflight names `tool_changed`/`server_quarantined` in ≤1 step; committed as an E2E assertion

## Phase 8: Docs & Polish

- [x] T028 [P] `docs/api/rest-api.md`: endpoint reference (envelope, tiers, 400/503 rules, wait semantics) (FR-017)
- [x] T029 [P] `docs/cli-management-commands.md`: `tools preflight` reference with exit-code table and cron/CI recipe (FR-017)
- [x] T030 [P] NEW `docs/features/tools-preflight.md`: concept, taxonomy + precedence tables, disclosure tiers, transparency/activity story, cron + GitHub Actions + n8n recipes, composition with code_execution/stored scripts (REST-from-harness pattern), Phase-2+ roadmap (FR-017)
- [x] T031 [P] Usage-examples expansion: README/docs agent-workflow examples — token-saving discovery flow, typical agent actions through mcpproxy, preflight-gated automation example (FR-017)
- [ ] T032 Full gates: `go test -race ./...` (incl. cmd/ CLI tests), server-edition build+test+lint (T021 set), `./scripts/test-api-e2e.sh`, golangci-lint v2 `.github/.golangci.yml`, swagger + generate-types diff-clean
- [ ] T033 Cross-model review of the full diff (opencode gpt-5.6-sol), fix→re-review ≤5 rounds; then quickstart walkthrough end-to-end on the isolated instance

## Dependencies

Phase 2 strictly ordered: T003→T004→T005→T006→T007→T008→(T009 [P] anytime after T003)→T010→T011→T012→T013→T014. US1: T015 needs T012+T014 (activity lands with handler); T016–T018 need T015; T019 last in US1. US2/US3 after T015, mutually parallel. T024/T025 after T013+T015. T026–T027 after US1+US2. Docs [P] anytime after US1; T032–T033 last.

## Implementation strategy

MVP = Phases 1–3 (evaluator + gates consolidation + REST(+activity) + CLI). Then US2 (pins/OAS), US3 (browsability), US4, sabotage matrix as acceptance gate, docs, gates, cross-model review.
