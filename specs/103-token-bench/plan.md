# Implementation Plan: Token-efficiency benchmark — measured savings, published results

**Branch**: `103-token-bench` | **Date**: 2026-08-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/103-token-bench/spec.md`

## Summary

Add two measurement capabilities to the existing `bench/` harness, and one small backend
contract fix that makes the first of them honest.

1. **US1 — deterministic replay** (`-replay`): load exported activity JSONL, group it into
   units of work, and recompute what that real workload would have cost under every valid
   mode. No model, byte-reproducible.
2. **US2 — live agent loop**: run MCPMark against mcpproxy under each mode, capturing
   provider-reported token usage and per-task pass verdicts, to produce tokens per
   *completed* task, first-attempt success and retry rates.

The research pass changed three things materially versus the spec's assumptions, all in the
direction of less work:

- **The mode matrix is 5 distinct behaviours, not 12.** The three axes are not a free product:
  each serialization axis has exactly one consumer, so the other 7 combinations are
  configurable but **behaviourally redundant** — each collapses onto one of the 5. They are
  reported as skip-with-reason rows naming the cell they collapse onto, never as zeros and
  never as "impossible".
- **The routing-mode axis costs nothing to cross.** All three routing-mode servers are built
  at startup and permanently mounted, so that axis is selected by endpoint URL with no config
  change and no restart. The two serialization axes still require config; whether they
  hot-reload is an open question that changes the runner's shape (see below).
- **MCPMark needs one `elif` to point at mcpproxy**, and already emits per-task token usage,
  pass verdicts, turn counts and pass^k. The spec's fallback plan (build an in-repo task set)
  is not needed.

## Technical Context

**Language/Version**: Go 1.25 (`bench/`, `internal/`); Python 3 for the pinned external suite
**Primary Dependencies**: existing only — `tiktoken-go` (pinned `v0.1.8`, `cl100k_base`),
`mark3labs/mcp-go` transport via `bench/mcpcaller.go`, chi/bbolt/zap unchanged.
**No new Go module dependencies.** MCPMark is an external, SHA-pinned tool invoked out of
process, not vendored and not a module dependency.
**Storage**: N/A for the harness. Replay inputs are operator-supplied JSONL files that live
outside the repository and are never committed.
**Testing**: `go test ./bench/...` (unit + invariant + schema-conformance), plus the existing
`reportv2_e2e_test.go` JSON-schema validation path.
**Target Platform**: developer machine + CI (ubuntu-latest) for the deterministic half; the
live half is operator-run and never CI-gated, because it costs model spend.
**Project Type**: single Go project; `bench/` is an existing in-repo tool tree.
**Performance Goals**: N/A — this is a measurement tool. The relevant budget is model spend,
not latency.
**Constraints**: reports are never committed; the deterministic half must be byte-reproducible
and must run offline; no recorded session content may reach a report or a third party.
**Scale/Scope**: 5 matrix cells + 1 baseline; MCPMark's 51 credential-free tasks
(filesystem 30 + postgres 21) as the clean core, 127 total if credentialed services are added.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Assessment |
|---|---|
| **I. Performance at Scale** | N/A to production paths. The harness adds no code to a request path. The one production-touching change (Phase 0 item 3 below) adds two integers to an export DTO. |
| **II. Actor-Based Concurrency** | No new concurrency in production code. The harness is a batch tool. |
| **III. Configuration-Driven** | PASS: the matrix is crossed by endpoint URL plus existing config fields, adding no benchmark-only configuration to the product. |
| **IV. Security by Default** | **The binding gate for this feature.** Replay inputs are raw user traffic; the export path deliberately does not mask. Design must default to bodies-off and must never transmit recorded content off-box. See Security Design below. |
| **V. TDD** | PASS. Every unit below is specified test-first; the deterministic half is fully unit-testable with no proxy, following the `flipgate.go` precedent of parameterising the driver over a function type. |
| **VI. Documentation Hygiene** | PASS. `bench/README.md` is the feature's user-facing doc and must gain the replay and live-loop sections; `CLAUDE.md` needs no change (no new commands at the top level, no architecture change). |

**No violations. Complexity Tracking is therefore empty and omitted.**

### Security Design (Principle IV)

Three findings drive this and each becomes a design rule:

1. **The export path never masks.** `maskActivityPayloads` is wired into the list and detail
   handlers only, not into export. Bodies-on export is raw and unmasked.
   → **Rule**: replay defaults to bodies-off. Bodies-on is a separate, explicitly-opted-in
   mode with a printed warning.
2. **`has_sensitive_data` is set asynchronously after the record is persisted.** A freshly
   exported record can be sensitive but not yet flagged.
   → **Rule**: exclude-by-flag is a best-effort reducer and MUST NOT be described as a
   guarantee, in code comments or in the published methodology.
3. **Bodies are not needed for the headline.** Byte sizes recorded pre-truncation give an
   accurate response-cost figure without reading content (see Phase 0 item 3).
   → **Rule**: the default configuration produces the headline without ever loading a body.

## Phase 0: Research

Complete. See [research.md](./research.md) for the full decisions, alternatives and evidence.
Five topics were resolved; the load-bearing outcomes:

1. **Extension seams** — three exist. Spec 103 uses the "new measurement mode" seam twice
   (flag on `cmd/bench` + driver in package `bench` + report block), following the spec-085
   flip-gate precedent. **No new arm is needed**, and no new binary.
2. **The matrix is 5 distinct behaviours**; the other 7 combinations of the 3-axis product
   are configurable but redundant and collapse onto them. The routing-mode axis is selected by
   endpoint URL with no restart; the serialization axes still need config.
3. **One backend change is required**: `request_bytes` / `response_bytes` exist on the storage
   record, explicitly measured pre-truncation, but are absent from the export contract. Adding
   them turns FR-002 from "exclude truncated records" into "annotate them accurately", which
   materially improves the headline's coverage.
4. **MCPMark is adopted**, SHA-pinned, with one `elif` in its single MCP factory. Its per-task
   `meta.json` feeds FR-010/011/012/018 directly.
5. **Token accounting stays split**: tiktoken for everything deterministic; provider `usage`
   read off the model response for the live loop; the two never summed, enforced by the
   harness's existing withhold-rather-than-compute pattern.

### Risks carried into Phase 1

- **`ReportV2.Tokenizer` is a report-level singleton.** The moment provider-usage figures
  enter the same envelope, that field silently claims the whole report was tiktoken-counted.
  It must be scoped to the deterministic sections, not merely supplemented.
- **Provenance is section-level, not per-figure.** FR-013 requires measured and estimated
  figures to coexist inside one block, so new row types need their own provenance field.
- **tiktoken fetches its vocabulary over the network** unless `TIKTOKEN_CACHE_DIR` is set;
  nothing in the repo or CI sets it. SC-004 (an outsider reproduces the deterministic figures)
  is not currently achievable on a restricted network.
- **`RetryRateForArm` returns 0.0 for unknown arms**, indistinguishable from a measured 0.0.
  Mixing a measured rate with a defaulted one under a single section badge is a live hazard
  for FR-013.
- **SC-011 has no enforcement.** "Reports are never committed" is gitignore plus convention;
  no CI job or test fails on a committed report.

## Project Structure

### Documentation (this feature)

```text
specs/103-token-bench/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0 decisions + evidence
├── data-model.md        # Phase 1 entities
├── quickstart.md        # Phase 1 operator walkthrough
├── contracts/
│   ├── report-v2-additions.md      # additive report blocks + per-row provenance
│   ├── mode-matrix.md              # the 5 valid cells + 7 skip reasons
│   └── replay-input.md             # activity JSONL contract consumed by replay
├── checklists/
│   └── requirements.md
└── tasks.md             # /speckit.tasks output — NOT created here
```

### Source Code (repository root)

```text
bench/
├── replaycorpus/                 # NEW — activity JSONL loader, grouping, usability flags
│   ├── load.go
│   ├── group.go                  # work_session_id grouping; parent_id ↔ request_id join
│   └── flags.go                  # truncated / bodies-missing / sensitive / unreplayable
├── replay.go                     # NEW — deterministic cost recomputation over the matrix
├── modematrix.go                 # NEW — the 5 valid cells, skip reasons, cell→URL mapping
├── agentloop.go                  # NEW — live-loop driver + MCPMark meta.json ingestion
├── reportv2.go                   # EXTEND — additive optional blocks; per-row provenance
├── live_report.go                # EXTEND — scope Tokenizer to deterministic sections
├── session.go                    # EXTEND — measured rates supersede armRetryRates
└── cmd/bench/main.go             # EXTEND — -replay and -agent-loop branches

