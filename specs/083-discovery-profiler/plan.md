# Implementation Plan: Discovery Effectiveness Profiler (mcp-discovery-bench)

**Branch**: `083-discovery-profiler` | **Date**: 2026-07-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/083-discovery-profiler/spec.md`

## Summary

Extend the existing `bench/` harness into a discovery-effectiveness profiler with three new capabilities: (1) live measurement of `retrieve_tools` **response** token cost over the real MCP protocol (per-query, percentiles, component breakdown, break-even analysis), (2) deterministic **encoding arms** (full-JSON baseline, compact signature, TSCG via `@tscg/core` subprocess, TOON via `toon-go`, optional TRON-style dedup) measured on a new schema-bearing frozen corpus with arm-aware retrieval scoring, and (3) **public corpus loaders** (ToolRet runtime-fetch for retrieval quality; LiveMCPTool committed snapshot for token/scale), plus a version-pinned **LAP** run in CI as an independent verdict, and an extended report.json + self-contained dashboard suitable for publication. Production proxy behavior is untouched.

## Technical Context

**Language/Version**: Go 1.25.5 (go.mod; CI uses Go 1.25) — bench package, same module as mcpproxy-go; Node.js ≥20 for the TSCG arm subprocess (CI-provided, matches existing E2E prereqs); Python via `uv` for dataset fetch + LAP (CI only)
**Primary Dependencies**:
- Existing: `github.com/pkoukk/tiktoken-go` (cl100k_base), `github.com/blevesearch/bleve/v2` (offline arm indexes), `github.com/mark3labs/mcp-go` (MCP client for real retrieve_tools calls)
- New Go dep: `github.com/toon-format/toon-go@v0.0.0-20251202084852-7ca0e27c4e8c` (MIT; bench-only import)
- External pinned tools: `@tscg/core@1.4.3` (npm, MIT, zero-dep; invoked via committed JS shim + package-lock), `lap-score==0.8.0` (PyPI, MIT; via `uvx`), `huggingface_hub`+`pyarrow` (fetch script via `uv run --with`, not committed deps)
**Storage**: Committed JSON fixtures under `specs/083-discovery-profiler/datasets/` (schema-bearing corpus_v2, tool-result fixtures, LiveMCPTool snapshot); runtime cache dir `bench/results/cache/` for ToolRet (never committed)
**Testing**: `go test ./bench/...` unit tests (arm determinism, bucket-sum invariant, break-even math, loader validation); live E2E via existing docker-less CI boot path
**Target Platform**: darwin/linux dev machines + ubuntu CI runner (existing bench.yml)
**Project Type**: single project — extension of existing `bench/` package + CI workflow
**Performance Goals**: default CI bench job < 15 min; ToolRet subset run < 30 min (SC-007); offline arm run on corpus_v2 < 1 min
**Constraints**: offline mode fully deterministic & network-free (FR-021); CI job stays `continue-on-error: true` (FR-022); no ToolRet data committed (FR-013); arm encoding byte-deterministic (FR-010)
**Scale/Scope**: corpora from 45 tools (frozen) to 43k tools (ToolRet); 47–7,615 queries (seeded subset default 250 for CI)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment |
|-----------|------------|
| I. Performance at Scale | Not a production path; bench must itself meet SC-007 budgets. ToolRet 43k-tool offline index build is the only scale risk — mitigated by seeded query subset + one-time index build per run. PASS |
| II. Actor-Based Concurrency | Bench remains a sequential CLI; no locks introduced. Live calls reuse one MCP client session. PASS |
| III. Configuration-Driven | Bench flags (existing pattern) + one new optional config surface: none in production `mcp_config.json`. PASS |
| IV. Security by Default | No new listeners; LAP/CI talk to the CI-booted proxy with its API key; fetch script downloads public datasets over HTTPS. PASS |
| V. TDD | Unit tests written first for: bucket-sum invariant, break-even formula, arm determinism (golden encoded-output fixtures), loader validation, seeded subset stability. PASS |
| VI. Documentation Hygiene | bench/README.md updated (new modes/flags, dataset provenance, license notes); CLAUDE.md untouched (no architecture change). PASS |

**New-dependency justification** (CLAUDE.md "avoid new dependencies without clear need"): `toon-go` is required to measure TOON honestly (FR-006/007) and is imported only from `bench/`; it never enters production binaries' import graph (verified in tasks via `go list -deps ./cmd/mcpproxy`). TSCG and LAP are subprocess/external tools, not Go deps.

## Project Structure

### Documentation (this feature)

```text
specs/083-discovery-profiler/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── report-v2.schema.json    # extended machine-readable report contract
│   └── arm-interface.md         # encoding-arm behavioral contract
├── datasets/
│   ├── corpus_v2.tools.json     # NEW schema-bearing frozen corpus (generated once, committed)
│   ├── result_fixtures_v1.json  # NEW deterministic tool-call outputs for TOON-results arm
│   └── livemcptool_snapshot/    # NEW Apache-2.0 frozen snapshot + ATTRIBUTION.md
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
bench/
├── arms/                    # NEW: encoding arms
│   ├── arm.go               # Arm interface + registry (name, IndexAltering, EncodeTool, EncodeListing)
│   ├── baseline.go          # full JSON schema rendering (canonical, deterministic key order)
│   ├── compact.go           # compact signature: name(param:type, opt?:type)|description
│   ├── toon.go              # TOON listing + results arms (toon-go)
│   ├── tscg.go              # subprocess arm → tscg/shim.mjs; skip-with-reason when node absent
│   ├── tron.go              # optional TRON-style named-class dedup (SHOULD)
│   └── *_test.go            # determinism + golden-output tests per arm
├── tscg/                    # NEW: pinned Node shim
│   ├── package.json         # @tscg/core@1.4.3 pinned
│   ├── package-lock.json
│   └── shim.mjs             # JSON tools on stdin → encoded text per tool on stdout
├── corpusio/                # NEW: public corpus loaders
│   ├── toolret.go           # cache-dir JSON → Corpus + GoldenSet (validation, seeded subset)
│   ├── livemcptool.go       # committed snapshot → Corpus
│   └── *_test.go
├── mcpcall.go               # NEW: real MCP retrieve_tools invocation + response capture
├── respcost.go              # NEW: span-based component attribution (sum==total by construction) + percentiles
├── breakeven.go             # NEW: break-even + session estimator (documented assumptions)
├── armindex.go              # NEW: temp-dir index via internal/index.Manager.BatchIndexTools → SearchFunc
│                            #      (production funnel; baseline parity test gates SC-003)
├── lapcheck.go              # NEW: parse LAP JSON artifact, compare menu counts, tolerance warning
├── report.go                # EXTENDED: report.json v2 + dashboard sections (arms, corpora, provenance)
├── live_report.go           # EXTENDED: response-cost measurement wired into RunLive
├── tokens.go                # EXTENDED: corpus_v2 loading; CountToolWithSchema reused per arm
└── cmd/bench/main.go        # EXTENDED: -arms, -corpus-v2, -toolret, -livemcptool, -seed, -subset flags

