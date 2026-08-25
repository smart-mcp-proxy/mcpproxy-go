# Phase 0 Research: Schema-Deferred Direct Mode

**Spec**: [spec.md](spec.md) · **Plan**: [plan.md](plan.md)

Resolves the three `[NEEDS CLARIFICATION]` markers in the spec (R1–R3), records the
channel decision FR-007 demands (R4), and pins the mechanical findings the plan's
design rests on (R5–R10). Every code reference was verified against this tree
(base: `origin/main`, post-#1026).

---

## R1 — Config surface (FR-001): dedicated key, not the shared axis

**Decision**: New dedicated field `direct_tool_response_mode: "full" | "deferred"`,
default `"full"`, beside `tool_response_mode` in `internal/config/config.go`. Full
axis parity: serve flag `--direct-tool-response-mode` (help: "direct-surface
tools/list serialization: full (default) or deferred") and env alias
`MCPPROXY_DIRECT_TOOL_RESPONSE_MODE`, following the exact Spec-085 wiring pattern
(`cmd/mcpproxy/main.go:141` + `applyToolResponseModeFlag`, `loader.go:717`).

**Rationale**:
- Extending the existing `tool_response_mode: "compact"` to also govern the direct
  surface would silently change `/mcp/all` output for every deployment already
  running compact — a violation of FR-015's byte-stability promise and of the Spec
  085 discipline that a serialization flip is an explicit operator act per surface.
- The existing flag/env pair is documented as "retrieve_tools serialization mode"
  (`main.go:141`); the one-axis resolution would mutate the meaning of a shipped
  flag and env var out from under scripts that set them.
- The two axes also have different value sets (`compact` vs `deferred`): the direct
  surface's deferred rendering is not the compact retrieve_tools entry shape (it
  keeps full descriptions and annotations, appends the signature, and substitutes a
  placeholder schema), so sharing one enum would conflate two serializations.
- Cost: one more config field, wired through all four silent points (see R7).

**Alternatives considered**: (a) one-axis `tool_response_mode` extension — rejected
above; (b) per-endpoint setting (`/mcp/all` vs `/mcp` direct) — rejected by FR-003
(no per-endpoint divergence); (c) value `schema_deferred` on `routing_mode` —
rejected by the maintainer's accepted direction and FR-002 (the validator gains a
targeted error message naming the composition instead).

## R2 — Output schemas (FR-006): defer them too; describe_tool carries them

**Decision**: Deferred entries strip `outputSchema` along with `inputSchema`
(today applied by `applyToolOutputSchemaJSON`, `mcp_routing.go:143`). To keep the
information reachable, `describe_tool` definitions gain an additive
`output_schema` field — present only when the resolved tool declares one — on
**all** surfaces, emitted by the describe_tool definition assembly in
`mcp_describe_tool.go` (after `buildToolEntry`), **not** by `buildFullToolEntry`.

**Rationale**:
- Output schemas are part of the ~77% schema share of the payload (Spec 083); the
  MCP protocol allows structured content without a declared schema, so stripping is
  protocol-safe, and a permissive placeholder is not even needed (`outputSchema` is
  optional — the deferred entry simply omits it).
- Placement matters for the frozen goldens: `ToolMetadata.OutputSchemaJSON` is
  already indexed (`internal/index/bleve.go:39,177`), so describe_tool can render
  it from either resolver. Adding it inside `buildFullToolEntry` would change
  full-mode `retrieve_tools` responses and break the byte-exact
  `TestRetrieveToolsFullMode_GoldenByteIdentity` golden
  (`retrieve_full_default.golden.json`) — so the field is added at the definition
  assembly seam only. The definition-equality tests ("full-mode entry minus score")
  are updated to enumerate the new field.
- **Correction — a response-bytes golden DOES exist.** An earlier draft of this
  decision claimed "no golden pins describe_tool *response* bytes". That is
  false: `TestDescribeToolPlainCorpus_ByteIdenticalWithOneEnumeratedDelta`
  (`describe_plain_corpus_test.go`,
  golden `testdata/describe_plain_corpus/pre099.json`) replays 18 plain-mode
  `describe_tool` calls and compares each **response body byte for byte**,
  permitting only the substitutions enumerated in `describePlainDelta`; its doc
  comment is explicit that "a reordered key, a reworded remediation, a changed
  cap message" fails. `output_schema` is `omitempty` and today's
  `seedVisibilityFixture` tools declare no output schema, so D2 by itself does
  not move these bytes — but the R5 prose work does, and any fixture that later
  declares an output schema would. This gate is enumerated as the FOURTH frozen
  gate in plan §Test strategy.
