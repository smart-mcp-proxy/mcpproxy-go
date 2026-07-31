# Tasks: Tray Glance v2 — Grouped Calls, Intent Reasons, Failure-Only Marks, Idle Clients

**Input**: Design documents from `/specs/090-tray-glance-v2/` (spec.md, plan.md, research.md D1–D9, data-model.md, contracts/api-deltas.md)
**Tests**: TDD per constitution — every behavior task is preceded by a failing-test task in the same story phase.

Swift paths are relative to `native/macos/MCPProxy/` (sources under `MCPProxy/`, tests under `MCPProxyTests/`). Go paths are repo-relative.

## Phase 1: Setup

- [ ] T001 Verify green baseline on branch 090-tray-glance-v2: `cd native/macos/MCPProxy && swift test` and `go test ./internal/httpapi/... ./internal/runtime/... ./internal/storage/... ./internal/server/...` both pass before any change (record failures that pre-exist, if any, in specs/090-tray-glance-v2/verification/baseline.md)

## Phase 2: Foundational (blocking prerequisites)

- [ ] T002 Write failing tests for `ActivityEntry` derived accessors (`intentReason`, `blockReason`, `reason`, `outcomeClass` incl. decision blocked/block and legacy-absence cases) in MCPProxyTests/ModelsTests.swift
- [ ] T003 Implement the accessors as an `ActivityEntry` extension in MCPProxy/API/Models.swift (data-model.md table); make T002 pass

## Phase 3: User Story 1 — Repeated calls collapse into one row (P1) 🎯 MVP

**Goal**: Consecutive same-(server, tool, outcome-class) records render as one ×N row; excluded records never split runs.

**Independent test**: Feed recorded sequences through the pure pipeline; verify collapse, ordering, age, worst outcome, and stable run identity.

