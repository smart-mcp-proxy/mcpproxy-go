# Tasks: Tray Auto-Update Failure UX & Telemetry

**Input**: spec.md, plan.md, research.md, data-model.md, contracts/api-deltas.md (all Codex-approved)
**Tests**: TDD per constitution — every implementation task is preceded by a distinct failing-test task (a test that fails to compile against a not-yet-existing seam counts as red).
**Tracks**: Go core (US2) and Swift tray (US1 + US2-tray) are independent until Phase 5 integration; tasks marked [P] can run in parallel across tracks.

## Phase 1: Setup

- [x] T001 Baseline verification recorded in specs/095-update-failure-ux/verification/baseline.md (Go: httpapi/telemetry/diagnostics all ok; Swift: 939 tests, 1 known-environmental failure in AppLifecycleTests)

## Phase 2: Foundational (blocking prerequisites)

**Goal**: the shared Stage vocabulary + pure classifier both stories consume.

- [x] T002 [P] RED: Write failing classifier tests in native/macos/MCPProxy/MCPProxyTests/UpdateFailureClassifierTests.swift — full data-model.md precedence table: every SUSparkleErrorDomain code group (incl. 4000/4001 → install), exclusions return nil (1001/4007/4008), download-provenance latch wins over code, underlying-chain recursion (NSURLErrorDomain top-level wrapping a Sparkle code), unknown domain/code → other
- [x] T003 [P] GREEN: Create UpdateFailureStage enum + pure classify() in native/macos/MCPProxy/MCPProxy/Services/UpdateFailureClassifier.swift per data-model.md (no AppKit imports; no message-text inspection)

## Phase 3: User Story 1 — Recover from a failed update (P1) 🎯 MVP

**Goal**: recovery dialog with Try Again / Download from Website / Cancel replaces the stock alert.
**Independent test**: local Sparkle rig with unreachable enclosure → user-initiated check → new dialog, both actions work.
**Checkpoint**: dialog fully functional without any telemetry.

- [x] T004 [P] [US1] RED: Write failing URL-precedence tests in native/macos/MCPProxy/MCPProxyTests/UpdateFailureDialogTests.swift — installer candidate version-match rule, same-cycle rule when offeredVersion nil, stale-cycle installer skipped, non-HTTPS/relative candidate skipped, browser-open failure falls through to next candidate, releases-page constant always last and always attempted, invocation ORDER asserted via injected opener spy
- [x] T005 [P] [US1] RED: Add failing retry-ordering tests to native/macos/MCPProxy/MCPProxyTests/UpdateFailureDialogTests.swift — Try Again before terminal signal queues exactly one retry fired on signal; after signal fires immediately; double-click never stacks; Cancel and Download-from-Website never trigger a retry; queued retry dropped on teardown
- [x] T006 [US1] GREEN: Create dialog controller + presenter in native/macos/MCPProxy/MCPProxy/Services/UpdateFailureDialog.swift — stage-specific plain-language primary message, local-only secondary error description (FR-003), three buttons (Try Again default), injected seams: candidate opener, retry trigger, terminal-signal source; NSAlert presentation kept in a thin AppKit shell so ordering/precedence logic stays unit-testable
- [x] T007 [US1] RED: Write failing FeedUpdater session tests in native/macos/MCPProxy/MCPProxyTests/FeedUpdaterSessionTests.swift — session-level matrix per Codex finding 8: silent scheduled failure (driver never called) → occurrence emitted, no dialog request; duplicate abort callbacks in one session → one occurrence; provenance latch set by failedToDownloadUpdate and reset on next session; nil cycle error (user canceled download / no-update) → no occurrence; stashed driver error → dialog requested at cycle end with session already terminal; tray-synthesized postpone/tripwire messages bypass classification entirely
- [x] T008 [US1] GREEN: Rework native/macos/MCPProxy/MCPProxy/Services/FeedUpdater.swift — add RecoveryUserDriver subclass (override showUpdaterError: stash + immediate acknowledgement), migrate SPUStandardUpdaterController → SPUUpdater(hostBundle:applicationBundle:userDriver:delegate:) with retained driver, session state per data-model.md (downloadProvenance, lastOfferedItem, stashedError, sessionFinished, queuedRetry), occurrence emission from didFinishUpdateCycleForUpdateCheck, widened FeedUpdaterObserver carrying (stage, offeredVersion?, feedInfoURL?, dialogRequested)
- [x] T009 [US1] RED: Write failing UpdateService candidate-assembly tests in native/macos/MCPProxy/MCPProxyTests/UpdateServiceCandidateTests.swift — checkCycleID increments per runCheck; latestGitHubInstaller set ONLY on true -installer.dmg match and stamped with initiating cycle; candidate list assembly at occurrence time honors version-match/same-cycle/eligibility rules; existing downloadURL behavior untouched
- [x] T010 [US1] GREEN: Extend native/macos/MCPProxy/MCPProxy/Services/UpdateService.swift — checkCycleID + latestGitHubInstaller capture in checkWithGitHub(), occurrence handling (assemble candidates, drive dialog via seams), preserve all existing published state (SC-006: existing UpdateServiceFeedTests/UpdateMenuStateTests assertions unmodified)

