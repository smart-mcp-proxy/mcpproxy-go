# Tasks: Config Commit Consistency, Atomic Removal and Upstream Retirement

**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md), [data-model.md](data-model.md), [quickstart.md](quickstart.md), [contracts/surfaces.md](contracts/surfaces.md)

**Format**: `[ID] [P?] [Story] Description`. `[P]` means it can run in
parallel with other `[P]` tasks in the same group (different files). The
stories are US1 (PR1), US2 (PR2), US3 (PR3) and US4 (PR4).

**TDD rule**: in each PR, the "Tests first" tasks are written and committed
**failing** against the parent branch before any implementation task in that
PR. The failing output goes into the PR's evidence file. A test that passes
before the fix is either vacuous or the window is already closed, and has to be
redesigned (the r8-BC/r9-D lesson).

**Scope rule**: each PR implements only its own tasks. Review findings that
belong to a later PR are added to that PR's task list and are not fixed in
place.

---

## PR1: Commit critical section and generations (US1; #1558, #1560, root of #1559; B1)

Branch `116-pr1-config-commit` from `main`.

### Setup

- [ ] T001 [US1] Add test seams (nil in production): `commitHooks`, `reconcileEntry` (at the `LoadConfiguredServers` entry on `main`), `beforeCommit`, `beforeApprovalLock`, `discoveryBeforeIndex`, `restPatchAfterParse`, `createAfterConfigRead` (`config_cow.go:93`)
- [ ] T002 [P] [US1] Add `scripts/check-upstream-write-sites.sh` (allowlist: `internal/storage/**`, `internal/runtime/commit*.go`, `*_test.go`) and wire it into `.github/workflows/unit-tests.yml`. It is expected to FAIL until T030

### Tests first (must fail on `main`)

- [ ] T010 [P] [US1] `internal/runtime/commit_reconcile_race_test.go`: T1.1 (stale reconcile vs REST add of B with approvals, block, history), with an `ApplyConfig`-removing-A variant
- [ ] T011 [P] [US1] `internal/runtime/commit_apply_test.go`: T1.2 (ApplyConfig quarantine+disable A, then B toggle; assert all stores; no async reconcile goroutine)
- [ ] T012 [P] [US1] `internal/httpapi/patch_commit_race_test.go` + `internal/server/mcp_patch_commit_race_test.go`: T1.3 (parked PATCH vs newer trust_mode/allowlist/enabled/env/header edits)
- [ ] T013 [P] [US1] `internal/runtime/commit_quarantine_race_test.go`: T1.4 (reconcile holding an explicit false vs `QuarantineServer(true)`)
- [ ] T014 [P] [US1] `internal/server/create_commit_race_test.go`: T1.5 (parked add vs ApplyConfig profiles edit)
- [ ] T015 [P] [US1] `internal/server/mcp_quarantine_index_test.go`: T1.6 (B1: MCP `quarantine_server` purges the index before returning)
- [ ] T016 [P] [US1] `internal/server/shared_authbroker_patch_test.go`: T1.7 (REST and MCP patch keep `Shared` and `AuthBroker`)
- [ ] T017 [P] [US1] `internal/runtime/configsvc/generations_test.go`: generation rule table (data-model §2), incarnation kept on update and reset on recreate, `SeedVersion` monotonic (T1.9)
- [ ] T018 [P] [US1] `internal/storage/server_commit_test.go`: `ApplyServerCommit` is all-or-nothing (inject an error on the 2nd row → no row changed), writes the meta generation, stamps Generation/Incarnation
- [ ] T018a [P] [US1] `internal/runtime/commit_bootstrap_test.go`: T1.10 (boot with an empty storage and manager repairs the projection and schedules every enabled server; an unchanged `SaveConfiguration` still writes the file; restart keeps generations of unchanged rows)
- [ ] T018b [P] [US1] `internal/runtime/commit_removal_exclusion_test.go`: T1.11 (formerly T2.3; regression coverage that a parked removal blocks a B toggle, REST update and MCP patch, which then get 404)
- [ ] T018c [P] [US1] `internal/runtime/discovery_purge_race_test.go`: T1.12 (a pass parked after its approval check vs `QuarantineServer(true)` → no re-index)
- [ ] T018d [P] [US1] `internal/runtime/discovery_noreentry_test.go` (T1.13) and `internal/runtime/approval_policy_storage_test.go` (T1.14)
- [ ] T018e [P] [US1] `internal/runtime/commit_compensation_pending_test.go`: T1.16 (a pending restart-required `routing_mode` change survives a storage failure during an unrelated server edit; disk bytes, `desiredCfg` and the pending report unchanged; review R5.5)
- [ ] T018f [P] [US1] `internal/runtime/commit_replace_same_name_test.go` + `internal/runtime/configsvc/generations_test.go` (replacement rows): T1.17 (one-commit remove and re-create, equal and changed entries → `Replaced`, new incarnation, connect scheduled; `Prepare` rejects a replaced name absent from base or next; review R8.2)
- [ ] T018g [P] [US1] `internal/runtime/commit_compensation_reload_test.go`: T1.18 (an external edit A→A+B applied by `ReloadConfiguration`, storage failure injected after the disk write → file byte-identical to the edited input, live and storage unchanged, no reload loop; no-pending and absent-file variants; review R9.2)
- [ ] T018h [P] [US1] `internal/runtime/commit_retained_instance_test.go`: T1.19 (patch only `forward_headers`, then only `expose_prompts`, on a connected HTTP server → the retained instance has the new config and generation when the commit returns, the revoked header is not forwarded, and a new discovery pass updates the index; regression coverage, also run against a commit with step 7 stubbed out to show it detects the loss; review R9.1)
- [ ] T018i [P] [US1] `internal/runtime/commit_projection_repair_stamp_test.go` + `internal/storage/server_commit_test.go` (stamp cases): T1.20 (delete an unchanged server's row, run `ReconcileLive` → the repaired row equals the snapshot's `ServerGen`; `ApplyServerCommit` rejects a zero stamp and a stamp above `G`; review R9.3)
- [ ] T018j [P] [US1] `internal/runtime/commit_boot_matching_rows_test.go`: T1.21 (boot with rows and generations that match the file, an empty manager and no config change → every enabled server is scheduled and connects, and keeps its persisted generation; `Bootstrap()` outside boot is an invariant error; regression coverage, also run with the boot call missing `Bootstrap()` to show it detects the loss; review R9.6)
- [ ] T019 [P] [US1] `internal/runtime/lock_order_pr1_test.go`: `TestLockOrder_*` watchdog tests (commit vs capture section; commit vs discovery under the approval lock; a re-entrant `Commit` from inside `fn` is rejected)

