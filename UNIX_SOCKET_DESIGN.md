# UNIX Socket Communication Design Document

**Version:** 1.0
**Date:** 2025-10-23
**Status:** Phase 1 - Design Readiness

## Executive Summary

This document outlines the design for introducing Unix domain sockets (macOS/Linux) and named pipes (Windows) as the primary transport for tray ↔ core communication in mcpproxy, while preserving TCP/HTTP access for browsers and remote clients.

## Current Architecture Analysis

### Tray → Core Startup Flow

1. **API Key Generation** (`cmd/mcpproxy-tray/main.go:119-132`)
   - Tray checks `MCPPROXY_API_KEY` environment variable
   - If not set, generates cryptographic random key (32 bytes, hex-encoded)
   - Passes key to core via environment variable

2. **Core Binary Resolution** (`cmd/mcpproxy-tray/main.go:283-298`)
   - Checks `MCPPROXY_CORE_PATH` environment variable
   - Tries bundled binary in app bundle (macOS: `Contents/Resources/bin/mcpproxy`)
   - Falls back to PATH search
   - Stages binary in managed location: `~/Library/Application Support/mcpproxy/bin/`

3. **Process Launch** (`cmd/mcpproxy-tray/main.go:840-908`)
   - Builds command args: `mcpproxy serve --listen <addr> [--config <path>]`
   - Wraps in user shell (`/bin/zsh -l -c 'exec mcpproxy ...'`) for env loading
   - Passes environment including `MCPPROXY_API_KEY`
   - Uses ProcessMonitor for lifecycle management

4. **Health Monitoring** (`cmd/mcpproxy-tray/main.go:910-929`)
   - HealthMonitor polls `/ready` endpoint via HTTP
   - Waits for core to become ready (30s timeout)
   - Sends state machine events: `EventCoreReady` or `EventTimeout`

5. **API Connection** (`cmd/mcpproxy-tray/internal/api/client.go:134-233`)
   - Creates HTTP client with TLS support
   - Establishes SSE connection to `/events` endpoint
   - Sets `X-API-Key` header for authentication
   - Implements exponential backoff retry (10 attempts, max 30s delay)

### Core HTTP Entry Points

Located in `internal/server/server.go:startCustomHTTPServer()`

**Unprotected Endpoints** (no API key required):
- `/mcp`, `/mcp/` - MCP protocol (line 829-830)
- `/v1/tool_code`, `/v1/tool-code` - Legacy MCP (line 833-834)
- `/ui/*` - Web UI (line 853-854)
- `/healthz`, `/readyz`, `/livez`, `/ready`, `/health` - Health checks (line 843-846)
- `/` - Redirect to Web UI (line 856-862)

**Protected Endpoints** (API key required):
- `/api/v1/*` - REST API (line 248, protected by middleware)
- `/events` - SSE stream (line 307, protected by middleware)

### Authentication Middleware

Located in `internal/httpapi/server.go:apiKeyAuthMiddleware()`

**Authentication Flow** (line 120-154):
1. Gets config from controller via `GetCurrentConfig()`
2. If API key is empty → allow through (authentication disabled)
3. Checks `X-API-Key` header (line 159-161)
4. Checks `apikey` query parameter (line 164-166)
5. Returns 401 if neither match

**Applied To** (line 248-251, 307-308):
- All `/api/v1/*` routes via chi router middleware
- `/events` SSE endpoint via explicit middleware wrapper

### HTTP Server Bootstrap

Located in `internal/server/server.go:startCustomHTTPServer()`

**Current Implementation** (line 871-877):
```go
listener, err := net.Listen("tcp", listenAddr)
if err != nil {
    if isAddrInUseError(err) {
        return &PortInUseError{Address: listenAddr, Err: err}
    }
    return fmt.Errorf("failed to bind to %s: %w", listenAddr, err)
}
actualAddr := listener.Addr().String()
```

**Key Observations:**
- Single listener bound to TCP address
- No abstraction for injecting custom listeners
- Direct coupling to `net.Listen("tcp", ...)`

## Socket Location Conventions

### Using Existing Data Directory Configuration

**Key Decision:** Socket location should align with existing `--data-dir` configuration

