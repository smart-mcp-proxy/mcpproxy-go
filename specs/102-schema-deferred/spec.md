# Feature Specification: Schema-Deferred Direct Mode — Full Enumeration Without Schemas

**Feature Branch**: `102-schema-deferred`
**Created**: 2026-08-24
**Status**: Draft
**Input**: User description: "schema_deferred routing (issue #971): direct-mode tools/list keeps ALL tool names + descriptions but defers inputSchema; agents fetch full schemas on demand via describe_tool. Built as a thin composition over the Spec 085 compact-router machinery (compact signatures, describe_tool, pre-dispatch validation + self-healing errors) per the maintainer's accepted direction (issue #971 comment, 2026-08-13)."

**Related**: #971 (accepted-direction, 2026-08-13). Spec 085 (compact router — the reused machinery: signature cache, `describe_tool`, pre-dispatch validation, self-healing invalid-params errors). Specs 098/099 (preflight evaluator + `describe_tool` `check` mode — the full describe_tool contract travels wherever the tool is exposed). Industry precedent: Atlassian mcp-compressor "Low Compression" (94 tools: 17,600 → 3,900 tokens), Alibaba Qoder two-phase discovery, MCP client best-practices Catalog → Inspect → Execute.

## Context & Motivation

`retrieve_tools` mode has a blind-search gap: the agent sees only meta-tools and must guess a query to learn what exists. `direct` mode (the `/mcp/all` surface, or `/mcp` with `routing_mode: "direct"`) closes that gap by enumerating every upstream tool — but today it always ships full `inputSchema` (and `outputSchema`) per tool, ~30K tokens for a 100-tool fleet. Spec 083 profiling showed ~77% of tool payload is raw `inputSchema` that agents rarely read for flat tools.

Spec 085 already built schema-deferral — compact one-line signatures, `describe_tool` for full schemas on demand, pre-dispatch validation with the full schema embedded in invalid-params errors — but only on the `retrieve_tools` surface. This feature applies the same deferral axis to the *enumeration* (`tools/list`) surface of direct mode: every tool stays visible by name and description (with its precompiled compact signature appended), the schema is dropped from the listing, and `describe_tool` becomes available on the direct surface to recover it. Flat tools stay one-shot callable from the signature alone (zero extra round trips); the `~` lossy marker tells the agent when a `describe_tool` call is actually needed; a wrong guess costs exactly one self-healing retry.

Per the maintainer's accepted direction, this is **not a new routing mode**: `routing_mode` keeps its three values, and deferral is a serialization mode of the direct surface — the same axis (`tool_response_mode`) that already governs `retrieve_tools` serialization.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Deferred enumeration: see everything, pay for schemas only when needed (Priority: P1)

An agent connects to the direct surface of a proxy with deferred serialization enabled. `tools/list` returns every visible upstream tool — same `serverName__toolName` names, same annotations — but each entry carries the description plus its precompiled compact signature (`*` = required, `~` = lossy) instead of a full `inputSchema`; the declared input schema is a minimal permissive object. For a 100-tool fleet the listing drops from ~30K to roughly 3.5–5K tokens, and for flat (non-lossy) tools the agent can call directly from the signature with zero extra round trips.

**Why this priority**: This is the headline gap #971 names — full visibility at deferred-schema cost — and the entire token win of the feature.

**Independent Test**: Enable deferral on a fixture proxy with multiple upstream servers; fetch `tools/list` on the direct surface and assert: every tool present, no entry carries upstream schema properties, every entry's description ends with its compact signature, and total payload tokens are reduced ≥70% versus full mode on the frozen 45-tool reference corpus.

**Acceptance Scenarios**:

