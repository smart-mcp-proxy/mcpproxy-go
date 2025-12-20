# Data Model: Tool Change Notifications

**Feature**: 013-tool-change-notifications
**Date**: 2025-12-20
**Status**: Complete

## Overview

This feature primarily extends existing structures rather than introducing new entities. The main additions are callback types and a capability flag check.

## Existing Entities (Extended)

### Core Client (`internal/upstream/core/client.go`)

**Current Structure**:
```go
type Client struct {
    // ... existing fields
    client     *client.Client
    serverInfo *mcp.InitializeResult
}
```

**Extension**:
```go
type Client struct {
    // ... existing fields

    // Callback for tool change notifications
    onToolsChanged func(serverName string)
}
```

**New Method**:
```go
// SetOnToolsChangedCallback sets the callback invoked when tools/list_changed notification is received
func (c *Client) SetOnToolsChangedCallback(callback func(serverName string))
```

---

### Managed Client (`internal/upstream/managed/client.go`)

**Current Structure**:
```go
type Client struct {
    core            *core.Client
    stateNotifier   func(state string, err error)
    // ... existing fields
}
```

**Extension**:
```go
type Client struct {
    // ... existing fields

    // Reference to runtime for triggering tool discovery
    toolDiscoveryCallback func(ctx context.Context, serverName string) error
}
```

**New Method**:
```go
// SetToolDiscoveryCallback sets the callback for triggering tool re-indexing
func (c *Client) SetToolDiscoveryCallback(callback func(ctx context.Context, serverName string) error)
```

---

### Server Capabilities (mcp-go library, read-only)

**Structure** (from `mcp/types.go`):
```go
type ServerCapabilities struct {
    Tools *struct {
        ListChanged bool `json:"listChanged,omitempty"`
    } `json:"tools,omitempty"`
}
```

**Usage**:
```go
// Check if server supports notifications
caps := c.serverInfo.Capabilities
supportsNotifications := caps.Tools != nil && caps.Tools.ListChanged
```

---

## New Entities

### None Required

All functionality is implemented via:
1. Callbacks on existing structs
2. Reading existing capability structures
3. Reusing existing `discoveryInProgress` sync.Map

---

## State Transitions

### Notification Processing State

```
[Notification Received]
        |
        v
[Check: Discovery in progress?] --Yes--> [Log: Skip duplicate] --> [Done]
        |
        No
        v
[Mark: Discovery in progress]
        |
        v
[Call: DiscoverAndIndexToolsForServer]
        |
        v
[Mark: Discovery complete]
        |
        v
[Done]
```

---

## Validation Rules

### FR-001: Notification Handler Registration
- Handler MUST be registered after `client.Start()` succeeds
- Handler MUST be registered before any tool operations

### FR-002: Capability Check
- Notification handling SHOULD only be enabled for servers with `capabilities.tools.listChanged: true`
- Warning SHOULD be logged if notification received from server without capability

### FR-005: Deduplication
- `discoveryInProgress` MUST be checked before starting discovery
- Entry MUST be added before discovery, removed after (defer pattern)

---

## Relationships

```
┌─────────────────────────────────────────────────────────────────┐
│                           Runtime                                │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │            DiscoverAndIndexToolsForServer()              │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              ▲                                   │
│                              │ callback                          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Managed Client                        │    │
│  │   ┌───────────────────────────────────────────────┐     │    │
│  │   │     toolDiscoveryCallback (set by Manager)     │     │    │
│  │   └───────────────────────────────────────────────┘     │    │
│  │                          ▲                               │    │
│  │                          │ forward                       │    │
│  │   ┌───────────────────────────────────────────────┐     │    │
│  │   │  Core Client: onToolsChanged callback          │     │    │
│  │   └───────────────────────────────────────────────┘     │    │
│  │                          ▲                               │    │
│  │                          │ invoke                        │    │
│  │   ┌───────────────────────────────────────────────┐     │    │
│  │   │  OnNotification handler (filters for tools)    │     │    │
│  │   └───────────────────────────────────────────────┘     │    │
│  │                          ▲                               │    │
│  │                          │ receives                      │    │
│  │   ┌───────────────────────────────────────────────┐     │    │
│  │   │         mcp-go Client (notifications)          │     │    │
│  │   └───────────────────────────────────────────────┘     │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

---

## API Surface Changes

### No New REST API Endpoints

The feature is internal to the upstream client layer. No REST API changes required.

### No New MCP Tools

The feature enhances existing behavior of tool discovery. No new MCP tools exposed.

### Logging Enhancements

New log entries at various levels:
- **INFO**: "Received tools/list_changed notification from server: {name}"
- **DEBUG**: "Server supports tool change notifications: {name}"
- **DEBUG**: "Skipping duplicate discovery - already in progress for: {name}"
- **WARN**: "Received tools notification from server without listChanged capability: {name}"