**Configuration Sources** (priority order):
1. CLI flag: `mcpproxy serve --data-dir=/custom/path`
2. Config file: `"data_dir": "/custom/path"` in `mcp_config.json`
3. Environment: `MCPPROXY_DATA_DIR=/custom/path`
4. Default: `~/.mcpproxy`

### macOS and Linux (Unix Domain Sockets)

**Primary Location:**
```
Default:  ~/.mcpproxy/mcpproxy.sock
Custom:   <data-dir>/mcpproxy.sock
```

**Examples:**
```bash
# Default location
mcpproxy serve
# Socket: ~/.mcpproxy/mcpproxy.sock

# Custom data directory
mcpproxy serve --data-dir=/opt/mcpproxy
# Socket: /opt/mcpproxy/mcpproxy.sock

# Per-project data directory
mcpproxy serve --data-dir=./data
# Socket: ./data/mcpproxy.sock
```

**Rationale:**
- **Consistency:** Socket lives alongside database, logs, and config
- **User-specific:** Default `~/.mcpproxy` is user-specific
- **Flexible:** Respects `--data-dir` for custom deployments
- **No permissions needed:** User controls data directory location
- **Predictable:** Users already know where their data lives

**Permission Model:**
- Socket file: `0600` (user read/write only)
- Data directory: `0700` (user execute/read/write only)
- Ownership checked: Verify connecting UID matches socket owner UID

**Pre-flight Security Validation:**
Core server **MUST NOT START** if data directory permissions are insecure:
- Check data directory exists and is owned by current user
- Check data directory permissions are `0700` or stricter
- If permissions are wrong, **FAIL** with clear error message and exit code 5
- User must fix permissions manually (prevents accidental permission escalation)

**Fallback Path:**
```
Temporary: /tmp/mcpproxy-$UID-$PID.sock
```
- Used if data directory unavailable
- Includes UID and PID for multi-user safety and uniqueness
- Cleaned up on reboot

### Windows (Named Pipes)

**Primary Location:**
```
Default:  \\.\pipe\mcpproxy-$USERNAME
Custom:   \\.\pipe\mcpproxy-$USERNAME-$HASH
```

**Note on Windows:** Named pipes don't support file paths like Unix sockets, so we use the username as the unique identifier. For custom data directories, we append a hash of the data-dir path to ensure uniqueness.

**Examples:**
```powershell
# Default location
mcpproxy serve
# Pipe: \\.\pipe\mcpproxy-johnsmith

# Custom data directory (uses hash for uniqueness)
mcpproxy serve --data-dir=C:\mcpproxy-data
# Pipe: \\.\pipe\mcpproxy-johnsmith-a1b2c3d4
```

**Rationale:**
- **User-specific:** Includes username for isolation
- **Standard location:** Windows named pipe namespace
- **Unique per data-dir:** Hash prevents conflicts when running multiple instances
- **No admin needed:** User can create pipes in own namespace
- **Automatic cleanup:** OS cleans up on process exit

**Security Descriptor:**
- Owner: Current user SID
- ACL: `GENERIC_READ | GENERIC_WRITE` for owner only
- Denial: `DENY_ALL` for Everyone else

**Implementation Library:**
```go
import "github.com/Microsoft/go-winio"
```
- Pure Go, no CGO
- Windows ACL support
- Compatible with net.Listener interface

### Socket Path Resolution Logic

```go
// GetSocketPath returns the Unix socket path based on data directory
func GetSocketPath(dataDir string) string {
    if dataDir == "" {
        dataDir = getDefaultDataDir() // ~/.mcpproxy
    }

    // Expand ~ if present
    if strings.HasPrefix(dataDir, "~/") {
        home, _ := os.UserHomeDir()
        dataDir = filepath.Join(home, dataDir[2:])
    }

    return filepath.Join(dataDir, "mcpproxy.sock")
}

// GetPipeName returns the Windows pipe name based on data directory
func GetPipeName(dataDir string) string {
    username := os.Getenv("USERNAME")
    if username == "" {
        username = "default"
    }

    // If using default data dir, use simple name
    defaultDataDir := getDefaultDataDir()
    if dataDir == "" || dataDir == defaultDataDir {
        return fmt.Sprintf("\\\\.\\pipe\\mcpproxy-%s", username)
    }

    // For custom data dirs, add hash suffix
    hash := sha256.Sum256([]byte(dataDir))
    hashStr := fmt.Sprintf("%x", hash[:4])
    return fmt.Sprintf("\\\\.\\pipe\\mcpproxy-%s-%s", username, hashStr)
}
```

