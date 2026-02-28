# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.


## Autonomous Operation Constraints
When operating to complete a task, adhere strictly to the following constraints to ensure zero-interruption execution:

### Must-Do (Defaults & Assumptions)
- **Zero Interruption Policy**: If a decision is needed and no explicit instruction exists, you MUST make an informed, safe assumption based on idiomatic Go best practices and document it in the PR/commit. Do NOT ask for human clarification mid-task.
- **Test-Driven Progress**: You must write a failing Go test (`_test.go`) for every sub-task before implementing the feature. 
- **Graceful Fallbacks**: If an API or dependency lacks documentation, use mock interfaces or a simplified implementation rather than blocking the task.
- **Continuous Logging**: Document every step completed in an `execution_log.md` within the current working directory to maintain state.

### Must-Nots
- **Do NOT ask for plan approval**: Once a plan/spec is generated, begin execution immediately.
- **Do NOT stop for code style choices**: Run `gofmt` or `goimports` and strictly follow standard Go conventions.

### Escalation Triggers (Stop Conditions)
Only halt execution and ask a human IF:
1. You need to perform destructive data operations or delete core proxy logic that cannot be mocked.
2. A required environment variable is missing from `.env` and cannot be mocked for the scope of the task.
3. You are stuck in an error loop for the same `go test` failing after 5 consecutive attempts.



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

### Activity Log CLI
```bash
mcpproxy activity list              # List recent activity
mcpproxy activity list --type tool_call --status error  # Filter by type/status
mcpproxy activity list --request-id <id>  # Filter by HTTP request ID (for error correlation)
mcpproxy activity watch             # Real-time activity stream
mcpproxy activity show <id>         # View activity details
mcpproxy activity summary           # Show 24h statistics
mcpproxy activity export --output audit.jsonl  # Export for compliance
```

See [docs/cli/activity-commands.md](docs/cli/activity-commands.md) for complete reference.

### CLI Output Formatting
```bash
mcpproxy upstream list -o json      # JSON output for scripting
mcpproxy upstream list -o yaml      # YAML output
mcpproxy upstream list --json       # Shorthand for -o json
mcpproxy --help-json                # Machine-readable help for AI agents
```

**Formats**: `table` (default), `json`, `yaml`
**Environment**: `MCPPROXY_OUTPUT=json` sets default format

See [docs/cli-output-formatting.md](docs/cli-output-formatting.md) for complete reference.

## Architecture Overview

### Core Components

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/` | CLI entry point, Cobra commands |
| `cmd/mcpproxy-tray/` | System tray application with state machine |
| `internal/cli/output/` | CLI output formatters (table, JSON, YAML) |
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
- **`retrieve_tools`** - BM25 keyword search across all upstream tools, returns annotations and recommended tool variant
- **`call_tool_read`** - Proxy read-only tool calls to upstream servers (Spec 018)
- **`call_tool_write`** - Proxy write tool calls to upstream servers (Spec 018)
- **`call_tool_destructive`** - Proxy destructive tool calls to upstream servers (Spec 018)
- **`code_execution`** - Execute JavaScript to orchestrate multiple tools (disabled by default)
- **`upstream_servers`** - CRUD operations for server management

**Tool Format**: `<serverName>:<toolName>` (e.g., `github:create_issue`)

**Intent Declaration (Spec 018)**: Tool variants enable granular IDE permission control. The `operation_type` is automatically inferred from the tool variant (`call_tool_read` → "read", etc.). Optional `intent` fields for audit:
```json
{
  "intent": {
    "data_sensitivity": "public",
    "reason": "User requested list of repositories"
  }
}
```

### HTTP API Endpoints

**Base Path**: `/api/v1`

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/status` | Server status and statistics |
| `GET /api/v1/servers` | List all upstream servers |
| `POST /api/v1/servers/{name}/enable` | Enable/disable server |
| `POST /api/v1/servers/{name}/quarantine` | Quarantine/unquarantine server |
| `GET /api/v1/tools` | Search tools across servers |
| `GET /api/v1/activity` | List activity records with filtering |
| `GET /api/v1/activity/{id}` | Get activity record details |
| `GET /api/v1/activity/export` | Export activity records (JSON/CSV) |
| `GET /events` | SSE stream for live updates |

