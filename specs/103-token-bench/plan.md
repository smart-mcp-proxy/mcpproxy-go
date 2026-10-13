# Implementation Plan: Token-efficiency benchmark — measured savings, published results

**Branch**: `103-token-bench` | **Date**: 2026-08-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/103-token-bench/spec.md`

## Summary

Add two measurement capabilities to the existing `bench/` harness, and one small backend
contract fix that makes the first of them honest.

1. **US1 — deterministic replay** (`-replay` PLUS a fleet input): load exported activity JSONL,
   group it into units of work, and recompute menu cost per mode cell and the direct-cell delta
   for that real workload shape. **A fleet input is mandatory** — a frozen fleet corpus or a
   live proxy — because a menu is a property of the tool definitions and the export carries no
   fleet snapshot. No model. Reproducible **modulo the report's `generated_at` stamp**, which
   must be excluded or pinned before any byte-identical comparison (see Risks).
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
  change and no restart. The two serialization axes still require config, but **both
  hot-reload**, so the whole matrix still crosses on one long-lived instance with a config
  apply between serialization cells.
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
| **I. Performance at Scale** | Two production-touching changes, both trivial. (a) Two integers added to an export DTO — off the request path. (b) Propagating an already-computed `wasTruncated` boolean into the internal tool-call activity emit, which IS on the `retrieve_tools` request path: it passes a value the handler already has, adds no computation and no I/O. If even that is unwanted, the blanket-exclusion fallback removes it entirely at the cost of coverage. |
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
3. **Bodies-off yields menu costs and one cross-mode delta — never an absolute workload cost.**
   Complete workload includes every consumed response, and that text is absent bodies-off, so
   NO cell has a measured absolute figure. What it does give is menu cost per cell, plus the
   delta between the two direct cells, whose identical call responses cancel in the comparison.
   The recorded byte sizes are byte *lengths*, not token counts, and support only an estimate.
   → **Rule**: bodies-off is the default and reports menu costs plus the direct-cell delta;
   absolute workload figures are reported unavailable, never as zero.
   → **Rule**: response tokenization happens inside the loader, so no body text ever reaches
   the scoring layer (otherwise the no-content-leaves-the-loader invariant cannot hold).

## Phase 0: Research

Complete. See [research.md](./research.md) for the full decisions, alternatives and evidence.
Five topics were resolved; the load-bearing outcomes:

1. **Extension seams** — three exist. Spec 103 uses the "new measurement mode" seam twice
   (flag on `cmd/bench` + driver in package `bench` + report block), following the spec-085
   flip-gate precedent. **No new arm is needed**, and no new binary.
2. **The matrix is 5 distinct behaviours**; the other 7 combinations of the 3-axis product
   are configurable but redundant and collapse onto them. The routing-mode axis is selected by
   endpoint URL with no restart; the serialization axes still need config.
3. **Two backend changes are needed, both small.**
   (a) `request_bytes` / `response_bytes` exist on the storage record, measured pre-truncation,
   but are absent from the export contract. Adding them lets a truncated record carry an
   explicitly-estimated response cost instead of being dropped. They are byte lengths, **not**
   token counts, so they do not make response cost measurable.
   (b) **Truncation is not recorded for internal tool calls.** `wasTruncated` is computed in the
   `retrieve_tools` handler and only logged; `emitActivityInternalToolCall` has no truncation
   parameter, so the flag never reaches the activity record. Without propagating it the
   exclusion rule for truncated `retrieve_tools` records is unimplementable and the loader would
   silently overstate agent cost. The fallback — excluding every internal `retrieve_tools`
   record from response-cost accounting — must be an explicit decision, not a silent default.
4. **MCPMark is adopted**, SHA-pinned, with one `elif` in its single MCP factory. Its per-task
   `meta.json` feeds FR-010/011/012/018 directly.
5. **Token accounting stays split**: tiktoken for everything deterministic; provider `usage`
   read off the model response for the live loop; the two never summed, enforced by the
   harness's existing withhold-rather-than-compute pattern.

### Risks carried into Phase 1

- **`ReportV2.Tokenizer` is a report-level singleton.** The moment provider-usage figures
  enter the same envelope, a reader could take that field to describe them too. Resolve it
  **additively** — leave the existing field's meaning intact and give every new block its own
  `accounting_source` — rather than by narrowing the existing field, which would itself require
  a version bump. See contracts/report-v2-additions.md.
- **Provenance is section-level, not per-figure.** FR-013 requires measured and estimated
  figures to coexist inside one block, so new row types need their own provenance field.
- **tiktoken fetches its vocabulary over the network** unless `TIKTOKEN_CACHE_DIR` is set;
  nothing in the repo or CI sets it. Setting the variable only *names* a cache — a first run
  still downloads — so an offline reproduction additionally needs the cache **populated**
  (warmed once with network, or restored/vendored in CI). Without that, SC-004 is not
  achievable on a restricted network.
- **The report carries a wall-clock `generated_at` stamp.** SC-002 asks for byte-identical
  replay reports; two runs will differ on that field alone unless replay pins or excludes it.
  Decide which before writing the determinism test.
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
├── live_report.go                # EXTEND — per-block accounting_source (do NOT narrow Tokenizer)
├── session.go                    # EXTEND — measured rates supersede armRetryRates
└── cmd/bench/main.go             # EXTEND — -replay and -agent-loop branches

internal/contracts/activity.go    # EXTEND — add request_bytes / response_bytes
internal/httpapi/activity.go      # EXTEND — copy them in the export projection
internal/server/mcp.go            # EXTEND — propagate wasTruncated to internal tool-call
                                  #          activity (or accept blanket exclusion)

specs/083-discovery-profiler/contracts/report-v2.schema.json   # EXTEND — declare new blocks
.github/workflows/bench.yml       # EXTEND — TIKTOKEN_CACHE_DIR set AND cache populated
<a PR-triggered required workflow>     # SC-011 gate — bench.yml runs only on v* tags and
                                  # workflow_dispatch, so a gate there cannot block a PR
```

