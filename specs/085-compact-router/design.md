> **Status note (2026-07-14)**: This is the judge-panel synthesis RECORD that seeded the spec — authoritative for architecture *rationale* and rejected alternatives only. Normative requirements live in [spec.md](spec.md); where the two differ, **spec.md wins**. Known deltas: `describe_tool` ships in Phase 1 (P1 user story), the per-call `detail` parameter has no default (unset follows the configured mode), invalid-params errors embed the FULL schema (fragments were a draft idea), and env-var naming follows the config loader's conventions rather than the literal name below.

# Spec 085 — Compact Router: Progressive-Disclosure Tool Discovery

## Chosen architecture

**Compact-by-default `retrieve_tools` + on-demand `describe_tool` + self-healing schema-on-error** (Design 0 core), hardened with the best mechanisms from the other proposals: index-time signature compilation (Designs 1/3), pre-dispatch argument validation with schema-fragment errors and the never-elide-required-params invariant (Design 3), and family grouping + adaptive-k as a gated Phase 3 (Design 2).

Thesis: the median 8,640-token discovery response is ~77% raw `inputSchema` JSON that agents rarely read for flat tools; the rest of the reduction to ~700 tokens comes from truncating descriptions to their first sentence (verbatim, never paraphrased). We do not touch BM25 query→ranking→top-k — the change is pure serialization, so retrieval recall (half of LiveMCPBench failures) cannot regress by construction. Ranking identity is asserted by the profiler, not assumed.

Every mechanism is a plain MCP tool call/response — works today in Claude Code, Cursor, and generic clients. No dependence on `tool_reference` expansion, `listChanged` dynamic registration, or client sniffing.

## Tool surface (exact)

| Tool | Status | Params | Description (agent-facing) |
|---|---|---|---|
| `retrieve_tools` | modified | `query:str*, limit:int=5, detail:"compact"\|"full"="compact", include_disabled:bool=false` | "Search upstream tools by natural-language query; returns ranked compact signatures. Use describe_tool for full schemas when a signature is marked lossy (`~`)." |
| `describe_tool` | **new** | `tool_ids:[str]* (max 5, "<server>:<tool>")` | "Return full JSON Schema + long description for specific tools found via retrieve_tools." |
| `call_tool_read/write/destructive` | description updated | unchanged | Must stop saying "refer to the tool's inputSchema from retrieve_tools" (mcp.go ~:672) — reference signatures + describe_tool instead. Same PR as Phase 2. |
| `upstream_servers`, `quarantine_security`, `code_execution` | unchanged | — | — |

