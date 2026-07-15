# Feature Specification: Compact Router — Progressive-Disclosure Tool Discovery

**Feature Branch**: `085-compact-router`
**Created**: 2026-07-14
**Status**: Draft
**Input**: User description: "Compact router: progressive-disclosure tool discovery. Compact-by-default retrieve_tools responses (signatures + first-sentence descriptions, lossy marker, never-elide-required-params) + describe_tool for full schemas on demand + self-healing InvalidParams errors embedding the failing tool's full schema. tool_response_mode: full (Phase 1 default) | compact (Phase 2 flip after profiler gates), per-call detail override, hot-reload. Ranking untouched by construction. Rejected: tool_reference/listChanged, two-stage server cards, TOON listings." — Full architecture rationale in [design.md](design.md) (judge-panel synthesis of 4 competing designs).

## Context & Motivation

Live profiling (spec 083 / July 2026, 907-tool deployment) established: each `retrieve_tools` call returns 15 full JSON schemas — median 8,640 tokens, max 54,865, 77% of it raw `inputSchema` that agents rarely read for flat tools; the proxy stops paying for itself after ~38 discovery calls per session. Community asks (users aolin480, armorer-labs, issue #175) converge on a small, stable, predictable router surface. The offline profiler measured compact signatures at −52.6% on the 45-tool reference corpus (−92% previously measured on live responses of the 907-tool deployment) at unchanged recall, dominating TSCG (−23.8%), TRON (+8.3% worse), and TOON (+23.9% worse).

The design (see [design.md](design.md)) keeps `retrieve_tools` as the entry point — no renames, no new search tool — and changes only response serialization: compact signatures by default, full schemas on demand via a new `describe_tool`, and self-healing errors that attach the full schema exactly when an agent's call fails validation. Retrieval ranking is untouched by construction, and the profiler hard-gates that claim.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Compact discovery responses (Priority: P1)

An agent searches for tools and receives, instead of 15 full JSON schemas, a ranked list of compact signatures — `id`, `score`, one-line signature with required/optional parameters and types, first sentence of the description, and a lossiness flag — plus a hint explaining how to get full schemas. For a typical query the response drops from thousands of tokens to hundreds, and for flat tools the agent can call the tool directly with zero extra round trips.

**Why this priority**: This is the headline token fix — the measured −52.6%…−92% lever at unchanged recall.

**Independent Test**: With compact mode on, run a golden-set query: response contains compact entries (no `inputSchema` field), required params marked, description = first sentence; the same query in full mode returns today's response byte-identically.

**Acceptance Scenarios**:

1. **Given** compact mode, **When** `retrieve_tools` returns results, **Then** each entry carries id, score, signature, first-sentence description, and lossy flag — and no full input schema.
2. **Given** a tool whose parameters are all scalars, **When** its signature is rendered, **Then** every required parameter appears marked as required with its type, every optional parameter with its type and default/enum when short — no required parameter NAME is ever omitted (hard invariant; complex required params keep name+marker and collapse internally per the lossy rule).
3. **Given** a tool with nested-object parameters, **When** its signature is rendered, **Then** the nested parameter is collapsed with an explicit lossy marker and the entry is flagged lossy, signaling "describe me before calling".
4. **Given** compact mode and the same query in full mode, **When** the ranked result IDs are compared, **Then** they are identical — serialization must not touch ranking.
5. **Given** an agent that wants full schemas anyway, **When** it passes the per-call detail override, **Then** the response matches full mode for that call only.

---

### User Story 2 - Full schemas on demand: describe_tool (Priority: P1)

An agent that selected a lossy-flagged tool (or wants certainty) calls `describe_tool` with up to 5 tool ids and receives the full JSON schema and complete description for each — the same definitions it would have gotten from today's `retrieve_tools`.

**Why this priority**: Progressive disclosure only works if the second stage exists; without it, compact mode would trade tokens for failed calls.

**Independent Test**: `describe_tool` with a valid id returns the full schema identical to the full-mode `retrieve_tools` entry; unknown ids produce per-id errors without failing the batch.

**Acceptance Scenarios**:

1. **Given** ids returned by `retrieve_tools`, **When** `describe_tool` is called, **Then** each returned definition contains the full input schema and untruncated description, **field-equal to the full-mode rendering of the same tool over `{name, description, inputSchema, server, annotations, call_with}`** — ranked-only fields (e.g. `score`) are absent, since `describe_tool` is a lookup, not a ranked search.
2. **Given** a mix of valid and unknown ids, **When** `describe_tool` is called, **Then** valid ids return definitions and unknown ids return per-id error entries; the call as a whole succeeds.
3. **Given** more than the maximum ids (5), **When** `describe_tool` is called, **Then** the call returns a clear error naming the limit.
4. **Given** the `retrieve_tools` routing mode, **Then** `describe_tool` is present beside `retrieve_tools`; its own definition costs ≤ ~150 tokens; quarantined/disabled/out-of-profile ids return per-id not-found errors, not definitions.

---

### User Story 3 - Self-healing failed calls (Priority: P2)

An agent calls a tool with arguments that fail upstream validation (it guessed from a lossy signature). The error response embeds that tool's full schema and a corrective hint, so the agent's next attempt succeeds — capping the cost of lossiness at one retry, with zero cost on the happy path.

**Why this priority**: This is the safety net that makes compact-by-default viable; it converts the worst case (opaque failure loop) into a bounded, self-correcting flow.

**Independent Test**: Call a tool with an invalid required-param omission through `call_tool_*` — the error content includes the tool's full schema and the standard error info.

**Acceptance Scenarios**:

1. **Given** a `call_tool_*` invocation rejected for invalid/missing parameters, **When** the error is rendered, **Then** it includes the failing tool's full input schema and a one-line hint, regardless of response mode.
2. **Given** a tool that fails for non-argument reasons (upstream down, timeout, auth), **When** the error is rendered, **Then** no schema is attached (schema only helps argument errors).
3. **Given** the same failing call in full mode, **Then** the error behavior is identical — self-healing is mode-independent.

---

### User Story 4 - Operator rollout control and stability (Priority: P2)

An operator ships Phase 1 with `tool_response_mode: "full"` (byte-identical behavior), later flips one config line to `compact` (or back) via the existing config reload/apply path, without restart. The proxy's tool menu keeps its existing names and count (one addition: `describe_tool`); agents' stored prompts keep working because `retrieve_tools` is unchanged as an entry point.

**Why this priority**: The community ask is stability and predictability; the rollout story is what makes the change safe to ship.

**Independent Test**: Toggle `tool_response_mode` values with a running proxy; observe response shape change on next call, no restart, no tool renames; default (unset) behaves as `full`.

**Acceptance Scenarios**:

1. **Given** no config (default `full`), **Then** `retrieve_tools` RESPONSE PAYLOADS are byte-identical to pre-feature behavior; the tools/list surface differs by exactly: one added tool (`describe_tool`), the added optional `detail` parameter on `retrieve_tools` (all existing parameters preserved unchanged), and the updated `call_tool_*`/`retrieve_tools` descriptions (FR-014) — asserted as such, not as global byte-identity.
2. **Given** a config flip to `compact` while running, **When** the change is applied through the existing config reload path, **Then** the next `retrieve_tools` call returns compact entries — no restart.
3. **Given** either mode, **Then** the built-in tool menu contains no renames and no removals relative to today; the only additions are `describe_tool` and the optional `detail` parameter on `retrieve_tools`; all existing `retrieve_tools` parameters (limit, include_stats, debug, read_only_only, exclude_destructive, include_disabled, …) are preserved unchanged.
4. **Given** signature rendering, **Then** signatures are compiled once per tool at index time (keyed by the existing tool-change hash) rather than per request, so compact responses do not add per-request latency.

---

### User Story 5 - Profiler gates for the default flip (Priority: P3)

A maintainer runs the spec-083 profiler against a compact-mode proxy and reads the gate metrics that decide the Phase-2 default flip: ranked-result identity between modes (must be 100%), discovery-response token reduction, lossy-signature rate across the corpus, and describe_tool usage statistics.

**Why this priority**: The flip to compact-by-default is an evidence decision, not a vibe decision; the gates were defined in the design doc.

**Independent Test**: Profiler live run against a compact-mode proxy emits the gate metrics in the report.

**Acceptance Scenarios**:

1. **Given** a live profiler run in each mode, **When** golden queries execute, **Then** the report asserts ranked-ID identity per query (any mismatch fails the gate) and reports per-mode response-token distributions.
2. **Given** the frozen corpus, **When** signatures are compiled, **Then** the report shows the lossy-signature rate (gate: <20%) and the heaviest signatures.
3. **Given** the E2E suite driving realistic tasks, **When** it runs in compact mode, **Then** describe_tool call counts are recorded (target <0.3 per completed task, informational until the flip decision).

---

### Edge Cases

- **Tools with no parameters**: signature renders as empty parens; never lossy.
- **Tools with stub/empty descriptions**: first-sentence extraction of an empty description is empty — entry renders with the id and signature only; flagged in the profiler's degenerate-description count (fix belongs to upstream hygiene, not this feature).
- **Enums/defaults too long to inline**: inline only when short (≤5 values); otherwise collapse into the type with the lossy marker.
- **Description first-sentence edge cases** (no period, markdown headers, code blocks, non-Latin scripts): extraction must be deterministic and verbatim-prefix — never paraphrase; fall back to a length-capped prefix when no sentence boundary exists.
- **`describe_tool` for a tool that disappeared** (server removed between search and describe): per-id not-found error with the standard remediation hint.
- **Agents that cached full-mode response shapes mid-session** when the operator flips modes: acceptable — each response is self-describing, and the per-call detail override covers agents that must have schemas.
- **Very large tool ids batches**: capped at 5 per call (matches search default k; prevents describe_tool from becoming a bulk-dump loophole).
- **Quarantined/disabled tools in describe_tool**: same visibility rules as `retrieve_tools` today — describe_tool must not leak definitions that search would not return.

## Requirements *(mandatory)*

### Functional Requirements

**Compact serialization (US1)**

- **FR-001**: The proxy MUST support `tool_response_mode` config (string: `full` default | `compact`), hot-reloadable, controlling only the serialization of `retrieve_tools` responses — never the query, ranking, or result set.
- **FR-002**: In compact mode, each `retrieve_tools` entry MUST contain: tool id, relevance score, compact signature, first-sentence description (verbatim prefix, deterministic extraction), and a lossy flag; it MUST NOT contain the full input schema.
- **FR-003**: Signatures MUST mark every required parameter as required with its type; required parameter NAMES must never be omitted from a signature (hard invariant, tested per-corpus) — a required parameter with a complex schema keeps its name and required marker while its internal structure collapses under FR-004's lossy marker. Optional parameters render with type and, when short, default/enum values.
- **FR-004**: Parameters whose schema cannot be fully represented in a signature (nested objects, long enums, complex constraints) MUST be collapsed with an explicit lossy marker, and the entry's lossy flag MUST be set.
- **FR-005**: An optional per-call `detail` parameter on `retrieve_tools` (`compact` | `full`; no default — when unset, the configured `tool_response_mode` applies) MUST override the configured mode for that call.
- **FR-006**: With `tool_response_mode: full` (or unset), `retrieve_tools` response payloads MUST be byte-identical to pre-feature behavior (menu-level changes are governed by FR-014, not this requirement).
- **FR-007**: Ranked result IDs and their order MUST be identical between modes for any query (serialization independence); this MUST be verifiable by an automated comparison.
- **FR-008**: Signatures MUST be compiled at index time into an in-memory cache keyed by the indexed per-tool hash (`ToolMetadata.Hash`, the same SHA-256 the Spec-032 pipeline computes; index rebuilds and tool-definition changes naturally invalidate entries), not per request and not persisted as new index fields in v1.
- **FR-009**: Compact responses MUST include a single short hint line explaining the lossy marker and `describe_tool`; the hint MUST be deterministic and count toward measured response size.

**describe_tool (US2)**

- **FR-010**: A new built-in tool `describe_tool` MUST accept 1–5 tool ids and return, per id, the full input schema and complete description — **field-equal to the full-mode rendering of the same tool over `{name, description, inputSchema, server, annotations, call_with}`; ranked-only fields (`score`) are absent** — or a per-id error for unknown/invisible ids without failing the batch.
- **FR-011**: `describe_tool` MUST be exposed in the `retrieve_tools` routing mode (the surface this feature changes); other routing modes keep their current surfaces in v1 (extending to code_execution mode is follow-up work). Resolution MUST apply the same visibility pipeline as search — profile scoping, authorization, callability, quarantine and disabled rules — before returning any definition: `describe_tool` must never return a definition that the same session's `retrieve_tools` could not return. Its own definition SHOULD cost ≤150 tokens.
- **FR-012**: `describe_tool` MUST work identically in both response modes.

**Self-healing errors (US3)**

- **FR-013**: The proxy MUST validate `call_tool_*` arguments against the target tool's stored input schema BEFORE dispatching upstream (pre-dispatch validation — new capability; today calls dispatch unvalidated), producing a typed invalid-params error on failure. That error — and best-effort-classified upstream invalid-params errors — MUST embed the failing tool's full input schema and a one-line corrective hint, in both response modes. Non-argument failures (transport, auth, timeout, upstream crash) MUST NOT attach schemas.
- **FR-013b**: Pre-dispatch validation MUST be fail-open for schema-system limitations: if the stored schema cannot be compiled or uses unsupported constructs, the call dispatches as today (validation skipped, counted in logs) — validation must never block a call a schemaless proxy would have allowed.

**Rollout & stability (US4)**

- **FR-014**: The built-in tool surface MUST NOT rename or remove any existing tool; the only addition is `describe_tool`. `call_tool_*` tool descriptions MUST be updated to reference signatures + `describe_tool` instead of instructing agents to read `inputSchema` from `retrieve_tools`.
- **FR-015**: Mode changes MUST propagate via the existing config hot-reload path and apply to the next call without restart; invalid values fail validation with a clear message.
- **FR-016**: Phase 1 ships with default `full`. The default flip to `compact` is a separate, one-line change gated by the FR-018 profiler metrics — it is NOT part of this feature's initial release.

**Measurement (US5)**

- **FR-017**: The spec-083 profiler MUST gain a compact-mode arm/flag that measures live compact responses with the same pipeline as full responses (token distributions, component breakdown, break-even).
- **FR-018**: The profiler MUST emit the flip-gate metrics: per-query ranked-ID identity across modes (gate: 100%), lossy-signature rate on the frozen corpus (gate: <20%), response-token reduction, and describe_tool usage counts from the E2E suite (informational).
- **FR-019**: Signature compilation MUST be deterministic: identical tool definition in, identical signature bytes out.

### Key Entities

- **Compact Signature**: deterministic one-line rendering of a tool's parameters (required marked, optionals typed, short enums/defaults inline, lossy collapse marker) + first-sentence description.
- **Response Mode**: `full` | `compact`, global config + per-call `detail` override; serialization-only.
- **describe_tool**: built-in second-stage tool; batch of ≤5 ids → full definitions with per-id errors.
- **Self-healing Error**: argument-validation error carrying the failing tool's full schema + hint.
- **Flip Gates**: profiler-emitted metrics (ranked-ID identity 100%, lossy rate <20%, token reduction, describe_tool usage) that authorize the Phase-2 default change.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the live reference deployment, median `retrieve_tools` response tokens in compact mode are at least 50% below full mode (profiler-measured; prior measurements: −52.6% offline 45-tool corpus, −92% live 907-tool deployment).
- **SC-002**: Ranked result IDs are 100% identical between modes across the full golden set (hard gate; any mismatch is a release blocker for the feature).
- **SC-003**: With default config, a byte-level regression suite shows zero differences in `retrieve_tools` response payloads vs pre-feature behavior, and the tools/list surface differs by exactly: one added tool, the added optional `detail` parameter on `retrieve_tools` (existing parameters preserved), and the FR-014 description updates.
- **SC-004**: Required parameters appear in 100% of compact signatures across the frozen corpus (never-elide invariant, automated check).
- **SC-005**: Lossy-signature rate on the frozen corpus is below 20%, reported per release.
- **SC-006**: An argument-validation failure followed by one schema-informed retry succeeds in the E2E self-healing test; the error path adds zero tokens to successful calls.
- **SC-007**: Mode toggle takes effect within one hot-reload cycle with no restart (E2E-verified), and the menu contains exactly one new tool.

## Assumptions

- The design-doc architecture (design.md) is authoritative for rationale and rejected alternatives (tool_reference/listChanged is Anthropic-API-only; two-stage server cards add a misrouting failure mode; TOON listings measured net-negative in spec 083).
- Signature grammar details (type abbreviations, marker characters) are plan-level; the spec's hard requirements are determinism, required-param completeness, and lossiness legibility.
- "First sentence" extraction is verbatim-prefix with a deterministic fallback; no LLM involvement.
- The existing tool-change hash (Spec 032 quarantine hashing) is a suitable compilation cache key; quarantine-driven changes already invalidate it.
- Spec 083's profiler (PR #851, branch 083-discovery-profiler) merges before the FR-017/FR-018 measurement work lands in CI. Concrete fixtures: golden set `specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json` (47 queries), frozen corpus `specs/083-discovery-profiler/datasets/corpus_v2.tools.json` (45 tools), profiler invocation `make bench-discovery` + live mode per that spec's quickstart; the E2E self-healing test uses one deterministic invalid-params fixture against a reference server.
- Family grouping / adaptive-k (design.md Phase 3) is out of this spec — a future feature gated by the confusion-gap metric.
- The current `retrieve_tools` response builder is monolithic (entry building interleaved with annotations, stats, notices); the plan MUST include an extraction step creating a tested full/compact entry-builder seam before compact mode lands — this is a refactor prerequisite, not optional cleanup.
- Env-var override naming follows the existing loader conventions (`MCPP_` viper prefix; explicit `MCPPROXY_*` aliases only where already established) — exact naming is plan-level.
- This spec intentionally spans config + serialization + describe_tool + pre-dispatch validation as one cohesive feature; tasks.md will phase them (validation and serialization are independently shippable increments) rather than splitting into separate specs.

## Out of Scope

- Flipping the default to compact (Phase 2 — separate one-line change after gates pass).
- Family grouping, adaptive top-k, server cards (design.md Phase 3+; rejected or deferred).
- tool_reference / defer_loading integration, listChanged dynamic registration (rejected — see design.md).
- TOON or other re-encodings of listings (measured net-negative; spec 084 covers results).
- Changes to BM25/ranking, index content, or search behavior (spec 086+ territory: dead clauses, description enrichment).
- code_execution and direct-mode surfaces.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(server): compact retrieve_tools responses + describe_tool

Related #175

Compact signatures with never-elide-required invariant; full schemas
on demand; self-healing InvalidParams errors.

## Changes
- Index-time signature compilation keyed by tool hash
- tool_response_mode config (full default) + per-call detail override

## Testing
- Byte-identity regression in full mode; ranked-ID identity across modes
```
