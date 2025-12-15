# CLI Management Commands

This document describes the CLI commands for managing MCPProxy upstream servers and monitoring system health.

## Overview

MCPProxy provides two command groups:

1. **`mcpproxy upstream`** - Server management (list, logs, enable, disable, restart)
2. **`mcpproxy doctor`** - Health checks and diagnostics

All commands support both **daemon mode** (fast, via socket) and **standalone mode** (direct connection).

## Command Reference

### `mcpproxy upstream list`

List all configured upstream servers with connection status.

**Usage:**
```bash
mcpproxy upstream list [flags]
```

**Flags:**
- `--output, -o` - Output format (table, json) [default: table]
- `--log-level, -l` - Log level (trace, debug, info, warn, error) [default: warn]
- `--config, -c` - Path to config file

**Examples:**
```bash
# List servers with table output
mcpproxy upstream list

# JSON output for scripting
mcpproxy upstream list --output=json

# With debug logging
mcpproxy upstream list --log-level=debug
```

**Output Fields:**
- NAME - Server name
- PROTOCOL - Transport protocol (stdio, http, sse, streamable-http)
- TOOLS - Number of available tools
- STATUS - Unified health status with emoji indicator and summary
- ACTION - Suggested remediation command (if applicable)

**Status Indicators:**
- ✅ Healthy - Server connected and working
- ⚠️ Degraded - Server has warnings (e.g., token expiring soon)
- ❌ Unhealthy - Server has errors or not functioning
- ⏸️ Disabled - Server manually disabled by user
- 🔒 Quarantined - Server pending security approval

**Example Output:**
```
NAME                      PROTOCOL   TOOLS      STATUS                         ACTION
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅    github-server           http       15         Connected (15 tools)           -
❌    oauth-server            http       0          Token expired                  auth login --server=oauth-server
⏸️    disabled-server         stdio      0          Disabled by user               upstream enable disabled-server
```

---

### `mcpproxy upstream logs <name>`

Display recent log entries from a specific server.

**Usage:**
```bash
mcpproxy upstream logs <server-name> [flags]
mcpproxy upstream logs --server <server-name> [flags]
```

**Flags:**
- `--server, -s` - Server name (alternative to positional argument)
- `--tail, -n` - Number of lines to show [default: 50]
- `--follow, -f` - Follow log output (requires daemon)
- `--log-level, -l` - Log level [default: warn]
- `--config, -c` - Path to config file

**Examples:**
```bash
# Show last 50 lines (either form works)
mcpproxy upstream logs github-server
mcpproxy upstream logs --server github-server
mcpproxy upstream logs -s github-server

# Show last 200 lines
mcpproxy upstream logs github-server --tail=200

# Follow logs (Ctrl+C to stop)
mcpproxy upstream logs github-server --follow
```

**Behavior:**
- **Daemon mode**: Fetches logs via API from running server
- **Standalone mode**: Reads directly from log file (read-only)
- **Follow mode**: Requires running daemon, polls for new lines

---

### `mcpproxy upstream enable <name|--all>`

Enable a disabled server or all disabled servers.

**Usage:**
```bash
mcpproxy upstream enable <server-name>
mcpproxy upstream enable --server <server-name>
mcpproxy upstream enable --all [--force]
```

**Flags:**
- `--server, -s` - Server name (alternative to positional argument)
- `--all` - Enable all disabled servers
- `--force` - Skip confirmation prompt (for automation)

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Enable single server (either form works)
mcpproxy upstream enable github-server
mcpproxy upstream enable --server github-server
mcpproxy upstream enable -s github-server

# Enable all servers (interactive confirmation)
mcpproxy upstream enable --all

# Enable all servers (skip prompt)
mcpproxy upstream enable --all --force
```

**Interactive Confirmation:**
```bash
$ mcpproxy upstream enable --all
⚠️  This will enable 5 server(s). Continue? [y/N]: _
```

**Non-interactive mode requires --force:**
```bash
$ echo "y" | mcpproxy upstream enable --all
Error: --all requires --force flag in non-interactive mode
```

---

### `mcpproxy upstream disable <name|--all>`

Disable a server or all enabled servers.

**Usage:**
```bash
mcpproxy upstream disable <server-name>
mcpproxy upstream disable --server <server-name>
mcpproxy upstream disable --all [--force]
```

**Flags:**
- `--server, -s` - Server name (alternative to positional argument)
- `--all` - Disable all enabled servers
- `--force` - Skip confirmation prompt

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Disable single server (either form works)
mcpproxy upstream disable github-server
mcpproxy upstream disable --server github-server
mcpproxy upstream disable -s github-server

# Disable all servers (interactive confirmation)
mcpproxy upstream disable --all

# Disable all in script
mcpproxy upstream disable --all --force
```

---

### `mcpproxy upstream restart <name|--all>`

