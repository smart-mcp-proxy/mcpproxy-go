# Contracts: Tool Change Notifications

**Feature**: 013-tool-change-notifications
**Date**: 2025-12-20

## Overview

This feature does not introduce new API contracts. It implements internal notification handling that enhances existing behavior.

## MCP Protocol

The feature uses the existing MCP notification protocol:

### Notification Message (Incoming)

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/tools/list_changed",
  "params": {}
}
```

### Server Capabilities (Check)

```json
{
  "capabilities": {
    "tools": {
      "listChanged": true
    }
  }
}
```

## Internal Contracts

### Callback Type: OnToolsChanged

```go
// Signature for tools changed notification callback
type OnToolsChangedCallback func(serverName string)
```

### Callback Type: ToolDiscoveryCallback

```go
// Signature for triggering tool discovery
type ToolDiscoveryCallback func(ctx context.Context, serverName string) error
```

## No REST API Changes

No new endpoints are added. The feature is purely internal to the upstream client subsystem.

## No OpenAPI Changes

The existing `/api/v1/servers` endpoint already returns tool information. When notifications trigger re-indexing, the existing endpoint will automatically return updated data.
