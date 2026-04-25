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

## Editions (Personal & Server)

MCPProxy is built in two editions from the same codebase using Go build tags:

| Edition | Build Command | Binary | Distribution |
|---------|--------------|--------|-------------|
| **Personal** (default) | `go build ./cmd/mcpproxy` | `mcpproxy` | macOS DMG, Windows installer, Linux tar.gz |
| **Server** | `go build -tags server ./cmd/mcpproxy` | `mcpproxy-server` | Docker image, .deb package, Linux tar.gz |

> Every feature decision should ask: "Does this make the personal edition so good that developers tell their teammates about it?"

### Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/edition.go` | Default edition = "personal" |
| `cmd/mcpproxy/edition_teams.go` | Build-tagged override for server edition |
| `cmd/mcpproxy/teams_register.go` | Server feature registration entry point |
| `internal/teams/` | Server-only code (all files have `//go:build server`) |
| `internal/teams/auth/` | OAuth authentication, session management, JWT tokens, middleware |
| `internal/teams/users/` | User/session models, BBolt store, user server management |
| `internal/teams/workspace/` | Per-user workspace manager for personal upstream servers |
| `internal/teams/multiuser/` | Multi-user router, tool filtering, activity isolation |
| `internal/teams/api/` | Server REST API endpoints (user, admin, auth) |
| `native/macos/MCPProxy/` | Swift macOS tray app (SwiftUI, macOS 13+) |
| `native/macos/MCPProxyUITest/` | Swift MCP server for UI testing (accessibility + screenshots) |
| `native/windows/` | Future C# tray app (placeholder) |

### Edition Detection

The binary self-identifies its edition:
- `mcpproxy version` → `MCPProxy v0.21.0 (personal) darwin/arm64`
- `/api/v1/status` → `{"edition": "personal", ...}`

## Server Multi-User Authentication (Spec 024)

Server edition supports OAuth-based multi-user authentication with Google, GitHub, or Microsoft identity providers.

### Server Configuration

```json
{
  "teams": {
    "enabled": true,
    "admin_emails": ["admin@company.com"],
    "oauth": {
      "provider": "google",
      "client_id": "xxx.apps.googleusercontent.com",
      "client_secret": "GOCSPX-xxx",
      "tenant_id": "",
      "allowed_domains": ["company.com"]
    },
    "session_ttl": "24h",
    "bearer_token_ttl": "24h",
    "workspace_idle_timeout": "30m",
    "max_user_servers": 20
  }
}
```

### Server API Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /api/v1/auth/login` | Public | Initiate OAuth login flow |
| `GET /api/v1/auth/callback` | Public | OAuth callback (creates session) |
| `GET /api/v1/auth/me` | Session/JWT | Get current user profile |
| `POST /api/v1/auth/token` | Session | Generate JWT bearer token for MCP |
| `POST /api/v1/auth/logout` | Session | Invalidate session |
| `GET /api/v1/user/servers` | Session/JWT | List user's servers (personal + shared) |
| `POST /api/v1/user/servers` | Session/JWT | Add personal upstream server |
| `GET /api/v1/user/activity` | Session/JWT | User's activity log |
| `GET /api/v1/user/diagnostics` | Session/JWT | Server health for user's servers |
| `GET /api/v1/admin/users` | Admin | List all users |
| `POST /api/v1/admin/users/{id}/disable` | Admin | Disable a user |
| `GET /api/v1/admin/activity` | Admin | All users' activity logs |
| `GET /api/v1/admin/sessions` | Admin | List active sessions |

### Server Architecture

- **Auth flow**: OAuth 2.0 + PKCE → Session cookie (Web UI) + JWT bearer (MCP/API)
- **Server types**: Shared (config file, single connection) + Personal (DB, per-user connections)
- **Isolation**: Users see only shared + own personal servers. Activity logs user-scoped.
- **Admin**: Identified by `admin_emails` config. Sees all activity, manages users.
- **Build tag**: All server code behind `//go:build server`. Personal edition unaffected.

### Server Testing

