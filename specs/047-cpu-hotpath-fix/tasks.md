# Tasks — CPU Hot-Path Fix

**Feature**: CPU Hot-Path Fix (A1 + B1 + B2)
**Branch**: `047-cpu-hotpath-fix`
**Generated from**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`contracts/sse-events.md`](./contracts/sse-events.md), [`quickstart.md`](./quickstart.md)

> **TDD note**: per `CLAUDE.md` "Test-Driven Progress", every implementation task is preceded by a failing-test task. Each phase below interleaves tests and code in that order.

## User Story Map

The single observable user story (S0) is *"MCPProxy core stays under 5% CPU at idle when the macOS tray is the only active client."* Inside the spec, it decomposes into five independent sub-stories:

- **US1** (P1) — `service.GetScanSummary` caches "no scans found" so untouched servers don't re-scan the bucket. *Highest CPU yield; ships value alone.*
- **US2** (P1) — `emitServersChanged` includes the server list and stats in the event payload. *Required before clients can stop refetching.*
- **US3** (P1) — `serversChangedCoalescer` collapses bursts. *Reduces event volume; complements US2.*
- **US4** (P2) — Swift tray consumes the embedded payload, falls back to refetch when absent. *Realises the perf win on macOS.*
- **US5** (P2) — Vue Web UI consumes the embedded payload, falls back to refetch when absent. *Realises the perf win in the browser.*

US1 alone delivers the core CPU win even before US2-5 ship. US2+US4+US5 together eliminate the trip-back-to-server entirely.

---

## Phase 1 — Setup

- [ ] T001 Confirm working tree clean except for the in-progress pprof endpoint at `internal/server/server.go` (already on branch); preserve those changes for the PR.

## Phase 2 — Foundational

(none — no blocking prerequisites; existing event bus, scanner cache, and Swift/Vue clients are already in place.)

---

## Phase 3 — US1: Cache the "no scans found" sentinel

**Goal**: A future call to `GetScanSummary(name)` for an unscanned server completes without touching BBolt.

**Independent test**: Unit test mocks the storage layer with a call counter; asserts that 10 consecutive `GetScanSummary("never-scanned")` invocations result in exactly 1 storage call.

- [ ] T002 [P] [US1] Add failing unit test `TestGetScanSummary_CachesNegativeResult` in `internal/security/scanner/service_test.go` that asserts a mock storage's call counter equals 1 after 10 consecutive `GetScanSummary("never-scanned")` calls.
- [ ] T003 [P] [US1] Add failing unit test `TestGetScanSummary_DoesNotCacheOnTransientError` in `internal/security/scanner/service_test.go` that asserts non-`errNoScans` errors do **not** populate the cache.
- [ ] T004 [P] [US1] Add failing unit test `TestGetScanSummary_OverwritesNilSentinelOnRealScan` in `internal/security/scanner/service_test.go` that asserts a real scan summary replaces the nil sentinel.
- [ ] T005 [US1] Introduce `var errNoScans = errors.New("no scan jobs found for server")` in `internal/security/scanner/service.go`; have `findLatestPassJobs` return `errNoScans` for the empty-bucket case (replacing the current `fmt.Errorf` constructions on lines that say "no scan jobs found for server").
- [ ] T006 [US1] In `internal/security/scanner/service.go` `GetScanSummary`, when `findLatestPassJobs` returns an error matching `errNoScans` via `errors.Is`, call `s.cacheScanSummary(serverName, nil)` before returning `nil`. Do not cache on any other error.
- [ ] T007 [US1] Run `go test -race ./internal/security/scanner/... -v` and confirm T002–T004 now pass.

## Phase 4 — US2: SSE `servers.changed` payload includes server list and stats

**Goal**: Subscribers of `/events` receive `servers.changed` events whose payload contains the full post-redaction server list and aggregate stats.

**Independent test**: Subscribe to a fake bus, trigger `emitServersChanged("test", nil)`, assert the published event's payload has non-nil `servers` and `stats` matching what `mgmt.ListServers` returned.

