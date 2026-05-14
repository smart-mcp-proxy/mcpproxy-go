---
id: management-commands
title: Management Commands
sidebar_label: Management Commands
sidebar_position: 2
description: CLI commands for managing upstream servers and monitoring health
keywords: [cli, management, upstream, logs, restart, doctor]
---

# Management Commands

MCPProxy provides CLI commands for managing upstream servers and monitoring system health.

## Quick Diagnostics

Run this first when debugging any issue:

```bash
mcpproxy doctor
```

This checks for:
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets
- Runtime warnings
- Docker isolation status

## Common Workflow

```bash
mcpproxy doctor                     # Check overall health
mcpproxy upstream list              # Identify issues
mcpproxy upstream logs failing-srv  # View logs
mcpproxy upstream restart failing-srv
```

## Upstream Commands

### List Servers

```bash
mcpproxy upstream list
```

Output shows unified health status:
- Server name and protocol type
- Tool count
- Health status with emoji indicator (✅ healthy, ⚠️ degraded, ❌ unhealthy, ⏸️ disabled, 🔒 quarantined)
- Suggested action command when applicable

Example output:
```
NAME                      PROTOCOL   TOOLS      STATUS                         ACTION
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅    github-server           http       15         Connected (15 tools)           -
❌    oauth-server            http       0          Token expired                  auth login --server=oauth-server
```

### View Logs

```bash
# View last 100 lines
mcpproxy upstream logs github-server --tail=100

# Follow logs in real-time (requires daemon)
mcpproxy upstream logs github-server --follow
```

### Restart Server

```bash
# Restart single server
mcpproxy upstream restart github-server

# Restart all servers
mcpproxy upstream restart --all
```

### Enable/Disable

```bash
mcpproxy upstream enable server-name
mcpproxy upstream disable server-name
```

### Patch Headers / Env

`mcpproxy upstream patch` updates HTTP `headers` and stdio `env` on an
existing server using JSON Merge Patch semantics — keys you specify are
upserted, keys named in `--header-remove` / `--env-remove` are deleted,
and every other key on the stored config is preserved.

This means you can rotate a single Bearer token without seeing or
touching any other header. The same applies to env vars on stdio servers.

```bash
# Rotate the Authorization header on a connected server
mcpproxy upstream patch synapbus --header "Authorization: Bearer new-token"

# Add a custom header without disturbing existing ones
mcpproxy upstream patch synapbus --header "X-Trace: on"

# Remove a stale header
mcpproxy upstream patch synapbus --header-remove "X-Old"

# Set + remove in one round-trip
mcpproxy upstream patch synapbus --header "X-New: v" --header-remove "X-Old"

# Update env vars on a stdio server
mcpproxy upstream patch obsidian-pilot \
  --env "LOG_LEVEL=debug" --env-remove "OBSOLETE_VAR"
```

**Flags** (all repeatable):

| Flag | Semantics |
|---|---|
| `--header NAME: value` | Upsert one header (single colon delimits name and value) |
| `--header-remove NAME` | Delete a header by name |
| `--env KEY=value` | Upsert one env var |
| `--env-remove KEY` | Delete an env var by name |

**Notes:**

- Requires the daemon to be running (`mcpproxy serve`). The subcommand
  applies changes through the live REST endpoint so connection state
  and OAuth tokens stay coordinated; editing `mcp_config.json` by hand
  is only safe while the daemon is offline.
- Specifying the same key in both `--header` and `--header-remove` is a
  conflict and errors out with a useful message.
- For new servers, use `upstream add` (HTTP/stdio) or
  `upstream add-json` (full JSON shape) instead.

## Socket Communication

CLI commands automatically detect and use Unix socket/named pipe communication when the daemon is running.

**Benefits of socket mode:**
- Reuses daemon's existing server connections (faster)
- Shows real daemon state (not config file state)
- Coordinates OAuth tokens with running daemon
- No redundant server connection overhead

**Commands with socket support:**
- `upstream list/logs/enable/disable/restart/patch`
- `doctor` (requires daemon)
- `call tool`
- `code exec`
- `tools list`
- `auth login/status`

**Standalone commands** (no socket needed):
- `secrets` - Direct OS keyring operations
- `trust-cert` - File system operations
- `search-servers` - Registry API operations

## Log Locations

| Platform | Location |
|----------|----------|
| macOS | `~/Library/Logs/mcpproxy/` |
| Linux | `~/.mcpproxy/logs/` |
| Windows | `%LOCALAPPDATA%\mcpproxy\logs\` |

Files:
- `main.log` - Main application log
- `server-{name}.log` - Per-server logs
