# Feature Specification: `describe_tool` Check Mode — In-Band Preflight

**Feature Branch**: `099-describe-check-mode`
**Created**: 2026-08-16
**Status**: Draft
**Input**: User description: "Phase-2 in-band preflight surface for issue #969 item 3: an optional `check` mode on the existing `describe_tool` built-in that returns verdict-only availability results from the spec-098 evaluator, so an agent can gate its own workflow without leaving the MCP session. Reporter-endorsed shape (issue #969 comment, 2026-08-13): 'a check mode on an existing built-in rather than a new top-level MCP tool', same evaluator, same reason codes."

**Related**: #969 (item 3, Phase 2). Item 1 (`filter_diagnostics`) shipped in spec 094 / v0.55.0. Phase 1 (shared evaluator + `POST /api/v1/preflight` + `mcpproxy tools preflight`) is spec 098. This spec adds the in-band MCP surface listed as the first non-goal of 098, and closes one pre-existing describe_tool differential the 098 evaluator exposed.

**Base**: branched from `098-tools-preflight` (merge commit `b8e53210`). If 098 lands on `main` before this feature starts, rebase onto `main` — nothing here depends on 098 being an unmerged branch, only on its shipped evaluator (`internal/preflight`), its 15-code enum, and its committed sabotage matrix.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent gates its own multi-step plan before spending a turn on it (Priority: P1)

An agent has drawn up a plan that will call eight tools across three upstream servers. Before executing step 1 — and before burning turns discovering the failure one tool call at a time — it sends one `describe_tool` call with `check: true` and the eight ids. It gets back one verdict per id: `ready`, or exactly one reason code with a retryable flag and an action. If everything is ready it proceeds. If `slack:post_message` reports `server_quarantined`, it stops and tells the user what to approve, in the same turn, instead of failing eight steps later with an opaque error.

**Why this priority**: This is the reporter's endorsed Phase-2 shape and the only preflight path available to a running agent — spec 098's REST/CLI surfaces require a harness outside the session. Without it, an agent inside an MCP session has no way to distinguish "this tool is quarantined" from "my search query was bad", which is exactly the ambiguity issue #969 opened about.

**Independent Test**: Against a fixture proxy with one quarantined server, one disabled tool and one misspelled id, issue a single `describe_tool` call with `check: true` over MCP and assert one verdict per id with the exact reason codes — no REST endpoint, no CLI, no other feature involved.

**Acceptance Scenarios**:

1. **Given** all requested tools are indexed, approved, on enabled healthy in-scope servers, **When** the agent calls `describe_tool` with `check: true`, **Then** the response carries `verdict: "ready"` and one `status: "ready"` result per id, with no schemas and no upstream I/O.
2. **Given** one requested tool sits on a quarantined server, **When** the check runs, **Then** that id reports `reason: "server_quarantined"`, `retryable: false`, `action: "approve"`, and the set-level `verdict` is `blocked`.
3. **Given** one requested server is still connecting or indexing, **When** the check runs, **Then** its tools report `server_initializing` with `retryable: true` and the set verdict is `degraded_retryable`, so the agent knows to wait rather than to escalate to the user.
4. **Given** a misspelled id, **When** the check runs, **Then** that id reports `not_found` with a scope-filtered `did_you_mean` list, and the other ids still report their own verdicts (one bad id never fails the batch).
5. **Given** 50 ids, **When** the check runs, **Then** all 50 are evaluated in one call; **Given** 51 ids, **Then** the call fails with a single error naming the 50-id cap and evaluates nothing.

---

### User Story 2 - The same session's plain `describe_tool` keeps behaving exactly as before (Priority: P1)

An agent (or an MCP client, or a stored prompt) that never sends `check` sees the same 1–5 id batch cap, the same `definitions` + `errors` payload, the same wording, and the same per-id error codes — with **one deliberate exception**: an out-of-scope id now reports `not_found` where it used to report `invisible` (FR-011). That single change is a disclosure fix, not a refactor, and it is the only thing an existing integration could notice.

