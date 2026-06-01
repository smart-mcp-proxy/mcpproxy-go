# Implementation Plan: Observability — Usage Statistics & Graphs in the Web UI

**Branch**: `069-observability-usage-graphs` | **Date**: 2026-05-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/069-observability-usage-graphs/spec.md`

## Summary

Surface the existing activity log as fast, glanceable usage graphs in the Web UI: a per-tool call histogram, a response-size "token-sink" ranking, per-tool error rate + latency, and a call-volume timeline, behind a Dashboard switcher (Overview ↔ Usage) with a time-window selector (24h / 7d / all) and a "tokens saved by mcpproxy" headline.

The backend serves these from an **actor-owned, incrementally-maintained usage aggregate** updated on each activity write (O(1) per write), snapshot copy-on-write for readers, periodically persisted, with a short-TTL cache for wide windows — never an on-demand full scan of the log (CN-002/CN-003/FR-005). The frontend reuses the **already-installed chart.js + vue-chartjs**.

> **Codebase verification changed three spec assumptions** (see [research.md](./research.md)). These are the gating design decisions for the board:
> 1. **`ActivityRecord` does NOT store request/response byte sizes today** — it stores a truncated `Response` string + `ResponseTruncated` bool (`internal/storage/activity_models.go:66-87`). FR-006's "response bytes as token proxy" needs a real byte source. **Decision: capture `RequestBytes`/`ResponseBytes` (full, pre-truncation) at the single write path** rather than deriving `len(truncatedResponse)` (which undercounts and lacks request bytes). Additive + backward-compatible (legacy records report 0 → rendered "unknown").
> 2. **chart.js + vue-chartjs are ALREADY dependencies** (`frontend/package.json`, used by `TokenPieChart.vue`). The spec's "select a charting library" decision is moot — we reuse chart.js. One fewer work-stream.
> 3. **There is no `GET /api/v1/activity/stats`** — `GET /api/v1/activity/summary` exists (counts + top-5 servers/tools, period param). We add a new `GET /api/v1/activity/usage` rather than overload `summary`.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10); TypeScript 6.0 / Vue 3.5 (frontend)
**Primary Dependencies**: backend — `go.etcd.io/bbolt` (existing), `go.uber.org/zap`, Chi router, existing `ActivityService` actor; frontend — Vue 3.5, Pinia 3, Tailwind 4 / DaisyUI 5, **chart.js ^4.5 + vue-chartjs ^5.3 (already present)**, Vite 8
**Storage**: BBolt (`~/.mcpproxy/config.db`) — read-only reuse of `activity_records` bucket; **one new `activity_stats` bucket** for the periodically-persisted aggregate snapshot (cold-start fast path). Additive ActivityRecord fields (`RequestBytes`, `ResponseBytes`).
**Testing**: `go test ./internal/... -race` (unit + API), `./scripts/test-api-e2e.sh`, Playwright Web-UI sweep + HTML report (FR-011)
**Target Platform**: macOS/Linux/Windows desktop (embedded Web UI)
**Project Type**: web (Go backend + embedded Vue frontend)
**Performance Goals**: usage endpoint returns from the in-memory snapshot / TTL cache, no full-log scan per request (SC-005); does not block dashboard first paint (graphs load async — SC-004); aggregate update O(1) per activity write (CN-002)
**Constraints**: all-local, no new external calls (CN-004); actor-owned aggregation, no new locks on the hot path (Constitution II / CN-003); stated freshness bound for the TTL cache (FR-005); CLAUDE.md edits constrained by the 40k-char CI gate (put notes in specs/)
**Scale/Scope**: activity logs up to ~10^5–10^6 records; per-(server,tool) cardinality up to ~1k tools (Constitution I) → top-N + "other" bucket for charts (edge case)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Incremental O(1) aggregate + snapshot reads + TTL cache; no full-scan-per-request. Cold-start single rebuild only. |
| II. Actor-Based Concurrency | ✅ PASS | Aggregate is owned by the existing `ActivityService` goroutine, updated inside `handleEvent` on the single `SaveActivity` write path; readers get a copy-on-write snapshot. No new shared-memory locks on the hot path. |
| III. Configuration-Driven | ✅ PASS | Freshness/TTL + persistence interval exposed as config with sensible defaults; no tray-local state (REST only). |
| IV. Security by Default | ✅ PASS | Aggregates expose only counts/sizes/latency the user already owns; sensitive-data flags are NOT surfaced in aggregates (CN-004). No new network listeners. |
| V. TDD | ✅ PASS | Failing tests first for byte capture, aggregate math, endpoint contract; Playwright for UI. |
| VI. Documentation Hygiene | ✅ PASS | Spec/plan/tasks trace + swagger update for the new endpoint; CLAUDE.md kept under 40k. |

**No violations** → Complexity Tracking section omitted.

## Project Structure

### Documentation (this feature)

```text
specs/069-observability-usage-graphs/
├── plan.md              # This file
├── research.md          # Phase 0 — assumption corrections + design decisions
├── data-model.md        # Phase 1 — aggregate / time-bucket / endpoint shapes
├── quickstart.md        # Phase 1 — how to run + verify
├── contracts/
│   └── usage-endpoint.md # Phase 1 — GET /api/v1/activity/usage contract
├── checklists/
│   └── requirements.md   # (existing)
└── tasks.md             # Phase 2 — /speckit.tasks output
```

### Source Code (repository root)

```text
internal/
├── storage/
│   ├── activity_models.go   # +RequestBytes, +ResponseBytes (additive)
│   ├── activity.go          # AggregateToolUsage already exists (cold-start basis)
│   └── activity_stats.go    # NEW: persist/load aggregate snapshot (activity_stats bucket)
├── runtime/
│   ├── activity_service.go  # capture bytes at write; own + update incremental aggregate
│   └── usage_aggregate.go   # NEW: actor-owned aggregate type, snapshot, TTL cache, rebuild
├── httpapi/
│   ├── activity.go          # NEW handler: handleActivityUsage (+ filters/window)
│   └── server.go            # register GET /api/v1/activity/usage
└── contracts/
    └── types.go             # NEW: UsageAggregateResponse + sub-structs

