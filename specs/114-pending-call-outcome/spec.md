# Feature Specification: Typed Pending-Call Outcome and Call Correlation

**Feature Branch**: `114-pending-call-outcome`
**Created**: 2026-10-06
**Revised**: 2026-10-06 (review round 1, 29 findings; review round 2, 20 findings; see "Revision notes" at the end)
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
- mcp-go was read from the module cache at `github.com/mark3labs/mcp-go@v1.0.0`, the version pinned at `go.mod:21`. It is not vendored in the repo, so a reviewer working from the repo alone cannot check these citations. **Every mcp-go behavior cited below (Context items 2, 6, 6a and 7) is a dependency assumption**, not a repo fact. Each one gets a pin test in phase 1 (Assumption A5), so an mcp-go bump that changes it fails CI before any code relies on it.
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
- *Principal*: the caller identity that owns an entry and governs visibility and cancel (FR-020).
- *Gate scope*: the key the gate and reconciliation use. It equals the principal, except that `local` is split per MCP session (FR-020a).
- *Admission tier*: the tier the caller was admitted at (`tierForAnnotations` for scoped callers). Cancel is authorized against it (FR-021).

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
   - HTTP, streamable HTTP and SSE clients are built separately (`internal/transport/http.go:390` and the SSE constructor below it). The decorator is **not** installed there in v1 (FR-002, FR-019).
6a. **One logical call can be several `tools/call` requests [dependency assumption, pinned].**
   - mcp-go v1.0.0 `client.CallTool` runs `multiRoundTrip` (`client/client.go:631-643`, `client/mrtr.go:56-103`). Each round is a new `tools/call` with a new JSON-RPC id.
   - A round ends with a result for which `CallToolResult.NeedsInput()` is true. With a non-empty request map, mcp-go calls `fulfillInputRequests` (elicitation, sampling, roots). With an empty map (load shedding), it simply retries, up to `maxLoadSheddingRoundTrips`.
   - mcpproxy registers **no** elicitation, sampling or roots handler on its upstream clients (no such option is set in `internal/upstream/core`). So `fulfillInputRequest` fails at once ("no elicitation handler configured"), and the logical call ends with an error. A deadline therefore cannot fire *while mcpproxy waits on a human* inside `fulfillInputRequests`. The only multi-round case that runs today is a load-shedding retry.
   - Consequences: one `call_id` can map to several JSON-RPC ids (FR-002); a late response can be an input-required intermediate result (FR-010, `late_outcome: input_required`); Q1 is unchanged, because no attributable human-wait signal exists in mcpproxy.
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
    - a rule that the classifier does not read message text **except a documented last-resort fallback list covered by tests** (FR-049). This spec does not rely on that fallback: a refusal is recognized by `errors.Is` (FR-006), and the legacy timeout string is unchanged, so whatever the fallback matches today it still matches.
11. **Auth identity, audit order and surface plumbing.**
    - `auth.AgentToken` has no stable id field (`auth/agent_token.go:95-148`). It has `Name` (the store key), `TokenPrefix` (which changes on regenerate), `UserID`, `ClientID` (client tokens only) and `CreatedAt`.
    - `AuthContext` carries `AgentName`, `TokenPrefix`, `UserID` and `ClientID` (`auth/context.go:34-74`).
    - Permissions are exact-match strings (`HasPermission`, `context.go:150-163`). `read` does not imply `write`, and `destructive` does not imply `write`.
    - Token names are unique **per owner** (`UserID`), not globally (`storage/agent_tokens.go:186-193`). Two tenants can each hold a token called `ci`.
    - An unauthenticated `/mcp` request gets `AnonymousContext()`, which keeps `Type = AuthTypeAdmin` on purpose (`auth/context.go:173-185`), so a raw `IsAdmin()` is true for it. When `anonymous_profile` or a binding guard confines that caller, handlers narrow it with `auth.ScopedView(ac, confinedAnonymous)` (`auth/scoped_view.go:3-21`), a non-admin, agent-shaped view. The predicate is `(ac == nil || ac.Anonymous) && resolution.anonymousConfinementActive()` (`server/mcp.go:2872-2873`, `server/profile_resolver_v3.go:39`).
    - Scoped token callers are admitted against `tierForAnnotations` (`server/mcp.go:3022-3027`, `mcp_code_execution.go:1744-1751`), which maps an unannotated tool to `read`. So a read-only token may call an unannotated tool through `call_tool_read`. This **admission tier** differs from the gate tier (FR-015), and FR-021 authorizes cancel against it.
    - `auditAuthz` is first-write-wins (`server/audit_funnel.go:327-338`), and the `authz allow` line is written by `emitActivityToolCallStarted` (`server/mcp.go:851-854`) **before** `dispatchOnEpoch` reaches `Manager.callTool`. Every check inside the Manager or the managed client therefore runs after `authz allow` (FR-030).
    - On REST, `CallToolDirect` flattens an `isError` result to `fmt.Errorf("%s", text)` (`server/mcp.go:7243-7264`), and httpapi wraps it as `Failed to call tool: …` (`httpapi/server.go:6147-6152`). Typed refusals reach HTTP only through a capture box installed in ctx (`withShedCapture`, `withCodeExecCapture`, `withProfileToolCapture`). The REST legacy string therefore *contains* the `call_tool_*` JSON body.
    - `code_execution` host functions return an envelope `{ok:false, error:{code, message}}` to scripts (`internal/jsruntime/runtime.go:472-481`, `errorEnvelope`); they do not throw.
    - Activity replay writes a `storage.ToolCallRecord` directly and emits no activity event (`runtime/runtime.go:1574-1607`). It folds every non-shed error into `record.Error`. The Server's replay wrapper picks the audit line from a switch on the returned error (`server/server.go:4164-4194`), and httpapi maps typed replay errors to status codes (`httpapi/server.go:5481-5508`).
    - The runtime event bus drops an event when a subscriber's channel is full (`runtime/event_bus.go:205-218`, `select … default`). The activity service consumes that bus (`runtime/activity_service.go:292-333`). Delivery is therefore lossy under load.
    - The server-edition tenant allowlist admits **every** GET under `/api/v1/servers/{id}/**` except the named `tool-calls` sub-path (`httpapi/session_principal.go:144-160`).
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
| The Spec 107 audit funnel and its `authz` / `tool_call` phases | Extended by one `rejected` reason on `tool_call` lines only; the `authz` vocabulary is unchanged (FR-030) |
| The REST capture boxes (`withShedCapture`, `withCodeExecCapture`, `withProfileToolCapture`) | One more box, `withPendingCallCapture`, for the timeout record and the refusal (FR-003b) |
| `auth.ScopedView` and the confined-anonymous predicate | The only admin test used by FR-021 |
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
- Correlation, late completion and cancel **for HTTP, streamable HTTP and SSE upstreams** (FR-002, FR-019). These transports get the typed outcome, the gate, listing, reconcile and TTL only.
- Back-writing later resolutions into replay records (FR-024c).

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
4. **Given** the activity record and the output of `mcpproxy activity show <id> -o json`, **Then** both carry `call_id`, `pending_state` and `pending_reason`, and `error_class` is `timeout` once 113-c is merged. For replay, which writes no activity record, the replay `ToolCallRecord` carries the same three fields (FR-024c).
5. **Given** each of the five entry points (call_tool_*, the direct surface, code_execution, REST and replay), **Then** each produces its FR-003a envelope. There is one contract test per surface.
6. **Given** a REST `POST /api/v1/tools/call` timeout, **Then** the HTTP `error` string is byte-identical to the base-commit golden (it is built from the legacy body, FR-003b), and `pending_call` is a sibling key of the envelope.

