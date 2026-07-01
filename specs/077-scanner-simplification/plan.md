# Implementation Plan: Scanner Simplification — Deterministic Default, Opt-In Deep Scan

**Branch**: `077-scanner-simplification` | **Date**: 2026-06-30 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/077-scanner-simplification/spec.md`

## Summary

Make the deterministic offline detection engine (Spec 076, `internal/security/detect/`) the always-on default scanner that requires zero Docker, and demote the six Docker scanner plugins plus source-code extraction to an opt-in "deep scan" that never blocks or degrades the baseline verdict. Remove the duplicate legacy phrase/secret rules layered on top of the detect engine, preserving today's blocking posture through one new hard-tier `phrase_injection` check. Merge all scanner output into a single normalized report where cross-scanner agreement boosts confidence, and collapse the per-scanner notification storm into one settled event. This is an incremental refactor of the existing scanner pipeline; the tool-quarantine state machine and the Docker plugins themselves are retained unchanged.

## Technical Context

**Language/Version**: Go 1.24 (backend/core), TypeScript 5.9 / Vue 3.5 (frontend Web UI)
**Primary Dependencies**: Existing only — `internal/security/detect` (stdlib + `golang.org/x/text/unicode/norm`, already an indirect dep), `internal/security/scanner`, BBolt (scanner records + tool approvals), Bleve (index, untouched), zap (logging). **No new third-party dependency.**
**Storage**: BBolt `config.db` (scanner config records, tool-approval hashes), `mcp_config.json` (config, hot-reloaded)
**Testing**: `go test -race ./internal/...`, `cmd/scan-eval --gate` (recall/FP corpus gate in `eval.yml`), `./scripts/test-api-e2e.sh`, Playwright Web-UI sweep
**Target Platform**: Personal edition (macOS/Windows/Linux, no Docker assumed) + server edition (Docker); baseline MUST run on all
**Project Type**: Web application — Go core (`internal/`, `cmd/`) + Vue frontend (`frontend/src/`) + CLI
**Performance Goals**: Baseline scan offline and fast for up to 1,000 tools (constitution I); deterministic (identical output for identical input)
**Constraints**: Offline-capable baseline (zero Docker/network/subprocess); no new dependency; no detection-coverage regression; back-compat config migration
**Scale/Scope**: Up to 1,000 tools per instance; ~6 Docker plugins demoted; 2 legacy rule sets removed; 1 new hard check; ~1 config block added, 1 removed

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` (6 principles).*

- **I. Performance at Scale** — PASS. Baseline is the existing detect engine (pure Go, in-process, already benchmarked for the 1k-tool target). Removing Docker from the default path *reduces* latency and resource use. Deep scan (opt-in) keeps the existing parallel-goroutine engine.
- **II. Actor-Based Concurrency** — PASS. No new locking. Deep scan reuses the existing goroutine-per-scanner engine with context propagation; the notification debounce is a channel/timer collapse, not a mutex.
- **III. Configuration-Driven Architecture** — PASS. New `security.deep_scan` block is JSON-config-driven, hot-reloadable, default-off (sensible default). Deprecated keys migrate on load. Tray remains a UI controller (reads the unified report via REST).
- **IV. Security by Default** — PASS WITH NOTE. The constitution requires automatic quarantine + transparency + isolation. **Unchanged**: new servers still quarantined (FR-019), tool calls still logged, the quarantine state machine is untouched. The change demotes Docker *scanner plugins* (a detection aid), **not** stdio-server Docker *isolation* (a separate subsystem governed by `isolation.mode`, MCP-34). Net effect on default security is **positive**: today users without Docker get a degraded/absent scan; after this change every user gets an always-on deterministic baseline that blocks poisoned tools offline. The one deliberate posture change (some legacy-blocked phrases become review-only unless in the curated hard set) is bounded by FR-004 and gated by the eval corpus (SC-003/SC-004). See Complexity Tracking.
- **V. Test-Driven Development** — PASS. Red-first tests for: no-coverage-loss after legacy-rule deletion, `phrase_injection` hard-tier recall/FP, deep-scan isolation from baseline verdict, config migration round-trip, notification collapse. `scan-eval --gate` extended. `golangci-lint` clean.
- **VI. Documentation Hygiene** — PASS. Update `docs/features/tool-scanner.md` (remove legacy-coexistence caveat, document baseline/deep-scan split + new check) and `docs/features/security-scanner-plugins.md` (deep-scan opt-in, config migration).

