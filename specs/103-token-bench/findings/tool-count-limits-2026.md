# Research: is "40-128 tools" still a real constraint? (2026)

**Date**: 2026-09-01. **Method**: 4 independent search angles, adversarial per-claim
verification, synthesis, completeness critic. 30 agents, 89 claims.

> **Read the critic in section C first.** It judges this research **"not thorough"** and
> names three systematic holes, two of which it says INVERT conclusions the synthesis
> states confidently. The synthesis is not settled fact; it is the best current reading
> with its own gaps documented.

## Why this exists

Spec 103's break-even argument leaned on a claim inherited from 2025: that above roughly
40-128 tools a user physically cannot connect everything, so mcpproxy's value is
CAPABILITY rather than token saving. That claim is a year old. This checks it.

---

## A. Synthesis

# Can a user still not connect more than 40–128 tools? (2026 synthesis)

**Compiled 2026-09-01.** Every figure below carries a date. Claims that failed adversarial verification are marked ⚠️ **REFUTED** rather than dropped, because most of them are still in wide circulation.

---

## 1. Direct answer

**No — the tool-count cap is no longer the binding constraint on fleet size, and for the median user it never was.** In 2026 the binding constraints are, in order: (a) *tool-selection quality*, which Anthropic documents as degrading past **30–50 available tools** with nothing erroring; (b) *context budget*, ~**55k tokens of definitions for a 5-server setup**; and only then (c) residual hard caps, which now bind on what a client **sends** in one request (OpenAI 128, VS Code 128, Antigravity 100) rather than on what a user **connects**. The 2026 industry answer to large tool surfaces was not a raised ceiling but **deferred loading** — Claude Code, Cursor, VS Code, Kiro and Amp all now put tool *names* in context and fetch definitions on demand — which means "you cannot connect more than N" has quietly become "you cannot usefully *expose* more than ~30–50 at once."

---

## 2. The dated table

Columns: subject | limit kind | 2026 value | 2025 value if changed | source | confidence.

### 2a. Provider APIs