## Security Model

### Connection Trust Model

**Trusted Channel (Socket/Pipe):**
- Tray ↔ Core communication
- No API key required
- Relies on OS-level permissions (UID/GID/SID matching)
- Higher trust due to local-only access

**Untrusted Channel (TCP):**
- Browser ↔ Core communication
- Remote client ↔ Core communication
- API key required for `/api/*` and `/events`
- Lower trust due to network exposure

### Runtime Validation

**Unix Domain Sockets (macOS/Linux):**
```go
// Verify connecting UID matches socket owner
conn, _ := listener.Accept()
ucred, _ := conn.(*net.UnixConn).SyscallConn().Control(...)
if ucred.Uid != os.Getuid() {
    conn.Close()
    return errors.New("UID mismatch")
}
```

**Named Pipes (Windows):**
```go
// go-winio provides SID validation
pipe, _ := winio.ListenPipe(name, &winio.PipeConfig{
    SecurityDescriptor: currentUserOnlySD,
})
// Library automatically enforces ACL
```

### Attack Surface Analysis

**Threats Mitigated:**
1. **API Key Theft:** Tray no longer passes API key over network (even localhost)
2. **Port Conflicts:** Socket file replaces TCP port binding
3. **Network Sniffing:** Socket traffic never leaves the host
4. **Unauthorized Access:** OS enforces UID/SID matching

**Threats Remaining:**
1. **Local Privilege Escalation:** If attacker gains user privileges, they can connect
2. **Socket File Manipulation:** Attacker with user privileges can delete socket
3. **Browser Access:** Still requires API key over TCP

**Mitigation Strategy:**
- Stale socket cleanup on startup
- Permission verification before listening
- Graceful fallback to TCP if socket unavailable

## Endpoint Naming and Configuration

### Automatic Socket Path Detection

**Default Behavior:** Socket path is automatically derived from `--data-dir`:
```bash
# Core: Socket created at <data-dir>/mcpproxy.sock
mcpproxy serve --data-dir=~/.mcpproxy

# Tray: Automatically detects socket at <data-dir>/mcpproxy.sock
mcpproxy-tray
```

**How Tray Finds the Socket:**
1. Check core process info (if tray launched core)
2. Read from config file (`~/.mcpproxy/mcp_config.json`)
3. Check standard data directory (`~/.mcpproxy/mcpproxy.sock`)
4. Fallback to TCP on default port

### Environment Variables

**Existing Variables (Enhanced):**
```bash
MCPPROXY_DATA_DIR=~/.mcpproxy  # Sets data directory (and socket location)
MCPPROXY_LISTEN=127.0.0.1:8080 # TCP listener for browsers
MCPPROXY_API_KEY=...           # API key for TCP connections
```

**New Variables (Optional Override):**
```bash
MCPPROXY_TRAY_ENDPOINT=unix:///custom/path.sock  # Override socket path
MCPPROXY_TRAY_ENDPOINT=http://127.0.0.1:8080     # Force TCP mode
MCPPROXY_TRAY_ENDPOINT=npipe:////./pipe/custom   # Override pipe name (Windows)
```

### CLI Flags

**Existing Flags (Enhanced):**
```bash
mcpproxy serve --data-dir=/custom/path  # Sets data-dir (and socket location)
mcpproxy serve --listen 127.0.0.1:8080  # TCP listener for browsers
mcpproxy serve --api-key <key>          # API key for TCP connections
```

**New Flags (Optional Override):**
```bash
mcpproxy serve --tray-endpoint=unix:///custom/path.sock  # Override socket path
mcpproxy serve --tray-endpoint=http://127.0.0.1:8080     # Force TCP mode
```

### Configuration Priority

**Socket Path Resolution (priority order):**
1. CLI flag `--tray-endpoint` (explicit override)
2. Environment variable `MCPPROXY_TRAY_ENDPOINT` (explicit override)
3. Derived from `--data-dir` (automatic, recommended)
4. Default: `~/.mcpproxy/mcpproxy.sock` (automatic fallback)

**TCP Listener (separate, always available for browsers):**
1. CLI flag `--listen`
2. Environment variable `MCPPROXY_LISTEN`
3. Config file `"listen"` field
4. Default: `127.0.0.1:8080`

