# Tasks: Output Sanitisation Enforcement (Spec 054 Track B)

**Feature**: `059-output-sanitisation` | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

TDD is mandatory (Constitution V / FR-X3): the failing `_test.go` task precedes each implementation task.

## Phase 1: Setup

- [x] T001 Confirm baseline green: `go build ./cmd/mcpproxy` and `go test ./internal/security/ ./internal/config/ ./internal/server/ -count=1` pass before any changes.

## Phase 2: Foundational (blocking prerequisites)

- [x] T002 [P] Write failing tests for `OutputSanitisationConfig` defaults + helpers in `internal/config/config_test.go` (DefaultOutputSanitisationConfig values; IsSpotlightEnabled/IsRedact/IsBlock/IsStripEnabled/EnabledStripClasses/WouldMutate).
- [x] T003 Implement `OutputSanitisationConfig`, `DefaultOutputSanitisationConfig()`, helper methods in `internal/config/config.go`; wire `OutputSanitisation` field into root `Config` and into `DefaultConfig()` (mirror `OutputValidationConfig` at line ~144/473/766). Make T002 pass.

## Phase 3: User Story 1 — Untrusted output spotlighted by default (P1) 🎯 MVP

**Goal**: untrusted text wrapped in source-identifying delimiters with spoof escaping; trusted/non-text untouched.
**Independent test**: untrusted stub tool → response wrapped; sentinel-mimicking body escaped; trusted tool byte-identical; image/audio blocks untouched.

- [x] T004 [P] [US1] Write failing unit tests for `SpotlightUntrusted(text, server, tool)` in `internal/security/sanitizer_test.go`: wraps with `«untrusted:server/tool»` framing; escapes `«`/`»` in body (FR-B2); empty text → no-op.
- [x] T005 [US1] Implement `SpotlightUntrusted` in `internal/security/sanitizer.go` (new file). Make T004 pass.
- [x] T006 [P] [US1] Write failing decision-core tests in `internal/server/output_sanitisation_test.go`: `evaluateOutputSanitisation` returns Action=spotlight+Spotlighted for untrusted+default; Action=none for trusted+default (mirror `output_validation_test.go`).
- [x] T007 [US1] Implement `evaluateOutputSanitisation` decision core + `applyOutputSanitisation` (no-op for spotlight beyond wrapping) in `internal/server/output_sanitisation.go` (new file). Make T006 pass.
- [x] T008 [US1] Write failing integration tests in `internal/server/content_forward_test.go`: untrusted result text wrapped; trusted byte-identical; ImageContent/AudioContent/embedded preserved (FR-B5); spotlight applied AFTER truncation (outermost frame).
- [x] T009 [US1] Thread `contentTrust string` + `*config.OutputSanitisationConfig` (+ optional detector) into `forwardContentResult` (`internal/server/content_forward.go`); apply spotlight in the `TextContent` branch only; leave non-text untouched. Make T008 pass.
- [x] T010 [US1] Update the 2 call sites in `internal/server/mcp.go` (~1795, ~2174) to pass `contentTrust` (already computed at ~1525) + config into `forwardContentResult`. Verify trusted-tool path unchanged.

## Phase 4: User Story 2 — Opt-in redaction & control-sequence stripping (P2)

**Goal**: mask detected secrets with `[REDACTED:<category>]`; strip ANSI/C0-C1/zero-width/bidi per class; emit policy_decision.
**Independent test**: redact mode masks a planted AWS key + logs it; strip toggles neutralise only enabled classes; both off → no mutation.

