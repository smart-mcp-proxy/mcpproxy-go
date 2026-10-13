# Feature Specification: Auto Routing Mode — Budget-Fitted Tool Surface Per Session

**Feature Branch**: `104-auto-routing-mode`
**Created**: 2026-09-07
**Status**: Draft — cross-model review complete (14 rounds, gpt-6-astra, ready-for-plan 2026-09-07; fixed-surface corrections split into Spec 105)
**Input**: User description: "Auto routing mode: routing_mode "auto" picks the tool surface per MCP session from a context token budget — direct/full, direct/deferred, or retrieve_tools+compact — using per-session tools, with the decision observable in API/doctor/tray/telemetry"

**Related**: **Depends on Spec 105 (agent-token scope hardening)** — the fixed-surface corrections that make FR-016's invariant true. Spec 085 (compact router: `describe_tool`, compact signatures, pre-dispatch validation), Spec 102 (schema-deferred direct mode: the middle rung), Spec 103 (token-bench: the measurement vocabulary and mode-cell names reused here), Spec 031 (dedicated routing-mode endpoints), #971.

## Context & Motivation

A developer picks a routing mode once, by hand, and the fixed choice is wrong at both ends of the fleet-size scale. With ~20 tools the default `retrieve_tools` mode makes the agent search for tools it could simply have been shown, so every task starts with a discovery round trip that buys nothing. With ~1,000 tools `direct` mode ships a listing measured at 528,322 tokens on a real fleet — more than twice a 200k window — and deferral (Spec 102) brought the same fleet to 300,893 tokens: cheaper, still not loadable. In the band between, the right answer depends on the developer's context budget, and nothing today tells them which surface they are on or why.

The three surfaces already exist. What is missing is the decision, and the developer's ability to see and trust it. `auto` makes the proxy measure the listing a session would actually receive, with the real tokenizer, and serve the largest surface that fits a budget. It then shows the arithmetic in every place the mode is displayed today, so the choice is never a black box. Two properties make it safe: the measurement is of real bytes, never an estimate; and a session keeps its surface for its lifetime, so long-lived agents never lose their prompt cache to a surface flip.

This release ships `auto` as opt-in and instruments the decision. Whether it becomes the default is a separate decision, gated on the telemetry this feature collects.

## Scope Boundary *(read before planning)*

| Already exists | Reused, not rebuilt |
|---|---|
| Three routing surfaces and their built-ins: `retrieve_tools` + `describe_tool` + `call_tool_*`, direct full, direct deferred, `code_execution` | renderers, dispatch handlers, `describe_tool`, validation, self-healing errors |
| Real tokenizer with JSON counting (cl100k_base by default) | candidate measurement |
| Compact-signature cache (Spec 085/102) | deferred and compact rendering |
| Profile scope, agent-token server restriction and permission tiers, per surface | catalog scoping |
| Direct-surface catalog snapshot with fingerprint-guarded republish (Spec 102) | within-rung refresh |
| Bench mode-cell names `direct_full` / `direct_deferred` / `retrieve_compact` (Spec 103) | rung naming, so bench, API and telemetry agree |

**Release shape.** P1 (User Stories 1–4) is the smallest slice that is safe to expose: `auto` works on the default endpoint, sessions are stable, every request is authorized by its own scope, and the decision is visible in the routing API and `doctor`. P2 (Stories 5–6) adds hysteresis reporting, tray and Web UI rendering, and telemetry. P1 and P2 are internal milestones for ordering the work: **every FR and SC in this spec is required before the feature is enabled in a public release**; `auto` is not offered to users with only P1 landed. `auto` selects among existing surfaces; it adds no new built-in tool and no new tool argument to any fixed surface.

**Prerequisite (Spec 105).** Eight behaviours of the existing fixed surfaces contradict "every request is authorized by its own scope" (cached responses, `set_profile` and profile-URL responses, `retrieve_tools` metadata, direct-publication filtering, target-tier execution permission, aggregated prompts, per-server management operations, legacy cache entries). They are specified and tested in Spec 105 and MUST ship before `auto` is enabled; this spec assumes them and does not restate them.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — The small-fleet developer gets the whole menu, and knows why (Priority: P1)

A developer with two or three upstream servers (about 20 tools) sets `routing_mode: "auto"` and connects an agent to `/mcp`. The session receives every tool with its full schema — no search step, no `describe_tool` round trips — because the measured listing is well under the 12,000-token budget. When they run `mcpproxy doctor`, the routing line names the surface served, the tokens each candidate measured, and the budget.

**Why this priority**: This is the population most likely to try mcpproxy and leave: for them the default mode is strictly worse than no proxy. It is also the cheapest rung to verify.

**Independent Test**: Run a fixture proxy with ~20 tools under `auto`; open a session on `/mcp`; assert its `tools/list` carries the same upstream entries as `/mcp/all` configured for full serialization (same names, same schemas, same annotations) plus the enumerated built-ins for the active settings, and that the decision record shows `direct_full` with its measurement under the budget.

**Acceptance Scenarios**:

1. **Given** a catalog whose full direct listing measures under the budget, **When** a new session initializes on `/mcp`, **Then** its `tools/list` is the direct full surface — every visible upstream tool with full schema, plus the direct built-ins — and no search meta-tool.
2. **Given** that session, **When** the agent calls an upstream tool by its listed name, **Then** the call dispatches exactly as it would on `/mcp/all`: same validation, same self-healing invalid-params errors, same scope and permission gates.
3. **Given** that session, **When** the initialize response is read, **Then** its instructions are composed per FR-019 for the `direct_full` rung and end with one line naming the chosen rung and the budget.
4. **Given** the session is open, **When** an administrator reads the routing API or runs `doctor`, **Then** each shows the chosen rung `direct_full`, the measured tokens for all three candidates, the budget, the per-candidate listed and discoverable counts, the scope label, the decision reason and the decision time.

---

### User Story 2 — The large-fleet developer stays under budget, per call (Priority: P1)

A developer running ~1,000 tools turns on `auto`. Neither full nor deferred fits, so the session gets `retrieve_tools` with compact entries and `describe_tool`. Each search returns a handful of compact signatures; full schemas arrive only for the tools the agent chooses to inspect. The developer sees the three measured numbers side by side and understands that no direct rung could have fit.

**Why this priority**: This is the population the headline savings claim is about. `auto` must never make them worse than the current default, and the observability must make "why not direct?" answerable without a support thread.

**Independent Test**: Run a fixture proxy with the frozen 527-tool snapshot under `auto`; open a session; assert `tools/list` equals the `retrieve_tools`-mode surface as served by `/mcp/call` under the same code-execution and management settings, that `retrieve_tools` answers compact without an option, and that the decision record shows both direct candidates over budget.

**Acceptance Scenarios**:

