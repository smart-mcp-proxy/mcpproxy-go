# Feature Specification: Adaptive TOON Output for Tool Results

**Feature Branch**: `084-toon-output`
**Created**: 2026-07-14
**Status**: Draft
**Input**: User description: "TOON output for mcpproxy tool results (opt-in, measured): config-switchable TOON serialization for tool-call RESULT payloads (call_tool variants), adaptive — encode only when payload is tabular-uniform AND TOON is smaller than compact JSON by a threshold (default 15%), else passthrough. toon_output: off (default) | adaptive | always; per-server override; hot-reload. Encoding marker + decode hint in response. retrieve_tools listings OUT OF SCOPE (measured net-negative in spec 083). TOON before truncation; sensitive-data detection scans ORIGINAL JSON."

## Context & Motivation

Spec 083's profiler produced the measurements that shape this feature:

- TOON on **tool listings**: +23.9% *worse* than compact JSON (measured on corpus_v2) — consistent with TOON's own spec, which concedes deeply-nested/non-uniform structures favor JSON. Listings are therefore **out of scope**.
- TOON on **mixed result fixtures**: only −2.2% — because the fixture mix is mostly non-tabular.
- TOON's documented winning regime — large uniform tabular arrays (lists of rows) — is real (~30–60% in TOON's benchmarks) but occurs only in a *subset* of tool results (database queries, list endpoints, log/message dumps).

Conclusion: a blanket "TOON everything" switch would burn tokens on most responses to win on a few. The honest production feature is **adaptive**: per-response, encode to TOON only when the payload is tabular-uniform *and* the encoding actually wins by a configurable margin; otherwise pass through unchanged. Off by default; every decision measurable by the profiler.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Adaptive TOON on tabular results (Priority: P1)

An agent calls an upstream tool through mcpproxy (via any `call_tool_*` intent variant) that returns a large uniform JSON array (e.g., 200 database rows). With `toon_output: "adaptive"` enabled, the agent receives the result as TOON — with an explicit encoding marker and one-line decode hint — and pays measurably fewer tokens. A different tool returning a deeply nested object passes through as JSON, byte-identical to today.

**Why this priority**: This is the entire feature: savings where TOON wins, zero regression where it loses.

**Independent Test**: With adaptive mode on, call a tool returning a uniform 100-row array → response is TOON-encoded with marker, smaller than the JSON rendering by at least the threshold; call a tool returning a nested object → response identical to `toon_output: "off"`.

**Acceptance Scenarios**:

1. **Given** adaptive mode and a tool result that is a uniform array of flat objects, **When** the complete encoded emission (marker + hint + TOON body) is smaller than the exact passthrough emission by at least the configured threshold (default 15%), **Then** the agent receives the TOON encoding, preceded by the marker and one-line decode hint.
2. **Given** adaptive mode and a tool result that is non-tabular (nested, non-uniform, scalar, or non-JSON text), **When** the response is rendered, **Then** it is byte-identical to the response with the feature off.
3. **Given** adaptive mode and a tabular result whose TOON encoding wins by less than the threshold, **When** the response is rendered, **Then** it passes through unchanged (near-ties are not worth the agent-side decode risk).
4. **Given** adaptive mode, **Then** by construction no emitted block is ever larger than its exact passthrough emission, marker included (the encoder compares complete emissions before choosing).

---

### User Story 2 - Operator control: off / adaptive / always, per-server (Priority: P1)

An operator enables the feature globally with one config line (`toon_output: "adaptive"`), disables it for one incompatible server via a per-server override, and reverts everything with a hot-reload — no restart, no client reconfiguration.

**Why this priority**: Off-by-default and instant rollback are the safety story; per-server override handles agents/servers that can't tolerate non-JSON results.

**Independent Test**: Toggle config values and observe response encoding change on the next call without restart.

**Acceptance Scenarios**:

1. **Given** no config (default), **Then** all responses are byte-identical to pre-feature behavior (`off`).
2. **Given** global `adaptive` and a per-server override `off`, **When** calling tools on that server, **Then** its responses always pass through, while other servers' tabular results encode.
3. **Given** a config edit while running, **When** the file watcher reloads, **Then** the new mode applies to subsequent calls without restart.
4. **Given** `always` mode (benchmarking only, documented as such), **Then** every JSON-parseable result is TOON-encoded regardless of size comparison, and the config documentation warns this can increase token cost.

---

### User Story 3 - Safety-chain ordering: detection and truncation unaffected (Priority: P2)

A security-conscious operator confirms that enabling TOON changes neither what the sensitive-data scanner finds nor how truncation behaves: sanitisation runs on the raw pre-encoding result, detection receives an equivalent pre-encoding rendering, and the observable guarantee is identical detection FINDINGS either way (a secret is caught identically; incidental byte differences such as timestamped truncation banners carry no upstream data). Truncation applies after encoding so a truncated TOON payload is explicitly marked as truncated.

**Why this priority**: Encoding must never weaken the security pipeline; this is a hard invariant, but it's P2 because it's a property of the implementation rather than new user-facing behavior.

**Independent Test**: Feed a fixture result containing a known detectable secret through a tool call with TOON on and off — identical detection events; feed an oversized tabular result with a small `tool_response_limit` — response is marked truncated and the truncation notice is intact.

**Acceptance Scenarios**:

1. **Given** a tool result containing sensitive data, **When** TOON encoding is active, **Then** sensitive-data detection produces the same FINDINGS as with the feature off (each pipeline stage receives an equivalent pre-encoding rendering per FR-007; finding-set parity is the tested guarantee).
2. **Given** a result exceeding the configured response limit, **When** it is TOON-encoded, **Then** the size limit is applied to the final rendered payload and the standard truncation notice is present (encode first, then truncate).
3. **Given** any encoding failure (encoder error, unparseable JSON), **Then** the response falls back to passthrough — a TOON bug can never lose result data.

---

### User Story 4 - Measured by the profiler (Priority: P2)

A maintainer runs the spec-083 profiler's results arm against the adaptive encoder (not just against raw TOON) and the report shows: savings on the tabular subset, zero delta on the non-tabular subset, and the decision statistics (how many payloads encoded vs passed through, and why).

**Why this priority**: "Measured" was the design mandate; the profiler is how this feature proves or disproves itself in every release.

**Independent Test**: Run the profiler's results arm with the adaptive encoder on the result-fixtures corpus; report contains per-class savings and decision counts.

**Acceptance Scenarios**:

1. **Given** the result-fixtures corpus (spec 083), **When** the profiler runs the adaptive-encoder arm, **Then** the report shows: savings ≥ the configured byte threshold on the ENCODED subset (token savings reported alongside as the informational metric), an informational savings figure across all tabular-classified fixtures, byte-identical passthrough on every non-tabular fixture, and per-payload decisions (encoded / passthrough-not-tabular / passthrough-below-threshold).

---

### Edge Cases

