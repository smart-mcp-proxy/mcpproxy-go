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
   The storage write in step 6 then never silently diverges from what is
   published.
3. **Prepare**: `configsvc.Prepare(next)` runs the pre-publish hook, assigns
   generation `G = version+1`, and computes per-server generations and
   incarnations (D6). Nothing is visible yet.
4. **Lock and hooks**: take the approval lock of every server whose
   generation changes, sorted by name. Run the approval hooks registered with
   `tx.UnderApprovalLock(name, fn)`. These are the review-bound server
   approval steps from Spec 117 (validate, record the decision, baseline and
   blocks, promote). A hook error aborts the commit. Hooks may write only in
   the fail-safe direction (§6), because steps 5-6 can still fail after them.
5. **Disk**: write the pending-aware file and record the self-write marker.
   A failure aborts the commit before any server row changes. ApplyConfig
   already saves first (`runtime.go:1930`).
6. **Storage**: run **one** bbolt transaction with the row saves/deletes, the
   purge, the generation and incarnation stamps, and the meta key
   `config_commit_generation = G`. Purge the index for removed or newly
   quarantined servers, then do the in-memory purge. Release the approval
   locks. On failure, rewrite the previous config to disk (compensating
   write), return the error and publish nothing.
7. **Advance manager** (PR3/PR4): unregister and retire instances whose
   generation moved with a connection-relevant change. Restamp instances with a
   generation-only change.
8. **Publish**: `configsvc.Publish(pending)` and the legacy `r.cfg` and
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
  against `Manager.desired[name]`, which only the commit sets (step 7) under
  `captureMu`+`mu`. A mismatch returns `ErrStaleGeneration`. The check happens
  in the same critical section as the map edit, so it has no
  time-of-check/time-of-use gap.
- *Approval writers* (PR2): under the server's approval lock they compare the
  **incarnation** they captured (the generation at which the name was last
  created) against the **storage row's** incarnation. Binding approvals to the
  incarnation instead of the per-change generation means an unrelated edit
  (say, `disabled_tools`) never rejects an approval in flight, while a remove
  and re-add always does. Approvals already survive an endpoint edit today
  (they are keyed by name and hash), and changing that is out of scope. Step 6
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

In step 7 the manager removes the stale instance from `clients` while holding
`captureMu` for writing. A capture section that is already running either
finishes before the commit proceeds, or sees the instance missing and drops
its result. This answers r9-A #2. The commit takes `captureMu` **after**
releasing the approval locks, and never holds both. That makes it compatible
with today's capture order `captureMu`(R) → approval lock (§1.2) **and** with
Spec 117 PR-3's inverted order (approval lock → `captureMu`(R) → `epochMu`,
§6). Neither can deadlock with the commit.

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

**Amended after review R1.3/R1.4** (§7): mcp-go transports are wrapped by
`dialgate.WrapTransport` (stdio stdin writes included). `Retire` kills a stdio
process group synchronously. Every OAuth preflight, discovery and cache fetch
takes the instance transport through `oauth.Env`.

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
  ├─ approval lock(s), sorted by server name   [steps 4-6; released before step 7]
  │    └─ storage.Manager.mu → bbolt tx
  │    └─ index (bleve) delete
  ├─ upstream.Manager.captureMu (W) → Manager.mu → dialgate (W, Retire)   [step 7]
  └─ configsvc.updateMu (observers run here) ; Runtime.mu   [step 8, leaves]

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

## 6. Coordination with Spec 117 (#1565-#1567, branch `117-review-bound-approvals`)

Spec 117 is being designed in parallel and touches the same seams. These are
the integration points. Whichever spec's PR lands second adapts, as stated
here.

