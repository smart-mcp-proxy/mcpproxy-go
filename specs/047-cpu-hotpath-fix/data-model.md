# Data Model — CPU Hot-Path Fix

No persistent storage changes. This document captures the in-memory shapes introduced or modified.

## 1. `serversChangedCoalescer` (new)

Lives on the `*Runtime` struct in `internal/runtime/runtime.go`. Owned by a single drainer goroutine started in `Runtime.Start`.

```go
// internal/runtime/event_bus.go
type serversChangedCoalescer struct {
    pending atomic.Pointer[Event]    // latest event waiting to be published
    wake    chan struct{}            // signal drainer that pending was updated
    interval time.Duration            // 50 * time.Millisecond
}
```

**State transitions**:

```
no event pending  --produce-->  pending set
pending set       --produce-->  pending overwritten (last-write-wins)
pending set       --drainer ticks-->  swap to nil, publish to bus
shutdown          --drainer ticks one final time--> publish residual, exit
```

**Invariants**:
- At most one publish per `interval` window per coalescer.
- The published event is always the most recently produced one.
- On shutdown, residual `pending` is flushed before the drainer exits.

## 2. `servers.changed` event payload (modified)

`Event.Payload` is `map[string]any`. Existing producer at `internal/runtime/event_bus.go:62`:

```go
// Before
payload := map[string]any{}
// ... merge `extra` ...
payload["reason"] = reason
r.publishEvent(newEvent(EventTypeServersChanged, payload))
```

After this change:

```go
// After
payload := map[string]any{}
// ... merge `extra` ...
payload["reason"] = reason
if r.mgmt != nil {
    if servers, stats, err := r.mgmt.ListServers(ctx); err == nil {
        redacted := redactServerHeaders(servers)   // see contracts/sse-events.md
        payload["servers"] = redacted
        payload["stats"]   = stats
    } else {
        r.logger.Warn("emitServersChanged: ListServers failed; emitting notify-only", zap.Error(err))
    }
}
r.coalescer.submit(newEvent(EventTypeServersChanged, payload))
```

If `ListServers` errors, the event still publishes with just the `reason` field — older clients (and resilient new clients with fallbacks) handle this exactly as today.

## 3. Scanner summary cache (modified semantics)

`internal/security/scanner/service.go`:

```go
// Existing field (unchanged)
summaryCache   map[string]*ScanSummary  // nil value now means "we checked, no scans found"
summaryCacheMu sync.RWMutex
```

**Lookup contract**:

| Map state | `(value, ok)` from `m[k]` | Meaning | Behavior |
|---|---|---|---|
| key absent | `(nil, false)` | never checked | full BBolt scan |
| key present, value nil | `(nil, true)` | checked, no scans | return nil immediately (NEW) |
| key present, value non-nil | `(*Summary, true)` | checked, scan found | return the summary |

**Invalidation**: `cacheScanSummary(name, summary)` overwrites the entry. `delete(summaryCache, name)` removes it (existing test path). Both operate identically on the new nil-sentinel entries.

## 4. Sentinel error: `errNoScans` (new)

`internal/security/scanner/service.go`:

```go
var errNoScans = errors.New("no scan jobs found for server")
```

Returned by `findLatestPassJobs(serverName)` when the bucket has zero matching records. Caller distinguishes via `errors.Is(err, errNoScans)`.

## 5. SwiftUI `AppState` (no schema change, behavior change)

`native/macos/MCPProxy/MCPProxy/State/AppState.swift` — the `servers` field is already there. The change is purely behavioral: SSE handler writes to it directly when the event payload includes a server list, instead of triggering a fresh `GET /api/v1/servers`.

## 6. Vue Pinia store (no schema change, behavior change)

`frontend/src/stores/server.ts` (or wherever `servers` is held) — same: field exists, behavior changes to consume from event payload when present.
