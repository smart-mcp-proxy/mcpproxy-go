# Feature Specification: retrieve_tools Filter Diagnostics

**Feature Branch**: `094-filter-diagnostics`
**Created**: 2026-08-10
**Status**: Draft (revised after cross-model spec review round 1)
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

1. **Given** an upstream with callable tools that match "finance" but have no annotations, **When** the caller runs `retrieve_tools(query="finance", read_only_only=true)`, **Then** the response includes a `filter_diagnostics` block with `matched_before_filters` ≥ 1, a non-zero `missing_annotation` count under the `read_only_only` key, and a suggestion mentioning upstream annotations.
2. **Given** the same setup after the upstream publishes `readOnlyHint=true` on those tools, **When** the same query runs, **Then** the tools are returned normally and **no** `filter_diagnostics` block is present.
3. **Given** a query where filters omit nothing, **When** it runs with filters set, **Then** no `filter_diagnostics` block is present (zero noise when filters have no effect).

---

### User Story 2 - Operator distinguishes "fix upstream" from "filter working as intended" (Priority: P2)

An operator investigating why an automation lost a tool reads the diagnostics block and can tell whether omissions were caused by *missing* annotations (remediation: fix upstream metadata) or by *explicit* unsafe annotations (`readOnlyHint=false`, `destructiveHint=true`, `openWorldHint=true` — remediation: none; the filter is doing its job).

**Why this priority**: The two causes have opposite remediations; a bare count would still leave the operator guessing.

**Independent Test**: Two upstreams — one with unannotated tools, one with tools explicitly annotated `readOnlyHint=false`. A `read_only_only=true` query matching both yields a diagnostics block whose per-reason counts separate the two.

**Acceptance Scenarios**:

1. **Given** matched tools omitted for mixed reasons, **When** the query runs with `read_only_only=true`, **Then** the diagnostics separate omissions with missing annotations (`missing_annotation`) from omissions with explicitly unsafe annotations (`explicit`).
2. **Given** omissions caused only by explicit unsafe annotations, **When** the query runs, **Then** the suggestion text does NOT tell the operator to fix upstream annotations (it states the omitted tools are explicitly marked unsafe for the requested filter(s) and offers retrying without those filters to inspect them).

---

### User Story 3 - Zero results no longer reads as "discovery is broken" (Priority: P3)

When filters remove *every* match, the response (`tools: []`, `total: 0`) currently looks identical to "nothing exists". With diagnostics present, an agent or operator immediately sees that N tools matched and were all withheld by filters — the most confusing variant of the failure mode.

**Why this priority**: This is the highest-confusion case (it looks like an outage), but it is a special case of Story 1's mechanism.

**Independent Test**: A query whose every match is unannotated, with `read_only_only=true`, returns zero tools plus a diagnostics block accounting for all matches.

**Acceptance Scenarios**:

1. **Given** every query match lacks annotations, **When** `retrieve_tools(query=..., read_only_only=true)` runs, **Then** `total` is 0 and `filter_diagnostics.matched_before_filters` equals `filter_diagnostics.omitted_total`.
2. **Given** the same call, **Then** the existing locked-tools `notice` (which covers disabled/quarantined tools) is unchanged and, when both conditions apply, both signals appear without contradicting each other.

---

### Edge Cases

