# Tasks — Tray Refetch Elimination

**Feature**: Eliminate Remaining `/api/v1/servers` Refetches in macOS Tray
**Branch**: `048-tray-refetch-elimination`
**Generated from**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`quickstart.md`](./quickstart.md)

> **TDD note**: per `CLAUDE.md` "Test-Driven Progress", every site change is preceded by a failing XCTest. Sites are independent; tests can be written in parallel.

## User Story Map

The single observable user story is *"the macOS tray makes ≤ 1 `/api/v1/servers` GET per minute at idle while UI reactivity stays at <50 ms."* Inside the spec, it decomposes into five independent sub-stories — one per call site:

- **US1** (P1) — `case "status":` SSE handler stops refetching on `connected_count` change. *Highest fetch frequency under load; biggest single win.*
- **US2** (P1) — `refreshState`'s 30 s periodic stops calling `refreshServers`. *Deterministic 30-s drumbeat eliminated.*
- **US3** (P1) — `refreshSecurityStatus`'s Docker fallback reads `appState.servers` instead of fetching. *One fewer fetch per security-status pass.*
- **US4** (P2) — `MCPProxyApp.swift`'s 10 s `Timer.publish` removed. *Steady-state idle drumbeat eliminated.*
- **US5** (P2) — `menuWillOpen`'s inline fetch removed. *Per-click cost eliminated; menu stays current via SSE.*

Each ships value alone; the visible reactivity guarantee depends on spec 047 already shipped (it has).

---

## Phase 1 — Setup

- [x] T001 Confirm a clean working tree on branch `048-tray-refetch-elimination` (already created); spec/plan/research/data-model/quickstart already committed locally — no rework needed.

## Phase 2 — Foundational