1. **Given** deferred serialization is active, **When** `tools/list` is served on the direct surface, **Then** every tool that full mode would list is listed — same names, same count, same annotations — and no entry carries upstream `inputSchema` properties or required lists.
2. **Given** a deferred entry, **Then** its declared input schema is the minimal permissive object (`{"type":"object"}` — never literal `{}` and never absent), so clients that drop arguments not covered by the declared schema still pass all arguments through.
3. **Given** a deferred entry, **Then** its description is the existing direct-mode description (`[server] …`) with the tool's compact signature appended, rendered by the Spec 085 signature rules (required markers never omitted, lossy collapse with `~`), served from the index-time signature cache — no per-request compilation.
4. **Given** a flat tool (signature not lossy), **When** the agent builds arguments from the signature alone and calls it, **Then** the call succeeds with zero additional discovery round trips.
5. **Given** deferral is NOT enabled (default), **Then** direct-surface `tools/list` payloads are unchanged from pre-feature behavior except the enumerated tool-surface delta of FR-010.

---

### User Story 2 - describe_tool on the direct surface (Priority: P1)

The same agent hits a `~`-marked (lossy) tool — say `github__create_issue` with nested objects. It calls `describe_tool` with the exact name it saw in `tools/list` and receives the full input schema and untruncated description, then makes the call correctly. Today `describe_tool` exists only on the `retrieve_tools`-mode surfaces — this is the gap #971 names explicitly.

**Why this priority**: Deferral without the second stage trades tokens for failed calls; the Inspect step of Catalog → Inspect → Execute must live on the same surface as the Catalog.

**Independent Test**: On the direct surface, call `describe_tool` with a direct (`server__tool`) name and assert the returned definition is field-equal to the Spec 085 full rendering; assert a tool outside the session's visibility returns a per-id not-found error.

**Acceptance Scenarios**:

1. **Given** the direct surface, **Then** `describe_tool` is present in `tools/list` alongside the upstream tools, in both serialization modes, with its existing contract (batch of ids → full schemas; per-id errors; Spec 099 `check` mode included).
2. **Given** a direct (`server__tool`) id from the listing, **When** `describe_tool` is called with it, **Then** it resolves using the same first-`__` parse as direct dispatch (describe and call can never diverge on ambiguous names) and returns the full definition; canonical `server:tool` ids are accepted equivalently.
3. **Given** an id whose tool the current session cannot see (out of agent-token scope, outside the pinned/URL profile, quarantined, or disabled), **When** `describe_tool` is called, **Then** that id returns a per-id `not_found` error — `describe_tool` never returns a definition that the same session's filtered `tools/list` would not include.
4. **Given** a server removed between `tools/list` and `describe_tool`, **Then** the stale id returns a per-id not-found error without failing the batch.

---

### User Story 3 - Self-healing direct calls when the guess was wrong (Priority: P2)

An agent calls a deferred tool with arguments guessed from a lossy signature and gets them wrong. Instead of an opaque upstream error, the proxy's pre-dispatch validation rejects the call before it leaves the proxy and embeds the tool's full input schema plus a one-line hint in the error, so the next attempt succeeds. The worst case of deferral is one bounded retry.

**Why this priority**: This is the safety net that makes schema-less enumeration viable — Spec 085 built it for `call_tool_*`; direct-mode dispatch currently dispatches unvalidated.

**Independent Test**: On the direct surface, call a tool omitting a required parameter; assert the error content embeds the tool's full input schema and hint, and that a transport-level failure for a healthy call attaches no schema.

**Acceptance Scenarios**:

1. **Given** a direct-mode call with invalid/missing arguments, **When** the error is rendered, **Then** it embeds the failing tool's full input schema and a one-line corrective hint — in both serialization modes (self-healing is mode-independent).
2. **Given** a call that fails for non-argument reasons (upstream down, auth, timeout), **Then** no schema is attached.
3. **Given** a stored schema that cannot be compiled or uses unsupported constructs, **Then** validation is fail-open: the call dispatches exactly as today (skipped, counted in logs) — validation never blocks a call a schemaless proxy would have allowed.
4. **Given** a client that cached a full-mode `tools/list` before the operator enabled deferral, **When** it calls with arguments valid against the real schema, **Then** the call succeeds — validation always runs against the stored upstream schema, never against the advertised permissive placeholder.

