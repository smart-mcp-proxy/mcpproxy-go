# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MCPProxy is a Go-based desktop application that acts as a smart proxy for AI agents using the Model Context Protocol (MCP). It provides intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

## Architecture: Core + Tray Split

**Current Architecture** (Next Branch):
- **Core Server** (`mcpproxy`): Headless HTTP API server with MCP proxy functionality
- **Tray Application** (`mcpproxy-tray`): Standalone GUI application that manages the core server

**Key Benefits**:
- **Auto-start**: Tray automatically starts core server if not running
- **Port conflict resolution**: Built-in detection and handling
- **Independent operation**: Core can run without tray (headless mode)
- **Real-time sync**: Tray updates via SSE connection to core API

## Development Commands

### Build
```bash
# Build for current platform
go build -o mcpproxy ./cmd/mcpproxy

# Cross-platform build script (builds for multiple architectures)
./scripts/build.sh

# Quick local build
scripts/build.sh

#Build frontend and backend
make build
```

### Icon Generation
```bash
# Generate all icon files (PNG for macOS/Linux, ICO for Windows)
# Requires: inkscape, imagemagick (convert), python3 with Pillow
./scripts/logo-convert.sh

# Generate Windows ICO file only (requires Python 3 with Pillow)
python3 scripts/create-ico.py

# Install Pillow if needed
pip install --break-system-packages Pillow
# or
python3 -m pip install --break-system-packages Pillow
```

**Note**: The Windows tray icon uses `.ico` format for better compatibility with the Windows system tray. The macOS/Linux versions use `.png` format. If you modify the logo, regenerate all icon files using the script above.

### Windows Installer
```bash
# Build Windows installer (requires Windows environment)
# Prerequisites: Inno Setup 6+ or WiX Toolset 4.x

# Using Inno Setup (recommended - single multi-arch installer):
# Install Inno Setup via Chocolatey
choco install innosetup -y

# Build installer for specific architecture
.\scripts\build-windows-installer.ps1 -Version "v1.0.0" -Arch "amd64"
.\scripts\build-windows-installer.ps1 -Version "v1.0.0" -Arch "arm64"

# Using WiX Toolset 4.x (alternative - MSI format):
# Install WiX as .NET global tool
dotnet tool install --global wix

# Build MSI installer
wix build -arch x64 -d Version=1.0.0.0 -d BinPath=dist\windows-amd64 wix\Package.wxs

# Output: dist\mcpproxy-setup-{version}-{arch}.exe (Inno Setup)
#     or: dist\mcpproxy-{version}-windows-{arch}.msi (WiX)
```

**Note**: Windows installers automatically configure system PATH, create Start Menu shortcuts, and support in-place upgrades. Inno Setup produces a single multi-architecture installer, while WiX generates separate MSI files per architecture. Both support silent installation (`/VERYSILENT` for Inno Setup, `/qn` for WiX MSI).

### Prerelease Builds

- **`main` branch**: Stable releases
- **`next` branch**: Prerelease builds with latest features
- macOS DMG installers are signed and notarized

See `docs/prerelease-builds.md` for download instructions and workflow details.

### Testing

**IMPORTANT: Always run tests before committing changes!**

```bash
# Quick API E2E test (required before commits)
./scripts/test-api-e2e.sh

# Full test suite (recommended before major commits)
./scripts/run-all-tests.sh

# Run unit tests
go test ./internal/... -v

# Run unit tests with race detection
go test -race ./internal/... -v

# Run original E2E tests (internal mocks)
./scripts/run-e2e-tests.sh

# Run binary E2E tests (with built mcpproxy)
go test ./internal/server -run TestBinary -v

# Run MCP protocol E2E tests
go test ./internal/server -run TestMCP -v

# Run specific test package
go test ./internal/server -v

# Run tests with coverage
go test -coverprofile=coverage.out ./internal/...
go tool cover -html=coverage.out
```

#### E2E Test Requirements

The E2E tests use `@modelcontextprotocol/server-everything` which provides:
- **Echo tools** for testing basic functionality
- **Math operations** for complex calculations
- **String manipulation** for text processing
- **File operations** (sandboxed)
- **Error simulation** for error handling tests

**Prerequisites for E2E tests:**
- Node.js and npm installed (for everything server)
- `jq` installed for JSON parsing
- Built mcpproxy binary: `go build -o mcpproxy ./cmd/mcpproxy`

