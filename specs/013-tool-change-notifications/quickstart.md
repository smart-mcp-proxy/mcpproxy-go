# Quickstart: Tool Change Notifications

**Feature**: 013-tool-change-notifications
**Date**: 2025-12-20

## Overview

This guide helps developers quickly understand and implement the tool change notification subscription feature.

## What This Feature Does

When an upstream MCP server updates its available tools (add, remove, or modify), MCPProxy now automatically detects the change via the `notifications/tools/list_changed` notification and re-indexes the tools immediately - no more waiting up to 5 minutes for the background polling cycle.

## Prerequisites

- Go 1.24+
- mcp-go v0.43.1 (already in go.mod)
- Familiarity with `internal/upstream/` architecture

## Key Files to Modify

1. **`internal/upstream/core/client.go`**
   - Add `onToolsChanged` callback field
   - Add `SetOnToolsChangedCallback()` method

2. **`internal/upstream/core/connection.go`**
   - Register `OnNotification` handler after `client.Start()`
   - Filter for `mcp.MethodNotificationToolsListChanged`
   - Invoke callback when notification received

3. **`internal/upstream/managed/client.go`**
   - Add `toolDiscoveryCallback` field
   - Wire up core's `onToolsChanged` to trigger discovery
   - Set callback during client construction

4. **`internal/upstream/manager.go`**
   - Set `toolDiscoveryCallback` on managed clients
   - Callback should call `runtime.DiscoverAndIndexToolsForServer()`

## Implementation Steps

### Step 1: Core Client Callback

```go
// internal/upstream/core/client.go

type Client struct {
    // ... existing fields
    onToolsChanged func(serverName string)
}

func (c *Client) SetOnToolsChangedCallback(callback func(serverName string)) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.onToolsChanged = callback
}
```

### Step 2: Notification Handler Registration

```go
// internal/upstream/core/connection.go
// Add after client.Start() succeeds in connectStdio, connectHTTP, connectSSE

c.client.OnNotification(func(notification mcp.JSONRPCNotification) {
    if notification.Notification.Method == mcp.MethodNotificationToolsListChanged {
        c.logger.Info("Received tools/list_changed notification",
            zap.String("server", c.config.Name))

        c.mu.RLock()
        callback := c.onToolsChanged
        c.mu.RUnlock()

        if callback != nil {
            callback(c.config.Name)
        }
    }
})
```

### Step 3: Managed Client Wiring

```go
// internal/upstream/managed/client.go

type Client struct {
    // ... existing fields
    toolDiscoveryCallback func(ctx context.Context, serverName string) error
}

func NewClient(/* params */) *Client {
    mc := &Client{/* ... */}

    // Wire up core notification callback
    mc.core.SetOnToolsChangedCallback(func(serverName string) {
        if mc.toolDiscoveryCallback != nil {
            ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            defer cancel()

            if err := mc.toolDiscoveryCallback(ctx, serverName); err != nil {
                mc.logger.Error("Tool discovery triggered by notification failed",
                    zap.String("server", serverName),
                    zap.Error(err))
            }
        }
    })

    return mc
}

func (c *Client) SetToolDiscoveryCallback(callback func(ctx context.Context, serverName string) error) {
    c.toolDiscoveryCallback = callback
}
```

### Step 4: Manager Integration

```go
// internal/upstream/manager.go

func (m *Manager) AddServer(name string, config *config.ServerConfig) error {
    // ... existing code

    client, err := managed.NewClient(/* params */)
    if err != nil {
        return err
    }

    // Set discovery callback that uses runtime
    client.SetToolDiscoveryCallback(func(ctx context.Context, serverName string) error {
        return m.runtime.DiscoverAndIndexToolsForServer(ctx, serverName)
    })

    // ... rest of existing code
}
```

## Testing

### Unit Test Example

```go
func TestNotificationHandler(t *testing.T) {
    called := make(chan string, 1)

    client := &core.Client{}
    client.SetOnToolsChangedCallback(func(name string) {
        called <- name
    })

    // Simulate notification
    client.handleNotification(mcp.JSONRPCNotification{
        Notification: mcp.Notification{
            Method: mcp.MethodNotificationToolsListChanged,
        },
    })

    select {
    case name := <-called:
        assert.Equal(t, expectedServerName, name)
    case <-time.After(time.Second):
        t.Fatal("callback not invoked")
    }
}
```

### Integration Test

Run with a test MCP server that supports notifications:

```bash
# Start test server with notification support
go run ./tests/notification-server/

# Run mcpproxy
./mcpproxy serve

# Trigger tool change on test server
curl -X POST http://localhost:9999/add-tool

# Verify logs show notification received and tools re-indexed
```

## Verification

After implementation, verify:

1. **Capability Check**: Logs show "Server supports tool change notifications" for capable servers
2. **Notification Receipt**: Logs show "Received tools/list_changed notification" when server updates tools
3. **Re-indexing**: Tools are updated within 5 seconds of notification (vs 5 minutes polling)
4. **Backward Compatibility**: Servers without notifications still work via 5-minute polling

## Common Issues

### Notification Not Received
- Check server capabilities: `caps.Tools.ListChanged` should be `true`
- Verify `OnNotification` handler is registered after `client.Start()`

### Duplicate Discoveries
- Existing `discoveryInProgress` sync.Map prevents duplicates
- Check logs for "already in progress" messages

### Callback Not Invoked
- Ensure `SetOnToolsChangedCallback` is called before connection
- Check that callback is not nil at invocation time