---

### User Story 4 - Operator opt-in, hot-reload, and cache-safe rollout (Priority: P2)

An operator flips deferred serialization on (or off) in config with the proxy running. The change applies without restart, the direct surface rebuilds, and connected clients receive a tools-list-changed notification so cached listings are refetched. Default behavior is unchanged: deferral is opt-in.

**Why this priority**: Clients cache `tools/list`; a serialization flip that stranded cached schemas (or required a restart) would make the feature unshippable for long-lived sessions.

**Independent Test**: With a client connected to the direct surface, flip the config value; assert a `notifications/tools/list_changed` is emitted, the next `tools/list` reflects the new serialization, and the tool *set* (names and count) is identical across the flip.

**Acceptance Scenarios**:

1. **Given** a running proxy, **When** the deferral setting changes via the existing config hot-reload path, **Then** the next direct-surface `tools/list` reflects it — no restart — and connected direct-surface sessions receive a tools-list-changed notification.
2. **Given** a mode flip, **Then** only serialization changes: the set of listed tools (including `describe_tool`) is identical in both modes, so a flip never adds or removes capabilities mid-session.
3. **Given** no config (defaults), **Then** deferral is off and behavior is governed by FR-010's byte-stability requirement.
4. **Given** an invalid value for the controlling setting, **Then** config validation fails with a clear message naming the accepted values, and `routing_mode: "schema_deferred"` is rejected with a message pointing at the supported composition (direct surface + deferred serialization).

---

### Edge Cases

- **Server or tool names containing `__`**: direct names split on the FIRST `__` (existing rule). `describe_tool` with direct ids MUST use the identical parse as dispatch so a name that is ambiguous resolves the same way for Inspect and Execute; canonical `server:tool` ids remain the unambiguous escape hatch.
- **Strict client-side validators**: clients that validate or prune arguments against the declared schema get `{"type":"object"}` (accepts any properties), not `{}` — some strict clients treat literal `{}`/missing schemas as "no arguments allowed" and drop everything.
- **Schema-driven client UIs** (inspector-style form rendering): a deferred listing renders an empty form. Documented, accepted consequence of an opt-in mode; the operator choosing deferral is optimizing for agents, not form UIs.
- **Tools with no parameters**: signature is empty parens, never lossy; permissive placeholder is harmless; one-shot callable.
- **Signature cache miss** (tool visible but its signature not yet compiled, e.g. mid-reindex): the entry MUST still be listed — name + description without the appended signature — never dropped and never blocking `tools/list` on compilation.
- **Clients that cached full-mode listings across an operator flip**: stale schemas remain *valid* knowledge (validation uses stored schemas — US3 scenario 4); stale compact listings degrade to describe-before-call. Both directions are safe; the listChanged notification (US4) bounds the staleness window.
- **Agent tokens / profiles**: the existing direct-surface discovery filter (token scope + profile) runs BEFORE serialization; deferral must not change which tools are listed, only how. Out-of-scope tools leak neither schemas nor names nor signatures, and `describe_tool` reports them as plain `not_found` (Spec 099 disclosure discipline).
- **Prompts and other non-tool surfaces**: untouched — this feature changes tool serialization only.
- **`retrieve_tools`-mode and code-execution surfaces**: untouched. The code-execution surface still has no `describe_tool` and still serializes full (Spec 085 rule); extending deferral there stays follow-up work.

## Requirements *(mandatory)*

### Functional Requirements

**Config surface & mode resolution**

