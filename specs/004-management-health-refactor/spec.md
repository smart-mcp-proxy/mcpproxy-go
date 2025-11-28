# Feature Specification: Management Service Refactoring & OpenAPI Generation

**Feature Branch**: `004-management-health-refactor`
**Created**: 2025-11-23
**Status**: Draft
**Input**: User description: "Need to refactor mcpproxy core service part responsible to upstream servers lifecycle management and health diagnostics read details in @docs/shared-management-health-plan.md \ Need to do refactoring but keep current cmd options, subcommands the same - don't break this cmd interface. Important addition I want to have openapi spec .yaml file actual for each mcpproxy release. Research best way to generate this file from source code. We can reuse `make build` command to regenerate file."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Unified Management Operations (Priority: P1)

Operations teams and CLI users need consistent server management behavior whether they interact via CLI commands, REST API, or MCP tools. Currently, there is duplicated logic across these interfaces leading to inconsistent behavior and maintenance burden.

**Why this priority**: This is the foundation for reliable server management. Without a unified service layer, bugs and inconsistencies multiply across interfaces, and new features must be implemented multiple times.

**Independent Test**: Can be fully tested by executing the same operation (e.g., restart server) via all three interfaces (CLI, REST, MCP) and verifying identical behavior, event notifications, and state changes.

**Acceptance Scenarios**:

1. **Given** a server is running, **When** an operator restarts it via `mcpproxy upstream restart <name>`, **Then** the same restart logic executes as calling `POST /api/v1/servers/{name}/restart` or using the `upstream_servers` MCP tool
2. **Given** management is disabled via `disable_management` config, **When** any interface attempts a write operation, **Then** all interfaces return the same error message without executing the operation
3. **Given** `read_only` mode is enabled, **When** any interface attempts to modify server state, **Then** all interfaces block the operation consistently
4. **Given** a server has OAuth requirements, **When** diagnostics are requested via any interface, **Then** all return identical diagnostic information including OAuth status

---

### User Story 2 - Comprehensive Health Diagnostics (Priority: P1)

Operators debugging server issues need a single `doctor` command that provides comprehensive health information across all servers, identifying connection errors, authentication issues, missing secrets, and runtime warnings.

**Why this priority**: This is the primary troubleshooting entry point. Without reliable diagnostics, operators waste time investigating issues manually across multiple log files and status endpoints.

**Independent Test**: Can be fully tested by creating known failure conditions (e.g., missing OAuth token, invalid config) and verifying `mcpproxy doctor` detects and reports all issues with actionable guidance.

**Acceptance Scenarios**:

1. **Given** a server requires OAuth authentication, **When** operator runs `mcpproxy doctor`, **Then** the output identifies which servers need authentication and provides login instructions
2. **Given** multiple servers have connection errors, **When** operator runs `mcpproxy doctor`, **Then** all errors are aggregated with server names, error types, and resolution steps
3. **Given** secrets are missing from environment/keyring, **When** operator runs `mcpproxy doctor`, **Then** missing secrets are identified by name and location
4. **Given** Docker isolation is configured, **When** operator runs `mcpproxy doctor`, **Then** Docker daemon status and container health are included in diagnostics
5. **Given** all systems are healthy, **When** operator runs `mcpproxy doctor`, **Then** output confirms "No issues found" with summary statistics

---

### User Story 3 - Automatic OpenAPI Documentation (Priority: P2)

API consumers and integrators need accurate OpenAPI 3.x specification files that automatically stay synchronized with the codebase for every release.

**Why this priority**: Manual API documentation becomes outdated quickly, breaking integrations and wasting developer time. Generated specs ensure documentation accuracy and enable automated client generation.

**Independent Test**: Can be fully tested by running `make build`, verifying the generated OpenAPI YAML file exists, and validating it against the OpenAPI 3.x schema. Success means third-party tools can consume the spec without errors.

**Acceptance Scenarios**:

