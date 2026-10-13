# Implementation Plan: Adaptive TOON Output for Tool Results

**Branch**: `084-toon-output` | **Date**: 2026-07-14 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `specs/084-toon-output/spec.md`

## Summary

Add an opt-in, per-response **adaptive** TOON encoder for the text-block rendering of
`call_tool_read|write|destructive` results. A new deterministic package
`internal/toonenc` classifies a JSON text block as tabular-uniform and, when the complete
TOON emission (marker + decode hint + body) beats the exact passthrough emission by a
configured margin (`toon_min_savings_pct`, default 15%), replaces the block with TOON;
otherwise the block passes through byte-identically. The encoder is inserted into
`handleCallToolVariant` (`internal/server/mcp.go`) **after output sanitisation and before
truncation**, so the security-critical redact/block/strip pass still sees the raw upstream
result, the activity sensitive-data scan still sees the pre-encoding text, and the truncator
still applies to the final rendered payload. Mode is `off` (default) | `adaptive` | `always`,
global with a per-server override, hot-reloaded through the existing config path. Every
decision (encoded / passthrough-not-tabular / passthrough-below-threshold / passthrough-error)
lands in the `tool_call` activity metadata, and the spec-083 profiler gains a results arm that
imports the exact production encoder.

## Technical Context

