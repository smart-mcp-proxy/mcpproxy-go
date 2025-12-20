# Research: Tool Change Notifications

**Feature**: 013-tool-change-notifications
**Date**: 2025-12-20
**Status**: Complete

## Research Questions

### RQ-001: mcp-go Notification Handler API

**Question**: How does the mcp-go library expose notification handling for MCP clients?

**Research Findings**:

The mcp-go library (v0.43.1) provides a clean notification handler API:

```go
// In client/client.go
func (c *Client) OnNotification(handler func(notification mcp.JSONRPCNotification)) {
    c.notifyMu.Lock()
    defer c.notifyMu.Unlock()
    c.notifications = append(c.notifications, handler)
}
```

The handler is invoked for ALL incoming notifications. The handler function receives a `mcp.JSONRPCNotification` with the following structure:

```go
type JSONRPCNotification struct {
    JSONRPC      string       `json:"jsonrpc"`
    Notification Notification `json:"notification"`
}

type Notification struct {
    Method string `json:"method"`
    Params any    `json:"params,omitempty"`
}
```

**Decision**: Use `client.OnNotification()` to register a handler that filters for `mcp.MethodNotificationToolsListChanged`.

**Rationale**: This is the official API provided by mcp-go. It follows the same pattern as `OnConnectionLost()` which is already used in the codebase.

**Alternatives Considered**:
- Polling more frequently (rejected: wastes resources, still has latency)
- Modifying mcp-go library (rejected: unnecessary, API already sufficient)

---

### RQ-002: Server Capabilities Detection

**Question**: How do we detect if an upstream server supports `tools/list_changed` notifications?

**Research Findings**:

After MCP initialization, server capabilities are available via:

```go
// In mcp/types.go
type ServerCapabilities struct {
    Tools *struct {
        // Whether this server supports notifications for changes to the tool list.
        ListChanged bool `json:"listChanged,omitempty"`
    } `json:"tools,omitempty"`
}
```

The client exposes this via:

```go
caps := c.client.GetServerCapabilities()
if caps.Tools != nil && caps.Tools.ListChanged {
    // Server supports tool change notifications
}
```

**Decision**: Check `serverInfo.Capabilities.Tools.ListChanged` after successful initialization to determine if notification subscription is meaningful.

**Rationale**: This follows the MCP specification exactly. Servers that support notifications will have `capabilities.tools.listChanged: true` in their initialize response.

**Alternatives Considered**:
- Always subscribing regardless of capability (rejected: wasteful, could cause confusion with unexpected notifications)
- Adding config option per server (rejected: over-engineering, auto-detection is better)

---

### RQ-003: Integration Point in Existing Architecture

**Question**: Where should notification handlers be registered in the 3-layer upstream client architecture?

**Research Findings**:

Current architecture (internal/upstream/):
1. **Core Client** (`core/client.go`): Raw MCP client wrapper, owns the `*client.Client`
2. **Managed Client** (`managed/client.go`): Adds state management, retries, callbacks
3. **Manager** (`manager.go`): Manages all upstream connections, has access to Runtime

The notification handler needs to:
1. Be registered at **core level** (where `c.client` is created)
2. Forward notification events to **managed level** (state management)
3. Trigger tool discovery via **runtime** (has `DiscoverAndIndexToolsForServer`)

Existing pattern in `core/connection.go`:

```go
c.client.OnConnectionLost(func(err error) {
    // Handle connection loss
})
```

**Decision**:
1. Register `OnNotification` in `core/connection.go` after `client.Start()`
2. Add callback mechanism in core Client struct for notification forwarding
3. Managed client sets callback during construction
4. Managed client forwards to runtime's discovery function

**Rationale**: Follows existing patterns (`OnConnectionLost`), maintains layer separation, allows managed layer to handle state concerns.

