# Tasks: Review-Bound Approvals, Durable Baselines and Ordered Inventories (Spec 117)

**Input**: the design documents in `/specs/117-review-bound-approvals/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**TDD rule (constitution V)**: write each `test` task first. It must FAIL for the stated behavioural reason, on a build that compiles, before its paired `impl` task starts. Add a stub where a new symbol is needed, so the failure is behavioural and not a compile error (memory `verify-test-bites-build-failure`). Record the failing output in the PR description.

**Format**: `[ID] [P?] [PR] Description`. `[P]` means the task can run in parallel with other `[P]` tasks in its group (different files). The tasks are grouped by the three planned PRs. Each PR is cut from the then-current `origin/main` and merged on its own (no stacking).

---

## PR-1: Durable tool baseline (#1567), branch `117-pr1-durable-baseline`

**Declared scope**: `internal/storage` (new bucket + removal), `internal/runtime/tool_quarantine.go` (`serverHasBaseline`, marking), `internal/security/scanner/service.go` (marking inside the locked commit), blind-approval copy in `ReviewScreen.vue` and `review_cmd.go`, `docs/features/security-quarantine.md`. Nothing else.

### Setup
- [ ] T101 Branch from `origin/main`. Run the baseline suites once and record the timings: `go test -race ./internal/runtime/... ./internal/storage/... ./internal/security/scanner/...`.

### Storage
- [ ] T102 [P] [PR-1] test: `internal/storage/tool_baseline_decision_test.go`.
  - `MarkToolBaselineDecided` then `ToolBaselineDecided` → true.
  - Idempotent: the first `decided_at` is kept, and `source` is upgraded only from `inferred`.
  - Unknown server → false.
  - An unreadable value → true (fail closed).
  - `DeleteServerToolApprovals("s")` removes the marker in the same tx (assert with a single `View` after).
  - `PruneToolApprovalsNotIn({a})` drops b's marker and keeps a's.
  - A store without the bucket reads false and does not create it on read.
- [ ] T103 [PR-1] impl: `internal/storage/models.go` const, `tool_baseline_decision.go`, `bbolt.go` delete hooks, `manager.go` wrappers (data-model §5).

### Runtime
- [ ] T104 [P] [PR-1] test: `internal/runtime/tool_baseline_durable_test.go` (US2 scenarios 1, 2, 4, 5), on a trusted manual-trust server with quarantine enabled.
  - (a) Drive the **full** apply path (`commitDiscoveredTools` → `applyDifferentialToolUpdate`, with the index manager), because absent records are deleted there, not in `checkToolApprovals` (`lifecycle.go:1149-1185`; design review r1 #5). Steps: baseline `[A]` (A indexed) → apply `[B]` (A's record and index entry deleted) → apply `[B]` ×2 → new Runtime on the same DB → apply `[B]`. Expect B pending throughout, B absent from the index, and dispatch of B refused via the `internal/server` call gate.
  - (b) The same with an authoritative empty apply (`[]` through the authoritative path) between A and B.
  - (c) A pre-seeded approved record without a marker → one pass → marker present with `source: inferred`.
  - (d) Delete the server records → first inventory auto-baselined.
  - (e) First inventory `[]` → no marker; then `[A]` → auto-baselined (FR-025).

  (a) and (b) fail on main: B becomes approved by `auto-baseline` on the second apply, as the live repro showed. (c) and (e) are marker-inference tests and are kept separate from the failing-first pair.
- [ ] T105 [PR-1] impl: `tool_quarantine.go`.
  - `serverHasBaseline` ORs `ToolBaselineDecided` (read error → true).
  - After the per-tool loop, still under the lock: if any record of the server is approved or changed after the pass (track it in the loop, plus `priorByName`), mark with `discovery`, or `inferred` when only prior records qualified.
  - Log at Info once per server when a marker is first written.
- [ ] T106 [P] [PR-1] test: `internal/security/scanner/service_test.go` + `internal/runtime/tool_baseline_durable_test.go` (US2 scenario 3).
  - `ApproveServer` and `ApproveServerWithBlocks` on an empty inventory write the marker (`server_approval`, `by: api`) inside the locked commit: the recording locker sees the mark between lock and unlock.
  - After the unquarantine, a pass with `[T]` leaves T pending.

  Fails on main: no marker, T auto-approved.
- [ ] T107 [PR-1] impl:
  - new leaf package `internal/toolreview` (`BaselineDecision`);
  - `Runtime.CommitServerApproval` in its PR-1 form: lock, mark, `commit()`;
  - scanner `ServerApprovalCommitter` capability, used by both branches (`SaveIntegrityBaseline` and `…WithBlocks`) when present, with a fallback to today's path for fakes;
  - the adapter in `internal/server/server.go`.

  The scanner must not import `internal/storage` or `internal/runtime` (storage imports scanner).

### Copy and docs
- [ ] T108 [P] [PR-1] test: vitest `frontend/tests/unit/review-screen-blind-copy.spec.ts` (the dialog text mentions that later tools wait for review) + `cmd/mcpproxy/review_cmd_test.go` (the blind prompt text).
- [ ] T109 [PR-1] impl: copy changes (FR-026); `docs/features/security-quarantine.md` "Baseline" section; CLAUDE.md Recent Changes line.

### Verify and ship
- [ ] T110 [PR-1] Run the full lanes (quickstart §4), both lints, `make build`. Live check **L1** (quickstart §3) with evidence under `evidence/s117-pr1/`.
- [ ] T111 [PR-1] Open the PR (`Closes #1567`) and run an opencode Sol 6.1 review (≤10 rounds; record in research.md "PR-1 review log").

