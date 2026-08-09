---
id: environment-variables
title: Environment Variables
sidebar_label: Environment Variables
sidebar_position: 3
description: Configure MCPProxy using environment variables
keywords: [environment, variables, env, configuration]
---

# Environment Variables

MCPProxy consists of two components that can be configured via environment variables:

- **mcpproxy** (core server) - The main proxy server that handles MCP connections
- **mcpproxy-tray** (tray application) - Optional GUI for user convenience

## Core Server (mcpproxy)

The core server is the main MCPProxy application. It handles all MCP proxy functionality, upstream server management, and API endpoints.

:::tip Recommended: Use Config File
For the core server, prefer configuring settings in `~/.mcpproxy/mcp_config.json`. See [Config File](./config-file.md) for details.

Environment variables are useful for CI/CD environments or temporary overrides during development.
:::

### Server Configuration

| Variable | Description | Example |
|----------|-------------|---------|
| `MCPPROXY_LISTEN` | Override listen address | `127.0.0.1:8080` or `:8080` |
| `MCPPROXY_API_KEY` | Set API key for authentication | `my-secret-key` |
| `MCPPROXY_DATA_DIR` | Override data directory | `/var/lib/mcpproxy` |
| `MCPPROXY_DATA` | Override data directory (backward compatibility, prefer `MCPPROXY_DATA_DIR`) | `/var/lib/mcpproxy` |

### Security Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TRUSTED_HOSTS` | Comma-separated `Host` header allowlist for loopback listeners behind a reverse proxy (see [Reverse Proxy Deployment](/operations/reverse-proxy)) | - |
| `MCPPROXY_TLS_ENABLED` | Enable TLS/HTTPS | `false` |
| `MCPPROXY_TLS_CERT` | Path to TLS certificate | - |
| `MCPPROXY_TLS_KEY` | Path to TLS private key | - |
| `MCPPROXY_TLS_REQUIRE_CLIENT_CERT` | Require client certificates for mTLS | `false` |
| `MCPPROXY_CERTS_DIR` | Custom directory for TLS certificates | - |

**Note:** TLS certificates are managed in `~/.mcpproxy/certs/` or via the `tls.certs_dir` config option. Use `mcpproxy trust-cert` to set up certificates.

### OAuth Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_OAUTH` | Disable OAuth for testing | `false` |

### Browser Detection

These variables control browser behavior for OAuth flows:

| Variable | Description | Default |
|----------|-------------|---------|
| `HEADLESS` | Disable browser launching | `false` |
| `NO_BROWSER` | Prevent browser opening for OAuth | `false` |
| `CI` | CI environment detection (disables browser) | - |
| `BROWSER` | Custom browser executable for OAuth | System default |

### Concurrency Limits

