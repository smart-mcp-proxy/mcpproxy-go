# mcpproxy benchmark harness

The reproducible numbers behind mcpproxy's marketing claims — **token reduction**,
**discovery accuracy**, and **latency** — comparing three ways an agent can be
wired to upstream MCP tools, plus the **discovery-effectiveness profiler**
(Spec 083): encoding-arm comparisons, real-MCP response cost, break-even
analysis, session-cost estimates, public-corpus validation, and an independent
LAP verdict.

> Roadmap item #19 (MCP-42) + Spec 083. In-repo (`bench/`), reproducible,
> intended to be refreshed on release. Reports are **never committed** (Spec 065
> CN-003); only code, fixtures, and this methodology are versioned.

## The three modes

| Mode | What the agent sees in context | mcpproxy server |
|------|--------------------------------|-----------------|
| `baseline` | Every upstream tool definition, loaded directly | (no proxy discovery) |
| `retrieve_tools` | `retrieve_tools` + `call_tool_read/write/destructive` + `read_cache` + `code_execution` + management tools; tools found on demand via BM25 | `callToolServer` |
| `code_execution` | `code_execution` + `retrieve_tools` + management tools; many tools orchestrated from sandboxed JS in one round-trip | `codeExecServer` |

Both proxy modes also append the shared **management tool set** —
`upstream_servers`, `quarantine_security`, `search_servers`, `list_registries`
— that the live routing-mode servers expose. These count against the proxy
context cost: omitting them undercounts that cost and inflates the savings.

The per-mode catalog is **derived directly from the live tool builders**
(`buildCallToolModeTools` / `buildCodeExecModeTools` in
`internal/server/mcp_routing.go`, via `server.ProxyModeToolDefs`), so it can
never drift from production.

## What ships today (deterministic, offline)

The **token-reduction** measurement is fully deterministic and runs with no
network or LLM:

```bash
go run ./bench/cmd/bench            # scores the committed Spec 065 corpus (v1 report)
make bench-discovery                # Spec 083 profiler: all encoding arms on corpus_v2 (v2 report)
go test ./bench/...                 # unit + invariant tests
```

It counts the context-token cost of each mode over a **frozen tool corpus** and
reports the savings of each proxy mode versus the baseline. Output: a
`report.json` and a self-contained `dashboard.html` in `bench/results/`
(gitignored).

#### Current deterministic result

Over the 45-tool Spec 065 reference corpus, counting **tool name + description
only** (schemas excluded uniformly — see limitations), `cl100k_base`:

| Mode | Context tools | Tokens | Savings vs. baseline |
|------|---------------|--------|----------------------|
| `baseline` | 45 | 1730 | — |
| `retrieve_tools` | 10 | 1431 | **~17%** |
| `code_execution` | 6 | 986 | **~43%** |

These are deliberately modest: the proxy context here is the *full* per-mode
tool set (discovery + call-tool variants + management tools), and the corpus is
small. Savings grow toward the asymptote as the upstream tool count rises (the
baseline grows linearly while the proxy context stays fixed) — always quote the
corpus size alongside a percentage. Reproduce with `go run ./bench/cmd/bench`.

### Scoring rubric — token reduction

- **Tool universe**: the frozen Spec 065 snapshot
  `specs/065-evaluation-foundation/datasets/corpus_v1.tools.json` — 45 tools
  across 7 no-auth reference servers. Frozen + versioned so scoring never runs
  against a drifting corpus (CN-002).
- **Tokenizer**: `tiktoken cl100k_base`, a widely-used reproducible BPE
  (already a repo dependency). It is a **model-agnostic estimator** — see the
  tokenizer caveat under "Known limitations" before quoting an absolute number.
- **Proxy-mode tools**: the *complete* per-mode catalog, derived from the live
  server builders — discovery, the call-tool variants, `code_execution`, **and
  the shared management tool set** (`upstream_servers`, `quarantine_security`,
  `search_servers`, `list_registries`). Nothing the agent actually sees is
  dropped from the proxy cost.
