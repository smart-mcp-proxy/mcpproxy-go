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
- [ ] T018g [P] [US1] `internal/runtime/commit_compensation_reload_test.go`: T1.18 (an external edit A→A+B applied by `ReloadConfiguration` with a storage failure injected into the reload commit → the reload writes no file, so the file stays byte-identical to the edited input, live and storage unchanged, no reload loop. Disk-writing compensation is tested separately: a toggle of A that fails in storage while the file holds the unapplied A+B edit restores A+B byte for byte (`TestCommit_R3_CompensationKeepsUnappliedEdit`), a pending restart-required value survives (T1.16), and an absent file stays absent (`TestCommit_T1_18_AbsentFileStaysAbsent`); review R9.2, PR1 review rounds 3 and 4)
- [ ] T018h [P] [US1] `internal/runtime/commit_retained_instance_test.go`: T1.19 (patch only `forward_headers`, then only `expose_prompts`, on a connected HTTP server → the retained instance has the new config and generation when the commit returns, the revoked header is not forwarded, and a new discovery pass updates the index; regression coverage, also run against a commit with step 7 stubbed out to show it detects the loss; review R9.1)
- [ ] T018i [P] [US1] `internal/runtime/commit_projection_repair_stamp_test.go` + `internal/storage/server_commit_test.go` (stamp cases): T1.20 (delete an unchanged server's row, run `ReconcileLive` → the repaired row equals the snapshot's `ServerGen`; `ApplyServerCommit` rejects a zero stamp and a stamp above `G`; review R9.3)
- [ ] T018j [P] [US1] `internal/runtime/commit_boot_matching_rows_test.go`: T1.21 (boot with rows and generations that match the file, an empty manager and no config change → every enabled server is scheduled and connects, and keeps its persisted generation; `Bootstrap()` outside boot is an invariant error; regression coverage, also run with the boot call missing `Bootstrap()` to show it detects the loss; review R9.6)
- [ ] T019 [P] [US1] `internal/runtime/lock_order_pr1_test.go`: `TestLockOrder_*` watchdog tests (commit vs capture section; commit vs discovery under the approval lock; a re-entrant `Commit` from inside `fn` is rejected)

### Implementation

