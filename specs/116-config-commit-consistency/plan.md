# Implementation Plan: Config Commit Consistency, Atomic Removal and Upstream Retirement

**Branch**: `116-config-commit-consistency` | **Date**: 2026-10-11 | **Spec**: [spec.md](spec.md)
**Input**: issues #1558, #1559, #1560, #1562, #1563, #1564, plus the new findings B1 and B2 ([research.md](research.md) §2)

## Summary

One primitive, `Runtime.Commit`, serializes every change to the server set or
to a server's fields. It reads the live config inside `configCommitMu`, mutates
a copy, and runs one pipeline: gate, prepare generations, disk, one bbolt
transaction (with the purge on removal), advance the manager (retire stale
instances), publish. The live config becomes the only source. Storage is a
projection written in the same commit, and `SaveConfiguration` no longer
rebuilds anything from storage, so there is nothing to resurrect from. Each
server carries a generation that changes when its entry changes, and an
incarnation that changes only on remove followed by create. Asynchronous
workers and the supervisor act through manager methods that check the
generation inside the manager's own registration lock. Approval writers check
the incarnation under the server's approval lock against the storage row. Each
client instance owns a dial gate that the commit retires before it publishes,
so a retired instance can start no process launch, dial or connection write,
OAuth requests and `net/http` retries included.

The work ships as **four independently mergeable PRs from `main`**, in order
(§PR split). PR2 and PR3 depend only on PR1. PR4 depends on PR3.

## Technical Context

