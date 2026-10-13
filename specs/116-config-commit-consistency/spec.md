# Feature Specification: Config Commit Consistency, Atomic Removal and Upstream Retirement

**Feature Branch**: `116-config-commit-consistency`
**Created**: 2026-10-11
**Status**: Design (draft). Implementation is split into four PRs (see [plan.md](plan.md) §PR split)
**Input**: GitHub issues [#1558](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1558), [#1559](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1559), [#1560](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1560), [#1562](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1562), [#1563](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1563), [#1564](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1564). OpenCode Sol 6.1 found all six while cross-reviewing the oversized UX-01/UX-02 PRs (#1553, #1555). They were verified on `origin/main` and kept out of the minimal core PRs that landed as #1570 (create-only add) and #1571 (per-server tool-approval lock).

**Related**: Spec 032 (tool-level quarantine), Spec 100 (prompt approvals), Spec 093 (per-server limits), Spec 113 (upstream resilience), Spec 115 (credentials service, for the facade pattern), issue #937 (admission gate), #1061 (quarantine index purge), #1176 (call-history GC). Issue #1569 (tray) is handled separately in #1572. This spec does not touch `native/`. **Spec 117** (#1565-#1567, review-bound approvals) is designed in parallel. [research.md](research.md) §6 fixes the integration points: server approval as `Commit` + `UnderApprovalLock`, the baseline marker in the purge set, one `Retire()`, and an acyclic lock-order union.

## Context and Motivation

A server's state lives in four places:

1. the **live config**: the `configsvc` snapshot plus the legacy `Runtime.cfg`;
2. **storage**: the bbolt `upstreams` row plus the per-server buckets for tool approvals, prompt approvals, OAuth, scans and call history;
3. the **config file** on disk;
4. the **upstream manager**: the running client instance.

Today a change reaches these stores in different orders depending on the writer. Only some steps hold `configCommitMu`. Two flows run in opposite directions: `LoadConfiguredServers` copies config to storage, and `SaveConfiguration` rebuilds the config from storage. Asynchronous workers then act on snapshots that may already be stale. [research.md](research.md) §1 lists every writer with citations. The six issues are symptoms of that model:

| Issue | Symptom | Root cause |
|---|---|---|
| #1558 | A delayed reconcile deletes a server that was just added, prunes its approvals, or reverts a quarantine | `LoadConfiguredServers` runs on a captured snapshot outside the commit lock. `ApplyConfig` publishes before storage catches up. The add path reads `Config()` before it takes the lock |
| #1559 | A successful DELETE leaves the server configured and running | Removal is not one commit. `SaveConfiguration` rebuilds the server list from storage rows that are still waiting for asynchronous deletion |
| #1560 | Update, patch, quarantine or enable resurrects a removed server or reverts a security field | These writers read and write storage outside the lock and resolve partial updates from an earlier snapshot |
| #1562 | A same-name re-add inherits approvals and OAuth credentials | Each removal path purges a different subset of the name-keyed state, and approval writers are not tied to the server they reviewed |
| #1563 | A stale connect or disconnect worker, or a supervisor action, installs an old config after a newer commit | Workers are validated when they are scheduled, not when they run |
| #1564 | A removed, replaced or quarantined server still launches a process or receives credential-bearing requests | No client instance has a permanent retirement latch that covers stdio, HTTP, SSE, OAuth and retries |

This design session also found two related defects on `main` and verified them live:
- **B1**: MCP `quarantine_security quarantine_server` does not purge the index. The descriptions stay retrievable, which bypasses the #1061 control.
- **B2**: `ClearOAuthState("foo")` also deletes `foo_bar`'s OAuth tokens.

The evidence is in [research.md](research.md) §2.

The rejected branch `fix/qa070-ux01-create-only-add` added one patch for each reader of this racy model and needed ten review rounds ([research.md](research.md) §3). This spec takes a different approach. It removes the second source of truth and adds one primitive: a **config commit** that every writer uses and that carries a **per-server generation**. Asynchronous actors check that generation at the point where they act.

## Scope Boundary

| Reused, not rebuilt | Added or changed |
|---|---|
| `configCommitMu` (`runtime.go:117`) | `Runtime.Commit(reason, fn)` holds it for every server writer, including reconcile |
| `configsvc` snapshot and version (`configsvc/service.go:196`) | Two-phase `Prepare`/`Publish`; `Snapshot.ServerGenerations`; generation persisted in `meta` |
| Atomic `CreateUpstreamServer` and `ServerExistsError` (#1570) | Called by the commit. `createMu` is removed because `configCommitMu` gives the same exclusion |
| Per-server approval lock `lockToolApprovals` (#1571) | Taken by the commit for servers whose generation changes. Approval writers check the generation under it. Also covers prompt approvals |
| Admission gate (#937) and the `SaveUpstreamKeepingQuarantine` invariant | Run in commit pipeline step 2 (admission), against the stored view and before `configsvc.Prepare`, so no storage read happens under `configsvc.updateMu` (FR-001a) |
| `SetServerShared` (already a commit-shaped writer) | Becomes one more `Commit` caller |
| `Manager.captureMu` and `WithCurrentClientOnEpoch` | Stale instances are unregistered under `captureMu` inside the commit |
| Storage deleters (`DeleteServerToolApprovals`, `DeleteServerPromptApprovals`, `ClearOAuthState`, scanner `RejectServer` helpers, call-history GC) | One transactional `PurgeServer` with exact OAuth key matching |
| `runtime.CredentialsService` (Spec 115) | Not changed. Agent tokens and client credentials are not keyed by upstream name, so they are out of the purge set (A5) |

**Out of scope**: the tray (`native/`), the Web UI beyond sending `expected_incarnation` on approvals, the CLI except its error mapping, server-edition per-user upstreams, the #1576 schema fix, and changing what `tool_response_mode` or profiles mean.

## Definitions

- **Commit**: one execution of `Runtime.Commit`. It holds `configCommitMu` from reading the live config until the new config is published. Every change to the server set or to any server field is a commit.
- **Commit generation `G`**: a monotonic `int64` assigned to each commit. It equals `Snapshot.Version` and persists across restarts.
- **Server generation `g(name)`**: the commit generation at which the server's entry was created or last changed. It is stored in the published snapshot and on the storage row. An unchanged entry keeps its generation across unrelated commits.
- **Incarnation `inc(name)`**: the generation at which the name was last created. It stays the same through updates and changes only on remove followed by create. Approval writers bind to it, so editing an unrelated field never rejects an approval in flight. A same-name re-add always rejects one.
- **Instance**: one `managed.Client` object. An instance belongs to exactly one `(name, g)`.
- **Retire**: to permanently close an instance's dial gate. After `Retire` returns, the instance starts no process launch, dial or connection write.
- **Purge set**: every piece of name-keyed state that grants or caches trust or credentials for a server. See [data-model.md](data-model.md) §PurgeSet.
- **Approval writer**: any code that creates or changes tool-approval records, prompt-approval records, integrity baselines or tool blocks for a server, or that unquarantines it.

## Invariants

These are what the tests prove.

- **I1 (single source)**: after a commit returns, the live config, storage rows and config file all describe the same server set and fields (the disk-write failure case is A2). Nothing outside the commit writes an `upstreams` row.
- **I2 (linear history)**: commits are totally ordered. Each one starts from the result of the previous one, so no commit can overwrite a newer commit's changes with values it read earlier.
- **I3 (atomic removal)**: once a removal commit returns, the name is absent from the live config, storage, disk, the manager, the search indexes and every purge-set bucket. No later commit or worker brings it back unless an explicit create commit does.
- **I4 (clean re-add)**: a server created under a previously used name has a generation greater than every earlier generation for that name, and nothing from the purge set survives from them.
- **I5 (stale workers are inert)**: a manager registration, removal or connect bound to `(name, g)` has no effect unless `g` equals the manager's desired generation for `name`. The check runs in the same critical section as the change.
- **I6 (retirement barrier)**: when a commit removes a server, or changes its connection fields, `Enabled` or `Quarantined`, the old instance is unregistered and retired before the commit publishes. No launch of the upstream (its command, its container `run`, its launcher, the OAuth browser, or a Docker diagnostic/recovery exec), no TCP or TLS dial (readiness probes included), and no HTTP/SSE request byte or OAuth request byte (metadata, DCR, token, refresh, or a `net/http` internal retry) for that instance starts after that point. Teardown commands that end the instance (`docker stop/kill/rm`, process-group kill) are exempt.
- **I7 (incarnation-bound approvals)**: an approval writer that captured `inc(name)` writes nothing once a commit has removed the server, or removed and re-created it.
- **I8 (no deadlock)**: the lock order in [plan.md](plan.md) §Lock order holds on every path. No code holding a lower lock calls `Commit`.

## User Scenarios & Testing

### User Story 1: Concurrent changes never undo each other (Priority: P1), PR1

An operator applies a config through the Web UI while an agent adds a server over MCP and the file watcher reloads a hand edit. Every change that reports success is in the final state, and no stale snapshot reverts it.

**Independent Test**: hook-gated `-race` tests that park each writer at its read and let another writer commit (quickstart §T1), plus live check L1.

**Acceptance Scenarios**:
1. **Given** a reconcile parked after reading the live config, **When** server B is added through REST, approved, has one tool blocked and makes one call, and the reconcile is then released, **Then** B's row, approval records, tool block and call history all survive. A rediscovery does not re-approve the blocked tool.
2. **Given** an `ApplyConfig` that quarantines and disables server A, **When** server B is toggled before any asynchronous work runs, **Then** A stays quarantined and disabled in the live config, storage, disk and the manager.
3. **Given** a REST PATCH parked after it started, **When** another writer commits `trust_mode: manual`, removes a forwarding allowlist and disables the server, and the PATCH (which changes only `args`) is then released, **Then** the newer trust mode, allowlist and enabled state are unchanged, and disjoint env and header edits from both writers are present.
4. **Given** a reconcile that captured an explicit `quarantined:false`, **When** `QuarantineServer(name, true)` succeeds, **Then** the server is quarantined in every store, its tools are not in the index, and the reconcile cannot lower the quarantine.
5. **Given** `upstream_servers add` parked before its commit, **When** `ApplyConfig` changes the profiles, **Then** the add commits on top of the new profiles and does not overwrite them.
6. **Given** a server with `Shared=true` and an `auth_broker` block, **When** it is patched through REST and MCP, **Then** both fields survive in the live config and on disk.

### User Story 2: Removal is final and a re-add starts clean (Priority: P1), PR2

An operator removes server `acme` with REST, MCP or a config edit. They later add a different server named `acme` with `trust_mode: manual`. The new server goes through fresh review and fresh OAuth. Nothing from the old one is reused.

**Independent Test**: quickstart §T2 and live check L2.

**Acceptance Scenarios**:
1. **Given** `acme` with approved tool hashes, approved prompt hashes, OAuth tokens, a DCR client, an integrity baseline, a clean scan report and call history, **When** it is removed through any of the three paths, **Then** each of those is absent when the call returns. This holds even when the remaining configured set is empty.
2. **Given** that removal, **When** a different `acme` exposing the same tool and prompt definitions is added under manual trust, **Then** every tool and prompt is pending and withheld, an unforced scanner approval is refused for lack of a scan, and the first connection needs a new OAuth sign-in.
3. **Given** a removal in progress, **When** an unrelated commit for server B runs at any point, **Then** `acme` is not republished.
4. **Given** `ApproveTools(acme, …)` parked after it captured the generation, **When** `acme` is removed and re-added and the approval is released, **Then** the approval writes nothing and returns `server_replaced` (409). An unrelated edit to `acme` (for example `disabled_tools`) in the same window does **not** reject the approval.
5. **Given** scanner approval parked before unquarantine, **When** `acme` is removed and re-added, **Then** the replacement stays quarantined.
6. **Given** servers `foo` and `foo_bar` that both have OAuth tokens, **When** `foo` is removed, **Then** `foo_bar`'s tokens remain (B2).
7. **Given** the old instance of `acme` parked mid-persistence of an OAuth token and a DCR client, **When** `acme`'s OAuth config changes while its name and URL stay the same, the commit returns, and the write is released, **Then** the cleared token and client registration stay absent, and the new instance does not reuse them (review R5.3).

### User Story 3: Stale workers and supervisor actions are inert (Priority: P1), PR3

**Independent Test**: quickstart §T3 and live check L3.

**Acceptance Scenarios**:
1. **Given** an enabled server's connect worker parked before `AddServerConfig`, **When** newer commits remove the server, re-add it with another endpoint, or quarantine it, and the worker is then released, **Then** the manager holds only the newest configuration and the removed server has no client.
2. **Given** a disabled server's disconnect worker parked before `RemoveServer`, **When** the server is re-enabled and the worker is released, **Then** the new client stays registered and connected.
3. **Given** a supervisor `ActionConnect` or `ActionReconnect` parked before its `AddServer`, **When** a newer commit removes, replaces or quarantines the server, **Then** the action has no effect.
4. **Given** `RestartServer(name)` racing a removal, **Then** the restart does not re-create the removed server.
5. **Given** a supervisor disconnect captured for the current instance and parked, **When** the server is restarted without a config change (a new instance at the same generation) and the disconnect is released, **Then** the replacement stays connected and the action is rejected as stale (review R5.2).

### User Story 4: Retired servers send nothing (Priority: P1), PR4

**Independent Test**: quickstart §T4 (marker process, request counters) and live check L4.

**Acceptance Scenarios**:
1. **Given** a stdio instance parked after its last lookup and before `cmd.Start`, **When** a commit removes the server and returns, and the instance is then released, **Then** the launch marker file is never created.
2. **Given** an HTTP or SSE instance parked before its first request (headers or OAuth strategy), **When** a commit removes the server, replaces its endpoint or quarantines it and returns, **Then** the old endpoint's counter shows zero new requests and zero `Authorization` headers.
3. **Given** a reused connection whose write fails and triggers a `net/http` internal retry that is parked, **When** retirement completes and the retry resumes, **Then** no retry reaches the old endpoint and `Retire` does not hang.
4. **Given** an OAuth token or refresh request parked before sending, **When** the server is retired, **Then** the token endpoint receives no client secret and no refresh token.
5. **Given** a quarantined server's inspection capture parked before persisting, **When** a commit replaces its endpoint, **Then** no definitions from the old endpoint are stored and no `review.changed` is sent for them.
6. **Given** an old instance parked before its stdio launch, or before its HTTP or OAuth send, **When** `RestartServer`, `ForceReconnectAll` or Docker recovery replaces it at the same config generation and the old instance is released, **Then** the old instance launches nothing and sends nothing, and the replacement is unaffected (review R5.4).
7. **Given** an admitted dial parked after it connected but before it registered the connection, **When** `Retire` runs its first sweep, the connection is then registered, and a blocking write is attempted, **Then** the connection is closed, the write returns `ErrRetired`, and `Retire` returns within its bound (review R5.1).

### Edge Cases

- **Remove, then immediately re-add the same name** (#1570 behaviour): the re-add succeeds immediately with a higher generation. The add sees no `exists` refusal.
- **Removing the last server**: the purge runs. The orphan prune's "empty set" guard (`lifecycle.go:1456`) now applies only to the startup commits (the admission commit and the boot GC).
- **Restart-required `ApplyConfig`**: if no hot field changed, nothing is published and no generation is assigned. Disk is still written, as today.
- **Disk write fails**: nothing is published and storage is untouched (A2).
- **Storage transaction fails after the disk write** (a disk-writing commit: a server mutation, `ApplyConfig`, `SaveConfiguration`, a path change): the bytes the file had before this commit are written back byte for byte with their mode, or the file is removed when there was none; nothing is published and an error is returned (A2). Those bytes may hold a pending restart-required change or an unapplied external edit, and both survive (reviews R5.5, R9.2, PR1 review rounds 3 and 4). A **file reload** never writes the file, so its storage failure has nothing to compensate: the file keeps the operator's edit, and live config and storage keep what was committed.
- **Config file path absent** (tests, stdin config): the disk step is skipped. Everything else is unchanged.
- **Rename vs replacement**: an `ApplyConfig` (or reload) rename `a`→`b` is `Removed(a)` plus `Created(b)`; the purge runs for `a`. A same-name entry in a whole-config apply or reload is an update (`Updated` when it changed, no change when equal), never `Replaced`, because a config file identifies a server only by name (data-model §`Replaced`). Only an explicit `RemoveServer(n)` then `CreateServer(n)` in one `CommitTx` is a same-name replacement (`Replaced`, new incarnation, T1.17).
- **Path change** (`UpdateConfig` or `ApplyConfig` with a new path, the latter on every branch: hot change, unchanged config and restart-required-only): the commit writes the committed config at the new path, replacing whatever file was there, and publishes the path to both stores only after disk and storage succeed. A disk or storage failure keeps the old path and leaves the destination as it was (PR1 review rounds 4 and 5).
- **A generation-only change** (for example `disabled_tools`, `forward_headers` or `expose_prompts`): the instance is kept and receives the new config and generation in commit step 7, before the commit returns (PR1 already, review R9.1), with no reconnect. Discovery passes captured at the old generation drop their writes, and the next pass rewrites them.
- **A quarantined server with an inspection exemption**: quarantining retires the old instance. The supervisor may then connect a new inspection instance under the new generation, as the policy allows today.
- **Index purge fails**: this is logged. The boot GC removes index entries for unconfigured servers. The removal still succeeds (A8).

## Requirements

### Functional Requirements: Commit (PR1)

- **FR-001**: `Runtime.Commit(reason string, fn func(*CommitTx) error) (*CommitResult, error)` MUST hold `configCommitMu` from reading the live config until publication. Steps 1-8 MUST run in the order given in [plan.md](plan.md) §Commit pipeline.
- **FR-001a**: a commit MUST diff `next` against both `base` (config diff: generations, publish, events) and the stored view (projection diff: storage writes). It MAY skip steps 4-8 only when both diffs are empty and neither `Bootstrap()` (boot reconcile: seed generations from matching stored rows, schedule workers for every enabled server) nor `ForceDisk()` (`SaveConfiguration`) was requested, and no `UpdateServer`/`SetQuarantined` in the commit left an entry explicitly quarantined: a repeated explicit quarantine (an identical REST PATCH `{"quarantined":true}`, `POST .../quarantine` or MCP `quarantine_server` on a server already explicitly quarantined) takes that server's approval lock and purges its global and profile index entries before returning, publishing nothing (Sol round 12; main's `Server.UpdateServer` purged on every explicit true request). The boot reconcile MUST be `ReconcileLive()` together with `Bootstrap()`, and `Bootstrap()` MUST NOT be used anywhere else (review R9.6). The `configsvc` pre-publish hook MUST be removed. Admission runs only in step 2.
- **FR-001c**: when the storage step fails after the disk step, the commit MUST restore the file exactly as it was read from disk inside the lock before step 5: the same bytes and mode, or no file when there was none. It MUST NOT reconstruct the previous file from in-memory config. It MUST record a self-write marker for the restored bytes and leave `desiredCfg` and the pending-restart report unchanged (reviews R5.5, R9.2).
- **FR-001b**: `ReplaceConfig(cfg)` MUST only receive a config the caller submitted in the same call, or the file read inside the lock. Reconciliation MUST use `ReconcileLive()`, which takes no argument. The captured-argument form of `LoadConfiguredServers` MUST be deleted (D14).
- **FR-002**: `CommitTx` MUST provide `Server`, `CreateServer`, `UpdateServer(name, fn)`, `SetEnabled`, `SetQuarantined`, `RemoveServer`, `ReplaceConfig`, `ReconcileLive`, `MutateGlobal`, `ExpectGeneration(name, g)` and `ExpectIncarnation(name, inc)`. Existence and `exists` checks MUST use the commit's working copy of the live config, never storage alone.
- **FR-003**: these MUST be `Commit` callers: `ApplyConfig`, `ReloadConfiguration` (one publish: the raw file is never published unpinned), the boot reconcile, `UpdateConfig`, `UpdateListenAddress`, `SaveConfiguration`, REST add/update/patch/enable/disable/quarantine/unquarantine/remove, MCP `upstream_servers` add/update/patch/remove, MCP `quarantine_security quarantine_server`, `BulkEnableServers`, `SetServerShared`, registry source add/edit/remove, the `RestartServer` disk refresh, and `gateInitialConfig`.
- **FR-004**: `SaveConfiguration` MUST NOT read the server list from storage. It MUST persist the live snapshot, keeping today's pending-aware handling.
- **FR-005**: reconcile (today `LoadConfiguredServers`) MUST run inside the commit against the commit's working copy. Orphan GC of tool approvals and call history MUST use that copy. The empty-set guard applies only at startup: the startup admission commit (`config_load_admission_gate`) and the boot reconcile (PR1 review round 8).
- **FR-006**: REST PATCH and MCP update/patch MUST resolve omitted fields, map merges, isolation and forward-header validation inside `UpdateServer`'s callback, on a copy of the live entry (including `Shared` and `AuthBroker`).
- **FR-007**: every server change MUST land in one bbolt transaction, together with the generation stamps and `meta.config_commit_generation`.
- **FR-007a**: every row the commit writes, including a projection-repair write for an unchanged server, MUST carry exactly the `ServerGen` that `Prepare` computed for that name, passed per row in the commit batch. Storage MUST NOT derive a row's stamps from the batch generation or from a previous row (review R9.3).
- **FR-008**: a newly quarantined server MUST be purged from the global and per-profile indexes inside the commit, whatever path set the flag (fixes B1 and r10-C #2).
- **FR-008a**: every discovery pass MUST perform its final eligibility check (the row exists, `Generation` equals the generation it captured, and the server is not quarantined or disabled) and **all** of its index, StateView and last-good writes in one hold of the server's approval lock (D15). This includes a result for a server that is not eligible for indexing (recorded for review: its review records, last-good set and capture stamp, checked for row presence and generation only) and an empty result (the `ToolsDiscovered` stamp), on the sweep and the single-server paths (review R2.2, R2.3). A pass rejected for a generation mismatch on an otherwise eligible row MUST cause the current instance to be re-listed (the replacement's own discovery may have been deduped while the stale pass listed); a pass rejected because the server was removed, quarantined or disabled MUST NOT re-list (PR1 review round 7).
- **FR-008b**: every approval decision (discovery, operator approve/block/toggle, scanner, baseline promotion) MUST resolve its policy from storage under the server's approval lock: the row's trust mode, quarantine, skip and auto-approve fields, and `meta.quarantine_enabled` written in the commit transaction (D16). It MUST NOT read the published config.
- **FR-008c**: a commit that changes a global approval-policy field (`quarantine_enabled`) MUST take every configured server's approval lock, in sorted order, in step 4 (D16 amended).
- **FR-009**: `scripts/check-upstream-write-sites.sh` MUST fail when `upstreams` rows are written outside `internal/storage`, `internal/runtime/commit*.go` and tests. It MUST run in CI.
- **FR-009a**: `CommitTx.UnderApprovalLock(name, fn)` MUST run `fn` in step 4 while holding `name`'s approval lock, in the same lock hold as step 6's row write. It is the integration point for Spec 117's review-bound server approval. The storage quarantine flip MUST only ever be the commit's row write.
- **FR-010**: `configsvc` MUST compute `ServerGenerations` at prepare time. An unchanged entry keeps its generation, and a new or changed entry gets `G`. Generations MUST be monotonic across restarts.
- **FR-010a**: a `RemoveServer(n)` followed by `CreateServer(n)` in one commit MUST be passed to `Prepare` as explicit replacement metadata and MUST be represented as one `Replaced` change (old and new `ServerGen`). It MUST get a new generation, incarnation and OAuth epoch even when the re-created entry equals the removed one. Every consumer MUST treat it as a removal followed by a create: approval lock, full purge before the new row in one bbolt transaction, in-memory purge, old instance unregistered and retired, and a connect scheduled for the new generation (review R8.2).

- **FR-019a**: from PR1, commit step 7 MUST give every retained instance of an `Updated`, non-reconnect change its new config and generation as one atomic pair, applied only when the generation is newer than the instance's, before the commit returns. The commit MUST then request one discovery pass for each such connected server. PR3 MAY move this into `AdvanceGenerations` but MUST NOT drop it (review R9.1).

### Functional Requirements: Removal and purge (PR2)

- **FR-011**: `CommitTx.RemoveServer` MUST, inside the commit and under the server's approval lock, delete the row and the whole purge set in one bbolt transaction. OAuth keys MUST match exactly: `name` or `name + "_" + 16 lowercase hex` (fixes B2).
- **FR-012**: the removal commit MUST also purge the server from the global and per-profile search indexes. It MUST cancel the OAuth refresh schedule, revoke the inspection exemption, drop the toolsig cache, the discovery generation and `lastGoodTools`/`lastCaptureAt`, and unregister the manager instance.
- **FR-013**: REST `DELETE /api/v1/servers/{id}`, MCP `upstream_servers remove` and config-driven removal (ApplyConfig, file reload, boot) MUST all use FR-011/FR-012. The asynchronous removal goroutines MUST be deleted.
- **FR-014**: approval writers (`ApproveTools`, baseline approval on unquarantine, block/unblock, prompt approve/block, scanner baseline+blocks, discovery's `checkToolApprovals` and quarantined capture) MUST capture the incarnation and re-check it against the storage row under the server's approval lock. On a mismatch they MUST write nothing and return `ErrServerReplaced`.
- **FR-014a**: scan jobs and reports MUST record the incarnation they were started for and MUST be persisted with an incarnation-conditional storage write (row exists and matches, in the same bbolt tx). `approveServer` MUST read only reports of the current incarnation. Removal MUST invalidate the scan-summary cache.
- **FR-014c**: server-identity registration and per-server tool-call history writes MUST be incarnation-conditional (`code_execution` exempt). OAuth completion events MUST carry their incarnation, the consumer MUST drop stale ones, and the purge MUST match completion records by decoded `ServerName`.
- **FR-014b**: OAuth token, DCR-client and completion writes MUST be conditional on the row's `CredentialBinding{Incarnation, OAuthEpoch}` in the same way. `OAuthEpoch` MUST be raised in the same bbolt transaction as every OAuth-state clear (an OAuth config change) and set on create. The token store and DCR lookups MUST be bound to the instance's binding and MUST ignore records written under another one. Refresh schedules MUST be keyed by `(name, incarnation, oauthEpoch)` and dropped on mismatch (review R5.3).
- **FR-014d**: an instance's `CredentialBinding` MUST come from the same committed view (`CommitResult` or published `Snapshot` `ServerGen{Gen, Incarnation, OAuthEpoch}`) as its config and generation. It MUST NOT be read from the storage row and adopted. Before an instance or its OAuth config is constructed, the worker MUST check, in one storage read, that the row equals that `ServerGen` in all three fields, and MUST build nothing (`ErrStaleGeneration`) on a mismatch (review R6.1).
- **FR-014h**: the standalone CLI logout (no daemon) MUST use one exported, exact-key current-state clear on `storage.BoltDB`, and the write-site grep MUST allow it only in `internal/upstream/cli/client.go`. Both editions of `./cmd/mcpproxy` MUST build after the unconditional primitives are unexported (review R8.3).
- **FR-014e**: every instance-originated destructive OAuth operation (the 5xx `ClearToken`, the DCR clears in `createOAuthConfigInternal`, and the refresh and stale-client compare-and-clears) MUST check the instance's `CredentialBinding` against the row, and the record's epoch against the binding, in the same bbolt transaction as the delete, together with a check that the record's `Revision` (a per-bucket sequence bumped by every write to `oauth_tokens`) equals the revision of the record the caller observed when it decided to delete. A field compare is not sufficient, because a same-binding replacement can keep the access token or client ID while changing other fields (review R7.3). On a mismatch it MUST change nothing. The token clear (`DeleteOAuthTokenIfBound`) deletes the record. The DCR clear (`ClearOAuthClientIfBound`) MUST only empty the four registration fields (`ClientID`, `ClientSecret`, `CallbackPort`, `RedirectURI`), keep the token fields and the record epoch, and bump `Revision`, as every DCR clear on `main` keeps the tokens (review R9.4). The unconditional delete primitives MUST NOT be callable from `internal/upstream` or `internal/oauth`. Intentional current-state clears (the commit's OAuth-config clear and purge, and logout) stay separate and unconditional on the binding (review R6.2).
- **FR-014f**: the process-wide OAuth refresh coordinator MUST isolate flights, their results, the terminal latch, the transient cooldown and the completion outcome by `(serverKey, CredentialBinding)`. A caller MUST NOT join a flight, or receive a token or error, started under another binding. `RefreshManager` MUST ignore outcomes whose binding differs from its schedule key (review R7.1).
- **FR-014g**: value types shared between `configsvc` and `storage` (`ServerGen`, `CredentialBinding`, `ErrStaleGeneration`) MUST live in a leaf package (`internal/commitview`) so the two packages never import each other (review R7.2).
- **FR-014i**: an approval follow-up that runs after a commit (the baseline promotion after `QuarantineServer(name, false)`, and any later one) MUST be bound to the incarnation of the commit that triggered it, taken from that commit's `CommitResult` or from the request's `expected_incarnation`. It MUST NOT recapture the incarnation by name. Under the server's approval lock it MUST require the row to have that incarnation and to be unquarantined, and otherwise MUST write nothing (review R9.5).
- **FR-015**: scanner approval MUST unquarantine through `Commit` with `ExpectIncarnation(name, inc)`.
- **FR-016**: prompt-approval writes MUST take the server's approval lock.
- **FR-017**: REST approval endpoints MUST accept an optional `expected_incarnation`. The review payload and `GET /api/v1/servers/{id}` MUST expose `incarnation` and `generation`. The Web UI MUST send `incarnation` back.

### Functional Requirements: Generation-bound workers (PR3)

- **FR-018**: `upstream.Manager` MUST keep `desired map[string]desiredGen` that only the commit sets (step 7), and MUST offer `AddServerConfigAt`, `RemoveServerAt`, `ConnectServerAt` and `DisconnectServerAt` (every disconnect leg, including the first half of `ActionReconnect`). These return `ErrStaleGeneration` unless the generation matches, checked under `captureMu`+`mu` in the same section as the map change. Actions that target an existing instance (`DisconnectServerAt`, the disconnect leg of `ActionReconnect`, `ReplaceInstanceAt`) MUST also match the captured `managed.Client.InstanceID()` (or `expected` pointer), because a replacement can share the generation (review R5.2).
- **FR-019**: the commit's step 7 MUST, for each server whose generation moved, either (a) unregister the instance under `captureMu` (for removal or for a change to connection fields, `Enabled` or `Quarantined`), or (b) restamp it and call `SetConfig` (for any other change).
- **FR-020**: the connect/disconnect workers spawned after a commit, supervisor `executeAction`, `ActorPoolSimple`, `RestartServer` and `postConfigReload`'s `ConnectAll` MUST use the `*At` methods with the generation from the snapshot or commit result they act on. `ErrStaleGeneration` is logged at debug level and is not an error.
- **FR-020a**: `Manager.ForceReconnectAll` and its Docker-recovery caller MUST use `ReplaceInstanceAt(name, gen, expected)`. Inside `manager.go`, no internal call may use the legacy mutators outside the `*At` implementations. Every removal or overwrite of an entry in `Manager.clients` MUST go through one `unregisterLocked` helper (review R5.4).
- **FR-021**: the capture and discovery publish paths MUST carry the instance's generation and drop results whose generation is no longer desired.

### Functional Requirements: Retirement barrier (PR4)

- **FR-021b**: cached tool snapshots (`lastGoodTools`) MUST carry `{Incarnation, Gen}`. A reapply (sweep fallback or approval reindex) MUST reject a snapshot whose stamp differs from the current row.
- **FR-022**: each `managed.Client` instance MUST own a `dialgate.Gate`. **Every** instance that leaves `Manager.clients` MUST be retired before the call that removed it returns: `unregisterLocked` calls `Retire()`. This covers step 7 (`AdvanceGenerations`, before publishing in step 8) and the same-generation replacements (`ReplaceInstanceAt` from `RestartServer`, `ForceReconnectAll` and Docker recovery, which retire the old instance before installing the new one) (review R5.4).
- **FR-023**: `exec.Cmd.Start` for stdio, the Docker isolation launch and the launcher spawn MUST run under `Gate.Admit`.
- **FR-023a**: every mcp-go `transport.Interface` that core builds (stdio, streamable HTTP, SSE) MUST be wrapped by `dialgate.WrapTransport`, which gates `Start`, `SendRequest` and `SendNotification`. For stdio, `Retire` MUST also kill the process group (Windows: terminate the Job Object) synchronously.
- **FR-023b**: the wrapper MUST preserve every capability that is asserted on the transport. For mcp-go's own assertions it MUST forward `BidirectionalInterface`, `HTTPConnection`, `SetConnectionLostHandler` and `RequiresLegacyProtocol` exactly when the inner transport has them. It MUST implement `Unwrap()`, and core's assertions (the OAuth handler lookup and the Streamable HTTP session snapshot) MUST run on the unwrapped transport through one helper. A test MUST assert capability parity for every production transport kind, and pin the mcp-go method sets (review R8.1).
- **FR-024a**: every OAuth preflight, resource auto-detect, metadata discovery and cache fetch MUST use the instance transport through `oauth.Env`. CI MUST reject `http.Get/Head/Post(`, `http.DefaultClient`, `http.DefaultTransport` and transport-less `&http.Client{` in `internal/oauth/**`, `internal/upstream/core/**` and `internal/transport/**` outside an allowlist.
- **FR-024**: HTTP and SSE MCP traffic MUST use a per-instance `*http.Transport` whose `DialContext` is gated and whose connections gate every `Write`. The OAuth `HTTPClient` (metadata, DCR, authorization-code exchange, refresh) MUST use the same instance transport. `Manager.RefreshOAuthToken` and manual OAuth MUST refuse to run against an unregistered or retired instance.
- **FR-025a**: the gate's read lock MUST be held only around one leaf operation (a dial syscall, one conn `Write`, or `cmd.Start` + process registration) and never nested. `WrapTransport` MUST NOT take the lock. Stdio process-group/Job registration and `TrackProcess` MUST happen inside the admitted launch section. A failed Job assignment MUST kill the child inside the section. `Retire` MUST kill tracked processes before **and** after its barrier.
- **FR-025b**: on Windows, stdio, Docker and launcher processes MUST be created suspended, assigned to their Job and resumed inside the admitted section. The OAuth browser launch MUST be an admitted section, and the code exchange and token write it leads to are gated by FR-024 and FR-014b.
- **FR-025**: `Retire()` MUST be bounded. It sets the latch (under `connsMu`), cancels the gate context, closes tracked connections, then runs a write-lock barrier, then closes any connection tracked during the drain. It MUST log a warning when it takes more than 1 s.
- **FR-025c**: a gated connection MUST be registered inside the admitted dial section, before it is returned to its caller. Registration MUST check the latch under `connsMu` and close the connection with `ErrRetired` when the gate is already retired (review R5.1).
- **FR-026**: tool calls, `tools/list`, health probes and ambiguous-call probes on a retired instance MUST fail with `ErrRetired` and MUST NOT mark the current instance unhealthy.

### Functional Requirements: Errors and surfaces

- **FR-027**: errors MUST map as follows. `ErrServerNotFound` → 404 / MCP `isError` "Server 'x' not found". `ServerExistsError` → 409 (unchanged). `ErrServerReplaced` → 409 `server_replaced`. `ErrStaleGeneration` and `ErrRetired` are internal and never reach a surface.
- **FR-028**: the events `servers.changed`, `review.changed`, `config.saved` and `config.reloaded`, and the activity records, MUST be sent after the commit is released, at most once per commit and reason.

### Key Entities

See [data-model.md](data-model.md): `CommitTx`, `CommitResult`, `PreparedSnapshot`, `ServerGen{Gen, Incarnation, OAuthEpoch}`, `UpstreamRecord.Generation/Incarnation/OAuthEpoch`, `CredentialBinding`, `PurgeSet`, `PurgeReport`, `Manager.desired`, `dialgate.Gate`, and the errors.

## Success Criteria

- **SC-001**: every acceptance scenario in US1-US4 has a deterministic hook-gated test that fails on `main` (or on the parent PR) and passes on its PR, under `go test -race`, 20 runs with `-count=20` and at `GOMAXPROCS=1` and `4`.
- **SC-002**: `check-upstream-write-sites.sh` reports zero violations, and CI runs it.
- **SC-003**: live harness checks L1-L4 ([quickstart.md](quickstart.md)) pass on the built binary. The evidence goes under `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s116-pr<N>/EVIDENCE.md` and is not committed.
- **SC-004**: a commit with no index work takes p99 < 50 ms on the qa070 `big` profile (25 servers). A removal with an index purge of a 320-tool server takes p99 < 500 ms. A commit that retires one instance adds < 100 ms. All three are measured in PR2 and PR4.
- **SC-005**: no new dependencies. Both golangci-lint passes (bare and `--build-tags server`) are clean. `./scripts/test-api-e2e.sh` passes. The CI `-skip` race lane is green.
- **SC-006**: the existing tests for #1570 (create-only), #1571 (approval lock), #937 (admission) and #1061 (quarantine purge) still pass without edits to their assertions.

## Assumptions

- **A1**: `UpstreamRecord` fields are a subset of `ServerConfig`. Config-only fields are already preserved from the snapshot. Storage stopping to feed the live config therefore loses nothing.
- **A2**: when a storage failure is followed by a failed compensating disk write, the disk may run ahead of the live config until the next commit. This is logged at error level. It matches today's "in-memory agrees, disk stale" failure mode. A failed reload writes nothing (the reload never writes the file), so the file keeps the operator's unapplied edit. The next reload applies it; the next disk-writing commit (`SaveConfiguration`, a server mutation, `ApplyConfig`) instead writes the live config and erases it, as on `main`. Compensation after a storage failure in a disk-writing commit restores the bytes the file had before that commit, unapplied edits included (review R9.2, corrected in PR1 review round 3).
- **A3**: commits are rare (seconds apart at most). Holding `configCommitMu` for a bbolt transaction, an index delete and bounded retirements is acceptable. SC-004 bounds it.
- **A4**: the boot reconcile keeps "config file is the source of truth". Storage rows absent from the file are removed. Recorded quarantines are inherited through the admission gate, as today.
- **A5**: agent tokens and client credentials (Spec 115) are not keyed by upstream name, so they are not in the purge set. Profiles that name a removed server keep the name, so a deliberate re-add is visible to them again. This is today's behaviour and the operator's choice.
- **A6**: `expected_incarnation` is optional (Q1). Callers that omit it are bound to the incarnation read when the request started.
- **A7**: scan reports and jobs for a removed server are purged. The activity log keeps the audit trail (Q2).
- **A8**: index purge on removal and quarantine stays synchronous (Q3). If it fails, the server is still removed, and the boot GC cleans the index.
- **A9**: an instance admitted before `Retire` returns may complete one launch or write. The asynchronous disconnect terminates it. The barrier guarantees only that nothing new starts afterwards. Sol's r8-BC/r9-A/r10-A findings were about operations that started **after** retirement, and those are covered.
- **A10**: each HTTP upstream instance gets its own idle connection pool. The extra memory per instance (one `http.Transport`) is negligible.
- **A11**: the `toolhash` bucket keys are checked in PR2 (T2xx). If the keys carry no server name, the bucket is left out of the purge set and the reason is recorded.
- **A12**: no REST, MCP or CLI contract changes other than FR-017's optional field and the new `server_replaced` 409 code.

## Out of Scope

- The tray (#1569, #1572), the Swift app and Web UI layout.
- Multi-user upstreams in the server edition (they do not exist yet).
- Cross-process locking (two `mcpproxy` processes on one data dir are already refused by the DB lock).
- Replacing bbolt or bleve. Making bleve deletes transactional with bbolt.

## Traceability

| Issue bullet | Requirement | PR | Test (quickstart) |
|---|---|---|---|
| #1558 orphan GC uses the snapshot name set | FR-005 | PR1 | T1.1 |
| #1558 removal of a row missing from the snapshot | FR-005, FR-013 | PR1, PR2 | T1.1, T2.3 |
| #1558 storage save loop and connect goroutines write a stale snapshot | FR-005, FR-020 | PR1, PR3 | T1.2, T3.1 |
| #1558 ApplyConfig publishes before storage | FR-001, FR-007 | PR1 | T1.2 |
| #1558 add path reads `Config()` before the lock | FR-002, FR-003 | PR1 | T1.5 |
| #1559 REST removal before `SaveConfiguration` | FR-004, FR-013 | PR1, PR2 | T1.11 |
| #1559 asynchronous config-driven cleanup resurrected by a save | FR-004, FR-013 | PR1, PR2 | T1.11 |
| #1559 delayed sync after resurrection | FR-005 | PR1 | T1.1 |
| #1560 MCP update/patch outside the lock | FR-003, FR-006 | PR1 | T1.3 |
| #1560 REST update/PATCH stale resolution | FR-006 | PR1 | T1.3 |
| #1560 Quarantine/Enable write storage before the lock | FR-003 | PR1 | T1.4 |
| #1560 update accepts a name pending removal | FR-002, FR-013 | PR1, PR2 | T1.11 |
| #1562 prompt approvals, history, OAuth not purged | FR-011, FR-012 | PR2 | T2.1 |
| #1562 `ApproveTools` restores a deleted baseline | FR-014 | PR2 | T2.4 |
| #1562 scanner `ApproveServer` crosses a re-add | FR-015 | PR2 | T2.5 |
| #1563 `LoadConfiguredServers` workers | FR-018, FR-020 | PR3 | T3.1, T3.2 |
| #1563 supervisor actions with captured snapshots | FR-018, FR-020 | PR3 | T3.3, T3.6 |
| #1563 supervisor treats a pending removal as current | FR-004, FR-013 (no pending state exists) | PR1, PR2 | T3.3 |
| #1564 `GetClient`→`Connect` after removal | FR-019, FR-022 | PR3, PR4 | T4.1 |
| #1564 stdio `context.Background()` launch | FR-023 | PR4 | T4.1 |
| #1564 HTTP/SSE admission released before the strategy | FR-024 | PR4 | T4.2 |
| #1564 `net/http` retries | FR-024 | PR4 | T4.3 |
| #1564 OAuth `HTTPClient` outside the gate | FR-024 | PR4 | T4.4 |
| #1564 publication and reconciliation are separate steps | FR-019, FR-022 (retire before publish) | PR3, PR4 | T4.2, T4.5 |
| B1 MCP quarantine skips the index purge | FR-008 | PR1 | T1.6 |
| B2 OAuth prefix collision | FR-011 | PR2 | T2.6 |
| Review R1.1 late scan result restores evidence | FR-014a | PR2 | T2.8 |
| Review R1.2 late OAuth save restores tokens | FR-014b | PR2 | T2.9 |
| Review R1.3 stdio writes after retire | FR-023a | PR4 | T4.8 |
| Review R1.4 OAuth preflight clients | FR-024a | PR4 | T4.9 |
| Review R1.5 stale supervisor disconnect | FR-018 | PR3 | T3.6 |
| Review R1.6 boot / no-op commits | FR-001a | PR1 | T1.10 (regression) |
| Review R2.1 stale replacement | FR-001b | PR1 | T1.1 |
| Review R2.2 discovery re-index after purge | FR-008a | PR1 | T1.12 |
| Review R2.3/R2.4 gate nesting, stdio launch handoff | FR-025a | PR4 | T4.10, T4.11 |
| Review R2.5 last-good snapshot after re-add | FR-012, FR-021b | PR2, PR3 | T2.10, T3.7 |
| Review R3.1 D15 re-entry | FR-008a | PR1 | T1.13 |
| Review R4.1 Docker recovery resurrection | FR-020a | PR3 | T3.8 |
| Review R4.2 global policy change | FR-008c | PR1 | T1.15 |
| Review R4.4 launcher probe, Docker helpers | I6, FR-023 | PR4 | T4.14 |
| Review R3.2 policy from published config | FR-008b | PR1 | T1.14 |
| Review R3.3/R3.4 Windows descendants, browser launch | FR-025b | PR4 | T4.12, T4.13 |
| Review R3.5/R3.6 completion, identity, history | FR-014c | PR2 | T2.11, T2.12 |
| Review R5.1 conn registered after the sweep | FR-025, FR-025c | PR4 | T4.16 |
| Review R5.2 stale disconnect hits a same-generation replacement | FR-018 | PR3 | T3.9 |
| Review R5.3 OAuth write survives an OAuth-config clear | FR-014b | PR2 | T2.13 |
| Review R5.4 same-generation replacement not retired | FR-020a, FR-022 | PR3, PR4 | T4.15 |
| Review R5.5 compensation erases a pending restart change | FR-001 (step 6) | PR1 | T1.16 |
| Review R5.6 T2.5 park point deadlocks on L1 | FR-014, FR-015 | PR2 | T2.5 (amended) |
| Review R6.1 stale construction adopts the replacement's binding | FR-014d | PR2 | T2.14 |
| Review R6.2 stale instance's OAuth clear deletes the replacement's credentials | FR-014e | PR2 | T2.15 |
| Review R7.1 refresh singleflight crosses credential bindings | FR-014f | PR2 | T2.16 |
| Review R7.2 `configsvc`↔`storage` import cycle | FR-014g | PR1, PR2 | `TestCommitviewIsLeaf`, T055d compile check |
| Review R7.3 same-binding clear erases a replacement that kept the token or client ID | FR-014e | PR2 | T2.15 (amended), T048e |
| Review R8.1 transport wrapper hides OAuth, session and mcp-go capabilities | FR-023b | PR4 | T4.17, `TestWrapTransportCapabilityParity`, `TestMCPGoTransportMethodSetPinned` |
| Spec 117 review-bound server approval needs the commit's approval-lock hold (`UnderApprovalLock`) | FR-009a | PR1 | T029a |
| Review R8.2 same-commit remove and re-create collapses to unchanged or updated | FR-010a | PR1, PR2, PR3 | T1.17, T2.17, T3.10 |
| Review R8.3 standalone CLI logout calls the unexported delete | FR-014h | PR2 | T2.18, two-edition `./cmd/mcpproxy` build, write-site self-test |
| Review R9.1 retained instance never receives a non-connection edit in PR1 | FR-019a | PR1, PR3 | T1.19 |
| Review R9.2 compensation rebuilt from memory erases a reload's edit | FR-001c | PR1 | T1.18 |
| Review R9.3 commit batch cannot carry per-row stamps | FR-007a | PR1 | T1.20 |
| Review R9.4 bound DCR clear specified as a record delete | FR-014e | PR2 | T2.20 |
| Review R9.5 unquarantine baseline promotion recaptures the incarnation | FR-014i | PR2 | T2.19 |
| Review R9.6 boot reconcile without `Bootstrap()` skips scheduling | FR-001a | PR1 | T1.21 |