- **FR-001**: Deferred serialization of the direct enumeration surface MUST be opt-in and default-off, hot-reloadable through the existing config reload path, and MUST NOT introduce a new `routing_mode` value. Controlling setting: [NEEDS CLARIFICATION: one-axis vs dedicated key — does the existing global `tool_response_mode: "compact"` extend to govern the direct surface too (zero new config, matches the maintainer's "honor the schema-deferral axis", but silently changes `/mcp/all` output for deployments that already run compact), or does the direct surface get its own key (e.g. `direct_tool_response_mode: "full" | "deferred"`, default `full`), keeping existing compact deployments byte-stable at the cost of one more config field? The spec below is written against "the deferral setting" and works under either resolution.]
- **FR-002**: `routing_mode` validation MUST continue to accept exactly `retrieve_tools`, `direct`, `code_execution`; the value `schema_deferred` MUST be rejected with an error message that names the supported composition, so users arriving from #971's proposed config get a self-explaining failure.
- **FR-003**: The deferral setting MUST govern every serving of the direct surface — `/mcp` when `routing_mode: "direct"`, the always-available `/mcp/all` endpoint, and their profile-scoped `/mcp/p/<slug>` equivalents — identically; there is no per-endpoint divergence.

**Deferred serialization (US1)**

- **FR-004**: With deferral active, each direct-surface `tools/list` entry MUST carry: the unchanged `serverName__toolName` name; the existing description with the tool's compact signature appended (Spec 085 rendering rules: every required parameter named and marked, lossy collapse with explicit `~` marker); unchanged annotations; and a minimal permissive input schema (`{"type":"object"}` — never literal `{}`, never absent). It MUST NOT carry the upstream tool's schema properties or required list.
- **FR-005**: Signatures MUST be served from the Spec 085 index-time signature cache (keyed by the per-tool hash) — no per-request compilation. On a cache miss the entry is listed without a signature rather than dropped or delayed.
- **FR-006**: Output schemas: [NEEDS CLARIFICATION: does deferral also strip upstream `outputSchema` from deferred entries (it is part of the token cost, and stripping is protocol-safe since structured content is allowed without a declared schema)? If stripped, must `describe_tool` gain an `output_schema` field in its definitions so the information stays reachable — an additive change to a response shape that Spec 099 froze byte-for-byte on existing surfaces?]
- **FR-007**: The deferral convention (signature syntax, `*`/`~` markers, "call describe_tool for full schemas") MUST be explained in-band on the direct surface — via the MCP server instructions and/or the `describe_tool` description — deterministically, without adding a synthetic pseudo-tool to the listing.
- **FR-008**: Deferral MUST change serialization only: the set of tools listed (identity, count, visibility filtering, ordering source) MUST be identical between full and deferred serialization for the same session, verifiable by automated comparison.

**describe_tool on the direct surface (US2)**

- **FR-009**: `describe_tool` MUST be exposed on the direct surface with its full existing contract (batch ids → full definitions; per-id errors that never fail the batch; Spec 099 `check` mode and its caps). It MUST be present in BOTH serialization modes, so a mode flip never changes the tool set. Batch cap for definition fetches on this surface: [NEEDS CLARIFICATION: keep the existing 5-id cap, or raise it (e.g. 20) for the deferred surface — where an agent that just saw the whole catalog may legitimately want schemas for its entire multi-tool plan in one call, but a large cap re-opens the bulk-dump loophole the 5-id cap was chosen to close?]
- **FR-010**: This is a deliberate change to the frozen built-in tool surface: exactly one addition (`describe_tool` on the direct surface), no renames, no removals, no changes to other surfaces. The three tool-surface golden baselines MUST be regenerated/updated to encode exactly this delta, and the byte-stability requirement of US1 scenario 5 is asserted modulo this enumerated delta only.
- **FR-011**: `describe_tool` on the direct surface MUST accept both canonical `server:tool` ids and direct `server__tool` names; direct names MUST resolve via the same first-`__` parse as direct dispatch. Resolution MUST apply the same visibility pipeline as the direct-surface listing filter (agent-token scope, pinned/URL profile, quarantine, disabled): it must never return a definition, signature, or existence signal for a tool the same session's `tools/list` would not include — invisible ids report plain `not_found`.

**Pre-dispatch validation & self-healing (US3)**

- **FR-012**: Direct-mode dispatch MUST validate arguments against the tool's stored upstream schema before dispatching (extending the Spec 085 validator to the direct path), in both serialization modes. Validation failures produce the typed invalid-params error embedding the failing tool's full input schema and a one-line hint; non-argument failures attach no schema; validation is fail-open for uncompilable/unsupported schemas exactly per Spec 085 FR-013b.
- **FR-013**: Validation MUST always use the stored upstream schema — never the advertised permissive placeholder — so calls from clients holding stale full-mode listings validate correctly.

**Rollout & client compatibility (US4)**

- **FR-014**: A change to the deferral setting MUST rebuild the direct surface via the existing hot-reload path (no restart) and MUST emit `notifications/tools/list_changed` to connected direct-surface sessions, so caching clients refetch.
- **FR-015**: With deferral off (the default), direct-surface `tools/list` payloads MUST be byte-identical to pre-feature behavior except the FR-010 enumerated delta.
- **FR-016**: Existing filtering MUST be unaffected: agent-token scope and profile filters run before serialization; deferred serialization must not alter which tools any session can list, describe, or call.

### Key Entities

- **Deferred tool entry**: the direct-surface `tools/list` rendering of one upstream tool under deferral — name, description + appended signature, annotations, permissive placeholder schema. Derived at rebuild time from the same discovery + filter pipeline as full mode.
- **Compact signature cache** (existing, Spec 085): index-time compiled one-line signatures keyed by per-tool hash; this feature adds the direct surface as a second consumer.
- **Serialization mode resolution**: the config axis (per FR-001 resolution) mapping to `full | deferred` for the direct surface; hot-reloadable; never affects tool-set membership.
- **Tool id forms**: canonical `server:tool` (MCP discovery contract) and direct `server__tool` (direct-surface display/dispatch name); `describe_tool` on the direct surface must honor both without diverging from dispatch parsing.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the frozen 45-tool reference corpus, deferred direct-surface `tools/list` payload is ≥70% smaller in tokens than full mode, measured by the spec-083 profiler pipeline; on a ~100-tool fleet the projection matches the #971 estimate (~30K → ~3.5–5K tokens, ≥85% reduction).
- **SC-002**: ≥80% of corpus tools (the non-lossy share; Spec 085 lossy-rate gate <20%) are callable one-shot from the deferred listing — zero `describe_tool` round trips — verified against the corpus signature set.
- **SC-003**: An agent whose guessed arguments fail validation completes the call in exactly one retry using only the error-embedded schema (no additional discovery calls), demonstrated in E2E.
- **SC-004**: With deferral off, automated comparison shows direct-surface responses byte-identical to pre-feature behavior modulo the single enumerated surface delta (FR-010); the three golden suites pass with regenerated baselines encoding exactly that delta.
- **SC-005**: Scoped sessions (agent token or profile) list, describe, and call exactly the same tool set before and after enabling deferral — zero visibility widening, asserted by the existing scope/profile integration suites extended to deferred mode.
- **SC-006**: A serialization flip on a live proxy propagates to connected clients (notification observed, next listing reflects the mode) with zero restarts and zero tool-set changes.

## Non-Goals

- No new `routing_mode` value, endpoint, or execution primitive; no changes to `retrieve_tools`-mode or code-execution surfaces (code-execution deferral remains Spec 085 follow-up).
- No pagination, ranking, or filtering changes to the direct listing — serialization only.
- No default flip: deferral ships opt-in; any future default change is a separate, evidence-gated decision (Spec 085 discipline).
- No per-call detail override on the enumeration surface (`tools/list` takes no parameters in MCP); the schema escape hatch is `describe_tool`, the mode escape hatch is config.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #971` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #971`, `Closes #971`, `Resolves #971` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.