**Authentication**: Use `X-API-Key` header or `?apikey=` query parameter.

**Request ID Tracking**: All responses include `X-Request-Id` header. Error responses include `request_id` in JSON body. Use for log correlation: `mcpproxy activity list --request-id <id>`.

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
- **REST API** requires authentication - API key is always enforced (auto-generated if not provided)
- **Secure by default**: Empty or missing API keys trigger automatic generation and persistence to config

See [docs/api/rest-api.md](docs/api/rest-api.md) and `oas/swagger.yaml` for API reference.

### Unified Health Status

All server responses include a `health` field that provides consistent status information across all interfaces (CLI, web UI, tray, MCP tools):

```json
{
  "health": {
    "level": "healthy|degraded|unhealthy",
    "admin_state": "enabled|disabled|quarantined",
    "summary": "Human-readable status summary",
    "detail": "Additional context about the status",
    "action": "login|restart|enable|approve|view_logs|"
  }
}
```

**Health Levels**:
- `healthy`: Server is connected and functioning normally
- `degraded`: Server has warnings (e.g., OAuth token expiring soon)
- `unhealthy`: Server has errors or is not functioning

**Admin States**:
- `enabled`: Normal operation
- `disabled`: User disabled the server
- `quarantined`: Server pending security approval

**Actions**: Suggested remediation action for the current state. Empty when no action is needed.

**Configuration**: Token expiry warning threshold can be configured:
```json
{
  "oauth_expiry_warning_hours": 24
}
```

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

- **Localhost-only by default**: Core server binds to `127.0.0.1:8080`
- **API key always required**: Auto-generated if not provided
- **Quarantine system**: New servers quarantined until manually approved
- **Tool Poisoning Attack (TPA) protection**: Automatic detection of malicious descriptions

See [docs/features/security-quarantine.md](docs/features/security-quarantine.md) for details.

## Sensitive Data Detection

Automatic scanning of tool call arguments and responses for secrets, credentials, and sensitive data. Enabled by default and integrates with the activity log for security auditing.

### Detection Categories

| Category | Examples | Severity |
|----------|----------|----------|
| `cloud_credentials` | AWS keys, GCP API keys, Azure storage keys | critical |
| `private_key` | RSA, EC, DSA, OpenSSH, PGP private keys | critical |
| `api_token` | GitHub, GitLab, Stripe, Slack, OpenAI, Anthropic, Google AI, xAI, Groq, HuggingFace, Replicate, Perplexity, Fireworks, Anyscale, Mistral, Cohere, DeepSeek, Together AI tokens | critical |
| `database_credential` | MySQL, PostgreSQL, MongoDB connection strings | critical/high |
| `credit_card` | Visa, Mastercard, Amex (Luhn validated) | high |
| `sensitive_file` | Paths to `.ssh/`, `.aws/`, `.env` files | high/medium |
| `high_entropy` | Base64/hex strings with high Shannon entropy | medium |

### Key Files

| File | Purpose |
|------|---------|
| `internal/security/detector.go` | Main detector with `Scan()` method |
| `internal/security/types.go` | Detection, Result, Severity, Category types |
| `internal/security/patterns/` | Pattern definitions by category |
| `internal/security/patterns/cloud.go` | AWS, GCP, Azure credential patterns |
| `internal/security/patterns/keys.go` | Private key detection patterns |
| `internal/security/patterns/tokens.go` | API token patterns |
| `internal/security/patterns/database.go` | Database connection string patterns |
| `internal/security/patterns/creditcard.go` | Credit card patterns with Luhn validation |
| `internal/security/entropy.go` | High-entropy string detection |
| `internal/security/paths.go` | Sensitive file path patterns |
| `internal/runtime/activity_service.go` | Integration point via `SetDetector()` |

### CLI Commands

