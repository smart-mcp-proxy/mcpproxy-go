# Implementation Plan: Batched call_tools() for Parallel Upstream Calls

**Branch**: `096-batched-call-tools` | **Date**: 2026-08-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/096-batched-call-tools/spec.md`

## Summary

Add a `call_tools(requests, options?)` host function to the code-execution JS sandbox that dispatches independent upstream calls concurrently (bounded by a new `code_execution_max_parallel` config field, per-call overridable 1–32) and returns per-slot `{ok, result}|{ok, error}` envelopes in input order. Concurrency is a bounded worker pool over the existing `ToolCaller.CallTool` path — Spec 093 per-server admission applies automatically inside `managed.Client.CallTool`. Workers produce plain Go values (dispatch + JSON-round-trip normalization); all goja conversion happens on the script goroutine after the join. Pre-dispatch checks (shape, scope, budget) run deterministically in input order before any dispatch.

## Technical Context

**Language/Version**: Go 1.24 module toolchain (repo builds with local Go 1.25)
**Primary Dependencies**: existing only — goja (sandbox), mark3labs/mcp-go (tool surface), zap. **No new dependencies.**
**Storage**: none new (existing BBolt tool-call history via the existing locked path)
**Testing**: `go test -race` (`internal/jsruntime`, `internal/server`, `internal/config`, `internal/runtime`), stub ToolCaller with per-call latency/concurrency high-water tracking; `./scripts/test-api-e2e.sh`
**Target Platform**: all supported (darwin/linux/windows), both editions (no server-tag code involved)
**Project Type**: single Go project, existing package layout
**Performance Goals**: SC-001/SC-002 — batch of N independent calls ≈ slowest element (10×300ms < 35% of serial; 31×500ms < 4s at default max_parallel 8)
**Constraints**: goja VM single-owner (script goroutine); `ec.ToolCalls`/`ec.maxPermissionLevel` script-thread-only; workers must honor execution ctx (FR-007); batch cap 100; sandbox stays timer-free/synchronous
**Scale/Scope**: ~5 production files touched + config wiring + 2 description strings + docs; ~10 new test files/functions

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Feature exists to cut fan-out wall-clock; no indexing/search impact. |
| II. Actor-Based Concurrency | PASS | Bounded worker pool + channels; join before VM conversion; no new locks (workers return values; script thread owns state). Context propagation used for cancellation (FR-007) as the principle requires. |
| III. Configuration-Driven | PASS | `code_execution_max_parallel` in mcp_config.json with default+validation; hot-reload fixed as part of this feature (R6). |
| IV. Security by Default | PASS | Per-element parity enforcement (R5); Spec 093 admission preserved (R3); no new listener/surface. |
| V. TDD | PASS | Red-green per task; race-detector tests for concurrency invariants. |
| VI. Documentation Hygiene | PASS | Tool description ×2, 7 docs files, swagger regen, frontend settings field. |

**Post-design re-check**: PASS — no violations introduced by Phase 1 design; Complexity Tracking not needed.

## Project Structure

### Documentation (this feature)

```text
specs/096-batched-call-tools/
├── plan.md              # This file
├── research.md          # Phase 0 (complete)
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   └── call-tools-api.md  # Sandbox JS API + config contract
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/jsruntime/
├── runtime.go           # call_tools binding, worker pool, pre-dispatch pass,
│                        #   ExecutionContext.ctx field, gate-check extraction
├── errors.go            # promote bare error-code strings to constants
├── batch_test.go        # NEW: batch semantics, ordering, concurrency, cancellation
└── tool_result_test.go  # existing wire-shape tests (unchanged)

internal/server/
├── mcp_code_execution.go  # max_parallel resolution via currentConfig(); options parse
├── mcp.go                 # code_execution description (+call_tools, +max_parallel)
├── mcp_routing.go         # duplicated description in buildCodeExecutionTool
└── code_execution_options_test.go  # extend: max_parallel shapes/range