1. **Given** HTTP handlers have annotation comments, **When** `make build` executes, **Then** an OpenAPI 3.x YAML file is generated in the project root with all documented endpoints
2. **Given** a new REST endpoint is added with annotations, **When** build runs, **Then** the endpoint appears in the generated spec with correct path, method, parameters, and responses
3. **Given** the OpenAPI spec is generated, **When** validated with standard tools, **Then** it conforms to OpenAPI 3.x specification without errors
4. **Given** authentication is configured, **When** spec is generated, **Then** security schemes (API key header/query param) are documented correctly

---

### User Story 4 - Bulk Server Management (Priority: P3)

Operators managing multiple servers need efficient bulk operations (enable-all, disable-all, restart-all) to manage entire fleets without individual commands.

**Why this priority**: This improves operational efficiency for users with many configured servers but is not critical for basic functionality. Can be deferred if time-constrained.

**Independent Test**: Can be fully tested by configuring 5+ servers and executing `mcpproxy upstream restart --all`, verifying all servers restart and return counts match configuration.

**Acceptance Scenarios**:

1. **Given** 10 servers are configured, **When** operator runs `mcpproxy upstream enable --all`, **Then** all servers are enabled and command returns count of 10
2. **Given** management is disabled, **When** operator attempts `restart --all`, **Then** operation is blocked with appropriate error message
3. **Given** bulk restart is requested via REST API, **When** POST to `/api/v1/servers/restart_all` executes, **Then** all servers restart sequentially and status events are emitted for each

---

### Edge Cases

- What happens when a management service method is called while the server is shutting down? (Should return context-canceled errors gracefully)
- How does the system handle rapid successive restart requests to the same server? (Should queue or reject duplicate operations)
- What happens if OpenAPI generation fails during build? (Build should fail with clear error, not produce invalid spec)
- How are concurrent read/write operations to server state handled across interfaces? (Management service should use appropriate locking)
- What happens when diagnostics are requested for a server that doesn't exist? (Should skip gracefully and note in output)
- How does the system handle partial failures in bulk operations? (Should continue processing and report which servers succeeded/failed)

## Requirements *(mandatory)*

### Functional Requirements

**Core Service Layer**:
- **FR-001**: System MUST provide a single `ManagementService` interface that all CLI, REST, and MCP interfaces invoke for server lifecycle operations
- **FR-002**: Management service MUST enforce `disable_management` and `read_only` configuration flags before executing any write operations
- **FR-003**: Management service MUST emit standardized events for all state changes (server enabled/disabled, restarted, quarantined)
- **FR-004**: Service methods MUST return consistent error types and messages across all calling interfaces

**Server Lifecycle Operations**:
- **FR-005**: System MUST support listing all servers with connection status, tool counts, and error states
- **FR-006**: System MUST support enable/disable operations for individual servers and bulk operations for all servers
- **FR-007**: System MUST support restart operations for individual servers with optional `--all` flag for bulk restart
- **FR-008**: System MUST retrieve server logs with configurable tail length (default 50 lines, max 1000 lines)

**Logging Requirements**:
- **FR-008a**: For **stdio servers**, logs MUST include stderr output from the MCP server process
- **FR-008b**: For **HTTP servers**, logs MUST include request metadata (method, URL, headers, status codes) with sanitized authentication tokens
- **FR-008c**: For **OAuth flows**, logs MUST include token operations (load/save/clear), scope discovery, and token exchange requests with redacted tokens (e.g., `eyJh...***...xyz`)
- **FR-008d**: For **Docker containers**, logs MUST include container stderr output streamed to server log files
- **FR-008e**: All logs MUST be written to per-server log files (`server-{name}.log`) with automatic sanitization of secrets (API keys, tokens, passwords)
- **FR-008f**: Management service MUST expose all logged information via `GetServerLogs()` method

**Health Diagnostics**:
- **FR-009**: System MUST provide a `Doctor` method that aggregates diagnostics across all servers
- **FR-010**: Diagnostics MUST identify servers requiring OAuth authentication and provide login instructions
- **FR-011**: Diagnostics MUST detect missing secrets referenced in server configurations
- **FR-012**: Diagnostics MUST report upstream connection errors with error messages and timestamps
- **FR-013**: Diagnostics MUST include Docker isolation status when configured
- **FR-014**: System MUST support both JSON and human-readable output formats for diagnostics

