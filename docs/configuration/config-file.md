---
id: config-file
title: Configuration File
sidebar_label: Config File
sidebar_position: 1
description: Complete reference for mcp_config.json
keywords: [config, configuration, mcp_config.json, settings]
---

# Configuration File

MCPProxy uses a JSON configuration file located at `~/.mcpproxy/mcp_config.json`.

## Location

| Platform | Default Location |
|----------|-----------------|
| macOS | `~/.mcpproxy/mcp_config.json` |
| Linux | `~/.mcpproxy/mcp_config.json` |
| Windows | `%USERPROFILE%\.mcpproxy\mcp_config.json` |

## Complete Reference

```json
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "api_key": "your-secret-api-key",
  "enable_socket": true,
  "health_check_interval": "30s",
  "tool_discovery_interval": "5m",
  "http_read_timeout": "120s",
  "http_write_timeout": "120s",
  "http_idle_timeout": "180s",
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  "code_execution_max_parallel": 8,
  "features": {
    "enable_web_ui": true
  },
  "update_check": {
    "enabled": true,
    "channel": "stable"
  },
  "mcpServers": []
}
```

## Options

### Server Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `listen` | string | `127.0.0.1:8080` | Address and port to listen on |
| `data_dir` | string | `~/.mcpproxy` | Directory for data storage |
| `api_key` | string | auto-generated | API key for REST API authentication |
| `trusted_hosts` | string[] | `[]` | Non-loopback `Host` header values accepted on a loopback listener. Needed when running behind a reverse proxy — see [Reverse Proxy Deployment](/operations/reverse-proxy). This is DNS-rebinding protection only; it is **not** the control for forwarded headers (see `trusted_proxies`) |
| `trusted_proxies` | string[] | `[]` (trust nobody) | CIDRs or IP addresses whose `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto` and `X-Forwarded-Host` headers are believed. Headers from any other peer are ignored and the direct `RemoteAddr` is used. Env `MCPPROXY_TRUSTED_PROXIES` (comma list). Live (hot-reload, no restart). Validation: `trusted_proxies[N] "value" is not a valid CIDR or IP address` — refused identically at boot, `PATCH /api/v1/config` and `/config/apply`. See [Reverse Proxy Deployment](/operations/reverse-proxy#trusted_proxies-forwarded-headers) |
| `require_mcp_auth` | boolean | `false` | Require an API key on the `/mcp` endpoint (off by default for client compatibility). Enable when exposing MCPProxy beyond localhost. **Server edition:** forced to `true` whenever `server_edition.enabled` is `true` — an explicit `false` is not an error, but boot logs `require_mcp_auth: false is overridden to true because server_edition.enabled is true` and `mcpproxy doctor` reports the same finding |
| `enable_socket` | boolean | `true` | Enable Unix socket/named pipe for local communication |

### HTTP Server Timeouts

Deadlines applied to MCPProxy's own HTTP listener (REST API, `/mcp`, `/events`).
Each accepts a duration string; **`"0s"` means "no timeout"** (not "use the
default" — omit the key for that; for `http_idle_timeout`, `"0s"` falls back to
the read timeout — see its row). Valid range: `1s`–`24h`, or `0s`.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `http_read_timeout` | duration | `"120s"` | Deadline for reading the whole request (headers + body) |
| `http_write_timeout` | duration | `"120s"` | Wall-clock cap on writing the whole response, counted from when the request headers were read. Governs **non-streaming endpoints only** (REST API, Web UI, health); MCP endpoints and SSE `/events` are exempt by design. `"0s"` disables it globally ([#965](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/965)) |
| `http_idle_timeout` | duration | `"180s"` | Keep-alive timeout for idle persistent connections (`"0s"` falls back to the read timeout; unbounded only if that is also `"0s"`) |

- **Streaming routes are exempt from `http_write_timeout`.** The MCP endpoints (`/mcp*`, plus the legacy `/v1/tool_code` and `/v1/tool-code` aliases) and `/events` clear their own per-request write deadline (and, being body-less GETs, their read deadline), so a slow tool call or a long-lived SSE stream is never truncated. You do not need to disable the deadline to run long tool calls.
- **Restart required.** These are baked into the HTTP server when it binds, so a change is reported as restart-required, not hot-reloaded.
- **Slowloris protection is unaffected** — the 60s request-header read deadline is hardcoded and not configurable.
- **Long tool calls need `call_tool_timeout`.** It (default `2m`) separately caps tool execution; raise it when you expect tool calls longer than two minutes.
- Environment overrides: `MCPPROXY_HTTP_READ_TIMEOUT`, `MCPPROXY_HTTP_WRITE_TIMEOUT`, `MCPPROXY_HTTP_IDLE_TIMEOUT`.

### Feature Flags

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `features.enable_web_ui` | boolean | `true` | Enable the web management interface |

### Tool Discovery Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `tools_limit` | integer | `15` | Maximum tools to return in a single request |
| `tool_response_limit` | integer | `20000` | Maximum characters in tool response |

### Tool Discovery & Health Check Intervals

MCPProxy keeps upstream connections fresh with two independent background loops:

- a lightweight **liveness probe** that sends a standard MCP `ping` to confirm the connection is alive, and
- a periodic **tool-discovery sweep** that re-lists tools to rebuild the search index. (Tool changes are also picked up reactively via `notifications/tools/list_changed`; the sweep is a fallback for servers that don't advertise `listChanged`.)

Both cadences are configurable globally, and can be overridden per server (see [Upstream Servers](/configuration/upstream-servers)). Values are [duration strings](https://pkg.go.dev/time#ParseDuration) such as `30s`, `5m`, or `1h`.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `health_check_interval` | duration | `30s` | Cadence of the lightweight liveness `ping`. Accepts `0s` or `5s`–`1h`. `0s` disables the probe. |
| `tool_discovery_interval` | duration | `5m` | Cadence of the periodic `tools/list` re-index sweep. Accepts `0s` or `30s`–`24h`. `0s` disables the sweep. |

**Resolution order**: per-server value → global value → built-in default. Leaving a key unset preserves the previous behaviour, so existing configs are unaffected by an upgrade.

```json
{
  "health_check_interval": "30s",
  "tool_discovery_interval": "5m",
  "mcpServers": [
    {
      "name": "chatty-server",
      "health_check_interval": "2m",
      "tool_discovery_interval": "0s"
    }
  ]
}
```

**Notes:**

- **`0s` = disabled.** Disabling the discovery sweep for a server that does **not** support `listChanged` means tool changes are only picked up on (re)connect — fine for static servers, worth knowing for dynamic ones. With the liveness probe disabled, a dead transport is detected lazily (on the next real tool call or discovery sweep) rather than proactively.
- **Docker-isolated servers**: `health_check_interval` is a **no-op** — their liveness is monitored at the container level, not via MCP `ping`. `tool_discovery_interval` still applies. Remote (HTTP/SSE) servers benefit most from the `ping`-based probe.
- **Hot reload**: interval changes take effect on the next cycle without a full restart.
- These intervals are also editable in the Web UI and macOS app under **Settings → Advanced → Tool discovery & health checks**.

### Code Execution Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_code_execution` | boolean | `true` | Enable JavaScript code execution tool (on by default since v0.66.0) |
| `code_execution_timeout_ms` | integer | `120000` | Execution timeout in milliseconds |
| `code_execution_max_tool_calls` | integer | `0` | Maximum tool calls (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | VM pool size for code execution |
| `code_execution_max_parallel` | integer | `8` | Default concurrency for `call_tools()` batches (1-32) |

Scripts call one tool at a time with `call_tool(server, tool, args)`, or fan out
independent calls with `call_tools(requests, options)`:

```javascript
var slots = call_tools([
  {server: "github", tool: "get_pull_request", args: {owner: "acme", repo: "api", pullNumber: 1}},
  {server: "github", tool: "get_pull_request", args: {owner: "acme", repo: "api", pullNumber: 2}}
], {max_parallel: 5});
// slots[i] is {ok: true, result} or {ok: false, error} for requests[i], in input order
```

`requests` takes up to 100 elements and each one costs a unit of
`code_execution_max_tool_calls`. Concurrency precedence is `options.max_parallel`
(1-32) > `code_execution_max_parallel` > built-in 8. The whole batch lives inside
the execution timeout. Changes to these keys hot-reload and apply to executions
that start afterwards.

**Batching vs. per-server limits.** The [concurrency limits](#concurrency-limits--request-queueing)
below still govern every element. A server with `max_concurrent_requests` set and
**no** `queue_size` sheds everything past the cap, so a 10-element batch against
`max_concurrent_requests: 1` comes back as 1 result and 9 per-slot `queue_full`
errors. Give such servers `queue_size` headroom (or lower `max_parallel`) before
fanning out against them.

### Update Check Settings

Controls the background upgrade-awareness checker. Both keys are optional and
hot-reloadable (no restart needed).

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `update_check.enabled` | boolean | `true` | Master switch. When `false`, no network check runs (background poll and manual re-check) and no upgrade nudge appears on any surface — the `update` object is omitted from `/api/v1/info`. |
| `update_check.channel` | string | `"stable"` | Release channel: `"stable"` (prereleases never offered) or `"rc"` (prerelease tags like `v0.47.0-rc.1` included). |

The existing environment switches keep working and **win over** these keys:
`MCPPROXY_DISABLE_AUTO_UPDATE=true` force-disables checking, and
`MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` force-selects the prerelease channel.
They only widen in one direction — they cannot re-enable checking that the
config disabled. See [Version Updates](/features/version-updates) for where
updates are surfaced.

### Concurrency Limits & Request Queueing

Caps how many upstream tool calls may run at once, so a burst cannot overwhelm a
fragile upstream. **Off by default** — with no keys set there is no limiting, no
queueing and no new errors.

Three separately named scopes carry the same three settings:

| Scope | Where | What it caps |
|-------|-------|--------------|
| Global aggregate | top-level `max_concurrent_requests` / `queue_size` / `queue_timeout` | All upstream tool calls across the whole proxy |
| Per-server defaults | `server_concurrency_defaults` object | Blanket per-server values, inherited by servers that do not override them |
| Per-server override | the same three keys on an `mcpServers[]` entry | That one server |

```json
{
  "max_concurrent_requests": 50,
  "queue_size": 100,
  "queue_timeout": "30s",

  "server_concurrency_defaults": {
    "max_concurrent_requests": 5,
    "queue_size": 10
  },

  "mcpServers": [
    { "name": "fragile-db", "command": "db-mcp", "max_concurrent_requests": 1, "queue_size": 2 },
    { "name": "fast-api", "url": "https://api.example.com/mcp", "max_concurrent_requests": 0 }
  ]
}
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `max_concurrent_requests` | integer | unset (off) | Upstream tool calls allowed to run at once in this scope. `0` or unset = no limiter for this scope |
| `queue_size` | integer | `0` | How many calls may wait for a slot. `0` = shed immediately at the cap |
| `queue_timeout` | duration | `"30s"` when a limiter is active | How long a call may wait before being shed |

**Tri-state per-server semantics.** Each per-server key is independent: **absent**
inherits from `server_concurrency_defaults`, **`0`** disables that setting for
this server (`max_concurrent_requests: 0` opts the server out of per-server
limiting entirely), and a **positive** value overrides the default.

The global limiter is never an inheritance source — it applies on top, so a
server's effective concurrency is **min(per-server limit, global limit)**.
`queue_timeout` is one total wait budget across both tiers, not one per tier,
and queue waiting never eats into the call's execution timeout.

**Shedding.** A shed call gets a readable, retry-friendly error: an error tool
result for MCP calls, HTTP 429 with `Retry-After` for the REST tool-call
endpoint, and an activity record with the `rejected` status carrying the reason
(`queue_full` or `queue_timeout`) and scope (`server` or `global`). All limits
are hot-reloadable.

For stdio upstreams, start at `5` rather than `1`: the transport multiplexes and
most SDK servers use a small worker pool.

Full reference — validation rules, metrics, and which origins are limited —
lives in [`docs/configuration.md`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/configuration.md#concurrency-limits--request-queueing)
in the repository.

### Server Edition (`server_edition`) {#server-edition}

The `server_edition` block configures multi-user SSO in the **Server edition**
(`mcpproxy-server`, the Docker image / `.deb`). The Personal edition carries the
block as opaque JSON: it is preserved key-for-key and value-for-value through load, save and
`PATCH /api/v1/config`, never validated and never acted on. The block is not
part of the OpenAPI schema; this section is its reference. Development notes
and the REST endpoints live in
[Server Multi-User Authentication](/development/server-edition-multiuser-auth).

```json
{
  "listen": "0.0.0.0:8080",
  "trusted_proxies": ["10.42.0.0/16"],
  "server_edition": {
    "enabled": true,
    "admin_emails": ["admin@example.com"],
    "public_url": "https://mcp.example.com",
    "session_cookie_secure": "auto",
    "session_ttl": "24h",
    "bearer_token_ttl": "24h",
    "credential_encryption_key": "${env:MCPPROXY_CRED_KEY}",
    "oauth": {
      "provider": "oidc",
      "issuer_url": "https://login.example.com/realms/team",
      "client_id": "mcpproxy",
      "client_secret": "${env:OIDC_CLIENT_SECRET}",
      "scopes": ["openid", "profile", "email", "groups"],
      "groups_claim": "groups",
      "email_verified_policy": "refuse_false",
      "display_name": "Example SSO",
      "allowed_domains": ["example.com"]
    }
  }
}
```

**Reload column.** *Restart* keys are bound when the login handler and session
store are built: an edit is reported by the hot-reloader as
`server_edition` with `RequiresRestart=true` (`server_edition settings are
bound at startup`) and takes effect on the next start. *Live* keys apply on the
next request after the file reload lands. Validation messages are the exact
strings emitted at boot, by `PATCH /api/v1/config` and by `/config/apply`
(all three refuse the same input the same way).

| Key | Type | Default | Reload | Validation / notes |
|-----|------|---------|--------|--------------------|
| `enabled` | boolean | `false` | Restart | Turns the block on. When `true`: `admin_emails` and `oauth` become required, `/mcp` requires a credential regardless of `require_mcp_auth` (agent tokens, the API key and the socket are unchanged; a session cookie or user JWT is **never** an MCP credential) |
| `admin_emails` | string[] | — (required when enabled) | **Live** | `server_edition.admin_emails must contain at least one admin email`. Case-insensitive match; the single source of the admin role, re-derived on every request (a removed admin is demoted on their next request without re-login) |
| `public_url` | string | `""` | Restart | Absolute origin only — `server_edition.public_url must be an absolute origin (scheme://host[:port]) with no path`. Env alias `MCPPROXY_PUBLIC_URL` (the only nested `server_edition.*` key with one; the Personal edition ignores it). When set it is the **sole** source of the IdP `redirect_uri` (`<public_url>/api/v1/auth/callback`), of the connect-flow base URL and of the scheme behind the `Secure` cookie decision — `Host` and `X-Forwarded-*` are ignored for those. Unset on a non-loopback listener (the Docker image listens on `0.0.0.0:8080`) is a boot warning + `mcpproxy doctor` finding, never an error |
| `session_cookie_secure` | `auto` \| `true` \| `false` | `auto` | Restart | `auto` = `Secure` when the effective scheme is https (an https `public_url`, in-process TLS, or `X-Forwarded-Proto: https` from a peer in `trusted_proxies`). `false` with an https `public_url` or `tls.enabled` is refused: `server_edition.session_cookie_secure=false cannot be combined with an https public_url or tls.enabled`. An explicit `false` elsewhere is honoured with one boot warning + `doctor` finding (loopback/test deployments only). Any other value: `server_edition.session_cookie_secure must be one of: auto, true, false`. `HttpOnly` and `SameSite=Lax` are always set |
| `session_ttl` | duration | `24h` | Restart | `server_edition.session_ttl must be positive` |
| `bearer_token_ttl` | duration | `24h` | Restart | `server_edition.bearer_token_ttl must be positive`. Lifetime of user JWTs minted by `POST /api/v1/auth/token` (REST/CLI only) |
| `credential_encryption_key` | string | env `MCPPROXY_CRED_KEY` | Restart | Encrypts per-user upstream credentials at rest (`oauth_connect` broker). An explicit value wins over the environment. Secret — keep it as `${env:...}` or set only the variable; it never appears in the Settings UI |
| `store_idp_tokens` | boolean | `false` | — | **Deprecated no-op.** Accepted so older files load; `true` logs `server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it` once at load. See [IdP Token Storage](/features/idp-token-storage) |
| ~~`max_user_servers`~~, ~~`workspace_idle_timeout`~~ | — | — | never reported | **Removed.** A file that still carries them loads: each is dropped with one startup diagnostic, `server_edition.max_user_servers is no longer supported and was ignored` (likewise `workspace_idle_timeout`), and the next write-back omits them. `PATCH /api/v1/config` and `/config/apply` refuse them with the same text |
| `oauth` | object | — (required when enabled) | Restart | `server_edition.oauth configuration is required when server_edition is enabled`. Every `oauth.*` edit is restart-pinned (`server_edition.oauth.* is bound at login handler construction`) |
| `oauth.provider` | `google` \| `github` \| `microsoft` \| `oidc` | — | Restart | `server_edition.oauth.provider must be one of: google, github, microsoft, oidc (got: X)`. The three legacy providers keep their provider-specific exchange exactly as before (no nonce, no JWKS step, `client_secret_post`, groups always `[]`); `oidc` is the generic OpenID Connect Discovery provider with a verified ID token. The front-door keys (`public_url`, `session_cookie_secure`, `trusted_proxies`, forced MCP auth, subject binding, relative post-login redirect) apply to all four |
| `oauth.client_id` | string | — | Restart | `server_edition.oauth.client_id is required` |
| `oauth.client_secret` | string (`${env:...}`) | — | Restart | `server_edition.oauth.client_secret is required`. Masked in every API response and log; never a Settings row — reference a variable |
| `oauth.tenant_id` | string | `common` | Restart | `microsoft` only (multi-tenant `common` when unset); ignored by the other providers |
| `oauth.allowed_domains` | string[] | `[]` = allow all | Restart | Email-domain allowlist, matched case-insensitively after login. Applies to every provider |
| `oauth.issuer_url` | string | — | Restart | **`oidc` only, required**: `server_edition.oauth.issuer_url is required when provider is oidc`. Must be absolute `https`; `http` is admitted only for a loopback host together with `allow_insecure_issuer: true` — `server_edition.oauth.issuer_url must use https (http is allowed only for a loopback host with allow_insecure_issuer: true)`. Discovery reads `<issuer_url>/.well-known/openid-configuration` lazily on the first login (never at boot — readiness does not depend on the IdP), and the document's `issuer` must equal the configured value **byte for byte** (a trailing slash or path difference refuses the login as `discovery_failed`; the log line names both values) |
| `oauth.allow_insecure_issuer` | boolean | `false` | Restart | Development toggle for an in-process or loopback fake IdP: admits a plain-`http` issuer and plain-`http` discovered endpoints **only** when their host is loopback. Non-loopback `http` is refused regardless. Raw-JSON only — deliberately has no Settings row |
| `oauth.scopes` | string[] | `["openid","profile","email"]` | Restart | `oidc` only. `openid` is appended when missing. Add whatever your IdP needs for the groups claim (Okta: `groups`; see the table below) |
| `oauth.groups_claim` | string | `"groups"` | Restart | `oidc` only. Name of the ID-token (then userinfo) claim carrying group memberships. Accepted shapes: a flat JSON array of strings or a single string; anything else is treated as absent. Compared as exact strings by the group → server map |
| `oauth.email_verified_policy` | `refuse_false` \| `require_true` \| `ignore` | `refuse_false` | Restart | `server_edition.oauth.email_verified_policy must be one of: refuse_false, require_true, ignore`. See the cost note below |
| `oauth.display_name` | string | provider family name | Restart | Login-button label; at most 64 characters (`server_edition.oauth.display_name must be at most 64 characters`). It is the **only** field returned by the public `GET /api/v1/auth/provider` probe (never the issuer, client id, tenant, scopes or domains) |

#### `email_verified_policy` — what each value costs

| Value | `email_verified: false` | claim absent | Use when |
|-------|------------------------|--------------|----------|
| `refuse_false` (default) | refused (`email_unverified`) | login proceeds | Most IdPs. Refuses only what the IdP explicitly marks unverified |
| `require_true` | refused | refused | The IdP always emits the claim and every account must be verified — unverified or self-registered accounts can never pass `allowed_domains` |
| `ignore` | ignored | ignored | The IdP never emits the claim. **Cost:** a self-asserted email is trusted, so an attacker who can register `anything@example.com` at the IdP passes an `allowed_domains: ["example.com"]` check. Compensate at the IdP (verified-only registration) or with the group map |

#### Groups claim by identity provider

The proxy compares group strings exactly and case-sensitively. What the claim
carries is decided by the IdP:

| IdP | `groups_claim` | Values | Notes |
|-----|----------------|--------|-------|
| Keycloak | `groups` | `/path/names` (e.g. `/engineering/backend`) | Needs a **Group Membership** mapper on the client scope; untick *Full group path* to get bare names |
| Okta | `groups` | group names | Add the `groups` scope to `scopes` and a groups claim filter to the authorization server |
| Auth0 | `https://example.com/groups` | whatever your Action emits | Custom claims must be namespaced (a URL-shaped name); set it verbatim |
| Authentik | `groups` | group names | Shipped in the default `profile` scope, no extra mapping |
| Microsoft Entra ID | `groups` | group **object ids** (GUIDs) | Use the GUIDs in the map, or app roles. Above ~200 groups Entra omits the claim and sends an overage marker (`_claim_names`); the proxy treats that as **no groups** (`[]`) and logs a warning |

Groups are read from the verified ID token first; only when the token lacks
the claim is `userinfo_endpoint` consulted once, and only if its `sub` equals
the token's `sub`. A claim absent from both means `[]` (fail closed: the user
still logs in and receives only the default grant) with one warning naming the
user id and the claim looked for — never the token.

#### `public_url` and `trusted_proxies` in a container

```text
browser ──https──▶ ingress / TLS terminator (10.42.0.7) ──http──▶ mcpproxy-server 0.0.0.0:8080
                    sets X-Forwarded-Proto: https
                         X-Forwarded-For: <client ip>
                         Host: 127.0.0.1:8080 (or the public host)
```

| Setting | What it decides | If you leave it out |
|---------|-----------------|---------------------|
| `server_edition.public_url: "https://mcp.example.com"` | The exact `redirect_uri` registered at the IdP, the connect-flow base URL and the `Secure` cookie decision — from configuration, not from `Host`/`X-Forwarded-*` | The callback URL is derived from the request: the IdP's exact-match `redirect_uri` registration is then the only guard against a rewritten `Host`, and you get a boot warning + `doctor` finding on a non-loopback listener |
| `trusted_proxies: ["10.42.0.0/16"]` (the ingress's source range) | Which peers may set the forwarded scheme, host and client IP — for the session IP, the per-request client IP tagged for attribution and, without `public_url`, the callback scheme | Every forwarded header is ignored: the session IP is the ingress's address, and without `public_url` the callback is `http://…` (`redirect_uri_mismatch` at the IdP) |
| `session_cookie_secure: "auto"` (default) | `Secure` when the effective scheme is https | — |

Set both keys in the container. `trusted_hosts` is unrelated to this door: it
is DNS-rebinding protection for **loopback** listeners and never runs on
`0.0.0.0:8080`. When `public_url` is https but the callback reaches the proxy
over plain http, the login proceeds and one warning is logged
(`public_url is https but the OAuth callback arrived over http … check the
ingress forwards X-Forwarded-Proto from an address in trusted_proxies`).

### MCP Servers

See [Upstream Servers](/configuration/upstream-servers) for detailed server configuration.

## Hot Reload

MCPProxy watches the configuration file for changes and automatically reloads when modifications are detected. No restart is required for most configuration changes.

Exceptions that require a restart include `listen`, `data_dir`, `api_key`, the TLS block, the three `http_*_timeout` options, and — in the Server edition — every `server_edition` key except `admin_emails` (see [Server Edition](#server-edition)). `trusted_proxies` is live.

## Environment Variable Overrides

Configuration options can be overridden using environment variables. See [Environment Variables](/configuration/environment-variables) for details.