**Alternatives Considered**:
- Register at managed level (rejected: can't access `c.client` directly, violates encapsulation)
- Direct runtime call from core (rejected: core shouldn't know about runtime, breaks layering)

---

### RQ-004: Debouncing Strategy

**Question**: How should we handle rapid successive notifications?

**Research Findings**:

The existing codebase has a deduplication mechanism for tool discovery:

```go
// In runtime/lifecycle.go
if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
    r.logger.Debug("Tool discovery already in progress for server, skipping duplicate")
    return
}
defer r.discoveryInProgress.Delete(serverName)
```

This pattern:
- Uses `sync.Map` for lock-free concurrent access
- Prevents duplicate discovery operations
- Is already battle-tested in the codebase

**Decision**: Extend the existing `discoveryInProgress` mechanism. When a notification arrives while discovery is in progress, the notification is effectively ignored since the in-progress discovery will fetch the latest state.

**Rationale**: Simple, reuses existing infrastructure, no need for time-based debouncing since in-progress check is sufficient.

**Alternatives Considered**:
- Time-based debouncing with timer (rejected: adds complexity, existing check is sufficient)
- Queue-based coalescing (rejected: over-engineering for this use case)
- Atomic counter for pending notifications (rejected: adds state, existing check works)

---

### RQ-005: Error Handling Strategy

**Question**: How should we handle errors during notification-triggered discovery?

**Research Findings**:

Existing error handling in `DiscoverAndIndexToolsForServer`:

```go
// Retry logic with exponential backoff
maxRetries := 3
baseDelay := 500 * time.Millisecond

for attempt := 0; attempt < maxRetries; attempt++ {
    // ... retry with backoff
}

if err != nil {
    return fmt.Errorf("failed to list tools after %d attempts: %w", maxRetries, err)
}
```

The function already has:
- Retry logic with exponential backoff
- Proper error wrapping
- Logging at appropriate levels

**Decision**: No changes needed to discovery function. Notification handler should:
1. Log the notification receipt at INFO level
2. Call existing `DiscoverAndIndexToolsForServer`
3. Log any errors but not crash (fire-and-forget pattern)

**Rationale**: Reuses robust existing implementation. Notification processing should be async and non-blocking.

**Alternatives Considered**:
- Adding notification-specific retry logic (rejected: discovery already has retries)
- Blocking on discovery completion (rejected: could block notification processing)

---

### RQ-006: Testing Approach

**Question**: How do we test notification handling?

**Research Findings**:

Existing testing infrastructure:
1. **Mock MCP servers** in tests
2. **Test OAuth server** in `tests/oauthserver/` with dynamic tool support
3. **E2E test framework** using Playwright

For unit testing notifications:
- Can mock the `OnNotification` callback registration
- Can simulate notification dispatch

For integration testing:
- Need MCP server that sends `tools/list_changed` notifications
- The OAuth test server could be extended OR a simple test server created

**Decision**:
1. **Unit tests**: Test notification handler registration, filtering, and callback
2. **Integration tests**: Create minimal test MCP server that supports notifications
3. **E2E tests**: Verify full flow from notification to index update

**Rationale**: Layered testing approach ensures coverage at all levels.

**Alternatives Considered**:
- Only unit tests (rejected: need to verify actual notification flow)
- Only E2E tests (rejected: too slow, harder to debug)

---

## Summary of Decisions

| Topic | Decision |
|-------|----------|
| Notification API | Use `client.OnNotification()` to register handler |
| Capability Check | Check `ServerCapabilities.Tools.ListChanged` after init |
| Integration Point | Register in core, forward via callback to managed, trigger runtime |
| Debouncing | Reuse existing `discoveryInProgress` sync.Map |
| Error Handling | Fire-and-forget with logging, rely on existing retry logic |
| Testing | Unit + Integration + E2E layered approach |

## Unresolved Questions

None - all technical questions have been resolved.

## References

- mcp-go v0.43.1: `github.com/mark3labs/mcp-go`
- MCP Spec: https://modelcontextprotocol.io/specification/2025-06-18/server/tools#list-changed-notification
- Existing code: `internal/upstream/core/connection.go`, `internal/runtime/lifecycle.go`
