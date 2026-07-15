# Phase 0 Research: Compact Router

All decisions below are grounded in the actual code (paths + line numbers verified in the
`085-compact-router` worktree and the `083-discovery-profiler` branch).

## R1 — Signature grammar: production vs the existing bench arm (DIVERGENCE — must resolve)

**Finding**: `bench/arms/compact.go` on branch `083-discovery-profiler` already implements a
compact-signature encoding, but its grammar **differs materially** from the normative grammar
in spec.md / design.md:

| Aspect | Bench arm (`compact_sig`, 083) | Spec 085 (normative) |
|---|---|---|
| Required marker | none — bare `param:type` | `param*:type` (`*` marks required) |
| Optional marker | `param?:type` (`?` suffix) | no suffix; `param:type` |
| Lossy marker | none (nested obj → bare `obj`) | `param~:obj` (`~`), sets entry `lossy=true` |
| Enums | dropped | `enum[a|b|c]` inline when ≤5 values |
| Defaults | dropped | `=value` inline for short optional scalars |
| Type names | `string/int/number/bool/obj/arr/any` | `str/int/num/bool/obj/[str]` |
| Array type | `arr` | `[elem]` (e.g. `[str]`) |
| Description | **verbatim full** appended `\|description` | **first sentence only**, separate `desc` field |
| Output shape | flat text `name(...)\|desc` | JSON fields `sig`, `desc`, `lossy` separate |

