# CLI Management Commands Design

**Date:** 2025-11-19
**Status:** Validated Design
**Goal:** Add CLI commands for managing MCPProxy upstream servers and monitoring system health

---

## Overview

Add two command groups to the MCPProxy CLI for server management and health monitoring:

1. **`mcpproxy upstream`** - Manage upstream MCP servers (list, logs, enable, disable, restart)
2. **`mcpproxy doctor`** - Health checks and diagnostics (overall health + drill-down subcommands)

All commands support both daemon mode (fast, via socket) and standalone mode (direct connection fallback).

---

## Command Structure

### `mcpproxy upstream` - Server Management

```bash
# List all servers with connection status
mcpproxy upstream list [--output=table|json]

# View server logs
mcpproxy upstream logs <name> [--tail=N] [--follow]

# Enable/disable servers
mcpproxy upstream enable <name|--all> [--force]
mcpproxy upstream disable <name|--all> [--force]

# Restart servers
mcpproxy upstream restart <name|--all>
```

**Output Format:**
- `--output=table` (default) - Tabular display with columns: NAME, ENABLED, PROTOCOL, CONNECTED, TOOLS, STATUS
- `--output=json` - JSON format for scripting

**Bulk Operations:**
- All actions support `--all` flag for operating on all servers
- `enable --all` and `disable --all` require interactive confirmation or `--force` flag
- `restart --all` operates without confirmation (non-destructive)

### `mcpproxy doctor` - Health Checks

```bash
# Run all health checks (default)
mcpproxy doctor [--output=pretty|json]

# Drill down into specific checks
mcpproxy doctor docker      # Docker isolation status
mcpproxy doctor oauth       # OAuth authentication issues
mcpproxy doctor secrets     # Missing secrets
mcpproxy doctor upstream    # Upstream connection errors
```

**Output Format:**
- `--output=pretty` (default) - Rich formatted output with emojis and sections (like Homebrew doctor)
- `--output=json` - JSON format for scripting

**Health Checks:**
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets (unresolved references)
- Runtime warnings
- Docker isolation status

---

## Design Decisions

### 1. Naming: `upstream` (not `server`)

**Rationale:** Clear distinction from `mcpproxy serve` command (which starts the daemon). "Upstream" matches internal terminology and config structure.

**Rejected alternatives:**
- `server` - Too similar to `serve`, confusing
- `servers` - Still similar, less precise
- `mcp` - Less intuitive for users unfamiliar with protocol

### 2. Naming: `doctor` (not `diagnostics`)

**Rationale:** Follows Homebrew convention (`brew doctor`), more approachable than "diagnostics".

**Pattern:** One command to check everything, subcommands for drill-down.

### 3. Drill-down via Subcommands (not flags)

**Rationale:** More discoverable via `--help`, follows CLI conventions, extensible.

```bash
mcpproxy doctor docker      # Subcommand (chosen)
mcpproxy doctor --docker    # Flag (rejected)
```

### 4. Consistent `--all` Flag Pattern

**Rationale:** All server actions support `--all` for consistency and flexibility.

```bash
mcpproxy upstream enable --all
mcpproxy upstream disable --all
mcpproxy upstream restart --all
```

**Safety:** Bulk enable/disable require confirmation (see below).

### 5. Interactive Confirmation for Bulk Operations

**Commands requiring confirmation:**
- `mcpproxy upstream enable --all`
- `mcpproxy upstream disable --all`

**Behavior:**

**Interactive mode (TTY detected):**
```bash
$ mcpproxy upstream disable --all
⚠️  This will disable 12 servers. Continue? [y/N]: _
```
- Waits for user input
- Accepts: `y`, `yes` (case-insensitive)
- Rejects: `n`, `no`, Enter, or anything else
- Exit code 1 if user declines

**Non-interactive mode (pipe, script, CI):**
```bash
$ echo "y" | mcpproxy upstream disable --all
Error: --all requires --force flag in non-interactive mode
Exit code: 2
```
- Fails immediately without `--force`
- Clear error message

**Force flag (skips prompt):**
```bash
$ mcpproxy upstream disable --all --force
✅ Successfully disabled 12 servers
```

**Single-server operations (no prompt):**
```bash
$ mcpproxy upstream disable github-server
✅ Successfully disabled server 'github-server'
```

**Rationale:** Both enable and disable can be disruptive. Enable might trigger unwanted connections/API calls. Disable stops services. Both warrant confirmation for bulk operations.

