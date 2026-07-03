# Implementation Plan: Deterministic Offline MCP Tool-Scanner v2

**Branch**: `076-deterministic-tool-scanner` | **Date**: 2026-06-26 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/076-deterministic-tool-scanner/spec.md`

## Summary

Replace the in-process tool security detector — today a hardcoded substring matcher (recall ~0.10) — with a deterministic, fully-offline **signal pipeline** in a new package `internal/security/detect/`. Independent `Check`s each emit `Signal`s (tier hard/soft, threat type, confidence, evidence); a per-tool aggregator turns signals into `ScanFinding`s with confidence and contributing-check IDs. Six checks: three **hard** (auto-quarantine, near-zero FP) — hidden-Unicode, cross-server shadowing, decode-then-confirm — and three **soft** (review-raise, severity = distinct-signal count) — imperative-directive, capability-mismatch, embedded-secret. The `tpa-descriptions` scanner delegates to this engine. A labeled-corpus eval (`cmd/scan-eval`) is wired as a **blocking CI gate** at recall ≥ 0.90 / hard-negative FP ≤ 5%. Existing quarantine hashing, state machine, report types, and `patterns/` matchers are reused unchanged (patterns gains a confidence value). External scanner plugins are untouched.

## Technical Context

**Language/Version**: Go 1.24
**Primary Dependencies**: stdlib only for detection (`unicode`, `unicode/utf8`, `encoding/base64`, `encoding/hex`, `regexp`); `golang.org/x/text/unicode/norm` (already an indirect dep via x/text) for NFKC; existing `internal/security/patterns/`, `internal/security/scanner/`, `internal/runtime/tool_quarantine.go`. No new third-party dependency.
**Storage**: Reuses BBolt tool-approval store (quarantine states) and the existing `AggregatedReport` in-memory/job model. No schema change beyond additive `Confidence`/`Signals` fields on `ScanFinding`.
**Testing**: `go test -race ./internal/security/...`; corpus eval via `cmd/scan-eval` against `specs/065-evaluation-foundation/datasets/` + new fixtures; CI gate job.
**Target Platform**: All editions/platforms (personal + server); detection is platform-independent (no Landlock/Docker), so no build tags.
**Project Type**: Single Go module (backend); no frontend or schema-API surface changes required (report fields are additive and already serialized).
**Performance Goals**: Scanning is O(tools × text length); must stay well under the existing scan timeout budget for 1,000 tools (constitution: no degradation at 1k tools). All checks are linear-scan / bounded-regex; no backtracking-explosive patterns.
**Constraints**: Fully offline (no network/FS/Docker/LLM); deterministic (same input → same output); each check total (recover-isolated). Bounded work — no silent truncation of findings.
**Scale/Scope**: ~6 checks + normalizer + aggregator + registry-snapshot adapter; corpus grows from 43 to ~80–100 labeled entries; one new CI gate.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Performance at Scale** — ✅ Linear/bounded checks; regexes authored without catastrophic backtracking; cross-server check builds one registry index per scan, not per-tool. Stays under 1k-tool budget.
- **II. Actor-Based Concurrency** — ✅ No new long-lived actors. Checks are pure functions invoked within the existing scan job; per-check `recover()` keeps one failure from crashing the job goroutine.
- **III. Configuration-Driven Architecture** — ✅ Thresholds (recall/FP gate, confidence cutoffs, signal-count→severity map) are constants/config, not scattered magic numbers. No new user-facing config required for v1 (two-tier behavior is the default); auto-quarantine reuses existing `quarantine_enabled`.
- **IV. Security by Default** — ✅ This *is* the security feature. Hard checks default to auto-quarantine; the detector adds no egress (strengthens the privacy posture vs cloud scanners). Evidence is render-safe (truncated, control-char-escaped) to avoid the report itself becoming an injection vector.
- **V. Test-Driven Development** — ✅ Mandatory here: every check is built test-first with positive + hard-negative fixtures; the corpus gate is the integration-level TDD contract. (Repo CLAUDE.md: failing `_test.go` before implementation.)
- **VI. Documentation Hygiene** — ✅ Update `docs/features/security-quarantine.md` and `docs/features/sensitive-data-detection.md` (or a new `docs/features/tool-scanner.md`) describing the six checks, the two-tier model, and the eval gate.

**Result**: PASS, no violations. Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/076-deterministic-tool-scanner/
├── plan.md              # This file
├── spec.md              # Feature spec
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (internal Go interface contracts)
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/security/detect/                 # NEW package — the signal pipeline
├── signal.go            # Signal, Tier, ThreatType, Check interface, ToolView, RegistryView
├── normalize.go         # raw→normalized text pipeline (NFKC, strip zero-width, lower, collapse, stem)
├── position.go          # instruction-vs-example position classifier (hard-negative discriminator)
├── engine.go            # runs checks over a registry snapshot, recover-isolates each, aggregates
├── aggregate.go         # signals → ScanFinding (tier, severity=distinct-count, confidence, check IDs)
└── checks/
    ├── unicode_hidden.go        # HARD — raw-text hidden/bidi/tag/PUA detection + escalation
    ├── shadowing.go             # HARD — cross-server name reference / collision (uses RegistryView)
    ├── payload_decoded.go       # HARD — base64/hex decode-then-confirm shell/exfil
    ├── directive_imperative.go  # SOFT — regex families over normalized text + position discount
    ├── capability_mismatch.go   # SOFT — declared vs implied capability + unused-param data-sink
    └── embedded_secret.go       # SOFT — wraps internal/security/patterns with confidence

internal/security/scanner/inprocess.go    # MODIFIED — tpa-descriptions delegates to detect.Engine
internal/security/scanner/types.go        # MODIFIED — add Confidence float64, Signals []string to ScanFinding; stop dedup-collapsing agreement in risk score
internal/security/patterns/*.go           # MODIFIED (minimal) — surface a confidence per match (validated→high, entropy→low)

cmd/scan-eval/                             # MODIFIED — add recall/FP gate mode (exit non-zero on regression)
specs/065-evaluation-foundation/datasets/ # MODIFIED — expand corpus with new attack classes + hard-negatives

.github/workflows/ (existing test workflow) # MODIFIED — add the scan-eval gate step

docs/features/                             # MODIFIED — document the six checks, two-tier model, eval gate
```

**Structure Decision**: Single Go module. The detector lives in a new self-contained package `internal/security/detect/` with one file per check under `checks/`, so each check is independently testable and reviewable (DDD layering + isolation). The existing `scanner` package becomes a thin adapter that feeds a registry snapshot in and renders findings out, preserving all current entry points (CLI/REST/MCP) and the quarantine integration.

## Complexity Tracking

No constitution violations. Section intentionally empty.