- **Multiple filters active, tool fails several**: each omitted tool is attributed to exactly one filter — the first that excluded it, in the filter implementation's existing evaluation order (read-only → destructive → open-world) — so per-filter counts always sum to `omitted_total` (no double counting).
- **Read-only implies non-destructive**: the existing filter semantics (a tool with explicit `readOnlyHint=true` passes `exclude_destructive` regardless of `destructiveHint`) must be reflected unchanged in the counts — diagnostics describe what the filter actually did, never a simplified model of it.
- **Candidate window, not the whole catalog**: annotation filters run on the *effective candidate window* — the ranked hits the search layer returned for this call (the search layer normalizes a non-positive `limit` to its default of 20), minus hits removed by scope/callability visibility, with no backfill after those removals. `matched_before_filters` is the size of that window. It can therefore exceed a caller-supplied `limit` of 0 or a negative value, and describes this call's window, not the index. The tool description must state this caveat.
- **Interplay with locked/quarantined tools**: disabled and out-of-scope hits are dropped by the visibility step *before* annotation filtering and never enter the candidate window. Disabled hits belong to the existing locked-tools flow (`notice`/`include_disabled`); out-of-scope hits remain entirely invisible (they are counted by neither mechanism — existing behavior, unchanged). Quarantined tools are normally absent from the search index entirely (their untrusted metadata is withheld) and are surfaced only by the separate name-only locked-entry pass, which also precedes annotation filtering. In the brief stale-index window after a runtime quarantine toggle, a quarantined tool may still appear as an index hit and thus be *counted* by these diagnostics; because the block contains counts only (no names, no servers, no schemas — see FR-005), this cannot leak quarantined-tool metadata. The two mechanisms are additive and must not double-report a tool.
- **Annotation lookup fails / server briefly unavailable**: a tool whose annotations cannot be resolved is treated as unannotated (matches current filter behavior); its omission is classified `missing_annotation`.
- **No filters set**: response must be byte-identical to today's response regardless of what would have been filtered (back-compat guarantee, cf. SC-002).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When at least one annotation filter (`read_only_only`, `exclude_destructive`, `exclude_open_world`) is active AND at least one tool in the candidate window was omitted by those filters, the `retrieve_tools` response MUST include a `filter_diagnostics` block. In all other cases (no filters active, or filters omitted nothing) the block MUST be entirely absent and the response byte-identical to current behavior.

- **FR-002**: The block MUST report `matched_before_filters` — the size of the effective candidate window that entered annotation filtering for this call, as defined in Edge Cases (post-search-normalization, post-visibility, no backfill).

- **FR-003**: The block MUST conform to this normative shape (field names and types exact; this example shows all three filters active and omitting tools):

  ```json
  "filter_diagnostics": {
    "matched_before_filters": 12,
    "omitted_total": 8,
    "omitted_by_filter": {
      "exclude_destructive": { "missing_annotation": 1, "explicit": 0 },
      "exclude_open_world":  { "missing_annotation": 0, "explicit": 1 },
      "read_only_only":      { "missing_annotation": 5, "explicit": 1 }
    },
    "suggestion": "…one string…"
  }
  ```

  Normative rules:
  - `omitted_by_filter` contains an entry **only** for each *active* filter that omitted ≥1 tool. Inactive filters never appear; active filters with zero omissions are omitted from the map. (The block itself only exists when `omitted_total` ≥ 1, per FR-001.)
  - Within an entry, both `missing_annotation` and `explicit` always appear (one may be 0; both integers ≥ 0).
  - `omitted_total` = sum of all `missing_annotation` + `explicit` across entries; `matched_before_filters` = `omitted_total` + the response's `total`.
  - Key order in `omitted_by_filter` follows the serializer's canonical map ordering (alphabetical); no ordering promise beyond determinism.
  - All values are counts and one string — the block MUST NOT contain tool names, server names, descriptions, or schemas.

- **FR-004**: Each omitted tool is attributed to exactly one filter (first-failure order per Edge Cases) and classified by the *decisive* annotation field of that filter:
  - `read_only_only`: annotations absent or `readOnlyHint` unset → `missing_annotation`; `readOnlyHint=false` → `explicit`.
  - `exclude_destructive`: annotations absent or `destructiveHint` unset → `missing_annotation`; `destructiveHint=true` → `explicit`. (A tool with explicit `readOnlyHint=true` is never omitted by this filter — existing shortcut preserved.)
  - `exclude_open_world`: annotations absent or `openWorldHint` unset → `missing_annotation`; `openWorldHint=true` → `explicit`.

- **FR-005**: The block MUST NOT identify the omitted tools or their servers in any way (counts and the suggestion string only). Rationale: (a) issue #969 item 1 asked for compact diagnostic counts, not identities; (b) counts-only makes the stale-quarantine-index edge case leak-proof (see Edge Cases); (c) it gives the block a fixed, bounded serialized size (SC-003). "Retry without the filter(s)" is the canonical way to inspect the omitted tools. (Naming affected servers was considered and deferred; if ever added, it requires authoritative quarantine/approval suppression first.)

- **FR-006**: The block MUST include exactly one `suggestion` string, selected by **cause precedence**: if the total `missing_annotation` count across entries is ≥ 1, the suggestion advises checking/publishing upstream tool annotations and mentions retrying without the specific active filter(s) — named literally, e.g. "retry without read_only_only" — for diagnosis. Otherwise (all omissions `explicit`), it states the omitted tools are explicitly marked unsafe for the named filter(s) and offers retrying without them to inspect. The suggestion MUST name only the filter(s) actually responsible for omissions, MUST consist of printable ASCII only, and MUST be ≤ 200 characters.

