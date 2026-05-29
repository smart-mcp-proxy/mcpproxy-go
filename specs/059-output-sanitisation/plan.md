# Implementation Plan: Output Sanitisation Enforcement (Spec 054 Track B)

**Branch**: `059-output-sanitisation` | **Date**: 2026-05-29 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/059-output-sanitisation/spec.md`

## Summary

Make mcpproxy's existing-but-discarded content-trust classification (Spec 035) and secret detection (Spec 026) actually contain untrusted tool output. At the single response chokepoint `forwardContentResult` (`internal/server/content_forward.go`), apply — gated by the tool's `trusted`/`untrusted` tag and a new `OutputSanitisationConfig` — (1) **default, non-mutating** spotlighting of untrusted text in source-identifying delimiters with delimiter-spoof escaping, and (2) **opt-in** secret redaction, control-sequence stripping, and block-on-critical, each emitting a `policy_decision` activity record. Mirror Track A's (Spec 056) config + decision-core + activity-emit pattern. Default config changes nothing for trusted tools and only adds a lossless wrapper for untrusted ones.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `github.com/mark3labs/mcp-go` (content block types), existing `internal/security` detector (Spec 026), `internal/contracts` trust tagging (Spec 035), `go.uber.org/zap`, stdlib `unicode`/`regexp`/`strings`
**Storage**: None new. Reuses the existing activity log (`ActivityBucket` in BBolt) for `policy_decision` records. Config lives in `mcp_config.json`.
**Testing**: `go test ./internal/...` (unit + integration), `go test -race`, `scripts/test-api-e2e.sh`, Playwright/chrome-ext for the Web UI activity view
**Target Platform**: Linux/macOS/Windows core server; both personal and server editions (no edition divergence)
**Project Type**: Single Go project (backend) with a Vue Web UI that only *displays* the resulting activity records (no new frontend logic required)
**Performance Goals**: Sanitisation runs on the response path only; spotlighting is O(n) string work, redaction reuses the already-budgeted detector. Must not regress the existing forward/truncation hot path (Constitution I).
**Constraints**: Default config MUST be non-mutating beyond the lossless wrapper (FR-B6); non-text blocks byte-identical (FR-B5); backward compatible (FR-X1).
**Scale/Scope**: Track B only. Tracks C/D/E out of scope. ~3 new files + edits to config + 2 chokepoint call sites.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Performance at Scale** — PASS. Response-path only; spotlight is linear; redaction reuses the existing detector with its existing size budget. Add a fast-path early return when sanitisation is off + trust is trusted, so the common case adds one branch.
- **II. Actor-Based Concurrency** — PASS. Pure functions + a per-call hook; no new shared mutable state, no new locks.
- **III. Configuration-Driven Architecture** — PASS. New `output_sanitisation` config block in `mcp_config.json`, hot-reloadable like other config, sensible documented defaults.
- **IV. Security by Default** — PASS / reinforces. Default spotlights untrusted output (improves the default posture) while keeping the no-silent-mutation promise; destructive actions are opt-in.
- **V. TDD** — PASS. Failing `_test.go` first for each pure function and the decision core (per FR-X3).
- **VI. Documentation Hygiene** — PASS. Update `docs/features/` + CLAUDE.md note (mind the 40k gate — put detail in `docs/`, one-liner in CLAUDE.md if room) + this spec's quickstart.

No violations → Complexity Tracking left empty.

## Project Structure

### Documentation (this feature)

```text
specs/059-output-sanitisation/
├── plan.md              # This file
├── research.md          # Phase 0 — design decisions (detector spans, spotlight format)
├── data-model.md        # Phase 1 — config + decision entities
├── quickstart.md        # Phase 1 — how to enable & verify
├── checklists/
│   └── requirements.md  # spec quality (done)
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── security/
│   ├── sanitizer.go             # NEW: pure spotlight + escape, control-seq stripping (per-class), redaction span replacement
│   ├── sanitizer_test.go        # NEW: unit tests (TDD)
│   ├── detector.go              # EDIT: expose match spans (offsets) for in-place redaction — additive method
│   └── detector_test.go         # EDIT: span coverage
├── config/
│   ├── config.go                # EDIT: OutputSanitisationConfig + DefaultOutputSanitisationConfig + helpers + wire into Config + DefaultConfig
│   └── config_test.go           # EDIT: defaults + helper tests
└── server/
    ├── output_sanitisation.go        # NEW: evaluateOutputSanitisation decision core + applyOutputSanitisation hook (mirrors output_validation.go)
    ├── output_sanitisation_test.go   # NEW: decision-core unit tests (TDD)
    ├── content_forward.go            # EDIT: thread trust + sanitiser into the TextContent branch; non-text untouched
    ├── content_forward_test.go       # EDIT: spotlight/redact/block + non-text-preserved cases
    └── mcp.go                        # EDIT: pass contentTrust + config + emit policy_decision at the 2 call sites (1795, 2174)
```

**Structure Decision**: Single Go project. New sanitiser logic lives in `internal/security` (pure, reusable, testable in isolation) and `internal/server` (the hook + decision core, mirroring Track A's `output_validation.go`). The Web UI requires no changes — `policy_decision` records already render in the activity view; verification confirms they appear.

## Phase 0 — Research (see research.md)

Resolved design questions: (1) how to get secret span offsets out of the existing detector without breaking its API; (2) the exact spotlight delimiter format + spoof-escape strategy; (3) control-sequence class definitions; (4) where spotlighting sits relative to truncation/caching.

## Phase 1 — Design (see data-model.md, quickstart.md)

`OutputSanitisationConfig` shape, the `SanitisationDecision` verdict, and the trust-gated decision table. Agent context refreshed via the speckit script.

## Complexity Tracking

*No constitution violations — section intentionally empty.*