1. **Given** a catalog where both direct candidates measure over the budget, **When** a new session initializes on `/mcp`, **Then** its `tools/list` is the `retrieve_tools` surface and `retrieve_tools` answers with compact entries (signature plus first-sentence description) without the agent passing any option.
2. **Given** that session, **When** the agent passes `detail: "full"` on a single `retrieve_tools` call, **Then** that call is honoured exactly as on `/mcp/call` — the per-call override is the agent's choice and is outside the rung.
3. **Given** that session, **When** the agent calls `describe_tool` for an id it retrieved, **Then** it receives the full definition, exactly as on `/mcp/call`.
4. **Given** that session, **When** the decision record is read, **Then** every candidate's measurement is present and the two direct candidates are shown exceeding the budget, so the reason for the rung is self-evident.
5. **Given** the same catalog under `routing_mode: "retrieve_tools"` with `tool_response_mode: "compact"`, **When** the two `tools/list` responses are compared, **Then** they are identical — `auto` on the bottom rung is the existing compact retrieve surface, not a fourth surface.

---

### User Story 3 — The middle-band developer is decided by the budget, not by a guess (Priority: P1)

A developer with ~90 tools sits where full is over budget but deferred is under. Their session gets the deferred direct surface: every name with a one-line signature, schemas on demand. A colleague with ~200 tools and a larger model raises `context_budget_tokens` to 40,000 in config and, without restarting, their *next* session lands on deferred instead of `retrieve_tools`. Sessions already open keep the surface they started with.

**Why this priority**: This is the band where fixed modes force the wrong choice and where the budget is the developer's steering wheel. It also exercises the two properties that make `auto` safe: the measurement is real, and the session contract is stable.

**Independent Test**: With a catalog sized so that only deferred fits, assert the session gets the deferred direct surface (Spec 102 rendering); raise the budget via hot-reload; assert existing sessions are unchanged and a new session moves up a rung.

**Acceptance Scenarios**:

1. **Given** a catalog where full is over budget and deferred is under, **When** a new session initializes, **Then** its `tools/list` is the deferred direct surface — every tool with its compact signature appended, the permissive placeholder schema, `describe_tool` present — and the initialize instructions carry the deferral legend.
2. **Given** an open session on any rung, **When** `context_budget_tokens` changes via config hot-reload, **Then** that session's surface does not change, no tools-list-changed notification is sent to it for that reason, and no restart is required.
3. **Given** the budget change moves the measured catalog across the budget, **When** the next session initializes, **Then** it gets the rung that now fits; a budget change resets hysteresis history so the new budget is applied plainly.
4. **Given** token counting is not available at runtime (disabled in config, or the tokenizer failed to initialize), **When** a session initializes under `auto`, **Then** the proxy serves the `retrieve_compact` rung and the decision record says the measurement was unavailable and why — a zero count is never treated as "fits".

---

### User Story 4 — A scoped session is measured on the catalog it will actually see, and only that (Priority: P1)

A team pins a profile onto an agent token, restricts the token to a few servers, or grants it read-only permission. That session's catalog is a fraction of the fleet, so a 1,000-tool proxy can still hand a 15-tool profile the full direct surface. Two sessions on the same `/mcp` endpoint with different scopes get different rungs; each is measured on what its own scope can see; and every request is authorized by the scope it presents, never by the scope the session was decided for.

**Why this priority**: Profiles and tokens are how large fleets are carved into usable slices; measuring the whole fleet for a scoped session would defeat `auto` for exactly the users who most need it. Because `/mcp` is agent-accessible, authorization correctness cannot be deferred to a later slice.

**Independent Test**: On one proxy, open a session with a profile-pinned token whose scope fits full, and an unscoped administrator session whose fleet fits only `retrieve_compact`; assert the rungs differ, that each session can list, describe and call exactly what its own request scope allows (the unscoped session legitimately sees more), and that no registry, instruction, notification or decision-record state leaks between them.

**Acceptance Scenarios**:

1. **Given** a session whose token pins a profile, restricts servers, or limits permission tiers, **When** it initializes, **Then** the candidates are measured over the tools that scope can see on each rung — using that rung's own discovery predicate (the direct listing filters by permission tier; retrieve discovery filters by server scope and availability only, as today) — not over the whole fleet. A read-only token on the `retrieve_compact` rung can therefore discover and describe an approved destructive tool on an allowed server and cannot call it; on a direct rung that tool is not listed. This per-surface asymmetry is preserved, not changed.
2. **Given** two concurrent sessions on `/mcp` with different scopes, **When** each lists tools, **Then** each receives the rung its own measurement chose, and each sees exactly what its own request scope allows and nothing more.
3. **Given** two concurrent sessions whose tokens allow the same servers but carry different permission tiers, **When** each lists tools, **Then** each is measured and served on its own listing; they do not share a verdict.
4. **Given** a session on the `retrieve_compact` rung that calls `set_profile` after initializing, **When** the profile narrows the catalog, **Then** the session's rung does not change, and the retrieve visibility filters apply the new scope exactly as they do today. The `set_profile` response lists only servers the caller's token may see, and an invalid selection from an agent token names no profiles it may not see (Spec 105 FR-003/FR-004). On a direct rung `set_profile` is not listed and is rejected exactly as on `/mcp/all` today.
5. **Given** a later request on an existing session presents a token with a narrower or different scope than the one the session was decided for, **When** it lists, describes or calls tools, **Then** visibility and dispatch are gated by the scope of *that request*; the rung only ever governs serialization and which built-ins appear. A request that presents no valid credential where one is required is rejected as today.
6. **Given** a decision record, **When** it is read by an administrator, **Then** it names the scope it was measured for (the profile slug, `token_scoped`, or `unscoped`) and never lists tool or server names.

---

### User Story 5 — The catalog grows and nothing flips on a 1% change (Priority: P2)

A developer adds a server. Existing sessions keep their rung; their listings refresh within the rung exactly as today. If the addition pushes the measured catalog over the budget by more than the hysteresis band, the next new session lands on a lower rung; if it nudges it by a few percent, nothing changes. Removing the server later has the mirror behaviour with the same band, so a fleet hovering near the budget does not alternate rungs session to session.

**Why this priority**: Long-lived agents rely on prompt caching; a surface flip mid-session throws that cache away. A fleet near the boundary would otherwise flip on every reconnect, which makes the mode look unstable and erodes trust in the decision.

**Independent Test**: Start with a catalog that measures 95% of budget on the full rung and a first verdict of `direct_full`; add tools totalling 8% of budget (listing now 103%); assert 100 consecutive new sessions keep `direct_full`. Add tools bringing the listing to 115%; assert the very next new session drops a rung and every open session keeps its rung.

**Acceptance Scenarios**:

1. **Given** an open session on a direct rung, **When** an upstream server connects or disconnects, **Then** the session's rung is unchanged; its listing updates within the rung by the existing direct-surface refresh, which does not republish when the surface fingerprint is unchanged.
2. **Given** the previous verdict for a scope is `direct_full` and the full listing now measures within the band above the budget, **When** a new session initializes, **Then** it still receives `direct_full`, with reason `held_by_hysteresis`.
3. **Given** the full listing measures beyond the band, **When** a new session initializes, **Then** it receives the first eligible candidate in preference order per the FR-007 table (which can be the floor even when a lower rung fits the nominal budget but was ineligible before and has not cleared the band), and the decision record shows the previous rung and the crossing.
4. **Given** the previous verdict is `direct_deferred` and the full listing shrinks back to within the band below the budget, **When** a new session initializes, **Then** it keeps `direct_deferred` until the full listing measures below the band — the band applies in both directions.
5. **Given** no measurement input has changed since the last verdict for a measurement identity, **When** many sessions initialize, **Then** the proxy does not re-measure per session: the cached verdict is reused, so connect latency is not proportional to fleet size.

---

### User Story 6 — Every existing surface reports the decision honestly (Priority: P2)

An operator turns on `auto` and looks in the places they already look: `/api/v1/status`, `/api/v1/routing`, `mcpproxy doctor`, `mcpproxy status`, the tray glance, the Web UI mode switcher, the telemetry heartbeat. Each says `auto` is the served mode and shows what it is entitled to show of the decision. None silently displays "Retrieve" because `auto` was an unknown word.

**Why this priority**: Today an unrecognised routing value is folded to `retrieve_tools` when resolved, the Web UI falls back to "Retrieve" for unknown values, `doctor` reads the config file rather than the daemon, and telemetry reports the configured rather than the served mode. Any one of those left in place means an operator cannot tell whether `auto` is running.

**Independent Test**: With `auto` running, read each surface as an administrator and assert it names `auto` and shows the projection defined in FR-021; with the daemon stopped, assert `doctor` reports `auto` as configured and states that no live decision is available.

**Acceptance Scenarios**:

1. **Given** `auto` is served, **When** an administrator reads `/api/v1/status` and `/api/v1/routing`, **Then** `routing_mode` is `auto`, `available_modes` includes `auto`, and the routing payload carries the latest verdict per measurement identity and the recent-verdict history.
2. **Given** `auto` is served, **When** an agent-token caller reads `/api/v1/status` or `/api/v1/routing`, **Then** it sees `routing_mode: auto` and only the verdict for its own scope; the unscoped verdict, other scopes' verdicts, their catalog sizes and their profile names are withheld, as deployment-wide activation data is withheld from scoped callers today.
3. **Given** `auto` is served, **When** `mcpproxy doctor` or `mcpproxy status` runs, **Then** it reports the served mode and the latest unscoped verdict from the running daemon; the CLI states in FR-022 (no daemon, unauthorized, daemon without the field, no verdict yet) each produce their defined output and exit status.
4. **Given** `auto` is served, **When** the tray glance and the Web UI mode switcher render, **Then** each shows `auto` with the latest unscoped verdict's rung and measured-versus-budget figures; the settings selectors offer `auto` as a fourth option marked restart-required, as the other routing values are.
5. **Given** a telemetry heartbeat from an install serving `auto`, **When** it is inspected, **Then** `routing_mode` is `auto`, the aggregate record of FR-026 is present with closed-enum keys and values only, and no tool, server or profile name appears; from an install not serving `auto` the record is absent, not zero-filled.
6. **Given** an operator switches from a fixed mode to `auto` (or back), **When** the config is saved, **Then** the change is reported as restart-required exactly as other routing-mode changes are today, and the pending value shows in the routing API and Web UI.

---

### Edge Cases

