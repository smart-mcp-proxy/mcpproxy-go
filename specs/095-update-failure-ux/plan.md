# Implementation Plan: Tray Auto-Update Failure UX & Telemetry

**Branch**: `095-update-failure-ux` | **Date**: 2026-08-13 | **Spec**: [spec.md](./spec.md)
**Input**: Approved spec (Codex cross-review, 4 rounds) + research validated against vendored Sparkle 2.9.3 source and existing telemetry machinery.

## Summary

Replace Sparkle's dead-end "Update Error!" alert with a recovery dialog (Try Again / Download from Website / Cancel) by subclassing `SPUStandardUserDriver` and overriding only `showUpdaterError(_:acknowledgement:)`, and record every update-failure occurrence as one of four fixed `MCPX_UPDATE_*` diagnostics codes delivered tray→core through one new strict POST endpoint, riding the existing BBolt-persisted, 24h-windowed, heartbeat-surfaced error-code counter machinery unchanged.

## Technical Context

**Language/Version**: Swift 5.9 (tray, AppKit + Sparkle 2.9.3 vendored via SwiftPM) · Go 1.24 module toolchain (repo builds with local Go 1.25)
**Primary Dependencies**: existing only — Sparkle 2.9.3 (`SPUUpdater`, `SPUStandardUserDriver`), chi (httpapi), bbolt (diagnostics counters), swaggo/swag v2 (contract regen). **No new dependencies.**
**Storage**: existing BBolt `diagnostics_counters` bucket via `bboltDiagnosticsCounterStore.RecordErrorCode` (MCPX_-prefix guard, 24h decay window)
**Testing**: Go — httpapi handler tests (MockServerController pattern), diagnostics counter tests; Swift — XCTest (`cd native/macos/MCPProxy && swift test`), stub URLProtocol for APIClient, seam-injected fakes for Sparkle types; live rig — local Sparkle test rig with broken enclosure URL + manual protocol with screenshots
**Target Platform**: macOS 13+ (tray), all core platforms (endpoint is inert without a caller)
**Project Type**: Core + Tray split, additive deltas on both sides
**Performance Goals**: n/a (error path); recording request is off-UI-path, bounded timeout
**Constraints**: strict wire body `{"stage": ...}`; no free text in telemetry ever; event-time opt-out gating; version-skew inert both directions
**Scale/Scope**: ~6 Swift files touched/added, ~4 Go files touched/added, 1 new REST route, 4 new MCPX codes

## Constitution Check

- **I. Performance at Scale**: PASS — error-path only; no impact on search/indexing/routing.
- **II. Actor-Based Concurrency**: PASS — Go side is one handler + existing store; Swift side uses the existing `APIClient` actor; recording is a detached task off the main actor.
- **III. Configuration-Driven Architecture**: PASS — no new config keys; tray keeps no persistent state (dialog + queued-retry flag are ephemeral UI state; counts live in core BBolt). Telemetry gates read existing config/env.
- **IV. Security by Default**: PASS — new route sits inside `/api/v1` behind the existing auth middleware (socket bypass = AdminContext, TCP requires API key); strict body validation; endpoint records only closed-enum values; MCPX_ prefix guard is a second layer.
- **V. TDD**: PASS — every implementation task is preceded by a failing test (tasks.md enforces red→green pairing); Go + Swift suites; `golangci-lint` v2 gate.
- **VI. Documentation Hygiene**: PASS — swagger regen (`make swagger`) is a committed artifact + CI-verified; api-deltas contract doc in spec dir; no CLAUDE.md/README change needed (no command or architecture change).
- **Core+Tray split**: PASS — tray renders UI and relays one REST call; core owns persistence and telemetry.

## Project Structure

### Documentation (this feature)