- [ ] T008 [P] [US2] Add failing unit test `TestEmitServersChanged_PayloadIncludesServers` in `internal/runtime/event_bus_test.go` that asserts a published event has a non-nil `payload["servers"]` slice and `payload["stats"]` value, both reflecting a fake `mgmt.ListServers` return.
- [ ] T009 [P] [US2] Add failing unit test `TestEmitServersChanged_FallsBackToNotifyOnlyWhenListServersFails` in the same file that asserts when `mgmt.ListServers` errors, the event still publishes with just `payload["reason"]` (no `servers` key) and a Warn log line is emitted.
- [ ] T010 [P] [US2] Add failing unit test `TestEmitServersChanged_RedactsSensitiveHeaders` in the same file that asserts header values matching the existing redaction predicate appear masked in `payload["servers"]`.
- [ ] T011 [US2] In `internal/runtime/event_bus.go` `emitServersChanged`, after merging `extra` and setting `reason`, call the management service's `ListServers` (use the existing `r.mgmt` or equivalent injection point on `*Runtime`). On success, set `payload["servers"] = redactServerHeaders(servers)` and `payload["stats"] = stats`. On error, log Warn and skip the keys.
- [ ] T012 [US2] If `redactServerHeaders` is currently package-private to `httpapi`, hoist a copy into `internal/runtime/redaction.go` (or expose via a small adapter) so the runtime can call it without circular import. Keep behavior identical to the HTTP handler.
- [ ] T013 [US2] Run `go test -race ./internal/runtime/... -v` and confirm T008–T010 pass.

## Phase 5 — US3: Coalesce `servers.changed` bursts

**Goal**: A storm of `emitServersChanged` calls within a 50 ms window publishes ≤ 1 event with the most recent payload.

**Independent test**: Fire 100 calls within 10 ms; assert the bus receives exactly 1 event in the next 50 ms whose `reason` matches the *last* call's reason.

- [ ] T014 [P] [US3] Add failing unit test `TestCoalescer_CollapsesBurstToSingleEvent` in `internal/runtime/coalescer_test.go` (new file) using a fake clock + `flushNow()` hook to drive the drainer deterministically.
- [ ] T015 [P] [US3] Add failing unit test `TestCoalescer_LastWriteWins` asserting the published event's `reason` equals the last submitted call's reason.
- [ ] T016 [P] [US3] Add failing unit test `TestCoalescer_FlushesOnShutdown` asserting a final event publishes when the runtime's stop is invoked while one is pending.
- [ ] T017 [P] [US3] Add failing unit test `TestCoalescer_NoStarvationOnSingleEvent` asserting a single submitted event publishes within ~1 interval period.
- [ ] T018 [US3] In `internal/runtime/event_bus.go`, add `serversChangedCoalescer` struct with `pending atomic.Pointer[Event]`, `wake chan struct{}`, `interval time.Duration`, plus methods `submit(*Event)`, `flushNow()` (test hook), and a private drainer goroutine that loops on `select { case <-time.After(interval): ...; case <-r.shutdown: ... }`.
- [ ] T019 [US3] In `internal/runtime/runtime.go`, instantiate the coalescer in `Runtime.New` (interval 50 ms), start the drainer in `Runtime.Start`, and stop it in `Runtime.Stop` (final flush, then exit).
- [ ] T020 [US3] In `emitServersChanged`, replace the direct `r.publishEvent` call with `r.coalescer.submit(newEvent(EventTypeServersChanged, payload))`.
- [ ] T021 [US3] Run `go test -race ./internal/runtime/... -v` and confirm T014–T017 pass.

## Phase 6 — US4: Swift tray consumes embedded payload (with refetch fallback)

**Goal**: When the Swift tray receives a `servers.changed` SSE event whose payload includes a `servers` array, it updates `appState.servers` directly. If the array is missing, it falls back to `refreshServers()`.

**Independent test**: XCTest using a stubbed `SSEClient` that emits a synthetic event; assert appState mutates and no `APIClient.listServers` call fires.

