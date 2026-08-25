# Phase 0 Research: Schema-Deferred Direct Mode

**Spec**: [spec.md](spec.md) · **Plan**: [plan.md](plan.md)

Resolves the three `[NEEDS CLARIFICATION]` markers in the spec (R1–R3), records the
channel decision FR-007 demands (R4), and pins the mechanical findings the plan's
design rests on (R5–R13). Every code reference was verified against this tree
(base: `origin/main`, post-#1026). R11–R13 and the D9–D14 decisions were added
during self-review and two cross-model review rounds; each states the claim it
corrects.

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
modes, phrased conditionally so it is true in both.

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
instructions are the operator's `cfg.Instructions` when non-empty, **otherwise a
direct-specific default** — NOT `resolveInstructions`'s built-in
`defaultInstructions`, whose DISCOVERY line reads "Use 'retrieve_tools' to search
for tools by description" and whose CALLING line names `call_tool_read/write/
destructive` (`mcp.go:72-80`). None of those tools exist on the direct surface
(`buildDirectModeTools` is upstream tools plus `describe_tool`), so importing
that text would put the in-band guidance FR-007 exists to provide in direct
contradiction with the listing, pointing agents at a nonexistent tool. The
direct-specific default keeps only what this surface actually exposes — the
`server__tool` naming, `describe_tool`, and the ABOUT links — and the deferral
legend follows after a blank line. It must NOT mention `upstream_servers`
either: management tools are registered by the code-exec and call-tool builders
only (`mcp_routing.go:398`, `:507`), never by `buildDirectModeTools`, so naming
it would repeat the very defect this decision fixes. One helper,
`resolveDirectInstructions(custom string)`, is the single place this composition
lives, so `resolveInstructions` keeps its current callers and bytes untouched.
Operator text, when set, still leads and is never rewritten.
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

| Window | catalog published first | `SetTools` first (**chosen**) |
|---|---|---|
| Tool removed in the new generation | listing still shows it (old registry) while the filter finds no catalog entry — a *leak* unless the filter denies | already out of the registry: not listed, and rule 4's `GetTool` check makes describe answer `not_found`. **Window-free.** |
| Tool added in the new generation | not listed; describe would resolve from the new catalog → describable-but-unlisted | listed, but the old catalog has no entry → rule 2 denies it for filtered sessions and rule 4 answers `not_found`. **Fails closed.** |
| Same name, definition changed | filter/describe run ahead of the registry | registry runs ahead of the catalog: describe briefly returns the *previous* definition — which is the one the caller's own listing advertised. Accepted; see below. |

`SetTools` first wins on every row: removal becomes window-free and addition
fails closed, whereas catalog-first makes removal a potential leak. The two
publications sit adjacent in the same function with nothing between them.

**What cannot be built, stated plainly.** There is no way to stamp the generation
onto the registered entry and compare it at read time: `mcp.Tool`'s only
free-form field is `Meta`, which marshals to `_meta` on the wire
(`mcp/tools.go` `Tool.Meta`), so using it would move the very bytes FR-015
freezes. And mcp-go takes its own snapshot of `s.tools` under `toolsMu` *before*
invoking our tool filters, so no lock of ours can span the registry read either.
The plan therefore does not promise a single transaction; it promises a
**safety property** and enumerates the residual:

> No request observes a state less restrictive than **both** generations, and no
> request receives a definition for a name the registry is not currently serving.

**Decision** — five rules that deliver that property:

1. **`SetTools` runs first, the catalog is published immediately after**, in the
   same function with nothing between them. Note what this forbids: the *builder*
   must not publish. `buildDirectCatalog` returns the snapshot,
   `renderDirectTools` turns it into `[]ServerTool`, and only
   `RefreshDirectModeTools` — the single publisher, which the D15 initial rebuild
   calls rather than duplicating — publishes, after its `SetTools` call. Today's `buildDirectModeTools` does the opposite, calling
   `setDirectToolPermissions` mid-build (`mcp_routing.go:82`, `:151`) before
   `RefreshDirectModeTools` reaches `SetTools` (`:673`); that ordering is exactly
   what this rule reverses. This is the ordering that makes
   *removal* window-free (a removed name leaves the registry before the catalog
   stops describing it, and rule **4**'s registry check then answers
   `not_found`), and it makes an *addition* fail closed (listed but not yet in
   the catalog → rule 2 denies it for filtered sessions, rule **4** answers
   `not_found`).
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
3. **The permission tier comes from the catalog entry's `requiredPermission`,
   which is derived from the UPSTREAM annotations — never from the registered
   `mcp.Tool.Annotations`.** *(Corrected in cross-model review round 8; the
   round-5 draft said the opposite, and it was wrong twice over.)*

   The draft rule read the tier off the entry the filter is filtering, via
   `contracts.DeriveCallWith(tool.Annotations)`, "exactly as dispatch does". Two
   verified defects:

   - **Type.** `DeriveCallWith` takes `*config.ToolAnnotations`
     (`internal/contracts/intent.go:208`). The registered entry carries
     `mcp.ToolAnnotation` — a different type. Dispatch calls it on the
     **upstream** `*config.ToolAnnotations` the handler closed over
     (`mcp_routing.go:211-213`), not on anything registered.
   - **Semantics, and this one is severe.** `mcp.NewTool` seeds every tool with
     `ReadOnlyHint=false, DestructiveHint=true, IdempotentHint=false,
     OpenWorldHint=true` (`mcp/tools.go` `NewTool`), and each
     `With…HintAnnotation` option overwrites only its own field. So a read-only
     upstream tool registers with `readOnlyHint=true` **and** the untouched
     default `destructiveHint=true`. `DeriveCallWith` gives destructive top
     priority, so reading the tier off the registered entry would classify
     nearly every direct tool as **destructive**, hiding it from every read- and
     write-scoped agent token — and diverging from dispatch, which would still
     admit the call as `read`. That is a listing↔dispatch divergence, precisely
     the class of defect this design exists to remove.

   **Rule as it now stands**: the tier is the catalog entry's
   `requiredPermission` (`requiredPermissionForDirectTool(upstreamAnnotations)`,
   `mcp_direct_scope.go:18` — byte-identical to today's behavior and to
   dispatch's), used **only after the rule-5 discriminator has confirmed the
   catalog entry and the registered entry are the same generation**. The
   discriminator, not the annotation source, is what buys generation-correctness
   here.

   **Residual this creates, stated exactly**: an **annotations-only** change
   whose rendered description is byte-identical passes the discriminator, so for
   the duration of the window the listing and describe may gate on the previous
   generation's tier. It is bounded the same way the schema-only residual is:
   **dispatch is never wrong** — `makeDirectModeHandler` re-derives the tier from
   the annotations *its own* registration captured (`mcp_routing.go:211-213`), so
   a call is never mis-authorized, only a listing can be one generation stale.
   This is the third and last item on the residual list below; it needs the same
   maintainer assent as the other two. The catalog is also still the source for
   the `(server, tool)` split (rule 2) — that is what `ParseDirectToolName` gets
   wrong.
4. **Describe requires registry membership as well as catalog visibility.**
   Before returning a definition the direct resolver checks
   `p.directServer.GetTool(displayName) != nil` (mcp-go
   `server/server.go:1023`, an O(1) read of the very map the listing is served
   from). Catalog visibility ∧ registry presence closes describable-but-unlisted
   in *both* orderings, and it strengthens rather than weakens FR-017's
   "membership is decided by the direct-surface snapshot, not index presence" —
   the registry is the listing's own source, not the index. The describe
   resolver's permission tier follows rule 3 exactly — the catalog entry's
   `requiredPermission`, after the rule-5 discriminator — for the same reason
   rule 3 gives: the registered entry's `mcp.ToolAnnotation` carries mcp-go's
   constructor defaults and cannot be used as a tier source. `GetTool` is
   consulted here **only** for registry membership, never for annotations.
5. **A display name never denotes two origins — within a generation, or across
   the window.** Two mechanisms, because the hazard has two shapes:
   - *Within* a generation: display-name collisions (`a` + `b__c` vs `a__b` + `c`)
     are **withheld — neither entry is registered** — with a warning naming both
     pairs, replacing the first-writer-wins guard the earlier draft proposed. The
     spec sanctions this explicitly ("MUST resolve a colliding id deterministically
     **or report it** rather than guess", Edge Cases), and it is strictly safer
     than today's behavior, which is not merely non-deterministic but *undefined*:
     `buildDirectModeTools` appends both entries and `SetTools` collapses them
     last-writer-wins into a map whose input order comes from a map iteration over
     `m.clients`. Cost: two genuinely colliding tools become unreachable via the
     direct surface until an operator renames one — a documented, logged,
     pathological case, and the canonical `server:tool` route is unaffected.
   - *Across* the window: a name can still change origin without ever colliding —
     server A removed and server B added in one reconcile, where B's tool flattens
     to A's old display name. Neither generation has a collision, yet the old
     catalog's `(server, tool)` would be used to scope-check the new registered
     entry. Closed by a **generation discriminator**: both the listing filters and
     the describe resolver compare the entry's **stored** `renderedDescription`
     — the exact string `renderDirectTools` handed to `SetTools` for that entry,
     captured on the entry at render time — against the registered
     `mcp.Tool.Description`, and **fail closed** (deny / `not_found`) on a
     mismatch. One string comparison, no wire change.

     It must be the stored value, never a re-render. The deferred suffix comes
     from `toolsig.Cache`, which mutates on its own schedule — `Warm` adds
     entries at index time and `RetainHashes` evicts them
     (`internal/toolsig/cache.go:79`, `:108`), neither of which triggers a direct
     rebuild. Recomputing the description at filter or describe time would
     therefore flip after a cache miss→warm (or hit→eviction) with no generation
     change at all, manufacturing a false mismatch that leaves a still-registered
     tool unlisted and undescribable **until the next rebuild** — a persistent
     "listed ⇒ describable" violation (SC-007), far worse than the microsecond
     window the discriminator exists to police. Comparing two immutable snapshots
     has no such failure mode.

   **What the discriminator does NOT catch** (cross-model review round 6,
   correcting an over-broad claim in the round-5 draft that it "subsumes" the
   schema-change case): a **schema-only** change leaves the description
   identical. The direct description is `fmt.Sprintf("[%s] %s", ServerName,
   Description)` (`mcp_routing.go:95`) in full mode, and in deferred mode the
   appended signature is lossy by design, so a nested-property edit can render
   the same `~`. No field on the registered `mcp.Tool` reliably encodes the
   schema in *both* modes either — deferred advertises the same
   `{"type":"object"}` placeholder for every tool — and `Tool.Meta` is
   wire-visible, so a hash stamp is closed off by FR-015 (see the top of this
   section). The identity/origin hazards ARE fully closed; the schema-only one is
   not, and it is recorded as a residual rather than papered over.

**Residual, stated exactly** (rounds 5, 6, 8 and 9). Four different things happen
in the window, and three of them are still open:

- *Anything the discriminator sees* — an origin flip, a description change, a
  signature change — is **withheld**: the filters drop it and describe answers
  `not_found`, rather than being answered from the stale generation. That is
  availability loss, not correctness loss: fail-closed, self-correcting on the
  next instruction, and bounded from the client's side by the
  `tools/list_changed` notification `SetTools` has already emitted.
**What is invisible to the discriminator, field by field** (round 9, corrected in
rounds 10 and 11).

What the renderer actually reads, stated exactly: in **full** mode
`fmt.Sprintf("[%s] %s", serverName, description)` (`mcp_routing.go:95`); in
**deferred** mode that same string plus `"\n" + toolName +
toolsig.Render(paramsJSON, description).Sig` when `Peek(hash)` hits, and nothing
appended when it misses. So `renderedDescription` is a function of `serverName`,
`toolName`, `description`, `paramsJSON`, the serialization mode, and whether the
signature cache held `hash` at render time — **not** of `description` alone.

**Scope of this walk**: it covers the per-entry *definition* fields, because the
discriminator is a per-name comparison. The catalog-level fields are generation
state, not definition data, and are accounted for separately: `entries`,
`byDisplayName` and `byCanonical` express **membership**, whose changes are the
add / remove / origin-flip / collision cases already in the test matrix — a name
that leaves or joins them is caught by rule 2 (deny on catalog miss) and rule 4
(registry membership), not by the description comparison; `serializationMode` is
*visible* (a flip adds or removes the whole signature suffix, which is why the
FR-014 guard rebuilds on it); and `generation` is never rendered and is
observability only. None of them is an invisible per-entry skew axis.

Now the entry itself, split into **source** fields (set from the upstream
projection) and **derived** fields, because only the source fields are
independent axes of change:

*Source fields.*

| Field | Visible in a rendered-description comparison? |
|---|---|
| `serverName`, `toolName` | Yes. `serverName` is in the `[server]` prefix in both modes; `toolName` is in the deferred suffix. `displayName` is derived from the pair (`serverName__toolName`) and cannot move on its own. |
| `description` | Yes, by construction — it *is* the rendered string in full mode. |
| `paramsJSON` | **No, not reliably.** Never rendered in full mode. In deferred mode it usually moves the signature, but an edit that renders the same `Sig` — a re-ordering, or a change confined to nested properties the grammar collapses to `~` — is invisible. |
| `outputSchemaJSON` | **No.** Never rendered in either mode. It moves the Spec-032 hash, so it can flip a `Peek` hit to a miss and change the suffix that way — but only until the indexer warms the new hash, after which `Render` reproduces the identical `Sig` (it never reads the output schema). |
| `annotations` | **No.** Never rendered in either mode. |

*Derived fields — no new axis.* The property that matters is one-directional:
each **cannot move unless one of its sources moves**, so none of them adds a
fourth independent case. The converse does not hold, and the plan does not claim
it.

| Field | Derived from | Note |
|---|---|---|
| `renderedDescription` | the renderer inputs listed above, at render time | it *is* the discriminator |
| `hash` | server, tool, description, input schema, output schema (`hash.ToolHashWithOutputSchema`, `internal/hash/hash.go:10-16`) — **not** annotations | schemas are canonicalized first (`:59-104`), so a purely representational schema edit leaves the hash — and therefore the cached signature — untouched. That is what makes the input-schema-only skew case constructible in a test. |
| `requiredPermission` | `annotations`, via `requiredPermissionForDirectTool` (`mcp_direct_scope.go:18`) → `contracts.DeriveCallWith` | it reads only `destructiveHint` and `readOnlyHint` (`internal/contracts/intent.go:208-227`), so a title / idempotent / open-world edit moves `annotations` without moving the tier. It is the *mechanism* of the annotations residual, not a fourth one. |

So there are exactly **three independent source fields** that can change while the
rendered description stays byte-identical — `paramsJSON`, `outputSchemaJSON`,
`annotations` — and each is enumerated as a residual below. The skew tests drive
those three classes (the annotations case using a `read → destructive` edit,
since that is the sub-change the tier is sensitive to) and assert the derived
consequences.

- *A schema-only change* (`paramsJSON`) passes the discriminator, so for those few
  instructions `describe_tool` may return the **previous** generation's
  `inputSchema` for a name whose registered handler already carries the new one.
  (Note that in deferred mode a `paramsJSON` edit usually *is* visible, because it
  moves the Spec-032 hash and the rendered signature with it — but not reliably:
  a re-ordered or semantically-equal edit can render the identical signature, and
  full mode never renders the schema at all.) Its blast radius is small
  and, importantly, is absorbed by a mechanism this very feature ships: dispatch
  is never wrong (the handler validates against the schema it captured, R9), so
  an agent that acted on the stale definition is rejected by the pre-dispatch
  validator with the **correct** schema embedded (FR-012/FR-013) and succeeds on
  one retry — precisely the US3/SC-003 self-healing path. Cost: one extra round
  trip, in a microsecond window, for a tool whose schema changed mid-refresh.
- *An output-schema-only change* (`outputSchemaJSON`) is invisible for the same
  reason, and — unlike the input schema — **the pre-dispatch validator does not
  absorb it**: nothing validates against an output schema, so there is no
  self-healing retry here. Stated plainly rather than folded into the row above
  (round 9): for the width of the window `describe_tool` may return the previous
  generation's `output_schema` for a tool that now declares a different one. The
  consequence is bounded by what an output schema *is* on this protocol —
  advisory shape metadata for structured content, which MCP permits without any
  declared schema at all (R2). A stale one cannot cause a call to be
  mis-authorized, mis-validated, or rejected; at worst an agent parses the
  response against last generation's shape. It self-corrects at the catalog
  publish microseconds later, and a client that re-reads after the
  `tools/list_changed` notification never sees it.
- *An annotations-only change* whose rendered description is byte-identical also
  passes the discriminator (round 8, with rule 3's correction), so the listing
  filters and the describe resolver may gate on the **previous** generation's
  permission tier for those few instructions. Bounded identically: the tier that
  actually authorizes a call is re-derived inside `makeDirectModeHandler` from
  the annotations *its own* registration captured (`mcp_routing.go:211-213`), so
  no call is ever mis-authorized — only a listing/describe decision can be one
  generation stale, and it self-corrects at the catalog publish that follows
  microseconds later.

What is explicitly NOT residual any more: no session can be scope-checked against
one origin and dispatched to another; no display name can denote two origins, in
any interleaving; and no *call* can be authorized against a tier the registered
handler does not itself derive.

**These three residuals narrow FR-017's "no window exposes one without the
other", and the annotations-only one additionally narrows FR-011/SC-005 while the
two schema ones narrow FR-017/SC-007. All are recorded in plan §Complexity
Tracking and cross-referenced from spec.md FR-017 for the maintainer's assent at
the tasks stage**, alongside the transaction narrowing — they are not to be
discovered later in a task.

The catalog carries a monotonically increasing `generation`. It is not
decorative: every publish logs it beside the entry count and the serialization
mode, and the skew tests below assert on it (each paused rebuild must show
exactly one increment, and a guarded no-op reload must show none), which is what
makes "did this request see the old or the new generation" observable rather than
inferred.

**Tests** — a rebuild paused between the two publications, with a concurrent
scoped `tools/list` and `describe_tool`, covering **eight** cases: an **added**
name, a **removed** name, a **same-name description-visible** change, a
**same-name origin flip** (server A removed and server B added in one reconcile,
B's tool flattening to A's old display name), a **within-generation collision**
(both entries withheld, warning logged, neither listed nor describable, in both
generations), and the three cases the discriminator cannot see: an
**input-schema-only change**, an **output-schema-only change**, and an
**annotations-only change** (read→destructive), each with the description and
rendered signature held byte-identical. Those three are exactly the *independent
source* fields that can move invisibly (the field table above; the derived
`hash` and `requiredPermission` add no fourth case), so the set doubles as the
proof of that enumeration. They assert the documented residuals behave as
claimed — the stale definition/tier may be returned, while dispatch still
validates against the new input schema and re-derives the new tier, so the
`invalid_params` error carries the NEW schema (one retry succeeds) and the
destructive call is still refused a read-scoped token at the handler; the
output-schema case asserts only that a stale advisory `output_schema` may be
returned and is corrected by the next publish. Plus the **two no-rebuild
cases** of rule 5: a signature-cache
**miss→warm** and a **hit→eviction** between registration and a later
filter/describe call (`toolsig.Cache.Warm` / `RetainHashes`), both of which must
leave the tool listed and describable — the proof that the discriminator reads
the stored `renderedDescription` rather than re-rendering.

Assertions across the set: no describable-but-unlisted id; no entry scope-checked
against one origin while its handler dispatches to another; no read-scoped token
having a destructive tool's **call** admitted; and no case where a stale
definition leads to a call that *succeeds* against the wrong schema.

## R14 — The direct surface is never initialized eagerly (FR-009/FR-014 gap)

**Finding** (cross-model review round 3, verified): `initRoutingModeServers`
registers the built-ins for the code-exec and call-tool servers but deliberately
registers **nothing** on the direct server:

> `// Note: Direct mode tools are built lazily/on-demand via RefreshDirectModeTools`
> `// because upstream servers may not be connected yet during initialization.`
> `// The servers.changed event will trigger a refresh.`
> (`mcp_routing.go:651-653`)

Composing `describe_tool` into `buildDirectModeTools` (FR-018) is therefore
necessary but **not sufficient**: until something calls
`RefreshDirectModeTools`, the direct server has an empty tool map and FR-009's
"present in BOTH serialization modes" is false. The exposure is worst in exactly
the configuration the new built-in golden models — **zero upstream servers**,
where a `servers.changed` refresh may never be a meaningful trigger — and the
golden itself does not catch it, because it asserts over
`buildDirectModeTools()`'s return value rather than over what `p.directServer`
actually serves.

It also interacts with the R8 nil-catalog rule: with no initial rebuild the
catalog stays nil, so "a nil catalog rebuilds unconditionally" turns the first
unrelated `config.reloaded` into a rebuild plus a `tools/list_changed` broadcast
— precisely the churn FR-014's no-op requirement forbids.

**Decision**:
1. `initRoutingModeServers` performs one **initial direct rebuild** by calling
   `RefreshDirectModeTools()` — the single publisher (R12), not a second copy of
   the build→`SetTools`→publish sequence — before the HTTP listeners are wired,
   so no session can ever observe an empty direct surface. With no upstreams connected yet this seeds exactly the built-in set
   (`describe_tool`) and an empty catalog recording the effective mode; the
   `servers.changed` refresh then replaces it normally. The stale comment at
   `:651-653` is updated rather than left contradicting the code.
2. Because the catalog is now always published after init, the nil-catalog
   "rebuild unconditionally" branch becomes unreachable in production and is kept
   only as the defensive path (R8, R13 rule 2's nil exception). FR-014's no-op
   guarantee then holds from the first reload onward.

   **This removes an accidental self-heal, so one ordering bug must be fixed with
   it** (cross-model review round 8, verified): the routing-refresh listener
   creates its subscription *inside* its own goroutine —
   `eventCh := s.runtime.SubscribeEvents()` is the first line of
   `listenForRoutingModeRefresh` (`server.go:528-530`) — and that goroutine is
   merely *scheduled* at `server.go:301`, one line before
   `s.runtime.StartBackgroundInitialization()` (`:302`). Nothing orders the
   subscription before the first publish, and the bus delivers only to
   subscribers already registered, so a fast background init can drop the first
   `servers.changed` outright. Today that is self-healing by accident: the
   catalog is nil, so the next `config.reloaded` rebuilds unconditionally. With
   D15 publishing an empty catalog at init and FR-014 guarding the reload on a
   mode comparison, the unconditional rebuild no longer fires and a dropped first
   event would leave the direct surface at built-ins-only until the next upstream
   change. **Decision**: `SubscribeEvents()` is hoisted into the constructor —
   called synchronously before `StartBackgroundInitialization()`, with the
   channel handed to the goroutine — so the subscription provably precedes any
   publish. `server.go` is already in the touch list for the FR-014 guard; this
   is the same file. A test asserts a `servers.changed` published immediately
   after construction still reaches the direct rebuild.
3. The new direct built-in gate asserts over **`p.directServer.ListTools()`** —
   what the server actually serves after init — not over
   `buildDirectModeTools()`'s return value, so it fails if the initial
   registration is ever dropped again. A companion test asserts an unrelated
   `config.reloaded` on a freshly initialized zero-upstream proxy triggers no
   `SetTools` and no notification.

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
   **zero-valued** — every hint pointer `nil`. Copying only the upstream hints
   onto it is NOT enough (cross-model review round 8): `mcp.NewTool` seeds a tool
   with `ReadOnlyHint=false, DestructiveHint=true, IdempotentHint=false,
   OpenWorldHint=true` before any option runs, and each `With…HintAnnotation`
   overwrites only its own field, so a full-mode entry whose upstream declares
   only `readOnlyHint:true` still marshals **all four** hints. A deferred entry
   built by copying just that one hint would marshal only `readOnlyHint`, and an
   entry with nil upstream annotations would marshal none at all — different
   wire bytes for the same tool in the two modes, violating FR-004 ("unchanged
   annotations") and FR-008 (set identity includes annotations).
   **Decision**: the deferred renderer seeds the returned `mcp.Tool.Annotations`
   with mcp-go's exact `NewTool` defaults and *then* applies the same five
   upstream overrides `buildDirectModeTools` applies today
   (`mcp_routing.go:99-115`). The unit matrix asserts the marshalled
   `annotations` object is byte-identical between the two modes for three
   fixtures: nil upstream annotations, partial (one hint), and full.
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
//   renderDirectTools also records each entry's renderedDescription on the
//   catalog before it is published, so the R13 rule 5 discriminator compares
//   two immutable snapshots rather than re-rendering against a mutable cache.

// DiscoverTools → buildDirectCatalog → renderDirectTools. Returns BOTH halves:
// the rendered tool set AND the catalog it was rendered from, unpublished.
func (p *MCPProxyServer) buildDirectModeTools() ([]mcpserver.ServerTool, *directCatalog)
```

**The return type must change, and this is load-bearing** (cross-model review
round 8): R13 rule 1 forbids the builder from publishing, so if
`buildDirectModeTools` still returned only `[]ServerTool` — as the round-7 draft
of this section said, "keeps its current signature and call sites" — the catalog
it built would be unreachable and `RefreshDirectModeTools` would have nothing to
publish after its `SetTools` call (`mcp_routing.go:666-673`). The two-value
return is what makes rule 1 expressible.

**There is exactly ONE publisher, and this is pinned deliberately.**
`RefreshDirectModeTools` is it; the D15 initial rebuild does not re-implement the
sequence, it *calls* `RefreshDirectModeTools()`. Two independent
build→`SetTools`→publish sequences would be two places for rule 1's ordering to
drift, for no gain — the init case differs only in that `DiscoverTools` returns
nothing yet. So the blast radius of the signature change is two call sites of the
*builder*: `RefreshDirectModeTools` (`mcp_routing.go:666`) and one test
(`mcp_describe_tool_test.go:369`, already being edited for FR-009); and one new
call site of the *publisher*: `initRoutingModeServers`.

The publisher reads:

```go
tools, cat := p.buildDirectModeTools()
p.directServer.SetTools(tools...)   // rule 1: registry first
p.publishDirectCatalog(cat)         // …catalog immediately after
```

`buildDirectModeTools` returns a non-nil catalog on **every** path, including the
`DiscoverTools` error path (`mcp_routing.go:81-85`, which returns `nil` today):
there it returns the built-ins-only tool set and an **empty catalog recording the
effective mode**, so FR-014's guard always has a mode to compare against and
FR-018's built-in survives an upstream outage.

The unit matrix feeds `buildDirectCatalog` a fixture `[]*config.ToolMetadata`
slice (including the `__`-in-server-name collision pair) and asserts over
`renderDirectTools` output. No interface extraction and no new dependency;
`buildDirectModeTools` keeps its name and its two call sites, and gains the
second return value the publication rule requires (above).

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
tool list and **withholds every colliding display name** — neither pair is
registered — logging a warning that names both origins. (An earlier draft kept
the first writer, mirroring the F7 prompt guard; round 5 showed that still lets
one display name denote different origins across a rebuild. See R13 rule 5.)
Describe and dispatch therefore agree by construction, because an ambiguous name
is never served at all.

**Scope of the atomicity this buys**: handler ↔ its own schema/hash, which is
what consumer (4) needs. It does NOT make the catalog swap and `SetTools` one
transaction — see [R13](#r13--two-publications-one-generation-closing-the-catalogregistry-skew-window)
for the filter/resolver skew window and the five rules that close it.

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
