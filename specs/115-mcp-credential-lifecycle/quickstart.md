# Quickstart and E2E Test Plan: MCP Credential Lifecycle

This file is both the user walkthrough and the binding end-to-end plan. It mirrors the "Required end-to-end tests" of issue #1552. In the success paths, **administration is MCP-only**: no `exec`/CLI, no hand-written REST management call, no direct store mutation. Initial daemon and fixture setup (config file, profiles seeded in config where noted, upstream fixtures) is allowed. Profiles are created through `profiles create` over MCP wherever the scenario says "admin creates".

Preflight/required-tools readiness is never asserted (issue #1548).

## 1. Walkthrough (admin agent's view)

```jsonc
// 1. profile (existing tool)
{"name":"profiles","arguments":{"operation":"create","name":"daily-research","servers":["library"],
  "max_tier":"read","unannotated":"deny","tools":{"deny":["library:read_private*"]},
  "code_execution":false,"management_tools":false}}
// 2. review the grant
{"name":"profiles","arguments":{"operation":"effective_tools","name":"daily-research"}}
// 3. issue a locked worker client (secret returned ONCE)
{"name":"credentials","arguments":{"operation":"create_client","client":"delegated-worker",
  "profile":"daily-research","expires_in":"1h","purpose":"Summarise today's library additions; assumes no writes needed"}}
// → {"client":{...,"binding":"locked","lease":true},"credential":"mcp_cli_…","snippet":{...},"delivery":{...},"links":{...}}
// 4. worker connects with header X-API-Key: <credential> (or Authorization: Bearer) and works
// 5. inspect
{"name":"credentials","arguments":{"operation":"get","client":"delegated-worker"}}
// 6. revoke when done
{"name":"credentials","arguments":{"operation":"revoke","client":"delegated-worker"}}
```

The daemon must have `require_mcp_auth: true` or a confining `anonymous_profile`. Otherwise step 3 is refused with `binding_bypassable_without_auth`, and the refusal lists the fixes.

## 2. Fixtures

**Go (`internal/server/credentials_lifecycle_e2e_test.go`)**
- `NewTestEnvironmentWithOptions` with `Mutate` setting `RequireMCPAuth=true`, an API key, and two upstream servers that `CreateMockUpstreamServer` builds in process:
  - `library`: `search_books` (readOnlyHint), `read_private_notes` (readOnlyHint), `add_note` (readOnlyHint=false), `mystery_tool` (no annotations)
  - `tracker`: `list_issues` (read), `comment_issue` (write)
- Each mock tool handler increments `MockUpstreamServer.Dispatches(tool)` (an atomic counter, new). Assertions read the counters before and after each call.
- MCP sessions use mcp-go streamable-HTTP clients with a header option per credential. Each session stays open across steps, so "same session" holds literally. The admin uses the API key.
- Each semantic check inspects `CallToolResult.IsError` and the inner text or JSON. HTTP 200 alone never counts as success.

**Live harness (built binary)**
- `start.sh i1552-<n> 1892X <binary> min`. Then edit `$QA_CFG`: two servers using a ledger variant of `fixture.py` that appends `{"ts","server","tool"}` per `tools/call` to `$QA_RUN/ledger.jsonl`, plus `require_mcp_auth: true` and `quarantine` off for both. Restart.
- The MCP client is a copy of `mcp.py` extended with `--session-file` (persists `Mcp-Session-Id` and reuses it without re-initializing) and `--header 'X-API-Key: …'`.
- Ports 18920–18929. Stop only own runs, with `stop.sh <run>`.
- Evidence goes to `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/i1552/` (EVIDENCE.md, raw request/response JSON, ledger snapshots, log greps, screenshots) and is never committed.

## 3. Scenarios

### E2E-1 Fresh client path (`TestE2E_CredentialsLifecycle_FreshClient`)
1. The admin initializes and checks that `tools/list` contains `credentials` and `profiles`.
   (Unit-level companion, not E2E: a fault-injection store whose `ListAgentTokens` fails after a successful mint still yields the secret and view; a failing mint yields no record and no `issue` record. See T015a.)
2. The admin runs `profiles create daily-research` (servers `[library]`, `max_tier: read`, deny `library:read_private*`, `unannotated: deny`, code/management off).
3. The admin runs `credentials create_client delegated-worker profile=daily-research expires_in=1h`. Assert `binding=locked`, `lease=true`, `state=active`, a credential with prefix `mcp_cli_`, a snippet header `X-API-Key`, and links with no `apikey`.
4. The worker initializes with the credential. Its `tools/list` contains neither `credentials` nor `profiles`.
5. Allowed: `call_tool_read library:search_books` succeeds, and the `search_books` dispatch count goes up by 1.
6. Refused, each with zero dispatch delta:
   - write: `call_tool_write library:add_note`
   - private read: `library:read_private_notes`
   - other server: `tracker:list_issues`
   - unannotated: `library:mystery_tool`
   - admin operation: `credentials list` and `profiles list` (each a JSON-RPC "tool not found" error)
   - profile escape: `set_profile` to another profile, refused
7. The admin runs `credentials revoke client=delegated-worker`. Assert `changed=true`.
8. The worker's next request **on the same session** gets HTTP 401 with `token has been revoked`, and the dispatch delta is 0.
9. The admin runs `credentials get client=delegated-worker`. Assert `state=revoked` and `revoked_at` is set.

### E2E-2 Fresh token path plus expiry (`TestE2E_CredentialsLifecycle_FreshToken`, `…_TokenExpiry`)
1. The admin runs `credentials create_token research-task-42 profile=daily-research expires_in=30m`. Assert `binding=pinned`, `lease=true`, and a credential prefixed `mcp_agt_`.
2. The worker initializes with the token. For each tool in the fixture, `profiles explain token=research-task-42 tool=<t>` gives a decision, and it must equal the real call's outcome (success means dispatch +1; refusal means +0 and a reason consistent with the explain reason).
3. The admin revokes the token over MCP. The next request on the same session gets 401.
4. Expiry: the admin runs `create_token research-task-43 … expires_in=3s`. The worker's first call succeeds. Then `require.Eventually` (≤ 10 s) waits for a request to get 401 `token has expired`. No admin action happens in between. `credentials get` reports `state=expired`.

### E2E-3 Live reassignment (`TestE2E_CredentialsLifecycle_LiveReassign`)
1. The admin creates profiles `research` (read-only on `tracker`) and `triage` (`max_tier: read`, `tools.allow: ["tracker:comment_issue"]`, the one exact write exception).
2. The admin runs `credentials create_client w1 profile=research expires_in=1h` and `create_token t1 profile=research expires_in=1h`.
3. The w1 and t1 sessions each call `tracker:comment_issue`. Both are refused (+0).
4. The admin runs `profiles assign client=w1 profile=triage`.
5. On the same w1 session, with no re-initialize: `comment_issue` succeeds (+1) and `list_issues` succeeds. The t1 session's `comment_issue` is still refused (+0).
6. `profiles effective_tools name=triage` lists `tracker:comment_issue` as admitted with its write tier, and the UI label check in T065 covers the "exception" wording.

### E2E-4 Parity and negative paths (`TestE2E_CredentialsLifecycle_Negatives`, `…_SurfaceParity`)
- **Surface parity** uses two profiles, because `/mcp/code` hides `code_execution` from a profile with `code_execution: false` (`mcp_routing.go:1076-1078`), so nested policy cannot be exercised under `daily-research`:
  - `daily-research-code`: the same policy as `daily-research` (servers `[library]`, `max_tier: read`, deny `library:read_private*`, `unannotated: deny`) but `code_execution: true`. For a token and a client issued to it, check every fixture tool via (a) `/mcp` `call_tool_*`, (b) `/mcp/call`, (c) `/mcp/code` `code_execution` with a nested `call_tool`, and (d) `/mcp/all` direct `library__search_books`. Each outcome must equal the `explain` decision. Admitted nested reads (`library:search_books`) dispatch exactly once per call; forbidden nested calls (`add_note`, `read_private_notes`, `mystery_tool`, `tracker:list_issues`) dispatch zero times and the script sees a policy refusal.
  - `daily-research` (code off): for a token and a client issued to it, `/mcp/code` `tools/list` lacks `code_execution`, and a forged `tools/call code_execution` whose script calls `library:search_books` is refused before the script runs, with zero dispatches for every fixture tool.
- **Deleted pin fails closed**: run `profiles delete daily-research force=true` while worker token X is pinned to it. X's next call is refused with zero dispatches. `credentials get` reports `profile_state=dangling`. Then, with X still active, try `require_mcp_auth: false` through the API (refused, `binding_bypassable_without_auth` naming X) and by editing `config.json` on disk (hot reload applies, but an anonymous `tools/call` without X's credential is refused and the ledger shows zero dispatches until X is revoked).
- **Non-admin cannot mutate**: for a worker client, a worker token, an anonymous caller (a second daemon config with `require_mcp_auth: false` and `anonymous_profile` set), and an admin session after `set_profile` into a non-management profile: `tools/list` lacks `credentials`, and a forged `tools/call credentials create_token …` is rejected with a JSON-RPC "tool not found" error (no tool result; A25). A `credentials list` (admin, afterwards) shows no new identity. After the admin runs `set_profile("")`, the tool is visible again.
- **Write-disabled management**: with `read_only_mode: true` (hot reload), `create_client`, `create_token` and `revoke` each return `code=read_only_mode`, and `list`/`get` still work. Repeat with `disable_management: true` → `management_disabled`. Check with list that no identity was created or revoked.
- **Duplicates, invalid input, conflicts**:
  - `create_client` with an existing active id, then with a revoked one: `identity_exists` with `state=active` and `state=revoked`
  - `create_token client-foo`: `reserved_identity`
  - `create_client cursor`: `reserved_identity`
  - `profile: ""`: `profile_required`
  - unknown profile: `unknown_profile`
  - `expires_in` of `"1y"`, `"-1h"`, `"0s"`, `"400d"`, `"106752d"`, `"9999999999d"`, or missing: `invalid_expiry` / `missing_argument`, and `credentials list` is unchanged afterwards
  - two secrets in one call, a secret nested in an array/object, and a secret used as an argument key: `secret_in_argument`; grep activity export, SSE capture, `main.log` and a copy of `config.db` for both issued credentials and the API key (no hits)
  - an unknown argument such as `allowed_servers`: `invalid_argument`
  - 20 concurrent `create_token same-name`: exactly 1 succeeds and 19 return `identity_exists`

  After each failure: `credentials list` and the raw REST list (read-only verification, not management) show no new record, and no orphan credential authenticates. Each would-be secret from a failed call is absent, because none was returned.
