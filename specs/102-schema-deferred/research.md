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
  are updated to enumerate the new field; no golden pins describe_tool *response*
  bytes (the goldens pin its tools/list *definition*, untouched by a response
  field).
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

> "Some tool descriptions end with a compact signature `(param*:type, …)` —
> `*` = required, `~` = collapsed/lossy. When a signature is present, the listed
> inputSchema is a placeholder; flat signatures are directly callable, and
> `describe_tool` returns the full schema for any listed tool."

(Exact wording finalized at implementation; budgeted and pinned by the new
direct-surface built-in gate — see plan §Test strategy.)

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

**Alternatives considered**: describe_tool description (rejected above); synthetic
pseudo-tool (forbidden by FR-007); per-entry legend repetition (token-multiplied
across every tool — defeats the feature).

## R5 — FR-009 prose: surface-neutral rewrite + one deliberate golden regen

**Decision**: Keep ONE `buildDescribeToolTool` builder; rewrite its description to
surface-neutral prose (drop "found via retrieve_tools"; e.g. "Return full JSON
Schema + long description for specific tools by id. …"), keeping it under the
250-token budget. Regenerate `toolslist_goldens/default_server.json` and
`toolslist_goldens/retrieve_tools_mode.json` **once**, as the FR-010-sanctioned
enumerated delta; extend `toolsListAllowedDelta` accordingly. The frozen `pre099/`
baseline and `code_execution_mode.json` are never touched.

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
- `applyStrictInputSchemaDefault` is inert here (mcpproxy does not set
  `WithStrictInputSchemaDefault`), so the `{"type":"object"}` placeholder passes
  through `SetTools` unmodified — no injected `additionalProperties: false`.
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
(token server scope + operation-permission tier via
`requiredPermissionForDirectTool`) and `filterDirectToolsForAgentCallability`,
both registered only on `p.directServer` (`mcp_routing.go:614-618`), plus profile
scope at dispatch. `toolVisibleToSession` checks scope + callability but **no
permission tier**, and answers existence-confirming reason codes
(`quarantined`/`pending_approval`/`changed`/`disabled`) — both divergences the
spec names are real in this tree.

**Decision**: a direct-surface resolver (`directToolVisibleToSession`) built from
the catalog + the same step helpers (`serverInScope`, `evaluateToolGate`,
`isToolCallable`) with the mode split of FR-011: invisible-to-this-session →
plain `not_found` in both modes; visible → definition-mode always renders the
snapshot-backed definition (even pending/changed/quarantined-listed states, which
non-agent direct listings retain); `check: true` delegates to the shared
spec-098/099 preflight evaluator for the informative verdict, with the id gate
swapped to catalog membership so a listed-but-unindexed id is never short-circuited to
`not_found`. Retrieve-surface semantics untouched: `handleDescribeTool` gains a
per-surface resolver seam (the registration passes it), and the existing surfaces
keep the index-backed resolver byte-for-byte.