Restart a server or all enabled servers.

**Usage:**
```bash
mcpproxy upstream restart <server-name>
mcpproxy upstream restart --server <server-name>
mcpproxy upstream restart --all
```

**Flags:**
- `--server, -s` - Server name (alternative to positional argument)
- `--all` - Restart all enabled servers

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Restart single server (either form works)
mcpproxy upstream restart github-server
mcpproxy upstream restart --server github-server
mcpproxy upstream restart -s github-server

# Restart all servers (no confirmation needed)
mcpproxy upstream restart --all
```

**Note:** Restart does not require confirmation as it's non-destructive.

---

### `mcpproxy doctor`

Run comprehensive health checks to identify issues.

**Usage:**
```bash
mcpproxy doctor [flags]
```

**Flags:**
- `--output, -o` - Output format (pretty, json) [default: pretty]
- `--log-level, -l` - Log level [default: warn]
- `--config, -c` - Path to config file

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Pretty output with issue categorization
mcpproxy doctor

# JSON output for scripting
mcpproxy doctor --output=json
```

**Health Checks:**
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets (unresolved references)
- Runtime warnings
- Docker isolation status

**Output:**
- Total issue count
- Categorized issues with actionable remediation steps
- Exit code 0 even if issues found (for scripting)

---

## Common Workflows

### Debugging Server Connection Issues

```bash
# 1. Run health check to see all issues
mcpproxy doctor

# 2. Check specific server status
mcpproxy upstream list

# 3. View logs for failing server
mcpproxy upstream logs failing-server --tail=100

# 4. Restart server
mcpproxy upstream restart failing-server

# 5. Verify fix
mcpproxy upstream list
```

### Monitoring Server Health

```bash
# Follow logs in terminal 1
mcpproxy upstream logs github-server --follow

# In terminal 2, trigger operations
mcpproxy call tool --tool-name=github:get_user --json_args='{"username":"octocat"}'

# Watch logs update in real-time
```

### Bulk Server Management

```bash
# List all servers first
mcpproxy upstream list

# Disable all for maintenance
mcpproxy upstream disable --all --force

# Perform maintenance...

# Re-enable all
mcpproxy upstream enable --all --force

# Verify
mcpproxy upstream list
```

---

## Daemon Mode vs Standalone Mode

### Daemon Mode (Preferred)
- **Detection**: Socket file exists at `~/.mcpproxy/mcpproxy.sock`
- **Behavior**: Uses HTTP API via Unix socket
- **Speed**: Fast (reuses existing connections)
- **Requirements**: `mcpproxy serve` running

### Standalone Mode (Fallback)
- **Detection**: No socket file found
- **Behavior**: Direct connection to servers or file reading
- **Speed**: Slower (establishes new connections)
- **Limitations**: Some commands unavailable (enable, disable, restart, follow, doctor)

### Command Availability

| Command | Daemon Mode | Standalone Mode | Notes |
|---------|-------------|-----------------|-------|
| `upstream list` | ✅ Full status | ✅ Config only | Standalone shows "unknown" |
| `upstream logs` | ✅ Via API | ✅ File read | Follow requires daemon |
| `upstream enable` | ✅ | ❌ | Requires daemon |
| `upstream disable` | ✅ | ❌ | Requires daemon |
| `upstream restart` | ✅ | ❌ | Requires daemon |
| `doctor` | ✅ | ❌ | Requires daemon |

---

## Safety Considerations

### Bulk Operations Warning

⚠️  **Safety Warning: Bulk Operations**

The `--all` flag affects all servers simultaneously:

- `disable --all` - Stops all upstream connections (reversible with `enable --all`)
- `enable --all` - Activates all servers (may trigger API calls, OAuth flows)
- `restart --all` - Reconnects all servers (may cause brief service disruption)

**Best practices:**
- Use `upstream list` first to see affected servers
- Test with single server before using `--all`
- Use `--force` in automation only when appropriate

### Interactive Confirmation

Confirmation prompt shows count:

```bash
⚠️  This will disable 12 server(s). Continue? [y/N]:
```

Shows what will be affected before proceeding.

---

## Exit Codes

- `0` - Success
- `1` - Execution failure (API error, connection failure, user declined confirmation)
- `2` - Invalid arguments or configuration (non-interactive without --force)

---

## Implementation Notes

### Architecture
- Commands in `cmd/mcpproxy/*_cmd.go`
- Client library in `internal/cliclient/client.go`
- API endpoints in `internal/httpapi/server.go`

### Adding New Commands
1. Create command file in `cmd/mcpproxy/`
2. Add methods to `internal/cliclient/client.go`
3. Register in `cmd/mcpproxy/main.go`
4. Follow existing patterns for daemon detection
5. Support both daemon and standalone modes where possible

### Testing
All commands support manual testing:
```bash
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy <command> [args]
```