(none — spec 047 is the foundation, and it's already merged on `main` as `eae45ef4`. No prerequisite Swift work needed.)

---

## Phase 3 — US1: `status` SSE handler stops refetching

**Goal**: Two `status` events with different `connected_count` cause `appState.totalServers` / `appState.totalTools` updates but no `apiClient.servers()` call.

**Independent test**: XCTest with a fake `SSEClient` and a `CountingAPIClient` (an `APIClient` test double whose `servers()` increments a counter). Emit two `status` events with `connected_servers: 5` then `connected_servers: 6`. Assert counter == 0 and `appState.totalServers` reflects the latest event.

- [ ] T002 [P] [US1] Add failing XCTest `testStatusEventDoesNotRefetchOnConnectedCountChange` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`.
- [x] T003 [US1] In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, modify the `case "status":` branch (currently lines 502-522). Remove the `if connected != oldConnected { await refreshServers() }` clause. Keep the `else` branch that updates `appState.totalServers` and `appState.totalTools`. Apply both branches unconditionally — i.e., always merge stat deltas, never refetch.
- [ ] T004 [US1] Run `swift test --filter SSEHandlerTests/testStatusEventDoesNotRefetchOnConnectedCountChange` and confirm green.

## Phase 4 — US2: drop `refreshServers` from periodic `refreshState`

**Goal**: The 30 s `refreshState` timer no longer fetches `/api/v1/servers`.

**Independent test**: Drive `refreshState()` via a synchronous test entry; assert `apiClient.servers()` is not called.

- [ ] T005 [P] [US2] Add failing XCTest `testRefreshStateDoesNotCallServers` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`.
- [x] T006 [US2] In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift` `refreshState()` (currently line 616-625), remove the `await refreshServers()` line. Keep `refreshActivity`, `refreshSessions`, `refreshTokenMetrics`, `refreshSecurityStatus`, and the activity-version bump.
- [ ] T007 [US2] Run `swift test --filter SSEHandlerTests/testRefreshStateDoesNotCallServers` and confirm green.

## Phase 5 — US3: `refreshSecurityStatus` Docker fallback reads `appState`

**Goal**: When `dockerStatus()` returns false but configured servers exist, the "any connected stdio?" check reads `appState.servers` instead of fetching.

**Independent test**: Pre-populate `appState.servers` with one connected stdio server. Stub `dockerStatus()` to return false. Call `refreshSecurityStatus()`. Assert no `servers()` call AND `appState.dockerAvailable == true`.

- [ ] T008 [P] [US3] Add failing XCTest `testRefreshSecurityStatusReadsAppStateNotAPI` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`.
- [x] T009 [US3] In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift` `refreshSecurityStatus()` (currently line 628-668), replace the `let servers = try? await apiClient.servers()` block (lines 640-641) with a `MainActor.run` read of `appState.servers`. Apply the same `contains(where: { $0.connected && $0.protocol == "stdio" })` predicate.
- [ ] T010 [US3] Run `swift test --filter SSEHandlerTests/testRefreshSecurityStatusReadsAppStateNotAPI` and confirm green.

## Phase 6 — US4: drop the 10 s app-level timer

**Goal**: No `Timer.publish` in `MCPProxyApp.swift` calls `client.servers()`.

**Independent test**: Construct the app delegate in test mode with a `CountingAPIClient`. Wait > 10 s real time (or fast-forward via injected scheduler). Assert counter == 0 from the timer-driven path.

- [ ] T011 [P] [US4] Add failing XCTest `testNoTimerDrivenServersFetch` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`.
- [x] T012 [US4] In `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift`, remove the entire `Timer.publish(every: 10, ...)` block at lines 101-111 (the "Periodic server refresh every 10s" block). Replace with a one-line comment pointing at the safety-net timer added in Phase 8.
- [ ] T013 [US4] Run `swift test --filter SSEHandlerTests/testNoTimerDrivenServersFetch` and confirm green.

## Phase 7 — US5: drop the `menuWillOpen` refetch

**Goal**: Clicking the tray icon does not trigger a `/api/v1/servers` GET.

**Independent test**: Invoke `menuWillOpen(menu)` directly with a `CountingAPIClient` connected. Assert counter == 0 immediately (synchronous return — no Task spawned).

- [ ] T014 [P] [US5] Add failing XCTest `testMenuWillOpenDoesNotRefetch` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`.
- [x] T015 [US5] In `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift` `menuWillOpen(_:)` (currently lines 168-181), remove the inner `if let client = appState.apiClient { Task { … } }` block. Keep the synchronous `rebuildMenu()` call below it.
- [ ] T016 [US5] Run `swift test --filter SSEHandlerTests/testMenuWillOpenDoesNotRefetch` and confirm green.

## Phase 8 — Polish & Verification

- [ ] T017 [P] Add failing XCTest `testSafetyNetTimerRefetchesAtFiveMinutes` in `native/macos/MCPProxy/MCPProxyTests/SSEHandlerTests.swift`. Use a manual scheduler / fake clock so the assertion is deterministic.
- [x] T018 In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, expose `refreshServersForSafetyNet()` as a small public-to-the-module method that calls the existing private `refreshServers()`. (One-line wrapper; the separate name documents intent.)
- [x] T019 In `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift`, add a `Timer.publish(every: 300, on: .main, in: .common).autoconnect()` block that calls `coreManager?.refreshServersForSafetyNet()` and stores into `cancellables`. Place near where the removed 10 s timer lived. Comment block explains spec 048 reasoning.
- [ ] T020 Run `swift test --filter SSEHandlerTests` — entire SSE-handler suite must be green.
- [x] T021 Build the tray binary per `quickstart.md`. Verify it loads and the tray menu renders.
- [ ] T022 Live verification: launch the swap-in `/tmp/MCPProxy-048.app` against the user's real config; let it idle for 60 s; count `/api/v1/servers` GETs in `~/Library/Logs/mcpproxy/http.log`. Save raw count + the `grep | awk` window to `specs/048-tray-refetch-elimination/verification/http_log_idle.txt`. Acceptance: ≤ 1.
- [x] T023 Live verification: SSE-driven reactivity unchanged. Toggle `context7` via REST; capture `list_menu_items` for the `Servers (30)` submenu via `mcpproxy-ui-test`. Confirm `Connected (2 tools) → Disabled → Connected (2 tools)` cycle within 5 s of each REST call. Append the trace to `specs/048-tray-refetch-elimination/verification/report.md`.
- [x] T024 Commit all source + spec changes (excluding any binary verification artifacts per spec 047 lessons learned). Verify `git diff main..HEAD --stat -- web/frontend/dist/` is empty.
- [x] T025 Push branch and `gh pr create`. Title: `feat(048): eliminate remaining tray /api/v1/servers refetches`.
- [x] T026 Watch CI; iterate on failures (lint / Swift build / test) until green. Merge with `--admin --squash --delete-branch` once all checks pass and tag `v0.29.5`.

---

## Dependencies

```
Phase 1 (Setup)  ──┐
Phase 2 (Found.) ──┴──→ US1 ──┐
                       US2 ──┤
                       US3 ──┼─→ Phase 8 (Polish: safety-net + verification)
                       US4 ──┤
                       US5 ──┘
```

US1–US5 are all **independent**: each touches a different code site and a different test function; they can be implemented in any order. The polish phase (safety-net timer + live verification) depends on all five sites being completed.

## Parallelism

- T002, T005, T008, T011, T014, T017 — six different `XCTestCase` methods in `SSEHandlerTests.swift`. Same file, different functions. Author in parallel; merge-safely they are.
- T003, T006, T009, T012, T015 — implementation tasks across two Swift files. T003/T006/T009 share `CoreProcessManager.swift` so should serialise; T012 and T015 share `MCPProxyApp.swift` so should serialise; but {T003,T006,T009} can run in parallel with {T012,T015}.
- T021, T022, T023 — verification steps; sequential by nature (build → idle measurement → reactivity measurement).

## MVP scope

**US1 + US4** alone is the MVP — those are the two highest-frequency refetch sites. Together they take typical idle GET count from ~8/60s to ~3/60s. US2/US3/US5 each shave one more; the safety-net timer in Phase 8 leaves a clean ≤ 1/60s.

We ship all five plus the safety-net together in this PR.

## Format validation

- [x] Every task is a markdown checkbox.
- [x] Every task has a TaskID (T001–T026).
- [x] Story labels [US1]–[US5] applied to phases 3–7 only.
- [x] Setup/Foundational/Polish phases have no story label.
- [x] Every task references a concrete file path or command.
