# Feature Specification: Eliminate Remaining `/api/v1/servers` Refetches in macOS Tray

**Feature Branch**: `048-tray-refetch-elimination`
**Created**: 2026-05-08
**Status**: Draft

**Input**: User description: "Need to delete remaining tray polls of /api/v1/servers — proper fix for the paths spec 047 left out of scope."

## Background

Spec 047 (PR #450, v0.29.4) eliminated the **burst-storm** refetch path: each `servers.changed` SSE event used to trigger a `GET /api/v1/servers` round trip from the macOS tray, and during upstream retry-storms the rate could exceed 10 fetches/second. The `servers.changed` handler now consumes the embedded payload directly. CPU dropped 92% on the tray-only steady state.

What remains is a smaller residual: the Swift tray has **five other call sites** that still hit `/api/v1/servers`. Live verification with the user's real config showed ~8 fetches in an 18-second window even with no SSE storms, traced to:

| # | File:line | Trigger | Cadence under load |
|---|---|---|---|
| 1 | `CoreProcessManager.swift:514-515` | `case "status":` SSE handler — refetches when `upstream_stats.connected_servers` changes between successive `status` events | Every 1-3 s during retry storms (one flap per status event) |
| 2 | `CoreProcessManager.swift:608` | `startPeriodicRefresh` — 30 s `Timer.scheduledTimer` calling `refreshState` (which calls `refreshServers`) | Every 30 s |
| 3 | `CoreProcessManager.swift:640` | `refreshSecurityStatus` — falls through to `apiClient.servers()` when the Docker health check returns false but configured servers exist | Every 30 s (called from `refreshState`) |
| 4 | `MCPProxyApp.swift:101-110` | 10 s `Timer.publish` that always calls `client.servers()` | Every 10 s |
| 5 | `MCPProxyApp.swift:172-174` | `menuWillOpen` — refetches each time the user clicks the tray icon | Per user click |

`appState.servers` is already kept fresh by spec 047's SSE handler. These five paths refetch *the same data* that's already in memory.

## Goals

1. Cut steady-state `/api/v1/servers` fetches from the macOS tray to **≤ 1 per minute** at idle (only a long-cadence safety-net refresh remains).
2. Tray UI reactivity (menu state, dashboard, badges) stays as snappy as today — within ≤ 50 ms of an SSE event.
3. No behavior change for the user; this is pure cost removal.
4. No changes to the core. Pure Swift-side refactor.

## Non-Goals

- Changing the core SSE contract (`servers.changed` payload, `status` event shape).
- Touching the Web UI (its handler is already covered by spec 047).
- Refactoring the unrelated `refreshActivity`, `refreshSessions`, `refreshTokenMetrics`, or `refreshSecurityStatus` Docker/diagnostics paths beyond the one specific `apiClient.servers()` fall-through.
- Changing `apiClient.servers()` itself or the underlying transport. The function stays available for the safety-net refresh and any future explicit-refresh UI.

## User-Visible Behavior

None visible. Tray menu, badge counts, and dashboard remain the same. Clicking the tray icon still opens a menu reflecting current state — but the data comes from in-memory `appState.servers` instead of a fresh fetch on every click.

## Architecture

### Site-by-site fix

**Site 1 — `case "status":` SSE handler (`CoreProcessManager.swift:500-522`)**
The handler currently calls `refreshServers()` when `connected_count` changes. With spec 047, `servers.changed` events already deliver the full server list whenever upstream state actually changes. The status event's `connected_count` is just a derived stat. Replace the `refreshServers()` call with: update `appState.totalServers` and `appState.totalTools` from the inline stats only. The `servers.changed` event will deliver the matching server-list update within ~50 ms (coalescer window).

**Site 2 — periodic 30 s refresh (`CoreProcessManager.swift:600-613`)**
Drop `refreshServers` from `refreshState`. Keep the other periodic refreshes (`refreshActivity`, `refreshSessions`, `refreshTokenMetrics`, `refreshSecurityStatus`) since they cover data SSE doesn't carry. Lower the cadence of the *server-list-only* safety-net to a separate 5-minute timer that fires `refreshServers` once — guards against a missed SSE event, doesn't add measurable cost.

**Site 3 — `refreshSecurityStatus` Docker fallthrough (`CoreProcessManager.swift:640`)**
Replace `try? await apiClient.servers()` with a synchronous read of `appState.servers` on the main actor. The check is "do we have any connected stdio servers?" — that's available in-memory.

**Site 4 — 10 s `MCPProxyApp.swift` timer (`:101-110`)**
Remove the timer entirely. Its purpose ("keep health/action data current") is exactly what `appState.servers` updated by SSE provides. Replace with a comment block explaining the SSE-driven model.

**Site 5 — `menuWillOpen` refetch (`MCPProxyApp.swift:168-181`)**
Remove the in-line `client.servers()` call. The menu rebuilds from `appState.servers` which SSE keeps current. The `rebuildMenu()` call below it stays.

### New: server-list safety-net timer

A single new long-cadence Timer in `MCPProxyApp.swift` calls `coreManager?.refreshServers()` once every 5 minutes (configurable via the existing config service if needed). This is purely a defense-in-depth measure: if an SSE event was dropped (which the buffer + coalescer already work hard to prevent), the worst-case staleness window is bounded.

## Error Handling

| Failure | Behavior |
|---|---|
| SSE stream drops | Existing reconnect logic in `CoreProcessManager` reconnects; on reconnect the initial `status` event delivers fresh stats. The 5-minute safety-net catches any gap. |
| `appState.servers` is empty (e.g., immediately after launch before first SSE event) | Pre-existing behavior unchanged; the existing initial fetch in `coreManager.connect` populates state once at startup. |
| Core publishes a notify-only `servers.changed` (older core, downgrade scenario) | Spec 047's existing fallback (`if !consumedFromPayload { await refreshServers() }`) still triggers a single targeted refetch. |

## Testing

Per CLAUDE.md TDD rule, every site change is preceded by a failing XCTest.

- **Site 1**: `testStatusEventDoesNotRefetchOnConnectedCountChange` — fake SSE client emits two `status` events with different `connected_count`; assert `apiClient.servers()` was not called.
- **Site 2**: `testPeriodicRefreshDoesNotCallServers` — fast-forward the periodic refresh tick; assert no `servers()` call.
- **Site 3**: `testRefreshSecurityStatusReadsAppStateNotAPI` — pre-populate `appState.servers` with a connected stdio server; force the Docker fallback path; assert no `servers()` call and `dockerAvailable=true`.
- **Site 4**: `testNoTimerDrivenServersFetch` — install a counter on `apiClient.servers()`; wait > 10 s; assert counter == 0.
- **Site 5**: `testMenuWillOpenDoesNotRefetch` — invoke `menuWillOpen` directly; assert no `servers()` call.
- **Safety-net**: `testSafetyNetTimerRefetchesAt5min` — fast-forward time by 5 minutes; assert exactly one `servers()` call.

### Live verification (per CLAUDE.md "Verifying Web UI changes" pattern)

Replicate the spec 047 verification harness:
1. Build the Go binary + Swift tray; swap into a clone of `/Applications/MCPProxy.app`.
2. Launch with the user's real 30-server config.
3. With the tray running and only the tray polling, count `/api/v1/servers` GETs in `~/Library/Logs/mcpproxy/http.log` over a 60-second window.

**Acceptance**: ≤ 1 GET per 60 seconds at idle. Ad-hoc clicks on the tray icon should not generate a GET. SSE-driven state changes (toggle a server) should not generate a GET.

## Out of Scope (Deferred)

- Replacing the 30 s `refreshActivity` periodic with an `activity` SSE-driven model — separate spec; needs a `last-N` history bootstrap.
- Replacing `refreshTokenMetrics` and `refreshSessions` periodics — same shape as activity.
- Investigating whether `refreshSecurityStatus` Docker check could be SSE-driven entirely (currently independent of server list).
- A user-visible "refresh now" button — out of scope for this PR; the SSE-driven model already feels instant.

## Risks & Mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| Removing the 10 s timer hides a stale-state bug we don't know about today | Low | Keep the 5-minute safety-net. Verify in live tests that SSE actually delivers all the state the UI cares about. |
| `menuWillOpen` removal causes the menu to render with stale data when the tray was just relaunched | Low | The existing initial fetch on `connect` populates state before SSE catches up. If observed in QA, add a "first-open-after-connect" exception. |
| `status` event handler skips the refetch but `connected_count` divergence persists | Medium | Live verification step explicitly toggles servers and asserts UI reflects within 50 ms. If divergence shows up, document the gap and consider keeping the refetch behind a 1 Hz rate limit. |

## Acceptance Criteria

- All five identified call sites stop refetching `/api/v1/servers` in their normal path.
- One new 5-minute safety-net timer exists in `MCPProxyApp.swift`.
- New XCTests pass; existing Swift tests stay green.
- Live verification artifact (`http.log` GET count over 60 s idle) committed under `specs/048-tray-refetch-elimination/verification/`.
- Spec, plan, tasks, research, contracts (none — no API change), quickstart all committed.

## Open Questions

None. Sites are well-identified and the fix shape is mechanical.
