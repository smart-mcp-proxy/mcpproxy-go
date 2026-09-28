# Quickstart: verifying a Spec 108 PR

Applies to every PR 108-a … 108-l. Run from the PR's own worktree, branched from `origin/main` (or the previous letter's branch while it is unmerged).

## 1. Common gates (every PR)

```bash
# Red first: the PR's new tests fail on the merge base, then pass on the branch
go test -race -count=1 -run '<PR test names>' ./internal/...

# Go suites (server package needs the CI skip regex or it hangs to the 7m panic)
go test -race -count=1 ./internal/profile/... ./internal/config/... ./internal/auth/... ./internal/storage/... ./internal/runtime/... ./internal/connect/... ./internal/httpapi/...
go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...
go test -race -tags server -timeout 20m -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/serveredition/... ./internal/config/... ./internal/server/... ./internal/httpapi/... ./internal/storage/...
go build -tags server -o /dev/null ./cmd/mcpproxy        # never bare: it clobbers ./mcpproxy

# Spec 057/105 suites unmodified (SC-003): no diff to their test files
test -z "$(git diff --name-only origin/main -- 'internal/server/*profile*_test.go' 'internal/server/*scope*_test.go' | grep -v '_v3_')"

# Frozen goldens: only the files a PR declares may change (108-b: retrieve_tools_profile_v3; 108-h: profiles tool)
git diff --stat origin/main -- internal/server/testdata/

# Lint exactly like CI (both runs)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...

# API E2E (required). Until Spec 109-a T011a lands, the script's cleanup runs `pkill -f "mcpproxy.*serve"`,
# which kills EVERY core on the machine (the user's tray core, other worktrees' instances). Run it only
# when the precheck prints nothing; otherwise run it later — never "check and hope".
if pgrep -f 'mcpproxy.*serve' >/dev/null; then echo 'another mcpproxy core is running: skip the E2E gate for now'; else LISTEN_PORT=18$((RANDOM%900+100)) ./scripts/test-api-e2e.sh; fi
# After 109-a T011a the script stops only the PID it started and the precheck can go.
```

Frontend PRs (108-i, 108-j, 108-l):

```bash
cd frontend && npm ci && npx vitest run tests/unit/ && npm run build && cd ..
go run ./cmd/generate-types && git diff --exit-code frontend/src/types/contracts.ts   # generated types in sync
make build && MCPPROXY_BINARY_PATH=$PWD/mcpproxy ./scripts/run-web-smoke.sh           # web-ui-sweep incl. profiles-scope.spec.ts
```

macOS PR (108-k):

```bash
cd native/macos/MCPProxy && swift test --filter 'ProfilesV3ContractTests|ClientsTrayMenuTests|ScopeFilterTests|MenuStructureTests' && cd -
# build + swap binary per docs/development/macos-tray.md, then verify with the mcpproxy-ui-test MCP:
#   list_menu_items (tray: "Clients" submenu present, no "Profile:" submenu), click_menu_item, screenshot_window (Profiles, Clients views)
```

Cross-model review (per CLAUDE.md reviewer ladder): `zcode` default, `codex` only for 108-d/108-c (security/auth), ≤ 10 rounds per PR, chunked briefs, stdin closed:

```bash
git diff origin/main...HEAD > .review-tmp/pr.diff
gtimeout 900 zcode --prompt "Review .review-tmp/pr.diff against specs/108-profiles-v3/spec.md FR-<ids> and contracts/<file>. Verify claims against file:line. End with VERDICT: CLEAN or VERDICT: FINDINGS." --mode plan --no-color --cwd "$PWD" < /dev/null > .review-tmp/zcode-r1.txt
grep "VERDICT:" .review-tmp/zcode-r1.txt   # no verdict line = UNREVIEWED, not clean
```

## 2. Isolated live instance (every PR that changes runtime behaviour)