**Design Philosophy:**
- **Zero configuration:** Default behavior "just works" without any flags
- **Data-dir alignment:** Socket location follows data directory configuration
- **Override available:** Power users can specify custom socket paths if needed
- **TCP always available:** Browsers and remote clients always use TCP listener

## Dual Listener Architecture

### Listener Abstraction

**Goal:** Support both TCP and socket listeners simultaneously

**Implementation Strategy:**
```go
// New interface for listener management
type ListenerManager interface {
    StartTCPListener(addr string) (net.Listener, error)
    StartUnixListener(path string) (net.Listener, error)
    StartPipeListener(name string) (net.Listener, error)
    StopAll() error
}

// Modified server startup
func (s *Server) Start(ctx context.Context) error {
    cfg := s.runtime.Config()

    // Start TCP listener for browsers (always)
    tcpListener, err := net.Listen("tcp", cfg.Listen)
    if err != nil {
        return err
    }

    // Start tray listener if configured (optional)
    var trayListener net.Listener
    if cfg.TrayEndpoint != "" {
        trayListener, err = s.createTrayListener(cfg.TrayEndpoint)
        if err != nil {
            // Log warning but continue with TCP only
            s.logger.Warn("Failed to create tray listener, falling back to TCP only")
        }
    }

    // Combine listeners
    if trayListener != nil {
        go s.serveTrayListener(ctx, trayListener)
    }
    go s.serveTCPListener(ctx, tcpListener)

    // Wait for shutdown...
}
```

### Connection Tagging

**Goal:** Middleware needs to identify socket vs TCP connections

**Implementation:**
```go
// Context key for connection source
type contextKey string
const connSourceKey contextKey = "connection_source"

// Tag connections in Accept loop
func (s *Server) serveTrayListener(ctx context.Context, ln net.Listener) {
    for {
        conn, err := ln.Accept()
        if err != nil {
            return
        }

        // Tag this connection as tray-origin
        go s.handleConn(context.WithValue(ctx, connSourceKey, "tray"), conn)
    }
}

// Check in middleware
func (s *Server) apiKeyAuthMiddleware() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Trust tray connections
            if source := r.Context().Value(connSourceKey); source == "tray" {
                next.ServeHTTP(w, r)
                return
            }

            // Validate API key for TCP connections
            if !s.validateAPIKey(r, cfg.APIKey) {
                s.writeError(w, http.StatusUnauthorized, "Invalid or missing API key")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

## Tray Client Integration

### Dialer Abstraction

**Goal:** Tray HTTP client should transparently dial socket or TCP

**Implementation:**
```go
// cmd/mcpproxy-tray/internal/api/client.go
func (c *Client) createDialer(endpoint string) func(context.Context, string, string) (net.Conn, error) {
    u, err := url.Parse(endpoint)
    if err != nil {
        return nil
    }

    switch u.Scheme {
    case "unix":
        // Unix domain socket dialer
        return func(ctx context.Context, _, _ string) (net.Conn, error) {
            var d net.Dialer
            return d.DialContext(ctx, "unix", u.Path)
        }
    case "npipe":
        // Windows named pipe dialer
        return func(ctx context.Context, _, _ string) (net.Conn, error) {
            return winio.DialPipeContext(ctx, u.Path)
        }
    default:
        // Standard TCP dialer
        return nil // Use default HTTP transport
    }
}

// Update Client creation
func NewClient(endpoint string, logger *zap.SugaredLogger) *Client {
    transport := &http.Transport{
        TLSClientConfig: createTLSConfig(logger),
    }

    // Override dialer if socket endpoint
    if dialer := createDialer(endpoint); dialer != nil {
        transport.DialContext = dialer
    }

    return &Client{
        baseURL: endpoint,
        httpClient: &http.Client{
            Timeout: 0,
            Transport: transport,
        },
        logger: logger,
        statusCh: make(chan StatusUpdate, 10),
        connectionStateCh: make(chan tray.ConnectionState, 8),
    }
}
```

### URL Rewriting

**Challenge:** HTTP client expects `http://` URLs, but sockets use `unix://`