---

## PR-2: Review-bound approvals (#1566), branch `117-pr2-review-binding` (requires PR-1 merged)

**Declared scope**:
- runtime: fingerprint, review field, reviewed approvals, `CommitServerApproval` / `FinishServerApproval`, the `QuarantineServer` split;
- `StaleToolReviewError` in storage;
- the scanner reviewed approval;
- httpapi: both handlers + 409 mapping, swagger;
- CLI `review approve`;
- Web `ReviewScreen.vue` / `api.ts` / types / `reviewPresentation.ts`;
- docs.

Not in scope: Tools.vue, `security approve` CLI, MCP operations, `native/`.

### Fingerprint and review payload
- [ ] T201 [P] [PR-2] test: `internal/runtime/review_fingerprint_test.go`.
  - Golden vectors (`testdata/review_fingerprint_vectors.json`).
  - It changes for a description, input schema, output schema and each of the 4 hints (nil → false → true).
  - It does not change for title, status, disabled or held evidence.
  - A record with an empty `CurrentHash` gets a non-empty, definition-sensitive fingerprint.
  - The prefix is `rv1-`.
- [ ] T202 [PR-2] impl: `internal/runtime/review_fingerprint.go` (data-model §1).
- [ ] T203 [P] [PR-2] test: `internal/runtime/review_test.go` + `internal/httpapi/review_test.go`. Every tool in `GET /servers/{id}/review` has `current_hash == reviewFingerprint(record)`. It is absent when there are no records. Fails: the field does not exist.
- [ ] T204 [PR-2] impl: `ReviewTool.CurrentHash` in `review.go` (data-model §2); `frontend/src/types/api.ts`.

### Runtime validation
- [ ] T205 [P] [PR-2] test: `internal/runtime/tool_approval_review_test.go` (US1 scenario 4 + edge cases).
  - `ApproveToolsReviewed` with a matching fingerprint → approved.
  - Changed definition → `*StaleToolReviewError{Changed}` and the record unchanged (status, hashes).
  - Hint-only change → Changed.
  - Name missing from expected → Unreviewed.
  - Empty value → Unreviewed.
  - A name without a record → not an error, reported missing.
  - `expected == nil` → identical to `ApproveTools` (table shared with the existing tests).
  - `ApproveAllToolsReviewed` validates pending+changed.
  - The check and write are atomic: `toolApprovalReadHook` mutates the record (simulated capture) after the handler read but before the lock → still stale, because validation re-reads under the lock.
