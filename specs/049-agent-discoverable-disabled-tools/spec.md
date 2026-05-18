# Feature Specification: Agent-Discoverable Disabled Tools

**Feature Branch**: `049-agent-discoverable-disabled-tools`
**Created**: 2026-05-18
**Status**: Draft
**Input**: Follow-up to PR #468. Brainstormed design of record: `docs/superpowers/specs/2026-05-18-agent-discoverable-disabled-tools-design.md`

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent learns a needed capability exists but is locked (Priority: P1)

An AI agent is working a task that requires a capability (e.g. deleting a
repository). The matching tool exists on an upstream server but is currently
locked. Today the agent receives nothing — the tool is invisible — so it tells
the user "that's not possible" instead of "the capability exists but is turned
off." The agent should be able to, on demand, discover that the tool exists,
learn *why* it is unavailable, and relay the *correct* next step to the user or
operator.

**Why this priority**: This is the core value. Without it the other stories have
nothing to act on. Delivers a working MVP by itself: an agent can opt into
seeing locked tools and explain them.

**Independent Test**: Configure an upstream with a tool denied by config and
another disabled by the user. Issue a discovery request with the opt-in flag and
confirm both appear with distinct, correct status and remediation, while a
normal discovery request still hides them.

**Acceptance Scenarios**:

1. **Given** a tool denied by server config, **When** the agent runs discovery
   with the opt-in flag, **Then** the tool is returned with status
   `disabled_by_config` and remediation stating it is operator policy that the
   user cannot lift from the UI.
2. **Given** a tool disabled by the user, **When** the agent runs discovery with
   the opt-in flag, **Then** the tool is returned with status `disabled_by_user`
   and remediation telling the agent to ask the user to re-enable it.
3. **Given** the opt-in flag is absent or false, **When** the agent runs
   discovery, **Then** the response is byte-for-byte identical to today (no
   locked tools, no extra fields).
4. **Given** a mix of callable and locked tools match the query, **When** the
   agent runs discovery with the opt-in flag, **Then** callable results appear
   first in their existing order, locked results follow, and no more than
   `min(limit, 10)` locked results are returned.

---

### User Story 2 - Agent is nudged toward the opt-in path when blocked (Priority: P2)

An agent that does not know the discovery flag exists should still find it. When
a tool call is rejected because the tool is locked, or when a normal discovery
returns zero callable results while relevant locked tools exist, the agent
should be told how to get the full picture and the correct remediation.

**Why this priority**: Closes the discoverability gap of an opt-in design.
Valuable but inert without Story 1.

**Independent Test**: Call a locked tool and assert the rejection message is
status-aware and points to the opt-in discovery path; run a query whose only
matches are locked and assert the zero-result response carries a one-line count
nudge.

**Acceptance Scenarios**:

1. **Given** a config-denied tool, **When** the agent calls it, **Then** the
   rejection message states it is operator policy (not user-overridable) and is
   distinct from the message for a user-disabled tool.
2. **Given** a query whose only matches are locked, **When** the agent runs
   normal discovery, **Then** the response includes a one-line note with the
   count of locked matches and how to reveal them — but not the entries
   themselves.

---

### User Story 3 - Operator/agent sees which servers hide capability (Priority: P3)

When listing or inspecting upstream servers, an agent or operator should be able
to tell, cheaply, which servers have non-callable tools and roughly why, so a
targeted discovery is warranted — without listing every locked tool name.

**Why this priority**: A corroborating signal that improves efficiency; the
feature is fully usable without it.

**Independent Test**: List servers where one has locked tools and one is fully
callable; assert the locked one carries a compact counts block and the fully
callable one carries none.

**Acceptance Scenarios**:

1. **Given** a server with at least one non-callable tool, **When** servers are
   listed, **Then** that server entry includes a tool-counts block broken down
   by reason, omitting any zero-valued reason.
2. **Given** a server whose tools are all callable, **When** servers are listed,
   **Then** that server entry includes no tool-counts block at all.

### Edge Cases

- A tool is both config-denied and user-disabled → it resolves to a single
  status by fixed precedence (config wins), never double-counted.
- The reason cannot be determined (transient lookup failure) → status is
  `disabled_unknown` with a neutral remediation; the system never emits a
  *wrong* remediation (e.g. "toggle it in the UI" for a config lock).
- A fully disabled server does not re-list its tools → its tools may not appear
  in discovery at all; the authoritative signal for a fully-off server is its
  server-level state, not discovery. Documented limitation, not a defect.
- An agent has restricted server scope → locked tools on servers it cannot
  access are never revealed, even with the opt-in flag.
- A pathologically restrictive config locks hundreds of tools → the locked
  portion of any single discovery response is capped so token cost stays bounded.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Discovery MUST accept an optional opt-in parameter
  (`include_disabled`, default false) that, when true, additionally returns
  tools that exist but are not callable.