**Why this priority**: `describe_tool` is on the hot path of the compact router (spec 085) — it is how agents recover a full schema after a lossy signature. A regression here degrades every compact-mode session. Byte-identity is the spec-094 discipline this repo applies to every MCP-surface change and is the cheapest possible guarantee to test.

**Independent Test**: Replay the spec-085 describe_tool fixture corpus without `check` against the feature branch and diff every response against the pre-099 release byte-for-byte; exactly one enumerated delta is permitted (FR-011).

**Acceptance Scenarios**:

1. **Given** any request that omits `check` (or sends `check: false`), **When** `describe_tool` runs, **Then** the response is byte-identical to the pre-099 release, with the single enumerated exception in FR-011, which ships with a release note (FR-011).
2. **Given** a request that omits `check` with 6 ids, **When** it runs, **Then** it fails with the existing over-cap error text verbatim — the 50-id check-mode cap does not leak into plain mode.
3. **Given** a request that omits `check` with duplicate ids, **When** it runs, **Then** duplicates are still rendered once per occurrence, as today (dedup is check-mode-only, FR-006).

---

### User Story 3 - The in-band verdict cannot disagree with the out-of-band one (Priority: P2)

A platform engineer debugging a failed nightly run compares what the agent saw in-band (`describe_tool` check mode, from the activity log) with what `mcpproxy tools preflight` reports from the shell. For the same ids, the same proxy state and the same disclosure tier, the two name the same reason. There is one evaluator and therefore one story.

**Why this priority**: A second, subtly different availability answer would be worse than no in-band answer at all — it would make every incident report ambiguous about which surface to trust. It is also the property that keeps the reason taxonomy a single maintained thing rather than two drifting ones.

**Independent Test**: A parity test that drives the same fixture states through `preflight.Evaluate` via the REST handler and via the MCP check handler at both disclosure tiers and asserts equal `{status, reason, retryable, action}` tuples per id.

**Acceptance Scenarios**:

1. **Given** any sabotage-matrix state and a fixed disclosure tier, **When** the same id is checked in-band and over REST, **Then** `status`, `reason`, `retryable` and `action` are equal.
2. **Given** an agent-token MCP session whose scope excludes a server, **When** the agent checks a tool on that server, **Then** it reports plain `not_found` — byte-indistinguishable from an unknown id — matching the REST agent-token tier exactly (FR-009).
3. **Given** a check-mode run, **When** the operator inspects the activity log, **Then** a preflight activity record exists for it carrying the same reason codes the agent received (FR-013).

---

### User Story 4 - Surfaces without `describe_tool` get an honest interim answer (Priority: P3)

A user running the proxy in `code_execution` routing mode, or in direct mode, has no `describe_tool` at all (spec 085 v1 decision, unchanged here). The documentation tells them plainly where the in-band check does and does not exist, and what to use instead, rather than letting them discover the gap by trying.

**Why this priority**: Guard-rail/documentation story. Getting it wrong costs a support round-trip, not a broken product — but leaving it unwritten is exactly how the #969 confusion started.

**Independent Test**: Snapshot `tools/list` for `code_execution` mode and assert it is byte-identical to the pre-099 release; confirm the docs name the interim path for that mode.

**Acceptance Scenarios**:

1. **Given** `code_execution` or direct routing mode, **When** the client lists tools, **Then** `describe_tool` is still absent and the mode's `tools/list` payload is byte-identical to the pre-099 release.
2. **Given** a harness driving a `code_execution`-mode session, **When** it needs a preflight, **Then** the documented path is `POST /api/v1/preflight` from the harness (spec 098), and the docs state that registering check mode on those surfaces is a later phase.

---

### Edge Cases