- [ ] T004 [US1] Write failing tests in MCPProxyTests/GlanceGroupingTests.swift: maximal consecutive runs by (server, tool, outcomeClass); A-B-A stays 3 rows; dropped records (management built-ins, collapsed wrappers) don't split runs (US1 scenario 6); worst outcome error > success with error clause from newest erroring record; ×N only when N > 1; run identity = recordKey(oldest) stable under head-extension; window-capped counts (FR-001–004, FR-024)
- [ ] T005 [US1] Implement `GlanceRun` and `groupConsecutive` as pure additions to MCPProxy/Menu/Glance/GlanceSelection.swift; re-shape `activityRows(from:)` into the 4-step pipeline returning `[GlanceRun]`; make T004 pass
- [ ] T006 [US1] Update MCPProxy/Menu/Glance/GlanceSection.swift to render `GlanceRun` rows (×N suffix in title, age from newest, group identity drives `apply`'s same-record check) and adapt MCPProxy/State/AppState.swift call sites; update existing MCPProxyTests/GlanceSelectionTests.swift, GlanceSelectionCollapseTests.swift, AppStateGlanceTests.swift expectations; `swift test` green

**Checkpoint**: MVP — grouped rows visible; all other stories independent from here.

## Phase 4: User Story 2 — The reason a call happened is visible (P2)

**Goal**: Intent reasons ride the poll and the live stream, and render as macOS 14.4+ subtitles with tooltip/VoiceOver parity.

**Independent test**: Records with/without reasons render one- or two-line rows; polled and SSE entries expose identical reasons.

- [ ] T007 [P] [US2] Write failing Go test in internal/httpapi/activity_test.go: with `exclude_payloads=true` the response keeps ONLY the contextual whitelist (`intent.reason`, `intent.operation_type`, `decision`, `reason`, `client_name`, `client_version`) and still omits `arguments`, `response`, and all other metadata (contracts/api-deltas.md §1)
- [ ] T008 [US2] Implement the whitelist projection in internal/httpapi/activity.go, fix the `exclude_payloads` swagger annotation, run `make swagger` (commits oas/swagger.yaml, oas/docs.go); T007 green, `make swagger-verify` green
- [ ] T009 [US2] Write failing tests in MCPProxyTests/GlanceEventTests.swift for intent extraction from both completed-event payloads (metadata populated, reason exposed); then implement in MCPProxy/Menu/Glance/GlanceEvent.swift (research D9, intent part)
- [ ] T010 [US2] Write failing tests in MCPProxyTests/GlanceSectionTests.swift + GlanceFormattingTests.swift for: subtitle set only on macOS 14.4+ (inject availability as a testable flag), 60-char tail truncation, single-line when no reason, FR-011a failed-row template and truncation precedence (error clause 40 tail-truncated before label's 34 middle-truncation tightens; age never truncated), tooltip carries full label+reason+error, VoiceOver label includes reason; then implement in MCPProxy/Menu/Glance/GlanceSection.swift + GlanceFormatting.swift (FR-005–007, FR-011a)
- [ ] T011 [US2] Write failing test in MCPProxyTests/GlanceMenuPolicyTests.swift: `updateInPlace` precomputes ALL rows' line counts before mutating; when a LATER row's line count changes, it returns false with ZERO mutations to earlier rows; then implement the atomic preflight in MCPProxy/Menu/Glance/GlanceSection.swift `updateInPlace` (FR-023, data-model "Atomic preflight")

## Phase 5: User Story 3 — Only failures are marked, and blocked calls appear (P2)

**Goal**: Success rows lose their icon; errors/blocked get distinct marks; policy blocks become rows with end-to-end request identity.

**Independent test**: A feed with successes, errors, and blocks renders exactly the failure/blocked marks; blocked SSE rows reconcile with polled records without duplication.

- [ ] T012 [P] [US3] Write failing Go tests: internal/runtime/activity_service_test.go (persisted policy record carries `request_id`; SSE payload and persisted record share the identical non-empty id) and internal/server tests covering pre-intent-validation blocks, direct-routing blocks (internal/server/mcp_routing.go), output-sanitisation blocks/redactions and output-schema decisions (internal/server/output_sanitisation.go) each emitting a non-empty request id (research D3)
- [ ] T013 [US3] Implement request-id threading: `Runtime.EmitActivityPolicyDecision` gains `requestID` param + payload field in internal/runtime/event_bus.go; persistence subscriber copies it in internal/runtime/activity_service.go; ALL 16 emit sites updated (13 in internal/server/mcp.go with minting hoisted above the earliest policy gate, 1 in internal/server/mcp_routing.go, 2 in internal/server/output_sanitisation.go via helper-signature propagation); T012 green
- [ ] T014 [P] [US3] Write failing test in MCPProxyTests/APIClientGlanceTests.swift asserting the exact glance URL now includes `type=tool_call,internal_tool_call,policy_decision` with `exclude_payloads=true&limit=100`; then update MCPProxy/API/APIClient.swift `glanceActivity` (FR-014)
- [ ] T015 [US3] Write failing tests in MCPProxyTests/GlanceEventTests.swift (adapt `activity.policy_decision`: status "blocked", provisional id `"<request_id>:policy_decision"`, reason into metadata slot) and a routing test in MCPProxyTests/AppStateGlanceTests.swift proving a policy SSE event reaches the glance feed; then implement in MCPProxy/Menu/Glance/GlanceEvent.swift and add the event name to the SSE dispatch switch in MCPProxy/Core/CoreProcessManager.swift (research D9)
- [ ] T016 [US3] Write failing tests in MCPProxyTests/GlanceSelectionTests.swift (qualifies admits blocked policy_decision records — decision blocked/block only, warnings/redactions rejected; blocked runs never merge with call runs) and MCPProxyTests/GlanceSectionTests.swift (success rows have nil image + "succeeded" VoiceOver; error = red xmark.circle; blocked = orange exclamationmark.triangle, shape-distinct; blocked row's subtitle is the block reason); then implement in GlanceSelection.swift + GlanceSection.swift (FR-010–012, US3 scenario 5)

## Phase 6: User Story 4 — Clients show presence honestly (P2)

**Goal**: Clients section lists deduped clients with active/idle/seen states over all retained sessions.

**Independent test**: Session records at various ages classify/dedupe/order correctly; summary counts cover the full deduped set.

- [ ] T017 [P] [US4] Write failing Go test in internal/storage/manager_test.go: an old-start but recently-active session survives `GetRecentSessions(limit)` truncation (ordering by `last_activity` desc happens BEFORE limit, across statuses) (contracts §3)
- [ ] T018 [US4] Implement collect-all→sort→filter→truncate in internal/storage/manager.go `GetRecentSessions`; T017 green; confirm runtime post-sort at internal/runtime/runtime.go stays harmless
- [ ] T019 [P] [US4] Write failing tests in MCPProxyTests/GlancePresenceTests.swift: state boundaries (<5m active; 5:00 and 30:00 idle; >30m..24h seen; >24h excluded), missing `last_activity` falls back to start time, unparseable excluded, negative age → 0s, dedupe by name+version keeping most recent, "Unknown client" fallback, top-5 ordering, summary counts over full deduped set with seen excluded and empty states omitted; then implement pure MCPProxy/Menu/Glance/GlancePresence.swift (FR-017–020, research D6)
- [ ] T020 [US4] Wire it: MCPProxy/API/APIClient.swift `activeSessions` → unfiltered `limit=100` (rename to `recentSessions`; assert exact URL in MCPProxyTests/APIClientGlanceTests.swift); MCPProxy/State/AppState.swift summary "N active · M idle"; MCPProxy/Menu/Glance/GlanceSection.swift presence rows (filled/gray/hollow indicators, idle/seen ages, placeholder only when lookback empty); update MCPProxyTests/GlanceSelectionTests.swift `activeClients` replacement + AppStateGlanceTests.swift + GlanceSectionTests.swift

## Phase 7: User Story 5 — 24h picture first (P3)

- [ ] T021 [US5] Write failing order assertion in MCPProxyTests/GlanceSectionTests.swift (summary → Activity (24h) → "Recent" → rows → Clients), then move the histogram item in MCPProxy/Menu/Glance/GlanceSection.swift `items(for:)` (FR-021)

## Phase 8: Polish & Cross-Cutting

- [ ] T022 [P] Write MCPProxyTests/GlanceFixtureReplayTests.swift: locate `specs/090-tray-glance-v2/fixtures/activity-replay.jsonl` by walking up from `#filePath`; chronological replay (reverse file order) with sliding latest-100 window; assert SC-001 (no two adjacent rows share a group key; ≥19-burst occupies one row) and SC-004 (all 52 blocks become visible at some step)
- [ ] T023 Add the injectable data-source seam to MCPProxy/MCPProxyApp.swift (production default APIClient) and write MCPProxyTests/MenuOpenNetworkTests.swift driving the REAL `menuWillOpen` → rebuild → updateInPlace sequence on a real NSMenu with the counting stub, asserting a zero request delta (FR-022, research D8)
- [ ] T024 Documentation + gates: note the `exclude_payloads` contextual whitelist in docs/features/activity-log.md; run `./scripts/run-linter.sh`, full `go test ./internal/...`, `cd native/macos/MCPProxy && swift test`, `make swagger-verify`; fill specs/090-tray-glance-v2/verification/manual-protocol.md results after a live run

## Dependencies & Execution Order

- Phase 1 → Phase 2 → everything else.
- US1 (T004–T006) is the MVP and blocks nothing except sharing files with later stories.
- US2, US3, US4 are mutually independent but all touch GlanceSection.swift/AppState.swift — execute sequentially (T007/T012/T017/T019 test-writing tasks marked [P] can be written in parallel).
- US5 (T021) can run any time after Phase 3.
- T022 needs US1+US3 (grouping + blocked rows); T023 needs US4 (session fetch changes); T024 last.

## Implementation Strategy

MVP = Phases 1–3 (grouped rows). Then US2 → US3 → US4 → US5 in order, polish last. Each phase ends with `swift test` (and `go test` where touched) green before moving on; commits are per-task or per-phase, conventional format, no AI attribution.