```bash
RUN=$(mktemp -d /tmp/mcpp108.XXXX); PORT=18$((RANDOM%900+100))
mkdir -p $RUN/home/.cursor $RUN/home/.codex && echo '{"mcpServers":{}}' > $RUN/home/.cursor/mcp.json && touch $RUN/home/.codex/config.toml
FX=$PWD/internal/server/testdata/preflight_fixture_server.js
TD=$PWD/internal/server/testdata/profiles_v3          # github.tools.json, notion.tools.json, filesystem.tools.json (added in 108-a)
cat > $RUN/mcp_config.json <<JSON
{ "listen": "127.0.0.1:$PORT", "data_dir": "$RUN/data", "enable_web_ui": true, "quarantine_enabled": false,
  "mcpServers": [
    {"name":"github","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/github.tools.json"},"protocol":"stdio","enabled":true},
    {"name":"notion","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/notion.tools.json"},"protocol":"stdio","enabled":true},
    {"name":"filesystem","command":"node","args":["$FX"],"env":{"FIXTURE_TOOLS_FILE":"$TD/filesystem.tools.json"},"protocol":"stdio","enabled":true} ] }
JSON
go build -o $RUN/mcpproxy ./cmd/mcpproxy
# start as its OWN background call (run_in_background), never `&` inside a compound call:
HOME=$RUN/home $RUN/mcpproxy serve --config $RUN/mcp_config.json --data-dir $RUN/data --log-level=debug
# readiness: poll, don't sleep
KEY=$(jq -r .api_key $RUN/mcp_config.json); until curl -sf -H "X-API-Key: $KEY" http://127.0.0.1:$PORT/api/v1/status >/dev/null; do sleep 1; done
# stop only yours:
pkill -f "mcpproxy serve --config $RUN"
```

Docker isolation is irrelevant for `node` fixtures when `docker_isolation` is absent from the scratch config.

## 3. Live-verification recipes per PR

Every bare `curl $M/api/...` below is sent with the admin key, `curl -H "X-API-Key: $KEY" …`: REST refuses an unauthenticated TCP request with `401` (`internal/httpapi/server.go`, API key required for every TCP connection), so a recipe that omits the header verifies nothing. Where a recipe names another credential (`<ro-bot>`, a scoped `mcp_agt_` token, a client credential) that header replaces the admin key.