```
specs/095-update-failure-ux/
├── spec.md                      # approved
├── plan.md                      # this file
├── research.md                  # Phase 0 — decisions + Sparkle source evidence
├── data-model.md                # Phase 1 — stage enum, codes, wire shape
├── quickstart.md                # Phase 1 — local verification walkthrough
├── contracts/api-deltas.md      # Phase 1 — REST + heartbeat deltas
├── checklists/requirements.md   # spec quality gate (done)
├── verification/baseline.md     # pre-change test baseline (done)
└── verification/manual-protocol.md  # live-rig protocol + screenshots (Phase: polish)
```

### Source Code (repository root)

```
# Go core (additive deltas)
internal/diagnostics/codes.go                 # +4 codes: MCPX_UPDATE_{APPCAST,DOWNLOAD,INSTALL,OTHER}_FAILED
internal/httpapi/update_failure.go            # NEW: POST /api/v1/telemetry/update-failure handler (strict decode, event-time gate)
internal/httpapi/update_failure_test.go       # NEW: handler tests (valid stages, rejects, gate no-op, durability call)
internal/httpapi/server.go                    # route registration + ServerController seam (one method: RecordUpdateFailure)
internal/httpapi/contracts_test.go            # MockServerController + shared baseController: implement new seam method
internal/server/server.go                     # forwarding implementation: gate + diagStore.RecordErrorCode behind one call
internal/diagnostics/*                        # register 4 codes in the catalog (Has/All)
internal/telemetry/anonymity.go               # scanner rule: error_code_counts_24h keys ∈ catalog, values non-negative ints
oas/swagger.yaml, oas/docs.go                 # regenerated (make swagger)

# Swift tray (native/macos/MCPProxy/MCPProxy)
Services/UpdateFailureClassifier.swift        # NEW: pure (provenance, domain, code, underlying) → Stage
Services/UpdateFailureDialog.swift            # NEW: NSAlert presenter + URL precedence + retry queue (seam-injectable)
Services/FeedUpdater.swift                    # SPUStandardUpdaterController → SPUUpdater + RecoveryUserDriver subclass;
                                              #   failedToDownloadUpdate provenance latch; didFinishUpdateCycle occurrence emit
Services/UpdateService.swift                  # wire observer: classify → record via APIClient; expose URL candidates
API/APIClient.swift                           # recordUpdateFailure(stage:) via postAction

# Swift tests (native/macos/MCPProxy/MCPProxyTests)
UpdateFailureClassifierTests.swift            # NEW: full classification table incl. cancellation/no-update exclusions
UpdateFailureDialogTests.swift                # NEW: URL precedence + fall-through, retry-after-terminal-signal ordering
APIClientUpdateFailureTests.swift             # NEW: exact request URL/body via stub URLProtocol; non-2xx single-log
```

## Key Design Bindings (from research)

1. **Dialog**: `RecoveryUserDriver: SPUStandardUserDriver` overrides only `showUpdaterError`. Sparkle's update drivers already gate the call per FR-002's matrix (`SPUUserInitiatedUpdateDriver` → `showErrorToUser:YES`; `SPUScheduledUpdateDriver` → `showErrorToUser:_showedUpdate`), so visibility is inherited, not re-implemented. `SPUStandardUpdaterController` is replaced by `SPUUpdater(hostBundle:applicationBundle:userDriver:delegate:)` + `start()` — the controller hardwires the stock driver (verified: FeedUpdater uses only `controller.updater`, no menu-action/validation reliance; the driver instance must be retained by FeedUpdater).
   **Cleanup ordering**: the stock `showUpdaterError` closes Sparkle's checking/status windows before alerting; the override must NOT run its own alert inside that call. Instead it stashes the error and calls `acknowledgement()` immediately (letting Sparkle tear down its windows via `dismissUpdateInstallation`), and the custom dialog is presented from the `didFinishUpdateCycleForUpdateCheck` handler. Corollary: when the dialog is on screen the failed session is already terminal, so **Try Again can start immediately** — the queued-retry path exists only as a belt-and-braces guard, and both orderings are unit-tested (FR-004).
