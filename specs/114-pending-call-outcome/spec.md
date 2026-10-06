# Feature Specification: Typed Pending-Call Outcome and Call Correlation

**Feature Branch**: `114-pending-call-outcome`
**Created**: 2026-10-06
**Revised**: 2026-10-06 (review round 1, 29 findings; see "Revision notes" at the end)
**Status**: Draft (docs only, for maintainer review before any code)
**Input**: Issue #1321, "Typed pending-call outcome + call correlation for long-running stdio tool calls (follow-up to #1317)". The issue asks for four things:
1. A typed `waiting_human` / `deadline_exceeded` result that carries a call id.
2. Rejection of a blind retry while that call id is unresolved.
3. Explicit cancellation, or a fresh read-only reconciliation on the same connection generation, before another mutating call.
4. Structured logging at timeout.

**Related**:
- #1317 (closed): Chrome DevTools MCP `--autoConnect` consent dialog. The process kill was fixed in #1320.
- #1513 (merged): item 4, structured timeout logging.
- Spec 105: connection generation (`connectionEpoch`), pinned dispatch, and scope intersection.
- Spec 107: the audit line, its closed vocabularies and count invariants, and the tenant-session allowlist.
- Spec 108 / 109: profile tool policy, and the single tier function `contracts.AnnotationTier`.
- Spec 018: the intent variants `call_tool_read|write|destructive`.
- Spec 093: the concurrency limiter (admission) and its refusal shape.
- Spec 113 (open, PR #1495), especially 113-c (call-error taxonomy, PR #1502), 113-d (health from failure rate, PR #1504) and 113-e (HTTP session re-init, PR #1501).

**Evidence base** (what was read, and what is assumed):
- mcpproxy code was read at `origin/main` `44244b858`. The file:line anchors below refer to that commit and will drift.
- mcp-go was read from the module cache at `github.com/mark3labs/mcp-go@v1.0.0`, the version pinned at `go.mod:21`. It is not vendored in the repo.
- Spec 113 is **not present in this worktree's base**. Its text was read from commit `d5de9320d` (`specs/113-upstream-resilience/spec.md` on branch `113-upstream-resilience`). The FR numbers cited from it (FR-047, FR-049, FR-061, FR-062, FR-083) come from that commit and may change before it merges. That text does not mention #1321. Whether its final form overlaps with this spec is re-checked when it merges (Assumption A4).
- Anything labelled **[assumption]** below was not checked against code.

**Scope note**: #1513 already did item 4 of #1321. This spec covers items 1–3 only. #1513's PR body says it fixes #1321, but it does not. Keep #1321 open until this spec ships.

**Glossary**:
- *Call id*: an opaque id that mcpproxy mints for one upstream `tools/call` (`pc_` prefix). It is neither the JSON-RPC id nor the activity `request_id`, though the record links both.
- *Connection generation* / *epoch*: `managed.Client.connectionEpoch` (Spec 105). It is bumped under `epochMu` on every successful connect and every disconnect. Values come from a process-wide counter, so a value is never reused.
- *Dispatch record*: the registry's record of one call, from the moment the call passes the authoritative gate (FR-013) until it completes or reaches a terminal state. It is internal and never gates.
- *Unresolved call*: a dispatched call whose local wait ended (on a deadline or a caller cancellation) while the upstream may still be running it. Only unresolved entries gate.
- *Gate tier*: the tier that the gate and reconciliation use (FR-015). It is **not** `tierForAnnotations`. That function goes through `contracts.DeriveCallWith`, which maps a known tool with no annotations, or with no hints set, to `read` (`internal/contracts/intent.go:205-227`, `internal/server/mcp_code_execution.go:1744-1751`). Using it would make the gate do nothing for unannotated mutating tools. The gate tier is `contracts.AnnotationTier` (`internal/contracts/tier.go:40`), with `unannotated` mapped to `write` and an unresolvable tool mapped to `destructive`.
- *Mutating call*: a call whose gate tier is `write` or `destructive`.
- *Principal*: the caller identity that the gate is scoped to (FR-020).

## Context & Motivation

1. **Path.**
   - `handleCallToolVariant` (`internal/server/mcp.go:2640`) dispatches through `dispatchOnEpoch` (`:8253`) to `Manager.CallTool` / `CallToolOnEpoch` (`internal/upstream/manager.go:1550,1563`; `callTool` is at `:1569`).
   - `managed.Client.callTool` checks the generation and runs admission (`acquireAdmission`). It then **re-checks the generation after the admission wait**, reads `callEpoch`, and calls `invoker.CallTool(core.WithConnectionGeneration(ctx, callEpoch), …)` (`managed/client.go:1170-1204`).
   - `core.Client.CallTool` (`core/client.go:462`) creates the `call_tool_timeout` context and calls mcp-go's `client.CallTool` (`:554`).
   - Four other entry points reach the managed client:
     - the direct surface (`mcp_routing.go`);
     - `code_execution` sub-calls (`mcp_code_execution.go:1032-1035`), which call `CallTool` / `CallToolOnEpoch` on the managed client directly, not through `Manager.callTool`;
     - REST `POST /api/v1/tools/call` (`httpapi/server.go:6090`);
     - **activity replay**. `POST /api/v1/tool-calls/{id}/replay` (`httpapi/server.go:1185`) calls `Runtime.ReplayToolCall` (`runtime/runtime.go:1484`), which calls `client.CallTool` on `upstreamManager.GetClient(...)` at `:1571`. This bypasses `Manager.callTool`.

     The only seam that all five entry points share is `managed.Client.callTool`.
2. **Timeout.**
   - `call_tool_timeout` defaults to 2 minutes (`config.go:1789`).
   - mcp-go's `Stdio.SendRequest` selects on three things: the response, `c.done` and `ctx.Done()`. On `ctx.Done()` it deletes the response channel and returns `ctx.Err()` (`client/transport/stdio.go:~594-650`).
   - `readResponses` then drops any response whose id has no channel (`stdio.go:571-574`).
   - `client.Client.CallTool` passes the caller's ctx straight to `transport.SendRequest` (`client/client.go:265`).
   - Consequence: **once the ctx given to `SendRequest` is done, the late response cannot be observed.** mcpproxy can only see a late response if the ctx it gave the inner `SendRequest` never cancels, from the start of the call (FR-010).
   - **Nothing is sent upstream**: there is no `notifications/cancelled`, so the child keeps working.
   - `core.CallTool` returns `CallTool '<tool>' timed out after <d>` (`core/client.go:570`). `Manager.callTool` wraps that in its generic branch (`manager.go:1757-1758`): `tool '<t>' on server '<s>' failed: <err>. Check server configuration, authentication, and tool parameters`.
3. **The process survives.** Since #1320, a timeout no longer marks the server unhealthy or bumps the epoch (`managed/calltool_cancel_test.go`).
4. **The client gets an untyped error, and its envelope differs per surface.**
   - `call_tool_*`: `createDetailedErrorResponse` (`mcp.go:6897`) falls through to a JSON `TextContent`, `{"error","server_name","tool_name","troubleshooting":"Check server configuration, connectivity…"}` (`mcp.go:6979-6987`).
   - Direct surface: plain text, `Error calling <s>:<t>: <err>` (`mcp_routing.go:726-729`).
   - `code_execution`: the sub-call returns a Go error to the JS runtime, not an `isError` `CallToolResult` (`mcp_code_execution.go:1089-1093`).
   - REST: HTTP 500 with `Failed to call tool: <err>` (`httpapi/server.go:6147-6152`).
5. **mcpproxy forgets the call.**
   - `beginInFlightCall` (`managed/client.go:215`) counts only calls that are inside `callTool`, and its count ends at the timeout.
   - Nothing stops an immediate second mutating call while the first is still running upstream.
6. **mcp-go cannot cancel or correlate by itself.**
   - It defines `notifications/cancelled`, but no client code sends it.
   - `client.Client` assigns the JSON-RPC id internally and does not return it.
   - A `transport.Interface` decorator can see both the ctx and the id: `SendRequest(ctx, req)` receives `req.ID`, which is a typed `mcp.RequestId`.
   - mcpproxy builds stdio clients with `client.NewClient(stdioTransport)` (`core/connection_stdio.go:244`), so a decorator fits there. `c.stderr` is taken from the concrete `*transport.Stdio` (`:282`), not from the client, so wrapping does not affect it.
   - The stdio client path relies on these interfaces:
     - `transport.Interface`;
     - `transport.BidirectionalInterface`, type-asserted at `client/client.go:201` and needed for sampling, elicitation and roots;
     - `transport.HTTPConnection` (`client/client.go:403`), which `Stdio` does not implement;
     - mcpproxy's own `oauthTransport` assertion (`core/client.go:731-737`), which `Stdio` does not implement.
7. **Inbound cancellation cannot be attributed.**
   - mcp-go's server wraps each request handler's ctx in a plain `context.WithCancel` (`server/request_handler.go:111-120`).
   - It handles an inbound `notifications/cancelled` itself, before any user notification handler runs (`server/server.go:2477-2486`).
   - The handler's ctx therefore carries no cause. mcpproxy cannot tell a client's explicit `notifications/cancelled` apart from a client-side timeout, an SSE disconnect, a `code_execution` script deadline or mcpproxy's own shutdown.
   - #1513's `logCallInterrupted` labels every end of the parent ctx as `caller_cancel` (`core/client.go:1055-1061`).
8. **Progress is ignored.** `core.registerNotificationHandler` drops every notification except `list_changed` (`core/connection_lifecycle.go:214-223`).
9. **Tasks.** mcp-go v1.0.0 has `CallToolParams.Task` and `GetTask`/`CancelTask`. mcpproxy never sets `Params.Task`. Tasks are out of scope (Known Limitation 4).
10. **113-c (unmerged, commit `d5de9320d`).** It adds:
    - `internal/callerr`, with `error_class` (`timeout`, `proxy_policy`, `cancelled`, …);
    - a total mapping to the Spec 107 audit `error_class` (FR-047);
    - a rule that the classifier does not read message text (FR-049).
11. **Auth identity.**
    - `auth.AgentToken` has no stable id field (`auth/agent_token.go:95-148`). It has `Name` (the store key), `TokenPrefix` (which changes on regenerate), `UserID`, `ClientID` (client tokens only) and `CreatedAt`.
    - `AuthContext` carries `AgentName`, `TokenPrefix`, `UserID` and `ClientID` (`auth/context.go:34-74`).
    - Permissions are exact-match strings (`HasPermission`, `context.go:150-163`). `read` does not imply `write`, and `destructive` does not imply `write`.
12. **SSE scoping.**
    - `/events` is open to agent tokens.
    - `eventVisibleToCaller` filters scoped callers by the server named in the payload. For identity-bearing event types that name no server, it fails closed (`httpapi/sse_scope.go:260-287`).
    - Filtering by owner exists only for activity attribution (`:331-346`).

## Scope Boundary

| Already exists | Reused, not rebuilt |
|---|---|
| `connectionEpoch`, `WithConnectionGeneration`, the generation re-check after admission | The authoritative gate sits at that same post-admission point (FR-013) |
| `contracts.AnnotationTier` (Spec 109-a FR-028) | The gate tier (FR-015), with a documented mapping for `unannotated` |
| The Spec 093 limiter's `LimitError`, which survives `Manager.callTool` as a typed error (`manager.go:1714-1718`) | The pattern for a typed refusal that reaches every surface |
| The Spec 107 audit funnel and its `authz` / `tool_call` phases | Extended by one reason value for each line type (FR-030) |
| 113-c `error_class` / `fault_domain` | Timeouts stay `timeout`, and refusals are `proxy_policy/proxy`. **No second error vocabulary** |
| #1513 `logCallInterrupted` | Gains `call_id` and the pending fields. `cancellation_source` is unchanged |
| `mintCorrelationID` (`mcp.go:994`) | The pattern for minting `call_id` |

**Out of scope**:
- MCP Tasks (`Params.Task`) support.
- Durable (BBolt) persistence of the registry across mcpproxy restarts.
- A `waiting_human` reason inferred from anything other than an explicit signal (Q1).
- **Forwarding any caller cancellation upstream** in v1 (FR-012, Q4).
- Changes to the `call_tool_timeout` defaults.
- Web UI and macOS tray screens beyond a read-only count.
- Changes to 113-e session re-init semantics.
- Management of pending calls by server-edition tenant sessions (`/user/*`) (FR-023).

## User Scenarios & Testing *(mandatory)*

### User Story 1: A timeout says the call may still be running, and gives a call id (Priority: P1)

An agent calls `call_tool_write chrome-devtools:navigate_page`. Chrome shows a consent dialog, and the call runs past `call_tool_timeout`. With this feature the agent gets the existing error string plus a typed block. The block carries the call id, `state: unresolved`, a reason, the connection generation, and the actions available to resolve the call.

**Why this priority**: every other story needs a call id and a state. The change is purely additive.

**Independent Test**: use a stdio fixture tool that sleeps past a 1s `call_tool_timeout`, and assert the per-surface envelope (FR-003a).

**Acceptance Scenarios**:

1. **Given** a stdio server whose tool sleeps 5s, and `call_tool_timeout: 1s`, **When** the client calls the tool with `call_tool_write`, **Then**:
   - the result is `isError:true`;
   - the `error` value in the JSON body is byte-identical to today's, meaning the full Manager-wrapped string, captured as a golden from the base commit;
   - the body and `_meta["io.mcpproxy/pending_call"]` both carry the FR-003 object with every required key, including `server` and `tool`.
2. **Given** the same tool sends `notifications/progress` with this call's injected progress token within 30s before the deadline, **Then** `reason` is `still_running`.
3. **Given** a call that is refused before dispatch (by admission, quarantine, a generation change, a connection that is down, or the gate itself), **Then** no `pending_call` block is added and no dispatch record exists.
4. **Given** the activity record and the output of `mcpproxy activity show <id> -o json`, **Then** both carry `call_id`, `pending_state` and `pending_reason`, and `error_class` is `timeout` once 113-c is merged.
5. **Given** each of the five entry points (call_tool_*, the direct surface, code_execution, REST and replay), **Then** each produces its FR-003a envelope. There is one contract test per surface.

---

### User Story 2: A blind mutating retry is refused with a self-healing error (Priority: P1)

While the first call is unresolved, mcpproxy refuses the same principal's mutating calls to that server before they reach the upstream.

**Independent Test**: with the gate in `enforce` mode, time out a write call. Then issue a second write and a read.

**Acceptance Scenarios**:

1. **Given** an unresolved write call `pc_A` on `srv` for principal P, in mode `enforce`, **When** P calls any tool on `srv` whose gate tier is `write` or `destructive`, **Then** the call does not reach the upstream (the fixture's receipt count does not change), and:
   - the result is `isError:true`. The text names `pc_A`, its tool, its age and `expires_at`, and lists **only the resolution actions that P can take on this build and configuration** (FR-016);
   - the activity status is `rejected`, with a `blocked` policy decision and reason `pending_call_unresolved`;
   - the audit line follows FR-030;
   - the 113-c classification is `proxy_policy/proxy`.
2. **Given** the same state, **When** P calls a tool on `srv` that has `readOnlyHint: true`, **Then** the call passes the gate.
3. **Given** the same state, **When** P calls an **unannotated** tool on `srv`, **Then** the call is refused, because its gate tier is `write`. **When** an unannotated call itself times out, **Then** it registers a gating entry.
4. **Given** the same state, **When** P calls a write tool on a different server, or another principal Q calls a write tool on `srv`, **Then** neither call is affected.
5. **Given** mode `warn`, **Then**:
   - the write is dispatched;
   - its result `_meta` and its activity record carry `pending_call_warning` naming `pc_A`;
   - one Warn log is emitted.
6. **Given** the same state, reached through the direct surface, `code_execution`, REST `POST /api/v1/tools/call`, or **activity replay of the timed-out call**, **Then** the authoritative gate refuses each one. None of them bypasses the gate.
7. **Given** this sequence, **Then** the authoritative check after admission refuses B, and B does not reach the upstream:
   1. A write B passes the advisory check before admission while `pc_A` is still in flight.
   2. B waits in the admission queue.
   3. `pc_A` becomes unresolved during that wait.

   This is a deterministic test: hold B in a 1-slot queue behind A, time A out, then release B.
8. **Given** `call_tool_read` aimed at a write-annotated tool (`readOnlyHint:false`), **Then**:
   - under both `strict` settings, the intent validator allows the call. The validator rejects only a non-destructive variant aimed at a tool with `destructiveHint:true`, and only in strict mode (`contracts/intent.go:162-196`);
   - **the gate still refuses the call**, because it uses the target tool's gate tier, never the variant.

---

### User Story 3: The agent can resolve the ambiguity (Priority: P1)

**Independent Test**: for each path, time out a write, resolve it, then assert that a new write is dispatched.

**Acceptance Scenarios**:

1. **Reconcile**: **Given** `pc_A` is unresolved, **When** P makes a successful call to a tool on `srv` with gate tier `read`, dispatched after `pc_A.unresolved_at` (and, for stdio, on `pc_A`'s epoch), **Then** `pc_A` becomes `reconciled`, subject to Q3. This also holds when the read goes through `code_execution` or replay.
2. **Reconcile ordering**: **Given** a read R dispatched at t0, and a write `pc_B` that becomes unresolved at t1 > t0, **When** R completes successfully at t2 > t1, **Then** `pc_B` stays unresolved.
3. **Cancel**: **Given** `pc_A` is unresolved, **When** P calls `upstream_servers {operation: cancel_pending_call, call_id: pc_A}`, or the REST or CLI equivalent, **Then**:
   - for stdio, if the transport instance that sent `pc_A` is still open, mcpproxy sends `notifications/cancelled {requestId, reason}` through **that instance**. `requestId` is `pc_A`'s JSON-RPC id, with its original JSON type;
   - `pc_A` is marked `cancelled`, whether or not the notification was sent;
   - the response carries `cancel_sent`. When it is false, the response also carries `cancel_error` (`transport_closed | unsupported_transport | send_failed | id_unknown`).
4. **Late completion (stdio)**: **Given** `pc_A` is unresolved and the upstream responds before `expires_at`, **Then**:
   - `pc_A` becomes `completed_late`, with a `late_outcome` (FR-010);
   - the activity record is updated (FR-024a);
   - an SSE `pending_call.resolved` event is emitted to the subscribers allowed to see it;
   - the gate opens;
   - the late result content is not delivered to any client.
5. **Late response racing the deadline**: **Given** the response arrives after the caller's deadline fires but before the timeout error has propagated back to `core.Client.CallTool`, **Then** the entry ends as `completed_late`, never as `unresolved`. This is a deterministic interleaving test.
6. **Connection reset (stdio)**: **Given** `pc_A` is unresolved on epoch 7, **When** the stdio server restarts or reconnects (including through the `reconnect_on_use` path in `Manager.callTool`), **Then** `pc_A` becomes `connection_reset` and the gate opens. The result text and the docs say that this is accepted ambiguity: killing the child does not undo an action it has already handed off (FR-009).
7. **HTTP/SSE across a reconnect**: **Given** `pc_A` is unresolved on an HTTP server, **When** the client reconnects (the epoch moves), **Then** `pc_A` stays unresolved and keeps gating. It resolves only by reconcile, cancel or TTL.
8. **Expiry**: **When** `pending_call_ttl` passes with no other resolution, **Then** `pc_A` becomes `expired` and the gate opens.

---

### User Story 4: Callers cannot see, cancel, or unblock each other's calls (Priority: P1)

**Independent Test**: two agent tokens, T1 and T2, on a shared server and on disjoint servers.

**Acceptance Scenarios**:

1. **Given** T1 has an unresolved `pc_A`, **When** T2 lists pending calls, **Then** T2 sees none of T1's entries, and `pc_A` does not gate T2.
2. **Given** T1 has `pc_A`, **When** T2 tries `cancel_pending_call pc_A`, **Then** the response is byte-identical to the `not_found` for an id that does not exist.
3. **Given** T1 and T2 are both subscribed to `/events`, **When** `pc_A` becomes unresolved, a call is refused because of it, and it then resolves, **Then** T1 receives all three `pending_call.*` events and T2 receives none of them. This holds on a shared server and on a server T2 cannot see.
4. **Given** T2 floods `srv` with timed-out calls of read and write tier, **Then**:
   - T1's gating entry is never evicted;
   - T2's detached waits never push T1's calls out of detached mode, as long as T1 stays within its own share (FR-007a).
5. **Given** an admin caller (`IsAdmin()`), **Then** it can list and cancel every entry. The activity record and the log name the admin as the actor. For the audit line, see FR-030.
6. **Given** a token with only the `read` permission, **Then** it can list its own entries. For entries it may not cancel, `cancel_pending_call` returns the `not_found`-shaped refusal (FR-021).
7. **Given** a token with the `destructive` permission but not `write`, **Then** it can cancel its own destructive-tier entries but not its write-tier entries.
8. **Given** T1's token is narrowed after `pc_A` was created (the server is removed from `allowed_servers`, or the token's profile pin moves to a profile without `srv`), **Then** T1 can no longer list `pc_A`, cancel it, or receive events about it. Each attempt gets a `not_found` that does not reveal whether the entry exists.
9. **Given** T1's token is deleted and a new token with the same name is created, **Then** the new token is a different principal and sees none of the old entries. **Given** T1's token is regenerated instead, **Then** it keeps its principal and its entries.

---

### User Story 5: Operators see pending calls (Priority: P2)

**Acceptance Scenarios**:

1. **Given** unresolved calls, **When** the operator lists them through any of these, **Then** all three return the same entries with the same fields (parity test):
   - CLI `mcpproxy upstream pending list [--server srv] -o json`;
   - REST `GET /api/v1/servers/{name}/pending-calls`;
   - MCP `upstream_servers operation: pending_calls`.
2. **Given** a server with at least one unresolved call, **Then** `upstream list` and the server's REST payload show `pending_calls: N`, the count visible to the caller. The unified `health` field does not change.
3. **Given** a timeout, **Then** the #1513 Warn log line also carries `call_id`, `pending_state` and `pending_reason`.

### Edge Cases

- **The caller's ctx ends after dispatch.** Causes include a client-side timeout shorter than `call_tool_timeout`, a client `notifications/cancelled`, an SSE disconnect, and a `code_execution` script deadline.
  - The call is registered as unresolved, with `reason` set per FR-004.
  - **Nothing is sent upstream** (FR-012). In #1317 the client-side timeout is the likely case, and forwarding it would kill the consent dialog.
- **mcpproxy shutdown**: shutdown ends the in-flight calls. No entries are created after shutdown starts, and none are reported.
- **Many timeouts in a row**: each one gets its own entry. The refusal names the oldest blocking entry and gives the count. FR-007a sets the bounds.
- **A read-tier call that times out**: it is registered as a non-gating entry, so `pending_calls` stays truthful. It never gates and never counts as a reconciliation.
- **Same call id reused**: this cannot happen. Ids carry 80 random bits.
- **`code_execution` script deadline**: every sub-call cut off after dispatch gets its own entry, and the script error lists those call ids (FR-003a).
- **HTTP, streamable HTTP and SSE upstreams**:
  - The outcome and the gate apply.
  - There is no late-completion observation (FR-010).
  - Entries survive epoch bumps (FR-009).
  - A cancel is sent only if the same transport instance (same epoch) is still open.
- **113-e session re-init with an unchanged toolset**: there is no epoch bump (113-e FR-083 at `d5de9320d`), so entries survive the re-init.
- **mcpproxy restart**: the registry is in memory and is lost.
  - Stdio children are killed along with mcpproxy.
  - Remote HTTP/SSE servers are not restarted, and they may still complete the work.
  - This is accepted ambiguity (Known Limitation 6). The entries are not moot.
- **Server quarantined while entries exist**: quarantine keeps the server dialed (`manager.go:2174-2176`), so the epoch does not move. Entries stay unresolved, and quarantine refuses the calls anyway.
- **Server disabled while entries exist**: the disconnect bumps the epoch. Stdio entries become `connection_reset`. HTTP/SSE entries persist.
- **Clock skew**: the TTL uses the monotonic clock.

## Requirements *(mandatory)*

### Functional Requirements

**Call correlation**

- **FR-001 (call id)**: Every upstream `tools/call` that reaches `managed.Client.callTool` MUST get a `call_id`: `pc_` + 16 base32 characters (80 bits).
  - The entry point mints it. If the entry point did not, `managed.Client.callTool` mints it.
  - It travels in ctx (`pending.CallMeta`), never in a frozen signature.
  - It is recorded on the activity record for all five entry points.
- **FR-002 (JSON-RPC id capture)**: For stdio upstreams, a `transport.Interface` decorator installed at `client.NewClient` MUST record `call_id → JSON-RPC request id`. It does this in `SendRequest(ctx, req)` when `req.Method == "tools/call"` and ctx carries `CallMeta`.
  - The id MUST be stored as the typed `mcp.RequestId`, or as its raw JSON, never as a string.
  - `notifications/cancelled` MUST serialize `requestId` with the same JSON type and value.
  - Tests cover numeric `1`, string `"1"`, and an integer above 2^53.
  - The decorator MUST preserve every interface listed in Context item 6. It implements `transport.Interface`, and `transport.BidirectionalInterface` when the inner transport does. It does not claim `HTTPConnection` or `oauthTransport`.
  - A reflection test asserts that the wrapper satisfies exactly the same set of these interfaces as the inner `*transport.Stdio`.

**Typed outcome**

- **FR-003 (pending block)**: When a dispatched call ends because a ctx ended (FR-004), the error MUST carry a `pending_call` object.
  - **Required** keys: `call_id`, `server`, `tool`, `state` (`unresolved`), `reason`, `connection_generation`, `transport` (`stdio|http|sse|streamable-http`), `upstream_may_still_be_running` (`true`), `late_completion_observable` (bool, FR-010), `dispatched_at`, `unresolved_at`, `expires_at` and `resolve`.
  - `resolve` lists only the actions available to this caller under this build and configuration (FR-016).
- **FR-003a (per-surface envelope)**: Each surface MUST preserve its legacy error string verbatim. That is today's full string at that surface, including Manager's prefix and suffix. The additions per surface are:
  - **`call_tool_*`**:
    - the existing JSON `TextContent` keeps every existing key, and `error` is unchanged;
    - `pending_call` is added;
    - for this case, `troubleshooting` changes to say that the upstream may still be running. This is the only existing value that changes;
    - `_meta["io.mcpproxy/pending_call"]` carries the same object.
  - **Direct surface**: the text block keeps `Error calling <s>:<t>: <err>` and appends one line: `mcpproxy: the upstream may still be running this call (call_id <id>); see _meta io.mcpproxy/pending_call.` It also carries the same `_meta`.
  - **`code_execution`**:
    - the error thrown into the script exposes `pendingCall` (the FR-003 object);
    - for a refusal, the error has `code: "PENDING_CALL_UNRESOLVED"`;
    - the final `code_execution` result lists every `call_id` that became unresolved during the run.
  - **REST `/tools/call`**: a timeout keeps status 500, and the error envelope gains `pending_call`. A refusal is a 409 (FR-016).
  - **Replay**: the replay record gains `call_id` and `pending_state`.
  - A contract test per surface pins the envelope, and a golden pins the legacy string.
- **FR-004 (reason)**: `reason` is one of:
  - `deadline_exceeded`: mcpproxy's own `call_tool_timeout` fired while the parent ctx was still live;
  - `still_running`: the same as `deadline_exceeded`, and a matching `notifications/progress` arrived within 30s before the deadline. The 30s window is a documented constant, not a config field;
  - `caller_deadline`: the parent ctx ended with `DeadlineExceeded` (a client-side or script deadline);
  - `caller_cancelled`: the parent ctx was cancelled. The cause may be an explicit client cancel, a disconnect, or something else; mcp-go does not distinguish them (Context item 7).

  Rules:
  - `context.Cause` is consulted first. An in-process cause that mcpproxy sets, for example a script deadline wrapped with `context.WithTimeoutCause`, maps to `caller_deadline`.
  - A shutdown cause creates no entry.
  - mcpproxy MUST NOT emit `waiting_human` (Q1).
- **FR-005 (progress token)**: For stdio upstreams, when the outgoing request has no `_meta.progressToken`, `core.Client.CallTool` sets it to `call_id`.
  - The notification handler routes each `notifications/progress` whose token matches a live dispatch record to `Registry.Touch`.
  - It does not forward these notifications, and it does not log them at Info.
  - Progress for a token that the caller supplied is out of scope.
- **FR-006 (orthogonal to 113-c)**: The pending fields are metadata alongside the 113-c classification, never a new `error_class` value.
  - A timed-out call is `timeout`.
  - A caller-cancelled call is `cancelled`.
  - A refusal is `proxy_policy/proxy`.
  - 113-c must recognize `pending.ErrPendingCallUnresolved` with `errors.Is`. **[assumption]** This needs one new arm in 113-c's classifier; it is not present at `d5de9320d`.

**Pending-call registry**

- **FR-007 (registry)**: An in-memory registry (`internal/upstream/pending`), owned by `upstream.Manager` and handed to every managed client.
  - Record fields: `call_id`, `server`, `transport`, `epoch`, `principal`, `tool`, `gate_tier`, `json_rpc_id` (typed, optional), `dispatched_at`, `unresolved_at`, `expires_at`, `reason`, `state`, `last_progress_at`, `late_outcome`, `resolved_at` and `resolution_actor` (the principal kind only).
- **FR-007a (bounds)**: Every bound below is a hard bound, enforced at insertion.
  - **Gating entries**: at most `max_pending_calls_per_principal` (default 16) unresolved gating entries per `(server, principal)`.
    - Past the cap, that principal's own oldest gating entry is evicted as `expired`, with a Warn. The gate stays closed, because the other entries under the cap remain.
    - **Insertions by another principal never evict a gating entry.**
  - **Non-gating entries** (read tier) and **terminal tombstones**: at most 256 per server, combined.
    - They are evicted oldest first, terminal records before read entries.
    - Terminal records are also dropped after `pending_call_history` (default 15m).
  - **Detached waits** (FR-010): at most `max_detached_waits_per_server` (default 64).
    - One principal may hold at most half of them.
    - The cap is checked **at dispatch time**, against the detached calls in flight.
    - A call over its share is dispatched attached, which is today's behavior. If it times out, its entry has `late_completion_observable:false`.
  - **Correlation state** (`call_id → json_rpc_id` in the decorator): one item per dispatch record, removed when the wait ends. It is bounded by the detached waits plus the attached in-flight calls. Attached calls are themselves bounded by `call_tool_timeout`, and by the Spec 093 admission limits when those are configured.
  - **Global**: at most 4096 records across all servers. Past this cap, only non-gating and terminal records are evicted, and new non-gating records are counted but not stored.
- **FR-008 (states and transitions)**:
  - The allowed transitions are:
    - `dispatched → completed` (normal completion; the record is dropped);
    - `dispatched → unresolved`;
    - `dispatched → completed_late` (the response arrived before the deadline path published the entry, FR-010a);
    - `dispatched → connection_reset` (a stdio epoch moved before publication);
    - `unresolved → completed_late | reconciled | cancelled | connection_reset | expired`.
  - Terminal records are **tombstones**. No transition leaves a terminal state. Every later `MarkUnresolved`, `ResolveLate`, `Cancel` or `Reconcile` for that `call_id` is an idempotent no-op that returns the terminal state.
  - Only `unresolved` entries gate.
- **FR-009 (epoch resolution)**: One per-server registry mutex serializes every registry transition for that server. The mutex is never held across I/O or foreign callbacks.
  - On a stdio epoch bump, `ResolveEpoch(server, newEpoch)` runs under `epochMu`, in the same critical section as the store at `managed/client.go:578-580` and `:669-671`. It:
    - moves every non-terminal stdio record with `epoch < newEpoch` to `connection_reset`;
    - raises the server's `epoch_floor` to `newEpoch`.
  - `MarkUnresolved` for a record whose `epoch < epoch_floor` produces a `connection_reset` tombstone, never an `unresolved` entry. This closes the window in which a delayed timeout path could register an entry after the epoch has already moved.
  - **An epoch bump does not resolve HTTP/SSE records**, because the remote server may keep working across a reconnect. The gate key for these records ignores the epoch.
  - `connection_reset` means "mcpproxy can no longer observe or cancel this call". It does not mean "the work stopped". For stdio the child is killed, but side effects it already handed off (a Chrome navigation, a file write) may still land. The refusal text and the docs say so. This is accepted ambiguity (Q5).
- **FR-010 (late completion, stdio)**: For stdio `tools/call` requests that carry `CallMeta`, the decorator MUST use an **always-detached** design:
  - **Dispatch**: it calls the inner `SendRequest` in a goroutine with `context.WithDeadline(context.WithoutCancel(ctx), dispatched_at + remaining(ctx) + pending_call_ttl)`. The deadline is fixed at dispatch. The inner request ends at the first of: a response, a transport close (`c.done`), or that deadline.
  - **Return**: it returns to the caller at the first of the inner result or `ctx.Done()`. **Ties go to the result**: after `ctx.Done()`, it does a non-blocking receive on the result channel before it reports the deadline.
  - **Late response**: if the caller's ctx ended first, the goroutine keeps waiting. When the response arrives, it calls `ResolveLate(call_id, outcome)`. `late_outcome` is one of:
    - `success`;
    - `tool_error` (`isError:true`);
    - `jsonrpc_error` (a JSON-RPC error response);
    - `malformed` (a result that cannot be parsed).
  - **Other endings**:
    - A transport close yields no late outcome. The record stays unresolved until FR-009 or the TTL resolves it.
    - The inner deadline yields `expired`, unless the TTL sweeper has already expired the record.
  - **Late content**: the late result content:
    - MUST pass through the existing sensitive-data detector. Its detections are recorded on the activity record, as for any response;
    - MUST NOT be stored beyond what today's policy stores for a successful response;
    - MUST NOT be returned to any client.
  - **Cost**: every stdio `tools/call` pays for one goroutine, one buffered channel and one select. That is at most tens of µs and about 2–4 KB of stack. The plan states this as a performance goal, and a benchmark measures it. Calls over the FR-007a detached share use today's attached path.
  - HTTP and SSE transports do no detached waits.
- **FR-010a (publication handshake)**:
  - `Begin` creates the dispatch record, in state `dispatched`, at the authoritative gate, before the transport is called.
  - `ResolveLate` on a `dispatched` record marks it `responded_late` internally. The later `MarkUnresolved` on that record yields `completed_late` and returns that state. The error path then reports `pending_call.state: completed_late`. The content is still not delivered.
  - `MarkUnresolved` on an `unresolved` record is a no-op.
  - Deterministic tests cover:
    - a response before registration;
    - a response exactly at the deadline (a tie);
    - a transport close during the wait;
    - an epoch bump between the deadline and `MarkUnresolved`.
- **FR-011 (no upstream cancel on mcpproxy's deadline)**: mcpproxy MUST NOT send `notifications/cancelled` when its own deadline fires. In #1317 the long call was waiting on a human consent.
- **FR-012 (no forwarding of caller cancellation in v1)**: mcpproxy MUST NOT send `notifications/cancelled` upstream when the *caller's* ctx ends, whatever the reason.
  - Rationale: the handler ctx cannot be attributed to an explicit client cancel (Context item 7). Forwarding would also fire on a client-side tool timeout shorter than `call_tool_timeout`, a script deadline, an SSE disconnect or a shutdown. Each of those would kill exactly the work that FR-011 protects.
  - Only `cancel_pending_call` (FR-019) sends a cancellation.
  - Q4 covers a future `pending_call_forward_cancel` mode, once an attributable signal exists.

**Retry gate**

- **FR-013 (gate placement)**: The **authoritative** gate runs in `managed.Client.callTool`, immediately after the post-admission generation re-check and before `invoker.CallTool` (`managed/client.go:1195-1204`).
  - It is one registry operation, `CheckAndBegin(meta, server, callEpoch)`, under the per-server registry mutex. The same mutex serializes `MarkUnresolved`, so "B passes the gate" and "A becomes unresolved" are totally ordered:
    - if A's publication wins, B is refused;
    - if B's check wins, B was dispatched while A was still in flight. That is concurrency, not a blind retry, and it is allowed.
  - Because the gate sits at the managed seam, it covers all five entry points, including activity replay.
  - An **advisory** check also runs before admission, in `Manager.callTool` and in the `code_execution` dispatch, so that a refused call usually does not take a concurrency slot. The advisory check never admits a call on its own authority.
  - The signatures of `Manager.CallTool`, `CallToolOnEpoch` and `managed.Client.CallTool` stay frozen. `CallMeta` arrives through ctx.
  - Every entry point MUST set `CallMeta` (principal, gate tier and call id).
  - A call that reaches the managed seam **without** `CallMeta` is gated fail-closed:
    - its gate tier is `destructive`;
    - its principal comes from `auth.AuthContextFromContext(ctx)`, or is `local`;
    - a Debug log names the caller.
  - A test enumerates the five entry points and asserts that each one sets `CallMeta`.
- **FR-014 (gate rule)**: A call is refused if and only if all of these hold:
  - the mode is `enforce`;
  - its gate tier is `write` or `destructive`;
  - the registry has at least one `unresolved` gating entry for the same server and principal;
  - for stdio only, that entry is on the same epoch.
- **FR-015 (gate tier)**: `gateTier(annotations, found)` is defined as:
  - a tool that is not found or cannot be resolved → `destructive`;
  - otherwise `contracts.AnnotationTier(annotations)`, with `unannotated` mapped to `write`.

  So a tool counts as `read`, for both the gate and reconciliation, **only** if it has an explicit `readOnlyHint:true` and no `destructiveHint:true`.
  - This is a new, deliberately conservative rule that applies only to this feature. It does not change `tierForAnnotations`, call-variant routing, token permission checks or the Spec 108 profile policy.
  - Entry points that hold the annotations compute it. Replay and REST derive it from the stored tool annotations.
  - A truth-table test covers nil annotations, empty hints, each hint combination, and a tool that is not found.
- **FR-016 (refusal shape)**: The refusal is an `isError` tool result, never a JSON-RPC protocol error.
  - It is recorded like the Spec 093 shed: activity status `rejected`, a `blocked` policy decision with `telemetry.BlockReasonPendingCallUnresolved`, and the 113-c classification `proxy_policy/proxy`.
  - The text names the blocking `call_id`(s), their tool, their age and `expires_at`.
  - It names **only the available actions**, computed per caller:
    - *wait*: always available;
    - *reconcile*: always available. The text names "a tool with readOnlyHint:true on <server>";
    - *cancel*: available only if this build ships FR-019 **and** the caller can reach a cancel surface. That means all of these hold:
      - `upstream_servers` is registered (neither `disable_management` nor `read_only_mode` is set);
      - `upstream_servers` is visible under the caller's profile. It is annotated `destructiveHint:true`, so a `max_tier` below `destructive`, or a `management_tools` exclusion, hides it;
      - the caller holds the permission that FR-021 requires.

      Otherwise the text omits `cancel`. Operators can still cancel through REST or the CLI.
  - REST returns `409 Conflict` with the same JSON body.
  - The typed error passes through `Manager.callTool` unchanged, as `LimitError` does today.
- **FR-017 (mode)**: A global `pending_call_gate` setting (`off|warn|enforce`), with a per-server override in `ServerConfig`.
  - In `warn` mode, a call that the gate would refuse is dispatched anyway. It carries `pending_call_warning` in its `_meta` and its activity record, and one Warn log is emitted.
  - The registry, the typed outcome and listing work in every mode, including `off`.
  - Config validation accepts `enforce` only in a build that ships late completion and cancel (FR-034).
  - Q2: the default mode. Recommended: `warn`.
- **FR-018 (reconciliation)**: Reconciliation is per entry and runs at the managed seam, so reads through `code_execution`, replay and REST also reconcile. A completed call R reconciles an entry E if and only if:
  - R and E have the same server and principal;
  - for stdio, they are on the same epoch;
  - R's gate tier is `read`;
  - R's `dispatched_at` is later than E's `unresolved_at`;
  - R completed with `!isError` and no transport error.

  Each qualifying E becomes `reconciled`. Entries that became unresolved after R was dispatched are not touched.
  - Q3: whether any such read is enough. Recommended: yes.
- **FR-019 (cancel operation)**: `cancel_pending_call(call_id)` MUST:
  1. Authorize the caller per FR-021. A failure returns the non-disclosing `not_found`.
  2. If the entry is `unresolved`, on stdio, and has a JSON-RPC id, send `notifications/cancelled` through **the transport instance captured at dispatch**.
     - The record holds a reference to that decorator instance. There is no lookup by epoch, so a reconnect cannot redirect an old id to a new session.
     - The send is bounded to 5s, and it never holds `epochMu` or the registry mutex.
  3. Mark the entry `cancelled`, with `resolution_actor`, whatever the result of the send.
  4. Return `{call_id, state:"cancelled", cancel_sent, cancel_error?, note}`. `note` says that the upstream may ignore the cancellation.

  For HTTP/SSE, a cancel is sent only if the captured transport instance is still open and the session supports notifications. Otherwise `cancel_error` is `transport_closed` or `unsupported_transport`.

  Cancelling an entry that is already terminal is an idempotent no-op that returns its current state.

**Security**

- **FR-020 (principal)**: The principal is a `Principal{Kind, ID}`:
  - **Agent token** (any `Kind`, including client credentials):
    - `Kind=agent_token`, and `ID = "tok_" + hex(sha256(Name ‖ 0x00 ‖ CreatedAt.UTC().Format(RFC3339Nano)))[:16]`;
    - `Name` is the store key. `CreatedAt` is set once at creation, and `RegenerateAgentToken*` keeps it;
    - so a regenerated token keeps its principal, a token deleted and then recreated with the same name gets a new principal, and tokens owned by the same user stay separate principals;
    - the id is derived from neither the secret, the hash nor the prefix;
    - propagation: `AgentToken.AuthContext()` MUST stamp a new, non-secret `AuthContext.TokenPrincipalID`, computed once at authentication. The registry never reads token storage.
  - **Server-edition session user**: not a dispatch principal, because Spec 107 gives tenant sessions no dispatch door. A session user therefore owns no entries.
  - **Everything else** (API key, socket, stdio transport, anonymous `/mcp` in the personal edition, internal callers): `Kind=local`, `ID=local`.
  - Profiles do not split principals.
- **FR-021 (authorization)**: Every request is checked against the caller's *current* effective scope.
  - **Visibility** of an entry (in a list, a count, an SSE event or a refusal): the caller is an admin, or the caller owns the entry **and** can currently see the server. "Can see" means `CanAccessServer` intersected with the profile scope (the Spec 105 intersection).
  - **Cancel** requires all of:
    - visibility;
    - the exact permission that matches the entry's gate tier (`HasPermission("write")` for a `write` entry, `HasPermission("destructive")` for a `destructive` entry). Permissions are exact-match;
    - `read_only_mode` is not set.
  - Every failure returns the same `not_found` body as an unknown id.
  - A refusal names only the caller's own visible entries.
  - When an admin cancels another principal's entry, the admin is recorded as `resolution_actor`.
- **FR-022 (no new secrets)**: Call ids are not credentials and grant nothing on their own. Every operation re-checks FR-021. Pending-call fields MUST NOT include tool arguments or results. Principal ids are never returned to non-admin callers.
- **FR-022a (agent operation policy)**: `pending_calls` (a read operation) and `cancel_pending_call` are deliberately **not** added to `agentDeniedServerOps` (`auth/server_ops.go:59`). Agent tokens may therefore use them, subject to FR-021. Named constants `ServerOpPendingCalls` and `ServerOpCancelPendingCall` are added, and a test pins this decision.

**Surfaces and observability**

- **FR-023 (parity)**: List and cancel MUST be available with identical fields on all three surfaces:
  - MCP: `upstream_servers` operations `pending_calls` (optional `name`) and `cancel_pending_call` (new property `call_id`);
  - REST: `GET /api/v1/servers/{name}/pending-calls`, `GET /api/v1/pending-calls` and `POST /api/v1/pending-calls/{call_id}/cancel`;
  - CLI: `mcpproxy upstream pending list|cancel`.

  Two further decisions:
  - **Server edition**: these routes are **not** added to the Spec 107 tenant allowlist (`tenant_allowlist_walk_test.go`). Tenant sessions get the fixed 403, and the walk test asserts this. Letting a tenant manage the calls of its own tokens is deferred to a `/user/*` follow-up.
  - The per-server gate override is **not** exposed through the `upstream_servers` patch. It is set through the config file, REST PATCH and the Web UI's raw JSON only. This keeps the tool-surface delta to what FR-033 lists.
- **FR-024 (activity)**:
  - `storage.ActivityRecord` and `contracts.ActivityRecord` gain `call_id`, `pending_state`, `pending_reason`, `late_outcome` and `resolved_at`, all `omitempty`.
  - `ActivityFilter` gains `pending_state`.
  - For scoped callers, these fields are rendered only on rows that the caller already sees **and** whose attribution matches the caller. This is the same rule that `renderActivityAttributionForCaller` uses. Otherwise the fields are stripped.
- **FR-024a (activity linkage)**:
  - The tool-call completed event carries `call_id`. The activity service persists it, and writes a `call_id → activity id` index entry in the same bbolt transaction.
  - A resolution emits a bus event, `pending_call.resolved`. The activity service updates the record **by call_id**.
  - If the record has not been persisted yet (the save is asynchronous), the update is held in a bounded map (at most 1024 items, for 60s) and applied when the record is inserted (upsert semantics).
  - Updates for the same call id apply in state order. A terminal state wins and never regresses.
  - A test covers a response that arrives immediately after the timeout.
- **FR-024b (SSE)**: Three event types: `pending_call.unresolved`, `pending_call.resolved` and `pending_call.refused`.
  - Payload: `server`, `call_id`, `state`, `reason`, timestamps, and an internal `_principal` attribute.
  - All three types are added to `identityBearingEventTypes`, so a payload without `server` fails closed.
  - A new principal-aware filter delivers these events to a scoped subscriber only if FR-021 visibility holds for that subscriber. `_principal` is stripped before any frame is sent. Admin subscribers receive every event.
  - Tests use two tokens, on shared and on disjoint servers, for all three types.
- **FR-025 (logging)**:
  - `logCallInterrupted` adds `call_id`, `pending_state` and `pending_reason`.
  - Each resolution and each refusal logs once at Info, with `server`, `call_id`, `connection_generation`, `principal_kind` (never the principal id) and `resolution`.
- **FR-026 (113-d interplay)**:
  - A timed-out call is counted once, at its original outcome.
  - A late completion records no second health outcome.
  - A refusal is excluded from both the numerator and the denominator (113-d FR-061 at `d5de9320d`).
- **FR-027 (server status)**: Server payloads gain `pending_calls`, the count of unresolved entries visible to the caller (FR-021). The `health` field is unchanged.

**Configuration**

- **FR-028 (config)**:
  - New global fields:
    - `pending_call_gate` (FR-017);
    - `pending_call_ttl` (Duration, default `10m`, minimum `call_tool_timeout`);
    - `pending_call_history` (default `15m`);
    - `max_pending_calls_per_principal` (default 16, range 1–256);
    - `max_detached_waits_per_server` (default 64, range 0–1024; 0 disables detached waits).
  - New per-server field: `pending_call_gate`.
  - All fields are `omitempty`.
  - They are wired per the config-field checklist:
    - the config file;
    - BBolt `UpstreamRecord` (the per-server field);
    - `CopyServerConfig` and `MergeServerConfig`;
    - REST PATCH;
    - `DetectConfigChanges`;
    - `ValidateDetailed`: the enum, TTL ≥ `call_tool_timeout`, the ranges, and the `enforce` restriction from FR-017;
    - `make swagger`;
    - `docs/configuration.md`.
  - A change MUST NOT reconnect an upstream, and MUST hot-reload.
  - Tests change each field through the file watcher and through REST PATCH. They assert that the epoch does not move and that the next call uses the new value. Invalid values are refused, and the refusal names the field.

**Audit, tool surface, rollout**

- **FR-030 (Spec 107 audit compatibility)**:
  - A refusal at the **advisory** check comes before the `authz allow` line, so it is a pre-dispatch gate. It is recorded as `authz decision:deny reason:pending_call_unresolved`.
    - This adds one value to the `authz` reason vocabulary. That is a widening, which Spec 107 FR-013 treats as a minor change, so `schema_version` stays 1.
    - `auditReasonFromBlockKey` gains the matching arm, with `disclosed:true`.
  - A refusal at the **authoritative** check comes after the `authz allow` line was written. It is recorded like a limiter shed: `tool_call outcome:rejected reason:pending_call_unresolved`. This adds one value to the set of `rejected` reasons on `tool_call` lines.
  - The count invariants do not change: `#authz == #pre-dispatch decisions` and `#tool_call == #authz(allow)`. Tests assert both at each refusal point.
  - A timed-out call's `tool_call` line is written once, at the timeout, with `error_class: upstream_timeout` (through 113-c FR-047). A late completion writes no audit line.
  - `cancel_pending_call` and `pending_calls` are built-in invocations, which Spec 107 FR-012 excludes from v1 audit. They produce **no** audit line. The actor is recorded on the activity record and in the Info log.
  - Q6: whether to add an audit event for cancellations.
- **FR-033 (tool-surface delta)**: The only intended change to the frozen tool-surface goldens is in `upstream_servers`:
  - two new `operation` enum values, `pending_calls` and `cancel_pending_call`;
  - one new string property, `call_id`;
  - the matching description text.

  The goldens are regenerated for exactly that diff, and the PR shows the diff. No other built-in tool changes. Spec 107 FR-044 ("no argument added") applied to Spec 107's own change. This spec makes a deliberate, documented exception to it.
- **FR-034 (phase-gated capabilities)**: The `resolve` actions and the refusal text are computed from a capability set compiled into the build (`late_completion`, `cancel`), never from the documentation. Config validation refuses `enforce` mode until both capabilities ship (plan phase 3). No release can therefore block a caller whose only ways out are the TTL or a read.

### Key Entities

- **DispatchRecord / PendingCall**: one dispatched call. Its fields are in FR-007 and its lifecycle in FR-008.
- **PendingCallRegistry**: per-process and per-server, owned by `upstream.Manager`. Its transitions are serialized per server (FR-009).
- **Principal**: see FR-020.
- **Gate decision**: `allow | warn | refuse`, plus the blocking call ids. It is never persisted beyond the activity record.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of dispatched calls that end by `call_tool_timeout` or by the end of the caller's ctx carry a `call_id` and a `pending_state` on the activity record and on any delivered result. `pending_state` is `unresolved`, or `completed_late` per FR-010a. The rate is 0% for calls refused before dispatch.
- **SC-002**: In `enforce` mode, a write or destructive retry by the same principal during the unresolved window reaches the upstream 0 times on each of the five entry points. This includes the queued-write interleaving (US2 AS7). Reads reach the upstream every time.
- **SC-003**: Each resolution path opens the gate within 100 ms of its trigger in the test harness. Late completion is detected for 100% of stdio responses that arrive before `expires_at`, for calls within the detached share.
- **SC-004**: With two agent tokens there are 0 of each of these across principals: list entries, cancels, SSE `pending_call.*` frames, pending fields on activity rows, and evictions. The `not_found` bodies for "another principal's id", "narrowed scope" and "unknown id" are byte-identical.
- **SC-005**: In every mode, the legacy error string at each surface is byte-identical to the base-commit golden (FR-003a), and success results are unchanged. These tests pass unchanged:
  - `calltool_cancel_test.go`;
  - the Spec 105 epoch tests;
  - the toolsurface goldens, apart from exactly the FR-033 diff.
- **SC-006**: 10,000 timed-out calls from one principal against one server leave at most:
  - `max_pending_calls_per_principal` gating entries;
  - 256 non-gating and terminal records;
  - `max_detached_waits_per_server` detached waits.

  A second principal's gating entry survives. `go test -race` is clean, and no goroutine outlives its fixed inner deadline by more than 1s (goleak).
- **SC-007**: A real-instance check passes. The setup is an isolated data dir, a non-default port, and a stdio fixture tool that sleeps past the timeout and then succeeds. The expected sequence is:
  1. The timeout result carries the typed block.
  2. An immediate write is refused with a self-healing message.
  3. The late response shows as `completed_late` in `mcpproxy activity show`.
  4. The next write is dispatched.

  The check is repeated with `cancel_pending_call`, and with a reconcile read.
- **SC-008**: For the same registry state, the MCP, REST and CLI list outputs are equal field for field.

## Assumptions

- **A1**: Agents read `isError` text, so the self-healing text helps even clients that ignore `pending_call`.
- **A2**: The first concrete user is a stdio upstream with human-in-the-loop latency. HTTP upstreams benefit from the typed outcome and the gate, but get no late completion.
- **A3**: A personal-edition instance has one human, so callers that do not use an agent token share the `local` principal.
- **A4**: 113-c lands before or together with the gate phase. If it has not landed, the refusal is recorded like the Spec 093 shed, without `error_class` stamping. The Spec 113 citations are pinned to `d5de9320d` and are re-checked when it merges.
- **A5**: mcp-go's `Stdio.SendRequest` keeps its current cancellation semantics (Context item 2). A test pins them, so an mcp-go upgrade that changes them fails loudly.

## Known Limitations

1. **Cancellation is advisory.** An upstream may ignore `notifications/cancelled`. `cancelled` means "the agent accepted the ambiguity".
2. **Reconciliation is the agent's judgment** (Q3).
3. **No late completion for HTTP/SSE.**
4. **No MCP Tasks.** Tasks are the durable, protocol-level answer, and belong in a separate spec.
5. **No `waiting_human`**, unless Q1 is answered otherwise.
6. **In-memory only.** A restart forgets all entries. Stdio children die with mcpproxy, but remote HTTP/SSE work may still land after a restart, unobserved.
7. **`connection_reset` opens the gate without proof.** Killing a stdio child does not undo side effects it already handed off (FR-009, Q5).
8. **No forwarding of caller cancellation** in v1 (FR-012).
9. **Unannotated tools gate conservatively** (FR-015). In `enforce` mode, an unannotated tool that only reads is refused after a timed-out write. Adding `readOnlyHint:true` (upstream or through annotation overrides) fixes this.

## Clarifications Needed (summary)

| # | Question | Recommended default |
|---|---|---|
| Q1 | Emit a `waiting_human` reason? (FR-004) | No. Reserve the value, and revisit if an elicitation-in-flight signal is accepted |
| Q2 | Default `pending_call_gate` mode (FR-017) | `warn` this release. `enforce` is available from phase 3. Flip stdio to `enforce` one minor release later |
| Q3 | Does any successful, explicitly read-only call reconcile? (FR-018) | Yes, as #1321 proposes |
| Q4 | Forward caller cancellation upstream? (FR-012) | Not in v1. Revisit as an opt-in mode once mcp-go exposes an attributable cancel cause (for example `WithCancelCause` in the request handler) |
| Q5 | Should a stdio `connection_reset` open the gate? (FR-009) | Yes, with the ambiguity stated in the text. Alternative: keep gating until the TTL |
| Q6 | Add an audit event for `cancel_pending_call`? (FR-030) | No in v1, because Spec 107 excludes built-in invocations. Record it in activity and the log only |

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
feat(upstream): gate mutating retries at the managed seam

Related #1321

Spec 114 phase 3: authoritative gate at managed.Client.callTool,
self-healing refusal, warn/enforce modes.

## Testing
- go test -race ./internal/upstream/pending/... ./internal/upstream/...
- stdio fixture: timeout -> refused write -> late completion -> write passes
```

## Revision notes (review round 1)

Every finding was checked against the code at `44244b858` and against mcp-go v1.0.0. All of them were accepted. Where each one is addressed:

| Finding | Addressed in |
|---|---|
| Gate placement and queued writes | FR-013 (authoritative check at the managed seam, serialized with publication; covers replay), US2 AS6–7 |
| Tier rule | Glossary, FR-015, US2 AS2–3 and AS8, Known Limitation 9 |
| SSE and activity leakage | FR-024, FR-024b, SC-004, US4 AS3 |
| Principal identity | FR-020, US4 AS9 |
| Current-scope authorization and exact-match permissions | FR-021, US4 AS6–8 |
| Registry ordering and tombstones | FR-008, FR-009, FR-010a |
| Detached-wait feasibility, inner deadline, cost, dispatch-time cap | FR-010, FR-007a |
| Typed JSON-RPC id | FR-002 |
| Caller cancellation | FR-004, FR-012, Context item 7, Q4 |
| Cancel TOCTOU | FR-019 |
| Backward compatibility | FR-003a, SC-005 |
| Per-surface envelopes | FR-003a |
| Epoch semantics for HTTP/SSE, quarantine and restart | FR-009, Edge Cases, US3 AS6–7, Known Limitations 6–7, Q5 |
| Memory bounds and cross-principal eviction | FR-007a, US4 AS4, SC-006 |
| Reconciliation ordering and seam | FR-018, US3 AS1–2 |
| Spec 107 audit | FR-030, Q6 |
| Phase ordering | FR-034, plan Phases |
| Server-edition routes | FR-020, FR-023 |
| Intent validator scenario | US2 AS8 |
| Toolsurface delta | FR-033 |
| Evidence pinning and transport interfaces | Evidence base, Context item 6, FR-002, FR-006 |
| Contract gaps | FR-003 (`server` and `tool` required), FR-004 (the progress window is a constant), FR-010 (`jsonrpc_error`, `malformed`), FR-024a, FR-028 tests |
| Admin-cancel audit, unavailable cancel surface, agent policy | FR-016, FR-022a, FR-030 |

Two findings were narrowed rather than adopted as proposed:
- The suggested configurable forwarding mode (FR-012) is deferred to Q4, because mcp-go v1.0.0 gives no attributable cancel signal.
- The requirement that SSE events carry ownership is met by an internal `_principal` attribute that is stripped before sending. Ownership is not exposed in the payload.
