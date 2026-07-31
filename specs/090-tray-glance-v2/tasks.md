# Tasks: Tray Glance v2 — Grouped Calls, Intent Reasons, Failure-Only Marks, Idle Clients

**Input**: Design documents from `/specs/090-tray-glance-v2/` (spec.md, plan.md, research.md D1–D9, data-model.md, contracts/api-deltas.md)
**Tests**: TDD per constitution — every implementation task is preceded by a distinct failing-test task (a test that fails to compile against a not-yet-existing seam counts as red).

Swift paths are relative to `native/macos/MCPProxy/` (sources under `MCPProxy/`, tests under `MCPProxyTests/`). Go paths are repo-relative.

## Phase 1: Setup

- [x] T001 Verify green baseline on branch 090-tray-glance-v2: `cd native/macos/MCPProxy && swift test` and `go test ./internal/httpapi/... ./internal/runtime/... ./internal/storage/... ./internal/server/...` both pass before any change (record pre-existing failures, if any, in specs/090-tray-glance-v2/verification/baseline.md)

## Phase 2: Foundational (blocking prerequisites)

- [x] T002 Write failing tests for `ActivityEntry` derived accessors (`intentReason`, `blockReason`, `reason`, `outcomeClass` incl. decision blocked/block and legacy-absence cases) in MCPProxyTests/ModelsTests.swift
- [x] T003 Implement the accessors as an `ActivityEntry` extension in MCPProxy/API/Models.swift (data-model.md table); T002 green

## Phase 3: User Story 1 — Repeated calls collapse into one row (P1) 🎯 MVP

**Goal**: Consecutive same-(server, tool, outcome-class) records render as one ×N row; excluded records never split runs.

**Independent test**: Feed recorded sequences through the pure pipeline; verify collapse, ordering, age, worst outcome, and stable run identity.

- [x] T004 [US1] Write failing tests in MCPProxyTests/GlanceGroupingTests.swift: maximal consecutive runs by (server, tool, outcomeClass); A-B-A stays 3 rows; dropped records (management built-ins, collapsed wrappers) don't split runs (US1 scenario 6); worst outcome error > success with error clause from newest erroring record; ×N only when N > 1; run identity = recordKey(oldest) stable under head-extension; window-capped counts (FR-001–004, FR-024)
- [x] T005 [US1] Implement `GlanceRun` and `groupConsecutive` as pure additions to MCPProxy/Menu/Glance/GlanceSelection.swift; re-shape `activityRows(from:)` into the 4-step pipeline returning `[GlanceRun]`; T004 green
- [x] T006 [US1] Write failing rendering tests: MCPProxyTests/GlanceSectionTests.swift asserts a run renders one row with "×N" title suffix, age from newest, and in-place `apply` keyed by run identity (same-run count bump ≠ different-run turnover); update MCPProxyTests/GlanceSelectionTests.swift, GlanceSelectionCollapseTests.swift, AppStateGlanceTests.swift expectations to the `[GlanceRun]` pipeline (red)
- [x] T007 [US1] Implement run rendering in MCPProxy/Menu/Glance/GlanceSection.swift and adapt MCPProxy/State/AppState.swift call sites; T006 and full `swift test` green

**Checkpoint**: MVP — grouped rows visible; all other stories independent from here.

## Phase 4: User Story 2 — The reason a call happened is visible (P2)

**Goal**: Intent reasons ride the poll and the live stream, and render as macOS 14.4+ subtitles with tooltip/VoiceOver parity.

**Independent test**: Records with/without reasons render one- or two-line rows; polled and SSE entries expose identical reasons.