---

### User Story 2: A blind mutating retry is refused with a self-healing error (Priority: P1)

While the first call is unresolved, mcpproxy refuses the same principal's mutating calls to that server before they reach the upstream.

**Independent Test**: with the gate in `enforce` mode, time out a write call. Then issue a second write and a read.

**Acceptance Scenarios**:

1. **Given** an unresolved write call `pc_A` on `srv` for principal P, in mode `enforce`, **When** P calls any tool on `srv` whose gate tier is `write` or `destructive`, **Then** the call does not reach the upstream (the fixture's receipt count does not change), and:
   - the result is `isError:true`. The text names `pc_A`, its tool, its age and `expires_at`, and lists **only the resolution actions that P can take on this build and configuration** (FR-016);
   - the activity status is `rejected`, with a `blocked` policy decision and reason `pending_call_unresolved`;
   - the audit lines are `authz allow` plus `tool_call outcome:rejected reason:pending_call_unresolved`, at both refusal points (FR-030);
   - the 113-c classification is `proxy_policy/proxy`.
2. **Given** the same state, **When** P calls a tool on `srv` that has `readOnlyHint: true`, **Then** the call passes the gate.
3. **Given** the same state, **When** P calls an **unannotated** tool on `srv`, **Then** the call is refused, because its gate tier is `write`. **When** an unannotated call itself times out, **Then** it registers a gating entry.
4. **Given** the same state, **When** P calls a write tool on a different server, or another principal Q calls a write tool on `srv`, **Then** neither call is affected.
4a. **Given** two MCP sessions S1 and S2 that both authenticate as `local` (no agent token), and `pc_A` was created by S1, **When** S2 calls a write tool on `srv`, **Then** the gate does not refuse it, because the gate scope of a `local` call is its MCP session (FR-020). S2 can still list and cancel `pc_A`, because visibility is per principal.
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

1. **Reconcile**: **Given** `pc_A` is unresolved and its late completion is **not** observable (HTTP/SSE, or its detached wait was abandoned), **When** P, in the same gate scope, makes a successful call to a tool on `srv` with gate tier `read`, dispatched after `pc_A.unresolved_at` (and, for stdio, on `pc_A`'s epoch), **Then** `pc_A` becomes `reconciled`, subject to Q3. This also holds when the read goes through `code_execution` or replay.
1a. **No implicit reconcile while the call is observably running (#1317)**: **Given** `pc_A` (`chrome-devtools:navigate_page`) is unresolved with a live detached wait, **When** P calls `take_snapshot` (`readOnlyHint:true`) and it succeeds, **Then** `pc_A` stays unresolved and keeps gating. **When** P makes the same read with `_meta["io.mcpproxy/reconciles"]: ["pc_A"]`, **Then** `pc_A` becomes `reconciled` (FR-018).
2. **Reconcile ordering**: **Given** a read R dispatched at t0, and a write `pc_B` that becomes unresolved at t1 > t0, **When** R completes successfully at t2 > t1, **Then** `pc_B` stays unresolved.
3. **Cancel**: **Given** `pc_A` is unresolved, **When** P calls `upstream_servers {operation: cancel_pending_call, call_id: pc_A}`, or the REST or CLI equivalent, **Then**:
   - for stdio, if the transport instance that sent `pc_A` is still open, mcpproxy sends `notifications/cancelled {requestId, reason}` through **that instance**. `requestId` is the JSON-RPC id of `pc_A`'s **latest** round (FR-002), with its original JSON type;
   - for HTTP, streamable HTTP and SSE, nothing is sent; the response has `cancel_sent:false, cancel_error:"unsupported_transport"` (FR-019);
   - `pc_A` is marked `cancelled`, whether or not the notification was sent;
   - the response carries `cancel_sent`. When it is false, the response also carries `cancel_error` (`transport_closed | unsupported_transport | send_failed | id_unknown`).
4. **Late completion (stdio)**: **Given** `pc_A` is unresolved and the upstream responds before `expires_at`, **Then**:
   - `pc_A` becomes `completed_late`, with a `late_outcome` (FR-010). An input-required intermediate result yields `late_outcome: input_required`;
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
   - T1's gating entry is never evicted, and never folded by T2's insertions (FR-007a);
   - T1 keeps its fair share of post-deadline detached waits (FR-007a).
4a. **Given** `max_detached_waits_per_server: 6` and three tokens T1, T2, T3, **When** T1 and T2 each hold 3 post-deadline detached waits on `srv` and a call by T3 then times out, **Then** T3's entry gets a detached wait (late completion observable) by preempting the newest wait of the principal most over its fair share of 2. The preempted entry keeps gating, with `late_completion_observable:false`.
5. **Given** an admin caller, **Then** it can list and cancel every entry. "Admin" means `IsAdmin()` on the **`auth.ScopedView`** of the caller (FR-021), never a raw `IsAdmin()`. The activity record and the log name the admin as the actor. For the audit line, see FR-030.
5a. **Given** `anonymous_profile: "p"` confines unauthenticated `/mcp` callers, and token T1 owns `pc_A` on a server inside `p`, **When** a confined anonymous caller lists or cancels, **Then** it sees and cancels only its own entries on servers visible under `p`. `pc_A` is `not_found` for it. **Given** no `anonymous_profile`, **Then** the unconfined anonymous caller keeps today's admin-shaped behavior (FR-021).
6. **Given** a token with only the `read` permission that called an **unannotated** tool through `call_tool_read` (admission tier `read`, gate tier `write`) and timed out, **Then** it can list **and cancel** that entry, because cancel is authorized against the admission tier (FR-021). It cannot cancel an entry whose admission tier it no longer holds; that returns the `not_found`-shaped refusal.
7. **Given** a token with the `destructive` permission but not `write`, **Then** it can cancel its own entries admitted at `destructive`, but not entries admitted at `write`.
8. **Given** T1's token is narrowed after `pc_A` was created (the server is removed from `allowed_servers`, or the token's profile pin moves to a profile without `srv`), **Then** T1 can no longer list `pc_A`, cancel it, or receive events about it. Each attempt gets a `not_found` that does not reveal whether the entry exists.
9. **Given** T1's token is deleted and a new token with the same name is created, **Then** the new token is a different principal and sees none of the old entries. **Given** T1's token is regenerated instead, **Then** it keeps its principal and its entries. **Given** two owners U1 and U2 each hold a token named `ci` with the same `CreatedAt` (forced in the test), **Then** they are different principals (FR-020).

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
  - No JSON-RPC id is captured, and no cancel is sent in v1: `cancel_pending_call` returns `cancel_sent:false, cancel_error:"unsupported_transport"` and still marks the entry `cancelled` (FR-019).
- **Registry full**: when every stored record is dispatched or gating and nothing can be evicted or folded, a new call is refused **before dispatch** with a capacity refusal (FR-007a). It never reaches the upstream untracked.
- **Load-shedding rounds**: a call that mcp-go retries because the upstream shed load sends several `tools/call` requests. Every id is captured; cancel targets the latest (FR-002).
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
  - It is recorded on the activity record for the four entry points that write one, and on the replay `ToolCallRecord` (FR-024c).
- **FR-002 (JSON-RPC id capture)**: For stdio upstreams, a `transport.Interface` decorator installed at `client.NewClient` (`connection_stdio.go:244`) MUST record `call_id → JSON-RPC request ids`. It does this in `SendRequest(ctx, req)` when `req.Method == "tools/call"` and ctx carries `CallMeta`. HTTP, streamable HTTP and SSE clients get no decorator in v1 (Context item 6).
  - **Multi-round calls** (Context item 6a): every round of one logical call carries the same ctx, so the decorator appends each round's id to the record's ordered id list. Only the latest round can be outstanding, because mcp-go sends round N+1 only after round N returned. Cancel targets the latest id (FR-019). Late completion applies to the latest round (FR-010). A test drives a fixture that sheds load twice and then times out, and asserts three ids and a cancel on the third.
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
  - **`code_execution`**: host functions return an envelope to the script; they do not throw (`internal/jsruntime/runtime.go:472-481`). The envelope `{ok:false, error:{code, message}}` gains **additive** keys only:
    - on a timeout: `error.pendingCall` (the FR-003 object). `code` and `message` are unchanged;
    - on a refusal: `error.code` is `"PENDING_CALL_UNRESOLVED"` (a new `jsruntime.ErrorCode`), `error.message` is the FR-016 text, and `error.pendingCalls` lists the blocking entries;
    - the final `code_execution` result lists every `call_id` that became unresolved during the run, under `pending_calls`.
    - `internal/jsruntime` is in scope: it gains the error code and an `errorEnvelope` variant that takes extra fields. Scripts that read only `ok`, `error.code` and `error.message` see no change for a timeout.
  - **REST `/tools/call`**: see FR-003b. A timeout keeps status 500; a refusal is a 409 (FR-016).
  - **Replay**: see FR-024c.
  - A contract test per surface pins the envelope, and a golden pins the legacy string.
- **FR-003b (REST capture and the REST legacy string)**: The REST legacy string is `Failed to call tool: tool call failed: <call_tool_* JSON body>` (Context item 11), so it embeds the body that FR-003a extends. Therefore:
  - `CallToolDirect` installs `withPendingCallCapture(ctx)` beside the existing shed, code_execution and profile capture boxes. The `call_tool_*` handler deposits into it either the timeout record together with the **legacy body** (the body exactly as the base commit would have built it: no `pending_call`, the old `troubleshooting` value), or the `*pending.RefusedError`.
  - When the box holds a timeout, `CallToolDirect` returns a typed `*pendingCallDispatchError` whose `Error()` is built from the legacy body. httpapi answers 500 with the byte-identical legacy `error` string and adds `pending_call` as a sibling key of the envelope.
  - When the box holds a refusal, httpapi answers 409 with the FR-016 body.
  - The capture is taken before the generic `fmt.Errorf("%s", text)` flattening, in the same order as the existing boxes.
  - SC-005's REST golden is the HTTP `error` string, captured from the base commit, and it stays byte-identical.
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
  - Record fields: `call_id`, `server`, `transport`, `epoch`, `principal`, `gate_scope` (FR-020), `tool`, `gate_tier`, `admission_tier` (FR-021), `json_rpc_ids` (typed, ordered, optional), `late_completion_observable`, `dispatched_at`, `unresolved_at`, `expires_at`, `reason`, `state`, `last_progress_at`, `late_outcome`, `resolved_at` and `resolution_actor` (the principal kind only).
- **FR-007a (bounds)**: Every bound below is a hard bound. Records are created **only** by `CheckAndBegin` (FR-013); every later transition (`dispatched → unresolved`, …) rewrites a record in place and never adds one. So all capacity decisions happen at `CheckAndBegin`, before dispatch.
  - **Record kinds**, all counted against the global cap: `dispatched` records (one per in-flight call), unresolved gating entries, unresolved non-gating (read-tier) entries, and terminal tombstones.
  - **Overflow blockers**: a per-`(server, gate_scope)` aggregate `{count, oldest_unresolved_at, max_expires_at, max_gate_tier, id}` with its own id (`pcb_` prefix). It is not a record and does not count against the record caps. There is at most one per `(server, gate_scope)`, so blockers are bounded by servers × gate scopes. A blocker **gates exactly like an unresolved gating entry** until `max_expires_at`, and resolves by reconcile, cancel (no notification is sent; `cancel_error: id_unknown`), `connection_reset` (stdio) or TTL. Late completion of a folded call does not resolve the blocker, which is conservative. A folded `call_id` looked up by list or cancel resolves to its blocker while the blocker lives.
  - **Gating entries per `(server, gate_scope)`**: at most `max_pending_calls_per_principal` (default 16). Past the cap, the scope's own oldest gating entry is **folded** into the scope's blocker, with a Warn. Folding never opens a gate. **Insertions by another principal never evict or fold a gating entry**, except under the global rule below, which folds and never opens.
  - **Non-gating entries** (read tier) and **terminal tombstones**: at most 256 per server, combined.
    - They are evicted oldest first, terminal records before read entries.
    - Terminal records are also dropped after `pending_call_history` (default 15m), once FR-024a's resync has acknowledged them.
  - **Global**: at most 4096 records. When `CheckAndBegin` needs a slot and the cap is reached, it tries in order:
    1. evict the oldest terminal tombstone, then the oldest non-gating entry (any server);
    2. fold the oldest gating entry of the **inserting gate scope** (any server) into its blocker;
    3. fold the oldest gating entry of the gate scope that holds the most gating entries (ties: oldest entry) into that scope's blocker. This changes only the granularity of that scope's blocking, never whether it is blocked;
    4. otherwise every slot is a `dispatched` record, which means 4096 calls are in flight. The call is **refused before dispatch** with `*pending.CapacityError` (reason `pending_registry_full`, Retry-After 1s). It is handled like a Spec 093 shed on every surface (REST 429, activity `rejected`, audit `tool_call outcome:rejected reason:pending_registry_full`). It never reaches the upstream untracked, so SC-001 holds.
  - **Detached waits** (FR-010): every stdio call is detached at dispatch (its in-flight cost is bounded by the in-flight `dispatched` records). The cap `max_detached_waits_per_server` (default 64) applies to **post-deadline waits**, i.e. detached waits whose caller has already returned. It is checked at `MarkUnresolved`, before the timeout result is built, so `late_completion_observable` is reported correctly:
    - **Fair share**: `share = max(1, floor(cap / N))`, where N is the number of distinct principals holding a post-deadline wait on the server, plus the requester if it holds none.
    - If a slot is free, the requester keeps its wait.
    - If the cap is full and the requester holds fewer than `share`, the newest post-deadline wait of the principal most over its share (ties: newest wait) is **preempted**: its inner ctx is cancelled (nothing is sent upstream, Context item 2), and that entry's `late_completion_observable` becomes false. The requester keeps its wait.
    - Otherwise the requester's own wait is abandoned the same way, and its entry is reported with `late_completion_observable:false`.
    - A preempted or abandoned entry keeps gating and resolves by reconcile, cancel, `connection_reset` or TTL. A preemption re-emits SSE `pending_call.unresolved` for the preempted entry with the updated flag, and logs at Debug.
    - **Guarantee**: while N ≤ cap, every principal can hold at least `floor(cap / N)` post-deadline waits on a server, whatever the others do. "At most half" from round 1 is dropped: it was a per-principal ceiling, and two principals could still take every slot.
  - **Correlation state** (`call_id → json_rpc_ids` in the decorator): one item per `dispatched` or detached record, removed when its wait ends. It is bounded by the records above.
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
    - moves every non-terminal stdio record with `epoch < newEpoch` (and every stdio overflow blocker) to `connection_reset`;
    - raises the server's `epoch_floor` to `newEpoch`;
    - **returns** the list of transitions. It emits nothing itself.
  - **Emission rule (all registry methods)**: registry methods never publish SSE or bus events, write activity, log at Info or call any callback. They return a `[]Transition`. The caller emits those transitions **after** releasing `serverSet.mu` and, for `ResolveEpoch`, after releasing `epochMu` too. This keeps `epochMu`'s invariant (`managed/client.go:151-158`: never held across foreign callbacks). A test installs an emitter that asserts neither lock is held (via a lock-ownership probe) and drives every transition, including `ResolveEpoch` on both reconnect sites.
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
    - `malformed` (a result that cannot be parsed);
    - `input_required`: the result decodes as a `CallToolResult` for which `NeedsInput()` is true (Context item 6a). Nobody will run the next round, so the upstream is no longer executing this call, and the state is `completed_late`. The tool did **not** finish, and may have done partial work, so the resolution text and the activity record say "the upstream stopped and asked for input; the action did not complete; reconcile before retrying". The detection uses mcp-go's exported `NeedsInput()`, and a pin test covers it.
  - **Other endings**:
    - A transport close yields no late outcome. The record stays unresolved until FR-009 or the TTL resolves it.
    - The inner deadline yields `expired`, unless the TTL sweeper has already expired the record.
  - **Late content**: the late result content:
    - MUST pass through the existing sensitive-data detector. Its detections are recorded on the activity record, as for any response;
    - MUST NOT be stored beyond what today's policy stores for a successful response;
    - MUST NOT be returned to any client.
  - **Cost**: every stdio `tools/call` pays for one goroutine, one buffered channel and one select. That is at most tens of µs and about 2–4 KB of stack. The plan states this as a performance goal, and a benchmark measures it. After the caller's deadline, a wait over the FR-007a share is abandoned or preempted, which yields today's behavior (the late response is dropped).
  - HTTP and SSE transports do no detached waits.
- **FR-010a (publication handshake)**:
  - `Begin` creates the dispatch record, in state `dispatched`, at the authoritative gate, before the transport is called.
  - Every transition below returns its `[]Transition` for emission outside the locks (FR-009).
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
  - An **advisory** check also runs before admission, in `Manager.callTool` and in the `code_execution` dispatch, so that a refused call usually does not take a concurrency slot. The advisory check never admits a call on its own authority. Both checks run **after** the `authz allow` line has been written (Context item 11), so both refusals are audited the same way (FR-030).
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
  - the registry has at least one `unresolved` gating entry, or an overflow blocker, for the same server and **gate scope** (FR-020);
  - for stdio only, that entry is on the same epoch.
- **FR-015 (gate tier)**: `gateTier(annotations, found)` is defined as:
  - a tool that is not found or cannot be resolved → `destructive`;
  - otherwise `contracts.AnnotationTier(annotations)`, with `unannotated` mapped to `write`.

  So a tool counts as `read`, for both the gate and reconciliation, **only** if it has an explicit `readOnlyHint:true` and no `destructiveHint:true`.
  - This is a new, deliberately conservative rule that applies only to this feature. It does not change `tierForAnnotations`, call-variant routing, token permission checks or the Spec 108 profile policy.
  - The record also stores the **admission tier**: the tier the caller was admitted at, i.e. `tierForAnnotations` for scoped callers (Context item 11), or the variant's tier for unscoped callers. Gate tier ≥ admission tier always. The gate and reconciliation use the gate tier; cancel authorization uses the admission tier (FR-021).
  - Entry points that hold the annotations compute it. Replay and REST derive it from the stored tool annotations.
  - A truth-table test covers nil annotations, empty hints, each hint combination, and a tool that is not found.
- **FR-016 (refusal shape)**: The refusal is an `isError` tool result, never a JSON-RPC protocol error.
  - It is recorded like the Spec 093 shed: activity status `rejected`, a `blocked` policy decision with `telemetry.BlockReasonPendingCallUnresolved`, and the 113-c classification `proxy_policy/proxy`.
  - The text names the blocking `call_id`(s), their tool, their age and `expires_at`.
  - It names **only the available actions**, computed per caller:
    - *wait*: always available. It names `expires_at`;
    - *reconcile*: available only if, **for this caller and this server**, at least one tool has gate tier `read` **and** is callable by the caller (visible under its profile's `max_tier` and tool rules, and admitted by its token permissions). The text names up to three such tools. For a blocking entry whose late completion is still observable, the text says the read must carry `_meta["io.mcpproxy/reconciles"]` (FR-018). Otherwise the text omits `reconcile`;
    - *cancel*: available only if this build ships FR-019 **and** the caller can reach a cancel surface. That means all of these hold:
      - `upstream_servers` is registered (neither `disable_management` nor `read_only_mode` is set);
      - `upstream_servers` is visible under the caller's profile. It is annotated `destructiveHint:true`, so a `max_tier` below `destructive`, or a `management_tools` exclusion, hides it;
      - the caller holds the permission that FR-021 requires for the blocking entry (its admission tier).

      Otherwise the text omits `cancel`. Operators can still cancel through REST or the CLI, and the text says so when it is the only remaining action besides *wait*.
  - The availability computation is a pure function of (caller, server, build capabilities, config), and a table test covers: no read-only tool on the server; read-only tools hidden by `max_tier` or tool rules; `upstream_servers` hidden; and the wait-only outcome.
  - REST returns `409 Conflict` with the same JSON body, via the FR-003b capture box. Replay returns the typed error (FR-024c), and httpapi's replay handler maps it to 409 too.
  - The typed error passes through `Manager.callTool` unchanged, as `LimitError` does today.
- **FR-017 (mode)**: A global `pending_call_gate` setting (`off|warn|enforce`), with a per-server override in `ServerConfig`.
  - In `warn` mode, a call that the gate would refuse is dispatched anyway. It carries `pending_call_warning` in its `_meta` and its activity record, and one Warn log is emitted.
  - The registry, the typed outcome and listing work in every mode, including `off`.
  - Config validation accepts `enforce` only in a build that ships late completion and cancel (FR-034).
  - Q2: the default mode. Recommended: `warn`.
- **FR-018 (reconciliation)**: Reconciliation is per entry and runs at the managed seam, so reads through `code_execution`, replay and REST also reconcile. A completed call R reconciles an entry (or overflow blocker) E if and only if:
  - R and E have the same server and **gate scope** (FR-020);
  - for stdio, they are on the same epoch;
  - R's gate tier is `read`;
  - R's `dispatched_at` is later than E's `unresolved_at`;
  - R completed with `!isError` and no transport error;
  - **and** one of:
    - **implicit**: E's late completion is not observable (`late_completion_observable:false`: HTTP/SSE, or an abandoned, preempted or transport-closed wait) **and** E has received no progress within the FR-004 window before R's dispatch;
    - **explicit**: R's request carries `_meta["io.mcpproxy/reconciles"]` listing E's `call_id` (or blocker id). This is the only way to reconcile an entry whose late completion is still observable, i.e. whose upstream request mcpproxy provably still has outstanding. It prevents the #1317 failure, in which a `take_snapshot` read succeeds while `navigate_page` is still blocked on a consent dialog, and an unrelated background read would otherwise open the gate. The `_meta` key is read from the request's `params._meta`, so no input schema changes (FR-033). It is stripped before forwarding upstream.

  Each qualifying E becomes `reconciled`. Entries that became unresolved after R was dispatched are not touched. An explicit id that names an entry R does not qualify for is ignored (and logged at Debug); it never reveals whether that entry exists.
  - Q3: whether any such read is enough for implicit reconciliation. Recommended: yes, within the rules above.
- **FR-019 (cancel operation)**: `cancel_pending_call(call_id)` MUST:
  1. Authorize the caller per FR-021. A failure returns the non-disclosing `not_found`.
  2. If the entry is `unresolved`, on stdio, and has a JSON-RPC id, send `notifications/cancelled` for its **latest** id (FR-002) through **the transport instance captured at dispatch**.
     - The record holds a reference to that decorator instance. There is no lookup by epoch, so a reconnect cannot redirect an old id to a new session.
     - The send is bounded to 5s, and it never holds `epochMu` or the registry mutex.
  3. Mark the entry `cancelled`, with `resolution_actor`, whatever the result of the send.
  4. Return `{call_id, state:"cancelled", cancel_sent, cancel_error?, note}`. `note` says that the upstream may ignore the cancellation.

  **HTTP, streamable HTTP and SSE are local-only in v1**: no request id is captured (FR-002), so nothing is sent. The entry is still marked `cancelled`, and the response is `cancel_sent:false, cancel_error:"unsupported_transport"`. The `note` says the remote server was not told. A test asserts that no frame is sent on these transports. Capturing ids on HTTP transports, and making a captured id safe across a 113-e session re-init, is a follow-up.

  Cancelling an entry that is already terminal is an idempotent no-op that returns its current state.

**Security**

- **FR-020 (principal)**: The principal is a `Principal{Kind, ID}`:
  - **Agent token** (any `Kind`, including client credentials):
    - `Kind=agent_token`, and `ID = "tok_" + hex(sha256(len32(UserID) ‖ UserID ‖ len32(Name) ‖ Name ‖ CreatedAt.UTC().Format(RFC3339Nano)))[:32]` (128 bits). Each variable field is length-prefixed, so no two `(UserID, Name)` pairs can encode to the same bytes;
    - token names are unique per owner, not globally (Context item 11), so the owner namespace (`UserID`, `""` for ownerless personal-edition tokens) is part of the id;
    - `CreatedAt` is set once at creation, and `RegenerateAgentToken*` keeps it;
    - so a regenerated token keeps its principal; a token deleted and recreated with the same name gets a new principal; tokens with the same name and the same `CreatedAt` under different owners are different principals;
    - the id is derived from neither the secret, the hash nor the prefix;
    - propagation: `AgentToken.AuthContext()` MUST stamp a new, non-secret `AuthContext.TokenPrincipalID`, computed once at authentication. The registry never reads token storage;
    - a test creates two tokens named `ci` under two owners with a forced equal `CreatedAt` and asserts distinct ids, and a property test checks that length-prefixing separates `("a","bc")` from `("ab","c")`.
  - **Confined anonymous caller** (unauthenticated `/mcp` with `anonymousConfinementActive()`, Context item 11): `Kind=anonymous_profile`, `ID = "anon:" + <confining profile name>`. It is a non-admin principal (FR-021).
  - **Server-edition session user**: not a dispatch principal, because Spec 107 gives tenant sessions no dispatch door. A session user therefore owns no entries.
  - **Everything else** (API key, socket, stdio transport, unconfined anonymous `/mcp` in the personal edition, internal callers): `Kind=local`, `ID=local`.
  - Profiles do not otherwise split principals.
  - Principal derivation reads the same scoped view as FR-021, at every entry point and in the fail-closed path of FR-013, so a confined anonymous caller is never recorded as `local`.
- **FR-020a (gate scope)**: The gate (FR-014) and reconciliation (FR-018) are keyed on a **gate scope**, which is finer than the principal only for `local`:
  - for `agent_token` and `anonymous_profile` principals, gate scope = principal;
  - for `local` calls that arrive through an MCP session (call_tool_*, the direct surface, and `code_execution` sub-calls, which inherit the parent session), gate scope = `local/session:<MCP session id>`;
  - for `local` calls with no MCP session (REST, CLI, replay, socket callers without a session), gate scope = `local/nosession`.

  So one Claude Code session's timed-out write does not refuse, and is not silently reconciled by, a Cursor session or another Claude Code session on the same instance. **Visibility, listing and cancel stay per principal** (FR-021): any `local` caller can list and cancel any `local` entry, which matches A3 (one human). The refusal text names only entries in the caller's gate scope. Tests cover two `local` MCP sessions (US2 AS4a) and a `code_execution` sub-call that inherits its parent's scope.
- **FR-021 (authorization)**: Every request is checked against the caller's *current* effective scope.
  - **The caller view** is `auth.ScopedView(ac, confinedAnonymous)`, with the same `confinedAnonymous` predicate the management handlers use (Context item 11). **"Admin" means `IsAdmin()` on that view**, never on the raw `AuthContext`. A raw `IsAdmin()` would make a profile-confined anonymous caller (whose raw context is `AuthTypeAdmin` on purpose) a global admin over every entry, including token-owned ones. The `pending.Caller` adapter is built from the scoped view, and a lint-style test asserts that the pending-call handlers on all three surfaces never call `IsAdmin()` on an unscoped context.
  - **Visibility** of an entry (in a list, a count, an SSE event or a refusal): the caller view is an admin, or the caller owns the entry **and** can currently see the server. "Can see" means `CanAccessServer` intersected with the profile scope (the Spec 105 intersection; for a confined anonymous caller, the confining profile).
  - **Cancel** requires all of:
    - visibility;
    - the exact permission that matches the entry's **admission tier** (FR-015): `HasPermission(admission_tier)`. Permissions are exact-match. The admission tier is the tier the same caller was already allowed to call at, so whoever could make the call can cancel it. In particular, a read-only token that called an unannotated tool through `call_tool_read` (gate tier `write`, admission tier `read`) can cancel its own entry;
    - `read_only_mode` is not set.
  - Admin callers (on the scoped view) may cancel any entry.
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
  - **Server edition**: tenant sessions get the fixed 403 on all three routes. Leaving a route out of the allowlist is **not** enough for the per-server GET, because `sessionGETAllowed` admits every GET under `/api/v1/servers/{id}/**` except `tool-calls` (Context item 11). So `internal/httpapi/session_principal.go` gains an explicit named must-refuse, `sub == "pending-calls"` (and any deeper path under it) returns false, beside the `tool-calls` one. `GET /api/v1/pending-calls` and the `POST` are outside the `/servers/` prefix and already refused by default. The walk test (`tenant_allowlist_walk_test.go`) and a direct unit test of `sessionGETAllowed` assert the 403 for all three. Letting a tenant manage the calls of its own tokens is deferred to a `/user/*` follow-up.
  - The per-server gate override is **not** exposed through the `upstream_servers` patch. It is set through the config file, REST PATCH and the Web UI's raw JSON only. This keeps the tool-surface delta to what FR-033 lists.
- **FR-024 (activity)**:
  - `storage.ActivityRecord` and `contracts.ActivityRecord` gain `call_id`, `pending_state`, `pending_reason`, `late_outcome` and `resolved_at`, all `omitempty`. This applies to the four entry points that write activity records; replay is covered by FR-024c.
  - `ActivityFilter` gains `pending_state`.
  - For scoped callers, these fields are rendered only on rows that the caller already sees **and** whose attribution matches the caller. This is the same rule that `renderActivityAttributionForCaller` uses. Otherwise the fields are stripped.
- **FR-024a (activity linkage)**:
  - The tool-call completed event carries `call_id`. The activity service persists it, and writes a `call_id → activity id` index entry in the same bbolt transaction.
  - A resolution emits a bus event, `pending_call.resolved`. The activity service updates the record **by call_id**.
  - If the record has not been persisted yet (the save is asynchronous), the update is held in a bounded map (at most 1024 items, for 60s) and applied when the record is inserted (upsert semantics). This handles **ordering** only.
  - Updates for the same call id apply in state order. A terminal state wins and never regresses.
  - **Loss**: the bus drops an event when a subscriber's channel is full (Context item 11), and the held map can overflow. Neither may leave a persisted `pending_state: unresolved` forever. So the bus is only the fast path, and the **registry is the source of truth**:
    - every terminal transition is kept as a tombstone with `activity_synced=false`;
    - a resync loop in the activity service runs every 30s. It asks the registry for unsynced tombstones (`Registry.UnsyncedTerminal(limit)`), applies each one by `call_id` (an idempotent upsert), and acknowledges them (`Registry.AckSynced(ids)`). A tombstone is not evicted by `pending_call_history` until it is acknowledged, but the 256-per-server and global caps still apply; a tombstone evicted unacknowledged is handled by the next rule;
    - the same loop scans persisted activity records with `pending_state: unresolved` older than `pending_call_ttl + 1m` (an indexed query on the new `pending_state` field). If the registry has neither a live entry, a blocker containing it, nor a tombstone for that `call_id`, the record is set to `pending_state: untracked` ("mcpproxy no longer tracks this call: it was evicted, folded or lost in a restart"). `untracked` is an activity-only value; the registry never uses it;
    - at activity-service start, the same scan runs once, which covers mcpproxy restarts (Known Limitation 6).
  - Tests cover a response that arrives immediately after the timeout, a resolution whose bus event is dropped (subscriber channel full), a held-map overflow, and a restart with persisted unresolved rows. In each case the record reaches its terminal or `untracked` state within one resync interval.
- **FR-024c (replay)**: Replay writes a `storage.ToolCallRecord` directly and emits no activity event (Context item 11), so the `ActivityRecord` fields of FR-024 do not reach it. Instead:
  - `storage.ToolCallRecord` and `contracts.ToolCallRecord` gain `call_id`, `pending_state` and `pending_reason` (`omitempty`), set from the replay's own outcome. `record.Error` keeps the legacy string;
  - `ReplayToolCall` returns `*pending.RefusedError` and `*pending.CapacityError` **typed**, like the Spec 093 `LimitError` (`runtime.go:1596-1601`), instead of folding them into `record.Error`. The Server replay wrapper's audit switch (`server.go:4164-4194`) gains an arm that writes `tool_call outcome:rejected reason:pending_call_unresolved` (or `pending_registry_full`), and httpapi's replay handler (`httpapi/server.go:5481-5508`) maps them to 409 and 429;
  - later resolutions are **not** back-written into the replay record (out of scope). The live state is visible through the pending-calls list, SSE and the `call_id`;
  - tests: replay refused in `enforce` (409, one `tool_call rejected` audit line, no upstream receipt); replay timeout (record carries the three fields, `record.Error` matches the golden).
- **FR-024b (SSE)**: Three event types: `pending_call.unresolved`, `pending_call.resolved` and `pending_call.refused`.
  - Payload: `server`, `call_id`, `state`, `reason`, timestamps, and an internal `_principal` attribute.
  - All three types are added to `identityBearingEventTypes`, so a payload without `server` fails closed.
  - A new principal-aware filter delivers these events to a scoped subscriber only if FR-021 visibility holds for that subscriber, evaluated on the subscriber's scoped view. `_principal` is stripped before any frame is sent. Admin subscribers receive every event.
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
  - **Both** refusal points (advisory and authoritative) run after `emitActivityToolCallStarted` has written `authz allow` (Context item 11). Because `auditAuthz` is first-write-wins, an `authz deny` written there would be a silent no-op. So both are recorded like a Spec 093 limiter shed: `tool_call outcome:rejected reason:pending_call_unresolved`, through the same helper shape as `auditToolCallShed`. The capacity refusal (FR-007a) is recorded the same way with `reason:pending_registry_full`.
    - This adds two values to the set of `rejected` reasons on `tool_call` lines. That is a widening, which Spec 107 FR-013 treats as a minor change, so `schema_version` stays 1. The `authz` reason vocabulary is **not** changed, and `auditReasonFromBlockKey` gains no arm.
    - Round 1's "advisory refusal → `authz deny`" is withdrawn: it claimed the advisory check ran before `authz allow`, which the code contradicts.
  - The count invariants do not change: `#authz == #pre-dispatch decisions` and `#tool_call == #authz(allow)`. Tests assert both at each refusal point, on all five entry points, and assert that no `authz deny` line is produced.
  - A timed-out call's `tool_call` line is written once, at the timeout, with `error_class: upstream_timeout` (through 113-c FR-047). A late completion writes no audit line.
  - `cancel_pending_call` and `pending_calls` are built-in invocations, which Spec 107 FR-012 excludes from v1 audit. They produce **no** audit line. The actor is recorded on the activity record and in the Info log.
  - Q6: whether to add an audit event for cancellations.
- **FR-033 (tool-surface delta)**: The only intended change to the frozen tool-surface goldens is in `upstream_servers`:
  - two new `operation` enum values, `pending_calls` and `cancel_pending_call`;
  - one new string property, `call_id`;
  - the matching description text.

  The goldens are regenerated for exactly that diff, and the PR shows the diff. No other built-in tool changes. Spec 107 FR-044 ("no argument added") applied to Spec 107's own change. This spec makes a deliberate, documented exception to it.
- **FR-034 (phase-gated capabilities)**: The `resolve` actions and the refusal text are computed from a capability set compiled into the build (`late_completion`, `cancel`), never from the documentation. Config validation refuses `enforce` mode until both capabilities ship (plan phase 3).
  - What this guarantees: no release enforces the gate without a cancel operation, and the owner of an entry can always cancel it on some surface while it still holds the permission it was admitted with (FR-021). Round 1's stronger claim ("no release can block a caller whose only ways out are the TTL or a read") did not hold: an MCP-only caller can have `upstream_servers` hidden by its profile and no visible read-only tool, so its in-band actions can shrink to *wait*. FR-016 now names exactly the actions that remain, including "ask an operator to cancel through REST or the CLI".
  - Such callers are why `enforce` stays opt-in (Q2). A test covers a caller whose only in-band action is *wait*, and asserts the refusal text says so.

### Key Entities

- **DispatchRecord / PendingCall**: one dispatched call. Its fields are in FR-007 and its lifecycle in FR-008.
- **PendingCallRegistry**: per-process and per-server, owned by `upstream.Manager`. Its transitions are serialized per server (FR-009).
- **Principal**: see FR-020.
- **Gate decision**: `allow | warn | refuse`, plus the blocking call ids. It is never persisted beyond the activity record.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of dispatched calls that end by `call_tool_timeout` or by the end of the caller's ctx carry a `call_id` and a `pending_state` on the activity record and on any delivered result. `pending_state` is `unresolved`, or `completed_late` per FR-010a. The rate is 0% for calls refused before dispatch, including capacity refusals (FR-007a); no call is ever dispatched untracked.
- **SC-002**: In `enforce` mode, a write or destructive retry by the same principal during the unresolved window reaches the upstream 0 times on each of the five entry points. This includes the queued-write interleaving (US2 AS7). Reads reach the upstream every time.
- **SC-003**: Each resolution path opens the gate within 100 ms of its trigger in the test harness. Late completion is detected for 100% of stdio responses that arrive before `expires_at`, for calls within the detached share.
- **SC-004**: With two agent tokens there are 0 of each of these across principals: list entries, cancels, SSE `pending_call.*` frames, pending fields on activity rows, and evictions. The `not_found` bodies for "another principal's id", "narrowed scope" and "unknown id" are byte-identical.
- **SC-005**: In every mode, the legacy error string at each surface is byte-identical to the base-commit golden (FR-003a). For REST that is the HTTP `error` string, which embeds the legacy `call_tool_*` body (FR-003b); for `code_execution` it is `error.code` and `error.message`. Success results are unchanged. These tests pass unchanged:
  - `calltool_cancel_test.go`;
  - the Spec 105 epoch tests;
  - the toolsurface goldens, apart from exactly the FR-033 diff.
- **SC-006**: 10,000 timed-out calls from one principal against one server leave at most:
  - `max_pending_calls_per_principal` gating entries plus one overflow blocker;
  - 256 non-gating and terminal records;
  - `max_detached_waits_per_server` post-deadline detached waits.

  The gate stays closed for that principal throughout (folding never opens it). A second principal's gating entry survives. With the global cap forced to a small value and all slots `dispatched`, the next call is refused before dispatch with `pending_registry_full` and reaches the upstream 0 times. With three principals, each holds at least `floor(cap/3)` post-deadline waits. `go test -race` is clean, and no goroutine outlives its fixed inner deadline by more than 1s (goleak).
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
- **A3**: A personal-edition instance has one human, so callers that do not use an agent token share the `local` principal for visibility and cancel. Gating is still per MCP session (FR-020a), because one human often runs several independent agents.
- **A4**: 113-c lands before or together with the gate phase. If it has not landed, the refusal is recorded like the Spec 093 shed, without `error_class` stamping. The Spec 113 citations are pinned to `d5de9320d` and are re-checked when it merges.
- **A5**: The mcp-go v1.0.0 behaviors in Context items 2, 6, 6a and 7 hold. They were read from the module cache, not the repo, so they are dependency assumptions. Phase 1 adds one pin test each: `Stdio.SendRequest` deletes the response channel on `ctx.Done()`; `client.CallTool` goes through `multiRoundTrip` and sends a new id per round; `CallToolResult.NeedsInput()` detects an input-required result; the set of interfaces `client.Client` type-asserts on its transport; the server handler ctx carries no cancel cause. An mcp-go bump that changes any of them fails CI.

## Known Limitations

1. **Cancellation is advisory.** An upstream may ignore `notifications/cancelled`. `cancelled` means "the agent accepted the ambiguity".
2. **Reconciliation is the agent's judgment** (Q3).
3. **No late completion and no upstream cancel for HTTP/SSE** in v1 (FR-002, FR-019). Cancel there is local-only.
4. **No MCP Tasks.** Tasks are the durable, protocol-level answer, and belong in a separate spec.
5. **No `waiting_human`**, unless Q1 is answered otherwise.
6. **In-memory only.** A restart forgets all entries. Stdio children die with mcpproxy, but remote HTTP/SSE work may still land after a restart, unobserved. Persisted activity rows left `unresolved` become `untracked` at the next start (FR-024a).
7. **`connection_reset` opens the gate without proof.** Killing a stdio child does not undo side effects it already handed off (FR-009, Q5).
8. **No forwarding of caller cancellation** in v1 (FR-012).
9. **Unannotated tools gate conservatively** (FR-015). In `enforce` mode, an unannotated tool that only reads is refused after a timed-out write. Adding `readOnlyHint:true` (upstream or through annotation overrides) fixes this.
10. **Overflow blockers are coarse.** Past the per-scope cap, older entries fold into one blocker per `(server, gate scope)`; folded calls lose their individual late completion and cancel notification (FR-007a).
11. **Replay records are snapshots.** A replay's `pending_state` is not updated by later resolutions (FR-024c).

## Clarifications Needed (summary)

| # | Question | Recommended default |
|---|---|---|
| Q1 | Emit a `waiting_human` reason? (FR-004) | No. Reserve the value. mcpproxy registers no elicitation handler on upstream clients (Context item 6a), so no deadline can fire while it waits on a human. Revisit if mcpproxy ever forwards upstream elicitation to its own clients |
| Q2 | Default `pending_call_gate` mode (FR-017) | `warn` this release. `enforce` is available from phase 3. Flip stdio to `enforce` one minor release later, after the per-session gate scope (FR-020a) has been observed in `warn` telemetry |
| Q3 | Does any successful, explicitly read-only call reconcile implicitly? (FR-018) | Yes, as #1321 proposes, but only for entries whose late completion is not observable and that show no recent progress, within the caller's gate scope. Entries with a live detached wait need an explicit `_meta["io.mcpproxy/reconciles"]` |
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

## Revision notes (review round 2)

Each finding was checked against the code at `44244b858` (and mcp-go v1.0.0 in the module cache). All 20 were accepted; one was accepted in part.

| Finding | Verdict | Addressed in |
|---|---|---|
| P1 anonymous confinement: raw `IsAdmin()` | Verified (`auth/context.go:173-185`, `auth/scoped_view.go`) | FR-020 (`anonymous_profile` principal), FR-021 (admin = scoped view), FR-024b, US4 AS5–5a |
| P1 4096 cap has no overflow rule | Verified: records are only created at `CheckAndBegin`, and admission can be unlimited (`limiter.go:252-259`) | FR-007a (eviction → fold into overflow blockers → pre-dispatch `pending_registry_full`), FR-030, SC-001, SC-006 |
| P2 code_execution returns an envelope, not a throw | Verified (`jsruntime/runtime.go:472-481`) | FR-003a (additive `pendingCall`/`pendingCalls`, new code), plan scope adds `internal/jsruntime` |
| P2 REST flattening drops typed errors | Verified (`server/mcp.go:7243-7264`) | FR-003b (`withPendingCallCapture`), FR-016 |
| P2 / high advisory refusal after `authz allow` | Verified (`mcp.go:851-854`, first-write-wins `audit_funnel.go:327-338`) | FR-030 (both points are `tool_call rejected`; no `authz` vocabulary change), FR-013 |
| P2 tenant allowlist admits every `/servers/{id}/**` GET | Verified (`session_principal.go:144-160`) | FR-023 (named must-refuse for `pending-calls`) |
| P2 token names unique per owner | Verified (`storage/agent_tokens.go:186-193`) | FR-020 (owner in the id, length-prefixed, 128 bits), US4 AS9 |
| P2 lossy bus leaves rows unresolved | Verified (`event_bus.go:205-218`) | FR-024a (registry is the source of truth; 30s resync; `untracked`), Known Limitation 6 |
| P2 replay writes no activity record | Verified (`runtime.go:1574-1607`, `server.go:4164-4194`, `httpapi/server.go:5481-5508`) | FR-024c, FR-001, US1 AS4 |
| P2 / medium HTTP/SSE cancel unimplementable | Verified (decorator only at `connection_stdio.go:244`) | FR-019 (local-only, `unsupported_transport`), FR-002, Scope, Edge Cases, Known Limitation 3 |
| P2 "at most half" is not a reserved share | Verified by arithmetic | FR-007a (fair share with preemption of post-deadline waits; guarantee `floor(cap/N)`), US4 AS4a, SC-006 |
| P3 evidence: Spec 113 FR-049 has a text fallback; mcp-go claims unverifiable | Verified (`d5de9320d` spec line 197) | Evidence base, Context item 10, A5 (one pin test per mcp-go behavior) |
| high gate tier vs permission tier | Verified (`mcp.go:3022-3027`) | FR-015 (admission tier stored), FR-021 (cancel against admission tier), FR-016 (reconcile availability per caller and server), FR-034 (claim narrowed), US4 AS6–7 |
| high mcp-go multi-round-trip | Accepted in part. Multi-id and input-required late results are real (`client.go:631-643`, `mrtr.go:56-103`). **Rejected part**: "a deadline inside `fulfillInputRequests` is an explicit human-wait signal". mcpproxy registers no elicitation, sampling or roots handler on upstream clients, so `fulfillInputRequest` fails at once and cannot wait on a human. Q1 stays | Context item 6a, FR-002 (ordered id list, cancel the latest), FR-010 (`input_required`), Q1 |
| high REST legacy string embeds the JSON body | Verified (`httpapi/server.go:6147-6152`) | FR-003b (error built from the legacy body; `pending_call` as a sibling), SC-005, US1 AS6 |
| medium implicit reconcile opens the gate in #1317 | Accepted | FR-018 (implicit only for non-observable entries without recent progress; explicit `_meta` otherwise), US3 AS1–1a, Q3 |
| medium shared `local` principal | Accepted | FR-020a (gate scope per MCP session for `local`), FR-014, FR-018, US2 AS4a, A3, Q2 |
| medium emission under `epochMu` | Verified (`managed/client.go:151-158`) | FR-009 (methods return transitions; emit after unlocking; lock-probe test), FR-010a |
| medium `dispatched` records unbounded at the global cap | Verified | FR-007a (all record kinds counted; capacity refusal) |
