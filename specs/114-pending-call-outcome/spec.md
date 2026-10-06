# Feature Specification: Typed Pending-Call Outcome and Call Correlation

**Feature Branch**: `114-pending-call-outcome`
**Created**: 2026-10-06
**Status**: Draft (docs only, for maintainer review before any code)
**Input**: Issue #1321, "Typed pending-call outcome + call correlation for long-running stdio tool calls (follow-up to #1317)". The issue asks for four things:
1. A typed `waiting_human` / `deadline_exceeded` result that carries a call id.
2. Rejection of a blind retry while that call id is unresolved.
3. Explicit cancellation, or a fresh read-only reconciliation on the same connection generation, before another mutating call.
4. Structured logging at timeout.

**Related**:
- #1317 (closed): Chrome DevTools MCP `--autoConnect` consent dialog. The process kill was fixed in #1320.
- #1513 (merged): item 4, structured timeout logging.
- Spec 105: connection generation (`connectionEpoch`) and pinned dispatch.
- Spec 107: the audit line and its frozen `error_class` vocabulary.
- Spec 018: the intent variants `call_tool_read|write|destructive`.
- Spec 093: the concurrency-limiter shed. This spec reuses its refusal shape.
- Spec 113 (open, PR #1495), especially:
  - 113-c, call-error taxonomy (PR #1502).
  - 113-d, health from failure rate (PR #1504).
  - 113-e, HTTP session re-init (PR #1501).

  Spec 113 does not list #1321 and does not overlap with it.

**Scope note**: #1513 already did item 4 of #1321. This spec covers items 1–3 only. #1513's PR body says it fixes #1321, but it does not. Keep #1321 open until this spec ships.

**Glossary**:
- *Call id*: an opaque id that mcpproxy mints for one upstream `tools/call` (`pc_` prefix). It is neither the JSON-RPC id (mcp-go does not expose that to mcpproxy) nor the activity `request_id`, though the record links both.
- *Connection generation* / *epoch*: `managed.Client.connectionEpoch` (Spec 105). It is bumped on every connect and disconnect and drawn from a process-wide counter, so a value is never reused.
- *Unresolved call*: a call whose local wait ended (on the deadline or a caller cancel) while the upstream may still be running it.
- *Pending-call registry*: the in-memory set of unresolved calls, keyed by `(server, epoch)`.
- *Mutating call*: a call whose **target tool tier** is `write` or `destructive` under the existing `tierForAnnotations` rule. An unknown tool, or one with no `destructiveHint`, counts as destructive.
- *Principal*: the caller identity that the gate is scoped to (FR-020).

## Context & Motivation

These facts were checked at `origin/main` `44244b858` and mcp-go `v1.0.0`:

1. **Path.**
   - `handleCallToolVariant` (`internal/server/mcp.go:2640`) dispatches at `:3262` through `dispatchOnEpoch` (`:8253`).
   - From there the call goes to `Manager.CallTool` / `CallToolOnEpoch` (`internal/upstream/manager.go:1550,1563`; `callTool` is at `:1569`).
   - Next is `managed.Client.callTool`. It runs admission, then `invoker.CallTool(core.WithConnectionGeneration(ctx, callEpoch), …)` at `managed/client.go:1204`.
   - Last is `core.Client.CallTool` (`core/client.go:462`).
   - The direct surface (`mcp_routing.go`), `code_execution` sub-calls (`mcp_code_execution.go`, which calls the managed client directly) and REST `POST /api/v1/tools/call` (`httpapi/server.go:6090`) all reach the same managed client.
2. **Timeout.**
   - `call_tool_timeout` defaults to 2 minutes (`config.go:1789`). It applies only when the caller's ctx has no shorter deadline.
   - When it expires, mcp-go `Stdio.SendRequest` deletes the response channel and returns `ctx.Err()` (`client/transport/stdio.go:648-650`).
   - **Nothing is sent upstream**: there is no `notifications/cancelled`, so the child keeps working.
   - A late response finds no channel and is silently dropped (`stdio.go:570-579`).
   - `core.CallTool` returns `CallTool '<tool>' timed out after <d>` (`core/client.go:570`).
3. **The process survives.** Since #1320, a timeout no longer marks the server unhealthy or bumps the epoch. `managed/calltool_cancel_test.go` asserts that the server stays `Ready`.
4. **The client gets an untyped error.**
   - `createDetailedErrorResponse` (`mcp.go:6897`) falls through to `{"error":"CallTool 'x' timed out after 2m0s","server_name","tool_name","troubleshooting":"Check server configuration, connectivity…"}`.
   - The result is `isError` with no call id, no state, and nothing that says the upstream may still be running.
   - The troubleshooting text is wrong for this case.
5. **mcpproxy forgets the call.**
   - `beginInFlightCall` (`managed/client.go:215`) counts only calls that are inside `callTool`, and its count ends at the timeout.
   - It stops health checks and reconnects from killing a running call. It does not guard retries.
   - Nothing stops an immediate second mutating call while the first is still running upstream. This is the double-submit hazard that the #1317 commenter described.
6. **mcp-go cannot cancel or correlate by itself.**
   - It defines `notifications/cancelled` (`mcp/types.go:118,537-553`), but no client code sends it.
   - `client.Client` assigns the JSON-RPC id internally and does not return it.
   - A `transport.Interface` decorator can see the id: `SendRequest(ctx, JSONRPCRequest)` receives the request with its `ID` and the caller's ctx (`client/transport/interface.go:17-36`).
   - mcpproxy builds stdio clients with `client.NewClient(stdioTransport)` (`core/connection_stdio.go:244`), so a decorator fits there without forking mcp-go.
7. **Progress is ignored.**
   - `core.registerNotificationHandler` drops every notification except `list_changed` (`core/connection_lifecycle.go:214-223`).
   - `Meta.ProgressToken` exists (`mcp/types.go:200-250`).
   - A server is not obliged to emit progress.
8. **Tasks.**
   - mcp-go v1.0.0 has `CallToolParams.Task`, `GetTask`/`CancelTask` and `notifications/tasks/status`.
   - mcpproxy never sets `Params.Task` and does not negotiate the capability.
   - Tasks are the protocol's durable mechanism for long-running calls. They need upstream support, and they are out of scope here (Known Limitation 4).
9. **#1513 logging.**
   - `logCallInterrupted` (`core/client.go:1055`) emits one Warn, "Upstream tools/call interrupted".
   - Its fields are server, tool, transport, pid, `connection_generation`, `request_id`, `cancellation_source` (`call_timeout|caller_cancel`) and `resulting_state`.
   - It has no call id and no `instance_id`.
10. **113-c (unmerged).** It adds:
    - `internal/callerr`, with `error_class` (`timeout`, `proxy_policy`, `cancelled`, …), `fault_domain` and a typed `callTimeoutError`.
    - These fields stamped on every activity write path.
    - A total mapping to the Spec 107 audit classes (FR-047).
    - No reading of message text (FR-049).

## Scope Boundary

| Already exists | Reused, not rebuilt |
|---|---|
| `connectionEpoch`, `WithConnectionGeneration`, `ErrConnectionGenerationChanged` | The registry key. When the epoch moves, every entry of the old epoch is resolved |
| `tierForAnnotations` (`mcp_code_execution.go:1744`) | The only tier rule the gate uses. It reads the target tool's tier, not the variant the caller chose |
| Spec 093 shed and Spec 105 generation refusal at `mcp.go:3333-3385` | The shape of the gate refusal: an `isError` result, an activity record and a policy decision. The call never reaches the upstream |
| 113-c `error_class` / `fault_domain` | Timeouts stay `error_class=timeout` and refusals are `proxy_policy/proxy`. **No second error vocabulary** |
| #1513 `logCallInterrupted` | Gains `call_id` and the pending state. Otherwise unchanged |
| `mintCorrelationID` (`mcp.go:994`) | The pattern for minting `call_id` |

**Out of scope**:
- MCP Tasks (`Params.Task`) support.
- Durable (BBolt) persistence of the registry across mcpproxy restarts.
- A `waiting_human` state inferred from anything other than an explicit signal (FR-004, NEEDS CLARIFICATION Q1).
- An automatic `notifications/cancelled` on deadline (FR-011).
- Changes to the `call_tool_timeout` defaults.
- Web UI and macOS tray screens beyond a read-only display. These are deferred to a follow-up.
- Changes to 113-e session re-init semantics.

## User Scenarios & Testing *(mandatory)*

### User Story 1: A timeout says the call may still be running, and gives a call id (Priority: P1)

An agent calls `call_tool_write chrome-devtools:navigate_page`. Chrome shows a consent dialog, and the call runs past `call_tool_timeout`.

Today the agent gets "timed out… check server configuration" and usually retries. With this feature the agent gets the same error text plus a typed block with:
- the call id;
- `state: unresolved`;
- `reason: deadline_exceeded`, or `still_running` if the upstream sent progress;
- the connection generation;
- how to resolve the call.

**Why this priority**: Every other story needs a call id and a state. The change is purely additive, so it is safe to ship on its own.

**Independent Test**: Use a stdio fixture tool that sleeps past a 1s `call_tool_timeout`, and assert the result shape.

**Acceptance Scenarios**:

1. **Given** a stdio server whose tool sleeps 5s, and `call_tool_timeout: 1s`, **When** the client calls the tool, **Then**:
   - the result is `isError:true`;
   - the human text still starts with `CallTool '<tool>' timed out after 1s`;
   - the JSON body carries `pending_call: {call_id, server, tool, state:"unresolved", reason:"deadline_exceeded", connection_generation, upstream_may_still_be_running:true, dispatched_at, expires_at, resolve:[…]}`.
2. **Given** the same tool sends `notifications/progress` for the call within `progress_window` before the deadline, **Then** `reason` is `still_running`.
3. **Given** a call that fails before dispatch (an admission refusal, quarantine, or a connection that is down), **Then** no `pending_call` block is added and nothing is registered, because nothing reached the upstream.
4. **Given** the activity record and the `mcpproxy activity show <id> -o json` output for that call, **Then** both carry `call_id`, `pending_state` and `pending_reason`, and `error_class` is still `timeout` once 113-c is merged.
5. **Given** a client that ignores the new fields, **Then** it sees exactly today's `isError` result text.

---

### User Story 2: A blind mutating retry is refused with a self-healing error (Priority: P1)

The agent immediately retries the same write call, or another one, on the same server. While the first call is unresolved, mcpproxy refuses the mutating call before it reaches the upstream. The refusal tells the agent how to proceed: wait for the call to resolve, reconcile with a read, or cancel.

**Why this priority**: This is the safety property #1321 asks for. It prevents a second submit of a mutation whose first attempt may still land.

**Independent Test**: With the gate in `enforce` mode, time out a write call. Then issue a second write and a read on the same server.

**Acceptance Scenarios**:

1. **Given** an unresolved write call `pc_A` on `(srv, epoch 7)` for principal P, in mode `enforce`, **When** P calls any write-tier or destructive-tier tool on `srv`, **Then** the call is refused and does not reach the upstream, and:
   - the result is `isError:true`, with text that names `pc_A`, its age, and the three ways to resolve it;
   - the activity status is `rejected`, with a `blocked` policy decision (reason `pending_call_unresolved`);
   - the 113-c classification is `proxy_policy/proxy`.
2. **Given** the same state, **When** P calls a read-tier tool on `srv`, **Then** the call passes the gate and is dispatched.
3. **Given** the same state, **When** P calls a write tool on a different server, **Then** that call is not affected.
4. **Given** the same state in mode `warn`, **Then** the write is dispatched, its result and activity record carry `pending_call_warning` naming `pc_A`, and a Warn log is emitted.
5. **Given** the same state, reached through `code_execution` sub-calls, the direct surface `srv__tool`, or REST `POST /api/v1/tools/call`, **Then** each path gets the same refusal. None of them bypasses the gate.
6. **Given** a `call_tool_read` aimed at a write-annotated tool, **Then** the existing `validateIntentAgainstServer` check still rejects it, so the read variant is not a way around the gate.

---

### User Story 3: The agent can resolve the ambiguity (Priority: P1)

The agent resolves an unresolved call in one of five ways, and the gate opens.

**Independent Test**: For each path, time out a write, resolve it, then assert that a new write is dispatched.

**Acceptance Scenarios**:

1. **Reconcile**: **Given** `pc_A` is unresolved on epoch 7, **When** P makes a successful read-tier call on `srv`, on epoch 7, *dispatched after* `pc_A` timed out, **Then** `pc_A` becomes `reconciled` and the next write passes (subject to Q3).
2. **Cancel**: **Given** `pc_A` is unresolved, **When** P calls `upstream_servers` with `operation: cancel_pending_call, call_id: pc_A` (or the REST or CLI equivalent), **Then**:
   - mcpproxy sends `notifications/cancelled {requestId: <JSON-RPC id of pc_A>, reason}` upstream, when the transport supports it;
   - `pc_A` is marked `cancelled`, and the next write passes;
   - the response says whether the notification was sent (`cancel_sent: true|false`) and that the upstream may ignore it.
3. **Late completion (stdio)**: **Given** `pc_A` is unresolved and the upstream sends its response after the deadline, **Then**:
   - `pc_A` becomes `completed_late`, with `late_outcome: success|tool_error`;
   - the activity record for `pc_A` is updated with the late outcome;
   - an SSE `pending_call.resolved` event is emitted;
   - the gate opens;
   - the late result content is not delivered to any client.
4. **Connection reset**: **Given** `pc_A` is unresolved on epoch 7, **When** the server reconnects or restarts (the epoch moves), **Then** `pc_A` becomes `connection_reset` and the gate opens.
5. **Expiry**: **Given** `pc_A` is unresolved, **When** `pending_call_ttl` passes with no other resolution, **Then** `pc_A` becomes `expired` and the gate opens. The timeout result already gave `expires_at`.

---

### User Story 4: Callers cannot see or cancel each other's calls (Priority: P1)

**Independent Test**: Two agent tokens, T1 and T2, against one server.

**Acceptance Scenarios**:

1. **Given** T1 has an unresolved `pc_A`, **When** T2 lists pending calls, **Then** T2 sees none of T1's entries, and `pc_A` does not gate T2's write calls on `srv`.
2. **Given** T1 has `pc_A`, **When** T2 tries `cancel_pending_call pc_A`, **Then** the response is the same `not_found` that T2 would get for an id that does not exist. The response does not reveal whether the id exists.
3. **Given** an admin caller (`IsAdmin()`), **Then** it can list and cancel every entry, and the audit line records the admin as the actor.
4. **Given** a read-only-scoped agent token, **Then** it can list its own entries, but `cancel_pending_call` is refused, because a cancellation is a write action on the upstream.

---

### User Story 5: Operators and humans see pending calls (Priority: P2)

**Acceptance Scenarios**:

1. **Given** unresolved calls, **When** the operator lists them through any of these, **Then** all three return the same entries with the same fields (parity test):
   - CLI `mcpproxy upstream pending list [--server srv] -o json`;
   - REST `GET /api/v1/servers/{name}/pending-calls`;
   - MCP `upstream_servers operation: pending_calls`.
2. **Given** a server with at least one unresolved call, **Then** `upstream list` and the server's REST payload show `pending_calls: N`. This is a count only; this feature does not change the health level.
3. **Given** a timeout, **Then** the #1513 Warn log line also carries `call_id`, `pending_state` and `pending_reason`.

### Edge Cases

- **Caller cancel after dispatch** (the client disconnects, or sends mcpproxy `notifications/cancelled`):
  - The call is registered as unresolved with `reason: caller_cancelled`.
  - mcpproxy forwards `notifications/cancelled` upstream (FR-012), because the caller explicitly abandoned the call.
  - No client is left to receive the typed result, but the entry still gates P's next write.
- **Many timeouts in a row**: each one gets its own entry. The refusal names the oldest entry and gives the count.
  - A per-server cap, `max_pending_calls_per_server` (default 32), bounds memory.
  - Past the cap, the oldest entry is evicted as `expired`, with a Warn.
- **A read-tier call that times out**: it is registered, so `pending_calls` stays truthful. It never gates anything and does not count as a reconciliation.
- **Same call id reused**: this cannot happen. Ids are 80 random bits plus a prefix.
- **`code_execution` script timeout** (a script-level deadline): every sub-call cut off after dispatch is registered on its own with its own call id. The script error lists those call ids.
- **HTTP/streamable upstream**:
  - The outcome and the registry apply, because the server may still finish the work after mcp-go aborts the request.
  - There is no late-completion observation (FR-010).
  - A cancel is sent only if the session is still live.
- **SSE upstream**: same as HTTP.
- **113-e session re-init on HTTP with an unchanged toolset**: this does not bump the epoch (113-e FR-083), so entries survive the re-init. That is correct, because a new session does not prove the old work stopped.
- **mcpproxy restart**: the registry is in memory. Every upstream process is restarted too, so all entries are moot and no persistence is needed.
- **A server disabled or quarantined while entries exist**: the disconnect bumps the epoch, so entries become `connection_reset`.
- **Clock skew**: the TTL uses the monotonic clock.

## Requirements *(mandatory)*

### Functional Requirements

**Call correlation**

- **FR-001 (call id)**: Every upstream `tools/call` that reaches `core.Client.CallTool` MUST get a `call_id`, minted before dispatch as `pc_` + 16 random base32 characters (80 bits).
  - The id MUST travel in ctx, not in a frozen signature.
  - It MUST be recorded on the activity record (`call_id`, `omitempty`) for all four entry points: call_tool_*, the direct surface, code_execution sub-calls and REST.
- **FR-002 (JSON-RPC id capture)**: For stdio upstreams, a `transport.Interface` decorator installed at `client.NewClient` MUST record `call_id → JSON-RPC request id`. It records this from `SendRequest(ctx, req)` when `req.Method == "tools/call"` and ctx carries a call id. The decorator MUST forward every optional interface that the wrapped transport implements, including `BidirectionalInterface` and any other interface mcp-go type-asserts, so that sampling, elicitation and roots keep working.

**Typed outcome**

- **FR-003 (pending block)**: When a dispatched call ends because the caller's ctx is done (deadline or cancel), the result MUST keep today's `isError` text. It MUST also add a machine-readable `pending_call` object in two places: the JSON error body, and the result `_meta` under `io.mcpproxy/pending_call`.
  - The object's fields are `call_id`, `server`, `tool`, `state` (`unresolved`), `reason` (FR-004), `connection_generation`, `upstream_may_still_be_running` (`true`), `dispatched_at`, `expires_at`, and `resolve`.
  - `resolve` is an ordered list of the available actions, each with the exact call to make.
  - The `troubleshooting` text for this case MUST say that the upstream may still be running. It MUST NOT suggest checking connectivity.
- **FR-004 (reason)**: `reason` is one of `deadline_exceeded`, `still_running` or `caller_cancelled`.
  - `still_running` applies if and only if a `notifications/progress` with this call's progress token arrived within `progress_window` (default 30s) before the deadline.
  - mcpproxy MUST NOT emit `waiting_human` unless an explicit signal is defined.
  - [NEEDS CLARIFICATION Q1: should a `waiting_human` reason exist? Recommended default: **no** in v1. No MCP or mcp-go signal tells "waiting for a human" apart from "slow", and a guess would mislead agents. Reserve the value in the enum documentation. Revisit if an elicitation-in-flight signal is accepted, that is, an `elicitation/create` outstanding for the same session at the deadline.]
- **FR-005 (progress token)**: For stdio upstreams, when the forwarded request has no progress token, `core.Client.CallTool` MUST set `_meta.progressToken = call_id` on the outgoing request.
  - The notification handler MUST record progress notifications whose token matches a live call.
  - It MUST NOT forward them, and MUST NOT log them at Info.
  - Progress for a token that the caller supplied is out of scope.
- **FR-006 (orthogonal to 113-c)**: The pending fields MUST be metadata alongside the 113-c classification, never a new `error_class` value.
  - A timed-out call is `error_class=timeout`.
  - A caller-cancelled call is `cancelled`.
  - A gate refusal is `proxy_policy/proxy`.
  - The audit line's frozen vocabulary is untouched, and the 113-c FR-047 mapping applies.

**Pending-call registry**

- **FR-007 (registry)**: A new in-memory registry (`internal/upstream/pending`) MUST hold one entry per unresolved call.
  - Entry fields: `call_id`, `server`, `epoch`, `principal`, `tool`, `tier`, `json_rpc_id` (optional), `dispatched_at`, `unresolved_at`, `expires_at`, `reason`, `state`, `last_progress_at`.
  - Entries are created only for calls that were dispatched. "Dispatched" means the 113-c dispatched mark, or the equivalent point in `core.Client.CallTool` if 113-c is not merged.
- **FR-008 (states)**: An entry moves from `unresolved` to exactly one of `completed_late`, `reconciled`, `cancelled`, `connection_reset` or `expired`.
  - Terminal states are kept for listing for `pending_call_history` (default 15 minutes), then dropped.
  - Only `unresolved` entries gate.
- **FR-009 (epoch resolution)**: Every epoch bump of server S (connect, disconnect, reconnect, restart, disable, quarantine) MUST move every `unresolved` entry of S with an older epoch to `connection_reset`.
  - This MUST happen synchronously with the bump, under the same `epochMu` serialization that Spec 105 uses.
  - As a result, no call on the new epoch can be gated by an old-epoch entry.
- **FR-010 (late completion, stdio)**: For stdio upstreams, when the caller's ctx ends, the decorator MUST keep waiting for the response in a detached wait. The wait ends at the first of: a response arrives, the transport closes, or `expires_at` passes.
  - When a response arrives, the decorator MUST:
    - mark the entry `completed_late`, with `late_outcome` set from `isError` (`success` or `tool_error`);
    - update the call's activity record (`pending_state`, `late_outcome`, `resolved_at`);
    - emit an SSE `pending_call.resolved` event.
  - The late result content:
    - MUST NOT be stored in activity beyond what today's policy stores for a successful response;
    - MUST NOT be returned to any client;
    - MUST pass through the existing sensitive-data detection.
  - Detached waits are capped per server at `max_pending_calls_per_server`. Past the cap, the decorator falls back to today's drop behavior, and the entry resolves only by TTL or epoch.
  - HTTP and SSE transports do no detached waits. Their request is aborted, as today.
- **FR-011 (no automatic upstream cancel on deadline)**: mcpproxy MUST NOT send `notifications/cancelled` when its own deadline fires. In #1317 the "slow" call was waiting on a human consent. Cancelling it would discard exactly what the user was doing.
- **FR-012 (forward caller cancellation)**: When the *caller* cancels after dispatch, mcpproxy MUST send `notifications/cancelled {requestId, reason:"cancelled by client"}` upstream.
  - The caller cancels either by sending mcpproxy `notifications/cancelled` for its request, or by disconnecting from `/mcp`.
  - This applies only when the JSON-RPC id is known (stdio) and the connection is still on the same epoch.
  - This is the behavior the MCP protocol expects from an intermediary.

**Retry gate**

- **FR-013 (gate placement)**: The gate MUST run at both dispatch choke points that 113-d FR-062 identifies:
  - `Manager.callTool`, which covers `call_tool_*`, the direct surface and REST;
  - the `code_execution` sub-call dispatch.

  It runs after the quarantine, intent, profile and permission gates. It runs before admission, so a refused call never takes a concurrency slot. The `Manager.CallTool` and `managed.Client.CallTool` signatures MUST NOT change; intent and principal arrive through ctx.
- **FR-014 (gate rule)**: A call is refused if and only if all of the following hold:
  - the mode is `enforce`;
  - its target tier (FR-015) is `write` or `destructive`;
  - the registry has at least one `unresolved` entry with tier `write|destructive` for the same `(server, current epoch, principal)`.

  Read-tier calls always pass.
- **FR-015 (tier source)**: The tier MUST be the target tool's annotation tier, computed with the existing `tierForAnnotations` rule. It is never the variant the caller chose. When ctx carries no tier (for example, a REST call), the choke point MUST derive the tier from the stored tool annotations with the same rule. An unknown tool is destructive.
- **FR-016 (refusal shape)**: The refusal MUST be an `isError` tool result, never a JSON-RPC protocol error.
  - It is recorded like the Spec 093 shed: activity status `rejected`, a `blocked` policy decision with a new `telemetry.BlockReasonPendingCallUnresolved`, and the 113-c classification `proxy_policy/proxy`.
  - Its text MUST be self-healing. It MUST name:
    - the blocking `call_id`(s), their tool and their age;
    - `expires_at`;
    - the exact call to make for each way to proceed: *wait* (until `expires_at` or a late completion), *reconcile* (call a read-tier tool on the same server), or *cancel* (`upstream_servers` `cancel_pending_call`).
  - REST returns HTTP `409 Conflict` with the same JSON body.
- **FR-017 (mode)**: A global `pending_call_gate` setting (`"off" | "warn" | "enforce"`), with a per-server override in `ServerConfig`.
  - In `warn` mode, a call the gate would refuse is dispatched anyway. It carries `pending_call_warning` in its result `_meta` and its activity record, and one Warn log is emitted.
  - The registry, the typed outcome and listing work in every mode, including `off`.
  - [NEEDS CLARIFICATION Q2: the default mode. Recommended default: **`warn`** for this release. Clients that ignore the new fields then behave exactly as today, which meets the backward-compatibility constraint. A documented plan flips the default to `enforce` for `stdio` servers in the next minor release, once warn-mode telemetry (FR-024) shows the false-positive rate. Alternative: `enforce` for stdio from day one, accepting that a client that retries a timed-out write will see a new refusal.]
- **FR-018 (reconciliation)**: A read-tier call by principal P on `(server, epoch)` reconciles when both of these hold:
  - it is dispatched after an entry's `unresolved_at`;
  - it completes successfully (`!isError`, no transport error).

  Such a call MUST move every `unresolved` entry of P on that `(server, epoch)` to `reconciled`.
  - [NEEDS CLARIFICATION Q3: is any successful read enough? mcpproxy cannot judge whether the read actually saw the effect of the earlier mutation. If the first call is still running, a read does not stop it from landing later. Recommended default: **yes, any successful read-tier call clears the gate**, as #1321 proposes (for example, `list_pages` after a navigation). Document it as "the agent has looked; the decision is now the agent's". Stricter alternative: reconciliation requires the read to name the call id through an `acknowledge_pending_call` argument on `call_tool_read`, which changes a frozen tool schema.]
- **FR-019 (cancel operation)**: `cancel_pending_call(call_id)` MUST:
  1. authorize the caller (FR-021);
  2. send `notifications/cancelled`, if the entry is `unresolved`, is on the current epoch, and has a known JSON-RPC id;
  3. mark the entry `cancelled`, whether or not the notification was sent;
  4. return `{call_id, state:"cancelled", cancel_sent: bool, note}`, where `note` says the upstream may ignore a cancellation.

  Cancelling an entry that is already terminal is an idempotent no-op that returns its current state.

**Security**

- **FR-020 (principal)**: The principal is the first of these that applies:
  1. the agent token's stable id (never its secret or hash);
  2. the server-edition authenticated user id;
  3. otherwise `local`. This covers every API-key, socket and unauthenticated caller of a personal-edition instance, which is one human.

  Profiles (Spec 108) do not split principals, so two clients of one human share a gate.
- **FR-021 (authorization)**:
  - Listing returns only the caller's own entries, unless the caller passes `IsAdmin()`.
  - Cancel requires write permission on the server, plus either ownership of the entry or `IsAdmin()`.
  - A non-owner who is not an admin gets the same `not_found` as for an unknown id.
  - A refusal never names another principal's call.
  - When an admin cancels another principal's call, the audit line records the admin as the actor.
- **FR-022 (no new secrets)**: Call ids are not credentials and grant nothing on their own. Every operation re-checks FR-021. Pending-call fields MUST NOT include tool arguments or results.

**Surfaces and observability**

- **FR-023 (parity)**: List and cancel MUST be available with identical fields on all three surfaces:
  - MCP: `upstream_servers` operations `pending_calls` and `cancel_pending_call`.
  - REST: `GET /api/v1/servers/{name}/pending-calls`, `GET /api/v1/pending-calls` and `POST /api/v1/pending-calls/{call_id}/cancel`.
  - CLI: `mcpproxy upstream pending list [--server] [-o json|yaml]` and `mcpproxy upstream pending cancel <call_id>`.

  `oas/swagger.yaml`, `contracts` and the generated TS types are updated to match. A parity test compares the three outputs for the same registry state.
- **FR-024 (activity and SSE)**:
  - `storage.ActivityRecord` and `contracts.ActivityRecord` gain `call_id`, `pending_state`, `pending_reason`, `late_outcome` and `resolved_at`, all `omitempty`.
  - `ActivityFilter` gains `pending_state` (REST `?pending_state=`, CLI `--pending-state`).
  - SSE emits `pending_call.unresolved`, `pending_call.resolved` and `pending_call.refused`.
  - Existing status values and filters are unchanged.
- **FR-025 (logging)**: `logCallInterrupted` (#1513) MUST add `call_id`, `pending_state` and `pending_reason`. A resolution and a refusal each log once at Info, with `server`, `call_id`, `connection_generation`, `principal_kind` (not the principal id) and `resolution`.
- **FR-026 (113-d interplay)**:
  - 113-d counts a timed-out call exactly once, at its original outcome.
  - A late completion MUST NOT record a second health outcome.
  - A gate refusal is a proxy-side refusal, so it is excluded from both 113-d's numerator and its denominator (113-d FR-061).
- **FR-027 (server status)**: Server payloads (REST, MCP `upstream_servers list`, CLI `upstream list -o json`) gain `pending_calls`, the count of `unresolved` entries visible to the caller. Pending calls do NOT change the unified `health` field.

**Configuration**

- **FR-028 (config)**:
  - New global fields:
    - `pending_call_gate` (FR-017);
    - `pending_call_ttl` (Duration, default `10m`, minimum `call_tool_timeout`);
    - `pending_call_history` (default `15m`);
    - `max_pending_calls_per_server` (default 32).
  - New per-server override: `pending_call_gate`.
  - All fields are `omitempty`. They round-trip through the config file, BBolt `UpstreamRecord` (the per-server field), `CopyServerConfig`, `MergeServerConfig`, REST/MCP `upstream_servers` patch, `DetectConfigChanges` and `docs/configuration.md`.
  - A change to any of them MUST NOT reconnect an upstream.

### Key Entities

- **PendingCall**: one dispatched call whose local wait ended. Its attributes are in FR-007 and its lifecycle in FR-008. It belongs to one `(server, epoch, principal)`.
- **PendingCallRegistry**: per-process, per-server sets of PendingCall, owned by `upstream.Manager`. Entries are resolved by epoch bumps, late responses, reads, cancels and the TTL.
- **Principal**: see FR-020.
- **Gate decision**: `allow | warn | refuse`, plus the blocking call ids. It is never persisted beyond the activity record.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For 100% of dispatched calls that hit `call_tool_timeout` or a caller cancel, the client result (when one is delivered) and the activity record carry a `call_id` and `pending_state=unresolved`. For calls refused before dispatch, the rate is 0%.
- **SC-002**: In `enforce` mode, a write or destructive retry by the same principal during the unresolved window reaches the upstream 0 times on every one of the four entry points. The fixture counts `tools/call` receipts. Read calls reach the upstream every time.
- **SC-003**: Each resolution path (late completion, reconcile, cancel, epoch bump, TTL) opens the gate within 100 ms of the triggering event in the test harness. Late completion is detected for 100% of stdio responses that arrive before `expires_at`.
- **SC-004**: With two agent tokens there are 0 cross-principal list entries and 0 successful cross-principal cancels, and the `not_found` bodies for "another principal's id" and "unknown id" are identical.
- **SC-005**: With `pending_call_gate: warn` (or `off`), a client that ignores the new fields gets results whose text is byte-identical to today's, for both timeouts and successes. These tests pass unchanged:
  - the existing `calltool_cancel_test.go`;
  - the Spec 105 epoch tests;
  - the toolsurface goldens, apart from the documented addition to the `upstream_servers` operation enum.
- **SC-006**: Registry memory is bounded. 10,000 timed-out calls against one server leave at most `max_pending_calls_per_server` live entries and at most that many detached waits. `go test -race` is clean, and no goroutine outlives its `expires_at` by more than 1s (goleak).
- **SC-007**: A real-instance check passes. The setup is an isolated data dir, a non-default port, and a stdio fixture tool that sleeps past the timeout and then succeeds. The expected sequence is:
  1. The timeout result carries the typed block.
  2. An immediate write is refused with a self-healing message.
  3. The late response shows as `completed_late` in `mcpproxy activity show`.
  4. The next write is dispatched.
- **SC-008**: For the same registry state, the MCP, REST and CLI list outputs are equal field for field (parity test).

## Assumptions

- Agents read `isError` result text, so the self-healing text alone improves behavior, even for clients that do not parse `pending_call`.
- The first concrete user is a stdio upstream with human-in-the-loop latency (Chrome DevTools MCP `--autoConnect`). HTTP upstreams benefit from the typed outcome but much less from the gate.
- A personal-edition instance has one human principal (FR-020).
- 113-c lands before or together with phase 2 of this spec. If it has not landed:
  - phase 1 ships without `error_class` / `fault_domain` stamping;
  - the refusal is audited like the Spec 093 shed (outcome `rejected`).

  When 113-c lands, its classifier produces `timeout`, `cancelled` or `proxy_policy` for these calls with no change here. It recognizes the typed `callTimeoutError` and `ErrPendingCallUnresolved` by `errors.Is`, not by text.

## Known Limitations

1. **Cancellation is advisory.** An upstream may ignore `notifications/cancelled`, and the MCP spec allows that. `cancelled` means "the agent accepted the ambiguity", not "the upstream stopped".
2. **Reconciliation is the agent's judgment.** mcpproxy cannot tell whether a read saw the mutation's effect (Q3).
3. **No late completion for HTTP/SSE.** mcp-go aborts the HTTP request when ctx ends. The server may still finish, but mcpproxy never learns the outcome. Those entries resolve by TTL, epoch, reconcile or cancel.
4. **No MCP Tasks.** Upstreams that support task augmentation would give a durable, protocol-level pending call. That is the right long-term design for those upstreams, and it belongs in a separate spec.
5. **No `waiting_human`**, unless Q1 is answered otherwise.
6. **In-memory only.** A restart forgets all entries. It also restarts every upstream, which makes the entries moot.

## Clarifications Needed (summary)

| # | Question | Recommended default |
|---|---|---|
| Q1 | Emit a `waiting_human` reason? (FR-004) | No. Reserve the value and revisit if an elicitation-in-flight signal is accepted |
| Q2 | Default `pending_call_gate` mode (FR-017) | `warn` this release. Flip stdio to `enforce` in the next minor |
| Q3 | Does any successful read reconcile? (FR-018) | Yes, as #1321 proposes. Document it as the agent's judgment |

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #1321`
- ❌ **Do NOT use**: `Fixes #1321`, `Closes #1321`, `Resolves #1321`

**Rationale**: close #1321 by hand after the gate is verified on a real stdio upstream.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

### Example Commit Message
```
feat(upstream): register unresolved tool calls and gate mutating retries

Related #1321

Spec 114 phase 2: pending-call registry keyed by (server, epoch,
principal), retry gate at Manager.callTool and code_execution,
self-healing refusal, warn/enforce modes.

## Testing
- go test -race ./internal/upstream/pending/... ./internal/upstream/...
- stdio fixture: timeout -> refused write -> late completion -> write passes
```
