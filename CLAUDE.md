# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MCPProxy is a Go-based desktop application that acts as a smart proxy for AI agents using the Model Context Protocol (MCP). It provides intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

## Architecture: Core + Tray Split

- **Core Server** (`mcpproxy`): Headless HTTP API server with MCP proxy functionality
- **Tray Application** (`mcpproxy-tray`): Standalone GUI application that manages the core server

**Key Benefits**: Auto-start, port conflict resolution, independent operation, real-time sync via SSE.

## Development Commands

### Build
```bash
go build -o mcpproxy ./cmd/mcpproxy                     # Core server
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray  # Tray app
make build                                               # Frontend and backend
./scripts/build.sh                                       # Cross-platform build
```

### Testing

**IMPORTANT: Always run tests before committing changes!**

```bash
./scripts/test-api-e2e.sh           # Quick API E2E test (required)
./scripts/verify-oas-coverage.sh    # OpenAPI coverage (if modifying REST endpoints)
./scripts/run-all-tests.sh          # Full test suite
go test ./internal/... -v           # Unit tests
go test -race ./internal/... -v     # Race detection
./scripts/run-oauth-e2e.sh          # OAuth E2E tests
```

**E2E Prerequisites**: Node.js, npm, jq, built mcpproxy binary.

### Linting
```bash
./scripts/run-linter.sh             # Requires golangci-lint v1.59.1+
```

### Running
```bash
./mcpproxy serve                    # Start core (localhost:8080)
./mcpproxy serve --listen :8080     # All interfaces (CAUTION)
./mcpproxy serve --log-level=debug  # Debug mode
./mcpproxy-tray                     # Start tray (auto-starts core)
```

### CLI Management
```bash
mcpproxy upstream list              # List all servers
mcpproxy upstream logs <name>       # View logs (--tail, --follow)
mcpproxy upstream restart <name>    # Restart server (supports --all)
mcpproxy doctor                     # Run health checks
```

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for complete reference.

## Architecture Overview

### Core Components

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/` | CLI entry point, Cobra commands |
| `cmd/mcpproxy-tray/` | System tray application with state machine |
| `internal/runtime/` | Lifecycle, event bus, background services |
| `internal/server/` | HTTP server, MCP proxy |
| `internal/httpapi/` | REST API endpoints (`/api/v1`) |
| `internal/upstream/` | 3-layer client: core/managed/cli |
| `internal/config/` | Configuration management |
| `internal/index/` | Bleve BM25 search index |
| `internal/storage/` | BBolt database |
| `internal/management/` | Centralized server management |
| `internal/oauth/` | OAuth 2.1 with PKCE |
| `internal/logs/` | Structured logging with per-server files |

See [docs/architecture.md](docs/architecture.md) for diagrams and details.

### Tray-Core Communication

- **Unix sockets** (macOS/Linux): `~/.mcpproxy/mcpproxy.sock`
- **Named pipes** (Windows): `\\.\pipe\mcpproxy-<username>`
- Socket connections bypass API key (OS-level auth)
- TCP connections require API key authentication

See [docs/socket-communication.md](docs/socket-communication.md) for details.

## Configuration

**Default Locations**:
- **Config**: `~/.mcpproxy/mcp_config.json`
- **Data**: `~/.mcpproxy/config.db` (BBolt database)
- **Index**: `~/.mcpproxy/index.bleve/` (search index)
- **Logs**: `~/.mcpproxy/logs/` (main.log + per-server logs)

### Example Configuration
```json
{
  "listen": "127.0.0.1:8080",
  "api_key": "your-secret-api-key-here",
  "enable_socket": true,
  "enable_web_ui": true,
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
      "enabled": true
    }
  ]
}
```

### Environment Variables

- `MCPPROXY_LISTEN` - Override network binding (e.g., `127.0.0.1:8080`)
- `MCPPROXY_API_KEY` - Set API key for REST API authentication
- `MCPPROXY_DEBUG` - Enable debug mode
- `HEADLESS` - Run in headless mode (no browser launching)

See [docs/configuration.md](docs/configuration.md) for complete reference.

## MCP Protocol

### Built-in Tools
- **`retrieve_tools`** - BM25 keyword search across all upstream tools
- **`call_tool`** - Proxy tool calls to upstream servers
- **`code_execution`** - Execute JavaScript to orchestrate multiple tools (disabled by default)
- **`upstream_servers`** - CRUD operations for server management

**Tool Format**: `<serverName>:<toolName>` (e.g., `github:create_issue`)

### HTTP API Endpoints

**Base Path**: `/api/v1`

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/status` | Server status and statistics |
| `GET /api/v1/servers` | List all upstream servers |
| `POST /api/v1/servers/{name}/enable` | Enable/disable server |
| `POST /api/v1/servers/{name}/quarantine` | Quarantine/unquarantine server |
| `GET /api/v1/tools` | Search tools across servers |
| `GET /events` | SSE stream for live updates |

