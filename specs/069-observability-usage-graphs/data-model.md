# Phase 1 Data Model: Observability Usage Graphs

## 1. ActivityRecord (additive change)

`internal/storage/activity_models.go` — add two fields:

| Field | Type | JSON | Notes |
|-------|------|------|-------|
| `RequestBytes` | `int` | `request_bytes` | Full serialized request arg size, captured pre-truncation at the write path. Legacy records → `0` ("unknown"). |
| `ResponseBytes` | `int` | `response_bytes` | Full response payload size, captured **before** `Response` truncation. Legacy records → `0`. |

Backward-compatible: BBolt JSON decode of old records yields `0`; aggregates and UI render `0`-byte records as "size unknown" and exclude them from byte averages.

## 2. UsageAggregate (in-memory, actor-owned)

Owned by `ActivityService`; updated in `handleEvent`; exposed to readers as an immutable snapshot via atomic pointer swap (copy-on-write).

```
UsageAggregate
  Tools     map[string]*ToolUsage   // key = "<server>:<tool>"
  Buckets   []TimeBucket            // ring of fixed-width time buckets (timeline)
  UpdatedAt time.Time               // freshness stamp (drives TTL/freshness bound)

ToolUsage
  Server, Tool   string
  Calls          int64
  Errors         int64              // Status == "error"
  Blocked        int64              // Status == "blocked"
  ReqBytesSum    int64              // excludes RequestBytes==0
  RespBytesSum   int64              // excludes ResponseBytes==0
  SizedCalls     int64              // # calls with known byte size (for averages)
  LatencyBuckets [N]int64           // fixed DurationMs buckets → approx p50/p95
  LastUsed       time.Time

TimeBucket
  Start    time.Time               // bucket-aligned (e.g. hourly for 7d, minute for 24h)
  Calls    int64
  Errors   int64
  RespBytesSum int64
```

**Derivations**: `ErrorRate = Errors / Calls`; `AvgRespBytes = RespBytesSum / SizedCalls` (guard div-by-zero → null); `p50/p95` interpolated from `LatencyBuckets`.

**Time windows**: bucket width chosen per window — 24h → minute/5-min buckets; 7d → hourly; all → daily. Window selection trims the ring to the requested span server-side.

**High cardinality (edge case)**: endpoint returns top-N tools by the requested sort key + a synthesized `"other"` aggregate folding the tail, so charts stay readable (spec Edge Cases).

**A2 implementation notes** (as built — informs A3):
- **Time buckets are hourly** (`usageBucketWidth = 1h`), matching the contract example (`start: …T11:00:00Z`). One fixed-width ring bounded to `24*90` buckets (~90d, the default activity retention); oldest evicted first. A3 selects/trims the requested window span over these hourly buckets. (Minute/5-min granularity for 24h from the §2 table above is deferred — hourly bars render cleanly and keep the aggregate bounded.)
- **Sized averages** track **separate** counts: `SizedRespCalls` (records with `ResponseBytes>0`) and `SizedReqCalls` (`RequestBytes>0`), so `avg_resp_bytes`/`avg_req_bytes` each exclude their own 0-byte (legacy) records exactly. The contract's `sized_calls` maps to `SizedRespCalls` (the token-sink metric). `AvgRespBytes()`/`AvgReqBytes()` return `ok=false` when their sized count is 0.
- **Latency percentiles** use a fixed histogram (`latencyBucketBoundsMs = {10,25,50,100,250,500,1000,2500,5000,10000}` + overflow). `ToolUsage.Percentile(p)` returns the upper bound of the bucket holding the p-th call (approximate; bounded memory).
- **Persistence is byte-oriented** in storage (`SaveUsageSnapshot`/`LoadUsageSnapshot` + `ScanAllActivities`) to avoid a storage→runtime import cycle; the runtime owns JSON encode/decode and the load-or-rebuild orchestration.

## 3. Persisted snapshot (`activity_stats` bucket)

New BBolt bucket `activity_stats`, single key (e.g. `usage_aggregate_v1`) → gob/JSON-encoded `UsageAggregate`. Written on a periodic flush (default 30s, configurable) and on graceful shutdown. On cold start: load if present and recent; else one full `AggregateToolUsage`-style scan to rebuild. Schema-versioned key so a future shape change forces a clean rebuild rather than mis-decoding.

## 4. Config (additive, sensible defaults — Constitution III)

```
"observability": {
  "usage_cache_ttl": "5s",       // wide-window read cache / freshness bound (FR-005)
  "usage_persist_interval": "30s" // snapshot flush cadence
}
```

Both optional with defaults; env override allowed; hot-reloadable like other config.

## 5. API response (see contracts/usage-endpoint.md)

`UsageAggregateResponse` in `internal/contracts/types.go`: `window`, `generated_at`, `freshness` (cache age), `token_source: "bytes"` (FR-006 label), `tools[]` (per-tool rollup incl. error rate + p50/p95 + avg/total bytes), `timeline[]` (time buckets), `tokens_saved` (echoed `ServerTokenMetrics.SavedTokens`), and an `other` bucket when truncated.