- **Catalog not yet settled at initialize.** Upstream servers connect and are discovered in the background, so the first sessions of a process would otherwise measure an empty or partial catalog, receive `direct_full`, and be stranded on a full-fleet listing when the rest arrives. A server is *settled* when its discovery has completed and its tools are published to the surfaces, or it is in a terminal error or disabled state, or 15 seconds have elapsed since its **admission**: the first connection attempt after the server was added, enabled, unquarantined, or had its connection configuration replaced. Automatic retries do not restart the deadline; repeated attempts inside the window settle at the original deadline whether or not a publication event occurs. Settlement is evaluated **per scope**: the catalog is settled for a scope when every enabled, non-quarantined server that scope may see is settled; servers outside the scope never affect it, so a scoped session neither waits for nor learns about servers it cannot see. Until settled for its scope, a new session is served the floor with reason `catalog_pending`; it keeps the floor for its lifetime like any other session. A scope with zero visible enabled servers is settled immediately. A server admitted while the process is running starts its own 15-second window for the scopes that may see it; sessions that initialize inside it get `catalog_pending`, and sessions after it are measured on whatever that server published. A server that keeps reconnecting counts as settled once its admission window has elapsed; its listing changes then flow through re-measurement like any other catalog change. The remaining risk — a slow server publishing after its window — is accepted and handled by re-measurement for later sessions.
- **Fallback verdicts are transient.** `catalog_pending` ends when the catalog settles, whether or not that publishes a listing change; `measurement_error` is not cached — the next session re-measures; `tokenizer_unavailable` lasts until restart, because tokenizer configuration is restart-pinned today. Fallback verdicts never establish hysteresis history.
- **Token counting unavailable.** The measurement component MUST know whether counting is available and which encoding it uses, distinguishing configured-off from initialization failure; both are reported as *unavailable* with their reason, never as a zero count. A direct rung is never inferred from tool counts, per-tool averages or the fleet-wide savings estimator.
- **Session identity.** The client-facing HTTP surface is pinned to the protocol era that mints a session id per session; that pin stays. Transport validation takes precedence: a request without a valid session id, or with an id the proxy does not know (for example after a restart), is rejected by the protocol as today; there is no accepted no-session path on the default endpoint, and no request ever inherits another session's rung. A repeated `initialize` on an existing session keeps that session's binding.
- **Serialization settings under `auto`.** The rung dictates serialization on the default endpoint: `tool_response_mode` and `direct_tool_response_mode` are not consulted there, and `doctor` says so when they are set while `auto` is served. They keep governing the dedicated endpoints. A hot-reload of either is not a measurement input and does not re-measure.
- **Listing-affecting settings.** `enable_code_execution` (a live tool or a disabled stub on the retrieve rung; a live tool or absent on the direct rungs) changes what a rung lists; it is a measurement input, and a change re-measures for new sessions and refreshes open sessions within their rung exactly as it does on the fixed endpoints today. `disable_management` and `read_only_mode` also change the retrieve listing, but the listing is built once at startup today even though change reporting treats them as hot; for this feature they become **startup-pinned** measurement inputs in every mode: a change is reported as restart-required and pinned in the effective configuration (listed under FR-004), and no hot-toggle behaviour is specified or tested.
- **`code_execution` on the direct rungs.** When enabled, the tool is present on every rung under `auto`, rendered as it is today on the surface that already carries it; its tokens are included in each candidate's measurement. On the direct rungs this is the one enumerated built-in delta from `/mcp/all`. Calls made from inside a script follow the existing code-execution authorization and dispatch contract (server and tool named separately), not the direct catalog's admission: a tool withheld from the direct listing because its flattened display name collides is still callable from a script, exactly as it is on `/mcp/code` today. Parity with `/mcp/all` (FR-011, FR-030, SC-005) is therefore defined over **direct-name dispatch**; nested script calls are compared with `/mcp/code`. A fixture with a display-name collision pins this. `set_profile` is not added to the direct rungs.
- **Rung order is a preference order, not a size order.** With an empty or tiny catalog the direct listing (one built-in) can be smaller than the retrieve menu. The ladder is evaluated top-down: the first candidate that fits wins; `retrieve_compact` is served whenever no direct candidate fits, regardless of its own measurement. The decision record says "all candidates over budget" only when the measurements show it.
- **Catalog changes during initialize.** The decision is bound to a measurement revision; a change landing between measurement and the first `tools/list` is handled by the existing within-rung refresh, not by re-deciding. The decision record keeps the revision it was measured on.
- **Config reload churn.** Today a config reload republishes the `retrieve_tools` surface to all its clients without a change check. Under `auto` a budget or unrelated change MUST NOT cause a republish to sessions whose surface did not change (FR-012). This is a deliberate compatibility exception to FR-004.
- **Availability predicates differ by surface.** The direct listing and the search index disagree by design on quarantined-but-lingering and pending tools, and agent-token callers and administrators see pending tools differently on the direct surface. Each candidate is measured with its own predicate for the caller kind and scope in question, so the number shown is the number that rung would ship. Display-name collisions withheld from the direct listing are withheld from the direct measurements too: measurement is of the served listing, never of the raw catalog.
- **Scope definition edits.** Editing a profile's server list, a token's allowed servers or its permissions changes what that scope can see without changing the tool catalog; it is a measurement input, and it resets hysteresis history for that scope.
- **Per-session state.** A session binding lives exactly as long as its transport session. Transport sessions end on explicit termination, at restart, or by **live-session idle expiry**: 30 minutes since the last request *completed* on that session with no request in flight, counted from initialize for a session that never sends another request. An in-flight tool call counts as activity and defers expiry until it completes (a 40-minute call on an otherwise idle session keeps the session alive and its binding intact); an open receiving stream does not count as activity and is closed when the session expires. A call still running when its session is explicitly terminated completes upstream but its result is not delivered, and neither a late completion nor a late stream write can resurrect an expired session or binding. Clock-controlled tests cover the 40-minute call and the idle stream. The expiry applies to every HTTP MCP session in every mode (a deliberate change, listed under FR-004). Expiry ends the transport session, so a request on an expired id is rejected as unknown (never served without its binding). This live expiry is distinct from the existing cleanup of persisted session history rows, which is unchanged. Initialize-only sessions appear in the administrator session projection as live sessions with no activity; persisted history rows never show a binding after the live session has ended. The retention bounds of FR-017 hold whether or not the transport reports session end.
- **Dedicated endpoints and aliases.** `/mcp/all`, `/mcp/code`, `/mcp/call` and `/mcp/p/<slug>` keep their fixed modes and ignore the budget. The legacy aliases of `/mcp` are true aliases today and remain so: under `auto` they are auto-routed with `/mcp`.
- **Budget below every candidate.** Hysteresis eligibility (FR-007) is applied first: a previously eligible direct candidate inside its band stays eligible and is served even when every measurement exceeds the nominal budget. Only when neither direct candidate is eligible and all three measurements exceed the nominal budget is the floor served with reason `budget_below_all`, and `doctor` warns. A fixture MUST cover the all-over-budget case with a previously eligible direct candidate inside the 110% band.
- **Who may read decision records.** Administrators (API key or local socket) see all verdicts, history and session bindings. An agent-token caller sees only the latest verdict for its own scope, and only while that verdict's measurement identity still matches the caller's current authorization; when a profile or token is narrowed or deleted, the earlier broader record is withheld from that caller immediately (state `no_current_authorized_verdict`) while administrators keep it as history. Session endpoints stay administrator-only as today.
- **Effective budget under an environment override.** When the environment variable is set, it is the effective budget: an API or file edit is persisted, reported by the API as `overridden_by_environment` with the effective value, and does not change verdicts or reset hysteresis. Without the override, an API edit is applied immediately and persisted, and a later reload of an unrelated file change keeps it.
- **Invalid local config while the daemon runs.** If the local config file fails validation (for example `context_budget_tokens: 0`) while a daemon is running on the previous valid configuration, `doctor` and `status` still connect to the daemon and report the live decision; the local validation failure is reported as a separate finding, not as a reason to skip the live diagnostics.
- **stdio transport.** stdio does not honour `routing_mode` today and continues to serve the default surface; `auto` is a property of the default HTTP endpoint only.

## Requirements *(mandatory)*

### Definitions *(binding)*