## Phase 4: User Story 2 — Measure update failures in telemetry (P2)

**Goal**: every occurrence lands as MCPX_UPDATE_* counts in the heartbeat, privacy-safe, opt-out-gated at event time.
**Independent test**: quickstart.md REST smoke — 204/400/500 behavior + counts in payload preview + gate no-ops.
**Checkpoint**: Go track is fully independent of Phase 3 and may run in parallel with it.

### Go core track

- [x] T011 [P] [US2] RED: Write failing catalog tests in internal/diagnostics/codes_update_test.go — the four MCPX_UPDATE_{APPCAST,DOWNLOAD,INSTALL,OTHER}_FAILED codes exist, are catalog-registered (diagnostics.Has), and appear in All()
- [x] T012 [P] [US2] GREEN: Add the four codes + UPDATE domain doc comment to internal/diagnostics/codes.go and register them in the catalog (wherever Has()/All() are backed)
- [x] T013 [US2] RED: Write failing handler tests in internal/httpapi/update_failure_test.go — table: each valid stage → 204 and seam called with right code; invalid stage → 400 nothing recorded; unknown extra field → 400; trailing JSON value → 400; empty body → 400; seam returns (false, nil) [gate inactive] → 204 and NO store call; seam returns error → 500; route registered under /api/v1 with auth middleware (socket-bypass smoke via existing test harness pattern); MockServerController + baseController gain RecordUpdateFailure
- [x] T014 [US2] GREEN: Create internal/httpapi/update_failure.go — POST /api/v1/telemetry/update-failure handler (strict decode: DisallowUnknownFields + second-Decode io.EOF check; 400/500/204 per contracts/api-deltas.md), full swaggo annotation block, ServerController seam method RecordUpdateFailure(stage string) (bool, error), route registration in internal/httpapi/server.go, test doubles updated
- [x] T015 [US2] RED: Write failing forwarding tests in internal/server/ (alongside existing server tests) — RecordUpdateFailure: gate composition (config disabled → (false,nil); DO_NOT_TRACK/CI/MCPPROXY_TELEMETRY env → (false,nil); non-semver version → (false,nil); all-active → RecordErrorCode called with mapped code, (true,nil)); disable→record→re-enable leaves NO persisted count; each gate exercised independently (pin CI="" per repo gotcha)
- [x] T016 [US2] GREEN: Implement the forwarding method on internal/server/server.go binding gate (config.IsTelemetryEnabled + telemetry.IsDisabledByEnv + semver check) and synchronous bboltDiagnosticsCounterStore.RecordErrorCode behind one call
- [x] T017 [US2] RED: Write failing scanner tests in internal/telemetry/anonymity_update_test.go — assembled payload containing all four MCPX_UPDATE_* codes passes ScanForPII; a free-text key in error_code_counts_24h (e.g. server name or URL) is rejected; negative/float values rejected; full heartbeat assembly test (SC-004): record via store → payload preview contains the per-stage count
- [x] T018 [US2] GREEN: Add anonymity-scanner rule in internal/telemetry/anonymity.go validating diagnostics.error_code_counts_24h (keys must be catalog-registered diagnostics codes, values non-negative integers)
- [x] T019 [US2] Run make swagger; commit regenerated oas/swagger.yaml + oas/docs.go; add OAS contract assertion for the new route following internal/httpapi/connect_oas_test.go pattern