- [ ] T206 [PR-2] impl: `internal/toolreview/errors.go` (leaf package); `staleReviewedRecords`, `ApproveToolsReviewed`, `ApproveAllToolsReviewed` in `tool_quarantine.go` (validation and approval in one locked section; `notifyToolsApproved` after unlock).
- [ ] T207 [P] [PR-2] test: `internal/runtime/server_approval_commit_test.go` (US1 scenarios 1-3, 5; FR-011/FR-012).
  - Bound mismatch → error, and: `commit` was not called, no marker written, storage still quarantined, records unchanged.
  - Bound `{}` on an empty review → success; then a pass with `[T]` after `FinishServerApproval` → T pending (marker from PR-1; fails without PR-1's mark because the pass is a baseline pass).
  - Bound match → promoted == the validated pending set, blocks applied by `commit`, storage unquarantined **before** unlock (assert from inside a recording `commit` that runs last and from a hook after the flip).
  - An approved+enabled tool with a hint change → Changed (the activation set).
  - Unbound → promotes the pending records present at commit only. A record filed by a pass after `CommitServerApproval` returns but before `FinishServerApproval` stays pending. On main, `approveBaselineToolsForServer` promotes it; that is the failing assertion.
  - `FinishServerApproval` does not call `approveBaselineToolsForServer` (spy).
  - A missing block target under a bound request → `MissingBlocks`, nothing written.
- [ ] T208 [PR-2] impl: `CommitServerApproval`, `FinishServerApproval` in `tool_approval_lock.go`. Split `QuarantineServer` into flip + `finishQuarantineChange(server, quarantined, promote bool)` in `lifecycle.go` (the non-review path keeps `promote=true`). This extends PR-1's `CommitServerApproval` (the mark stays the first write after validation).
- [ ] T209 [P] [PR-2] test: `internal/security/scanner/service_test.go`.
  - `ApproveServerReviewed` uses `ReviewedServerApprover` when present and passes `expected` through.
  - The dangerous gate still runs first (its message contains "dangerous").
  - A stale error propagates unchanged (`errors.As`).
  - `ApproveServer`/`WithBlocks` call it with nil.
  - A fake without the capability keeps the legacy path.
- [ ] T210 [PR-2] impl: scanner `ApproveServerReviewed`, interface; adapter in `internal/server/server.go`.

### REST
- [ ] T211 [P] [PR-2] test: `internal/httpapi/tool_quarantine_test.go` + `security_scanner_test.go`.
  - Body decoding: absent / `null` → unbound; `{}` → bound.
  - 409 body exactly per contracts/rest-approval-binding.md: `error_code`, `stale_tools` lists always present, the message contains "out of date" and not "dangerous".
  - A bound missing block → 409 (an unbound one keeps 400).
  - Success bodies unchanged.
  - The controller receives `expected`.
- [ ] T212 [PR-2] impl: `security_scanner.go`, `server.go` handlers, `review_errors.go`; controller interface additions; swagger annotations + `oas/swagger.yaml` regeneration.

### Clients
- [ ] T213 [P] [PR-2] test: vitest `frontend/tests/unit/review-screen-binding.spec.ts`.
  - The approve request carries the click-time hashes of all reviewed tools.
  - Not-captured / empty → `{}`.
  - No hashes anywhere → no key.
  - A force retry after "dangerous" re-sends the original binding even after a reload.
  - A per-tool approve sends `{name: hash}`.
  - A 409 out of date → the review reloads, a notice shows the message, the force dialog is not opened, and explicit choices are kept for tools still present.
- [ ] T214 [PR-2] impl: `api.ts` (`securityApprove(server, force, block, expectedHashes?)`, `approveTools(server, tools, expectedHashes?)`), `reviewPresentation.ts` (`expectedHashes(tools, review)` mirroring the tray's `ReviewPresentation.expectedHashes`), `ReviewScreen.vue`.
- [ ] T215 [P] [PR-2] test: `cmd/mcpproxy/review_cmd_test.go`.
  - Quarantined path body includes `expected_hashes` (all tools, or `{}` when empty).
  - Trusted path: `tools` → expected for those tools; default → `approve_all` + expected for the pending+changed tools.
  - A 409 out of date → exit 1, the message + hint line, the JSON envelope under `-o json`.
- [ ] T216 [PR-2] impl: `review_cmd.go` (`reviewToolState.CurrentHash`; body builders; error mapping).

### Docs, verify, ship
- [ ] T217 [PR-2] docs: `docs/api/rest-api.md` (review field, both approve bodies, 409, compatibility matrix), `docs/cli/review-commands.md`, `docs/features/security-quarantine.md`; CLAUDE.md Recent Changes.
- [ ] T218 [PR-2] Full lanes, both lints, `make build`. Live check **L2** (all 8 steps; step 8 only once #1572 is merged), evidence under `evidence/s117-pr2/`. Playwright review sweep per `docs/development/web-ui-verification.md`.
- [ ] T219 [PR-2] Open the PR (`Closes #1566`; note that #1572 becomes effective) and run an opencode Sol 6.1 review. This is a security PR: escalate to codex Sol only if Sol misses a defect twice.

---

## PR-3: Apply tool inventories in capture order (#1565), branch `117-pr3-capture-order`

**Declared scope**: `internal/upstream/managed` (capture, retire), `internal/upstream/manager.go` (captured sweep, retire on replace/remove), `internal/runtime` (lock state, `applyInventory`, sweep publish, `QuarantineServer` purge, `lastGoodTools` seq, the quarantined capture via `applyInventory`). No wire changes. No UI.

### Capture identity
- [ ] T301 [P] [PR-3] test: `internal/upstream/managed/listtools_capture_test.go` (US3 scenario 5).
  - The leader's capture seq is strictly greater than any capture whose leader started earlier.
  - A coalesced waiter returns the leader's capture (same seq and epoch).
  - The health check and tool-count refresh paths publish a capture too.
  - `ListTools` is unchanged.
- [ ] T302 [PR-3] impl: `capture.go`, `client.go` (`runListToolsAsLeader` stamps seq next to `listEpoch`; published with the result), `ListToolsCaptured`.
- [ ] T303 [P] [PR-3] test: `internal/upstream/managed/retire_test.go` + `internal/upstream/manager_test.go` (US3 scenario 6).
  - `Retire` makes `WithConnectionEpoch` and `CaptureIsCurrent` false.
  - `AddServerConfig` with a changed command retires the old client before the map entry is replaced.
  - `RemoveServer` retires.
  - `DiscoverToolsReportCaptured` returns one capture per listed server, each from that server's own list.
- [ ] T304 [PR-3] impl: `Retire`, `CaptureIsCurrent`, manager changes, `DiscoverToolsReportCaptured`.

### Ordered apply
- [ ] T305 [P] [PR-3] test: `internal/runtime/inventory_lock_order_test.go`.
  - (a) **Failing-first** deadlock test (design review r1 #2): run concurrently, with hooks forcing the overlap:
    - a normal apply holding `captureSnapMu` + the approval lock and waiting for `captureMu`;
    - a quarantined capture;
    - an endpoint replacement (`AddServerConfig`).

    On main's lock order, the quarantined capture holds `captureMu`/`epochMu` and waits for `captureSnapMu`, so the test times out once the new apply phase exists. It must pass after T307 removes the outer wrapper. Write it against T307's stubbed `applyInventory`, so the failure is the deadlock and not a compile error.
  - (b) **Regression check** (already true on main; not failing-first): supervisor `RefreshServerToolsFromDiscovery` called under a held approval lock completes without re-entering the runtime (`supervisor.go:1214-1230`).
  - (c) **Regression check**: no function reachable from `approveToolsAndNotify` or `applyInventory`'s locked phase takes `captureSnapMu`. If PR-2 is already merged, also check `CommitServerApproval`. This is a composition check, not a PR-3 dependency.
- [ ] T306 [P] [PR-3] test: `internal/runtime/inventory_capture_order_test.go`, with paused passes via the new hooks `inventoryBeforeApplyLock` / `inventoryAfterDecisions` (US3 scenarios 1-4):
  - (1) C1 paused, C2 applied, C1 resumes → no record write (`SaveToolApproval` spy count), no index op (index spy), `lastGoodTools.seq == C2`, StateView still has C2's hints (dispatch tier destructive).
  - (2) A pass computes `t` blocked → the operator approves `t` and the reindex completes → the pass resumes → `t` is still in the index. Because the decision and the eviction are now one section, the operator approval waits; assert the order with the hook.
  - (3) Older-with-x vs newer-without-x → no delete or resurrect of x.
  - (4) A pass paused after eligibility → `QuarantineServer(true)` → resume → shared and profile indexes have no entry for the server, including previously approved tools.
  - (5) seq 0 is applied only while `applied == 0`.
  - (6) **Regression check** (already true on main): re-applying the last-good snapshot after an approval (`seq == applied`, capture current) works.
  - (6b) **Failing-first**: after a reconnect (epoch moved) or endpoint replacement, the approval reindex does **not** re-apply the old snapshot; it re-lists. On main the old snapshot is re-applied (`tool_quarantine.go:2006-2027`).
  - (7) A retired client's capture → discarded.

  (1)-(5), (6b) and (7) must fail on main for the stated reason; (6) is a regression check. For (1), C1's write-back restores read-only hints.
- [ ] T307 [PR-3] impl: `toolApprovalServerLock{mu, applied, published}`, `lockToolApprovalsForCapture`. Extract `checkToolApprovalsLocked`. Split `applyDifferentialToolUpdate` into `applyInventoryLocked` + tail. Remove the quarantined capture's outer `WithCurrentClientOnEpoch` wrapper (`lifecycle.go:649`) and add `Manager.WithCaptureCurrent`, used inside the locked section. Store the full capture in `lastGoodTools`. Change `commitDiscoveredTools`, `discoverAndIndexToolsForServerOnce`, `captureQuarantinedToolDefinitions*` and `reindexFromLastGoodSnapshot` / `reapplyLastGoodSnapshot` to go through `applyInventory` with their capture. Add `lastGoodTools` seq. Make the sweep use `DiscoverToolsReportCaptured` and publish per server through the gate (keep `markZeroToolServersDiscovered`).
- [ ] T308 [P] [PR-3] test: `internal/runtime/quarantine_purge_lock_test.go`. `QuarantineServer(true)` purges under the lock: a pass blocked on the lock observes `serverQuarantined` and writes no index entries. Fails on main: the purge runs after unlock.
- [ ] T309 [PR-3] impl: move the storage flip + `purgeQuarantinedServerFromIndex` (shared index part) into one locked section. The profile-index refresh stays after unlock.
- [ ] T310 [P] [PR-3] test: stress `internal/runtime/inventory_stress_test.go`. Concurrent refresh, approvals, block, quarantine toggles, sweep and reindex for 2 s under `-race`. A goroutine dump on a 30 s timeout. Final invariant: every indexed tool has an approved, enabled record, and no pending/changed record is indexed. Run the existing UX-02 concurrency suite unchanged (SC-004).
- [ ] T311 [PR-3] impl: fixes found by T310 (if any). Remove dead code (`captureQuarantinedToolDefinitionsFromTools` if still unused).

### Verify and ship
- [ ] T312 [PR-3] Full lanes incl. the internal/server race lane under `-tags server`, both lints, `make build`. Live check **L3** with the SC-005 timing, evidence under `evidence/s117-pr3/`.
- [ ] T313 [PR-3] Open the PR (`Closes #1565`). Run an opencode Sol 6.1 review plus an independent Opus subagent review (concurrency; CLAUDE.md routing).

---

## Dependencies

- PR-2 is cut from `origin/main` after PR-1 merges (hard dependency; design review r1 #3). PR-1 and PR-3 have no dependency. Default order: PR-1 → PR-2 → PR-3 (O4).
- Within a PR: each `test` comes before its `impl`. T102→T103→T104→T105. T201→T202→T203→T204→T205→T206→T207→T208. T305 gates T307.
- Parallel: T102/T104/T106/T108 (different files); T201/T203/T205/T209/T211/T213/T215; T301/T303/T305/T306/T308.