- Uniform across surfaces: a per-surface response shape would break the
  single-assembly invariant and give the same id two different answers.

**Alternatives considered**: keep `outputSchema` on deferred entries (wastes the
tokens the feature exists to save; Atlassian's low-compression precedent strips
it); direct-surface-only `output_schema` (surface divergence, rejected).

## R3 — describe_tool batch cap (FR-009): keep 5

**Decision**: The definition-mode cap stays `maxDescribeToolIDs = 5` on the direct
surface, identical to the existing surfaces. `check: true` keeps its 50-id cap.

**Rationale**: One builder feeds every registration (`buildDescribeToolTool`,
`mcp_describe_tool.go:62`) and its prose/cap are pinned by the tools/list goldens;
a per-surface cap would force a second builder or a parameterized schema — new
drift surface for ~zero benefit. The bulk-dump loophole the cap closes is *more*
live on a surface that enumerates the whole catalog, not less. An agent planning N
lossy tools batches ⌈N/5⌉ calls; SC-002's ≥80% non-lossy share bounds how often
that happens, and check-mode (cap 50) already covers the "gate a whole plan"
case without shipping schemas.

## R4 — In-band convention channel (FR-007): direct-server instructions

**Decision**: The deferral convention is carried by **MCP server instructions on
the direct server instance** — a static string set via
`mcpserver.WithInstructions(...)` when `p.directServer` is constructed in
`initRoutingModeServers` (`mcp_routing.go:619`), present in BOTH serialization
modes, phrased conditionally so it is true in both:

The single reference wording lives in
[contracts/direct-deferred-surface.md §2](contracts/direct-deferred-surface.md)
— it is not restated here, so the two documents cannot drift. Exact bytes are
finalized at implementation and pinned by the new direct-surface built-in gate
(plan §Test strategy).

**Rationale**:
- The `describe_tool`-description channel is budget-capped
  (`describeToolTokenBudget = 250`, currently measuring 243) and golden-pinned; it
  cannot absorb the signature legend, and cramming the *direct surface's* listing
  convention into a tool that also serves two retrieve_tools surfaces would make
  its prose wrong somewhere (the exact FR-009 trap).
- Instructions arrive in `initialize`, i.e. at Catalog time before `tools/list` —
  the only channel the agent is guaranteed to see before reading the deferred
  entries.