- [ ] T022 [P] [US4] Add failing XCTest in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift` (new file) named `testServersChangedWithPayloadUpdatesStateWithoutRefetch`.
- [ ] T023 [P] [US4] Add failing XCTest `testServersChangedWithoutPayloadFallsBackToRefetch` in the same file.
- [ ] T024 [US4] In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, modify the `case "servers.changed":` branch of `handleSSEEvent`. Decode the event's `payload` field; if `payload.servers` is present and non-null, decode into `[Server]` (existing struct from `API/Models.swift`), update `appState.servers` on the main actor, and skip the `refreshServers()` call. Otherwise, keep current behavior.
- [ ] T025 [US4] Update `native/macos/MCPProxy/MCPProxy/API/Models.swift` if needed to add a `ServersChangedPayload` decode struct with `servers: [Server]?` and `stats: ServerStats?` (both optional for forward compat).
- [ ] T026 [US4] Build the tray binary (see `quickstart.md`) and confirm it runs against the v0 core (notify-only path) and the v1 core (payload path).

## Phase 7 — US5: Web UI consumes embedded payload (with refetch fallback)

**Goal**: When the Web UI's SSE store receives a `servers.changed` event whose payload includes a `servers` array, it merges into the Pinia store directly. Missing array falls back to `fetch('/api/v1/servers')`.

**Independent test**: Vitest using a fake `EventSource` emitting a synthetic event; assert store updates and no `fetch` call to `/api/v1/servers` fires.

- [ ] T027 [P] [US5] Add failing Vitest in `frontend/src/__tests__/sse-handler.test.ts` (or the existing equivalent path) named `'servers.changed with payload updates store without refetch'`.
- [ ] T028 [P] [US5] Add failing Vitest `'servers.changed without payload falls back to fetch'` in the same file.
- [ ] T029 [US5] Locate the SSE handler (likely `frontend/src/composables/useEventStream.ts` or `frontend/src/stores/server.ts`); modify the `servers.changed` branch to decode `payload.servers` / `payload.stats`, write to the store directly, and skip the refetch when both are present.
- [ ] T030 [US5] Run `npm run test --prefix frontend` and confirm T027–T028 pass.

## Phase 8 — Polish & Verification

- [ ] T031 Run `go test -race ./internal/... -v` (full Go suite) and confirm green.
- [ ] T032 Run `./scripts/test-api-e2e.sh` and confirm green.
- [ ] T033 Run `./scripts/run-linter.sh` and confirm green.
- [ ] T034 Run `go vet ./...` and `gofmt -l .` (must produce no output).
- [ ] T035 Build the personal-edition binary (`make build`) and the server-edition (`make build-server`).
- [ ] T036 Capture verification pprof on the same MCPProxy.app + 30-server scenario per `quickstart.md`. Save artifacts to `specs/047-cpu-hotpath-fix/verification/cpu_post.pb.gz`, `cputime_delta.txt`, and `report.html`. Assert the thresholds in `spec.md` "Acceptance Criteria".
- [ ] T037 Run `mcpproxy-ui-test` MCP tools end-to-end: `screenshot_status_bar_menu`, `list_menu_items` (assert server names match config), trigger an enable/disable, capture screenshots before/after. Save under `specs/047-cpu-hotpath-fix/verification/tray-*.png`.
- [ ] T038 Run the Playwright Web UI sweep per CLAUDE.md "Verifying Web UI changes" pattern. Save the report HTML to `specs/047-cpu-hotpath-fix/verification/webui-report.html`.
- [ ] T039 Commit all of `internal/`, `native/macos/`, `frontend/`, `specs/047-cpu-hotpath-fix/` (including verification artifacts) plus the pprof endpoint changes already on the branch.
- [ ] T040 Push branch and `gh pr create` with a description summarising A1+B1+B2, observed CPU before/after, and links to the verification artifacts.
- [ ] T041 Watch CI; iterate on failures (lint / test / build) until the PR is green.

---

## Dependencies

```
Phase 1 (Setup)
    └─ Phase 3 (US1) ──┐
                       ├─→ Phase 8 (Polish)
       Phase 4 (US2) ──┤
       Phase 5 (US3) ──┤   (US3 depends on US2's emit path)
       Phase 6 (US4) ──┤   (US4 depends on US2 having shipped)
       Phase 7 (US5) ──┘   (US5 depends on US2 having shipped)
```

US1 is completely independent and can ship first. US2/US3 must be sequenced (US3 calls into US2's modified emit path). US4/US5 each depend on US2 being merged but are independent of each other.

## Parallelism

- T002, T003, T004 (test files) — same file, different test functions; serialise.
- T008, T009, T010 — same.
- T014, T015, T016, T017 — same.
- T022 + T023, T027 + T028 — same.
- All [P] markers within a phase indicate the test functions can be authored in parallel as long as they all land before the implementation tasks in that phase.

## MVP scope

**US1 alone** is the MVP. It cuts ~80% of the observed CPU and ships independently. US2-US5 lift the remaining ~20% by eliminating the round trip. We ship all five together in this PR per the user's preference, but the breakdown is preserved here so a partial revert is possible if needed.

## Format validation

- [x] Every task is a markdown checkbox.
- [x] Every task has a TaskID (T001–T041).
- [x] Story labels [US1]–[US5] applied to phases 3–7 only.
- [x] Setup/Foundational/Polish phases have no story label.
- [x] Every task references a concrete file path or command.