**Language/Version**: Go 1.24 (backend/core). No frontend or Swift changes.
**Primary Dependencies**: `github.com/toon-format/toon-go` (official encoder — already used by the
spec-083 bench arm; **added to this branch's `go.mod` by this feature**, see Research D-DEP and
the coordination note under Assumptions). stdlib `encoding/json` for parse/classification and the
exact passthrough byte measurement. No other new third-party dependency.
**Storage**: BBolt — reuses the existing `ActivityRecord.Metadata map[string]interface{}`
(`internal/storage/activity_models.go:87`); no new bucket, no schema migration. The encoding
decision is a nested metadata key on the existing `tool_call` record.
**Testing**: `go test -race ./internal/...` (unit: classifier/encoder determinism, never-larger
invariant, config validation, resolver), `./scripts/test-api-e2e.sh` and
`internal/server/e2e_test.go` (hot-reload mode switching, detection parity, marker presence),
`go test ./bench/...` (profiler results arm). Frontend Playwright: **not applicable** (no UI surface).
**Target Platform**: All editions (personal + server). Personal edition is the primary surface;
server edition inherits it unchanged (no `//go:build server` code).
**Project Type**: Single Go project (backend core). Web-app / mobile options from the template do
not apply.
**Performance Goals**: Encoder runs synchronously in the call_tool hot path once per text block.
It must parse the block once, classify in a single pass, and encode at most once; classification
short-circuits before any TOON marshal on non-tabular blocks. Constitution I (BM25 < 100ms) is
unaffected — this feature never touches the discovery/index path (FR-013). Target: negligible
added latency versus the existing `json.Marshal`-based token counting already on this path.
**Constraints**: Determinism (FR-011) — identical input ⇒ identical decision and identical bytes;
enforced by `json.Number` decoding and a `canonicalToon` pass that recursively key-sorts objects into
`toon.NewObject` before marshaling (so Go's randomized map iteration never leaks into output — the
plan does NOT rely on toon-go's own map-key ordering). Never-larger (FR-004) —
enforced by the size comparison, not policy. Zero-regression when `off` (FR-002/SC-002) —
guaranteed because the encoder is a no-op that returns the input block unchanged when the resolved
mode is `off`.
**Scale/Scope**: One new internal package (~4 files), one seam in `mcp.go`, config fields +
validation + resolver, one detection-input thread, one activity-metadata thread, one bench arm.
No change to `retrieve_tools`, code_execution, direct mode, or any listing.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.1.0. Re-checked after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| **I. Performance at Scale** | PASS | Feature is confined to the `call_tool_*` result path; never touches BM25 search, indexing, or routing. One parse + one classify + at most one encode per text block. |
| **II. Actor-Based Concurrency** | PASS | Encoder is a pure, stateless function called inline on the goroutine already handling the tool call. No new goroutines, no shared mutable state, no locks. The detection scan stays on the existing async worker (`workersWG`). |
| **III. Configuration-Driven Architecture** | PASS | Behavior is fully driven by `toon_output` / `toon_min_savings_pct` (global) + per-server `toon_output` in `mcp_config.json`. Hot-reload via the existing `config_hotreload.go` path; no restart. Sensible default (`off`). Tray holds no state. |
| **IV. Security by Default** | PASS | Encoder runs **after** output sanitisation (redact/block/strip see the raw result) and the activity sensitive-data scan is fed the **pre-encoding** text (FR-007). No new network surface. Default `off` changes nothing. Any encoder error falls back to passthrough (FR-006) — a TOON bug can never lose or leak data. |
| **V. Test-Driven Development** | PASS | Every task is TDD-paired (failing test first). Unit + E2E + bench. Determinism and never-larger are property tests. Lint via golangci-lint v2. |
| **VI. Documentation Hygiene** | PASS | Updates: `docs/configuration.md` (new keys), tool-description wording for the marker contract (in `buildCallToolVariantTool`), CLAUDE.md "Recent Changes", and a new `docs/features/toon-output.md`. |

**Result**: PASS, no violations. Complexity Tracking below is empty by design.

## Project Structure

### Documentation (this feature)

```text
specs/084-toon-output/
├── plan.md              # This file
├── research.md          # Phase 0 — design decisions (encoder seam, marker, config, profiler)
├── data-model.md        # Phase 1 — entities: mode, classification, decision, marker
├── quickstart.md        # Phase 1 — operator + developer walkthrough
├── contracts/
│   ├── encoder-decision.md   # classify → size-compare → encode/passthrough decision contract
│   └── marker-format.md      # exact deterministic marker + decode-hint text contract
└── tasks.md             # Phase 2 — TDD-paired, phased by user story
```

### Source Code (repository root)

```text
internal/toonenc/                     # NEW — importable encoder package (NOT under internal/server,
│                                     #       so bench/ imports it without importing the server)
├── mode.go                           # Mode enum (off/adaptive/always) + ParseMode helper
├── classifier.go                     # deterministic tabular-uniform predicate (FR-003b)
├── classifier_test.go
├── canonical.go                      # canonicalToon: recursive key-sorted toon.Object ordering (FR-011)
├── encoder.go                        # EncodeBlock: parse → classify → canonicalize → size-compare → emit
├── encoder_test.go                   # determinism (incl. randomized-key-order) + never-larger property tests
├── marker.go                         # marker + decode-hint constant + assembly (contract)
└── marker_test.go

internal/truncate/
└── truncator.go                      # + SimpleTruncateBudget() int helper (limit - min(200,limit/2); 0=unlimited)
                                      #   so the encoder's too-small guard matches the truncator's real budget

internal/config/
├── config.go                         # + top-level ToonOutput, ToonMinSavingsPct (~line 138);
│                                     #   + ServerConfig.ToonOutput (~line 445);
│                                     #   + DefaultConfig defaults (~line 1358);
│                                     #   + ValidateDetailed rules (~line 1600);
│                                     #   + ResolveToonOutput(sc) string resolver (string-only; NO toonenc import)
└── config_toon_test.go               # validation + precedence (per-server > global > default)

internal/runtime/
├── config_hotreload.go               # + change-detection for toon_output / toon_min_savings_pct (~line 91)
├── event_bus.go                      # + detection_text passthrough on ToolCallCompleted payload (~line 409)
└── activity_service.go               # + merge encoding decision into tool_call Metadata (~line 470);
                                      #   + feed runAsyncDetection the pre-encoding text (~line 551)

internal/server/
├── mcp.go                            # SEAM: insert encoder AFTER the raw-byte measurement (~2100)
│                                     #   and BEFORE forwardContentResult (~2102) in handleCallToolVariant
│                                     #   (do NOT move the Spec 069 measurement at 2099-2100);
│                                     #   capture pre-encoding detection text + per-block decisions;
│                                     #   thread both into emitActivityToolCallCompleted (~2148)
├── toon_encode.go                    # NEW — server-side seam helper: walks TextContent blocks,
│                                     #   calls toonenc.EncodeBlock per block, returns encoded result
│                                     #   + pre-encoding text + []toonenc.Decision (mirrors the
│                                     #   forwardContentResult content walk)
└── toon_encode_test.go               # seam: sanitise-before-encode, encode-before-truncate ordering,
                                      #   direct-mode / code_execution non-application

bench/                                # spec-083 profiler (arrives via PR #851; see Assumptions)
└── arms/toon_results.go              # NEW — adaptive-encoder results arm importing internal/toonenc
                                      #   (FR-012); exercises the exact production EncodeBlock over
                                      #   the spec-083 result-fixtures corpus

docs/
├── configuration.md                  # + toon_output / toon_min_savings_pct reference
└── features/toon-output.md           # NEW — feature doc (adaptive rationale, modes, safety chain)
```

**Structure Decision**: Single Go project. The encoder is a **standalone `internal/toonenc`
package**, not a helper inside `internal/server`, for one load-bearing reason: FR-012 requires the
spec-083 bench arm to exercise *the exact production code path*, and `bench/` must import the
encoder without dragging in the entire `internal/server` package (which pulls the HTTP server, MCP
runtime, storage, etc.). `internal/server` and `bench/arms` both depend on `internal/toonenc`;
`internal/toonenc` depends only on stdlib + `toon-go`. This mirrors the constitution's DDD layering
(domain logic — the classifier/encoder — lives below the infrastructure/presentation layer in
`internal/server`).

## Complexity Tracking

*No constitution violations. No new abstraction requires justification: the single new package is
mandated by FR-012 (shared import between server and bench) and by DDD layering, not introduced
speculatively.*

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| (none) | — | — |

## Phase 0 → 1 → 2 Traceability

- **Phase 0 (research.md)**: locks the four open design decisions — encoder seam position, marker
  text, config field shape, profiler integration — plus the FR-007 two-stage security ordering and
  the FR-008 too-small-limit guard.
- **Phase 1 (data-model.md, contracts/, quickstart.md)**: entities (Mode, Classification, Decision,
  Marker), the encode-decision contract, the marker-format contract, and an operator/dev walkthrough.
- **Phase 2 (tasks.md)**: TDD-paired tasks phased by user story (US1 adaptive core → US2 operator
  control → US3 safety-chain → US4 profiler), with a full FR/SC → task coverage matrix.
