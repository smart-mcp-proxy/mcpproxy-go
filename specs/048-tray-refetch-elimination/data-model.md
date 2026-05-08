# Data Model — Tray Refetch Elimination

No persistent storage changes. No new `appState` fields. No new types. Pure refactor of existing methods.

## Existing types touched

| Type | File | Change |
|---|---|---|
| `CoreProcessManager` | `Core/CoreProcessManager.swift` | Modify `handleSSEEvent` (case `"status"`), `refreshState`, `refreshSecurityStatus`. Add nothing. |
| `AppDelegate` (or whatever holds the Combine cancellables in `MCPProxyApp.swift`) | `MCPProxyApp.swift` | Drop the 10 s server-refresh timer. Drop the `menuWillOpen` server fetch. Add a 5 min safety-net timer. |
| `AppState` | `State/AppState.swift` | No change. `appState.servers` is already the canonical state and is fed by SSE. |

## New: safety-net timer

Lives on the same `cancellables: Set<AnyCancellable>` set already used by the app-level coordinator (`MCPProxyApp.swift`):

```swift
// MCPProxyApp.swift — replaces the existing 10 s timer block
//
// Spec 048: SSE-driven appState.servers is authoritative. We keep one
// long-cadence safety-net refresh in case SSE drops events past the
// 256-event buffer or the runtime momentarily forgets to emit.
Timer.publish(every: 300, on: .main, in: .common)   // 5 minutes
    .autoconnect()
    .sink { [weak self] _ in
        guard let self, let core = self.coreManager else { return }
        Task { await core.refreshServersForSafetyNet() }
    }
    .store(in: &cancellables)
```

`refreshServersForSafetyNet()` is a tiny new method on `CoreProcessManager` that does exactly what the existing private `refreshServers()` does — separate name purely so future readers don't confuse the safety-net path with the on-demand path. Internally it just calls `refreshServers()`.

## Removed lifecycle hooks

- `MCPProxyApp.swift:101-110` — the 10 s `Timer.publish` that called `client.servers()`. Replaced by the safety-net above.
- `MCPProxyApp.swift:172-178` — the inline `client.servers()` fetch inside `menuWillOpen`. The function reduces to `rebuildMenu()`.

## Modified lifecycle hooks

- `CoreProcessManager.swift:514-515` — `case "status":` no longer calls `refreshServers()`. The branch becomes a stat-update only.
- `CoreProcessManager.swift:617` — `refreshState()` drops the `await refreshServers()` call. The other periodic refreshes stay.
- `CoreProcessManager.swift:640` — `refreshSecurityStatus()` reads `appState.servers` instead of fetching.