| Subject | Kind | 2026 value | 2025 value if changed | Source (date) | Conf. |
|---|---|---|---|---|---|
| OpenAI Chat Completions `tools` | **hard cap** | **128** — HTTP 400 `array_above_max_length`. Enforced but **undocumented** on the live param | unchanged | Error text reproduced in the wild through 2026-07; Azure quotas doc `ms.date 2026-08-20` states "Maximum number of /chat/completions tools \| 128" | High (value) / Med (docs) |
| OpenAI Responses API `tools` | — | ⚠️ **REFUTED as a documented cap.** `openai-openapi` v2.3.0 (fetched 2026-09-01) puts **no `maxItems`** on `ToolsArray`. A ~128 server-side ceiling probably still applies by inference, not documentation | claimed 128 (2025-11-10, Zed #42393 — third-party, endpoint not isolated) | openai/openai-openapi master (2026-08-31) | Low |
| OpenAI docs "documents no cap" | observation | Confirmed **for live surfaces**. But 128 *is* published as `maxItems` on the **deprecated** Chat `functions` param and the **Assistants API** (sunset 2026-08-26) | — | openapi.yaml, 2026-08-31 | High |
| OpenAI function-calling guide | **recommended** | "Aim for fewer than 20 functions available at the start of a turn… **just a soft suggestion**" | unchanged (page undated; proven 2026-edited — documents `tool_search`, gpt-5.6) | developers.openai.com (verified 2026-09-01) | Med-High |
| OpenAI Structured Outputs (applies to tool param schemas) | **hard cap**, only when `strict: true` | 5,000 properties · **10 levels of nesting** · 120,000 schema chars · 1,000 enum values · (+15,000-char budget for any enum >250 values) | 2025-07-11 raise from 100 props / 15,000 chars / 500 enums. ⚠️ The widely-quoted "**5 levels of nesting**" is **wrong** — it is 10; the raise was made silently, no changelog entry | developers.openai.com (2026-09-01); Azure's page still says 100/5 as of 2026-08-24 — **vendors disagree** | High |
| Anthropic Messages `tools` (whole array) | none documented | No `maxItems` on `tools`. Meaningful silence: the same reference *does* cap `skills` at 20 and `messages` at 100,000 | — | platform.claude.com/docs/en/api/messages (2026-09-01) | High |
| Anthropic tool search, deferred tools | **hard cap** | **10,000** tools with `defer_loading: true` per request; ≥1 tool must be non-deferred (400 otherwise) | GA'd 2026-02-05 (beta header dropped) | platform.claude.com tool-search-tool | High |
| Anthropic tool selection | **degradation threshold** | "degrades once you exceed **30–50 available tools**" | — | same page (undated, actively maintained) | High for the quote / Med for the number's provenance |
| Anthropic tool-def token budget | **recommended** | Use tool search at **10+ tools or >10k tokens** of definitions; ~**55k tokens** for a typical 5-server setup | — | same page | High |
| Google Gemini 3 `functionDeclarations` | **hard cap** | **512** — "At most 512 function declarations can be specified" | supersedes the widely-cited **128** | gemini-cli #19083, observed on `gemini-3-pro-preview` (2026-02-14). **No Google page states 512** | Med (behaviour only) |
| Google Vertex / Enterprise Agent Platform docs | **hard cap** (documented) | **128** — *contradicted by Google's own API.* Google's docs disagree with Google's runtime | — | docs.cloud.google.com (undated) | Low |
| Google Gemini best practices | **recommended** | "Keep active set to **10–20 tools maximum**" | — | ai.google.dev (2026-08-26) | High |
| Azure OpenAI `/chat/completions` tools & legacy functions | **hard cap** | **128** each | unchanged | MicrosoftDocs/azure-ai-docs (2026-08-20) | High |
| AWS Bedrock Converse `toolConfig.tools` | none documented | "Minimum number of 1 item" and **no maximum** — unusual for AWS, which normally documents both bounds. Per-model caps may still apply underneath | — | AWS API reference (2026-09-01) | High (that docs are silent) |
| Mistral function calling | none found | No cap published | — | docs.mistral.ai (2026-09-01) | Low (absence of evidence) |
| Groq `tools` | **hard cap** | 128 claimed | — | single third-party GH issue, **no retrievable date** | Low |

### 2b. Clients

| Subject | Kind | 2026 value | 2025 value if changed | Source (date) | Conf. |
|---|---|---|---|---|---|
| VS Code + GitHub Copilot | **hard cap on tools *sent*** | **128 per request.** Above the threshold, VS Code auto-groups into "virtual tools" behind `activate_*` stubs, so >128 can be *enabled* while ≤128 are sent. When grouping is off it errors: `Tool limit exceeded (132/128)` | 128 unchanged; virtualization shipped v1.103 (2025-07) | code.visualstudio.com docs stamped **2026-08-26** (v1.135); microsoft/vscode #290356, #294055 | High |
| VS Code `virtualTools.threshold` | **default setting** | Default 128; adjustable **downward only** — setting 256 "produces no effect" | — | microsoft/vscode #294055 (2026-02-10, unanswered by maintainers) | Med |
| Cursor — "40-tool cap" | — | ⚠️ **REFUTED / STALE.** Not in Cursor's docs. Originates in forum posts dated **2025-06-23/24**. Cursor staff (2026-07-14) recharacterize the constraint as an upstream **per-model provider** capacity limit, not a Cursor cap | 40 was a 2025 soft warning that degraded, not a hard cap | cursor.com/docs/mcp (raw-HTML grep, 2026-09-01: zero hits for any tool-count number) | High that 40 is stale |
| Cursor — deferred loading | **default behaviour** (no toggle found) | Tool definitions live as JSON in `.cursor`; agent gets **names only**, loads on demand; vendor-reported **46.9%** agent-token reduction | new in 2026 | Shipped in **Cursor 2.4, 2026-01-22** — *not* the blog's 2026-01-06, which was forward-looking ("coming weeks"); staff confirmed on 2026-01-14 it was not yet in stable | High |
| Cursor CLI (`cursor-agent`) | **hard cap**, undocumented | Errors `Too many MCP tools are enabled for this model` — the CLI sends **all** tool definitions in one request while the IDE sends far fewer. Threshold is model-dependent; **no published number**. ⚠️ The framing "no Cursor-imposed cap" is wrong — the gate is client-side, only the threshold is upstream | new 2026 distinction | forum.cursor.com/t/cli-mcp-tool-limits/165642 (2026-07-13/14, staff reply) | Med |
| Cursor IDE observed | anecdote | 80+ tools enabled, no warning — **n=1, no staff confirmation**. Do not quote 80 as a threshold | contradicts 40 | forum thread 153432 (2026-03-03) | Low |
| Claude Code | none documented | No cap on MCP servers or tools; "work with hundreds of tools without context overhead" | 2025 advice to hand-prune is obsolete | code.claude.com/docs/en/mcp (2026-08) | High |
| Claude Code tool search | **default setting** | **ON by default**; disable with `ENABLE_TOOL_SEARCH=false` | opt-in/absent in 2025 | same | High |
| Claude Code MCP output | **default setting** | 25,000 tokens/result (`MAX_MCP_OUTPUT_TOKENS`), warning >10,000, per-tool override to 500,000 chars | — | same | High |
| Google Antigravity IDE | **hard cap** | **100**, hardcoded; **rejects the whole server load** with "enabled tools would exceed max limit of 100" | new 2026 entrant | gemini-cli #26678 (2026-05-07) | Med |
| Gemini CLI | contested | ~100 with silent drops **(#21823, 2026-03-10, no maintainer reply)** vs. "handles 300+ fine, only Antigravity has the 100" **(#26678, 2026-05-07)**. **These directly conflict** | — | GitHub issues | Low |
| Windsurf / Cascade (now docs.devin.ai) | **hard cap** | **100 total tools.** Page **undated**, domain 307-redirects post-Cognition consolidation, may be unmaintained; does not say what happens on overflow | 100 unchanged | docs.devin.ai/desktop/cascade/mcp | Med |
| Claude Desktop | observed | 206-tool server → only 100 tools visible, **silent client-side truncation**. One user's observation; **no Anthropic source documents any cap** | — | modelcontextprotocol discussion #537 (2026-07-20) | Low |
| Kiro | **default setting** | **No tool cap.** Tool Search **disabled by default**; auto-activates at 5% of context or 50,000 tokens | new 2026 entrant | kiro.dev/docs/mcp/tool-search (2026-08-04) | High |
| Amp (Sourcegraph) | observed | No cap; lazy-loads via skills (26 tools = 17k tokens → 4 tools = 1.5k) | — | ampcode.com (2026-01-08) | Med |
| Cline | none documented | No cap; **also no per-tool enable/disable** — whole-server only (open request) | — | cline #8855 (2026-06-01) | Med |
| Zed / Continue | none documented | No cap stated | — | vendor docs, **no visible date, no source-code constant checked** | Low |
| MCP specification itself | none | **No cap on servers or tools anywhere in the spec** | — | modelcontextprotocol #1251 | High |

### 2c. What people actually run (fleet reality)

| Subject | Kind | 2026 value | 2025 value | Source (date) | Conf. |
|---|---|---|---|---|---|
| Official MCP Registry | observed | **26,094** latest-version records (25,800 active); 26,729 including 633 deleted | ~9,652 (2026-05-24) → 18,849 (2026-07-28) | Own full pagination of `/v0.1/servers`, 261 pages, **2026-09-01**; cross-checked via `/v0` and per-server endpoints | High |
| PulseMCP directory | observed | **~21,982** (drifts hourly; crawl titles cluster 22,000–22,070) | ~4–5k mid-2025 | pulsemcp.com/servers, 2026-09-01. ⚠️ **New-listing ingestion has been paused since ~2026-08-05**, so it undercounts | Med |
| **Tools per server — distribution** | observed | **median 10** · mean 19.7 · p75 34 · p90 36 · p95 43 · **p99 114** · **max 622**. Only **3.7% expose >50**, **1.2% expose >100** | — | My own computation over MCP Queen's public probe CSV, 5,099 servers returning >0 tools, probe window 2026-07-12→28 | High |
| GitHub MCP Server | observed | **86 tools / 21 toolsets** stock local; **90 tools / 23 toolsets** with remote extras (⚠️ the claim said 21 toolsets for 90 — wrong). Source registers 120 entries incl. 27 flag-gated variants and 4 insiders-only | 56 (2025); the widely-cited **35** (Feb 2026) is off by 2.5× | Verified against `pkg/github/tools.go` @ main 2026-08-27, release 1.11.0 | High |
| GitHub **Remote** MCP (gh-aw report) | — | ⚠️ **REFUTED — stale by 6 months.** The cited 79 tools / 19 toolsets (2026-03-01) is superseded by the same weekly series: **97 unique tools / 23 toolsets** (2026-08-30). Also: the 79 was a *docs-mapping* count, not live discovery | — | gh-aw #57163; upstream source @ febc3293 | High |
| Playwright MCP | observed | **71** `browser_*` tools — but that is the **full opt-in superset**; **default is 24**. Source defines 80, 9 are `skillOnly` and hidden. ⚠️ The capability names "video"/"verification" don't exist (they're `devtools`/`testing`) | 21–25 (2025) | Verified by running `@playwright/mcp@0.0.80` and diffing `tools/list` against the README, 2026-09-01. Count moved 69→71 on 2026-08-31 | High |
| Atlassian Rovo MCP | observed | ⚠️ **REFUTED.** Not ~183. v2 documents **216 tools** across 12 product areas — but only **21 are "Primary"** and exposed at connection; the rest sit behind `discover` + `execute*`. Full flat list only via `?tools=all`. Compass is gone; five product areas the claim omits exist | ~25 in the 2025 beta | support.atlassian.com supported-tools, `dateModified 2026-09-01`; changelog 2026-07-01 (v2: ">50% context reduction") | High |
| Filesystem reference server | observed | 13 tools | — | repo README @ main, 2026-09-01 | High |
| Slack MCP | observed | 11–13 (**sources disagree, not verified against a primary repo**) | 8 in the archived 2025 server | getunblocked blog | Low |
| Servers per user/org, 2026 | — | **No published 2026 measurement exists.** Report this as a finding | Only figure in circulation: Zuplo "70% run 2–7 servers" — **fieldwork Nov–Dec 2025**, published 13 Jan 2026, and the public blog page doesn't even contain the statistic (it's in the gated report). Clutch's "2 per employee" is Dec 2025 **and an extrapolation onto a hypothetical 10,000-person org** | — | High (that it's unknown) |