**REST API Alignment**:
- **FR-015**: All existing REST endpoints under `/api/v1/servers/*` MUST delegate to the management service layer
- **FR-016**: System MUST add `/api/v1/doctor` endpoint returning the same diagnostics as CLI `doctor` command
- **FR-017**: System MUST add bulk operation endpoints: `/api/v1/servers/restart_all`, `/api/v1/servers/enable_all`, `/api/v1/servers/disable_all`
- **FR-018**: REST endpoints MUST respect authentication requirements (API key or socket-based trust)

**MCP Tool Parity**:
- **FR-019**: `upstream_servers` MCP tool MUST support `restart` operation alongside existing list/enable/disable operations
- **FR-020**: System MUST add `doctor` MCP tool returning diagnostic information in JSON format
- **FR-021**: MCP tools MUST call the same management service methods as REST and CLI interfaces

**CLI Interface Preservation**:
- **FR-022**: All existing `mcpproxy upstream` subcommands (list, logs, enable, disable, restart) MUST maintain current flag names and behavior
- **FR-023**: `mcpproxy doctor` command MUST maintain current output format and flag options (--output, --log-level, --config)
- **FR-024**: CLI commands MUST continue to work in both daemon mode (via REST client) and standalone mode where applicable

**OpenAPI Documentation**:
- **FR-025**: System MUST generate OpenAPI 3.x specification from code annotations using swaggo/swag
- **FR-026**: All REST API endpoints MUST have swag annotations documenting path, method, parameters, request/response schemas
- **FR-027**: Generated OpenAPI spec MUST validate against OpenAPI 3.x schema without errors
- **FR-028**: `make build` command MUST regenerate OpenAPI spec automatically as part of the build process
- **FR-029**: OpenAPI spec MUST document authentication schemes (X-API-Key header and apikey query parameter)
- **FR-030**: Generated spec file MUST be located at `oas/swagger.yaml` in the repository root

### Key Entities

- **ManagementService**: Core service interface providing lifecycle, diagnostics, and log retrieval methods. Enforces configuration gates and emits events for state changes.

- **Diagnostics**: Aggregated health information containing: total issue count, upstream connection errors (server name, error message, timestamp), OAuth requirements (server name, auth state), missing secrets (secret names, referenced by which servers), runtime warnings, Docker daemon status.

- **ServerStats**: Summary statistics returned with server lists: total servers, enabled count, disabled count, connected count, error count, quarantined count.

- **LogEntry**: Individual log line with timestamp, level, server name, and message. Used for server-specific log retrieval.

- **AuthStatus**: OAuth authentication state for a server containing: server name, state (unauthenticated/authenticated/expired), token expiration timestamp, actionable message.

## Logging Coverage *(detailed specification)*

The `mcpproxy upstream logs <server-name>` command (via management service) exposes comprehensive operational visibility:

### Stdio MCP Servers

**What's Logged** (to `server-{name}.log`):
- ✅ **Stderr output** - Every line written to stderr by the MCP server process
- ✅ **Process lifecycle** - Start/stop events, PID, exit codes
- ✅ **Pipe errors** - Broken pipes, closed pipes, connection issues
- ✅ **Initialization errors** - Missing API keys, invalid config (captured before timeout)

**Example Log Entries**:
```
2025-11-23T10:30:15Z INFO  [server=weather-api] stderr message="API key loaded successfully"
2025-11-23T10:30:16Z INFO  [server=weather-api] stderr message="Listening for MCP requests..."
2025-11-23T10:35:20Z ERROR [server=weather-api] Stderr stream ended server=weather-api
```

### HTTP MCP Servers

**What Will Be Logged** (enhancement - FR-008b):
- ✅ **HTTP request metadata** - Method, URL, headers (sanitized)
- ✅ **Response status** - Status code, response time
- ✅ **Authentication headers** - Logged with tokens redacted (e.g., `Bearer eyJ...***...xyz`)
- ❌ **NOT logged** - Full response bodies (to prevent log flooding)

**Example Log Entries**:
```
2025-11-23T10:30:00Z INFO  [server=github-mcp] HTTP Request method=POST url=/mcp/tools/list headers="Authorization: Bearer ghp_***abc"
2025-11-23T10:30:01Z INFO  [server=github-mcp] HTTP Response status=200 duration=125ms
2025-11-23T10:30:15Z WARN  [server=github-mcp] HTTP Request Failed status=401 error="Unauthorized"
```

