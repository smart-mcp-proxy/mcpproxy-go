# Feature Specification: Request Queueing & Per-Upstream Concurrency Limits

**Feature Branch**: `093-concurrency-limits`
**Created**: 2026-08-07
**Status**: Draft
**Input**: User description: "Request queueing and per-upstream concurrency limits for multi-user deployments (fixes #955). Two-tier semaphore limiter (admission + run) at the managed-client choke point covering all dispatch paths including code_execution and activity replay. Config: max_concurrent_requests (global + per-server), queue_size, queue_timeout; 0=unlimited opt-in defaults; hot-reloadable. Shed semantics: isError tool result for MCP tools/call, HTTP 429 + Retry-After for REST, activity status rejected, rejection metrics. Decision report: docs/research/request-concurrency-issue-955-2026-08-07.html"

> Related: issue #955 ("request queueing / concurrency limit for multi-user deployments"). Decision analysis with choke-point audit and option comparison: `docs/research/request-concurrency-issue-955-2026-08-07.html` (Option A chosen).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Cap concurrent load on a fragile upstream (Priority: P1)

An operator runs mcpproxy as a shared service for multiple users/agents. One upstream MCP server is backed by a database that falls over under concurrent load. The operator sets a per-server concurrency limit; bursts of simultaneous tool calls to that server now run at most N at a time, with excess requests waiting briefly in a bounded queue instead of hammering the upstream — regardless of which user, agent, or internal feature (including sandboxed code execution) originated them.

**Why this priority**: This is the core ask of #955 — protecting stateful upstreams from multi-user bursts without external infrastructure. Everything else refines it.

**Independent Test**: Configure `max_concurrent_requests: 1, queue_size: 1` on a slow test upstream; fire 3 concurrent tool calls; observe exactly one running, one queued (then running), one rejected — and the upstream never sees 2 simultaneous requests.

**Acceptance Scenarios**:

1. **Given** a server with `max_concurrent_requests: 2`, **When** 5 tool calls arrive simultaneously for it, **Then** at most 2 execute against the upstream at any moment and the rest wait their turn in arrival order.
2. **Given** the same limit, **When** calls originate from different surfaces (MCP tool-call variants, legacy call, REST tool-call endpoint, sandboxed code-execution scripts, activity replay), **Then** all of them count against and are bounded by the same per-server limit — no origin bypasses it.
3. **Given** a stdio-based upstream with a limit, **When** concurrent calls arrive, **Then** the limit applies the same way it does for HTTP upstreams.
4. **Given** no limits are configured (defaults), **When** any burst arrives, **Then** behavior is exactly as today — unlimited, no queueing, no new failure modes.

---

### User Story 2 - Bounded queue with predictable shedding (Priority: P2)

When a limited server is saturated, excess requests wait in a bounded queue. If the queue is full, new requests are rejected immediately; if a queued request waits longer than the configured queue timeout, it is rejected then. In both cases the caller gets a clear, machine-actionable "server busy — retry" signal rather than a hang, a cryptic failure, or an aborted agent session.

**Why this priority**: Queueing without bounds trades overload for unbounded latency and memory; shedding without clear semantics breaks agent loops. This story makes saturation safe and observable for callers.

**Independent Test**: With `max_concurrent_requests: 1, queue_size: 1, queue_timeout: 2s` and a deliberately slow upstream, verify: 2nd call queues then runs; 3rd call is rejected instantly with a busy message; a queued call that waits > 2s is rejected with a timeout-flavored busy message; the AI agent making the calls sees a readable error it can retry, not a dropped connection.

**Acceptance Scenarios**:

1. **Given** the queue is full, **When** another call for that server arrives, **Then** it is rejected immediately (no waiting) with a message naming the server, its limit, and advising retry.
2. **Given** a call is queued, **When** it waits longer than `queue_timeout`, **Then** it is rejected with a busy/timeout message; **When** the caller cancels while queued, **Then** the slot is released and the cancellation is reported as such (not as "server busy").
3. **Given** an MCP agent client made the call, **When** it is shed, **Then** the rejection arrives as a normal tool-call error result (the kind agents read and retry), not a protocol/transport failure that can abort the session.
4. **Given** the call came via the REST API, **When** it is shed, **Then** the response is HTTP 429 with a Retry-After hint.
5. **Given** requests are being shed, **When** the operator inspects the activity log, **Then** shed calls are recorded with a distinct "rejected" status (distinguishable from upstream errors) including which limit triggered (queue full vs. queue timeout).

---

### User Story 3 - Global backstop and live tuning (Priority: P3)

The operator also sets a global concurrency cap as a backstop for the whole proxy, and tunes any of the limits at runtime by editing the config file — changes apply to a running instance without a restart and without disrupting in-flight calls. Saturation is visible in metrics so the operator can right-size limits.

**Why this priority**: Multi-user operators need a whole-instance guardrail and zero-downtime tuning; observability closes the loop. Valuable, but only after per-server limiting works.

**Independent Test**: Set a global limit lower than the sum of per-server limits, verify aggregate concurrency never exceeds it; change limits in the config file while under load and verify the new values take effect without restart or dropped in-flight calls; confirm rejection counters and queue metrics move.

**Acceptance Scenarios**:

1. **Given** a global `max_concurrent_requests`, **When** load spans many servers, **Then** total concurrent upstream tool calls never exceed the global cap, and a slow server's queue does not consume global capacity while waiting.
2. **Given** a running instance under load, **When** the operator changes limit values in the config file, **Then** new values apply to subsequent calls without restart; in-flight and already-queued calls complete under the rules they started with.
3. **Given** limits are active, **When** the operator scrapes metrics, **Then** rejection counts (by server and reason) and queue depth are available; sustained saturation is visible.
4. **Given** the server edition with multiple users, **When** several users call the same upstream, **Then** per-server limits bound the users' combined load on that upstream.

---

### Edge Cases

- Caller cancels/disconnects while queued → the queue slot frees immediately; no leaked capacity.
- Queue wait must not silently consume the existing per-call execution timeout — a call that queues 20s still gets its full execution time budget.
- Limits change while requests are queued → old waiters drain under the old rules; new arrivals see new rules; no double-counting or lost slots during the swap.
- A server is disabled/quarantined/removed while calls are queued for it → queued calls fail with the existing server-unavailable semantics, not a hang until queue timeout.
- Per-server limit larger than global → global still wins; documentation states effective concurrency is min of the two.
- Per-server tri-state: absent inherits the global default; explicit 0 opts a server out of per-server limiting while the global aggregate cap still applies; nonsensical configs (negative values, queue attached to an unlimited limit) are rejected at validation with clear messages.
- A queued call must never stall unrelated server management: admission happens outside any shared manager lock, and disabling/removing a server promptly fails its queued calls.
- Internal traffic that is not upstream tool calls (local tool search, cached tool listings, lightweight health probes) is not throttled and cannot deadlock behind the limiter.
- The standalone CLI debug client (separate process) is out of scope and documented as such.

## Requirements *(mandatory)*

### Functional Requirements

**Limiting & queueing**