**Test failure investigation:**
- Check `/tmp/mcpproxy_e2e.log` for server logs
- Verify everything server is connecting: look for "Everything server is connected!"
- Ensure no port conflicts on 8081

### Linting
```bash
# Run linter (requires golangci-lint v1.59.1+)
./scripts/run-linter.sh

# Or directly
golangci-lint run ./...
```

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

### CLI Socket Communication

CLI commands automatically detect and use Unix socket/named pipe communication when the daemon is running:

**Commands with socket support:**
- `upstream list/logs/enable/disable/restart` - Daemon connection with standalone fallback
- `doctor` - **Requires daemon** (shows live diagnostics)
- `call tool` - Daemon connection with standalone fallback
- `code exec` - Daemon connection with standalone fallback
- `tools list` - Daemon connection with standalone fallback (NEW)
- `auth login` - Daemon connection with standalone fallback (NEW)
- `auth status` - **Requires daemon** (shows live OAuth state) (NEW)

**Benefits of socket mode:**
- Reuses daemon's existing server connections (faster)
- Shows real daemon state (not config file state)
- Coordinates OAuth tokens with running daemon
- No redundant server connection overhead

**Standalone commands** (no socket support needed):
- `secrets` - Direct OS keyring operations
- `trust-cert` - File system operations
- `search-servers` - Registry API operations

See `docs/socket-communication.md` for socket implementation details.

### Running the Application

#### Core + Tray Architecture (Current)

MCPProxy is split into two separate applications:

1. **Core Server** (`mcpproxy`): Headless API server
2. **Tray Application** (`mcpproxy-tray`): GUI management interface

```bash
# Build both applications
CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy          # Core server
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray  # Tray app

# Start core server (required) - binds to localhost by default for security
./mcpproxy serve

# Start core server on all interfaces (CAUTION: Network exposure)
./mcpproxy serve --listen :8080

# Start with custom API key
./mcpproxy serve --api-key="your-secret-key"

# Start tray application (optional, connects to core via API with auto-generated API key)
./mcpproxy-tray

# Custom configuration
./mcpproxy serve --config=/path/to/config.json

# Debug mode with trace logging
./mcpproxy serve --log-level=debug

# Debug specific server tools
./mcpproxy tools list --server=github-server --log-level=trace
```

#### Tray Application Features
- **Auto-starts core server** if not running
- **Port conflict resolution** built-in
- **Real-time updates** via Server-Sent Events (SSE) over socket/pipe connection
- **Cross-platform** system tray integration
- **Server management** via GUI menus

#### Tray Application Architecture (Refactored)

The tray application uses a robust state machine architecture for reliable core management:

**State Machine States**:
- `StateInitializing` → `StateLaunchingCore` → `StateWaitingForCore` → `StateConnectingAPI` → `StateConnected`
- Error states: `StateCoreErrorPortConflict`, `StateCoreErrorDBLocked`, `StateCoreErrorGeneral`, `StateCoreErrorConfig`
- Recovery states: `StateReconnecting`, `StateFailed`, `StateShuttingDown`

**Key Components**:
- **Process Monitor** (`cmd/mcpproxy-tray/internal/monitor/process.go`): Monitors core subprocess lifecycle
- **Health Monitor** (`cmd/mcpproxy-tray/internal/monitor/health.go`): Performs socket-aware HTTP health checks on core API (`/healthz`, `/readyz`)
- **State Machine** (`cmd/mcpproxy-tray/internal/state/machine.go`): Manages state transitions and automatic retry logic

**Error Classification**:
Core process exit codes are mapped to specific state machine events:
- Exit code 2 (port conflict) → `EventPortConflict`
- Exit code 3 (database locked) → `EventDBLocked`
- Exit code 4 (config error) → `EventConfigError`
- Exit code 5 (permission error) → `EventPermissionError`
- Other errors → `EventGeneralError`

**Automatic Retry Logic**:
Error states automatically retry core launch with exponential backoff:
- `StateCoreErrorGeneral`: 2 retries with 3s delay (3 total attempts)
- `StateCoreErrorPortConflict`: 2 retries with 10s delay
- `StateCoreErrorDBLocked`: 3 retries with 5s delay
- After max retries exceeded → transitions to `StateFailed`
- Retry count and attempts logged for transparency