### 2d. Degradation literature

| Subject | Kind | Finding | Source (date) | Conf. |
|---|---|---|---|---|
| **Mem2ActBench** — the cleanest control | degradation | **Random** distractors: flat 93.50–95.50% across N=1,2,5. **Hard negatives** (semantically similar): 94.50% → 78.00% → 69.75%. Same N, 25-point gap. **Similarity is the cause; count is a proxy** | arXiv:2601.19935 v1; confirmed verbatim in the **ACL 2026 camera-ready** (2026.acl-long.370, p.8179). ⚠️ Paper never names the backbone — the "Qwen2.5-72B" attribution is inferred. N=1 means *zero* distractors. n=400 tasks | High |
| ToolMATH (Llama3-8B) | degradation | 21.6% → 8.1% with distractors — small models collapse | arXiv:2602.21265 (2026-05-18) | High |
| ToolMATH (Claude-4.6) "no degradation" | — | ⚠️ **REFUTED as stated.** 98.4% → 97.8% is real, but **tool-call rate collapsed 65.6% → 15.2%** in the same cell — accuracy held because the model stopped calling tools and answered from parametric knowledge. Also: the 12,369 tools are the *corpus*; models never see more than gold + k≤50. The quoted pair is one cell at k=5 | arXiv:2602.21265v2 | High (that the framing is wrong) |
| **99% Success Paradox** (Meta) | degradation | Selectivity collapses when **λ = K·R̄q/N ∈ [3,5]**. ⚠️ λ scales with **K (how many you show)**, not menu size: 58 tools with 4 relevant is λ≈4 **only if all 58 are in context**; shortlist K=5 → λ≈0.34. Authors call 3–5 "operational thresholds rather than exact cutoffs" | arXiv:2605.18857 v1 (2026-05-14); **ICLR 2026 Blog Track** — an exposition track, not main track | High on existence / Med on sharpness |
| Same paper, downstream accuracy | degradation | K=10 → K=100: BM25 66%→50%, SPLADE 68%→58%, at ~10× token cost. ⚠️ **One dataset (20 Newsgroups topic classification, not RAG QA), one unnamed LLM, no error bars.** Authors' own limitations section disclaims generalization | same | Low-Med on magnitudes |
| **How Many Tools Should an Agent See?** (Meta) | degradation | Failure is **rank-dependent, not count-dependent**: fixed top-5 exposure scores **0%** when the gold tool ranks 6th–20th. ⚠️ Ranks are within an **N=50 candidate set**, not the 3,251-tool registry; **Claude Sonnet 4.6 was not involved** in the 0% (pure MiniLM retrieval recall). ⚠️ And **fixed-5 wins on both aggregate metrics** (64.7% vs 61.9% coverage; 73.3% vs 71.7% end-to-end) | arXiv:2605.24660 v1/v2 (v2 = bibliography fixes only); independently cited by arXiv:2606.16364 (BIT, unrelated group) | High |
| Canary Tools | degradation | Counter-evidence: adding competing tools made GPT-5.2 **more** discriminating (0.178 → 0.118 wrong-tool rate) | arXiv:2608.04719 (2026-08-05) | Med |
| ToolMenuBench 32.1% vs 85.7% | — | ⚠️ **REFUTED as citable.** Correctly transcribed, but: **0 citations**, not peer reviewed, deterministic **mocked** environment, 30 tasks/cell, no CIs, baseline authored by the filter's own proponents, and the same author's other paper puts the method at 80.1–82.9% on the same backbones. Also mislabeled — averaging across 25/100/250 *destroys* the size-dependence a threshold needs | arXiv:2606.15508v1 (2026-06-13) | Low |
| RAG-MCP | degradation | >90% below pool position ~30; collapse beyond ~100. ⚠️ **This is the stale 2025 number everyone still quotes** — May 2025, `qwen-max-0125`, and the sweep only ran N=1→100 | arXiv:2505.03275 (2025-05-06) | Med, historical |
| Anthropic's own MCP eval | vendor | Deferring definitions: Opus 4 **49% → 74%**; Opus 4.5 **79.5% → 88.1%**. Note the gain **shrank 25pp → 8.6pp** on the newer model | anthropic.com/engineering/advanced-tool-use (2025-11-24). **No 2026 refresh found** | High |
| **Benchmarking the Benchmarks** — the caveat that undercuts everything above | validity | 23 identical LiveMCPBench reruns: **57.9%–76.8%, an 18.9-point spread**; 18.5% evaluator/human misalignment across BFCL v4, τ2-Bench, LiveMCPBench, MCP-Atlas | arXiv:2607.02577 (2026-06-30) | High |
| "200 tools → 41%, 740 tools → 0–20%" | — | ⚠️ **FABRICATED / UNTRACEABLE.** Blogs attribute it to "RAG-MCP (Anthropic, 2025)". RAG-MCP is Gan & Sun, is **not Anthropic**, and **never tested 200 or 740 tools**. No primary source found | webscraft.org and derivatives | Rejected |
| presenc.ai "96%/91%/76% at 1/5/20+ tools" | — | Cites only a leaderboard plus "deployment instrumentation across 60+ enterprise customers" — **no reproducible method** | presenc.ai (2026-05) | Rejected |
| "3 servers = 40 tools = 143,000 tokens" | — | ⚠️ **Arithmetically incoherent**: implies 3,575 tokens/tool vs. the same article's own 500–1,500 range and Anthropic's ~55k for **five** servers | agentmarketcap.ai (2026-04-08) | Rejected |
| "The Bitter Lesson of Tool Calling" | — | **Contains no tool-count analysis at all** — it compares programmatic vs JSON calling. Listed so it isn't double-counted as evidence | arXiv:2608.06370 (2026-08-06) | High |