- [x] T011 [P] [US2] Write failing tests for `StripControlSequences(text, classes)` in `internal/security/sanitizer_test.go`: per-class ANSI/c0c1/zero_width/bidi removal; `\n`/`\t` preserved; returns stripped class names.
- [x] T012 [US2] Implement `StripControlSequences` in `internal/security/sanitizer.go`. Make T011 pass.
- [x] T013 [P] [US2] Write failing tests for `(*Detector) Redact(content)` in `internal/security/detector_test.go`: replaces valid pattern matches with `[REDACTED:<category>]`; respects category-enabled + IsValid + IsKnownExample; returns detections; no match → unchanged.
- [x] T014 [US2] Implement `Redact` on `Detector` in `internal/security/detector.go` (additive; `regex.ReplaceAllStringFunc` over `patterns+customPatterns`). Make T013 pass.
- [x] T015 [US2] Extend decision-core tests in `internal/server/output_sanitisation_test.go`: redact action sets RedactedCount/Categories; strip action sets StrippedClasses; Reason populated.
- [x] T016 [US2] Extend `evaluateOutputSanitisation`/`applyOutputSanitisation` to perform redact (via detector) then strip (per D4 order) before truncate+spotlight. Make T015 pass.
- [x] T017 [US2] Integration test in `internal/server/content_forward_test.go`: redact masks secret in untrusted+trusted text; strip removes enabled classes; both still preserve non-text. Wire detector through; make green.
- [x] T018 [US2] Emit `policy_decision` activity record for redact/strip via `emitActivityPolicyDecision` at the hook in `internal/server/mcp.go`; assert in test (mirror Track A's emit + `mcp_output_schema_test`-style coverage).

## Phase 5: User Story 3 — Block on critical detection (P3)

**Goal**: block action replaces payload with remediation error + `blocked` audit on critical detection.
**Independent test**: block mode + critical secret → remediation error returned, blocked policy_decision written; non-critical passes through.

- [x] T019 [P] [US3] Write failing decision-core tests in `internal/server/output_sanitisation_test.go`: block + critical detection → Blocked=true, Action=block; block + non-critical → not blocked.
- [x] T020 [US3] Implement block short-circuit in `evaluateOutputSanitisation`/`applyOutputSanitisation` (evaluate before mutation per D4); return remediation `mcp.NewToolResultError`. Make T019 pass.
- [x] T021 [US3] Integration test in `internal/server/content_forward_test.go` + emit `blocked` policy_decision in `internal/server/mcp.go`; assert payload replaced and audit written.

## Phase 6: Polish & Cross-Cutting

- [x] T022 [P] Run `gofmt`/`goimports`; `./scripts/run-linter.sh`; ensure `go build -tags server ./cmd/mcpproxy` and personal build both pass (FR-X2 edition parity).
- [x] T023 Full suite: `go test -race ./internal/security/ ./internal/config/ ./internal/server/ -count=1`; then `./scripts/test-api-e2e.sh` green (SC-006 no regression).
- [x] T024 [P] Docs: add `docs/features/output-sanitisation.md`; update `docs/features/sensitive-data-detection.md` cross-link. (Do NOT grow CLAUDE.md past 40k — one-liner only if room.)
- [x] T025 **Mandatory verification** (see quickstart.md): curl/MCP roundtrip vs stub untrusted upstream (spotlight + redact + block); chrome-ext inspection of the Web UI activity view showing policy_decision rows; capture screenshots → build HTML report at `specs/059-output-sanitisation/verification/report.html`.

## Dependencies

- Phase 2 (config) blocks all stories.
- US1 (T004–T010) is the MVP and must land before US2/US3 (they extend the same hook + decision core).
- US2 before US3 (block reuses the detector wiring from US2).
- T010's call-site change is shared by all stories — touch once in US1, reuse.

## Parallel opportunities

- T002, then within US1: T004 ∥ T006 (different files). US2: T011 ∥ T013. Polish: T022 ∥ T024.
- The pure `internal/security` work (T004/T005, T011/T012, T013/T014) is independent of the `internal/server` hook and can be built by a separate agent in parallel with the decision-core scaffolding.

## MVP scope

User Story 1 (T001–T010): default spotlighting of untrusted output — delivers the core containment value, non-mutating, backward compatible.
