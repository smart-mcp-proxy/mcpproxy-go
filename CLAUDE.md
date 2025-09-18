# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MCPProxy is a Go-based desktop application that acts as a smart proxy for AI agents using the Model Context Protocol (MCP). It provides intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

## Development Commands

### Build
```bash
# Build for current platform
go build -o mcpproxy ./cmd/mcpproxy

# Cross-platform build script (builds for multiple architectures)
./scripts/build.sh

# Quick local build
scripts/build.sh
```

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
```bash
# Start server with system tray (default)
./mcpproxy serve

# Start without tray
./mcpproxy serve --tray=false

# Custom configuration
./mcpproxy serve --config=/path/to/config.json

# Debug mode with trace logging
./mcpproxy serve --log-level=debug

# Debug specific server tools
./mcpproxy tools list --server=github-server --log-level=trace
```

## Architecture Overview

### Core Components

- **`cmd/mcpproxy/`** - Main CLI application entry point
  - `main.go` - Cobra CLI setup and command routing
  - `tools_cmd.go` - Tools debugging commands
  - `call_cmd.go` - Tool execution commands
  - `tray_gui.go`/`tray_stub.go` - System tray interface (build-tagged)

- **`internal/server/`** - Core server implementation
  - `server.go` - Main server lifecycle and HTTP server management
  - `mcp.go` - MCP protocol implementation and tool routing

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
- **`internal/tray/`** - Cross-platform system tray UI
- **`internal/logs/`** - Structured logging with per-server log files

### Key Features

1. **Tool Discovery** - BM25 search across all upstream MCP server tools
2. **Security Quarantine** - Automatic quarantine of new servers to prevent Tool Poisoning Attacks
3. **Docker Security Isolation** - Run stdio MCP servers in isolated Docker containers for enhanced security
4. **OAuth 2.1 Support** - RFC 8252 compliant OAuth with PKCE for secure authentication
5. **System Tray UI** - Native cross-platform tray interface for server management
6. **Per-Server Logging** - Individual log files for each upstream server
7. **Hot Configuration Reload** - Real-time config changes via file watching

## Configuration

### Default Locations
- **Config**: `~/.mcpproxy/mcp_config.json`
- **Data**: `~/.mcpproxy/config.db` (BBolt database)
- **Index**: `~/.mcpproxy/index.bleve/` (search index)
- **Logs**: `~/.mcpproxy/logs/` (main.log + per-server logs)

### Example Configuration
```json
{
  "listen": ":8080",
  "data_dir": "~/.mcpproxy",
  "enable_tray": true,
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

## Security Model

### Quarantine System
- **All new servers** added via LLM tools are automatically quarantined
- **Quarantined servers** cannot execute tools until manually approved
- **Tool calls** to quarantined servers return security analysis instead of executing
- **Approval** requires manual action via system tray or config file editing

### Tool Poisoning Attack (TPA) Protection
- Automatic detection of malicious tool descriptions
- Security analysis with comprehensive checklists
- Protection against hidden instructions and data exfiltration attempts

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

#### OAuth Flow Diagnostics
```bash
# Debug OAuth with detailed logging
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(🔐|🌐|🚀|⏳|✅|❌|oauth|OAuth)"

# Monitor callback server status
grep -E "(callback|redirect_uri|127\.0\.0\.1)" ~/Library/Logs/mcpproxy/main.log

# Check token store persistence
grep -E "(token.*store|has_existing_token_store)" ~/Library/Logs/mcpproxy/main.log
```

#### Common OAuth Issues
1. **Browser not opening**: Check environment variables (`DISPLAY`, `HEADLESS`, `CI`)
2. **Token persistence**: Look for `"has_existing_token_store": false` on restart
3. **Rate limiting**: Search for "rate limited" messages
4. **Callback failures**: Monitor callback server logs

### Tool Discovery and Indexing Debug

#### Test Tool Availability
```bash
# List tools from specific server
mcpproxy tools list --server=github-server --log-level=debug

# Search for tools (uses BM25 index)
mcpproxy tools search "create issue" --limit=10

# Test direct tool calls
mcpproxy call tool --tool-name=Sentry:whoami --json_args='{}'
```

#### Index Debugging
```bash
# Check index status and rebuilds
grep -E "(index|Index|rebuild|BM25)" ~/Library/Logs/mcpproxy/main.log

# Monitor tool discovery
grep -E "(tool.*discovered|discovered.*tool)" ~/Library/Logs/mcpproxy/main.log

# Check server connection states
grep -E "(Ready|Connecting|Error|state.*transition)" ~/Library/Logs/mcpproxy/main.log
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

#### Quarantine Management
```bash
# List quarantined servers
mcpproxy quarantine list

# Review quarantined server details
mcpproxy quarantine inspect --name="suspicious-server"

# Manually quarantine server
mcpproxy quarantine add --name="unsafe-server"
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
go build && ./mcpproxy --log-level=debug --tray=false

# Start with trace-level logging (very verbose)
./mcpproxy --log-level=trace --tray=false

# Debug specific operations
./mcpproxy tools list --server=github-server --log-level=trace
```

#### Environment Variables for Debugging
```bash
# Disable OAuth for testing
export MCPPROXY_DISABLE_OAUTH=true

# Enable additional debugging
export MCPPROXY_DEBUG=true

# Test in headless environment
export HEADLESS=true
```

### Troubleshooting Common Issues

1. **Tools not appearing in search**:
   - Check server authentication status: `mcpproxy auth status`
   - Verify server can list tools: `mcpproxy tools list --server=<name>`
   - Check index rebuild: `grep -E "index.*rebuild" ~/Library/Logs/mcpproxy/main.log`

2. **OAuth servers failing**:
   - Test manual login: `mcpproxy auth login --server=<name> --log-level=debug`
   - Check browser opening: Look for "Opening browser" in logs
   - Verify callback server: `grep "callback" ~/Library/Logs/mcpproxy/main.log`

3. **Server connection issues**:
   - Monitor retry attempts: `grep "retry" ~/Library/Logs/mcpproxy/main.log`
   - Check Docker isolation: `grep "Docker" ~/Library/Logs/mcpproxy/main.log`
   - Verify server configuration: `mcpproxy upstream list`

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

### Build Tags
- System tray functionality uses build tags (`tray_gui.go` vs `tray_stub.go`)
- Platform-specific code should use appropriate build constraints

### Configuration Management
- Config changes should update both storage and file system
- File watcher triggers automatic config reloads
- Validate configuration on load and provide sensible defaults

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
- to memory 
if u want to test tool call in mcpproxy instead of curl call, use mcpproxy call. Example  `mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'`
- to memory
Never use curl to interact with mcpproxy, it uses mcp protocol. USE DIRECT mcp server call