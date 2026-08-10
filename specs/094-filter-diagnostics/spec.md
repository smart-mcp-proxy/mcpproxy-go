# Feature Specification: retrieve_tools Filter Diagnostics

**Feature Branch**: `094-filter-diagnostics`
**Created**: 2026-08-10
**Status**: Draft
**Input**: User description: "retrieve_tools filter diagnostics (GH #969): compact filter_diagnostics block reporting how many query-matched tools were omitted by annotation-based safety filters (read_only_only, exclude_destructive, exclude_open_world) and why, with an actionable suggestion; absent on the happy path; works in full and compact response modes"
**Origin**: GitHub issue #969 (field report), item 1 of 3. Items 2 (upstream-scoped retrieval) and 3 (required-tool preflight) are explicitly out of scope — see Out of Scope.

## Problem

When a `retrieve_tools` query uses the annotation-based safety filters (`read_only_only`, `exclude_destructive`, `exclude_open_world`), tools that matched the query but were removed by a filter vanish silently. The caller cannot distinguish between materially different situations that require different actions:

| What actually happened | What the caller should do |
|---|---|
| Tool doesn't exist / not indexed | Check server connection, wait for indexing |
| Tool is locked (disabled/quarantined) | Existing `notice`/`include_disabled` flow already covers this |
| Tool exists and is callable, but upstream never published annotations | Fix upstream annotations (or drop the filter deliberately) |
| Tool exists but is explicitly marked unsafe for the filter | The filter is working as intended |

The reporter's real-world case: a finance automation with `read_only_only=true` lost access to healthy, callable read-only tools because the upstream server didn't publish `readOnlyHint=true`. From the response alone, this was indistinguishable from "discovery is broken". Once upstream published annotations, discovery recovered — but nothing in the product told the operator that was the fix.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent learns why matched tools were withheld (Priority: P1)

An AI agent (or the operator reading its transcript) calls `retrieve_tools` with a safety filter enabled. Some tools match the query but are removed by the filter. The response now carries a compact diagnostics block stating how many tools matched before filtering, how many were omitted by which filter, and why (missing annotations vs. explicitly unsafe) — so the agent can report the true cause instead of "tool not found".

**Why this priority**: This is the exact failure mode from the field report, against the product's flagship discovery feature. Without it, safety filters actively mislead the people most careful about safety.

**Independent Test**: Configure an upstream whose tools carry no annotations. Query with `read_only_only=true` for a term that matches those tools. The response must contain the diagnostics block attributing the omissions to missing read-only annotations, and must contain the same tools when the filter is dropped.

**Acceptance Scenarios**:

1. **Given** an upstream with callable tools that match "finance" but have no annotations, **When** the caller runs `retrieve_tools(query="finance", read_only_only=true)`, **Then** the response includes a `filter_diagnostics` block with `matched_before_filters` ≥ 1, a non-zero omission count attributed to the read-only filter with a "missing annotation" reason, and an actionable suggestion mentioning upstream annotations.
2. **Given** the same setup after the upstream publishes `readOnlyHint=true` on those tools, **When** the same query runs, **Then** the tools are returned normally and **no** `filter_diagnostics` block is present.
3. **Given** a query where filters omit nothing, **When** it runs with filters set, **Then** no `filter_diagnostics` block is present (zero noise when filters have no effect).

---

### User Story 2 - Operator distinguishes "fix upstream" from "filter working as intended" (Priority: P2)

An operator investigating why an automation lost a tool reads the diagnostics block and can tell whether omissions were caused by *missing* annotations (remediation: fix upstream metadata) or by *explicit* unsafe annotations (`readOnlyHint=false`, `destructiveHint=true`, `openWorldHint=true` — remediation: none; the filter is doing its job). The block also names which servers the omitted tools belong to, so the operator knows where to look.

**Why this priority**: The two causes have opposite remediations; a bare count would still leave the operator guessing. Server names turn "something was filtered" into "go check wx-copilot".