- **FR-001**: Operators MUST be able to set a per-server maximum on concurrently executing upstream tool calls; excess calls wait in a bounded FIFO queue of configurable size (`max_concurrent_requests`, `queue_size` per server).
- **FR-002**: Operators MUST be able to set a global maximum on concurrently executing upstream tool calls across all servers, layered with per-server limits such that waiting for a specific server's slot does not consume global capacity.
- **FR-003**: Every in-process origin of upstream tool calls MUST be subject to the limits — the MCP tool-call variants, the legacy tool-call path, direct-routing mode, the REST tool-call endpoint, sandboxed code-execution scripts, and activity replay. No origin may bypass the limiter.
- **FR-004**: A call arriving when the queue is full MUST be rejected immediately. A queued call MUST be rejected when its total wait exceeds the configured `queue_timeout` — a single absolute deadline spanning both the per-server and global admission steps combined (never `queue_timeout` per step). Queue order MUST be first-in-first-out.
- **FR-005**: The call's execution timeout MUST begin only after admission (all limiter tiers acquired) so queue waiting never consumes execution budget; this applies to every origin, including internal ones whose execution deadline is currently created before the upstream call (activity replay MUST be adjusted accordingly). A caller's own deadline/cancellation is still honored while queued: cancellation MUST release the slot immediately and be reported as cancellation, not as shedding.
- **FR-006**: With no limits configured (the default), observable behavior MUST be unchanged: no queueing, no limiting, no new errors, and performance overhead within the agreed regression threshold (see SC-002).
- **FR-007**: Lightweight non-tool-call traffic (local tool search, coalesced tool listings, health probes) MUST NOT be throttled by these limits.
- **FR-008**: A call MUST NOT wait in a limiter queue while holding any proxy-wide or manager-wide lock: the dispatch layer MUST resolve the target client and release shared locks before admission begins, so queued calls never block server add/remove/disable, reconciliation, or config reload.
- **FR-009**: When a server is disabled, removed, or quarantined, its queued calls MUST be failed promptly with the existing server-unavailable semantics (not left to hit `queue_timeout`), and its limiter state MUST be released.

**Shed semantics**

- **FR-010**: A shed MCP tool call MUST return a normal tool-call error result (readable by agent LLMs, retry-friendly) that states which limit triggered — the named server's limit or the proxy-wide (global) limit — and advises retrying shortly; never a transport/protocol failure that can abort an agent session. The message MUST NOT blame a server when the global limiter was the trigger.
- **FR-011**: A shed REST tool call MUST return HTTP 429 with a Retry-After hint. The limiter's typed rejection identity MUST be preserved end-to-end through the REST dispatch path (today intermediate layers flatten errors into strings, which would make 429 mapping impossible); Retry-After derives from the effective `queue_timeout` (or a documented constant when unlimited queueing is configured).
- **FR-012**: Every shed call MUST be recorded with a dedicated "rejected" activity status regardless of origin — including origins that bypass the MCP dispatch layer (sandboxed code execution, activity replay) — via an origin-independent rejection seam at or below the limiter, carrying stable metadata: `reason` (queue_full | queue_timeout) and `scope` (server | global) plus the server name and origin. Because activity status is a closed vocabulary today, the new status MUST be propagated through the full consumer contract: storage/API schema, activity filters and summaries, usage aggregation, exports, and Web UI rendering.
- **FR-013**: Rejection counters (per server, per reason, per scope) and queue depth MUST be exposed through the existing metrics surface, again independent of call origin.

**Configuration & operations**

- **FR-020**: Limits MUST be configurable globally and per server in the standard config file with explicit tri-state per-server semantics: **absent = inherit the global per-server default; explicit 0 = no per-server limit for this server (the global aggregate limiter still applies); positive = override**. Global `max_concurrent_requests: 0` (the default) means no global limiter. The global limiter's own queue settings and the inherited per-server defaults MUST be distinguishable in the config surface, and documentation MUST state that a server's effective concurrency is bounded by both its own and the global limit.
- **FR-021**: All limit settings MUST be hot-reloadable without restart. A reload MUST publish the global and per-server limit values as one atomic generation — a call is "admitted" under exactly one generation (captured at admission), and calls never observe a mix of new global and old per-server rules (or vice versa). In-flight and already-queued calls complete under the generation they captured.
- **FR-022**: The global limit settings MUST be overridable via environment variables following the existing naming convention, documented, and reflected in the API schema like any other config field. Per-server values are file/API-configured only (no per-server env scheme exists or is introduced).
- **FR-023**: Validation MUST reject negative values and a nonzero queue size whose corresponding concurrency limit resolves to unlimited, with actionable error messages naming the offending server and field.
- **FR-024**: Limits MUST apply identically in the personal and server editions; in the server edition, per-server limits bound the combined load of all users on that upstream.

