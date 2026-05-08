# Research — Eliminate Tray Refetches

All decisions resolved during spec drafting on 2026-05-08. No `NEEDS CLARIFICATION` items remain.

## D1. What cadence for the safety-net `/api/v1/servers` refresh?

**Decision**: Single 5-minute Combine timer in `MCPProxyApp.swift` (replacing the current 10 s timer that this PR removes).

**Rationale**:
- SSE `servers.changed` events drive normal updates within ~50 ms (spec 047).
- The safety-net only matters if SSE delivers nothing for an extended period (network blip, runtime bug, missed event past the 256-event buffer).
- 5 minutes is well below typical user-attention spans and any UX-relevant staleness threshold; 1 minute would be fine performance-wise but adds 4× more idle GETs for no visible benefit.

**Alternatives considered**:
- 1 minute — too chatty for what's a defense-in-depth measure.
- 30 minutes — too long; if SSE silently broke we'd miss a half-hour of state.
- No timer — viable, but removes a safety net for free; one cheap fetch every 5 min is worth keeping.

## D2. How does `refreshSecurityStatus` get the "any connected stdio servers?" answer without `apiClient.servers()`?

**Decision**: Read `appState.servers` synchronously on the main actor inside `refreshSecurityStatus`. Use the same `contains(where: { $0.connected && $0.protocol == "stdio" })` predicate; just on `appState.servers` instead of a fresh fetch.

**Rationale**:
- `appState.servers` is a `[ServerStatus]` kept current by the SSE handler. The "connected stdio" predicate runs cleanly against it.
- Eliminates the slowest blocking I/O on the periodic refresh path.
- `MainActor.run` access is already used throughout `refreshSecurityStatus`; one more read fits naturally.

**Alternatives considered**:
- Cache the boolean separately — pointless duplication of state.
- Move the docker fallback into `appState` itself — wider refactor than warranted.

## D3. What about the `menuWillOpen` refetch — is the menu allowed to render with stale data?

**Decision**: Yes. Drop the inline `client.servers()` call. `appState.servers` is current within ~50 ms of the last upstream change (SSE coalescer window).

**Rationale**:
- A user clicking the tray icon to inspect status has, by definition, not been actively interacting with that server in the last 50 ms.
- The previous code already called `rebuildMenu()` synchronously *before* the async fetch result arrived — so the user always saw whatever was in `appState.servers` at click time anyway. The fetch only updated the menu *after* the click. Removing the fetch just removes the post-click silent refresh that the user never saw and shaves a network round trip per click.

**Alternatives considered**:
- Keep the refetch but add a 1 Hz rate limit — still costs a fetch per first-click in a window, with no measurable benefit.
- Show a "loading" badge while fetching — actively worse UX for state that's already current.

## D4. What about the `case "status":` handler — does dropping the refetch leave `connected_count` stale?

**Decision**: Drop the `refreshServers()` call in the `connected_count != oldConnected` branch. Update `appState.totalServers` and `appState.totalTools` from the inline stats only. The `servers.changed` event the supervisor publishes alongside any state transition will deliver the matching server-list update within ~50 ms.

**Rationale**:
- Every transition that changes `connected_count` already triggers a `servers.changed` event (verified in spec 047 measurements: 22 events fired during a single toggle).
- The `status` event's `connected_count` is *derived* from the same supervisor state that drives `servers.changed`, so they're co-emitted; the order is governed by the coalescer's drainer goroutine.
- If we ever observe divergence in QA (status event saying "9 connected" while the coalescer is still holding a `servers.changed`), we can add a 1-second deferred `refreshServers` as a safety. Defer that until measurement justifies it.

**Alternatives considered**:
- Keep the refetch but rate-limit to 1 / sec — clamps the storm but still adds idle traffic.
- Have the core stop emitting `status` events on connection-count change — too invasive for this PR.

## D5. Why a Combine timer for the safety-net instead of `Task.sleep` in a loop?

**Decision**: `Timer.publish(every: 300, on: .main, in: .common).autoconnect()` mirroring the existing pattern at `MCPProxyApp.swift:101`.

**Rationale**:
- Already-imported `Combine` machinery; identical lifecycle handling (`.store(in: &cancellables)`).
- Survives app sleep/wake cleanly.
- Test-friendly: tests can swap the timer for a manual fire.

**Alternatives considered**:
- Detached `Task` with `Task.sleep` — equivalent functionally but a different lifetime model than the rest of the file uses; consistency wins.

## D6. Backward compatibility — what about users on older mcpproxy cores?

**Decision**: Pre-existing fallback in spec 047 covers it. The `case "servers.changed":` handler in `CoreProcessManager.swift:524` already falls through to `refreshServers()` when the SSE payload lacks the `servers` field (older core, notify-only event). That fallback path stays untouched.

**Rationale**: The new tray binary remains bidirectionally compatible: with a v0.29.4+ core it consumes the embedded payload (no refetch); with an older core it falls back to refetch on every `servers.changed`. Both work.

## D7. What changes are explicitly **not** made?

- `apiClient.servers()` itself stays exactly as-is. Function still callable for the safety-net + the spec 047 fallback.
- `refreshActivity`, `refreshTokenMetrics`, `refreshSessions`, the Docker `dockerStatus()` call, and `diagnostics()` call inside `refreshSecurityStatus` are untouched.
- `appState.updateServers` itself untouched — the SSE handler already feeds it.