frontend/src/
├── views/Dashboard.vue      # add Overview ↔ Usage switcher (preserve Overview state)
├── views/Usage.vue          # NEW: usage view, window selector, 4 charts + tokens-saved headline
├── components/usage/*.vue    # NEW: chart components (chart.js/vue-chartjs)
└── services/api.ts          # add getActivityUsage()

oas/swagger.yaml             # document GET /api/v1/activity/usage
e2e/ or specs/069-.../verification/  # Playwright sweep + HTML report (FR-011)
```

**Structure Decision**: Web application (Go backend + embedded Vue frontend). Backend changes are confined to `internal/` (Backend-engineer lane). Frontend changes (`frontend/src/`) are the **Frontend/Vue engineer's lane** and are delegated via a child issue (see Decomposition below).

## Decomposition / Plan-of-Attack (Gate 1)

Cross-lane work split into dependency-ordered streams. **Backend streams (A) are my lane; the Frontend stream (B) is delegated to the Frontend/Vue engineer.**

| ID | Stream | Lane | Depends on | Deliverable |
|----|--------|------|-----------|-------------|
| **A1** | Byte capture | Backend | — | Add `RequestBytes`/`ResponseBytes` to `ActivityRecord`, populate (full, pre-truncation) at the `SaveActivity` write path; failing tests first. |
| **A2** | Usage aggregate | Backend | A1 | Actor-owned incremental aggregate in `ActivityService` (per-(server,tool) counts/byte sums/error counts/latency buckets + time buckets); copy-on-write snapshot; periodic persist to `activity_stats`; cold-start rebuild; short-TTL cache for wide windows. |
| **A3** | Usage endpoint | Backend | A2 | `GET /api/v1/activity/usage` (window 24h/7d/all + tool/server/status filters, top-N + "other"); contract + API tests + swagger. |
| **B1** | Dashboard switcher | Frontend | A3 | Overview ↔ Usage switcher on Dashboard, preserve Overview state; window selector. |
| **B2** | Usage charts | Frontend | A3 | `Usage.vue` + 4 chart.js visualizations (call histogram, response-size ranking, error rate, timeline) + tokens-saved headline (from existing `ServerTokenMetrics`); Playwright verification + HTML report. |

Order: **A1 → A2 → A3**, then **A3 unblocks B1 + B2** (parallel). FR-010 (per-call token estimation) stays a tracked phase-2 follow-on, not in this decomposition.

**Open question for the board** (decided at this gate): Approve capturing two new `int` byte fields on `ActivityRecord` at the write path (Decision 1 above), vs. deriving sizes from the truncated `Response` string. Recommendation: capture fields (accurate, request-side coverage, additive/backward-compatible). This is the only spec assumption that requires a capture-path change; everything else is pure read-side aggregation.

## Complexity Tracking

> No constitution violations — section intentionally omitted.
