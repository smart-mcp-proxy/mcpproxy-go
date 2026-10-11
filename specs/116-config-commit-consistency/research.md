# Research: Config Commit Consistency, Atomic Removal and Upstream Retirement

**Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)
**Baseline**: `origin/main` at `ee7834e71`. Every `file:line` below refers to that commit.

This document records the code facts the design rests on, what the rejected
branch tried, and each design decision with the alternatives that were turned
down.

## 1. How a server change is committed today

There are three stores for one server: the **live config** (the `configsvc`
snapshot plus the legacy `r.cfg`), **storage** (the bbolt `upstreams` bucket and
the per-server buckets beside it), and the **config file** on disk. The
**upstream manager** (`upstream.Manager.clients`) holds the running client.
Nothing makes a change land in all four together.

| Writer | What it does, in order | Lock held | Cite |
|---|---|---|---|
| `Runtime.ApplyConfig` | validate, save disk, swap `r.cfg`, publish to `configsvc`, release the lock, **then** spawn `LoadConfiguredServers(copy)` in a goroutine | `configCommitMu` around everything except the reconcile | `runtime.go:1823-1835`, publish `:2075`, async reconcile `:2100-2116` |
| `Runtime.ReloadConfiguration` (file watcher) | `configSvc.ReloadFromFile` publishes the **raw** file, republishes the pinned copy, runs `LoadConfiguredServers(nil)` synchronously | `configCommitMu` | `lifecycle.go:1859-2022` |
| `Runtime.LoadConfiguredServers` | read storage, admission gate, prune orphan approvals and history by the **snapshot's** name set, save every snapshot server to storage, spawn connect/disconnect goroutines, spawn removal goroutines for names missing from the snapshot | **none** of its own | `lifecycle.go:1396`, prune `:1456-1478`, save `:1557`, workers `:1573`, `:1591`, removal `:1598-1645` |
| `Runtime.SaveConfiguration` | **rebuilds the server list from storage**, publishes it, writes disk | `configCommitMu` | `lifecycle.go:1667-1677`, rebuild `:1703` |
| `Runtime.EnableServer` | storage flag, `SaveConfiguration`, async `LoadConfiguredServers(nil)` | storage write before the lock | `lifecycle.go:2081-2108` |
| `Runtime.QuarantineServer` | storage flag (under the approval lock), index purge, stamp explicit bit, `SaveConfiguration`, `LoadConfiguredServers(nil)` | storage write before the lock | `lifecycle.go:2175-2215` |
| `Runtime.BulkEnableServers` | per-name storage flag, one save, one reconcile | storage writes before the lock | `lifecycle.go:2260-2305` |
| `Runtime.RestartServer` | read disk entry, save it to storage, re-create the client in a goroutine | none | `lifecycle.go:2332-2349`, `:2382` |
| `Server.AddServer` / `createServer` | `createMu`; existence check against `runtime.Config()`; `CreateUpstreamServer`; `UpdateConfig(copy of Config())` | `createMu`; `configCommitMu` only inside `UpdateConfig` | `server.go:1969`, `config_cow.go:74-96` |
| `Server.UpdateServer` (REST PUT/PATCH) | read storage, merge, save storage, **mutate the published snapshot in place**, `UpdateConfig`, `SaveConfiguration` | none for the read-modify-write | `server.go:2016-2125`, in-place write `:2117` |
| REST PATCH handler | resolves omitted fields from `GetConfig()` before calling `UpdateServer` | none | `httpapi/server.go:2682-2982` |
| `Server.RemoveServer` (REST) | manager `RemoveServer`, storage delete + config drop under `createMu`, OAuth clear, index delete, tool-approval delete, `SaveConfiguration` | `createMu` for the delete only | `server.go:2139-2190` |
| MCP `upstream_servers` add | `createServer` | as above | `mcp.go:8520` |
| MCP update / patch | read storage, merge, `UpdateUpstream`, manager `RemoveServer` + `AddServer`, `SaveConfiguration` | none | `mcp.go:6144-6226`, `:6264-6334` |
| MCP remove | storage delete + config drop, manager remove, index delete, save | `createMu` for the delete only | `mcp.go:6069-6120` |
| MCP `quarantine_security quarantine_server` | sets `Quarantined=true` on the storage row via `UpdateUpstream`, saves | none; **skips `QuarantineServer`** | `mcp.go:5681-5716` |
| Registry source add/edit/remove | read `Config()`, copy, `UpdateConfig`, save | none for the read | `add_registry_source.go:136`, `edit_registry_source.go:100`, `remove_registry_source.go:60` |
| `Runtime.SetServerShared` | clone the live config and `applyConfigLocked` under the lock | `configCommitMu` for the whole read-modify-write | `server_sharing.go:15-35` |

