# Quickstart: Verifying Spec 116

**Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md) | **Tasks**: [tasks.md](tasks.md)

Two kinds of proof are required for every PR:

1. **Deterministic tests (T)** that park a **real production path** at the
   exact window using the plan's test seams, let a competing commit finish,
   then release the parked path and assert on **every** store: live config,
   storage, disk, manager, index, and the purge-set buckets. Sol rejected tests
   that settled the manager before the stale action (r8-BC, r9-D #2) and tests
   that released the parked request before retirement was observable (r10-A #3).
   Every T test therefore synchronizes on a "competing commit returned"
   channel, never on a sleep.
2. **Live checks (L)** on a built binary with the qa070 harness.

Each T test must **fail on the parent commit** (on `main` for PR1, on PR1 for
PR2 and PR3, on PR3 for PR4). Record the failing output in the PR's evidence
file before implementing.

### Two test shapes (review R1.8)

- **Stale-read tests** prove I2: a writer must not act on what it read before
  the lock. Park the writer **before** it calls `Commit`, at its own read site
  (`restPatchAfterParse`, the worker's `beforeWorkerAct`, the reconcile
  caller's `beforeCommit` seam). Let the competing commit return, then release.
  On `main` the writer has already read its snapshot, so the test fails. After
  the PR the writer re-reads inside the lock, so it passes.
- **Exclusion tests** prove a section is atomic. Park inside the commit
  (`commitHooks.after*`). Start the competitor in a goroutine. Assert it is
  **blocked** through `r.commitWaiters() == 1` (a test probe counting
  goroutines waiting on `configCommitMu`). Release, then assert the order of
  effects. Never wait for the competitor to return while a lock is still held.
- **Dual-mode release (review R3.7)**: when one test must be failing-first on
  the parent and must not deadlock on the PR, release the parked section as
  soon as the competitor has **either returned or been observed blocked**
  (`commitWaiters()` / `approvalLockWaiters(name)`), then await both and
  assert. T1.1 and T1.12 use this.
- For capture and dial sections: hold the section, start the commit, assert
  that the commit is waiting for that section (`Manager.CaptureReplacementQueued()`
  or `Gate.RetireWaiting()`), release the section, **then** wait for the commit
  and assert.

## Running

```bash
# unit + race, the PR's tests, repeated
go test -race -count=20 ./internal/runtime/... -run 'TestCommit|TestLockOrder'
GOMAXPROCS=1 go test -race -count=20 ./internal/runtime/... -run 'TestCommit'
# CI-equivalent lane (server tag, skip regex from unit-tests.yml)
go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" \
  ./internal/runtime/... ./internal/server/... ./internal/httpapi/... ./internal/storage/... ./internal/upstream/...
./scripts/check-upstream-write-sites.sh
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...
./scripts/test-api-e2e.sh
```

## T1: Commit section (PR1)

| ID | Park | Competing commit | Assert after release |
|---|---|---|---|
| T1.1 | stale-read: `ApplyConfig` returns; its downstream reconcile is parked at the `reconcileEntry` seam (`LoadConfiguredServers` entry on `main`; inside the apply commit after PR1, where the add of B then blocks on L0) | REST add B, approve all, block one tool, record one call | B row, approvals, block and history present. Rediscovery keeps the block. Same with an `ApplyConfig` that removes A in the parked commit: A gone, B intact |
| T1.2 | `ApplyConfig` (quarantine and disable A) at `afterStorage` | none (the stale-snapshot window no longer exists). Then toggle B | A quarantined and disabled in live, storage, disk and manager. B toggled. No async reconcile goroutine exists (assert with the goroutine-count probe) |
| T1.3 | REST PATCH `{args}` at `restPatchAfterParse` | MCP patch sets `trust_mode: manual`, removes the forwarding allowlist, disables, adds env K2. REST PUT adds header H2 | PATCH result has the new args **and** manual trust, no allowlist, disabled, K2, H2. Repeat with MCP update parked inside its callback (it must block on L0, not read early) |
| T1.4 | stale-read: reconcile caller parked at `beforeCommit` holding an explicit `quarantined:false` | `QuarantineServer(name,true)` | quarantined in all stores, tools absent from global and profile index, `SaveUpstreamKeepingQuarantine` never refuses |
| T1.5 | stale-read: `upstream_servers add` parked after the `Config()` read at `config_cow.go:93`, before `UpdateConfig` (after PR1 the seam sits before `Commit`) | `ApplyConfig` changing `profiles` | the profiles change survives. The add's server is present. Previously `config_cow.go:74` would publish a copy of the stale `Config()` |
| T1.6 | none | MCP `quarantine_security quarantine_server` on an indexed trusted server | `retrieve_tools` returns 0 hits for its tools immediately after the call returns (B1) |
| T1.7 | none | REST and MCP patches of a `Shared=true` + `auth_broker` server | both fields survive in live and on disk (r9-B #2) |
| T1.8 | n/a | static | `scripts/check-upstream-write-sites.sh` fails on a planted direct `SaveUpstreamServer` in `internal/server`, and passes on the PR |
| T1.10 | n/a (regression coverage; passes on `main`) | boot with an empty storage and manager; an unchanged `SaveConfiguration` | rows written, every enabled server scheduled, file written (FR-001a, R1.6) |
| T1.12 | discovery pass parked after `checkToolApprovals`, before its index write (`lifecycle.go:1028`) | `QuarantineServer(true)` returns | after release: no tool of the server is in the global or profile index (FR-008a, R2.2) |
| T1.11 | exclusion: removal parked at `afterStorage` | B toggle, REST update and MCP patch of the removed server, started in goroutines | all three are blocked (`commitWaiters()==3`). After release: the toggle succeeds, both updates get 404, the server never reappears (formerly T2.3, regression coverage) |
| T1.13 | n/a | a discovery pass whose upstream dropped a tool | completes without re-entering L1 (watchdog 5 s); the absent tool's record is removed (R3.1) |
| T1.14 | approval writer parked after acquiring L1 is impossible on the PR, so: commit parked at `afterStorage` (L1 released, before publish) | `ApproveTools`/tool toggle on that server under the new `trust_mode: manual` | the decision uses manual (from storage), not the still-published auto (R3.2). Parent: fails |
| T1.15 | discovery pass holding A's L1 with `quarantine_enabled=false` (parked inside, dual-mode) | global-only commit enabling quarantine | the commit is observed waiting on A's L1, or (parent) returns first. After release, a new tool is pending, not auto-approved (R4.2) |
| T1.17 | none | in **one** `Commit`: `RemoveServer("acme")` then `CreateServer(acme)`, once with a field-for-field **equal** entry and once with a changed `args` | both variants: `Changes["acme"].Kind == Replaced`, `New.Incarnation == G != Old.Incarnation`, the row and the snapshot carry the same new `ServerGen`, and a connect worker is spawned for `(acme, G)`. Parent (no replacement metadata in `Prepare`): the equal variant is "unchanged" with the old incarnation, and the changed variant is `Updated` with the old incarnation (review R8.2) |
| T1.16 | storage failure injected (`commitHooks.afterDisk` returns an error into `ApplyServerCommit`) during an unrelated server edit, after a pending restart-required `routing_mode` change was persisted | the unrelated edit | the edit fails. The file on disk is byte-identical to before the edit (the pending `routing_mode` is still there), `desiredCfg` and the pending-restart report are unchanged, and the watcher does not reload the compensation write as an external edit. Parent: compensating with `base` drops the pending value (review R5.5) |
| T1.18 | storage failure injected (`commitHooks.storageFault`) into the commit that `ReloadConfiguration` runs. Before it, the file containing servers A was edited externally to A+B | the reload | the reload fails. The file is byte-identical to the externally edited A+B input (same mode). Live config, storage and the manager still hold only A. The watcher does not re-run the reload (the restored bytes carry a self-write marker). Variants: (a) a pending restart-required `routing_mode` change exists (no-pending is the main case); (b) no config file existed before an `ApplyConfig` that would create it → after the failure there is still no file. On `main` the test fails because the reload publishes A+B to live although the storage write failed. Against the round-8 design (compensation from `pendingAwareDiskConfig(base)`) it fails because B is erased, or nothing is restored when nothing is pending (review R9.2) |
| T1.19 | none | connected HTTP server `fh` (a `httptest` upstream recording request headers) with `forward_headers: ["X-Tenant"]`. PATCH only `forward_headers: []`. Then PATCH only `expose_prompts` | after each commit returns: `manager.GetClient("fh").GetConfig()` is the new entry and `.Generation()` equals the row's `Generation`, with no reconnect (same `InstanceID` where PR3 is present, same client pointer on PR1). A tool call carrying `X-Tenant` reaches the upstream without it. A discovery pass started after the commit writes the index (D15 accepts it), and the prompt set follows `expose_prompts`. Regression coverage: passes on `main` (its reconcile calls `SetConfig`); with commit step 7 stubbed out it fails (header still forwarded, discovery dropped by D15) (review R9.1) |
| T1.20 | none | delete the `upstreams` row of an unchanged server `u` (stamps `{g0, i0}` in the snapshot), then `Commit("reconcile", ReconcileLive)` | the repaired row has `Generation == g0` and `Incarnation == i0`, not the commit's `G`, and `CheckCommittedView("u", snapshot.ServerGeneration("u"))` returns nil (PR2 also asserts `OAuthEpoch`). `ApplyServerCommit` called directly with a zero stamp or with `Stamp.Gen > Generation` returns an invariant error and writes nothing. On `main` it fails because rows carry no stamps. Against the round-8 design (batch-wide `G`) the row carries `G` and the check fails (review R9.3) |
| T1.21 | none | stop the runtime; start it again on the same data dir with an unchanged file. Rows and generations already match, the manager is empty | every enabled server gets a connect worker and reaches Ready, and each keeps the generation it had before the restart. Calling `Bootstrap()` from a non-boot commit returns an invariant error. Regression coverage: passes on `main`; with the boot call made without `Bootstrap()` it fails (no server scheduled, skip rule) (review R9.6) |
| T1.9 | n/a | restart | after a restart, `Snapshot.Version` is greater than or equal to the persisted `meta.config_commit_generation`, and generations of unchanged servers are kept |

## T2: Removal and purge (PR2)

Fixture: server `acme` with approved tool records, approved and changed prompt
records, an OAuth token and a DCR client under `acme_<16hex>`, an
`oauth_completion` entry, an integrity baseline, a clean scan report and job,
call history and index entries. A second server `acme_x` also has OAuth
tokens.

| ID | Scenario | Assert |
|---|---|---|
| T2.1 | remove through REST, through MCP, through `ApplyConfig` that drops it, through a file reload, and through `ApplyConfig` leaving an **empty** server set | for each path, every purge-set bucket has zero keys for `acme` when the call returns. `acme_x` untouched. Index has no `acme` docs. Refresh schedule is gone |
| T2.2 | re-add a different `acme` (manual trust) exposing identical tools and prompts | all tools and prompts pending and withheld. Unforced scanner approve fails "no scan results". Connect needs OAuth (no token used) |
| T2.4 | `ApproveTools(acme)` parked at `approvalHooks.afterCapture` | remove and re-add `acme`, then release: 409 `server_replaced`, no records written. A variant with an unrelated `disabled_tools` edit instead of the re-add: the approval succeeds |
| T2.5 | scanner `ApproveServerWithBlocks(acme)` parked **after its approval-lock (L1) section that saved the baseline and blocks has exited**, and before `Commit(ExpectIncarnation, SetQuarantined(false))` (`approvalHooks.afterBaselineBeforeUnquarantine`). Parking inside the L1 section would deadlock the recipe, because removal needs the same lock (review R5.6) | remove and re-add `acme` while the approval is parked: both **return** (assert `approvalLockWaiters("acme") == 0` and that they complete under a 5 s watchdog). Then release: the approval fails with `server_replaced`, the replacement stays quarantined, and it inherits no baseline or blocks (the removal purge deleted them, and the replacement's rows are empty) |
| T2.6 | `foo` and `foo_bar` both with tokens; remove `foo` | `foo_bar`'s token readable (B2) |
| T2.8 | scanner job for old `acme` parked before its report callback | remove and re-add `acme`, then release: no job or report for the new incarnation; unforced approve refused (R1.1) |
| T2.9 | `SaveToken` parked after its context check (code exchange) | remove and re-add `acme` (same URL), then release: no token stored; the old schedule dropped (R1.2) |
| T2.10 | none | remove `acme` and re-add it immediately; make its first discovery fail | the sweep fallback does not reapply the old `lastGoodTools` (R2.5) |
| T2.11 | completion event for old `acme` fetched, consumer parked | remove + re-add, release | no retry against the replacement; completion record purged (R3.5) |
| T2.12 | worker parked before `RegisterServerIdentity`; call parked before history write | remove `acme`, release | no identity or history bucket for `acme` (R3.6) |
| T2.13 | the old instance's `SaveToken` and DCR `SaveOAuthClient` parked after the network response, before persisting | change `acme`'s OAuth config (scopes or client id), keeping name and URL; the commit returns; release | no token and no DCR client for `acme` in storage; the new instance starts a fresh OAuth flow and does not load the old records; the old refresh schedule is dropped. Parent (incarnation-only check): the cleared records are restored (review R5.3) |
| T2.14 | a commit that changes `acme`'s OAuth config (same name and URL) parked at `commitHooks.afterStorage` (row has the new epoch; step 7 has not run). An old-generation worker for `acme`, holding the previous `ServerGen`, released at `oauthHooks.beforeBindingCheck`. Variant: remove and re-add `acme` (same URL) parked at the same point | the worker returns `ErrStaleGeneration` and builds no instance and no OAuth config. No token, DCR client or completion is written under the new binding, and the records the commit cleared stay absent. After the commit is released, the new worker connects with the new config. Parent (binding read from the row): the old-config instance saves a token that passes `SaveOAuthTokenIfBound` (review R6.1) |
| T2.15 | (a) the old instance's 5xx-recovery `ClearToken` and (b) its config-build DCR clear (`clearOAuthCredentials`, stale redirect URI case), each parked at `oauthHooks.beforeClear`, before its storage transaction. Then change `acme`'s OAuth config (same name and URL), let the replacement save a fresh token and DCR client, and release. Variant: `RestartServer(acme)` (same binding) instead of the config change. Variant (review R7.3): same binding, and the replacement rewrites the record keeping the **same access token** (for the 5xx clear) or the **same DCR client ID** with a new callback port and redirect URI (for the DCR clear) | the replacement's token and DCR registration are byte-for-byte unchanged, including `ClientSecret`, `CallbackPort` and `RedirectURI`. The old clears return `false` with a debug log. In both same-binding variants the revision check rejects the clear. Parent (unconditional delete): both are erased. Parent (round-6 access-token / client-ID compare): the R7.3 variant is erased (reviews R6.2, R7.3) |
| T2.16 | an old-binding caller (`PersistentTokenStore.GetToken` refresh, and separately `RefreshOAuthTokenDirect`) parked at `oauthHooks.beforeRefreshDo` after its bound read. Reverse variant: a flight started under the old binding, held on the fake token endpoint | change `acme`'s OAuth config (same name and URL) and start the replacement's refresh under the new binding; release. Repeat with remove and re-add (same URL) instead | the old caller does not receive the replacement's token (it gets `ErrNoRefreshToken` from its own bound load, with no network request). In the reverse variant the replacement does not inherit the old flight's failure, latch or cooldown, and `RefreshManager` ignores the old outcome. Parent (flights keyed by `GenerateServerKey` only): the old caller joins the replacement's flight and gets its token (review R7.1) |
| T2.17 | `ApproveTools(acme)` parked at `approvalHooks.afterCapture`, and an old-binding `SaveOAuthTokenIfBound` parked at `oauthHooks.beforePersist` | in **one** commit, `RemoveServer("acme")` then `CreateServer(acme)` (equal entry; then a changed entry); release | `PurgeReport["acme"]` is present (approvals, tokens and baselines deleted). The row has `OAuthEpoch == Incarnation == G`. The approval returns 409 `server_replaced` and writes nothing. The OAuth save returns `false` and stores nothing. The replacement has no approvals or tokens (review R8.2) |
| T2.18 | none | standalone `mcpproxy auth logout --server acme` (no daemon) against a `config.db` holding tokens for `acme`, `acme_bar` and `other` | exit 0, the `acme` record at `GenerateServerKey` is gone, and the `acme_bar` and `other` records are byte-identical. `go build ./cmd/mcpproxy` and `go build -tags server -o /dev/null ./cmd/mcpproxy` succeed. `check-upstream-write-sites.sh` passes, and its self-test fails on a planted `ClearOAuthTokenForStandaloneLogout` call in `internal/upstream/core` (review R8.3) |
| T2.19 | the real `QuarantineServer("acme", false)` parked at `approvalHooks.afterUnquarantineCommitBeforeBaseline`, after its commit returned for incarnation I1 | remove `acme` and re-add it under manual trust (incarnation I2); let discovery record pending tool records for I2; release | the replacement's tool records are all still `pending`; no `system:server-approval-baseline` approval and no `tools.approved` notification is recorded for I2; the parked call logs `ErrServerReplaced` at debug level. Parent (the helper recaptures the incarnation by name): I2's pending tools are promoted (review R9.5) |
| T2.20 | none | seed `oauth_tokens[acme_<hex>]` with an access token, a refresh token, an expiry, `OAuthEpoch = e` and DCR fields (client id, secret, callback port, redirect URI) at revision `r`. Call `ClearOAuthClientIfBound` with the matching binding and `observedRev = r`; then repeat with a wrong binding, a wrong record epoch and a stale revision on a fresh seed | matching call: returns `true`; the token fields, expiry and record epoch are unchanged; the four DCR fields are empty; `Revision > r`. Mismatched calls: return `false` and the record is byte-identical. An absent record returns `false`. Parent (the spec's former "delete the record"): the token is gone (review R9.4) |
| T2.7 | discovery pass for old `acme` parked at `beforeApprovalLock` | remove and re-add, then release: no records with the old incarnation are written |

## T3: Generation-bound workers (PR3)

| ID | Park | Competing commit | Assert |
|---|---|---|---|
| T3.1 | the enabled worker for `(s, g1)` at `beforeWorkerAct` | (a) remove s; (b) re-add s with another command; (c) quarantine s | (a) no client for s. (b) the manager client config is the new command. (c) the client config is quarantined. All return `ErrStaleGeneration` from the parked worker |
| T3.2 | the disabled worker for `(s, g1)` at `beforeWorkerAct` | enable s (g2); its worker connects | the new client is still registered and connected after the old worker is released |
| T3.3 | supervisor `ActionConnect` and `ActionReconnect` at `beforeExecuteAction` | remove, replace or quarantine | the action is a no-op. The manager holds only the latest generation |
| T3.4 | `RestartServer(s)` worker parked | remove s | no client for s |
| T3.5 | (a) capture parked **before** `WithCurrentClientOnEpoch`; (b) capture parked inside it | (a) replace the endpoint, commit returns, release capture; (b) start the commit, assert `CaptureReplacementQueued()`, release capture, await commit | (a) the capture finds the instance unregistered and persists nothing; (b) the capture completed before the replacement (the old definitions are recorded under the old incarnation and then superseded). No `review.changed` for old definitions after the commit |
| T3.7 | approval reindex with a cached snapshot stamped with an old `Gen` | the endpoint changes | reapply rejected; re-list instead (FR-021b) |
| T3.8 | `ForceReconnectAll` parked after capturing clients | remove A; replace B | A not re-created, B's replacement not removed (R4.1) |
| T3.9 | a real `ActionDisconnect` (and the disconnect leg of `ActionReconnect`) captured for instance I1 at `beforeExecuteAction` | `RestartServer(s)` without a config change installs I2 at the **same** generation; I2 connects | release: `DisconnectServerAt` returns `ErrStaleGeneration` (InstanceID mismatch), I2 stays connected. Parent (generation-only check): I2 is disconnected (review R5.2) |
| T3.10 | none | one-commit remove and re-create of a connected `acme` (equal entry) | `AdvanceGenerations` unregistered the old instance: the `InstanceID` changed, `desired["acme"] == {G, present}`, and the new instance connects. With PR4, the old instance's gate is `Retired()` before the commit publishes. Parent (equal entry treated as unchanged): the old instance stays registered (review R8.2) |
| T3.6 | old `ActionDisconnect` at `beforeExecuteAction` | the newer commit installs and connects the replacement | release: `DisconnectServerAt` returns `ErrStaleGeneration`, the replacement stays connected (R1.5) |

## T4: Retirement barrier (PR4)

Fixtures (test-only):
- **Marker launcher**: a stdio command `sh -c 'touch "$MARK"; exec <fixture>'`
  (on Windows, a Go helper binary that writes the marker).
- **Counting HTTP upstream**: an `httptest.Server` that counts requests,
  `Authorization` headers and bodies on `/mcp`, `/sse`, `/.well-known/*`,
  `/register` and `/token`.
- **Flaky conn**: a `net.Listener` wrapper that closes the first reused
  connection mid-request, which makes `net/http` retry.

| ID | Park | Competing | Assert (counter or marker read **after** the competing commit returned) |
|---|---|---|---|
| T4.1 | stdio connect at `dialgate.beforeAdmit` | remove the server | no marker. Connect returns `ErrRetired`. Same for Docker isolation (mock docker binary) and the launcher |
| T4.2 | HTTP headers-auth strategy at `beforeAdmit` (before the first request; an admitted section is covered by the exclusion variant: hold it, assert `RetireWaiting()`, release, await) | remove; replace endpoint; quarantine (three subtests, HTTP and SSE) | the old server's counters do not change. Zero `Authorization` headers after the commit |
| T4.3 | the retry attempt (flaky conn), parked in the dialer for the retry | retire | no retry reaches the server. `Retire` returns within 1 s |
| T4.4 | OAuth token request (code exchange) and refresh, parked before send | retire | `/token` receives no client secret and no refresh token after retirement. `Manager.RefreshOAuthToken` on the retired name returns "server not found" |
| T4.5 | quarantined inspection client mid-`tools/list` | replace the endpoint | (as T3.5) plus the old instance's subsequent `Write` returns `ErrRetired` |
| T4.6 | a tool call in flight on an instance being retired | retire | the call fails as unavailable. The replacement instance's health is unaffected |
| T4.8 | `SendRequest` on a launched stdio instance, parked before the gate | retire | `ErrRetired`, nothing on the child's stdin, process group dead when `Retire` returns (R1.3) |
| T4.9 | `preflightPRMScopes`, `autoDetectResource`, `httpFetchRaw` parked before send | retire | zero hits (R1.4) |
| T4.10 | an admitted HTTP request about to enter its nested dial and write | `Retire` | no deadlock: `Retire` returns ≤1 s, the nested dial/write gets `ErrRetired` (R2.3) |
| T4.11 | stdio launch parked between `cmd.Start` and group/Job registration (inside the admitted section) | `Retire` (asserts `RetireWaiting()`), release | the child is killed before `Retire` returns; on Windows, a failed Job assignment kills the child and Start fails (R2.4) |
| T4.12 | Windows-only: a child that spawns a grandchild immediately | `Retire` | the grandchild is in the Job and dead when `Retire` returns (R3.3) |
| T4.13 | `openBrowser` parked before `Start` | `Retire` | no browser process; a code delivered to the callback afterwards is not exchanged and no token is stored (R3.4) |
| T4.14 | launcher `WaitForURL` parked after its cancellation check; Docker diagnostic exec parked | `Retire` | no dial, no diagnostic exec; teardown `docker stop` still runs (R4.4) |
| T4.15 | the old instance parked (a) at `dialgate.beforeAdmit` before its stdio launch (marker launcher) and (b) before its HTTP send and before its OAuth token send (counting fixture) | `RestartServer`, `ForceReconnectAll` and Docker recovery each replace it at the **same** config generation | release: no marker from the old instance, zero requests and zero `Authorization` headers from it; the replacement connects and its counters are unaffected (review R5.4) |
| T4.16 | an admitted dial parked after the TCP connect but before `track` | `Retire` runs its first sweep (`afterSweepBeforeBarrier` seam), then release the dial and start a blocking `Write` against a peer that never reads | `track` closes the conn and the dial returns `ErrRetired`. In the variant that bypasses `track`'s latch check (to prove the second sweep), the post-barrier sweep closes the conn and the blocked `Write` returns. Either way `Retire` returns within 1 s and no byte reaches the peer after the latch (review R5.1) |
| T4.17 | none (production wrapped transports, built through `transport.CreateHTTPClient` and the stdio branch) | — | (a) `extractOAuthHandler` returns the handler for wrapped OAuth Streamable HTTP and SSE, and `bindOAuthRefresher` binds it. (b) A concurrent reactive refresh (token store `GetToken`) and proactive `RefreshOAuthTokenDirect` over the wrapped transport cause exactly one token request (Spec 113 FR-001). (c) Manual login against the GH #1271 per-method-auth fixture (anonymous `initialize`, 401 only on `tools/call`) returns an authorization URL. (d) A legacy Streamable HTTP fixture that answers 404 for the session: `SessionSnapshot` reports the id before the 404, `ReinitializeSession` gets a new id, and the retried call succeeds. (e) The `Mcp-Protocol-Version` header is present after negotiation, a server-to-client `ping` over wrapped stdio and wrapped Streamable HTTP gets mcp-go's ping response (the request handler is installed through the forwarded `BidirectionalInterface`), and SSE connection loss fires the `OnConnectionLost` handler. (f) After `Retire`, an incoming server `ping` gets a JSON-RPC error and the client handler is not called. Parent (embed-only wrapper): (a)-(e) fail (review R8.1) |
| T4.7 | benchmark | — | `BenchmarkGatedConnWrite` shows < 5% overhead against an ungated conn on a 1 KiB write |

## Live checks (qa070 harness)

Setup (each PR, from its worktree):

```bash
go build -o /private/tmp/claude-501/qa070/bin-s116-prN/mcpproxy ./cmd/mcpproxy
cp /private/tmp/claude-501/qa070/start.sh /private/tmp/claude-501/qa070/runs/   # adapt profile if needed
/private/tmp/claude-501/qa070/start.sh s116-prN <free-port> /private/tmp/claude-501/qa070/bin-s116-prN/mcpproxy big
source /private/tmp/claude-501/qa070/runs/s116-prN/env.sh
MCP="python3 /private/tmp/claude-501/qa070/mcp.py $QA_BASE/mcp?apikey=$QA_KEY"
CLI="env HOME=$QA_RUN/home MCPPROXY_TELEMETRY=false $QA_BIN"
# ... checks below ...
/private/tmp/claude-501/qa070/stop.sh s116-prN
```

Never use port 8080, `~/.mcpproxy`, the installed app or real client configs.
Check the port is free with `lsof -nP -iTCP:<port> -sTCP:LISTEN` first,
because other sessions share the harness. Evidence goes to
`docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s116-pr<N>/EVIDENCE.md`
(not committed).

### L1 (PR1)

1. **B1**: `retrieve_tools "read customer records pagination"` → hits on
   `trusted-library`. Then `quarantine_security quarantine_server
   trusted-library`. Then `retrieve_tools` again → **0** hits from it, at once.
   On `main` this returned the same hits after 35 s (baseline evidence B1).
2. **Concurrent writers**: in parallel, run 20 iterations of (a) `PATCH
   /api/v1/servers/team-03-customer-data {"args":[...]}` (b) `upstream_servers
   patch team-03-customer-data trust_mode=manual` (c) `PUT /api/v1/config` with
   a profiles edit (d) `touch $QA_CFG` (watcher reload). Then check that
   `GET /api/v1/servers/team-03-customer-data`, `$QA_CFG` and `$CLI upstream
   list -o json` agree, and the last writer's values for every field are
   present.
3. **Remove/add race**: 50 iterations of `DELETE` then immediate `POST` of
   `team-04-customer-data`, interleaved with `PATCH` of `team-05`. Every
   `DELETE` 200 is followed by a successful `POST` 201. The final state has
   exactly one `team-04` (live, disk, storage via the CLI).
4. **Retained-instance update (review R9.1)**: add `hdr-echo`, an HTTP
   fixture that logs the headers it receives (PR1 adds
   `header_echo_fixture.py` next to `fixture.py`), with `forward_headers:
   ["X-Tenant"]`, and call one of its tools through the MCP client with
   `X-Tenant: t1`: the fixture log shows `X-Tenant`. PATCH only
   `forward_headers: []`, call again at once: the log shows no `X-Tenant`,
   and `upstream logs hdr-echo` shows no reconnect. The REST `generation`
   field is PR2 (FR-017, [contracts/surfaces.md](contracts/surfaces.md)), so
   PR1 reads generations from storage: the `servers.changed` SSE event of the
   PATCH carries the commit's `generation`, and after `stop.sh s116-pr1` the
   `hdr-echo` row in `config.db` (read-only bbolt dump of the `upstreams`
   bucket) carries that value as its `generation`. A recipe that reads
   `generation` from `GET /api/v1/servers` in PR1 compares two missing values
   and must be treated as a failed check, not a pass. Unit coverage:
   `TestCommit_R1_ConnectedRetainedInstanceGetsNewConfig`.
5. **Restart with matching rows (review R9.6)**: stop the daemon with
   `stop.sh s116-pr1` and dump every row's `generation` and `incarnation`
   from `config.db` (read-only). Start it again on the same run directory,
   wait for Ready, check every enabled server is connected, stop it again and
   dump the rows: every stamp equals the value before the restart, and
   `meta.config_commit_generation` did not decrease. (From PR2 the same check
   can read `generation` from `GET /api/v1/servers` while the daemon runs.)

### L2 (PR2)

1. Add `acme` (stdio fixture, 12 tools), approve it, record a call. Seed OAuth
   by adding `acme-oauth` against a local fake OAuth server
   (`/private/tmp/claude-501/qa070/` fixture, PR2 adds `oauth_fixture.py`) and
   completing the flow with the headless helper.
2. Remove `acme` through REST. Re-add it under manual trust: `GET
   .../tools` shows every tool pending. Repeat with MCP remove and with a
   config-file edit (watcher).
3. `foo`/`foo_bar` OAuth: remove `foo` and confirm `foo_bar` stays connected
   with no re-auth prompt.
4. Stop the daemon and inspect `config.db` with the PR2 debug command
   `mcpproxy debug purge-report --data-dir $QA_DATA acme` (test-only build
   tag): zero keys in every purge bucket.
5. OAuth binding (review R6.1, R6.2): with `acme-oauth` signed in, make the
   fake OAuth server answer the next MCP `initialize` with 500 so the old
   instance enters 5xx recovery, and in the same second change
   `acme-oauth`'s `oauth.scopes` through REST PATCH. Complete the new login.
   `mcpproxy auth status --server acme-oauth` shows the new token, and the
   purge-report command shows one token record whose `oauth_epoch` equals the
   row's. The main log has a debug `oauth clear skipped: binding mismatch`
   line and no "OAuth token cleared" line after the new save. This is a
   best-effort live race. The deterministic proof is T2.14-T2.16.

### L3 (PR3)

Built with `-tags qa_hooks`, which exposes `MCPPROXY_QA_PARK_WORKER=<server>`
so the named server's post-commit worker blocks until
`$QA_RUN/release-<server>` appears.
1. Park `trusted-library`'s worker (enable toggle). Remove the server. Release.
   `upstream list` has no `trusted-library`, and no process with its fixture
   args is running (`pgrep -f`).
2. Park, re-add with another tool count (fixture arg), release. `tool_count`
   matches the **new** arg.
3. **Same-generation restart**: note the fixture PID of `trusted-library`,
   run `$CLI upstream restart trusted-library`, and check that the old PID is
   gone and that exactly one fixture process with its args remains. Then, with
   `MCPPROXY_QA_PARK_SUPERVISOR_DISCONNECT=trusted-library`, park a supervisor
   disconnect, restart again, release: `upstream list` shows it connected
   (review R5.2).

### L4 (PR4)

1. **stdio marker**: add `marker-srv` whose command touches
   `$QA_RUN/marker-<n>` per launch. With `qa_hooks`, park its dial
   (`MCPPROXY_QA_PARK_DIAL=marker-srv`), remove it, release. The marker count
   does not increase.
2. **HTTP counter**: run `python3 counting_http_fixture.py <port>` (PR4 adds it
   next to `fixture.py`; it logs every request with a header digest). Add
   `count-srv` with `headers: {"Authorization": "Bearer qa-secret"}`. Park its
   dial, replace its URL with another port, release. The old port's log has no
   request after the commit timestamp.
3. **OAuth**: point the fake OAuth server's `/token` at the counter. Park the
   refresh (`MCPPROXY_QA_PARK_OAUTH=…`), remove the server, release. No
   `refresh_token` reaches `/token`.
