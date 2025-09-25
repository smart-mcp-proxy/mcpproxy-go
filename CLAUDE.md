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

### Prerelease Builds

**MCPProxy supports automated prerelease builds from the `next` branch with signed and notarized macOS installers.**

#### Branch Strategy
- **`main` branch**: Stable releases (hotfixes and production builds)
- **`next` branch**: Prerelease builds with latest features

#### Downloading Prerelease Builds

**Option 1: GitHub Web Interface**
1. Go to [GitHub Actions](https://github.com/smart-mcp-proxy/mcpproxy-go/actions)
2. Click on the latest successful "Prerelease" workflow run
3. Scroll to **Artifacts** section
4. Download:
   - `dmg-darwin-arm64` (Apple Silicon Macs)
   - `dmg-darwin-amd64` (Intel Macs)
   - `versioned-linux-amd64`, `versioned-windows-amd64`, etc. (other platforms)

**Option 2: Command Line**
```bash
# List recent prerelease runs
gh run list --workflow="Prerelease" --limit 5

# Download specific artifacts from a run
gh run download <RUN_ID> --name dmg-darwin-arm64    # Apple Silicon
gh run download <RUN_ID> --name dmg-darwin-amd64    # Intel Mac
gh run download <RUN_ID> --name versioned-linux-amd64  # Linux
```

#### Prerelease Versioning
- Format: `{last_git_tag}-next.{commit_hash}`
- Example: `v0.8.4-next.5b63e2d`
- Version embedded in both `mcpproxy` and `mcpproxy-tray` binaries

#### Security Features
- **macOS DMG installers**: Signed with Apple Developer ID and notarized
- **Code signing**: All macOS binaries are signed for Gatekeeper compatibility
- **Automatic quarantine protection**: New servers are quarantined by default

#### GitHub Workflows
- **Prerelease workflow**: Triggered on `next` branch pushes
- **Release workflow**: Triggered on `main` branch tags
- **Unit Tests**: Run on all branches with comprehensive test coverage
- **Frontend CI**: Validates web UI components and build process

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
- **Real-time updates** via SSE connection to core API
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
- **Health Monitor** (`cmd/mcpproxy-tray/internal/monitor/health.go`): Performs HTTP health checks on core API
- **State Machine** (`cmd/mcpproxy-tray/internal/state/machine.go`): Manages state transitions and retry logic

**Error Classification**:
Core process exit codes are mapped to specific state machine events:
- Exit code 2 (port conflict) → `EventPortConflict`
- Exit code 3 (database locked) → `EventDBLocked`
- Exit code 4 (config error) → `EventConfigError`
- Other errors → `EventGeneralError`

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

### Key Features

1. **Tool Discovery** - BM25 search across all upstream MCP server tools
2. **Security Quarantine** - Automatic quarantine of new servers to prevent Tool Poisoning Attacks
3. **Docker Security Isolation** - Run stdio MCP servers in isolated Docker containers for enhanced security
4. **OAuth 2.1 Support** - RFC 8252 compliant OAuth with PKCE for secure authentication
5. **System Tray UI** - Native cross-platform tray interface for server management
6. **Per-Server Logging** - Individual log files for each upstream server
7. **Real-time Event System** - Event bus with SSE integration for live updates (Phase 3 refactoring)
8. **Hot Configuration Reload** - Real-time config changes with event notifications

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
  "enable_web_ui": true,
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "docker_isolation": {
    "enabled": true,
    "memory_limit": "512m",
    "cpu_limit": "1.0",
    "timeout": "60s",
    "default_images": {
      "python": "python:3.11",
      "uvx": "python:3.11",
      "node": "node:20",
      "npx": "node:20"
    }
  },
  "mcpServers": [
    {
      "name": "github-server",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "python-mcp-server",
      "command": "uvx",
      "args": ["some-python-package"],
      "protocol": "stdio",
      "env": {
        "API_KEY": "your-api-key"
      },
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "ast-grep-project-a",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "/home/user/projects/project-a",
      "protocol": "stdio",
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "filesystem-work",
      "command": "npx",
      "args": ["@modelcontextprotocol/server-filesystem"],
      "working_dir": "/home/user/work/company-repo",
      "protocol": "stdio",
      "enabled": true,
      "quarantined": false
    }
  ]
}
```

### Environment Variables

MCPProxy supports several environment variables for configuration and security:

**Security Configuration**:
- `MCPP_LISTEN` - Override network binding (e.g., `127.0.0.1:8080`, `:8080`)
- `MCPP_API_KEY` - Set API key for REST API authentication

**Debugging**:
- `MCPPROXY_DEBUG` - Enable debug mode
- `MCPPROXY_DISABLE_OAUTH` - Disable OAuth for testing
- `HEADLESS` - Run in headless mode (no browser launching)

**Examples**:
```bash
# Start with custom network binding
export MCPP_LISTEN=":8080"
./mcpproxy serve

# Start with custom API key
export MCPP_API_KEY="my-secret-key"
./mcpproxy serve

# Disable authentication for testing
export MCPP_API_KEY=""
./mcpproxy serve