| PR | Recipe (against the §2 instance; `M=http://127.0.0.1:$PORT`) | Pass condition |
|---|---|---|
| 108-a | (built on top of Spec 109-a, which adds `contracts.AnnotationTier` and the `tier` field) `curl -H "X-API-Key: $KEY" $M/api/v1/servers/github/tools` and `go test ./internal/profile/ -run IntrinsicTier` (T005a); add the enforcement-matrix profiles to `$RUN/mcp_config.json`; add one invalid profile | every github tool's REST `tier` equals the tier contracts/enforcement-matrix.md declares for it — the same values T005a pins for `IntrinsicTier` (one mapping, research D30); T005a green; with the shipped gate (`PolicyEnforcementReady=false`) the v3 profiles are rejected with the FR-009a text and the legacy profile loads; under the test build tag / override: the exact validator warnings (incl. the `filesystem` allow-rule warning), invalid enum → config reload rejected with the FR-007 text; `GET /api/v1/profiles` still lists legacy fields unchanged |
| 108-b | **Real instance**: the built binary refuses a `max_tier` profile (exit 4, FR-009a text) — no build before 108-d can enable the policy, because the override exists only inside `go test` binaries (FR-009a, T004a). The discovery behaviour below is therefore verified here by the T016/T017 tests under `EnablePolicyForTest`, and on a real instance from 108-d (rerun this row there): MCP session on `$M/mcp/p/work-readonly` (curl JSON-RPC: `initialize`, then `tools/call`; or `bench/mcpcaller.go`) → `retrieve_tools {"query":"issue"}` | no `create_issue`; `hidden_by_profile: 1` and `profile: "work-readonly"` (URL source); `limit: 1` still returns an admitted tool; `/mcp/all` with a token pinned to work-readonly lists no write tools and `retrieve_tools` there carries no `profile` field (pin source) |
| 108-c | **Legacy profiles only** — FR-009a still rejects v3 policy fields until 108-d, so add `{"name":"ro","servers":["github"]}` and `{"name":"full","servers":["github","notion","filesystem"]}` (no policy fields) to `$RUN/mcp_config.json`. **Before starting the PR binary**, run the `origin/main` binary once against the scratch data dir to create a regular token named `client-codex` (the PR binary refuses the `client-` prefix). Then, with `require_mcp_auth: false`: `HOME=$RUN/home $RUN/mcpproxy connect cursor --profile ro --lock` must exit 1 with `409 binding_bypassable_without_auth` whose `fixes` is only `require_mcp_auth` and write nothing; `PATCH /config {"anonymous_profile":"ro"}` → `400` with the FR-009a text (the shipped 108-c binary rejects `anonymous_profile` until 108-d); set `require_mcp_auth: true` and repeat (socket) → inspect `$RUN/home/.cursor/mcp.json`; use its token on `$M/mcp`; then `curl -H "X-API-Key: $KEY" -X PUT $M/api/v1/clients/cursor/binding -d '{"profile":"full","mode":"locked"}'` (succeeds); `PATCH /config {"require_mcp_auth":false}` and a `POST /config/apply` of the current document with `require_mcp_auth: false` → both `409 binding_bypassable_without_auth`, config file unchanged (existing routes guarded from 108-c, T033a); `PATCH /config {"anonymous_profile":"ro"}` → `400` with the FR-009a text (no built binary can set `anonymous_profile` before 108-d: the override exists only inside `go test`, T004a; the `anonymous_profile: ro` sequence — re-binding to `full` succeeds, `PATCH /config {"anonymous_profile":"full"}` succeeds, re-binding Cursor to `ro` is refused `409 binding_bypassable_without_auth` — is covered by T033a under `EnablePolicyForTest` and rerun on the real instance from 108-d); reconnect over the active credential with `HOME=$RUN/home $RUN/mcpproxy connect cursor --profile full --lock` (staged rotation, FR-021a); `curl -H "X-API-Key: <any scoped mcp_agt_ token>" $M/api/v1/connect` and `.../connect/cursor/preview` (FR-025a) | config holds an `mcp_cli_` credential, not `$KEY`; the preview's masked `credential` starts with `mcp_cli_`; hand-edit `require_mcp_auth` to `false` → a keyless session sees no tools (runtime guard); reconnect: both secrets valid until the config write, then only the new one; kill the core between stage 1 and the config write, restart → the reconciler rolls back (old secret still works); a hand-corrupted client record (remove `kind` with a bbolt editor on the scratch DB) → `401 malformed credential record`; `shasum` of the file unchanged after reassignment; `GET /api/v1/activity?type=profile_change` shows one `assign` record per mint/reassignment (FR-030; first-class `profile_source` on call records is checked in 108-e); `PUT $M/api/v1/clients/codex/binding` for a client never connected → `409 no_client_credential`; open session receives `notifications/tools/list_changed`; token on `$M/api/v1/status` → 403; `$M/mcp/p/full` with the locked token → admitted after the reassignment (own pin), `$M/mcp/p/ro` → refused; `connect codex` exits 1 with the `409` remediation and leaves the pre-created `client-codex` token untouched; the scoped-token `GET /connect` and `/connect/cursor/preview` return `403` with no `credential_state` in the body, while the same calls with `$KEY` return `200` with `credential_state`. **Not in this recipe** (not shipped by 108-c): `client rotate`/`client forget` (REST in 108-f, CLI in 108-g), the `GET /clients` warnings (decorated in 108-f), v3 policy profiles and `anonymous_profile` on the shipped binary (108-d, FR-009a) |
| 108-d | with the ro-bot token below: `curl -H "X-API-Key: <ro-bot>" "$M/api/v1/index/search?q=issue"`, `$M/api/v1/tools`, `$M/api/v1/servers/github/tools`, `$M/api/v1/servers/github/tools/create_issue/diff`, and replay of a recorded `filesystem:read_text_file` call (FR-015/FR-015a); the 108-b session → `call_tool_write github:create_issue`; `code_execution` listing; a Cursor token locked to a `management_tools: true` profile calls `upstream_servers {operation:"patch"}` and `{operation:"restart"}` on an in-scope server; with `require_mcp_auth:false` and `anonymous_profile` naming a `management_tools: true` profile, a keyless session tries the same `patch`/`restart`; `mcpproxy token create --name ro-bot --servers '*' --permissions read,write,destructive --profile-pin work-readonly` then `curl -H "X-API-Key: <ro-bot>" -X POST $M/api/v1/tools/call -d '{"tool_name":"call_tool_write","arguments":{"name":"github:create_issue","args_json":"{}"}}'` and the same with `{"tool_name":"upstream_servers","arguments":{"operation":"list"}}`; `curl -H "X-API-Key: <ro-bot>" -X POST $M/api/v1/code/exec -d '{"code":"1+1"}'`; replay a `github:create_issue` record created with the admin key: `curl -H "X-API-Key: <ro-bot>" -X POST $M/api/v1/tool-calls/<id>/replay` | refusal text from contracts/refusals.md (no profile title or slug in it); a fresh binary now accepts the v3 profiles without the override (FR-009a lifted); `GET /api/v1/activity?status=blocked` shows `block_reason=profile_tier` and the profile; fixture upstream log shows zero calls; `patch`/`restart` refused for both the client credential and the confined keyless session, `mcp_config.json` unchanged, no process restart in the server log; REST `/tools/call` → `403` with the same refusal text for `create_issue` and the unknown-tool error for `upstream_servers` (same status and body shape as `"tool_name":"no_such_tool"`, differing only in the echoed name and request id); `/code/exec` → `403` with `error.code: PROFILE_BLOCKED` and the refusals.md code-execution text (not `500 EXECUTION_FAILED`); replay → `403` with the `profile_tier` text and zero fixture upstream calls; the REST discovery calls list no write/destructive/unannotated/secret/filesystem tool and the diff answers `404`; the `filesystem` replay answers the same `404` as an unknown record id |
| 108-e | issue calls as Cursor (bound locked, 108-c recipe) and as an API-key session; check that Cursor's call records carry `profile_source=pin` (US2-2); `curl -H "X-API-Key: $KEY" "$M/api/v1/activity?client=cursor&status=blocked"`, `/sessions?client=cursor`, `/activity/usage?profile=work-readonly`, `/tools?client=cursor`; `/tools?profile=work-readonly` with the ro-bot token; `curl -H "X-API-Key: $KEY" $M/api/v1/status` | exact record sets; legacy records show empty fields; `?client=-` returns only unattributed; the scoped view-as returns visible rows + `counts` only; status carries `features.scope_filters`; `curl -H "X-API-Key: $KEY" "$M/api/v1/tokens?profile=work-readonly"` still answers `400 unsupported_scope_filter` until 108-f (a loud refusal, never unfiltered rows); a name the handler does not honour is refused too — `GET /tools?token=ci-bot` → `400 unsupported_scope_filter` whether or not Spec 109-k has merged (108-e carries the gate function if it merges first); `/activity/usage?agent=ro-bot` is filtered (honoured from this PR) |
| 108-f | `DELETE $M/api/v1/clients/cursor` then `connect cursor` again (revoked record replaced, FR-021); `POST $M/api/v1/clients/cursor/rotate` + `/rotate/finalize`; `GET $M/api/v1/clients` under the 108-c hand-edit fixture carries `anonymous_denied_by_binding_guard`; `POST /config/apply` of a document that clears `anonymous_profile` while Cursor is bound → `409 binding_bypassable_without_auth`, nothing applied; full CRUD round trip via curl incl. delete-in-use (expect 409 `used_by`), delete of the `anonymous_profile` with `?force=true` (expect 409 `profile_is_anonymous_profile`), `GET /clients?profile=work-readonly`, `GET /tokens?profile=work-readonly`, rename, `POST /profiles/try`, `GET /access/explain?client=cursor&tool=github:create_issue`; `GET /profiles` and `GET /profiles/<unreachable>/effective-tools` with the ro-bot token; seed a client config holding `$KEY`, then `POST /clients/upgrade-admin-key-holders` without and with `apply`; **FR-008a delta** (T070/T070a): with `require_mcp_auth` off, `anonymous_profile: wf-anon` (a copy of work-full) and Cursor switchable on work-readonly, `PUT /profiles/work-full` without `filesystem` and `DELETE /profiles/work-full` → both `409 binding_bypassable_without_auth`, config file byte-identical; with no `anonymous_profile`, `POST /clients/upgrade-admin-key-holders {"profile":"work-readonly","apply":true}` → `409` (the preview already carried `guard`), the seeded client config still holds `$KEY` | shapes match contracts/rest-api.md; one `profile_change` per mutation; SSE `profiles.changed` observed on `/events`; after `rename work-readonly work-ro`, Cursor's next MCP call resolves to `work-ro` (pin moved, not deny-all) and its open session receives `tools/list_changed`; hand-editing `max_tier` in `$RUN/mcp_config.json` also sends `tools/list_changed` to that session; with `require_mcp_auth: true` (set it first: with auth off, a locked Cursor on work-readonly makes `anonymous_profile: work-readonly` bypassable, because its `switchable_to` reaches work-full — FR-008a), `PATCH /config {"anonymous_profile":"work-readonly"}` writes one `profile_change` `change=anonymous`; the scoped token never sees the unreachable profile (omitted / `404`) and gets no excluded rows; the admin-key upgrade previews then rewrites the seeded config to an `mcp_cli_` credential and returns `next_step: rotate_admin_api_key`; `GET /clients` rows keep Spec 109's presence fields |
| 108-g | run every command in contracts/cli.md with `-o json` and table output, incl. `client rotate cursor --yes`, `client forget cursor`, `client set-profile --from-profile work-readonly --to-profile work-full` and `profile create x --servers github --unannotated as-write` (stored as `as_write`) | JSON equals REST `data`; `client list` keeps Spec 109's columns (incl. `CONFIG PATH`) with the binding columns appended; `doctor` reports `profiles.binding_bypass` after a hand edit leaves `require_mcp_auth` off and Cursor bound (locked **or** switchable) to a profile narrower than `anonymous_profile`; `client set-profile cursor work-full` with no flag keeps Cursor's mode (`client show cursor`) |
| 108-h | API-key MCP session: `tools/list` contains `profiles`; `profiles {operation:"assign",client:"cursor",profile:"work-full"}`; Cursor-token session: `tools/list` lacks it | as in US2 scenario 5; the `profiles` rows of contracts/mcp-tools.md and contracts/enforcement-matrix.md hold; with `read_only_mode: true` the tool is still listed, `list` works and `assign` is refused |
| 108-i | open `$M/ui/profiles?apikey=$KEY` in the in-app browser; create profile, Try it, classify `github:search_code`, rename it and back, assign Cursor on `/ui/clients` (Spec 109's page with this PR's controls), create token with profile | no header "Profile:" switcher; Viewing chip present in Spec 109's header slot and driven by `useScopeQuery`; Profiles appears in the sidebar's Connect group (109-i); explainer opens from a client row |
| 108-j | load `/ui/tools?client=cursor`, `/ui/usage?profile=work-readonly`, `/ui/sessions?client=cursor` (Spec 109 redirects it to `/ui/activity?view=sessions&client=cursor`); navigate between them via the link-map links; press Back; open a blocked Activity row | each loads filtered without an unfiltered flash (Spec 109-k behaviour, un-hidden by `features.scope_filters`); Tools shows greyed excluded rows with reasons; attribution chips on Activity; "Allow in profile…" and "Why?" work (audit check 6) |
| 108-k | point a dev build of the app at the scratch core as described in docs/development/macos-tray.md (never at the live tray's core), open Profiles and Clients, reassign Cursor from the tray Clients submenu, then open Home (Spec 109-d's `HomeView.swift`) and filter its usage summary and sessions list by Client = cursor | tray shows `Cursor — Work · Read-only 🔒`; reassignment reflected in Web UI within 1 s (SSE); Home's usage and sessions narrow to Cursor with client/profile/source columns (T119a); no `DashboardView.swift` exists in the tree |
| 108-l | run the six audit acceptance checks end to end on one instance across Web UI, macOS, CLI and MCP | SC-001, SC-006 pass; parity test green |

## 4. PR body checklist

- Spec 108 FR ids closed and the tests that pin each; enforcement-matrix rows covered.
- Declared golden changes (file list) or "none".
- Live-verification recipe run: port, commands, observed output excerpt (no QA report files committed).
- Reviewer, model, rounds, final VERDICT line.
- Docs links use docs.mcpproxy.app URLs.
