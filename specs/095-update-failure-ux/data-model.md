# Data Model — spec 095 (Phase 1)

## Stage enum (the only cross-boundary value)

```
Stage := "appcast" | "download" | "install" | "other"
```

Swift: `enum UpdateFailureStage: String, CaseIterable { case appcast, download, install, other }`
Go: validated string set in the handler (no new type needed).

## Classification function (Swift, pure)

```
classify(downloadProvenance: Bool,
         domain: String, code: Int,
         underlying: [(domain: String, code: Int)]) -> Stage?   // nil = not an occurrence (excluded)
```

Precedence (first match wins), per spec FR-007/FR-008 and vendored `SUErrors.h`:

| # | Condition | Result |
|---|---|---|
| 1 | `SUSparkleErrorDomain` code ∈ {1001 noUpdate, 4007 installationCanceled, 4008 authorizeLater} (top-level or first Sparkle code found) | `nil` (excluded) |
| 2 | download-provenance latch set (`failedToDownloadUpdate` fired this session) | `download` |
| 3 | `SUSparkleErrorDomain` code ∈ {1000, 1002, 1004} ∪ {0003, 0004} | `appcast` |
| 4 | `SUSparkleErrorDomain` code ∈ {2000, 2001} | `download` |
| 5 | `SUSparkleErrorDomain` code ∈ {3000, 3001, 3002} ∪ {4000, 4001} ∪ {4002…4006, 4009, 4010, 4012} | `install` |
| 6 | top-level domain ≠ Sparkle but underlying chain contains a Sparkle code → recurse on that code (rules 1,3-5) | (as mapped) |
| 7 | anything else | `other` |

## Session-scoped tray state (ephemeral, in `FeedUpdater`)

| Field | Type | Lifecycle |
|---|---|---|
| `downloadProvenance` | Bool | set by `failedToDownloadUpdate`; reset when a new check starts |
| `lastOfferedItem` | `(version: String, infoURL: URL?)?` | captured at `didFindValidUpdate`; reset at new check |
| `stashedError` | NSError? | set by `showUpdaterError` override (which acknowledges immediately so Sparkle tears down its windows); consumed at cycle end to present the custom dialog |
| `sessionFinished` | Bool | set by `didFinishUpdateCycleForUpdateCheck`; when the dialog shows the session is already terminal, so Try Again usually starts immediately |
| `queuedRetry` | Bool | belt-and-braces guard for the not-yet-terminal ordering; consumed exactly once; dropped on quit |

Download cancellation: `userDidCancelDownload` fires and the cycle ends with a **nil** error (Sparkle 2.9.3 has no download-canceled error code) — nil error ⇒ no occurrence.

## Check-cycle state (ephemeral, in `UpdateService` — the single owner)

`UpdateService.runCheck()` triggers both brains in one cycle and owns cycle identity: a monotonically increasing `checkCycleID: Int` incremented at each `runCheck()`.

| Field | Type | Lifecycle |
|---|---|---|
| `checkCycleID` | Int | incremented at each `runCheck()` |
| `latestGitHubInstaller` | `(cycleID: Int, version: String, installerURL: URL)?` | set by `checkWithGitHub()` ONLY when a true `-installer.dmg` asset matched (the existing `downloadURL` field is NOT installer-only and has no cycle identity), stamped with the initiating `checkCycleID` |

Boundary: FeedUpdater reports occurrences upward as `(stage, offeredVersion: String?, feedInfoURL: URL?)` via the observer; **UpdateService assembles the FR-005 candidate list at occurrence time** — installer candidate eligible iff `installer.version == offeredVersion`, or `offeredVersion == nil && installer.cycleID == checkCycleID` (same cycle). FeedUpdater never sees GitHub state.

## Wire contract

`POST /api/v1/telemetry/update-failure`

Request (strict; unknown fields rejected):
```json
{"stage": "download"}
```

Responses: `204` (recorded durably, or telemetry-inactive no-op — indistinguishable by design) · `400` (invalid JSON / unknown or trailing fields / stage outside enum) · `401` (TCP without API key, existing middleware) · `500` (persistence failure — durability promise of 204 must hold).

## Persisted representation (existing machinery, no schema change)

Stage → code mapping (new `UPDATE` domain in `internal/diagnostics/codes.go`):

| Stage | Code |
|---|---|
| appcast | `MCPX_UPDATE_APPCAST_FAILED` |
| download | `MCPX_UPDATE_DOWNLOAD_FAILED` |
| install | `MCPX_UPDATE_INSTALL_FAILED` |
| other | `MCPX_UPDATE_OTHER_FAILED` |

Stored via `bboltDiagnosticsCounterStore.RecordErrorCode` (24h decay window, `diagnostics_counters` bucket, unique-codes-ever set) and surfaced in the heartbeat's existing `diagnostics.error_code_counts_24h` map — occurrence counts, absent when zero (existing semantics).

## URL candidate list (dialog)

Ordered candidates, each `(url: URL, source)`; first absolute-HTTPS candidate whose browser-open succeeds wins:

1. `legacyInstallerURL` — from GitHub-releases check; eligible iff its version == failed session's offered version, OR session has no offered version (appcast-stage failure) and the URL came from the same check cycle.
2. `feedInfoURL` — `SUAppcastItem.infoURL` of the offered update.
3. `fallbackReleasesURL` — constant `https://github.com/smart-mcp-proxy/mcpproxy-go/releases` (always valid).