internal/contracts/activity.go    # EXTEND — add request_bytes / response_bytes
internal/httpapi/activity.go      # EXTEND — copy them in the export projection

specs/083-discovery-profiler/contracts/report-v2.schema.json   # EXTEND — declare new blocks
.github/workflows/bench.yml       # EXTEND — TIKTOKEN_CACHE_DIR; assert no committed reports
```

**Structure Decision**: The feature lives almost entirely inside the existing `bench/` tree,
using its established seams. `bench/replaycorpus/` is a new sibling of `bench/corpusio/`
rather than an addition to it, because `corpusio` is scoped by its own doc comment to public
tool-retrieval corpora and produces `Corpus`/`GoldenSet` values — a session trace is neither.
`replay.go`, `modematrix.go` and `agentloop.go` sit in package `bench` (not a subpackage)
because they need `Tokenizer`, `ProxyToolsForMode` and the `EncodingArm` interface, and
`bench` cannot import `bench/arms` (a documented cycle); they cross that boundary with the
same structural interface `RunArms` already uses.

The only production-code change is two additive DTO fields plus their projection — deliberately
minimal, and justified because without them FR-002 degrades from accurate annotation to
wholesale exclusion.

## Phase 1: Design & Contracts

Artifacts produced: [data-model.md](./data-model.md), [contracts/](./contracts/),
[quickstart.md](./quickstart.md).

**Post-design constitution re-check: PASS.** The design adds no production request-path code,
introduces no new abstraction beyond one loader package that mirrors an existing sibling, and
adds no module dependency. Principle IV is satisfied by the bodies-off default rather than by
a masking layer, which is the simpler of the two available designs.

## Open decisions for the operator (not blockers)

These need a human, and none blocks starting US1:

0. **Can the two serialization fields be hot-reloaded, or does each value need a fresh
   instance?** This decides whether the matrix crosses on one long-lived proxy or on several,
   and it is the one open item that changes the runner's shape. Verify before `/speckit.tasks`.
1. **Pinned model and spend ceiling** for the live loop. US1 delivers alone without it.
2. **Whether cache-read tokens count toward "tokens per completed task."** FR-014 requires
   tracking them separately; FR-018 does not say which composite the headline uses. This is a
   definition decision, not a discovery.
3. **Whether to publish the measured tiktoken→Claude divergence.** The repo's caveat says up
   to ~60%; Anthropic's guidance says ~15–20% on typical text. Neither is sourced in-repo and
   they differ threefold. `count_tokens` settles it with no inference spend, and doing so turns
   FR-022's tolerance into a measured number.
