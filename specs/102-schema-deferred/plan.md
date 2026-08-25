# Implementation Plan: Schema-Deferred Direct Mode — Full Enumeration Without Schemas

**Branch**: `102-schema-deferred` | **Date**: 2026-08-25 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/102-schema-deferred/spec.md` (issue #971, accepted direction 2026-08-13)

## Summary

Add a second serialization mode to the **direct surface's `tools/list`**: with
`direct_tool_response_mode: "deferred"` (new dedicated config key, default `"full"`,
opt-in — research.md R1) every visible upstream tool is still listed with its
unchanged `serverName__toolName` name and annotations, but the entry ships the
existing `[server] …` description with its precompiled Spec-085 compact signature
appended and a minimal permissive `{"type":"object"}` input schema instead of the
upstream `inputSchema`/`outputSchema` (~30K → ~3.5–5K tokens per 100-tool listing).
`describe_tool` joins the direct surface (both modes) as the schema recovery stage,
and direct-mode dispatch gains the Spec-085 pre-dispatch validator so a wrong guess
costs exactly one self-healing retry. No new `routing_mode` value: `schema_deferred`
is rejected with a message naming this composition (FR-002).

Technical approach, grounded in the code:

- **Catalog snapshot (FR-017) is the load-bearing structure.** A new immutable
  `directCatalog` is built inside the direct rebuild from the same
  `upstreamManager.DiscoverTools` projection the listing already renders from
  (`mcp_routing.go:76` `buildDirectModeTools`), one entry per tool: display name,
  `(server, tool)` pair, description, `ParamsJSON`, `OutputSchemaJSON`, Spec-032
  `Hash`, annotations, required permission. Atomic-pointer swapped on
  `MCPProxyServer`; it absorbs today's `directToolPermissions` map and becomes the
  single source for (1) listing rendering in both modes, (2) signature lookup, (3)
  direct-surface `describe_tool` resolution, (4) pre-dispatch validation — the
  handler closure captures its own entry's schema+hash at build time, so a
  dispatch can never validate against a definition other than the one its own
  registration advertised (research.md R9). That covers handler↔schema; the
  catalog swap and `SetTools` are still two publications, so the filter/resolver
  skew window is bounded separately by R13's four rules (`SetTools` first then
  publish, filters deny on catalog miss, the permission tier derived from the
  entry's own annotations, describe requiring registry membership as well as
  catalog visibility) — a stated safety property, not a claim of atomicity. The
  catalog build iterates a sorted tool list and **withholds** every `__`
  display-name collision outright, logging both origins, so an ambiguous name is
  never served and dispatch registration, listing and describe resolution cannot
  disagree (D13 rule 5; today's code is undefined here, not merely
  non-deterministic).
- **Deferred rendering** reuses the Spec-085 signature machinery read-only: a new
  non-compiling `toolsig.Cache.Peek(hash)` (research.md R6) serves index-warmed
  signatures with observable misses — a miss lists the entry without a signature,
  never drops or delays it (FR-005). The permissive placeholder is **not** what
  `mcp.NewTool` produces: mcp-go's schema marshaller always emits
  `"properties":{}` and `"required":[]`, so deferred entries are built with
  `mcp.NewToolWithRawSchema(…, json.RawMessage(`{"type":"object"}`))` with
  annotations copied on explicitly, and the full-mode branch keeps the `NewTool`
  path untouched (verified empirically — research.md R11; mixing the two on one
  tool is a hard marshal error, not a silent override).
- **describe_tool on the direct surface** (FR-009/FR-011): registration is
  composed into direct tool-set *construction* (`buildDirectModeTools` appends it)
  so every `SetTools` refresh keeps it (FR-018) — including the
  `DiscoverTools`-error path, which returns `nil` early today
  (`mcp_routing.go:81-85`) and would otherwise drop the built-in on the first
  upstream hiccup. The built-in is appended on **every** return path, and the
  direct built-in golden's zero-upstream fixture is exactly that shape. One `buildDescribeToolTool`
  builder still feeds all surfaces — its **four** retrieve_tools-specific strings
  (tool description, both parameter descriptions, and the two shared remediation
  constants) are rewritten surface-neutral, the one-time enumerated golden regen
  of FR-010 plus named `describePlainDelta` substitutions (research.md R5). The
  `tool_ids` prose must also name both accepted id forms, since FR-011 requires
  `server__tool` here. The handler gains a
  per-surface resolver seam: existing surfaces keep the index-backed
  `toolVisibleToSession` unchanged; the direct surface resolves both id forms
  (canonical `server:tool`, and direct `server__tool` via the catalog's
  registration mapping — never by re-parsing) through a catalog-backed resolver
  with listing-parity visibility: token server scope + **operation-permission
  tier** + profile + agent callability. Invisible ids answer plain `not_found` in
  both modes; visible ids always get their snapshot-backed definition in
  definition mode, and the informative verdict (`pending_approval`/`changed`/…)
  under `check: true` via the shared spec-098/099 preflight evaluator, reached
  through an adapter that canonicalizes `server__tool` → `server:tool` first
  (the evaluator accepts colon ids only) and restores the caller's id and
  ordering on the way back (research.md R10, D14). Definitions additively carry `output_schema` when the tool
  declares one — added at the definition-assembly seam, NOT in
  `buildFullToolEntry`, so full-mode retrieve_tools bytes are untouched (R2).
- **Pre-dispatch validation** (FR-012/FR-013): `makeDirectModeHandler` calls the
  existing `p.inputValidator.validateArgs` (`mcp_input_validation.go`) against the
  catalog entry's stored `ParamsJSON` — never the advertised placeholder — after
  the callability gate and before `CallTool`, rendering the existing
  `invalidParamsErrorResult` (full schema + hint) on failure; fail-open per Spec
  085 FR-013b; non-argument failures keep their current shapes. **Exact
  placement**: immediately after `directToolCallabilityBlockWithReason`
  (`mcp_routing.go:235`) and before `markSessionWorked` — and, matching the Spec-085
  `call_tool_*` path (`mcp.go:2348-2349`), a validation failure emits the
  `emitActivityToolCallStarted` + `emitActivityToolCallCompleted("error", …)` pair
  so the availability funnel keeps the blind-spot-free property issue #969
  established for this handler. The unconditional
  `emitActivityToolCallStarted` at `:246` stays on the dispatch path only.
- **Rollout (FR-014)**: `SetTools` already emits `notifications/tools/list_changed`
  to all initialized sessions (verified in mcp-go v0.57.0 — R8), so the new wiring
  is one guarded call in the `config.reloaded` branch of
  `listenForRoutingModeRefresh` (`server.go:554`): compare the mode the current
  catalog was built with against the live effective mode (read via
  `p.currentConfig()`, never construction-time `p.config`) and rebuild only on a
  real change — an unrelated config edit rebuilds nothing and emits nothing.
- **Convention channel (FR-007)**: static MCP server instructions on the direct
  server instance (today it has none — only the default server carries
  instructions, `mcp.go:480`), conditionally phrased so they are true in both
  modes; the `initialize`-response delta is enumerated and pinned (R4). The value
  comes from one new helper — the operator's `cfg.Instructions` when non-empty,
  otherwise a **direct-specific default**, then the deferral legend — so the
  operator-configurable `instructions` key (`config.go:489`) is not silently
  unreachable here (D11) and the fallback never names a tool this surface does
  not expose (D16). It is deliberately NOT `resolveInstructions`, whose
  `defaultInstructions` advertises `retrieve_tools` and `call_tool_*`.

## Technical Context

**Language/Version**: Go 1.24 module toolchain (repo builds with local Go 1.25). Backend-only; no frontend, tray, or Swift changes.
**Primary Dependencies**: existing only — `github.com/mark3labs/mcp-go` v0.57.0 (tool registration, `SetTools` listChanged notification, `WithInstructions`), `github.com/santhosh-tekuri/jsonschema/v6` (already used by the Spec-085 validator being reused), `go.uber.org/zap`, stdlib. **No new dependencies.**
**Storage**: none changed — BBolt and Bleve schemas untouched; the `directCatalog` is in-memory per-rebuild state; `ToolMetadata.OutputSchemaJSON` is already indexed (`internal/index/bleve.go:39`).
**Testing**: `go test -race ./internal/...` (locally: `internal/server` with the CI skip regex); frozen-golden gates in `internal/server` (see Test strategy); E2E in `internal/server/e2e_test.go`; token measurements against the frozen 45-tool corpus via the spec-083/085 profiler pipeline.
**Target Platform**: Linux/macOS/Windows core binary, personal + server editions — pure `internal/`, edition-agnostic (no `//go:build server` code touched; server-edition build verified).
**Project Type**: Single Go project (core server).
**Performance Goals**: Constitution I unaffected — serialization + registration-time work only; signatures come from the index-warmed cache via a pure read (`Peek`), no per-request compilation (FR-005); validation is memoized per tool hash and cheaper than the upstream round trip it prevents.
**Constraints**: FR-015 byte-stability of full-mode direct listings; FR-010 existing goldens pass unregenerated except the two enumerated FR-009 regens; FR-008 identical tool-set membership across modes; FR-013b fail-open validation; SC-001 ≥70% token reduction on the 45-tool corpus.
**Scale/Scope**: ~6 packages touched: `internal/server` (core of the change), `internal/config`, `internal/runtime` (one clause), `internal/toolsig` (one method), `cmd/mcpproxy` (one flag), `bench/arms` (one measurement arm for the SC-001/SC-002 gates); plus docs and generated OpenAPI artifacts.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.* — Constitution v1.1.0.