- **FR-002**: When the opt-in is absent or false, discovery output MUST be
  byte-for-byte identical to current behavior (no locked tools, no added
  fields, no reordering).
- **FR-003**: Each returned locked tool MUST carry a lean shape: name, owning
  server, the existing one-line description, and a single `status` value.
- **FR-004**: `status` MUST be one of exactly five values —
  `disabled_by_config`, `disabled_by_user`, `pending_approval`,
  `server_disabled`, `disabled_unknown` — assigned by fixed first-match
  precedence in that order (server-off, then config, then user-disabled, then
  pending approval, else unknown).
- **FR-005**: The response MUST include a single remediation map emitted once,
  containing only the keys for statuses actually present in the response; no
  per-tool remediation text.
- **FR-006**: Callable results MUST retain their existing ranking and appear
  before any locked results; locked results MUST be capped at `min(limit, 10)`
  entries.
- **FR-007**: Agent server-scope filtering MUST be applied before locked-tool
  classification, so an agent never sees locked tools on inaccessible servers.
- **FR-008**: The tool-call rejection message MUST be status-aware: a
  config-denied rejection MUST state it is operator policy and not
  user-overridable, distinct from the user-disabled/quarantine wording.
- **FR-009**: When normal discovery returns zero callable results but relevant
  locked tools exist, the response MUST include a one-line note with the count
  of locked matches and how to reveal them, without including the entries.
- **FR-010**: Server listing/inspection MUST include a per-server tool-counts
  block broken down by reason, emitted only when at least one non-callable
  count is greater than zero, with zero-valued reasons omitted.
- **FR-011**: The feature MUST NOT change enforcement — a discovered locked tool
  remains non-callable; the callability decision is unchanged.
- **FR-012**: The feature MUST NOT introduce any new persistent storage;
  classification is computed at request time from already-available state.
- **FR-013**: Opt-in usage MUST be observable via an in-memory counter only
  (never persisted), consistent with existing telemetry privacy constraints.
- **FR-014**: The opt-in parameter MUST be documented in the discovery tool's
  own description (one sentence) so an agent can use it proactively.

### Key Entities *(include if feature involves data)*

- **Locked tool entry**: A discovered tool that is not callable — name, server,
  one-line description, and one `status` of the five-value set.
- **Status**: The single machine-branchable reason a tool is not callable,
  mapped 1:1 to a remediation class.
- **Remediation map**: Response-level mapping from each present status to one
  human/agent-actionable instruction string, emitted at most once per response.
- **Per-server tool counts**: A compact per-server rollup of tool counts by
  callability reason, conditionally attached to server list/inspect entries.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With the opt-in off, discovery responses are identical to the
  pre-feature baseline in 100% of regression cases (byte-for-byte).
- **SC-002**: When a needed capability is locked, an agent can obtain the tool's
  existence, reason, and correct remediation within one additional request.
- **SC-003**: A config-denied lock is never communicated with user-toggle
  remediation in any surface (discovery, rejection message, server listing) —
  0 occurrences across the test matrix.
- **SC-004**: The locked portion of any single discovery response never exceeds
  10 entries regardless of how many tools are locked.
- **SC-005**: Server-listing responses for fully-callable servers gain 0 added
  bytes from this feature.
- **SC-006**: An agent that ignores the static hint still reaches the opt-in
  path via the reactive nudge in 100% of zero-callable-result cases where
  locked matches exist.

## Assumptions

- Locked tools are present in the existing search index (verified during
  brainstorming: indexing does not filter by callability; filtering is
  request-time only).
- The config-vs-user discriminator introduced by PR #468 is available and is
  reused unchanged as the authoritative config-denial signal.
- "Limit" refers to the discovery request's existing result-limit parameter.
- The four UX fixes from the PR #468 review (status-aware rejection message,
  lock-badge color, bulk-enable feedback, copy consistency) ship in PR #468
  itself; FR-008's status-aware message is the discovery-facing extension of
  that and is owned here only insofar as it points at the opt-in path.

## Dependencies

- Builds on PR #468 (`feat/config-tool-allowlist`) being merged: requires its
  config-denial signal and the `config_denied`-aware data already plumbed.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` — links without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

**Rationale**: Issues are closed manually after production verification.

### Co-Authorship
- ❌ **Do NOT include** `Co-Authored-By: Claude ...`
- ❌ **Do NOT include** "🤖 Generated with Claude Code"
- ❌ The committer/author MUST be the human contributor, not "Claude Code"

**Rationale**: Per-repo contributor policy (user instruction 2026-05-18) —
authorship reflects the human, not the AI tool. Matches the speckit template.

### Example Commit Message
```
feat(mcp): opt-in include_disabled tool discovery

Related #[issue-number]

## Changes
- ...

## Testing
- ...
```