**Independent Test**: Two upstreams — one with unannotated tools, one with tools explicitly annotated `readOnlyHint=false`. A `read_only_only=true` query matching both yields a diagnostics block whose per-reason counts separate the two, and whose affected-server list names both servers.

**Acceptance Scenarios**:

1. **Given** matched tools omitted for mixed reasons, **When** the query runs with `read_only_only=true`, **Then** the diagnostics separate omissions with missing annotations from omissions with explicitly unsafe annotations, and list the affected server names.
2. **Given** omissions caused only by explicit unsafe annotations, **When** the query runs, **Then** the suggestion text does NOT tell the operator to fix upstream annotations (it acknowledges the filter excluded explicitly unsafe tools and offers retrying without the filter to inspect them).

---

### User Story 3 - Zero results no longer reads as "discovery is broken" (Priority: P3)

When filters remove *every* match, the response (`tools: []`, `total: 0`) currently looks identical to "nothing exists". With diagnostics present, an agent or operator immediately sees that N tools matched and were all withheld by filters — the most confusing variant of the failure mode.

**Why this priority**: This is the highest-confusion case (it looks like an outage), but it is a special case of Story 1's mechanism.

**Independent Test**: A query whose every match is unannotated, with `read_only_only=true`, returns zero tools plus a diagnostics block accounting for all matches.

**Acceptance Scenarios**:

1. **Given** every query match lacks annotations, **When** `retrieve_tools(query=..., read_only_only=true)` runs, **Then** `total` is 0 and `filter_diagnostics.matched_before_filters` equals the number of withheld tools.
2. **Given** the same call, **Then** the existing locked-tools `notice` (which covers disabled/quarantined tools) is unchanged and, when both conditions apply, both signals appear without contradicting each other.

---

### Edge Cases

