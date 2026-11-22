# Quickstart: verifying browser login

Use a scratch data directory and a high port so the installed MCPProxy is untouched. Kill any core using the same data directory first.

```bash
go build -o mcpproxy ./cmd/mcpproxy
```

## 1. Default off: nothing changed

```bash
./scripts/test-api-e2e.sh
```

```bash
go test -race ./internal/server -run 'TestOAuthLogin_DefaultOff' -v
```

Expected: both pass; the second compares `/mcp/oauth*`, `/oauth/*` and `/.well-known/oauth-*` with the previous handlers and checks that no new bucket exists.

## 2. The whole flow, scripted

```bash
./scripts/test-client-oauth-e2e.sh
```

It starts a core with `MCPPROXY_MCP_OAUTH_ENABLED=true`, then with a scripted public client:

1. `401` challenge and both metadata documents for `/mcp/oauth/cursor`.
2. Registration, authorize, approve through the admin REST, token exchange, `tools/list`.
3. Refresh rotation; replay of the old refresh token revokes the login; a forged token with the real grant id changes nothing.
4. Token used on `/mcp`, on `/mcp/oauth/claude-code` and on `localhost` instead of `127.0.0.1`: refused.
5. Double approve; approve after the binding changed; named profile with `require_mcp_auth` off (guard refusal).
6. Revoke one login on an open session; forget the client.
7. Feature off after use: `/mcp/oauth/cursor` is `404`; on again: the remaining login works.

## 3. By hand

```bash
MCPPROXY_MCP_OAUTH_ENABLED=true ./mcpproxy serve --listen 127.0.0.1:18080 --data-dir "$(mktemp -d)"
```

Configure a client with `http://127.0.0.1:18080/mcp/oauth/<client-id>`, start it, approve on the page that opens, then check **Clients** in the Web UI.

## 4. Real clients (SC-005)

For Claude Code, Cursor, VS Code and MCP Inspector, record in `verification/real-clients.md`: the name it registered with, redirect URI shape, whether discovery on the per-client path, exchange and refresh worked. Only clients that pass get `SupportsBrowserLogin`.

## 5. Surfaces (PR-2, PR-3)

- Web UI: Playwright sweep for `/ui/authorize`, `/ui/clients`, `/ui/settings` per `docs/development/web-ui-verification.md`.
- macOS tray: start a sign-in from a browser profile without the key; the menu shows "Approve sign-in for …".
- Connect: `./mcpproxy connect cursor --auth oauth` writes only the address; the preview shows no header.
