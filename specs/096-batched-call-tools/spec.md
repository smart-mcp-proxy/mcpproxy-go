# Feature Specification: Batched call_tools() for Parallel Upstream Calls in the Code-Execution Sandbox

**Feature Branch**: `096-batched-call-tools`
**Created**: 2026-08-14
**Status**: Draft
**Input**: User description: "Batched call_tools() host primitive so independent upstream calls run in parallel inside the code-execution JS sandbox (GitHub issue #987)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Fan-out orchestration completes in parallel time (Priority: P1)

An AI agent uses the code-execution sandbox to orchestrate many independent upstream tool calls in one request — fetch 20 pull requests, read 15 issues, poll 8 servers. Today each `call_tool()` is strictly serial, so the run costs the *sum* of the upstream latencies (a measured run made 31 upstream calls in 20.6 seconds, nearly all of it waiting on I/O with no ordering dependency). With `call_tools()`, the script hands the whole independent set to the host in one call and receives all results at once, so the run costs roughly the *slowest* call, not the sum.

**Why this priority**: Fan-out is the headline use case the code-execution feature exists for, and it is the pattern that pays the most under serial execution. Without this story there is no feature.

**Independent Test**: Run a script that issues N independent calls against a stub upstream with a fixed per-call delay, once as a `call_tool()` loop and once as one `call_tools()` batch; the batched run completes in a fraction of the serial time and returns identical per-call results in input order.

**Acceptance Scenarios**:

1. **Given** a connected upstream server whose tool responds in ~300ms, **When** a script calls `call_tools()` with 10 independent requests to it, **Then** all 10 results arrive in input order, each carrying the same envelope a lone `call_tool()` would return, and the batch completes in significantly less time than 10 serial calls.
2. **Given** a batch whose elements target several different servers, **When** the batch runs, **Then** every element executes with the same scope, approval, and logging treatment it would receive as an individual `call_tool()`.
3. **Given** an empty array, **When** `call_tools([])` is called, **Then** it returns an empty array immediately and consumes none of the execution's tool-call budget.

---

### User Story 2 - One failure never poisons the batch (Priority: P2)

A script fans out 20 calls; one target server is down and one element references a tool that does not exist. The batch still returns 20 slots in input order: 18 carry `{ok: true, result}`, the 2 failures carry `{ok: false, error}` with the same error codes a lone `call_tool()` would produce. The script inspects each slot and proceeds with the successes.

**Why this priority**: Per-slot error isolation is the reason to prefer an explicit batch API over implicit parallelism; without it a single flaky upstream destroys whole-batch results and the primitive is unusable for real fan-out.

**Independent Test**: Build a batch mixing valid elements, an unknown server, an unknown tool, and a scope-violating element; assert every slot resolves, failures carry per-slot errors, and successes are unaffected.

**Acceptance Scenarios**:

1. **Given** a batch of 5 where element 3 targets a server that is not connected, **When** the batch runs, **Then** slots 1, 2, 4, 5 carry results, slot 3 carries `{ok: false, error}`, and no slot is missing or reordered.
2. **Given** a batch element that violates the execution's server scope (allowed_servers, profile, or agent-token scope), **When** the batch runs, **Then** that slot fails with the same error a lone `call_tool()` to that server would produce, and sibling slots are unaffected.

---

### User Story 3 - Concurrency stays operator-controlled (Priority: P3)

An operator caps how hard one sandbox execution may hammer upstreams. The batch runs at most `max_parallel` elements at once — from configuration by default, overridable per call downward or upward within the configured ceiling — and always honors the per-server concurrency and queueing limits that already govern individual calls (Spec 093). A batch can never become a way around those limits.

**Why this priority**: Without a bound, one script could open unbounded concurrent upstream requests; without respecting per-server limits, the batch would bypass protections that individual calls obey. Both would make the feature unshippable, but the default bound makes this safe out of the box, so it ranks after the core semantics.

**Independent Test**: Point a batch of 10 at a stub server that records concurrent-request high-water mark; with `max_parallel` 3 the high-water mark never exceeds 3; with a per-server concurrency limit of 1 configured, requests to that server serialize regardless of `max_parallel`.

**Acceptance Scenarios**:

1. **Given** a configured default `max_parallel`, **When** a batch larger than the bound runs, **Then** no more than `max_parallel` elements are in flight at any moment and all elements still complete.
2. **Given** a per-call `max_parallel` override within the permitted range, **When** the batch runs, **Then** the override governs; an override outside the permitted range is rejected before any element is dispatched.
3. **Given** a per-server concurrency limit lower than `max_parallel`, **When** a batch targets that server, **Then** the per-server limit governs those elements (the batch queues rather than bypasses).

---

### Edge Cases

