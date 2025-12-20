# Feature Specification: Subscribe to notifications/tools/list_changed for Automatic Tool Re-indexing

**Feature Branch**: `013-tool-change-notifications`
**Created**: 2025-12-20
**Status**: Draft
**Issue**: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/209
**Input**: User description: "Need to fix this issue https://github.com/smart-mcp-proxy/mcpproxy-go/issues/209 fix, add tests, update online docs"

## Problem Statement

The system currently relies on a 5-minute background polling cycle to discover tool changes from upstream MCP servers. This approach has two significant issues:

1. **Stale Tool Data**: When an MCP server updates its tools (adds, removes, or modifies), users continue to receive outdated results from `retrieve_tools` calls until the next polling cycle completes.

2. **Wasted Resources**: The periodic polling approach makes unnecessary requests even when no changes have occurred, wasting CPU and network resources.

**Concrete Scenario**: When an MCP server upgrades from v1 (tools: `tool_a`, `tool_b`) to v2 (tools: `tool_c`, `tool_d`), users who don't manually trigger discovery via the Web UI receive outdated results - old tools persist while new ones remain missing.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Automatic Tool Discovery on Server Change (Priority: P1)

As an AI developer using MCPProxy, when an upstream MCP server updates its available tools, I want MCPProxy to automatically detect the change and update its tool index immediately, so I always have access to the current tools without manual intervention.

**Why this priority**: This is the core fix for issue #209. Without reactive notification handling, users experience stale tool caches that can persist for up to 5 minutes, causing tool calls to fail or return outdated results.

**Independent Test**: Can be fully tested by connecting to an MCP server that supports `tools/list_changed` notifications, triggering a tool change on the server, and verifying the index updates within seconds.

**Acceptance Scenarios**:

1. **Given** MCPProxy is connected to an upstream MCP server that supports `capabilities.tools.listChanged: true`, **When** the server adds a new tool and sends `notifications/tools/list_changed`, **Then** MCPProxy automatically discovers and indexes the new tool within 5 seconds.

2. **Given** MCPProxy is connected to an upstream MCP server that supports tool change notifications, **When** the server removes a tool and sends `notifications/tools/list_changed`, **Then** MCPProxy removes the tool from its index within 5 seconds.

3. **Given** MCPProxy is connected to an upstream MCP server that supports tool change notifications, **When** the server modifies a tool's schema and sends `notifications/tools/list_changed`, **Then** MCPProxy updates the tool in its index with the new schema.

4. **Given** MCPProxy is connected to an upstream MCP server that does NOT support tool change notifications, **When** the server changes its tools, **Then** MCPProxy continues to discover changes via the existing 5-minute polling mechanism.

---

### User Story 2 - Resilient Notification Handling (Priority: P2)

As a system administrator, I want MCPProxy to gracefully handle notification edge cases (duplicate notifications, notifications during disconnection, rapid successive notifications), so the system remains stable under various conditions.

**Why this priority**: Robust error handling prevents crashes and ensures reliable operation in production environments where network issues and rapid changes are common.

**Independent Test**: Can be tested by simulating rapid successive notifications and verifying the system debounces/handles them correctly without performance degradation.

**Acceptance Scenarios**:

1. **Given** MCPProxy receives multiple `notifications/tools/list_changed` in rapid succession (within 1 second), **When** processing these notifications, **Then** MCPProxy coalesces them into a single discovery operation.

2. **Given** an upstream server disconnects after sending a notification, **When** MCPProxy attempts to discover tools, **Then** it logs the error and continues operating normally.

3. **Given** MCPProxy is processing a tool discovery, **When** another notification arrives for the same server, **Then** the duplicate discovery is skipped with a debug log.

---

### User Story 3 - Logging and Observability (Priority: P3)

As a developer troubleshooting tool discovery issues, I want clear log entries when notifications are received and processed, so I can trace the flow of tool updates through the system.

**Why this priority**: Good observability is essential for debugging issues in production, especially for a feature that operates automatically without user intervention.

**Independent Test**: Can be tested by enabling debug logging, triggering a notification, and verifying the log entries contain useful information.

**Acceptance Scenarios**:

1. **Given** debug logging is enabled, **When** MCPProxy receives a `notifications/tools/list_changed`, **Then** the log includes the server name and timestamp.

2. **Given** a notification triggers tool discovery, **When** discovery completes, **Then** the log includes counts of added, modified, and removed tools.

3. **Given** a notification is received for a server that doesn't support the capability, **When** MCPProxy logs the event, **Then** it logs a warning about the unexpected notification.

---

### Edge Cases