- **`check: false` explicitly**: identical to omitting it — plain mode, byte-identical response. Only `check: true` switches modes.
- **`filters` sent without `check: true`**: request error (whole call fails, nothing evaluated). Silently ignoring it would let an agent believe a safety filter was applied when it was not. Plain-mode byte-identity is unaffected: requests that do not send the field are untouched.
- **`expect_hashes` sent at all**: request error naming the field as reserved — in-band hash pins were trimmed from v1 (decision 2026-08-16; see Non-Goals). Rejecting rather than ignoring keeps the field additive later without a silent-drop window.
- **`check: null` or a non-boolean `check`**: request error, not "absent" — an agent that sent the field meant to use the mode, and coercing it to plain mode would return schemas to a caller expecting verdicts (FR-012a).
- **Duplicate ids under `check: true`**: deduplicated; one result per unique id, in first-occurrence order (mirrors spec 098 FR-008). Plain mode keeps its existing duplicate-in/duplicate-out behavior.
- **Malformed id (no `server:tool` separator) under `check: true`**: per-id `not_found` with a format hint in `detail` — one bad entry never masks the rest (spec 098 edge case). Plain mode keeps its existing per-id `not_found` + format-hint remediation.
- **Empty `tool_ids` under `check: true`**: request error with check-mode-accurate wording (the existing text says "1-5 tool ids", which is wrong for a 50-id mode). An empty check is a caller bug, matching spec 098's 400 on an empty list.
- **Runtime degraded** (no storage / index / connection snapshot): the check fails with an MCP tool error carrying the evaluator's refusal, never reduced-fidelity verdicts — the in-band mirror of spec 098's 503 (FR-012).
- **Activity record cannot be written**: the check fails with an MCP tool error rather than answering — the in-band mirror of spec 098's "unauditable preflight is not answered" rule (FR-013).
- **Unauthenticated `/mcp` session**: `/mcp` is unauthenticated by default (`require_mcp_auth: false`) and the middleware hands such requests a full **admin** auth context for back-compat. Moot under the locked FR-009: the whole in-band surface evaluates at the agent-token tier, so no auth context — admin or otherwise — can raise disclosure in-band.
- **Profile-pinned session** (`/mcp/p/<slug>`) and `set_profile`: the session's resolved active profile, intersected with any agent-token scope and profile pin, is the evaluation scope (FR-009a). Check mode takes no `profile` parameter — an agent cannot widen or re-point its own scope by asking.
- **Very large batches and cost**: 50 verdict-only results are bounded (~30–60 tokens each); the cap exists so the response cannot become a discovery-bypass dump, the same reasoning that set the plain-mode cap at 5.
- **Never triggers side effects**: like every other preflight surface, check mode performs zero upstream I/O and mutates no runtime state; the durable activity record is the one permitted local write.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001 (Check parameter)**: `describe_tool` MUST accept an optional boolean parameter `check`. `check: true` selects verdict-only check mode; `check: false` or an absent `check` selects today's definition mode. No other parameter changes the mode.
- **FR-002 (Registration surfaces)**: The parameter MUST appear on exactly the surfaces where `describe_tool` is already registered — the default `/mcp` server and the retrieve_tools routing mode (`/mcp/call`, `/mcp/p/<slug>`). It MUST NOT be registered in `code_execution` or direct mode, whose `tools/list` payloads stay byte-identical (FR-014). Documentation MUST state the interim path for those modes: `POST /api/v1/preflight` from the harness (spec 098); registering the built-in there is a later phase.
- **FR-003 (Same evaluator)**: Check mode MUST call the spec-098 evaluator (`preflight.Evaluate`) verbatim through the same glue seam the REST surface uses — same 15-code closed enum, same precedence chain, same state sources, same `ready | unavailable` status vocabulary. Check mode MUST NOT introduce a reason code, re-order precedence, or re-derive a verdict from any other gate. Adding a code remains a spec-098 change with all its obligations (FR-016 there, FR-015 here).
- **FR-004 (Verdict-only payload)**: A check-mode response MUST carry the set-level `verdict` (`ready | degraded_retryable | blocked | unknown_ids`), a `checked_at` timestamp, a `request_id`, and one result per unique requested id carrying `id` and `status`; `unavailable` results additionally carry `reason`, `retryable`, `action` (omitted when the reason has none), `detail`, `remediation`, and `did_you_mean` (≤3 entries, on `not_found` only, computed over the caller-visible scope). It MUST NOT carry `inputSchema`, long descriptions, `call_with`, or any other definition field — that omission is what makes the higher cap safe. The check-mode payload does NOT carry the plain-mode `definitions`/`errors` keys; callers branch on the presence of `verdict`.
  - `checked_at` is the UTC instant the answering evaluation completed, serialized exactly as the REST surface serializes it. Parity assertions (FR-017) MUST exclude it — it is a timestamp, not a verdict.
  - `request_id` MUST be the same correlation id written to the activity record (FR-013), so the agent can hand a human the id that finds the run — SC-006 is otherwise unachievable from inside the session. Today's `describe_tool` handler mints a correlation id internally and never returns it; check mode MUST surface it.
  - **No hash is ever returned**, at any tier. This is a deliberate divergence from the spec-098 REST payload, which discloses a ready tool's current hash at the operator tier: in band there is no operator use case for reading hashes back (pins are written by a harness, not by an agent), the field costs response tokens, and never emitting it removes a disclosure path from a surface that is unauthenticated by default (FR-009). Current hashes stay discoverable exactly where spec 098 put them.