```bash
go test -tags server ./internal/teams/... -v -race  # All server unit + integration tests
go build -tags server ./cmd/mcpproxy                # Build server edition
go build ./cmd/mcpproxy                            # Verify personal edition unaffected
```

## Architecture: Core + Tray Split

- **Core Server** (`mcpproxy`): Headless HTTP API server with MCP proxy functionality
- **Tray Application** (`mcpproxy-tray`): Standalone GUI application that manages the core server

**Key Benefits**: Auto-start, port conflict resolution, independent operation, real-time sync via SSE.

## Development Commands

### Build
```bash
go build -o mcpproxy ./cmd/mcpproxy                     # Core server (personal)
go build -tags server -o mcpproxy-server ./cmd/mcpproxy   # Core server (server edition)
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray  # Tray app
make build                                               # Frontend and backend (personal)
make build-server                                        # Frontend and backend (server)
make build-docker                                        # Server Docker image
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
mcpproxy upstream inspect <name>    # Inspect tool approval status (Spec 032)
mcpproxy upstream approve <name>    # Approve pending/changed tools (Spec 032)
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

### Agent Token CLI
```bash
mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write
mcpproxy token list                    # List all agent tokens
mcpproxy token show deploy-bot         # Show token details
mcpproxy token revoke deploy-bot       # Revoke a token
mcpproxy token regenerate deploy-bot   # Regenerate token secret
```

See [docs/features/agent-tokens.md](docs/features/agent-tokens.md) for complete reference.

### Telemetry CLI
```bash
mcpproxy telemetry status              # Show telemetry status and anonymous ID
mcpproxy telemetry enable              # Enable anonymous usage telemetry
mcpproxy telemetry disable             # Disable telemetry (no data sent)
```

### Feedback CLI
```bash
mcpproxy feedback "message"                          # Submit bug report
mcpproxy feedback --category feature "Add SAML"      # Feature request
mcpproxy feedback --category bug --email me@x.com "Crash"  # With contact email
```

See [docs/features/telemetry.md](docs/features/telemetry.md) for telemetry details and privacy policy.

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
  "require_mcp_auth": false,
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
- `MCPPROXY_TELEMETRY` - Set to `false` to disable anonymous telemetry (overrides config)
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
- **`quarantine_security`** - Security quarantine management: list/inspect quarantined servers, inspect/approve/approve-all tools (Spec 032)

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
| `POST /api/v1/servers/{name}/enable` | Enable server |
| `POST /api/v1/servers/{name}/disable` | Disable server |
| `POST /api/v1/servers/{name}/quarantine` | Quarantine a server |
| `POST /api/v1/servers/{name}/unquarantine` | Unquarantine a server |
| `GET /api/v1/index/search` | Search tools across servers (`?q=query&limit=N`) |
| `GET /api/v1/activity` | List activity records with filtering |
| `GET /api/v1/activity/{id}` | Get activity record details |
| `GET /api/v1/activity/export` | Export activity records (JSON/CSV) |
| `POST /api/v1/tokens` | Create agent token |
| `GET /api/v1/tokens` | List agent tokens |
| `GET /api/v1/tokens/{name}` | Get agent token details |
| `DELETE /api/v1/tokens/{name}` | Revoke agent token |
| `POST /api/v1/tokens/{name}/regenerate` | Regenerate agent token secret |
| `POST /api/v1/servers/{id}/tools/approve` | Approve pending/changed tools (Spec 032) |
| `GET /api/v1/servers/{id}/tools/{tool}/diff` | View tool description/schema changes (Spec 032) |
| `GET /api/v1/servers/{id}/tools/export` | Export tool approval records (Spec 032) |
| `POST /api/v1/feedback` | Submit feedback/bug report (proxied to GitHub Issues) |
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

The `code_execution` tool enables orchestrating multiple upstream MCP tools in a single request using sandboxed JavaScript (ES2020+). Modern syntax is fully supported: arrow functions, const/let, template literals, destructuring, classes, for-of, optional chaining (?.), nullish coalescing (??), spread/rest, Promises, Symbols, Map/Set, Proxy/Reflect, and generators.

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
- **Agent tokens**: Scoped credentials for AI agents with server and permission restrictions (`mcp_agt_` prefix, HMAC-SHA256 hashed)
- **`require_mcp_auth`**: When enabled, `/mcp` endpoint rejects unauthenticated requests (default: false for backward compatibility)
- **Quarantine system**: New servers quarantined until manually approved
- **Tool Poisoning Attack (TPA) protection**: Automatic detection of malicious descriptions
- **Tool-level quarantine (Spec 032)**: SHA-256 hash-based change detection for individual tool descriptions/schemas. New tools start as "pending", changed tools marked as "changed" (rug pull detection). Configurable via `quarantine_enabled` (global) and `skip_quarantine` (per-server).

See [docs/features/agent-tokens.md](docs/features/agent-tokens.md) and [docs/features/security-quarantine.md](docs/features/security-quarantine.md) for details.

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

## macOS Tray App (native/macos/)

### Building the Tray App
```bash
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy -emit-executable -O \
  -o /tmp/MCPProxy-new \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')