# Run in headless mode
export HEADLESS=true
./mcpproxy serve
```

### Working Directory Configuration

The `working_dir` field allows you to specify the working directory for stdio MCP servers, solving the common problem where file-based servers operate on mcpproxy's directory instead of your project directories.

#### Use Cases
- **File-based MCP servers**: `ast-grep-mcp`, `filesystem-mcp`, `git-mcp`
- **Project isolation**: Separate work and personal project contexts
- **Multiple instances**: Same MCP server type for different projects

#### Configuration Examples

**Project-specific servers**:
```json
{
  "mcpServers": [
    {
      "name": "ast-grep-project-a",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "/home/user/projects/project-a",
      "enabled": true
    },
    {
      "name": "ast-grep-work-repo",
      "command": "npx", 
      "args": ["ast-grep-mcp"],
      "working_dir": "/home/user/work/company-repo",
      "enabled": true
    }
  ]
}
```

**Management via Tool Calls**:
```bash
# Add server with working directory
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"add","name":"git-myproject","command":"npx","args_json":"[\"@modelcontextprotocol/server-git\"]","working_dir":"/home/user/projects/myproject","enabled":true}'

# Update working directory for existing server
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"update","name":"git-myproject","working_dir":"/home/user/projects/myproject-v2"}'

# Add server via patch operation
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"patch","name":"existing-server","patch_json":"{\"working_dir\":\"/new/project/path\"}"}'
```

#### Error Handling
If a specified `working_dir` doesn't exist:
- Server startup will fail with detailed error message
- Error logged to both main log and server-specific log  
- Server remains disabled until directory issue is resolved

#### Backwards Compatibility
- Empty or unspecified `working_dir` uses current directory (existing behavior)
- All existing configurations continue to work unchanged

#### Docker Integration
Working directories are compatible with Docker isolation. When both are configured:
- `working_dir` affects the host-side directory context
- `isolation.working_dir` affects the container's internal working directory

## MCP Protocol Implementation

### Built-in Tools
- **`retrieve_tools`** - BM25 keyword search across all upstream tools
- **`call_tool`** - Proxy tool calls to upstream servers
- **`upstream_servers`** - CRUD operations for server management
- **`tools_stat`** - Usage statistics and analytics

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

## Security Model

### Network Security
- **Localhost-only binding by default**: Core server binds to `127.0.0.1:8080` by default to prevent network exposure
- **Override options**: Can be changed via `--listen` flag, `MCPP_LISTEN` environment variable, or config file
- **API key authentication**: REST API endpoints protected with optional API key authentication
- **MCP endpoints open**: MCP protocol endpoints (`/mcp`, `/mcp/`) remain unprotected for client compatibility

### API Key Authentication
- **Automatic generation**: API key generated if not provided and logged for easy access
- **Multiple authentication methods**: Supports `X-API-Key` header and `?apikey=` query parameter
- **Tray integration**: Tray app automatically generates and manages API keys for core communication
- **Configuration options**: Set via `--api-key` flag, `MCPP_API_KEY` environment variable, or config file
- **Optional protection**: Empty API key disables authentication (useful for testing)
- **Protected endpoints**: `/api/v1/*` and `/events` (SSE) require authentication when enabled

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

### Log Locations and Analysis

#### Log File Structure
- **Main log**: `~/Library/Logs/mcpproxy/main.log` (macOS) or `~/.mcpproxy/logs/main.log` (Linux/Windows)
- **Per-server logs**: `~/Library/Logs/mcpproxy/server-{name}.log`
- **Archived logs**: Compressed with timestamps (e.g., `main-2025-09-02T10-17-31.851.log.gz`)

#### Essential Grep Commands
```bash
# Monitor real-time logs
tail -f ~/Library/Logs/mcpproxy/main.log

# Filter for specific issues
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(ERROR|WARN|oauth|OAuth|tool|Tool)"

# Debug specific server
tail -f ~/Library/Logs/mcpproxy/server-Sentry.log

# Search for authentication issues
grep -E "(auth|Auth|token|Token|401|invalid_token)" ~/Library/Logs/mcpproxy/main.log

# Find tool indexing problems
grep -E "(index|Index|tool.*list|list.*tool)" ~/Library/Logs/mcpproxy/main.log

# Check OAuth flow details
grep -E "(OAuth|oauth|browser|callback|authorization)" ~/Library/Logs/mcpproxy/main.log
```

### OAuth Debugging

#### Manual Authentication Testing
```bash
# Test OAuth flow for specific server
mcpproxy auth login --server=Sentry --log-level=debug

# Check current authentication status
mcpproxy auth status

# Force re-authentication
mcpproxy auth login --server=Sentry --force
```



### Server Management Commands

#### Upstream Server Operations
```bash
# List all upstream servers with status
mcpproxy upstream list

# Add new server
mcpproxy upstream add --name="new-server" --url="https://api.example.com/mcp"

# Remove server
mcpproxy upstream remove --name="old-server"

# Enable/disable server
mcpproxy upstream update --name="test-server" --enabled=false
```

### Performance and Resource Debugging

#### Docker Isolation Monitoring
```bash
# Check Docker container status
docker ps | grep mcpproxy

# Monitor container resource usage
docker stats $(docker ps -q --filter "name=mcpproxy")

# Debug isolation setup
grep -E "(Docker|docker|isolation|container)" ~/Library/Logs/mcpproxy/main.log
```

#### Connection and Retry Analysis
```bash
# Monitor connection attempts and retries
grep -E "(retry|Retry|connection.*attempt|backoff)" ~/Library/Logs/mcpproxy/main.log

# Check connection state transitions
grep -E "(state.*transition|Connecting|Ready|Error)" ~/Library/Logs/mcpproxy/main.log
```

### Running with Debug Mode

#### Start mcpproxy with Enhanced Debugging
```bash
# Kill existing daemon
pkill mcpproxy

# Start with debug logging
go build && ./mcpproxy serve --log-level=debug

# Start with trace-level logging (very verbose)
./mcpproxy serve --log-level=trace

# Debug specific operations
./mcpproxy tools list --server=github-server --log-level=trace
```


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