**Language/Version**: Go 1.26 (backend). The Web UI changes only to send `expected_incarnation` (PR2).
**Primary Dependencies**: existing only: bbolt, bleve, zap, mark3labs/mcp-go v1.0.0, `net/http`. **No new dependencies.**
**Storage**: bbolt `config.db`. Additive fields `UpstreamRecord.Generation` and `.Incarnation`, meta key `config_commit_generation`. No migration.
**Testing**: `go test -race` with deterministic hook-gated tests (test seams in §Test seams). They are run at `-count=20` and at `GOMAXPROCS=1` and `4`. `./scripts/test-api-e2e.sh`. The CI unit-tests `-skip` lane with `-tags server`. Live verification on the qa070 harness ([quickstart.md](quickstart.md)).
**Target Platform**: both editions. The server edition shares the runtime paths unchanged. Linux, macOS and Windows: the dial gate is pure Go, and stdio admission wraps `cmd.Start` on every OS, including the Windows Job Object path.
**Performance Goals**: SC-004 (commit p99 < 50 ms without index work, removal < 500 ms with a 320-tool purge, retirement adds < 100 ms).
**Constraints**: no `native/` changes. Both lint passes stay clean. The #1570/#1571/#937/#1061 test assertions stay unchanged.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at scale | ✅ | The commit path is rare. The request path gains one `RLock` per connection `Write` on gated conns, which is uncontended outside `Retire`. A benchmark in PR4 (T4xx) |
| II. Actor-based concurrency | ⚠️ justified | Adds no new long-held lock. It reuses `configCommitMu` and adds a per-instance `RWMutex` barrier in `dialgate`. See Complexity Tracking |
| III. Configuration-driven | ✅ | No new config keys. The config file stays the source of truth at boot |
| IV. Security by default | ✅ | Closes the TPA purge bypass (B1), inheritance of trust and credentials on re-add (#1562), and dials after retirement (#1564). Fail-closed quarantine invariant kept |
| V. TDD | ✅ | Each task group starts with failing hook-gated tests ([tasks.md](tasks.md)) |
| VI. Documentation hygiene | ✅ | `docs/architecture.md` (commit pipeline, lock order), `docs/api/rest-api.md` + `oas/swagger.yaml` (PR2 fields), a CLAUDE.md Recent Changes line per PR |
| Core + tray split | ✅ | The tray is untouched. It reads the same REST |
| Event-driven updates | ✅ | Events are sent after the commit, once per commit |
| DDD layering | ✅ | Commit logic lives in `internal/runtime`. `internal/server` and `internal/httpapi` become thin adapters |
| Upstream 3-layer design | ✅ | The gate is created in `managed`, passed to `core`, and used by `transport` and `oauth`. The CLI client gets an always-open gate |

## Project Structure

### Documentation (this feature)

```text
specs/116-config-commit-consistency/
├── spec.md
├── plan.md            # this file
├── research.md        # code facts, rejected-branch lessons, decisions D1-D11
├── data-model.md      # types, generations, purge set, gate
├── quickstart.md      # deterministic test recipes + live harness checks per PR
├── contracts/
│   └── surfaces.md    # REST/MCP/CLI error and field deltas
└── tasks.md           # grouped by PR, TDD order
```

### Source code (touched, by PR)

```text
PR1  internal/runtime/commit.go (new), commit_reconcile.go (new; replaces LoadConfiguredServers' storage phase)
     internal/runtime/configsvc/{service.go,snapshot.go}      # Prepare/Publish, ServerGenerations, SeedVersion
     internal/runtime/{runtime.go,lifecycle.go,server_sharing.go,config_load_admission_gate.go}
     internal/storage/{manager.go,bbolt.go,models.go}           # ApplyServerCommit, Generation/Incarnation, meta key
     internal/config/server_entry_equal.go (new)
     internal/server/{server.go,config_cow.go,mcp.go,add_registry_source.go,edit_registry_source.go,remove_registry_source.go}
     internal/httpapi/server.go                                 # PATCH resolution moves into the callback
     scripts/check-upstream-write-sites.sh (new) + .github/workflows wiring
PR2  internal/storage/{purge.go (new), manager.go (ClearOAuthState exact match)}
     internal/runtime/{commit.go (RemoveServer purge), tool_quarantine.go, tool_approval_lock.go}
     internal/server/{prompt_quarantine.go, server.go, mcp.go}  # removal callers collapse onto Commit
     internal/security/scanner/service.go                       # incarnation-bound approve + ExpectIncarnation unquarantine
     internal/httpapi/*approval*  frontend/src/** (send expected_incarnation)  oas/swagger.yaml  docs/api/rest-api.md
PR3  internal/upstream/manager.go (desired, *At, AdvanceGenerations)
     internal/upstream/managed/client.go (Generation)
     internal/runtime/supervisor/{supervisor.go,actor_pool.go}
     internal/runtime/{commit.go (step 7), lifecycle.go (workers, RestartServer, capture)}
PR4  internal/upstream/dialgate/ (new)
     internal/upstream/managed/client.go, internal/upstream/core/{client.go,connection_stdio.go,connection_http.go,connection_launcher.go,connection_oauth.go}
     internal/transport/http.go, internal/oauth/{config.go,transport_wrapper.go}
     internal/upstream/manager.go (Retire in AdvanceGenerations; RefreshOAuthToken/StartManualOAuth refuse retired)
```

## Commit pipeline

Steps 1-8 run inside `configCommitMu`. Steps 4-8 are skipped when nothing
changed.

| # | Step | Failure behaviour |
|---|---|---|
| 1 | **Build**: `base := configSvc.Current()`, `next := deep clone`, run `fn(tx)` | `fn` error → abort, nothing written |
| 2 | **Gate**: admission gate on `next` against the stored view. Force `Quarantined=true` where storage keeps a quarantine without an explicit decision. Validate (`ValidateDetailed`, trust modes, forward headers) | validation error → abort. The stored view cannot be read → abort with nothing written (the projection diff needs it); only the boot commit continues, writing nothing to storage or disk, with the gate abstaining (#937), as `main`'s boot sync did (implementation review R1) |
| 3 | **Prepare**: `configSvc.Prepare(next)` runs the pre-publish hook and computes `G`, generations and incarnations. Compute `Changes` | — |
| 4 | **Lock and hooks**: approval locks for changed names (sorted; a name with a hook counts as changed), then `tx.UnderApprovalLock` hooks (Spec 117 review-bound server approval: validate, decide, baseline and blocks, promote) | hook error (for example a stale review) → release locks, abort, nothing written |
| 5 | **Disk** (when a path exists): before writing, capture `prevDisk := readDiskState(path)`, the **actual file state** read from disk inside L0: `{exists, bytes, mode}` (review R9.2). Then the pending-aware write of `next` plus its self-write marker | abort. Hook writes from step 4 stay, which is fail-safe: a promoted tool on a still-quarantined server is unreachable |
| 6 | **Storage**: `ApplyServerCommit` (one bbolt tx: saves carrying their prepared per-server stamps (review R9.3), deletes and purges, meta `G`; it runs for every commit that publishes, a global-only one included, so `meta.config_commit_generation` never lags the published generation, implementation review R1), then the index purge for removed and newly quarantined servers, then the in-memory purge. Release the approval locks | tx error → compensating disk restore of `prevDisk`: write `prevDisk.bytes` back byte-for-byte with its mode (temp file + rename, as the normal save does), or remove the file when `prevDisk.exists` was false. Forget this commit's self-write marker and record one for the restored bytes. Leave `desiredCfg` and the pending-restart report untouched. Abort (reviews R5.5, R9.2) |
| 7 | **Advance manager**. PR1: `RestampRetained(changes)` gives every retained instance its new config and generation (review R9.1). PR3/PR4: `AdvanceGenerations(changes)` unregisters, retires (PR4) and restamps, and replaces `RestampRetained` | cannot fail (in-memory) |
| 8 | **Publish**: `configsvc.Publish(prepared)` and the legacy `r.cfg`/`desiredCfg` swap | `ErrConcurrentPublish` is an invariant bug: logged as an error, the commit returns an error, and the state is still consistent because nothing else can publish |
| — | **After unlock**: spawn workers bound to `(name, g)` for created, replaced and reconnecting servers; request one discovery pass (`DiscoverAndIndexToolsForServer`) for each restamped connected server; asynchronous `Disconnect` of retired instances; events, telemetry, activity | logged |

**Compensation restores the bytes that were on disk (reviews R5.5, R9.2).**
It does not rebuild a file from memory. `pendingAwareDiskConfig`
(`runtime.go:698-716`) reconstructs a config from the live snapshot and
`desiredCfg`, and it returns `nil` when nothing restart-gated is pending, so
it is not the file. Two cases show the difference. With a restart-required
change pending, disk intentionally differs from the live snapshot (R5.5,
T1.16). And a disk-writing commit (a server toggle, a PATCH) can run while
the file holds an operator edit that no reload has applied yet, so
compensating from memory would erase that edit (R9.2, T1.18 variant c). The
raw bytes cover these cases, and the absent-file case too. The restore records
a self-write marker for the restored bytes, so the watcher does not re-run a
reload in a loop.

`ReloadConfiguration` itself never writes the file (`tx.noDisk()`): a failed
reload of an edit A→A+B leaves the file at A+B with nothing to compensate,
and live config and storage at A (T1.18 main case). What happens to B next
depends on the next writer (review round 3 on PR1; corrects R9.2):

- the next **reload** (the watcher on the next edit, or a manual reload)
  reads the file and applies B;
- the next **disk-writing commit** (`SaveConfiguration`, a server add,
  update, toggle or removal, an `ApplyConfig`) builds from the live config, so
  on success it writes A plus its own change and **B is gone from disk**. This
  is `main`'s behaviour too (its `SaveConfiguration` writes the in-memory
  config), and making such commits merge unapplied file edits is out of scope
  for Spec 116. If that commit fails in storage, compensation restores A+B.

So the "disk ahead of live" state after a failed reload (assumption A2) lasts
until the next reload or the next disk-writing commit, whichever comes first,
and only the reload keeps B. An external write that lands between the capture and the
restore is overwritten. That window lies inside one commit, and today's
`SaveConfiguration` already has it.

**Retained instances are updated in PR1 (review R9.1).** On `main` every
config reconcile calls `AddServer` for every enabled server
(`lifecycle.go:1570-1592`). When the connection fields are unchanged,
`AddServerConfig` calls `SetConfig` on the retained client
(`upstream/manager.go:484-496`). That is the only path by which a
non-connection edit (`forward_headers`, `expose_prompts`, `disabled_tools`,
concurrency limits) reaches a live instance. The supervisor ignores such edits
(`supervisor.go:564`, `ConnectionEquivalent`). PR1 replaces the reconcile with
the commit, and its after-unlock workers run only for created, replaced and
reconnecting servers, so PR1 must carry this path itself. Step 7 is therefore
not a no-op in PR1. For every `Updated` change with `Reconnect == false`,
`upstream.Manager.RestampRetained` (under L3 W + L4, the same edges as
`AdvanceGenerations`) calls `mc.SetConfigAt(cfg, g)` on the registered
instance. That stores `{cfg, g}` as one atomic pair, and only when `g` is
greater than the instance's current generation, so a late legacy worker
cannot roll the config back. `SetConfigAt` takes no lock (atomic stores, as
`SetConfig` today). The limiter update (`applyServerConcurrency`) runs after
L4 is released, as on `main`. A revoked `forward_headers` entry therefore
stops being forwarded before the commit returns, because `forwardPolicy` reads
the config on every call (`managed/client.go:794-806`). The instance's
generation now equals the row's, so D15 accepts the next discovery pass, and
the after-unlock discovery request replaces any pass that D15 dropped for
carrying the old generation. PR3 folds this into `AdvanceGenerations`, which
restamps only when `desired` moved (§PR3).

Steps 7 and 8 always run in that order, so publication never comes before
retirement. Before PR3 lands, step 7 only restamps retained instances, and the
after-unlock workers use today's manager methods.

### Implementation review round 1 amendments

- **Skip rule and the desired config**: a commit that publishes nothing (both
  diffs empty) still records `tx.desired` when the caller set one. A reload
  that only cancels or changes a restart-gated value therefore resets the
  desired config, and the next write does not re-persist the cancelled value.
- **Restart refresh** (`RestartServer`'s disk import, FR-003) is a commit with
  no disk step: the file is its source and is never rewritten from the live
  config, so an unreloaded edit of another server or a global field stays on
  disk for the watcher.
- **Reconnect predicate**: `upstream.Manager.AddServerConfigAt` replaces an
  instance exactly when `config.ConnectionEquivalent` is false, the predicate
  that marks a change `Reconnect`. A narrower manager comparison kept the old
  isolation, OAuth, `working_dir` or init timeout on a retained instance.
- **Registry edit with a probe**: the commit applies the request to the live
  entry and copies only the probe-derived fields (`servers_url`, `protocol`).
- **D15 hold**: `applyDifferentialToolUpdateHeld` runs the discovery pass's
  last-good write and its StateView publish inside the approval-lock hold,
  after the final eligibility/generation check. The sweep publishes each
  server's StateView in its own hold instead of one batch afterwards.
- **Compensation mode**: the restore sets the captured mode on the temp file
  before the rename (`Chmod`, not filtered by the umask). `0000` is restored
  as `0000`.

## Lock order

Normative. It extends [research.md](research.md) D9.

```text
L0 configCommitMu                                   (runtime; outermost; only Commit takes it)
L1 server approval lock(name)  [sorted when >1]     (runtime.toolApprovalLocks; also guards prompt approvals after PR2)
L2 storage.Manager.mu → bbolt tx                    (leaf)
L1' index (bleve) writes                            (taken under L1 in step 6; leaf w.r.t. L0-L2)
L3 upstream.Manager.captureMu (W in commit step 7: RestampRetained in PR1, AdvanceGenerations from PR3; R in capture) → L4 Manager.mu
L5 dialgate.Gate.mu (W only in Retire; R per admitted section / conn Write)
L5' dialgate.Gate.connsMu (leaf: track/sweep; taken under L5 R in a dial section, and without L5 in Retire)
L6 configsvc.updateMu (leaf observers only; the pre-publish hook is removed, review R1.7)
L7 Runtime.mu                                        (leaf: field swaps only)
```

**Allowed nestings**: L0→L1→{L2, L1'}; L0→L3→L4→L5→L5'; dial section L5(R)→L5'; L0→L6; L0→L7;
capture path: L3(R)→L1(one)→L2 on `main`, or L1(one)→L3(R)→`epochMu` after Spec 117 PR-3 (either is compatible, because the commit never holds L1 and L3 together); L1(one)→L2 / L1' / L4(R) (discovery, approvals). Spec 117's global `captureSnapMu` sits above L1, and `Commit` never takes it.

**Forbidden** (each one has a test in PR1/PR2/PR3/PR4, `TestLockOrder_*`):
1. Calling `Commit` (directly or through `SaveConfiguration`, `ApplyConfig`, `ReloadConfiguration`, `UpdateConfig`, `QuarantineServer`, `EnableServer`, `RemoveServer`, scanner unquarantine) while holding any of L1-L7. Enforced by `TestLockOrder_*`, which drive each forbidden pairing concurrently with a commit under a 5 s watchdog that dumps all goroutines on timeout. Same-goroutine re-entry is caught by a `commitOwner` goroutine-id check that is compiled only under the `commitlockcheck` test build tag.
2. Taking L1 while holding L3 for **writing**. The commit releases L1 before step 7.
3. Taking more than one L1 outside `Commit`.
4. Taking L0-L5 inside a `configsvc` hook or observer (L6).
5. Taking any runtime lock from `upstream`, `managed`, `core`, `transport`, `oauth` or `supervisor`. These packages never call back into the runtime synchronously, except through the existing tool-discovery callback, which takes only L1(one).

**Credential-binding checks add no edge (review R6.1, R6.2).** `CheckCommittedView`
is one bbolt `View`, and `DeleteOAuthTokenIfBound`/`ClearOAuthClientIfBound`
are one bbolt `Update`. Both are L2 leaves taken with no runtime lock held,
from `oauth`/`core`/workers (rule 5). Neither waits on L0, L1 or L3, so a
worker parked in one cannot block a commit beyond bbolt's own serialization.
Keying `RefreshCoordinator` by `(serverKey, binding)` (review R7.1) changes
only the map key. Its `mu` stays a leaf that is never held across `Load`,
the network call or a storage write, as on `main`. `Forget` takes only that
`mu`, so calling it from the commit's in-memory purge adds an L1/L2 → coordinator
edge and no reverse edge.

**`createMu`** is deleted in PR1, because `configCommitMu` subsumes it. Only
the mutex goes: the #1570 *behaviour* (create-only refusal with
`ServerExistsError` against the live config and storage, delete-then-re-add
succeeding at once) is kept by `CommitTx.CreateServer` under `configCommitMu`,
and the #1570 test assertions are unchanged (review R2.8: "keep createMu
behaviour until the plan says to delete it" refers to that behaviour; this plan
deletes the mutex in PR1). **`asyncMgr`**
storage operations (`EnableUpstreamServer`/`QuarantineUpstreamServer`) are no
longer used by daemon paths, so the commit writes rows directly in its
transaction.

**Why there is no cycle.** Within a commit, L1 is released before L3 is taken
for writing. The only other L3→L1 holder is the capture path (L3 R). It can
wait for L1 while the commit holds L1, and the commit does not need L3 until
L1 is released. Discovery holds L1 and may take L4 (read) through `GetClient`,
but never L3 for writing. `AdvanceGenerations` takes L3 for writing and L4 and
never L1.

## Test seams

These are package-private hook fields, set only in tests and nil in
production, following the `toolApprovalReadHook` and
`quarantinedCaptureBeforePersist` precedent:

| Seam | Where | Used by |
|---|---|---|
| `commitHooks.afterBuild`, `.afterPrepare`, `.afterDisk`, `.afterStorage`, `.afterAdvance` | `runtime/commit.go` | T1.x, T2.x |
| `beforeWorkerAct(name, gen)` | runtime worker closure | T3.1, T3.2 |
| `supervisor.beforeExecuteAction(name, action)` | supervisor | T3.3, T3.6, T3.9 |
| `approvalHooks.afterCapture(name, inc)` | approval writers | T2.4, T2.17 |
| `approvalHooks.afterBaselineBeforeUnquarantine(name, inc)` | scanner `approveServer`, after the L1 section exits | T2.5 (review R5.6) |
| `approvalHooks.afterUnquarantineCommitBeforeBaseline(name, inc)` | `QuarantineServer(name, false)`, after its `Commit` returned, before the baseline promotion | T2.19 (review R9.5) |
| `commitHooks.diskWriteFault` / `commitHooks.storageFault` | `runtime/commit.go` (inject a failure into step 5 or `ApplyServerCommit`) | T1.16, T1.18 |
| `forwardHeaderProbe` (an `httptest` upstream recording request headers) | `upstream/managed` tests | T1.19 |
| `oauthHooks.beforePersist(name, binding)` | token store and DCR client save | T2.9, T2.13, T2.17 |
| `oauthHooks.beforeBindingCheck(name, sg)` | worker, before `CheckCommittedView` | T2.14 |
| `oauthHooks.beforeRefreshDo(name, binding)` | `PersistentTokenStore` / `RefreshOAuthTokenDirect`, after the caller's bound read, before `RefreshCoordinator.Do` | T2.16 |
| `oauthHooks.beforeClear(name, binding, op)` | `DeleteOAuthTokenIfBound` / `ClearOAuthClientIfBound` callers, before the storage tx | T2.15 |
| `restPatchAfterParse` | httpapi PATCH handler | T1.3 |
| `dialgate.TestHooks.beforeAdmit` / `afterAdmitBeforeAct` / `beforeTrack` / `afterSweepBeforeBarrier` | dialgate | T4.x, T4.16 |
| `transport` test RoundTripper that fails the first write on a reused connection | `transport` tests | T4.3 |

## PR split

Each PR merges from `main` independently in this order, ships its own tests,
and leaves `main` green. The scope stated for each PR is binding: a reviewer
finding outside it is filed against the later PR that owns it and is not fixed
in place. This is the guard against the rejected branch's scope creep.

### PR1: Commit critical section and generations (#1558, #1560, root of #1559; B1)

- **Scope**: `Commit`/`CommitTx`, two-phase `configsvc` with `ServerGenerations`, persisted generation, `ApplyServerCommit` (saves plus the existing row and history delete), the in-commit reconcile replacing `LoadConfiguredServers`' storage phase, `SaveConfiguration` persisting the live config, and every writer in FR-003 converted. PATCH and MCP update/patch resolve inside the callback. Quarantine purges the index inside the commit on every path (B1). `createMu` is removed. `check-upstream-write-sites.sh` is added. Also from design review: the discovery final check and index writes under L1 with a generation check (D15), approval policy read from storage (D16, with global-policy changes taking every L1), `ReconcileLive` vs `ReplaceConfig` (D14), projection repair and `Bootstrap`/`ForceDisk` (D13), removal of the pre-publish hook, and instance generation stamping (`managed.Client.Generation()`). From design review round 9: the retained-instance update at step 7 (`RestampRetained`, `SetConfigAt`, review R9.1), compensation from the raw pre-write file bytes (R9.2), per-save prepared stamps in `ServerCommitBatch` (R9.3), and the boot reconcile as `ReconcileLive()` + `Bootstrap()` (R9.6). The leaf package `internal/commitview` for the value types shared by `configsvc`, `storage`, `oauth` and `upstream` (review R7.2). Explicit same-name replacement: `CommitTx.replaced`, `Prepare(PrepareInput{…, Replaced})`, the `Replaced` change kind and `ServerCommitBatch.Replaces` (review R8.2).
- **Not in scope**: the full purge set (PR2), generation checks in workers or the supervisor (PR3), retirement (PR4). Removal still deletes only what each path deletes today, but now synchronously inside the commit for **all** paths (this closes the resurrection window).
- **Failing-first tests**: T1.1-T1.7, T1.17, T1.18 and T1.20 ([quickstart.md](quickstart.md) §T1). T1.19 and T1.21 are regression coverage: they pass on `main`, whose reconcile updates retained clients and schedules every enabled server, and they guard behaviour that the commit design would otherwise lose (reviews R9.1, R9.6). Each is also run against a deliberately broken commit (step 7 a no-op; boot without `Bootstrap()`) to show that it detects the loss.
- **Live verification**: L1 (concurrent apply, add and PATCH; B1 quarantine purge; a forward-header revoke on a connected server; a restart with matching rows).
- **Size estimate**: about 1,400 LOC production code and 1,200 LOC tests.
- **Risk**: the widest diff. Mitigations: the write-site script, unchanged #1570/#1571/#937/#1061 assertions, and `./scripts/test-api-e2e.sh`.

### PR2: Atomic removal, purge and incarnation-bound approvals (#1559 remainder, #1562; B2)

- **Scope**: `PurgeServer` in the commit transaction (data-model §PurgeSet), the exact OAuth key match (B2), the in-memory purge, all three removal paths on one `CommitTx.RemoveServer` (the removal goroutines deleted), the `OAuthEpoch` stamp and `CredentialBinding`-conditional OAuth writes, reads and deletes (review R5.3, R6.2), `ServerGen.OAuthEpoch` and the construction-time `CheckCommittedView` (review R6.1), the `oauth_tokens` record `Revision` behind every binding-conditional delete (review R7.3), refresh-coordinator flights keyed by credential binding (review R7.1), the full purge for a same-commit replacement (review R8.2), the standalone CLI logout converted to `BoltDB.ClearOAuthTokenForStandaloneLogout` with a one-file write-site exception (review R8.3), incarnation capture and re-check in every approval writer, prompt approvals under L1, scanner `ExpectIncarnation` unquarantine, REST `incarnation`/`expected_incarnation` with 409 `server_replaced`, and the Web UI sending it. From design review round 9: the unquarantine baseline promotion bound to the incarnation of the commit that unquarantined (R9.5), and `ClearOAuthClientIfBound` as a field-clearing rewrite that keeps the tokens (R9.4).
- **Depends on**: PR1.
- **Failing-first tests**: T2.1-T2.20, plus T048e (record revision). Both editions of `./cmd/mcpproxy` build.
- **Live verification**: L2 (remove and re-add `acme` under manual trust through REST, MCP and a config edit; OAuth fixture; `foo`/`foo_bar`).
- **Size estimate**: about 700 LOC production code and 900 LOC tests, plus a small frontend change.

### PR3: Generation-bound workers and supervisor (#1563)

- **Scope**: `Manager.desired`, `managed.Client.InstanceID()`, `AddServerConfigAt`/`RemoveServerAt`/`ConnectServerAt`/`DisconnectServerAt(name, gen, inst)`/`ReplaceInstanceAt` (including `ForceReconnectAll` and Docker recovery), the single `unregisterLocked` path for every removal from `m.clients` (review R5.4; its `Retire` call is a no-op until PR4), instance-targeted supervisor actions bound to `(gen, InstanceID)` (review R5.2), `AdvanceGenerations` (unregister or restamp, under L3 W + L4) called at commit step 7, replacing PR1's `RestampRetained` (restamp only when `desired[name].gen` moves, review R9.1), workers and supervisor actions and `RestartServer` moved to the `*At` methods, capture and discovery carrying the generation, the CI grep extended to forbid the legacy manager mutators in `internal/runtime/**`.
- **Depends on**: PR1 (generations). Independent of PR2.
- **Failing-first tests**: T3.1-T3.10.
- **Live verification**: L3 (parked worker via a debug-only hook build tag `qa_hooks`, or the timing variant in quickstart).
- **Size estimate**: about 500 LOC production code and 700 LOC tests.

### PR4: Retirement barrier, OAuth included (#1564)

- **Scope**: the `dialgate` package (with retirement-aware connection registration, review R5.1), the capability-preserving `WrapTransport` with `Unwrap` and its consumers (`extractOAuthHandler`, `SessionSnapshot` through `core.innerTransport`, and mcp-go's bidirectional, protocol-version, connection-lost and legacy-protocol assertions; review R8.1), the per-instance gate and transport, stdio/Docker/launcher admission, the HTTP/SSE gated transport, the OAuth `WithBaseTransport`, `Retire` called from `unregisterLocked`, so that every unregistration retires: `AdvanceGenerations` before publish, and `ReplaceInstanceAt`/`RemoveServerAt` at the same generation (restart, `ForceReconnectAll`, Docker recovery; review R5.4), refresh and manual OAuth refusing retired instances, tool calls and probes on retired instances mapped to unavailable, and the benchmark.
- **Depends on**: PR3 (`AdvanceGenerations` is the retirement point).
- **Failing-first tests**: T4.1-T4.17.
- **Live verification**: L4 (marker-file stdio launcher, an HTTP fixture counting requests and `Authorization` headers, a fake OAuth token endpoint).
- **Size estimate**: about 600 LOC production code and 800 LOC tests.

### Review protocol per PR

The reviewer is opencode `github-copilot/gpt-6.1-sol` (the default per maintainer
memory, 2026-10-07). The brief is split into file-named chunks (commit and
storage, writers, manager and supervisor, transport and OAuth). The cap is 10
rounds per PR. A round counts only when a genuine finding is fixed and
re-reviewed. Findings outside the PR's declared scope are recorded against the
owning later PR (see "Scope is binding" above).

## Risks

| Risk | Mitigation |
|---|---|
| A missed writer keeps writing storage directly, and its change is lost because `SaveConfiguration` no longer rebuilds from storage | `check-upstream-write-sites.sh` in CI (FR-009). The PR1 inventory in research §1 is exhaustive for `main` at `ee7834e71`. The script turns any new site into a CI failure |
| The commit holds `configCommitMu` longer (bbolt, bleve, retire) | SC-004 budgets. The index purge happens only for removal and quarantine. `Retire` is bounded with a 1 s warning |
| Changing `ApplyConfig` from "save disk, then async reconcile" to "reconcile in the commit" slows `PUT /config` | Measured in PR1. Storage writes take milliseconds, and connects stay asynchronous |
| Deadlock from a new edge | The lock order above, the `TestLockOrder_*` watchdog tests, and the `commitlockcheck` re-entry check |
| A per-instance `http.Transport` changes connection reuse | Distinct hosts per upstream in practice. Idle connections close on `Retire` and on `Disconnect` |
| Generation counter reset (meta lost) | `SeedVersion(max(meta, max row generation))` |
| Spurious 409 on approval during an unrelated edit | Approvals bind to the incarnation, not the generation (spec Definitions) |

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Keep and widen `configCommitMu` (a mutex, against principle II) | The commit must be synchronous for its callers (REST returns after the commit) and must serialize disk, bbolt and publish | A commit actor goroutine with a request channel gives the same serialization but loses re-entrancy detection, and callers still block on the reply. The mutex already exists and every whole-config path uses it |
| `dialgate.Gate` `RWMutex` barrier | `Retire` must establish a happens-before edge against every connection write and launch: "nothing starts after `Retire` returns" | Channels or context alone cannot order an in-progress `Write` or `cmd.Start` against retirement. Sol rejected flag and context approaches in r8, r9 and r10 |
| Generation **and** incarnation | Workers need "this exact config". Approvals need "this exact server identity" | Generation alone gives spurious approval rejections on unrelated edits. Incarnation alone lets stale workers install an old endpoint |
