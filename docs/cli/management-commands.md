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

The first section is the same needs-attention list `mcpproxy attention` prints (see [Attention Command](/cli/attention-command)); the diagnostics follow under `Diagnostics: N findings`.

It checks for:
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
- **STATUS**: the status label, the same word the Web UI card, the macOS row and the tray show (`Online`, `Connecting`, `Sign-in required`, `Needs review`, `Secret required`, `Needs configuration`, `Error`, `Disabled`). A server that is not usable never reads Online, healthy or connected
- **ACTION**: the CLI command for the server's first suggested action, or `-`

Example output:
```
NAME                      PROTOCOL   TOOLS      STATUS                         ACTION
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅    github-server           http       15         Online                         -
❌    oauth-server            http       0          Sign-in required               auth login --server=oauth-server
```

`-o json` is unchanged and carries `health.status`, `health.usable` and `health.actions` beside every older field.

Filter by status with `--status`. The flag is repeatable, and a comma-separated value is the same as repeating it (union semantics):

```bash
mcpproxy upstream list --status needs_review,sign_in_required
mcpproxy upstream list --status error --status connecting
```

The values are `ready`, `connecting`, `sign_in_required`, `needs_review`, `needs_secret`, `needs_config`, `error` and `disabled`.

### Add a Server

```bash
mcpproxy upstream add notion https://mcp.notion.com/sse
mcpproxy upstream add fs -- npx -y @modelcontextprotocol/server-filesystem /tmp
mcpproxy upstream add github https://api.githubcopilot.com/mcp/ --secret-header "Authorization: Bearer ghp_..."
mcpproxy upstream add weather -- npx -y weather-mcp --secret-env WEATHER_API_KEY=abc123
```

`--secret-env KEY=VALUE` and `--secret-header "Name: value"` (both repeatable) write the value to the OS keyring and store `${keyring:<server>-env-<name>}` in the config instead of the value. New servers are quarantined. To pick a server from a catalog instead of typing its command, see [Catalog Commands](/cli/catalog-commands).

### Review and Approve

New servers and new or changed tools wait for review. `mcpproxy review` is the one entry point (see [Review Commands](/cli/review-commands)); the older verbs stay as documented aliases:

| Older command | Use instead |
|---|---|
| `mcpproxy upstream approve <server> [tools...]` | `mcpproxy review approve <server> --tools ...` |
| `mcpproxy security approve <server>` / `security reject` | `mcpproxy review approve <server>` / `mcpproxy review reject <server>` |
| `mcpproxy tools approve` / `tools reject` | `mcpproxy review approve` / `mcpproxy review reject` with `--tools` |

`mcpproxy tools list --risk` is an alias of `--tier`.

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
- `server-{name}.log` - Per-server logs (reserved characters in `{name}`, e.g. the `/` in registry names, are sanitized to `_`)