**Development Environment Variables**:
- `MCPPROXY_TRAY_SKIP_CORE=1` - Skip core launch (for development)
- `MCPPROXY_CORE_URL=http://localhost:8085` - Custom core URL
- `MCPPROXY_TRAY_PORT=8090` - Custom tray port

## Architecture Overview

### Core Components

- **`cmd/mcpproxy/`** - Main CLI application entry point
  - `main.go` - Cobra CLI setup and command routing
  - `tools_cmd.go` - Tools debugging commands
  - `call_cmd.go` - Tool execution commands
  - `tray_gui.go`/`tray_stub.go` - System tray interface (build-tagged)

- **`internal/runtime/`** - Core runtime lifecycle management (Phase 1-3 refactoring)
  - `runtime.go` - Non-HTTP lifecycle, configuration, and state management
  - `event_bus.go` - Event system for real-time updates and SSE integration
  - `lifecycle.go` - Background initialization, connection management, and tool indexing
  - `events.go` - Event type definitions and payload structures

- **`internal/server/`** - HTTP server and MCP proxy implementation
  - `server.go` - HTTP server management and delegation to runtime
  - `mcp.go` - MCP protocol implementation and tool routing

- **`internal/httpapi/`** - REST API endpoints with chi router
  - `server.go` - `/api/v1` endpoints, SSE events, and server controls

- **`internal/upstream/`** - Modular client architecture (3-layer design)
  - `core/` - Basic MCP client (stateless, transport-agnostic)
  - `managed/` - Production client (state management, retry logic)
  - `cli/` - Debug client (enhanced logging, single operations)

- **`internal/config/`** - Configuration management
  - `config.go` - Configuration structures and validation
  - `loader.go` - File loading and environment variable handling

- **`internal/index/`** - Full-text search using Bleve
  - BM25 search index for tool discovery
  - Automatic tool indexing and updates

- **`internal/storage/`** - BBolt database for persistence
  - Tool statistics and metadata
  - Server configurations and quarantine status

- **`internal/cache/`** - Response caching layer
- **`cmd/mcpproxy-tray/`** - Standalone system tray application (separate binary)
  - `main.go` - Core process launcher with state machine integration
  - `internal/state/` - State machine for core lifecycle management
  - `internal/monitor/` - Process and health monitoring systems
  - `internal/api/` - Enhanced API client with exponential backoff
- **`internal/logs/`** - Structured logging with per-server log files
- **`internal/management/`** - Centralized management service layer
  - `service.go` - Business logic for server lifecycle operations
  - Shared by CLI, REST API, and MCP protocol interfaces
  - Configuration gates, event emission, and bulk operations

### Management Service Architecture

The management service (`internal/management/`) provides a centralized business logic layer for upstream server management operations, eliminating code duplication across CLI, REST API, and MCP interfaces.

**Architecture Diagram**:
```
┌─────────────────────────────────────────────────────────────┐
│                    Client Interfaces                         │
├───────────────┬─────────────────┬───────────────────────────┤
│  CLI Commands │   REST API      │   MCP Protocol            │
│  (upstream)   │   (/api/v1/*)   │   (upstream_servers tool) │
└───────┬───────┴────────┬────────┴───────────┬───────────────┘
        │                │                    │
        └────────────────┼────────────────────┘
                         │
                         ▼
              ┌─────────────────────┐
              │  Management Service │
              │  (internal/mgmt/)   │
              └──────────┬──────────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
  ┌──────────┐    ┌──────────┐    ┌──────────┐
  │ Runtime  │    │  Config  │    │  Events  │
  │Operations│    │  Gates   │    │  Emitter │
  └──────────┘    └──────────┘    └──────────┘
```

**Key Components**:

- **Service Interface** (`service.go:16-102`): Defines all management operations
  - Single-server: `RestartServer()`, `EnableServer()`, `DisableServer()`
  - Bulk operations: `RestartAll()`, `EnableAll()`, `DisableAll()`
  - Diagnostics: `GetServerHealth()`, `RunDiagnostics()`
  - Server CRUD: `AddServer()`, `RemoveServer()`, `QuarantineServer()`
  - Tool operations: `GetServerTools()`, `TriggerOAuthLogin()` (added in spec 005)

- **Configuration Gates**: All operations respect centralized configuration guards
  - `disable_management`: Blocks all write operations when true
  - `read_only_mode`: Blocks all configuration modifications