| Spec 117 element | Interaction with this spec | Rule |
|---|---|---|
| `CommitServerApproval` (one hold of the approval lock: validate `expected_hashes`, mark baseline, commit baseline and blocks, promote, **flip storage quarantine**) + `FinishServerApproval` (`SaveConfiguration`, `LoadConfiguredServers`) | After 116 PR1, a storage flip outside `Commit` is lost, because `SaveConfiguration` no longer reads storage, and the write-site script fails CI | The server approval becomes `Commit(fn)`, where `fn` does `ExpectIncarnation(name, inc)` + `SetQuarantined(name, false)` + `UnderApprovalLock(name, validate→decide→baseline/blocks→promote)`. The flip is the commit's row write in step 6, in the same approval-lock hold as the hooks (step 4). `FinishServerApproval` reduces to the after-unlock events. If 117 PR-2 merges first, 116 PR1 task T029a converts it. If 116 PR1 merges first, 117 PR-2 builds on `UnderApprovalLock` |
| Hooks write before the disk and row steps | Steps 5-6 can still fail | **Fail-safe rule**: a hook may only write state that is harmless if the server stays quarantined (tool records, baseline marker, blocks). The quarantine flip itself is only ever the commit's row write |
| Durable tool-baseline marker (117 PR-1), "cleared only by removing the server" | Must be in the removal purge | Added to the PurgeSet (data-model §PurgeSet). The purge is the only path that clears it |
| Capture sequence and `managed.Client.Retire()` / `CaptureIsCurrent` (117 PR-3), "manager retires replaced clients" | Same retirement point as 116 PR3/PR4 | There is one `Retire()` per instance. 116 PR3's `AdvanceGenerations` is the manager-side retirement point for commit-driven replacement. 117's `Retire()` (capture invalid) and 116 PR4's `Gate.Retire()` (dial barrier) are one method: whichever lands second extends it |
| Lock order `captureSnapMu → approval lock → captureMu(R) → epochMu` | 116 adds `configCommitMu → approval locks` (released) `→ captureMu(W)` | The union is acyclic: `Commit` never takes `captureSnapMu`, never holds an approval lock while taking `captureMu`, and no holder of `captureSnapMu`, an approval lock, `captureMu` or `epochMu` calls `Commit` |
| `QuarantineServer` purge moved under the approval lock (117) | 116 runs the index purge in step 6 under the approval lock | Same outcome. The 116 commit subsumes it |

## 7. Design review round 1 (opencode `github-copilot/gpt-6.1-sol`, 2026-10-11)

Verdict: FINDINGS (8). Each was checked against the code before it was
accepted. All 8 were genuine, and their resolutions are folded into the spec:

| # | Sev | Finding (verified at) | Resolution |
|---|---|---|---|
| R1.1 | high | Scan jobs and reports are saved from asynchronous scanner callbacks keyed by name (`scanner/service.go:892-916`). A scan started for the old `acme` can finish after the purge and restore a clean report that a replacement's approval then reads (`:1733`) | **D12**: scan jobs and reports carry the incarnation they were started for. They are written with `Save*IfIncarnation`, which checks the `upstreams` row incarnation in the same bbolt tx and drops the write on mismatch or a missing row. `approveServer` only reads reports of the current incarnation. Removal invalidates the scan-summary cache (FR-014a, T047, T055a) |
| R1.2 | high | `PersistentTokenStore.SaveToken` checks the context once and then upserts (`persistent_token_store.go:232-293`, `bbolt.go:1112-1133`). A delayed code-exchange or refresh result recreates purged tokens, and the same-name/same-URL re-add inherits them | **D12**: token, DCR-client and `oauth_completion` writes become `…IfIncarnation` conditional writes. The token store is constructed with the instance's incarnation. `RefreshManager` schedules are keyed by `(name, incarnation)` and dropped on mismatch (FR-014b, T048, T055b) |
| R1.3 | high | Gating `cmd.Start` does not gate later writes to the child's stdin. Initialize, notifications and tool calls go through mcp-go's stdio transport (`connection_stdio.go:235-244`, `:289-292`) | **D8 amended**: every mcp-go `transport.Interface` that core builds (stdio, streamable HTTP, SSE) is wrapped by `dialgate.WrapTransport`. It gates `Start`/`SendRequest`/`SendNotification` under the read lock. For stdio, `Retire` also kills the process group synchronously (SIGKILL / Job Object terminate, which is bounded). A stdio instance can then make no write after `Retire` returns (FR-023a, T081a) |
| R1.4 | high | OAuth preflight and discovery use their own clients: `http.Head` (`oauth/config.go:2048`), `autoDetectResource`'s client (`:637`), `httpFetchRaw` (`discovery_cache.go:299-307`) | **D8 amended**: an `oauth.Env{Transport, Ctx}` is threaded through every preflight, discovery and cache-fetch helper and `CreateOAuthConfig*`. `scripts/check-upstream-write-sites.sh` (PR4 extension) forbids `http.Get/Head/Post(`, `http.DefaultClient`, `http.DefaultTransport` and `&http.Client{` without an explicit gated transport in `internal/oauth/**`, `internal/upstream/core/**` and `internal/transport/**` (FR-024a, T084a, T099) |
| R1.5 | high | Supervisor disconnects (`ActionDisconnect` and the first leg of `ActionReconnect`, `supervisor.go:611-616`) resolve the client by name when they run (`actor_pool.go:146-154`), and the design had no generation-bound disconnect | `DisconnectServerAt(name, gen)` added. It acts only on the instance registered at `gen`, and every disconnect leg uses it (FR-018, T061a) |
| R1.6 | high | No-op detection would skip boot reconciliation: `configsvc.NewService` already holds the file config (`service.go:79-87`), so `ReplaceConfig(live)` compares equal while storage and the manager are empty. An unchanged `SaveConfiguration` would skip its disk write | **D13**: step 6 diffs `next` against the **stored view** (rows plus generation stamps), not against `base`, so storage is always repaired. `tx.Bootstrap()` (boot only) seeds generations from the stored rows that match and schedules workers for every enabled server. `tx.ForceDisk()` (`SaveConfiguration`) always writes the file. The skip rule is "next == base **and** stored view consistent **and** not forced" (FR-001a) |
| R1.7 | medium | The admission pre-publish hook reads storage (`config_load_admission_gate.go:311`, `:408`) under `configsvc.updateMu`, which contradicts "L6 is a leaf" | The pre-publish hook is **removed** in PR1. Every publish goes through `Commit`, whose step 2 runs admission once on captured inputs before `Prepare`. The only observer left under L6 is the profile-index warm (`server/profile_tool.go:892`, its own `warmMu`, a leaf) (T021) |
| R1.8 | medium | Several T tests park inside `configCommitMu` (or inside the capture or admitted section) and then wait for a competing commit to return, which deadlocks the test. T2.3 already passes on PR1 | quickstart restructured into **stale-read tests** (the writer is parked **before** `Commit`, at its own read site) and **exclusion tests** (park inside, assert the competitor is **blocked** through a `commitWaiters()` probe, release, assert the order). T3.5/T4.x release the held section before awaiting the commit. T2.3 moved to PR1 as regression coverage |

### D12. Asynchronous writers of purge-set state are incarnation-conditional

Some state in the purge set is written by asynchronous workers that outlive a
removal: scanner callbacks, OAuth token persistence, refresh scheduling and
discovery. Gating the network is not enough, because the write is local. Each
such write is a storage method that takes the expected incarnation and, **in
the same bbolt transaction**, checks that the `upstreams` row exists and
carries that incarnation. The commit's purge runs in one transaction with the
row delete (D4), so either the write committed first (and is purged) or it
sees the row missing or re-incarnated (and is dropped). No lock is needed
beyond bbolt's own serialization, so this adds no lock-order edge.

### D13. Projection repair is not a config change

A commit has two diffs:
- **Config diff** (`base` → `next`): decides generations, publication and events.
- **Projection diff** (`next` → stored view): decides storage writes.

The commit skips work only when both are empty and nothing is forced. Boot
(`Bootstrap`) and an explicit `SaveConfiguration` (`ForceDisk`) are the two
forced cases. Generations survive restarts through the row stamps: at boot a
row whose content equals the file entry keeps its stored `{Gen, Incarnation}`,
and everything else gets the boot `G`.

## 8. Design review round 2 (opencode `github-copilot/gpt-6.1-sol`, 2026-10-11)

Verdict: FINDINGS (6). All were verified and accepted. Round 1's resolutions
were not disputed, except where R2.3, R2.4 and R2.6 refine them.

