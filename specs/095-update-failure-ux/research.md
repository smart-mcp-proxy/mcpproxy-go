# Research — spec 095 (Phase 0)

All decisions below were validated against the vendored Sparkle checkout at
`native/macos/MCPProxy/.build/checkouts/Sparkle/` (2.9.3 per `Package.resolved`) and the live Go tree at `9ba444270`.

## D1. How to replace only the error alert

**Decision**: Subclass `SPUStandardUserDriver`, override `showUpdaterError(_:acknowledgement:)`; construct the updater via `SPUUpdater(hostBundle:applicationBundle:userDriver:delegate:)` (`SPUUpdater.h:62`) instead of `SPUStandardUpdaterController`.

**Rationale**:
- The stock alert lives in `SPUStandardUserDriver.m:584 showUpdaterError:` (NSAlert, "Update Error!", single "Cancel Update" button).
- Sparkle 2.9.3 offers **no** delegate hook to suppress/replace it: `SPUUpdaterDelegate.didAbortWithError` (h:455) is notification-only; `SPUStandardUserDriverDelegate` has only will/did-show notifications.
- `SPUStandardUpdaterController` hardwires `SPUStandardUserDriver` (`SPUStandardUpdaterController.h:69`), so direct `SPUUpdater` construction is required to inject the subclass.
- The method is a `SPUUserDriver` protocol requirement → dynamically dispatched → a subclass override is always hit.
- **Cleanup ordering** (Codex plan-review finding): the stock implementation closes Sparkle's checking/status windows before alerting; an override that blocks inside the call would leave them up. The override therefore stashes the error and calls `acknowledgement()` immediately; the custom dialog is presented at `didFinishUpdateCycleForUpdateCheck` — by which point the session is terminal, so Try Again usually starts immediately (queued-retry path kept as a guard for the other ordering).
- Verified safe to drop `SPUStandardUpdaterController`: FeedUpdater uses only `controller.updater` (no menu-action/validation reliance); the subclassed driver must be retained by FeedUpdater and initialized with `delegate: self`.

**Alternatives considered**:
- *Full custom `SPUUserDriver`*: rejected — replaces every piece of update UI (the existing code comment at `FeedUpdater.swift:126-132` already rules this out).
- *Follow-up dialog after the stock alert* (`didAbortWithError`): rejected — double-dialog UX.
- *Injecting `localizedRecoverySuggestion` into the error*: impossible — the same NSError instance flows internally; delegates can't mutate it.

## D2. Visibility semantics (FR-002)

**Finding**: Sparkle already implements the spec's matrix. `SPUUIBasedUpdateDriver.m:452 _abortUpdateWithError:showErrorToUser:` calls `showUpdaterError:` only when `showErrorToUser` is YES; `SPUUserInitiatedUpdateDriver.m:146` always passes YES; `SPUScheduledUpdateDriver.m:106` passes `_showedUpdate` (a one-way latch). **The subclass override therefore inherits FR-002 for free**; the tray does not re-derive visibility. Silent failures never reach the driver — recording must hook the updater delegate instead (D3).

## D3. Occurrence point & retry-ordering signal

**Decision**: `updater(_:didFinishUpdateCycleForUpdateCheck:error:)` (`SPUUpdaterDelegate.h:475`) is both the **occurrence point** (fires exactly once per session, error non-nil on failure, covers silent sessions) and the **terminal-completion signal** for FR-004 (Try Again queues until it has fired for the failed session).

**Rationale**: `didAbortWithError` can conflate stages and fires alongside other callbacks; the cycle-finished callback has exactly-once semantics per check and carries the `SPUUpdateCheck` kind. The download-provenance latch comes from `updater(_:failedToDownloadUpdate:error:)` (h:309), reset at session start (`willStartUpdateCheck`-equivalent seam in FeedUpdater).

## D4. Classification inputs (FR-007)

**Decision**: pure Swift function over `(downloadProvenanceLatch, error.domain, error.code, underlying chain domains/codes)`.

Sparkle error codes (verified in vendored `SUErrors.h`, phase-ranged): appcast — 1000 `SUAppcastParseError`, 1002 `SUAppcastError`, 1004 `SUResumeAppcastError`, plus feed-URL config errors 0003/0004; download — 2000 `SUTemporaryDirectoryError`, 2001 `SUDownloadError`; install — 3000-3002 (`SUUnarchivingError`, `SUSignatureError`, `SUValidationError`), 4000 `SUFileCopyFailure`, 4001 `SUAuthenticationFailure`, 4002-4006, 4009, 4010, 4012 (`SUMissingUpdateError` … `SUInstallationWriteNoPermissionError`); exclusions (not occurrences) — 1001 `SUNoUpdateError`, 4007 `SUInstallationCanceledError`, 4008 `SUInstallationAuthorizeLaterError`. Anything else (0001/0002/0005-0007 setup errors, 1003 running-from-DMG, 1006/1007 release-notes errors, 5000, unknown domains incl. bare `NSURLErrorDomain` without provenance) → `other`. Full table in data-model.md; the spec table is the normative contract.