- **Cost of a tool**: `name + "\n" + description`. JSON input schemas are
  excluded **uniformly** across all modes (the committed corpus snapshot does
  not carry schemas). The Spec 083 profiler measures schema-bearing renderings
  on `corpus_v2` instead.
- **Savings** for a mode `m`: `1 - tokens(m) / tokens(baseline)`.

## Discovery-effectiveness profiler (Spec 083)

The profiler answers three questions the v1 harness could not: **what does a
`retrieve_tools` response actually cost** over the real MCP protocol, **which
tool-definition encoding is cheapest** without wrecking retrieval quality, and
**do the in-house numbers survive external corpora and an independent linter**.
Output is a versioned **v2 report** (`report.json` conforming to
`specs/083-discovery-profiler/contracts/report-v2.schema.json`) plus a
self-contained `dashboard.html`, where every headline number carries a
**provenance badge**: `measured` (observed on the wire), `computed`
(deterministic arithmetic over measurements), or `estimated` (a model with
documented assumptions).

### Quickstart

One command (SC-008):

```bash
make bench-discovery
# = npm ci --prefix bench/tscg  (pinned @tscg/core, one-time)
#   + go run ./bench/cmd/bench -corpus-v2 ... -arms all -out bench/results
# → bench/results/report.json (v2) + bench/results/dashboard.html
# CI runs this same target.
```

Offline (deterministic, no network):

```bash
# All mandatory arms on the schema-bearing frozen corpus
go run ./bench/cmd/bench \
  -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json \
  -arms baseline_json,compact_sig,tscg,toon_listing \
  -out bench/results
```

TSCG arm prerequisite (covered by `make bench-discovery`): `npm ci --prefix
bench/tscg` (Node ≥20). Without it the arm reports `skipped: node runtime
unavailable` (allowed locally, fails SC-002 in CI).

Live (response cost + break-even + latency):

```bash
# 1. Boot the snapshot proxy (same as existing bench live mode)
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy serve --config specs/065-evaluation-foundation/datasets/snapshot-servers.config.json \
  --listen 127.0.0.1:8092 &   # MCPPROXY_API_KEY=eval-corpus-snapshot

# 2. Run live measurement (real MCP retrieve_tools calls per golden query)
go run ./bench/cmd/bench -live \
  -proxy http://127.0.0.1:8092 -api-key eval-corpus-snapshot \
  -golden specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json \
  -out bench/results
```

Public corpora:

```bash
# ToolRet (runtime fetch — never committed; license unstated upstream)
./scripts/fetch-toolret.sh                # uv-run parquet→JSON into bench/results/cache/toolret/
go run ./bench/cmd/bench -toolret bench/results/cache/toolret \
  -subset 250 -seed 42 -arms baseline_json,compact_sig -out bench/results

# LiveMCPTool (committed Apache-2.0 snapshot) — token/scale measurement
go run ./bench/cmd/bench \
  -livemcptool specs/083-discovery-profiler/datasets/livemcptool_snapshot \
  -arms baseline_json,compact_sig,tscg,toon_listing -out bench/results
```

Independent LAP verdict (what CI runs):

```bash
uvx --from lap-score==0.8.0 lap lint \
  --mcp-url "http://127.0.0.1:8092/mcp?apikey=eval-corpus-snapshot" --json > bench/results/lap.json
```

Regenerating fixtures (maintainers only):

```bash
./scripts/gen-corpus-v2.sh     # schema-bearing frozen corpus from booted snapshot proxy
# result_fixtures_v1.json: captured once from reference servers; see datasets/README
```

Verifying success criteria:

- SC-003 gate: baseline arm on in-house golden set must report recall@5 = 0.68 ± 0.05.
- SC-001: report.json `.response_cost.p50/.p95`, `.break_even.break_even_calls` populated after a live run.
- SC-005: dashboard shows provenance badge + tokenizer caveat on every headline number.

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-corpus` | `specs/065-evaluation-foundation/datasets/corpus_v1.tools.json` | Legacy v1 corpus (name+description only); used when no profiler flag is set |
| `-out` | `bench/results` | Output directory for `report.json` + `dashboard.html` |
| `-encoding` | `cl100k_base` | tiktoken encoding name |
| `-live` | off | Live benchmark against a running proxy: full-schema tokens, accuracy, latency, **and** real-MCP response cost + break-even |
| `-proxy` | `http://127.0.0.1:8092` | Live proxy base URL |
| `-api-key` | `eval-corpus-snapshot` | Live proxy API key (`X-API-Key`; the MCP session uses `?apikey=`) |
| `-golden` | `specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json` | Retrieval golden set (the SC-003 gate corpus) |
| `-arms` | (empty) | Comma-separated encoding arms, or `all` — enables profiler mode. `toon_results` is selectable here as a fixture-driven pseudo-arm |
| `-corpus-v2` | (empty) | Spec 083 schema-bearing frozen corpus (`corpus_v2.tools.json`) |
| `-toolret` | (empty) | ToolRet cache dir from `scripts/fetch-toolret.sh` (revision dir, or its parent with exactly one revision) — runtime fetch, never committed |
| `-livemcptool` | (empty) | LiveMCPTool committed snapshot directory (or its `tools.json`) |
| `-subset` | `250` | Seeded ToolRet query-subset size (FR-014) |
| `-seed` | `42` | Seed for the deterministic query subset — same revision+seed+size ⇒ same subset |
| `-lap-json` | (empty) | Path to a LAP lint artifact to merge as the independent verdict |
| `-result-fixtures` | `specs/083-discovery-profiler/datasets/result_fixtures_v1.json` | Tool-result fixture set for the `toon_results` arm |

Any of `-arms` / `-corpus-v2` / `-toolret` / `-livemcptool` switches the CLI
into profiler mode (v2 report); `-live` runs the live benchmark and *also*
emits a v2 report; with none of them the legacy v1 offline run executes.

### Encoding arms

An **arm** is one deterministic way of rendering tool definitions into
agent-context text (`bench/arms/`; behavioral contract in
`specs/083-discovery-profiler/contracts/arm-interface.md`). Arms are
byte-deterministic (FR-010), fail explicitly instead of truncating silently
(an unencodable tool is a counted *skip*, with examples in the report), and
declare whether they alter what the retrieval index ingests — index-altering
arms are scored for retrieval quality through the production Bleve index
(`bench/armindex.go`), where the baseline arm must reproduce the golden-set
recall@5 = 0.68 ± 0.05 (SC-003 gate).

| Arm | What it renders (one-line example) | Index-altering | Lower-bound |
|-----|-------------------------------------|:---:|:---:|
| `baseline_json` | Full definition, canonical JSON schema — `fetch` ⏎ `Fetches a URL from the internet…` ⏎ `{"properties":{"url":{…}},"required":["url"],…}`. THE canonical renderer: it is also the naive-menu count, every savings denominator, and the break-even input | no | no |
| `compact_sig` | Flat signature `fetch(url:string, max_length?:int, raw?:bool, start_index?:int)\|Fetches a URL from the internet…` — required vs `opt?` params, nested objects collapse to `obj`, descriptions preserved verbatim | yes | no |
| `tscg` | Reference TSCG compiler (pinned `@tscg/core@1.4.3` via the committed Node shim `bench/tscg/shim.mjs`, JSONL protocol) — `fetch: Fetches a URL…` ⏎ `  max_length (integer): Maximum number of characters… \| raw (bool): …`. Rewrites/elides description filler, so savings are a **lower bound**. Without node/`node_modules` the whole arm is a skip-with-reason row (never per-tool failures) | yes | yes |
| `toon_listing` | Official [TOON](https://github.com/toon-format) encoding (toon-go) of `{name, description, inputSchema}` per tool — `- name: fetch` ⏎ `  description: "Fetches a URL…"` ⏎ `  inputSchema:` ⏎ `    properties: …` | yes | no |
| `tron_dedup` | In-tree TRON-style named-class schema dedup (no upstream Go impl): byte-identical canonical schemas declared once in the listing preamble — `class Cbae0cfe3 = {…canonical schema…}` — and referenced per tool as `fetch\|Fetches a URL…\|Cbae0cfe3`. Content-addressed class names, exact-bytes dedup key: deliberately conservative vs. the paper (deviations documented in `bench/arms/tron.go`) | yes | no |
| `toon_results` | Pseudo-arm over **tool-call outputs**, not definitions: the committed `result_fixtures_v1.json` payloads encoded as TOON vs. a compact-JSON baseline of the same payloads, split tabular vs. non-tabular. Selectable via `-arms`; included automatically by `-arms all` when the fixture file exists | n/a | no |

Listing-level amortization (TOON header, TRON class preamble) lives only in
`EncodeListing`; per-tool means stay comparable across arms. Each arm row also
reports skip counts with examples, the top-N heaviest tools, and the corpus's
degenerate-description count (FR-020: description missing, shorter than 20
runes, or matching stub patterns) so cheap-but-useless corpora are visible.

### Live response cost, break-even, and session estimates (US1)

The `-live` run now measures what the v1 REST-based run could not — the
**response** an agent pays for on every discovery call:

- **Real MCP protocol.** `bench/mcpcall.go` speaks streamable-HTTP MCP
  (mark3labs/mcp-go) against the proxy's `/mcp` endpoint and calls
  `retrieve_tools` for every golden query — capturing the exact response text
  an agent's context ingests (REST `/api/v1/index/search` misses
  `usage_instructions`, `call_with`, annotations, `session_risk`), plus
  client-measured latency. Provenance: `measured`.