`SetServerShared` is the only single-server writer that already has the right
shape: read the live config inside the lock, change a copy, commit it. The
design generalizes that shape.

### 1.1 `configsvc` already has a monotonic version

`configsvc.Service.updateLocked` increments `s.version` on every publish and
stores it in `Snapshot.Version` (`configsvc/service.go:196-207`). It is
in-memory only and starts at 0 in each process. `UpdateIfCurrent`
(`service.go:186`) is a compare-and-swap on the config pointer and is used by
the admission gate (`config_load_admission_gate.go:476-480`).

### 1.2 Manager and client

- `Manager.AddServerConfig` replaces a client when the connection fields,
  `Enabled` or `Quarantined` changed. Otherwise it calls `SetConfig` on the
  existing client. It holds `captureMu` then `mu` (`manager.go:443-560`).
- `Manager.AddServer` calls `AddServerConfig`, then `GetClient`, then
  `client.Connect` with no re-check (`manager.go:590-650`).
- `Manager.RemoveServer` deletes the map entry under `captureMu`+`mu` and
  disconnects outside the locks (`manager.go:654-700`).
- `managed.Client.Connect` accepts a client in Disconnected/Error state. Its
  cancel func is installed only after phase 1 (`client.go:436-500`).
  `Disconnect` cancels only an installed connect context (`client.go:651`).
  Nothing stops a removed client instance from dialing later.
- `WithCurrentClientOnEpoch` (`manager.go:1228`) is the capture linearization
  point. Its callers hold `captureMu` (read) and then take the per-server
  approval lock inside `persistQuarantinedToolDefinitions`
  (`lifecycle.go:649-675`). **Existing lock order: `captureMu` → approval
  lock.**
- The supervisor dispatches each action in its own goroutine with the snapshot
  it planned from (`supervisor.go:442-455`). `executeAction` calls
  `upstream.AddServer` / `ConnectServer` from that snapshot
  (`supervisor.go:569-650`). `ActorPoolSimple.ConnectServer` does
  `GetClient` + `Connect` (`actor_pool.go:123`).

### 1.3 Dial and request sites

- stdio launch uses `context.Background()` for `client.Start`
  (`core/connection_stdio.go:257-259`). Cancelling the connect context cannot
  stop it.
- HTTP/SSE: auth strategies build an `http.Client` and call `Start` +
  `initialize` (`core/connection_http.go:177-230`). The base transport is
  `http.DefaultTransport` in most branches (`transport/http.go:245-262`,
  `:444`). That pool is **shared by every upstream**.
- OAuth: `CreateOAuthConfig...` builds its own `http.Client` around a wrapper
  of a separate transport (`oauth/config.go:1255-1322`). Token refresh goes
  `RefreshManager.executeRefresh` → `Runtime.RefreshOAuthToken` →
  `Manager.RefreshOAuthToken` → the registered client's
  `RefreshOAuthTokenDirect` (`refresh_manager.go:604`, `runtime.go:3283`,
  `manager.go:3496`).

### 1.4 Name-keyed security state

The bbolt buckets keyed by server name: `upstreams`, `tool_approvals`,
`prompt_approvals`, `oauth_tokens` (key `name_<16 hex>` from
`oauth.GenerateServerKey`, `persistent_token_store.go:64`),
`oauth_completion`, `integrity_baselines`, `security_reports`,
`security_scan_jobs` (+ `security_scan_job_index`), `server_identities` and the
per-identity tool-call buckets, `toolhash` (`storage/models.go:29-66`,
`tool_call_bounds.go:34`). In memory: supervisor inspection exemptions,
discovery generations, the refresh schedule, the toolsig cache, call stats and
the limiter (the last two are already dropped by `Manager.RemoveServer`).