scripts/
├── fetch-toolret.sh         # NEW: uv-run parquet→JSON fetch at pinned HF revision (cache only, never committed)
└── gen-corpus-v2.sh         # NEW: one-time schema-bearing corpus generation from booted snapshot proxy

Makefile                     # EXTENDED: `bench-discovery` target = npm ci (bench/tscg) + offline arm run (SC-008; CI uses it)

.github/workflows/bench.yml  # EXTENDED: node setup, npm ci (bench/tscg), uvx lap-score step (pinned),
                             # LAP artifact upload, arms run, ToolRet subset job (non-blocking)
```

**Structure Decision**: single-project extension of the existing `bench/` package (same Go module). All new code lives under `bench/` (measurement domain), `scripts/` (fetch/generate helpers), and dataset fixtures under the spec directory, mirroring the 065 precedent. No production packages are modified except none — `internal/` is untouched.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| New Go module dep `toon-go` | FR-006/007 require honest TOON measurement with the official Go implementation | Reimplementing TOON in-tree would be more code and risk mis-measuring the format we're evaluating |
| Node subprocess for TSCG arm | TSCG reference impl is TypeScript-only; porting 1,200 LOC + 459 tests to Go is out of scope and would measure our port, not TSCG | Skipping TSCG entirely would drop a mandatory arm (FR-006) validated by two papers |
| Python (uv) fetch script for ToolRet | ToolRet ships as parquet; a Go parquet reader is a heavy new dep for a fetch-once path | Committing converted JSON violates FR-013 (license unstated) |
