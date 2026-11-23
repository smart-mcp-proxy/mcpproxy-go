# Real-client check (Spec 132, T044, research R13)

Date: 2026-11-23. Build: `feat/client-oauth-login-e2e` (slices 1a-1e), scratch core on `127.0.0.1:19551`, scratch data dir, `mcp_oauth_enabled` on, plain HTTP.

**Constraint of this run:** no real client's configuration was touched and no human was present. The consent step was done through the admin REST (`approve`), not a browser page (there is none in PR-1).

## Result summary

| Client | Status | Evidence |
|---|---|---|
| MCP TypeScript SDK 1.32.2 (`@modelcontextprotocol/sdk`, the OAuth client code that MCP clients build on), driven by `sdk-client.mjs` | **VERIFIED** over plain HTTP | below |
| MCP Inspector CLI 2.13.0 | **NOT verified** | it needs a TTY and a browser for OAuth (`auth_required: Interactive OAuth requires a TTY ... or MCP_AUTO_OPEN_ENABLED=true`); a pty wrapper produced no request to the core within 20 s; not pursued further |
| Claude Code | **NOT verified** | needs `claude mcp add` / config edits and an interactive browser step |
| Cursor | **NOT verified** | needs its real config and a GUI |
| VS Code | **NOT verified** | needs its real settings and a GUI |

`SupportsBrowserLogin` (PR-3) must stay unset for Claude Code, Cursor and VS Code until the manual checklist below is done for each. Note: the SDK result shows the server side works over plain HTTP for a standards-following client; it does not prove that any of the products above accepts an `http://127.0.0.1` issuer.

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
8. With an invalid access token the SDK re-ran steps 1-3 (metadata fetched again), refreshed with `grant_type=refresh_token` + `resource` + `client_id` -> `200`, and continued.

Findings that matter for the design:

- The SDK **sends `resource`** on authorize, exchange and refresh, and the value equals the address it connected to (per-client path included). The per-client protected-resource metadata path is followed.
- Redirect URI shapes registered: `http://127.0.0.1:<port>/callback` and `http://localhost:<port>/callback`; both accepted.
- The sanitised reported name is lower-cased (`Cursor IDE` -> `cursor ide`); the consent request shows it as the unverified name.
- `initialize` `clientInfo.name` is recorded as `observed_name` on the login.
- Not exercised: a client that sends `state` (the script and the e2e script do), a client that omits `resource`.

## Manual checklist (needs a human, not done)

For each of Claude Code, Cursor, VS Code, MCP Inspector (web UI), with a scratch core (`MCPPROXY_MCP_OAUTH_ENABLED=true ./mcpproxy serve --listen 127.0.0.1:18080 --data-dir "$(mktemp -d)"`) and the client configured with `http://127.0.0.1:18080/mcp/oauth/<client-id>` (back up the client config first):

1. Start the client; does it show a sign-in step instead of failing on the 401?
2. In the core log or with `GET /api/v1/oauth/authorizations`, note: name it registered (`reported_name`), redirect URI shape, whether `resource` was sent, and the metadata URLs it fetched in order.
3. Approve with `POST /api/v1/oauth/authorizations/<id>/approve`; does the client finish and list tools?
4. Leave it past access-token expiry (1 h) or revoke the access token path by restarting the client; does it refresh without a new browser step?
5. Revoke the login (`DELETE /api/v1/clients/<id>/logins/<grant_id>`); does the client prompt to sign in again?
6. Record: accepts an `http://127.0.0.1` issuer (yes/no), follows per-client metadata (yes/no).

If none of the four accepts a plain-HTTP loopback issuer, stop before PR-2 (research R13 fallback: serve the OAuth endpoints only when local TLS is on).