2. **Occurrence & recording**: `updater(_:didFinishUpdateCycleForUpdateCheck:error:)` fires exactly once per session — the occurrence point (recording for ALL failures, including silent ones), the terminal-completion signal for FR-004, and the dialog presentation point (stashed error non-nil ⇔ Sparkle wanted the alert shown). `updater(_:failedToDownloadUpdate:error:)` sets the per-session download-provenance latch consumed by the classifier. Download cancellation surfaces as `userDidCancelDownload` + nil cycle error (there is no download-canceled error code in 2.9.3) — nil error ⇒ no occurrence, covered by tests.
3. **Classification**: pure function; exclusions first (`SUNoUpdateError` 1001, `SUInstallationCanceledError` 4007, `SUInstallationAuthorizeLaterError` 4008); then provenance/domain/code table → `appcast|download|install|other` (install range includes 4000 `SUFileCopyFailure` and 4001 `SUAuthenticationFailure`).
4. **Telemetry**: stage → `MCPX_UPDATE_<STAGE>_FAILED`. **One atomic seam method** `RecordUpdateFailure(stage string) (recorded bool, err error)` on `ServerController`, implemented as a forwarding method on `internal/server.Server` (the actual implementor — not runtime): it evaluates the gate (`config.IsTelemetryEnabled() && !telemetry.IsDisabledByEnv() && build-is-semver`) and performs the synchronous BBolt increment behind one call, eliminating the gate/record race. Handler: strict decode (`DisallowUnknownFields` **plus** trailing-value check via second `Decode` → `io.EOF`), 400 on invalid, 500 when `RecordUpdateFailure` errors (204's durability promise must hold), otherwise 204 whether recorded or no-op. The four codes are also registered in the diagnostics catalog (`diagnostics.Has`/`All()` — note `RecordErrorCode` itself only guards the MCPX_ prefix, not the catalog), and the anonymity scanner gains a rule validating `diagnostics.error_code_counts_24h` (catalog-registered keys, non-negative int values) per FR-014 — it does not structurally inspect that map today.
5. **URL precedence**: UpdateService's existing `downloadURL` is NOT installer-only (installer DMG, else any DMG, else `html_url`) and carries no cycle identity. New capture owned by **UpdateService** (which runs both brains per cycle and owns `checkCycleID`): `latestGitHubInstaller: (cycleID, version, installerURL)?` populated only when a true `-installer.dmg` asset matched. FeedUpdater reports occurrences as `(stage, offeredVersion?, feedInfoURL?)`; UpdateService assembles the FR-005 candidate list at occurrence time (installer candidate eligible on version match, or same-cycle when no offered version); candidate 2 = feed item `infoURL`; candidate 3 = constant releases page. HTTPS-absolute validation + fall-through in the dialog presenter.
6. **Tray delivery wiring**: UpdateService has no APIClient today — the recorder is injected as a closure/protocol (`UpdateFailureRecorder`) wired at the composition root that owns both UpdateService and APIClient. The APIClient method uses a **status-only** request helper (discards response data; never parses non-2xx bodies — `postAction`/`performRequest` would, violating FR-016) and logs one line: stage + numeric status/error class.

## Complexity Tracking

| Risk | Mitigation |
|---|---|
| SPUStandardUpdaterController → SPUUpdater migration changes updater bootstrap | Behavior-preserving: same delegate, same driver class (subclassed); existing UpdateServiceFeedTests must pass unchanged (SC-006) |
| Subclass override not called (Sparkle internals) | Verified in vendored source: `showUpdaterError` is dispatched via the `SPUUserDriver` protocol → dynamic dispatch; live-rig check in manual protocol is the backstop |
| Event-time gate divergence from other counters | Deliberate, spec'd (FR-013); handler-level gate, store untouched |
| Route reachable by agent tokens over TCP | Same posture as sibling telemetry-adjacent routes; body is a closed enum, worst case is counter noise from an authenticated caller |