## D5. Telemetry lane

**Decision**: 4 fixed codes in the existing diagnostics-counter machinery: `MCPX_UPDATE_APPCAST_FAILED`, `MCPX_UPDATE_DOWNLOAD_FAILED`, `MCPX_UPDATE_INSTALL_FAILED`, `MCPX_UPDATE_OTHER_FAILED` (new `UPDATE` domain in `internal/diagnostics/codes.go`).

**Rationale** (verified in source):
- `bboltDiagnosticsCounterStore.RecordErrorCode` (`internal/telemetry/diagnostics_counters.go:143`) enforces the MCPX_ **prefix only** (not the catalog), 24h decay windows, BBolt persistence, unique-codes-ever set — FR-012's persistence/window semantics with zero new machinery. Catalog membership (`diagnostics.Has`/`All()`) is a separate registration the four new codes must make.
- Heartbeat surfacing already covers the `error_code_counts_24h` map, but the anonymity scanner does **not** structurally inspect it today — FR-014 requires adding a rule (keys ∈ catalog, values non-negative ints).
- Existing runtime call-site pattern at `internal/runtime/runtime.go:2819`.

**Alternatives considered**:
- *Opt-out-beacon-style one-shot event*: rejected — needs ingest-side routing in the separate telemetry-worker repo; heartbeat lane lands data immediately.
- *New heartbeat field / schema v9*: rejected — larger blast radius (scanner rules, dash repo) for no added value at this granularity.
- *`error_category_counts` registry counter*: rejected — in-memory, reset-on-heartbeat lane; loses events on restart (spec requires persistence).

## D6. Endpoint & gate

**Decision**: `POST /api/v1/telemetry/update-failure`, body exactly `{"stage":"appcast|download|install|other"}`, strict JSON decode (`DisallowUnknownFields`), 204 on success and on inactive-telemetry no-op; 400 on invalid body/stage. Event-time gate (FR-013): telemetry active ⇔ `config.IsTelemetryEnabled()` (config.go:2603) AND NOT `telemetry.IsDisabledByEnv()` (env_overrides.go:26) AND version is valid semver (the daemon's existing dev-build rule, telemetry.go:641-645/isValidSemver). Exposed to httpapi through ONE atomic `ServerController` method — `RecordUpdateFailure(stage) (recorded bool, err)` — implemented as a forwarding method on `internal/server.Server` (the interface's actual implementor); gate evaluation and the synchronous BBolt increment happen behind the single call (no gate/record TOCTOU). Strict decode = `DisallowUnknownFields` + second-`Decode`-returns-`io.EOF` trailing-value check. Test doubles to update: `MockServerController` and the shared `baseController`.

**Auth**: inside the `/api/v1` middleware chain — socket callers (tray) admitted as AdminContext, TCP requires API key; same posture as `POST /api/v1/onboarding/mark`. Not strict-socket-gated: recording a closed-enum counter is not an administrative write.

## D7. Tray delivery

**Decision**: one new `APIClient` method using a **status-only** request helper (reads HTTP status, discards response data — `postAction`/`performRequest` parse non-2xx bodies, which FR-016 forbids relying on), invoked via an injected `UpdateFailureRecorder` seam wired at the composition root (UpdateService has no APIClient dependency today); detached task, exactly one attempt per occurrence; any failure → one `NSLog` line with stage + numeric status/error class only. Timeout is the APIClient session default (bounded).

## D8. URL precedence data sources (FR-005)

- Legacy brain: `UpdateService.checkWithGitHub()` resolves installer DMG **or any DMG or `html_url`** into one `downloadURL` field (`UpdateService.swift:329-346`) — NOT installer-only and no cycle identity. New capture `latestGitHubInstaller: (cycleID, version, installerURL)?` set only on a true `-installer.dmg` match feeds FR-005 candidate 1 (version-match rule; same-cycle rule for feed-stage failures).
- Feed brain: `SUAppcastItem.infoURL` (`SUAppcastItem.h:100`) — CI populates it via `generate_appcast --link https://github.com/<repo>/releases/tag/<tag>` (`release.yml:1651-1656`); captured at `didFindValidUpdate`.
- Constant: `https://github.com/smart-mcp-proxy/mcpproxy-go/releases` (final fallback, valid by construction).
- Candidate validation (absolute HTTPS) + fall-through live in the dialog presenter; `NSWorkspace.open` failure falls through.

## D9. Local live verification

Reuse the Sparkle local test rig (project memory `sparkle-local-test-rig`): local appcast offering a higher version whose enclosure URL points at an unreachable port → user-initiated check → new dialog must appear. Screenshots via `mcpproxy-ui-test screenshot_window`. Protocol recorded in `verification/manual-protocol.md`. Rig traps to respect: plist-only feed URL, `.dev` bundle id, stray entitlements, relaunch-drops-env.