- **Rung**: one of three candidate surfaces in preference order: `direct_full`, `direct_deferred`, `retrieve_compact`. Names are the bench mode-cell identifiers so bench, API and telemetry agree.
- **Measured object**: the complete `tools/list` result a session would receive on that rung for its measurement identity — the tools array as rendered for serving (including deferred signature suffixes as they will actually be served, given the signature cache state at measurement time) plus any metadata the proxy adds to the result — serialized as the proxy serializes it, excluding only the JSON-RPC envelope. The proxy serves the listing unpaginated, so one page is the whole listing.
- **Candidate measurement**: the token count of the measured object by the runtime tokenizer, labelled with the encoding name.
- **Measurement identity**: every input that changes a candidate's measured object: the visible tool set with its hashes and annotations; per-tool approval and callability state as seen by the caller kind; the caller kind (administrator or agent) and the token's profile pin, server restriction and permission tiers (tiers affect the direct rungs' listing only; retrieve discovery is tier-blind, as today); the effective budget; and the listing-affecting settings (code execution; management and read-only as startup-pinned values). A dedicated endpoint's serialization setting is not an input.
- **Scope label**: the redacted display form of the authorization part of a measurement identity: a tagged pair of **category** (`unscoped`, `token_scoped`, or `profile`) and, for `profile`, the slug. The category is a separate field, never inferred from the slug, so a profile literally named `unscoped` or `token_scoped` (both valid names today) is distinct from the categories in every record, projection and retention rule. Used in records and displays; never the key itself.
- **Measurement revision**: an opaque identifier that changes whenever any input of a measurement identity changes. Recorded on every verdict.
- **Verdict**: the rung chosen for one measurement identity at one revision, with its decision record.
- **Session binding**: the rung and reason a session was served at initialize, immutable for the session's lifetime.
- **Hysteresis**: see FR-007 and the transition table under it.
- **Settled catalog**: see Edge Cases; the precondition for any verdict other than `catalog_pending`.

### Functional Requirements

**Configuration**

- **FR-001**: `routing_mode` MUST accept the value `auto` in addition to the existing three. The value MUST pass config validation and MUST NOT be folded to `retrieve_tools` anywhere a mode is resolved or displayed. *Release-required*: the routing API's `available_modes`, and the tray and Web UI mode vocabularies and settings selectors, MUST recognise `auto` in the same release as FR-005, so no surface displays a fallback name.
- **FR-002**: A setting `context_budget_tokens` MUST exist with default 12,000. Validation matrix: absent → default; `0` or negative → rejected with the field named; non-integer, overflowing or malformed environment value → rejected with the field named; when the environment variable is set it is the effective value and file or API edits are persisted but reported as `overridden_by_environment` (see Edge Cases).
- **FR-003**: Switching to or from `auto` MUST be restart-gated, reported and pinned exactly as other `routing_mode` changes are today. Changing the effective `context_budget_tokens` MUST be hot-reloadable, reported as applied immediately with the field named, MUST affect only sessions that initialize after the reload, and MUST reset hysteresis history; an edit that does not change the effective value MUST NOT reset history.
- **FR-004**: `auto` MUST be opt-in in this release; the shipped default remains `retrieve_tools`. With `auto` unset every existing surface MUST be identical to pre-feature behaviour, with these enumerated exceptions, each of which applies in every mode: FR-012 (notification suppression); the Spec 105 corrections (shipped separately, listed there); FR-022's daemon-discovery correction; `disable_management` and `read_only_mode` becoming restart-gated in change reporting and effective-config pinning (they are reported as hot changes today although the listing is built once at startup); and the live-session idle expiry of the Per-session state edge case, which applies to every HTTP MCP session regardless of mode.

**Decision**

- **FR-005**: At each new session's initialize on the default endpoint, the proxy MUST obtain a verdict for that session's measurement identity, and the session's first `tools/list` MUST already reflect it. Serving the verdict MUST NOT depend on a notification the client may never read.
- **FR-006**: Candidate measurements MUST be made over the measured object as defined, with the runtime tokenizer, from the same rendered revision that will be served. Tool counts, per-tool averages and the fleet-wide savings estimator MUST NOT be used to decide.
- **FR-007**: The chosen rung MUST be the first candidate in preference order that is eligible; `retrieve_compact` is the unconditional floor. Eligibility with hysteresis (band = 10% of budget, fixed):

  | Situation for a candidate | Eligible when |
  |---|---|
  | No history for this scope (first verdict, after restart, after a budget change or a scope-definition edit) | measurement ≤ budget |
  | History exists and the candidate was eligible in the previous verdict | measurement ≤ budget × 1.10 |
  | History exists and the candidate was not eligible in the previous verdict | measurement < budget × 0.90 |

  Comparisons are exact: a previously ineligible candidate at exactly budget × 0.90 stays ineligible; a previously eligible candidate at exactly budget × 1.10 stays eligible. History per scope records the previous verdict's rung and each candidate's eligibility. Multi-rung transitions are allowed: the first eligible candidate wins whatever the previous rung was, and a lower rung that fits the nominal budget but was ineligible before and has not cleared the band is skipped — the floor is served (accepted, symmetric hysteresis). Fallback verdicts do not write history.
- **FR-008**: Settlement for the session's scope (Edge Cases) MUST be checked before any verdict is reused, independently of the measurement revision: a newly admitted in-scope server inside its window yields `catalog_pending` even though no listing changed, and a cached successful verdict is reused again once the scope is settled (a fixture covers cached verdict → delayed admission → pending → settlement by deadline without any tool publication). Otherwise verdicts MUST be reused for subsequent sessions with the same measurement identity until the measurement revision changes **or the scope's hysteresis history expires**; either event MUST trigger re-decision for the next session only, never a change to sessions already open (measured bytes may be reused across a history expiry; the decision may not). A verdict does not outlive its scope's history: when the history expires 24 hours after the scope's last session ends, the cached verdict is dropped with it. For display only, the administrator projections retain one **last-bound record** per scope label — keyed by category plus slug, so the administrator-unscoped record is never replaced by a profile named `unscoped` (tested) — marked `historical`, never reused for routing, bounded by the number of scope labels seen since start and independent of the 20-record rolling history so that after expiry the API, CLI and UI show that historical record with state `no_retained_verdict` rather than a fabricated no-session state. Telemetry is excluded from this projection: its record carries no state field, and after expiry its measurement bucket reports `none`. The clock-controlled expiry fixture asserts those projections before another session connects, and a second fixture binds more than 20 scoped verdicts after an unscoped session ends and then lets the unscoped verdict expire, asserting the historical unscoped record is still displayed. Fallback verdicts follow the transience rules in Edge Cases. Two independently seeded clock-controlled tests MUST cover history expiry with identical measurements at 95% of budget: (a) a scope whose last session ended, reconnecting 23 hours later, observes the held rung and — because that session renews retention when it ends — the same scope reconnecting 25 hours after *that* session ends observes a plain-fit decision; (b) a scope with two overlapping sessions, where ending one does not start expiry while the other remains live. After recovery from `catalog_pending` the reused verdict is reported per FR-021's binding-order rule.
- **FR-009**: A session MUST keep its rung for its lifetime. No catalog change, budget change, profile selection, or config reload may change an open session's rung or emit a tools-list-changed notification to it for that reason. Within-rung refreshes (server connects and disconnects, listing-affecting setting changes) behave as they do on that rung's fixed endpoint today.
- **FR-010**: When measurement is impossible — catalog not settled, counting unavailable, measurement error — the proxy MUST serve `retrieve_compact` and record the reason (`catalog_pending`, `tokenizer_unavailable`, `measurement_error`).
- **FR-011**: Each rung served under `auto` MUST be behaviourally identical to the same surface on its fixed endpoint configured for the corresponding serialization (`direct_full` ↔ `/mcp/all` with full direct serialization; `direct_deferred` ↔ `/mcp/all` with deferred serialization; `retrieve_compact` ↔ `/mcp/call` with compact retrieve serialization), under the same code-execution, management and read-only settings: same upstream tool set, same serialization, same visibility predicates, same scope and permission gates, same dispatch validation and error shapes. The only permitted built-in delta is `code_execution` on the direct rungs when enabled.
- **FR-012**: A config reload MUST NOT republish the `retrieve_tools` surface to sessions whose surface is unchanged; unchanged surfaces MUST be detected as they are for the direct surface today. This applies whether or not `auto` is served. On the `auto` endpoint, additionally, every list-changed notification — tools **and prompts** — MUST be decided **per recipient** (as defined in FR-016e): a recipient is notified only when its own authorized, serialized listing (tool listing or prompt listing respectively) changed. A prompt change on an inaccessible server produces no prompt-list notification to a recipient that cannot see it; the A-POST/B-stream and credential-narrowing cases of FR-016e are tested for prompt notifications as well. A catalog change that leaves a recipient's authorized listing unchanged produces no notification to it, so fleet activity is not signalled to recipients that cannot see it; a change that alters the authorized listing is notified without changing the rung. Deliberate exceptions, all of the same shape: display-name collision admission on the direct surface, prompt-name collision admission, and the global prompt cap are fleet-wide (retained by Spec 105), so a change on an inaccessible server can remove or restore an entry in a scoped tool or prompt listing; that listing change **is** notified, because withholding it would leave the recipient with a stale listing, and the fixed surfaces already expose the same withholding. Fixtures with a token allowed only server `a` pin each: a tool collision introduced on inaccessible `a__b`, a prompt collision introduced on `a__b`, and a prompt-cap displacement caused by `b`. The fixed endpoints keep their fleet-wide notification behaviour, which is why the within-rung parity of FR-009 and FR-011 is a parity of listing content and dispatch, not of notification delivery.
- **FR-013**: The dedicated endpoints and the profile-URL endpoint MUST keep their fixed modes and MUST ignore the budget. The legacy aliases of the default endpoint remain aliases and follow it.
- **FR-014**: The client-facing protocol-era pin that guarantees a session id MUST be retained and covered by a test that fails if it is lifted.
- **FR-015**: Under `auto`, the global `tool_response_mode` and `direct_tool_response_mode` settings MUST NOT influence the default endpoint; the per-call `detail` override on `retrieve_tools` MUST keep working on the `retrieve_compact` rung.

**Visibility and security**

- **FR-016**: Discovery, description and dispatch on every request MUST be gated by the authorization of that request; the rung MUST govern only serialization and which built-ins appear. Discovery authorization (server scope, availability, approval; plus permission tier on the direct rungs only) and execution permission (tier at dispatch) remain the separate checks they are today. A request presenting a narrower or different token than the one the session was decided for MUST NOT discover, describe, call, or retrieve from cache anything outside its own authorization, and MUST NOT be able to read a decision record that belongs to a scope it is not entitled to. The fixed-surface corrections that make this true are Spec 105 FR-001–FR-009.
- **FR-016e**: A list-changed notification's (tools **and prompts**) **recipient is the authenticated receiving stream or request**, never the session id alone. A notification is authorized against the credential that opened the receiving stream, at delivery time; a stream whose credential has since been narrowed, revoked or expired is closed rather than served. Tests MUST cover a session initialized with broad token A whose notification stream is opened with narrow token B while another request on the same session uses A (B's stream learns nothing A-only), and a credential narrowed after the stream opened.
- **FR-017**: Retention bounds, each independently testable: session bindings ≤ live sessions (released on termination, idle expiry, or restart); cached verdicts ≤ live measurement identities, with verdicts of superseded revisions dropped once each live identity has a newer verdict; hysteresis history ≤ scopes seen, kept for 24 hours after that scope's last session ends so a reconnecting scope keeps its hysteresis; recent-verdict history ≤ 20 records.
- **FR-018**: Local decision records MUST NOT contain tool names, server names, descriptions or schemas. The scope label MAY be the caller's own profile slug; an agent-token caller MUST be shown only the verdict for its own scope, never the unscoped verdict, other scopes' verdicts, their catalog sizes or their profile names. Session endpoints remain administrator-only.

