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

- [ ] T001 [US1] Add test seams: `commitHooks` struct (nil in production) in `internal/runtime/commit_hooks_test_seams.go`, `restPatchAfterParse` hook in `internal/httpapi/server.go`
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
- [ ] T019 [P] [US1] `internal/runtime/lock_order_pr1_test.go`: `TestLockOrder_*` watchdog tests (commit vs capture section; commit vs discovery under the approval lock; a re-entrant `Commit` from inside `fn` is rejected)

### Implementation

- [ ] T020 [US1] `internal/config/server_entry_equal.go`: `ServerEntryEqual` (ignores Created/Updated; includes the explicit bit, Shared, AuthBroker) with a table test
- [ ] T021 [US1] `configsvc`: `ServerGen`, `Snapshot.ServerGenerations`, `Prepare`/`Publish`/`SeedVersion`. Re-express `Update`/`UpdateIfCurrent` on top. Remove `ReloadFromFile`'s raw publish (makes T017 pass)
- [ ] T022 [US1] storage: `UpstreamRecord.Generation/Incarnation`, `ApplyServerCommit` (one tx; saves through the quarantine-keeping invariant; deletes as today: row + call history), meta key (makes T018 pass)
- [ ] T023 [US1] `internal/runtime/commit.go`: `Commit`, `CommitTx` ops, pipeline steps 1-6 and 8 plus after-unlock (plan §Commit pipeline). Approval locks taken in sorted order for changed names in step 4, with `UnderApprovalLock` hooks. Compensating disk write on a storage failure. Re-entry guard
- [ ] T024 [US1] `internal/runtime/commit_reconcile.go`: move `LoadConfiguredServers`' storage phase into step 6 (diff against the stored view; OAuth-config-change clear; newly-quarantined index purge, FR-008; orphan GC on the working copy, with the empty guard at boot only). Keep `LoadConfiguredServers(nil)` as a thin `Commit("reconcile", ReplaceConfig(live))` wrapper for the boot path. Delete the removal goroutines (removal is synchronous in step 6)
- [ ] T025 [US1] after-unlock workers: spawn connect/disconnect workers for `Changes` (still the legacy manager calls; PR3 binds them), the events, the telemetry `NotifyConfigChanged`
- [ ] T026 [US1] `ApplyConfig`/`applyConfigLocked`, `ReloadConfiguration`, `UpdateConfig`, `UpdateListenAddress`, `SaveConfiguration` (persist the live config, no storage read), `gateInitialConfig`, `SetServerShared` → `Commit` (makes T011 pass)
- [ ] T027 [US1] `EnableServer`, `BulkEnableServers`, `QuarantineServer` (index purge in the commit; baseline approval after the commit, unchanged), `RestartServer`'s disk refresh → `Commit` (makes T013 pass)
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
- [ ] T042 [P] [US2] `internal/runtime/removal_exclusion_test.go`: T2.3 (removal parked at `afterStorage` vs B toggle, REST update, MCP patch)
- [ ] T043 [P] [US2] `internal/runtime/approve_incarnation_test.go`: T2.4 (re-add rejects; an unrelated `disabled_tools` edit does not reject) + T2.7 (discovery write after re-add dropped)
- [ ] T044 [P] [US2] `internal/security/scanner/approve_incarnation_test.go`: T2.5
- [ ] T045 [P] [US2] `internal/storage/oauth_exact_key_test.go`: T2.6 (B2)
- [ ] T046 [P] [US2] `internal/storage/toolhash_keys_test.go`: pin the `toolhash` key format (A11) so the purge decision is justified

### Implementation

