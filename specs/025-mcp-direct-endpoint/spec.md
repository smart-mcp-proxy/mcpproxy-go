# Feature Specification: MCP Direct Endpoint

**Feature Branch**: `025-mcp-direct-endpoint`
**Created**: 2026-01-26
**Status**: Draft
**Input**: User description: "Add MCP direct endpoint to expose all proxied tools directly alongside internal tools, with server:tool namespacing for upstream tools"
**Related Issue**: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/279

## Problem Statement

Currently, MCPProxy exposes tools through a search-first workflow:
1. Clients call `retrieve_tools` to search for tools
2. Clients call `call_tool_read/write/destructive` with `server:tool` format

This workflow provides significant token savings (~99%) for large tool sets. However, some clients and use cases need direct access to all tools without the search intermediary, especially as AI platforms adopt built-in tool search capabilities (per Anthropic's "Advanced Tool Use" announcement).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Direct Tool Access (Priority: P1)

As an MCP client user, I want to connect to a single endpoint that exposes all available tools directly, so I can use any tool without first searching for it.

**Why this priority**: This is the core value proposition - enabling direct tool access for clients that don't need the search-first workflow.

**Independent Test**: Can be fully tested by connecting an MCP client to the `/mcp/direct` endpoint and verifying all upstream tools appear in the tools/list response alongside internal tools.

**Acceptance Scenarios**:

1. **Given** the direct endpoint is enabled and upstream servers are connected, **When** a client requests the tools list from `/mcp/direct`, **Then** the response includes all tools from connected upstream servers with `server:tool` naming format.

2. **Given** the direct endpoint is enabled, **When** a client requests the tools list, **Then** the response includes management MCPProxy tools (upstream_servers, quarantine_security, code_execution, list_registries, search_servers, read_cache) without a server prefix. Note: Search-workflow tools (retrieve_tools, call_tool_read, call_tool_write, call_tool_destructive) are excluded as they are redundant in direct mode.

3. **Given** the direct endpoint is enabled and a tool exists as `github:create_issue`, **When** a client calls this tool directly, **Then** the call is routed to the github upstream server and executed successfully.

---

### User Story 2 - Configuration Toggle (Priority: P1)

As a system administrator, I want to enable or disable the direct endpoint via configuration, so I can choose whether to expose all tools directly or require the search-first workflow.

**Why this priority**: Essential for deployment flexibility - some environments want search-first (token savings), others want direct access.

**Independent Test**: Can be tested by toggling the configuration flag and verifying the endpoint availability changes accordingly.

**Acceptance Scenarios**:

1. **Given** the configuration option `enable_direct_endpoint` is set to `true`, **When** the server starts, **Then** the `/mcp/direct` endpoint is available and functional.

2. **Given** the configuration option `enable_direct_endpoint` is set to `false` (or not set), **When** a client attempts to access `/mcp/direct`, **Then** the endpoint is not available (returns 404).

3. **Given** the direct endpoint is enabled, **When** viewing server status or logs, **Then** the system indicates the direct endpoint is active.

---

### User Story 3 - Security Exclusions (Priority: P2)

As a security-conscious administrator, I want quarantined servers' tools excluded from the direct endpoint, so I don't accidentally expose unapproved tools to clients.

**Why this priority**: Important for security, but secondary to core functionality since quarantine is an existing security feature.

**Independent Test**: Can be tested by quarantining a server and verifying its tools disappear from the direct endpoint's tool list.

**Acceptance Scenarios**:

1. **Given** a server is quarantined, **When** a client requests the tools list from `/mcp/direct`, **Then** tools from the quarantined server are not included in the response.

2. **Given** a server is disabled, **When** a client requests the tools list from `/mcp/direct`, **Then** tools from the disabled server are not included in the response.

3. **Given** a server is disconnected, **When** a client requests the tools list from `/mcp/direct`, **Then** tools from the disconnected server are not included in the response.

---

### User Story 4 - Dynamic Tool Synchronization (Priority: P2)

As an MCP client user, I want the direct endpoint's tool list to update automatically when upstream servers connect or disconnect, so I always see the current available tools.

**Why this priority**: Important for reliability but secondary to initial tool exposure.

**Independent Test**: Can be tested by connecting/disconnecting an upstream server and observing the tool list update on the direct endpoint.

**Acceptance Scenarios**:

1. **Given** the direct endpoint is active and an upstream server connects, **When** the server's tools become available, **Then** the direct endpoint's tool list is updated to include the new tools.

2. **Given** the direct endpoint is active and an upstream server disconnects, **When** the server becomes unavailable, **Then** the direct endpoint's tool list is updated to remove the server's tools.

3. **Given** the direct endpoint is active, **When** an upstream server's tools change (via tools/list_changed notification), **Then** the direct endpoint's tool list reflects the changes.

---

### User Story 5 - Tool Annotation Preservation (Priority: P3)

As an MCP client developer, I want upstream tool annotations (readOnlyHint, destructiveHint, etc.) preserved on the direct endpoint, so my client can make informed decisions about tool behavior.

**Why this priority**: Nice-to-have for client intelligence but not critical for basic functionality.

**Independent Test**: Can be tested by verifying tool annotations from upstream servers appear correctly on tools retrieved from the direct endpoint.

**Acceptance Scenarios**:

1. **Given** an upstream tool has `readOnlyHint: true` annotation, **When** the tool is listed on the direct endpoint, **Then** the annotation is preserved in the tool definition.

2. **Given** an upstream tool has `destructiveHint: true` annotation, **When** the tool is listed on the direct endpoint, **Then** the annotation is preserved in the tool definition.

---

### User Story 6 - Admin Server Management (Priority: P2)

As an administrator, I want connected clients to be notified when I disable, enable, quarantine, or unquarantine an upstream server, so their tool lists stay synchronized with the current server state.

**Why this priority**: Essential for operational consistency - admin actions must propagate to all connected clients.

**Independent Test**: Can be tested by disabling/enabling a server while a client is connected and verifying the client receives a tool list update notification.

**Acceptance Scenarios**:

1. **Given** a client is connected to `/mcp/direct` and server "github" is enabled, **When** an administrator disables "github" via CLI/WebUI/API, **Then** the system sends `notifications/tools/list_changed` to the client, and the client's subsequent `tools/list` call excludes github:* tools.

2. **Given** a client is connected to `/mcp/direct` and server "github" is disabled, **When** an administrator enables "github" via CLI/WebUI/API, **Then** the system sends `notifications/tools/list_changed` to the client, and the client's subsequent `tools/list` call includes github:* tools.

3. **Given** a client is connected to `/mcp/direct` and server "github" is not quarantined, **When** an administrator quarantines "github", **Then** the system sends `notifications/tools/list_changed` to the client, and the client's subsequent `tools/list` call excludes github:* tools.

4. **Given** a client is connected to `/mcp/direct` and server "github" is quarantined, **When** an administrator unquarantines (approves) "github", **Then** the system sends `notifications/tools/list_changed` to the client, and the client's subsequent `tools/list` call includes github:* tools (once connected).

---

### Edge Cases

- What happens when all upstream servers are disabled or quarantined? → Only internal tools are listed
- What happens when two upstream servers have tools with the same name? → They're disambiguated by server prefix (e.g., `serverA:read_file` vs `serverB:read_file`)
- What happens if an upstream server provides a tool named like an internal tool? → Server-prefixed name prevents collision (e.g., `myserver:retrieve_tools` vs `retrieve_tools`)
- How does the system handle rapid connect/disconnect cycles? → Tool list updates atomically on each state change

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a new MCP endpoint at `/mcp/direct` when the feature is enabled
- **FR-002**: System MUST expose all tools from connected, enabled, non-quarantined upstream servers on the direct endpoint
- **FR-003**: System MUST expose management MCPProxy tools (upstream_servers, quarantine_security, read_cache, code_execution, list_registries, search_servers) on the direct endpoint. Search-workflow tools (retrieve_tools, call_tool_read, call_tool_write, call_tool_destructive) MUST be excluded as they are redundant in direct mode
- **FR-004**: System MUST namespace upstream tools using `server:tool` format to prevent collisions
- **FR-005**: System MUST NOT namespace internal tools (they appear without prefix)
- **FR-006**: System MUST exclude tools from quarantined servers
- **FR-007**: System MUST exclude tools from disabled servers
- **FR-008**: System MUST exclude tools from disconnected servers
- **FR-009**: System MUST synchronize the tool list when upstream server state changes (connect, disconnect, quarantine, enable/disable)
- **FR-010**: System MUST preserve tool annotations (readOnlyHint, destructiveHint, idempotentHint, openWorldHint, title) from upstream servers
- **FR-011**: System MUST route tool calls to the appropriate upstream server based on the server prefix
- **FR-012**: System MUST handle internal tool calls by delegating to existing handler logic
- **FR-013**: System MUST provide a configuration option `enable_direct_endpoint` to enable/disable this feature
- **FR-014**: System MUST default the `enable_direct_endpoint` configuration to `false` (opt-in)
- **FR-015**: System MUST return 404 for `/mcp/direct` when the feature is disabled
- **FR-016**: System MUST send MCP `notifications/tools/list_changed` notification to all connected `/mcp/direct` clients when the tool list changes due to: upstream server connect/disconnect, upstream server enable/disable, upstream server quarantine/unquarantine, or upstream server's own `tools/list_changed` notification

### Key Entities

- **DirectMCPServer**: A separate MCP server instance that manages the direct endpoint, distinct from the search-based MCPProxyServer
- **Tool Namespace**: The `server:tool` naming convention that maps upstream tools to their source server
- **Tool Synchronization**: The mechanism that keeps the direct endpoint's tool list in sync with upstream server state, using MCP `notifications/tools/list_changed` to notify connected clients of changes

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Clients connecting to `/mcp/direct` receive a complete tool list within 500ms of connection
- **SC-002**: Tool list updates reflect upstream server changes within 2 seconds of state change
- **SC-003**: 100% of upstream tool calls through the direct endpoint succeed when the upstream server is healthy
- **SC-004**: Tool annotations are preserved with 100% fidelity from upstream to direct endpoint
- **SC-005**: Zero tools from quarantined/disabled/disconnected servers appear in the direct endpoint's tool list
- **SC-006**: All connected clients receive `notifications/tools/list_changed` within 1 second of an admin action (enable/disable/quarantine/unquarantine)

## Assumptions

- The existing `EventTypeServersChanged` event provides sufficient notification for tool synchronization
- The `StateView` contains cached tool information suitable for rebuilding the tool list
- The `mcp-go` library's `SetTools()` method provides atomic tool list replacement
- The `mcp-go` library supports sending `notifications/tools/list_changed` to connected clients
- Internal tool handlers can be shared between the existing MCPProxyServer and the new DirectMCPServer

## Out of Scope

- Changing the behavior of the existing `/mcp` endpoint
- Per-server or per-tool filtering on the direct endpoint (all-or-nothing exposure)
- Session sharing between `/mcp` and `/mcp/direct` endpoints
- WebUI integration for the direct endpoint

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #279` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #279`, `Closes #279`, `Resolves #279` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add MCP direct endpoint for all-tools exposure

Related #279

Adds a new /mcp/direct endpoint that exposes all upstream tools directly
alongside internal MCPProxy tools, enabling clients to use tools without
the search-first workflow.

## Changes
- Add EnableDirectEndpoint config option (default: false)
- Create DirectMCPServer with tool synchronization
- Register internal tools and upstream tools with server:tool namespacing
- Mount endpoint at /mcp/direct when enabled

## Testing
- E2E tests for tool listing and calling
- Unit tests for tool synchronization logic
```
