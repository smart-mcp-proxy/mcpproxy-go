# Research — CPU Hot-Path Fix

All decisions resolved during the 2026-05-08 brainstorming session. No `NEEDS CLARIFICATION` items remain.

## D1. How to fix `BoltDB.ListScanJobs` re-scanning the entire bucket on every call?

**Decision**: A1 — cache the "no scans found" sentinel in `service.GetScanSummary` for this PR. Defer in-memory `(serverName, pass) → latestJobID` index and bucket re-keying to a follow-up spec.

**Rationale**:
- A1 is a 3-line change with zero blast radius. Eliminates ≥80% of the observed CPU load on the steady-state hot path.
- The other in-scope improvements (B1 + B2) make first-call cost less painful too — the producer-side `mgmt.ListServers` will warm caches once per coalesced event, not once per subscriber per fetch.
- Cleaner solutions (rekey bucket as `<serverName>|<jobID>`; build an in-memory index) require a one-time migration or extra storage layer surface — appropriate for a separate PR with its own risk envelope.

**Alternatives considered**:
- Re-key the BBolt bucket so `cursor.Seek("<server>|")` scopes the iteration. Rejected for this PR: requires migration code, larger storage-layer surface change.
- Build a `(serverName, pass) → latestJobID` map in memory at startup. Rejected for this PR: still pays the startup full-bucket scan; better as a follow-up paired with retention work.
- Drop scan-summary enrichment from `/api/v1/servers` entirely and require a separate `/api/v1/servers/{id}/scan-summary` call. Rejected: changes the public response shape.

## D2. How to reduce repeated `GET /api/v1/servers` round trips from SSE subscribers?

**Decision**: B1 — embed the server list and stats inside the `servers.changed` SSE event payload.

**Rationale**:
- The Swift tray and Web UI already subscribe to `servers.changed` and react by calling `refreshServers()`. Replacing the round trip with a payload read collapses two operations into one.
- Producer cost is amortized: one `mgmt.ListServers` per coalesced event window vs. one per subscriber per fetch.
- Backward compatible: older clients that ignore unknown payload fields keep working (they refetch as before); only the new clients benefit.

**Alternatives considered**:
- Send a per-server delta (`{server_id, new_state}`) only. Rejected: forces clients to reconcile state, adds merge logic in two languages.
- ETag / If-None-Match on `/api/v1/servers`. Rejected: still pays the full handler cost server-side; the body is the only thing saved.
- Long polling. Rejected: SSE already provides push semantics; long polling would be a regression.

## D3. How to handle retry-storm bursts of `servers.changed`?

**Decision**: B2 — coalesce via a single `pending atomic.Pointer[Event]` + 50 ms drainer goroutine, last-write-wins.

**Rationale**:
- Existing buffer (`defaultEventBuffer = 256`) was sized up specifically because retry storms flooded the bus. Coalescing addresses the root cause instead of the symptom.
- 50 ms is below the threshold of human-perceptible UI latency on a tray menu / dashboard, but enough to coalesce many state transitions during a reconnect storm into one event.
- All existing subscribers are state-overwrites (Swift tray, Web UI store, supervisor event forwarder). None are increment-counters or "every event matters" consumers, so collapsing is safe.

**Alternatives considered**:
- Time-windowed batching with a slice of recent events. Rejected: clients only need the latest snapshot.
- Token-bucket rate limit. Rejected: introduces a knob, doesn't capture the "latest snapshot" intent as cleanly.
- Per-subscriber dedup. Rejected: subscriber-local state to maintain, more code.

## D4. What is the safe payload size for the `servers.changed` event?

**Decision**: Acceptable up to ~50 KB per event. Loopback SSE writes this in a single TCP segment; no observable latency impact.

**Rationale**:
- 30 servers × ~1 KB each = ~30 KB observed in measurement.
- For the rare install with 100+ servers, payload reaches ~100 KB, still cheap on loopback.
- If a real install grows beyond 300 servers, revisit; until then there is no benefit to a delta encoding.

## D5. What is the cache-invalidation contract for the new nil sentinel?

**Decision**: Existing `cacheScanSummary(name, summary)` overwrite path handles it. The first real scan that runs for a server overwrites the nil sentinel with a real summary. Other invalidation paths (test-only `delete(summaryCache, name)`) also work unchanged.

**Rationale**: The cache is a `map[string]*ScanSummary`. `nil` value with `ok==true` is distinguishable from "not in map" via the existing `(value, ok) := m[k]` pattern that `GetScanSummary` already uses.

## D6. How does the change interact with the existing `redactServerHeaders` step?

**Decision**: The producer-side payload build (inside `emitServersChanged`) calls `redactServerHeaders` on the slice before merging into the payload, identically to the HTTP handler at `internal/httpapi/server.go:1064`.

**Rationale**: SSE goes to the same trust boundary as the HTTP API. Redaction policy must be identical. Centralizing the redaction call in the producer ensures all SSE subscribers (browser-based Web UI, Swift tray, future REST clients tailing `/events`) receive the same redaction.

## D7. What's the testing strategy for the coalescer's timing behavior?

**Decision**: Use a `time.Now`-injectable clock (or a synchronous flush hook) so the test doesn't have to actually `time.Sleep(50 * time.Millisecond)`.

**Rationale**: Real timer-based assertions are flaky on CI. The simplest pattern is to expose a non-public `flushNow()` method that the test calls directly; the production drainer goroutine calls `flushNow()` on its 50 ms ticker. Same code path, deterministic test.

## D8. Backward-compat: how do older Swift / Vue clients handle the new payload?

**Decision**: Both Swift (`JSONDecoder`) and Vue (`JSON.parse`) ignore unknown fields by default. Older clients see the existing `{reason}` key and ignore the new `servers`/`stats` keys. They keep refetching as today.

**Rationale**: This is the standard backward-compat pattern for SSE / JSON event streams. New clients gain the perf win; old clients keep working.

## D9. Should `GET /api/v1/servers` itself be improved?

**Decision**: No — out of scope for this PR. The handler stays exactly as today.

**Rationale**: The hotfix targets the trigger pattern, not the endpoint. Once tray and Web UI consume the embedded payload, the endpoint's call rate drops to "occasional" (CLI/agents/scripts/manual refresh). Optimizing it further is deferred until measurement justifies it.
