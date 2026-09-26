# Quickstart: verifying a Spec 109 PR

Applies to every PR from 109-a to 109-m. Run each PR from its own worktree, branched from `origin/main`, or from the previous letter's branch while that branch is unmerged. **Never** touch the user's real `~/.mcpproxy`, the real client configs, or a running tray/core.

## 1. Common gates (every PR)

```bash
# Red first: the PR's new tests fail on the merge base, then pass on the branch
go test -race -count=1 -run '<PR test names>' ./internal/...

# Go suites (the server package needs the CI skip regex, or it hangs until the 7m panic)
go test -race -count=1 ./internal/health/... ./internal/contracts/... ./internal/runtime/... ./internal/connect/... ./internal/configimport/... ./internal/registries/... ./internal/httpapi/... ./internal/storage/...
go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...
go test -race -tags server -timeout 20m -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/serveredition/... ./internal/config/... ./internal/server/... ./internal/httpapi/... ./internal/storage/...
go build -tags server -o /dev/null ./cmd/mcpproxy          # never bare: it clobbers ./mcpproxy
go test -count=1 ./cmd/mcpproxy/...                         # CLI goldens

# Frozen MCP goldens: only 109-j may change search_servers/list_registries schema text
git diff --stat origin/main -- internal/server/testdata/

# Generated types and swagger in sync
go run ./cmd/generate-types && git diff --exit-code frontend/src/types/contracts.ts
make swagger-verify

# Lint exactly like CI (both runs)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...

# API E2E (required). First check that no other session's core is running: the script pkills them.
# Until T011a (109-a) lands, the script's cleanup runs `pkill -f "mcpproxy.*serve"`, which kills EVERY core on
# the machine (the user's tray core, other worktrees' instances). Run it only when the precheck prints nothing.
if pgrep -f 'mcpproxy.*serve' >/dev/null; then echo 'another mcpproxy core is running: skip the E2E gate for now'; else LISTEN_PORT=18$((RANDOM%900+100)) ./scripts/test-api-e2e.sh; fi
# From 109-a on (T011a) the script stops only the PIDs it started, and this precheck is dropped.
```

Frontend PRs (a, b, c, d, e, f, g, h, i, j, k, l, m):

```bash
cd frontend && npm ci && npx vitest run tests/unit/ && npm run build && cd ..
git checkout -- frontend/package-lock.json 2>/dev/null   # npm ci in a worktree may rewrite it
make build && MCPPROXY_BINARY_PATH=$PWD/mcpproxy ./scripts/run-web-smoke.sh   # web-ui-sweep + visual-a11y-sweep (+ navigation-consistency.spec.ts once 109-e's T068 adds it to the script's Playwright list; 109-i extends it)
```

macOS PRs (a, b, c, d, e, f, g, h, i, j, k, l, m):

```bash
cd native/macos/MCPProxy && swift test --filter '<PR test classes>' && cd -
# Build and swap the binary per docs/development/macos-tray.md, then verify with the mcpproxy-ui-test MCP:
#   list_menu_items / click_menu_item (tray), screenshot_window (views), read_status_bar
# Point the dev app at the §2 scratch core, never at the user's running core.
```