- **Multiple filters active, tool fails several**: each omitted tool is attributed to exactly one filter — the first that excluded it, evaluated in a fixed documented order (read-only → destructive → open-world) — so per-filter counts always sum to total omissions (no double counting).
- **Read-only implies non-destructive**: the existing filter semantics (a tool with explicit `readOnlyHint=true` passes `exclude_destructive` regardless of `destructiveHint`) must be reflected unchanged in the counts — diagnostics describe what the filter actually did, never a simplified model of it.
- **Interplay with result limit**: filters run on the candidate set the search produced for this call (already capped by `limit`). Diagnostics therefore describe that candidate window, not the whole index; this is acceptable and must be documented in the tool description. Counts never exceed `limit`.
- **Interplay with locked/quarantined tools**: tools dropped earlier by visibility rules (disabled, quarantined, out of scope) are *not* part of the annotation-filter candidate set and must not appear in these diagnostics — the existing `notice`/`include_disabled` flow owns them. The two mechanisms are additive and must not double-report a tool.
- **Annotation lookup fails / server briefly unavailable**: a tool whose annotations cannot be resolved is treated as unannotated (matches current filter behavior); its omission is attributed to the "missing annotation" reason.
- **No filters set**: response must be byte-identical to today's response regardless of what would have been filtered (back-compat guarantee, cf. SC-002).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When at least one annotation filter (`read_only_only`, `exclude_destructive`, `exclude_open_world`) is active AND at least one query-matched, otherwise-returnable tool was omitted by those filters, the `retrieve_tools` response MUST include a `filter_diagnostics` block. In all other cases (no filters active, or filters omitted nothing) the block MUST be entirely absent and the response byte-identical to current behavior.
- **FR-002**: The block MUST report `matched_before_filters` — the number of candidate tools that entered annotation filtering for this call (post-search, post-visibility, post-limit).
- **FR-003**: The block MUST report per-filter omission counts, where each omitted tool is attributed to exactly one active filter (fixed evaluation order: read-only → destructive → open-world), and the counts sum to the total number of omitted tools.
- **FR-004**: For each filter's omissions, the block MUST distinguish omissions caused by missing/unresolvable annotations from omissions caused by explicit annotation values, because their remediations differ.
- **FR-005**: The block MUST include a bounded list (at most 5, deduplicated, alphabetical) of server names owning omitted tools. Rationale for why this is not a leak: every tool in the candidate set already passed agent-scope, profile, and quarantine visibility for this same caller — the identical call without filters would return these tools in full. Tool names and schemas of omitted tools MUST NOT be included (counts and server names only), keeping the block small and the without-filter call the explicit way to inspect them.
- **FR-006**: The block MUST include exactly one short suggestion string, chosen by dominant cause: if any omissions stem from missing annotations, it advises checking/publishing upstream annotations (naming no specific vendor tooling) and mentions retrying without the filter for diagnosis; if all omissions are explicit, it states the tools are explicitly marked unsafe for the requested filter and offers retrying without the filter to inspect them.
- **FR-007**: The block MUST appear in both full and compact (`detail`) response modes with identical content. Existing response fields (`tools`, `total`, `notice`, `disabled`, `remediation`, `session_risk`, `debug`, `hint`) MUST be unchanged in presence, shape, and values.
- **FR-008**: Diagnostics MUST be computed from the candidate set already produced by the call (no additional index queries, no extra upstream calls, no per-call persistence).
- **FR-009**: The `retrieve_tools` tool description MUST be updated to mention the diagnostics block so agents know to read it, including the limit-window caveat (FR: counts describe this call's candidate window, not the whole catalog).
- **FR-010**: The code-execution routing surface, which exposes the same underlying search, MUST carry the same diagnostics under the same conditions.

### Key Entities

- **Filter diagnostics block**: `matched_before_filters` (count), per-filter omission entries (filter name → {missing-annotation count, explicit-annotation count}), `omitted_servers` (≤5 names), `suggestion` (one string). Appears only under FR-001's condition.
- **Candidate set**: the ranked, visibility-filtered, limit-capped tools that annotation filtering operates on today — unchanged by this feature; diagnostics only observe it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the reproduced field scenario (healthy upstream, matching tools, no annotations, `read_only_only=true`), the response alone is sufficient to determine: the tools exist, how many were withheld, that missing annotations are the cause, and what to do next — without consulting logs, other tools, or a second call.
- **SC-002**: For any call with no annotation filters active, and any filtered call where nothing is omitted, the response is byte-identical to the pre-feature response (verified by golden tests over representative full-mode and compact-mode calls).
- **SC-003**: The diagnostics block adds at most ~80 tokens to a response in the worst case (all three filters active, 5 servers listed) — small enough that enabling safety filters never meaningfully erodes the token savings that motivate using mcpproxy.
- **SC-004**: All existing `retrieve_tools` tests (annotation filtering, compact mode, TOON surface isolation, locked-tool discovery) pass unchanged, demonstrating the feature is purely additive.

## Out of Scope

Explicitly deferred, matching the issue author's own suggestion of a small first PR:

- **Upstream-scoped retrieval** (issue #969 item 2 — a `server` parameter or query syntax to scope discovery to a known upstream).
- **Required-tool / automation-preflight validation** (issue #969 item 3 — validating a pinned list of expected tool IDs with risk metadata).
- Any change to which tools the filters return — filter *semantics* are frozen; this feature only explains them.
- Web UI or CLI surfacing of these diagnostics (response-payload only for now).

## Assumptions

- Attribution to the *first* failing filter (fixed order) is preferred over multi-attribution because summable counts are easier for agents to reason about and keep the block compact; the order matches the existing evaluation order of the filter implementation.
- Server names of omitted tools are safe to disclose to the caller (justification in FR-005); tool names are withheld not for security but for token economy and to keep "retry without the filter" the canonical inspection path.
- The limit-window caveat (diagnostics describe the capped candidate set) is acceptable for the field scenario: the reporter's lost tools ranked within the window; a tool that ranks below the limit is a ranking concern, not a filter-legibility concern.
- No configuration knob is needed: the block is self-suppressing on the happy path, so there is nothing to turn off.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #969` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #969`, `Closes #969`, `Resolves #969` - These auto-close issues on merge

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
