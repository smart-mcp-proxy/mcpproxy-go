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
  "enable_code_execution": false,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
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
| `trusted_hosts` | string[] | `[]` | Non-loopback `Host` header values accepted on a loopback listener. Needed when running behind a reverse proxy — see [Reverse Proxy Deployment](/operations/reverse-proxy) |
| `require_mcp_auth` | boolean | `false` | Require an API key on the `/mcp` endpoint (off by default for client compatibility). Enable when exposing MCPProxy beyond localhost |
| `enable_socket` | boolean | `true` | Enable Unix socket/named pipe for local communication |

### HTTP Server Timeouts

Deadlines applied to MCPProxy's own HTTP listener (REST API, `/mcp`, `/events`).
Each accepts a duration string; **`"0s"` means "no timeout"** (not "use the
default" — omit the key for that). Valid range: `1s`–`24h`, or `0s`.

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
| `enable_code_execution` | boolean | `false` | Enable JavaScript code execution tool |
| `code_execution_timeout_ms` | integer | `120000` | Execution timeout in milliseconds |
| `code_execution_max_tool_calls` | integer | `0` | Maximum tool calls (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | VM pool size for code execution |

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

### MCP Servers

See [Upstream Servers](/configuration/upstream-servers) for detailed server configuration.

## Hot Reload

MCPProxy watches the configuration file for changes and automatically reloads when modifications are detected. No restart is required for most configuration changes.

Exceptions that require a restart include `listen`, `data_dir`, `api_key`, the TLS block, and the three `http_*_timeout` options.

## Environment Variable Overrides

Configuration options can be overridden using environment variables. See [Environment Variables](/configuration/environment-variables) for details.