**Result**: All gates pass; one justified nuance recorded in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/077-scanner-simplification/
├── plan.md              # This file
├── research.md          # Phase 0 — technical decisions grounded in current code
├── data-model.md        # Phase 1 — report/finding/deep-scan entities + config schema
├── quickstart.md        # Phase 1 — how to verify the feature
├── contracts/           # Phase 1 — unified report JSON + config schema contracts
│   ├── scan-report.schema.json
│   └── security-config.schema.json
├── checklists/
│   └── requirements.md  # (from /speckit.specify)
└── tasks.md             # Phase 2 — /speckit.tasks output (NOT created here)
```

### Source Code (repository root)

```text
internal/security/
├── detect/
│   ├── checks/
│   │   ├── phrase_injection.go        # NEW hard-tier curated check
│   │   └── phrase_injection_test.go   # NEW
│   ├── engine.go                      # (unchanged; registration at wiring layer)
│   └── aggregate.go                   # tier/severity aggregation (unchanged)
├── scanner/
│   ├── inprocess.go                   # DELETE legacy tpaRules + legacy embedded-secret; detect-only
│   ├── registry_bundled.go            # Docker plugins default enabled:false; tpa-descriptions default on
│   ├── engine.go                      # deep-scan gating; drop degradeIfIncompleteCoverage-on-plugin-fail
│   ├── sarif.go                       # CalculateRiskScore: cross-source consensus fix
│   └── service.go                     # ScanSummary: baseline-only verdict + deep_scan descriptor
└── patterns/                          # (curated phrase patterns live here or in the new check)

internal/config/
└── config.go                          # remove auto_scan_quarantined; add security.deep_scan; migrate old keys

internal/runtime/
└── (scan notification emit path)      # collapse per-scanner SSE into one debounced scan.settled

cmd/scan-eval/
└── gate.go                            # gateChecks(): add phrase_injection hard check

frontend/src/
├── views/ServerDetail.vue             # approve modal gates on baseline dangerous only
├── views/ScanReport.vue               # single merged report; deep-scan availability as info
└── views/Security.vue                 # deep-scan opt-in affordance

testdata / eval corpus
└── detect_corpus_v1.json              # add curated phrase positives + benign near-misses
```

**Structure Decision**: Existing web-application layout (Go core + Vue frontend + CLI). This feature edits existing packages in place; the only net-new files are the `phrase_injection` check (+test), the two contract schemas, and corpus additions. No new package or binary.

## Complexity Tracking

| Violation / Nuance | Why Needed | Simpler Alternative Rejected Because |
|--------------------|------------|--------------------------------------|
| Security-by-default posture change: some phrases that legacy rules hard-blocked become review-only unless in the curated `phrase_injection` set | Legacy rules used plain substring matching with high false-positive risk; the curated hard set preserves blocking for high-confidence phrases while the detect engine's soft tier surfaces the rest | Keeping all legacy phrase rules blocking (rejected: perpetuates false-positive blocking the detect engine was built to fix); dropping all phrase blocking (rejected: weakens protection below today's baseline) |
| Retaining the Docker scanner engine/abstraction for opt-in use rather than deleting it | Preserves deep source-level analysis for advanced users at zero default cost | Hard-removing the plugins (rejected by design decision: deep scan must remain available; out of scope for this spec) |
