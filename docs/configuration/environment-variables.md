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
| `MCPPROXY_DATA` | Override data directory | `/var/lib/mcpproxy` |

### Security Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TLS_ENABLED` | Enable TLS/HTTPS | `false` |
| `MCPPROXY_TLS_REQUIRE_CLIENT_CERT` | Enable mutual TLS (mTLS) | `false` |

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

### Auto-Update Settings (Tray)

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable automatic update checks | `false` |
| `MCPPROXY_UPDATE_NOTIFY_ONLY` | Only notify about updates, don't auto-install | `false` |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Allow prerelease/beta version updates | `false` |
| `MCPPROXY_UPDATE_APP_BUNDLE` | Enable app bundle updates (macOS) | `false` |

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