Removal today purges different subsets on different paths:

| Purged | REST remove | MCP remove | Config-driven removal |
|---|---|---|---|
| upstream row + call history | yes | yes | yes |
| search index | yes | yes | yes |
| tool approvals | yes | **no** | only via orphan prune, skipped when the configured set is empty or the name was resurrected |
| prompt approvals | **no** | **no** | **no** |
| OAuth tokens / DCR client | yes (prefix-collision bug, B2) | **no** | **no** |
| integrity baseline, scan reports/jobs | **no** | **no** | **no** |
| refresh schedule | yes | **no** | **no** |

## 2. Baseline evidence (live, this design session)

Evidence file (not committed):
`docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s116-design/EVIDENCE.md`.

- **B1, new finding.** MCP `quarantine_security {operation: quarantine_server}`
  leaves the server's tool descriptions retrievable. A trusted 320-tool fixture
  returned the same 3 `retrieve_tools` hits 10 s and 35 s after the quarantine
  succeeded, while `/api/v1/servers` reported `quarantined=true`. The log has no
  "Removed quarantined server tools from index" line. Cause:
  `handleQuarantineUpstream` writes the storage row directly
  (`mcp.go:5708-5709`). The following reconcile sees storage already
  quarantined, so `newlyQuarantined` is false (`lifecycle.go:1528`) and nothing
  purges. This is the #1061 control bypassed. It is the same class as Sol's
  r10-C #2 (REST PATCH quarantine), which still applies on main.
- **B2, new finding.** `ClearOAuthState(name)` deletes every key with the prefix
  `name + "_"` (`storage/manager.go:2489`). Server `foo_bar`'s keys
  (`foo_bar_<16 hex>`) match `foo_`, so **removing `foo` logs out `foo_bar`**.
  A throwaway test confirmed it (`oauth token not found` for `foo_bar`'s token
  after `ClearOAuthState("foo")`).

Both are folded into the requirements (B1 → FR-008, B2 → FR-011).

## 3. The rejected branch `fix/qa070-ux01-create-only-add`

13 commits, 51 files, +5,220/−748. It grew round by round as Sol 6.1 found new
interleavings (`docs/qa/0.70.0-ux-2026-10-10/remediation/reviews/ux01-r1..r10*.txt`).
The minimal create-only add was split out and landed as #1570, and the
per-server approval lock as #1571. The rest was not merged.

### 3.1 What worked and is kept

- **Atomic create in storage** (`CreateUpstreamServer`, one bbolt tx) and the
  `ServerExistsError` contract. These landed in #1570 and stay.
- **Re-reading under the lock.** Running reconcile inside `configCommitMu` and
  reconciling against the *live* config instead of a captured snapshot fixed the
  r2–r5 findings about deleted servers and pruned approvals.