- **Bulk Operations** (`service.go:243-388`): Efficient multi-server management
  - Sequential execution with partial failure handling
  - Returns `BulkOperationResult` with success/failure counts
  - Collects per-server errors in results map
  - Continues on individual failures, reports aggregate results

- **Event Integration**: All operations emit events through event bus
  - `servers.changed`: Notifies UI of server state changes
  - Triggers SSE updates to web UI and tray application
  - Enables real-time synchronization across interfaces

**Benefits**:
- **Code Deduplication**: 40%+ reduction in duplicate code across interfaces
- **Consistent Behavior**: All interfaces use identical business logic
- **Centralized Validation**: Configuration gates enforced in one place
- **Easier Testing**: Unit tests cover all interfaces through service layer
- **Future Extensibility**: New interfaces can reuse existing service methods

**Usage Examples**:

```go
// CLI usage (cmd/mcpproxy/upstream_cmd.go:547-636)
result, err := client.RestartAll(ctx)
fmt.Printf("  Total servers:      %d\n", result.Total)
fmt.Printf("  ✅ Successful:      %d\n", result.Successful)
fmt.Printf("  ❌ Failed:          %d\n", result.Failed)

// REST API usage (internal/httpapi/server.go:772-866)
mgmtSvc := s.controller.GetManagementService().(ManagementService)
result, err := mgmtSvc.RestartAll(r.Context())
s.writeSuccess(w, result)

// MCP protocol usage (future integration)
result, err := mgmtService.RestartAll(ctx)
return mcpResponse(result)
```

**OpenAPI Documentation**: All REST endpoints are documented with OpenAPI 3.1 annotations and auto-generated Swagger spec. See `docs/swagger.yaml` for complete API reference.

### Tray-Core Communication (Unix Sockets / Named Pipes)

MCPProxy uses platform-specific local IPC for secure communication between tray and core:

- **Platform-Specific**: Unix sockets (macOS/Linux), Named pipes (Windows)
- **Zero Configuration**: Auto-detects socket path from data directory
- **Socket connections**: Trusted by default (skip API key validation)
- **TCP connections**: Require API key authentication

**File Locations**:
- **macOS/Linux**: `~/.mcpproxy/mcpproxy.sock`
- **Windows**: `\\.\pipe\mcpproxy-<username>`

**Disable socket** (use TCP + API key instead):
```bash
./mcpproxy serve --enable-socket=false
```

See `docs/socket-communication.md` for complete security model, implementation details, and testing info.

### Key Features

1. **Tool Discovery** - BM25 search across all upstream MCP server tools
2. **Security Quarantine** - Automatic quarantine of new servers to prevent Tool Poisoning Attacks
3. **Docker Security Isolation** - Run stdio MCP servers in isolated Docker containers for enhanced security
4. **JavaScript Code Execution** - Execute JavaScript to orchestrate multiple upstream tools in a single request, reducing latency and enabling complex workflows
5. **OAuth 2.1 Support** - RFC 8252 compliant OAuth with PKCE for secure authentication
6. **System Tray UI** - Native cross-platform tray interface for server management
7. **Per-Server Logging** - Individual log files for each upstream server
8. **Real-time Event System** - Event bus with SSE integration for live updates (Phase 3 refactoring)
9. **Hot Configuration Reload** - Real-time config changes with event notifications
10. **Unix Socket/Named Pipe IPC** - Secure local communication between tray and core without API keys (macOS/Linux: Unix sockets, Windows: Named pipes)

## Configuration

### Default Locations
- **Config**: `~/.mcpproxy/mcp_config.json`
- **Data**: `~/.mcpproxy/config.db` (BBolt database)
- **Index**: `~/.mcpproxy/index.bleve/` (search index)
- **Logs**: `~/.mcpproxy/logs/` (main.log + per-server logs)

### Example Configuration
```json
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "api_key": "your-secret-api-key-here",
  "enable_socket": true,
  "enable_web_ui": true,
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "mcpServers": [
    {
      "name": "github-server",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "enabled": true
    },
    {
      "name": "ast-grep-project",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "/home/user/projects/myproject",
      "protocol": "stdio",
      "env": { "API_KEY": "your-api-key" },
      "enabled": true
    }
  ]
}
```

### Environment Variables

MCPProxy supports several environment variables for configuration and security:

**Security Configuration**:
- `MCPPROXY_LISTEN` - Override network binding (e.g., `127.0.0.1:8080`, `:8080`)
- `MCPPROXY_API_KEY` - Set API key for REST API authentication

**Debugging**:
- `MCPPROXY_DEBUG` - Enable debug mode
- `MCPPROXY_DISABLE_OAUTH` - Disable OAuth for testing
- `HEADLESS` - Run in headless mode (no browser launching)

**Tray-Core Communication**:
- `MCPPROXY_API_KEY` - Shared API key for tray-core authentication (auto-generated if not set)
- `MCPPROXY_TLS_ENABLED` - Enable TLS/HTTPS for both tray and core (automatically passed through)
- `MCPPROXY_TRAY_SKIP_CORE` - Skip core launch in tray app (for development)
- `MCPPROXY_CORE_URL` - Custom core URL for tray to connect to

**Examples**:
```bash
# Start with custom network binding
export MCPPROXY_LISTEN=":8080"
./mcpproxy serve

# Start with custom API key
export MCPPROXY_API_KEY="my-secret-key"
./mcpproxy serve

# Disable authentication for testing
export MCPPROXY_API_KEY=""
./mcpproxy serve

# Run in headless mode
export HEADLESS=true
./mcpproxy serve
```

### Working Directory Configuration

The `working_dir` field specifies the working directory for stdio MCP servers. Useful for file-based servers (`ast-grep-mcp`, `filesystem-mcp`, `git-mcp`) to operate on your project directories instead of mcpproxy's directory.

- Empty/unspecified `working_dir` uses current directory (default behavior)
- If directory doesn't exist, server startup fails with detailed error
- Compatible with Docker isolation (`isolation.working_dir` for container path)

## MCP Protocol Implementation

### Built-in Tools
- **`retrieve_tools`** - BM25 keyword search across all upstream tools
- **`call_tool`** - Proxy tool calls to upstream servers
- **`code_execution`** - Execute JavaScript to orchestrate multiple upstream tools (disabled by default)
- **`upstream_servers`** - CRUD operations for server management

### Tool Name Format
- Format: `<serverName>:<originalToolName>` (e.g., `github:create_issue`)
- Tools are automatically prefixed with server names to prevent conflicts

### HTTP API Endpoints

The HTTP API provides REST endpoints for server management and monitoring:

**Base Path**: `/api/v1` (legacy `/api` routes removed in Phase 4)

**Core Endpoints**:
- `GET /api/v1/status` - Server status and statistics
- `GET /api/v1/servers` - List all upstream servers with connection status
- `POST /api/v1/servers/{name}/enable` - Enable/disable server
- `POST /api/v1/servers/{name}/quarantine` - Quarantine/unquarantine server
- `GET /api/v1/tools` - Search tools across all servers
- `GET /api/v1/servers/{name}/tools` - List tools for specific server

**Real-time Updates**:
- `GET /events` - Server-Sent Events (SSE) stream for live updates
- Streams both status changes and runtime events (`servers.changed`, `config.reloaded`)
- Used by web UI and tray for real-time synchronization

**API Authentication Examples**:
```bash
# Using X-API-Key header (recommended for curl)
curl -H "X-API-Key: your-api-key" http://127.0.0.1:8080/api/v1/servers

# Using query parameter (for browser/SSE)
curl "http://127.0.0.1:8080/api/v1/servers?apikey=your-api-key"

# SSE with API key
curl "http://127.0.0.1:8080/events?apikey=your-api-key"

# Open Web UI with API key (tray app does this automatically)
open "http://127.0.0.1:8080/ui/?apikey=your-api-key"
```

**Security Notes**:
- **MCP endpoints (`/mcp`, `/mcp/`)** remain **unprotected** for client compatibility
- **REST API** requires authentication when API key is configured
- **Empty API key** disables authentication (useful for testing)

## JavaScript Code Execution

The `code_execution` tool enables orchestrating multiple upstream MCP tools in a single request using sandboxed JavaScript (ES5.1+).

### Configuration

```json
{
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10
}
```

### CLI Usage

```bash
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'
mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'
```

### Documentation

See `docs/code_execution/` for complete guides:
- `overview.md` - Architecture and best practices
- `examples.md` - 13 working code samples
- `api-reference.md` - Complete schema documentation
- `troubleshooting.md` - Common issues and solutions