### 6. Output Format Conventions

Follow existing patterns from codebase:

- **Tabular data:** `--output=table|json` (like `tools list`)
  - Used by: `upstream list`

- **Rich formatted output:** `--output=pretty|json` (like `call tool`)
  - Used by: `doctor`, `doctor docker`, etc.

**Rationale:** Semantic consistency - tables for tabular data, pretty for rich diagnostic output.

### 7. Daemon Detection

All commands auto-detect running daemon via socket availability:

```go
socketPath := socket.DetectSocketPath(dataDir)
isDaemonRunning := socket.IsSocketAvailable(socketPath)
```

**Daemon mode (preferred):**
- Uses HTTP API via Unix socket
- Fast (reuses existing connections)
- Full feature set (follow mode, enable/disable/restart)

**Standalone mode (fallback):**
- Direct connection or file reading
- Slower (establishes new connections)
- Limited features (some commands require daemon)

**Commands requiring daemon:**
- `upstream enable/disable/restart`
- `upstream logs --follow`
- `doctor` (all subcommands)

### 8. Progressive Disclosure in Documentation

**CLAUDE.md (brief summary):**
- 15-20 lines with most important commands
- Quick workflow example
- Link to detailed doc

**docs/cli-management-commands.md (comprehensive):**
- Full command reference with all flags
- Output examples
- Daemon vs standalone explanation
- Common workflows and troubleshooting
- Implementation notes

**Rationale:** Keep CLAUDE.md lean and maintainable while providing complete reference separately.

---

## Implementation Structure

### File Organization

```
cmd/mcpproxy/
  upstream_cmd.go         # New: upstream command group
  doctor_cmd.go           # New: doctor command group
  main.go                 # Modified: register new commands

internal/cliclient/
  client.go               # Modified: add API methods

internal/httpapi/
  server.go               # Modified: ensure endpoints exist
```

### Code Patterns

Following existing conventions from `tools_cmd.go` and `call_cmd.go`:

1. Each command file has its own flag variables
2. Shared helper functions for config loading and logger creation
3. Auto-detection of daemon via `socket.DetectSocketPath()` and `socket.IsSocketAvailable()`
4. Client instantiation via `cliclient.NewClient(socketPath, logger)`

### Interactive Confirmation Helper

New shared function in `upstream_cmd.go`:

```go
func confirmBulkAction(action string, count int, force bool) (bool, error)
```

- Checks if stdin is TTY using `term.IsTerminal(int(os.Stdin.Fd()))`
- Shows prompt in interactive mode
- Requires `--force` in non-interactive mode
- Returns (proceed bool, error)

### API Endpoints

Existing endpoints in `internal/httpapi/server.go`:

- `GET /api/v1/servers` - List servers
- `GET /api/v1/servers/{name}/logs?tail=N` - Server logs
- `POST /api/v1/servers/{name}/enable` - Enable server
- `POST /api/v1/servers/{name}/disable` - Disable server
- `POST /api/v1/servers/{name}/restart` - Restart server

New endpoints needed:

- `GET /api/v1/diagnostics` - Run all health checks
- `GET /api/v1/diagnostics/docker` - Docker status
- `GET /api/v1/diagnostics/oauth` - OAuth issues
- `GET /api/v1/diagnostics/secrets` - Missing secrets
- `GET /api/v1/diagnostics/upstream` - Connection errors

---

## Error Handling

### Daemon Not Running

```bash
$ mcpproxy doctor
Error: This command requires running daemon. Start with: mcpproxy serve
Exit code: 1
```

### Server Not Found

```bash
$ mcpproxy upstream restart nonexistent-server
Error: server 'nonexistent-server' not found
Exit code: 1
```

### Empty `--all` Operations

```bash
$ mcpproxy upstream disable --all
⚠️  No servers to disable
Exit code: 0
```

### API Failures

- Connection timeout: "Failed to connect to daemon at socket"
- API error response: Show error message from API
- Exit code: 1

### Log File Not Found (Standalone Mode)

```bash
$ mcpproxy upstream logs github-server
Error: log file not found: ~/.mcpproxy/logs/server-github-server.log
(daemon may not have run yet)
Exit code: 1
```

### Follow Mode Without Daemon

```bash
$ mcpproxy upstream logs github-server --follow
Error: --follow requires running daemon
Exit code: 1
```

---

## Testing Strategy

### Manual Testing

