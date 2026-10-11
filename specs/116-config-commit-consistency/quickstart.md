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
| T1.1 | boot/reload reconcile at `afterBuild` holding the pre-B config | REST add B, approve all, block one tool, record one call | B row, approvals, block and history present. Rediscovery keeps the block. Same with an `ApplyConfig` that removes A in the parked commit: A gone, B intact |
| T1.2 | `ApplyConfig` (quarantine and disable A) at `afterStorage` | none (the stale-snapshot window no longer exists). Then toggle B | A quarantined and disabled in live, storage, disk and manager. B toggled. No async reconcile goroutine exists (assert with the goroutine-count probe) |
| T1.3 | REST PATCH `{args}` at `restPatchAfterParse` | MCP patch sets `trust_mode: manual`, removes the forwarding allowlist, disables, adds env K2. REST PUT adds header H2 | PATCH result has the new args **and** manual trust, no allowlist, disabled, K2, H2. Repeat with MCP update parked inside its callback (it must block on L0, not read early) |
| T1.4 | reconcile holding an explicit `quarantined:false` at `afterBuild` | `QuarantineServer(name,true)` | quarantined in all stores, tools absent from global and profile index, `SaveUpstreamKeepingQuarantine` never refuses |
| T1.5 | `upstream_servers add` at `afterBuild` | `ApplyConfig` changing `profiles` | the profiles change survives. The add's server is present. Previously `config_cow.go:74` would publish a copy of the stale `Config()` |
| T1.6 | none | MCP `quarantine_security quarantine_server` on an indexed trusted server | `retrieve_tools` returns 0 hits for its tools immediately after the call returns (B1) |
| T1.7 | none | REST and MCP patches of a `Shared=true` + `auth_broker` server | both fields survive in live and on disk (r9-B #2) |
| T1.8 | n/a | static | `scripts/check-upstream-write-sites.sh` fails on a planted direct `SaveUpstreamServer` in `internal/server`, and passes on the PR |
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
| T2.3 | park the removal commit at `afterStorage`, then attempt a B toggle, a REST update of `acme`, an MCP patch of `acme` | the toggle and both updates block on L0, then get 404 for `acme`. `acme` never reappears in live, disk or storage |
| T2.4 | `ApproveTools(acme)` parked at `approvalHooks.afterCapture` | remove and re-add `acme`, then release: 409 `server_replaced`, no records written. A variant with an unrelated `disabled_tools` edit instead of the re-add: the approval succeeds |
| T2.5 | scanner `ApproveServerWithBlocks(acme)` parked after baseline save | remove and re-add `acme`, then release: the replacement stays quarantined, its baseline and blocks are absent |
| T2.6 | `foo` and `foo_bar` both with tokens; remove `foo` | `foo_bar`'s token readable (B2) |
| T2.7 | discovery pass for old `acme` parked inside `checkToolApprovals` before writing | remove and re-add, then release: no records with the old incarnation are written |

## T3: Generation-bound workers (PR3)

| ID | Park | Competing commit | Assert |
|---|---|---|---|
| T3.1 | the enabled worker for `(s, g1)` at `beforeWorkerAct` | (a) remove s; (b) re-add s with another command; (c) quarantine s | (a) no client for s. (b) the manager client config is the new command. (c) the client config is quarantined. All return `ErrStaleGeneration` from the parked worker |
| T3.2 | the disabled worker for `(s, g1)` at `beforeWorkerAct` | enable s (g2); its worker connects | the new client is still registered and connected after the old worker is released |
| T3.3 | supervisor `ActionConnect` and `ActionReconnect` at `beforeExecuteAction` | remove, replace or quarantine | the action is a no-op. The manager holds only the latest generation |
| T3.4 | `RestartServer(s)` worker parked | remove s | no client for s |
| T3.5 | quarantined capture parked at `quarantinedCaptureDuringPersist` | replace the endpoint (commit returns) | the commit's step 6 waits for the capture section (L3 W), **or** the capture sees the instance unregistered and persists nothing. No `review.changed` for the old definitions |

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
| T4.2 | HTTP headers-auth strategy at `afterAdmitBeforeAct` (before the first request) | remove; replace endpoint; quarantine (three subtests, HTTP and SSE) | the old server's counters do not change. Zero `Authorization` headers after the commit |
| T4.3 | the retry attempt (flaky conn), parked in the dialer for the retry | retire | no retry reaches the server. `Retire` returns within 1 s |
| T4.4 | OAuth token request (code exchange) and refresh, parked before send | retire | `/token` receives no client secret and no refresh token after retirement. `Manager.RefreshOAuthToken` on the retired name returns "server not found" |
| T4.5 | quarantined inspection client mid-`tools/list` | replace the endpoint | (as T3.5) plus the old instance's subsequent `Write` returns `ErrRetired` |
| T4.6 | a tool call in flight on an instance being retired | retire | the call fails as unavailable. The replacement instance's health is unaffected |
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

### L3 (PR3)

Built with `-tags qa_hooks`, which exposes `MCPPROXY_QA_PARK_WORKER=<server>`
so the named server's post-commit worker blocks until
`$QA_RUN/release-<server>` appears.
1. Park `trusted-library`'s worker (enable toggle). Remove the server. Release.
   `upstream list` has no `trusted-library`, and no process with its fixture
   args is running (`pgrep -f`).
2. Park, re-add with another tool count (fixture arg), release. `tool_count`
   matches the **new** arg.

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
