# Implementation Plan: Built-in tool scanner — SkillSpector-informed checks

**Branch**: `122-scanner-skillspector-checks` | **Date**: 2026-11-02 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/122-scanner-skillspector-checks/spec.md`

## Summary

Add four review-tier checks and one reconstruction check to the offline detect engine
(`internal/security/detect`): `refusal.directive` (anti-refusal / safety nullification),
`padding.hidden` (whitespace and repetition used to hide text), `payload.reconstructed` (bounded,
non-executing removal of an explicitly declared literal marker from a nearby payload, then the existing
matchers over the result), `exfil.composite` (credential access + transmission + external collector
inside one field region); tighten prohibition handling in `phrase.injection` with a bounded
direct-prohibition grammar (negation immediately governs the action, no exception / reversal cue, not
quoted, not a split line). All checks read fields structurally through one new bounded raw-JSON string
walker that keeps field paths and exact offsets, so schema strings are inspected on their own and never
assembled across fields. The eval harness gains a review-tier corpus and mode; adapted NVIDIA material
is attributed under Apache-2.0.

## Technical Context

**Language/Version**: Go 1.26 (module `github.com/smart-mcp-proxy/mcpproxy-go`)
**Primary Dependencies**: stdlib `regexp`, `unicode`, `encoding/json`; `golang.org/x/text/unicode/norm`
(already imported by detect). **No new module dependency.**
**Storage**: none (pure functions over in-memory tool views)
**Testing**: `go test` (table tests beside sources, `-race`), `go test -bench`, `cmd/scan-eval --gate`
and the new `--review-gate`
**Target Platform**: every mcpproxy build (personal + server edition); the detect package is
platform-independent
**Project Type**: single Go project, changes confined to `internal/security/detect/**`,
`internal/security/scanner/inprocess.go` (check registration), `cmd/scan-eval/**`, datasets, docs
**Performance Goals**: one tool with a 64 KiB adversarial description scanned by the new checks in
< 50 ms on a dev laptop; linear in field length (constitution I: 1,000 tools without degradation)
**Constraints**: offline (import guard `imports_test.go`), deterministic, total (recover), bounded
work and output; soft-only verdict for the four review checks; reconstruction never executes or
fetches
**Scale/Scope**: ~1,800 lines of Go across 5 new check files + 1 walker + 1 prohibition file, ~1,500
lines of tests, 1 corpus, docs

## Constitution Check

| Principle | Gate | Status |
|---|---|---|
| I. Performance at scale | linear per-field work, bounded caps, benchmarks on adversarial input | PASS (FR-038, SC-006) |
| II. Actor-based concurrency | no goroutines, no shared state; checks are pure | PASS (n/a) |
| III. Configuration-driven | no new config switch: the checks are part of the baseline set like the existing seven; thresholds are constants documented in the feature doc | PASS (documented in research R7) |
| IV. Security by default | review tier never auto-quarantines; hard tier only for the reconstructed shell payload; prohibition cap never suppresses | PASS (FR-005/006, FR-042) |
| V. TDD | failing table tests per family before each check; eval corpus before calibration | PASS |
| VI. Documentation hygiene | `docs/features/tool-scanner.md` section, third-party notices, CLAUDE.md "Recent Changes" line | PASS |
| DDD layering | detection stays in `internal/security/detect`; wiring in scanner; no runtime/httpapi change | PASS |

No violations; Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/122-scanner-skillspector-checks/
├── plan.md              # this file
├── research.md          # Phase 0: upstream coverage matrix + decisions
├── data-model.md        # Phase 1: Field, Signal extensions, PaddingRun, Reconstruction, Indicator
├── quickstart.md        # Phase 1: how to run the checks, tests, eval and benchmarks
├── contracts/
│   ├── check-ids.md     # check ids, tiers, confidences, detail wording, evidence shapes
│   ├── field-walker.md  # FieldStrings contract (paths, offsets, caps)
│   └── review-corpus.md # review-tier corpus schema + gate semantics
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/security/detect/
├── fields.go                     # NEW: bounded raw-JSON string walker → []Field (path, raw offsets, text, exact)
├── fields_test.go
├── prohibition.go                # NEW: DirectProhibition(text, start, end) bounded grammar (spec D)
├── prohibition_test.go
├── span.go                       # SchemaSpan(field, …) sibling of DescriptionSpan (exact-offset only)
└── checks/
    ├── anti_refusal.go           # NEW: refusal.directive (adapted AR1–AR3, Apache-2.0 header)
    ├── anti_refusal_test.go
    ├── padding_hidden.go         # NEW: padding.hidden (adapted whitespace_padding, Apache-2.0 header)
    ├── padding_hidden_test.go
    ├── marker_reconstruction.go  # NEW: payload.reconstructed (bounded subset of security_reconstruction)
    ├── marker_reconstruction_test.go
    ├── marker_reconstruction_bench_test.go
    ├── composite_exfil.go        # NEW: exfil.composite (agent_skills.yar credential/webhook rule, in Go)
    ├── composite_exfil_test.go
    ├── phrase_injection.go       # MODIFIED: prohibition cap for every family via detect.DirectProhibition
    ├── phrase_injection_prohibition_test.go  # NEW: spec D cases
    └── payload_decoded.go        # MODIFIED: export shellPayloadSignal helper for reuse by reconstruction

internal/security/scanner/
├── inprocess.go                  # MODIFIED: register the 5 checks in baselineEngineChecks
└── scan_verdict_review_test.go   # NEW: ScanToolMetadataVerdict → "warnings" / "dangerous" paths

internal/runtime/
└── tool_quarantine_scan_review_test.go  # NEW: scan-mode hold with only review signals (held_reason scan_findings)

cmd/scan-eval/
├── gate.go                       # MODIFIED: categoryCheck + gateChecks gain payload.reconstructed
├── review_gate.go                # NEW: --review-gate mode over the review corpus
└── review_gate_test.go

specs/065-evaluation-foundation/datasets/
├── detect_corpus_v1.json         # MODIFIED: + reconstruction malicious/hard-negative entries (hard tier)
└── detect_review_corpus_v1.json  # NEW: review-tier corpus (anti_refusal, padding, composite_exfil, prohibition, reconstruction_limits)

internal/security/scanner/bundled/selfcheck-corpus.json  # MODIFIED: copy of detect_corpus_v1.json (parity test)

docs/features/tool-scanner.md     # MODIFIED: "SkillSpector-informed checks" section
THIRD_PARTY_NOTICES.md            # NEW: NVIDIA SkillSpector attribution, reviewed commit, file list
third_party/skillspector/LICENSE  # NEW: Apache-2.0 text as shipped upstream
.github/workflows/eval.yml        # MODIFIED: review gate step after the hard gate
```

**Structure Decision**: all detection logic lives in the existing `detect` package tree (domain layer);
only the registration line in `scanner/inprocess.go` and the eval tool change outside it.

## Complexity Tracking

None.