| Principle | Assessment |
|-----------|------------|
| **I. Performance at Scale** | PASS. BM25 search and indexing untouched. The direct rebuild already runs on upstream changes; deferred rendering *removes* per-entry schema unmarshal work and reads signatures via a pure cache lookup. Pre-dispatch validation is memoized per tool hash (existing validator). |
| **II. Actor-Based Concurrency** | PASS (with one justified primitive). The catalog is written only on the single routing-refresh listener goroutine and read on request paths — an immutable snapshot behind an `atomic.Pointer` swap, replacing the existing mutex-guarded `directToolPermissions` map with the same read-mostly pattern Spec 085 justified for the signature cache. The catalog swap and mcp-go's `SetTools` remain two publications and cannot be made one transaction (mcp-go owns its registry read), so the resulting skew window is closed by ordering + deny-on-miss + registry-membership rules rather than papered over — D13/R13, with an interleaving test. See Complexity Tracking. |
| **III. Configuration-Driven Architecture** | PASS. One JSON config field with env override, serve flag, validation, and hot-reload (FR-001/FR-014); default preserves today's behavior; no tray state. |
| **IV. Security by Default** | PASS. Serialization never changes membership (FR-008/FR-016): all discovery filters run before rendering. describe_tool on the direct surface applies listing-parity visibility *including the operation-permission tier the existing resolver lacks*, and never emits an existence-confirming reason code **or suggestion** for an id this session's listing omitted (FR-011). Parity is enforced on both sides (D10): the listing filters resolve through the catalog instead of re-parsing `__`, and both suggestion channels (`did_you_mean`, case-correction) are catalog-gated or suppressed — closing four concrete parity defects that exist in this tree today — three disclosure paths plus a wrong-safety-hint path (a listed destructive tool describing as `call_with: "read"`) — rather than opening any. Quarantine/approval gates on dispatch are untouched; validation is fail-open, never blocking a call a schemaless proxy would allow. |
| **V. Test-Driven Development** | PASS. Every behavior lands test-first; the golden gates make surface drift a failing test by construction (see Test strategy). |
| **VI. Documentation Hygiene** | PASS. `docs/configuration.md`, the feature doc under `docs/features/`, `CLAUDE.md` MCP-protocol line, and `make swagger` regen are in scope (see Documentation & wiring). |