- [ ] T050 [US2] `internal/storage/purge.go`: `PurgeServer` inside the `ApplyServerCommit` tx for each `Removes` entry; `PurgeReport`. Rewrite `ClearOAuthState` with the exact key match (`name` or `name_` + 16 lowercase hex) (makes T045 pass)
- [ ] T051 [US2] `commit.go` step 6: removal purge under L1; in-memory purge (index + profile reindex, `RefreshManager.OnTokenCleared`, `RevokeInspectionExemption`, toolsig drop, discovery generation reset); `CommitResult.Purged`; activity `purged` metadata (makes T040 and T042 pass)
- [ ] T052 [US2] Collapse REST `RemoveServer` and MCP `handleRemoveUpstream` onto `Commit(RemoveServer)` and delete their ad hoc cleanup (makes T041 pass)
- [ ] T053 [US2] Incarnation binding: `approvalHooks`; capture in `ApproveTools`, block/unblock, `approveBaselineToolsForServer`, `checkToolApprovals`/capture; re-check against the storage row under L1; `ErrServerReplaced` (makes T043 pass)
- [ ] T054 [US2] Prompt approvals (`internal/server/prompt_quarantine.go`) under L1 with the incarnation check
- [ ] T055 [US2] Scanner: incarnation captured in `approveServer`; baseline save under L1 with the check; `UnquarantineServer` → `Commit(ExpectIncarnation, SetQuarantined(false))` (makes T044 pass)
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
- [ ] T064 [P] [US3] `internal/upstream/manager_generation_test.go`: `*At` methods; `AdvanceGenerations` unregisters vs restamps; a removed tombstone refuses re-registration at the old generation and is overwritten by a create

### Implementation

- [ ] T070 [US3] `upstream.Manager`: `desired`, `AddServerConfigAt`, `RemoveServerAt`, `ConnectServerAt`, `AdvanceGenerations` (under captureMu W + mu); `managed.Client.Generation()` (makes T064 pass)
- [ ] T071 [US3] `commit.go` step 7 calls `AdvanceGenerations(changes)`; the instances it returns are disconnected after unlock
- [ ] T072 [US3] Post-commit workers use `*At` with `(name, g)` (makes T060 pass)
- [ ] T073 [US3] Supervisor: actions carry `ServerGen.Gen` from their snapshot; `ActorPoolSimple` uses `*At`; `ErrStaleGeneration` → debug + no-op (makes T061 pass)
- [ ] T074 [US3] `RestartServer` → `manager.ReplaceInstance(name, gen)` (retire + fresh instance at the same generation) (makes T062 pass)
- [ ] T075 [US3] Capture and discovery carry the instance generation and drop on mismatch (makes T063 pass)
- [ ] T076 [US3] Extend `check-upstream-write-sites.sh`: forbid `AddServerConfig(`, `AddServer(` and `RemoveServer(` on the manager inside `internal/runtime/**` and `internal/server/**` outside tests
- [ ] T077 [US3] `qa_hooks` build tag: `MCPPROXY_QA_PARK_WORKER`; live L3; evidence `s116-pr3`; the race/lint/e2e matrix; Sol review

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
- [ ] T086 [P] [US4] `internal/upstream/dialgate/bench_test.go`: T4.7

### Implementation

- [ ] T090 [US4] `internal/upstream/dialgate`: `Gate`, `gatedConn`, `Transport(base)`, `DialContext`, `Admit`, `Retire` (makes T080 and T086 pass)
- [ ] T091 [US4] `managed.NewClient` creates the gate; the CLI client gets an always-open gate; `Disconnect` does not retire (only the manager retires)
- [ ] T092 [US4] `core`: stdio `client.Start`, Docker launch and launcher spawn under `gate.Admit` (makes T081 pass)
- [ ] T093 [US4] `transport/http.go`: every branch builds on `gate.Transport(base)` instead of `http.DefaultTransport` (trace, plain, headers, OAuth, SSE) (makes T082 and T083 pass)
- [ ] T094 [US4] `oauth`: `WithBaseTransport` on `CreateOAuthConfig*` and the transport wrapper `inner`; the core OAuth strategy and manual OAuth pass the instance transport; `Manager.RefreshOAuthToken`/`StartManualOAuth*` refuse an unregistered or retired instance (makes T084 pass)
- [ ] T095 [US4] `AdvanceGenerations` calls `Retire()` on every instance it unregisters, before returning (and so before publish) (makes T085 pass)
- [ ] T096 [US4] A retired-instance call or probe maps to the existing unavailable error and does not feed call-stats or health for the replacement
- [ ] T097 [US4] Live fixtures `counting_http_fixture.py` and the marker launcher; `qa_hooks` `MCPPROXY_QA_PARK_DIAL` / `MCPPROXY_QA_PARK_OAUTH`; live L4; evidence `s116-pr4`
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
