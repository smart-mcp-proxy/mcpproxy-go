# Feature Specification: CPU Hot-Path Fix — Scanner Cache + SSE Payload Embedding

**Feature Branch**: `047-cpu-hotpath-fix`
**Created**: 2026-05-08
**Status**: Draft

**Input**: User description: "fix list scan jobs / options how to fix every-3-seconds tray polls of /api/v1/servers / cache vs long polling vs listen for updates only"

## Background

The MCPProxy core process showed sustained ~28% CPU on macOS in production. Pprof profiling against a representative scenario (MCPProxy.app tray + 30 configured servers, 1.06 GB BBolt database, 91,537 activity records) revealed:

- A 60-second CPU profile recorded **28.44 s of CPU samples** = 47% sustained CPU.
- **56% of CPU time** was spent inside `bbolt.(*DB).View` calls.
- **83% of that** was attributable to `BoltDB.ListScanJobs(serverName)`.
- **>50% of flat CPU** was JSON parser internals (`encoding/json.checkValid`, `stateInString`, `unquoteBytes`, `rescanLiteral`).

Two mechanisms drive the load:

**A. `BoltDB.ListScanJobs` unmarshals every record before filtering.**
At `internal/storage/scanner.go:103`, `ListScanJobs(serverName)` iterates the entire `security_scan_jobs` bucket and calls `record.UnmarshalBinary(v)` on each record *before* checking whether the record's `ServerName` matches the requested `serverName`. With a bucket grown over months of normal use, this is an O(N) JSON-decode operation on every call.

**B. Tray re-fetches `/api/v1/servers` on every `servers.changed` SSE event.**
The macOS tray (`native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift:524`) handles `servers.changed` SSE events by calling `refreshServers()`, which performs a `GET /api/v1/servers`. The handler at `internal/httpapi/server.go:1014` enriches every one of the user's servers via `securityController.GetScanSummary(name)` — and the existing summary cache in `internal/security/scanner/service.go:1417` does *not* cache the "no scans found" result, so untouched servers re-trigger the full bucket scan from (A) every time.

When upstreams retry-storm during reconnection, each state transition publishes a `servers.changed` event, the tray re-fetches, and the handler runs N full bucket scans (where N is the configured server count). At 30 servers and a fast retry storm, this exceeds 10 full bucket scans per second.

This spec covers a **hotfix PR** that targets the root cause with the smallest possible blast radius:

- **A1**: cache the negative result in scanner summary (3 lines).
- **B1**: embed the server list in the `servers.changed` SSE event payload, so subscribers don't need to re-fetch.
- **B2**: coalesce bursts of `servers.changed` events in the runtime event bus.

A separate follow-up spec will cover the durable redesigns: in-memory `(serverName, pass) → latestJobID` index, activity log retention/pagination, optional bucket re-keying. Those are explicitly **out of scope** for this PR.

## Goals

1. Reduce sustained CPU on the steady-state hot path from ~19% (tray-only, idle) to <2% on the same scenario.
2. Eliminate `bbolt.(*DB).View` from the top-10 cumulative-CPU functions in a 60-second profile of the same scenario.
3. Eliminate the per-event re-fetch round trip for the macOS tray and the Web UI.
4. Preserve full backward compatibility with existing API consumers (CLI, agents, scripts that call `GET /api/v1/servers`).
5. No BBolt schema migration. No on-disk format change.

## Non-Goals

- Re-keying or restructuring the `security_scan_jobs` BBolt bucket (deferred).
- Building an in-memory scan-job index (deferred).
- Activity log retention, pagination, or compaction (deferred).
- Replacing `encoding/json` with a faster decoder on the hot paths (deferred).
- Changing the `/api/v1/servers` endpoint shape or removing the per-server `SecurityScan` enrichment.
- Changing the tray's polling cadence on any other endpoint (only `servers.changed`-driven refetch is in scope).

## User-Visible Behavior

For the user, the only observable change is that the tray, the Web UI, and the dashboard feel snappier and the core process no longer pegs a CPU core. There are no new screens, settings, or commands. The fix is invisible except via Activity Monitor / `top`.

## Architecture

### Component map

| Component | File | Change |
|---|---|---|
| Scanner summary cache | `internal/security/scanner/service.go` | Cache "no scans found" sentinel so untouched servers stop re-triggering the bucket scan. |
| Scanner storage | `internal/storage/scanner.go` | Unchanged in this PR (the inefficient ListScanJobs stays — the cache makes its hot-path call rate go to ~0). |
| Runtime event bus | `internal/runtime/event_bus.go`, `internal/runtime/runtime.go` | Embed `servers` and `stats` in `servers.changed` payload. Add coalescer. |
| HTTP SSE writer | `internal/httpapi/server.go` (`handleSSEEvents`) | Unchanged — it already forwards arbitrary event payloads. |
| Swift tray SSE handler | `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift` | Decode `servers` directly from the event payload; skip the `refreshServers()` call. Fall back to refetch if the payload is missing (older core). |
| Web UI SSE handler | `frontend/src/` (the relevant store/composable) | Same shape change as the tray. |