internal/config/
├── config.go            # field, default, validation, post-load defaulting
└── config_test.go       # extend

internal/runtime/
└── config_hotreload.go  # code_execution changed-field clause (R6)


oas/                     # make swagger regen
frontend/src/views/settings/fields.ts  # settings field
docs/…                   # 7 files per R6
```

**Structure Decision**: All changes live in existing packages; the only new file is the batch test file. The batch engine is private to `internal/jsruntime` (a `runBatch` helper beside `makeCallToolsFunction`), keeping the ToolCaller interface unchanged so every existing ToolCaller implementation (server, tests) works as a batch target unmodified.

## Design Outline (Phase 1 condensed)

1. **Binding**: `vm.Set("call_tools", ec.makeCallToolsFunction(vm))` beside `call_tool` in `Execute` (R7). The closure runs on the script goroutine.
2. **Parse & validate (script goroutine)**: arity/array checks; per-element shape check ({server, tool, args?}); options parse (`max_parallel` integer 1–32); batch cap 100; malformed → single `{ok:false,error}` envelope (FR-012), nothing dispatched.
3. **Pre-dispatch pass (script goroutine, input order, per-element check order IDENTICAL to lone call_tool today — R5)**: for each element: shape → **budget → allow-list → agent-scope → permission-tier** (budget first, exactly as `runtime.go:301-315` orders it, so an exhausted-budget + disallowed element errors with `MAX_TOOL_CALLS_EXCEEDED` on both primitives). Failing elements get their slot error immediately and are excluded from dispatch. The budget check for element k uses `len(ec.ToolCalls) + dispatchedSoFar(k)` — a script-thread-local counter of elements already accepted for dispatch in this batch; no cross-goroutine accounting exists, so it cannot race. Every accepted element is GUARANTEED exactly one ToolCallRecord at the join (success, upstream error, serialization error, or cancellation error), so reservations and records can never diverge.
4. **Dispatch**: prefill a buffered channel with all dispatchable indices, close it, then start `min(max_parallel, len(dispatchable))` workers — no producer goroutine exists, so nothing can deadlock on send. Each worker drains the closed channel (checking `ec.ctx.Err()` between takes; a cancelled worker records the remaining takes as cancellation-error slots rather than dispatching), and per index: `toolCaller.CallTool(ec.ctx, …)` → normalize (R1) → write the slot value and the ToolCallRecord as plain Go data into its index-owned cell (each cell written by exactly one worker — no lock).
5. **Join & convert (script goroutine)**: the join is an UNCONDITIONAL WaitGroup wait — the script goroutine never proceeds (and never touches slots) while any worker is alive; cancellation accelerates worker completion (in-flight `CallTool` calls return promptly on ctx error) but never bypasses the join. After the join: append the per-element records to `ec.ToolCalls` in input order (R2), `updateMaxPermissionLevel` per dispatched element, single `vm.ToValue(slots)` conversion.
6. **Cancellation (FR-007)**: `Execute`'s existing `defer cancel()` fires when it returns on the timeout branch, cancelling in-flight upstream calls; workers finish their cells and exit; the join completes; the script goroutine then continues into state that is already orphaned — exactly the lone-call post-timeout behavior today (parity). Workers themselves never mutate execution state (`ec.*`); their only shared writes are index-owned cells (pre-join) and `upstreamToolCaller`'s internally-locked records. Records completing after `handleCodeExecution` has read `getToolCalls()` are dropped from that response, identical to a lone call in flight at timeout today.
7. **Config**: 7-point wiring + the two hot-reload fixes (R6). No REST/MCP execution-level `max_parallel` option — the spec mandates only the config default and the per-batch `call_tools(..., {max_parallel})` override; precedence is exactly `per-batch override > code_execution_max_parallel > built-in 8`.
8. **Docs & descriptions**: both description strings; 7 docs files; swagger; frontend settings entry.

## Complexity Tracking

Not needed — no constitution violations.