# Replace in .app bundle:
cp /tmp/MCPProxy-new /tmp/MCPProxy.app/Contents/MacOS/MCPProxy
```

### Building the UI Test Tool
```bash
cd native/macos/MCPProxyUITest
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -O -o /tmp/mcpproxy-ui-test Sources/main.swift
```

### Testing with mcpproxy-ui-test (MCP Server)

The `mcpproxy-ui-test` MCP server provides 7 tools for automated UI verification:

| Tool | Description |
|------|-------------|
| `check_accessibility` | Verify Accessibility API permissions |
| `list_running_apps` | List running macOS apps with bundle IDs |
| `list_menu_items` | Read status bar menu tree |
| `click_menu_item` | Click menu items by path |
| `read_status_bar` | Read status bar item info |
| `screenshot_window` | Capture app window or full screen (CGWindowListCreateImage) |
| `screenshot_status_bar_menu` | Open tray menu, capture screenshot, close menu |

**After every macOS tray code change, verify by:**
1. Build the tray binary (see above)
2. Replace in `/tmp/MCPProxy.app/Contents/MacOS/MCPProxy` and restart
3. Use `screenshot_window` to capture the window and visually verify
4. Use `click_menu_item` + `list_menu_items` to verify tray menu behavior
5. Use `screenshot_status_bar_menu` for tray menu visual verification

**MCP config** (in Claude Code settings or `~/.claude/settings.json`):
```json
{
  "mcpServers": {
    "mcpproxy-ui-test": {
      "command": "/tmp/mcpproxy-ui-test",
      "args": ["--bundle-id", "com.smartmcpproxy.mcpproxy.dev"]
    }
  }
}
```

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
Sandboxed JavaScript (ES2020+), orchestrates multiple upstream tools in single request. See [docs/code_execution/overview.md](docs/code_execution/overview.md).

### Connection Management
Exponential backoff, separate contexts for app vs server lifecycle, state machine: Disconnected → Connecting → Authenticating → Ready.

### Tool Indexing
Full rebuild on server changes, hash-based change detection, background indexing.

### Tool-Level Quarantine (Spec 032)
SHA-256 hash-based approval system for individual tools. Key files:
- `internal/storage/models.go` - `ToolApprovalRecord` model and `ToolApprovalBucket`
- `internal/storage/bbolt.go` - CRUD operations for tool approvals
- `internal/runtime/tool_quarantine.go` - Hash calculation, approval checking, blocking logic
- `internal/runtime/lifecycle.go` - Integration in `applyDifferentialToolUpdate()`
- `internal/server/mcp.go` - Tool-level blocking in `handleCallToolVariant()` and MCP tool operations
- `internal/httpapi/server.go` - REST API endpoints for inspection/approval
- `internal/config/config.go` - `QuarantineEnabled` (global) and `SkipQuarantine` (per-server)
- `frontend/src/views/ServerDetail.vue` - Web UI quarantine panel

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
- Go 1.24 (toolchain go1.24.10) + Cobra (CLI), Chi router (HTTP), Zap (logging), existing cliclient, socket detection, config loader (027-status-command)
- `~/.mcpproxy/mcp_config.json` (config file), `~/.mcpproxy/config.db` (BBolt - not directly used) (027-status-command)
- Go 1.24 (toolchain go1.24.10) + Cobra (CLI), Chi router (HTTP), BBolt (storage), Zap (logging), mcp-go (MCP protocol), crypto/hmac + crypto/sha256 (token hashing) (028-agent-tokens)
- BBolt database (`~/.mcpproxy/config.db`) — new `agent_tokens` bucket (028-agent-tokens)
- Go 1.24 (toolchain go1.24.10) + TypeScript 5.9 / Vue 3.5 + Chi router, BBolt, Zap logging, mcp-go, golang-jwt/jwt/v5, Vue 3, Pinia, DaisyUI (024-teams-multiuser-oauth)
- BBolt database (`~/.mcpproxy/config.db`) - new buckets for users, sessions, user servers (024-teams-multiuser-oauth)
- Go 1.24 (toolchain go1.24.10) + `github.com/dop251/goja` (existing JS sandbox), `github.com/evanw/esbuild` (new - TypeScript transpilation), `github.com/mark3labs/mcp-go` (MCP protocol), `github.com/spf13/cobra` (CLI) (033-typescript-code-execution)
- N/A (no new storage requirements) (033-typescript-code-execution)
- Swift 5.9+ / Xcode 15+ + SwiftUI, AppKit (escape hatches), Sparkle 2.x (SPM), Foundation (URLSession, Process, UNUserNotificationCenter) (037-macos-swift-tray)
- N/A (tray reads all state from core via REST API — no local persistence per Constitution III) (037-macos-swift-tray)
- Go 1.24 (toolchain go1.24.10) — primary; Swift 5.9 — macOS tray header change only + `github.com/google/uuid` (existing), `github.com/go-chi/chi/v5` (existing, for `RoutePattern()`), `github.com/spf13/cobra` (existing, new subcommand), `go.uber.org/zap` (existing), stdlib `sync/atomic`, `sync`, `os` (042-telemetry-tier2)
- Config file `~/.mcpproxy/mcp_config.json` only — counters live in memory and are never persisted between restarts (privacy constraint). No BBolt buckets, no new files. (042-telemetry-tier2)
- Bash / GitHub Actions YAML for the CI job; Astro 4.x for the website; Markdown for docs. No Go code changes required. (043-linux-package-repos)
- Go 1.24 (toolchain go1.24.10), Swift 5.9+ (macOS tray only), Bash (DMG post-install script) + `go.etcd.io/bbolt` (existing), `go.uber.org/zap` (existing), `github.com/mark3labs/mcp-go` (existing MCP protocol lib), `github.com/google/uuid` (existing). macOS: `ServiceManagement.framework` (SMAppService, macOS 13+), existing `native/macos/MCPProxy` module. No new external dependencies. (044-retention-telemetry-v3)
- BBolt (`~/.mcpproxy/config.db`) — new `activation` bucket alongside existing buckets; no migration required because absence of bucket means "fresh install, all flags false". (044-retention-telemetry-v3)
- Go 1.24 (toolchain go1.24.10), TypeScript 5.9 / Vue 3.5, Swift 5.9 (macOS 13+) (044-diagnostics-taxonomy)
- No new persistent storage. Diagnostic state lives on in-memory stateview snapshot. Fix-attempt audit rows reuse existing activity log (`ActivityBucket` in BBolt). Telemetry counters are in-memory only (consistent with spec 042). (044-diagnostics-taxonomy)
- Markdown (agent instruction files, wiki articles); optionally shell or AppleScript helpers for bootstrap idempotency + Paperclip AI (paperclipai/paperclip, MIT) running locally on loopback :3100; Synapbus on kubic; Anthropic API via Paperclip's Claude Code subprocess adapter (045-paperclip-cockpit)
- Paperclip's embedded Postgres (existing, port 54329); Synapbus DB (existing); no new storage in mcpproxy-go (045-paperclip-cockpit)

## Recent Changes
- 001-update-version-display: Added Go 1.24 (toolchain go1.24.10)
