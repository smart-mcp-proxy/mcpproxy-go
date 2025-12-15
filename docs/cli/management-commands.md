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

## Socket Communication

CLI commands automatically detect and use Unix socket/named pipe communication when the daemon is running.

**Benefits of socket mode:**
- Reuses daemon's existing server connections (faster)
- Shows real daemon state (not config file state)
- Coordinates OAuth tokens with running daemon
- No redundant server connection overhead

**Commands with socket support:**
- `upstream list/logs/enable/disable/restart`
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