```bash
# Build
go build -o mcpproxy ./cmd/mcpproxy

# Test with daemon running
./mcpproxy serve &
sleep 2
./mcpproxy upstream list
./mcpproxy doctor

# Test without daemon
pkill mcpproxy
./mcpproxy upstream list  # Should show standalone mode

# Test confirmations
./mcpproxy upstream disable --all        # Should prompt
echo "n" | ./mcpproxy upstream disable --all  # Should error
./mcpproxy upstream disable --all --force    # Should succeed

# Test output formats
./mcpproxy upstream list --output=json
./mcpproxy doctor --output=json

# Test follow mode
./mcpproxy upstream logs github-server --follow
```

### Test Matrix

| Command | Daemon Mode | Standalone Mode | Notes |
|---------|-------------|-----------------|-------|
| `upstream list` | ✅ Full status | ✅ Config only | Standalone shows "unknown" |
| `upstream logs` | ✅ Via API | ✅ File read | Follow requires daemon |
| `upstream enable` | ✅ | ❌ | Requires daemon |
| `upstream disable` | ✅ | ❌ | Requires daemon |
| `upstream restart` | ✅ | ❌ | Requires daemon |
| `doctor` | ✅ | ❌ | Requires daemon |

---

## Documentation Plan

### 1. CLAUDE.md Updates

Add brief "CLI Management Commands" section after "Development Commands":

```markdown
### CLI Management Commands

MCPProxy provides CLI commands for managing upstream servers and monitoring health:

```bash
mcpproxy upstream list              # List all servers
mcpproxy upstream logs <name>       # View logs (--tail, --follow)
mcpproxy upstream restart <name>    # Restart server (supports --all)
mcpproxy doctor                     # Run health checks
```

**Common workflow:**
```bash
mcpproxy doctor                     # Check overall health
mcpproxy upstream list              # Identify issues
mcpproxy upstream logs failing-srv  # View logs
mcpproxy upstream restart failing-srv
```

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for complete reference.
```

### 2. New File: docs/cli-management-commands.md

Comprehensive reference including:

- Full command reference with all flags
- Output format examples
- Daemon vs standalone mode explanation
- Common workflows and troubleshooting
- Safety warnings for `--all` flag
- Implementation notes for contributors

### 3. Update Existing "Debugging Guide" in CLAUDE.md

Update to reference new `doctor` command:

```markdown
## Debugging Guide

### Quick Diagnostics

Run this first when debugging any issue:

```bash
mcpproxy doctor
```

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for detailed workflows.
```

### 4. Rename Implementation Plan

Rename `docs/plans/2025-11-19-cli-debugging-commands.md` to `docs/plans/2025-11-19-cli-management-commands.md` to match new framing.

---

## Safety Considerations

### Bulk Operations Documentation

Add prominent warning in documentation:

```markdown
⚠️  **Safety Warning: Bulk Operations**

The `--all` flag affects all servers simultaneously:

- `disable --all` - Stops all upstream connections (reversible with `enable --all`)
- `enable --all` - Activates all servers (may trigger API calls, OAuth flows)
- `restart --all` - Reconnects all servers (may cause brief service disruption)

**Best practices:**
- Use `upstream list` first to see affected servers
- Test with single server before using `--all`
- Use `--force` in automation only when appropriate
```

### Interactive Confirmation UX

Confirmation prompt shows count:

```bash
⚠️  This will disable 12 servers. Continue? [y/N]:
```

Shows what will be affected before proceeding.

---

## Future Enhancements

Not in scope for initial implementation:

1. **Filtering for bulk operations**
   ```bash
   mcpproxy upstream restart --protocol=stdio
   mcpproxy upstream disable --tag=experimental
   ```

2. **Server groups**
   ```bash
   mcpproxy upstream restart @production
   ```

3. **Watch mode**
   ```bash
   mcpproxy upstream list --watch
   ```

4. **Batch operations from file**
   ```bash
   mcpproxy upstream restart --from-file=servers.txt
   ```

---

## Summary

This design provides comprehensive CLI management for MCPProxy:

✅ **Clear naming** - `upstream` and `doctor` avoid confusion with existing commands
✅ **Consistent patterns** - `--all` flag, output formats, daemon detection
✅ **Safety first** - Interactive confirmation for bulk operations
✅ **Progressive disclosure** - Brief docs in CLAUDE.md, detailed reference separate
✅ **Automation-friendly** - `--force` flag, JSON output, exit codes
✅ **Discoverable** - Subcommands over flags, clear help text

Ready for implementation.