### OAuth Flows

**What's Already Logged**:
- ✅ **Token operations** - Load/save/clear from storage
- ✅ **Token expiry** - Expiration warnings, refresh recommendations
- ✅ **Scope discovery** - RFC 9728 Protected Resource Metadata, RFC 8414 Authorization Server Metadata
- ✅ **OAuth completion** - Cross-process notifications

**What Will Be Added** (enhancement - FR-008c):
- ✅ **Token exchange requests** - Authorization code → access token, with redacted tokens
- ✅ **Dynamic Client Registration (DCR)** - Client registration requests/responses
- ✅ **Token refresh** - Refresh token usage and new token acquisition

**Example Log Entries**:
```
2025-11-23T10:25:00Z INFO  [server=sentry-mcp] 💾 Saving OAuth token to persistent storage token_type=Bearer valid_for=7d
2025-11-23T10:25:01Z DEBUG [server=sentry-mcp] 🔍 Loading OAuth token from persistent storage
2025-11-23T10:25:02Z INFO  [server=sentry-mcp] ✅ OAuth token is valid and not expiring soon expires_at=2025-11-30T10:00:00Z
2025-11-23T11:00:00Z INFO  [server=sentry-mcp] 🔄 Token exchange: POST /oauth/token grant_type=authorization_code
2025-11-23T11:00:01Z INFO  [server=sentry-mcp] ✅ Token exchange succeeded access_token=eyJ...***...xyz refresh_token=eyJ...***...abc
```

### Docker Containers

**What's Currently Available**:
- ✅ **Container ID** - Captured for cleanup
- ✅ **Docker logs** - Accessible via Docker API (`docker logs <container_id>`)
- ✅ **Container lifecycle** - Start/stop events

**What Will Be Added** (enhancement - FR-008d):
- ✅ **Container stderr stream** - Real-time stderr output in `server-{name}.log`
- ❌ **NOT added** - Container stdout (to prevent log flooding per user choice)

**Example Log Entries**:
```
2025-11-23T10:30:00Z INFO  [server=isolated-server] Docker container started container_id=a1b2c3d4e5f6
2025-11-23T10:30:01Z INFO  [server=isolated-server] Container ID captured container_id=a1b2c3d4e5f6
2025-11-23T10:30:05Z INFO  [server=isolated-server] Container stderr: "Starting MCP server in Docker..."
2025-11-23T10:30:06Z WARN  [server=isolated-server] Container stderr: "Warning: Using default configuration"
```

### Secret Sanitization

**All logs automatically sanitize**:
- 🔒 **GitHub tokens** (ghp_, gho_, ghu_, ghs_, ghr_) → `ghp_***ab`
- 🔒 **Bearer tokens** → `Bearer eyJ...***...xyz`
- 🔒 **JWT tokens** (eyJ...) → `eyJ...***...xyz`
- 🔒 **API keys** (high-entropy strings) → `***`
- 🔒 **Passwords** in URLs → Redacted

**Implementation**: `internal/logs/sanitizer.go` (existing)

### Log Access via Management Service

The management service `GetServerLogs()` method returns all log entries from `server-{name}.log`:

**Parameters**:
- `name` (string) - Server identifier
- `tail` (int) - Number of lines (default 50, max 1000)

**Returns**: `[]contracts.LogEntry` with:
- `Timestamp` - ISO 8601 timestamp
- `Level` - INFO, WARN, ERROR, DEBUG
- `Server` - Server name
- `Message` - Log message (already sanitized)

**CLI Access**:
```bash
mcpproxy upstream logs weather-api --tail=100
mcpproxy upstream logs github-mcp --follow  # Real-time (daemon mode only)
```