- **FR-007**: The block MUST appear in both full and compact (`detail`) response modes with identical content. **Every** existing response field and tool-entry value MUST remain unchanged in presence, shape, and value — including but not limited to `tools`, `query`, `total`, `usage_instructions`, `hint`, `notice`, `disabled`, `remediation`, `session_risk`, `debug`, `explanation`, and `usage_summary`.

- **FR-008**: Diagnostics MUST be computed from the candidate window already produced by the call (no additional index queries, no extra upstream calls, no per-call persistence).

- **FR-009**: There are three independently constructed `retrieve_tools` registrations: the default registration, the code-execution routing registration, and the call-tool routing registration. All three descriptions MUST be updated to mention the diagnostics block and the candidate-window caveat. Additionally, the default registration's schema MUST gain the three annotation-filter parameters (`read_only_only`, `exclude_destructive`, `exclude_open_world`) with descriptions matching the routing registrations — the handler already honors them there, but agents on the default surface currently cannot discover them, which is how the field report's confusion started.

- **FR-010**: The code-execution routing surface's `retrieve_tools` response (the `/mcp` endpoint in code-execution routing mode — not the output of the `code_execution` tool itself) MUST carry the same diagnostics under the same conditions. That surface is always full-mode; FR-007's mode-parity requirement applies to the default/call-tool surfaces.

### Key Entities

- **Filter diagnostics block**: as normatively defined in FR-003. Appears only under FR-001's condition.
- **Candidate window**: the ranked, visibility-filtered, search-capped tools that annotation filtering operates on today — unchanged by this feature; diagnostics only observe it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the reproduced field scenario (healthy upstream, matching tools, no annotations, `read_only_only=true`), the response alone is sufficient to determine: the tools exist, how many were withheld, that missing annotations are the cause, and what to do next — without consulting logs, other tools, or a second call.
- **SC-002**: For any call with no annotation filters active, and any filtered call where nothing is omitted, the response is byte-identical to the pre-feature response (verified by golden tests over representative full-mode and compact-mode calls).
- **SC-003**: The serialized (compact, no-whitespace) `filter_diagnostics` JSON object is at most **500 bytes** in the worst reachable case — enforced by a unit test that serializes the maximal reachable fixture: all three filters present, `matched_before_filters` = 100 (the window cap), `omitted_total` = 99 with per-filter counts satisfying the FR-003 sum invariant, and a 200-character printable-ASCII suggestion (FR-006 guarantees ASCII, so characters = bytes and JSON escaping adds nothing). The shape is fixed and the suggestion strings are compile-time constants, so this bound is exact, not statistical.
- **SC-004**: All existing `retrieve_tools` tests (annotation filtering, compact mode, TOON surface isolation, locked-tool discovery) pass unchanged, demonstrating the feature is purely additive.

## Out of Scope

Explicitly deferred, matching the issue author's own suggestion of a small first PR:

- **Upstream-scoped retrieval** (issue #969 item 2 — a `server` parameter or query syntax to scope discovery to a known upstream).
- **Required-tool / automation-preflight validation** (issue #969 item 3 — validating a pinned list of expected tool IDs with risk metadata).
- **Identifying omitted tools or servers in the diagnostics** (deferred per FR-005; would need authoritative quarantine/approval suppression).
- Any change to which tools the filters return — filter *semantics* are frozen; this feature only explains them.
- Web UI or CLI surfacing of these diagnostics (response-payload only for now).

## Assumptions

- Attribution to the *first* failing filter (fixed order) is preferred over multi-attribution because summable counts are easier for agents to reason about and keep the block compact; the order matches the existing evaluation order of the filter implementation.
- The candidate-window caveat (diagnostics describe the capped, visibility-filtered search window) is acceptable for the field scenario: the reporter's lost tools ranked within the window; a tool that ranks below the window is a ranking concern, not a filter-legibility concern.
- Adding the three filter parameters to the default registration's schema (FR-009) is discoverability repair, not scope creep: the parameters and their handler behavior already exist on every surface; only the default schema omits them.
- No configuration knob is needed: the block is self-suppressing on the happy path, so there is nothing to turn off.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #969` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #969`, `Closes #969`, `Resolves #969` - These auto-close issues on merge

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