**Instructions**

- **FR-019**: The initialize response under `auto` MUST be composed, per session and without mutating shared state, as: (1) the rung's base text — the direct instructions for the direct rungs, the `retrieve_tools` instructions for the floor; (2) if the operator configured custom `instructions`, that text replaces the base text on every rung, as it does on the direct endpoint today; (3) on `direct_deferred`, the deferral legend is appended; (4) one line naming the chosen rung and the budget is appended last. Where the fixed retrieve endpoint currently carries no default instructions over HTTP, the floor under `auto` uses the `retrieve_tools` default text; this is a named addition. That text's guidance about management tools (`upstream_servers`) MUST appear only when the management tools are actually listed for that session (they are absent under `disable_management` or `read_only_mode`); operator-supplied instructions and the fixed-surface goldens are unaffected, and initialize instructions are tested under both disabling settings.

**Observability — decision record and projections**

- **FR-020**: A decision record MUST exist for every verdict, carrying: chosen rung; decision reason (`fitted`, `held_by_hysteresis`, `catalog_pending`, `tokenizer_unavailable`, `measurement_error`, `budget_below_all`); for each of the three candidates its measured tokens (or `unavailable` with reason) and two separately named count pairs — *listed* upstream tool count and the number of servers represented among those listed entries (built-ins excluded), and *discoverable* upstream tool count and the number of servers with at least one discoverable tool under that rung's discovery predicate for the scope (so a destructive-only server seen by a read-only token counts on the retrieve rung and not on the direct rungs; an empty fleet reports zeros); budget; hysteresis band; tokenizer encoding name; scope label; measurement revision; decision timestamp; the previous rung for that scope; **the prior per-candidate eligibility used for this decision's comparisons** (three values, or an explicit `no_history` state after a reset, restart or expiry); and whether hysteresis held the previous rung. Counts are `n/a` when the measurement is unavailable. A test MUST cover two decisions with the same previous rung but different prior eligibility for `direct_deferred` (e.g. previous rung `direct_full`, full now at 115%, deferred at 95%) and assert the differing outcomes are reconstructible from their records alone.
- **FR-021**: Projection matrix — the three objects are distinct and each surface shows exactly these:

  | Surface | Administrator sees | Agent-token caller sees |
  |---|---|---|
  | `/api/v1/routing` | latest verdict per measurement identity (scope label, full record); recent-verdict history | latest verdict for its own scope only |
  | `/api/v1/status` | `routing_mode: auto`; latest unscoped verdict's rung and reason | `routing_mode: auto`; own-scope rung and reason |
  | session listing | each session's binding (rung, reason, revision) | not available (administrator-only) |
  | `doctor`, `status` CLI | latest unscoped verdict, full record | n/a (administrator credential) |
  | tray, Web UI | latest unscoped verdict's rung, measured-versus-budget figures, reason | n/a |
  | heartbeat | aggregate of FR-026 only | n/a |

  "Latest verdict" in every projection and in the telemetry measurement bucket means **the verdict most recently bound to a session** (binding order), not the most recently evaluated one; a cached verdict reused after a `catalog_pending` interval therefore becomes the latest again the moment a session binds to it, without any new measurement. The FR-008 delayed-admission fixture asserts routing, status, CLI and the telemetry bucket after recovery.

  No-decision states are defined **per requested projection**, never fabricated: `no_session_evaluated` (the daemon is healthy and no session of any scope has initialized on an **auto-routed endpoint** — the default endpoint and its aliases — since start; sessions on the fixed endpoints do not count, though they remain visible in the administrator session listing, and the wording is "no auto-routed session has connected"; a fixture initializes only a `/mcp/all` session and asserts this state); `no_retained_verdict` (a verdict existed but expired with its history — the historical last-bound record is shown, marked as such); `no_unscoped_verdict` (only scoped sessions have initialized — the administrator projections then show the most recent verdict of any scope, carrying its scope label, rung, reason and measured figures, and say that no unscoped session has connected); `catalog_pending` and `tokenizer_unavailable` only when a session actually received that fallback; and, for agent-token callers, `no_current_authorized_verdict` when the caller's authorization no longer matches the latest record for its scope. The status API's administrator projection carries the alternate scoped record in the `no_unscoped_verdict` state so the CLI can render it; the CLI reads the administrator routing projection when it needs the full record. When several scopes have verdicts, the administrator default display is the unscoped verdict with a count of other scopes.