- **Guard**: with a daemon at `require_mcp_auth: false` and no `anonymous_profile`, `create_client`/`create_token` return `binding_bypassable_without_auth` with `fixes`, and nothing is minted.
- **Guard persistence (FR-012a)** (`TestE2E_CredentialsLifecycle_GuardPersists`): with `require_mcp_auth: true`, the admin issues token `confined-1` (profile `daily-research`) over MCP. Then:
  1. a config write through the API funnel (`PATCH /api/v1/config` and `POST /api/v1/config/apply`, used here only as the attack vector, not for administration) setting `require_mcp_auth: false` is refused with `binding_bypassable_without_auth` whose `bindings[].token_name = "confined-1"`; the live config is unchanged;
  2. the same write with `require_mcp_auth: false` plus an `anonymous_profile` wider than `daily-research` (or with the confining `anonymous_profile` removed, starting from a daemon confined by `anonymous_profile: daily-research`) is refused the same way;
  3. the same change applied as a hand edit of the config file (file-watcher hot reload) is applied, and then an anonymous session (no credential) gets a refusal on every fixture tool (`tools/call` refused, `anonymous_denied_by_binding_guard` warning naming `confined-1`) with zero dispatches across the ledger;
  4. after `credentials revoke token=confined-1`, the anonymous session's access follows the plain config again (the guard released), proving the binding is tied to the active token;
  5. compatibility: a token created with REST `POST /api/v1/tokens` with `profile_pin` does not block step 1 (A13).