**Result**: PASS. No new dependency, no new package; one new file-local data structure (`directCatalog`) that *removes* a split-source-of-truth defect (FR-017). Re-checked after Phase 1 design: unchanged.

## Key Decisions (normative for tasks)

Resolved in [research.md](research.md); recorded here as the plan of record:

| # | Decision | Where resolved |
|---|----------|----------------|
| D1 | Config = dedicated `direct_tool_response_mode: "full"\|"deferred"`, default `full`; flag `--direct-tool-response-mode`; env `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE`; `routing_mode: "schema_deferred"` rejected with composition-naming message | R1, R7 |
| D2 | Deferred entries strip `outputSchema` too; `describe_tool` definitions gain additive `output_schema` (all surfaces, definition-assembly seam only) | R2 |
| D3 | describe_tool batch cap stays 5 (50 for `check:true`) on every surface | R3 |
| D4 | FR-007 channel = static instructions on the direct server, both modes, conditionally phrased; `initialize` delta enumerated (composition with the operator's `instructions` key: D11) | R4 |
| D5 | FR-009 prose = surface-neutral rewrite of ALL FOUR retrieve_tools strings (description, both param descriptions, the two shared remediation constants), single builder kept; one-time regen of the two describe_tool-bearing goldens plus named `describePlainDelta` substitutions | R5 |
| D6 | FR-017 = immutable `directCatalog` snapshot, atomic swap, **colliding display names withheld** (not first-writer-wins — D13 rule 5); handlers capture their own schema | R9 |
| D7 | FR-011 = catalog-backed direct resolver with listing-parity gates incl. permission tier; plain `not_found` for invisible ids in both modes; snapshot-backed definitions for visible ids; check-mode verdicts via shared preflight evaluator | R10 |
| D8 | Rollout default = **opt-in, off**; no default flip in this feature (spec Non-Goals; any future flip is its own evidence-gated decision) | spec |
| D9 | Deferred `inputSchema` is emitted via `mcp.NewToolWithRawSchema` (annotations copied on explicitly); `mcp.NewTool` cannot produce the FR-004 wire shape and mixing the two is a marshal error | R11 |
| D10 | Parity is closed on every side: the two direct listing filters resolve `(server, tool)` through the catalog instead of re-parsing `__` (the tier itself comes from the filtered entry's own annotations — D13 rule 3, not the catalog); the definition assembly takes a catalog-supplied annotations override on this surface (`buildFullToolEntry` otherwise reads annotations from the StateView, not from the entry passed in, so a listed pending destructive tool would describe as `call_with: "read"`); and the two suggestion paths (`did_you_mean`, `suggestCanonicalToolID`) are gated by the direct catalog or suppressed on this surface | R10 |
| D11 | Direct-server instructions = operator's `cfg.Instructions` when non-empty + blank line + the deferral legend, so an operator's configured `instructions` is not lost on the direct surface (fallback when empty: D16, not `resolveInstructions`) | R4 |
| D12 | `buildDirectModeTools` splits into a pure `buildDirectCatalog` + `renderDirectTools` pair so the unit matrix can drive a fixture toolset (`upstream.Manager` is concrete and only lists connected clients) | R12 |
| D13 | The catalog swap and `SetTools` cannot be one transaction (`mcp.Tool`'s only free-form field marshals to `_meta`, and mcp-go snapshots its tool map before our filters run), so the plan promises a safety property, not atomicity. Five rules: **`SetTools` first, catalog published immediately after**; filters **deny** on catalog miss (built-ins by explicit name set; a *nil* catalog is not a miss); the **permission tier comes from the registered entry's own annotations** on BOTH the filter and the describe path, never from the catalog; describe requires `directServer.GetTool(name) != nil` **and** catalog visibility; and **a display name never denotes two origins** — colliding names are withheld outright (spec-sanctioned "report it"), and a mismatch between the entry's **stored** `renderedDescription` (captured at render time, never re-rendered — the signature cache mutates independently of rebuilds) and the registered `mcp.Tool.Description` fails closed. Identity/origin hazards are fully closed; a **schema-only** change is NOT description-visible and remains a stated residual, absorbed by the feature's own self-healing validator (one retry). `generation` counter + six interleaving tests | R13 |
| D14 | Direct check-mode adapter canonicalizes `server__tool` → `server:tool` before `preflight.Evaluate` (which accepts colon ids only, so direct ids would otherwise all answer `not_found`), gates invisibility itself without consulting the evaluator, **projects the evaluator's `status`/`reason`/`action`/`retryable` through untouched** (check mode's vocabulary is `status:"unavailable"` + `tool_pending_approval`/`tool_changed`/… — never definition mode's), and restores the caller's original id + ordering in the response and the activity record | R10, contract §3 |
| D15 | `initRoutingModeServers` performs one **initial direct rebuild** before the listeners are wired — the direct server registers nothing today (`mcp_routing.go:651-653`), so composing the built-in into rebuild construction alone would still leave FR-009 false until the first `servers.changed`, and would leave the catalog nil so the first unrelated reload churns every client | R14 |
| D16 | One helper `resolveDirectInstructions(custom string)`: custom when non-empty, else a **direct-specific default**, then the legend. Not `resolveInstructions` — its `defaultInstructions` names `retrieve_tools` and `call_tool_*`. The direct default names ONLY what this surface exposes: `server__tool` calling, `describe_tool`, and the ABOUT links — no `upstream_servers`, which is registered by the code-exec and call-tool builders only (`mcp_routing.go:398`, `:507`), never by `buildDirectModeTools` | R4/D11 |

### Config interaction matrix (D1 applied)

| Surface / axis | Governing setting | This feature changes |
|---|---|---|
| `retrieve_tools` serialization (default server, `/mcp/call`, `/mcp/p/<slug>`) | `tool_response_mode` (full\|compact) + per-call `detail` | Nothing |
| Direct enumeration (`/mcp/all`; `/mcp` + legacy aliases `/v1/tool_code`, `/v1/tool-code` when `routing_mode:"direct"`) | **`direct_tool_response_mode`** (full\|deferred) — identical across all four routes (FR-003), which share ONE `p.directServer` instance (`GetMCPServerForMode`, `mcp_routing.go:896`), so one `SetTools` covers them all | Serialization only; + describe_tool in both modes |
| `/mcp/p/<slug>` | retrieve_tools-mode server — **not** a direct route; no profile-scoped direct surface exists or is created | Nothing |
| Code-execution surface | n/a (always full, no describe_tool — Spec 085 rule) | Nothing |
| Agent tokens / profiles | Discovery filters run before serialization in every mode (FR-016) | describe_tool parity gates added (D7) |
| Both axes combined | Independent: `tool_response_mode:"compact"` + `direct_tool_response_mode:"deferred"` is legal and each governs only its own surface | — |

## Project Structure

### Documentation (this feature)

```text
specs/102-schema-deferred/
├── spec.md              # Feature spec (merged; normative)
├── plan.md              # This file
├── research.md          # Phase 0: D1–D8 resolutions + mechanical findings R1–R10
├── data-model.md        # Phase 1: directCatalog, deferred entry, mode resolution, id forms
├── quickstart.md        # Phase 1: build/enable/verify walkthrough
├── contracts/
│   └── direct-deferred-surface.md   # Deferred entry shape, instructions text, describe_tool-on-direct deltas & error parity
└── tasks.md             # Phase 2 (/speckit.tasks — NOT created by this plan)
```

### Source Code (repository root) — REAL paths

```text
internal/
├── config/
│   ├── config.go                    # DirectToolResponseMode field + ToolResponseModeDeferred const beside
│   │                                #   ToolResponseMode (:485); validation beside the tool_response_mode block
│   │                                #   (:2229); routing_mode "schema_deferred" rejection message (FR-002)
│   └── loader.go                    # MCPPROXY_DIRECT_TOOL_RESPONSE_MODE env alias (beside :717)
├── runtime/
│   └── config_hotreload.go          # DetectConfigChanges: direct_tool_response_mode clause (beside :157)
├── toolsig/
│   └── cache.go                     # NEW method Peek(hash) (Signature, bool) — non-compiling, miss-reporting (FR-005)
├── server/
│   ├── mcp_direct_catalog.go        # NEW: directCatalog type + build (sorted; colliding display names WITHHELD,
│   │                                #   both origins logged — D13 rule 5) + atomic store on MCPProxyServer,
│   │                                #   PUBLISHED BY THE CALLER AFTER SetTools, never mid-build; absorbs
│   │                                #   directToolPermissions
│   ├── mcp_routing.go               # buildDirectModeTools splits into buildDirectCatalog + renderDirectTools (D12);
│   │                                #   render full|deferred via live config (deferred uses NewToolWithRawSchema, D9)
│   │                                #   → append describe_tool registration (FR-018); makeDirectModeHandler:
│   │                                #   pre-dispatch validation from the captured catalog entry (FR-012/013);
│   │                                #   initRoutingModeServers: WithInstructions on directServer (FR-007/D11/D16,
│   │                                #     direct-specific default, NOT resolveInstructions' retrieve_tools text)
│   │                                #     + the INITIAL direct rebuild the surface has never had (D15, :651-653);
│   │                                #   refresh guard: rebuild-if-serialization-changed (FR-014)
│   ├── mcp_direct_scope.go          # filterDirectModeToolsForAuth: resolve (server,tool) through the catalog instead
│   │                                #   of ParseDirectToolName; derive the tier from the filtered entry's OWN
│   │                                #   annotations via contracts.DeriveCallWith, NOT from the catalog (D10/D13.3);
│   │                                #   retires lookupDirectToolPermission
│   ├── mcp_direct_callability.go    # filterDirectToolsForAgentCallability: same catalog resolution (D10)
│   ├── mcp_describe_tool.go         # Surface-neutral prose — all FOUR retrieve_tools strings (D5/R5); resolver seam
│   │                                #   parameter; additive output_schema at definition assembly (D2)
│   ├── mcp_entry_builder.go         # definition-assembly seam: optional annotations override so the direct surface
│   │                                #   sources them from the catalog, not the StateView (D10); full-mode retrieve
│   │                                #   path byte-identical (buildFullToolEntry logic otherwise untouched)
│   ├── mcp_describe_direct.go       # NEW: catalog-backed direct resolver (both id forms, listing-parity gates incl.
│   │                                #   permission tier, not_found discipline) + check-mode delegation (D7)
│   ├── mcp_describe_check.go        # id-gate seam for the direct surface (D10); verdict logic unchanged
│   ├── preflight_glue.go            # NEW directCatalogIndexReader passed as preflight.EvalContext.Index on the
│   │                                #   direct surface (:103 today builds preflightIndexReader), so id resolution
│   │                                #   AND the did_you_mean corpus share the catalog authority (D10/FR-017)
│   ├── mcp_visibility.go            # serverInScope reused (evaluateToolGate lives in tool_gate.go:80, isToolCallable
│   │                                #   in mcp.go:6068); suggestCanonicalToolID gains the resolver seam (D10);
│   │                                #   existing retrieve-surface resolvers byte-identical
│   ├── mcp.go                       # MCPProxyServer field for the catalog pointer; sigCache handle reuse
│   ├── server.go                    # listenForRoutingModeRefresh config.reloaded branch (:554): guarded direct rebuild
│   ├── toolslist_snapshot_test.go   # NEW standalone direct built-in gate (NOT added to toolsListGoldenSurfaces)
│   ├── describe_plain_corpus_test.go# describePlainDelta: enumerate the remediation-prose substitutions (gate 4)
│   ├── mcp_describe_tool_test.go    # invert + rename the "direct routing mode" subtest of
│   │                                #   TestDescribeTool_RegisteredInRetrieveToolsModeOnly (:367)
│   ├── mcp_routing_test.go          # makeDirectModeHandler signature change (it now captures its catalog entry)
│   ├── toon_surface_isolation_test.go # same signature change (:187)
│   └── e2e_test.go                  # flip-notification, self-healing-retry, describe-on-direct E2E
cmd/mcpproxy/
│   └── main.go                      # --direct-tool-response-mode serve flag (pattern of :141/:769)
bench/arms/                          # NEW deferred-direct arm + testdata golden for the SC-001/SC-002 gates
oas/                                 # regenerated via `make swagger` (config struct change)
docs/
├── configuration.md                 # new field + env/flag
└── features/ (schema-deferred doc)  # deferral convention, describe_tool on direct, rollout notes
```

**Structure Decision**: Single Go project; no new packages. The two new files live in
`internal/server` beside their Spec-085 siblings (`mcp_describe_tool.go`,
`mcp_visibility.go`) because the catalog and the direct resolver are server-surface
concerns with no external consumer — a leaf package (the 085 `toolsig` argument)
does not apply here. `internal/toolsig` gains one method, no structural change.

## Test Strategy (incl. frozen-golden handling — FR-010/SC-004)

**The four frozen gates, by name, and how each is handled** (the fourth pins
describe_tool *response* bytes, not tools/list bytes, and was missed by the first
draft of research.md R2):

1. `TestMenuSurface_ExactDeltaFromPreFeature` (`mcp_menu_surface_test.go`,
   baseline `testdata/tools_list_prefeature.golden.json`, pre-085 capture):
   **passes untouched.** describe_tool post-dates its baseline and is already in
   its allowed-additions set; this feature adds no tool to the three surfaces it
   snapshots and edits no pre-085 tool bytes. Any failure here is a regression.
2. `TestToolsListSnapshot_MatchesMergeBaseGoldens` + `_DeltaIsEnumerated`
   (`toolslist_snapshot_test.go`, `testdata/toolslist_goldens/`): the FR-009
   prose rewrite moves describe_tool's pinned bytes on `default_server.json` and
   `retrieve_tools_mode.json` → **regenerate exactly those two goldens once**,
   deliberately, via the documented `MCPPROXY_WRITE_TOOLSLIST_GOLDENS` flow.
   `toolsListAllowedDelta` needs **no edit**: it already enumerates
   `describe_tool` on both surfaces (`toolslist_snapshot_test.go:188-192`) from
   spec 099. `code_execution_mode.json` and the frozen `pre099/` baseline are
   **never regenerated** — the enumerated-delta comparison against `pre099/` is
   what proves the change reached exactly as far as claimed.
3. `TestRetrieveToolsFullMode_GoldenByteIdentity` (`mcp_entry_builder_test.go:116`,
   byte-exact): it pins **two** goldens, not one — `retrieve_full_default.golden.json`
   (subtest `default`) and `retrieve_full_stats.golden.json` (subtest
   `include_stats`). **Both pass unregenerated by construction**:
   `buildFullToolEntry`'s logic is not modified (`output_schema` lands at the
   describe_tool definition-assembly seam only, D2; the D10 annotations override
   is an unused optional argument on this path), and describe_tool passes
   `toolEntryOpts{}`, so the stats branch is never reached from the new code.
4. **`TestDescribeToolPlainCorpus_ByteIdenticalWithOneEnumeratedDelta`** (`describe_plain_corpus_test.go`,
   `testdata/describe_plain_corpus/pre099.json`): replays 18 plain-mode
   `describe_tool` calls and pins each **response body** byte-for-byte, allowing
   only the substitutions enumerated in `describePlainDelta`. The FR-009 prose
   work touches two *response* strings (`describeNotFoundRemediation` and the
   malformed-id format remediation — research.md R5), and its doc comment states
   that "a reworded remediation" fails. Handling: **extend `describePlainDelta`**
   with the named substitutions; the golden itself stays frozen. `output_schema`
   is `omitempty` and the current fixture tools declare none, so D2 does not move
   these bytes today — a task MUST re-run this gate after the D2 change rather
   than assume it.

**Existing assertion that must be inverted (not a golden — a hard-coded test):**
`TestDescribeTool_RegisteredInRetrieveToolsModeOnly`
(`mcp_describe_tool_test.go:342`) has a `"direct routing mode"` subtest
(`:367-372`) asserting `describe_tool must NOT be exposed in direct mode (v1)`.
FR-009/FR-018 deliberately reverse that v1 decision, so this subtest is updated
to assert presence (and the test is renamed accordingly). It is green on
`origin/main` today, so its failure during implementation is expected and
enumerated here rather than mistaken for a regression. `TestMenuSurface_*` is
unaffected: it snapshots three surfaces, none of them direct, and compares bytes
only for tools that existed pre-085 (describe_tool is in its `wantAdded` set).

**New gate (FR-010, mandatory)**: the direct surface's built-ins are currently
unpinnable (its listing is a live projection) — add
`testdata/toolslist_goldens/direct_mode_builtins.json` + a snapshot test that
asserts over **`p.directServer.ListTools()`** — what the server actually serves
after `initRoutingModeServers` — with zero upstream tools (listing = built-ins
only: `describe_tool`) in **both** serialization modes: membership + serialized
bytes byte-exact, plus the direct server's instructions string (D4/D11/D16,
captured with an empty `instructions` config so the bytes are deterministic). It
must read the live server, NOT `buildDirectModeTools()`'s return value: the direct
server registers nothing at init today (`mcp_routing.go:651-653`), so a gate over
the builder would pass while the served surface was empty (D15/R14). A later edit
to either becomes a reviewable golden diff.

⚠️ It MUST be a **standalone** test, not a new entry in `toolsListGoldenSurfaces`:
`TestToolsListSnapshot_DeltaIsEnumerated` reads a frozen `pre099/<surface>.json`
for every listed surface (`toolslist_snapshot_test.go:200`) and asserts every
listed surface appears in `toolsListAllowedDelta` (`:228`). Direct mode has no
pre-feature baseline and cannot have one — its built-in did not exist — so adding
it to that slice fails both assertions.

**Byte-stability (FR-015/SC-004)**: the naive form of this — "copy a test file
into a throwaway worktree of `origin/main` and drive `buildDirectModeTools` with a
fixture toolset" — is **not runnable at the merge-base**, because the D12 pure
seam does not exist there and `DiscoverTools` only returns tools from connected
clients (research.md R12). Two workable forms, in preference order:

1. **Live stdio fixture** (portable across commits): the capture test connects a
   real fixture upstream over stdio, reusing the harness already in this package
   (`preflight_e2e_test.go` + `testdata/preflight_fixture_server.js`), then
   serializes the direct `tools/list`. That runs unchanged at the merge-base and
   on this branch, so the write-env-then-compare procedure
   `toolslist_snapshot_test.go` documents applies verbatim. Heavier (needs Node),
   so it carries the same build-tag/skip treatment as the other fixture E2Es.
2. **Same-tree differential** (fallback, if 1 is judged too heavy): over one
   fixture `[]*config.ToolMetadata`, render through the untouched `NewTool` full
   path and through the new `renderDirectTools` in `full` mode, and assert the
   marshalled bytes are equal entry-for-entry. This proves the FR-015 property
   (full mode is unchanged) without a cross-commit capture, but it cannot catch a
   regression that lives in the shared code both sides call — so it is the
   fallback, not the default.

Either way the assertion is the same: deferral-off rendering is byte-identical to
pre-feature output modulo the appended `describe_tool` entry.

**Unit (test-first, per constitution V)** — the core matrix. All of it drives the
pure `buildDirectCatalog`/`renderDirectTools` pair (D12) with a fixture
`[]*config.ToolMetadata`, because `upstream.Manager` is concrete and
`DiscoverTools` only returns tools from connected clients (research.md R12):
- Deferred entry rendering: signature appended per 085 rules; the marshalled
  `inputSchema` is byte-exactly `{"type":"object"}` — asserted on the JSON, not on
  the Go struct, since `mcp.NewTool` would marshal
  `{"properties":{},"required":[],"type":"object"}` (R11/D9) — no upstream
  properties/required; annotations preserved through the raw-schema constructor;
  cache-miss entry listed signature-less (FR-004/005); a deferred entry marshals
  without `errToolSchemaConflict`.
- Set identity full↔deferred: same names, count, annotations, ordering source
  (FR-008), incl. under agent-token and profile filters (SC-005, FR-016).
- Catalog: colliding display names withheld in both flattening directions (neither
  listed, neither describable, warning names both origins), atomic swap, entry↔
  handler schema capture, `describe_tool` survives `SetTools` refresh (FR-018).
- Direct describe: both id forms resolve identically through the registration
  mapping (incl. a server name containing `__`); permission-tier gate (read-scoped
  token → `not_found` for a destructive tool); listed-pending tool → definition in
  definition mode and `pending_approval` under `check:true` (SC-007); removed
  server → per-id not_found without failing the batch; `output_schema` present iff
  declared; **a listed pending/changed destructive tool describes with its real
  annotations and `call_with: "destructive"`** — the case where the StateView
  annotation lookup would otherwise return nil and silently downgrade the safety
  hint to `read` (D10); **a `server__tool` id under `check:true` returns its
  verdict under the id the caller sent** — the canonicalization + restore path of
  D14, which without it answers `not_found` for every direct id.
- **Listing↔describe parity, both directions** (D10): with a server whose name
  contains `__`, the listing filters and the describe resolver agree on the same
  entry — asserted by comparing the session's rendered `tools/list` name set
  against the set of ids that resolve in definition mode, for an admin session, a
  read-scoped token, a write-scoped token, and a profile-pinned session. No id may
  be describable-but-unlisted (disclosure) or listed-but-undescribable (SC-007).
- **Suggestion discipline** (D10): a read-scoped token's `check:true` miss returns
  no `did_you_mean` entry naming a destructive tool absent from its own listing,
  and the definition-mode case-correction suggestion is gated by the same
  catalog-backed resolver.
- **Instructions** (D11): a custom `instructions` config value still appears on the
  direct server's `initialize`, with the deferral legend appended, in both modes.
- **Publication skew** (D13) — a rebuild paused between `SetTools` and the catalog
  publish, with a concurrent scoped `tools/list` and `describe_tool`. Six
  interleavings, in three groups:
  1. *Closed by the design* — an added name, a removed name, a same-name
     **description-visible** change, and a same-name **origin flip** (server A
     removed and server B added in one reconcile, B's tool flattening to A's old
     display name). Each must be withheld or served whole, never mixed.
  2. *Closed by withholding* — a within-generation display-name collision:
     neither entry listed, neither describable, in both generations, warning
     naming both origins.
  3. *The documented residual* — a **schema-only** change with the description
     and rendered signature held byte-identical. This asserts the accepted
     behavior, not a fix: the stale definition may be returned, dispatch still
     validates against the new schema, and the `invalid_params` error carries the
     NEW schema so one retry succeeds (US3/SC-003).

  Plus two **no-rebuild** cases the discriminator must not mistake for a
  generation change: a signature-cache **miss→warm** and a **hit→eviction**
  between registration and a later filter/describe call (`toolsig.Cache.Warm` /
  `RetainHashes`). Both must leave the tool listed and describable — the proof
  that the comparison reads the stored `renderedDescription` instead of
  re-rendering.

  Assertions across the whole set: no describable-but-unlisted id; no entry
  scope-checked against one origin while its handler dispatches to another; no
  read-scoped token receiving a destructive tool's definition (the tier comes from
  the registered entry's own annotations on both paths); and no case where a stale
  definition leads to a call that *succeeds* against the wrong schema.
- **Initial registration** (D15/R14): on a freshly initialized proxy with **zero**
  upstream servers, `p.directServer` already lists `describe_tool` before any
  `servers.changed` fires; and an unrelated `config.reloaded` on that proxy
  triggers no `SetTools` and no `tools/list_changed` (the FR-014 no-churn rule,
  which a nil catalog would otherwise defeat).
- **Check-mode projection** (D14): a direct check response is byte-compatible with
  the retrieve-surface response for the same underlying tool state — same
  `status`/`reason`/`action`/`retryable` values, `tool_`/`server_` prefixes intact,
  `tool_blocked_by_user` still distinct from `tool_denied_by_config`.
- Validation: missing-required → `invalid_params` with embedded full schema + hint
  in both modes; validates against stored schema (stale full-mode client scenario);
  fail-open on uncompilable schema; non-argument failures unchanged (FR-012/013).
- Config: validation messages (incl. `schema_deferred` rejection), env/flag
  precedence, `DetectConfigChanges` clause.
- Rebuild guard: serialization flip → rebuild + notification; unrelated config edit
  → no `SetTools` call (assert no notification) (FR-014); a flip while
  `DiscoverTools` is failing (empty catalog published, mode recorded) still
  rebuilds and is not lost (research.md R8).

**Token gates (SC-001/SC-002)**: measure deferred vs full direct `tools/list` over
the frozen 45-tool corpus with the spec-083 profiler's pinned tokenizer (the
085/099 pattern): assert ≥70% reduction and ≥80% one-shot-callable (non-lossy)
share. Concretely this is a **new arm** in `bench/arms/` (registry contract:
`specs/083-discovery-profiler/contracts/arm-interface.md`, `bench/arms/arm.go`),
alongside `baseline`/`compact_sig`/`toon`/`tron`/`tscg`, plus its
`bench/arms/testdata/*_golden.txt` render golden — the existing arms all encode
`retrieve_tools` result sets, none renders a direct `tools/list`. `bench/` is
therefore an in-scope directory for this feature (added to the touch list below).