The change set is concentrated in three files plus their tests. The Swift and Vue clients each grow one decode-from-payload branch with a fallback path.

### A1 — Cache-the-miss in scanner summary

`GetScanSummary(serverName)` at `service.go:1417` already checks `summaryCache[serverName]` and returns the cached value if present. The check uses the `(value, ok)` form, so distinguishing "not in map" from "in map with nil value" is already correct.

The only change: when `findLatestPassJobs(serverName)` returns the no-scans-found error, call `cacheScanSummary(serverName, nil)` before returning `nil`. Subsequent calls hit the map, see `ok == true`, and return `nil` without touching BBolt.

To avoid catching unrelated errors (e.g. transient BBolt I/O failure) in the negative cache, `findLatestPassJobs` returns a sentinel `errNoScans` for the no-rows case. `GetScanSummary` only caches the miss when `errors.Is(err, errNoScans)`. Other errors propagate through unchanged and re-trigger on the next call.

Existing invalidation via `cacheScanSummary(name, summary)` and `delete(summaryCache, name)` already handles the case where a real scan eventually runs for that server — the new sentinel is overwritten on the first real result.

### B1 — Embed server list in `servers.changed` payload

Today the payload is roughly `{"reason": "..."}` and the SSE event carries a few dozen bytes. After this change:

```json
{
  "reason": "server-state-change",
  "servers": [ { /* contracts.Server */ }, ... ],
  "stats":   { /* contracts.ServerStats */ }
}
```

The shape of `servers` and `stats` matches what `handleGetServers` returns today — same struct types, same redaction (sensitive header values are redacted in the publisher just as they are in the HTTP handler). Subscribers can consume the payload directly.