### E2E-5 Secret handling (`TestE2E_CredentialsLifecycle_SecretSinks`)
1. Capture these sinks: a zap observer at debug for core logs, the per-server log files, `/api/v1/activity` with `limit=1000`, `/api/v1/activity/export` (JSON and CSV), the SSE `/events` stream for the whole run, MCP notifications received by all sessions, every error text, the config file bytes, and the `config.db` bytes.
2. Run issue, list, get, revoke, and a failed duplicate issue for both kinds.
3. Secret fed back as input (FR-020a): for each delivered secret `S`, call `create_client`/`create_token` with `S` (and with `S` embedded in other text) as `purpose`, `display_name`, `client`, `name`, `profile`, `expires_in`, `operation`, and an unknown key's value. Each call answers `secret_in_argument` naming the field, the error text does not contain `S`, nothing is minted, and the call's activity record stores only the server-built summary (`_screened`, `operation?`, `offending_fields`, `unknown_offending_count`). Repeat with the daemon's API key as the value.
3a. Same secrets through REST (FR-020b): `POST /api/v1/tokens` with `S` as `purpose` and as `name`, and `POST /api/v1/clients` with `S` as `purpose`, `display_name` and `id`. Each answers 400 naming the field; the token and client lists are unchanged; no `profile_change` and no `credentials.changed`. Repeat with the API key.
3b. Detection settings cannot weaken the screen (A17): restart with `sensitive_data_detection.enabled: false`, then `scan_requests: false`, then `max_payload_size_kb: 1`, and repeat step 3 with an AWS-style vendor key (no `mcp_` prefix) placed after 1,100 bytes of filler in `purpose`. Each answers `secret_in_argument` with zero writes. A payload over 16 KiB answers `arguments_too_large` and its activity record is `{_screened, operation?, size_bytes}` only.
4. For each delivered secret `S`: `strings.Contains(sink, S)` is false for every sink, including credential metadata from `list`/`get` and the REST token/client lists. It is true only for the one create result. The `profile_change{issue}` record carries `token_prefix` (12 characters) and `purpose_set` but neither `S` nor the purpose text.
5. After revoke, the revoked credential cannot get metadata or tools on its existing session: `tools/list`, `credentials list` and `retrieve_tools` all return 401.
6. Revoke during connect (A19, T026a): begin a supported-client reconnect so a staged secret exists, revoke the client over MCP before the connect commits, and check: `changed: true` (no `connect_in_progress`), both the current and the staged secret get 401 on their existing sessions, the ledger shows zero dispatches after the revoke, and the connect's commit fails as `credential_superseded` without restoring access.