### Implementation

- [ ] T020 [US1] `internal/config/server_entry_equal.go`: `ServerEntryEqual` (ignores Created/Updated; includes the explicit bit, Shared, AuthBroker) with a table test
- [ ] T020a [US1] `internal/commitview` leaf package: `ServerGen{Gen, Incarnation}` (PR2 adds `OAuthEpoch`, `CredentialBinding`, `Binding()`), `ErrStaleGeneration`; `TestCommitviewIsLeaf` (`go list -deps`, no module-internal import). `configsvc.ServerGen` and `upstream.ErrStaleGeneration` are aliases (review R7.2)
- [ ] T021 [US1] `configsvc`: remove `SetPrePublishHook` (admission moves to commit step 2, R1.7); `ServerGen` (alias of `commitview.ServerGen`), `Snapshot.ServerGenerations`, `Prepare(PrepareInput{Config, Source, Replaced})`/`Publish`/`SeedVersion`; the replaced-name row of the generation table checked first, and `Changes` computed in `Prepare` (review R8.2). Re-express `Update`/`UpdateIfCurrent` on top. Remove `ReloadFromFile`'s raw publish (makes T017 pass)
- [ ] T022 [US1] storage: `UpstreamRecord.Generation/Incarnation`, `ApplyServerCommit` (one tx; `Saves []ServerSave{Config, Stamp}` written with each row's prepared stamp verbatim, zero or above-`G` stamps rejected, review R9.3; saves through the quarantine-keeping invariant; deletes as today: row + call history; `Replaces` deleted before `Saves`, any other delete/save overlap an invariant error, review R8.2), meta key (makes T018 pass)
- [ ] T023 [US1] `internal/runtime/commit.go`: `Commit`, `CommitTx` ops (with the `replaced` bookkeeping and the `Replaced` change kind, makes T018f pass; review R8.2), pipeline steps 1-6 and 8 plus after-unlock (plan §Commit pipeline). Approval locks taken in sorted order for changed names in step 4, with `UnderApprovalLock` hooks. Compensating disk restore on a storage failure: capture the raw file state `{exists, bytes, mode}` inside L0 before step 5 and restore **that** (same bytes and mode, or remove the file when it was absent), never a config rebuilt from memory, with the self-write marker bookkeeping (forget the failed write's marker, record one for the restored bytes) and `desiredCfg` untouched (makes T018e and T018g pass; reviews R5.5, R9.2). The batch's per-save stamps are copied from `PreparedSnapshot.ServerGenerations` and asserted equal before `ApplyServerCommit` (makes T018i pass; review R9.3). Re-entry guard
- [ ] T024 [US1] `internal/runtime/commit_reconcile.go`: move `LoadConfiguredServers`' storage phase into step 6 (diff against the stored view; OAuth-config-change clear; newly-quarantined index purge, FR-008; orphan GC on the working copy, with the empty guard at boot only). Replace the boot call (`lifecycle.go:206`) with `Commit("boot", ReconcileLive + Bootstrap)`; `Bootstrap()` anywhere else is an invariant error, and every other reconcile is `ReconcileLive()` alone (makes T018j pass; review R9.6). `LoadConfiguredServers` is deleted once T026/T027 have converted its other callers. Delete the removal goroutines (removal is synchronous in step 6)
- [ ] T024a [US1] D15: move a discovery pass's final eligibility/generation check and all its index, StateView and last-good writes into one hold of L1 (coordinate with Spec 117 PR-3 G8; skip if already landed) (makes T018c pass). `ReconcileLive()`; delete `LoadConfiguredServers(cfg)`'s captured-argument form (D14)
- [ ] T024b [US1] `checkToolApprovalsHeld` / `deleteToolApprovalHeld` caller-held variants and the inventory of helpers inside the D15 hold; `resolveToolQuarantineGateHeld` reads the row + `meta.quarantine_enabled` (written by the commit tx) (D16) (makes T018d pass)
- [ ] T024c [US1] `managed.Client.Generation()`: `{cfg, gen}` held as one atomic pair, stamped from the `(cfg, gen)` passed by post-commit workers; `SetConfigAt(cfg, gen)` applies only a newer generation; `upstream.Manager.RestampRetained(changes)` called at commit step 7 under L3 W + L4 for every `Updated`, non-reconnect change, with `applyServerConcurrency` after L4 is released; discovery captures the generation for D15 (makes T018h pass; reviews R4.3, R9.1). Global approval-policy change → all servers' L1 in step 4 (FR-008c, T1.15)
- [ ] T025 [US1] after-unlock workers: spawn connect/disconnect workers for created, replaced and reconnecting `Changes` (still the legacy manager calls; PR3 binds them); request one `DiscoverAndIndexToolsForServer` pass for each server `RestampRetained` returned that is connected (review R9.1); the events, the telemetry `NotifyConfigChanged`
- [ ] T026 [US1] `ApplyConfig`/`applyConfigLocked`, `ReloadConfiguration`, `UpdateConfig`, `UpdateListenAddress`, `SaveConfiguration` (persist the live config, no storage read), `gateInitialConfig`, `SetServerShared` → `Commit` (makes T011 pass)
- [ ] T027 [US1] `EnableServer`, `BulkEnableServers`, `QuarantineServer` (index purge in the commit; baseline approval after the commit, which keeps the `CommitResult` and passes its incarnation on rather than recapturing by name; PR2 T053 adds the check, review R9.5), `RestartServer`'s disk refresh → `Commit` (makes T013 pass)
- [ ] T028 [US1] `internal/server`: `createServer` → `Commit(CreateServer)` and delete `createMu`; `removeServerStorage` → `Commit(RemoveServer)` (PR1 deletes as today); `UpdateServer` → `Commit(UpdateServer)` with the in-place mutation removed; registry source add/edit/remove → `Commit(MutateGlobal)` (makes T014 and T016 pass)
- [ ] T029 [US1] `internal/server/mcp.go`: update/patch merge inside `UpdateServer`; the result's connection status comes from the generation's worker result (bounded wait, as today); quarantine → `Commit(SetQuarantined)`; remove → `Commit(RemoveServer)` (makes T015 and the MCP half of T012 pass)
- [ ] T029a [US1] If Spec 117 PR-2 has merged: convert `CommitServerApproval`/`FinishServerApproval` to `Commit(ExpectIncarnation(PR2: generation until then), SetQuarantined(false), UnderApprovalLock(...))` with its tests unchanged. Otherwise: add `UnderApprovalLock` with a unit test (hook runs under the lock; a hook error aborts with nothing written)
- [ ] T030 [US1] `internal/httpapi/server.go` PATCH: move omitted-field, map-merge, isolation and forward-header resolution into a `func(cur *config.ServerConfig) error` passed down to `UpdateServer` (makes T012 pass; T002 now passes)
- [ ] T031 [US1] `TestCommit_*` + `TestLockOrder_*` at `-race -count=20` with `GOMAXPROCS=1` and `4`; the CI skip lane under `-tags server`; both lint passes; `./scripts/test-api-e2e.sh`
- [ ] T032 [US1] Live L1 on the qa070 harness ([quickstart.md](quickstart.md)); evidence in `evidence/s116-pr1/EVIDENCE.md`
- [ ] T033 [US1] Docs: `docs/architecture.md` "Config commit" section (pipeline + lock order); CLAUDE.md Recent Changes line
- [ ] T034 [US1] opencode Sol review in chunks (commit/storage; writers; httpapi/mcp). Fix in-scope findings only

**Checkpoint**: every server writer goes through `Commit`, and storage no
longer feeds the live config. #1558 and #1560 are closed. The #1559
resurrection source is gone.

---

## PR2: Atomic removal, purge and incarnation-bound approvals (US2; #1559 remainder, #1562; B2)

Branch `116-pr2-atomic-removal` from `main` after PR1 merges.

### Tests first (must fail on PR1)

- [ ] T040 [P] [US2] `internal/runtime/removal_purge_test.go`: T2.1 across REST, MCP, ApplyConfig, reload, and the empty set; asserts per bucket (data-model §PurgeSet)
- [ ] T041 [P] [US2] `internal/server/readd_clean_test.go`: T2.2 (identical tools and prompts are pending on a manual-trust re-add; unforced scanner approve fails; no OAuth token is reused)
- [ ] T043 [P] [US2] `internal/runtime/approve_incarnation_test.go`: T2.4 (re-add rejects; an unrelated `disabled_tools` edit does not reject) + T2.7 (discovery write after re-add dropped)
- [ ] T044 [P] [US2] `internal/security/scanner/approve_incarnation_test.go`: T2.5
- [ ] T045 [P] [US2] `internal/storage/oauth_exact_key_test.go`: T2.6 (B2)
- [ ] T047 [P] [US2] `internal/security/scanner/late_scan_result_test.go`: T2.8 (scan for old `acme` completes after remove + re-add → no job or report under the new incarnation; unforced approve still refused)
- [ ] T048 [P] [US2] `internal/oauth/late_token_save_test.go`: T2.9 (a `SaveToken` parked after its context check, then remove + re-add with the same URL → no token; the refresh schedule for the old incarnation is dropped)
- [ ] T046a [P] [US2] `internal/runtime/readd_lastgood_test.go`: T2.10
- [ ] T046b [P] [US2] `internal/upstream/completion_incarnation_test.go` (T2.11) and `internal/storage/identity_history_incarnation_test.go` (T2.12)
- [ ] T048a [P] [US2] `internal/oauth/oauth_epoch_clear_test.go`: T2.13 (old instance's token and DCR persistence parked; OAuth config changed with the same name and URL; release → cleared records stay absent and are not reused; review R5.3)
- [ ] T048b [P] [US2] `internal/runtime/oauth_construction_view_test.go`: T2.14 (an OAuth-changing commit, and a remove and re-add, parked at `afterStorage`; an old-generation worker released at `beforeBindingCheck` → `ErrStaleGeneration`, no instance, nothing persisted under the new binding; review R6.1)
- [ ] T048c [P] [US2] `internal/oauth/oauth_bound_clear_test.go` and `internal/upstream/core/oauth_5xx_clear_bound_test.go`: T2.15 (the 5xx `ClearToken` and a config-build DCR clear parked at `beforeClear`; OAuth config change or `RestartServer`; replacement saves; release → replacement credentials unchanged; review R6.2)
- [ ] T048d [P] [US2] `internal/oauth/refresh_coordinator_binding_test.go`: T2.16 (old-binding caller parked at `beforeRefreshDo` after its read; OAuth config change, then remove and re-add; replacement-binding flight started; release → neither binding receives the other's token, error, latch or cooldown; stale outcome ignored by `RefreshManager`; review R7.1)
- [ ] T048f [P] [US2] `internal/runtime/commit_replace_purge_test.go`: T2.17 (one-commit remove and re-create with a parked approval and a parked old-binding OAuth save; purge ran, new epoch, both writes rejected; review R8.2)
- [ ] T048g [P] [US2] `internal/upstream/cli/standalone_logout_test.go` + `scripts/check-upstream-write-sites_test.sh` case: T2.18 (standalone logout clears exactly the `GenerateServerKey` record; neighbours `acme_bar` and `other` untouched; planted call outside `cli/client.go` fails the script; review R8.3)
- [ ] T048e [P] [US2] `internal/storage/oauth_revision_test.go`: every `oauth_tokens` write path bumps `Revision` from `NextSequence`; delete then re-create never reuses a revision (review R7.3)
- [ ] T048h [P] [US2] `internal/runtime/unquarantine_baseline_incarnation_test.go`: T2.19 (the real `QuarantineServer(acme, false)` parked at `afterUnquarantineCommitBeforeBaseline` after its commit; remove and re-add `acme` under manual trust; create pending tool records for the replacement; release → the replacement's records stay pending and no baseline promotion is recorded; review R9.5)
- [ ] T048i [P] [US2] `internal/storage/oauth_bound_client_clear_test.go`: T2.20 (a record with tokens and DCR metadata; `ClearOAuthClientIfBound` with the matching binding and revision → tokens, expiry and record epoch kept, the four DCR fields empty, `Revision` increased; mismatched binding, epoch or revision → byte-identical record; absent record → `false`; review R9.4)
- [ ] T046 [P] [US2] `internal/storage/toolhash_keys_test.go`: pin the `toolhash` key format (A11) so the purge decision is justified

### Implementation

- [ ] T050 [US2] `internal/storage/purge.go`: `PurgeServer` inside the `ApplyServerCommit` tx for each `Removes` entry; `PurgeReport`. Rewrite `ClearOAuthState` with the exact key match (`name` or `name_` + 16 lowercase hex) (makes T045 pass)
- [ ] T051 [US2] `commit.go` step 6: removal purge under L1; in-memory purge (index + profile reindex, `RefreshManager.OnTokenCleared`, `RevokeInspectionExemption`, toolsig drop, discovery generation reset, `lastGoodTools`/`lastCaptureAt` delete); `CommitResult.Purged`; activity `purged` metadata (makes T040 pass)
- [ ] T052 [US2] Collapse REST `RemoveServer` and MCP `handleRemoveUpstream` onto `Commit(RemoveServer)` and delete their ad hoc cleanup (makes T041 pass)
- [ ] T053 [US2] Incarnation binding: `approvalHooks`; capture in `ApproveTools`, block/unblock, `checkToolApprovals`/capture; re-check against the storage row under L1; `ErrServerReplaced` (makes T043 pass). The unquarantine baseline is `approveBaselineToolsForServerAt(name, inc)` with `inc` from the unquarantine commit's `CommitResult` (or the request's `expected_incarnation`), never recaptured by name; under L1 it requires the row to have `inc` and be unquarantined, else promotes nothing (makes T048h pass; review R9.5)
- [ ] T054 [US2] Prompt approvals (`internal/server/prompt_quarantine.go`) under L1 with the incarnation check
- [ ] T055 [US2] Scanner: incarnation captured in `approveServer`; baseline save under L1 with the check; the `afterBaselineBeforeUnquarantine` seam after the L1 section exits; `UnquarantineServer` → `Commit(ExpectIncarnation, SetQuarantined(false))` (makes T044 pass; T2.5 parks at that seam, review R5.6)
- [ ] T055a [US2] Scanner: `Incarnation` on jobs and reports; callbacks use `Save*IfIncarnation`; `approveServer` filters by the current incarnation; removal invalidates the summary cache (makes T047 pass)
- [ ] T055b [US2] OAuth: `UpstreamRecord.OAuthEpoch` stamped on create and in the same tx as every OAuth-config-change clear; `PersistentTokenStore` and DCR lookups bound to `CredentialBinding{Incarnation, OAuthEpoch}` (reads ignore records of another binding); token, DCR and completion writes use `…IfBound`; `RefreshManager` keyed by `(name, incarnation, oauthEpoch)` (makes T048 and T048a pass; review R5.3)
- [ ] T055d [US2] OAuth committed view and bound deletes: `ServerGen.OAuthEpoch` computed in `Prepare` and `ServerGen.Binding()`; workers, supervisor connect and `ConnectAll` pass `(cfg, ServerGen)` from one snapshot or commit result, and `RestartServer` reuses the replaced instance's binding; `storage.Manager.CheckCommittedView` before construction (`ErrStaleGeneration`, no OAuth config built); `DeleteOAuthTokenIfBound` (deletes the record) and `ClearOAuthClientIfBound` (rewrites the record with only the four DCR fields emptied, tokens and record epoch kept, `Revision` bumped, review R9.4) (row binding + record epoch + observed `Revision` in one `Update`, review R7.3; makes T048i pass) replacing `ClearToken`, `clearOAuthCredentials` and the two `…If` clears (their content predicates removed); `OAuthTokenRecord.Revision` set from the bucket's `NextSequence` on every write; `PersistentTokenStore` records the revision of the record it last served and the config-build hygiene check the revision it read; `CheckCommittedView` takes `commitview.ServerGen` and `CredentialBinding` lives in `commitview` (no `configsvc`↔`storage` import, review R7.2; compile and test `./internal/commitview ./internal/runtime/configsvc ./internal/storage ./internal/oauth ./internal/upstream/...` bare and `-tags server`); unexport the unconditional primitives; add `BoltDB.ClearOAuthTokenForStandaloneLogout(serverKey)` and convert `internal/upstream/cli/client.go:473` to it (review R8.3); extend `scripts/check-upstream-write-sites.sh` to forbid the unconditional primitives and their old names in `internal/upstream` and `internal/oauth`, with a one-file exception for the standalone clear in `internal/upstream/cli/client.go`; build `./cmd/mcpproxy` bare and with `-tags server -o /dev/null` (makes T048b, T048c and T048g pass; review R6.1, R6.2, R8.3)
- [ ] T055f [US2] Replacement purge: `ApplyServerCommit` runs the full `PurgeSet` for `Replaces` before saving the new row (`Incarnation = OAuthEpoch = G`); the in-memory purge and `RefreshCoordinator.Forget` run for replaced names (makes T048f pass; review R8.2)
- [ ] T055e [US2] Refresh coordinator bound by credential binding: `RefreshRequest.Binding`; flights, latch, cooldown and `lastSavedAt` keyed by `(serverKey, binding)`; `RefreshOutcome.Binding`, hook and `RefreshManager` ignore outcomes of another binding; `Forget(serverKey, binding)` from `OnTokenCleared`, the purge and the OAuth-config clear; both call sites pass the instance binding (makes T048d pass; review R7.1)
- [ ] T055c [US2] Completion events carry the incarnation and the consumer drops stale ones; the completion purge matches by decoded `ServerName`; `RegisterServerIdentityIfIncarnation`, `RecordToolCallIfIncarnation` (`code_execution` exempt) (makes T046b pass)
- [ ] T056 [US2] REST: `generation`/`incarnation` in server and review payloads; optional `expected_incarnation` on approve/block/unquarantine; 409 `server_replaced`; CLI message passthrough; `oas/swagger.yaml`; `docs/api/rest-api.md`
- [ ] T057 [P] [US2] Web UI: send `expected_incarnation` from the review and approval actions; vitest in `frontend/tests/unit/`; Playwright sweep per `docs/development/web-ui-verification.md`
- [ ] T058 [US2] Test-only `mcpproxy debug purge-report` (build tag `qa_hooks`) for live L2 inspection
- [ ] T059 [US2] The race/lint/e2e matrix (as T031); live L2; evidence `s116-pr2`; Sol review; docs (`docs/features/security-quarantine.md` "Removal purges")

**Checkpoint**: removal is final, and a re-add starts clean. #1559 and #1562
are closed.

---

## PR3: Generation-bound workers and supervisor (US3; #1563)

Branch `116-pr3-generation-workers` from `main` after PR1 merges. It is
independent of PR2.

### Tests first (must fail on PR1)

- [ ] T060 [P] [US3] `internal/runtime/worker_generation_test.go`: T3.1 (a/b/c) and T3.2, parking with `beforeWorkerAct`
- [ ] T061 [P] [US3] `internal/runtime/supervisor/stale_action_generation_test.go`: T3.3 with the **real** `ActorPoolSimple` + `upstream.Manager` (no settle-before-act)
- [ ] T062 [P] [US3] `internal/runtime/restart_generation_test.go`: T3.4
- [ ] T063 [P] [US3] `internal/runtime/capture_generation_test.go`: T3.5
- [ ] T061a [P] [US3] `internal/runtime/supervisor/stale_disconnect_test.go`: T3.6 (an old `ActionDisconnect` parked, then a newer commit installs and connects the replacement, then release → the replacement stays connected)
- [ ] T061b [P] [US3] `internal/runtime/supervisor/stale_disconnect_restart_test.go`: T3.9 (a real disconnect captured for instance I1 parked; `RestartServer` installs I2 at the same generation; release → `ErrStaleGeneration`, I2 stays connected; review R5.2)
- [ ] T063a [P] [US3] `internal/runtime/lastgood_stamp_test.go`: T3.7
- [ ] T064a [P] [US3] `internal/runtime/replace_same_name_instance_test.go`: T3.10 (one-commit remove and re-create of a connected server with an equal entry → old instance unregistered, new `InstanceID`, new connect; review R8.2)
- [ ] T064 [P] [US3] `internal/upstream/manager_generation_test.go`: `*At` methods; `AdvanceGenerations` unregisters vs restamps; a removed tombstone refuses re-registration at the old generation and is overwritten by a create

### Implementation

- [ ] T070 [US3] `upstream.Manager`: `desired`, `AddServerConfigAt`, `RemoveServerAt`, `ConnectServerAt`, `AdvanceGenerations` (under captureMu W + mu; restamps retained instances with `SetConfigAt` only when `desired[name].gen` moves and deletes PR1's `RestampRetained`, with T1.19 kept unchanged, review R9.1; `GenerationChange.Replaced` always unregisters, review R8.2, makes T064a pass); `managed.Client.InstanceID()`; `DisconnectServerAt(name, gen, inst)`, `ReplaceInstanceAt`; one `unregisterLocked` helper for every delete or overwrite of `m.clients` (its `Retire` hook is a no-op until PR4) (makes T064, T061a and T061b pass; reviews R5.2, R5.4)
- [ ] T071 [US3] `commit.go` step 7 calls `AdvanceGenerations(changes)`; the instances it returns are disconnected after unlock
- [ ] T072 [US3] Post-commit workers use `*At` with `(name, g)` (makes T060 pass)
- [ ] T073 [US3] Supervisor: actions carry `ServerGen.Gen` from their snapshot, and instance-targeted actions (`ActionDisconnect`, the first leg of `ActionReconnect`) also carry the observed `InstanceID`; `ActorPoolSimple` uses `*At` (its name-resolving `DisconnectServer`, `actor_pool.go:146-154`, is replaced); `ErrStaleGeneration` → debug + no-op (makes T061 pass)
- [ ] T074 [US3] `RestartServer` → `manager.ReplaceInstanceAt(name, gen, expected)` (unregister + retire the old instance through `unregisterLocked`, then a fresh instance with a new `InstanceID` at the same generation) (makes T062 pass)
- [ ] T075 [US3] Capture and discovery carry the instance generation and drop on mismatch; `lastGoodTools` stamped `{Incarnation, Gen}` and reapply rejects a mismatch (makes T063 and T063a pass)
- [ ] T075a [US3] `ForceReconnectAll` + Docker recovery → `ReplaceInstanceAt` (T3.8, `internal/upstream/force_reconnect_generation_test.go` written first)
- [ ] T076 [US3] Extend `check-upstream-write-sites.sh` (including manager-internal calls in `internal/upstream/manager.go`, review R4.1): forbid `AddServerConfig(`, `AddServer(` and `RemoveServer(` on the manager inside `internal/runtime/**` and `internal/server/**` outside tests; forbid `delete(m.clients` and `m.clients[...] =` in `manager.go` outside `unregisterLocked` and the install helper (review R5.4)
- [ ] T077 [US3] `qa_hooks` build tag: `MCPPROXY_QA_PARK_WORKER`, `MCPPROXY_QA_PARK_SUPERVISOR_DISCONNECT` (L3 step 3); live L3; evidence `s116-pr3`; the race/lint/e2e matrix; Sol review

**Checkpoint**: stale workers and actions are inert. #1563 is closed.

---

## PR4: Retirement barrier, OAuth included (US4; #1564)

Branch `116-pr4-retirement-barrier` from `main` after PR3 merges.

### Tests first (must fail on PR3)

- [ ] T080 [P] [US4] `internal/upstream/dialgate/gate_test.go`: Admit/Retire ordering, conn Write after Retire → `ErrRetired`, a blocked Write unblocked by Retire, Retire bounded (< 1 s with a hung peer), idempotent, under `-race -count=50`
- [ ] T081 [P] [US4] `internal/upstream/managed/retire_stdio_test.go`: T4.1 (marker launcher; Docker via a mock docker binary; launcher)
- [ ] T082 [P] [US4] `internal/upstream/managed/retire_http_test.go`: T4.2 for HTTP and SSE × remove/replace/quarantine, synchronized on "commit returned"
- [ ] T083 [P] [US4] `internal/transport/retire_retry_test.go`: T4.3 (flaky reused connection forces a retry; zero hits after retirement; Retire bounded)
- [ ] T084 [P] [US4] `internal/upstream/core/retire_oauth_test.go`: T4.4 (code exchange and refresh; no secret or refresh token after retirement; `RefreshOAuthToken` on a retired name refused)
- [ ] T085 [P] [US4] `internal/runtime/retire_capture_test.go`: T4.5; `internal/upstream/retire_inflight_call_test.go`: T4.6
- [ ] T081a [P] [US4] `internal/upstream/core/retire_stdio_write_test.go`: T4.8 (a launched stdio instance; `Retire` returns; a parked `SendRequest`/`SendNotification` resumes → `ErrRetired`, the child's stdin receives nothing, and the process group is dead when `Retire` returns)
- [ ] T084a [P] [US4] `internal/oauth/retire_preflight_test.go`: T4.9 (park `preflightPRMScopes`, `autoDetectResource` and `httpFetchRaw` before sending; retire; zero hits on the counter)
- [ ] T085a [P] [US4] `internal/upstream/dialgate/nesting_test.go` + `internal/upstream/core/retire_launch_registration_test.go`: T4.10, T4.11
- [ ] T085b [P] [US4] `internal/upstream/core/process_windows_job_test.go` (windows build tag, T4.12) and `internal/upstream/core/retire_browser_test.go` (T4.13)
- [ ] T085c [P] [US4] `internal/upstream/retire_same_generation_test.go`: T4.15 (old instance parked before its stdio launch and before its HTTP/OAuth send; `RestartServer`, `ForceReconnectAll` and Docker recovery replace it at the same generation; release → no launch, no request; replacement unaffected; review R5.4)
- [ ] T085d [P] [US4] `internal/upstream/dialgate/late_register_test.go`: T4.16 (dial parked before `track`; sweep finishes; register + blocking write → closed, `ErrRetired`, `Retire` ≤ 1 s; review R5.1)
- [ ] T085e [P] [US4] `internal/upstream/dialgate/capability_parity_test.go` (`TestWrapTransportCapabilityParity`, `TestMCPGoTransportMethodSetPinned`) and `internal/upstream/core/wrapped_transport_consumers_test.go`: T4.17 (a)-(f) with the production wrapped transports: OAuth handler extraction, concurrent reactive + proactive refresh, per-method-auth manual login, Streamable HTTP 404 session re-init, protocol header, server-to-client ping over stdio and Streamable HTTP, SSE connection loss, retired request handler (review R8.1)
- [ ] T086 [P] [US4] `internal/upstream/dialgate/bench_test.go`: T4.7

### Implementation

- [ ] T090 [US4] `internal/upstream/dialgate`: `Gate`, `gatedConn`, `Transport(base)`, `DialContext` (dial **and** `track` in one admitted section), retirement-aware `track` (latch checked under `connsMu`; close on retired), `Admit`, `Retire` (latch set under `connsMu`; second conn sweep after the barrier) (makes T080, T085d and T086 pass; review R5.1)
- [ ] T091 [US4] `managed.NewClient` creates the gate; the CLI client gets an always-open gate; `Disconnect` does not retire (only the manager retires)
- [ ] T092 [US4] `core`: stdio `client.Start`, Docker launch and launcher spawn under `gate.Admit` (makes T081 pass)
- [ ] T093 [US4] `transport/http.go`: every branch builds on `gate.Transport(base)` instead of `http.DefaultTransport` (trace, plain, headers, OAuth, SSE) (makes T082 and T083 pass)
- [ ] T093a [US4] Lock-free, capability-preserving `dialgate.WrapTransport` around every core-built mcp-go transport (`gatedStdio`/`gatedStreamableHTTP`/`gatedSSE` forwarding exactly their inner kind's capability rows, `Unwrap()`, retired-aware `SetRequestHandler`); `core.innerTransport` in `core/transport_unwrap.go` used by `extractOAuthHandler` and `SessionSnapshot`; T099 forbids `GetTransport()` elsewhere in `internal/upstream/**` (makes T085e pass; review R8.1); stdio `cmd.Start` + group/Job registration + `TrackProcess` in one admitted section, with a kill on a Job assignment failure; `Retire` kills before and after its barrier (makes T081a, T085a pass)
- [ ] T093b [US4] Windows: `CREATE_SUSPENDED` → `AssignProcessToJobObject` → `NtResumeProcess` inside the admitted section (stdio, Docker, launcher); `openBrowser` under `gate.Admit` (makes T085b pass)
- [ ] T093c [US4] `launcher.WaitForURL` takes the gate dialer; Docker diagnostic/recovery execs under `gate.Admit`; teardown exemption documented (T4.14, `internal/upstream/core/retire_launcher_probe_test.go` written first)
- [ ] T094a [US4] `oauth.Env{Transport, Ctx}` threaded through `preflightPRMScopes`, `autoDetectResource`, the discovery cache fetch and `CreateOAuthConfig*` (makes T084a pass)
- [ ] T094 [US4] `oauth`: `WithBaseTransport` on `CreateOAuthConfig*` and the transport wrapper `inner`; the core OAuth strategy and manual OAuth pass the instance transport; `Manager.RefreshOAuthToken`/`StartManualOAuth*` refuse an unregistered or retired instance (makes T084 pass)
- [ ] T095 [US4] `unregisterLocked` calls `Retire()` on the instance it removes, before returning. This covers `AdvanceGenerations` (and so before publish) and the same-generation `ReplaceInstanceAt`/`RemoveServerAt` paths (restart, `ForceReconnectAll`, Docker recovery), which retire the old instance before installing the replacement (makes T085 and T085c pass; review R5.4)
- [ ] T096 [US4] A retired-instance call or probe maps to the existing unavailable error and does not feed call-stats or health for the replacement
- [ ] T097 [US4] Live fixtures `counting_http_fixture.py` and the marker launcher; `qa_hooks` `MCPPROXY_QA_PARK_DIAL` / `MCPPROXY_QA_PARK_OAUTH`; live L4; evidence `s116-pr4`
- [ ] T099 [US4] Extend `check-upstream-write-sites.sh` with the FR-024a ungated-HTTP grep (allowlist file `scripts/ungated-http-allowlist.txt`)
- [ ] T098 [US4] The race/lint/e2e matrix; SC-004 measurements recorded; Sol review (transport + OAuth chunk separately); docs (`docs/architecture.md` "Retirement barrier")

**Checkpoint**: a retired instance cannot launch, dial or write. #1564 is
closed.

---

## Dependencies and execution order

```
main ──► PR1 ──┬──► PR2
               └──► PR3 ──► PR4
```

- Within a PR: Setup → Tests first (all `[P]`) → Implementation in the listed order → verification, live check, review.
- PR2 and PR3 may be developed in parallel after PR1 merges. Whichever lands second rebases. Their files overlap only in `commit.go` (step 6 vs step 7).
- Following the "merge early, don't stack" rule (maintainer memory), each PR branches from updated `main`, never from an unmerged sibling.
