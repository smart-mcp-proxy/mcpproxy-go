# mcpproxy benchmark harness

The reproducible numbers behind mcpproxy's marketing claims — **token reduction**,
**discovery accuracy**, and **latency** — comparing three ways an agent can be
wired to upstream MCP tools.

> Roadmap item #19 (MCP-42). In-repo (`bench/`), reproducible, intended to be
> refreshed on release. Reports are **never committed** (Spec 065 CN-003); only
> code, fixtures, and this methodology are versioned.

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
go run ./bench/cmd/bench            # scores the committed Spec 065 corpus
go test ./bench/                    # unit + invariant tests
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
  (already a repo dependency). It is a **model-agnostic estimator**; exact
  counts for a specific pinned model (e.g. Claude) will differ, but the
  *relative* savings between modes are stable.
- **Proxy-mode tools**: the *complete* per-mode catalog, derived from the live
  server builders — discovery, the call-tool variants, `code_execution`, **and
  the shared management tool set** (`upstream_servers`, `quarantine_security`,
  `search_servers`, `list_registries`). Nothing the agent actually sees is
  dropped from the proxy cost.
- **Cost of a tool**: `name + "\n" + description`. JSON input schemas are
  excluded **uniformly** across all modes (the committed corpus snapshot does
  not carry schemas).
- **Savings** for a mode `m`: `1 - tokens(m) / tokens(baseline)`.

### Known limitations (read before quoting a number)

- **Schemas excluded — direction is not clean.** Input schemas are dropped from
  *both* sides. The 45 baseline tools lose their schemas, but so do the proxy
  modes' management tools (e.g. `upstream_servers` carries a large multi-field
  schema). So the name+description-only number is **not** unambiguously
  conservative — it is its own well-defined metric. The live run below adds full
  schemas from `GET /api/v1/tools` for the exact headline number; quote that for
  marketing, not this offline estimate.
- **Savings scale with tool count.** The 45-tool reference corpus is small; real
  deployments expose hundreds–thousands of tools, where the baseline grows
  linearly and the proxy context stays fixed, so savings approach the asymptote.
  Quote the corpus size alongside any percentage.
- **`cl100k_base` ≠ the pinned model's tokenizer.** Pinning the exact tokenizer
  for the headline model is tracked as a follow-up (see "Roadmap").

## Live run — full schemas + accuracy + latency

The live run boots mcpproxy over the Spec 065 reference-server config and
measures the three headline claims against a *running* proxy. Everything here is
still deterministic and LLM-free.

```bash
# 1. Boot the reproducible substrate (proxy + 7 no-auth reference servers)
docker compose -f bench/docker-compose.yml up --build -d

# 2. Score against the running proxy (writes bench/results/live_report.json)
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
    (`authoritative_headline: false`). Never quote a withheld run.
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

## What is scoped but not yet built (follow-ups)

These require decisions and/or other roles, so they are tracked as child issues
rather than landed here:

- **End-to-end task success with a pinned LLM** — requires a pinned model + an
  LLM-call budget; this is the only part that costs spend.
- **CI publish-on-release-tag → public static dashboard** — Release/DevOps lane.

## Dataset sources & provenance

- Tool corpus + retrieval golden set: Spec 065 frozen datasets
  (`specs/065-evaluation-foundation/datasets/`), generated from 7 permissively
  reachable no-auth reference servers (filesystem, git, memory, sqlite, fetch,
  time, sequential-thinking).
- Proxy + management tool definitions: derived at run time from the live server
  tool builders (`internal/server/mcp_routing.go` →
  `buildCallToolModeTools` / `buildCodeExecModeTools`, exposed via
  `internal/server.ProxyModeToolDefs`). No hand-maintained fixture — the
  benchmark cannot drift from the tools the proxy actually serves.

## Reproducible live run

`docker-compose.yml` boots mcpproxy over the frozen reference-server config so
the corpus and live tool list are reproducible across machines. The live
accuracy/latency/full-schema scorers attach to it via `-live` (see "Live run"
above). Pin the upstream-server images before publishing headline numbers
(image drift can change the tool corpus).

## Reviewer contact

Methodology questions / disputes: open an issue in `smart-mcp-proxy/mcpproxy-go`
and tag the maintainers, or comment on the roadmap benchmark ticket (MCP-42).
