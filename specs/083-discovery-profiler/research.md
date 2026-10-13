# Research: Discovery Effectiveness Profiler

**Date**: 2026-07-14 · All decisions below resolve Technical Context unknowns; sources were adversarially verified in a 106-agent research sweep (session record) plus direct source reads.

## D1. Why measure responses, not just menus

**Decision**: Measure `retrieve_tools` responses over the real MCP protocol per golden query (FR-001).
**Rationale**: Live profiling of a 907-tool deployment showed median 8,640 / max 54,865 tokens per response with 77% of cost in raw `inputSchema`; break-even vs naive menu at ~38 calls. LAP and the existing bench both measure only the menu (bucket A). LiveMCPBench independently shows retrieval failures ≈ half of agent task failures, so response cost and quality must be tracked together.
**Alternatives considered**: REST `/api/v1/index/search` measurement (rejected — it is not the payload an agent pays for; misses `usage_instructions`, `call_with`, annotations).

## D2. Encoding arms set

**Decision**: baseline full-JSON, compact signature, TSCG, TOON (listings + results separately), optional TRON-dedup.
**Rationale**:
- Compact signatures: pre-measured −92% on live responses with recall unchanged; LAP independently reports +51% (Petstore, menu rendering); Anthropic's `tool_reference` design confirms search responses need not carry schemas at all.
- TSCG (arXiv:2605.04107, verified): deterministic compiler, ≥51% bound, 52–57% measured, accuracy *gains* on frontier models (+10.9pp mean vs native FC); follow-up (arXiv:2605.26165) gives budget-sweep design. Reference impl `@tscg/core@1.4.3` (MIT, zero-dep TS, v1.4.3 2026-04-26; npm packages `@tscg/core`, `@tscg/mcp-proxy` — no bare `tscg`).
- TOON: official spec concedes compact JSON often beats it on deeply-nested structures (= JSON Schema); independent benchmark (arXiv:2605.29676) measured ≤18% savings at ~9pp accuracy cost with multi-turn parsing cascades — we measure anyway because (a) user asked, (b) tabular tool *results* are TOON's favorable regime, and honest negative results are publishable.
- TRON (same paper): named-class schema dedup, up to 27% on tool-schema-heavy workloads; no Go impl exists → SHOULD-level in-tree minimal implementation or reported not-yet-measured.
**Alternatives considered**: LLMLingua-style learned compression (rejected — GPU, non-deterministic, FR-010 violation); `@tscg/mcp-proxy` as a live proxy-in-front-of-proxy arm (deferred to follow-up spec — this spec measures encodings, not deployment topologies).

## D3. TSCG invocation

**Decision**: committed Node shim (`bench/tscg/shim.mjs`) with pinned `package.json`/`package-lock.json` (`@tscg/core@1.4.3`); bench spawns `node shim.mjs` and exchanges **JSONL records keyed by `tool_id`** in both directions (`{"tool_id":..,"tool":{..}}` in, `{"tool_id":..,"encoded":"..."}` out) — encoded text travels as an escaped JSON string, so embedded newlines cannot split or merge records. Node absent → arm reported skipped-with-reason (CI always has Node per existing E2E prereqs).
**Rationale**: measures the reference implementation, not a port; deterministic (TSCG is a pure compiler, <1ms/tool); pinning satisfies FR-006's CI-mandatory requirement.
**Alternatives**: Go port (out of scope; measures the wrong thing); npx at runtime (unpinned, network in offline mode — violates FR-021).

## D4. Schema-bearing frozen corpus (corpus_v2)

**Decision**: generate `specs/083-discovery-profiler/datasets/corpus_v2.tools.json` once via `scripts/gen-corpus-v2.sh` (boots the 7-server snapshot config, exports `GET /api/v1/tools` with full input schemas, canonicalizes ordering), commit it, and use it for all arm comparisons. `corpus_v1` (schema-less) remains for back-compat with prior offline reports.
**Rationale**: Codex review P1 — corpus_v1 has no schemas, so arm comparison over it is meaningless; the 7 reference servers are no-auth and license-clean (065 precedent CN-001/005).
**Alternatives**: live-only arm measurement (rejected — non-deterministic, network-dependent, violates FR-010/021).

## D5. ToolRet access

**Decision**: runtime fetch, never committed. `scripts/fetch-toolret.sh` uses `uv run --with huggingface_hub,pyarrow` to download `mangopy/ToolRet-Tools` + `mangopy/ToolRet-Queries` (parquet, ~9.3 MB) **at a pinned HF revision** (`--revision`, default recorded in the script; the revision is stamped into the cache path and the report) and convert to JSON in `bench/results/cache/toolret/<revision>/`; the Go loader (`bench/corpusio/toolret.go`) reads only the cache, **sorts records by stable query/tool IDs before seeded subsetting** (fails on missing IDs), so identical revision + seed + size ⇒ identical subset (FR-014/021).
**Rationale**: dataset license unstated on HF (verified 2026-07-14) → FR-013 forbids committing; parquet→JSON via uv avoids a heavy Go parquet dependency for a fetch-once path. Code repo (mangopy/tool-retrieval-benchmark) is Apache-2.0; paper ACL 2025.
**Alternatives**: Go parquet reader (heavy dep); HF datasets-server REST API (row pagination brittle for 44.5k rows; extra service dependency).

## D6. LiveMCPTool access