Menu stays at 7–8 tools; `describe_tool` adds ~150 tokens once. No renames, no aliases, no deprecations — the frozen-surface stability the community asked for (#175).

## Response format

Signatures render from stored `ParamsJSON`: `*` = required, types abbreviate (`str/int/bool/num/[str]/obj`), defaults and enums ≤5 values inline, nested objects collapse to `obj~` — `~` marks a lossy signature ("describe me"). **Invariant: required params are never elided; only optionals truncate.** Descriptions: first sentence verbatim.

Worked example, `retrieve_tools({query:"create a cdn resource"})`:

```json
{
  "query": "create a cdn resource",
  "tools": [
    {"id": "digitalocean:cdn_create", "score": 0.94, "lossy": false,
     "sig": "(origin*:str, ttl:int=3600, certificate_id:str, custom_domain:str)",
     "desc": "Create a CDN for a Spaces bucket"},
    {"id": "digitalocean:cdn_update", "score": 0.71, "lossy": false,
     "sig": "(cdn_id*:str, ttl:int, custom_domain:str)",
     "desc": "Update an existing CDN's settings"},
    {"id": "cloudflare:zone_create", "score": 0.55, "lossy": true,
     "sig": "(name*:str, account~:obj, type:enum[full|partial])",
     "desc": "Create a new zone"}
  ],
  "hint": "Call via call_tool_write. If sig contains '~', call describe_tool({tool_ids:[id]}) first."
}
```

Happy path: zero extra round trips — the agent calls `call_tool_write` directly. Only `~`-marked selections ever need `describe_tool`.

## Config, defaults, migration

- `"tool_response_mode": "compact" | "full"` in `mcp_config.json`, hot-reloaded. **Orthogonal to `routing_mode`** (which selects the tool surface; this selects serialization within `retrieve_tools` mode) — extend `internal/config/config.go` validation beside the existing `routing_mode` block (~:1650), do not add a new `tool_router` config tree.
- `--tool-response-mode` flag + `MCPPROXY_TOOL_RESPONSE_MODE` for server edition.
- **Phase 1 ships default `full`** (byte-identical responses, flag opt-in). **Phase 2 flips default to `compact`** once profiler gates pass. Rollback = one config line; per-call `detail:"full"` is the agent-level escape hatch. No storage/index migration: signatures render from `ParamsJSON` (BBolt source of truth), precompiled at index time keyed by the Spec-032 tool hash — quarantine change-detection invalidation comes free.
- Agent-facing migration is the tool description itself: agents relearn the contract every session; there is no stored client dependency on response shape. Consumers that parse `inputSchema` out of retrieve_tools responses are covered by `detail:"full"`.

## Token math

| Scenario | Before | After | Notes |
|---|---|---|---|
| Menu (tool defs, once/session) | baseline | +~150 | describe_tool definition |
| Discovery response, median | 8,640 | ~700 (−92%) | schema drop (~77%) + first-sentence descriptions |
| Discovery response, max | 54,865 | ≤1,500 | giant schemas were the max driver |
| describe_tool call | n/a | ~1,300/tool | only lossy selections; target <0.3 calls/task |
| Tool call (args) | unchanged | unchanged | — |
| Failed call (InvalidParams) | error only | error + that tool's full schema | caps lossy failures at one retry |

Break-even vs a naive full-menu deployment depends on profile size and is **measured, not asserted**: the profiler emits a break-even curve per corpus size (arm 6 below). No hand-waved "38→3 calls" claims.

## Recall-risk mitigations

1. **Ranking untouched** — profiler hard-gates result-ID identity compact vs full.
2. **Lossiness is legible** — `lossy:true` + `~` markers tell the agent when a signature is insufficient, before it guesses.
3. **Self-healing errors** — upstream InvalidParams → `createDetailedErrorResponse` embeds the tool's full schema; one retry max, zero happy-path cost, mode-independent.
4. **Pre-dispatch validation** (Design 3) — `internal/upstream` validates args against stored schema before dispatch; violations return the offending schema fragment. Cheaper and faster than a round-trip to the upstream.
5. **Never elide required params**; enums/defaults inline keep most real-world (flat) schemas non-lossy — gate: <20% lossy across the corpus.
6. **Descriptions verbatim first sentence** — no paraphrase that could drop disambiguating text.

Rejected residual risk: the signature micro-DSL confusing weaker models — bounded by mitigations 3–4 and measured by arm 4.

## Phased implementation plan

**Phase 0 — self-healing + validation (ships immediately, mode-independent).** Extend `createDetailedErrorResponse` (mcp.go, call sites :2051/:2461) and `handleCallToolVariant` (:1649) to embed the full schema on InvalidParams; add pre-dispatch validation in `internal/upstream`. Unit + e2e tests.

**Phase 1 — signature compiler + opt-in compact.** New `internal/server/toolsig/`: pure `Render(paramsJSON) (sig string, lossy bool)`, TDD table tests over real captured schemas (budget explicitly for `$ref`, `anyOf/oneOf`, recursion — this is the hidden cost center). Precompute at index time alongside Spec-032 hashes (`internal/runtime/lifecycle.go` `applyDifferentialToolUpdate`, `internal/storage/`). Swap response assembly in `handleRetrieveToolsWithMode` (mcp.go :1203, assembly ~:1432–1460) behind `tool_response_mode`. Add compact variant to `bench_export.go` so the bench catalog can't drift (MCP-3161 lesson). Config in `internal/config/config.go`.

**Phase 2 — describe_tool + default flip.** Register `describe_tool` in `registerTools` (~:689), reusing the ParamsJSON lookup path; update `call_tool_*` and `retrieve_tools` descriptions in the same PR. Flip default to `compact` only after Phase 1 gates pass on the profiler.

**Phase 3 (gated, optional) — density upgrades from Waypoint.** Family grouping (tools sharing a stem + ≥90%-identical schemas collapse to `cloudflare:cdn_resource_{create,get,update,delete}` — one k-slot) and adaptive top-k (extend while score[i] ≥ 0.7×score[0], cap 7). Only if arm 7 shows near-duplicate families measurably hurting selection.

## Spec 083 profiler — arms and gates

Arms: `full` (baseline) vs `compact` vs `compact+families` (Phase 3), on the replay corpus + bench golden set.

| # | Metric | Gate | Gates phase |
|---|---|---|---|
| 1 | Discovery tokens p50/p95/max | median ≤1,000; max ≤1,500 | 1→2 |
| 2 | Recall@5 result-ID identity, compact vs full | 100% | 1→2 |
| 3 | describe_tool calls per completed task | <0.3 | 2 |
| 4 | InvalidParams retries per call (after self-heal) | no significant increase vs full | 0→1, 2 |
| 5 | Lossy-signature rate across corpus | <20% | 1→2 |
| 6 | End-to-end agentic-loop tokens + break-even curve vs naive menu, per profile size | −85%+ e2e survives real loops | 2 default flip |
| 7 | Correct-verb rate on near-identical-schema families | regression vs full triggers Phase 3 | 3 |

## Rejected alternatives

- **Anthropic-native path via `listChanged` dynamic registration (Design 1)**: the 49→74% accuracy figure is an Anthropic *API* Tool Search Tool measurement and doesn't transfer to MCP `listChanged`; no MCP capability advertises deferred-loading support (client name-sniffing is fragile); `server:tool` refs violate the tool-name charset (mcpproxy's own direct mode already sanitizes to `__`); Cursor's listChanged handling is inconsistent; the per-session LRU could evict a tool between search and call. Effectively Claude-only complexity for zero portable gain.
- **Hierarchical two-stage server cards (Design 2)**: introduces a genuinely new failure mode (stage-1 misrouting) into the metric that most needs protecting, plus response-shape polymorphism that undercuts predictability. Its best ideas (recall hedge, family grouping, adaptive-k) survive here without the routing hop — flat compact signatures at k=5–7 already fit the token budget cards were meant to buy.
- **ToolFS / code-first as the default (Design 3)**: `.d.ts` tree in the sandbox is a heavier mental model requiring `enable_code_execution`; the JSONSchema→TypeScript compiler is a real subsystem; and it duplicated routing config the codebase already has. `code_execution` remains an orthogonal `routing_mode`; ToolFS can layer onto it later.
- **New search tool names (`find_tools`/`search_tools`) + aliases**: renames break existing agent prompts for zero functional gain; "alias returns compact format" is not zero-breakage. Keeping `retrieve_tools` **is** the migration.
- **TOON/tabular encodings**: parsing-cascade risk on listings; signatures are for reading, not parsing.
