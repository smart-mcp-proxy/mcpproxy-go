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
| `enable_socket` | boolean | `true` | Enable Unix socket/named pipe for local communication |

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

### MCP Servers

See [Upstream Servers](/configuration/upstream-servers) for detailed server configuration.

## Hot Reload

MCPProxy watches the configuration file for changes and automatically reloads when modifications are detected. No restart is required for most configuration changes.

## Environment Variable Overrides

Configuration options can be overridden using environment variables. See [Environment Variables](/configuration/environment-variables) for details.
