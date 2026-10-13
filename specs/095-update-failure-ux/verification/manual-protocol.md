# Live-rig manual verification — spec 095 (executed 2026-08-13)

## Rig

- Tray bundle: `scripts/build-swift-app.sh v0.99.0 arm64` with `SPARKLE_PUBLIC_ED_KEY=<prod pubkey>` and `SPARKLE_FEED_URL=http://localhost:8123/appcast.xml`; semver-stamped core (`v0.99.0`, so the telemetry event-time gate is ACTIVE) embedded at `Contents/Resources/bin/mcpproxy`; rebadged `com.smartmcpproxy.mcpproxy.dev`; stray `Contents/MCPProxy.entitlements` removed; nested-first codesign with local Apple Development identity (`codesign --verify --strict` clean).
- Isolated instance: launched with `MCPPROXY_HOME=/tmp/mcpproxy-rig`; spawned core patched to `listen 127.0.0.1:18096` (default 8080 collides with the real instance → exit code 2 on first spawn — expected, patch the rig config).
- Feed: `python3 -m http.server 8123` serving `appcast-arm64.xml` (the tray arch-rewrites `appcast.xml`) offering **99.0.0** with enclosure `http://localhost:9/…` (unreachable port → deterministic download failure before any signature check; dummy `edSignature`).
- UI driven via `mcpproxy-ui-test` (menu clicks, keypresses, screenshots) + System Events for the non-default button.

## Steps & results

| # | Step | Expected | Result |
|---|---|---|---|
| 1 | Tray menu → "Check for Updates" | stock found-update window (FR-006) | ✅ `shots/01-found-update.png` |
| 2 | Return (Install Update) → download hits dead port | **new recovery dialog**, NOT stock "Update Error!" | ✅ `shots/02-failure-dialog.png` — "MCPProxy couldn't download the update." + OS detail + Try Again (default) / Download from Website / Cancel |
| 3 | Recording while dialog OPEN | `MCPX_UPDATE_DOWNLOAD_FAILED` +1 on the rig core immediately | ✅ after wiring fix (see finding below): POST logged same millisecond as the failure; payload preview incremented while dialog up (`shots/03-…`) |
| 4 | Try Again | exactly one new user-initiated session, stock UI | ✅ `shots/04-try-again-recheck.png`; log shows fresh `Sparkle check:` |
| 5 | Fail again → Download from Website (System Events click) | browser opens releases URL; **no retry starts**; dialog closes | ✅ browser (Comet) frontmost; `update session failed` count unchanged after click |
| 6 | Escape (Cancel) on dialog | closes, nothing else | ✅ (exercised in run 1) |
| 7 | Occurrence accounting | 3 failed sessions → count 3 | ✅ final payload: `{"MCPX_UPDATE_DOWNLOAD_FAILED":3}` |

## Finding fixed during verification

**Recorder starved by the modal dialog (FR-010 violation).** The original composition-root wiring resolved `appState.apiClient` via `await MainActor.run {…}` at occurrence time; the recovery dialog's modal session holds the main actor, so the recording POST was parked until the user dismissed the dialog (empirically: POST fired at dialog close, 2.5 min after the failure; would be lost on quit-with-dialog-open). Fixed in `MCPProxyApp.swift`: the current client is mirrored into an `OSAllocatedUnfairLock` box by a Combine sink on `appState.$apiClient`, and the recorder reads the box without touching the main actor; the nil-core path now logs the FR-010 single line. Re-verified live: recording lands while the dialog is open (step 3).

This failure mode is invisible to the unit suites (XCTest cannot hold a real modal session on the main actor while asserting off-actor progress) — the live rig is the regression net for it; keep step 3 in any future re-run.