**Authentication**: Use `X-API-Key` header or `?apikey=` query parameter.

See [docs/api/rest-api.md](docs/api/rest-api.md) and `oas/swagger.yaml` for API reference.

## Security Model

- **Localhost-only by default**: Core server binds to `127.0.0.1:8080`
- **API key always required**: Auto-generated if not provided
- **Quarantine system**: New servers quarantined until manually approved
- **Tool Poisoning Attack (TPA) protection**: Automatic detection of malicious descriptions

See [docs/features/security-quarantine.md](docs/features/security-quarantine.md) for details.

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error |
| `2` | Port conflict |
| `3` | Database locked |
| `4` | Config error |
| `5` | Permission error |

## Debugging

```bash
mcpproxy doctor                     # Quick diagnostics
mcpproxy upstream list              # Server status
mcpproxy upstream logs <name>       # Server logs (--tail, --follow)
tail -f ~/Library/Logs/mcpproxy/main.log  # Main log (macOS)
tail -f ~/.mcpproxy/logs/main.log         # Main log (Linux)
```

## Development Guidelines

- **File Organization**: Use `internal/` subdirectories, follow Go conventions
- **Testing**: Unit tests in `*_test.go`, E2E in `internal/server/e2e_test.go`
- **Error Handling**: Structured logging (zap), context wrapping, graceful degradation
- **Config**: Update both storage and file system, use file watcher for hot reload

## Key Implementation Details

### Docker Security Isolation
Runtime detection (uvx→Python, npx→Node.js), image selection, environment passing, container lifecycle management. See [docs/docker-isolation.md](docs/docker-isolation.md).

### OAuth Implementation
Dynamic port allocation, RFC 8252 + PKCE, flow coordinator (`internal/oauth/coordinator.go`), automatic token refresh. See [docs/oauth-resource-autodetect.md](docs/oauth-resource-autodetect.md).

### Code Execution
Sandboxed JavaScript (ES5.1+), orchestrates multiple upstream tools in single request. See [docs/code_execution/overview.md](docs/code_execution/overview.md).

### Connection Management
Exponential backoff, separate contexts for app vs server lifecycle, state machine: Disconnected → Connecting → Authenticating → Ready.

### Tool Indexing
Full rebuild on server changes, hash-based change detection, background indexing.

### Signal Handling
Graceful shutdown, context cancellation, Docker cleanup, double shutdown protection.

**Important**: Before running mcpproxy core, kill all existing instances as it locks the database.

## Windows Installer

```bash
# Using Inno Setup (recommended)
.\scripts\build-windows-installer.ps1 -Version "v1.0.0" -Arch "amd64"

# Using WiX Toolset
wix build -arch x64 -d Version=1.0.0.0 -d BinPath=dist\windows-amd64 wix\Package.wxs
```

See `docs/github-actions-windows-wix-research.md` for CI setup.

## Prerelease Builds

- **`main` branch**: Stable releases
- **`next` branch**: Prerelease builds with latest features
- macOS DMG installers are signed and notarized

See `docs/prerelease-builds.md` for download instructions.