- **FR-022**: `mcpproxy doctor` and `mcpproxy status` MUST report the served mode and the unscoped verdict from the running daemon. State table (text and JSON output; exit statuses use the existing CLI codes):

  | State | Output | Exit |
  |---|---|---|
  | running, `auto`, verdict present | full decision record | 0 |
  | running, `auto`, `no_session_evaluated` | "no decision yet: no auto-routed session has connected" | 0 |
  | running, `auto`, `no_unscoped_verdict` | "no unscoped session has connected; latest scoped decision:" followed by that record with its scope label | 0 |
  | running, `auto`, `no_retained_verdict` | "last decision expired; historical:" followed by the last-bound record marked historical | 0 |
  | running, `auto`, latest binding is a fallback | its reason (`catalog_pending`, `tokenizer_unavailable`, `measurement_error`) | 0 |
  | running, fixed mode | that mode, no decision line | 0 |
  | daemon reachable, request unauthorized | "unauthorized" with the credential source tried | 5 |
  | daemon reachable, response malformed or request failed | "daemon responded but the decision could not be read" | 1 |
  | daemon reachable, older than this feature (field absent) | "decision not reported by this daemon version" | 0 |
  | no daemon reachable | configured value with "no live decision available" | `status`: 0 (unchanged today); `doctor`: 1 (unchanged today) |
  | local config invalid, daemon running | live state as above, plus a separate config-validation finding | as above |

  *(prerequisite correction)* Daemon discovery MUST distinguish an authorization failure from a connection failure, so the unauthorized and no-daemon states are never conflated. `doctor` MUST warn on `budget_below_all` and MUST note when `tool_response_mode` or `direct_tool_response_mode` is set while `auto` is served.
- **FR-023**: The tray and the Web UI MUST render `auto` as the served mode and the full FR-021 projection for their row: the Web UI mode switcher shows the chosen rung, reason and measured-versus-budget figures; the tray shows the served mode, current rung, reason and measured-versus-budget figures.
- **FR-024**: Documentation for routing modes, configuration, the REST API and the CLI MUST describe `auto`, the budget, the preference ladder, the settled-catalog rule, the hysteresis table and the projection matrix, with worked figures at the default budget for a reproducible baseline: the frozen 45-tool and 527-tool corpora, administrator scope, code execution disabled, management enabled, warm signature cache, cl100k_base.

**Telemetry**

- **FR-025**: The heartbeat MUST report `routing_mode` as the served mode (`auto`), correcting the current behaviour of reporting the configured value.
- **FR-026**: The heartbeat MUST add an aggregate auto-routing record, present with zero-valued counters whenever `auto` is served and absent otherwise, reset on each accepted send as other window counters are (the existing in-flight loss window between snapshot and reset is accepted). Typed domains:

  | Field | Domain | Semantics |
  |---|---|---|
  | bindings by rung | histogram keyed by the three rung names → non-negative integer | session bindings created in the window (served-session distribution, cache hits included) |
  | bindings by reason | histogram keyed by the six reasons → non-negative integer | same window |
  | verdict decisions | non-negative integer | fresh verdict decisions in the window — including decisions made from cached measurements after a history expiry, excluding bindings that reuse a cached verdict |
  | held by hysteresis | non-negative integer | of those fresh decisions, the number with reason `held_by_hysteresis` |
  | budget bucket | enum `le_4k, le_8k, le_12k, le_24k, le_48k, gt_48k` | effective budget at send time |
  | unscoped measurement bucket | enum `none, unavailable, le_2k, le_6k, le_12k, le_24k, le_50k, le_100k, le_200k, gt_200k` | latest unscoped verdict's `direct_full` measurement at send time; `none` when no unscoped verdict exists or the last one has expired (historical display records are not reported) |
  | distinct scope labels | enum `1, 2_3, 4_9, ge_10` | distinct labels with a binding in the window; `1` when idle |

  Scopes are aggregated; no per-scope breakdown.
- **FR-027**: The telemetry schema version MUST be bumped; the anonymity scanner MUST gain a shape rule for the new record that rejects unknown keys, non-integer or negative counters, and any categorical value outside its enum; the telemetry documentation MUST be brought current with the code.

**Surface stability**

- **FR-028**: The feature MUST add no built-in tool and no tool argument to any fixed surface; the existing tool-surface goldens for the fixed surfaces MUST pass unregenerated. The `auto` default endpoint MUST get its own goldens per rung, per code-execution setting.
- **FR-029**: A regression test MUST assert that a session's rung survives an upstream server connect, an upstream disconnect, a budget hot-reload, a serialization-setting hot-reload, a listing-affecting setting change, and — for a retrieve-rung session — a `set_profile` call; and that an out-of-scope server change produces no notification to a scoped session while an in-scope listing change does (FR-012).
- **FR-030**: A visibility-parity test MUST assert, for each rung, that an `auto` session and a fixed-mode session with the same measurement identity agree exactly on what they can list, describe and call — including quarantined, pending, permission-tiered and out-of-scope cases — and a concurrency test MUST cover sessions with equal server scopes but different permissions, sessions with disjoint scopes, and a token change between initialize, list, describe and call.

### Key Entities

