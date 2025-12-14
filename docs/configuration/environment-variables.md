---
id: environment-variables
title: Environment Variables
sidebar_label: Environment Variables
sidebar_position: 3
description: Configure MCPProxy using environment variables
keywords: [environment, variables, env, configuration]
---

# Environment Variables

MCPProxy can be configured using environment variables, which take precedence over config file settings.

:::tip Recommended: Use Config File
For the core server, prefer configuring `listen`, `api_key`, and other settings in `~/.mcpproxy/mcp_config.json`. See [Config File](./config-file.md) for details.

Environment variables are primarily useful for:
- **Tray application settings** (variables starting with `MCPPROXY_TRAY_*`)
- **CI/CD environments** where config files aren't practical
- **Temporary overrides** during development
:::

## Server Configuration

| Variable | Description | Example |
|----------|-------------|---------|
| `MCPPROXY_LISTEN` | Override listen address | `127.0.0.1:8080` or `:8080` |
| `MCPPROXY_API_KEY` | Set API key for authentication | `my-secret-key` |
| `MCPPROXY_DATA` | Override data directory | `/var/lib/mcpproxy` |

## Security Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TLS_ENABLED` | Enable TLS/HTTPS | `false` |
| `MCPPROXY_TLS_REQUIRE_CLIENT_CERT` | Enable mutual TLS (mTLS) | `false` |

**Note:** TLS certificates are managed in `~/.mcpproxy/certs/` or via the `tls.certs_dir` config option. Use `mcpproxy trust-cert` to set up certificates.

## Debugging

| Variable | Description | Default |
|----------|-------------|---------|
| `HEADLESS` | Run without browser launching | `false` |

**Note:** Debug logging is enabled via `--log-level=debug` flag or `logging.level` in config file.

## OAuth Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_OAUTH` | Disable OAuth for testing | `false` |

## Tray Application

These variables configure the tray application behavior. They are the primary use case for environment variables since the tray doesn't use the config file directly.

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

## Auto-Update Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable automatic update checks | `false` |
| `MCPPROXY_UPDATE_NOTIFY_ONLY` | Only notify about updates, don't auto-install | `false` |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Allow prerelease/beta version updates | `false` |
| `MCPPROXY_UPDATE_APP_BUNDLE` | Enable app bundle updates (macOS) | `false` |

## Browser Detection

These variables control browser behavior for OAuth flows:

| Variable | Description | Default |
|----------|-------------|---------|
| `HEADLESS` | Disable browser launching | `false` |
| `NO_BROWSER` | Prevent browser opening for OAuth | `false` |
| `CI` | CI environment detection (disables browser) | - |
| `BROWSER` | Custom browser executable for OAuth | System default |

## Usage Examples

### Start with Custom Port

```bash
MCPPROXY_LISTEN=":9000" mcpproxy serve
```

### Enable Debug Logging

```bash
mcpproxy serve --log-level=debug
```

### Run in Headless Mode

```bash
HEADLESS=true mcpproxy serve
```

### Custom API Key

```bash
MCPPROXY_API_KEY="my-secure-key" mcpproxy serve
```

### Setting Tray Environment Variables on macOS

When launching mcpproxy-tray from Launchpad or the Applications folder, environment variables must be set system-wide using `launchctl`. This is an alternative to running the tray from terminal.

:::note
For core server settings like `listen`, `api_key`, and upstream servers, use the config file `~/.mcpproxy/mcp_config.json` instead. The tray will pass the config to the core automatically.
:::

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

## Priority Order

Configuration is applied in this order (later sources override earlier):

1. Default values
2. Configuration file (`~/.mcpproxy/mcp_config.json`)
3. Environment variables
4. Command-line flags
