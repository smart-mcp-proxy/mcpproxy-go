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
| Admission gate (#937) and the `SaveUpstreamKeepingQuarantine` invariant | Run in the commit's prepare step against the stored view |
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
- **I6 (retirement barrier)**: when a commit removes a server, or changes its connection fields, `Enabled` or `Quarantined`, the old instance is unregistered and retired before the commit publishes. No process launch, TCP or TLS dial, HTTP/SSE request byte or OAuth request byte (metadata, DCR, token, refresh, or a `net/http` internal retry) for that instance starts after that point.
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

### User Story 3: Stale workers and supervisor actions are inert (Priority: P1), PR3

**Independent Test**: quickstart §T3 and live check L3.

**Acceptance Scenarios**:
1. **Given** an enabled server's connect worker parked before `AddServerConfig`, **When** newer commits remove the server, re-add it with another endpoint, or quarantine it, and the worker is then released, **Then** the manager holds only the newest configuration and the removed server has no client.
2. **Given** a disabled server's disconnect worker parked before `RemoveServer`, **When** the server is re-enabled and the worker is released, **Then** the new client stays registered and connected.
3. **Given** a supervisor `ActionConnect` or `ActionReconnect` parked before its `AddServer`, **When** a newer commit removes, replaces or quarantines the server, **Then** the action has no effect.
4. **Given** `RestartServer(name)` racing a removal, **Then** the restart does not re-create the removed server.

### User Story 4: Retired servers send nothing (Priority: P1), PR4

**Independent Test**: quickstart §T4 (marker process, request counters) and live check L4.

**Acceptance Scenarios**:
1. **Given** a stdio instance parked after its last lookup and before `cmd.Start`, **When** a commit removes the server and returns, and the instance is then released, **Then** the launch marker file is never created.
2. **Given** an HTTP or SSE instance parked before its first request (headers or OAuth strategy), **When** a commit removes the server, replaces its endpoint or quarantines it and returns, **Then** the old endpoint's counter shows zero new requests and zero `Authorization` headers.
3. **Given** a reused connection whose write fails and triggers a `net/http` internal retry that is parked, **When** retirement completes and the retry resumes, **Then** no retry reaches the old endpoint and `Retire` does not hang.
4. **Given** an OAuth token or refresh request parked before sending, **When** the server is retired, **Then** the token endpoint receives no client secret and no refresh token.
5. **Given** a quarantined server's inspection capture parked before persisting, **When** a commit replaces its endpoint, **Then** no definitions from the old endpoint are stored and no `review.changed` is sent for them.

### Edge Cases

- **Remove, then immediately re-add the same name** (#1570 behaviour): the re-add succeeds immediately with a higher generation. The add sees no `exists` refusal.
- **Removing the last server**: the purge runs. The orphan prune's "empty set" guard (`lifecycle.go:1456`) now applies only to the boot GC.
- **Restart-required `ApplyConfig`**: if no hot field changed, nothing is published and no generation is assigned. Disk is still written, as today.
- **Disk write fails**: nothing is published and storage is untouched (A2).
- **Storage transaction fails after the disk write**: the previous config is written back to disk, nothing is published, and an error is returned (A2).
- **Config file path absent** (tests, stdin config): the disk step is skipped. Everything else is unchanged.
- **The same commit re-adds a name it just removed** (an ApplyConfig rename): this is a remove plus a create with a new generation. The purge runs for the old entry.
- **A generation-only change** (for example `disabled_tools`): the instance is kept and restamped with the new generation, with no reconnect. Discovery passes captured at the old generation drop their writes, and the next pass rewrites them.
- **A quarantined server with an inspection exemption**: quarantining retires the old instance. The supervisor may then connect a new inspection instance under the new generation, as the policy allows today.
- **Index purge fails**: this is logged. The boot GC removes index entries for unconfigured servers. The removal still succeeds (A8).

## Requirements

### Functional Requirements: Commit (PR1)

- **FR-001**: `Runtime.Commit(reason string, fn func(*CommitTx) error) (*CommitResult, error)` MUST hold `configCommitMu` from reading the live config until publication. Steps 1-8 MUST run in the order given in [plan.md](plan.md) §Commit pipeline.
- **FR-001a**: a commit MUST diff `next` against both `base` (config diff: generations, publish, events) and the stored view (projection diff: storage writes). It MAY skip steps 4-8 only when both diffs are empty and neither `Bootstrap()` (boot reconcile: seed generations from matching stored rows, schedule workers for every enabled server) nor `ForceDisk()` (`SaveConfiguration`) was requested. The `configsvc` pre-publish hook MUST be removed. Admission runs only in step 2.
- **FR-002**: `CommitTx` MUST provide `Server`, `CreateServer`, `UpdateServer(name, fn)`, `SetEnabled`, `SetQuarantined`, `RemoveServer`, `ReplaceConfig`, `MutateGlobal`, `ExpectGeneration(name, g)` and `ExpectIncarnation(name, inc)`. Existence and `exists` checks MUST use the commit's working copy of the live config, never storage alone.
- **FR-003**: these MUST be `Commit` callers: `ApplyConfig`, `ReloadConfiguration` (one publish: the raw file is never published unpinned), the boot reconcile, `UpdateConfig`, `UpdateListenAddress`, `SaveConfiguration`, REST add/update/patch/enable/disable/quarantine/unquarantine/remove, MCP `upstream_servers` add/update/patch/remove, MCP `quarantine_security quarantine_server`, `BulkEnableServers`, `SetServerShared`, registry source add/edit/remove, the `RestartServer` disk refresh, and `gateInitialConfig`.
- **FR-004**: `SaveConfiguration` MUST NOT read the server list from storage. It MUST persist the live snapshot, keeping today's pending-aware handling.
- **FR-005**: reconcile (today `LoadConfiguredServers`) MUST run inside the commit against the commit's working copy. Orphan GC of tool approvals and call history MUST use that copy. The empty-set guard applies only at boot.
- **FR-006**: REST PATCH and MCP update/patch MUST resolve omitted fields, map merges, isolation and forward-header validation inside `UpdateServer`'s callback, on a copy of the live entry (including `Shared` and `AuthBroker`).
- **FR-007**: every server change MUST land in one bbolt transaction, together with the generation stamps and `meta.config_commit_generation`.
- **FR-008**: a newly quarantined server MUST be purged from the global and per-profile indexes inside the commit, whatever path set the flag (fixes B1 and r10-C #2).
- **FR-009**: `scripts/check-upstream-write-sites.sh` MUST fail when `upstreams` rows are written outside `internal/storage`, `internal/runtime/commit*.go` and tests. It MUST run in CI.
- **FR-010a**: `CommitTx.UnderApprovalLock(name, fn)` MUST run `fn` in step 4 while holding `name`'s approval lock, in the same lock hold as step 6's row write. It is the integration point for Spec 117's review-bound server approval. The storage quarantine flip MUST only ever be the commit's row write.
- **FR-010**: `configsvc` MUST compute `ServerGenerations` at prepare time. An unchanged entry keeps its generation, and a new or changed entry gets `G`. Generations MUST be monotonic across restarts.

### Functional Requirements: Removal and purge (PR2)

- **FR-011**: `CommitTx.RemoveServer` MUST, inside the commit and under the server's approval lock, delete the row and the whole purge set in one bbolt transaction. OAuth keys MUST match exactly: `name` or `name + "_" + 16 lowercase hex` (fixes B2).
- **FR-012**: the removal commit MUST also purge the server from the global and per-profile search indexes. It MUST cancel the OAuth refresh schedule, revoke the inspection exemption, drop the toolsig cache and the discovery generation, and unregister the manager instance.
- **FR-013**: REST `DELETE /api/v1/servers/{id}`, MCP `upstream_servers remove` and config-driven removal (ApplyConfig, file reload, boot) MUST all use FR-011/FR-012. The asynchronous removal goroutines MUST be deleted.
- **FR-014**: approval writers (`ApproveTools`, baseline approval on unquarantine, block/unblock, prompt approve/block, scanner baseline+blocks, discovery's `checkToolApprovals` and quarantined capture) MUST capture the incarnation and re-check it against the storage row under the server's approval lock. On a mismatch they MUST write nothing and return `ErrServerReplaced`.
- **FR-014a**: scan jobs and reports MUST record the incarnation they were started for and MUST be persisted with an incarnation-conditional storage write (row exists and matches, in the same bbolt tx). `approveServer` MUST read only reports of the current incarnation. Removal MUST invalidate the scan-summary cache.
- **FR-014b**: OAuth token, DCR-client and completion writes MUST be incarnation-conditional in the same way. The token store MUST be bound to the instance's incarnation. Refresh schedules MUST be keyed by `(name, incarnation)` and dropped on mismatch.
- **FR-015**: scanner approval MUST unquarantine through `Commit` with `ExpectIncarnation(name, inc)`.
- **FR-016**: prompt-approval writes MUST take the server's approval lock.
- **FR-017**: REST approval endpoints MUST accept an optional `expected_incarnation`. The review payload and `GET /api/v1/servers/{id}` MUST expose `incarnation` and `generation`. The Web UI MUST send `incarnation` back.

### Functional Requirements: Generation-bound workers (PR3)

- **FR-018**: `upstream.Manager` MUST keep `desired map[string]desiredGen` that only the commit sets (step 7), and MUST offer `AddServerConfigAt`, `RemoveServerAt`, `ConnectServerAt` and `DisconnectServerAt` (every disconnect leg, including the first half of `ActionReconnect`). These return `ErrStaleGeneration` unless the generation matches, checked under `captureMu`+`mu` in the same section as the map change.
- **FR-019**: the commit's step 7 MUST, for each server whose generation moved, either (a) unregister the instance under `captureMu` (for removal or for a change to connection fields, `Enabled` or `Quarantined`), or (b) restamp it and call `SetConfig` (for any other change).
- **FR-020**: the connect/disconnect workers spawned after a commit, supervisor `executeAction`, `ActorPoolSimple`, `RestartServer` and `postConfigReload`'s `ConnectAll` MUST use the `*At` methods with the generation from the snapshot or commit result they act on. `ErrStaleGeneration` is logged at debug level and is not an error.
- **FR-021**: the capture and discovery publish paths MUST carry the instance's generation and drop results whose generation is no longer desired.

### Functional Requirements: Retirement barrier (PR4)

- **FR-022**: each `managed.Client` instance MUST own a `dialgate.Gate`. Step 7 MUST call `Retire()` on every instance it unregisters, before publishing (step 8).
- **FR-023**: `exec.Cmd.Start` for stdio, the Docker isolation launch and the launcher spawn MUST run under `Gate.Admit`.
- **FR-023a**: every mcp-go `transport.Interface` that core builds (stdio, streamable HTTP, SSE) MUST be wrapped by `dialgate.WrapTransport`, which gates `Start`, `SendRequest` and `SendNotification`. For stdio, `Retire` MUST also kill the process group (Windows: terminate the Job Object) synchronously.
- **FR-024a**: every OAuth preflight, resource auto-detect, metadata discovery and cache fetch MUST use the instance transport through `oauth.Env`. CI MUST reject `http.Get/Head/Post(`, `http.DefaultClient`, `http.DefaultTransport` and transport-less `&http.Client{` in `internal/oauth/**`, `internal/upstream/core/**` and `internal/transport/**` outside an allowlist.
- **FR-024**: HTTP and SSE MCP traffic MUST use a per-instance `*http.Transport` whose `DialContext` is gated and whose connections gate every `Write`. The OAuth `HTTPClient` (metadata, DCR, authorization-code exchange, refresh) MUST use the same instance transport. `Manager.RefreshOAuthToken` and manual OAuth MUST refuse to run against an unregistered or retired instance.
- **FR-025**: `Retire()` MUST be bounded. It sets the latch, cancels the gate context, closes tracked connections, then runs a write-lock barrier. It MUST log a warning when it takes more than 1 s.
- **FR-026**: tool calls, `tools/list`, health probes and ambiguous-call probes on a retired instance MUST fail with `ErrRetired` and MUST NOT mark the current instance unhealthy.

### Functional Requirements: Errors and surfaces

- **FR-027**: errors MUST map as follows. `ErrServerNotFound` → 404 / MCP `isError` "Server 'x' not found". `ServerExistsError` → 409 (unchanged). `ErrServerReplaced` → 409 `server_replaced`. `ErrStaleGeneration` and `ErrRetired` are internal and never reach a surface.
- **FR-028**: the events `servers.changed`, `review.changed`, `config.saved` and `config.reloaded`, and the activity records, MUST be sent after the commit is released, at most once per commit and reason.

### Key Entities

See [data-model.md](data-model.md): `CommitTx`, `CommitResult`, `PreparedSnapshot`, `ServerGen{Gen, Incarnation}`, `UpstreamRecord.Generation/Incarnation`, `PurgeSet`, `PurgeReport`, `Manager.desired`, `dialgate.Gate`, and the errors.

## Success Criteria

- **SC-001**: every acceptance scenario in US1-US4 has a deterministic hook-gated test that fails on `main` (or on the parent PR) and passes on its PR, under `go test -race`, 20 runs with `-count=20` and at `GOMAXPROCS=1` and `4`.
- **SC-002**: `check-upstream-write-sites.sh` reports zero violations, and CI runs it.
- **SC-003**: live harness checks L1-L4 ([quickstart.md](quickstart.md)) pass on the built binary. The evidence goes under `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s116-pr<N>/EVIDENCE.md` and is not committed.
- **SC-004**: a commit with no index work takes p99 < 50 ms on the qa070 `big` profile (25 servers). A removal with an index purge of a 320-tool server takes p99 < 500 ms. A commit that retires one instance adds < 100 ms. All three are measured in PR2 and PR4.
- **SC-005**: no new dependencies. Both golangci-lint passes (bare and `--build-tags server`) are clean. `./scripts/test-api-e2e.sh` passes. The CI `-skip` race lane is green.
- **SC-006**: the existing tests for #1570 (create-only), #1571 (approval lock), #937 (admission) and #1061 (quarantine purge) still pass without edits to their assertions.

## Assumptions

- **A1**: `UpstreamRecord` fields are a subset of `ServerConfig`. Config-only fields are already preserved from the snapshot. Storage stopping to feed the live config therefore loses nothing.
- **A2**: when a storage failure is followed by a failed compensating disk write, the disk may run ahead of the live config until the next commit. This is logged at error level. It matches today's "in-memory agrees, disk stale" failure mode.
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
| Review R1.6 boot / no-op commits | FR-001a | PR1 | T1.10 |