**E2E** (`internal/server/e2e_test.go`): live flip with a connected client —
`notifications/tools/list_changed` observed, next listing reflects the mode,
tool set identical (SC-006); guessed-wrong → one self-healing retry succeeds
(SC-003); legacy aliases `/v1/tool_code`/`/v1/tool-code` covered by the same
mode/notification assertions as `/mcp` (FR-003). Local runs use the CI skip regex
for `internal/server` and an isolated high-port instance; never
`scripts/test-api-e2e.sh` against a shared machine.

## Documentation & wiring checklist (Constitution VI)

- `make swagger` after the config struct change (generated artifacts committed).
- `docs/configuration.md`: `direct_tool_response_mode` + env + flag.
- New feature doc under `docs/features/` (deferral convention, describe_tool on
  direct, client-compat notes: schema-driven form UIs render empty forms; stale
  cached listings both directions safe; instructions delta on `initialize`).
- `CLAUDE.md`: MCP-protocol built-ins line (describe_tool availability) + Recent
  Changes entry.
- Issue linkage: commits use `Related #971` (never `Fixes`/`Closes`).

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| `atomic.Pointer` swap for `directCatalog` (Principle II prefers channel-owned actors) | The catalog is written only by the single routing-refresh listener goroutine and read on every direct `tools/list`, `describe_tool`, and dispatch; FR-017 requires the listing, resolver, and validator to observe one consistent snapshot. | A channel-owned actor to serve reads of an immutable snapshot adds a goroutine + request/response channels around data that never mutates after publication. An atomic pointer to an immutable value is the idiomatic Go read-mostly pattern, is strictly less code, and is the same trade Spec 085's constitution check already accepted for the signature cache. The existing mutex-guarded `directToolPermissions` map it replaces was already shared-memory; this change reduces lock scope to zero on the read path. |

