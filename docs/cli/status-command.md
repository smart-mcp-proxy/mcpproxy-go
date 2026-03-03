---
id: status-command
title: Status Command
sidebar_label: Status Command
sidebar_position: 5
description: CLI command for viewing MCPProxy status, API key, and Web UI URL
keywords: [status, api-key, web-ui, url, cli, monitoring]
---

# Status Command

The `mcpproxy status` command displays the current state of your MCPProxy instance including running status, API key, Web UI URL, and server statistics.

## Overview

```
mcpproxy status [flags]
```

The command operates in two modes:

- **Daemon mode**: When MCPProxy is running, queries live data via Unix socket
- **Config mode**: When MCPProxy is not running, reads from the config file

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--show-key` | | `false` | Display the full unmasked API key |
| `--web-url` | | `false` | Print only the Web UI URL (for piping) |
| `--reset-key` | | `false` | Regenerate API key and save to config |
| `--output` | `-o` | `table` | Output format: table, json, yaml |
| `--json` | | `false` | Shorthand for `-o json` |

## Examples

### Basic Status Check

```bash
mcpproxy status
```

**When daemon is running:**

```
MCPProxy Status
  State:       Running
  Version:     v1.2.0
  Listen:      127.0.0.1:8080
  Uptime:      2h 15m
  API Key:     a1b2****gh78
  Web UI:      http://127.0.0.1:8080/ui/?apikey=a1b2...gh78
  Servers:     12 connected, 2 quarantined
  Socket:      /Users/you/.mcpproxy/mcpproxy.sock
  Config:      /Users/you/.mcpproxy/mcp_config.json
```

**When daemon is not running:**

```
MCPProxy Status
  State:       Not running
  Listen:      127.0.0.1:8080 (configured)
  API Key:     a1b2****gh78
  Web UI:      http://127.0.0.1:8080/ui/?apikey=a1b2...gh78
  Config:      /Users/you/.mcpproxy/mcp_config.json
```

### Show Full API Key

```bash
# Display full key for copying
mcpproxy status --show-key

# Copy to clipboard (macOS)
mcpproxy status --show-key -o json | jq -r .api_key | pbcopy
```

### Open Web UI in Browser

```bash
# macOS
open $(mcpproxy status --web-url)

# Linux
xdg-open $(mcpproxy status --web-url)
```

### Reset API Key

```bash
mcpproxy status --reset-key
```

Output:

```
Warning: Resetting the API key will disconnect any HTTP clients using the current key.
         Socket connections (tray app) are NOT affected.

New API key: e7f8a9b0c1d2e3f4...
Saved to: /Users/you/.mcpproxy/mcp_config.json

MCPProxy Status
  State:       Running
  ...
  API Key:     e7f8a9b0c1d2e3f4...
```

:::note
If the `MCPPROXY_API_KEY` environment variable is set, resetting the key in the config file will not take effect until the environment variable is removed or updated.
:::

### JSON Output

```bash
mcpproxy status -o json
```

```json
{
  "state": "Running",
  "listen_addr": "127.0.0.1:8080",
  "uptime": "2h 15m",
  "uptime_seconds": 8100,
  "api_key": "a1b2****gh78",
  "web_ui_url": "http://127.0.0.1:8080/ui/?apikey=...",
  "servers": {
    "connected": 12,
    "quarantined": 2,
    "total": 14
  },
  "socket_path": "/Users/you/.mcpproxy/mcpproxy.sock",
  "config_path": "/Users/you/.mcpproxy/mcp_config.json",
  "version": "v1.2.0"
}
```

## API Key Masking

By default, the API key is masked showing only the first 4 and last 4 characters:

```
a1b2c3d4e5f6...7890abcd  →  a1b2****abcd
```

Use `--show-key` to reveal the full key. The `--reset-key` flag implicitly shows the full new key.

## Transport and Authentication

| Transport | Auth Required | Affected by Key Reset |
|-----------|--------------|----------------------|
| Unix socket (tray app, CLI) | No (OS-level auth) | No |
| HTTP/TCP (remote clients) | Yes (API key) | Yes - clients need new key |
| MCP endpoints (`/mcp`) | No | No |

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error (config load failure, etc.) |
| `4` | Config error |