- **FR-005 (Batch cap)**: Check mode MUST accept up to **50** ids per call. Over the cap, the whole call fails with a single error naming the limit — no partial evaluation (mirrors the plain-mode anti-bulk-loophole rule). Plain mode's cap stays at 5, with its existing error text unchanged.
- **FR-006 (Dedup, normalization and ordering)**: Under `check: true`, ids MUST be normalized exactly as the spec-098 REST surface normalizes them (surrounding whitespace trimmed) **before** dedup and before pin-key matching, then deduplicated, with results returned one per unique normalized id in first-occurrence order (spec 098 FR-008). The 50-id cap applies to the **raw** array, before trimming and dedup. Each result's `id` field MUST echo the normalized id, so a caller can join results to pin keys deterministically. Plain mode's normalization and duplicate handling are unchanged.
- **FR-007 (Filters)**: Check mode MUST accept an optional `filters` object carrying the three spec-094 annotation filters — `read_only_only`, `exclude_destructive`, `exclude_open_world` — with the same semantics and the same fixed evaluation order the discovery filters and the REST `policy` object use, producing `missing_annotation` / `policy_filtered` at their precedence slot. The nesting mirrors the REST body's `policy` object; the MCP field is named `filters` because that is the vocabulary `retrieve_tools` already teaches agents. This name divergence between the two surfaces MUST be stated in the docs and in the OpenAPI/contract notes.
- **FR-008 (Hash pins — TRIMMED from v1, decision 2026-08-16)**: Check mode does NOT accept `expect_hashes` in v1. Rationale: pins are authored and checked by harnesses, which already have the REST surface; the in-band parameter priced ~+50 tokens on every session of two surfaces for a niche mid-session use. The field name is reserved: sending it is a request error naming it as reserved (never silently ignored), so it can be added later without ambiguity. `hash_mismatch` therefore remains observable only via REST/CLI, and FR-016's in-band coverage exempts it. The no-hash-echo rule in FR-004 stands regardless.
- **FR-009 (Disclosure tier — the whole in-band surface is the agent-token tier; locked 2026-08-16)**: Check mode ALWAYS evaluates at the spec-098 agent-token tier, regardless of the session's auth context. There is no operator tier in-band: `server_not_in_scope` and `server_not_configured` collapse to the byte-indistinguishable `not_found` (spec 098 FR-013), no hashes are ever disclosed (FR-004), and `did_you_mean` never crosses the caller-visible scope. `AuthContext.IsAdmin()` MUST NOT be consulted — the MCP middleware injects a full admin context for unauthenticated requests when `require_mcp_auth` is false, so it proves nothing, and pinning the tier removes the need for any new credential-presented marker. Operators wanting the full diagnosis (scope names, hashes) use the REST surface over an authenticated channel, where spec 098 already provides it. This can only ever be relaxed additively (a future authenticated marker could raise the tier; nothing can lower it below this floor).
- **FR-009a (Evaluation scope)**: Check mode takes no `profile` parameter; its scope is the session's, composed exactly as the spec-098 glue composes a REST request's scope — agent-token `allowed_servers` ∩ agent-token profile pin ∩ the session's active profile — where "the session's active profile" is whatever the existing session profile resolver reports (path-pinned `/mcp/p/<slug>`, or a `set_profile` selection). The composition MUST reuse that glue rather than re-deriving a scope, so a check can never see a tool the same session's `retrieve_tools` cannot. A pinned profile that no longer exists in config inherits spec 098's rule verbatim: it narrows to a deny-all scope (every id reports `not_found` at the agent-token tier), never a widened one.
- **FR-010 (No upstream I/O, no mutation)**: A check-mode call MUST perform zero upstream server calls and mutate no runtime state (no connects, reconnects, re-index, config or approval writes), asserted by the spec-098 instrumented-transport test extended to this surface. The durable activity write (FR-013) is the one permitted local write.
- **FR-011 (Plain-mode byte-identity, with one enumerated exception)**: When `check` is absent or false, the response MUST be byte-identical to the pre-099 release **except** for out-of-scope ids: the per-id error code `invisible` is retired and out-of-scope ids report `not_found` instead, with the remediation text unchanged (it is already the shared not-found remediation, so only the `error` value moves). This closes the pre-existing differential where a distinct `invisible` code confirmed that an out-of-scope tool exists — the very leak the spec-085 contract intended to prevent and the 098 evaluator already prevents. After this change the plain-mode per-id error vocabulary is `not_found | quarantined | pending_approval | changed | disabled`; the spec-085 `describe_tool` contract MUST be amended accordingly. This delta MUST be the ONLY permitted difference and MUST be enumerated in the byte-identity test rather than blanket-allowed. It MUST be treated as a **compatibility break** — a repo search finds no consumer of `invisible`, but a repo search cannot see external ones — and therefore MUST ship with a release note and a versioned amendment to the spec-085 contract, not merely a code change.
- **FR-012 (Failure surfaces)**: Conditions the REST surface answers with a non-200 MUST map to an MCP tool error (never a fabricated verdict): request errors (over-cap, filters/pins without `check`, orphan or unparseable pin, empty ids, malformed arguments per FR-012a) mirror the 400 class; runtime-unavailable mirrors the 503 class. The error text MUST name the condition and, for the runtime class, say that no verdict could be computed. Check-mode error text MUST NOT reuse plain mode's wording where the numbers differ — in particular the empty-ids error must not tell a check-mode caller to supply "1-5 tool ids".
- **FR-012a (Strict argument validation)**: Under `check: true` the handler MUST reject, as request errors, arguments it cannot honor rather than coercing them: a non-boolean `check` (`null`, string, number); a non-object `filters`; an unknown member of `filters`; a non-boolean filter value; the reserved `expect_hashes` field in any shape (FR-008). `check: null` is treated as a malformed argument, not as "absent" — an agent that sent the field meant to use the mode. Plain mode's existing tolerance is unchanged, so this adds no back-compat risk: every one of these shapes is unreachable in a pre-099 request.
- **FR-013 (Activity record)**: Every executed check-mode call MUST write the spec-098 preflight activity record **synchronously and durably before the result is returned**, carrying the request/correlation id (the same id returned to the caller, FR-004), the requested-id count **defined exactly as the REST record defines it** (unique ids after dedup — the raw count stays recoverable from the recorded arguments), the set verdict, per-tool reason codes and a surface marker distinguishing it from the REST surface. A failed write fails the call (FR-012), mirroring spec 098 FR-014's "a preflight nobody can audit is not answered". Check-mode calls MUST NOT additionally emit the droppable `internal_tool_call` activity record that plain mode emits — one run, one record, under the kind whose durability guarantee it needs. Plain-mode activity behavior is unchanged. Records MUST NOT leak tool names to any telemetry surface.
- **FR-014 (MCP surface delta is exactly one tool on two surfaces)**: The `tools/list` payload MUST change in exactly one place — the `describe_tool` entry on the default server and the retrieve_tools mode — and MUST stay byte-identical for `code_execution` mode and for every other tool on every surface. The spec-098 no-delta snapshot test MUST be converted to an **enumerated-delta** test (the spec-085/094 pattern) rather than blanket-refreshed, and the goldens for the two changed surfaces MUST be regenerated deliberately with the delta named in the test.
- **FR-015 (Token budget, deliberately raised)**: The `describe_tool` definition's ≤150-token budget WILL be exceeded and MUST be replaced by a new explicit budget. Measured with the pinned encoder (tiktoken `cl100k_base`, the spec-083 profiler's encoder) over the marshalled tool definition on this branch's base:

  | Definition | Tokens | Δ vs. today |
  |---|---|---|
  | Today (`tool_ids` only) | 135 | — |
  | + `check` only | 189 | +54 |
  | + `check` + `filters` (declared sub-properties) + `expect_hashes` | **284** | **+149** |
  | + `check` + three flattened filter booleans + `expect_hashes` | 289 | +154 |

  With `expect_hashes` trimmed (FR-008 decision), the shipped shape is `check` + `filters` only — re-measure on the branch; the pre-trim measurement priced the full shape at 284 tokens, so the trimmed shape lands ≈230–240. The new budget is **≤250 tokens** (≈5% headroom — deliberately tight, so the next prose addition has to argue for itself). The cost is once per session on two surfaces, not per call, and is ~1 upstream tool schema's worth of context (the repo's own estimator uses ~150 tokens/schema). The budget MUST be enforced by the existing tokenized budget test with the new constant, AND the exact definition MUST be pinned by the `tools/list` golden snapshot (FR-014) so a prose edit shows up as a reviewable diff, not a silent drift under the ceiling. **The golden update is a deliberate, documented exception to the spec-098 FR-015 no-delta rule** — 098 forbade any MCP-surface movement because it shipped no MCP feature; 099 ships one, so its goldens move once, by intent, with the delta enumerated.
