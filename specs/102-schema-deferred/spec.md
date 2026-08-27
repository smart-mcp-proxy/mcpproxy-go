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
2. **Given** a direct (`server__tool`) id from the listing, **When** `describe_tool` is called with it, **Then** it resolves through the same registration mapping the dispatch handler was built from (describe and call can never diverge on ambiguous names) and returns the full definition; canonical `server:tool` ids are accepted equivalently.
3. **Given** an id whose tool the current session cannot see (out of agent-token scope, outside the active profile, below the token's operation-permission tier, quarantined, pending approval, or disabled), **When** `describe_tool` is called, **Then** that id returns a per-id `not_found` error — `describe_tool` never returns a definition, a reason code, or any other existence signal that the same session's filtered `tools/list` would not include.
4. **Given** a server removed between `tools/list` and `describe_tool`, **Then** the stale id returns a per-id not-found error without failing the batch.
5. **Given** a tool the session's direct `tools/list` DID include, **When** `describe_tool` is called with it in definition mode, **Then** it returns a definition — a listed tool is never undescribable, regardless of whether the search index currently holds it or what its approval state is. (Under `check: true` the same id returns its availability verdict instead — FR-011.)

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

1. **Given** a running proxy, **When** the deferral setting changes via config hot-reload, **Then** the next direct-surface `tools/list` reflects it — no restart — and connected direct-surface sessions receive a tools-list-changed notification. (FR-014: the config-reload path does not rebuild the direct surface today; this wiring is part of the feature.)
2. **Given** a mode flip, **Then** only serialization changes: the set of listed tools (including `describe_tool`) is identical in both modes, so a flip never adds or removes capabilities mid-session.
3. **Given** no config (defaults), **Then** deferral is off and behavior is governed by FR-010's byte-stability requirement.
4. **Given** an invalid value for the controlling setting, **Then** config validation fails with a clear message naming the accepted values, and `routing_mode: "schema_deferred"` is rejected with a message pointing at the supported composition (direct surface + deferred serialization).

---

### Edge Cases

- **Server or tool names containing `__`**: direct *dispatch* does not parse the display name at all — each registered handler closes over the original `(serverName, toolName)` pair it was built from, so execution is unambiguous by construction. Only the discovery *filters* parse, splitting on the FIRST `__`, which mis-splits a server name that itself contains `__`. `describe_tool` with direct ids therefore MUST resolve through the same registration mapping the handler was built from, NOT by re-parsing — a first-`__` parse would make Inspect disagree with Execute on exactly the names where it matters. Canonical `server:tool` ids remain the unambiguous escape hatch.
- **Display-name collisions**: two distinct upstream pairs can flatten to the same `server__tool` display name (server `a` + tool `b__c` and server `a__b` + tool `c`). Registration silently keeps one today. Deferral does not create the collision, but `describe_tool` on the direct surface makes it observable (one id, two candidate definitions), so the registration mapping MUST resolve a colliding id deterministically or report it rather than guess.
- **Strict client-side validators**: clients that validate or prune arguments against the declared schema get `{"type":"object"}` (accepts any properties), not `{}` — some strict clients treat literal `{}`/missing schemas as "no arguments allowed" and drop everything.
- **Schema-driven client UIs** (inspector-style form rendering): a deferred listing renders an empty form. Documented, accepted consequence of an opt-in mode; the operator choosing deferral is optimizing for agents, not form UIs.
- **Tools with no parameters**: signature is empty parens, never lossy; permissive placeholder is harmless; one-shot callable.
- **Signature cache miss** (tool visible but its signature not yet compiled): the entry MUST still be listed — name + description without the appended signature — never dropped and never blocking `tools/list` on compilation. This is not only a transient mid-reindex condition: the cache is warmed only from tools that passed the tool-level approval filter at index time, while the direct listing is built from live upstream discovery, so tools blocked by tool-level quarantine (pending/changed) are *systematically* absent from the cache — see the catalog-divergence bullet below.
- **Direct listing and index disagree** (the tool-level-quarantine case, and indexing lag generally): the direct listing is a projection of live upstream discovery and is filtered only at the SERVER level, while the signature cache and `describe_tool`'s resolver are both index-backed. A tool that is pending or changed under tool-level quarantine therefore appears in a non-agent session's direct `tools/list` but is in neither the index nor the signature cache. A naive implementation would therefore serve such an entry with no schema (deferred), no signature (cache miss), and no way to recover either (an index-backed `describe_tool` answering `not_found`) — strictly less usable than full mode, which ships its complete `inputSchema` today. That outcome is what FR-017 and FR-011 exist to prevent, and it is NOT the specified behavior: the direct surface has ONE authoritative catalog snapshot shared by listing, description, and validation, so a listed pending/changed tool resolves to its snapshot-backed definition in definition mode (and to a `pending_approval` / `changed` verdict under `check: true`). The signature stays absent until the tool is approved and indexed — a listing-only degradation, not a loss of the schema.
- **Clients that cached full-mode listings across an operator flip**: stale schemas remain *valid* knowledge (validation uses stored schemas — US3 scenario 4); stale compact listings degrade to describe-before-call. Both directions are safe; the listChanged notification (US4) bounds the staleness window.
- **Agent tokens / profiles**: the existing direct-surface discovery filters (token server scope, token operation-permission tier, profile scope, agent callability) run BEFORE serialization; deferral must not change which tools are listed, only how. Out-of-scope tools leak neither schemas nor names nor signatures, and `describe_tool` reports them as plain `not_found` (Spec 099 disclosure discipline). Note that a profile reaches the direct surface only through a profile-pinned agent token or a session default — the URL-scoped `/mcp/p/<slug>` endpoint serves retrieve_tools mode, not direct (FR-003).
- **Prompts and other non-tool surfaces**: untouched — this feature changes tool serialization only.
- **`retrieve_tools`-mode and code-execution surfaces**: untouched. The code-execution surface still has no `describe_tool` and still serializes full (Spec 085 rule); extending deferral there stays follow-up work.

## Requirements *(mandatory)*

### Functional Requirements

**Config surface & mode resolution**

- **FR-001**: Deferred serialization of the direct enumeration surface MUST be opt-in and default-off, hot-reloadable through the existing config reload path, and MUST NOT introduce a new `routing_mode` value. Controlling setting: [NEEDS CLARIFICATION: one-axis vs dedicated key — does the existing global `tool_response_mode: "compact"` extend to govern the direct surface too (zero new config, matches the maintainer's "honor the schema-deferral axis", but silently changes `/mcp/all` output for deployments that already run compact), or does the direct surface get its own key (e.g. `direct_tool_response_mode: "full" | "deferred"`, default `full`), keeping existing compact deployments byte-stable at the cost of one more config field? The spec below is written against "the deferral setting" and works under either resolution.] Whichever resolution is chosen MUST carry the full existing surface of that axis with it: `tool_response_mode` is not only a JSON config key but also the `--tool-response-mode` serve flag (whose help text currently reads "retrieve_tools serialization mode") and the `MCPPROXY_TOOL_RESPONSE_MODE` environment alias. The one-axis resolution therefore silently changes what both of those mean; the dedicated-key resolution requires a matching flag and env alias for parity.
- **FR-002**: `routing_mode` validation MUST continue to accept exactly `retrieve_tools`, `direct`, `code_execution`; the value `schema_deferred` MUST be rejected with an error message that names the supported composition, so users arriving from #971's proposed config get a self-explaining failure.
- **FR-003**: The deferral setting MUST govern every serving of the direct surface identically; there is no per-endpoint divergence. The complete set of direct-serving routes is: the always-available `/mcp/all` endpoint, and — when `routing_mode: "direct"` — `/mcp` plus the two legacy aliases wired to the same configured-mode handler, `/v1/tool_code` and `/v1/tool-code`. These aliases are easy to miss and MUST be covered by the same rollout and notification tests as `/mcp`. `/mcp/p/<slug>` is explicitly NOT in this set: it is wired to the retrieve_tools-mode server, so no profile-scoped direct surface exists today, and creating one is out of scope for this feature.

**Deferred serialization (US1)**

- **FR-004**: With deferral active, each direct-surface `tools/list` entry MUST carry: the unchanged `serverName__toolName` name; the existing description with the tool's compact signature appended (Spec 085 rendering rules: every required parameter named and marked, lossy collapse with explicit `~` marker); unchanged annotations; and a minimal permissive input schema (`{"type":"object"}` — never literal `{}`, never absent). It MUST NOT carry the upstream tool's schema properties or required list.
- **FR-005**: Signatures MUST be served from the Spec 085 index-time signature cache (keyed by the per-tool hash) — no per-request compilation. On a cache miss the entry is listed without a signature rather than dropped or delayed. This requires a read path the cache does not expose today: its only accessor compiles and memoizes on a miss, which would both violate "no per-request compilation" and make a miss unobservable. A non-compiling, miss-reporting lookup MUST be added for this consumer; the existing compiling accessor and the index-time warm path stay unchanged for their current callers.
- **FR-006**: Output schemas: [NEEDS CLARIFICATION: does deferral also strip upstream `outputSchema` from deferred entries (it is part of the token cost, and stripping is protocol-safe since structured content is allowed without a declared schema)? If stripped, must `describe_tool` gain an `output_schema` field in its definitions so the information stays reachable — an additive change to a response shape that Spec 099 froze byte-for-byte on existing surfaces?]
- **FR-007**: The deferral convention (signature syntax, `*`/`~` markers, "call describe_tool for full schemas") MUST be explained in-band on the direct surface, deterministically, without adding a synthetic pseudo-tool to the listing. Exactly one channel MUST be chosen rather than left to the implementer, because the two candidates are not equivalent and neither is free today: MCP server instructions are currently attached only to the default retrieve_tools server instance — none of the routing-mode server instances carries them — so using that channel means adding instructions to the direct server and changing its `initialize` response, a client-visible change that needs its own back-compat note; the `describe_tool` description is the cheaper channel but is constrained by FR-009. The chosen channel MUST carry the convention in BOTH serialization modes or be emitted only when deferral is active — stated explicitly, not left implicit.
- **FR-008**: Deferral MUST change serialization only: the set of tools listed (identity, count, visibility filtering, ordering source) MUST be identical between full and deferred serialization for the same session, verifiable by automated comparison.

**describe_tool on the direct surface (US2)**

- **FR-009**: `describe_tool` MUST be exposed on the direct surface with its full existing *shape* (batch ids → full definitions; per-id errors that never fail the batch; Spec 099 `check` mode and its caps). It MUST be present in BOTH serialization modes, so a mode flip never changes the tool set. Two parts of the "existing contract" cannot be carried over verbatim and MUST be resolved explicitly rather than inherited:
  - **Prose**: the current description tells the agent these are tools "found via retrieve_tools" — false on a surface that has no `retrieve_tools`. But one builder feeds both existing registrations by design, and those bytes are pinned by the tool-surface goldens, so editing it in place would move two surfaces this feature promises not to touch (FR-010). The resolution MUST be stated: either a per-surface description (breaking the single-builder invariant, which then needs its own drift guard) or prose neutral enough to be true on all three surfaces (regenerating the two existing goldens as a second enumerated delta).
  - **Per-id error vocabulary**: see FR-011.
- Batch cap for definition fetches on this surface: [NEEDS CLARIFICATION: keep the existing 5-id cap, or raise it (e.g. 20) for the deferred surface — where an agent that just saw the whole catalog may legitimately want schemas for its entire multi-tool plan in one call, but a large cap re-opens the bulk-dump loophole the 5-id cap was chosen to close?]
- **FR-010**: This is a deliberate change to the built-in tool surface: exactly one addition (`describe_tool` on the direct surface), no renames, no removals, no changes to other surfaces. The direct surface is **not** covered by the existing tool-surface goldens — those snapshot the three routing modes with a static built-in set (default server, retrieve_tools mode, code_execution mode), and direct mode is deliberately excluded from them because its listing is a live projection of upstream catalogs. Consequently:
  - the existing golden baselines MUST remain byte-identical; a diff in any of them means the change reached further than this feature claims, and is a regression to fix, not a baseline to regenerate (the one exception is the FR-009 prose resolution, which if chosen MUST be regenerated once as its own enumerated delta);
  - because direct mode has no golden, the added built-in would otherwise ship entirely unpinned. A NEW gate MUST pin the direct surface's built-in set — its membership and the serialized bytes of each built-in entry — so a later edit is a reviewable diff. Pinning the *upstream* portion of the direct listing is neither required nor possible; the gate covers built-ins only.
- **FR-011**: `describe_tool` on the direct surface MUST accept both canonical `server:tool` ids and direct `server__tool` names, resolving direct names through the registration mapping the dispatch handler was built from (never by re-parsing the display name — see Edge Cases). Resolution MUST apply the same visibility pipeline as the direct-surface listing filters, which is NOT the pipeline the existing `describe_tool` resolver uses. Two concrete divergences MUST be closed:
  - **Operation-permission tier**: the direct listing hides a tool whose required operation type exceeds the agent token's permissions; the existing resolver checks server scope and callability but no permission tier. Without this gate a read-scoped token could obtain the full schema of a destructive tool absent from its own `tools/list`.
  - **Existence-confirming reason codes**: the existing resolver deliberately answers `quarantined` / `pending_approval` / `changed` / `disabled` — codes that confirm the tool exists. On the direct surface those states are precisely what the listing filters hide from an agent session, so returning them would leak exactly what FR-011 forbids. The rule is parity with *this session's* listing, and it splits by mode:
    - **Invisible to this session** (the listing omitted it, for any reason): plain `not_found`, in BOTH definition and `check` mode. Never a reason code, never any other existence signal.
    - **Visible to this session** (the listing included it) — note that a non-agent session's direct listing deliberately retains tools that are pending, changed or **tool-level** disabled, since the callability filter narrows only agent-token sessions. **Server-level** quarantine and server-level disable are a different thing and never reach this surface at all: `DiscoverTools` skips those clients outright (`internal/upstream/manager.go` — "Skipping quarantined client for tool discovery"), so their tools are absent from the projection for every session, agent-token or not, and there is nothing for the visibility rule to retain:
      - *definition mode* MUST return the snapshot-backed definition. A listed tool is never undescribable (US2 scenario 5, FR-017, SC-007); withholding the schema of a tool the session can already see buys no confidentiality and breaks the deferral contract, since under deferral the listing no longer carries the schema itself.
      - *`check: true`* MUST return the informative availability verdict (`pending_approval`, `changed`, `quarantined`, `disabled`) rather than `ready` — that verdict is the entire purpose of check mode, which exists to gate a plan before its first call.
    - retrieve_tools-surface semantics are unchanged by all of the above.

**Pre-dispatch validation, catalog authority & surface durability (US3, cross-cutting)**

- **FR-012**: Direct-mode dispatch MUST validate arguments against the tool's stored upstream schema before dispatching (extending the Spec 085 validator to the direct path), in both serialization modes. Validation failures produce the typed invalid-params error embedding the failing tool's full input schema and a one-line hint; non-argument failures attach no schema; validation is fail-open for uncompilable/unsupported schemas exactly per Spec 085 FR-013b.
- **FR-013**: Validation MUST always use the stored upstream schema — never the advertised permissive placeholder — so calls from clients holding stale full-mode listings validate correctly.
- **FR-017** (catalog authority): the direct surface MUST have ONE authoritative catalog snapshot, and the listing, the appended signature, `describe_tool` resolution, and pre-dispatch validation MUST all read from it. Today they would not: the listing is rebuilt from live upstream discovery, while both the signature cache and `describe_tool`'s resolver are index-backed, and the index is filtered by tool-level approval where the listing is filtered only at the server level. That divergence is not hypothetical (see the catalog-divergence edge case) and produces three distinct failures — a listed tool that cannot be described, a listed tool that can never carry a signature, and validation resolving a different schema than the handler was registered with during refresh skew. Requirements:
  - membership is decided by the direct-surface snapshot, not by index presence: a tool absent from the index but present in the listing MUST still be describable (definition mode, per FR-011) and validatable, and a tool present in the index but absent from the listing MUST report `not_found`;
  - each registered direct entry MUST carry the schema (and hash) it was built from, so its handler validates against the same definition it advertised;
  - the advertised entry and its handler MUST be rebuilt together, **delivered as a safety property rather than as a transaction** (amended 2026-08-26; see the note below). Concretely: no request may observe a state LESS RESTRICTIVE than both the outgoing and incoming generations, and no request may receive a definition for a name the registry is not currently serving. The half of this that IS literally atomic stays literal — a handler validates against the definition its own registration captured, because it closes over it.

  > **Amendment (ACCEPTED 2026-08-26 by the maintainer; T001/T002/T003).** The third bullet above is written as "delivered as" rather than as an atomicity claim, because the claim was not deliverable: the advertised entry lives in mcp-go's tool registry and the catalog lives in mcpproxy's, and mcp-go reads its own tool map under its own lock before invoking our filters, so the two publications cannot be made one transaction without forking mcp-go or taking the direct listing off `SetTools`. The half of this bullet that names dispatch — the handler validating against what it advertised — IS literally satisfied. The plan substitutes a proven safety property plus enumerated residuals, all recorded in [plan.md §Complexity Tracking](plan.md#complexity-tracking) and **awaiting the maintainer's assent at the tasks stage**.
  >
  > **The narrowing is not confined to FR-017.** Because the two publications are not one transaction, three other requirements are absolute in steady state but not inside the microsecond publication window, and the plan says so rather than letting the contradiction sit unstated:
  > - **FR-011 / SC-005** (permission-tier parity, `not_found` for a destructive tool a read-scoped token cannot list): an **annotations-only** change — a tool that becomes destructive with a byte-identical rendered description — may be listed and described against the *previous* generation's tier for the width of the window. Call-time authorization is NOT affected: the handler re-derives the tier from the annotations its own registration captured, so the call is refused even while the listing is stale.
  > - **FR-017 / SC-007** (a listed tool is always describable with its current definition): a **schema-only** or **output-schema-only** change is equally invisible to the discriminator, so `describe_tool` may return the previous generation's `inputSchema` / `output_schema` for the same window.
  >
  > **All three narrowings are ACCEPTED** — FR-017's atomicity as a safety property (T001), a schema-only change describable one generation stale (T002), and an annotations-only change listed one generation stale (T003). They are recorded here, in the requirement text, rather than left in the plan's appendix, so a later reader sees what the spec actually promises instead of discovering the gap in an implementation note.
  >
  > **What makes each acceptable is a compensating mechanism this feature itself ships, not tolerance for the residual:**
  > - the **input-schema** case self-heals — the pre-dispatch validator rejects the stale guess with the CORRECT schema embedded, so one retry succeeds (US3/SC-003);
  > - the **annotations** case cannot mis-authorize — call-time authorization re-derives the tier from the annotations the handler captured, never from the catalog, so a read-scoped token's call to a newly-destructive tool is still refused;
  > - the **output-schema** case has no self-healing path and is accepted on its merits: output schemas are advisory shape metadata MCP permits to be absent entirely, so the worst outcome is one response parsed against last generation's shape.
  >
  > The window is one publication wide and self-corrects at the next publish. What is NOT accepted is any of these being invisible: the skew tests (D13) assert the partition itself, so a fourth residual cannot appear without a failing test.
- **FR-018** (rebuild survival): the direct-surface tool set is replaced wholesale on every upstream refresh — the refresh path sets the complete tool list, and that list is built exclusively from upstream discovery with no built-ins in it today. `describe_tool` MUST therefore be composed into direct-surface tool-set *construction*, not registered once alongside it, or the next upstream change would silently drop it. A regression test MUST assert `describe_tool` is still listed after a refresh.

**Rollout & client compatibility (US4)**

- **FR-014**: A change to the deferral setting MUST rebuild the direct surface (no restart) and MUST emit `notifications/tools/list_changed` to connected direct-surface sessions, so caching clients refetch. This wiring is NEW work, not a hook that already exists: the config-reloaded branch of the routing-mode refresh listener currently re-gates the security scanner and refreshes prompts only — the direct-mode tool set is rebuilt exclusively on the servers-changed event, so a config-only edit rebuilds nothing on the direct surface and emits no notification today. The config-reloaded branch MUST call the direct-surface rebuild, and the rebuild MUST be a no-op emitting no notification when the effective serialization did not actually change (a config edit touching unrelated keys must not churn every connected client's cache).
- **FR-015**: With deferral off (the default), direct-surface `tools/list` payloads MUST be byte-identical to pre-feature behavior except the FR-010 enumerated delta.
- **FR-016**: Existing filtering MUST be unaffected: agent-token scope and profile filters run before serialization; deferred serialization must not alter which tools any session can list, describe, or call.

### Key Entities

- **Deferred tool entry**: the direct-surface `tools/list` rendering of one upstream tool under deferral — name, description + appended signature, annotations, permissive placeholder schema. Derived at rebuild time from the same discovery + filter pipeline as full mode.
- **Compact signature cache** (existing, Spec 085): index-time compiled one-line signatures keyed by per-tool hash; this feature adds the direct surface as a second consumer.
- **Serialization mode resolution**: the config axis (per FR-001 resolution) mapping to `full | deferred` for the direct surface; hot-reloadable; never affects tool-set membership.
- **Tool id forms**: canonical `server:tool` (MCP discovery contract) and direct `server__tool` (direct-surface display name); `describe_tool` on the direct surface must honor both and must resolve the direct form through the registration mapping rather than by re-parsing, since dispatch itself never parses the display name.
- **Direct-surface catalog snapshot** (FR-017): the single authoritative per-rebuild record of what the direct surface exposes — display name, originating `(server, tool)` pair, upstream schema, hash, annotations — shared by the listing, the signature lookup, `describe_tool`, and pre-dispatch validation. Replaces today's split between a live-discovery listing and an index-backed describe/validate path.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the frozen 45-tool reference corpus, deferred direct-surface `tools/list` payload is ≥70% smaller in tokens than full mode, measured by the spec-083 profiler pipeline; on a ~100-tool fleet the projection matches the #971 estimate (~30K → ~3.5–5K tokens, ≥85% reduction).
- **SC-002**: ≥80% of corpus tools (the non-lossy share; Spec 085 lossy-rate gate <20%) are callable one-shot from the deferred listing — zero `describe_tool` round trips — verified against the corpus signature set.
- **SC-003**: An agent whose guessed arguments fail validation completes the call in exactly one retry using only the error-embedded schema (no additional discovery calls), demonstrated in E2E.
- **SC-004**: With deferral off, automated comparison shows direct-surface responses byte-identical to pre-feature behavior modulo the single enumerated surface delta (FR-010); the existing tool-surface goldens pass **unregenerated** (the delta lands on a surface they do not cover), and the new direct-surface built-in gate pins that delta.
- **SC-005**: Scoped sessions (agent token or profile) list, describe, and call exactly the same tool set before and after enabling deferral — zero visibility widening, asserted by the existing scope/profile integration suites extended to deferred mode. Specifically asserted: a read-scoped token receives `not_found` for a destructive tool absent from its own direct `tools/list`, and no per-id reason code ever confirms the existence of a tool that session's listing omitted.
- **SC-007**: Every tool in a session's direct `tools/list` is describable by that session in definition mode (no listed-but-undescribable entries) and, where the signature cache holds it, carries a signature — asserted with a tool that is pending tool-level approval, the case where the listing and the index disagree today (FR-017). The same id under `check: true` returns `pending_approval`, not `ready` and not `not_found`.
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