**Structure Decision**: The feature lives almost entirely inside the existing `bench/` tree,
using its established seams. `bench/replaycorpus/` is a new sibling of `bench/corpusio/`
rather than an addition to it, because `corpusio` is scoped by its own doc comment to public
tool-retrieval corpora and produces `Corpus`/`GoldenSet` values — a session trace is neither.
`replay.go`, `modematrix.go` and `agentloop.go` sit in package `bench` (not a subpackage)
because they need `Tokenizer`, `ProxyToolsForMode` and the `EncodingArm` interface, and
`bench` cannot import `bench/arms` (a real cycle — arms import `bench`); they cross that
boundary with the same structural interface `RunArms` already uses.

**Dependency rule for `bench/replaycorpus/` — decided, not deferred**: the existing
`bench/corpusio/` imports `bench`, so a `replaycorpus` that mirrored it could not be imported
by `bench/replay.go` without a second cycle. **Decision: `bench/replaycorpus` imports nothing
from `bench`.** It defines its own replay domain types, and `bench/replay.go` imports it. The
alternative (orchestrating from `cmd/bench`) is rejected because it would put measurement
logic outside the package that holds every other measurement.

**`modematrix.go` composes, it does not reimplement.** The pieces it needs already exist —
mode constants and proxy catalogs (`bench/tokens.go`), full rendering
(`bench/arms/baseline.go`), compact measurement (`bench/flipgate.go`), deferred-direct
rendering (`bench/arms/directdeferred.go`) and MCP transport (`bench/mcpcaller.go`). It must
reach them through passed structural interfaces, never by re-deriving a serialization.

The production-code changes are two, both deliberately minimal: the additive DTO fields plus
their export projection, and propagation of the already-computed truncation flag onto internal
tool-call activity. The first is justified because without it a truncated record must be
dropped rather than estimated; the second because without it a truncated `retrieve_tools`
record cannot be identified at all, and the loader would silently overstate agent cost.

If the second is rejected, the blanket-exclusion fallback stands in — at the cost of excluding
every internal `retrieve_tools` record from response-cost accounting. That trade must be made
explicitly and reported, not defaulted into.

## Phase 1: Design & Contracts

Artifacts produced: [data-model.md](./data-model.md), [contracts/](./contracts/),
[quickstart.md](./quickstart.md).

**Post-design constitution re-check: PASS.** The design touches the request path in exactly one
place — passing an already-computed boolean into an existing activity emit — and touches no
other request-path code. Its remaining production footprint is the additive export DTO fields
and their projection, both off the request path. It introduces no new abstraction beyond one loader package that mirrors an
existing sibling, and no module dependency. Principle IV is satisfied by the bodies-off default
rather than by a masking layer, which is the simpler of the two available designs.

## Open decisions for the operator (not blockers)

These need a human, and none blocks starting US1:

1. **Pinned model and spend ceiling** for the live loop. US1 delivers alone without it.
2. **Whether cache-read tokens count toward "tokens per completed task."** FR-014 requires
   tracking them separately; FR-018 does not say which composite the headline uses. This is a
   definition decision, not a discovery.
3. **Whether to publish the measured tiktoken→Claude divergence.** The repo's caveat says up
   to ~60%; Anthropic's guidance says ~15–20% on typical text. Neither is sourced in-repo and
   they differ threefold. `count_tokens` settles it with no inference spend, and doing so turns
   FR-022's tolerance into a measured number.