These override the **global aggregate** limiter only — the per-server default
set and per-server overrides are file/API-configured. See
[Concurrency Limits & Request Queueing](./config-file.md#concurrency-limits--request-queueing).

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_MAX_CONCURRENT_REQUESTS` | Proxy-wide cap on upstream tool calls running at once. `0` disables the global limiter | `0` (off) |
| `MCPPROXY_QUEUE_SIZE` | How many calls may wait for a global slot. `0` = shed immediately at the cap | `0` |
| `MCPPROXY_QUEUE_TIMEOUT` | How long a call may wait before being shed, e.g. `30s` | `30s` when the limiter is active |

### HTTP Server Timeouts

Deadlines on MCPProxy's own HTTP listener (REST API, `/mcp`, `/events`). Each
takes a duration string; **`0s` means "no timeout"** (unset means "use the
default"). Valid range: `1s`–`24h`, or `0s`. Malformed values are ignored with a
warning on stderr. Changing any of these requires a restart. See
[HTTP Server Timeouts](./config-file.md#http-server-timeouts).

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_HTTP_READ_TIMEOUT` | Deadline for reading the whole request (headers + body) | `120s` |
| `MCPPROXY_HTTP_WRITE_TIMEOUT` | Wall-clock cap on writing the whole response. Disabled by default so slow tool calls and SSE `/events` streams are not truncated ([#965](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/965)) | `0s` (off) |
| `MCPPROXY_HTTP_IDLE_TIMEOUT` | Keep-alive timeout for idle persistent connections | `180s` |

The 60s request-header read deadline (slowloris protection) is hardcoded and not
configurable. `call_tool_timeout` (default `2m`) separately caps tool execution —
raise it too when you expect tool calls longer than two minutes.

### Core Server Examples

```bash
# Start with custom port
MCPPROXY_LISTEN=":9000" mcpproxy serve

# Enable debug logging
mcpproxy serve --log-level=debug

# Run in headless mode (no browser for OAuth)
HEADLESS=true mcpproxy serve

# Custom API key
MCPPROXY_API_KEY="my-secure-key" mcpproxy serve
```

---

## Tray Application (mcpproxy-tray)

The tray application is an **optional** GUI component that provides user convenience features like system tray icon, menu access, and automatic core server management. The core server works independently without the tray.

**How tray connects to core:**
- **macOS/Linux**: Unix socket at `~/.mcpproxy/mcpproxy.sock`
- **Windows**: Named pipe at `\\.\pipe\mcpproxy-<username>`

Socket/pipe connections are trusted and don't require API key authentication.

:::note
The tray application doesn't read the config file directly. It launches the core server which reads `~/.mcpproxy/mcp_config.json`. Use tray environment variables only for tray-specific behavior.
:::

### Tray Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TRAY_PORT` | Port for tray-launched core | `8080` |
| `MCPPROXY_TRAY_LISTEN` | Listen address for core (e.g., `:8080`) | - |
| `MCPPROXY_CORE_URL` | Full URL override (e.g., `http://127.0.0.1:30080`) | - |
| `MCPPROXY_CORE_PATH` | Custom path to mcpproxy core binary | - |
| `MCPPROXY_TRAY_CONFIG_PATH` | Custom config file path for core | - |
| `MCPPROXY_TRAY_EXTRA_ARGS` | Extra CLI arguments for core | - |
| `MCPPROXY_TRAY_SKIP_CORE` | Skip core launch (for development) | `false` |
| `MCPPROXY_TRAY_CORE_TIMEOUT` | Core startup timeout in seconds | `30` |
| `MCPPROXY_TRAY_RETRY_DELAY` | Core connection retry delay (ms) | `1000` |
| `MCPPROXY_TRAY_STATE_DEBUG` | Enable state machine debug logging | `false` |
| `MCPPROXY_TRAY_ENDPOINT` | Override tray-core communication endpoint (unix:///path/socket.sock or npipe:////./pipe/name) | Auto-detect |
| `MCPPROXY_TRAY_INSPECT_ADDR` | Address for tray instrumentation/debug server | - |

### Auto-Update Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable automatic update checks (core + tray) | `false` |
| `MCPPROXY_UPDATE_NOTIFY_ONLY` | Only notify about updates, don't auto-install (tray) | `false` |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Allow prerelease/beta version updates (core + tray) | `false` |
| `MCPPROXY_UPDATE_APP_BUNDLE` | Enable app bundle updates (macOS tray) | `false` |

`CI=true` (or `CI=1`) additionally suppresses every update nudge and the tray's
unattended checks — a non-interactive run has nobody to nudge. Machine-readable
fields keep reporting the facts, and a user-initiated "Check for Updates" still
runs.

Update checking can also be controlled from the config file via the
`update_check` block (`enabled`, `channel`) — see
[Version Updates](/features/version-updates). When both are set, the
environment variables **win** over the config keys. The resolved answer is
published to the macOS tray as `update_policy` in `GET /api/v1/info`; the
one-click updater, the per-channel behaviour matrix and the release
infrastructure are documented in [Auto-Update](/features/auto-update).

### Setting Tray Variables on macOS

When launching mcpproxy-tray from Launchpad or the Applications folder, environment variables must be set system-wide using `launchctl`:

```bash
# Set custom port for the core server
launchctl setenv MCPPROXY_TRAY_PORT 30080

# Or use a custom config file
launchctl setenv MCPPROXY_TRAY_CONFIG_PATH "/path/to/custom-config.json"

# Restart Dock for apps to pick up the new environment
killall Dock

# Now launch mcpproxy-tray from Launchpad or Applications folder
```

**To clear environment variables:**

```bash
launchctl unsetenv MCPPROXY_TRAY_PORT
killall Dock
```

---

## Priority Order

Configuration is applied in this order (later sources override earlier):

1. Default values
2. Configuration file (`~/.mcpproxy/mcp_config.json`)
3. Environment variables
4. Command-line flags