| **FR-017's literal "rebuilt atomically, so no window exposes one without the other" is delivered as a safety property, not as a transaction** (spec.md FR-017 third bullet) | The advertised entry lives in mcp-go's tool registry and the catalog lives in ours; they are two publications. mcp-go reads its own `s.tools` under `toolsMu` before invoking our tool filters, so no lock of ours can span the registry read, and the only free-form field on `mcp.Tool` (`Meta`) marshals to `_meta`, so a generation stamp would move bytes FR-015 freezes. | Making it literally atomic would require either forking/patching mcp-go's registry or moving the direct listing off `SetTools` onto a hand-rolled tools/list handler — a far larger blast radius than this feature, and one that would put mcpproxy's own code on the protocol hot path for every routing mode. Instead R13 states the property that actually matters and proves it per-window: *no request observes a state less restrictive than both generations, and no request receives a definition for a name the registry is not currently serving*. The half of FR-017 that names dispatch — the handler validating against what it advertised — IS literally satisfied (closure capture, R9). **This narrowing is deliberate and needs the maintainer's assent at the tasks stage; it is the one place this plan does not deliver a spec MUST word-for-word.** |

| **A schema-only change can be described one generation stale for the duration of the two-publication window** (the second, narrower FR-017 narrowing) | The skew discriminator is the rendered description, which does not encode the schema: full mode renders `[server] description` (`mcp_routing.go:95`) and deferred mode's appended signature is lossy by design, so a nested-property edit can render identically. No field on the registered `mcp.Tool` encodes the schema in *both* modes — deferred advertises the same `{"type":"object"}` placeholder for every tool — and `Tool.Meta` is wire-visible, so a hash stamp is closed off by FR-015. | The alternatives are the same ones rejected above (fork mcp-go's registry, or take the direct listing off `SetTools`). The residual is bounded and self-correcting by a mechanism this feature itself ships: dispatch is never wrong (R9 closure capture), so an agent acting on a stale definition is rejected by the pre-dispatch validator with the **correct** schema embedded and succeeds on one retry — the US3/SC-003 path. Cost is one extra round trip, in a microsecond window, for a tool whose schema changed mid-refresh. **Like the row above, this needs the maintainer's assent at the tasks stage.** |

No other deviations: no new dependency, no new package, no new abstraction beyond
the catalog type the spec's FR-017 mandates by name.