### E2E-6 Dispatch proof (cross-cutting)
Every denied or revoked call in E2E-1 to E2E-5 asserts a dispatch delta of 0 **and** an inner semantic refusal: `isError: true` with a policy reason, or HTTP 401 with the agent-token error. The live harness repeats this with the ledger file. EVIDENCE.md lists every call with the expected and observed ledger delta.

## 4. UI verification (after E2E-1 and E2E-2 provisioning on the live harness)

Playwright plus screenshots, following `docs/development/web-ui-verification.md`:

1. Clients → the custom row `delegated-worker` shows: profile chip, Locked, "via MCP · api_key", "Lease ends in …", "Stated purpose — not enforced".
2. The Tokens tab row `research-task-42` shows Pinned, its lease and its issuer.
3. In Profiles → `daily-research` editor → Assigned to, the client link goes to `/clients?client=delegated-worker` and the token link goes to `/clients?tab=tokens&token=research-task-42`. Each opens a filtered view.
4. Tools → Callable lists exactly the admitted tools. The `triage` card reads "Read-only + 1 write exception".
5. Activity: the allowed calls, blocked policy decisions (reason and identity), and the `issue`/`revoke` records with actor and links. None contains the secret.
6. Live: with Clients open, run MCP `profiles assign` and `credentials revoke`, and screenshot before and after without reloading. With the profile editor dirty, an MCP `profiles update` of that profile keeps the draft and shows Reload.
7. Lease end: after a 90 s token expires, the row reads "Lease ended", with no warning colour and no reconnect prompt.

## 5. Commands

```bash
go test -race ./internal/runtime/... ./internal/auth/... ./internal/storage/... -run 'Credential|Issue|Revoke'
go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/... ./internal/httpapi/...
go test -race -timeout 10m ./internal/server -run TestE2E_CredentialsLifecycle
cd frontend && npx vitest run tests/unit/credentials*.spec.ts tests/unit/profiles-tier*.spec.ts
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...
./scripts/test-api-e2e.sh
```
