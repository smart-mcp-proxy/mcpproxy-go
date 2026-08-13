# Baseline verification — spec 095

**Date**: 2026-08-13, branch `095-update-failure-ux` at `9ba444270` (origin/main).

## Go

`go test ./internal/httpapi/... ./internal/telemetry/... ./internal/diagnostics/...` — **all ok** (httpapi 0.9s, telemetry 7.2s, diagnostics 0.2s).

## Swift

`cd native/macos/MCPProxy && swift test` — **939 tests, 1 failure (0 unexpected)**:

- `AppLifecycleTests.testTheSharedJournalNeverWritesToTheRealInstanceRootUnderTests` — pre-existing environmental failure on this machine (a real `~/…/tray-lifecycle.jsonl` exists outside the test sandbox; documented in project memory). Not caused by and not to be fixed by spec 095.

All other suites pass, including UpdateServiceFeedTests, UpdateMenuStateTests, SparkleFeedURLTests.
