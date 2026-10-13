# Tasks: Batched call_tools() for Parallel Upstream Calls

**Input**: Design documents from `/specs/096-batched-call-tools/`
**Prerequisites**: plan.md, research.md, data-model.md, contracts/call-tools-api.md, quickstart.md
**Convention**: TDD per constitution — every implementation task's tests are written first and observed failing.

## Phase 1: Setup

No setup tasks — existing Go project, no new dependencies.

## Phase 2: Foundational (blocking all user stories)

- [x] T001 Promote the bare sandbox error-code string literals (`INVALID_ARGS`, `ACCESS_DENIED`, `PERMISSION_DENIED`, `UPSTREAM_ERROR`) to constants in internal/jsruntime/errors.go and use them in internal/jsruntime/runtime.go makeCallToolFunction; existing tests must stay green (pure refactor, byte-identical error payloads).
- [x] T002 Extract the per-element gates (allow-list, agent-scope, permission-tier — R5 gates 4–6) from makeCallToolFunction into a pure helper on ExecutionContext returning a plain error map (nil = allowed) in internal/jsruntime/runtime.go, side effects (budget read, updateMaxPermissionLevel) staying at call sites; add a parity test in internal/jsruntime/batch_test.go asserting the helper reproduces the lone-call codes and check order (budget-first overall, per data-model.md).
- [x] T003 Add unexported `ctx context.Context` to ExecutionContext, assigned from the existing timeoutCtx right after its creation in Execute (internal/jsruntime/runtime.go:~181); unit test in internal/jsruntime/batch_test.go asserts the field is non-nil during execution and cancelled once Execute returns; lone call_tool continues to use context.Background() (assert unchanged behavior via existing tests).
- [x] T004 [P] Add `code_execution_max_parallel` config field: struct field beside siblings (internal/config/config.go:~418), DefaultConfig 8 (~1700), range validation 1–32 (~2142), post-load defaulting absent/0→8 (~2421); table-driven tests in internal/config/config_test.go (default, explicit, invalid range, zero) written first and observed failing.
- [x] T005 Fix the two hot-reload breaks (R6): add a code_execution changed-field clause to DetectConfigChanges in internal/runtime/config_hotreload.go covering all four CodeExecution* fields; switch the resolveCodeExecutionDefaults call site in internal/server/mcp_code_execution.go to read via p.currentConfig(); add `MaxParallel int` to jsruntime.ExecutionOptions and extend resolveCodeExecutionDefaults (unset/0 → config value); failing-first tests in internal/runtime/config_hotreload_test.go (edit touching only code_execution_max_parallel produces a change event) and internal/server/code_execution_options_test.go (MaxParallel default resolution).

**Checkpoint**: `go test -race ./internal/jsruntime/... ./internal/config/... ./internal/runtime/... ./internal/server/ -run 'CodeExec|Config'` green; no behavior change observable to existing callers.

## Phase 3: User Story 1 — Fan-out completes in parallel time (P1)

**Goal**: `call_tools()` exists, runs elements concurrently, returns ordered per-slot envelopes.
**Independent test**: stub ToolCaller with fixed 50ms latency; batch of 10 completes < 35% of serial time with identical results.

- [x] T006 [US1] Write failing batch-core tests in internal/jsruntime/batch_test.go using a latency/concurrency-recording stub ToolCaller: (a) 10-element batch returns 10 slots in input order with lone-call-identical envelopes; (b) wall-clock < 35% of the serial equivalent (SC-001 shape); (c) `call_tools([])` returns `[]` with zero budget consumed; (d) single-element batch byte-equivalent to lone call_tool for the same request; (e) results are plain JSON wire shapes (reuse tool_result_test.go assertions).
- [x] T007 [US1] Implement makeCallToolsFunction + private runBatch in internal/jsruntime/runtime.go per plan Design Outline steps 2–5: script-goroutine parse/validate (arity, dense array, element shape, options integer 1–32, cap 100 → single INVALID_ARGS envelope naming first offending index); input-order pre-dispatch pass (budget-first check order via T002 helper, script-thread-local dispatch counter); prefilled-closed-channel worker pool of min(max_parallel, dispatchable) honoring ec.ctx; workers produce plain slot values + ToolCallRecords into index-owned cells; unconditional WaitGroup join; input-order record append + updateMaxPermissionLevel + single vm.ToValue. Make T006 green.
- [x] T008 [US1] Bind `call_tools` in Execute beside call_tool (internal/jsruntime/runtime.go:~171) and add it (+ `options.max_parallel`) to BOTH code_execution description strings — internal/server/mcp.go:~913/934 and internal/server/mcp_routing.go buildCodeExecutionTool:~506/540 — with identical wording; test in internal/server/code_execution_options_test.go (or a new surfaces test) asserting both descriptions mention call_tools, written first.