- **Non-JSON results** (plain text, base64, binary content blocks): always passthrough; the classifier only considers JSON-parseable text content.
- **Mixed content responses** (multiple content blocks): each text block is evaluated independently; image/audio blocks always untouched.
- **Tiny tabular arrays** (2–3 rows): TOON's header overhead usually loses — the size comparison handles this naturally; no special-casing.
- **Arrays of *almost*-uniform objects** (90% same keys): classifier must treat "uniform enough" conservatively; when TOON's encoding of ragged rows inflates, the size comparison is the backstop.
- **Streaming/SSE partial results**: encoding applies only to complete tool results, never to partial frames.
- **Agents that echo results back into tool args**: decode hint tells the agent the payload is TOON; args remain JSON (input parsing is out of scope) — the hint must say "decode before reuse".
- **Truncation cutting a TOON table mid-row**: acceptable (same as JSON truncation today) because the truncation notice marks the payload incomplete; the marker + hint remain at the head of the payload.
- **`always` mode on non-JSON content**: passthrough with no marker (nothing was encoded).
- **Direct-mode and code-execution surfaces**: never encoded (FR-014); regression tests assert byte-identical behavior there with every mode value.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The proxy MUST support a top-level config field `toon_output` (string: `off` default | `adaptive` | `always`) and `toon_min_savings_pct` (integer, default 15, validated 1-90), plus an optional per-server `ServerConfig` field of the same name whose non-empty value overrides the global for that server's tools (precedence: per-server > global > default; invalid values fail config validation with a clear message). Changes to any of these MUST propagate via the existing config hot-reload path and apply to the next tool call without restart.
- **FR-002**: With `toon_output: off` (or unset), all tool-call responses MUST be byte-identical to pre-feature behavior.
- **FR-003**: In `adaptive` mode, a tool-result text block is TOON-encoded iff (a) it parses as JSON, (b) it classifies as tabular-uniform under these v1 rules: a JSON array of at least 4 objects; every value scalar or null (no nested objects/arrays in v1); the union key set derives from row keys where each key is present in at least 90% of rows; an object with exactly one key whose value is such an array ("envelope") also qualifies; empty arrays and arrays of non-objects do not qualify; key order is irrelevant; and (c) the complete encoded emission — marker + decode hint + TOON body — is smaller than the exact passthrough emission of the same block by at least the configured threshold (`toon_min_savings_pct`, default 15, integer percent, validated range 1-90; measured in bytes with a documented note that byte savings approximate token savings for this payload class). "Passthrough emission" means the exact text block the agent would receive with the feature off, not a re-rendered compact form.
- **FR-004**: In `adaptive` mode, no response may ever be larger than its passthrough rendering (enforced by the size comparison, not by policy).
- **FR-005**: Every TOON-encoded block MUST be preceded by a deterministic one-line marker identifying the encoding and giving a one-line decode hint (including that tool arguments must remain JSON); passthrough responses carry no marker.
- **FR-006**: Encoding failures of any kind MUST fall back to passthrough; the failure MUST be logged and counted (observable via existing logging/metrics), and MUST never surface as a tool-call error.
- **FR-007**: Security-pipeline parity, split by stage: (a) output sanitisation (redact/block modes) MUST run on the raw upstream result BEFORE encoding — the encoder's input is the sanitised result; (b) the activity-pipeline sensitive-data detection MUST receive the pre-encoding text rendering of the block (not the TOON encoding) as its scan input, so enabling TOON produces identical detection findings for identical inputs on both stages.
- **FR-008**: Response-size truncation MUST apply to the final rendered payload (after encoding); the standard truncation notice MUST survive encoding, and the marker/hint MUST NOT be truncated away (they precede the payload). When the configured response limit is too small to hold marker + hint + truncation notice + at least one data row, adaptive mode MUST pass through (truncation then behaves exactly as today). Truncation-notice behavior differences that already exist between render paths are out of scope — only the `call_tool_*` truncator's behavior is specified here.
- **FR-009**: `always` mode MUST TOON-encode every JSON-parseable text block regardless of the size comparison (benchmark/debug use; documented warning that it can increase cost), still honoring FR-005–FR-008 — including FR-008's too-small-limit rule, which takes precedence in every mode: when marker + hint + truncation notice + one data row cannot fit the configured limit, the block passes through.
- **FR-010**: The encoding decision (encoded / passthrough-not-tabular / passthrough-below-threshold / passthrough-error) MUST be recorded in the metadata of the `tool_call` activity record (the record type operators see by default), keyed per text-block index for multi-block responses, with before/after byte sizes when encoded.
- **FR-010b**: Output-schema validation of structured content MUST continue to evaluate the ORIGINAL structured result; TOON encoding applies only to the text-block rendering and MUST NOT affect structured-content validation outcomes.
- **FR-011**: The classifier and encoder MUST be deterministic: identical input produces an identical decision and identical encoded bytes.
- **FR-012**: The spec-083 profiler's results arm MUST gain an adaptive-encoder variant that exercises this exact production code path over the result-fixtures corpus, reporting per-class savings and decision counts.
- **FR-013**: `retrieve_tools` responses and tool listings MUST NOT be TOON-encoded by this feature (measured net-negative; out of scope).
- **FR-014**: The feature applies to exactly one surface: the text-block rendering of `call_tool_read`/`call_tool_write`/`call_tool_destructive` responses. It MUST NOT apply to: code-execution paths (nested `call_tool()` returns feed agent-written programs that expect JSON objects, and the final execution wrapper is a structured envelope), direct-mode server tools (that surface's contract is unmodified upstream passthrough), `retrieve_tools`, or any listing. Non-application on these surfaces is covered by explicit tests.

### Key Entities

- **Encoding Mode**: `off` | `adaptive` | `always`, global + per-server override; hot-reloadable.
- **Tabular Classifier**: deterministic predicate over parsed JSON (uniform array of objects, optional single-key envelope); produces a classification used in the decision record.
- **Encoding Decision**: per-call record — mode, classification, size comparison (bytes before/after, threshold), outcome; feeds the activity log and profiler.
- **Encoding Marker**: deterministic one-line header naming the encoding + decode hint; part of the response contract with agents.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the spec-083 result-fixtures corpus, the profiler reports three separated metrics: (a) savings on ENCODED fixtures ≥ the configured threshold (holds by construction of the size comparison), (b) savings across ALL tabular-classified fixtures (informational — passthrough-below-threshold rows dilute it), and (c) byte-identical passthrough on every non-tabular fixture (asserted, not approximated). Verified per release.
- **SC-002**: With the feature off or unset, a byte-level comparison of tool-call responses before/after deployment shows zero differences (regression suite).
- **SC-003**: In adaptive mode, zero responses across the full test corpus are larger than their passthrough rendering.
- **SC-004**: Sensitive-data detection findings are identical with the feature on and off across the security test corpus.
- **SC-005**: An operator can enable, per-server-disable, and fully revert the feature via config edits alone, each taking effect within one hot-reload cycle (no restarts, verified in E2E).
- **SC-006**: Every TOON-encoded response in the E2E suite carries the marker + decode hint; every passthrough response carries none.

## Assumptions

- Byte-size comparison is an acceptable proxy for token-size comparison for the tabular payload class (both encodings are ASCII-dominant; the threshold default of 15% absorbs tokenizer variance). The profiler reports true token deltas, so any systematic divergence becomes visible (FR-012).
- The marker + decode-hint format is a contract with agents; it is documented in the tool descriptions of the `call_tool_*` variants so agents learn it in-session (exact wording is a plan-level decision; the spec requires determinism and a one-line footprint).
- Per-server override lives alongside existing per-server settings (same mechanism as other per-server flags, e.g. `auto_approve_tool_changes`).
- The classifier's v1 rules (FR-003b) are deliberately conservative — flat scalar rows only; extending to nested values is future work gated by profiler measurement. The FR-004 never-larger invariant holds regardless of classifier quality.
- `always` mode exists for benchmarking/debugging and is documented as potentially cost-increasing; it is not a recommended production mode.
- Spec 083's profiler and result-fixtures corpus are merged (PR #851) before this feature's measurement gate (FR-012/SC-001) runs in CI.

## Out of Scope

- TOON encoding of `retrieve_tools` responses or any tool listing (measured net-negative in spec 083).
- TOON parsing of tool-call arguments (inputs remain JSON).
- TOON for MCP protocol envelopes, notifications, or non-tool payloads.
- Changing any default behavior (`off` is the default).
- Client-side/agent-side decoding helpers.

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
feat(server): adaptive TOON encoding for tool results

Related #175

Adds toon_output config (off/adaptive/always) with per-server override.
Adaptive mode encodes tabular-uniform results when TOON wins by >=15%.

## Changes
- Tabular classifier + size-compared encoder with passthrough fallback
- Marker + decode hint on encoded responses

## Testing
- Unit tests for classifier/encoder determinism and never-larger invariant
- E2E: hot-reload mode switching; detection-parity fixtures
```