| # | Sev | Finding | Resolution |
|---|---|---|---|
| R2.1 | high | `ReplaceConfig(old)` from a caller holding a stale captured config would still delete B. The real #1558 path is the **post-publish** async reconcile (`runtime.go:2075`, `:2104-2106`), not a replacement | **D14**: two different ops. `ReplaceConfig(cfg)` is authoritative and used only with a config the caller owns and submitted in this call (PUT/apply body, the file read inside the lock by reload, boot). `ReconcileLive()` takes **no argument** and reconciles the base read inside the lock. `LoadConfiguredServers(cfg)`'s captured-argument form is deleted, and ApplyConfig/Reload reconcile inside their own commit. T1.1 parks the real downstream path: a `reconcileEntry` seam at today's `LoadConfiguredServers` entry (`lifecycle.go:1396`), which T001 adds on `main` as test infrastructure |
| R2.2 | high | Discovery checks approvals under L1 but indexes **after** releasing it (`lifecycle.go:1018-1040`). A pass that checked before the commit can re-index a server the commit has just purged (quarantine or removal) | **D15**: a discovery pass's final eligibility check (row `Generation` == captured generation, not quarantined/disabled, row present) **and** its index/StateView/last-good writes run under L1 in one hold. The commit's step 6 bumps the row generation and purges under the same L1, so a pass either finishes before the purge (and is purged) or sees the new generation and writes nothing. Index writes under L1 are already allowed (L1 → L1'). This is the same rule as Spec 117 G8. Whichever PR lands first implements it: 116 PR1 T024a, or 117 PR-3 |
| R2.3 | high | Wrapping whole transport operations with the same `RWMutex` that dials and writes take recursively nests `RLock`, deadlocking behind a queued `Retire`, and would make `Retire` wait for responses | **D8 rule**: the gate `RWMutex` read side is held **only around one leaf operation**: a dial syscall, one `net.Conn.Write`, or `cmd.Start` + process registration. It is never held while calling code that can itself admit. `WrapTransport` takes **no lock**: it checks the atomic latch and merges the gate context into the operation context. The byte-level gate gives the ordering guarantee. Test T4.10: retire while an admitted request is about to enter its nested dial and write |
| R2.4 | high | The process group and Windows Job are registered **after** `Start` returns (`connection_stdio.go:259-275`, `process_windows.go:40-43`), so a kill hook can miss a just-launched child | **D8 rule**: the admitted stdio section is `cmd.Start` **plus** process-group / Job registration **plus** `gate.TrackProcess(handle)`, all under one read lock. If Job assignment fails, the child is killed inside the section and Start fails. `Retire` order: latch → cancel ctx → close conns → kill every tracked process (closing their pipes) → write-lock barrier (waits for in-flight launch sections) → **kill again** anything registered during the drain → poll ≤1 s for exit. A stdin write blocked on a full pipe is unblocked by the kill |
| R2.5 | high | `lastGoodTools`/`lastCaptureAt` are keyed by name and survive removal. A fast re-add can reapply the old snapshot through the sweep fallback (`lifecycle.go:2659-2680`) or the approval reindex (`tool_quarantine.go:2006-2027`) | Added to the in-memory purge (FR-012). Cached snapshots are stamped with `{Incarnation, Gen}`, and reapply rejects a mismatch (FR-021b, T063a) |
| R2.6 | medium | T2.7 parks inside `checkToolApprovals` (it holds L1, which removal needs). T1.10 already passes on `main`. T1.5's stale read is the append copy at `config_cow.go:93`, not the existence check | T2.7 parks at a new `beforeApprovalLock` seam. T1.10 is reclassified as regression coverage (not failing-first). T1.5 parks after the `Config()` read at line 93, before `UpdateConfig` |

### D14. Authoritative replacement versus reconciliation

| Op | Input | Used by |
|---|---|---|
| `ReplaceConfig(cfg)` | a config the caller submitted **in this call**, or the file read inside the lock | `ApplyConfig` (PUT/apply), `ReloadConfiguration`, boot |
| `ReconcileLive()` | none (base read under the lock) | projection repair: periodic/explicit reconcile, after-restart repair |

No API accepts a previously captured snapshot as a replacement.

### D15. Discovery writes are generation-checked under the approval lock

```
discovery pass (any trigger), per server:
  capture tools + (inc, gen) of the instance
  lock L1(name)
    row := storage upstream row
    if row missing || row.Generation != gen || row quarantined/disabled → record-for-review only (117 rules) / drop; unlock
    checkToolApprovals (inc check, D10)
    index mutation + StateView publish + lastGoodTools{inc,gen}
  unlock L1(name)
```

The order L3(R) → L1 (today's capture path) or L1 → L3(R) (Spec 117 PR-3) is
unchanged. `Commit` still never holds L1 and L3 together.
