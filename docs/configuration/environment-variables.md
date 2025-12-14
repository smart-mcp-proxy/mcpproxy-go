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

## Server Configuration

| Variable | Description | Example |
|----------|-------------|---------|
| `MCPPROXY_LISTEN` | Override listen address | `127.0.0.1:8080` or `:8080` |
| `MCPPROXY_API_KEY` | Set API key for authentication | `my-secret-key` |
| `MCPPROXY_DATA_DIR` | Override data directory | `/var/lib/mcpproxy` |

## Security Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TLS_ENABLED` | Enable TLS/HTTPS | `false` |
| `MCPPROXY_TLS_REQUIRE_CLIENT_CERT` | Enable mutual TLS (mTLS) | `false` |

**Note:** TLS certificates are managed in `~/.mcpproxy/certs/` or via the `tls.certs_dir` config option. Use `mcpproxy trust-cert` to set up certificates.

## Debugging

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DEBUG` | Enable debug mode | `false` |
| `HEADLESS` | Run without browser launching | `false` |

**Note:** Log level is configured via `--log-level` flag or `logging.level` in config file.

## OAuth Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_OAUTH` | Disable OAuth for testing | `false` |

## Tray Application

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_TRAY_SKIP_CORE` | Skip core launch (development) | `false` |
| `MCPPROXY_CORE_URL` | Custom core URL for tray | - |

## Usage Examples

### Start with Custom Port

```bash
MCPPROXY_LISTEN=":9000" mcpproxy serve
```

### Enable Debug Logging

```bash
MCPPROXY_DEBUG=true mcpproxy serve --log-level=debug
```

### Run in Headless Mode

```bash
HEADLESS=true mcpproxy serve
```

### Custom API Key

```bash
MCPPROXY_API_KEY="my-secure-key" mcpproxy serve
```

## Priority Order

Configuration is applied in this order (later sources override earlier):

1. Default values
2. Configuration file (`~/.mcpproxy/mcp_config.json`)
3. Environment variables
4. Command-line flags