Producer side: `runtime.emitServersChanged(reason, extra)` already lives at `event_bus.go:56` and accepts an `extra map[string]any` that gets merged into the event payload. The function gains an internal call to the management service: it calls `mgmt.ListServers(ctx)` once per invocation, redacts headers, and merges the result into `payload["servers"]` and `payload["stats"]` *before* it builds the event. With A1 in place, that call is cheap. Existing call sites (`lifecycle.go`'s seven `emitServersChanged(...)` calls) need no change — they continue to pass their own reason + extra and pick up the server list automatically.

If `mgmt.ListServers` returns an error, the event is logged and dropped. Subscribers stay in their last-known state; the next state change retries the publish. This is benign: SSE subscribers already have to tolerate dropped events (network hiccups, slow clients, etc.).

### B2 — Coalesce `servers.changed` bursts

The event bus already buffers 256 events per subscriber to absorb retry storms (see `defaultEventBuffer` in `event_bus.go`). With B1, each event carries a full server list, so deduplication becomes a clear win.

Implementation: a `serversChangedCoalescer` struct on the runtime, holding `pending atomic.Pointer[Event]` and a single drainer goroutine. `emitServersChanged(reason, extra)` builds the latest event (including the server-list payload) and stores it in `pending`, then signals the drainer; the drainer wakes at most once per ~50 ms, atomically swaps out `pending`, and calls the existing `publishEvent` path. If multiple `emitServersChanged` calls happen within the window, only the most recent payload is sent.

The coalescer drains pending state on shutdown (single final flush in the runtime stop path) so subscribers see the last event before disconnect.

50 ms is below the threshold of human-perceptible UI latency on a tray menu / dashboard refresh, but enough to coalesce the dozens of events that fire during a server retry storm into one.

### Data flow before/after

**Before** (per `servers.changed` event):
1. State change in supervisor → publish `{reason}` event.
2. Tray receives event → `await refreshServers()`.
3. `GET /api/v1/servers` → `handleGetServers`.
4. For each of N servers: `GetScanSummary(name)` → cache miss → `findLatestPassJobs(name)` → `BoltDB.ListScanJobs(name)` → full bucket iterate + JSON decode.
5. Marshal response, send.

**After**:
1. State change in supervisor → coalescer captures latest reason.
2. Drainer wakes (≤ 50 ms later) → calls `mgmt.ListServers(ctx)` once → builds payload → publishes `{reason, servers, stats}` event.
3. Tray receives event → reads `servers` directly from payload → updates `appState`. No HTTP round trip.
4. `GET /api/v1/servers` still works for any pull-based caller (CLI, agents, scripts).

## Error Handling

| Failure | Behavior |
|---|---|
| `findLatestPassJobs` returns `errNoScans` | Cache `nil` summary. Future `GetScanSummary` calls for the same server hit the cache. |
| `findLatestPassJobs` returns any other error | Do **not** cache. Propagate the error. Next call retries. |
| `mgmt.ListServers` errors during event production | Log at `Warn`. Drop the event. Subscribers retain their previous state. Next state change re-attempts. |
| Coalescer is dropped on shutdown | Single final flush at runtime stop. Subscribers see the last event before disconnect. |
| Older core still publishes notify-only `servers.changed` | Tray and Web UI fall back to their existing `refreshServers()` path when the payload doesn't include `servers`. |
| Subscriber consumes a payload from a future core that adds new fields | Decoding ignores unknown fields (Go and Swift defaults). No error. |

## Testing

Per CLAUDE.md autonomous-operation rules, every sub-task is preceded by a failing test.

**A1 — service_test.go**
- Test: `GetScanSummary` for a server with no scan history calls storage **once** across N consecutive invocations. Mock storage is a fake with a call counter; assert counter == 1 after N=10 calls. Fails today (counter == 10), passes with the fix.
- Test: when `findLatestPassJobs` returns a non-sentinel error, the cache is not populated. Counter == 10 after N=10 calls.
- Test: when `cacheScanSummary` is later called with a real summary, a subsequent `GetScanSummary` returns the real summary, not the nil sentinel.

**B1 — event_bus_test.go**
- Test: `emitServersChanged` produces an event whose payload, after JSON round-trip, contains `servers`, `stats`, and `reason`. The `servers` slice has the expected length and matches the management service's view.
- Test: when `mgmt.ListServers` is configured to error, no event is published, and the error is logged.
- Integration test: subscribe to `/events` via a test HTTP client, trigger a state change in a fake supervisor, parse the SSE event, assert the server list is non-empty.

**B2 — coalescer_test.go**
- Test: 100 calls to `emitServersChanged` within 10 ms produce at most 1 published event in the next 50 ms window.
- Test: the published event reflects the *last* reason / payload (last-write-wins).
- Test: a single call still publishes within ~50 ms (no starvation when there is no burst).
- Test: shutdown drains a pending event before exiting.

**Swift tray — XCTest**
- Test: SSE handler decodes a `servers.changed` event with embedded `servers`, updates `appState.servers` directly, does **not** call `refreshServers()`.
- Test: SSE handler with a payload missing the `servers` field falls back to `refreshServers()`.

**Verification (per CLAUDE.md "Verifying Web UI changes" pattern)**

Pprof on the same scenario as the original report (MCPProxy.app + 30 servers, the user's existing 1.06 GB BBolt). Capture a 60-second CPU profile. Assert in `specs/047-cpu-hotpath-fix/verification/`:

- `bbolt.(*DB).View` cum < 5% (was 56%).
- `encoding/json.checkValid` flat < 5% (was 13%).
- Cumulative-cputime delta over a 60-second wall window shows < 2 s of CPU consumed (was ~19 s = ~30%) when the tray is the only active client.

Commit the post-fix profile (`cpu.pb.gz`, `top` output, screenshots of Activity Monitor) alongside the PR per the same trace-with-the-spec convention used for spec 046.

## Out of Scope (Deferred to Follow-Up Specs)

- **In-memory `(serverName, pass) → latestJobID` index** — true O(1) lookup, eliminates the cold-cache spike.
- **Activity log retention/pagination** — 91,537 records is excessive for an interactive log; needs both a default retention window and cursor-based pagination at the BBolt layer.
- **`security_scan_jobs` bucket re-keying** as `<serverName>|<jobID>` — clean root-cause fix; needs a one-time on-disk migration.
- **`encoding/json` replacement** (goccy/go-json or sonic) on storage hot paths — drop-in 2-3× speedup, but defer until we have a bench.
- **PGO build** — Datadog reports ~14% sustained CPU savings on similar Go daemons. Capture a representative production profile, save as `cmd/mcpproxy/default.pgo`, build with `go build -pgo=auto`. Refresh per release.

## Risks & Mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| The coalescer hides a real "every-tick-matters" event consumer. | Low | Audit existing `servers.changed` subscribers (Swift, Vue, internal supervisor.go event forwarder). All are state-overwrites, none are increment-counters. |
| Embedded payload grows large enough to slow down the SSE writer. | Low | 30 servers × ~1 KB = ~30 KB per event; SSE on loopback writes this in a single TCP segment. If a real install grows to 300+ servers, revisit. |
| The negative cache hides a new scan that should have populated a result. | Low | The `cacheScanSummary` overwrite path on real scan completion already invalidates the sentinel. Test asserts this behavior. |
| Older core releases publish notify-only events; new tray expects payload. | Medium | Tray includes a fallback to `refreshServers()` when payload lacks the `servers` field. Verified via XCTest. |
| Older tray releases call `refreshServers()` on every event despite payload being present. | High (this is the existing case). | Already the case today — performance regresses to "just A1" for those clients, which is still a >80% improvement. |

## Acceptance Criteria

- A1, B1, B2 all merged in one PR.
- Pprof verification artifacts (`cpu.pb.gz` + summary screenshots) committed to `specs/047-cpu-hotpath-fix/verification/`.
- Post-fix `bbolt.(*DB).View` cum-CPU is < 5%.
- All new tests pass; existing test suite still passes; `go test -tags server ./...` is green.
- Swift tray + Vue Web UI updated to consume payload, with a manual smoke test in MCPProxy.app showing the dashboard updates within 50 ms of a server state change without any HTTP refetch.
- README / docs updated only if a user-visible knob changes (none is expected).

## Open Questions

None. All scope decisions were resolved during brainstorming on 2026-05-08:
- Both A and B in scope.
- Hotfix PR first; durable redesigns split into a follow-up spec.
- SSE event embeds the full server list payload (chosen over delta-only or notify-only).
- A1 + B1 + B2 all included in this PR.
