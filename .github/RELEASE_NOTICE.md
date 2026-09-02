## ⚠️ Security release — please read before upgrading

This release fixes credential-exposure and access-control defects that affect **all previously released versions** of MCPProxy. Two of them mean shipped versions were weaker than the documentation described. If you have used agent tokens, or connected an OAuth upstream, please read the remediation steps below.

### What was wrong

**Any agent token could obtain the admin API key** ([#1180](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1180))
`GET /api/v1/info` returned the global admin API key in its `web_ui_url` field to any authenticated caller, including a read-only token scoped to a single server. The holder could then act as an administrator.

The practical consequence: **`allowed_servers` was not a security boundary.** A scoped token never had to defeat the scope checks — it could read the admin key and stop being scoped. Treat any agent token you have issued as having had administrator-equivalent access.

**Upstream credentials were served to unprivileged callers** ([#1167](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1167))
With the opt-in `reveal_secret_headers` setting enabled, the MCP door correctly required an authenticated administrator, but the REST, SSE and diagnostics doors checked the flag alone. A read-only scoped token received upstream `Authorization` headers, URL query tokens, command-line arguments and environment values in plaintext from `GET /api/v1/servers`, `GET /api/v1/config` and the `/events` stream.

**Token scope was ignored on REST enumeration** ([#1166](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1166))
The MCP surface filtered by `allowed_servers`; the REST surface did not. Beyond server names, this exposed `GET /api/v1/servers/{id}/logs` — which serves an upstream server's own stdout/stderr, and therefore anything that server prints — and the activity and tool-call endpoints, which carry tool-call **arguments and responses** for every configured server.

**Credentials were written to the log** ([#1158](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1158))
By severity of exposure, and by the log level required:

| Level | What was logged |
|---|---|
| **`info` (the default)** | OAuth **authorization codes**, the full OAuth callback query string, and OAuth authorization URLs — which embed the configured upstream URL, including any credential in its query string. Also printed to stdout. |
| `debug` | Upstream command-line credentials (`--api-key …`), upstream URL query tokens, and — under Docker isolation, where `env` is converted to `-e KEY=VALUE` arguments — upstream **environment** secrets. |
| `trace` | `Authorization` and `Cookie` headers, request and response bodies (including OAuth token responses), and raw SSE frames. |

**No non-default configuration was required for the `info` row.** Anyone who has connected an OAuth upstream has authorization codes in `main.log` and in terminal scrollback. Authorization codes are single-use and short-lived, so the durable risk is the authorization-URL line: if an upstream URL carries a static token, that token is in the log of every OAuth-enabled install.

### Server edition

- **Admin demotion did not take effect** ([#1169](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1169)) — removing an address from `admin_emails` left the user with full administrator access, including secret reveal, until their session token expired (default 24 hours). Disabling the account did work; demotion alone did not.
- **Agent-token names were a global namespace** ([#1168](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1168)) — two tenants could not both use the same token name, and the differing responses let any user probe for other users' token names. Agent tokens also carried no tenant identity.
- **A disabled user's agent tokens kept working.** Disabling an account is the documented remediation for a compromised user, and it did not revoke that user's minted tokens.
- **Requested `allowed_servers` was not checked against entitlement**, so a tenant could mint a token scoped to servers they were not entitled to see.

### What you should do

1. **Rotate your MCPProxy API key.** Remove `api_key` from `~/.mcpproxy/mcp_config.json` and restart to have a new one generated, or set a new value directly.
2. **Revoke and re-mint every agent token** (`mcpproxy token list`, then `mcpproxy token revoke <name>`). Any token issued before this release should be assumed to have had administrator-equivalent reach.
3. **Rotate upstream credentials that appeared in logs.** In order of likelihood: any credential embedded in an upstream server URL; any credential passed as a command-line argument; and, if you ran with Docker isolation at `debug`, upstream environment secrets.
4. **Treat existing log files as secret-bearing.** `~/.mcpproxy/logs/*.log` on Linux, `~/Library/Logs/mcpproxy/*.log` on macOS, `%LOCALAPPDATA%\mcpproxy\logs\*.log` on Windows. Delete them or restrict access; note they may also exist in backups.
5. **Server edition only:** re-check `admin_emails` against who should actually hold admin, and revoke tokens belonging to any disabled account.

Redaction preserves diagnostics — flag names, hosts, paths and parameter names still appear in logs, so debugging a connection is unaffected. Only the credential values are masked.

### Reporting

Security issues can be reported privately through [GitHub Security Advisories](https://github.com/smart-mcp-proxy/mcpproxy-go/security/advisories/new).