- **Span-based component attribution** (`bench/respcost.go`). Per-query tokens
  split into `input_schemas` / `descriptions` / `usage_instructions` /
  `metadata` / `other`. BPE is not additive across concatenation boundaries, so
  the response is partitioned into labeled byte spans, tokenized **once**, and
  each token attributed to the span owning its starting byte — components sum
  *exactly* to the total (FR-002), by construction. Percentiles: p50/p95/max +
  mean.
- **Break-even** (`bench/breakeven.go`): `break_even_calls =
  (naive_full_menu_tokens − proxy_menu_tokens) / mean_response_tokens`, all
  three inputs echoed in the row so the number is recomputable. Both menus come
  from the same canonical renderer. Numerator ≤ 0 ⇒ explicit `no_break_even`
  row; break-even is withheld when menu token counts are non-authoritative
  (see the MCP-3161 safety valve below). Provenance: `computed`.
- **Session-cost estimator** (`bench/session.go`) — an ESTIMATE, not a
  measurement: `session_cost(arm, calls) = proxy_menu + calls ×
  mean_response(arm) × (1 + retry_rate(arm))` for `calls ∈ {1,3,5,10}`.
  `retry_rate` defaults are literature-derived (0.0 for format-native
  JSON/compact/TSCG/TRON; 0.05 for `toon_listing`, per the parsing-cascade
  evidence in arXiv:2605.29676 §5) and echoed in every row. Provenance:
  `estimated`, always.

### LAP independent verdict (US4)

CI runs the version-pinned external linter `lap-score==0.8.0` against the
booted proxy (command in the quickstart) and merges its artifact via
`-lap-json`: the report's `lap` block carries LAP's letter grade and
menu-token count, compared against the in-house count of the same surface.
Both use `cl100k_base` but frame the menu differently, so divergence within
**±15%** is expected; beyond it the report shows a **non-blocking** warning
(FR-016). LAP being absent or broken never fails the benchmark (FR-015): every
failure path yields `executed: false` with an explicit skip reason.

## Live run — full schemas + accuracy + latency

The live run boots mcpproxy over the Spec 065 reference-server config and
measures the headline claims against a *running* proxy. Everything here is
still deterministic and LLM-free.

```bash
# 1. Boot the reproducible substrate (proxy + 7 no-auth reference servers)
docker compose -f bench/docker-compose.yml up --build -d

# 2. Score against the running proxy (writes bench/results/live_report.json + v2 report)
go run ./bench/cmd/bench -live -proxy http://127.0.0.1:8092 -api-key eval-corpus-snapshot
```

What it adds over the offline token run:

- **Exact token number (full schemas).** Pulls `GET /api/v1/tools` for the
  upstream tools *with their full JSON input schemas* and counts them against
  the proxy modes — whose management-tool schemas come from the same live
  builders as the offline run (`server.ProxyModeToolDefs`). Because schemas are
  counted on **both** sides, the savings is authoritative.
  - **Safety valve (MCP-3161):** if any proxy tool is missing a schema, counting
    the baseline's schemas alone would *overstate* savings, so the run
    **withholds the headline %** and reports raw token totals only
    (`authoritative_headline: false`) — and withholds break-even, which depends
    on the same menus. Never quote a withheld run.
- **Accuracy.** Replays `retrieval_golden_v1.json` through the proxy's BM25
  search (`GET /api/v1/index/search`) and scores **Recall@{1,3,5,10}, MRR,
  nDCG@10, MAP** against the graded labels. Deterministic (BM25), so a single
  run is reported (`runs_averaged: 1`). The emitted `retrieval` block **conforms
  to** the Spec 065 `score-report.schema.json` shape — nested `metrics` + `gate`
  (verified by a schema-validation test). A standalone live run has no stored
  baseline to regress against, so `gate.passed` is `true` by construction;
  CI regression-gating against a committed baseline is the MCP-3133 lane.
- **Latency.** Client-measured per-query search latency (p50/p95/p99/max) vs.
  the one-shot cost of loading all tools. Measured client-side on purpose: the
  server's `SearchToolsResponse.took` field is currently a `"0ms"` stub.
- **Response cost + break-even (Spec 083 US1)** — see the profiler section
  above. The live run emits both the v1 `live_report.json` and the v2
  `report.json`/`dashboard.html`.

## What is scoped but not yet built (follow-ups)

These require decisions and/or other roles, so they are tracked as child issues
rather than landed here:

- **End-to-end task success with a pinned LLM** — requires a pinned model + an
  LLM-call budget; this is the only part that costs spend. Until then, the
  session-cost estimator above is the honest substitute — and it is labeled
  `estimated`, never `measured`.
- **CI publish-on-release-tag → public static dashboard** — Release/DevOps lane.

## Dataset sources & provenance

Full details and regeneration procedures:
`specs/083-discovery-profiler/datasets/README.md` (immutability rule: a
refresh is `*_v3.*`, never an edit of a committed `*_v2.*` file).

- **Spec 065 datasets** (`specs/065-evaluation-foundation/datasets/`): the
  45-tool `corpus_v1` snapshot (name+description) and
  `retrieval_golden_v1.json`, generated from 7 permissively reachable no-auth
  reference servers (filesystem, git, memory, sqlite, fetch, time,
  sequential-thinking).
- **`corpus_v2.tools.json`** (committed): the same 45 tools exported **with
  full JSON input schemas** — the universe for all encoding-arm comparisons.
  Captured 2026-07-14 (`corpus_v2@2026-07-14`), canonicalized (sorted tool IDs,
  sorted object keys). Provenance note: the export reads the **Bleve index**
  (`scripts/gen-corpus-v2.sh`), *not* `GET /api/v1/tools` — that REST endpoint
  currently serves stub schemas (supervisor StateView never parses
  `ParamsJSON`); the index is the authoritative record of what the retrieval
  funnel ingests, which is exactly what the arm comparison must measure.
- **`result_fixtures_v1.json`** (committed): 6 deterministic tool-call outputs
  captured once through the proxy from 5 reference servers for the
  `toon_results` arm — 2 tabular, 4 non-tabular; wall-clock values and
  capture-host specifics stripped. License-clean: outputs of our own reference
  servers over our own seed data (`fetch_fixture.html` is the committed fetch
  input).