- **Verdict**: the rung chosen for one measurement identity at one revision, with its decision record; reused until the revision changes.
- **Decision record**: the observable explanation of a verdict (FR-020), projected per FR-021.
- **Session binding**: the rung and reason a given session was served, fixed at initialize, exposed to administrators in the session listing, released at session end.
- **Hysteresis history**: per scope, the previous verdict's rung and per-candidate eligibility; reset by budget change and scope-definition edits; expires 24 hours after the scope's last session.
- **Budget policy**: `context_budget_tokens` plus the fixed 10% band; hot-reloadable; consulted only at verdict time.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the frozen 45-tool corpus at the default budget under the FR-024 baseline, a new `auto` session receives the same upstream entries as `/mcp/all` in full serialization with only the enumerated built-in delta; on the 527-tool snapshot it receives the same `tools/list` as `/mcp/call` in compact serialization. Both asserted in automated tests.
- **SC-002**: For every successful (non-fallback) verdict whose measurement identity and revision are unchanged between decision and first listing, the recorded measurement of the chosen rung equals an independent tokenizer count of the measured object the session actually received. Separately, for every candidate ahead of the chosen rung in preference order, the record shows either `unavailable` or a measurement that fails that candidate's exact FR-007 eligibility predicate given the recorded history (e.g. a previously ineligible candidate at exactly budget × 0.90 is correctly ineligible).
- **SC-003**: Starting from a `direct_full` verdict on a listing at 95% of budget, growth to 103% leaves 100 consecutive new sessions on `direct_full` with reason `held_by_hysteresis`; growth to 115% drops the very next session a rung; every open session keeps its rung in both runs. From `direct_deferred`, shrinking the full listing to 92% keeps `direct_deferred`; shrinking to 88% promotes the next session.
- **SC-004**: Connect latency, measured from the initialize request arriving to the initialize response leaving, on the 527-tool snapshot, 200 sequential sessions after warm-up, on the CI reference runner: p95 under `auto` with a cached verdict is within 50 ms of p95 under `retrieve_tools`; the first verdict after a revision change completes within 500 ms at p95.
- **SC-005**: For every scoped session in the profile and agent-token integration suites, the tool set listed, describable and callable is identical between `auto` (on whichever rung it lands) and the corresponding fixed mode — zero visibility widening — and the FR-030 concurrency test shows no state shared between sessions.
- **SC-006**: With a fixture whose upstreams publish with a delay: a session initializing before settlement gets `catalog_pending`, keeps it after the fleet publishes, and a session initializing after settlement gets the fitted rung; when settlement occurs by deadline without any tools arriving, the next session gets a fitted verdict, not `catalog_pending`; after an injected measurement error, the next session re-measures successfully.
- **SC-007**: Every reporting surface in the FR-021 matrix shows its defined projection for `auto`, including each no-decision state, and a test enumerates the surfaces and asserts none displays a fallback mode name.
- **SC-008**: The telemetry anonymity scan rejects a heartbeat whose auto-routing record carries an unknown key, a negative or non-integer counter, or a categorical value outside its enum, and accepts a record with legitimate integer counts; no string in the heartbeat matches any tool, server or profile name in the fixture fleet; local decision records contain no tool or server name and, for an agent-token caller, no profile name other than its own.
- **SC-013**: With a broad token and a narrow token on one session: CLI discovery against a daemon with a wrong API key and no socket reports unauthorized (exit 5), not "no daemon" (FR-022); and the A-POST/B-stream and narrowed-credential cases deliver nothing to the narrower stream for tools and prompts (FR-016e). Spec 105's SC-001–SC-004 are prerequisites and are not repeated here.
- **SC-009**: All existing tool-surface goldens for the fixed surfaces pass unregenerated on the feature branch.
- **SC-010**: `doctor` output under `auto` contains, in its documented format, the chosen rung, the decision reason, the three measured figures and the budget; a test compares the live output to the documented sample for each FR-022 state.
- **SC-011**: After N sessions across M scopes open and end, retained session bindings are zero after expiry, cached verdicts are ≤ M, hysteresis histories are ≤ M, and the recent-verdict history is ≤ 20, for N ≫ M.
- **SC-012**: Instrumentation acceptance: in the integration suite, on a quiescent snapshot (no session activity between payload construction and the accepted send), the heartbeat record's binding counts equal the sessions opened in the window and its decision count equals the fresh verdict decisions made (a history-expiry re-decision from cached measurements counts one decision and zero measurements; a cached-verdict binding counts one binding and zero decisions — tested separately). An idle window yields zero counters and distinct-label bucket `1` while the budget bucket and the latest-unscoped measurement bucket retain their current values; the measurement bucket is `none` only when no unscoped verdict exists, asserted separately. Operational KPI (not a merge gate): the record is present in at least 95% of heartbeats from installs serving `auto` in the release's first 30 days.

## Assumptions

- **Worked figures** in this spec (528,322 / 300,893 tokens for a 1,016-tool fleet; the frozen 45-tool and 527-tool corpora) come from existing measurements; they motivate the design but never enter the decision — FR-006 measures the real listing.
- **The hysteresis band is fixed at 10%** and is not a configuration knob; one knob (`context_budget_tokens`) is enough for this release.
- **Scope at verdict time** is what initialize can see: caller kind and the token's profile pin, server restriction and permission tiers. A `set_profile` selection made after initialize narrows visibility within the rung but does not re-decide.
- **Candidate measurements use each rung's own visibility predicate** for the caller kind in question, because that is what each rung would actually serve; no unified predicate is introduced.
- **The settlement window is 15 seconds from admission** (first attempt after add, enable, unquarantine or connection-config replacement; retries do not extend it), chosen to cover normal stdio-server startup while keeping the first session's wait bounded; the partial-catalog risk for servers slower than that is accepted and corrected by re-measurement for later sessions.
- **Tokenizer configuration is restart-pinned**, as it is today; the runtime tokenizer's availability and encoding name are exposed to the measurement component.
- **`disable_management` and `read_only_mode` are startup-pinned** for the purposes of this feature; making them hot-reloadable is a separate change.
- **Live-session idle expiry of 30 minutes** matches the existing persisted-session cleanup interval so operators see one number.
- **The per-session serving mechanism** is a planning choice (a session-aware listing with per-session serialization, or per-session tool registration). The binding constraints are FR-005, FR-009, FR-016 and FR-019.
- **Bench is unchanged**: `auto` sessions report one of the existing mode-cell ids, so bench and telemetry share vocabulary without a new cell.
- **Default flip** is out of this release. The metric it will be gated on is FR-026's served-session distribution; the threshold is decided in the follow-up spec, not here.
- **Tokenizer specificity**: measurements and the budget are figures for the runtime encoding (cl100k_base by default) and are labelled with it; documentation states that other models' tokenizers count differently and gives no universal bound.

## Out of Scope

- A fourth surface, a new built-in tool, or a new tool argument on a fixed surface.
- Per-session rung changes, client-declared budgets via headers, or any mid-session renegotiation.
- Applying the budget to `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, or stdio.
- Changing which quarantined, pending or disabled tools each surface shows, or how agent-token callers see pending tools.
- Making `auto` the default, or a configurable hysteresis band.
- Lifting the legacy protocol-era pin on the client-facing HTTP surface.
- Agent-token access to session endpoints.

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
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
