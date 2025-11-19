# Tray-Core Socket Communication

MCPProxy uses platform-specific local IPC for secure, low-latency communication between the tray application and core server.

## Architecture

- **Dual Listener Design**: Core server accepts connections on both TCP (for browsers/remote) and socket/pipe (for tray)
- **Unified Socket Transport**: Tray uses socket/pipe for ALL communication - both API calls AND Server-Sent Events (SSE)
- **No Hybrid Mode**: All HTTP traffic (including persistent SSE connection) is routed through the socket - no TCP fallback
- **Automatic Detection**: Tray auto-detects socket path from data directory configuration
- **Zero Configuration**: Works out-of-the-box with no manual setup required
- **Platform-Specific**: Unix sockets (macOS/Linux), Named pipes (Windows)

## Security Model (8 layers)

1. **Data Directory Permissions**: Must be `0700` (user-only access) or server refuses to start (exit code 5)
2. **Socket File Permissions**: Created with `0600` (user read/write only)
3. **UID Verification**: Server verifies connecting process belongs to same user
4. **GID Verification**: Group ownership validated on macOS/Linux
5. **SID/ACL Verification**: Windows ACLs ensure current user-only access
6. **Stale Socket Cleanup**: Automatic removal of leftover socket files from crashed processes
7. **Ownership Validation**: Socket file ownership verified before use
8. **Connection Source Tagging**: Middleware distinguishes socket vs TCP connections

## API Key Authentication

- **Socket/Pipe connections**: Trusted by default (skip API key validation)
- **TCP connections**: Require API key authentication
- **Middleware**: `internal/httpapi/server.go` checks connection source via context

## File Locations

- **macOS/Linux**: `<data-dir>/mcpproxy.sock` (default: `~/.mcpproxy/mcpproxy.sock`)
- **Windows**: `\\.\pipe\mcpproxy-<username>` (or hashed for custom data-dir)
- **Override**: `--tray-endpoint` flag or `MCPPROXY_TRAY_ENDPOINT` environment variable

## Implementation Files

- `internal/server/listener.go` - Listener manager and abstraction layer
- `internal/server/listener_unix.go` - Unix socket implementation (macOS/Linux)
- `internal/server/listener_darwin.go` - macOS-specific peer credential verification
- `internal/server/listener_linux.go` - Linux-specific peer credential verification
- `internal/server/listener_windows.go` - Windows named pipe implementation
- `internal/server/listener_mux.go` - Multiplexing listener combining TCP + socket/pipe
- `cmd/mcpproxy-tray/internal/api/dialer.go` - Tray client socket dialer with auto-detection
- `cmd/mcpproxy-tray/internal/api/dialer_unix.go` - Unix socket dialer (macOS/Linux)
- `cmd/mcpproxy-tray/internal/api/dialer_windows.go` - Named pipe dialer (Windows)
- `cmd/mcpproxy-tray/internal/api/client.go` - HTTP client with socket transport (lines 100-118, 318-377)

## How SSE Works Over Socket

The tray application uses a unified HTTP client that routes all traffic through the socket:

1. **Custom HTTP Transport**: Creates `http.Transport` with socket-based `DialContext` function
2. **API Calls**: Standard HTTP requests (`GET /api/v1/info`, `POST /api/v1/servers/{name}/enable`) use socket transport
3. **SSE Connection**: Persistent HTTP connection to `/events` endpoint also uses socket transport
4. **Real-time Updates**: Core sends `event: status` messages with `listen_addr` field for tray UI updates
5. **Single Source of Truth**: Tray UI reads `listen_addr` exclusively from SSE status events (no local fallbacks)

## Configuration

Socket/pipe communication is **enabled by default**. You can disable it using:

### Command-line Flag
```bash
# Disable socket communication (clients will use TCP + API key)
./mcpproxy serve --enable-socket=false

# Explicitly enable (default behavior)
./mcpproxy serve --enable-socket=true
```

### JSON Configuration File
```json
{
  "listen": "127.0.0.1:8080",
  "enable_socket": false,
  "mcpServers": [...]
}
```

### When Running via Tray (Launchpad/Autostart)

If you're running the core server via the tray application (e.g., automatically at startup), and want to disable socket communication:

- Edit your config file at `~/.mcpproxy/mcp_config.json`
- Add or update: `"enable_socket": false`
- Restart the core server (via tray menu: "Stop Core" → "Start Core")

When socket communication is disabled, the tray application will fall back to TCP connections using the auto-generated API key.

## Usage Examples

```bash
# Default: Socket auto-created in data directory
./mcpproxy serve

# Disable socket, use TCP only
./mcpproxy serve --enable-socket=false

# Custom socket path
./mcpproxy serve --tray-endpoint=unix:///tmp/custom.sock

# Windows named pipe
mcpproxy.exe serve --tray-endpoint=npipe:////./pipe/mycustompipe

# Verify socket creation
ls -la ~/.mcpproxy/mcpproxy.sock
# Should show: srw------- (socket, user-only permissions)

# Tray automatically connects via socket (no API key needed)
./mcpproxy-tray
```

## Testing

- Unit tests: `internal/server/listener_test.go` (13 tests covering TCP, Unix socket, permissions, multiplexing)
- Dialer tests: `cmd/mcpproxy-tray/internal/api/dialer_test.go` (14 tests covering dialers, auto-detection, URL parsing)
- E2E tests: `internal/server/socket_e2e_test.go` (3 scenarios: socket without API key, TCP with/without API key, concurrent requests)