- **FR-016 (Sabotage matrix extension)**: The committed spec-098 sabotage matrix MUST gain a row for every reason-surface cell this feature creates, reusing the existing matrix infrastructure (scenario-keyed JSON + reflection gate) rather than a parallel one. Rows MUST record the surface (`mcp-check`, `mcp-plain`) and the disclosure tier, and MUST cover at minimum: every enum code observable on the `mcp-check` surface (all 15 EXCEPT `hash_mismatch`, `server_not_in_scope` AND `server_not_configured`, which are REST-only under the locked FR-008/FR-009 — the third follows from FR-009 itself: unconfigured collapses to `not_found` at the agent-token tier or a token could probe names to learn what exists; the reflection gate encodes all three exemptions with recorded reasoning, and rows assert the observable collapses); out-of-scope and unconfigured servers in-band (⇒ byte-indistinguishable `not_found`); out-of-scope on the `mcp-plain` surface (⇒ `not_found`, FR-011); `missing_annotation` and `policy_filtered` via each of the three `filters`; the 50/51-id cap boundary; `filters` sent without `check`; `expect_hashes` sent (⇒ reserved-field request error). The reflection gate MUST be extended so a code with no `mcp-check` row fails CI, exactly as a code with no REST row does today. While extending it, one inherited defect MUST be corrected: the existing `mid_indexing` row's note claims a never-indexed server on a connecting upstream would report `not_found` ("existence outranks connection state"), which contradicts spec 098 FR-005 and the shipped evaluator — on a non-Ready server the connection-state verdict wins because existence is unknowable. Correct the note and add an explicit never-indexed-while-connecting row, so the matrix that 099's parity claims are measured against is itself right.
- **FR-017 (Parity)**: An automated parity test MUST assert that for identical ids, identical proxy state, the in-band surface (always agent-token tier, FR-009) and the REST surface AT the agent-token tier return equal `{status, reason, retryable, action}` per id. Fields the two payloads deliberately differ on (`checked_at`, and `hash`, which REST may disclose at the operator tier and check mode never does — FR-004) are excluded from the comparison by name, not by a loose matcher. A divergence in the compared fields is a defect in the glue, never a documented difference.
- **FR-018a (Inherited spec-098 erratum — REST disclosure tier)**: The spec-098 REST `disclosureTier` helper grants the operator tier to any admin-class auth context, which in the server edition includes a non-admin OAuth user (`AuthTypeUser`) — handing scope diagnostics and hash pins to a multi-user tenant. This change MUST correct it: the operator tier is granted POSITIVELY to admin-class contexts only (API-key / socket / named-pipe admin, and the server edition's `AuthTypeAdminUser` — tenant admins legitimately author pins); `AuthTypeUser` and every unknown future auth type fall to the agent-token tier, eliminating the residual-grant shape that caused the defect. Covered by a disclosure test in both editions.
- **FR-018 (Docs)**: Documentation MUST be updated: the preflight feature page (in-band section: when an agent should check vs. describe, worked agent-loop example, the 50-id cap rationale), the spec-085 `describe_tool` contract (new parameters, retired `invisible` code, new token budget), the REST API reference note on `describe_tool`, and the `filters`-vs-`policy` naming divergence (FR-007). The docs MUST state plainly that check mode is absent from `code_execution` and direct mode and what to use instead (FR-002). The release notes MUST carry the `invisible` → `not_found` compatibility break (FR-011) under a heading a consumer scanning for breaking changes will find.

### Key Entities

- **Check request**: the existing `tool_ids` array (≤50 under check mode) plus `check` and optional `filters` (three annotation booleans). No `expect_hashes` (trimmed, FR-008), no profile, no wait budget — scope is the session's, and waiting is a non-goal.
- **Check result**: per-id `{id, status, reason?, retryable?, action?, detail?, remediation?, did_you_mean?}` — the spec-098 result projected onto the MCP payload, minus operator-only fields at the agent-token tier.
- **Set verdict**: the spec-098 worst-class aggregate, carried in-band so an agent can branch on one field instead of scanning results.
- **Preflight activity record (in-band)**: the spec-098 record with a surface marker identifying the MCP check surface.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every one of the 15 reason codes has at least one `mcp-check` sabotage-matrix row asserting its exact `{reason, retryable, action}`, enforced by the extended reflection gate — 100% of cells, in CI.
- **SC-002**: Plain-mode (`check` absent) responses are byte-identical to the pre-099 release across the full describe_tool fixture corpus, with exactly one enumerated delta (out-of-scope `invisible` → `not_found`), asserted by an enumerated-delta test that fails on any second difference.
- **SC-003**: The `tools/list` delta is exactly the `describe_tool` entry on the default and retrieve_tools surfaces; the `code_execution` golden is unchanged; the marshalled `describe_tool` definition measures ≤300 tokens under the pinned encoder.
- **SC-004**: In-band and REST verdicts agree on `{status, reason, retryable, action}` for 100% of sabotage-matrix states at a fixed tier (parity test).
- **SC-005**: One check-mode call accepts 50 raw ids and returns 50 verdicts containing no definition field (`inputSchema`, long description, `call_with`) — asserted on the payload, not by comparison with a narrative baseline.
- **SC-006**: A check-mode call performs zero upstream calls (hard assertion via the instrumented transport, not a threshold) and every executed call is retrievable from the activity log by request id in the same session.
- **SC-007**: At the agent-token tier, an out-of-scope id's complete result is byte-identical to an unknown id's result across every field, asserted directly (not by inspection).

## Non-Goals (Phase 2)

- In-band hash pins (`expect_hashes`) — trimmed 2026-08-16 (FR-008); REST/CLI remains the pinning surface; field name reserved.
- An in-band operator disclosure tier — the whole surface is agent-token tier (FR-009); a future authenticated marker could raise it additively.

- **`wait` / long-polling in band.** No `wait_ms` equivalent: an agent that gets `server_initializing` (retryable) should retry on its own schedule, not hold an MCP call open. `POST /api/v1/preflight` keeps the wait budget for harnesses.
- **A `profile` parameter.** The session's profile is the scope; letting an agent name a profile in a check would let it probe scopes it cannot use.
- **Registering `describe_tool` (and therefore check mode) in `code_execution` or direct mode.** Later phase; interim path documented (FR-002).
- **`readyz` probe endpoint, SSE readiness events, `tools/list_changed` emission.**
- **Tool lockfile (`mcpproxy tools lock/verify`) and registered automation contracts.**
- **Agent-token-carried required-tools contracts; the MCP extension (`app.mcpproxy/required-tools`).**
- **Per-user verdicts in the server edition (`as_user`); `server_saturated`** — both still reserved in spec 098.
- **Any new reason code.** The enum is closed at 15 and owned by spec 098.

## Assumptions

- Spec 098 has shipped (or lands first) with `internal/preflight`, the glue seam, the REST handler, the durable preflight activity record and the committed sabotage matrix. This spec adds a consumer of all five; it does not re-open their contracts.
- The reporter's 2026-08-13 comment on #969 is authoritative for the shape: a check mode on an existing built-in, not a new top-level tool, same evaluator, same reason codes.
- The token measurements in FR-015 were taken on this branch's base with `tiktoken cl100k_base` over the marshalled `mcp.Tool`; final prose may shift them by a few tokens, which the ≤300 ceiling absorbs. If the ceiling is ever hit, the answer is shorter prose or a trimmed parameter set, not a raised ceiling.
- No **in-repo** consumer branches on the plain-mode `invisible` error code — a repo-wide search found it only in the spec-085 contract text (FR-011 amends it). A repo search says nothing about external consumers, which is why FR-011 requires release-note and contract-amendment treatment rather than resting on this assumption. Should an external consumer be discovered, the retirement still stands: it is a leak fix, and the replacement code carries identical remediation text.
- The plain-mode payload is treated as a compatibility contract; the check-mode payload is new and therefore free to differ in shape.

## Priced Alternatives (decided; recorded for the owner, non-blocking)

**The requirements above are the decision** — nothing here is an open requirement, and planning does not wait on this section. It exists because three choices were locked by input rather than by analysis, and a spec that hides their price makes them impossible to revisit later. Each entry names the shipped decision, its measured cost, and the one alternative that was rejected. Only the feature owner may override; an override edits the FR it names.

1. **Parameter set — DECIDED 2026-08-16: `check` + `filters`, `expect_hashes` trimmed.** The full three-parameter shape priced +149 tokens/session on two surfaces; check-only priced +54. The decision keeps `filters` (it is what makes in-band answer "would MY filters exclude tool X", the question spec 094 deliberately withholds at aggregate level) and trims pins to the REST/CLI surface where harnesses author them, with the field name reserved for additive return. Budget re-set in FR-015 (≤250).
2. **`filters` on MCP vs `policy` on REST (FR-007).** Shipped: `filters`, because `retrieve_tools` already teaches agents that word and an agent-facing surface should use the agent's vocabulary. Rejected: renaming for cross-surface symmetry, which would either break the REST contract or teach agents a second word. The divergence is documented in FR-007 rather than hidden.
3. **Admin-authenticated MCP sessions get operator disclosure (FR-009).** Rejected: pinning the entire MCP surface to the agent-token tier, which is simpler to reason about but denies an operator driving `/mcp` over the trusted socket the diagnosis they can already get from the CLI. The fail-closed marker rule in FR-009 is what makes the shipped choice safe; if that marker proves invasive to plumb, the fallback is the rejected alternative — and that fallback is a strictly smaller change, never a leak.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #969`
- ❌ **Do NOT use**: `Fixes #969`, `Closes #969`, `Resolves #969`

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