**Solution:**
```go
func (c *Client) buildURL(path string) (string, error) {
    u, err := url.Parse(c.baseURL)
    if err != nil {
        return "", err
    }

    // Rewrite unix:// to http:// for HTTP semantics
    if u.Scheme == "unix" || u.Scheme == "npipe" {
        u.Scheme = "http"
        u.Host = "localhost" // Dummy host for HTTP
    }

    rel, err := url.Parse(path)
    if err != nil {
        return "", err
    }

    return u.ResolveReference(rel).String(), nil
}
```

## Migration Strategy

### Phase 1: Opt-In (Feature Flag)

**Goal:** Allow testing without disrupting existing deployments

**Implementation:**
```bash
# Enable socket mode explicitly
export MCPPROXY_TRAY_SOCKET_ENABLED=true
./mcpproxy-tray
```

**Behavior:**
- If flag not set → use TCP only (current behavior)
- If flag set → try socket, fallback to TCP on error

### Phase 2: Opt-Out (Default Enabled)

**Goal:** Make socket the default, but allow disabling

**Implementation:**
```bash
# Disable socket mode explicitly
export MCPPROXY_TRAY_SOCKET_ENABLED=false
./mcpproxy-tray
```

**Behavior:**
- If flag not set → use socket with TCP fallback (new default)
- If flag set to `false` → use TCP only

### Phase 3: Socket Only (Deprecate TCP)

**Goal:** Remove TCP tray endpoint entirely

**Behavior:**
- Tray always uses socket
- TCP reserved for browser/remote only
- API key no longer passed to core

## Startup Validation and Security Checks

### Data Directory Permission Validation

**Critical Security Requirement:** Server MUST validate data directory permissions before starting

**Validation Steps:**
```go
func validateDataDirectory(dataDir string) error {
    // 1. Check directory exists
    info, err := os.Stat(dataDir)
    if err != nil {
        if os.IsNotExist(err) {
            // Try to create with secure permissions
            if err := os.MkdirAll(dataDir, 0700); err != nil {
                return fmt.Errorf("cannot create data directory: %w", err)
            }
            return nil // Created with secure permissions
        }
        return fmt.Errorf("cannot access data directory: %w", err)
    }

    // 2. Check it's a directory
    if !info.IsDir() {
        return fmt.Errorf("data path exists but is not a directory: %s", dataDir)
    }

    // 3. Check ownership (Unix only)
    if runtime.GOOS != "windows" {
        stat := info.Sys().(*syscall.Stat_t)
        if stat.Uid != uint32(os.Getuid()) {
            return fmt.Errorf("data directory not owned by current user (uid=%d, expected=%d)", stat.Uid, os.Getuid())
        }
    }

    // 4. Check permissions are secure (Unix only)
    if runtime.GOOS != "windows" {
        perm := info.Mode().Perm()
        // Must be 0700 or stricter (no group/other access)
        if perm & 0077 != 0 {
            return fmt.Errorf("data directory has insecure permissions %o, must be 0700 or stricter (chmod 0700 %s)", perm, dataDir)
        }
    }

    return nil
}
```

**Error Handling:**
```go
func (s *Server) Start(ctx context.Context) error {
    cfg := s.runtime.Config()

    // CRITICAL: Validate data directory security before proceeding
    if err := validateDataDirectory(cfg.DataDir); err != nil {
        s.logger.Error("Data directory security validation failed", zap.Error(err))
        s.logger.Error("Security check failed",
            zap.String("fix", fmt.Sprintf("chmod 0700 %s", cfg.DataDir)))
        os.Exit(5) // Exit code 5 = Permission error (see cmd/mcpproxy/exit_codes.go)
    }

    // Continue with normal startup...
}
```

**User-Facing Error Message:**
```
FATAL: Data directory has insecure permissions

Directory: /home/user/.mcpproxy
Current:   drwxr-xr-x (755)
Required:  drwx------ (700)

Security risk: Other users can read/write mcpproxy data and potentially access the control socket.

To fix, run:
  chmod 0700 /home/user/.mcpproxy

Exit code: 5 (Permission error)
```

**Why Fail Instead of Auto-Fix:**
- **Security principle:** Never auto-escalate or change permissions without user consent
- **Audit trail:** User explicitly acknowledges permission change
- **Prevents accidents:** Avoids unintended permission changes on shared systems
- **Clear responsibility:** User understands the security implications

### Windows Data Directory Validation