**REST API Access**:
```bash
GET /api/v1/servers/weather-api/logs?tail=100
```

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Operators can execute any server management operation (list, enable, disable, restart, logs) via CLI, REST API, or MCP tools and observe identical behavior and state changes
- **SC-002**: Running `mcpproxy doctor` identifies all common issues (connection errors, OAuth requirements, missing secrets) in under 3 seconds for configurations with up to 20 servers
- **SC-003**: Generated OpenAPI specification validates successfully with `swagger-cli validate` and `openapi-generator validate` tools
- **SC-004**: Build process regenerates OpenAPI spec in under 5 seconds as part of `make build` without manual intervention
- **SC-005**: All existing CLI commands maintain backward compatibility - existing scripts and automation continue to work without modification
- **SC-006**: Management service reduces code duplication by at least 40% across CLI/REST/MCP implementations (measured by lines of duplicate logic removed)
- **SC-007**: Unit test coverage for management service layer reaches at least 80% for all public methods
- **SC-008**: Documentation updates reflect the new architecture with clear diagrams showing CLI → Service ← REST ← MCP flow

## Assumptions

1. **Chi router compatibility**: The existing chi router setup supports middleware and mounting patterns required by swaggo/http-swagger
2. **Annotation effort**: Developers can add swag annotations to existing handlers incrementally without rewriting logic
3. **Build tooling**: Go build environment supports running `swag init` as a pre-build step via Makefile
4. **Backward compatibility**: Existing tray application and external API consumers will continue to work with refactored backend
5. **Event system**: Current runtime event bus (`internal/runtime/event_bus.go`) supports management service event emissions
6. **Error handling**: Existing error types and contracts can be reused across all interfaces without schema changes
7. **Testing infrastructure**: Current test suites can be adapted to test the new service layer without major rewrites
8. **OpenAPI versioning**: Generated specs will be versioned alongside releases, with old specs archived for historical reference

## Dependencies

- **Existing Components**:
  - `internal/server/manager.go`: Upstream manager providing low-level server control
  - `internal/runtime/event_bus.go`: Event system for state change notifications
  - `internal/httpapi/server.go`: REST API handlers that will delegate to management service
  - `internal/cliclient/client.go`: CLI-to-daemon REST client that needs new endpoint methods
  - `cmd/mcpproxy/upstream_cmd.go` and `cmd/mcpproxy/doctor_cmd.go`: CLI command implementations

- **External Tools**:
  - **swaggo/swag**: OpenAPI spec generator requiring `go install github.com/swaggo/swag/cmd/swag@latest`
  - **swaggo/http-swagger**: Chi router integration for serving generated docs

- **Configuration**:
  - Existing config flags: `disable_management`, `read_only` (already implemented, service must honor them)
  - No new configuration options required for management refactoring
  - Optional: `enable_swagger_ui` config flag to serve Swagger UI at `/swagger/` (defaulted to true in development, false in production)

## Out of Scope

- Changing the structure or format of existing configuration files beyond what's already planned
- Adding new CLI commands or subcommands (only refactoring existing ones)
- Modifying MCP protocol implementation beyond adding service layer delegation
- Implementing new server lifecycle features (e.g., server health checks, auto-restart on failure)
- Adding authentication/authorization beyond existing API key mechanism
- Real-time log streaming via WebSocket (the existing `--follow` flag is sufficient)
- GraphQL API or other API protocols beyond REST
- Automated OpenAPI client SDK generation (only spec generation is in scope)

**Note on Logging Enhancements**: While FR-008a through FR-008f specify comprehensive logging requirements (HTTP requests, OAuth token exchange, Docker stderr), the **implementation of these logging enhancements is NOT blocked by the management service refactoring**. The management service will expose whatever logs are currently written to `server-{name}.log` files. Logging enhancements (HTTP request logging, OAuth token exchange logging, Docker stderr streaming) can be added incrementally as separate commits or in parallel development.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
refactor: create unified management service layer

Related #[issue-number]

Extract server lifecycle and diagnostics logic into internal/management/service.go
to eliminate duplication across CLI, REST, and MCP interfaces.

## Changes
- Add ManagementService interface with lifecycle and diagnostics methods
- Update REST handlers to delegate to management service
- Migrate CLI client to use new REST endpoints
- Add swag annotations to all /api/v1 handlers
- Integrate swag generation into make build

## Testing
- Unit tests for management service (80% coverage)
- E2E tests verify CLI/REST/MCP consistency
- OpenAPI spec validates with swagger-cli
```