- Static-in-both-modes (FR-007's first branch) rather than emitted-only-when-
  deferred: mcp-go instructions are constructor-time state; emitting them
  conditionally would require rebuilding the server instance on hot-reload while
  sessions span the flip. The conditional phrasing keeps them accurate when
  deferral is off.
- Client-visible delta: the direct server's `initialize` response gains an
  `instructions` field where it had none (today only the default retrieve_tools
  server carries instructions, `mcp.go:480`). Enumerated as a back-compat note in
  the plan and pinned by a test.

**Precedence against the existing `instructions` config field** (interaction the
first draft missed): `instructions` is an operator-configurable config key
(`config.go:489`) that `resolveInstructions` (`mcp.go:94`) resolves to the
operator's string, or `defaultInstructions` when empty — and it is applied to
`p.server` ONLY (`mcp.go:480`). A hard-coded direct-server string would leave an
operator's configured instructions permanently unreachable on `/mcp/all` and on
`/mcp` under `routing_mode: "direct"`. **Decision**: the direct server's
instructions are `resolveInstructions(cfg.Instructions)` + a blank line + the
deferral legend, so the operator's text still leads and the legend is additive.
The legend is a package constant so the new direct built-in golden can pin its
exact bytes, and the golden fixture uses the empty-`instructions` default so the
pinned string is deterministic. A test asserts a custom `instructions` value
still appears, legend still appended. Caveat, stated rather than implied:
instructions are constructor-time state on both server instances, so a change to
the `instructions` key still needs a restart — this feature does not make it
hot-reloadable and does not regress it either (the default server behaves the
same today). Only `direct_tool_response_mode` is hot-reloadable (FR-014), and the
legend is phrased to stay true across a flip precisely so no instructions rebuild
is required.

**Alternatives considered**: describe_tool description (rejected above); synthetic
pseudo-tool (forbidden by FR-007); per-entry legend repetition (token-multiplied
across every tool — defeats the feature).

## R5 — FR-009 prose: surface-neutral rewrite + one deliberate golden regen

**Decision**: Keep ONE `buildDescribeToolTool` builder; rewrite its
retrieve_tools-specific prose to surface-neutral prose, keeping the marshalled
definition under the 250-token budget (`TestDescribeTool_DefinitionTokenBudget`,
currently measuring 243 — the rewrite has ~7 tokens of headroom and MUST be
re-measured, not assumed).

**Scope — FOUR strings, not one.** The top-level description is only the most
visible offender; the same builder and handler carry three more
retrieve_tools-specific strings that are equally false on a surface with no
`retrieve_tools`, and two of them are *runtime response* strings rather than
definition bytes:

| Site | Current text | Why it must change |
|---|---|---|
| `mcp_describe_tool.go:64` tool description | "…for specific tools found via retrieve_tools." | FR-009 (definition bytes; golden-pinned) |
| `mcp_describe_tool.go:71` `tool_ids` param description | "Tool ids in '\<server\>:\<tool\>' format from retrieve_tools. Max 5, or 50 with check:true." | definition bytes; ALSO contradicts FR-011, which requires the direct surface to accept `server__tool` ids — the prose must name both accepted forms |
| `mcp_describe_tool.go:82` `filters` param description | "check:true only. Annotation filters, as in retrieve_tools." | definition bytes; golden-pinned |
| `mcp_describe_tool.go:50` `describeNotFoundRemediation` + `:195` malformed-id remediation | "…re-run retrieve_tools." / "…exactly as returned by retrieve_tools." | **response** bytes — contract §3 calls these the direct surface's "standard remediation" / "format remediation"; pinned by the describe_plain_corpus gate (R2 correction) |

**Golden handling**:
- Regenerate `toolslist_goldens/default_server.json` and
  `toolslist_goldens/retrieve_tools_mode.json` **once**, as the FR-010-sanctioned
  enumerated delta. `toolsListAllowedDelta` needs **no edit**: it already lists
  `describe_tool` for both surfaces (`toolslist_snapshot_test.go:188-192`),
  because spec 099 enumerated it there. The frozen `pre099/` baseline and
  `code_execution_mode.json` are never touched.
- Extend `describePlainDelta` (`describe_plain_corpus_test.go:48`) with the named
  remediation substitutions, in the same one-substitution-per-scenario style the
  existing entries use, and regenerate nothing there — the delta map is the
  enumeration, the golden stays frozen.

**Rationale**: The alternative — per-surface descriptions — breaks the deliberate
single-builder invariant ("the two schemas cannot drift", `mcp_describe_tool.go:53`)
and would need its own drift guard; the spec explicitly blesses the one-time regen
path (FR-009/FR-010), and Spec 099 already established the exact
regenerate-deliberately-once + enumerated-delta procedure in
`toolslist_snapshot_test.go`.

## R6 — Signature cache read path (FR-005): add a non-compiling `Peek`

**Finding**: `toolsig.Cache` exposes only `Get(hash, paramsJSON, description)`
(compiles + memoizes on miss, `cache.go:44`) and `Warm` (index-time). FR-005
requires a lookup that (a) never compiles on the request path and (b) makes a miss
observable so the entry can be listed signature-less.

**Decision**: Add `Peek(hash string) (Signature, bool)` to `internal/toolsig` —
pure read, no memoization, miss returns `ok=false`. Existing callers
(`buildCompactToolEntry`, warm path) are untouched.

**Key availability confirmed**: the catalog's `hash` is real on the discovery
projection — `core/client.go:384` sets `toolMeta.Hash =
hash.ComputeToolHashWithOutputSchema(...)` inside `ListTools`, so every
`DiscoverTools` entry carries the same Spec-032 hash the indexer later warms the
cache with (`runtime/lifecycle.go:874`, which skips hashless tools). `Peek` and
the warm path therefore agree by construction; no hash is recomputed in the
direct rebuild.

## R13 — Two publications, one generation: closing the catalog/registry skew window

**Finding** (cross-model review, verified): the catalog pointer swap and
`SetTools` are two separate publications — `RefreshDirectModeTools` builds the
tools (publishing the catalog) and *then* calls
`p.directServer.SetTools(...)` (`mcp_routing.go:666-673`). An `atomic.Pointer`
makes the catalog swap atomic; it does **not** make catalog-plus-mcp-go-registry
a single transaction, and mcp-go's registry read happens inside its own
`ListTools` path where no lock of ours can span it. So the first draft's claim
that entry and handler "are rebuilt atomically and can never diverge" is only
half true, and FR-017's "no window exposes one without the other" is not
satisfied by the pointer alone.

**What IS already atomic**: handler ↔ its own schema/hash. The handler closes
over its `directCatalogEntry`, so a dispatch can never validate against a
definition other than the one its own registration advertised, regardless of
skew. That half of FR-017 needs no further mechanism.

**What skews**: the request-time *filters* and the *describe resolver*, which
read the published pointer independently of which registry generation produced
the list they are filtering. Enumerating both orderings:

| Window | catalog published first (chosen) | `SetTools` first |
|---|---|---|
| Tool removed in the new generation | listing still shows it (old registry), filter finds no catalog entry | not listed; describe would resolve from the old catalog → describable-but-unlisted |
| Tool added in the new generation | not listed; describe resolves → describable-but-unlisted | listed, filter finds no old catalog entry |

**Decision** — three rules that make every cell safe, rather than pretending the
window does not exist:

1. **Catalog is published first**, so the filters always enforce the newer,
   narrower truth.
2. **Filters deny on catalog miss.** A name absent from the current catalog is
   dropped, NOT passed through. Pass-through is reserved for built-ins and is
   matched against an explicit built-in name set (today: `describe_tool`), never
   inferred from "not in the catalog" — otherwise a stale upstream entry in the
   old registry would skip the permission-tier gate entirely, turning the skew
   window into a scope leak. (This corrects the fall-through wording in D10.)
   **One explicit exception**: a *nil* catalog pointer — never published yet —
   is not a miss, it is "no catalog", and the filters keep today's
   `ParseDirectToolName` behavior for it. Otherwise a pre-init request, and every
   hand-constructed test proxy that calls the filters directly
   (`mcp_routing_test.go`, `toon_surface_isolation_test.go`), would see the whole
   listing denied. An *empty published* catalog is a real generation and does
   deny; only the nil pointer falls back.
3. **Describe requires registry membership as well as catalog visibility.**
   Before returning a definition the direct resolver checks
   `p.directServer.GetTool(displayName) != nil` (mcp-go
   `server/server.go:1023`, an O(1) read of the very map the listing is served
   from). Catalog visibility ∧ registry presence closes describable-but-unlisted
   in *both* orderings, and it strengthens rather than weakens FR-017's
   "membership is decided by the direct-surface snapshot, not index presence" —
   the registry is the listing's own source, not the index.

The catalog carries a monotonically increasing `generation` so the skew is
observable in logs and assertable in tests. **Test**: a rebuild that pauses
between the two publications, with a concurrent scoped `tools/list` and
`describe_tool` for both an added and a removed tool, asserting no
describable-but-unlisted id and no unfiltered stale entry in either window.

## R11 — The `{"type":"object"}` placeholder needs `RawInputSchema` (verified empirically)

**Finding** (probe run against mcp-go v0.57.0 in this tree): the plan's first
draft asserted that "`mcp.NewTool` already produces" the permissive placeholder.
It does not. `ToolInputSchema.MarshalJSON` →
`toolArgumentsSchemaMarshalJSON` (`mcp/tools.go:765-792`) **unconditionally**
writes `properties` (as `{}` when nil) and `required` (as `[]` when empty), so a
schemaless `mcp.NewTool` entry serializes as:

```json
"inputSchema":{"properties":{},"required":[],"type":"object"}
```

That is not the FR-004 wire shape, and it is not cosmetic: an empty declared
`properties` map is exactly the arg-pruning hazard FR-004's "never literal `{}`"
rule exists to avoid — a client that prunes arguments to the declared properties
would drop **every** argument.

**Decision**: deferred entries are built with
`mcp.NewToolWithRawSchema(displayName, description, json.RawMessage(`{"type":"object"}`))`
(`mcp/tools.go:877`), which marshals to exactly `"inputSchema":{"type":"object"}`.
Two traps the implementation MUST honor, both verified:

1. `NewToolWithRawSchema` takes **no** `ToolOption`s and leaves `Annotations`
   zero — FR-004 requires annotations preserved, so the deferred builder copies
   the annotation fields onto the returned `mcp.Tool` explicitly (the same five
   hints `buildDirectModeTools` applies today, `mcp_routing.go:99-115`).
2. Setting `RawInputSchema` on a tool that already went through `mcp.NewTool`
   is a **hard marshal failure**, not a silent override: `Tool.MarshalJSON`
   (`mcp/tools.go:677-680`) returns `errToolSchemaConflict` when both
   `InputSchema.Type` and `RawInputSchema` are set, which would break the whole
   `tools/list` response. Full-mode entries keep the `NewTool` path unchanged
   (FR-015); only the deferred branch uses the raw-schema constructor.

A unit test asserts the exact marshalled bytes of a deferred entry's
`inputSchema` and that a deferred entry marshals without error.

## R12 — Direct-catalog test seam (`upstream.Manager` is concrete)

**Finding**: `MCPProxyServer.upstreamManager` is a concrete `*upstream.Manager`
(`mcp.go:121`), not an interface, and `buildDirectModeTools` calls
`p.upstreamManager.DiscoverTools` directly (`mcp_routing.go:80`).
`discoverTools` only returns tools from **connected** clients
(`upstream/manager.go:1160-1176`), so no unit test can produce a deterministic
multi-tool direct listing today. That blocks most of the plan's unit matrix
(deferred rendering, full↔deferred set identity, collision determinism,
describe/listing parity, the FR-015 fixture capture) — the one exception is the
new direct built-in golden, which is specified over **zero** upstream tools and
works with the existing `createTestMCPProxyServer` harness.

**Decision**: split `buildDirectModeTools` into a thin I/O wrapper plus two pure
functions the tests drive directly:

```go
func (p *MCPProxyServer) buildDirectCatalog(tools []*config.ToolMetadata, mode string) *directCatalog
func (p *MCPProxyServer) renderDirectTools(cat *directCatalog) []mcpserver.ServerTool
func (p *MCPProxyServer) buildDirectModeTools() []mcpserver.ServerTool // DiscoverTools → buildDirectCatalog → renderDirectTools → publish
```

The unit matrix feeds `buildDirectCatalog` a fixture `[]*config.ToolMetadata`
slice (including the `__`-in-server-name collision pair) and asserts over
`renderDirectTools` output. No interface extraction, no new dependency, and
`buildDirectModeTools` keeps its current signature and call sites.

## R7 — Config-field wiring points (verified against this tree)

A new config field is silently inert unless all four are wired:
1. **Hot-reload detection**: `DetectConfigChanges`
   (`internal/runtime/config_hotreload.go:44`) needs a
   `direct_tool_response_mode` clause (the `tool_response_mode` clause is at
   :157 — same pattern), else an apply of only this field reports "no changes".
2. **Contract regen**: `make swagger` after the config struct change (swaggo v2;
   generated artifacts are committed).
3. **Env override**: `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE` in
   `internal/config/loader.go` beside the Spec-085 alias (:717), validated by
   `cfg.Validate()` after overrides.
4. **Docs**: `docs/configuration.md` (+ the feature doc, plan §Documentation).

Additionally FR-002: the `routing_mode` validation error gains a special case for
the literal `schema_deferred` naming the supported composition (`config.go:2229`
area is the analogous `tool_response_mode` block; `routing_mode` validation sits
nearby).

## R8 — Rebuild + notification mechanics (FR-014, FR-018)

**Findings** (mcp-go v0.57.0, the pinned version in go.mod):
- `MCPServer.SetTools` replaces the tool map wholesale and **automatically emits
  `notifications/tools/list_changed` to all initialized sessions** when the tools
  capability declares listChanged (it does: `WithToolCapabilities(true)`). So
  FR-014's notification comes free with the rebuild; the "no-op on unrelated
  edits" requirement means *guarding the rebuild call*, not suppressing a
  notification.
- The `config.reloaded` branch of `listenForRoutingModeRefresh`
  (`internal/server/server.go:554`) currently calls only
  `reapplyScannerSecurityConfig()` and `RefreshPrompts()` — confirmed: no direct
  rebuild, exactly as the spec asserts. The fix is one call in this branch to a
  new guard method that compares the serialization mode the current direct tool
  set was built with (recorded on the catalog snapshot, R9) against the live
  effective mode (`p.currentConfig()`, the live snapshot per
  `profile_resolver.go:40` — never the construction-time `p.config`), and calls
  `RefreshDirectModeTools()` only on a real change. This runs on the same single
  listener goroutine as the servers.changed rebuild, so no new reentrancy.
- `SetTools` panics on task-tool name collisions and last-writer-wins on
  duplicate names — motivates the deterministic collision guard in R9.
- `applyStrictInputSchemaDefault` (`server/server.go:884`) is inert here
  (mcpproxy does not set `WithStrictInputSchemaDefault`), so `SetTools` injects
  no `additionalProperties: false`. Note this is *not* what produces the
  `{"type":"object"}` wire shape — see R11: the placeholder requires
  `NewToolWithRawSchema`, and note further that `applyStrictInputSchemaDefault`
  early-returns on `len(tool.RawInputSchema) > 0`, so a raw-schema entry would
  stay permissive even if that option were ever enabled.
- **Rebuild guard and the empty catalog**: `buildDirectModeTools` returns `nil`
  and clears permissions when `DiscoverTools` errors (`mcp_routing.go:81-85`).
  The catalog MUST still be published in that case (zero entries, recording the
  mode it was built with), so the FR-014 guard always has a mode to compare
  against; a nil/absent catalog is treated as "mode unknown" and rebuilds
  unconditionally, so a flip during an upstream outage is never lost.
- FR-018: `RefreshDirectModeTools` → `SetTools(buildDirectModeTools()...)` — the
  built list is 100% upstream-derived today (`mcp_routing.go:76`), so
  `describe_tool` must be appended inside `buildDirectModeTools` (tool-set
  construction), never registered once beside it.

## R9 — FR-017 catalog authority: one snapshot, four consumers

**Finding** (confirming the spec's divergence analysis): the direct listing is a
projection of `upstreamManager.DiscoverTools` (server-level filtering only), while
`describe_tool`'s existing resolver (`toolVisibleToSession`,
`mcp_visibility.go:51`) starts with `toolIndexed` — index-backed and filtered by
tool-level approval at index time — and the signature cache is warmed only from
indexed tools. A pending/changed tool is therefore listed but unindexed: no
schema, no signature, index-backed describe says not_found. Also,
`makeDirectModeHandler` closes over `(serverName, toolName)` but *not* over the
schema it advertised; validation resolved through the index could see a different
definition during refresh skew.

**Decision**: introduce a `directCatalog` — an immutable per-rebuild snapshot
(entry: display name, `(server, tool)` pair, description, `ParamsJSON`,
`OutputSchemaJSON`, `Hash`, annotations, required permission) built inside the
direct rebuild from the same `DiscoverTools` result the listing renders from,
stored via atomic pointer swap on `MCPProxyServer`, absorbing today's
`directToolPermissions` map. Consumers: (1) listing rendering (both modes), (2)
signature `Peek` by entry hash, (3) direct-surface describe_tool resolution
(display-name map = the registration mapping; no re-parsing), (4) pre-dispatch
validation — the handler closure captures its own entry's `ParamsJSON`+`Hash` at
build time, so it validates against exactly what it advertised. Collisions
(`a`+`b__c` vs `a__b`+`c`): the catalog build iterates a deterministically sorted
tool list, keeps the first writer, and logs a warning — mirroring the F7 prompt
guard (`buildAggregatedServerPrompts`) — so describe and dispatch agree on the
kept entry by construction.

## R10 — Direct-surface describe_tool visibility parity (FR-011)

**Finding**: the direct listing's filters are `filterDirectModeToolsForAuth`
(`mcp_direct_scope.go:61` — **profile scope for every auth type**, then token
server scope, then the operation-permission tier via
`lookupDirectToolPermission`) and `filterDirectToolsForAgentCallability`
(`mcp_direct_callability.go:49` — agent sessions only), both registered on
`p.directServer` (`mcp_routing.go:616-618`); profile scope is additionally
re-checked at dispatch (`mcp_routing.go:191`). `toolVisibleToSession`
(`mcp_visibility.go:51`) checks scope + callability but **no permission tier**,
and answers existence-confirming reason codes
(`quarantined`/`pending_approval`/`changed`/`disabled`) — both divergences the
spec names are real in this tree. (The step helpers live in three files, not one:
`serverInScope` in `mcp_visibility.go:150`, `evaluateToolGate` in
`tool_gate.go:80`, `isToolCallable` in `mcp.go:6068`.)

**Four parity leaks the first draft did not close.** "Listing parity" is not
achieved by building a correct resolver alone — the *listing* side, the
definition assembly, and two suggestion paths must all be brought onto the same
catalog:

1. **The listing filters still re-parse the display name.** Both filters resolve
   `(server, tool)` via `ParseDirectToolName` (first-`__` split;
   `mcp_direct_scope.go:76`, `mcp_direct_callability.go:65`), and
   `filterDirectModeToolsForAuth` looks the permission up by display name. For a
   server named `a__b` with tool `c` the filter evaluates a *nonexistent* server
   `a` while the catalog-backed describe resolver evaluates the real pair
   `a__b`/`c` — so describe can return a definition for a tool the same session's
   listing dropped, which is precisely the FR-011 violation this decision exists
   to prevent. **Decision**: both filters resolve through the catalog's
   `byDisplayName` map and **deny on a catalog miss**, so listing and describe
   agree by construction. Built-ins keep passing through, but matched against an
   explicit built-in name set (today `describe_tool`) — never inferred from
   "absent from the catalog", which during the R13 skew window would let a stale
   upstream entry skip the permission-tier gate entirely. This makes `mcp_direct_scope.go` and
   `mcp_direct_callability.go` in-scope files, and retires
   `lookupDirectToolPermission`'s separate map in favor of the catalog entry's
   `requiredPermission` (FR-017).
2. **`check:true` suggestions (`did_you_mean`) come from a server-level corpus.**
   The shared preflight evaluator builds them in
   `visibleCorpus.candidates()` (`internal/preflight/evaluator.go:594-650`),
   which filters only by token server scope and server policy
   (found/quarantined/enabled) — **no operation-permission tier and no
   tool-level callability/approval gate** — and they are surfaced to the caller
   (`mcp_describe_check.go:294`, `did_you_mean`). Swapping the *id gate* to
   catalog membership does not touch this corpus, so a read-scoped token's
   `not_found` could name destructive tools absent from its own direct
   `tools/list` (FR-011, SC-005). The root cause is structural, not incidental:
   `preflight.Scope` is a **server-name set** (`NewScope(profileName, allowed)`,
   built by `sessionPreflightScope` from `serverInScope`,
   `preflight_glue.go:209-226`), so the evaluator has no way to express a
   per-tool gate. **Decision — and the injection point exists**:
   `preflight.EvalContext.Index` is an interface, supplied today as
   `&preflightIndexReader{index: p.index, annotations: annotations}`
   (`preflight_glue.go:103`, reader at `:284`). The direct surface passes a
   `directCatalogIndexReader` backed by the catalog snapshot and filtered by the
   listing-parity gates instead, so both the id resolution and the `did_you_mean`
   corpus come from the same authority (FR-017) with no change to the evaluator
   itself. Suppressing `did_you_mean` on this surface is the fallback only if
   that reader turns out to be load-bearing for a verdict path. A test asserts a
   read-scoped token receives no suggestion naming a destructive tool.

   **The same adapter must canonicalize the id, or check mode is broken for
   every direct id** (cross-model review, verified): ids reach the evaluator
   untouched — `normalizeDescribeCheckIDs` forwards them verbatim
   (`mcp_describe_check.go:192-204`) and `RunPreflightForSession` hands them
   straight to `preflight.Evaluate` (`preflight_glue.go:182-188`) — and the
   evaluator accepts colon ids ONLY: `splitToolID` is a
   `strings.SplitN(id, ":", 2)` (`evaluator.go:500-510`) and `evaluateOne`
   turns a non-split id into `not_found` with `detailMalformedID`
   (`evaluator.go:205-212`). So `github__create_issue` under `check:true` would
   answer `not_found` today no matter what the id gate does. The direct
   check-mode adapter therefore MUST, per id, in this order: (1) resolve the id
   against the catalog by display name or canonical name; (2) apply the
   listing-parity gates, answering plain `not_found` for anything invisible
   **without** consulting the evaluator; (3) canonicalize the survivors to
   `server:tool` before `Evaluate`; (4) restore the caller's original id string
   and the requested ordering in both the response entries and the activity
   record, so an agent that sent `server__tool` gets `server__tool` back. No
   change to `internal/preflight/evaluator.go` is required under this design —
   the adapter lives on the server side — which is why the evaluator is
   deliberately absent from the file-touch list; if implementation finds a
   verdict path that cannot be expressed through the injected `IndexReader`,
   adding an evaluator seam becomes a scope change to record, not a silent edit.
3. **The definition's annotations are not read from the entry you pass in.**
   `buildFullToolEntry` (`mcp_entry_builder.go`) uses `result.Tool` only for
   name/description/inputSchema/server, and resolves annotations through
   `p.lookupToolAnnotations` (`mcp.go:6344`), which reads the **StateView
   snapshot** — not `result.Tool.Annotations`. `call_with` is then derived from
   whatever that lookup returned, defaulting to `contracts.ToolVariantRead` when
   it returns nil. So synthesizing a `*config.ToolMetadata` from the catalog and
   handing it to `buildToolEntry` would still resolve annotations out-of-band,
   re-introducing the exact index/StateView dependency FR-017 removes — and for
   the listed-but-unindexed pending/changed tool that SC-007 targets, the
   definition would come back with **no annotations and `call_with: "read"` for a
   destructive tool**. That is worse than a missing field: it is a wrong safety
   hint. **Decision**: the definition-assembly seam takes an optional
   annotations override, supplied from the catalog entry on the direct surface
   only; the retrieve_tools surfaces pass nothing and keep the StateView lookup
   byte-identical (protecting the `retrieve_full_default` golden and the
   describe_plain_corpus gate). A test asserts a listed pending destructive tool
   describes with its real annotations and `call_with: "destructive"`.
4. **Definition-mode case-correction suggestions.** `suggestCanonicalToolID`
   (`mcp_visibility.go:204`) is invoked on the definition-mode not-found path
   (`mcp_describe_tool.go:202-206`) and gates its suggestion with
   `toolVisibleToSession` — the index-backed, permission-tier-blind resolver.
   **Decision**: the direct surface uses the catalog-backed resolver for that
   gate too (or omits the suggestion), same rule and same test.

**Decision**: a direct-surface resolver (`directToolVisibleToSession`) built from
the catalog + the same step helpers (`serverInScope`, `evaluateToolGate`,
`isToolCallable`) with the mode split of FR-011: invisible-to-this-session →
plain `not_found` in both modes; visible → definition-mode always renders the
snapshot-backed definition (even pending / changed / tool-level-disabled states,
which non-agent direct listings retain — **server-level** quarantined or disabled
servers are dropped by `DiscoverTools` before projection
(`upstream/manager.go:1128-1138`) and so are simply unlisted and `not_found`
here, contract §3 note); `check: true` delegates to the shared
spec-098/099 preflight evaluator for the informative verdict, with the id gate
swapped to catalog membership so a listed-but-unindexed id is never short-circuited to
`not_found`. Retrieve-surface semantics untouched: `handleDescribeTool` gains a
per-surface resolver seam (the registration passes it), and the existing surfaces
keep the index-backed resolver byte-for-byte.
