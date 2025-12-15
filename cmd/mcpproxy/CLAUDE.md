# CLI Commands

## Structure

Cobra CLI in `main.go` with subcommands:
- `serve` - Start core server
- `upstream` - Server management (list, logs, restart, enable, disable)
- `doctor` - Health diagnostics
- `auth` - OAuth management (login, status)
- `tools` - Tool inspection
- `code` - JavaScript execution

## Socket Communication

CLI auto-detects daemon via Unix socket/named pipe:

**Daemon mode** (socket connected):
- Reuses existing server connections
- Shows live daemon state
- Coordinates OAuth tokens

**Standalone mode** (no daemon):
- Direct config file access
- Starts own connections

**Commands requiring daemon**:
- `doctor` - Live diagnostics only
- `auth status` - Live OAuth state

**Standalone-only commands**:
- `secrets` - Direct OS keyring
- `trust-cert` - File system ops
- `search-servers` - Registry API

## Exit Codes (`exit_codes.go`)

| Code | Meaning | Tray Event |
|------|---------|------------|
| 0 | Success | - |
| 1 | General error | EventGeneralError |
| 2 | Port conflict | EventPortConflict |
| 3 | Database locked | EventDBLocked |
| 4 | Config error | EventConfigError |
| 5 | Permission error | EventPermissionError |

## Key Files

- `main.go` - Cobra setup, command routing
- `serve_cmd.go` - Server startup
- `upstream_cmd.go` - Server management
- `tools_cmd.go` - Tool debugging
- `call_cmd.go` - Tool execution
- `exit_codes.go` - Exit code definitions

## Debugging

```bash
./mcpproxy serve --log-level=debug
./mcpproxy tools list --server=name --log-level=trace
```
