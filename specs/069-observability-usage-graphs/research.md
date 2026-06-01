# Phase 0 Research: Observability Usage Graphs

All claims below cite the current codebase (verified 2026-05-31). Where a finding contradicts the spec's stated assumption, it is marked **⚠ CORRECTION** and feeds a design decision.

## R1. ActivityRecord shape — ⚠ CORRECTION (byte sizes are NOT stored)

**Finding**: `ActivityRecord` (`internal/storage/activity_models.go:66-87`) has `Type`, `Source`, `ServerName`, `ToolName`, `Status`, `ErrorMessage`, `DurationMs`, `Timestamp`, `SessionID`, `RequestID`, plus `Response string` (truncated) and `ResponseTruncated bool`. There are **no** `RequestBytes`/`ResponseBytes` (or equivalent size) fields. The activity service truncates the response before storage (`activity_service.go:~231`, `storage/activity.go:35-43`).

**Spec assumption it contradicts**: Spec "Assumptions" and Clarification ("Bytes are recorded") state request/response byte sizes are already captured. They are not.

- **Decision**: Add `RequestBytes int` and `ResponseBytes int` to `ActivityRecord`, populated with the **full** byte length at the single write path (`ActivityService.handleEvent` → before truncation), so the size is accurate even when the stored `Response` string is truncated.
- **Rationale**: (a) FR-006 ("response bytes as token proxy") requires an accurate response size; deriving `len(record.Response)` undercounts truncated responses and gives no request size. (b) The change is additive and backward-compatible — legacy records decode `0`, treated as "size unknown" in aggregates/UI. (c) It is a 2-int capture at an existing write, not a parallel telemetry system (honors CN-001's intent).
- **Alternatives rejected**: derive `len(truncatedResponse)` (inaccurate, no request side); add a separate sizes bucket (more storage churn, no benefit over two ints on the record).
- **Board gate**: this is the one capture-path change and is the explicit Gate-1 question.

## R2. Activity service is already actor-based (good for CN-003)

**Finding**: `ActivityService` (`internal/runtime/activity_service.go:33-52`) owns a goroutine started by `Start(ctx, rt)`, consuming a buffered `eventCh chan Event` (100). Every event funnels through `handleEvent` (~line 181) which calls `storage.SaveActivity(record)` exactly once (~line 288).

- **Decision**: Own the incremental usage aggregate inside the `ActivityService` goroutine and update it in `handleEvent` right where the record is built. Readers (the HTTP handler) receive an immutable **copy-on-write snapshot** (atomic pointer swap), mirroring the stateview pattern used elsewhere in `internal/runtime/stateview`.
- **Rationale**: single-writer goroutine = no locks on the hot path (Constitution II); snapshot reads are O(1) and never block writes (CN-002).
- **Refinement (MCP-835)**: the copy-on-write clone is **publish-on-read**, not publish-on-write. `Apply` mutates the working aggregate under a short writer lock and only marks the snapshot stale (O(1), no clone); the O(tools×buckets) clone is deferred to the first `Snapshot()` after a write burst (the A3 endpoint / 30s persist flush). This keeps the activity write path O(1) per CN-002 — an earlier publish-on-write implementation cloned the whole aggregate on every write, violating it.

## R3. Aggregation fast-path + cold start

**Finding**: `storage.AggregateToolUsage(since time.Time)` (`internal/storage/activity.go:251-292`) does a single-pass scan returning `map[string]ToolUsageStat` (count + last_used), used by `GET /api/v1/tools` (spec 050). `activity_records` bucket is keyed `{nanosecond-ts}_{ULID}` enabling time-ordered iteration. `CountActivities` uses `bucket.Stats().KeyN`.

- **Decision**: Persist the aggregate snapshot periodically to a new `activity_stats` bucket. On cold start: load the persisted snapshot if present; otherwise run **one** `AggregateToolUsage`-style full scan to rebuild, then switch to incremental. Wide-window reads ("all") served from snapshot; a short-TTL cache (default 5s, configurable) absorbs bursts. **No full scan per request** (SC-005).
- **Rationale**: bounds cold-start cost to one scan; steady-state is O(1)/write and O(1)/read (FR-005, CN-002).
- **Open detail**: exact-percentile latency can't be maintained incrementally without storing all samples → use fixed latency buckets per (server,tool) for approximate p50/p95 (documented in data-model.md). Acceptable for "spot slow tools" (US2); exactness is not a requirement.

## R4. Endpoint surface — ⚠ CORRECTION (no /stats)

**Finding**: registered activity routes (`internal/httpapi/server.go:624-627`): `GET /api/v1/activity`, `/activity/summary` (`handleActivitySummary`, `activity.go:520-598`: counts + top-5 servers/tools, `period` ∈ 1h/24h/7d/30d), `/activity/export`, `/activity/{id}`. Filters parsed in `parseActivityFilters` (`activity.go:17-113`): type, server, tool, session_id, status, start/end_time, limit/offset, intent_type, request_id, sensitive_data, etc. **No `/activity/stats`.**

- **Decision**: Add a new `GET /api/v1/activity/usage` (window `24h|7d|all` + tool/server/status filters; top-N + "other" bucket). Do not overload `/summary` (different shape, existing consumers).
- **Rationale**: clean contract, reuses existing filter parsing; FR-008 filter parity for free.

## R5. Charting library — ⚠ CORRECTION (already installed)

**Finding**: `frontend/package.json` already depends on `chart.js ^4.5.0` + `vue-chartjs ^5.3.2`, used by `frontend/src/components/...TokenPieChart.vue`.

- **Decision**: Reuse chart.js + vue-chartjs for all four visualizations. No new dependency, no library-selection task.
- **Rationale**: removes a spec decision (and a supply-chain/bundle-size review); consistent with the existing pie chart.

## R6. Tokens-saved headline source (FR-007 / SC-008)

**Finding**: `ServerTokenMetrics` (`internal/contracts/types.go:256-263`: `SavedTokens`, `SavedTokensPercentage`, …) is embedded in `ServerStats.TokenMetrics` and returned by `GET /api/v1/servers`; already rendered by `TokenPieChart.vue`.

- **Decision**: The Usage view reuses this existing metric for the headline — no new backend work for FR-007. Optionally echo it in the `/usage` response for one-call convenience (decided in B2/A3).

## R7. Frontend integration points

**Finding**: `Dashboard.vue` renders overview cards + `TokenPieChart`; `Activity.vue` has KPI stat cards with `data-test="kpi-card-*"`, filters, and calls `api.listActivities/getActivitySummary/getActivityDetail` via the `frontend/src/services/api.ts` singleton; state via `useSystemStore()` (Pinia). No tab/switcher exists yet.

- **Decision**: Add the Overview↔Usage switcher on `Dashboard.vue` (preserve Overview state on switch-back, SC-006); new `Usage.vue` + chart components under `frontend/src/components/usage/`; `api.getActivityUsage()`; `data-test` attributes on all new elements for Playwright (FR-011).

## Resolved unknowns

- Charting library: **resolved** (chart.js, already present) — no NEEDS CLARIFICATION remains.
- Byte source: **resolved** (capture two int fields) — pending board accept at Gate 1.
- Endpoint name/shape: **resolved** (`/activity/usage`).
- Percentile strategy: **resolved** (bucketed approximate).
