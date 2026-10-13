# Feature Specification: Discovery Effectiveness Profiler (mcp-discovery-bench)

**Feature Branch**: `083-discovery-profiler`
**Created**: 2026-07-14
**Status**: Draft
**Input**: User description: "Discovery Effectiveness Profiler (mcp-discovery-bench): extend the existing bench/ harness into a stable, reproducible environment that measures MCP tool-discovery effectiveness across three axes — tokens (menu cost + live retrieve_tools RESPONSE cost, p50/p95/max, break-even analysis vs naive menu), retrieval quality (Recall@k/MRR/nDCG on the existing 47-query golden set PLUS public corpora ToolRet-Tools/ToolRet-Queries from HuggingFace and LiveMCPTool), and latency (client-side percentiles, existing). Switchable encoding arms: full JSON schemas (baseline), compact signatures, TSCG-compiled schemas, TOON for listings and tabular results separately, optional TRON-style dedup. LAP integrated as independent external measurement in CI. Corpus loaders for ToolRet parquet and LiveMCPTool JSON. Multi-turn end-to-end token estimator. Extended dashboard.html + report.json for public report."

## Context & Motivation

MCPProxy's core value proposition — massive token savings through on-demand tool discovery — is partially eaten back by the discovery phase itself. Live measurements on a 907-tool deployment (July 2026) show:

- The proxy menu costs 4,073 tokens vs 499,737 naive (−99.2%) — the menu side works.
- But each `retrieve_tools` call returns 15 full JSON schemas: median 8,640 tokens, worst case 54,865. **77% of that cost is raw `inputSchema` JSON.** The proxy stops paying for itself after ~38 retrieval calls per session.
- Community signals (GitHub discussions; users aolin480, armorer-labs; issue #175 re: Anthropic Tool Search Tool) converge on the same ask: a small, stable, predictable router surface with cheap discovery responses.

Independent research confirms both the problem and the direction: compressed/compact tool schemas *improve* selection accuracy while cutting tokens (TSCG paper, Anthropic Tool Search Tool internal evals: Opus 4 49%→74%); retrieval errors cause ~half of agent task failures at scale (LiveMCPBench); and single-call token counts understate real agent-loop cost due to multi-turn parsing-retry cascades (arXiv:2605.29676).

**Before changing production discovery behavior, we need a stable environment that measures discovery effectiveness.** This spec builds that profiler. A follow-up spec will use its results to add switchable discovery methods to the production proxy.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Measure live retrieve_tools response cost (Priority: P1)

A maintainer runs the benchmark against a live mcpproxy instance and gets, for every golden-set query, the actual token cost of the `retrieve_tools` response — distribution statistics (p50/p95/max), a breakdown by component (schemas vs descriptions vs instructions vs metadata), and a break-even analysis showing after how many retrieval calls the proxy stops saving tokens versus the naive full menu.

**Why this priority**: This is the main measurement gap today. The existing harness measures the menu (one-time cost) but not the response cost paid on *every* discovery call — which live profiling showed is the dominant leak. Every optimization decision downstream depends on this number existing and being tracked.

**Independent Test**: Run the live benchmark against a running proxy with the frozen 7-server corpus; verify the report contains per-query response token counts, percentile stats, a component breakdown, and a break-even call count.

**Acceptance Scenarios**:

1. **Given** a running proxy with the frozen snapshot corpus, **When** the live benchmark runs with the golden query set, **Then** the report includes, per query, the discovery-response token count and, in aggregate, p50/p95/max and mean.
2. **Given** the measured naive full-menu cost, proxy-menu cost, and mean discovery-response cost, **When** the report is generated, **Then** it includes the break-even call count computed as `(naive_full_menu_tokens − proxy_menu_tokens) ÷ mean_discovery_response_tokens`, with all three inputs shown.
3. **Given** a discovery response, **When** its tokens are counted, **Then** the count is decomposed into named components (input schemas, descriptions, usage instructions, name/server/score metadata, and a catch-all "other") whose sum equals the total response token count.

---

### User Story 2 - Compare encoding arms on the same corpus (Priority: P1)

A maintainer runs the benchmark with an encoding-arm flag and sees, side by side for the *same* tools and the *same* queries, what each candidate re-encoding of tool definitions would cost in tokens: (1) full JSON schemas — today's baseline, (2) compact signatures `name(param:type)|description`, (3) TSCG-compiled schemas, (4) TOON-encoded listings, and (5, optional) TRON-style schema-deduplicated listings. Retrieval quality metrics are reported alongside so any arm that would change what the agent sees is honestly paired with its recall impact.

**Why this priority**: This is the experiment engine — the reason to build the profiler. It converts the "which discovery format should mcpproxy adopt?" debate into measured numbers on our own corpus, on public corpora, and against published claims (TSCG ≥51% guarantee; TOON's predicted loss on nested schemas; compact signatures' measured −92%).

**Independent Test**: Run the benchmark in offline mode with all encoding arms enabled on the schema-bearing frozen corpus; verify the report contains one row per arm with token totals, per-tool mean, and savings % vs baseline, and that search-affecting arms carry recall metrics.

**Acceptance Scenarios**:

1. **Given** the schema-bearing frozen corpus, **When** the benchmark runs with all arms enabled, **Then** the report contains per-arm total tokens, per-tool mean, and savings vs the full-JSON baseline.
2. **Given** the compact-signature arm, **When** it renders a tool, **Then** required and optional parameters are distinguishable and the tool description is preserved (encodings that silently drop descriptions must be labeled as lower-bound estimates).
3. **Given** the TOON arm, **When** the report is generated, **Then** tool-listing encoding and tabular-result encoding are measured and reported as two separate numbers (research predicts opposite outcomes for these two payload shapes).
4. **Given** any arm whose output the retrieval index would ingest differently, **When** the benchmark runs, **Then** recall@k/MRR/nDCG for that arm are computed on the golden set and reported next to its token numbers.
5. **Given** an arm that cannot process a given tool (e.g., a malformed schema), **When** the benchmark runs, **Then** the tool is counted and reported as skipped for that arm rather than silently dropped.

---

### User Story 3 - Evaluate against public corpora (Priority: P2)

A maintainer loads public tool datasets into the benchmark: ToolRet (43k tools / 7.6k relevance-labeled queries, ACL 2025) provides retrieval-quality evaluation — recall@k/MRR/nDCG for mcpproxy's search over a corpus far larger and independently constructed than the in-house 47-query golden set; LiveMCPTool (70 servers / 527 tools, Apache 2.0) provides an MCP-native corpus for token/scale measurements of the encoding arms, with retrieval scoring only if relevance labels can be derived from its task annotations.

**Why this priority**: The in-house golden set is small (47 queries, 45 tools) and self-labeled. Public corpora give scale (external validity), let retrieval results be methodologically compared against published retriever baselines (ToolRet best: 33.83 nDCG@10), and make the eventual public report credible.

**Independent Test**: Run the corpus loader on a downloaded ToolRet snapshot; verify tools and queries map into the benchmark's corpus/golden-set types, the search index builds, and metrics are produced for a configurable query subset.

**Acceptance Scenarios**:

1. **Given** a local copy of the ToolRet corpus and query set, **When** the loader runs, **Then** tools and relevance-labeled queries are converted into the benchmark's native corpus and golden-set formats with per-record validation errors reported.
2. **Given** a loaded relevance-labeled public corpus (ToolRet; LiveMCPTool only when labels were derived per FR-011), **When** the retrieval benchmark runs, **Then** recall@1/3/5/10, MRR, and nDCG@10 are computed the same way as for the in-house golden set, and the corpus name/version is stamped in the report.
3. **Given** the ToolRet dataset's unstated license, **When** the repository is inspected, **Then** no ToolRet data files are committed — the loader fetches at runtime from the canonical source with a documented license note; the Apache-2.0 LiveMCPTool corpus MAY be committed as a frozen snapshot with attribution.
4. **Given** the full 7.6k ToolRet query set would be slow, **When** the benchmark runs in CI, **Then** a deterministic, seeded subset of configurable size is used and the subset definition is recorded in the report.

---

### User Story 4 - Independent measurement via LAP in CI (Priority: P2)

The CI benchmark job runs LAP (`lap-score`, an independent MIT-licensed token-efficiency linter for agent-facing APIs) against the booted proxy and archives its JSON verdict alongside the benchmark's own report, so mcpproxy's self-reported numbers are always accompanied by an external instrument's reading of the same surface.

**Why this priority**: Independent verification was an explicit design goal ("better to use LAP as an independent tool"). LAP measures the menu surface (bucket A) with its own methodology and grading; agreement between LAP and the in-house count is itself a regression signal. LAP does not cover response tokens, latency, or recall — the profiler covers those — so the two are complementary, and no fork is needed.

**Independent Test**: Run the CI job locally; verify a LAP JSON report is produced for the live proxy endpoint, archived as an artifact, and that a menu-token divergence between LAP and the in-house counter beyond a tolerance is surfaced as a warning (non-blocking).

**Acceptance Scenarios**:

1. **Given** a booted proxy in the benchmark CI job, **When** the LAP step runs, **Then** its machine-readable output is archived as a build artifact next to the benchmark report.
2. **Given** both LAP's menu-token count and the in-house count for the same proxy surface, **When** reports are generated, **Then** the two numbers appear side by side with their divergence; divergence beyond a documented tolerance emits a warning without failing the job.
3. **Given** LAP is unavailable (network, install failure), **When** the CI job runs, **Then** the benchmark still completes and the LAP step is marked skipped, keeping the overall job non-blocking.

---

### User Story 5 - Public-ready report output (Priority: P3)

Anyone can read a self-contained dashboard (and a machine-readable report file) that presents the three axes — tokens, quality, latency — per encoding arm and per corpus, with every number labeled by provenance (measured live / computed offline / estimated) and with the multi-turn cost estimator explained, suitable for publication without edits.

**Why this priority**: The public report is the promotion deliverable, but it is a rendering of data produced by stories 1–4; it can ship last.

**Independent Test**: Generate the dashboard from a benchmark run; verify it is a single self-contained file showing per-arm/per-corpus results with provenance labels and the estimator's assumptions stated.

**Acceptance Scenarios**:

1. **Given** a completed benchmark run, **When** the dashboard is generated, **Then** it renders tokens/quality/latency per arm and per corpus in one self-contained file with no external requests.
2. **Given** any headline number in the dashboard, **When** a reader inspects it, **Then** its provenance (measured / computed / estimated) and tokenizer basis are visible.
3. **Given** the multi-turn estimator, **When** it projects session-level cost from single-call measurements, **Then** its assumptions (calls per session, retry rate) are explicit inputs shown in the output, defaulting to documented literature-derived values.

---

### Edge Cases

- **Giant single tools**: individual tool definitions up to ~7,800 tokens exist in real corpora; percentile stats must not be masked by means, and the report must list the top-N heaviest tools per arm.
- **Tools with no description or stub descriptions** (e.g., "Proxy for `x.y.z`"): encoding arms must handle empty/degenerate descriptions without crashing, and such tools should be flaggable since they are a known retrieval-failure class.
- **Schemas that fail an arm's compiler** (invalid JSON Schema, exotic keywords, deep nesting): count, skip, and report — never abort the whole run or silently omit.
- **Corpus drift**: a live proxy may expose more/fewer tools than the frozen corpus (e.g., pending-approval tools invisible to the index); the report must state the tool count actually measured and warn when live count ≠ expected count. (Known instance: the frozen corpus has 45 tools while the existing live CI poll expects 44 — one tool differs at runtime; the profiler must surface, not hide, such gaps.)
- **Tokenizer divergence**: all absolute numbers use tiktoken cl100k_base by default, which underestimates Claude-tokenizer counts by roughly 60%; relative savings are stable across tokenizers. This caveat must appear wherever absolute numbers are shown.
- **Public dataset unavailability**: canonical dataset sources may be down or change format; loaders must fail with actionable errors and the rest of the benchmark must proceed.
- **Non-uniform result payloads for the TOON-results arm**: TOON's tabular advantage only applies to uniform arrays; the arm must classify sampled results as tabular vs non-tabular and report the split rather than assume uniformity.

## Requirements *(mandatory)*

### Functional Requirements

**Response-cost measurement (US1)**

- **FR-001**: The benchmark MUST measure discovery-response token cost by invoking the actual discovery tool over the MCP protocol (the same response an agent receives — not an internal search endpoint) for every golden-set query, reporting per-query values plus aggregate p50/p95/max and mean.
- **FR-002**: The benchmark MUST decompose discovery-response token cost into named components — at minimum: input schemas, descriptions, usage instructions, name/server/score metadata — plus a catch-all "other" bucket for unclassified fields, and the component sum MUST equal the total response token count (verified per response).
- **FR-003**: The benchmark MUST compute a break-even analysis as `break_even_calls = (naive_full_menu_tokens − proxy_menu_tokens) ÷ mean_discovery_response_tokens`, showing all three inputs in the report; a non-positive numerator (proxy menu ≥ naive menu) MUST be reported as "no break-even: proxy menu costs more than naive".
- **FR-004**: The benchmark MUST report the naive full-menu token cost (all tools, full definitions) and the proxy-menu token cost measured on the same live instance, using full definitions on both sides (parity with the existing authoritative-headline guard).

**Encoding arms (US2)**

- **FR-005**: The benchmark MUST support named encoding arms that re-encode the same tool corpus and report, per arm: total tokens, per-tool mean and p95, and savings % vs the full-JSON baseline arm. Arm comparisons run on a **schema-bearing frozen corpus** (a committed snapshot of the reference servers' tools including full parameter schemas — the existing schema-less frozen corpus is insufficient for arm comparison and remains for back-compat).
- **FR-006**: The following arms MUST be included and MUST all execute in the CI benchmark job (whose environment provides their runtimes): full JSON schema (baseline), compact signature (parameters with types, required/optional distinction, description preserved), TSCG-compiled, TOON tool-listing. In local runs an arm whose runtime is unavailable is reported as skipped-with-reason. A TRON-style schema-deduplication arm SHOULD be included if implementable with reasonable effort; otherwise it MUST be listed in the report as not-yet-measured.
- **FR-007**: The TOON arm MUST measure two payload classes separately: (a) tool listings (discovery responses) and (b) tool results, where (b) uses a committed, deterministic fixture set of representative tool-call outputs (own reference-server outputs; no license-encumbered data), reporting tabular vs non-tabular classification for (b).
- **FR-008**: Each arm MUST declare whether it alters the text the retrieval index ingests (name/description/parameter text). For index-altering arms the benchmark MUST rebuild an offline index from the arm's encoded text and compute retrieval-quality metrics on the golden set, presenting them adjacent to the arm's token numbers; rendering-only arms MUST be labeled quality-neutral without re-running retrieval.
- **FR-009**: Each arm MUST handle per-tool failures by skipping and counting them; the per-arm skip count and example failures MUST appear in the report.
- **FR-010**: Arm implementations MUST be deterministic: identical corpus in, identical encoded bytes out, so token numbers are reproducible across runs and machines.

**Public corpora (US3)**

- **FR-011**: The benchmark MUST provide loaders that convert (a) the ToolRet tool corpus + relevance-labeled query set into the benchmark's native corpus and golden-set formats (full retrieval-quality evaluation), and (b) the LiveMCPTool corpus into the native corpus format for token/scale measurements — LiveMCPTool retrieval-quality evaluation is included only if relevance labels can be derived from the accompanying task annotations (SHOULD, not MUST). Both loaders report per-record validation errors.
- **FR-012**: Retrieval-quality metrics on public corpora MUST be computed with the same metric implementations used for the in-house golden set (nDCG with linear gain on graded relevance, log2 discount — stated in the report), every report row MUST carry the corpus name and version/snapshot identifier, and comparisons against externally published baseline numbers MUST be labeled as methodological (not apples-to-apples) unless the metric formulas are verified identical.
- **FR-013**: ToolRet data MUST NOT be committed to the repository while its license is unstated; the loader fetches it at runtime from the canonical source and records the snapshot identity. LiveMCPTool (Apache 2.0) MAY be committed as a frozen, attributed snapshot.
- **FR-014**: For large query sets, the benchmark MUST support a deterministic, seeded query subset of configurable size, recording the seed and size in the report.

**Independent measurement (US4)**

- **FR-015**: The benchmark CI job MUST run the external LAP linter (version-pinned so CI installs deterministically) against the booted proxy endpoint, archiving its machine-readable output as a build artifact; LAP runtime failure MUST NOT fail the job (skipped-with-reason).
- **FR-016**: The report MUST show LAP's menu-token measurement next to the in-house menu measurement with their divergence; divergence beyond a documented tolerance MUST produce a visible warning.

**Reporting (US5)**

- **FR-017**: The benchmark MUST emit a machine-readable report containing all measurements, arm definitions, corpus identities, tokenizer identity, subset seeds, and provenance labels for every number (measured / computed / estimated).
- **FR-018**: The benchmark MUST render a single-file, self-contained dashboard presenting the three axes per arm and per corpus, with provenance and tokenizer caveats visible, suitable for publishing as-is.
- **FR-019**: The report MUST include a session-level cost estimator that projects end-to-end multi-turn token cost from single-call measurements using explicit, documented assumptions (discovery calls per session, retry/parsing-failure rate per encoding), and MUST label its outputs as estimates.
- **FR-020**: The report MUST list the top-N heaviest tools per arm (default N=10, configurable) and the count of description-degenerate tools, defined as: description empty/whitespace-only, shorter than 20 characters, or matching a configurable stub pattern list (default includes `^Proxy for `), since these drive tail costs and known retrieval failures.

**Reproducibility & CI**

- **FR-021**: Offline benchmark runs MUST be fully deterministic and network-free given a local corpus; live runs MUST record proxy version, tool count, and configuration relevant to discovery (result limit, routing mode).
- **FR-022**: The benchmark CI job MUST remain non-blocking for the release pipeline, and its runtime with default settings MUST stay within the existing CI budget (see SC-007).
- **FR-023**: The benchmark MUST collect and emit client-side latency percentiles (p50/p95/p99/max) for live discovery calls, per corpus and per measured surface, preserving the existing harness's latency reporting in the new report format.

### Key Entities

- **Encoding Arm**: a named, deterministic transformation from a tool definition (name, description, parameter schema) to the text an agent would receive; carries token measurements, optional quality metrics, skip counts, and a quality-neutral flag.
- **Corpus**: a versioned set of tool definitions (in-house frozen snapshot, live proxy export, ToolRet, LiveMCPTool) with provenance and license notes.
- **Golden Query Set**: relevance-labeled queries bound to a specific corpus version (existing 47-query set; converted public query sets), with graded relevance labels.
- **Discovery Response Measurement**: per-query record of a live discovery call — response tokens, component breakdown, result count, latency.
- **Break-even Analysis**: derived metric relating one-time menu savings to per-call discovery overhead for a given arm.
- **Session Cost Estimate**: projection of end-to-end session token cost per arm from measured single-call costs plus documented behavioral assumptions.
- **Independent Verdict**: the external linter's (LAP's) measurement and grade for the same proxy surface, stored alongside in-house numbers.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After one live benchmark run, a maintainer can answer — with numbers from the report, without ad-hoc scripting — (a) the median and p95 token cost of a discovery call, (b) which response component dominates that cost, and (c) after how many discovery calls the proxy stops saving tokens vs the naive menu.
- **SC-002**: All mandatory encoding arms that executed in the run produce token measurements on the schema-bearing frozen corpus, each with savings % vs baseline; in CI, all mandatory arms MUST execute (a skipped mandatory arm fails this criterion). As a non-gating sanity check, the compact-signature arm's savings vs the full-JSON rendering of the same tools is compared against the independently pre-measured ~92% (measured on live discovery responses of a 907-tool deployment); divergence beyond ±10 percentage points is explained in the report (corpus composition differences are an acceptable explanation).
- **SC-003**: Retrieval quality on the in-house golden set matches the recorded baseline (recall@5 = 0.68 ± 0.05) when run through the new arm-aware pipeline with the baseline arm — proving the profiler didn't change what it measures.
- **SC-004**: Both public corpora run end-to-end for their supported measurements: ToolRet through load → index → retrieve → score (producing recall/nDCG with the metric formula stated), and LiveMCPTool through load → encoding-arm token measurement (plus retrieval scoring if relevance labels were derived per FR-011).
- **SC-005**: Every headline number in the published dashboard carries a visible provenance label and tokenizer note; zero unlabeled numbers.
- **SC-006**: In runs where LAP executed, its verdict and the in-house menu count for the same surface agree within the documented tolerance, or the run shows a warning; across two consecutive releases, each run either archives a LAP artifact or records an explicit skip reason (a silent absence fails this criterion).
- **SC-007**: The default CI benchmark job (offline arms + golden set + LAP + report) completes in under 15 minutes; the ToolRet subset run completes in under 30 minutes.
- **SC-008**: A reader outside the project can reproduce the offline numbers from a fresh checkout with one documented command and no credentials.

## Assumptions

- The existing bench harness's metric definitions (recall@k, MRR, nDCG@10, MAP) and tokenizer default (tiktoken cl100k_base) are kept; absolute token numbers are estimates relative to Claude's tokenizer (documented ~60% underestimate) while relative savings are stable — reports must carry this caveat.
- The frozen 7-server / 45-tool corpus and 47-query golden set remain the in-house reference; public corpora complement rather than replace them.
- TSCG's reference implementation (MIT, TypeScript, zero-dependency) may be invoked as an external process during offline encoding; benchmark runs that include the TSCG arm therefore require a Node.js runtime, consistent with existing E2E prerequisites. CI provides this runtime and MUST run the arm (FR-006); only local runs may skip it, always with an explicit skip reason in the report.
- A schema-bearing frozen corpus (reference-server tools with full parameter schemas) is added as a committed snapshot for arm comparisons; the existing schema-less `corpus_v1` remains for back-compat with prior reports. The tool-result fixture set for the TOON-results arm is likewise committed and license-clean (outputs of our own reference servers).
- "Naive menu" means: all tools of the measured corpus rendered with full definitions (name + description + full parameter schema), the same rendering used by the baseline arm.
- Multi-turn cost cannot be measured without driving a real LLM agent loop (out of scope); the estimator with documented assumptions is the honest substitute at this stage, using published retry-cascade findings as defaults.
- Production proxy behavior (retrieve_tools response format, routing modes, tools_limit) is unchanged by this feature; a follow-up spec ("switchable discovery methods") will consume this profiler's results.
- The existing golden set's known properties (paraphrased queries, no verbatim tool names) carry over; no re-labeling is in scope.
- LAP is used as-is from its published package; contributing upstream (e.g., its in-progress deferred-facade support) is welcome but out of scope.

## Out of Scope

- Changing production `retrieve_tools` behavior, response format, or adding routing modes/config switches (follow-up spec).
- Training or fine-tuning embedding retrievers; evaluating embedding-based retrieval (may reuse loaders later).
- Forking LAP.
- Driving live LLM agents to measure true multi-turn cost (estimator only).
- Publishing/hosting the public report (rendering is in scope; distribution is a separate promotion task).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(bench): add discovery response cost measurement

Related #175

Measures live retrieve_tools response tokens per golden query with
component breakdown and break-even analysis.

## Changes
- Response token measurement with p50/p95/max
- Component decomposition (schemas/descriptions/instructions/metadata)

## Testing
- Unit tests for decomposition on fixture responses
- Live E2E against snapshot corpus
```