**Decision**: Extract one grammar into `internal/toolsig` implementing the **spec 085 grammar**
(the normative one). Migrate the offline `bench/arms/compact.go` arm to import
`internal/toolsig` so offline and live measurements agree (FR-019 "identical definition in,
identical bytes out"; the spec assumption "bench imports internal already"). The live profiler
arm (FR-017) measures the *real* proxy response and therefore already uses production bytes.

**Cross-branch sequencing (Codex finding 7)**: `internal/toolsig` is **born on the `085`
branch**; `bench/arms/compact.go` exists only on `083-discovery-profiler` (PR #851), not in the
`085` worktree today (the `085` tree has `bench/` but no `bench/arms/`). So the migration cannot
happen inside 085 until 083 has merged. Explicit order: (1) land `internal/toolsig` + the
production compact path via the `085` branch; (2) once **083 (PR #851) merges to main**, rebase
`085` on main — which brings `bench/arms/` into the tree; (3) **then**, on the rebased `085`
branch, migrate `bench/arms/compact.go` to import `internal/toolsig` and regenerate its goldens
(`bench/arms/testdata/compact_golden.txt`). This is why task T040 is gated on the 083 merge and
sequenced after the rebase, and T043's re-measurement runs only after T040.

**Consequence to flag** (see final report): the prior headline numbers (−52.6% offline 45-tool
corpus, −92% live) were measured against the *bench* grammar, which keeps **full descriptions**.
The spec grammar truncates to first sentence, so the true production reduction should be **≥**
the bench figure on the description axis while adding a few bytes of `*`/`~`/enum markers.
SC-001 is defined as *profiler-measured*, so re-baselining is expected, not a contradiction —
but the plan must not present −52.6%/−92% as already-achieved production numbers. Re-measure
after `internal/toolsig` lands (tasks Phase US5).

**Rationale**: two grammars in one repo drift (the MCP-3161 catalog-drift lesson design.md
cites). Determinism (FR-019) is only checkable if there is a single implementation.

**Alternatives rejected**: (a) keep the bench arm's grammar and adopt it in production — loses
`*`/`~`/enum/default/first-sentence that the spec makes hard requirements; (b) two grammars,
document the delta — violates FR-019's sharing intent and makes the profiler measure a
different artifact than production ships.

## R2 — First-sentence extraction (FR-002, verbatim, deterministic)

**Decision**: `FirstSentence(desc string) string` in `internal/toolsig` (terminator rule is
script-dependent — see contracts/signature-grammar.md §6, which this must match exactly):
1. Scan for the earliest sentence terminator, returning the verbatim prefix **including** it:
   - **CJK terminators** `。 ！ ？` match **unconditionally** (CJK text is unspaced, so a trailing
     whitespace requirement would never fire — this is what makes E6 split at `。`);
   - **ASCII terminators** `. ! ?` match **only** when immediately followed by whitespace, EOF, or
     a closing `" ) ] }` (so `e.g. text` splits after the space-followed period, but `3.14` /
     `v1.2` do not).
2. If no terminator is found within a hard cap (`maxDescPrefix = 200` runes), return the
   verbatim first `maxDescPrefix` runes (rune-boundary safe, never mid-rune), no ellipsis
   fabrication beyond a trailing `…` marker counted in size.
3. Empty/whitespace-only description → empty string (entry renders id + sig only; the
   profiler's degenerate-description counter flags it — upstream hygiene, not this feature).

**Rationale**: verbatim-prefix guarantees no paraphrase can drop disambiguating text (design
mitigation 6). The CJK terminators handle the non-Latin edge case deterministically; the
length cap handles "no sentence boundary" (markdown headers, code blocks, prose without
periods). No LLM, no locale tables.

**Alternatives rejected**: sentence segmentation libraries (new dep, non-deterministic across
versions); markdown-aware parsing (over-engineered for v1; verbatim prefix is sufficient and
the spec explicitly scopes hygiene out).

## R3 — Pre-dispatch argument validation (FR-013 / FR-013b)

**Finding**: `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` is **already a direct
dependency** (go.mod, used by `internal/outputvalidation/validator.go`). There is today **no**
pre-dispatch arg validation — `handleCallToolVariant` parses `args` (mcp.go ~1730) and calls
`upstreamManager.CallTool` (~1955) with no schema check.

**Decision**: Reuse santhosh v6. New `internal/server/mcp_input_validation.go`:
`validateArgs(paramsJSON string, args map[string]any) (ok bool, verr error, skipped bool)`.
- Compile `paramsJSON` with a cached `*jsonschema.Schema` (memoize by tool hash, reuse the
  same cache infrastructure as signatures or a sibling map).
- On **compile** failure or unsupported construct → `skipped=true`, dispatch as today, count in
  logs (FR-013b fail-open). Validation must never block a call a schemaless proxy would allow.
- On **validation** failure of otherwise-compilable schema → typed invalid-params error →
  self-healing path attaches the full schema (FR-013).

**Rationale**: the dependency is already vetted and in the tree; mirroring the
`internal/outputvalidation` pattern keeps input/output validation consistent. Fail-open makes
this strictly additive — no new failure mode for valid calls. A minimal in-tree required/type
validator (also viable per the spec) was rejected *because the library already exists here* —
writing a second validator would be net-new code for no dependency savings.

**Alternatives rejected**: `github.com/google/jsonschema-go` (indirect dep, less battle-tested
here); hand-rolled required/type checker (redundant given santhosh is present); validating
inside `internal/upstream` (design.md draft location) — rejected because the schema source
(`ParamsJSON` / index) and the error-rendering seam (`createDetailedErrorResponse`) both live
in `internal/server`; keeping validation there avoids threading schema lookups into the
transport layer.

## R4 — Signature cache keying + warm-up (FR-008)

**Finding**: `ToolMetadata.Hash` (`internal/index/bleve.go:40`, `ToolDocument.Hash`) is the
Spec-032 SHA-256 computed by `hash.ComputeToolHashWithOutputSchema(server, tool, desc,
inputSchema, outputSchema)` (`internal/upstream/core/client.go:372`). Tool-definition changes
and index rebuilds naturally change the hash; quarantine change-detection already keys on it.
`applyDifferentialToolUpdate` (`internal/runtime/lifecycle.go:542`) is where added/modified
tools flow into `indexManager.BatchIndexTools`.

**Decision**: In-memory `Cache` in `internal/toolsig`: `Get(hash, paramsJSON) Signature`,
compiling on miss and memoizing. Warm it from the indexing path (`applyDifferentialToolUpdate`
after `BatchIndexTools`, and the full-reindex branch) so the first request is a cache hit.
Because rendering is a pure deterministic function of `(paramsJSON)` and the hash already
covers the schema, lazy-compile-on-miss is *equivalent* to index-time compile; the warm-up
just moves the one-time cost off the request path (FR-008 "not per request"). **No new index
field** — the cache is process-local; a restart re-warms during the normal reindex.

**Rationale**: satisfies FR-008 literally (keyed by the indexed hash, warmed at index time,
not persisted) while keeping the change minimal. Invalidation is free: a changed tool gets a
new hash → new cache entry; stale entries are harmless (never read) and bounded by tool count.

**Alternatives rejected**: persisting signatures as a new Bleve/BBolt field (FR-008 forbids in
v1, adds a migration); recompiling per request (FR-008 forbids, adds latency at scale).

## R5 — Entry-builder extraction (spec Assumptions, prerequisite refactor)

**Finding**: `handleRetrieveToolsWithMode` (mcp.go ~1203) builds each entry inline
(~1428–1492: `name/description/inputSchema/score/server` + `annotations`/`call_with` +
optional `usage_count`/`last_used`) then appends cross-cutting sections
(~1494–1613: token-savings accounting, `usage_instructions`, `disabled`/`remediation`,
`notice`, `session_risk`, `debug`, `usage_summary`). Entry construction is interleaved with
these, so compact mode cannot be added cleanly without first isolating the per-entry shape.

**Decision**: Extract `buildToolEntry(result, mode, opts) map[string]any` (new
`mcp_entry_builder.go`). Full mode reproduces today's map exactly; compact mode returns
`{id, score, sig, desc, lossy}`. The cross-cutting sections stay in the handler. Compact mode
additionally sets the top-level `hint` (FR-009). This refactor ships **before** compact mode
(its own PR/tasks), guarded by a byte-identity golden test (FR-006/SC-003).

**Rationale**: the spec makes this a required prerequisite ("a tested full/compact
entry-builder seam before compact mode lands — this is a refactor prerequisite, not optional
cleanup"). Isolating it first means the compact change is a small, reviewable diff and the
byte-identity guarantee is provable on an unchanged full path.

## R6 — Config field, env alias, hot-reload (FR-001 / FR-015)

**Finding (CORRECTED after Codex review — the original claim was wrong)**: `RoutingMode string`
is a top-level `Config` field (`config.go:290`, `mapstructure:"routing-mode"`); validation is a
small block at `config.go:1650` (`ValidateDetailed`). Viper prefix is `MCPP` (`loader.go:172`)
with explicit `MCPPROXY_*` aliases added case-by-case (`loader.go:570+`). **But hot-reload does
NOT come for free, for two reasons:**
1. `handleRetrieveToolsWithMode` reads the **construction-time** `p.config` (e.g.
   `p.config.ToolsLimit` at `mcp.go:1236`), which is *not* swapped on reload. The live snapshot
   is obtained via `p.currentConfig()` (`internal/server/profile_resolver.go:38`), which reads
   `p.mainServer.runtime.Config()` and falls back to `p.config`. So `ToolsLimit` itself is
   latently stale on reload — the mode must NOT copy that pattern.
2. `DetectConfigChanges` (`internal/runtime/config_hotreload.go`) enumerates changed fields
   explicitly (listen, data_dir, tools_limit, routing… — no `else`/reflection). It has **no
   clause** for a new field, so an API "apply" that changes only `tool_response_mode` would
   compute an empty `ChangedFields` and be treated as "no changes detected" — the reload is a
   no-op and the mode never takes effect.

**Decision**: Add `ToolResponseMode string \`json:"tool_response_mode,omitempty"
mapstructure:"tool-response-mode"\`` beside `RoutingMode`. Then, to make FR-015 actually work:
- **(a)** Add a `tool_response_mode` clause to `DetectConfigChanges`
  (`internal/runtime/config_hotreload.go`) so `oldCfg.ToolResponseMode != newCfg.ToolResponseMode`
  appends `"tool_response_mode"` to `ChangedFields` — otherwise an apply of only this field is
  swallowed.
- **(b)** Resolve the effective mode from `p.currentConfig()` (the live snapshot), **not**
  `p.config`, in the retrieve path. Concretely: a small `p.effectiveToolResponseMode(detail
  string) string` helper reads `p.currentConfig().ToolResponseMode` (empty ⇒ `full`) and applies
  the per-call `detail` override.
- Validate beside the `routing_mode` block: empty | `full` | `compact`; else
  `ValidationError{Field:"tool_response_mode"}`. Add the `MCPPROXY_TOOL_RESPONSE_MODE` alias in
  `loader.go` and a `--tool-response-mode` cobra flag in `cmd/mcpproxy`.
- Tests: (i) `DetectConfigChanges` reports `tool_response_mode` when only that field differs;
  (ii) an E2E that flips the mode via the config-reload path **and** via an API apply of only
  that field, asserting the next `retrieve_tools` call changes shape with no restart (SC-007).

**Rationale**: "orthogonal to `routing_mode`; do not add a new `tool_router` config tree"
(design.md) — one field, one validation clause, existing reload plumbing plus the two wiring
fixes above. Nothing new to
watch (no fsnotify added; rides the existing manual/API/file reload).

## R7 — describe_tool resolution + registration (FR-010 / FR-011)

**Finding**: No `GetToolByName` exists, but `indexManager.GetToolsByServer(server)`
(`bleve.go:398`) returns full `[]*ToolMetadata` (with `ParamsJSON`, `Description`). **However**
(Codex): the retrieve handler's visibility logic is a **local closure** — `serverDiscoverable`
is defined inside `handleRetrieveToolsWithMode` (`mcp.go:1324`), and the callable/quarantine
passes are inline in that function and in `handleCallToolVariant`. It is **not reusable** as
written, so describe_tool cannot "reuse the same predicate" without first extracting it (see
R10). `describe_tool` must register only in the retrieve_tools routing mode:
`buildCallToolModeTools` (`mcp_routing.go:354`) and the default server's `registerTools`
(`mcp.go:689`); **not** in `buildCodeExecModeTools` or direct mode (FR-011, v1 scope).

**Decision**: New handler resolves each id to `(server, tool)`, runs it through the **shared
visibility resolver** (R10) — `p.toolVisibleToSession`, whose canonical order is index
presence → profile+agent scope → server quarantine → tool approval → `isToolCallable` — then reads the full
`ToolMetadata` via `GetToolsByServer` (filtered to the id) and returns a **definition** built by
`buildToolEntry(..., full)` **with the ranked-only keys stripped** (`score`). Equality with
full-mode retrieve_tools is asserted over the definition fields `{name, description, inputSchema,
server, annotations, call_with}` — **not** whole-object byte-equality, because full entries carry
`result.Score` (`mcp.go:1455`) that a non-ranked lookup has no value for (Codex finding 2; see
contracts/describe_tool.md). Unknown/invisible ids → per-id error, batch still succeeds. Cap 5 →
clear error naming the limit.

**Rationale**: reuses the *same* extracted visibility predicate (FR-011 "must never return a
definition search would not return") and the *same* full-field renderer, so describe_tool cannot
drift from either — while being honest that the ranked `score` field is out of scope for a
lookup.

**Alternatives rejected**: adding a dedicated `GetTool(server, tool)` index method — nice but
not required for v1; `GetToolsByServer` + filter is sufficient and avoids new index API.
Keeping `serverDiscoverable` as a closure and re-implementing it in describe_tool — rejected: two
copies of a security predicate is exactly the drift FR-011 forbids.

## R8 — Byte-identity + ranked-ID identity verification (SC-002 / SC-003)

**Decision**: (a) Golden byte test: capture a full-mode `retrieve_tools` response for a fixed
corpus/query pre-refactor, assert `json.Marshal` bytes unchanged after the entry-builder
extraction and after compact mode ships with default `full`. Go marshals map keys sorted, so
identity is well-defined. (b) Ranked-ID identity: because SC-002 is a **hard release blocker for
the feature**, the **full 47-query golden-set** ranked-ID identity check (full vs compact, exact
ordered `id` list per query) lives as a **US1 in-repo test** that must pass *before any
compact-mode ship* — **not** deferred to the US5 profiler (Codex finding 6). The compact path
must derive `id` from `result.Tool.Name`/`ServerName` and never re-sort. The US5 profiler arm
(FR-018) *additionally* emits the same metric on live runs as the standing release gate, but the
blocking check does not wait for the profiler.

**Rationale**: SC-002 is a hard release blocker; making it a US1 unit/integration test over the
whole golden set (not one query, not the deferred profiler) catches both code regressions and
corpus-specific surprises before the risky serialization change can merge.

## R9 — Signature-cache ownership + wiring (FR-008, Codex finding 4)

**Finding**: a memoized cache only helps if there is **one** instance shared between the writer
(indexing path, `runtime.applyDifferentialToolUpdate`) and the reader (`retrieve_tools` /
`describe_tool` in `internal/server`). Warming a cache the retrieve path doesn't hold is a silent
no-op. `internal/server.MCPProxyServer` is constructed from `internal/runtime`
(`NewMCPProxyServer(...)`), and the indexing lives on `Runtime`; there is no existing shared
handle for a signature cache.

**Decision**: `Runtime` **owns** a single `*toolsig.Cache`, created once at runtime init and
passed into `NewMCPProxyServer` (stored as `p.sigCache`). The indexing path warms *that* cache;
the retrieve/describe paths read *that* cache. A dedicated **compile-count test** proves the
wiring: the cache exposes a test-only compile counter; index N tools, then issue a
`retrieve_tools` in compact mode and assert the compile count did **not** increase (post-index
retrieve is a pure cache hit → FR-008 "not per request"). This is a concrete Foundational task,
not folded into the warm-up task.

**Rationale**: without an explicit single-owner wiring task, a plausible-but-wrong
implementation constructs a cache inside `internal/server` that the runtime warm-up never
touches — passing all functional tests while quietly compiling per request. The compile-count
test is the guardrail that makes FR-008 falsifiable.

## R10 — Shared visibility resolver (Codex finding 5)

**Finding**: the search-visibility decision (a tool an agent may see) is currently a **local
closure** `serverDiscoverable` (`mcp.go:1324`) plus inline `isToolCallable` and a quarantined-
tool second pass, all inside `handleRetrieveToolsWithMode`. `handleCallToolVariant` re-checks
similar rules inline. Nothing is reusable by `describe_tool`, so FR-011's "same visibility
pipeline as search" cannot be honored by construction — only by hand-copying, which drifts.

**Decision** (Foundational, blocks US2 and strengthens US1): extract a method
`p.toolVisibleToSession(ctx, server, tool) (visible bool, reason string)` capturing the exact
order search uses: (1) index presence, (2) profile scope (Spec 057) **and** agent-token server
scope (Spec 028) — today's `serverDiscoverable`, (3) server-level quarantine, (4) tool-level
approval `pending`/`changed` (Spec 032), (5) `isToolCallable` (disabled/blocked). Rewire
`handleRetrieveToolsWithMode` to call it (behavior-preserving — guarded by the existing
retrieve tests + the byte-identity golden), and have `describe_tool` call the **same** method. A
**parity test** drives the same session/token and asserts: for every tool, `describe_tool`
returns a definition **iff** `retrieve_tools` would surface it (no tool visible to one but not the
other).

**Rationale**: one predicate, two callers, one parity test — the only way FR-011's security
invariant (Constitution IV) is provable rather than asserted. Extracting it first also removes a
long closure from the retrieve handler, easing the entry-builder refactor (R5).
