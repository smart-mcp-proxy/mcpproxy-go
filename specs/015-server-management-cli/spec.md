# Feature Specification: Server Management CLI

**Feature Branch**: `015-server-management-cli`
**Created**: 2025-12-26
**Status**: Draft
**Input**: User description: "Implement Server Management CLI with Claude-style notation (RFC-001)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Add HTTP Server via CLI (Priority: P1)

Users and AI agents need to add new MCP servers to mcpproxy configuration using a simple CLI command. The command syntax should match Claude Code's MCP CLI for ecosystem consistency.

**Why this priority**: Adding servers is the most common operation. Without this, users must manually edit config files, which is error-prone and not automatable.

**Independent Test**: Can be fully tested by running `mcpproxy upstream add notion https://mcp.notion.com/sse` and verifying the server appears in config and connects.

**Acceptance Scenarios**:

1. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add github https://api.github.com/mcp`, **Then** a new HTTP server named "github" is added to the configuration
2. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add myapi https://api.example.com --header "Authorization: Bearer $TOKEN"`, **Then** the server is added with the specified header
3. **Given** a server named "github" already exists, **When** a user runs `mcpproxy upstream add github https://new-url.com`, **Then** an error is returned indicating the server already exists
4. **Given** a server named "github" already exists, **When** a user runs `mcpproxy upstream add github https://new-url.com --if-not-exists`, **Then** no error is returned and existing server is unchanged

---

### User Story 2 - Add Stdio Server via CLI (Priority: P1)

Users need to add stdio-based MCP servers (local processes) using the `--` separator to distinguish mcpproxy flags from the server command.

**Why this priority**: Many MCP servers are stdio-based (npx packages). This is equally important as HTTP server support.

**Independent Test**: Can be tested by running `mcpproxy upstream add filesystem -- npx -y @anthropic/mcp-server-filesystem` and verifying the process starts.

**Acceptance Scenarios**:

1. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add fs -- npx -y @anthropic/mcp-server-filesystem`, **Then** a stdio server named "fs" is added with the specified command
2. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add db --env DATABASE_URL=postgres://localhost/db -- npx -y @anthropic/mcp-server-postgres`, **Then** the server is added with the environment variable set
3. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add project --working-dir /path/to/project -- node ./server.js`, **Then** the server is configured to run in the specified directory

---

### User Story 3 - Remove Server via CLI (Priority: P2)

Users need to remove MCP servers from configuration using a simple CLI command with confirmation prompts for safety.

**Why this priority**: Removing servers is needed for cleanup but less frequent than adding. Safety prompts prevent accidental deletion.

**Independent Test**: Can be tested by running `mcpproxy upstream remove github` and verifying the server is removed from config.

**Acceptance Scenarios**:

1. **Given** a server named "github" exists, **When** a user runs `mcpproxy upstream remove github`, **Then** a confirmation prompt appears and the server is removed upon confirmation
2. **Given** a server named "github" exists, **When** a user runs `mcpproxy upstream remove github --yes`, **Then** the server is removed without confirmation prompt
3. **Given** no server named "unknown" exists, **When** a user runs `mcpproxy upstream remove unknown`, **Then** an error is returned indicating the server doesn't exist
4. **Given** no server named "unknown" exists, **When** a user runs `mcpproxy upstream remove unknown --if-exists`, **Then** no error is returned

---

### User Story 4 - Add Server from JSON Configuration (Priority: P3)

Power users and automation scripts need to add servers with complex configurations using inline JSON.

**Why this priority**: Covers advanced use cases where command-line flags are insufficient.

**Independent Test**: Can be tested by running `mcpproxy upstream add-json weather '{"url":"https://api.weather.com/mcp"}'`.

**Acceptance Scenarios**:

1. **Given** mcpproxy is running, **When** a user runs `mcpproxy upstream add-json myapi '{"url":"https://api.example.com","headers":{"X-API-Key":"secret"}}'`, **Then** the server is added with all specified configuration
2. **Given** invalid JSON is provided, **When** a user runs `mcpproxy upstream add-json bad '{invalid}'`, **Then** a clear JSON parse error is returned

---

### Edge Cases

- What happens when `--env` is specified without a value? (Return error explaining required KEY=value format)
- What happens when command after `--` is empty? (Return error requiring at least one command argument)
- What happens when server name contains invalid characters? (Return error with valid character list: alphanumeric, hyphens, underscores)
- How does the system handle very long header values? (Accept up to reasonable limit, warn if truncated)
- What happens when --transport is explicitly specified but conflicts with args? (Explicit flag takes precedence)

## Requirements *(mandatory)*

### Functional Requirements

**Server Add Command**:
- **FR-001**: System MUST support `mcpproxy upstream add <name> <url>` for HTTP servers
- **FR-002**: System MUST support `mcpproxy upstream add <name> -- <command> [args...]` for stdio servers
- **FR-003**: System MUST support `--transport http|stdio` flag to explicitly set transport type
- **FR-004**: System MUST infer transport type from arguments when not explicitly specified (URL = http, -- = stdio)
- **FR-005**: System MUST support `--env KEY=value` flag (repeatable) for setting environment variables
- **FR-006**: System MUST support `--header "Name: value"` flag (repeatable) for HTTP headers
- **FR-007**: System MUST support `--working-dir <path>` flag for stdio server working directory
- **FR-008**: System MUST support `--if-not-exists` flag for idempotent adds
- **FR-009**: System MUST persist server configuration to config file immediately after add

**Server Add-JSON Command**:
- **FR-010**: System MUST support `mcpproxy upstream add-json <name> '<json>'` command
- **FR-011**: JSON configuration MUST support all server fields: url, command, args, env, headers, working_dir, protocol
- **FR-012**: System MUST validate JSON structure before adding server

**Server Remove Command**:
- **FR-013**: System MUST support `mcpproxy upstream remove <name>` command
- **FR-014**: System MUST prompt for confirmation before removing (unless `--yes` specified)
- **FR-015**: System MUST support `--yes` flag to skip confirmation
- **FR-016**: System MUST support `--if-exists` flag for idempotent removes
- **FR-017**: System MUST stop the server process (if running) before removing from config

**Validation**:
- **FR-018**: Server names MUST be validated (alphanumeric, hyphens, underscores, 1-64 chars)
- **FR-019**: URLs MUST be validated as valid HTTP/HTTPS URLs
- **FR-020**: Environment variable format MUST be validated as KEY=value

**Core Integration**:
- **FR-021**: Add/remove operations MUST go through management service layer (not direct config manipulation)
- **FR-022**: Operations MUST emit appropriate events for tray/UI synchronization
- **FR-023**: New servers MUST be placed in quarantine by default (security requirement)

### Key Entities

- **ServerConfig**: Configuration for an MCP server including name, url/command, args, env, headers, working_dir, protocol, and enabled status.

- **AddServerRequest**: Request to add a new server with all configuration options.

- **RemoveServerRequest**: Request to remove a server with name and confirmation bypass flag.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can add an HTTP server in a single command under 5 seconds (verifiable by timing command execution)
- **SC-002**: Users can add a stdio server with environment variables in a single command (verifiable by running add command)
- **SC-003**: Server configuration persists across mcpproxy restarts (verifiable by restarting and checking config)
- **SC-004**: AI agents using mcp-eval scenarios can add/remove servers with 95%+ success rate (verifiable by running mcp-eval)
- **SC-005**: Command syntax matches Claude Code MCP CLI pattern (verifiable by comparing command structures)
- **SC-006**: New servers are automatically quarantined for security review (verifiable by checking server status after add)

## Assumptions

- The management service layer (from previous specs) handles the actual server lifecycle
- Server configuration is persisted to the same config file used by mcpproxy serve
- The `--` separator for stdio commands follows standard Unix conventions
- Environment variable values may contain `=` characters (only first `=` is the separator)

## Dependencies

- **Spec 014**: CLI Output Formatting (for consistent output)
- **Existing Components**:
  - `internal/management/service.go`: Management service for server operations
  - `internal/config/`: Configuration persistence
  - `cmd/mcpproxy/upstream_cmd.go`: Existing upstream commands to be extended

## Out of Scope

- `--scope` flag for project vs global config (marked as future in RFC-001)
- Server update/modify command (use remove + add workflow)
- Bulk add/remove operations
- Interactive server configuration wizard

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]`

**Rationale**: Issues should only be closed manually after verification and testing in production.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors.

### Example Commit Message
```
feat(cli): add server management commands with Claude-style notation

Related #151

Implement upstream add/remove commands following Claude Code MCP CLI patterns.
Supports HTTP and stdio servers with --env, --header, and --working-dir flags.

## Changes
- Add upstream add command with positional and flag arguments
- Add -- separator parsing for stdio server commands
- Add --env KEY=value flag with repeatable support
- Add --header "Name: value" flag for HTTP servers
- Add upstream add-json command for complex configurations
- Add upstream remove command with --yes and --if-exists flags
- Integrate with management service for server lifecycle

## Testing
- E2E tests for add/remove operations
- Unit tests for argument parsing
- mcp-eval scenarios for AI agent usage
```