```bash
mcpproxy activity list --sensitive-data              # Show only activities with detections
mcpproxy activity list --severity critical           # Filter by severity level
mcpproxy activity list --detection-type aws_access_key  # Filter by detection type
mcpproxy activity show <id>                          # View detection details
mcpproxy activity export --sensitive-data --output audit.jsonl  # Export for compliance
```

### Configuration

```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_requests": true,
    "scan_responses": true,
    "max_payload_size_kb": 1024,
    "entropy_threshold": 4.5,
    "categories": {
      "cloud_credentials": true,
      "private_key": true,
      "api_token": true,
      "database_credential": true,
      "credit_card": true,
      "high_entropy": true
    },
    "custom_patterns": [
      {
        "name": "internal_api_key",
        "regex": "INTERNAL-[A-Z0-9]{32}",
        "severity": "high",
        "category": "custom"
      }
    ],
    "sensitive_keywords": ["password", "secret"]
  }
}
```

See [docs/features/sensitive-data-detection.md](docs/features/sensitive-data-detection.md) for complete reference.

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

## Active Technologies
- Go 1.24 (toolchain go1.24.10) (001-update-version-display)
- In-memory only for version cache (no persistence per clarification) (001-update-version-display)
- Go 1.24 (toolchain go1.24.10) + Cobra CLI framework, encoding/json, gopkg.in/yaml.v3 (014-cli-output-formatting)
- N/A (CLI output only) (014-cli-output-formatting)
- Go 1.24 (toolchain go1.24.10) + BBolt (storage), Chi router (HTTP), Zap (logging), existing event bus (016-activity-log-backend)
- BBolt database (existing `~/.mcpproxy/config.db`) (016-activity-log-backend)
- Go 1.24 (toolchain go1.24.10) + Cobra CLI framework, encoding/json, internal/cli/output (spec 014), internal/cliclien (017-activity-cli-commands)
- N/A (CLI layer only - uses REST API from spec 016) (017-activity-cli-commands)
- Go 1.24 (toolchain go1.24.10) + Cobra CLI, Chi router, BBolt (storage), Zap (logging), mark3labs/mcp-go (MCP protocol) (018-intent-declaration)
- BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord extended with intent metadata (018-intent-declaration)
- TypeScript 5.9, Vue 3.5, Go 1.24 (backend already exists) + Vue 3, Vue Router 4, Pinia 2, Tailwind CSS 3, DaisyUI 4, Vite 5 (019-activity-webui)
- N/A (frontend consumes REST API from backend) (019-activity-webui)
- Go 1.24 (toolchain go1.24.10) + Cobra (CLI), Chi router (HTTP), Zap (logging), mark3labs/mcp-go (MCP protocol) (020-oauth-login-feedback)
- Go 1.24 (toolchain go1.24.10) + Cobra (CLI), Chi router (HTTP), Zap (logging), google/uuid (ID generation) (021-request-id-logging)
- BBolt database (`~/.mcpproxy/config.db`) - activity log extended with request_id field (021-request-id-logging)
- Go 1.24 (toolchain go1.24.10) + mcp-go v0.43.1 (OAuth client), BBolt (storage), Prometheus (metrics), Zap (logging) (023-oauth-state-persistence)
- BBolt database (`~/.mcpproxy/config.db`) - `oauth_tokens` bucket with `OAuthTokenRecord` model (023-oauth-state-persistence)
- Go 1.24 (toolchain go1.24.10) + TypeScript 5.x / Vue 3.5 + Cobra CLI, Chi router, BBolt storage, Zap logging, mark3labs/mcp-go, Vue 3, Tailwind CSS, DaisyUI (024-expand-activity-log)
- BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord model (024-expand-activity-log)
- Go 1.24 (toolchain go1.24.10) + BBolt (storage), Chi router (HTTP), Zap (logging), regexp (stdlib), existing ActivityService (026-pii-detection)
- BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord.Metadata extension (026-pii-detection)

## Recent Changes
- 001-update-version-display: Added Go 1.24 (toolchain go1.24.10)