**Windows Security Model:**
```go
func validateDataDirectoryWindows(dataDir string) error {
    // Windows uses ACLs instead of Unix permissions
    // Validate current user has full control and others don't

    info, err := os.Stat(dataDir)
    if err != nil {
        if os.IsNotExist(err) {
            // Create with current user-only ACL
            return createSecureDirectoryWindows(dataDir)
        }
        return err
    }

    // Check ACLs using Windows API
    // This is simplified - actual implementation needs Windows ACL checks
    // via syscall or go-winio library

    return nil
}

func createSecureDirectoryWindows(path string) error {
    // Create directory with restrictive ACL (current user only)
    // Implementation requires Windows security descriptor setup
    return os.MkdirAll(path, 0700) // Note: 0700 is ignored on Windows, need ACL
}
```

## Self-Healing and Recovery

### Stale Socket Cleanup

**Problem:** Crashed core leaves socket file behind

**Solution:**
```go
func (s *Server) cleanupStaleSocket(path string) error {
    // Check if socket file exists
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return nil // No cleanup needed
    }

    // Try to connect to socket
    conn, err := net.DialTimeout("unix", path, 1*time.Second)
    if err == nil {
        conn.Close()
        return errors.New("socket is in use")
    }

    // Socket exists but not accepting connections → stale
    s.logger.Info("Removing stale socket file", "path", path)
    return os.Remove(path)
}
```

### Permission Enforcement

**Problem:** Incorrect permissions compromise security

**Solution:**
```go
func (s *Server) ensureSecureSocket(path string) error {
    // Check parent directory permissions
    dir := filepath.Dir(path)
    info, err := os.Stat(dir)
    if err != nil {
        return fmt.Errorf("parent directory missing: %w", err)
    }

    if info.Mode().Perm() != 0700 {
        s.logger.Warn("Fixing insecure directory permissions", "path", dir)
        if err := os.Chmod(dir, 0700); err != nil {
            return fmt.Errorf("cannot secure directory: %w", err)
        }
    }

    // Check socket file permissions
    if info, err := os.Stat(path); err == nil {
        if info.Mode().Perm() != 0600 {
            s.logger.Warn("Fixing insecure socket permissions", "path", path)
            if err := os.Chmod(path, 0600); err != nil {
                return fmt.Errorf("cannot secure socket: %w", err)
            }
        }
    }

    return nil
}
```

### Automatic Retry

**Problem:** Race conditions during startup

**Solution:**
```go
func (s *Server) startUnixListener(path string, retries int) (net.Listener, error) {
    var lastErr error
    for i := 0; i < retries; i++ {
        // Cleanup stale socket
        if err := s.cleanupStaleSocket(path); err != nil {
            lastErr = err
            time.Sleep(time.Second)
            continue
        }

        // Ensure directory exists with correct permissions
        dir := filepath.Dir(path)
        if err := os.MkdirAll(dir, 0700); err != nil {
            lastErr = err
            time.Sleep(time.Second)
            continue
        }

        // Create listener
        ln, err := net.Listen("unix", path)
        if err != nil {
            lastErr = err
            time.Sleep(time.Second)
            continue
        }

        // Set permissions
        if err := os.Chmod(path, 0600); err != nil {
            ln.Close()
            lastErr = err
            time.Sleep(time.Second)
            continue
        }

        return ln, nil
    }

    return nil, fmt.Errorf("failed after %d retries: %w", retries, lastErr)
}
```

## Testing Strategy

### Unit Tests

**Socket Creation:**
```go
func TestUnixListenerCreation(t *testing.T) {
    tmpDir := t.TempDir()
    socketPath := filepath.Join(tmpDir, "test.sock")

    ln, err := net.Listen("unix", socketPath)
    require.NoError(t, err)
    defer ln.Close()

    // Verify permissions
    info, err := os.Stat(socketPath)
    require.NoError(t, err)
    assert.Equal(t, os.FileMode(0755), info.Mode().Perm()) // Note: umask may affect this
}
```

**Stale Socket Cleanup:**
```go
func TestStaleSocketCleanup(t *testing.T) {
    tmpDir := t.TempDir()
    socketPath := filepath.Join(tmpDir, "stale.sock")

    // Create stale socket file
    f, err := os.Create(socketPath)
    require.NoError(t, err)
    f.Close()

    // Cleanup should succeed
    err = cleanupStaleSocket(socketPath)
    assert.NoError(t, err)

    // Socket should be gone
    _, err = os.Stat(socketPath)
    assert.True(t, os.IsNotExist(err))
}
```