- [x] T008 [P] [US2] Write failing Go test in internal/httpapi/activity_test.go: with `exclude_payloads=true` the response keeps ONLY the contextual whitelist (`intent.reason`, `intent.operation_type`, `decision`, `reason`, `client_name`, `client_version`) and still omits `arguments`, `response`, and all other metadata (contracts/api-deltas.md §1)
- [x] T009 [US2] Implement the whitelist projection in internal/httpapi/activity.go, fix the `exclude_payloads` swagger annotation, run `make swagger` (commit oas/swagger.yaml, oas/docs.go); T008 green, `make swagger-verify` green
- [x] T010 [P] [US2] Write failing tests in MCPProxyTests/GlanceEventTests.swift: intent extraction from both completed-event payloads (metadata populated, reason exposed via the T003 accessors)
- [x] T011 [US2] Implement intent extraction in MCPProxy/Menu/Glance/GlanceEvent.swift (research D9, intent part); T010 green
- [x] T012 [P] [US2] Write failing tests in MCPProxyTests/GlanceSectionTests.swift + GlanceFormattingTests.swift: subtitle set only on macOS 14.4+ (availability injected as a testable flag), 60-char tail truncation, single-line when no reason, FR-011a failed-row template and truncation precedence (error clause 40 tail-truncated before the label's 34 middle-truncation tightens; age never truncated), tooltip carries full label+reason+error, VoiceOver label includes reason
- [x] T013 [US2] Implement subtitle rendering + budgets + failed-row template in MCPProxy/Menu/Glance/GlanceSection.swift and GlanceFormatting.swift (FR-005–007, FR-011a); T012 green
- [x] T014 [P] [US2] Write failing test in MCPProxyTests/GlanceMenuPolicyTests.swift: `updateInPlace` precomputes ALL rows' line counts before mutating; when a LATER row's line count changes, it returns false with ZERO mutations to earlier rows
- [x] T015 [US2] Implement the atomic line-count preflight in MCPProxy/Menu/Glance/GlanceSection.swift `updateInPlace` (FR-023, data-model "Atomic preflight"); T014 green

## Phase 5: User Story 3 — Only failures are marked, and blocked calls appear (P2)

**Goal**: Success rows lose their icon; errors/blocked get distinct marks; policy blocks become rows with end-to-end request identity.

**Independent test**: A feed with successes, errors, and blocks renders exactly the failure/blocked marks; blocked SSE rows reconcile with polled records without duplication.

- [ ] T016 [P] [US3] Write failing Go tests for policy request identity: internal/runtime/activity_service_test.go (persisted policy record carries `request_id`; SSE payload and persisted record share the identical non-empty id), internal/server/intent_validation_test.go (pre-intent-validation blocks), internal/server/mcp_routing_test.go (direct-routing blocks), internal/server/output_sanitisation_test.go (sanitisation blocks/redactions), internal/server/mcp_output_schema_test.go (output-schema decisions) — each asserting a non-empty request id on the emitted event (research D3)
- [ ] T017 [US3] Implement request-id threading: `Runtime.EmitActivityPolicyDecision` gains `requestID` param + payload field in internal/runtime/event_bus.go; persistence subscriber copies it in internal/runtime/activity_service.go; ALL 16 emit sites updated (13 in internal/server/mcp.go with minting hoisted above the earliest policy gate, 1 in internal/server/mcp_routing.go, 2 in internal/server/output_sanitisation.go via helper-signature propagation); T016 green
- [ ] T018 [P] [US3] Write failing test in MCPProxyTests/APIClientGlanceTests.swift asserting the exact glance URL includes `type=tool_call,internal_tool_call,policy_decision` with `exclude_payloads=true&limit=100`
- [ ] T019 [US3] Update `glanceActivity` in MCPProxy/API/APIClient.swift (FR-014); T018 green
- [ ] T020 [P] [US3] Write failing tests: MCPProxyTests/GlanceEventTests.swift adapts `activity.policy_decision` (status "blocked", provisional id `"<request_id>:policy_decision"`, reason into metadata slot); MCPProxyTests/AppStateGlanceTests.swift routing test proving a policy SSE event reaches the glance feed
- [ ] T021 [US3] Implement policy-event adaptation in MCPProxy/Menu/Glance/GlanceEvent.swift and add the event name to the SSE dispatch switch in MCPProxy/Core/CoreProcessManager.swift (research D9); T020 green
- [ ] T022 [P] [US3] Write failing tests: MCPProxyTests/GlanceSelectionTests.swift (qualifies admits blocked policy_decision records — decision blocked/block only, warnings/redactions rejected; blocked runs never merge with call runs); MCPProxyTests/GlanceSectionTests.swift (success rows have nil image + "succeeded" VoiceOver; error = red xmark.circle; blocked = orange exclamationmark.triangle, shape-distinct; blocked row's subtitle is the block reason)
- [ ] T023 [US3] Implement blocked qualification + failure-only iconography in MCPProxy/Menu/Glance/GlanceSelection.swift + GlanceSection.swift (FR-010–012, US3 scenario 5); T022 green

## Phase 6: User Story 4 — Clients show presence honestly (P2)

**Goal**: Clients section lists deduped clients with active/idle/seen states over all retained sessions.

**Independent test**: Session records at various ages classify/dedupe/order correctly; summary counts cover the full deduped set.

- [x] T024 [P] [US4] Write failing Go test in internal/storage/manager_test.go: an old-start but recently-active session survives `GetRecentSessions(limit)` truncation (ordering by `last_activity` desc happens BEFORE limit, across statuses) (contracts §3)
- [x] T025 [US4] Implement collect-all→sort→filter→truncate in internal/storage/manager.go `GetRecentSessions`; T024 green; confirm the runtime post-sort in internal/runtime/runtime.go stays harmless
- [ ] T026 [P] [US4] Write failing tests in MCPProxyTests/GlancePresenceTests.swift: state boundaries (<5m active; 5:00 and 30:00 idle; >30m..24h seen; >24h excluded), missing `last_activity` falls back to start time, unparseable excluded, negative age → 0s, dedupe by name+version keeping most recent, "Unknown client" fallback, top-5 ordering, summary counts over full deduped set with seen excluded and empty states omitted
- [ ] T027 [US4] Implement pure MCPProxy/Menu/Glance/GlancePresence.swift (FR-017–020, research D6); T026 green
- [ ] T028 [P] [US4] Write failing wiring tests: MCPProxyTests/APIClientGlanceTests.swift asserts the sessions URL is unfiltered `limit=100`; MCPProxyTests/AppStateGlanceTests.swift asserts the summary reads "N active · M idle" (seen excluded, empty states omitted); MCPProxyTests/GlanceSectionTests.swift asserts presence rows (filled/gray/hollow indicators, idle/seen ages) and the placeholder only when the lookback is empty; update GlanceSelectionTests.swift for the `activeClients` replacement
- [ ] T029 [US4] Implement the wiring: MCPProxy/API/APIClient.swift `activeSessions` → unfiltered `recentSessions(limit: 100)`; MCPProxy/State/AppState.swift summary counts; MCPProxy/Menu/Glance/GlanceSection.swift presence rows; T028 green

## Phase 7: User Story 5 — 24h picture first (P3)

- [ ] T030 [US5] Write failing order assertion in MCPProxyTests/GlanceSectionTests.swift (summary → Activity (24h) → "Recent" → rows → Clients)
- [ ] T031 [US5] Move the histogram item in MCPProxy/Menu/Glance/GlanceSection.swift `items(for:)` (FR-021); T030 green

## Phase 8: Polish & Cross-Cutting

- [ ] T032 [P] Write MCPProxyTests/GlanceFixtureReplayTests.swift: locate `specs/090-tray-glance-v2/fixtures/activity-replay.jsonl` by walking up from `#filePath`; chronological replay (reverse file order) with sliding latest-100 window; assert SC-001 (no two adjacent rows share a group key; ≥19-burst occupies one row) and SC-004 (all 52 blocks become visible at some step)
- [ ] T033 Write MCPProxyTests/MenuOpenNetworkTests.swift against the not-yet-existing injectable data-source initializer on the controller (red: fails to compile): construct the controller with a counting stub, drive the REAL `menuWillOpen` → rebuild → `updateInPlace` sequence on a real NSMenu, assert a zero request delta (FR-022, research D8)
- [ ] T034 Implement the injectable data-source seam in MCPProxy/MCPProxyApp.swift (production default APIClient); T033 green
- [ ] T035 Documentation + gates: note the `exclude_payloads` contextual whitelist in docs/features/activity-log.md; validate specs/090-tray-glance-v2/quickstart.md commands still match reality; run `./scripts/run-linter.sh`, full `go test ./internal/...`, `cd native/macos/MCPProxy && swift test`, `make swagger-verify`; fill specs/090-tray-glance-v2/verification/manual-protocol.md results after a live run

## Dependencies & Execution Order

- Phase 1 → Phase 2 → everything else.
- US1 (T004–T007) is the MVP.
- US2, US3, US4 are logically independent, but shared-file contention forces sequential implementation: production `GlanceSection.swift` is touched by US1/US2/US3/US4/US5; `AppState.swift` by US1/US4 (US3 only adds a test in AppStateGlanceTests, no production change); `GlanceEvent.swift` by US2/US3. Tasks marked [P] are parallelizable only WITHIN their phase (they touch files distinct from that phase's other [P] tasks). Across phases the same test files recur (GlanceEventTests: T010/T020; GlanceSectionTests: T012/T022/T028; APIClientGlanceTests: T018/T028; AppStateGlanceTests: T020/T028; GlanceSelectionTests: T022/T028), so cross-phase test authoring is sequential in phase order.
- US5 (T030–T031) can run any time after Phase 3.
- T032 needs US1+US3 (grouping + blocked rows); T033–T034 need US4 (session fetch changes); T035 last.

## Implementation Strategy

MVP = Phases 1–3 (grouped rows). Then US2 → US3 → US4 → US5 in order, polish last. Each phase ends with `swift test` (and `go test` where touched) green before moving on; commits are per-task or per-phase, conventional format, no AI attribution.