- **LiveMCPTool snapshot**
  (`specs/083-discovery-profiler/datasets/livemcptool_snapshot/`, committed):
  frozen, normalized copy of the LiveMCPBench tool corpus — **70 servers / 527
  tools**, pinned Hugging Face revision `ddea2d24`
  ([ICIP/LiveMCPBench](https://huggingface.co/datasets/ICIP/LiveMCPBench),
  arXiv:2508.01780). **Apache-2.0**, redistributed with the mandatory
  attribution in the snapshot's `ATTRIBUTION.md`. Used for token/scale
  measurement only: its task annotations name tools as unqualified free text
  (5/150 names resolve to no corpus tool, 13/150 to multiple servers), so
  relevance labels are *not derivable* — the loader records that absence
  explicitly rather than guessing (FR-011).
- **ToolRet** (never committed): the upstream `mangopy/ToolRet-Tools` +
  `ToolRet-Queries` datasets carry **no stated license**, so redistribution is
  not clearly permitted — the harness **fetches at runtime only**
  (`scripts/fetch-toolret.sh`, pinned revision, `uv run` parquet→JSON) into the
  gitignored `bench/results/cache/` and never writes ToolRet bytes into git
  (FR-013). Retrieval scoring runs on a **seeded query subset**
  (`-subset`/`-seed`): same revision + seed + size ⇒ byte-identical subset on
  every machine. Attribution: ToolRet tool-retrieval benchmark (Hugging Face;
  ACL 2025).
- **Proxy + management tool definitions**: derived at run time from the live
  server tool builders (`internal/server/mcp_routing.go` →
  `buildCallToolModeTools` / `buildCodeExecModeTools`, exposed via
  `internal/server.ProxyModeToolDefs`). No hand-maintained fixture — the
  benchmark cannot drift from the tools the proxy actually serves.
- **TSCG shim** (`bench/tscg/`): committed `shim.mjs` + `package-lock.json`
  pinning `@tscg/core@1.4.3`; `npm ci --prefix bench/tscg` (Node ≥20)
  reproduces the exact runtime everywhere, including CI.

## Known limitations (read before quoting a number)

- **Tokenizer caveat — absolute numbers are estimates.** All token counts use
  `tiktoken cl100k_base` as a reproducible, model-agnostic, offline estimator.
  It can **underestimate other tokenizers (e.g. Claude's) by up to ~60%**, so
  never quote an absolute token count as a Claude cost — but **relative savings
  between arms and modes are stable** across vocabularies, and those are the
  headline numbers. The v2 dashboard renders this caveat next to every
  absolute number (SC-005).
- **The session estimator is an ESTIMATE, not a measurement.** The multi-turn
  session cost is a model — `proxy_menu + calls × mean_response × (1 +
  retry_rate)` — whose retry rates are literature-derived defaults, not
  observations of a live agent loop. Every estimator row is provenance-labeled
  `estimated` and echoes its inputs; treat it as a decision aid, not a benchmark
  result. A measured agent loop is the pinned-LLM follow-up above.
- **Lower-bound arms.** Arms that rewrite or elide description text (TSCG)
  report savings as a lower bound: what they drop cannot be priced back in.
- **v1 offline run: schemas excluded — direction is not clean.** In the legacy
  name+description-only run, input schemas are dropped from *both* sides, so
  that number is its own well-defined metric, not unambiguously conservative.
  The live run adds full schemas for the exact headline number; the Spec 083
  arms measure schema-bearing renderings on `corpus_v2`. Quote those, not the
  v1 offline estimate.
- **Savings scale with tool count.** The 45-tool reference corpus is small; the
  527-tool LiveMCPTool snapshot and the ToolRet subset exist precisely to test
  scale. Quote the corpus size alongside any percentage.
- **LiveMCPTool has no relevance labels** (see provenance above): it validates
  token/scale claims only, never retrieval quality.
- **LAP divergence is expected.** LAP frames the tool menu its own way; within
  ±15% of the in-house count is normal, beyond it is a non-blocking warning —
  never a hidden discrepancy, never a build failure.

## Reproducible live run

`docker-compose.yml` boots mcpproxy over the frozen reference-server config so
the corpus and live tool list are reproducible across machines. The live
accuracy/latency/full-schema/response-cost scorers attach to it via `-live`
(see "Live run" above). Pin the upstream-server images before publishing
headline numbers (image drift can change the tool corpus).

## Reviewer contact

Methodology questions / disputes: open an issue in `smart-mcp-proxy/mcpproxy-go`
and tag the maintainers, or comment on the roadmap benchmark ticket (MCP-42).
