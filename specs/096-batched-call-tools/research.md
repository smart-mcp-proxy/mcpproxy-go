# Research: Batched call_tools() (Spec 096)

All findings verified against the codebase on branch `096-batched-call-tools` (which stacks on the PR #988 fixes). File:line references are to that state.

## R1. Goja thread-safety seam

**Decision**: Batch workers produce plain Go values only; every `vm.ToValue` happens on the script goroutine after all workers join.

**Rationale**: `Execute` creates the VM (`internal/jsruntime/runtime.go:156`) and hands it to a single goroutine (`:186-188`); the `select` waiter (`:191-209`) never touches it. The script goroutine is the VM's sole owner, and `call_tools` is invoked from inside `vm.RunString`, i.e. on that goroutine. `normalizeToolResult` (`:421-436`) is a JSON round-trip producing only plain Go types — safe to run inside workers. So the batch does dispatch + normalize off-thread, joins, then converts the assembled results array once.

**Alternatives considered**: locking the VM (goja explicitly unsupported); per-worker VMs (pointless — results are data, not code).

## R2. ToolCaller concurrency

**Decision**: `upstreamToolCaller.CallTool` may be invoked concurrently as-is; jsruntime-side execution state may only be mutated on the script goroutine.

**Rationale**: `upstreamToolCaller` guards its `toolCalls` with `mu sync.Mutex` (`internal/server/mcp_code_execution.go:512-525`, writer `:610-622`, reader returns a copy `:625-633`); correlation ids are atomic (`internal/server/mcp.go:667-677`); history writes lock storage (serialized — acceptable, they're small). The unsafe state is `ec.ToolCalls` appends (`runtime.go:377,397,409`) and `ec.maxPermissionLevel` (`:456-460`) — unguarded, script-thread-only today. Workers therefore return completed records; the script thread appends them after the join, in input order.

**Alternatives considered**: adding a mutex to `ExecutionContext` — more invasive, changes lone-call code paths for no benefit.

## R3. Spec 093 per-server limits

**Decision**: No batch-side work needed — admission happens inside `managed.Client.CallTool` (`internal/upstream/managed/client.go:726` → `acquireAdmission`, `internal/upstream/managed/admission.go:52-75`, FIFO semaphore in `internal/upstream/limiter/registry.go:344`). N concurrent workers targeting one server are bounded per config automatically.

**Correction to the spec (folded into spec.md)**: "queues rather than bypasses" only holds when `queue_size` is configured. `Limits.QueueSize` defaults to 0 = shed immediately (`internal/upstream/limiter/limiter.go:42-44`, shed at `:262`; default resolution `internal/config/concurrency.go:37-74`). With `max_concurrent_requests: 1` and no queue, a 10-element batch yields 1 success + 9 per-slot `queue_full` errors — which is the server's configured policy, not a bypass. Tests and docs must set `queue_size` explicitly when they want queueing; docs warn that large `max_parallel` against a limited server needs queue headroom.

## R4. Execution context threading

**Decision**: Add an unexported `ctx context.Context` field to `ExecutionContext`, assigned from the existing `timeoutCtx` right after `runtime.go:181`; batch workers run under it. Lone `call_tool` keeps `context.Background()` verbatim (parity).

**Rationale**: `timeoutCtx, cancel := context.WithTimeout(ctx, ...)` already exists with `defer cancel()` — when `Execute` returns (result or timeout), workers are cancelled instead of orphaning upstream calls, satisfying FR-007 without touching lone-call behavior. Known pre-existing gap, unchanged by this feature: there is no `vm.Interrupt` anywhere, so the script goroutine itself can outlive a timeout; batch workers do NOT inherit that gap because they honor ctx.

## R5. Per-element scope checks (parity set)

The gates in `makeCallToolFunction` (`runtime.go:273-356`), in order, with error codes:

1. arity < 3 → `INVALID_ARGS` (`:275`)
2. args not an object → `INVALID_ARGS` (`:290`)
3. budget: `len(ec.ToolCalls) >= maxToolCalls` → `MAX_TOOL_CALLS_EXCEEDED` (`:302`)
4. allow-list/profile: → `SERVER_NOT_ALLOWED` (`:315`)
5. agent-token scope: → `ACCESS_DENIED` (`:328`)
6. permission tier via `ToolAnnotationFunc` → `PERMISSION_DENIED` (`:340-352`), side effect `updateMaxPermissionLevel` (`:355`)

Post-dispatch: `UPSTREAM_ERROR` (`:382`), `SERIALIZATION_ERROR` (`:402`). Quarantined servers surface as unknown-server `UPSTREAM_ERROR` (they are absent from the manager, `mcp_code_execution.go:547`) — that IS the parity behavior.

**Decision**: Extract gates 4–6 into a pure helper returning a plain error map (nil = allowed); gate side effects (budget accounting, `updateMaxPermissionLevel`) stay with the callers: lone call_tool inline as today, batch in its input-order pre-dispatch pass. Promote the bare string error codes (`INVALID_ARGS`, `ACCESS_DENIED`, `PERMISSION_DENIED`, `UPSTREAM_ERROR`) to constants in `errors.go` during extraction so batch and lone paths cannot drift.

## R6. Config wiring for `code_execution_max_parallel`

Seven wiring points (verified against sibling fields `CodeExecutionTimeoutMs`/`CodeExecutionMaxToolCalls`/`CodeExecutionPoolSize`):

1. Struct field beside siblings — `internal/config/config.go:418` (`json:"code_execution_max_parallel,omitempty" mapstructure:"code-execution-max-parallel"`).
2. `DefaultConfig` — `config.go:1700-1703` (default 8).
3. Validation (range 1–32) — `config.go:2142-2158`.
4. Post-load defaulting (absent/0 → 8) — `config.go:2421-2427`.
5. Swagger/OAS regen (`make swagger`) — `oas/swagger.yaml:65-73` documents the siblings.
6. Frontend settings field — `frontend/src/views/settings/fields.ts:319-328` (`code-execution` section).
7. Docs — `docs/configuration.md`, `docs/configuration/config-file.md`, `docs/features/code-execution.md`, `docs/code_execution/{overview,api-reference,troubleshooting,cookbook}.md`.

**No env override**: none of the four `CodeExecution*` siblings has an `MCPPROXY_*` override (`internal/config/loader.go:627-750`); staying consistent.

**Hot-reload (two pre-existing breaks the plan must fix for FR-004)**:
- `DetectConfigChanges` (`internal/runtime/config_hotreload.go:44-272`) has no `CodeExecution*` clause — an edit touching only these fields is swallowed as "no changes". Add a `code_execution` changed-field clause covering the new field (and siblings).
- `handleCodeExecution` reads the construction-time `p.config` snapshot (`mcp_code_execution.go:63`); `p.currentConfig()` exists for live reads (`internal/server/profile_resolver.go:40-47`). Switch the defaults-resolution call site to `currentConfig()` so "applies to executions that start after the change" holds (incidentally makes the sibling fields hot-reload as their docs already imply).

## R7. Binding site and description text

- Host functions are bound inline in `Execute` — `input` (`runtime.go:165`), `call_tool` (`:171`); `call_tools` binds alongside with the same `vm.Set` pattern. There is no `log` global and no `setupExecutionEnvironment` function.
- The code_execution tool description is duplicated in `internal/server/mcp.go:913` (single-line string) and `internal/server/mcp_routing.go:506` (multi-line concatenation in `buildCodeExecutionTool`; disabled-stub variant at `:485-503`). Both must gain `call_tools` + `options.max_parallel` text in lockstep; the options list is at `mcp.go:934` and past `mcp_routing.go:540`.