## Security Model

### Network Security
- **Localhost-only binding by default**: Core server binds to `127.0.0.1:8080` by default to prevent network exposure
- **Override options**: Can be changed via `--listen` flag, `MCPPROXY_LISTEN` environment variable, or config file
- **API key authentication**: REST API endpoints protected with optional API key authentication
- **MCP endpoints open**: MCP protocol endpoints (`/mcp`, `/mcp/`) remain unprotected for client compatibility

### API Key Authentication
- **Automatic generation**: API key generated if not provided and logged for easy access
- **Multiple authentication methods**: Supports `X-API-Key` header and `?apikey=` query parameter
- **Tray integration**: Tray app automatically generates and manages API keys for core communication
- **Configuration options**: Set via `--api-key` flag, `MCPPROXY_API_KEY` environment variable, or config file
- **Optional protection**: Empty API key disables authentication (useful for testing)
- **Protected endpoints**: `/api/v1/*` and `/events` (SSE) require authentication when enabled

#### Tray-Core API Key Coordination
The tray application ensures secure communication with the core process through coordinated API key management:

1. **Environment Variable Priority**: If `MCPPROXY_API_KEY` is set, both tray and core use the same key
2. **Auto-Generation**: If no API key is provided, tray generates one and passes it to core via environment
3. **Core Process Environment**: Tray always passes `MCPPROXY_API_KEY` to the core process it launches
4. **TLS Configuration**: When `MCPPROXY_TLS_ENABLED=true`, it's automatically passed to the core process

This prevents the "API key auto-generated for security" mismatch that would prevent tray-core communication.

### Quarantine System
- **All new servers** added via LLM tools are automatically quarantined
- **Quarantined servers** cannot execute tools until manually approved
- **Tool calls** to quarantined servers return security analysis instead of executing
- **Approval** requires manual action via system tray or config file editing

### Tool Poisoning Attack (TPA) Protection
- Automatic detection of malicious tool descriptions
- Security analysis with comprehensive checklists
- Protection against hidden instructions and data exfiltration attempts

### Core Process Exit Codes

The core mcpproxy process uses specific exit codes to communicate failure reasons to the tray application:

**Exit Codes** (`cmd/mcpproxy/exit_codes.go`):
- `0` - Success (normal termination)
- `1` - General error (default for unclassified errors)
- `2` - Port conflict (listen address already in use)
- `3` - Database locked (another mcpproxy instance running)
- `4` - Configuration error (invalid config file)
- `5` - Permission error (insufficient file/port access)

**Tray Integration**:
The tray application's process monitor (`cmd/mcpproxy-tray/internal/monitor/process.go`) maps these exit codes to state machine events, enabling intelligent retry strategies and user-friendly error reporting.

## Debugging Guide

### Quick Diagnostics

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

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for detailed workflows.

### Common Commands

```bash
# Monitor logs
tail -f ~/Library/Logs/mcpproxy/main.log

# Check server status
mcpproxy upstream list

# View specific server logs
mcpproxy upstream logs github-server --tail=100

# Follow logs in real-time (requires daemon)
mcpproxy upstream logs github-server --follow

# Restart problematic server
mcpproxy upstream restart github-server

# Filter for errors
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(ERROR|WARN)"

# Debug mode
pkill mcpproxy && ./mcpproxy serve --log-level=debug

# OAuth debugging
mcpproxy auth login --server=Sentry --log-level=debug
```

### Log Locations
- **Main log**: `~/Library/Logs/mcpproxy/main.log` (macOS) or `~/.mcpproxy/logs/main.log` (Linux/Windows)
- **Per-server logs**: `~/Library/Logs/mcpproxy/server-{name}.log`


## Development Guidelines

### File Organization
- Use the existing modular architecture with clear separation of concerns
- Place new features in appropriate `internal/` subdirectories
- Follow Go package naming conventions

### Testing Patterns
- Unit tests alongside source files (`*_test.go`)
- E2E tests in `internal/server/e2e_test.go`
- Use testify for assertions and mocking
- Test files should be comprehensive and test both success and error cases

### Error Handling
- Use structured logging with zap
- Wrap errors with context using `fmt.Errorf`
- Handle context cancellation properly in long-running operations
- Graceful degradation for non-critical failures