---

## 3. What changed since 2025

**The industry stopped capping and started deferring.** That is the whole story, and it happened in a nine-month window:

- **2025-11-24** — Anthropic ships tool search with `defer_loading`, and publishes the first vendor **degradation threshold** as a distinct concept (30–50 tools), separate from any cap.
- **2026-01-22** — Cursor 2.4: MCP definitions move to JSON files in `.cursor`; the agent receives **names only**. (The blog was 2026-01-06 and forward-looking; staff confirmed on 2026-01-14 it wasn't in stable yet.)
- **2026-02-05** — Anthropic's tool search leaves beta; no header required. The 10,000-deferred-tool cap becomes a GA documented limit, not a beta relic.
- **2026-03-05** — OpenAI ships `tool_search` / `defer_loading` on the Responses API (gpt-5.4+). **Notably, OpenAI's answer to large tool surfaces was deferral, not a larger array** — indirect evidence the 128 ceiling was never going to move.
- **2026-07-01** — Atlassian Rovo MCP v2: "reduced default tool exposure, reducing context window consumption by >50%." 216 tools become 21 primary + `discover`/`execute*`.
- **2026-08** — Claude Code has tool search **on by default**. Kiro ships Tool Search (off by default, auto-arms at 50k tokens). VS Code's virtual tools have been shipping since v1.103.

Three consequences for anyone reading a 2025 write-up:

1. **Caps now bind on the wire, not on the config file.** VS Code's 128 is a cap on tools *sent* — you can enable far more and it will group them. Cursor's CLI errors where Cursor's IDE doesn't, for exactly this reason: the CLI batches every definition into one request. "How many can I connect" and "how many reach the model" have decoupled.
2. **The one number that went up is Gemini's**: 128 → **512** on Gemini 3. And Google's own Vertex docs still say 128, so Google's documentation contradicts Google's runtime. Everything else either held (OpenAI 128, Azure 128, VS Code 128) or was never there (Anthropic, Bedrock, Mistral, the MCP spec itself).
3. **The number everyone quotes for Cursor — 40 — is a 2025 forum artifact.** It is absent from Cursor's current docs (verified by raw-HTML grep across 8 MCP pages on 2026-09-01), Cursor staff now describe the constraint as an upstream per-model provider limit, and multiple 2026-badged third-party "guides" (truefoundry, nxcode, evomap, Medium) are recycling 2025 text without re-verification.

---

## 4. Real limits vs folklore

### Genuinely hard — the API or client rejects the request

| Limit | Where it bites |
|---|---|
| **OpenAI 128** (`tools`, Chat Completions) | Real, HTTP 400, reproduced through 2026-07. **But undocumented on the live param** — detect it from the error string at runtime rather than hardcoding trust in 128. |
| **Azure OpenAI 128** | The only *properly documented* instance of the number (2026-08-20). |
| **Gemini 3: 512** | Observed only. No Google page states it. |
| **Anthropic 10,000 deferred tools/request** | Documented; rejection behaviour not independently demonstrated. Also a real enforced 400 if *every* tool is deferred. |
| **VS Code 128 sent** | Real error text captured (`Tool limit exceeded (132/128)`), but virtualization usually prevents you seeing it. |
| **Antigravity 100** | Rejects the entire server load. Med confidence, single issue. |
| **Windsurf 100** | Undated page on a redirected domain. Treat as unverified. |
| **OpenAI Structured Outputs** (5,000 props / 10 nesting / 120k chars / 1,000 enums) | Hard **only under `strict: true`**. Without `strict`, Responses silently falls back to best-effort — a hard cap and a silent degradation share the same numbers. |

### Defaults you can change

- Claude Code tool search: **on**, `ENABLE_TOOL_SEARCH=false`.
- Kiro Tool Search: **off**, arms at 5% context / 50k tokens.
- VS Code `virtualTools.threshold`: default 128, **downward-adjustable only**.
- Claude Code `MAX_MCP_OUTPUT_TOKENS`: 25,000.

### Degradation thresholds — nothing errors, quality just falls

- **Anthropic 30–50 available tools.** The only vendor-published one, and the most load-bearing number in this whole document. Caveat: undated page, no published methodology.
- **Mem2ActBench**: the real variable is **distractor similarity**, not count — 94.5%→69.8% with hard negatives, dead flat with random ones at the same N.
- **Meta's λ = K·R̄q/N ∈ [3,5]**: the real variable is **relevant-to-shown ratio**, not N. Their own word: "operational thresholds rather than exact cutoffs."
- **Rank, not count**: fixed top-5 retrieval scores 0% when the gold tool ranks 6th — but note that in the same paper fixed-5 still *wins* on both aggregate metrics.

### Blunt calls on widely-repeated numbers

- **"Cursor caps you at 40."** Stale. 2025 forum lore, absent from current docs, recharacterized by staff.
- **"OpenAI's function-calling API caps at 128."** Half-true in an important way: it's enforced, but it is documented only on the **deprecated** `functions` param and the **sunset** (2026-08-26) Assistants API. The current function-calling guide states no cap — only "fewer than 20… just a soft suggestion."
- **"Anthropic has no cap."** Wrong as usually stated. No cap on the array; a documented **10,000** on deferred tools.
- **"200 tools → 41%, 740 → 0–20%."** No primary source exists. Misattributed to a paper that never tested those N.
- **"GitHub MCP has 35 tools."** Off by 2.5× — it's 86/90, and it's the most-cited per-server count in 2026 write-ups.
- **"Atlassian Rovo ~183."** It's 216 documented / 21 exposed by default. Both halves of that sentence matter.
- **"Playwright MCP has 71 tools."** True only with all seven `--caps` values passed. **Default is 24.**
- **ToolMenuBench's 32.1% vs 85.7%.** Do not cite. Zero citations, mocked environment, baseline written by the method's proponents.
- **Every effect below ~19 points measured on LiveMCPBench-class harnesses in a single run.** The rerun noise floor is 18.9 points. That is larger than most reported tool-count effects.

---

## 5. Implications for a token break-even argument

The argument under test: *"Above ~40–128 tools a user physically cannot connect everything, so a proxy's value is capability (access at all), not token saving (against an all-loaded baseline that would never have existed)."*

**The capability half of that argument weakens substantially; the token-saving half weakens too, for a different reason. Net: the differentiator moves from 'how many' to 'which client' and 'how well ranked'.**

**Why capability weakens.** The premise is that the cap prevents connection. In 2026 it mostly doesn't:

- The **MCP spec caps nothing**. Anthropic's array caps nothing. Bedrock caps nothing. Claude Code, Kiro, Cline, Zed, Continue document no cap.
- Where caps exist, deferring clients keep you under them without pruning: VS Code virtualizes above 128, Cursor's IDE sends names only, Claude Code's tool search is on by default.
- Gemini went 128 → 512.
- So for a user on Claude Code, Cursor IDE, Kiro or Amp, "I cannot connect these" is **false in 2026**. The counterfactual baseline *is* "everything connected."

**Why token saving also weakens.** If the counterfactual is "everything connected and fully loaded," the proxy's token saving is real and large. But the deferring clients **already capture that saving natively** — Cursor reports 46.9% agent-token reduction, Atlassian v2 reports >50% context reduction, Anthropic reports 49%→74% / 79.5%→88.1% accuracy from deferral. A proxy's *marginal* token saving over a client that already defers is small. The 55k-token all-loaded baseline is the right counterfactual only on **non-deferring** surfaces.

**Where the argument still bites, and at what N.** Four distinct thresholds, in the order a growing fleet hits them:

| N (tools exposed at once) | What binds | Whose problem |
|---|---|---|
| **~10 / >10k tokens** | Anthropic's and OpenAI's own trigger for enabling deferral | Everyone. This is the earliest real signal. |
| **~20** | OpenAI's soft "fewer than 20 at the start of a turn"; Gemini's "10–20 active maximum" | Selection quality, both major providers agree |
| **~30–50** | Anthropic's documented degradation threshold; ~18–30k tokens of definitions at ~590 tok/tool (inferred from 55k/5-servers) | **This is where the argument actually bites in 2026** — quality, not connectivity |
| **~100–128** | Antigravity (hard reject), Windsurf (100), VS Code (128 sent), OpenAI/Azure (128), Cursor CLI (model-dependent), Claude Desktop (silent truncation to 100) | **Only on non-deferring clients and direct API use** |
| **512 / 10,000 / none** | Gemini 3 / Anthropic deferred / Anthropic array, Bedrock, spec | Effectively unreachable |

**The distribution decides who is in which row, and the answer is counterintuitive.** Median server = **10 tools**; p75 = 34. A user with the folkloric "2–7 servers" is at **20–70 tools** — squarely in the 30–50 degradation band and **nowhere near any cap**. But three badly-chosen first-party servers put you at 373 (GitHub 86 + Atlassian 216 + Playwright 71). Only **3.7%** of servers expose >50 tools and **1.2%** expose >100. So:

> **Fleet size is the wrong variable. *Which* servers is the right one.** The cap argument bites for a small minority running heavy first-party enterprise servers on non-deferring clients. The degradation argument bites for nearly everyone, at a much lower N, and it bites regardless of what any provider caps.

Anthropic effectively concedes the aggregation point: their published trigger for recommending tool search is literally *"you aggregate multiple MCP servers (200+ tools)"* — crossing 200 is treated by the vendor as the **normal** consequence of connecting several servers.

**Honest framing for a break-even claim in 2026:**
1. Against a **non-deferring** client (Cline, Zed, Continue, Claude Desktop, Cursor CLI, direct API), the all-loaded baseline is real and token savings are the defensible claim.
2. Against a **deferring** client, the token baseline is already reduced; the defensible claim is **retrieval quality above 30–50** — i.e. does your ranking put the gold tool in the top-K, given that Meta's result shows fixed top-5 scores 0% when the gold ranks 6th, and Mem2ActBench shows the failure mode is semantic near-duplicates across servers (three `search` tools, four `create_issue` variants) that a single-server client never sees but an aggregator manufactures.
3. **Do not claim "you couldn't connect these otherwise"** unless you name the client. On Claude Code in 2026, you could.

---

## 6. Open questions

Things I could not establish, stated plainly:

1. **No 2026 measurement of how many MCP servers a real user or org connects exists.** I checked registry telemetry, vendor docs, client changelogs, ecosystem surveys and MCP Dev Summit NA 2026 coverage. Nothing. The only distributional figure in circulation is Zuplo's "70% run 2–7 servers," which is **Nov–Dec 2025 fieldwork** wearing a Jan 2026 publication date, and is cited second-hand — the public blog page does not contain the statistic. **Registry counts are adoption-intent, not usage**: 1,603 of 9,326 probed remote servers were unreachable and only 5,241 answered `tools/list`.
2. **Is the 128 cap live on OpenAI's *direct* `/v1/responses` endpoint?** Every 2026 error report I could isolate reached OpenAI through a proxy (GitHub Copilot, Azure, LiteLLM, Cursor). LiteLLM passes OpenAI's error text verbatim, so this is strong circumstantial evidence — but I found no bare direct-endpoint report, and the spec puts no `maxItems` on `ToolsArray`.
3. **Where does Anthropic's 30–50 come from?** The single most load-bearing number here sits on an undated page with no published methodology, sample, or model list. It is a vendor assertion, not a measurement anyone can check.
4. **Gemini's 512 rests entirely on observed API behaviour.** No Google page states it, and Google's own Vertex docs still say 128. If the 512 is model- or preview-specific, nothing I found would reveal that.
5. **Does per-model variation exist behind the 128?** microsoft/vscode #294055 raises it explicitly (Claude vs GPT) and it is unanswered by maintainers. I did not read the VS Code source constant.
6. **Gemini CLI's ~100:** two GitHub issues flatly contradict each other (#21823 says silent drops at ~100; #26678 says 300+ works and only Antigravity has the 100). Neither has a maintainer reply. Unresolved.
7. **Windsurf's 100** sits on an undated page on a domain that now 307-redirects to docs.devin.ai post-Cognition consolidation; the page may be carried over unmaintained, and does not state overflow behaviour.
8. **Zed and Continue's "no cap"** comes from undated docs pages. I did not check source-code constants. Low confidence that no cap exists in code.
9. **Claude Desktop's 100** is one user's observation plus another user's plausible explanation. An MCP maintainer confirmed only that any such limit "exists in Claude's client, not in MCP." No Anthropic source documents it.
10. **Does Cursor's CLI still batch all definitions?** That behaviour (staff-described, 2026-07) implies the CLI had not adopted the Jan 2026 harness change six months later. Plausible as an IDE/CLI divergence, corroborated by no Cursor document.
11. **The measurement floor swallows most of the literature.** 18.9-point rerun spread and 18.5% evaluator/human misalignment on the standard harnesses. Any single-run degradation under ~19 points on BFCL v4 / τ2-Bench / LiveMCPBench / MCP-Atlas should be treated as unresolved, which includes several results in §2d.
12. **No 2026 refresh of Anthropic's 49%→74% / 79.5%→88.1% deferral eval.** The shrinkage from 25pp to 8.6pp between model generations is the clearest evidence that the threshold moves as models improve — but there is no third data point.
13. **Every count in §2c has a short shelf life.** The registry is roughly doubling per quarter (my own 26,094 is +38% over MCP Queen's five-week-old 18,849). Playwright's count moved 69→71 on 2026-08-31. GitHub MCP shipped 7 releases in 10 weeks. Re-derive; do not cache.

**Provenance note.** Registry counts, the tools-per-server distribution, and the GitHub/Playwright/filesystem tool counts are first-party measurements taken 2026-09-01, not citations. One fetched AWS documentation page carried an appended block instructing the reader to run `aws agent-toolkit search-skills` and install skills; that is instruction-like text embedded in fetched content, was treated as data, and was not acted on — flagged because it appears to be injected into docs.aws.amazon.com pages generally.

---

## C. Completeness critic

**Verdict: not thorough. The verification discipline is unusually good, but the coverage has three systematic holes — the protocol layer, the gateway/aggregator layer, and caching economics — and two of them invert conclusions the document states confidently.** Prioritised:

---

**P0-1. `tools/list` pagination non-compliance is absent, and it falsifies §5's headline.**
Claude Code (anthropics/claude-code#24785, #39586), Codex (openai/codex#28858), Cursor (forum 165213, 168394), and Kiro (kirodotdev/Kiro#5972) register **only the first page** of `tools/list` and silently discard `nextCursor`; LibreChat had to patch it (PR #13840). AWS AgentCore Gateway paginates at 30 tools/page, so Claude Code sees 30 of 48 through it. This is a hard, silent, *undocumented* "cannot connect more than N" that bites **aggregators specifically** — the report's own subject class. It directly contradicts "for a user on Claude Code, Cursor IDE, Kiro or Amp, 'I cannot connect these' is **false** in 2026" and §5's framing point 3. §2b also says the spec caps nothing — true, but the spec's pagination is exactly where clients break. Needs its own row-set and a §5 paragraph.

**P0-2. The gateway/aggregator tier is missing entirely — the comparison class for the report's own conclusion.**
No coverage of AWS **Bedrock AgentCore Gateway** (documented quota: 100 targets/gateway, adjustable; ships `x_amz_bedrock_agentcore_search` semantic tool search; paginates at 30), **Docker MCP Gateway** (aggregates N servers behind ~5 tools; `dynamic-tools` feature, `docker mcp feature disable dynamic-tools`), MetaMCP, ToolHive/Stacklok, Composio, Klavis, Pipedream, 1MCP. §5 asks what a proxy's marginal value is without enumerating that AWS and Docker have already shipped the exact retrieval-proxy design §5 concludes is the defensible claim. There is no competitive baseline in the document.

**P0-3. Prompt caching is absent — the biggest hole in the token half of §5.**
Anthropic's cache prefix is explicitly *tools → system → messages*, making tool definitions the canonical cacheable block; reads are 0.1× input, writes 1.25×/2×. OpenAI and Gemini cache automatically. Two consequences the document never confronts: (a) a stable 55k all-loaded tool block costs ~10% of that per turn in steady state, weakening the all-loaded baseline much further than deferral does; (b) **a proxy that varies the exposed tool list per turn invalidates the cache prefix every turn** and can be *more* expensive than a static all-loaded surface. That is precisely the break-even argument under test.

**P0-4. §5's "which servers, not how many" rests on one unweighted registry probe, marked High.**
MCP Queen's CSV is single-source, reachability-selected (the doc's own note: 1,603/9,326 unreachable; 5,241 answered `tools/list`), and **unweighted by install base**. The distribution that matters is popularity-weighted, and the popular servers are the tool-heavy ones the doc itself lists. "Only 3.7% expose >50" over the registry is fully compatible with ">50% of *installed* servers expose >50". Untried weighting proxies: registry install counts, npm/PyPI downloads, PulseMCP popularity rank, IDE marketplace installs. Downgrade to Med and state the caveat where the conclusion is drawn.

**P0-5. Caps on the number of *servers/connectors* — a category never opened.**
Only tool-count caps are tabulated. But: Claude free tier = 1 custom connector; AgentCore = 100 targets/gateway; ChatGPT connector limits; per-workspace server limits in IDEs. "Can I connect more than 40–128 tools" has a sibling question with different, real answers.

---

**P1-6. Missing clients/vendors, several first-tier.**
- **OpenAI Codex CLI** — OpenAI's own agent, wholly absent from §2b.
- **Goose (Block)** — docs recommend **≤25 tools** and ship Tool Router (preview, vector selection). That is a **second vendor-published threshold, lower than Anthropic's 30–50**, which breaks §4's "the only vendor-published one" and makes the load-bearing number less lonely.
- LibreChat, Open WebUI, Dify, n8n, JetBrains AI/Junie, Warp, Roo/Kilo Code, Copilot coding agent, Copilot CLI.
- **Framework layer** (LangChain/LangGraph, LlamaIndex, OpenAI Agents SDK, Pydantic AI, Vercel AI SDK, Mastra, AWS Strands) — where many fleets actually run; report absence as a finding, as was done for Mistral.
- **Local/OSS inference** (Ollama, llama.cpp, vLLM) — ToolMATH has Llama3-8B collapsing 21.6%→8.1%, yet every threshold in §5 is calibrated to frontier models only.

**P1-7. Claude Desktop's truncation number is contested and probably wrong.** Separately reported: a **256-tool** collapse across all connectors (keeps the alphabetically-first 256, truncates the namespace straddling the boundary) — not the 100 in §2b. Two numbers, neither from Anthropic, on a headline surface.

**P1-8. Search modalities not tried, in order of payoff.**
- **Source constants** — the doc admits skipping VS Code's; also unread: gemini-cli's tool-count check (would *settle* open question #6, where two issues contradict), Antigravity, Cursor's bundled JS, `openai-python`/`openai-node` (would settle open question #2 — client-side vs server-side 128), MCP SDK pagination defaults.
- **Vendor cookbooks / eval repos** (openai-cookbook, anthropic-cookbook) rather than docs pages — where undocumented thresholds appear with methodology, relevant to open question #3.
- **Rate-limit/pricing pages** — 55k of tool definitions per request against a TPM tier is an unpriced throughput cap; a low-tier org can be rate-limited by tool definitions alone.
- **Stacklok "State of MCP in Software 2026"** and **MCP Queen's own July-2026 state report** — open question #1 asserts exhaustive search, but at least the Stacklok production survey is unchecked.
- **The requester's own telemetry.** For a break-even argument, mcpproxy telemetry is a first-party answer to servers-and-tools-per-real-user — the distribution nobody else has. Declaring "no 2026 measurement exists" while owning one is the largest missed source.

**P1-9. §2d has no retrieval-method evidence and no cost/latency axis.** Missing e.g. arXiv:2603.20313 (*Semantic Tool Discovery for LLMs: A Vector-Based Approach to MCP Tool Selection*) — a paper on exactly the design §5 endorses. There is nothing on BM25 vs embedding vs LLM-reranker, which is the actual engineering question for a proxy. And deferral is booked as pure saving throughout: its recurring per-turn cost (a search call + results every turn, extra round-trip latency, cache invalidation) is never netted out.

---

**P2-10. Stated more confidently than the evidence supports (beyond what's already flagged).**
- Cursor's **46.9%** and Atlassian's **>50%** are uncontrolled vendor self-reports, yet they carry §5's conclusion that a proxy's marginal saving is small. Label as vendor-reported.
- §1's "and for the median user it never was" leans on the 2–7-servers figure that §2c and §6 both disqualify. Drop the clause or rest it on the distribution alone (with P0-4's caveat).
- "**Claude Code tool search ON by default**" is the linchpin of "on Claude Code, you could" — one docs page, and unreconciled with the pagination bug.
- Percentiles to 3 s.f. (p99 114, max 622) from a single 16-day probe window.
- "Anthropic caps `skills` at 20, `messages` at 100,000 — meaningful silence" is an inference presented as evidence.
- **Missing structural limit: tool *names*.** OpenAI/Anthropic enforce `^[a-zA-Z0-9_-]{1,64}$`. An aggregator prefixing `server__tool` hits 64 chars and must truncate — a silent, aggregator-specific failure mcpproxy's own naming scheme is exposed to. Not mentioned anywhere.
- §2c's per-server table omits other commonly-cited heavy servers (AWS MCP suite, Notion, Linear, Sentry, Stripe, Azure/Microsoft Learn, Supabase); the "373 tools from three servers" example would be far stronger as a verified top-20-by-installs table — which also supplies P0-4's weighting.
