# Tasks: Observability — Usage Statistics & Graphs in the Web UI

**Input**: Design documents from `/specs/069-observability-usage-graphs/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/usage-endpoint.md

**Tests**: Included and required (Constitution V TDD + FR-011). Write the failing test first for every backend sub-task.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency)
- Lanes: **(BE)** Backend/Go — Backend-engineer lane (`internal/`, `cmd/`, `oas/`); **(FE)** Frontend/Vue — delegated to the Frontend engineer (`frontend/src/`).

## Path Conventions

Web app: Go backend under `internal/`; embedded Vue frontend under `frontend/src/`.

---

## Phase 1: Foundational — byte capture (Stream A1, BE) ⚠ GATE-1 decision

**Purpose**: Give the token-sink graphs a real byte source (research.md R1). Blocks all aggregate work.

- [ ] T001 [BE] Failing test in `internal/storage/activity_test.go`: `ActivityRecord` round-trips new `RequestBytes`/`ResponseBytes`; legacy records (no fields) decode to `0`.
- [ ] T002 [BE] Add `RequestBytes int` (`request_bytes`) + `ResponseBytes int` (`response_bytes`) to `ActivityRecord` in `internal/storage/activity_models.go`.
- [ ] T003 [BE] Failing test in `internal/runtime/activity_service_test.go`: byte sizes captured **pre-truncation** (truncated `Response` still reports full `ResponseBytes`; request args measured).
- [ ] T004 [BE] Populate `RequestBytes`/`ResponseBytes` in `ActivityService.handleEvent` before truncation; make T001/T003 green.

---

## Phase 2: Usage aggregate (Stream A2, BE) — US1/US2/US3 core

**Purpose**: Actor-owned incremental aggregate + snapshot + persistence + cold-start + TTL (CN-002/CN-003/FR-005).

- [x] T005 [BE] Failing unit tests in `internal/runtime/usage_aggregate_test.go`: incremental update math (calls, errors, blocked, byte sums excluding 0-byte, latency-bucket → approx p50/p95, time buckets per window).
- [x] T006 [BE] Implement `UsageAggregate`/`ToolUsage`/`TimeBucket` + incremental `Apply(record)` in `internal/runtime/usage_aggregate.go` (data-model.md §2); copy-on-write snapshot via atomic pointer.
- [x] T007 [BE] Wire aggregate into `ActivityService`: own it on the goroutine, `Apply` inside `handleEvent`; expose `UsageSnapshot()` returning the immutable snapshot. Test: snapshot reflects writes, reads never block.
- [x] T008 [BE] Failing test for persistence/rebuild in `internal/storage/activity_stats_test.go`: persist snapshot to `activity_stats` bucket (versioned key) + load; cold start with no snapshot triggers exactly one full-scan rebuild.
- [x] T009 [BE] Implement `internal/storage/activity_stats.go` persist/load; periodic flush (default 30s) + flush-on-shutdown; cold-start load-or-rebuild (reuse `AggregateToolUsage` scan). Make T008 green.
- [x] T010 [BE] [P] Add `observability.usage_cache_ttl` (5s) + `usage_persist_interval` (30s) to `internal/config/config.go` with defaults + hot-reload; test defaults.

---

## Phase 3: Usage endpoint (Stream A3, BE) — serves US1–US4

**Purpose**: `GET /api/v1/activity/usage` reading the snapshot/TTL cache (contracts/usage-endpoint.md).

- [x] T011 [BE] Failing API test in `internal/httpapi/activity_usage_test.go`: ranking by `sort`, `error_rate` math, avg excludes 0-byte calls, `window` filter (24h/7d/all), tool/server/status filters (FR-008), top-N + `other` fold, empty-state 200 (FR-009), 400 on bad enum.
- [x] T012 [BE] Add `UsageAggregateResponse` + sub-structs to `internal/contracts/types.go`.
- [x] T013 [BE] Implement `handleActivityUsage` in `internal/httpapi/activity.go`; short-TTL read cache (`usage_cache_ttl`) for wide windows; echo `tokens_saved` from `ServerTokenMetrics`. Register `GET /api/v1/activity/usage` in `internal/httpapi/server.go`. (Deviation: a dedicated `parseUsageParams` validates the usage-specific `window`/`sort`/`top` enums and returns 400 on bad input — `parseActivityFilters` silently ignores bad input; the shared `server`/`tool`/`status` param names are kept consistent.)
- [x] T014 [BE] [P] Document endpoint in `oas/swagger.yaml` (swag-generated from handler annotations); `./scripts/verify-oas-coverage.sh` passes in CI (note: local macOS BSD-sed lacks the `\U` GNU extension the script relies on, so the coverage % is wrong locally; the route + schemas are present and the documented count increments).
- [x] T015 [BE] Perf assertion (SC-005): `TestActivityUsage_NoFullScanPerRequest` proves `handleActivityUsage` never calls the full-scan path (`AggregateToolUsage`) per request.

---

## Phase 4: Frontend — switcher + charts (Streams B1/B2, FE — DELEGATED)

**Purpose**: Dashboard switcher + four chart.js visualizations + tokens-saved headline. Depends on T013 (endpoint/contract). **Owned by the Frontend/Vue engineer via a child issue.**

- [X] T016 [FE] [US4] Add Overview↔Usage switcher to `frontend/src/views/Dashboard.vue`, preserving Overview state on switch-back (SC-006); window selector (24h/7d/all); `data-test` attrs.
- [X] T017 [FE] Add `getActivityUsage()` to `frontend/src/services/api.ts`.
- [ ] T018 [FE] [US1] `frontend/src/components/usage/CallHistogram.vue` + `ResponseSizeRanking.vue` (token-sink, labeled size-based per FR-006) using vue-chartjs.
- [ ] T019 [FE] [US2] `frontend/src/components/usage/ErrorRateChart.vue` + per-tool latency (p50/p95).
- [ ] T020 [FE] [US3] `frontend/src/components/usage/Timeline.vue` honoring active filters (FR-008).
- [ ] T021 [FE] [US5] Tokens-saved headline in `Usage.vue` from existing `ServerTokenMetrics` (FR-007); empty/low-data states (FR-009).
- [ ] T022 [FE] Compose `frontend/src/views/Usage.vue`; async-load graphs so Dashboard first paint is not blocked (SC-004); `make build`.
- [ ] T023 [FE] Playwright sweep (CLAUDE.md Web-UI workflow) → switcher, window, four charts, empty-state; HTML report in `specs/069-observability-usage-graphs/verification/` (kept local, not committed).

---

## Phase 5: Polish & cross-cutting

- [x] T024 [BE][P] `./scripts/test-api-e2e.sh` green end-to-end with the new endpoint (65/65 passed).
- [x] T025 [BE][P] Run full `internal/runtime` suite `-race` (approval-hash canary safety) — all packages green.
- [x] T026 [P] Spec trace + swagger committed; conventional commits, no Claude attribution.

---

## Dependencies & ordering

- **A1 (T001–T004) → A2 (T005–T010) → A3 (T011–T015)** — strict backend order.
- **A3 (T013) → B1/B2 (T016–T023)** — frontend needs the endpoint/contract.
- FR-010 (per-call token estimation) is an explicit **phase-2 follow-on**, not in this task set.

## Lane / delegation note

T001–T015 + T024–T026 are Backend-engineer tasks (my lane). T016–T023 are Frontend/Vue tasks delegated via a child issue blocked by A3. This split is the Gate-1 decomposition; child issues are created **only after board acceptance**.
