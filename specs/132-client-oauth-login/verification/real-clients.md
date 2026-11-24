# Real-client check (Spec 132, T044, research R13)

Date: 2026-11-23. Build: `feat/client-oauth-login-e2e` (slices 1a-1e), scratch core on `127.0.0.1:19551`, scratch data dir, `mcp_oauth_enabled` on, plain HTTP.

**Constraint of this run:** no real client's configuration was touched and no human was present. The consent step was done through the admin REST (`approve`), not a browser page (there is none in PR-1).

## Result summary

| Client | Status | Evidence |
|---|---|---|
| MCP TypeScript SDK 1.32.2 (`@modelcontextprotocol/sdk`, the OAuth client code that MCP clients build on), driven by `sdk-client.mjs` | **VERIFIED** over plain HTTP | below |
| MCP Inspector CLI 2.13.0 | **NOT verified** | it needs a TTY and a browser for OAuth (`auth_required: Interactive OAuth requires a TTY ... or MCP_AUTO_OPEN_ENABLED=true`); a pty wrapper produced no request to the core within 20 s; not pursued further |
| Claude Code CLI 2.1.318 | **VERIFIED** for the login flow over plain HTTP loopback (see "Claude Code observations"); refresh, metadata order, browser callback path and in-session tool calls NOT verified | below |
| Cursor | **NOT verified** | needs its real config and a GUI |
| VS Code | **NOT verified** | needs its real settings and a GUI |

`SupportsBrowserLogin` (PR-3) may be set for `claude-code` only; it must stay unset for Cursor and VS Code (and Inspector) until the manual checklist below is done for each. The SDK result alone does not prove that a product accepts an `http://127.0.0.1` issuer; the Claude Code result below does for Claude Code.

**R13 go/no-go:** the condition "at least one of the four completes the flow over plain HTTP loopback" is MET by Claude Code. Cursor, VS Code and MCP Inspector remain unverified; keep their checklist items.

## Claude Code observations

Run by the main session on 2026-11-23, Claude Code CLI 2.1.318, macOS, against an isolated scratch core built from 44ac406a7 (explicit `--config`, scratch HOME, `--data-dir`, `--listen 127.0.0.1:18431`, `MCPPROXY_*` cleared, `require_mcp_auth` true, web UI off, `mcp_oauth_enabled` via env).

- Added with `claude mcp add --scope local --transport http mcpproxy-oauth-check http://127.0.0.1:18431/mcp/oauth/claude-code` from a scratch project dir; removed afterwards (`claude mcp logout`, `claude mcp remove -s local`).
- `claude mcp login --no-browser <name>` needs a TTY (without one it prints the authorize URL then exits 1: "stdin isn't a terminal"). It was run under a pseudo-terminal and the redirect URL was pasted back at its "Or paste the redirect URL here:" prompt. The browser/loopback-callback path (login without `--no-browser`) was NOT exercised.
- Observed: Claude Code accepted the plain-HTTP `http://127.0.0.1` issuer; it registered dynamically (same `client_id` reused across three login attempts). The authorize request carried `response_type=code`, `code_challenge_method=S256`, `state`, `resource=http://127.0.0.1:18431/mcp/oauth/claude-code` (exactly the per-client address), `redirect_uri` `http://localhost:<port>/callback` (host `localhost`, fixed port across attempts in this run: 65306), and no `scope`.
- Server side: authorize -> 302 `/ui/authorize?request=<id>`; the request showed `address_kind` "client", client id `claude-code`, `reported_name` "claude code (mcpproxy-oauth-check)" (i.e. "Claude Code (<server name>)" lower-cased); approved through the admin REST with profile "" / mode switchable; the client printed 'Authenticated with "mcpproxy-oauth-check". Its tools are now available in Claude Code.' and exited 0.
- After login `claude mcp get` showed "Status: Connected"; the client row had one `browser_login` credential, state active, `redirect_host` "localhost", `observed_name` "claude-code", `name_mismatch` false, `last_used_at` set; the server log shows POST and GET (stream) requests on `/mcp/oauth/claude-code`.
- NOT verified for Claude Code: token refresh (no expiry was waited for or forced), the order of metadata requests (those endpoints are not in the request log), the real browser + loopback callback path, behaviour inside an interactive Claude Code session (tool calls), the generic `/mcp/oauth` address, Windows/Linux.
- Note printed by the client, recorded verbatim without interpretation: "[mcp-sdk] SEP-2352: stored OAuth credential has no 'issuer' stamp (pre-upgrade storage or provider not round-tripping the value). SEP-2352 isolation is inactive for this read; ensure your provider round-trips the issuer field."

## SDK client observations (`node sdk-client.mjs <address> <redirect_uri> <name>`)