- **Budget exhaustion mid-batch**: each element counts against the execution's `max_tool_calls` budget individually. When a batch is larger than the remaining budget, the elements within budget (in input order) execute and the excess slots fail with the same over-budget error a lone `call_tool()` would produce — no short-circuit, no partial slot.
- **Execution timeout during the batch**: the batch is bounded by the overall execution timeout; at the deadline the execution ends exactly as it would if a lone `call_tool()` were in flight.
- **Malformed input**: a non-array argument, or an element that is not an object with a `server` string and a `tool` string, rejects the whole call before any element is dispatched, with an error naming the offending element index. `args` is optional per element and defaults to an empty object.
- **Single-element batch**: behaves identically to `call_tool()` for that element, envelope included.
- **Duplicate elements**: permitted; each executes and is budgeted independently.
- **Non-JSON-serializable result**: an element whose upstream result cannot be represented in the documented wire shape fails that slot with the same serialization error a lone `call_tool()` produces; siblings are unaffected.
- **Sandbox character preserved**: the sandbox remains ES5.1 and timer-free; `call_tools()` is synchronous from the script's point of view (it returns the completed results array; there are no promises, callbacks, or timers).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The code-execution sandbox MUST expose a `call_tools(requests, options?)` function accepting an array of `{server, tool, args?}` elements and returning an array of the same length, in input order, one slot per element.
- **FR-002**: Each result slot MUST carry exactly the envelope `call_tool()` returns — `{ok: true, result}` or `{ok: false, error: {code, message}}` — and a failing element MUST NOT affect any other slot (no short-circuiting).
- **FR-003**: Independent elements MUST execute concurrently such that a batch of N independent calls completes in wall-clock time bounded by the slowest element plus scheduling overhead, not the sum of element latencies, whenever N is within the concurrency bound.
- **FR-004**: Concurrency MUST be bounded by `max_parallel`: a configured default applies to every batch, and a per-call override is accepted within a permitted range; an out-of-range override MUST be rejected before any element is dispatched.
- **FR-005**: Batched elements MUST respect the same per-server concurrency and queueing limits that govern individual upstream calls (Spec 093); the batch MUST NOT provide any path around them.
- **FR-006**: Each element MUST count against the execution's `max_tool_calls` budget individually; elements beyond the remaining budget MUST fail per-slot with the same over-budget error as a lone `call_tool()`, without being dispatched upstream.
- **FR-007**: The batch MUST be bounded by the overall execution timeout, with no additional or extended deadline of its own.
- **FR-008**: Each slot's `result` MUST be presented in the same documented wire (JSON) shape as `call_tool()` results — never as live host values.
- **FR-009**: Every element MUST pass the same scope, permission, and approval enforcement as an individual `call_tool()` (allowed_servers, profile scope, agent-token scope, quarantine and approval gates), evaluated per element.
- **FR-010**: Every element MUST be recorded in tool-call history and activity logging exactly as an individual `call_tool()` would be, correlated to the same parent execution.
- **FR-011**: The sandbox MUST remain ES5.1 and timer-free; `call_tools()` MUST be synchronous from the script's perspective.
- **FR-012**: A malformed call — non-array argument, or an element lacking a `server` or `tool` string — MUST be rejected before any element is dispatched, with an error identifying the first offending element.
- **FR-013**: `call_tools([])` MUST return an empty array, consume no tool-call budget, and dispatch nothing.
- **FR-014**: The capability MUST be available wherever `call_tool()` is available today (every surface that executes sandbox code, in both editions), and the code-execution tool description MUST document `call_tools()` alongside `call_tool()`.

### Key Entities

- **Batch request element**: one intended upstream call — target server name, tool name, and optional arguments object.
- **Batch result slot**: the outcome for one element — success envelope with the wire-shaped result, or failure envelope with an error code and message; position matches the element's input position.
- **Concurrency bound (`max_parallel`)**: the maximum number of elements in flight at once — a configured default with a per-call override inside a permitted range; subordinate to per-server limits.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A batch of 10 independent calls to an upstream with uniform latency completes in under 35% of the time the same 10 calls take serially (measured against a stub upstream with fixed delay).
- **SC-002**: The 31-call fan-out scenario from the originating issue (~20.6s serial) completes in under 5 seconds when expressed as batches, on the same upstreams.
- **SC-003**: In a batch with any mix of failing elements (unreachable server, unknown tool, scope violation, over-budget), 100% of slots resolve — every failure is per-slot and every sibling success is intact.
- **SC-004**: With a per-server concurrency limit configured, a batch never exceeds that limit for that server's elements (observed concurrent-request high-water mark equals the limit), regardless of `max_parallel`.
- **SC-005**: Existing single-call scripts and all existing code-execution behavior are unchanged: the full existing test suite passes without modification (beyond additions).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #987` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #987`, `Closes #987`, `Resolves #987` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add batched call_tools() to the code-execution sandbox

Related #987

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]

## Testing
- [Test results summary]
```