**Decision**: commit a frozen snapshot under `datasets/livemcptool_snapshot/` with `ATTRIBUTION.md` (Apache 2.0, `ICIP/LiveMCPBench`, paper arXiv:2508.01780). Scope: token/scale corpus for arms; retrieval scoring only if labels derivable from task annotations (95 tasks) — SHOULD, attempted in a stretch task.
**Rationale**: Apache 2.0 permits redistribution; snapshot keeps offline runs network-free.
**Alternatives**: MCP-Zero MCP-tools corpus (rejected for now — Google-Drive-only distribution, embeddings baked in, HF "coming soon").

## D7. Arm-aware retrieval scoring

**Decision**: each arm declares `IndexAltering() bool`. For index-altering arms, `bench/armindex.go` builds a **temp-dir index through the production `internal/index.Manager` (`BatchIndexTools`)** — not a hand-rolled bleve mapping — so field mappings, analyzers, and the six-branch `SearchTools` query shape are the production code itself; the resulting index yields a `SearchFunc` for the existing `ScoreRetrieval`. A parity test proves the baseline arm reproduces the recorded golden-set baseline (recall@5 = 0.68 ± 0.05, SC-003) before any other arm's score is trusted. Rendering-only arms are labeled quality-neutral and skip scoring (FR-008).
**Rationale**: Codex review P2 — production index ingests `ParamsJSON` into searchable text, so schema re-encoding *can* move recall; replicating the production query shape keeps SC-003 meaningful (baseline arm must reproduce recall@5 = 0.68 ± 0.05).
**Alternatives**: re-index the live proxy per arm (slow, mutates shared state); skip quality for all arms (hides the recall cost the spec exists to expose).

## D7b. Canonical baseline renderer & span-based token attribution

**Decision**: one canonical renderer (`bench/arms/baseline.go`) produces the full-definition text (`name + description + canonical-JSON schema`) used by ALL of: the `baseline_json` arm, the naive full-menu count, the proxy-menu count, savings-% denominators, and break-even inputs — matching the existing `CountToolWithSchema` text shape so live "authoritative headline" parity is preserved. Response-component decomposition (FR-002) uses **span-based attribution**: partition the canonical response text into labeled byte spans, tokenize the whole text once, attribute each token to the span owning its starting byte — sum equals total by construction (BPE is not additive across separately tokenized fields).
**Rationale**: Codex plan-review P1/P2 — without a single renderer, savings percentages compare different denominators; without span attribution, the FR-002 invariant is mathematically unsatisfiable.

## D8. Break-even + session estimator

**Decision**: `break_even_calls = (naive_full_menu_tokens − proxy_menu_tokens) / mean_discovery_response_tokens` (FR-003). Session estimator: `session_cost(arm) = proxy_menu + calls_per_session × mean_response(arm) × (1 + retry_rate(arm))`, defaults `calls_per_session ∈ {1,3,5,10}`, `retry_rate` = 0 for JSON/compact (format-native), 0.05 for TOON listings (parsing-cascade literature, arXiv:2605.29676 §5) — all inputs surfaced in report as ESTIMATE provenance.
**Rationale**: honest substitute for driving a live agent loop (out of scope); literature-derived defaults documented per FR-019.

## D9. LAP integration

**Decision**: CI step `uvx --from lap-score==0.8.0 lap lint --mcp-url "$PROXY/mcp?apikey=$KEY" --json > lap.json`, uploaded as artifact; `bench/lapcheck.go` compares LAP's menu tokens vs in-house count, warns beyond ±15% tolerance (different tokenizer framing per LAP docs — LAP subtracts a base frame; divergence expected small since both use cl100k without ANTHROPIC_API_KEY).
**Rationale**: LAP 0.8.0 (2026-07-10, MIT) measures bucket A only — complementary, no fork needed; version pin satisfies FR-015/SC-006. Note: LAP's unreleased main adds a deferred-facade label relevant to us; revisit pin at implementation time if released.
**Alternatives**: fork (explicitly rejected in spec); mcpx (rejected — closed hosted backend, 2 stars).

## D10. Tool-result fixtures for TOON-results arm

**Decision**: `datasets/result_fixtures_v1.json` — deterministic outputs captured once from our own reference servers (filesystem list, git log, sqlite query, time now, fetch of a committed HTML fixture), classified tabular vs non-tabular; committed (license-clean, our own output).
**Rationale**: Codex review P2 — no data source existed for result measurements; own-server outputs avoid licensing issues (FR-007).

## D11. Tokenizer

**Decision**: keep tiktoken cl100k_base default with the documented ~60% underestimate caveat rendered wherever absolute numbers appear (existing bench limitation note, now surfaced in dashboard per SC-005).
**Rationale**: model-agnostic, deterministic, offline; relative savings stable (Kendall τ ≥ 0.992 across vocabularies per LAP's own validation).
**Alternatives**: Anthropic count_tokens API (network + key in CI; breaks FR-021 offline determinism) — MAY be added later as an opt-in live column.

## D12. Report v2 + dashboard

**Decision**: extend `report.json` with a versioned envelope (`report_version: 2`) — additive; existing consumers unaffected (report is not committed / externally consumed per CN-003). Dashboard stays a Go html/template single file; new sections: arms table, corpora table, response-cost percentiles, break-even, provenance badges (measured/computed/estimated), tokenizer caveat banner.
**Rationale**: FR-017/018, SC-005; keeps zero frontend deps.