**Checkpoint**: quickstart.md example works against a stub; US1 acceptance scenarios pass.

## Phase 4: User Story 2 — One failure never poisons the batch (P2)

**Goal**: per-slot error isolation and whole-call validation semantics pinned.
**Independent test**: mixed-failure batch resolves 100% of slots.

- [x] T009 [US2] Write failing error-isolation tests in internal/jsruntime/batch_test.go: (a) batch of 5 with element 3 hitting an upstream error → 4 ok slots + 1 UPSTREAM_ERROR slot, order intact; (b) scope-violating element → SERVER_NOT_ALLOWED/ACCESS_DENIED slot per lone-call parity, siblings unaffected; (c) over-budget tail elements → MAX_TOOL_CALLS_EXCEEDED slots, not dispatched (stub records dispatch count); (d) non-serializable result → SERIALIZATION_ERROR slot; (e) malformed calls (non-array, bad element shape, non-object args, sparse hole, options non-object, fractional/out-of-range max_parallel, >100 elements) → single INVALID_ARGS envelope naming the first offending index, stub records zero dispatches; (f) args omitted defaults to {}.
- [x] T010 [US2] Fix any behavior T009 exposes in internal/jsruntime/runtime.go until green; no test may be weakened to pass.

**Checkpoint**: US2 acceptance scenarios + SC-003 pass.

## Phase 5: User Story 3 — Concurrency stays operator-controlled (P3)

**Goal**: max_parallel bound + override, cancellation discipline, Spec-093 subordination.
**Independent test**: concurrency high-water mark obeys the bound; timeout cancels workers with a full join.

- [x] T011 [US3] Write failing concurrency-bound tests in internal/jsruntime/batch_test.go: (a) batch of 10 with max_parallel 3 → stub high-water mark ≤ 3, all complete; (b) per-call override beats ExecutionOptions.MaxParallel; (c) ExecutionOptions.MaxParallel (config default path) governs when no override; (d) built-in 8 when neither set.
- [x] T012 [US3] Write failing cancellation tests in internal/jsruntime/batch_test.go and make them green: execution timeout mid-batch (stub blocks until ctx cancel) → Execute returns TIMEOUT; stub asserts every in-flight ctx was cancelled; instrument runBatch (test hook or stub-side sync) to prove the join completed and exactly one ToolCallRecord exists per dispatched element; whole file must pass under -race.
- [x] T013 [US3] Concurrent-safety test at the server seam in internal/server/mcp_code_execution_test.go: N goroutines invoking upstreamToolCaller.CallTool concurrently (as batch workers will) → getToolCalls returns N records, -race clean; reference (not re-test) Spec 093's admission coverage in the test comment, and assert the ctx passed by workers reaches the ToolCaller (stub captures it).

**Checkpoint**: US3 acceptance scenarios + SC-004 shape pass; `go test -race ./internal/jsruntime/...` green.

## Phase 6: Polish & Cross-Cutting

- [x] T014 [P] Regenerate OpenAPI (`make swagger`) for the new config field and add `code_execution_max_parallel` (number, min 1, max 32) to the code-execution section of frontend/src/views/settings/fields.ts; frontend unit test only if an existing pattern covers sibling fields (tests live in frontend/tests/unit/*.spec.ts).
- [x] T015 [P] Documentation: add call_tools() + max_parallel to docs/configuration.md, docs/configuration/config-file.md, docs/features/code-execution.md, docs/code_execution/{overview,api-reference,troubleshooting,cookbook}.md, including the queue-vs-shed note from research R3 (queue_size headroom warning).
- [ ] T016 Full verification: `go build ./cmd/mcpproxy` + `-tags server`; `go test -race -count=1 ./internal/...`; `go test -tags server ./internal/serveredition/... -race`; `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`; `./scripts/test-api-e2e.sh`; revert e2e-config churn; run the quickstart example end-to-end via REST code/exec against the e2e everything-server as a smoke check.

## Dependencies

- Phase 2 (T001–T005) blocks everything; within it T001→T002 sequential, T003 independent, T004→T005 (T005 consumes the field), T004 parallel with T001–T003.
- US1 (T006–T008) → US2 (T009–T010) → US3 (T011–T013) is the natural order; US2/US3 tests are additive over the same engine, so stories stay independently verifiable but share T007's implementation.
- Polish (T014–T016) after all stories; T014/T015 parallel.

## Implementation Strategy

MVP = Phase 2 + US1 (a working, bounded, ordered batch). US2/US3 pin semantics the engine already carries; polish ships config surface + docs. Suggested execution: single implementation agent for Phases 2–5 (the engine is one coherent seam in runtime.go; splitting it across agents invites merge conflicts in one function), parallel agents for T014/T015, orchestrator runs T016.