### Key Entities

- **Concurrency limit (per server / global)**: maximum simultaneously executing upstream tool calls; per-server values are tri-state (absent = inherit, 0 = no per-server limit, positive = override); global 0 = no global limiter.
- **Wait queue**: bounded FIFO of admitted-but-not-yet-running calls for a given limit; characterized by size and per-call wait deadline.
- **Shed event**: a rejected call — reason (queue_full | queue_timeout), scope (server | global), origin surface, server, timestamp; appears in the activity log and metrics regardless of origin.
- **Limit generation**: the atomically published snapshot of all limit values (global + per-server) a call is admitted under; hot reload replaces the generation as a unit.
- **Effective limit resolution**: per-server explicit value (0 = none, positive = cap) → absent inherits global default → unlimited; effective concurrency for a server is bounded by both its own and the global limit.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With a per-server limit of N configured, the upstream never observes more than N simultaneous requests, under bursts of at least 10× N, from any mix of origins — verified for both stdio and HTTP upstreams.
- **SC-002**: With defaults (no limits), the existing regression suites (unit, race, API E2E) pass unchanged, and tool-call latency overhead versus the previous release stays within an agreed threshold (≤1% median in the standard benchmark) — measured, not asserted.
- **SC-003**: Under sustained 5× overload of a limited server, agent clients continue operating: 100% of shed calls surface as readable retryable errors; zero client sessions abort due to shedding.
- **SC-004**: An operator can raise or lower any limit on a loaded instance and see it take effect within one config-reload cycle, with zero dropped in-flight calls.
- **SC-005**: Every shed call is attributable in the activity log (status, reason, server) and counted in metrics; queue-full sheds respond in under 100 ms (no waiting on a full queue).
- **SC-006**: The #955 deployment pattern (multi-user headless service in front of a database-backed stdio server) can cap that server's concurrency without any external reverse proxy, including traffic that never traverses an external listener.

## Assumptions

- The chosen approach is Option A from the decision report: a two-tier (admission + run) limiter at the single managed-client choke point, per-server acquired before global. The report's choke-point audit — including the finding that code-execution and replay paths bypass the manager layer — is the authoritative placement rationale.
- Cross-model review (Codex, round 1) established two placement corrections the plan must honor: (1) the manager dispatch path currently holds a manager-wide read lock across the upstream call, so dispatch must snapshot the client and release shared locks before admission (FR-008); (2) activity replay currently creates its execution-timeout context before calling the client, so it must be restructured for FR-005 to hold.
- Defaults ship as unlimited (0/0, 30s queue timeout when queueing is enabled): zero behavior change on upgrade. Documentation will recommend a small limit (e.g. 5) for stdio upstreams, mirroring common SDK worker-pool sizes; stdio does not require limit=1 since the transport legitimately multiplexes.
- Per-user fairness within a server's queue (server edition) is out of scope for v1; the design keys limiters by server name so a future per-(user,server) extension (Spec 074 direction) does not require rework.
- Upstream tool listings and health probes stay outside the limits (already coalesced / lightweight); a strict "count everything" mode is not offered in v1.
- Shedding uses tool-result errors for MCP and 429 for REST; an additional JSON-RPC protocol-error convention (e.g. -32029) is deferred until client behavior is verified.
- The separate-process CLI debug client is documented as out of scope.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #955` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #955`, `Closes #955`, `Resolves #955` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(upstream): per-server concurrency limits with bounded queueing

Related #955

Two-tier limiter at the managed-client choke point; opt-in via
max_concurrent_requests/queue_size/queue_timeout (global + per-server).

## Changes
- internal/upstream/limiter package (admission + run semaphores)
- Config fields with hot-reload, env overrides, validation
- Shed semantics: isError tool result / 429 + Retry-After / activity "rejected"

## Testing
- Limiter unit tests incl. hot-swap under load
- E2E: slow stdio server, queue + shed assertions, code_execution coverage
```
