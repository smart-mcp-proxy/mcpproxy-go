# Quickstart: Discovery Effectiveness Profiler

## One command (SC-008)

```bash
make bench-discovery
# = npm ci --prefix bench/tscg  (pinned @tscg/core, one-time)
#   + go run ./bench/cmd/bench -corpus-v2 ... -arms all -out bench/results
# → bench/results/report.json (v2) + bench/results/dashboard.html
# CI runs this same target.
```

## Offline (deterministic, no network)

```bash
# All mandatory arms on the schema-bearing frozen corpus
go run ./bench/cmd/bench \
  -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json \
  -arms baseline_json,compact_sig,tscg,toon_listing \
  -out bench/results
```

TSCG arm prerequisite (covered by `make bench-discovery`): `npm ci --prefix bench/tscg` (Node ≥20). Without it the arm reports `skipped: node runtime unavailable` (allowed locally, fails SC-002 in CI).

## Live (response cost + break-even + latency)

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

## Public corpora

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

## Independent LAP verdict (what CI runs)

```bash
uvx --from lap-score==0.8.0 lap lint \
  --mcp-url "http://127.0.0.1:8092/mcp?apikey=eval-corpus-snapshot" --json > bench/results/lap.json
```

## Regenerating fixtures (maintainers only)

```bash
./scripts/gen-corpus-v2.sh     # schema-bearing frozen corpus from booted snapshot proxy
# result_fixtures_v1.json: captured once from reference servers; see datasets/README
```

## Verifying success criteria

- SC-003 gate: baseline arm on in-house golden set must report recall@5 = 0.68 ± 0.05.
- SC-001: report.json `.response_cost.p50/.p95`, `.break_even.break_even_calls` populated after a live run.
- SC-005: dashboard shows provenance badge + tokenizer caveat on every headline number.