- [ ] T020 [US1] `internal/config/server_entry_equal.go`: `ServerEntryEqual` (ignores Created/Updated; includes the explicit bit, Shared, AuthBroker) with a table test
- [ ] T020a [US1] `internal/commitview` leaf package: `ServerGen{Gen, Incarnation}` (PR2 adds `OAuthEpoch`, `CredentialBinding`, `Binding()`), `ErrStaleGeneration`; `TestCommitviewIsLeaf` (`go list -deps`, no module-internal import). `configsvc.ServerGen` and `upstream.ErrStaleGeneration` are aliases (review R7.2)
- [ ] T021 [US1] `configsvc`: remove `SetPrePublishHook` (admission moves to commit step 2, R1.7); `ServerGen` (alias of `commitview.ServerGen`), `Snapshot.ServerGenerations`, `Prepare(PrepareInput{Config, Source, Replaced})`/`Publish`/`SeedVersion`; the replaced-name row of the generation table checked first, and `Changes` computed in `Prepare` (review R8.2). Re-express `Update`/`UpdateIfCurrent` on top. Remove `ReloadFromFile`'s raw publish (makes T017 pass)
- [ ] T022 [US1] storage: `UpstreamRecord.Generation/Incarnation`, `ApplyServerCommit` (one tx; `Saves []ServerSave{Config, Stamp}` written with each row's prepared stamp verbatim, zero or above-`G` stamps rejected, review R9.3; saves through the quarantine-keeping invariant; deletes as today: row + call history; `Replaces` deleted before `Saves`, any other delete/save overlap an invariant error, review R8.2), meta key (makes T018 pass)
- [ ] T023 [US1] `internal/runtime/commit.go`: `Commit`, `CommitTx` ops (with the `replaced` bookkeeping and the `Replaced` change kind, makes T018f pass; review R8.2), pipeline steps 1-6 and 8 plus after-unlock (plan §Commit pipeline). Approval locks taken in sorted order in step 4 for every name step 6 writes (changed names, hook names, removes, replacements, OAuth clears and projection-repair saves; every configured server on a global approval-policy change; plan.md step 4, PR1 review rounds 4-5), with `UnderApprovalLock` hooks. Compensating disk restore on a storage failure: capture the raw file state `{exists, bytes, mode}` inside L0 before step 5 and restore **that** (same bytes and mode, or remove the file when it was absent), never a config rebuilt from memory, with the self-write marker bookkeeping (forget the failed write's marker, record one for the restored bytes) and `desiredCfg` untouched (makes T018e and T018g pass; reviews R5.5, R9.2). The batch's per-save stamps are copied from `PreparedSnapshot.ServerGenerations` and asserted equal before `ApplyServerCommit` (makes T018i pass; review R9.3). Re-entry guard
- [ ] T024 [US1] `internal/runtime/commit_reconcile.go`: move `LoadConfiguredServers`' storage phase into step 6 (diff against the stored view; OAuth-config-change clear; newly-quarantined index purge, FR-008; orphan GC on the working copy, with the empty guard at boot only). Replace the boot call (`lifecycle.go:206`) with `Commit("boot", ReconcileLive + Bootstrap)`; `Bootstrap()` anywhere else is an invariant error, and every other reconcile is `ReconcileLive()` alone (makes T018j pass; review R9.6). `LoadConfiguredServers` is deleted once T026/T027 have converted its other callers. Delete the removal goroutines (removal is synchronous in step 6)
- [ ] T024a [US1] D15: move a discovery pass's final eligibility/generation check and all its index, StateView and last-good writes into one hold of L1 (coordinate with Spec 117 PR-3 G8; skip if already landed) (makes T018c pass). `ReconcileLive()`; delete `LoadConfiguredServers(cfg)`'s captured-argument form (D14)
- [ ] T024b [US1] `checkToolApprovalsHeld` / `deleteToolApprovalHeld` caller-held variants and the inventory of helpers inside the D15 hold; `resolveToolQuarantineGateHeld` reads the row + `meta.quarantine_enabled` (written by the commit tx) (D16) (makes T018d pass)
- [ ] T024c [US1] `managed.Client.Generation()`: `{cfg, gen}` held as one atomic pair, stamped from the `(cfg, gen)` passed by post-commit workers; `SetConfigAt(cfg, gen)` applies only a newer generation; `upstream.Manager.RestampRetained(changes)` called at commit step 7 under L3 W + L4 for every `Updated`, non-reconnect change, with `applyServerConcurrency` after L4 is released; discovery captures the generation for D15 (makes T018h pass; reviews R4.3, R9.1). Global approval-policy change → all servers' L1 in step 4 (FR-008c, T1.15)
- [ ] T025 [US1] after-unlock workers: spawn connect/disconnect workers for created, replaced and reconnecting `Changes` (still the legacy manager calls; PR3 binds them); request one `DiscoverAndIndexToolsForServer` pass for each server `RestampRetained` returned that is connected (review R9.1); the events, the telemetry `NotifyConfigChanged`
- [ ] T026 [US1] `ApplyConfig`/`applyConfigLocked`, `ReloadConfiguration`, `UpdateConfig`, `UpdateListenAddress`, `SaveConfiguration` (persist the live config, no storage read), `gateInitialConfig`, `SetServerShared` → `Commit` (makes T011 pass)
- [ ] T027 [US1] `EnableServer`, `BulkEnableServers`, `QuarantineServer` (index purge in the commit; baseline approval after the commit, which keeps the `CommitResult` and passes its incarnation on rather than recapturing by name; PR2 T053 adds the check, review R9.5), `RestartServer`'s disk refresh → `Commit` (makes T013 pass)
- [ ] T028 [US1] `internal/server`: `createServer` → `Commit(CreateServer)` and delete `createMu` (the mutex only: the #1570 create-only behaviour and test assertions stay, plan.md `createMu` note); `removeServerStorage` → `Commit(RemoveServer)` (PR1 deletes as today); `UpdateServer` → `Commit(UpdateServer)` with the in-place mutation removed; registry source add/edit/remove → `Commit(MutateGlobal)` (makes T014 and T016 pass)
- [ ] T029 [US1] `internal/server/mcp.go`: update/patch merge inside `UpdateServer`; the result's connection status comes from the generation's worker result (bounded wait, as today): the update returns the server's committed generation and the monitor skips any registered instance stamped below it, so the replaced instance's ready/error state is never reported (PR1 review round 6); quarantine → `Commit(SetQuarantined)`; remove → `Commit(RemoveServer)` (makes T015 and the MCP half of T012 pass)
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

Branch `116-pr2-atomic-removal` from `main` after PR1 merges (implemented as
`fix/116-pr2-atomic-removal`).

**Implementation note (PR2 as shipped).** The removal purge, the exact OAuth
key match, the in-memory purge, the incarnation-bound approval writers (tool,
prompt, scanner, unquarantine baseline), the incarnation-conditional scan,
identity and history writes, the REST/Web UI `expected_incarnation` and the
test-only purge report ship in PR2. The OAuth **credential binding** —
`ServerGen.OAuthEpoch`, `CredentialBinding`-conditional token/DCR/completion
writes and reads, `CheckCommittedView`, the `oauth_tokens` record `Revision`,
binding-keyed refresh flights, `…IfBound` clears and the standalone-logout
primitive (T048, T048a-e, T048g, T048i, T055b, T055d, T055e and the OAuth half
of T055c/T055f) — is deferred to a follow-up **PR2b**. It has to thread the
committed `ServerGen` through `upstream.Manager` → `managed` → `core` →
`oauth` constructors, which PR3 rewrites (`AddServerConfigAt` and friends);
landing it before PR3 would make both PRs edit the same constructors. What PR2
closes: the removal purge deletes every OAuth record of the name that exists
when the removal commits, in the row's transaction (exact key, B2), so a
same-name re-add does not inherit the tokens or DCR client the removed server
had stored (#1562, the at-rest half). What it does NOT yet close (review round
1): `PersistentTokenStore.SaveToken` has no incarnation check, so an old
instance's in-flight OAuth write (a code exchange, a refresh or a DCR save
already past its network round-trip) can land after the purge and recreate a
record the re-added server then reads; likewise after an OAuth-config clear
that keeps the name and URL. #1562's credential half therefore stays open
until PR2b's parked-save test (T2.9, T048) passes; PR2 does not claim it. A
same-commit replacement (T055f) runs the full purge in PR2; only its epoch
stamp is PR2b.

The PR2 tests live in `internal/storage/purge_test.go`,
`internal/storage/incarnation_writes_test.go`,
`internal/runtime/spec116_pr2_test.go` and `spec116_pr2_api_test.go`,
`internal/server/spec116_pr2_test.go`,
`internal/security/scanner/late_scan_result_test.go`,
`internal/httpapi/expected_incarnation_test.go` and
`frontend/tests/unit/expected-incarnation-api.spec.ts` (one file per layer
instead of the per-task file names below), with the shared fixture
`internal/storage/purgetest`.

### Tests first (must fail on PR1)

- [x] T040 [P] [US2] `internal/runtime/removal_purge_test.go`: T2.1 across REST, MCP, ApplyConfig, reload, and the empty set; asserts per bucket (data-model §PurgeSet)
- [x] T041 [P] [US2] `internal/server/readd_clean_test.go`: T2.2 (identical tools and prompts are pending on a manual-trust re-add; unforced scanner approve fails; no OAuth token is reused)
- [x] T043 [P] [US2] `internal/runtime/approve_incarnation_test.go`: T2.4 (re-add rejects; an unrelated `disabled_tools` edit does not reject) + T2.7 (discovery write after re-add dropped)
- [x] T044 [P] [US2] `internal/security/scanner/approve_incarnation_test.go`: T2.5
- [x] T045 [P] [US2] `internal/storage/oauth_exact_key_test.go`: T2.6 (B2)
- [x] T047 [P] [US2] `internal/security/scanner/late_scan_result_test.go`: T2.8 (scan for old `acme` completes after remove + re-add → no job or report under the new incarnation; unforced approve still refused)
- [x] **(PR2b)** T048 [P] [US2] `internal/oauth/late_token_save_test.go`: T2.9 (a `SaveToken` parked after its context check, then remove + re-add with the same URL → no token; the refresh schedule for the old incarnation is dropped)
- [x] T046a [P] [US2] `internal/runtime/readd_lastgood_test.go`: T2.10
- [x] T046b [P] [US2] (T2.12 in PR2; T2.11 with the completion events in PR2b) `internal/upstream/completion_incarnation_test.go` (T2.11) and `internal/storage/identity_history_incarnation_test.go` (T2.12)
- [x] **(PR2b)** T048a [P] [US2] `internal/oauth/oauth_epoch_clear_test.go`: T2.13 (old instance's token and DCR persistence parked; OAuth config changed with the same name and URL; release → cleared records stay absent and are not reused; review R5.3)
- [x] **(PR2b)** T048b [P] [US2] `internal/runtime/oauth_construction_view_test.go`: T2.14 (an OAuth-changing commit, and a remove and re-add, parked at `afterStorage`; an old-generation worker released at `beforeBindingCheck` → `ErrStaleGeneration`, no instance, nothing persisted under the new binding; review R6.1)
- [x] **(PR2b)** T048c [P] [US2] `internal/oauth/oauth_bound_clear_test.go` and `internal/upstream/core/oauth_5xx_clear_bound_test.go`: T2.15 (the 5xx `ClearToken` and a config-build DCR clear parked at `beforeClear`; OAuth config change or `RestartServer`; replacement saves; release → replacement credentials unchanged; review R6.2)
- [x] **(PR2b)** T048d [P] [US2] `internal/oauth/refresh_coordinator_binding_test.go`: T2.16 (old-binding caller parked at `beforeRefreshDo` after its read; OAuth config change, then remove and re-add; replacement-binding flight started; release → neither binding receives the other's token, error, latch or cooldown; stale outcome ignored by `RefreshManager`; review R7.1)
- [x] T048f [P] [US2] (PR2 covers the purge and the parked approval; the parked OAuth save and the epoch are PR2b) `internal/runtime/commit_replace_purge_test.go`: T2.17 (one-commit remove and re-create with a parked approval and a parked old-binding OAuth save; purge ran, new epoch, both writes rejected; review R8.2)
- [x] **(PR2b)** T048g [P] [US2] `internal/upstream/cli/standalone_logout_test.go` + `scripts/check-upstream-write-sites_test.sh` case: T2.18 (standalone logout clears exactly the `GenerateServerKey` record; neighbours `acme_bar` and `other` untouched; planted call outside `cli/client.go` fails the script; review R8.3)
- [x] **(PR2b)** T048e [P] [US2] `internal/storage/oauth_revision_test.go`: every `oauth_tokens` write path bumps `Revision` from `NextSequence`; delete then re-create never reuses a revision (review R7.3)
- [x] T048h [P] [US2] `internal/runtime/unquarantine_baseline_incarnation_test.go`: T2.19 (the real `QuarantineServer(acme, false)` parked at `afterUnquarantineCommitBeforeBaseline` after its commit; remove and re-add `acme` under manual trust; create pending tool records for the replacement; release → the replacement's records stay pending and no baseline promotion is recorded; review R9.5)
- [x] **(PR2b)** T048i [P] [US2] `internal/storage/oauth_bound_client_clear_test.go`: T2.20 (a record with tokens and DCR metadata; `ClearOAuthClientIfBound` with the matching binding and revision → tokens, expiry and record epoch kept, the four DCR fields empty, `Revision` increased; mismatched binding, epoch or revision → byte-identical record; absent record → `false`; review R9.4)
- [x] T046 [P] [US2] `internal/storage/toolhash_keys_test.go`: pin the `toolhash` key format (A11) so the purge decision is justified

### Implementation

- [x] T050 [US2] `internal/storage/purge.go`: `PurgeServer` inside the `ApplyServerCommit` tx for each `Removes` entry; `PurgeReport`. Rewrite `ClearOAuthState` with the exact key match (`name` or `name_` + 16 lowercase hex) (makes T045 pass)
- [x] T051 [US2] `commit.go` step 6: removal purge under L1; in-memory purge (index + profile reindex, `RefreshManager.OnTokenCleared`, `RevokeInspectionExemption`, toolsig drop, discovery generation reset, `lastGoodTools`/`lastCaptureAt` delete); `CommitResult.Purged`; activity `purged` metadata (makes T040 pass)
- [x] T052 [US2] Collapse REST `RemoveServer` and MCP `handleRemoveUpstream` onto `Commit(RemoveServer)` and delete their ad hoc cleanup (makes T041 pass)
- [x] T053 [US2] Incarnation binding: `approvalHooks`; capture in `ApproveTools`, block/unblock, `checkToolApprovals`/capture; re-check against the storage row under L1; `ErrServerReplaced` (makes T043 pass). The unquarantine baseline is `approveBaselineToolsForServerAt(name, inc)` with `inc` from the unquarantine commit's `CommitResult` (or the request's `expected_incarnation`), never recaptured by name; under L1 it requires the row to have `inc` and be unquarantined, else promotes nothing (makes T048h pass; review R9.5)
- [x] T054 [US2] Prompt approvals (`internal/server/prompt_quarantine.go`) under L1 with the incarnation check
- [x] T055 [US2] Scanner: incarnation captured in `approveServer`; baseline save under L1 with the check; the `afterBaselineBeforeUnquarantine` seam after the L1 section exits; `UnquarantineServer` → `Commit(ExpectIncarnation, SetQuarantined(false))` (makes T044 pass; T2.5 parks at that seam, review R5.6)
- [x] T055a [US2] Scanner: `Incarnation` on jobs and reports; callbacks use `Save*IfIncarnation`; `approveServer` filters by the current incarnation; removal invalidates the summary cache (makes T047 pass)
- [x] **(PR2b)** T055b [US2] OAuth: `UpstreamRecord.OAuthEpoch` stamped on create and in the same tx as every OAuth-config-change clear; `PersistentTokenStore` and DCR lookups bound to `CredentialBinding{Incarnation, OAuthEpoch}` (reads ignore records of another binding); token, DCR and completion writes use `…IfBound`; `RefreshManager` keyed by `(name, incarnation, oauthEpoch)` (makes T048 and T048a pass; review R5.3)
- [x] **(PR2b)** T055d [US2] OAuth committed view and bound deletes: `ServerGen.OAuthEpoch` computed in `Prepare` and `ServerGen.Binding()`; workers, supervisor connect and `ConnectAll` pass `(cfg, ServerGen)` from one snapshot or commit result, and `RestartServer` reuses the replaced instance's binding; `storage.Manager.CheckCommittedView` before construction (`ErrStaleGeneration`, no OAuth config built); `DeleteOAuthTokenIfBound` (deletes the record) and `ClearOAuthClientIfBound` (rewrites the record with only the four DCR fields emptied, tokens and record epoch kept, `Revision` bumped, review R9.4) (row binding + record epoch + observed `Revision` in one `Update`, review R7.3; makes T048i pass) replacing `ClearToken`, `clearOAuthCredentials` and the two `…If` clears (their content predicates removed); `OAuthTokenRecord.Revision` set from the bucket's `NextSequence` on every write; `PersistentTokenStore` records the revision of the record it last served and the config-build hygiene check the revision it read; `CheckCommittedView` takes `commitview.ServerGen` and `CredentialBinding` lives in `commitview` (no `configsvc`↔`storage` import, review R7.2; compile and test `./internal/commitview ./internal/runtime/configsvc ./internal/storage ./internal/oauth ./internal/upstream/...` bare and `-tags server`); unexport the unconditional primitives; add `BoltDB.ClearOAuthTokenForStandaloneLogout(serverKey)` and convert `internal/upstream/cli/client.go:473` to it (review R8.3); extend `scripts/check-upstream-write-sites.sh` to forbid the unconditional primitives and their old names in `internal/upstream` and `internal/oauth`, with a one-file exception for the standalone clear in `internal/upstream/cli/client.go`; build `./cmd/mcpproxy` bare and with `-tags server -o /dev/null` (makes T048b, T048c and T048g pass; review R6.1, R6.2, R8.3)
- [x] T055f [US2] (PR2: the full purge for `Replaces` and the in-memory purge; the `OAuthEpoch = G` stamp and `RefreshCoordinator.Forget` are PR2b) Replacement purge: `ApplyServerCommit` runs the full `PurgeSet` for `Replaces` before saving the new row (`Incarnation = OAuthEpoch = G`); the in-memory purge and `RefreshCoordinator.Forget` run for replaced names (makes T048f pass; review R8.2)
- [x] **(PR2b)** T055e [US2] Refresh coordinator bound by credential binding: `RefreshRequest.Binding`; flights, latch, cooldown and `lastSavedAt` keyed by `(serverKey, binding)`; `RefreshOutcome.Binding`, hook and `RefreshManager` ignore outcomes of another binding; `Forget(serverKey, binding)` from `OnTokenCleared`, the purge and the OAuth-config clear; both call sites pass the instance binding (makes T048d pass; review R7.1)
- [x] T055c [US2] (PR2: the completion purge by decoded `ServerName`, `RegisterServerIdentityIfIncarnation`, `RecordToolCallIfIncarnation`; completion events carrying the incarnation are PR2b with the OAuth binding) Completion events carry the incarnation and the consumer drops stale ones; the completion purge matches by decoded `ServerName`; `RegisterServerIdentityIfIncarnation`, `RecordToolCallIfIncarnation` (`code_execution` exempt) (makes T046b pass)
- [x] T056 [US2] REST: `generation`/`incarnation` in server and review payloads; optional `expected_incarnation` on approve/block/unquarantine; 409 `server_replaced`; CLI message passthrough; `oas/swagger.yaml`; `docs/api/rest-api.md`
- [x] T057 [P] [US2] Web UI: send `expected_incarnation` from the review and approval actions; vitest in `frontend/tests/unit/`; Playwright sweep per `docs/development/web-ui-verification.md`
- [x] T058 [US2] Test-only `mcpproxy debug purge-report` (build tag `qa_hooks`) for live L2 inspection
- [ ] T059 [US2] (race/lint/e2e matrix, live L2, evidence and docs done in the PR; the cross-model review is run by the lead) The race/lint/e2e matrix (as T031); live L2; evidence `s116-pr2`; Sol review; docs (`docs/features/security-quarantine.md` "Removal purges")

**Checkpoint (PR2b)**: #1562's credential half and #1607 are closed — every
OAuth write, read and clear of an instance is bound to its committed
credential binding.

**Checkpoint (PR2)**: the at-rest half is done — a removal purges the whole
stored and in-memory purge set in its commit, and a re-add starts clean of
every record that existed when the removal committed. #1559 is closed. #1562
stays OPEN until PR2b: an in-flight write of the old instance (a `SaveToken`
or DCR persistence parked across the removal, T2.9, T2.13-T2.16) can still
recreate credentials after the purge, and only the PR2b credential binding
refuses it. Approval, history and scan writers are already incarnation-bound
in PR2.

---

### PR2b implementation note (as shipped, 2026-10-13; #1607)

Branch `fix/1607-oauth-incarnation-binding`, from `main` after PR3 and PR4.
The tests live in `internal/storage/oauth_binding_test.go` (T2.18, T2.20,
T048e, the bound writes and reads, `CheckCommittedView`, bound completion
events), `internal/oauth/oauth_binding_test.go` (T2.9, T2.13, T2.15 a/b with
the R7.3 same-binding variants, T2.16), `internal/upstream/core/
oauth_binding_test.go` (T2.9 through a real authorization-code exchange
against `tests/oauthserver`), `internal/upstream/manager_binding_test.go`
(binding threading, a binding move unregisters, T2.14 at the manager),
`internal/runtime/spec116_pr2b_test.go` (commit stamping, T2.14 with the real
commit parked at `afterStorage`, the OAuth half of T2.17) and
`internal/runtime/configsvc/oauth_epoch_test.go` (the epoch rule). On the
parent they fail to build (the API is new); with the storage binding check
or the revision check mutated out they fail (evidence `1607`). Deviations
from the design text:

- **Zero binding.** The zero `CredentialBinding` is "unbound" and acts on
  the current state of the name: once a commit was recorded an unbound write
  needs the row to exist (a removed server's write is refused) and takes the
  row's epoch; an unbound read sees every record. Daemon instances are always
  bound (the manager stamps every instance from its desired view); unbound
  callers are the standalone CLI and fixtures. Refusing every unbound write
  once a commit exists would break `mcpproxy auth login` without a daemon.
- **Record epoch only.** Records carry `OAuthEpoch` (and `Revision`), not
  the incarnation: an epoch value is the generation of one commit, so it
  identifies the binding of one name; the bound writers check both
  incarnation and epoch on the row.
- **Upgrade safety.** A server with no stored stamp (a database written
  before Spec 116) gets an incarnation and keeps epoch 0, the epoch of every
  record written before PR2b, so an upgrade signs nothing out. An unchanged,
  stamped entry whose OAuth state the commit clears only because the stored
  row differed (projection repair) keeps its stamp.
- **`CheckCommittedView` lives in the manager**, in `addServerConfigAt` and
  `ReplaceInstanceAt` (every constructor), with the binding from the
  manager's desired view (recorded by `AdvanceGenerations` from the
  snapshot), installed by the runtime through `SetCommittedViewChecker`. A
  storage read error lets construction proceed (the bound writes still
  guard the credentials); a missing or moved row refuses it.
- **Unexported primitives.** The unconditional `BoltDB` mutators stay
  exported for fixtures and tests; the write-site script forbids them (and
  their old names) in non-test `internal/upstream/**` and `internal/oauth/**`
  instead, and allows `ClearOAuthTokenForStandaloneLogout` only in
  `internal/upstream/cli/client.go`.
- **Login-path clears.** The two stale-DCR clears of the login flow
  (`oauth_stale_dcr_client.go`) keep their client-id predicate inside the
  bound transaction (`ClearOAuthClientIfBoundAndClientID`): that flow holds
  the client id it sent, not the record it was read from.
- **Refresh coordinator.** `RefreshRequest.Binding` keys flights, latch,
  cooldown, re-registration state and `lastSavedAt`; `ClearClientAt` clears by
  the revision of the record the failed request itself read (`rec`), never
  the post-request re-read; when the re-read's revision differs (a
  same-binding replacement rewrote the registration with the same client id
  and refresh token, Sol r5 on #1621) the invalid_client failure is
  superseded: no clear, no latch. The same holds when the rewrite lands
  after that check but before the clear (Sol r6 on #1621): the
  revision-checked clear clears nothing, `handleInvalidClient` reports it,
  and the flight skips the latch and the terminal outcome. When the clear
  succeeds but a same-binding writer re-saves a registration before the
  terminal re-read (Sol r7 on #1621: same client id and refresh token, so
  the flight's generation again), the re-read still carries a client id
  that the flight's own clear removed: that record is not the one cleared,
  so the flight skips the latch and the terminal outcome and resets its
  re-registration state for that client id. `Forget`/`ForgetServer` run for purged and
  OAuth-cleared servers in commit step 6 and on logout. The RefreshManager
  ignores a terminal outcome whose binding is not the registered instance's
  (`Runtime.CurrentCredentialBinding`), in addition to the existing
  expiry match. Each `RefreshSchedule` also carries the binding its token was
  saved under (`TokenStoreManager.NotifyTokenSavedFor` →
  `RefreshManager.OnTokenSavedFor`), and the outcome is matched against it
  while the schedule is selected under the manager lock, so a removal and
  re-add (or an OAuth-config commit) that lands between the resolver check
  and the selection cannot hand the old flight's failure to a replacement
  token with the same expiry (Sol r1 on #1621). A schedule with no binding
  (loaded from storage at startup) relies on the resolver check alone.
  A save notification from a binding older than the schedule's (bindings
  are commit generations, so older is decidable without the runtime) does
  not replace it: a bound save that committed before a removal or an
  OAuth-config change and notified late leaves the replacement's schedule,
  expiry, binding and timer alone (Sol r3 on #1621).
- **In-memory recent-completion flag.** `TokenStoreManager`'s
  recent-completion record (which lets a just-signed-in server skip the
  automatic OAuth deferral) carries the binding of the instance that
  completed the login. `MarkOAuthCompletedWithDBBound` sets it only after
  the bound completion event was written (a refused write records nothing),
  core instances mark and read it through `MarkOAuthCompletedBound` /
  `HasRecentOAuthCompletionBound` with their binding, and a completion of
  another binding is not recent for a same-name replacement, which keeps
  its deferral (Sol r8 on #1621). Unbound readers and unbound records (the
  standalone CLI) keep the name-only semantics.
- **Served revision after a reactive refresh.** `PersistentTokenStore`
  records as its served revision the record that holds the token it served:
  after a reactive refresh, the revision the refresh flight itself wrote
  (its persist reports it to the flight) or, for a flight that wrote nothing
  (another flight rotated the grant), the revision it read
  (`RefreshCoordinator.DoServed`). It is never a re-read after the flight: a
  same-binding replacement that rewrote the record since, even with the same
  access token, must not be adopted and then erased (Sol r3 on #1621). The
  5xx `ClearToken` then
  erases the refreshed token, while a write that replaced it afterwards
  still survives (Sol r1 on #1621). Any successful refresh moves the served
  revision, including one that returned the same access token with a new
  expiry or refresh token, which persistence still writes as a new revision
  (Sol r2 on #1621).
- **Superseded failures.** A login whose result storage refused, or whose
  instance was retired, is not recorded on the server status by name
  (`core.reportOAuthFailure`, `TokenStoreManager.RecordOAuthFailure`): the
  live check showed it flipping the re-added server from "pending auth" to
  an error state.
- **Refresh flights on `main`.** A refresh result parked across a removal
  was already discarded on `main` by the Spec 113 FR-006a generation CAS
  (the purge removed the record the flight started from), so the live
  baseline reproduces the bug with a login's code exchange, whose save has
  no flight generation.

### Review round 8 (Sol) design notes

- Approval writers that read tool definitions from the StateView (the bulk enable/disable inventory `collectKnownToolNames` and the record the toggle synthesizes for a record-less tool, `snapshotToolMetadata`) read it only when the view was reconciled from the incarnation the writer is bound to (`viewToolsAt`; 0 = unbound fixture database, unchanged). A view lagging a remove + same-name re-add therefore never has the removed server's descriptions, schemas or hashes persisted as the replacement's approval records; the index and approval-record fallbacks need no check because the removal commit purged both (`TestToolToggle_R8_LaggingViewNotPersistedUnderReplacementIncarnation`).
- The global tool listing applies the #1064 quarantine exclusion to the server whose rows it served: when the settled tool read moved to a replacement incarnation, the replacement's `GetAllServers` row is re-read and a quarantined replacement contributes no rows or statistics; a row that moved again is reported in `failed_servers` (`TestGlobalTools_R8_*`).
- Web UI: `getToolApprovals` joins the inventory's hold evidence onto the export's records only when both envelopes name the same incarnation; otherwise the records are returned without evidence, as for a failed enrichment fetch.
- Test oracle: `purgetest.Counts` finds `server_<id>_tool_calls` history buckets independently of surviving identity records (a bucket holding a record of the server counts), so a purge that drops an identity but keeps its history is reported.

### Review round 7 (Sol) design notes

- Between a removal and a same-name re-add the committed snapshot has no such server, so there is no incarnation a decision could bind to. A snapshot a commit published (it carries server generations) is authoritative for absence: `Runtime.GetServerTools` answers "server not found" (REST 404) for a server it does not configure, and `GetAllServers` skips such a server's lagging StateView row, so neither the per-server nor the global listing serves the removed server's rows unbound (`TestServerTools_R7_RemovedServerNotServedFromLaggingView`). A snapshot without generations (fixture or pre-boot) is not authoritative and keeps the old behavior.
- The per-server tool listing, the tools export and the global tool listing read the incarnation before AND after their rows (`readAtSettledIncarnation`) and return the rows only when it did not move; otherwise they read again, up to three times, and then answer 409 `server_replaced` (the global listing reports the server in `failed_servers`). A same-name server landing between an "absent" incarnation read and the row read therefore never yields unbound replacement rows (`TestServerTools_R7_*`, `TestToolExport_R7_*`, `TestGlobalTools_R7_*`). No new lock edge: both are lock-free snapshot reads.

### Review round 6 (Sol) design notes

- A server identity id is a hash of the configuration and does not include the incarnation, so a same-config re-add registers the SAME identity id as the removed server, and the round-5 identity check alone passes. Replay therefore also requires the original call record itself: before dispatch (`ToolCallRecordExists`) and again inside the history write's transaction (`RecordReplayedToolCallIfIncarnation(record, inc, originalCallID)`). The removal commit purged that record with the history bucket and nothing recreates it, so a replay of a removed server's call is refused and B's history stays empty (`TestReplay_R5_OriginalReadBeforeReplacementRecreatesNothing/replaced_same_config`, `TestRecordReplayedToolCall_R6_SameConfigReplacementRequiresOriginalRecord`).
- The supervisor's retained state and the StateView carry the committed `Incarnation` they were reconciled from (`ServerState.Incarnation`, `stateview.ServerStatus.Incarnation`, taken from the configuration snapshot's server generations). Reconcile no longer carries retained tools, the discovery stamp or the tool count across an incarnation change: it moves the connection generation, so a discovery result captured before the replacement is dropped at publish, and it re-kicks discovery for a connected replacement. Incarnation 0 (a snapshot without generations) is the same server, not a replacement (`TestReconcile_R6_ToolsNotCarriedAcrossIncarnation`).
- `Runtime.GetServerTools`, the only tool source of the per-server and global REST listings, serves the StateView's tools only when the view's incarnation equals the committed one. A view lagging a remove + same-name re-add falls through to the index, which the removal commit purged. The listings therefore never stamp the removed server's definitions with the replacement's incarnation (`TestServerTools_R6_LaggingViewNotServedUnderReplacementIncarnation`). A late discovery publish from the removed server's still-running worker remains PR3 (#1563). No new lock edge: the view check is a lock-free snapshot read, and the reconcile change runs under the existing `stateMu`.

### Review round 5 (Sol) design notes

- Activity replay reads the original call (and its identity) before it can know the server name, so it cannot capture the incarnation first. Instead, after capturing the incarnation it refuses the replay ("tool call not found") when the original call's identity no longer exists or names another server: the removal commit purged it, so the call belongs to a server that is gone and nothing is dispatched to the replacement. The history write uses `RecordReplayedToolCallIfIncarnation`, which also requires that identity in the write's own transaction, so a replay never recreates a purged identity or its bucket (`TestReplay_R5_OriginalReadBeforeReplacementRecreatesNothing`, `TestRecordReplayedToolCall_R5_RequiresOriginalIdentity`).
- Web UI: an optimistic tool toggle (single and bulk) rolls back only while the screen still shows the review it started on (same visit, server name, row incarnation and store incarnation), and the single toggle restores its row by object identity, not by index. A late `server_replaced` failure therefore leaves a same-name replacement's rows and quarantine counts alone.

### Review round 4 (Sol) design notes

- Direct and nested tool calls take the history incarnation from the shared tool gate, which reads it BEFORE the persisted server record the history is built from. A remove and same-name re-add that lands after the gate leaves the call bound to the removed incarnation, so its history write is refused instead of recreating the removed server's identity and bucket (`TestToolGate_R4_IncarnationReadWithRecord`).
- Scanner engine: a job whose incarnation is no longer current is not the name's active scan (`GetActiveJob`, `CancelScan`), and `StartScan` detaches it (as the purge observer would) instead of refusing the replacement with "already in progress"; the identity-checked cleanup still leaves the replacement's slot alone (`TestActiveJob_R4_ReplacementDoesNotInheritRemovedScan`).
- An unquarantine without `expected_incarnation` captures the incarnation current when it arrives (`CaptureApprovalIncarnation`, the same step 1 every approval writer runs) and commits bound to it, so a remove and re-add before its commit answers `server_replaced` and leaves the replacement quarantined (`TestUnquarantine_R4_UnboundBindsAtArrival`). Quarantining stays unbound.
- REST: unquarantine and `security/approve` map a server that is not configured to 404, as `docs/api/rest-api.md` documents (`TestApprovalWrites_R4_MissingServerIs404`).

### Review round 3 (Sol) design notes

- The in-memory purge bumps a per-server last-good purge sequence (under `lastGoodToolsMu`, inside the approval lock). The two local reindex paths (`reindexFromLastGoodSnapshot`, `reapplyLastGoodSnapshot`) read the snapshot and the sequence together and re-check the sequence under the approval lock before any write, so a copy selected before a remove and same-name re-add is dropped instead of being applied to the replacement with generation 0.
- Bulk enable-all/disable-all checks the incarnation under the approval lock when no per-tool write ran (empty or all-config-denied inventory), so a stale `expected_incarnation` gets `server_replaced` rather than a successful no-op.
- Scanner: summary-cache entries record the incarnation they were computed for and are served only for the current one; the purge observer reads the incarnation inside `DetachObsoleteJob`'s predicate, under the engine mutex.
- Docs disclose that in-flight OAuth writes are not incarnation-bound until PR2b.

### Review round 2 (Sol) design notes

- The removal purge set is built from the committed removals as well as the stored rows: a configured server whose projection row is missing still has its approvals, credentials, scan evidence and last-good snapshot purged.
- The OAuth refresh-schedule cancel and the inspection-exemption drop of a purged server run inside the commit section (under L0, after L1 is released; both are leaf locks), not after the commit lock is released, so a later commit's same-name re-add can never have its fresh schedule or exemption cleared by name. The supervisor's reconcile for a dropped exemption still runs after unlock.
- A tool-call history write ensures, in its own transaction, that a server identity names its hash-keyed bucket, so the purge, `PurgeSetCounts` and the orphan sweep find history recorded before the connect worker registered the identity.
- Scanner: engine registration re-checks the job's incarnation under the engine mutex (ordered against `DetachObsoleteJob`, which runs after the removal published); startup `CleanupStaleJobs` saves through the conditional writer, binding a legacy unbound job to the incarnation read before its list.
- Web UI: the review screen drops explicit choices and the Tools view drops batch selections when the incarnation on screen changes (selection keys carry the row incarnation).

### Review round 1 (Sol) design notes

- Server views (`GetServerReview`, `GetAllServers`, both paths) read the displayed configuration and the generation/incarnation from ONE committed snapshot, so a same-name re-add can never pair the old command or URL with the new incarnation.
- Unbound (incarnation 0) conditional writes of tool-call history, scan jobs and scan reports are refused once a config commit exists; the unbound fallback stays only for a database no commit has written. Replay captures the incarnation before resolving the client and rejects a server with no committed identity.
- The scanner binds Pass 2 and its failed-source placeholder to the originating Pass 1 job's incarnation (Pass 2 is skipped when it is no longer current), refuses to scan a server the committed view does not know, routes every save through the conditional writer, and the purge observer detaches an active job of an obsolete incarnation (identity-checked cleanup keeps a replacement's job).
- The tool listings carry the incarnation read before their rows (`incarnation`, `server_incarnation`); the Web UI binds global Tools batch actions and Server Detail per-tool decisions to the incarnation of the rows on screen, refuses a decision on stale rows, and the review screen shows a failed per-tool decision on every server.

## PR3: Generation-bound workers and supervisor (US3; #1563)

Branch `116-pr3-generation-workers` from `main` after PR1 merges. It is
independent of PR2. (Implemented as `fix/116-pr3-stale-workers`, from `main`
after PR1, PR2, #1603 and Spec 117 PR-1.)

**Implementation note (PR3 as shipped).** The runtime-level tests live in
`internal/runtime/spec116_pr3_test.go` (T3.1, T3.2, T3.3, T3.4, T3.5b, T3.6,
T3.7, T3.9; all failing on `main`, output in the PR evidence) and the manager
tests in `internal/upstream/manager_generation_test.go` (T064, T3.8, T3.10;
they need the new API, so on the parent they fail to build). T3.5(a) is the
existing `TestCaptureQuarantinedToolDefinitions_DropsReplacementBeforePersist`
and `_RelistsAfterClientReplacement`, now driven at the committed generation.
Deviations from the design text, each documented in the PR:

- `unregisterLocked` also marks the instance unregistered
  (`managed.Client.MarkUnregistered`); `Connect` on it returns
  `managed.ErrInstanceUnregistered`, and a dial that was in flight when it was
  unregistered discards its result. Without this a stale holder (a worker, the
  reconnect loop, `ConnectAll`) could reconnect an instance the manager no
  longer serves and leak its process; the baseline live run shows that leak.
  It is a connect-level latch only: the byte-level `Retire` barrier is PR4.
- `AdvanceGenerations` is handed every server of the published configuration
  (unchanged ones too) plus the removed ones, so the desired view is complete
  after the boot (and admission-gate) commit; it returns
  `AdvanceResult{Unregistered, Restamped}`. A registered instance behind the
  committed generation is restamped, or unregistered when it does not run
  the committed connection settings.
- `ReplaceInstanceAt(name, gen, expected, cfg)` returns the new instance and
  takes an optional config; the caller connects that exact instance
  (`Manager.ConnectInstance`). `AddServerAt` likewise connects the instance it
  installed, never the name (the round-6 shape recorded on #1563).
- `ActionReconnect` has had no disconnect leg since #1603, so only
  `ActionDisconnect` carries the observed `InstanceID`. `ActionRemove` calls
  `RemoveServerAt(name, 0)`, which acts only while the committed configuration
  has no such name (tombstone).
- The legacy unbound `AddServerConfig`/`AddServer`/`RemoveServer` stay for
  tests and CLI-only managers; for a name a commit recorded they install at the
  committed generation and never move it. The extended write-site script
  forbids them (and `DisconnectServer`) in `internal/runtime` and
  `internal/server`, and forbids `m.X(...)` calls of them and any `m.clients`
  write outside `unregisterLocked`/`installLocked` in `internal/upstream`.
- The OAuth logout (`TriggerOAuthLogout`) resolves the instance once and
  disconnects it with `DisconnectServerAt` (the hazard recorded on #1563); the
  token clear stays name-keyed (OAuth binding is #1607).
- The last-good tool snapshot is stamped with the instance generation only;
  the incarnation is already covered by the PR2 purge sequence.
- `actor_pool_complex_reference.go` (unreferenced, unbound manager calls) is
  deleted. The test-only discovery wrappers (`applyDifferentialToolUpdate`,
  `persistQuarantinedToolDefinitions`, `captureQuarantinedToolDefinitionsFromTools`)
  move to a test file and bind to the server's committed generation, since the
  generation-0 exemption of D15 is gone.
- `qa_hooks`: `MCPPROXY_QA_RELEASE_DIR` names the release directory; one call
  per park point is parked at a time (a later commit's worker passes through).

### Tests first (must fail on PR1)

- [x] T060 [P] [US3] `internal/runtime/worker_generation_test.go`: T3.1 (a/b/c) and T3.2, parking with `beforeWorkerAct`
- [x] T061 [P] [US3] `internal/runtime/supervisor/stale_action_generation_test.go`: T3.3 with the **real** `ActorPoolSimple` + `upstream.Manager` (no settle-before-act)
- [x] T062 [P] [US3] `internal/runtime/restart_generation_test.go`: T3.4
- [x] T063 [P] [US3] `internal/runtime/capture_generation_test.go`: T3.5
- [x] T061a [P] [US3] `internal/runtime/supervisor/stale_disconnect_test.go`: T3.6 (an old `ActionDisconnect` parked, then a newer commit installs and connects the replacement, then release → the replacement stays connected)
- [x] T061b [P] [US3] `internal/runtime/supervisor/stale_disconnect_restart_test.go`: T3.9 (a real disconnect captured for instance I1 parked; `RestartServer` installs I2 at the same generation; release → `ErrStaleGeneration`, I2 stays connected; review R5.2)
- [x] T063a [P] [US3] `internal/runtime/lastgood_stamp_test.go`: T3.7
- [x] T064a [P] [US3] `internal/runtime/replace_same_name_instance_test.go`: T3.10 (one-commit remove and re-create of a connected server with an equal entry → old instance unregistered, new `InstanceID`, new connect; review R8.2)
- [x] T064 [P] [US3] `internal/upstream/manager_generation_test.go`: `*At` methods; `AdvanceGenerations` unregisters vs restamps; a removed tombstone refuses re-registration at the old generation and is overwritten by a create

### Implementation

- [x] T070 [US3] `upstream.Manager`: `desired`, `AddServerConfigAt`, `RemoveServerAt`, `ConnectServerAt`, `AdvanceGenerations` (under captureMu W + mu; restamps retained instances with `SetConfigAt` only when `desired[name].gen` moves and deletes PR1's `RestampRetained`, with T1.19 kept unchanged, review R9.1; `GenerationChange.Replaced` always unregisters, review R8.2, makes T064a pass); `managed.Client.InstanceID()`; `DisconnectServerAt(name, gen, inst)`, `ReplaceInstanceAt`; one `unregisterLocked` helper for every delete or overwrite of `m.clients` (its `Retire` hook is a no-op until PR4) (makes T064, T061a and T061b pass; reviews R5.2, R5.4)
- [x] T071 [US3] `commit.go` step 7 calls `AdvanceGenerations(changes)`; the instances it returns are disconnected after unlock
- [x] T072 [US3] Post-commit workers use `*At` with `(name, g)` (makes T060 pass)
- [x] T073 [US3] Supervisor: actions carry `ServerGen.Gen` from their snapshot, and instance-targeted actions (`ActionDisconnect`, the first leg of `ActionReconnect`) also carry the observed `InstanceID`; `ActorPoolSimple` uses `*At` (its name-resolving `DisconnectServer`, `actor_pool.go:146-154`, is replaced); `ErrStaleGeneration` → debug + no-op (makes T061 pass)
- [x] T074 [US3] `RestartServer` → `manager.ReplaceInstanceAt(name, gen, expected)` (unregister + retire the old instance through `unregisterLocked`, then a fresh instance with a new `InstanceID` at the same generation) (makes T062 pass)
- [x] T075 [US3] Capture and discovery carry the instance generation and drop on mismatch; `lastGoodTools` stamped `{Incarnation, Gen}` and reapply rejects a mismatch (makes T063 and T063a pass)
- [x] T075a [US3] `ForceReconnectAll` + Docker recovery → `ReplaceInstanceAt` (T3.8, `internal/upstream/force_reconnect_generation_test.go` written first); the `(config, generation)` pair is read with one atomic load (`managed.Client.ConfigAt`) and re-read when only a restamp moved the generation (T3.8b, Sol PR3 round 1)
- [x] T075b [US3] Limiter publication is bound to the registry section that decided it (Sol PR3 round 2): every registry mutation (`AdvanceGenerations`, `AddServerConfigAt`, `RemoveServerAt`, `ReplaceInstanceAt`, the unbound legacy methods) and the hot-reload republication take `Manager.limiterPubMu` (L4') before releasing `mu` and hold it across the Spec 093 publication, so a stale action parked after its unlock cannot retire or overwrite the limiter a newer commit published (`internal/upstream/manager_generation_limiter_test.go`, written first)
- [x] T076 [US3] Extend `check-upstream-write-sites.sh` (including manager-internal calls in `internal/upstream/manager.go`, review R4.1): forbid `AddServerConfig(`, `AddServer(` and `RemoveServer(` on the manager inside `internal/runtime/**` and `internal/server/**` outside tests; forbid `delete(m.clients` and `m.clients[...] =` in `manager.go` outside `unregisterLocked` and the install helper (review R5.4)
- [x] T077 [US3] `qa_hooks` build tag: `MCPPROXY_QA_PARK_WORKER`, `MCPPROXY_QA_PARK_SUPERVISOR_DISCONNECT` (L3 step 3); live L3; evidence `s116-pr3`; the race/lint/e2e matrix; Sol review (the cross-model review is run by the lead)

**Checkpoint**: stale workers and actions are inert. #1563 is closed.

---

## PR4: Retirement barrier, OAuth included (US4; #1564)

Branch `116-pr4-retirement-barrier` from `main` after PR3 merges.

### Tests first (must fail on PR3)

- [x] T080 [P] [US4] `internal/upstream/dialgate/gate_test.go`: Admit/Retire ordering, conn Write after Retire → `ErrRetired`, a blocked Write unblocked by Retire, Retire bounded (< 1 s with a hung peer), idempotent, under `-race -count=50`
- [x] T081 [P] [US4] `internal/upstream/retire_stdio_test.go` and `internal/upstream/retire_launch_admission_test.go`: T4.1 (marker launcher; Docker via a mock docker binary; launcher; each with an unretired control and the launch observer; review round 2)
- [x] T082 [P] [US4] `internal/upstream/managed/retire_http_test.go`: T4.2 for HTTP and SSE × remove/replace/quarantine, synchronized on "commit returned"
- [x] T083 [P] [US4] `internal/transport/retire_retry_test.go`: T4.3 (flaky reused connection forces a retry; zero hits after retirement; Retire bounded)
- [x] T084 [P] [US4] T4.4 (code exchange and refresh; no secret or refresh token after retirement; `RefreshOAuthToken` on a retired name refused). As shipped: refresh with stored credentials parked and retired in `internal/upstream/core/retire_test.go` (`TestRetire_StoredCredentialRefresh_NoTokenRequest`); mcp-go's authorization-code exchange through the gated OAuth client, its token POST parked, in `internal/oauth/retire_preflight_test.go` (`TestRetireOAuthConfig_CodeExchangeParkedRefused`); `Manager.RefreshOAuthToken` on an instance retired after lookup returns "server not found" with zero upstream requests and no reconnect fallback, and both manual-login entry points retired mid-flight return the mapped error with zero requests and zero browser launches, in `internal/upstream/retire_manual_oauth_test.go` (review round 3)
- [x] T085 [P] [US4] `internal/runtime/retire_capture_test.go`: T4.5; `internal/upstream/retire_inflight_call_test.go`: T4.6
- [x] T081a [P] [US4] `internal/upstream/core/retire_stdio_write_test.go`: T4.8 (a launched stdio instance; `Retire` returns; a parked `SendRequest`/`SendNotification` resumes → `ErrRetired`, the child's stdin receives nothing, and the process group is dead when `Retire` returns)
- [x] T084a [P] [US4] `internal/oauth/retire_preflight_test.go`: T4.9 (park `preflightPRMScopes`, `autoDetectResource` and `httpFetchRaw` before sending; retire; zero hits on the counter)
- [x] T085a [P] [US4] `internal/upstream/dialgate/nesting_test.go` + `internal/upstream/core/retire_launch_registration_test.go`: T4.10, T4.11
- [ ] **(deferred: Windows follow-up)** T085b [P] [US4] `internal/upstream/core/process_windows_job_test.go` (windows build tag, T4.12) and `internal/upstream/core/retire_browser_test.go` (T4.13)
- [x] T085c [P] [US4] `internal/upstream/retire_same_generation_test.go`: T4.15 (old instance parked before its stdio launch and before its HTTP/OAuth send; `RestartServer`, `ForceReconnectAll` and Docker recovery replace it at the same generation; release → no launch, no request; replacement unaffected; review R5.4)
- [x] T085d [P] [US4] `internal/upstream/dialgate/late_register_test.go`: T4.16 (dial parked before `track`; sweep finishes; register + blocking write → closed, `ErrRetired`, `Retire` ≤ 1 s; review R5.1)
- [x] T085e [P] [US4] `internal/upstream/dialgate/capability_parity_test.go` (`TestWrapTransportCapabilityParity`, `TestMCPGoTransportMethodSetPinned`), `internal/upstream/dialgate/wrap_forwarding_test.go` (server-to-client ping over stdio and Streamable HTTP, SSE connection loss, retired request handler; review round 2) and `internal/upstream/core/retire_test.go` (`TestWrappedTransportConsumers`): T4.17 (a)-(f) with the production wrapped transports: OAuth handler extraction, concurrent reactive + proactive refresh, per-method-auth manual login, Streamable HTTP 404 session re-init, protocol header, server-to-client ping over stdio and Streamable HTTP, SSE connection loss, retired request handler (review R8.1)
- [x] T086 [P] [US4] `internal/upstream/dialgate/bench_test.go`: T4.7

### Implementation

- [x] T090 [US4] `internal/upstream/dialgate`: `Gate`, `gatedConn`, `Transport(base)`, `DialContext` (dial **and** `track` in one admitted section), retirement-aware `track` (latch checked under `connsMu`; close on retired), `Admit`, `Retire` (latch set under `connsMu`; second conn sweep after the barrier) (makes T080, T085d and T086 pass; review R5.1)
- [x] T091 [US4] `managed.NewClient` creates the gate; the CLI client gets an always-open gate; `Disconnect` does not retire (only the manager retires)
- [x] T092 [US4] `core`: stdio `client.Start`, Docker launch and launcher spawn under `gate.Admit` (makes T081 pass)
- [x] T093 [US4] `transport/http.go`: every branch builds on `gate.Transport(base)` instead of `http.DefaultTransport` (trace, plain, headers, OAuth, SSE) (makes T082 and T083 pass)
- [x] T093a [US4] Lock-free, capability-preserving `dialgate.WrapTransport` around every core-built mcp-go transport (`gatedStdio`/`gatedStreamableHTTP`/`gatedSSE` forwarding exactly their inner kind's capability rows, `Unwrap()`, retired-aware `SetRequestHandler`); `core.innerTransport` in `core/transport_unwrap.go` used by `extractOAuthHandler` and `SessionSnapshot`; T099 forbids `GetTransport()` elsewhere in `internal/upstream/**` (makes T085e pass; review R8.1); stdio `cmd.Start` + group/Job registration + `TrackProcess` in one admitted section, with a kill on a Job assignment failure; `Retire` kills before and after its barrier (makes T081a, T085a pass)
- [ ] **(deferred: Windows follow-up; `openBrowser` under `gate.Admit` is done)** T093b [US4] Windows: `CREATE_SUSPENDED` → `AssignProcessToJobObject` → `NtResumeProcess` inside the admitted section (stdio, Docker, launcher); `openBrowser` under `gate.Admit` (makes T085b pass)
- [x] T093c [US4] `launcher.WaitForURL` takes the gate dialer; Docker diagnostic/recovery execs under `gate.Admit`; teardown exemption documented (T4.14, `internal/upstream/core/retire_launcher_probe_test.go` written first)
- [x] T094a [US4] `oauth.Env{Transport, Ctx}` threaded through `preflightPRMScopes`, `autoDetectResource`, the discovery cache fetch and `CreateOAuthConfig*` (makes T084a pass)
- [x] T094 [US4] `oauth`: `WithBaseTransport` on `CreateOAuthConfig*` and the transport wrapper `inner`; the core OAuth strategy and manual OAuth pass the instance transport; `Manager.RefreshOAuthToken`/`StartManualOAuth*` refuse an unregistered or retired instance (makes T084 pass)
- [x] T095 [US4] `unregisterLocked` calls `Retire()` on the instance it removes, before returning. This covers `AdvanceGenerations` (and so before publish) and the same-generation `ReplaceInstanceAt`/`RemoveServerAt` paths (restart, `ForceReconnectAll`, Docker recovery), which retire the old instance before installing the replacement (makes T085 and T085c pass; review R5.4)
- [x] T096 [US4] A retired-instance call or probe maps to the existing unavailable error and does not feed call-stats or health for the replacement
- [x] T097 [US4] Live fixtures `counting_http_fixture.py` and the marker launcher; `qa_hooks` `MCPPROXY_QA_PARK_DIAL` / `MCPPROXY_QA_PARK_OAUTH`; live L4; evidence `s116-pr4`
- [x] T099 [US4] Extend `check-upstream-write-sites.sh` with the FR-024a ungated-HTTP grep (allowlist file `scripts/ungated-http-allowlist.txt`)
- [ ] T098 [US4] The race/lint/e2e matrix; SC-004 measurements recorded; Sol review (transport + OAuth chunk separately); docs (`docs/architecture.md` "Retirement barrier")
  - Status (review round 4, 2026-10-13): open. The race/lint/e2e matrix, the Sol review rounds and the docs are done. SC-004 is now measured with the specified workloads by `internal/runtime/spec116_sc004_test.go` (`MCPPROXY_SC004_MEASURE=1`, in-process, 25 servers): a commit with no index work has p99 20.0 ms (n=300, budget 50 ms), and the Retire of one connected instance has p99 42 µs (n=40, budget 100 ms; the whole removal commit has p99 39.8 ms). **A removal with a 320-tool index purge has p99 6.26 s (n=40, budget 500 ms) and misses the budget.** The cause predates this PR: `BleveIndex.DeleteServerTools` deletes one document per bleve transaction. It is tracked in #1617. Tick T098 once #1617 lands and the measurement passes.


### PR4 implementation note (as shipped, 2026-10-13)

Branch `fix/116-pr4-retirement-barrier`. Deviations from the design text,
each documented in the PR:

- `Retire` signals instead of polling: it SIGKILLs every tracked process
  group (Windows: closes the Job) before and after its barrier and returns
  without waiting for the kernel to reap the children; the reap belongs to
  the transport's own `Close`. Polling up to 1 s for exit would hold the
  manager's locks (L3 W + L4 W) for that long on every stdio removal.
  T4.8/T4.11 poll for the group's death after `Retire` returns.
- Windows suspended creation (T093b) and the Windows Job test (T085b, T4.12)
  are not in this PR: Windows keeps today's Job assignment right after
  `Start`, now inside the admitted section, so `Retire` reaches the Job; a
  grandchild forked between `Start` and the assignment can still escape it
  (pre-existing). Follow-up.
- Docker CLI calls other than the launch (`docker ps`/`rm` before a launch,
  `docker logs`, `inspect`, `stop`) are not admitted sections: they talk to
  the local Docker daemon, never to the upstream, and are ownership checks or
  teardown. The `docker run` launch is gated as the stdio/launcher start.
- stdio sends: the wrapper refuses after the latch (mcp-go v1.0.0 owns the
  stdin pipe, so a byte-level gate is not possible there); a send that passed
  the latch before `Retire` can reach the child's stdin, and `Retire` kills
  the process group before it returns.
- `oauth.Env` carries the instance transport (and the caller's context for
  the resource auto-detection only). Cached discovery requests keep their
  background context, so one caller's cancellation never poisons the shared
  cache; a flight refused by a retired gate is not cached, and a waiter that
  joined it runs its own fetch.
- The refresh coordinator treats a refusal by a retired gate as having no
  side effects (no latch, cooldown or client clear), and a caller that
  joined such a flight runs one of its own. Binding flights and token writes
  to the incarnation stays #1607 (`oauth.Env` and this refusal path are the
  hooks).
- `GatedConn.Write` checks the latch before touching the socket, so in T4.16's
  bypass variant the write returns `ErrRetired` immediately and the second
  sweep closes the connection.
- The single capability helper is `core.innerTransport` in
  `internal/upstream/core/gate.go` (not `transport_unwrap.go`); the write-site
  script allows `GetTransport()` only there.
- The manual OAuth flow's transient core clients share the registered
  instance's gate (`core.Client.SetGate`), so retiring the instance stops a
  login it started (browser, registration, code exchange).
- A streamable-HTTP session `DELETE` on the teardown of a retired instance is
  refused by the gate: nothing reaches the retired endpoint.
- Test placement: T4.1/T4.2/T4.5/T4.6/T4.11/T4.15 in `internal/upstream`
  (they drive the manager's real `unregisterLocked`), T4.3/T4.7/T4.10/T4.16
  and the parity tests in `internal/upstream/dialgate`, T4.4 (stored-credential
  refresh), T4.8, T4.13, T4.14, T4.17 (a)(d)(e: protocol header) in
  `internal/upstream/core/retire_test.go`, T4.9 and the OAuth-config part of
  T4.4 in `internal/oauth/retire_preflight_test.go`. T4.17 (b)(c) are the
  existing `oauth_refresh_race_test.go` and `per_method_auth_e2e_test.go`,
  which now run over the wrapped transports (they build the client like
  `CreateHTTPClient` with a gate: gated OAuth and MCP HTTP clients, a
  `dialgate.WrapTransport` wrapper, and assert the gate dialed their traffic).

#### PR4 review round 1 (Sol) design notes

- A gated connection's `Read` reports `ErrRetired` (wrapped around the
  socket error; `io.EOF` stays as is) when the gate is retired. A response
  cut off by retirement is then a retirement everywhere: the discovery cache
  does not store it, the refresh coordinator charges it to no grant, and the
  managed client answers "unavailable".
- `tools/list` maps any failure on a retired instance (led or coalesced) to
  `managed.ErrInstanceRetired`, as a tool call does, and moves no state
  machine (FR-026). T4.5 now retires with a list in flight and a second
  caller coalesced onto it.
- The refresh coordinator: a caller that joined a retired instance's flight
  tries again until it gets an answer that is not a retirement or leads a
  flight of its own; a leader's refusal is final. One retry was not enough:
  another call on the retired instance can start a new refused flight first.
- The launcher child is tracked by the gate until `Stop` has reaped it, and
  the retirement kill is `launcher.Handle.Kill` (the process group on Unix,
  the launcher's own Job Object on Windows; core's Windows Job registry never
  held launcher children).
- stdio: when the child is already dead at registration (a retirement during
  the launch section cancelled the gate context, which kills only the
  immediate child) the process group it led is taken from how it was started
  (`Setpgid`, pgid = pid), so the group kill still reaches its descendants.
  The marker fixture now keeps its command line (no `exec`) and starts a
  descendant; T4.11 waits for it and fails if any survives.
- The FR-024a scan checks every `http.Client{` literal on a line within its
  own braces.
- The OAuth browser launch is admitted only, not tracked: the opener
  (`open`/`xdg-open`/`rundll32`) hands the URL to a browser that is not the
  instance's process (docs corrected).

#### PR4 review round 2 (Sol) design notes

- The launcher records the process group it leads at spawn, and
  `Handle.Kill` signals that group also after the leader exited (a launcher
  that backgrounds a descendant with redirected streams closes `Done` while
  the descendant runs on). Once the leader is reaped the group ID is ours
  only while a member lives, and while one does the kernel does not hand the
  number out as a PID: a live process holding the number as its PID means
  the group is gone and the number reused, and `Kill` leaves it alone.
  (Superseded in review round 6: after the reap the group is never
  signalled; see the round-6 notes.)
- A process registration's kill is serialized with `Process.Untrack`: from
  `Untrack`'s return no kill of that registration runs (a kill `Retire`
  already started has finished). The owner untracks BEFORE it reaps the
  child: `Disconnect` releases the stdio child before the MCP close (which
  reaps it), and the post-OAuth reconnect does the same, so a retirement
  never signals a process-group ID the OS may have reused.
- FR-025a's "a failed Job assignment kills the child inside the section":
  Windows `assignProcessTree` now fails when the Job cannot be created or
  assigned, and the stdio launch section kills the child and fails the start
  (it used to log and carry on with the PID). `CREATE_SUSPENDED` (T093b)
  stays deferred.
- A probe (background health ping or the post-cancellation liveness probe)
  that was in flight when the instance was retired produces no verdict and
  no accounting: `setErrorIfCurrentConnection` and the ambiguous-probe
  verdict check the retired flag, and the health loop returns after the ping
  when the instance was retired meanwhile (FR-026).
- `RefreshCoordinator.Do` normalizes a nil context before its retry loop.
- The FR-024a scan tracks brace depth: each open `http.Client{` literal has
  the depth of its own body, and only a `Transport:` key at that depth gates
  it (a nested client's or a nested struct's does not; a func literal's
  closing brace does not close the client).
- T4.1 covers the Docker (mock docker binary) and launcher branches as well
  as plain stdio, each with an unretired control; a launch observer seam
  (`core.SetLaunchObserverForTest`) sees a child the gate would kill before
  it ran, so a marker file is not the only evidence. For stdio and Docker
  the parked-before-admission window is closed three times over (`Admit`,
  the gated transport's `Start`, the gate context the child is started
  under); the tests fail only with all three removed.
- T4.17's server-to-client ping (stdio and Streamable HTTP) and SSE
  connection loss are exercised through the production wrappers
  (`internal/upstream/dialgate/wrap_forwarding_test.go`); dropping the
  `SetRequestHandler` or `SetConnectionLostHandler` forwarding fails them.

#### PR4 review round 3 (Sol) design notes

- FR-025a's fail-closed containment now covers the launcher too: on Windows
  a launcher child whose Job Object cannot be created or assigned is killed
  and `launcher.Spawn` fails, inside the admitted launch section (it used to
  degrade to a single-process kill that left descendants running). The
  containment step is the `containTree` seam, so the shared failure path is
  tested on every platform.
- The launcher's reused-group guard keys on a `reaped` flag the reaper sets
  under `reapMu` as soon as `cmd.Wait` returns, not on `Done` (which closes
  only after the reaper logs). `Handle.Kill` runs under the same lock.
- A gated connection's `Write` that was admitted before the latch and is cut
  off by the sweep reports `ErrRetired` wrapped around the socket error, like
  `Read`. A refresh POST cut off by retirement therefore sets no cooldown
  for the grant the replacement instance uses.
- `StartManualOAuthQuick` and `StartManualOAuthWithInfo` map a login on an
  instance retired meanwhile to "server not found" (FR-027), as
  `RefreshOAuthToken` does; the browser launch is reported to the launch
  observer.
- The FR-024a scan matches `http.Client` with whitespace or a comment
  before its brace and spaced selectors (`http . Client{`,
  `http . DefaultTransport`, `http. Get(`).

#### PR4 review round 4 (Sol) design notes

- The launcher reaper now also reaps under `reapMu`. It first waits for the
  leader's exit without reaping it: `waitid(WNOWAIT)` on Linux, and a kqueue
  `EVFILT_PROC`/`NOTE_EXIT` event on Darwin and the BSDs. The zombie keeps
  the PID and the group ID reserved. Only then does it take the lock, call
  `cmd.Wait` and set `reaped`. A `Handle.Kill` that sees `reaped == false`
  therefore signals while the leader still holds the group ID. Platforms
  without a non-reaping wait keep the round-3 order. Windows has no group ID
  to reuse.
- When the gate refuses to track a stdio child that a retirement cut off,
  the launch section clears `processGroupID` before the transport's `Close`
  reaps the child. Connect's failure cleanup then has no released group ID
  that it could signal.
- The FR-024a scanner joins a selector that is split after its dot (`http.`
  at the end of a line) with the next code line, past blank and comment
  lines. The state resets for each file.
- SC-004 is measured by `internal/runtime/spec116_sc004_test.go`
  (`MCPPROXY_SC004_MEASURE=1`). The 320-tool purge misses its budget for a
  reason that predates this PR, tracked in #1617, so T098 stays open.

#### PR4 review round 5 (Sol) design notes

- When the non-reaping exit wait is not available (kqueue refused under fd
  exhaustion, `waitid` denied), the Unix reaper no longer falls back to a
  `cmd.Wait` outside `reapMu`. It polls a non-blocking `wait4(WNOHANG)`
  under `reapMu`, with a backoff capped at 50 ms. Each attempt finds the
  leader still running or a zombie, which releases nothing, or it reaps the
  leader and publishes `reaped` before it drops the lock. `Kill` is never
  held up by a live child. `cmd.Wait` then runs only for its cleanup, and
  the exit status comes from the `wait4` status. On this degraded path that
  status is a plain error, not an `*exec.ExitError`. Windows keeps
  `cmd.Wait`.
- The FR-024a scanner also tracks slice, array and map literals of clients:
  `[]http.Client{`, `[N]`, `[...]`, `map[K]`, multi-dimensional, and the
  `*http.Client` element forms. Each element brace, with its type elided,
  is a client literal whose `Transport:` key counts at its own depth. A
  `[]http.Client {` result type after a closing parenthesis opens a func
  body, not a literal.

#### PR4 review round 6 (Sol) design notes

- The launcher no longer signals a process group after its leader was
  reaped. The round-2 guard (signal only when no live process holds the
  number as its PID) could not tell our group from an unrelated group that
  reused the number and whose own leader was reaped too, and its check and
  signal raced with reuse. Instead the reaper, after observing the leader's
  exit without reaping it, keeps the zombie while another non-zombie member
  of its group runs (`awaitGroupEmpty`: the `kern.proc.pgrp` sysctl on
  macOS, `/proc/<pid>/stat` on Linux, polled with a backoff capped at
  250 ms). The zombie pins the group ID, so `Kill`, and `Stop`'s SIGTERM and
  SIGKILL, signal the group soundly in that time. All of them run under
  `reapMu` and only while `reaped` is false. `Done` stays open while a
  descendant the launcher left behind runs, so `Stop` on disconnect now
  brings that descendant down too. When the members cannot be listed (the
  BSDs, or a listing error) the reaper SIGKILLs the rest of the group under
  `reapMu` just before the reap, while the zombie still pins the ID. On the
  degraded path without a non-reaping exit wait (`reapUnobserved`) a
  descendant left in the group is not killed: an orphan is the safe
  failure, signalling a reused group is not (documented assumption).

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
