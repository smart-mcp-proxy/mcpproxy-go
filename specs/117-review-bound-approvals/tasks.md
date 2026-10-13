# Tasks: Review-Bound Approvals, Durable Baselines and Ordered Inventories (Spec 117)

**Input**: the design documents in `/specs/117-review-bound-approvals/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**TDD rule (constitution V)**: write each `test` task first. It must FAIL for the stated behavioural reason, on a build that compiles, before its paired `impl` task starts. Add a stub where a new symbol is needed, so the failure is behavioural and not a compile error (memory `verify-test-bites-build-failure`). Record the failing output in the PR description.

**Format**: `[ID] [P?] [PR] Description`. `[P]` means the task can run in parallel with other `[P]` tasks in its group (different files). The tasks are grouped by the three planned PRs. Each PR is cut from the then-current `origin/main` and merged on its own (no stacking).

---

## PR-1: Durable tool baseline (#1567), branch `117-pr1-durable-baseline`

**Declared scope**: `internal/storage` (new bucket + removal), `internal/toolreview` (value type), `internal/runtime/tool_quarantine.go` (`serverHasBaseline`, marking: inferred before the loop, discovery after it, FR-027a), `internal/runtime/lifecycle.go` (FR-027 error path in `applyDifferentialToolUpdate`, snapshot restore in `commitDiscoveredTools`, and the sweep's StateView batch omitting aborted servers, FR-027a), `internal/runtime/tool_approval_lock.go` (PR-1 `CommitServerApproval`), `internal/server/server.go` (adapter), `internal/security/scanner/service.go` (marking inside the locked commit), blind-approval copy in `ReviewScreen.vue` and `review_cmd.go`, `docs/features/security-quarantine.md`. Nothing else.

### Setup
- [x] T101 Branch from `origin/main`. Run the baseline suites once and record the timings: `go test -race ./internal/runtime/... ./internal/storage/... ./internal/security/scanner/...`.

### Storage
- [x] T102 [P] [PR-1] test: `internal/storage/tool_baseline_decision_test.go`.
  - `MarkToolBaselineDecided` then `ToolBaselineDecided` → true.
  - Idempotent: the first `decided_at` is kept, and `source` is upgraded only from `inferred`.
  - Unknown server → false.
  - An unreadable value → true (fail closed).
  - `DeleteServerToolApprovals("s")` removes the marker in the same tx (assert with a single `View` after).
  - `PruneToolApprovalsNotIn({a})` drops b's marker and keeps a's.
  - A store without the bucket reads false and does not create it on read.
- [x] T103 [PR-1] impl: `internal/storage/models.go` const, `tool_baseline_decision.go`, `bbolt.go` delete hooks, `manager.go` wrappers (data-model §5).

### Runtime
- [x] T104 [P] [PR-1] test: `internal/runtime/tool_baseline_durable_test.go` (US2 scenarios 1, 2, 4, 5), on a trusted manual-trust server with quarantine enabled.
  - (a) Drive the **full** apply path (`commitDiscoveredTools` → `applyDifferentialToolUpdate`, with the index manager), because absent records are deleted there, not in `checkToolApprovals` (`lifecycle.go:1149-1185`; design review r1 #5). Steps: baseline `[A]` (A indexed) → apply `[B]` (A's record and index entry deleted) → apply `[B]` ×2 → new Runtime on the same DB → apply `[B]`. Expect B pending throughout and B absent from the index. Dispatch refusal is asserted in a separate test, T104s (`internal/server`, exported runtime entry points only: refresh via `RefreshServerTools`, then `call_tool_destructive`), because `internal/server` imports `internal/runtime` and a runtime-package test cannot import it (design review r3 #2).
  - (b) The same with an authoritative empty apply (`[]` through the authoritative path) between A and B.
  - (c) A pre-seeded approved record without a marker → one pass → marker present with `source: inferred`.
  - (d) Delete the server records → first inventory auto-baselined.
  - (e) First inventory `[]` → no marker; then `[A]` → auto-baselined (FR-025).

  (a) and (b) fail on main: B becomes approved by `auto-baseline` on the second apply, as the live repro showed. (c) and (e) are marker-inference tests and are kept separate from the failing-first pair.
- [x] T104s [P] [PR-1] test: `internal/server/tool_baseline_durable_dispatch_test.go`. An in-process server with a stub upstream whose inventory can be swapped. Flow: baseline `[A]` → swap to `[B]` → `RefreshServerTools` ×2 → `call_tool_destructive server:B` refused, and the stub saw zero `tools/call`. Fails on main (B dispatched). It runs in the internal/server race lane under the CI skip regex.
- [x] T104f [P] [PR-1] test: `internal/runtime/tool_baseline_marker_fault_test.go` (FR-027). This is fault injection through a storage wrapper that fails `MarkToolBaselineDecided`, on an older store (approved A, no marker):
  - Apply `[B]` (wholesale replacement) → A's record is **not** deleted, B is not indexed, and the error is logged. Then heal the storage and apply `[B]` → marker written, A deleted, B pending.
  - The same with the authoritative empty inventory.

  - Both variants also assert that `lastGoodTools[server]` and `lastCaptureAt[server]` are unchanged after the failed apply (design review r7 #3).
  - **Hint update + marker fault** (FR-027a, spec review r3 #1): approved+enabled A (read-only hints) without a marker → apply `[A']` where A' has `destructiveHint: true` and the marker write fails → A's stored record is byte-identical to before (still read-only `CurrentAnnotations`, same `CurrentHash`), the StateView still carries the previous capture's A, the snapshot and stamp are unchanged, and `checkToolApprovals` made zero `SaveToolApproval` calls (the storage wrapper counts them). Heal → apply `[A']` → marker `inferred`, A's record now carries the destructive hints. Fails against a T105 that marks after the loop (record updated, marker failed).
  - **Prior-record listing fault**: a `ListToolApprovals` failure on the pre-pass read returns `errBaselineMarker` with zero record writes and no auto-baseline. Fails on main, where the listing error yields `serverHasBaseline=false` and auto-approves the inventory.
  - **Discovery-marker fault is not an abort**: a fresh trusted server, first inventory `[A]`, marker write fails → A approved (auto-baseline), the apply completes and A is indexed; the next pass `[B]` writes the `inferred` marker before the loop and leaves B pending.

  Fails on main: the swallowed error path deletes A, and the next pass auto-baselines B.
- [x] T105 [PR-1] impl: `tool_quarantine.go`.
  - `serverHasBaseline` ORs `ToolBaselineDecided` (read error → true).
  - Marking, under the lock: `inferred` before the loop when `priorByName` holds an approved or changed record; otherwise, after the loop, `discovery` when the pass approved at least one record (track it in the loop). See the FR-027a ordering bullet below.
  - Log at Info once per server when a marker is first written.
  - FR-027a ordering (spec review r3 #1): when no marker exists and `priorByName` holds an approved or changed record, write the `inferred` marker **before the loop**, ahead of every `SaveToolApproval` / `recordScanHold` / `scanApproveChange` write. A marker read error, that write's error, or a `ListToolApprovals` error on the pre-pass read returns `errBaselineMarker` before any record write. The `discovery` marker (no prior approved/changed record) stays after the loop and its failure is logged at Warn, not returned (FR-021 inference covers it).
  - FR-027: on `errBaselineMarker`, `applyDifferentialToolUpdate` (`lifecycle.go:1019-1026`) evicts the blocked set it can compute from the stored records, skips absent deletes, hash deletes and every index write, and **returns** `errBaselineMarker`, so the reactive caller publishes no StateView entry (`lifecycle.go:937-940`). The sweep's batch `RefreshToolsFromDiscovery` omits the servers whose apply aborted and keeps their previous entries. Other `checkToolApprovals` errors keep today's handling.
  - `commitDiscoveredTools` (`lifecycle.go:776-796`) publishes `lastGoodTools` and stamps `lastCaptureAt` **before** the apply. On `errBaselineMarker` it restores both previous values while still holding `captureSnapMu`, so the failed inventory is never available to the approval reindex or the sweep fallback (design review r7 #3).
- [x] T106 [P] [PR-1] test: `internal/security/scanner/service_test.go` + `internal/runtime/tool_baseline_durable_test.go` (US2 scenario 3).
  - `ApproveServer` and `ApproveServerWithBlocks` on an empty inventory write the marker (`server_approval`, `by: api`) inside the locked commit: the recording locker sees the mark between lock and unlock.
  - After the unquarantine, a pass with `[T]` leaves T pending.

  Fails on main: no marker, T auto-approved.
- [x] T107 [PR-1] impl:
  - new leaf package `internal/toolreview` (`BaselineDecision`);
  - `Runtime.CommitServerApproval` in its PR-1 form: lock, mark, `commit()`;
  - scanner `ServerApprovalCommitter` capability, used by both branches (`SaveIntegrityBaseline` and `…WithBlocks`) when present, with a fallback to today's path for fakes;
  - the adapter in `internal/server/server.go`.

  The scanner must not import `internal/storage` or `internal/runtime` (storage imports scanner).

### Copy and docs
- [x] T108 [P] [PR-1] test: vitest `frontend/tests/unit/review-screen-blind-copy.spec.ts` (the dialog text mentions that later tools wait for review) + `cmd/mcpproxy/review_cmd_test.go` (the blind prompt text).
- [x] T109 [PR-1] impl: copy changes (FR-026); `docs/features/security-quarantine.md` "Baseline" section; CLAUDE.md Recent Changes line.

### Verify and ship
- [x] T110 [PR-1] Run the full lanes (quickstart §4), both lints, `make build`. Live check **L1** (quickstart §3) with evidence under `evidence/s117-pr1/`.
- [ ] T111 [PR-1] Open the PR (`Closes #1567`) and run an opencode Sol 6.1 review (≤10 rounds; record in research.md "PR-1 review log").

---

## PR-2: Review-bound approvals (#1566), branch `117-pr2-review-binding` (requires PR-1 merged)

**Declared scope**:
- runtime: fingerprint, review field, reviewed approvals, `CommitServerApproval` / `FinishServerApproval`, the `QuarantineServer` split;
- `StaleToolReviewError` in the leaf package `internal/toolreview` (never in storage: storage imports scanner);
- the scanner reviewed approval;
- httpapi: both handlers + 409 mapping, swagger;
- CLI `review approve`;
- Web `ReviewScreen.vue` / `api.ts` / types / `reviewPresentation.ts`;
- docs.

Not in scope: Tools.vue, `security approve` CLI, MCP operations, `native/`.

### Fingerprint and review payload
- [x] T201 [P] [PR-2] test: `internal/runtime/review_fingerprint_test.go`.
  - Golden vectors (`testdata/review_fingerprint_vectors.json`).
  - It changes for a description, input schema, output schema and each of the 4 hints (nil → false → true).
  - It does not change for title, status, disabled or held evidence.
  - Two servers with identical tool definitions get different fingerprints.
  - A record with an empty `CurrentHash` gets a non-empty, definition-sensitive fingerprint.
  - The prefix is `rv2-` (`rv1-`, which hashed the storage `CurrentHash` in place of the fields, was retired in Sol r2).
- [x] T202 [PR-2] impl: `internal/runtime/review_fingerprint.go` (data-model §1).
- [x] T203 [P] [PR-2] test: `internal/runtime/review_test.go` + `internal/httpapi/review_test.go`. Every tool in `GET /servers/{id}/review` has `current_hash == reviewFingerprint(record)`. It is absent when there are no records. Fails: the field does not exist.
- [x] T204 [PR-2] impl: `ReviewTool.CurrentHash` in `review.go` (data-model §2); `frontend/src/types/api.ts`.

### Runtime validation
- [x] T205 [P] [PR-2] test: `internal/runtime/tool_approval_review_test.go` (US1 scenario 4 + edge cases).
  - `ApproveToolsReviewed` with a matching fingerprint → approved.
  - Changed definition → `*StaleToolReviewError{Changed}` and the record unchanged (status, hashes).
  - Hint-only change → Changed.
  - Name missing from expected → Unreviewed.
  - Empty value → Unreviewed.
  - A name without a record → not an error, reported missing.
  - `expected == nil` → identical to `ApproveTools` (table shared with the existing tests).
  - `ApproveAllToolsReviewed` validates pending+changed.
  - Bound request with a duplicate name → validated once, written once, counted once. Unbound duplicates keep today's behaviour (design review r3 #5).
  - The check and write are atomic: `toolApprovalReadHook` mutates the record (simulated capture) after the handler read but before the lock → still stale, because validation re-reads under the lock.
- [x] T205b [P] [PR-2] test: `internal/runtime/tool_quarantine_changed_tracking_test.go` (FR-017).
  - Approved H0 → capture H1 (changed) → capture H2 → the record's current fields describe H2, `Previous*` and `ApprovedHash` are still H0, and the review fingerprint is H2's.
  - `ApproveToolsReviewed(fp(H1))` → 409.
  - Hint-only variant (design review r5 #1): approved H0 → changed H1 (read-only hints) → the same H1 definition with destructive hints → the record's annotations are destructive, and `ApproveToolsReviewed(fp(H1 read-only))` → 409.
  - A revert to H0 still restores approved.

  Fails on main: the record still shows H1.
- [x] T205c [P] [PR-2] test: `internal/runtime/tool_quarantine_output_schema_test.go` (FR-017a, spec review r4 #2). Seed an approved read-only record at the pre-output-schema `HashSchemaVersion` on a quarantined server → capture the same description and input schema with `destructiveHint: true` (the migration branch) → the backfill succeeded (`HashSchemaVersion` is current, the rug-pull policy unchanged: still approved), and the record's `CurrentAnnotations` and the review payload show the destructive hints. `ApproveToolsReviewed` / a bound server approval carrying the fingerprint computed from the read-only hints → 409 out of date; the fingerprint of the destructive hints succeeds. A variant with a different description still routes to the rug-pull (`changed`) path. Fails on main: the backfill keeps the read-only `CurrentAnnotations`.
- [x] T205d [P] [PR-2] test (Sol r2): `internal/runtime/review_binding_r2_test.go`. (a) Two contracts with one storage hash, `(D, I, O)` and `("D|I", O, none)`: capturing B after A makes A's binding out of date in `ApproveToolsReviewed`, `ApproveAllToolsReviewed` and `CommitServerApproval`, which write nothing; a changed record follows B. (b) A changed record whose next capture spells out a default hint (`destructiveHint` unset → `true`) records the raw hint and refuses the old binding. (c) Approve-all over a pending and a changed record: a missing or stale binding for the changed one refuses the batch with no writes; a matching one approves both. (d) A capture injected between validation and the writes (seam `reviewBindingValidated`) queues on the lock and lands after the approval, for the named path and approve-all.
- [x] T206 [PR-2] impl: FR-017 in the `changed` branch of `checkToolApprovals` (refresh the current fields when the hash **or the hints** differ from the stored current ones, i.e. whenever the review fingerprint would change; keep `Previous*`); FR-017a in the output-schema backfill (`tool_quarantine.go:572-601`: set `CurrentAnnotations` from the capture before the save; a failed save counts as a failed record write for FR-018), plus an audit comment listing every recording branch and its hint refresh; `internal/toolreview/errors.go` (leaf package); `staleReviewedRecords`, `ApproveToolsReviewed`, `ApproveAllToolsReviewed` in `tool_quarantine.go` (validation and approval in one locked section; `notifyToolsApproved` after unlock).
- [x] T207 [P] [PR-2] test: `internal/runtime/server_approval_commit_test.go` (US1 scenarios 1-3, 5; FR-011/FR-012).
  - Bound mismatch → error, and: `commit` was not called, no marker written, storage still quarantined, records unchanged.
  - **Composition regression** (already green on top of PR-1): bound `{}` on an empty review → success; then a pass with `[T]` after `FinishServerApproval` → T pending.
  - The **failing-first** pair for T208 is the post-commit promotion window above: unbound, a record filed between commit and finish stays pending.
  - Bound match → promoted == the validated pending set, blocks applied by `commit`, storage unquarantined **before** unlock (assert from inside a recording `commit` that runs last and from a hook after the flip).
  - An approved+enabled tool with a hint change → Changed (the activation set).
  - Unbound → promotes the pending records present at commit only. A record filed by a pass after `CommitServerApproval` returns but before `FinishServerApproval` stays pending. On main, `approveBaselineToolsForServer` promotes it; that is the failing assertion.
  - `FinishServerApproval` does not call `approveBaselineToolsForServer` (spy).
  - A missing block target under a bound request → `MissingBlocks`, nothing written.
- [x] T208 [PR-2] impl: `CommitServerApproval`, `FinishServerApproval` in `tool_approval_lock.go`. Split `QuarantineServer` into flip + `finishQuarantineChange(server, quarantined, promote bool)` in `lifecycle.go` (the non-review path keeps `promote=true`). This extends PR-1's `CommitServerApproval` (the mark stays the first write after validation).
- [x] T209 [P] [PR-2] test: `internal/security/scanner/service_test.go`.
  - `ApproveServerReviewed` uses `ReviewedServerApprover` when present and passes `expected` through.
  - The dangerous gate still runs first (its message contains "dangerous").
  - A stale error propagates unchanged (`errors.As`).
  - `ApproveServer`/`WithBlocks` call it with nil.
  - A fake without the capability keeps the legacy path.
- [x] T210 [PR-2] impl: scanner `ApproveServerReviewed`, interface; adapter in `internal/server/server.go`.

### REST
- [x] T211 [P] [PR-2] test: `internal/httpapi/tool_quarantine_test.go` + `security_scanner_test.go`.
  - Body decoding: absent / `null` → unbound; `{}` → bound.
  - 409 body exactly per contracts/rest-approval-binding.md: `error_code`, `stale_tools` lists always present, the message contains "out of date" and not "dangerous".
  - A bound missing block → 409 (an unbound one keeps 400).
  - Success bodies unchanged.
  - The controller receives `expected`.
- [x] T212 [PR-2] impl: `security_scanner.go`, `server.go` handlers, `review_errors.go`; controller interface additions; swagger annotations + `oas/swagger.yaml` regeneration.

### Clients
- [x] T213 [P] [PR-2] test: vitest `frontend/tests/unit/review-screen-binding.spec.ts`.
  - The approve request carries the click-time hashes of all reviewed tools.
  - Not-captured / empty → `{}`.
  - No hashes anywhere → no key.
  - A force retry after "dangerous" re-sends the original binding even after a reload.
  - A per-tool approve sends `{name: hash}`.
  - A 409 out of date → the review reloads, a notice shows the message, the force dialog is not opened, and explicit choices are kept for tools still present.
- [x] T214 [PR-2] impl: `api.ts` (`securityApprove(server, force, block, expectedHashes?)`, `approveTools(server, tools, expectedHashes?)`), `reviewPresentation.ts` (`expectedHashes(tools, review)` mirroring the tray's `ReviewPresentation.expectedHashes`), `ReviewScreen.vue`.
- [x] T215 [P] [PR-2] test: `cmd/mcpproxy/review_cmd_test.go`.
  - Quarantined path body includes `expected_hashes` (all tools, or `{}` when empty).
  - Trusted path: `tools` → expected for those tools; default → `approve_all` + expected for the pending+changed tools.
  - A 409 out of date → exit 1, the message + hint line, the JSON envelope under `-o json`.
  - **Failing-first (design review r2 #1)**: save `review show -o json` at H1, change the stub server to H2, then run `review approve --from-review file --yes` → the request carries H1 and the CLI exits 1 on the stub's 409. Main has no `--from-review`, and its request carries no binding.
  - The interactive prompt lists the reachable tools with tier and status before asking.
  - Servers A and B with identical definitions: `review approve B --from-review A.json --yes` → exit 1, and the stub sees **zero** approve requests. A malformed or error-envelope file → exit 1, zero requests (design review r6 #2).
- [x] T216 [PR-2] impl: `review_cmd.go` (`reviewToolState.CurrentHash`; `--from-review`; prompt listing; body builders; error mapping); `docs/cli/review-commands.md`.

### Docs, verify, ship
- [x] T217 [PR-2] docs: `docs/api/rest-api.md` (review field, both approve bodies, 409, compatibility matrix), `docs/cli/review-commands.md`, `docs/features/security-quarantine.md`; CLAUDE.md Recent Changes.
- [ ] T218 [PR-2] Full lanes, both lints, `make build`. Live check **L2** (all 8 steps; step 8 only once #1572 is merged), evidence under `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/117pr2/` (the remediation evidence tree, not `evidence/s117-pr2/`). Playwright review sweep per `docs/development/web-ui-verification.md`. Status (Sol r1): lanes, lints, `make build`, L2 steps 1-7 and the Web Playwright stale/force sweep are done. Step 8 ran live against the fixed core through the merged tray client's own code (`APIClient`, review decoding, `ReviewPresentation.approvalDecision`/`toolApprovalExpected`/`staleReviewMessage`, the calls `ReviewQueueView.approve`/`approveTool` make; `L2-8-tray-fix.txt`): 409 out of date, nothing approved, reload shows the new definition, the reloaded binding approves. **Open:** the step-8 run on the rendered review sheet itself (a dev tray bundle swapped in for the production tray), left to the lead because it quits the operator's running tray and its managed core.
- [ ] T219 [PR-2] Open the PR (`Closes #1566`; note that #1572 becomes effective) and run an opencode Sol 6.1 review. This is a security PR: escalate to codex Sol only if Sol misses a defect twice.

---

## PR-3: Apply tool inventories in capture order (#1565), branch `117-pr3-capture-order`

**Declared scope** (spec review r2 #2: matches T301-T313 and the plan.md PR-3 inventory):
- `internal/upstream/managed` (capture, retire), `internal/upstream/manager.go` (captured sweep, retire on replace/remove);
- `internal/runtime` (lock state, `applyInventory`, sweep publish, `QuarantineServer` purge, `lastGoodTools` seq, the quarantined capture via `applyInventory`, `persistServerIneligibleLocked` and the disable/quarantine writers routed through it, FR-018 `reviewDirtyAt` + atomic mirror + `ToolReviewDirty` accessor and the guard in every reachability transition);
- `internal/runtime/supervisor` (drop retained tools of disabled/quarantined servers in reconcile and the StateView copy);
- `internal/toolreview/errors.go` (`ErrReviewNotRecorded`);
- `internal/server`: the FR-018 dispatch refusal (`tool_gate.go` `reviewDirty` field; dispatch refusal builders in `mcp.go`, `mcp_code_execution.go`, `mcp_routing.go`; `mcp_direct_callability.go` `reviewDirty` decision field + refusal kind, dispatch-only; `access_chain.go` explainer step) and `Server.UpdateServer` (PATCH `quarantined`/`enabled` writes through the locked writer);
- `internal/httpapi`: `ErrReviewNotRecorded` → HTTP 503 mapping on every approve/enable handler;
- `scripts/check-quarantine-writers.sh` + its CI wiring.

Wire changes are limited to that new 503 error response (and the MCP tool-error text); no new request or response fields. No UI.

### Capture identity
- [x] T301 [P] [PR-3] test: `internal/upstream/managed/listtools_capture_test.go` (US3 scenario 5).
  - The leader's capture seq is strictly greater than any capture whose leader started earlier.
  - A coalesced waiter returns the leader's capture (same seq and epoch).
  - The tool-count refresh publishes a capture into its flight. The health check stays ping-only and produces none (design review r2 #6).
  - **Failing-first (design review r2 #4)**: leader 1 finishes and closes its channel; leader 2 acquires and resets before leader 1's awakened waiter reads → the waiter still gets leader 1's tools and capture. On main it reads nil or leader 2's result.
  - `ListTools` is unchanged.
- [x] T302 [PR-3] impl: `capture.go`; in `client.go`, an immutable per-flight result (`listFlight`) replaces `listToolsLastResult`/`listToolsWaitCh`, and `runListToolsAsLeader` stamps seq next to `listEpoch` into its flight; `ListToolsCaptured`.
- [x] T303 [P] [PR-3] test: `internal/upstream/managed/retire_test.go` + `internal/upstream/manager_test.go` (US3 scenario 6).
  - `Retire` makes `WithConnectionEpoch` and `CaptureIsCurrent` false.
  - `AddServerConfig` with a changed command retires the old client before the map entry is replaced.
  - `RemoveServer` retires.
  - **Deadlock test** (design review r8 #1): retirement during `AddServerConfig`, run concurrently with a `WithCaptureCurrent` callback, completes under a timeout. The callback performs no manager or supervisor lookup; a static hook asserts that `GetClient`/`DiscoveryGeneration` are never called while the client's `epochMu` is held.
  - `DiscoverToolsReportCaptured` returns one capture per listed server, each from that server's own list.
- [x] T304 [PR-3] impl: `Retire`, `CaptureIsCurrent`, manager changes (retire after releasing `manager.mu`, still under `captureMu` W), `DiscoverToolsReportCaptured`.

### Ordered apply
- [x] T305 (superseded in part: see research.md "PR-3 implementation notes" items 2 and 6; the deadlock coverage is T310 with quarantine-toggle commits) [P] [PR-3] test: `internal/runtime/inventory_lock_order_test.go`.
  - (a) **Composition regression test** (design review r2 #5: main's apply does not take `captureMu`, so this cannot fail on main). Deadlock test (design review r1 #2): run concurrently, with hooks forcing the overlap:
    - a normal apply holding `captureSnapMu` + the approval lock and waiting for `captureMu`;
    - a quarantined capture;
    - an endpoint replacement (`AddServerConfig`).

    It fails against an intermediate T307 that widens the apply but keeps the quarantined capture's outer wrapper (record that run in the PR), and passes once the wrapper is removed.
  - (b) **Regression check** (already true on main; not failing-first): supervisor `RefreshServerToolsFromDiscovery` called under a held approval lock completes without re-entering the runtime (`supervisor.go:1214-1230`).
  - (c) **Regression check**: no function reachable from `approveToolsAndNotify` or `applyInventory`'s locked phase takes `captureSnapMu`. If PR-2 is already merged, also check `CommitServerApproval`. This is a composition check, not a PR-3 dependency.
- [x] T306 [P] [PR-3] test: `internal/runtime/inventory_capture_order_test.go`, with paused passes via the new hooks `inventoryBeforeApplyLock` / `inventoryAfterDecisions` (US3 scenarios 1-4):
  - (1) C1 paused, C2 applied, C1 resumes → no record write (`SaveToolApproval` spy count), no index op (index spy), `lastGoodTools.seq == C2`, StateView still has C2's hints (dispatch tier destructive).
  - (2) A pass is paused inside the locked phase, after computing `t` blocked. Start the operator approval of `t` **asynchronously**, and assert it is still blocked after 200 ms because the pass holds the lock. Release the pass, then await the approval and its reindex, and assert that `t` is indexed and approved at the end (design review r3 #3).
  - (2m) **Main-compatible failing-first** for the old decision-to-index window (design review r4 #2). A full discovery pass holds `captureSnapMu`, so its reindex cannot finish while the pass is paused. The test therefore does **not** await the reindex:
    1. Pause the pass at the hook placed between its approval decisions and its index writes.
    2. Start `ApproveTools(t)` **asynchronously** (design review r5 #2).
       - On main the approval lock is free at that point, so the approval completes while the pass is paused.
       - After T307 the hook sits inside the locked section, so the approval waits. Assert it has not completed after 200 ms.
    3. Resume the pass, then await the approval. Hold the reindex with the `localReindexAfterSnapshot` hook.
    4. Assert that `t` is approved in storage, and check the index:
       - on main, `t` was evicted by the older decision (the failing assertion);
       - after T307, the pass finished first and the approval followed. Release the reindex, await it, and assert `t` is indexed.
  - (3) Older-with-x vs newer-without-x → no delete or resurrect of x.
  - (4) A pass paused after eligibility → `QuarantineServer(true)` → resume → shared and profile indexes have no entry for the server, including previously approved tools.
  - (5) seq 0 is applied only while `applied == 0`.
  - (5r) **Composition regression** (design review r8 #3, r9 #4; already true on main, must stay true once the (5e) fence exists — record its failure against an implementation with (5e) but without the `definitions` rule): non-empty C1 applied → lenient-empty C2 (observation) → the operator approves held tool `t` → `t` is indexed. Then the operator blocks approved tool `u` → `u` is evicted. Both go through the snapshot reconciliation (`snapshot.seq == definitions`) without moving `applied`. Against an implementation of (5e) only, they fail (stale rejection).
  - (5e) **Failing-first** (FR-033a, design review r7 #2): non-empty C1 is paused; a newer C2 lists zero tools on the lenient path (reactive, and sweep) and finishes; then C1 resumes → C1 writes nothing. On main C1 writes records, index entries and StateView.
  - (6) **Regression check** (already true on main): re-applying the last-good snapshot after an approval (`seq == applied`, capture current) works.
  - (6b) **Failing-first**: after a reconnect (epoch moved) or endpoint replacement, the approval reindex does **not** re-apply the old snapshot; it re-lists. On main the old snapshot is re-applied (`tool_quarantine.go:2006-2027`).
  - (7) A retired client's capture → discarded.
  - (8) **Regression check, with a timeout**: applying an inventory that removes an indexed tool completes; it does not self-deadlock on `deleteToolApprovalLocked` (design review r2 #2).
  - (9c) **Failing-first**: disable races a paused pass, for each writer: `EnableServer(false)`, `BulkEnableServers`, PATCH `enabled:false`, config reload, and restart persist. After the purge, a **supervisor reconciliation** runs, and the StateView must still have no tools for the server (design review r9 #3; on main the supervisor re-copies retained `existing.Tools`, `supervisor.go:689-710,795-805`). After the pass resumes, the shared and profile indexes have no entry for the server and the StateView has no tools for it. On main the paused pass re-indexes it (design review r6 #1).
  - (9b) **Failing-first**: PATCH `quarantined: true` on an indexed server → the shared and profile indexes are empty afterwards. On main the entries stay, because the reload sees no transition. The restart-persist variant is the same (design review r3 #1).
  - (9) **Failing-first**: config-driven quarantine (edit `quarantined: true` in the config file → `LoadConfiguredServers`) races a paused pass → after the pass resumes, the shared and profile indexes have no entry for the server (design review r2 #3).

  (1)-(5), (6b) and (7) must fail on main for the stated reason; (6) is a regression check. For (1), C1's write-back restores read-only hints.
- [x] T307 [PR-3] impl: `toolApprovalServerLock{mu, applied, published}`, `lockToolApprovalsForCapture`. Extract `checkToolApprovalsLocked`. Split `applyDifferentialToolUpdate` into `applyInventoryLocked` + tail. Inside the locked phase, replace every helper that takes the approval lock itself with its lock-free form (design review r2 #2): `deleteToolApprovalLocked` (`lifecycle.go:1179`, `tool_approval_lock.go:86-88`) becomes a direct `storageManager.DeleteToolApproval`, and `pruneAbsentUndecidedApprovals` runs unlocked-internally. Audit every call reachable from the locked phase for `lockToolApprovals`; T306(8) pins it. Remove the quarantined capture's outer `WithCurrentClientOnEpoch` wrapper (`lifecycle.go:649`) and add `Manager.WithCaptureCurrent`, used inside the locked section. Store the full capture in `lastGoodTools`. Change `commitDiscoveredTools`, `discoverAndIndexToolsForServerOnce`, `captureQuarantinedToolDefinitions*` and `reindexFromLastGoodSnapshot` / `reapplyLastGoodSnapshot` to go through `applyInventory` with their capture. Add `lastGoodTools` seq. Make the sweep use `DiscoverToolsReportCaptured` and publish per server through the gate (lenient-empty results go through the gated section as an observation that advances `applied` without writes, and `markZeroToolServersDiscovered` is called from inside it; FR-033a).
- [ ] T306d (deferred to a follow-up: research.md "PR-3 implementation notes" item 6; the fence part is in T307) [P] [PR-3] test: `internal/runtime/tool_capture_persist_fault_test.go` (FR-018; design review r8 #2, r9 #1-#2). Fault injection is a storage wrapper failing `SaveToolApproval` for one tool:
  - (a) Review H1 (pending) → capture C3 with H2 and the write failing → snapshot and stamp unchanged, `applied == C3`, server dirty at C3.
  - (b) An older in-flight capture C2 (H1, seq < C3) resumes → rejected as stale; the server is still dirty.
  - (c) The operator blocks another tool → the reindex re-lists instead of replaying the retained snapshot, and the server is still dirty after a replay attempt.
  - (d) While dirty: `ApproveTools` (legacy, called directly), `ApproveAllTools`, the MCP `approve_tool` path and the non-review unquarantine promotion all refuse with `ErrReviewNotRecorded` / 503, and nothing is promoted.
  - (d2) Approved+disabled H1 → a failed H2 capture → `SetToolEnabled(true)` and `SetAllToolsEnabled(true)` both refuse, the stub upstream sees zero dispatches, and the tool can still be disabled or blocked. After a fresh successful capture, enable works (design review r10 #1). With PR-2 merged, a bound fp(H1) returns 409.
  - (e) Storage recovers and a fresh capture C4 > C3 records H2 → dirty clears, fp(H1) returns 409 (definition changed), and fp(H2) succeeds.
  - (f) **Dispatch while dirty** (spec review r1 #1): approved+enabled read-only tool t from C1 (StateView published) → C2 lists t with `destructiveHint: true` and a changed description, and its record write fails → a read-scoped caller calls t through `call_tool_read`, the direct surface (`server__t`) and `code_execution` (`call_tool("server:t")`) → every call is refused with the `ErrReviewNotRecorded` text, and the stub upstream sees **zero** calls. Another approved tool u of the same server, whose own write succeeded, is refused too (whole-server rule). `tools/list` still lists t and u. After storage recovers and a complete C3 > C2 records the change, t is `changed` (not callable) and u is callable again. Fails on main, where t is dispatched with the stale read-only tier.
  - (f2) **Direct surface stays listed while dirty** (spec review r2 #1; `internal/server/mcp_direct_review_dirty_test.go`): a read-scoped agent token on `/mcp/all`; fail a capture write for an approved read-only tool t → `tools/list` on `/mcp/all` still lists `server__t`, `describe_tool("server__t")` still describes it, and calling `server__t` returns the `ErrReviewNotRecorded` text as a tool error (not unknown-tool / -32602, not a hidden-tool error); the stub upstream sees zero calls. Guards against folding dirty into `directCallabilityDecision.callable`.
  - (g) **Lenient empty does not clear dirty** (spec review r1 #2): failed H2 capture at F → a lenient-empty result at F+1 (reactive path, `authoritative=false`) → `applied == F+1`, `reviewDirtyAt == F`, and `ApproveTools`, `SetToolEnabled(true)` and dispatch all still refuse. An authoritative empty capture at F+2 whose absent-record removal completes clears dirty; a fresh non-empty capture at F+2 that records everything clears it too (two sub-cases).
  - (h) **Marker abort composes with the fence** (PR-1/PR-3 composition, spec review r3 #1; extends T104f; runs in whichever of PR-1 and PR-3 merges second, default PR-3): older store with approved+enabled A (read-only), no marker. Capture C1 (`[A]` read-only) is paused before the apply lock. Capture C2 > C1 lists A with `destructiveHint: true` and the marker write fails → assert zero record writes, `applied == C2`, `reviewDirtyAt == C2`, and a read-scoped `call_tool_read server:A`, `server__A` and `code_execution` are refused with the `ErrReviewNotRecorded` text while the stub upstream sees **zero** calls. Resume C1 → it is rejected as stale and writes nothing (A's record still read-only only because C2 wrote nothing; C1 cannot replay over it). Heal storage; a fresh capture C3 > C2 records A' → dirty clears, the marker exists, and (with PR-2 merged) a bound approve carrying fp(A) from before C2 returns 409, while fp(A') succeeds.
  - (i) **Failure between record persistence and publication** (spec review r4 #1): approved+enabled read-only tool t; capture C1 publishes it (StateView read-only). C2 lists t with `destructiveHint: true` (same description and schemas, so the approval hash is unchanged) and its record write fails → dirty at C2. Storage heals; C3 > C2 records the destructive hints (the record write succeeds) but `BatchIndexTools` fails for the annotations-only refresh (fault injection through the index seam added in T309d) → `applied == C3`, `reviewDirtyAt == C3` (dirty stays set and moves up), the StateView still carries C1's read-only hints, snapshot and stamp are unchanged, and a read-scoped `call_tool_read server:t`, `server__t` and `code_execution` are refused with the `ErrReviewNotRecorded` text while the stub upstream sees **zero** calls. A clean-server variant: no C2, C3's index write fails directly → dirty at C3, same refusal. Heal the index; C4 > C3 publishes the destructive hints → dirty clears, and a read-scoped call is refused by the tier check (destructive) while `call_tool_destructive` dispatches. Fails against an implementation that clears dirty at record persistence.
  - (j) **Removal recovery** (FR-032a, spec review r5 #1): C1 indexes approved tool x (and approved y). C2 lists only y; the storage wrapper fails `DeleteToolApproval` for x after the index delete of x succeeded → dirty at C2, x's record still stored. Heal storage. Sub-case 1: C3 > C2 lists the same `[y]` → x's record and tool hash are deleted **before** dirty clears, `review show` no longer lists x, and a reappearing x with the old hash is pending (not approved). Sub-case 2: an authoritative empty C3 → the same, and y's record follows the empty rule. Sub-case 3 (retention): a pending, a changed and a user-disabled absent record, and a pre-105 collapsed alias of a served `ns:erase`, all survive C3. Sub-case 4 (restart): the same partial failure, then a restart with a fresh runtime over the same store → the first complete capture removes x. Sub-cases 1, 2 and 4 fail on main, where x's record survives every later pass.
  - (k) **Publication failure or rejection** (FR-018/FR-033, spec review r5 #2): approved tool t published by C1. C2 records and indexes successfully, then the StateView publish is rejected (a supervisor seam returning `published == false, err == nil`; second variant: an error) → `applied == C2`, `reviewDirtyAt == C2`, and `lastGoodTools`, the capture stamp, `definitions` and `published` all still equal C1's values; a review read pairs C1's snapshot with C1's stamp; the approval reindex re-lists instead of replaying. C3 > C2 publishes → all four advance to C3 and dirty clears. Fails against an implementation that updates the snapshot before the publish.
  - The same for approved → changed records. The changed → changed-again variant needs FR-017, so it is a **composition test**, run only once PR-2 is merged (PR-2 is cut before PR-3 by default). Without PR-2, a changed record is not rewritten on recapture, so there is no failed write to fence; that is main's frozen-record behaviour, documented in FR-017 (design review r10 #2).

  (a), (b), (d), (d2) and (f) fail on main, where the approval of H1 succeeds after the failed write and t is dispatched at its stale tier. (g) is a regression check for the new clearing rule. When PR-2 is merged, the 409 assertion is a composition check.
- [x] T308 (already true on main since Spec 116 FR-008: the quarantine commit purges under the approval lock and the D15 check rejects the paused pass) [P] [PR-3] test: `internal/runtime/quarantine_purge_lock_test.go`. `QuarantineServer(true)` purges under the lock: a pass blocked on the lock observes `serverQuarantined` and writes no index entries. Fails on main: the purge runs after unlock.
- [ ] T309d (deferred to a follow-up, see research.md "PR-3 implementation notes" item 6) [PR-3] impl: FR-018. `errCaptureIncomplete`, and PR-1's `errBaselineMarker` handled as the same incomplete capture (fence, dirty, no publish; FR-027a, spec review r3 #1); `reviewDirtyAt` in the per-server lock state; the fence advance on failure; the guard in every locked reachability transition (`approveToolsLocked`, `ApproveAllTools`, `approveBaselineToolsForServerCount`, `CommitServerApproval` when present, `setToolEnabledNoEmit` enable of an approved record, `SetAllToolsEnabled`, toggle-synthesized approvals; disable and block stay allowed); `toolreview.ErrReviewNotRecorded` → 503 in httpapi and a tool error on MCP; reindex re-lists while dirty. Every error of the locked apply after `checkToolApprovals` and before the StateView publish (deletes, eviction, `BatchIndexTools`, snapshot, publish) is mapped to the same incomplete-capture handling: fence at its seq, dirty set or moved up to it, nothing published (spec review r4 #1). A publication with `published == false` and no error is a failure too; the snapshot, stamp, `definitions` and `published` are staged and committed only after an accepted publication (research D10 steps 6-8, spec review r5 #2), with a narrow supervisor-publish seam for fault injection. Removal (FR-032a, spec review r5 #1): the workset is the index-derived set plus absent `approved && !Disabled` records (alias and pending/changed/disabled retention unchanged), deleted with `DeleteToolApprovalIf`, and every workset delete error becomes a tail failure instead of a debug log. Add a narrow index seam for fault injection (an unexported `toolIndexWriter` interface over `BatchIndexTools`/`DeleteTool` that `*index.Manager` satisfies, or a `toolIndexBatchHook` test hook; no new dependency). Clearing: only a complete capture with seq > F (non-empty fully recorded, indexed and published, or authoritative empty with removal completed and published), done in the publish step after it succeeded, never at record persistence; the lenient-empty observation path advances `applied` without touching `reviewDirtyAt`. Dispatch: an atomic `reviewDirtyAt` mirror written under the lock, a `Runtime.ToolReviewDirty(server)` accessor, and a `reviewDirty` field in `evaluateExactToolGate`, orthogonal to `class`, that makes only the dispatch predicate `callable()` false (variants, `handleCallTool`, code execution) with the `ErrReviewNotRecorded` text. On the direct surface the verdict is a separate `directCallabilityDecision.reviewDirty` that MUST NOT feed `callable` (listing, call-time filter, describe eligibility); only `directToolCallabilityBlockWithReason` refuses on it (spec review r2 #1). The access explainer reports it as a failed `tool_approval` step. Listing and description are unchanged. Add a guard test that fails if a listing/visibility function (`filterDirectToolsForAgentCallability`, `directEntryCallable`, `describeGateReason`, `classifyServerToolStatus`, the retrieve filter) reads `reviewDirty` or `toolGate.callable()`.
- [x] T309 (superseded by Spec 116: every ineligible writer is a Runtime.Commit that stores and purges under the approval lock; the supervisor retained-tools change is deferred, item 6) [PR-3] impl: move the storage flip + `purgeQuarantinedServerFromIndex` (shared index part) into one locked section, as `persistServerQuarantinedLocked`, used by every quarantined-record writer: `QuarantineServer(true)`, the `LoadConfiguredServers` newly-quarantined branch (save storage first, then purge), `Server.UpdateServer` when PATCH sets `quarantined: true`, and the restart persist in `lookupServerConfigForRestart`. A quarantined apply also purges the shared index entries (self-heal). Supervisor (design review r9 #3): reconciliation drops the retained `Tools` of a disabled or quarantined server, and the StateView copy publishes none for it (`supervisor.go:689-710,795-805`). Re-enabling restores them only through a fresh capture. Generalise to `persistServerIneligibleLocked`, and route the disable writers through it: `EnableServer(false)` (store + purge under the lock, replacing the async purge) and `BulkEnableServers` (one lock per server, taken in sorted name order) (design review r6 #1). Also route the disable writes of REST PATCH, config reload and the restart persist, regardless of quarantine (design review r7 #1). The CI guard script also covers `EnableUpstreamServer` and every `SaveUpstreamServer` that can store `Enabled=false`. Add `scripts/check-quarantine-writers.sh` (pattern of `scripts/check-no-unquarantine-callers.sh`), which fails CI on a new `SaveUpstreamServer`/`QuarantineUpstreamServer` call outside the allowlist (design review r3 #1). The profile-index refresh stays after unlock.
- [x] T310 [P] [PR-3] test: stress `internal/runtime/inventory_stress_test.go`. Concurrent refresh, approvals, block, quarantine toggles, sweep and reindex for 2 s under `-race`. A goroutine dump on a 30 s timeout. Final invariant: every indexed tool has an approved, enabled record, and no pending/changed record is indexed. Run the existing UX-02 concurrency suite unchanged (SC-004).
- [x] T311 [PR-3] impl: fixes found by T310 (if any). Remove dead code (`captureQuarantinedToolDefinitionsFromTools` if still unused).

### Verify and ship
- [x] T312 [PR-3] Full lanes incl. the internal/server race lane under `-tags server`, both lints, `make build`. Live check **L3** with the SC-005 timing, evidence under `evidence/s117-pr3/`.
- [ ] T313 (PR opened; the cross-model review is left to the lead) [PR-3] Open the PR (`Closes #1565`). Run an opencode Sol 6.1 review plus an independent Opus subagent review (concurrency; CLAUDE.md routing).

---

## Dependencies

- PR-2 is cut from `origin/main` after PR-1 merges (hard dependency; design review r1 #3). PR-1 and PR-3 have no dependency. Default order: PR-1 → PR-2 → PR-3 (O4).
- Within a PR: each `test` comes before its `impl`. T102→T103→T104→T105. T201→T202→T203→T204→T205→T206→T207→T208. T305 gates T307.
- Parallel: T102/T104/T106/T108 (different files); T201/T203/T205/T209/T211/T213/T215; T301/T303/T305/T306/T308.