Cross-model review (per the maintainer's reviewer ladder): `zcode` is the default; use `codex` only for 109-f (security semantics of approval). At most 10 rounds per PR. Split briefs into chunks and close stdin:

```bash
mkdir -p .review-tmp && git diff origin/main...HEAD > .review-tmp/pr.diff
gtimeout 900 zcode --prompt "Review .review-tmp/pr.diff against specs/109-ux-navigation-consistency/spec.md FR-<ids> and contracts/<file>. Verify claims against file:line. End with VERDICT: CLEAN or VERDICT: FINDINGS." --mode plan --no-color --cwd "$PWD" < /dev/null > .review-tmp/zcode-r1.txt
grep "VERDICT:" .review-tmp/zcode-r1.txt   # no verdict line = UNREVIEWED, not clean
```

## 2. Isolated live instance (every PR that changes runtime behaviour)

```bash
RUN=$(mktemp -d /tmp/mcpp109.XXXX); PORT=18$((RANDOM%900+100))
mkdir -p $RUN/home/.cursor $RUN/home/.codex $RUN/home/.claude
echo '{"mcpServers":{}}' > $RUN/home/.cursor/mcp.json; touch $RUN/home/.codex/config.toml
FX=$PWD/internal/server/testdata/preflight_fixture_server.js
TD=$RUN/tools; mkdir -p $TD
# filesystem-like fixture: 14 tools (9 read, 3 write, 2 destructive); one unannotated tool in "notes"
node -e 'const t=[];for(let i=0;i<9;i++)t.push({name:"read_"+i,description:"Read thing "+i,inputSchema:{type:"object"},annotations:{readOnlyHint:true}});for(let i=0;i<3;i++)t.push({name:"write_"+i,description:"Write thing "+i,inputSchema:{type:"object"},annotations:{readOnlyHint:false}});for(let i=0;i<2;i++)t.push({name:"delete_"+i,description:"Delete thing "+i,inputSchema:{type:"object"},annotations:{destructiveHint:true}});require("fs").writeFileSync(process.argv[1],JSON.stringify(t))' $TD/fs.json
echo '[{"name":"search_notes","description":"Search notes","inputSchema":{"type":"object"}}]' > $TD/notes.json
cat > $RUN/mcp_config.json <<JSON
{ "listen": "127.0.0.1:$PORT", "data_dir": "$RUN/data", "quarantine_enabled": true,
  "mcpServers": [
    {"name":"filesystem","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/fs.json","FIXTURE_CALL_LOG":"$TD/fs-calls.log"},"protocol":"stdio","enabled":true,"quarantined":true},
    {"name":"notes","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/notes.json"},"protocol":"stdio","enabled":true},
    {"name":"needs-secret","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/notes.json","API_TOKEN":"\${keyring:missing_token}"},"protocol":"stdio","enabled":true},
    {"name":"scratch","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/notes.json"},"protocol":"stdio","enabled":false} ] }
JSON
go build -o $RUN/mcpproxy ./cmd/mcpproxy
# Start as its OWN background call (run_in_background), never with `&` inside a compound call:
HOME=$RUN/home $RUN/mcpproxy serve --config $RUN/mcp_config.json --data-dir $RUN/data --log-level=debug
# Every CLI call in §3 goes through this function, so no recipe can fall through to the PATH binary,
# the real ~/.mcpproxy config or a running core (line 3 of this file):
mp() { HOME=$RUN/home $RUN/mcpproxy --config $RUN/mcp_config.json --data-dir $RUN/data "$@"; }
# Readiness: poll, don't sleep
KEY=$(jq -r .api_key $RUN/mcp_config.json); M=http://127.0.0.1:$PORT
until curl -sf -H "X-API-Key: $KEY" $M/api/v1/status >/dev/null; do sleep 1; done
# OAuth-required server (for sign-in items): the in-repo test server
go run ./tests/oauthserver/cmd/server -port $((PORT+1))    # own background call; then:
curl -s -H "X-API-Key: $KEY" -X POST $M/api/v1/servers -d "{\"name\":\"github\",\"url\":\"http://127.0.0.1:$((PORT+1))/mcp\",\"protocol\":\"http\",\"enabled\":true}"
# Changed tool (rug pull): notes is trusted, so its first toolset is auto-approved as a baseline (MCP-2931).
# Check: mp tools list --server notes -o json (approval_status approved); if not, mp upstream approve notes.
# Then edit $TD/notes.json's description; the fixture re-reads it on the next tools/list and the tool becomes `changed`.
# Stop only yours:
pkill -f "mcpproxy serve --config $RUN"; pkill -f "oauthserver.*-port $((PORT+1))"
```

Client presence: run `mp connect cursor` (socket, scratch HOME, scratch config) and open an MCP session with `clientInfo.name` set to Cursor's alias through a JSON-RPC `initialize` curl, or with `bench/mcpcaller.go`. Codex stays "connected, never seen" once `connect codex` has run and 5 minutes have passed. For the recipe, set the test-only env `MCPPROXY_ATTENTION_NEVER_SEEN_AFTER=5s` (a test hook, documented in 109-d).

Docker isolation does not matter for `node` fixtures as long as `docker_isolation` is absent from the scratch config.

## 3. Live-verification recipes per PR

Every REST call below carries the admin key unless it names another credential: write `curl -H "X-API-Key: $KEY" $M/api/v1/...`. REST refuses any unauthenticated TCP request with `401` (`internal/httpapi/server.go`: the API key is required for every TCP connection), so a bare `curl $M/api/...` verifies nothing (codex round 4). `FIXTURE_CALL_LOG` (T078c, from 109-f) makes the `filesystem` fixture append one line per `tools/call` it receives to `$TD/fs-calls.log`.

| PR | Recipe (against the §2 instance) | Pass condition |
|---|---|---|
| 109-a | Open `$M/ui/?apikey=$KEY` in the in-app browser; open Add Server, then check `elementFromPoint` at the sidebar version row; Settings; `/servers/filesystem?tab=security` → click Tools; Tools approval filter, then its "Needs review" stat; open `/review/filesystem`; `mp tools list --tier unannotated -o json` | `/` shows Overview; modal on top; "Settings" everywhere, no Server Edition tab, no emoji tabs; URL `?tab=tools`; filter shows exactly 3 approval values; `search_notes` listed as `unannotated` on Web, CLI and macOS Tools; header has no "Profile:" with no profiles; the "Needs review" stat lands on `/servers` (interim redirect, not the 404 page) and `/review/filesystem` on `/servers/filesystem?tab=tools` |
| 109-b | Walk the wizard with the scratch HOME holding a Claude Code config with two servers | import rows show command/URL + tags; client rows show `~/…`; Servers step stays incomplete while everything is quarantined; Verify shows the reload hint and no prompt for unusable servers; no telemetry banner behind the wizard; `mp connect cursor` prints `Next: …` |
| 109-c | `curl -H "X-API-Key: $KEY" $M/api/v1/servers` → `health`; Web cards and detail; `mp upstream list`; macOS Servers + tray | each server's `status/usable/actions` match health-vocabulary.md; no "healthy" shown for filesystem/scratch/github; `upstream list --status needs_review` lists only filesystem |
| 109-d | `curl -H "X-API-Key: $KEY" $M/api/v1/attention`; Web Home, pill and badge; macOS tray + Home; `mp attention`, `status`, `doctor`; `curl -H "X-API-Key: $AGENT_TOKEN" $M/api/v1/attention` with an agent token scoped to `filesystem` (`AGENT_TOKEN=$(mp token create --name qa109 --servers filesystem --permissions read -o json | jq -r .token)`); then sign in via the OAuth test server | identical five items in this order on every surface: github sign-in (10), needs-secret (20), filesystem review (50), github review (50; name tie-break; github was added quarantined because the manual trust mode is the default), notes changed after the rug-pull edit (60); scratch absent; codex never seen (70) is added live by 109-h, which feeds client presence (the pure rule is covered by T053); the scoped token sees only the filesystem item with `count: 1` (FR-007); after sign-in the pill drops by 1 within 1 s and the github review item stays (US1 scenario 3); the quiet-instance threshold case (an item appearing at 60 s / 5 min with no event) is covered by T053's fake-clock test, not live |
| 109-e | Servers grid at 1440 px; open ⋯ on each card; macOS Servers | one status line, ≤ 1 button, equal card heights, Delete only in ⋯ with confirmation, stats line links to `/activity?server=…` and the Activity page opens filtered to that server (109-k merged first) |
| 109-f | with `reveal_secret_headers` left at its default (off), add a server whose URL carries `?api_key=secret123` (`curl -H "X-API-Key: $KEY" -X POST $M/api/v1/servers -d …`) and `curl -H "X-API-Key: $KEY" $M/api/v1/servers/<it>/review`; `curl -H "X-API-Key: $KEY" $M/api/v1/servers/filesystem/review`; `curl -H "X-API-Key: $KEY" $M/api/v1/servers | jq '.data.servers[] | select(.name=="<it>") | .url'` (there is no `GET /servers/{id}` read route, research D27); `mp review show filesystem -o json`; MCP `quarantine_security inspect_quarantined filesystem`; `mp review approve filesystem --except delete_0,delete_1`; macOS Approve Server on a fresh copy | `secret123` appears in no review read (admin or agent token) and the review URL equals that server's redacted `url` in the `GET /servers` list (valid with `reveal_secret_headers` off; with the admin opt-out on, `GET /servers` shows the raw value to an administrator while every review read still redacts it, so the equality does not hold there by design — T077b covers that case); same `tier/annotations/scan_verdict` on the three reads; approve → filesystem unquarantined, `delete_0/1` blocked, and a `call_tool_destructive filesystem:delete_0` loop running during the approve never reaches the fixture upstream: `grep -c '^delete_0$' $TD/fs-calls.log` is `0` while `read_0` calls made after the approve do appear there (so the log is proven live; the race itself is pinned by T078b's counting-upstream test); macOS network log shows `security/approve`, never `unquarantine`; `go test ./internal/tray/...` green (the Go tray's quarantine click opens the Web UI review location, X12) |
| 109-g | Web `/review`, `/review/filesystem`, a changed tool's diff; uncheck 2 tools → Approve server; description with `<img onerror>` in the fixture; macOS Review Queue + tray item | tools visible before approval; diff rendered; inert text; results equal 109-f's CLI outcome; `/security` redirects |
| 109-h | Web `/clients` (all three tabs), wizard Clients step, "+ Add → Client" (after 109-i) or Home "Connect client"; connect Cursor; `/tokens?token=x` → redirect; macOS Clients; `mp client list` | same rows and states everywhere; combined diff before a bulk write; Mode and endpoints absent from the header, present in Endpoint & mode; `curl -H "X-API-Key: $KEY" $M/api/v1/attention` now also lists codex never seen (70) as the sixth item, on every surface; `GET /clients` with an agent token → 403, and the same token on `GET /connect`, `/connect/cursor` and `/connect/cursor/preview` → 403 with no config path in the body (FR-030a); a client configured by hand (write a Cursor entry pointing at `$M/mcp` into the scratch HOME without using connect) lists as `installed` with `connection_unverified: true` until it opens a session, while `GET /clients/cursor` reports it connected; `mp status` shows the `Endpoint & mode` section; after 40 MCP `initialize` calls with distinct random `clientInfo.name` values, `curl -H "X-API-Key: $KEY" $M/api/v1/onboarding/state | jq '.data.state.client_last_seen | length'` is ≤ 32 and the Cursor entry is still present (cap + alias pinning, data-model §7); `mp disconnect cursor` (or Disconnect on the Clients row) turns Cursor from `connected_seen` into `installed` with its `last_seen` kept and drops it from the sidebar's live count, until a new Cursor session arrives (FR-030, `client_disconnected_at`); after two Cursor tool calls `calls_24h` is 2 for Cursor and unchanged for every other row |
| 109-i | Resize to 1440/1100/900/390 in both themes; ⌘K "notes"; "+ Add ▾" items; sidebar groups; macOS sidebar + toolbar "+" | sidebar per navigation-map.md; no clipping; palette finds server, tool and setting; old routes redirect |
| 109-j | Add registry fixtures (official + a Smithery-like source with forks) via Settings → Catalog sources; search "github" on Web, macOS, `mp catalog search github -o json`, MCP `search_servers` without `registry`; paste URL, command and JSON snippets; toggle Secret on `GITHUB_TOKEN`; paste a server with env `API_KEY=env-value` and header `API_KEY: header-value`, both Secret, after pre-creating a keyring secret named `<server>-env-api-key` | identical ranking with the official server first; `/repositories` redirects; secret stored as `${keyring:…}`, and the value is absent from `mcp_config.json`; the pasted server's config references two different keyring names (`…-env-api-key-2`, `…-header-api-key`), the pre-created secret is unchanged, and `GITHUB_TOKEN` defaults to Secret even from a registry entry without `isSecret` |
| 109-k | Approve filesystem, make 3 real calls, load `/activity`, `/activity?view=system`, `/sessions?session=x`, `/usage` bar click, `/tools?server=notes&tier=read` via URL; macOS Activity; `mp activity list --view calls --from -1h`; `curl -H "X-API-Key: $KEY" "$M/api/v1/activity?profile=x&client=cursor"` and `.../sessions?token=t`; `curl -H "X-API-Key: $KEY" "$M/api/v1/activity/usage?agent=qa109"`; `curl -H "X-API-Key: $KEY" $M/api/v1/status`; click the Tools row "Calls" link of `filesystem:read_0` | default view shows 3 calls; 1 folded system row; URL round-trips; no unfiltered fetch in the network log; savings tile says "estimate" before the first retrieve; without Spec 108-e both curls return `400 unsupported_scope_filter` (never unfiltered rows) and status has no `features.scope_filters` (with 108-e merged: `200`, filtered, and the field lists all three); `?agent=` still works on `/activity` while `/activity/usage?agent=qa109` answers `400 unsupported_scope_filter` naming `agent` until 108-e (never unfiltered totals); the Tools "Calls" link lands on `/activity?view=calls&tool=filesystem:read_0` showing the `read_0` calls, and its network log shows `server=filesystem&tool=read_0` |
| 109-m | Run every story's independent test on one instance across all four surfaces | SC-001 to SC-012; parity tests green |
| 109-l | The "before Spec 108" half cannot run at 109-l's own merge point (its prerequisites include 108-f, which follows 108-e, so `features.scope_filters` is already listed); it is run on the build right after 109-k, before any Spec 108 PR — `/activity?client=cursor` keeps the parameter in the URL but shows no chip and sends no `client` to REST — and at every later build by T111/T116 with a status stub. At 109-l (Spec 108-f/i/j/k merged): bind Cursor, then use the Clients row links, the Viewing chip in the header slot, and hand-edit `anonymous_profile` away so Spec 108's binding guard warns | the hidden parameters and links appear without code changes once `features.scope_filters` is present; `?client=cursor` links work; the attention list shows `anonymous_denied_by_binding_guard` first and `client_holds_admin_key` for a seeded admin-key config |

## 4. PR body checklist

- The Spec 109 FR ids closed, the findings (O/N/H/S/C/A/T), the contradiction-register rows closed, and the tests that pin each.
- Which side of the Spec 108 ownership split the PR implements (research D1), and any extension point it leaves.
- Declared golden changes (file list), or "none".
- The live-verification recipe run: port, commands, and an excerpt of the observed output. Do not commit QA report files; attach screenshots to the PR only.
- Reviewer, model, number of rounds, and the final VERDICT line.
- Docs links use docs.mcpproxy.app URLs.