- What happens when a notification is received but the server is in the process of reconnecting?
- How does the system handle a notification from a server that's been removed from config?
- What happens if the tool discovery triggered by notification fails (timeout, network error)?
- How does the system behave when receiving notifications during mcpproxy shutdown?
- What happens if a server sends continuous rapid notifications (potential DoS)?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST register a notification handler for `notifications/tools/list_changed` when connecting to upstream MCP servers.
- **FR-002**: System MUST check server capabilities for `tools.listChanged: true` before relying on notifications.
- **FR-003**: Upon receiving `notifications/tools/list_changed`, system MUST trigger `DiscoverAndIndexToolsForServer(ctx, serverName)` for the specific server.
- **FR-004**: System MUST use the existing differential update logic (from PR #208) when processing notification-triggered discoveries.
- **FR-005**: System MUST deduplicate rapid successive notifications for the same server (within 1 second window).
- **FR-006**: System MUST NOT crash or deadlock when receiving notifications during server state transitions.
- **FR-007**: System MUST continue using 5-minute polling for servers that don't support notifications.
- **FR-008**: System MUST log notification events at INFO level and discovery details at DEBUG level.

### Key Entities

- **Notification Handler**: Callback registered with mcp-go client to receive `JSONRPCNotification` messages.
- **Tool Change Notification**: MCP protocol notification with method `notifications/tools/list_changed`.
- **Server Capabilities**: Metadata indicating whether a server supports `tools.listChanged` capability.
- **Debounce Window**: Time period (1 second) during which duplicate notifications are coalesced.

## Technical Context

### mcp-go Library Support

The mcp-go library (v0.43.1) already provides:

- `mcp.MethodNotificationToolsListChanged = "notifications/tools/list_changed"` constant
- `client.OnNotification(handler func(mcp.JSONRPCNotification))` registration method
- Server capabilities exposed via `GetServerCapabilities()` after initialization

### Existing Infrastructure

The codebase already has:

1. **Reactive tool discovery callback** (`supervisor.SetOnServerConnectedCallback`) - Pattern to follow
2. **Differential update logic** (`applyDifferentialToolUpdate`) - Reuse for efficient indexing
3. **Discovery deduplication** (`discoveryInProgress` sync.Map) - Extend for notification debouncing
4. **OnConnectionLost handler** - Same pattern for registering OnNotification

### Key Integration Points

1. **Registration**: In `internal/upstream/core/connection.go` after `client.Start()` succeeds
2. **Handler Callback**: New method in core client to forward notifications to managed client
3. **Trigger**: Managed client triggers runtime's `DiscoverAndIndexToolsForServer`

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Tools are indexed within 5 seconds of a `notifications/tools/list_changed` being received (down from up to 5 minutes).
- **SC-002**: Zero duplicate discovery operations when receiving rapid successive notifications (debouncing works).
- **SC-003**: Existing 5-minute polling continues to work for servers without notification support (backward compatible).
- **SC-004**: All unit tests pass with 100% coverage on new notification handling code.
- **SC-005**: E2E tests verify notification-triggered discovery works with a test MCP server.
- **SC-006**: No memory leaks or goroutine leaks from notification handlers (verified via pprof in tests).

## Testing Requirements

### Unit Tests

1. **Notification Handler Registration**: Verify handler is registered after successful connection.
2. **Notification Filtering**: Verify only `tools/list_changed` notifications trigger discovery.
3. **Debouncing**: Verify rapid notifications are coalesced correctly.
4. **Capability Check**: Verify warning logged for servers without capability.
5. **Error Handling**: Verify errors during discovery don't crash the system.

### Integration Tests

1. **E2E Notification Flow**: Start test MCP server, trigger tool change, verify index updates.
2. **Mixed Servers**: Verify system handles mix of notification-supporting and non-supporting servers.
3. **Reconnection**: Verify notifications work correctly after server reconnection.

### Test MCP Server Requirements

Need a test MCP server (or enhancement to existing `tests/oauthserver/`) that:
- Supports `capabilities.tools.listChanged: true`
- Can dynamically add/remove tools via API
- Sends `notifications/tools/list_changed` when tools change

## Documentation Updates Required

### Online Documentation (`docs/`)

1. **`docs/features/search-discovery.md`**: Add section on automatic tool discovery via notifications.
2. **`docs/configuration/upstream-servers.md`**: Note that servers supporting notifications get real-time updates.
3. **`docs/api/mcp-protocol.md`**: Document notification handling behavior.

### CLAUDE.md Updates

Add to "Key Implementation Details" section:
- Notification subscription for `tools/list_changed`
- Automatic tool re-indexing behavior

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #209` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #209`, `Closes #209`, `Resolves #209` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(upstream): subscribe to notifications/tools/list_changed for automatic re-indexing

Related #209

Implemented reactive tool discovery by subscribing to MCP notifications
when upstream servers change their available tools.

## Changes
- Register OnNotification handler in core client after connection
- Filter for tools/list_changed notifications
- Trigger DiscoverAndIndexToolsForServer on notification
- Add debouncing for rapid successive notifications
- Log notification events at appropriate levels

## Testing
- Unit tests for notification handler registration and filtering
- Integration test with mock MCP server sending notifications
- Verified backward compatibility with servers lacking notification support
```

## Assumptions

- The mcp-go library v0.43.1 correctly implements notification handling.
- Upstream MCP servers that support `capabilities.tools.listChanged` send notifications reliably.
- The existing differential update logic handles all tool change scenarios correctly.
- A test MCP server can be created or the existing test infrastructure can be extended.
- The 1-second debounce window is sufficient for most use cases.

## Out of Scope

- Forwarding `tools/list_changed` notifications to downstream MCP clients (noted in issue as optional).
- Subscribing to other notification types (`resources/list_changed`, `prompts/list_changed`).
- Web UI indicators for notification support status.