- **Resolving PATCH inside the commit** (r9-C #1). Presence-aware resolution has
  to run against the record read inside the lock.
- **Hook-gated deterministic tests** (`loadConfiguredServersBeforeSyncHook` and
  the r6–r10 test files). Sol accepted a test only when it parked a real
  production path at the exact window. Tests that settled the manager before
  the stale action were rejected as vacuous (r8-BC, r9-D #2, r10-A #3).
- **One purge for every removal path** (r8-A #3). The idea held. The
  implementation missed buckets (prompt approvals, r9-B #3, r9-D #1).

### 3.2 What Sol rejected, and why

| Attempt | Sol's objection | Lesson for this design |
|---|---|---|
| Serialize the reconcile but leave connect/disconnect goroutines outside the lock (r5→r6) | The lock covers scheduling, not execution. A delayed worker installs a stale config (r6a #1, r6b #1) | Workers must be validated **at execution time** against something the commit updates atomically. That is a per-server generation checked inside the manager's registration lock (D6) |
| Validate the live config before `AddServerConfig` in the runtime only (r7) | The supervisor's actions bypass it (r7b #2), and `GetClient`→`Connect` still dials a removed client (r7a #1, r7b #1) | The check must live **in the manager** so every caller hits it, and a removed instance needs a latch of its own (D6, D8) |
| Pending-removal tombstones so `SaveConfiguration` would not resurrect a removed server (r6–r10) | Every new reader of the server list had to learn the tombstone, and one always didn't: the supervisor (r10-B), update/patch (r9-C #2), the orphan prune (r8-A #3) | **Remove the resurrection source instead**: `SaveConfiguration` stops rebuilding the server list from storage, and removal becomes synchronous inside the commit. No tombstone is needed (D2, D4) |
| A `retired` flag checked before dialing (r8) | The check-then-act gap remains, and stdio uses `context.Background` (r8-BC #1) | Admission must be a lock held across the irreversible step, not a flag read (D8) |
| HTTP admission released before the auth strategy runs (r9-A #1) | Requests with credentials go out after retirement | Gate the **bytes**, not the strategy: wrap the dialer and every connection write (D8) |
| A request gate on the `RoundTripper` (r10) | `net/http` retries below the wrapper, `WroteRequest` fires per attempt, and OAuth's `HTTPClient` is outside it (r10-A #1, #2) | Gate at the `net.Conn` level, on a transport that belongs to one client instance, shared with that instance's OAuth client (D8) |
| Retire the client without invalidating its capture identity (r9-A #2) | A late `tools/list` capture is still accepted for the replacement | Retirement must **unregister** the instance under `captureMu` in the same critical section as the generation advance (D7) |
| `CommitServerUpdate` built the replacement from storage (r9-B #2) | It erased the config-only `Shared` and `AuthBroker` fields | Mutations start from the **live config entry**, which carries every field (D3) |

The overall lesson: the branch patched each reader of a racy two-source model.
Sol kept finding the next reader. This design removes the second source
(storage as an input to the live config) and gives the remaining asynchronous
actors a single value to check (the server generation).

## 4. Decisions

### D1. One commit primitive for every server writer

**Decision.** Add `Runtime.Commit(reason, fn)` (`internal/runtime/commit.go`).
It holds `configCommitMu` for the whole read-modify-write: it reads the live
config, lets `fn` mutate a copy through a `CommitTx`, and then runs the commit
pipeline (D5). Every writer in §1 calls it: `ApplyConfig`,
`ReloadConfiguration`, the boot reconcile, `UpdateConfig`, `SaveConfiguration`,
REST/MCP add, update, patch, quarantine, enable and remove, bulk enable,
sharing, registry sources, restart-from-disk, and the startup admission gate.

**Alternatives rejected.**
- *Per-server locks.* ApplyConfig and Reload change many servers at once, and
  `SaveConfiguration` writes the whole file. Per-server locks would need
  multi-lock acquisition on every whole-config path, with more deadlock surface
  and no gain at this write rate (a handful of commits per minute).
- *Optimistic CAS on the config pointer* (`UpdateIfCurrent` everywhere). It
  makes callers retry, and a retry after a storage write cannot be undone
  cleanly. Storage and disk side effects need mutual exclusion, not CAS.

### D2. The live config is the only source of server membership and fields

**Decision.** Storage becomes a projection of the live config that is written
inside the commit. `SaveConfiguration` stops reading `ListUpstreamServers`
(`lifecycle.go:1703`) and persists the live snapshot (pending-aware, as today).
No writer may write an `upstreams` row outside `commit.go`. PR1 adds
`scripts/check-upstream-write-sites.sh`, which fails CI when
`SaveUpstreamServer`, `CreateUpstreamServer`, `UpdateUpstream`,
`DeleteUpstreamServer`, `RemoveUpstream`, `EnableUpstreamServer` or
`QuarantineUpstreamServer` is called outside the storage package, `commit.go`
and tests. It follows the precedent of `scripts/check-no-unquarantine-callers.sh`.

**Why.** Rebuilding the list from storage is what resurrects removed servers
(#1559 bullets 1-2) and reverts concurrent decisions (#1558 bullet 4). With
one source there is nothing to resurrect from.

**Assumption A1.** The `UpstreamRecord` fields are a subset of `ServerConfig`
(`upstreamRecordFromConfig`, `storage/manager.go:119-155`). The config-only fields (`Shared`, `AuthBroker`)
already come from the snapshot, so no field is lost when storage stops feeding
the live config.

**Startup.** The boot reconcile still treats the config file as the source of
truth, as today. Storage rows for names absent from the file are removed. The
only storage-to-config flow that remains is the admission gate's "inherit a
recorded quarantine" rule (`applyConfigLoadAdmissionGate`). It runs in the
commit's prepare step, so storage and the published config agree.

### D3. Mutations run on the live entry, inside the lock

`CommitTx.UpdateServer(name, func(cur *config.ServerConfig) error)` hands the
callback a deep copy of the **live** entry. REST PATCH moves its presence-aware
resolution (`httpapi/server.go:2707-2949`) into such a callback. MCP
update/patch call `config.MergeServerConfig(cur, patch, opts)` inside it.
Existence is checked against the live config, so a removed server is
`not found` (#1560 scenarios 1, 2, 4). The in-place write into the published
snapshot (`server.go:2117`) goes away.

### D4. Removal is synchronous and atomic, and the purge is one bbolt transaction

**Decision.** `CommitTx.RemoveServer(name)` drops the entry. The pipeline then,
in one bbolt transaction, deletes the row and purges every name-keyed security
bucket (§1.4). It does this under the server's approval lock and stamps the
removal. Index purge and in-memory cleanup (refresh schedule, inspection
exemption, toolsig cache) follow in the same commit. REST, MCP and config-driven
removal all use it. The config-driven goroutines (`lifecycle.go:1624`) are
deleted.

**Why synchronous.** The asynchronous cleanup window is where every
resurrection in #1559 and r10-B happens. The bbolt work takes milliseconds. The
index delete for a 320-tool server takes tens to low hundreds of milliseconds.
The disconnect, which can take seconds, stays asynchronous because retirement
(D8) has already made the instance harmless.

**Alternative rejected.** Tombstones (§3.2).

### D5. Commit pipeline order

Inside `configCommitMu`:

1. **Build**: clone the live config, run `fn`.
2. **Gate**: run the admission gate against the stored view. It forces
   `Quarantined=true` where storage keeps a quarantine without an explicit
   decision, which is the same predicate as `SaveUpstreamKeepingQuarantine`.
   The storage write in step 5 then never silently diverges from what is
   published.
3. **Prepare**: `configsvc.Prepare(next)` runs the pre-publish hook, assigns
   generation `G = version+1`, and computes per-server generations (D6). Nothing
   is visible yet.
4. **Disk**: write the pending-aware file and record the self-write marker.
   This is done first so that a failure leaves nothing changed. ApplyConfig
   already saves first (`runtime.go:1930`).
5. **Storage**: for each server whose generation changed, take its approval
   lock (sorted by name). Run **one** bbolt transaction with the row
   saves/deletes, the purge, the generation stamp on each row and the meta key
   `config_commit_generation = G`. Purge the index for removed or newly
   quarantined servers. Release the approval locks. On failure, rewrite the
   previous config to disk (compensating write), return the error and publish
   nothing.
6. **Advance manager** (PR3/PR4): unregister and retire instances whose
   generation moved with a connection-relevant change. Restamp instances with a
   generation-only change.
7. **Publish**: `configsvc.Publish(pending)` and the legacy `r.cfg` and
   `desiredCfg` swap.

After unlock: start the workers (bound to `G` in PR3), send events
(`servers.changed`, `review.changed`, `config.saved`), notify telemetry, and
disconnect retired instances in the background.

**Assumption A2.** When the compensating disk write fails after a storage
failure, the error is logged at error level and the disk stays ahead of the
live config until the next commit rewrites it. Today a failed save already
leaves "in-memory stores agree, disk stale" (`lifecycle.go:1751-1760`). This
keeps that guarantee.

### D6. Per-server generation

**Decision.** The commit generation `G` is `configsvc`'s version. It is
persisted in the `meta` bucket and used as the seed at startup, so it is
monotonic across restarts. `Snapshot` gains an immutable
`ServerGenerations map[string]ServerGen` (`{Gen, Incarnation}`). For each server, Prepare keeps the
previous generation when the entry is semantically unchanged
(`config.ServerEntryEqual`, which ignores `Created`/`Updated`), and assigns `G`
when the entry is new or changed. Removed names drop out of the map. A created
name gets `Incarnation = G`, which is kept through updates. The storage row
stores both (additive `UpstreamRecord.Generation` and `.Incarnation`,
`omitempty`).

**Who checks it.**
- *Manager* (PR3): `AddServerConfigAt(name, cfg, gen)`,
  `RemoveServerAt(name, gen)` and `ConnectServerAt(ctx, name, gen)` compare
  against `Manager.desired[name]`, which only the commit sets (step 6) under
  `captureMu`+`mu`. A mismatch returns `ErrStaleGeneration`. The check happens
  in the same critical section as the map edit, so it has no
  time-of-check/time-of-use gap.
- *Approval writers* (PR2): under the server's approval lock they compare the
  **incarnation** they captured (the generation at which the name was last
  created) against the **storage row's** incarnation. Binding approvals to the
  incarnation instead of the per-change generation means an unrelated edit
  (say, `disabled_tools`) never rejects an approval in flight, while a remove
  and re-add always does. Approvals already survive an endpoint edit today
  (they are keyed by name and hash), and changing that is out of scope. Step 5
  writes that generation in the same transaction as the purge, so the approval
  lock plus the bbolt row are the linearization point. The approval lock does
  not have to be held across manager or publish steps.

**Alternatives rejected.**
- *Compare the global version.* Any unrelated commit would invalidate every
  in-flight worker.
- *Pointer identity of `*ServerConfig`.* Pointers change on every clone, even
  when the entry has not changed.
- *A generation that only the runtime knows.* The manager would then have to
  call back into the runtime, which brings back the cross-component gap from
  r7a.

### D7. Retirement unregisters under `captureMu` before publication

In step 6 the manager removes the stale instance from `clients` while holding
`captureMu` for writing. A capture section that is already running either
finishes before the commit proceeds, or sees the instance missing and drops
its result. This answers r9-A #2. The commit takes `captureMu` **after**
releasing the approval locks. That respects the existing order
`captureMu` → approval lock (§1.2), so the two cannot deadlock.

### D8. Retirement barrier: a per-instance dial gate at the byte level

**Decision.** A new package, `internal/upstream/dialgate`. Each
`managed.Client` instance owns one `Gate`:

- `Admit(ctx) (release, error)` takes a read lock and fails with `ErrRetired`
  once the gate is retired. It wraps irreversible steps that are not network
  writes: `exec.Cmd.Start` for stdio, the Docker isolation launch and the
  launcher spawn (`core/connection_stdio.go:258`, `connection_launcher.go`).
- `DialContext(next)` wraps the dialer. Admission covers the dial, and each
  returned connection is tracked and wrapped so that **every `Write`** runs
  under the read lock and checks the latch. TLS handshakes and HTTP/2 frames go
  through the same `Write`.
- `Retire()`: sets the latch, cancels the gate context (aborting dials that are
  in progress), closes every tracked connection (unblocking writes in
  progress), then takes and releases the write lock. After `Retire` returns, no
  dial, process launch or byte write for that instance can start. Every step
  above is bounded, so a commit never waits on a slow upstream.
- Each instance gets its **own** `*http.Transport` (a clone of the base, with
  the gated dialer) instead of `http.DefaultTransport`. The OAuth `HTTPClient`
  wraps the same transport (`oauth/config.go:1262`, new option
  `WithBaseTransport`). Metadata, DCR, token and refresh requests are gated the
  same way. Internal `net/http` retries must open or reuse a gated connection,
  so they are covered too.

**Invariant.** An instance admitted before `Retire` returns may already have
launched or written. The asynchronous `Disconnect` terminates it. Nothing new
starts after `Retire` returns.

**Alternatives rejected.**
- *A RoundTripper gate* (r10). It misses internal retries and the OAuth client.
- *Context cancellation only.* `net/http` checks the context before each
  attempt, but the attempt itself is not ordered against the cancel, and stdio
  Start ignores it.
- *Keep `http.DefaultTransport`.* Connections pooled across instances cannot be
  tracked per instance.

**Cost.** One `http.Transport` per HTTP upstream instance (idle pool per
instance). Upstreams are distinct hosts in practice, so pooling barely changes.

### D9. Lock order (normative)

```
configCommitMu
  ├─ approval lock(s), sorted by server name   [step 5 only; released before step 6]
  │    └─ storage.Manager.mu → bbolt tx
  │    └─ index (bleve) delete
  ├─ upstream.Manager.captureMu (W) → Manager.mu → dialgate (W, Retire)   [step 6]
  └─ configsvc.updateMu (observers run here) ; Runtime.mu   [step 7, leaves]

Outside a commit (unchanged):
  captureMu (R) → approval lock(one) → storage.Manager.mu
  approval lock(one) → storage.Manager.mu | index | Manager.mu (R, via GetClient)
```

**Forbidden edges.** These are enforced by review and by the
`TestLockOrder_*` watchdog tests.
- Holding an approval lock, `captureMu`, `Manager.mu`, a client gate,
  `storage.Manager.mu`, `configsvc.updateMu` or `Runtime.mu` while calling
  `Commit` or anything that reaches it (`SaveConfiguration`, `ApplyConfig`,
  `ReloadConfiguration`, `QuarantineServer`, `EnableServer`, `UpdateConfig`).
- Taking more than one approval lock outside `Commit`.
- A `configsvc` observer or hook taking any lock above it in this tree.
- `createMu`: deleted in PR1. `createServer` becomes a `CommitTx.CreateServer`
  call, and `configCommitMu` gives the same exclusion. During the PR1 diff, if
  any `createMu` user remains, its order is `createMu` → `configCommitMu`.

### D10. Approval writers bind to the generation they reviewed

`ApproveTools`, the baseline approval in `QuarantineServer(false)`, prompt
approval writers, scanner `approveServer` (baseline+blocks under the approval
lock, `service.go:1796`) and discovery's `checkToolApprovals` capture the
incarnation when they start. They re-check it against the storage row under
the approval lock (D6). A mismatch drops the write with `ErrServerReplaced`, which
is HTTP 409 on REST and an `isError` result on MCP. Scanner `UnquarantineServer`
becomes `Commit` with `tx.ExpectIncarnation(name, inc)`, so a remove plus re-add between
scan approval and unquarantine cannot release the replacement (#1562
bullet 4). Prompt approval writes (`internal/server/prompt_quarantine.go`) move
under the same per-server approval lock, which this spec renames "server
approval lock" in documentation.

REST approval endpoints accept an optional `expected_incarnation`. The review
payload exposes `incarnation`, and the Web UI sends it back. When it is
absent, the handler uses the incarnation it read when the request started.

### D11. What stays asynchronous

- Connecting and disconnecting upstreams, tool discovery and indexing of newly
  connected tools. These are bound to `G` in PR3.
- Events, telemetry and activity records.
- Killing the processes of retired instances.

## 5. Open questions (with the default taken)

| # | Question | Default (assumption) |
|---|---|---|
| Q1 | Should `expected_incarnation` be **required** on approval endpoints? | Optional. The handler falls back to the incarnation read at request start. Making it required breaks the CLI and older trays (A6) |
| Q2 | Scan reports for a removed server: purge or keep as history? | Purge. The scanner approval reads the latest report by name (`service.go:1733`), so a leftover clean report would let a same-name replacement be approved without a scan. The activity log keeps the audit trail (A7) |
| Q3 | Should index purge stay synchronous inside the commit? | Yes for removal and quarantine (security). The index is the TPA control (#1061). Measure it in PR2. If a 1k-tool server takes over 1 s, revisit (A8) |
| Q4 | Generation across restarts? | Persisted in `meta.config_commit_generation`, seeded at startup (D6) |
| Q5 | Cap on `Retire` duration? | All of its steps are bounded. A `Retire` slower than 1 s logs a warning with the instance and the step (A9) |