### Swift tray track

- [x] T020 [P] [US2] RED: Write failing APIClient tests in native/macos/MCPProxy/MCPProxyTests/APIClientUpdateFailureTests.swift — stub URLProtocol asserts exact path /api/v1/telemetry/update-failure, method POST, body {"stage":"download"}; 204 → success with response data discarded; 404/500/timeout → exactly one log line (via injectable logger spy), no body parsing, no retry, single attempt
- [x] T021 [US2] GREEN: Add status-only request helper + recordUpdateFailure(stage:) to native/macos/MCPProxy/MCPProxy/API/APIClient.swift (reads status, discards data, never parses non-2xx bodies)
- [x] T022 [US2] RED: Extend native/macos/MCPProxy/MCPProxyTests/FeedUpdaterSessionTests.swift (or UpdateServiceCandidateTests.swift) — every eligible occurrence triggers exactly one recorder invocation with the classified stage; excluded outcomes trigger none; recorder failure never affects dialog flow
- [x] T023 [US2] GREEN: Inject UpdateFailureRecorder closure/protocol into UpdateService (wired at the composition root that owns both UpdateService and APIClient), invoke off the UI path in a detached task, one attempt per occurrence

## Phase 5: Polish & Cross-Cutting

- [x] T024 Full Go gates: go test -race ./internal/... ; /opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./... ; make swagger-verify — record results in specs/095-update-failure-ux/verification/gates.md
- [x] T025 Full Swift gate: cd native/macos/MCPProxy && swift test — all suites pass except the documented baseline-environmental failure; existing update suites pass with UNMODIFIED assertions (SC-006) — append to verification/gates.md
- [x] T026 Quickstart REST smoke on an isolated core (specs/095-update-failure-ux/quickstart.md) — 204/400/500 + payload preview; record output in verification/gates.md
- [x] T027 Live-rig manual verification: write specs/095-update-failure-ux/verification/manual-protocol.md (rig setup per project memory: local appcast, higher version, enclosure → unreachable port; semver-versioned dev build) and execute it — new dialog appears (screenshot), Try Again re-checks (screenshot), Download from Website opens expected URL (screenshot), stock alert absent; screenshots into verification/shots/
- [x] T028 Verify version-skew tray behavior manually or via test: POST against an old-core simulation (404 route) → single log line, no user-visible effect

## Dependencies & Execution Order

- Phase 2 (T002-T003) blocks T004-T010 (US1) and T020-T023 (US2 Swift track).
- Go track T011-T019 has NO dependency on any Swift task — run in parallel with Phase 3 from the start.
- Within each red→green pair, red MUST be written and observed failing (or failing-to-compile) before green.
- T019 after T014/T016 (swagger needs final handler annotations).
- Phase 5 after everything; T027 needs a built tray with all Swift changes.

## Implementation Strategy

MVP = Phase 2 + Phase 3 (dialog, US1) — shippable without telemetry. US2's Go track can be built concurrently by a second worker. Integration risk concentrates in T008 (Sparkle bootstrap migration); if the SPUUpdater migration destabilizes existing suites, stop and re-verify against SC-006 before proceeding.