### Integration Tests

**Tray → Core Communication:**
```go
func TestTraySocketCommunication(t *testing.T) {
    // Start core with socket endpoint
    core := startTestCore(t, "--tray-endpoint=unix:///tmp/test.sock")
    defer core.Stop()

    // Wait for socket to be ready
    require.Eventually(t, func() bool {
        _, err := os.Stat("/tmp/test.sock")
        return err == nil
    }, 5*time.Second, 100*time.Millisecond)

    // Connect tray client
    client := api.NewClient("unix:///tmp/test.sock", logger)

    // Test API call
    servers, err := client.GetServers()
    require.NoError(t, err)
    assert.NotEmpty(t, servers)
}
```

**TCP Fallback:**
```go
func TestTCPFallbackOnSocketFailure(t *testing.T) {
    // Start core without socket endpoint (TCP only)
    core := startTestCore(t, "--listen=127.0.0.1:8081")
    defer core.Stop()

    // Tray client tries socket first, falls back to TCP
    client := api.NewClient("http://127.0.0.1:8081", logger)

    servers, err := client.GetServers()
    require.NoError(t, err)
    assert.NotEmpty(t, servers)
}
```

### Platform-Specific Tests

**macOS/Linux:**
- UID verification
- Permission bits (0600/0700)
- Stale socket cleanup
- Multi-user isolation

**Windows:**
- SID verification
- Named pipe ACLs
- Per-user pipe namespace
- Cleanup on process exit

## Dependencies

### Go Standard Library
- `net` - Socket and listener interfaces
- `net/http` - HTTP server over sockets
- `os` - File operations
- `syscall` - UID/GID verification
- `context` - Cancellation and timeouts
- `time` - Retry delays
- `path/filepath` - Cross-platform path handling

### Third-Party Libraries
- **`github.com/Microsoft/go-winio`** (Windows only)
  - Named pipe support
  - Windows ACL management
  - Pure Go, no CGO
  - Used by Docker, Kubernetes (battle-tested)

### No New External Dependencies for Unix
- All Unix socket functionality available in stdlib
- Optional `golang.org/x/sys` for advanced permission checks

## Open Questions

1. **Should we support multiple simultaneous tray connections?**
   - Current design: One tray per core (1:1)
   - Alternative: Multiple trays per core (1:N)
   - Recommendation: Start with 1:1, expand if needed

2. **Should browser access remain TCP-only?**
   - Current design: Yes (browsers can't connect to sockets)
   - Alternative: Proxy socket to TCP for browsers
   - Recommendation: Keep TCP for browsers (no change needed)

3. **Should we expose socket endpoint in API?**
   - Current design: No (implementation detail)
   - Alternative: Advertise via `/api/v1/status`
   - Recommendation: Keep internal for Phase 1

4. **Migration timeline?**
   - Phase 1 (Opt-In): 1-2 releases
   - Phase 2 (Default): 2-4 releases after Phase 1
   - Phase 3 (Socket Only): TBD based on feedback

## Next Steps (Phase 2)

1. **Implement listener abstraction** (`internal/server/listener.go`)
   - `ListenerManager` interface
   - Unix domain socket helper (`listener_unix.go`)
   - Windows named pipe helper (`listener_windows.go`)

2. **Refactor server bootstrap** (`internal/server/server.go:startCustomHTTPServer`)
   - Accept injected listeners
   - Support dual TCP + socket listeners
   - Tag connections with source

3. **Update authentication middleware** (`internal/httpapi/server.go:apiKeyAuthMiddleware`)
   - Check connection source context
   - Skip API key validation for tray connections
   - Maintain validation for TCP connections

4. **Extend tray client** (`cmd/mcpproxy-tray/internal/api/client.go`)
   - Socket-aware dialer
   - URL scheme handling (`unix://`, `npipe://`)
   - Automatic fallback logic

5. **Add comprehensive tests**
   - Unit tests for socket creation
   - Integration tests for tray-core communication
   - Platform-specific permission tests

---

**Review Status:** ✅ Ready for Phase 2 Implementation
**Approver:** _______________
**Date:** _______________
