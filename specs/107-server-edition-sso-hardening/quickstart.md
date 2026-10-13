# Local Verification — server edition + fake OIDC IdP

Isolated, synthetic, loopback-only. Nothing here touches `~/.mcpproxy`, the tray's core, or a production IdP. Every command is what `scripts/dev-server-edition.sh` (created in PR-B, `--phase` flag gates the steps that need later PRs) runs; the script is the single source of truth and this page is its narrative. Record exact commands and outcomes in `verification.md`, one section per PR.

## Rules (from memory, all verified)

- Build the server binary with `-o mcpproxy-server` — bare `go build -tags server ./cmd/mcpproxy` clobbers `./mcpproxy` (`project_server_tags_build_overwrite`).
- Run the instance with **both** `--config <root>/mcp_config.json` **and** `--data-dir <root>` — `MCPPROXY_DATA`/bare `--data-dir` write the generated API key to a file the next boot never reads (`agent5` §4). Use a high port (`18xxx`), start it in its own Bash call with `run_in_background`, and wait out the BBolt lock (exit 3) on restart (`reference_isolated_dev_instance`).
- Kill only your own instance by PID; never `pkill -f mcpproxy` (the e2e script's blanket pkill is the trap, not the pattern).
- The fake IdP's login form is HTML: the headless recipe POSTs `username,password,consent=on,action=approve` plus the authorize query parameters to `/authorize` **without following redirects**, then GETs the `Location` (`project_oauthserver_test_rig`).

## 0. One-time

```bash
ROOT=$(mktemp -d /tmp/mcpproxy-107.XXXX); PORT=18$((RANDOM % 900 + 100)); IDP=19$((RANDOM % 900 + 100))
go build -tags server -o "$ROOT/mcpproxy-server" ./cmd/mcpproxy
"$ROOT/mcpproxy-server" version | grep -q 'edition: server'      # self-identifies
```

## 1. Fake OIDC IdP (PR-B extension of `tests/oauthserver`)

```bash
go run ./tests/oauthserver/cmd/server -port "$IDP" -oidc \
  -redirect-uri "http://127.0.0.1:$PORT/api/v1/auth/callback" \
  -user 'alice@example.com:pass:eng' -user 'bob@example.com:pass:' -user 'carol@example.com:pass:ops' -user 'dana@example.com:pass:' \
  -groups-claim groups &  IDP_PID=$!
curl -s "http://127.0.0.1:$IDP/.well-known/openid-configuration" | jq -e '.userinfo_endpoint and .jwks_uri and (.id_token_signing_alg_values_supported|index("RS256"))'
```

The server prints `Confidential ID` / `Confidential Secret`; export them as `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` (the config references them with `${env:}` — nested `server_edition.*` keys have no env override, FR-020).

Tamper cases for the US2 matrix: restart with one of `-token-error bad-signature|wrong-iss|wrong-aud|expired|no-nonce|alg-none|hs256`, `-userinfo-sub-mismatch`, `-email-verified=false`, `-discovery-http-token-endpoint`, `-token-endpoint-redirect` (each maps to an `ErrorMode` field, research D2).

## 2. Scratch configuration

```bash
cat > "$ROOT/mcp_config.json" <<EOF
{
  "listen": "127.0.0.1:$PORT",
  "require_mcp_auth": false,
  "trusted_proxies": ["127.0.0.1/32"],
  "audit_log": {"enabled": true, "path": "$ROOT/audit.jsonl", "stdout": false},
  "mcpServers": [
    {"name": "a",    "command": "node", "args": ["$PWD/tests/echo-rugpull-server/index.js"], "protocol": "stdio", "enabled": true, "shared": true},
    {"name": "b",    "command": "node", "args": ["$PWD/tests/echo-rugpull-server/index.js"], "protocol": "stdio", "enabled": true, "shared": true},
    {"name": "a__b", "command": "node", "args": ["$PWD/tests/echo-rugpull-server/index.js"], "protocol": "stdio", "enabled": true, "shared": true}
  ],
  "server_edition": {
    "enabled": true,
    "admin_emails": ["dana@example.com"],
    "public_url": "http://127.0.0.1:$PORT",
    "session_cookie_secure": "auto",
    "oauth": {
      "provider": "oidc",
      "issuer_url": "http://127.0.0.1:$IDP",
      "allow_insecure_issuer": true,
      "client_id": "\${env:OIDC_CLIENT_ID}",
      "client_secret": "\${env:OIDC_CLIENT_SECRET}",
      "scopes": ["openid", "profile", "email", "groups"],
      "groups_claim": "groups",
      "email_verified_policy": "refuse_false",
      "display_name": "Example Corp"
    },
    "access": {"group_servers": {"eng": ["a"], "ops": ["a", "b"]}, "default_servers": []}
  }
}
EOF
```

`require_mcp_auth: false` is deliberate: US3.5/FR-029 assert the server edition overrides it (boot notice + `doctor` finding + `/mcp` 401 without a credential). The stdio fixture is `tests/echo-rugpull-server` (`index.js`, one deterministic `echo` tool while `DESC_FILE` is unset — the repo has no `test/fixtures/echo-server`; run `npm ci --prefix tests/echo-rugpull-server` once); the two-fixture oracle swaps this file for one without `b` and `a__b`.

## 3. Run

```bash
"$ROOT/mcpproxy-server" serve --config "$ROOT/mcp_config.json" --data-dir "$ROOT" --log-level=debug > "$ROOT/main.log" 2>&1 &  MP_PID=$!
until curl -sf "http://127.0.0.1:$PORT/readyz" >/dev/null; do sleep 0.5; done
grep -E 'require_mcp_auth.*overrid|public_url|audit_log.*enabled' "$ROOT/main.log"
API_KEY=$(jq -r .api_key "$ROOT/mcp_config.json")     # operator key; tenants never see it
```

## 4. Headless login (Alice) — no browser, no API key

```bash
J="$ROOT/alice.jar"
# 4a. /auth/login stores state+nonce server-side and 302s to the IdP's authorization_endpoint
AUTH=$(curl -s -o /dev/null -w '%{redirect_url}' -c "$J" "http://127.0.0.1:$PORT/api/v1/auth/login?redirect_uri=/my/tokens")
echo "$AUTH" | grep -Eq 'code_challenge_method=S256' && echo "$AUTH" | grep -q 'nonce='          # FR-021
# 4b. submit the fake IdP's form; do NOT follow the redirect
CB=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST "$AUTH" \
   --data-urlencode username=alice@example.com --data-urlencode password=pass --data consent=on --data action=approve)
# 4c. complete the callback; the session cookie lands in the jar
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' -b "$J" -c "$J" "$CB"     # 302 -> /my/tokens
curl -s -b "$J" "http://127.0.0.1:$PORT/api/v1/auth/me" | jq -e '.email=="alice@example.com" and .groups==["eng"]'
grep -c 'mcpproxy_session' "$J"                                                    # 1
```

Open-redirect check (US2.7): repeat 4a with `redirect_uri=https://evil.example/` and assert 4c redirects to `/ui/`.

## 5. Tenant principal on core REST and the Web UI probe (PR-C)

```bash
curl -s "http://127.0.0.1:$PORT/api/v1/auth/provider" | jq -e '.display_name=="Example Corp" and (keys|length==1)'   # FR-030
curl -s -b "$J" "http://127.0.0.1:$PORT/api/v1/servers" | jq -e '[.servers[].name]==["a"]'       # entitlement-filtered
curl -s -b "$J" "http://127.0.0.1:$PORT/api/v1/user/servers" | jq -e '[.shared[].name]==["a"]'
curl -s -o /dev/null -w '%{http_code}\n' -b "$J" "http://127.0.0.1:$PORT/api/v1/config"           # 403 (allowlist)
curl -s -o /dev/null -w '%{http_code}\n' -b "$J" -X POST -d '{}' "http://127.0.0.1:$PORT/api/v1/tools/call"   # 403, before body parse
curl -s -o /dev/null -w '%{http_code}\n' -b "$J" -H 'X-API-Key: wrong' "http://127.0.0.1:$PORT/api/v1/status"  # 401 (FR-001 precedence)
```

## 6. Mint an agent token as Alice, call `/mcp`, read the audit line

```bash
TOK=$(curl -s -b "$J" -X POST "http://127.0.0.1:$PORT/api/v1/user/tokens" -H 'Content-Type: application/json' \
   -d '{"name":"t1","allowed_servers":["*"],"permissions":["read"],"expires_in":"720h"}' | jq -r .raw_token)
curl -s -b "$J" "http://127.0.0.1:$PORT/api/v1/user/tokens" | jq -e '.tokens[0].allowed_servers==["a"]'   # "*" materialised (US1.3)
# JWT/cookie on /mcp is 401 (FR-003); no credential is 401 (FR-029)
curl -s -o /dev/null -w '%{http_code}\n' -b "$J" -X POST "http://127.0.0.1:$PORT/mcp" -d '{}'                 # 401
curl -s -o /dev/null -w '%{http_code}\n'          -X POST "http://127.0.0.1:$PORT/mcp" -d '{}'                 # 401
# initialise + one allowed call + one hidden call through the token
mcp() { curl -s -X POST "http://127.0.0.1:$PORT/mcp" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' "$@"; }
mcp -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"quickstart","version":"1"}}}' -D "$ROOT/init.h" >/dev/null
SID=$(grep -i '^mcp-session-id' "$ROOT/init.h" | tr -d '\r' | cut -d' ' -f2)
mcp -H "Mcp-Session-Id: $SID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"a:echo","args":{"text":"hello","secret":"AKIAQUICKSTART7SENTINEL0"}}}}' | jq -c .result.isError
mcp -H "Mcp-Session-Id: $SID" -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"b:echo","args":{}}}}' | jq -r '.result.content[0].text'   # non-disclosing refusal
# audit lines (PR-D): one authz allow + one tool_call for a:echo, one authz deny for b:echo, no secret bytes
tail -n 3 "$ROOT/audit.jsonl" | jq -c '{event,decision,outcome,server,tool,reason,disclosed,caller:.caller.kind,email:.caller.user_email,rid:.request_id}'
grep -c 'AKIAQUICKSTART7SENTINEL0' "$ROOT/audit.jsonl"   # 0
grep -c '"event":"auth_event"' "$ROOT/audit.jsonl"       # ≥1 (Alice's login, reason ok)
MCPPROXY_AUDIT_JSONL="$ROOT/audit.jsonl" go test ./internal/audit -run TestExternalJSONLValidates -count=1   # validates every line with the in-repo santhosh-tekuri/jsonschema (no npx, no network)
```

Correlate: `"$ROOT/mcpproxy-server" activity list --url "http://127.0.0.1:$PORT" --api-key "$API_KEY" --request-id <rid from the line>` — MCP ids are locally minted (no `X-Request-Id` on `/mcp`, critic C8), so the join key is the line's `request_id`, never an ingress header.

## 7. Hot reload and the freshness bound

```bash
jq '.server_edition.access.group_servers.eng=[]' "$ROOT/mcp_config.json" > "$ROOT/c.tmp" && mv "$ROOT/c.tmp" "$ROOT/mcp_config.json"
sleep 2; curl -s -b "$J" "http://127.0.0.1:$PORT/api/v1/servers" | jq -e '.servers==[]'          # narrowed, no restart, no rotation
mcp -H "Mcp-Session-Id: $SID" -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"retrieve_tools","arguments":{"query":"echo"}}}' | jq -e '.result.content[0].text|test("a:echo")|not'
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $(curl -s -b "$J" -X POST http://127.0.0.1:$PORT/api/v1/auth/token | jq -r .token)" -X POST "http://127.0.0.1:$PORT/api/v1/auth/token"   # 401 — a JWT cannot renew itself (FR-011)
```

## 8. Two-tenant token cap (US6, PR-A)

Seed 100 tokens for Alice through `/user/tokens` in a loop; the 101st is 409 whose body names *her* count; Bob's first mint succeeds; an ownerless operator mint via `X-API-Key` succeeds.

## 9. Personal-build round trip (US5, PR-A)

```bash
go build -o "$ROOT/mcpproxy" ./cmd/mcpproxy
cp "$ROOT/mcp_config.json" "$ROOT/before.json"
"$ROOT/mcpproxy" serve --config "$ROOT/mcp_config.json" --data-dir "$ROOT" --listen 127.0.0.1:$((PORT+1)) & P=$!; sleep 3; kill $P
python3 - "$ROOT/before.json" "$ROOT/mcp_config.json" <<'PY'
import json,sys
a,b=[json.load(open(p),parse_float=str,parse_int=str) for p in sys.argv[1:]]
assert a["server_edition"]==b["server_edition"], "server_edition block changed on personal write-back"
PY
```

## 10. Teardown

```bash
kill $MP_PID $IDP_PID; rm -rf "$ROOT"
```

## Playwright (US4, PR-C)

`e2e/playwright/server-edition-tenant.spec.ts` drives the same rig: fresh context → `/ui/` → `/login` (button labelled `Example Corp`) → fake IdP form → dashboard → server list (`a` only) → token mint → activity (served by `/user/activity`); asserts no XHR carries `?apikey=`/`X-API-Key`, every XHR is 2xx, and then walks the FR-045 refused-route list with `page.request` asserting 403. Reuse `e2e/playwright/node_modules`, pin the Chromium build, `data-test` selectors, `domcontentloaded` (`docs/development/web-ui-verification.md:29-70`). Synthetic keypresses do not reach document listeners — assert state via the DOM, not via Escape (`feedback_ui_verify_escape_false_negative`).
