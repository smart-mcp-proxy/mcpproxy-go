# CLI Client Mode

## Overview

MCPProxy CLI commands automatically detect if a daemon is running and use "client mode" to communicate via Unix sockets (macOS/Linux) or named pipes (Windows). This eliminates database locking issues and provides faster execution.

## How It Works

### Detection

1. Check `MCPPROXY_TRAY_ENDPOINT` environment variable
2. Check for socket file: `~/.mcpproxy/mcpproxy.sock` (Unix) or `\\.\pipe\mcpproxy-<username>` (Windows)
3. If socket exists → **Client Mode**
4. If no socket → **Standalone Mode**

### Client Mode

When a daemon is running:
- CLI connects via Unix socket/named pipe
- No API key required (socket = trusted connection)
- HTTP API calls over socket transport
- No database access (no locking issues)
- Faster execution (reuses daemon's connection pool)

### Standalone Mode

When no daemon is running:
- CLI opens database, index, and upstream managers directly
- Full functionality preserved
- Useful for offline or air-gapped environments

## Affected Commands

### `mcpproxy code exec`

Execute JavaScript code:

```bash
# Client mode if daemon running
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'

# Explicitly use standalone mode
MCPPROXY_TRAY_ENDPOINT="" mcpproxy code exec --code="..." --input='{...}'
```

### `mcpproxy call tool`

Call upstream tools:

```bash
# Client mode if daemon running
mcpproxy call tool --tool-name=github:get_user --json-args='{"username":"octocat"}'
```

### `mcpproxy tools list`

List available tools:

```bash
# Client mode if daemon running
mcpproxy tools list --server=github
```

## Troubleshooting

### Force Standalone Mode

Disable socket detection:

```bash
export MCPPROXY_TRAY_ENDPOINT=""
mcpproxy code exec --code="..." --input='{...}'
```

### Custom Socket Path

Use non-default socket location:

```bash
export MCPPROXY_TRAY_ENDPOINT="unix:///tmp/custom.sock"
mcpproxy code exec --code="..." --input='{...}'
```

### Verify Mode

Check logs for mode detection:

```bash
mcpproxy code exec --code="..." --log-level=debug
```

Look for:
- `Detected running daemon, using client mode via socket`
- `No daemon detected, using standalone mode`

## Benefits

1. **No Database Locking** - Multiple CLI commands can run concurrently
2. **No API Keys Needed** - Socket connections are trusted by OS permissions
3. **Faster Execution** - No initialization overhead
4. **Shared State** - All operations go through single daemon instance
5. **Graceful Fallback** - Standalone mode works when daemon isn't running

## Architecture

```
┌─────────────────────────────────────────┐
│ CLI Command                              │
│ (mcpproxy code exec --code="...")       │
└──────────────┬──────────────────────────┘
               │
        ┌──────▼──────┐
        │ Detect Mode │
        └──────┬──────┘
               │
       ┌───────┴───────┐
       │               │
    ┌──▼───┐      ┌───▼────┐
    │Socket│      │No Socket│
    │Exists│      │        │
    └──┬───┘      └───┬────┘
       │              │
┌──────▼──────┐  ┌───▼────────────┐
│Client Mode  │  │Standalone Mode │
│             │  │                │
│• Socket conn│  │• Open database │
│• HTTP API   │  │• Open index    │
│• No API key │  │• Open upstreams│
│• Fast       │  │• Execute local │
└─────────────┘  └────────────────┘
```

## Security

Socket/pipe connections are secured by:
- File system permissions (`0600` - owner-only)
- UID/GID verification (macOS/Linux)
- ACL verification (Windows)
- Same-user enforcement at OS level

This is more secure than API keys because the OS guarantees the client and server belong to the same user.

## Implementation Details

### Socket Detection Module

The `internal/socket/` package provides:
- `DetectSocketPath(dataDir string) string` - Detects socket endpoint
- `IsSocketAvailable(endpoint string) bool` - Checks socket existence
- `CreateDialer(endpoint string)` - Creates platform-specific dialer

### HTTP API Endpoints

CLI commands use these REST API endpoints when in client mode:
- `POST /api/v1/code/exec` - Code execution
- `POST /mcp` - Tool calls via MCP protocol
- `GET /api/v1/status` - Health checks

### Error Handling

When socket connection fails, CLI commands automatically fall back to standalone mode with a warning in debug logs.

## Performance

**Client Mode** (Daemon Running):
- **Startup**: <10ms (socket connection)
- **Execution**: Similar to daemon performance
- **Overhead**: Minimal (HTTP over socket)

**Standalone Mode** (No Daemon):
- **Startup**: 100-500ms (open database, index, upstreams)
- **Execution**: Same as daemon
- **Overhead**: Initialization time

For repeated operations, client mode is 10-50x faster due to zero initialization overhead.