### Configuration Management
- Config changes should update both storage and file system
- File watcher triggers automatic config reloads
- Validate configuration on load and provide sensible defaults

## Runtime Architecture (Phase 1-3 Refactoring)

### Runtime Package (`internal/runtime/`)

The runtime package provides the core non-HTTP lifecycle management, separating concerns from the HTTP server layer:

- **Configuration Management**: Centralized config loading, validation, and hot-reload
- **Background Services**: Connection management, tool indexing, and health monitoring
- **State Management**: Thread-safe status tracking and upstream server state
- **Event System**: Real-time event broadcasting for UI and SSE consumers

### Event Bus System

The event bus enables real-time communication between runtime and UI components:

**Event Types**:
- `servers.changed` - Server configuration or state changes
- `config.reloaded` - Configuration file reloaded from disk

**Event Flow**:
1. Runtime operations trigger events via `emitServersChanged()` and `emitConfigReloaded()`
2. Events are broadcast to subscribers through buffered channels
3. Server forwards events to tray UI and SSE endpoints
4. Tray menus refresh automatically without file watching
5. Web UI receives live updates via `/events` SSE endpoint

**SSE Integration**:
- `/events` endpoint streams both status updates and runtime events
- Automatic connection management with proper cleanup
- JSON-formatted event payloads for easy consumption

### Runtime Lifecycle

**Initialization**:
1. Runtime created with config, logger, and manager dependencies
2. Background initialization starts server connections and tool indexing
3. Status updates broadcast through event system

**Background Services**:
- **Connection Management**: Periodic reconnection attempts with exponential backoff
- **Tool Indexing**: Automatic discovery and search index updates every 15 minutes
- **Configuration Sync**: File-based config changes trigger runtime resync

**Shutdown**:
- Graceful context cancellation cascades to all background services
- Upstream servers disconnected with proper Docker container cleanup
- Resources closed in dependency order (upstream → cache → index → storage)

## Important Implementation Details

### Docker Security Isolation
- **Runtime Detection**: Automatically detects command type (uvx→Python, npx→Node.js, etc.)
- **Image Selection**: Maps to appropriate Docker images with required tools and Git support
- **Environment Passing**: API keys and config securely passed via `-e` flags
- **Container Lifecycle**: Proper cleanup with cidfile tracking and health monitoring
- **Conflict Avoidance**: Skips isolation for existing Docker commands to prevent nested containers
- **Resource Limits**: Memory and CPU limits prevent resource exhaustion
- **Full Image Support**: Uses `python:3.11` and `node:20` (not slim/alpine) for Git and build tools

### OAuth Implementation
- Uses dynamic port allocation for callback servers
- RFC 8252 compliant with PKCE for security
- Automatic browser launching for authentication flows
- Global callback server manager prevents port conflicts

### Connection Management
- Background connection attempts with exponential backoff
- Separate contexts for application vs server lifecycle
- Connection state machine: Disconnected → Connecting → Authenticating → Ready

### Tool Indexing
- Full rebuild on server changes
- Hash-based change detection to skip unchanged tools
- Background indexing doesn't block server operations

### Logging System
- Main application log: `main.log`
- Per-server logs: `server-{name}.log`
- Docker container logs automatically captured and integrated
- Automatic log rotation and compression
- Configurable log levels and output formats

### Signal Handling
- Graceful shutdown with proper resource cleanup
- Context cancellation for background operations
- HTTP server shutdown with timeout
- Docker container cleanup on shutdown
- Double shutdown protection

When making changes to this codebase, ensure you understand the modular architecture and maintain the clear separation between core protocol handling, state management, and user interface components.
- remember before running mcpproxy core u need to kill all mcpproxy instances, because it locks DB

## Active Technologies
- Go 1.21+, TypeScript/Vue 3 (003-tool-annotations-webui)
- BBolt (existing `server_{serverID}_tool_calls` buckets, new `sessions` bucket) (003-tool-annotations-webui)
- Go 1.24.0 (004-management-health-refactor)
- BBolt embedded database (`~/.mcpproxy/config.db`) for server configurations, quarantine status, and tool statistics (004-management-health-refactor)
- BBolt embedded database (`~/.mcpproxy/config.db`) - used by existing runtime, no changes required (005-rest-management-integration)

## Recent Changes
- 003-tool-annotations-webui: Added Go 1.21+, TypeScript/Vue 3