Run against `/mcp/oauth/cursor` (redirect `http://127.0.0.1:54321/callback`), `/mcp/oauth` (redirect `http://localhost:54322/callback`) and `/mcp/oauth/claude-code` (redirect `http://localhost:54323/callback`). All three completed: discovery, registration, authorize, (admin) approve, exchange, `initialize` + `tools/list` (10 tools), and a refresh.

Request order the SDK sent, identical for each address:

1. `POST <address>` (`initialize`, no token) -> `401` with the `WWW-Authenticate` challenge.
2. `GET /.well-known/oauth-protected-resource/mcp/oauth[/<client>]` -> `200` (it used the **per-address** metadata path, taken from the challenge).
3. `GET /.well-known/oauth-authorization-server` -> `200`.
4. `POST /oauth/register` -> `201`. Body: `client_name`, `redirect_uris`, `grant_types: [authorization_code, refresh_token]`, `response_types: [code]`, `token_endpoint_auth_method: none`. No `software_id`.
5. Authorize URL parameters: `response_type=code`, `client_id`, `code_challenge`, `code_challenge_method=S256`, `redirect_uri`, **`resource=<origin>/mcp/oauth[/<client>]`**. It sent **no `state` and no `scope`**.
6. `POST /oauth/token` (`authorization_code`) with `code`, `code_verifier`, `redirect_uri`, **`resource`**, `client_id` -> `200`, `token_type: Bearer`, `expires_in: 3600`, refresh token present.
7. Session: `initialize` -> `200`, `notifications/initialized` -> `202`, `GET` SSE stream `200`, `tools/list` -> `200`.
8. (Re-run 2026-11-23 with the hardened harness: it now exits non-zero unless a `refresh_token` grant answered 200, the access token changed and a following `tools/list` returned tools. All three addresses exited 0 with that proof.) With an invalid access token the SDK re-ran steps 1-3 (metadata fetched again), refreshed with `grant_type=refresh_token` + `resource` + `client_id` -> `200`, and continued.

Findings that matter for the design:

- The SDK **sends `resource`** on authorize, exchange and refresh, and the value equals the address it connected to (per-client path included). The per-client protected-resource metadata path is followed.
- Redirect URI shapes registered: `http://127.0.0.1:<port>/callback` and `http://localhost:<port>/callback`; both accepted.
- The sanitised reported name is lower-cased (`Cursor IDE` -> `cursor ide`); the consent request shows it as the unverified name.
- `initialize` `clientInfo.name` is recorded as `observed_name` on the login.
- Not exercised: a client that sends `state` (the script and the e2e script do), a client that omits `resource`.

## Manual checklist (needs a human, not done)

For each of Cursor, VS Code, MCP Inspector (web UI) (and, for the items marked NOT verified above, Claude Code), with an isolated scratch core (below) and the client configured with `http://127.0.0.1:18080/mcp/oauth/<client-id>` (back up the client config first):

1. Start the client; does it show a sign-in step instead of failing on the 401?
2. In the core log or with `GET /api/v1/oauth/authorizations`, note: name it registered (`reported_name`), redirect URI shape, whether `resource` was sent, and the metadata URLs it fetched in order.
3. Approve with `POST /api/v1/oauth/authorizations/<id>/approve`; does the client finish and list tools?
4. Refresh: either leave the client past real access-token expiry (1 h), or invalidate only its cached access token (as `sdk-client.mjs` does) and keep the refresh token. Restarting the client proves nothing (it may reuse the stored access token). Pass only if the core log or a proxy shows a successful `POST /oauth/token` with `grant_type=refresh_token` (200), followed by a working request, with no new browser step.
5. Revoke the login (`DELETE /api/v1/clients/<id>/logins/<grant_id>`); does the client prompt to sign in again?
6. Record: accepts an `http://127.0.0.1` issuer (yes/no), follows per-client metadata (yes/no).

Isolated scratch core (the same isolation as `scripts/test-client-oauth-e2e.sh`; never omit `--config`, a bare `--data-dir` can pick up a legacy `~/.mcpproxy/mcp_config.json` and save to it):

```bash
S="$(mktemp -d)"; mkdir -p "$S/data" "$S/home"
cat > "$S/config.json" <<CONF
{ "listen": "127.0.0.1:18080", "data_dir": "$S/data", "api_key": "scratch-key",
  "enable_socket": false, "enable_web_ui": false, "mcpServers": [] }
CONF
# clear every MCPPROXY_*/MCPP_* override, then set only what this check needs
env $(compgen -e | grep -E '^(MCPPROXY_|MCPP_)' | sed 's/^/-u /') \
  HOME="$S/home" MCPPROXY_TELEMETRY=false MCPPROXY_MCP_OAUTH_ENABLED=true \
  ./mcpproxy serve --config "$S/config.json" --data-dir "$S/data" --listen 127.0.0.1:18080
```

If none of the four accepts a plain-HTTP loopback issuer, stop before PR-2 (research R13 fallback: serve the OAuth endpoints only when local TLS is on).
