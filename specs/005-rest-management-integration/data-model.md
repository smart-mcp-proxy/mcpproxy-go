# Data Model: REST Endpoint Management Service Integration

**Feature**: 005-rest-management-integration
**Created**: 2025-11-27
**Related**: [spec.md](./spec.md) | [plan.md](./plan.md)

## Overview

This feature extends the existing ManagementService interface with two new methods to support server tool retrieval and OAuth authentication. The data model describes the interface extension and the data flow between components.

## Entity: ManagementService (Interface Extension)

**Purpose**: Provide unified server lifecycle and diagnostic operations for CLI, REST, and MCP interfaces

**Existing Methods** (from spec 004):
- `ListServers(ctx) ([]*contracts.Server, *contracts.ServerStats, error)`
- `GetServerLogs(ctx, name, tail) ([]contracts.LogEntry, error)`
- `EnableServer(ctx, name, enabled) error`
- `RestartServer(ctx, name) error`
- `RestartAll(ctx) (*BulkOperationResult, error)`
- `EnableAll(ctx) (*BulkOperationResult, error)`
- `DisableAll(ctx) (*BulkOperationResult, error)`
- `Doctor(ctx) (*contracts.Diagnostics, error)`
- `AuthStatus(ctx, name) (*contracts.AuthStatus, error)`

**New Methods** (this feature):

### GetServerTools

```go
// GetServerTools retrieves all available tools for a specific upstream MCP server.
// This method delegates to the runtime's GetServerTools() which reads from the
// StateView cache (lock-free, in-memory read).
//
// Parameters:
//   - ctx: Request context for cancellation and timeouts
//   - name: Server identifier (must match server name in configuration)
//
// Returns:
//   - []map[string]interface{}: Array of tool metadata objects
//   - error: Non-nil if server not found or retrieval fails
//
// Behavior:
//   - Returns error if server name is empty
//   - Returns error if server not found in configuration
//   - Returns error if server not connected
//   - Returns empty array if server has no tools
//
// Performance:
//   - Completes in <10ms (in-memory cache read)
//   - No blocking I/O operations
//
GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)
```

**Tool Metadata Structure** (existing format, returned by StateView):
```go
map[string]interface{}{
    "name":        string,  // Tool name (e.g., "github:create_issue")
    "description": string,  // Human-readable description
    "server_name": string,  // Originating server name
    "usage":       int,     // Call count (if available)
    "inputSchema": object,  // JSON Schema for parameters (if available)
}
```

### TriggerOAuthLogin

```go
// TriggerOAuthLogin initiates an OAuth 2.x authentication flow for a specific server.
// This method delegates to the upstream manager's StartManualOAuth() which launches
// a browser-based OAuth flow.
//
// Parameters:
//   - ctx: Request context for cancellation and timeouts
//   - name: Server identifier (must match server name in configuration)
//
// Returns:
//   - error: Non-nil if operation is blocked by config gates or OAuth fails to start
//
// Configuration Gates:
//   - Blocked if disable_management=true (returns HTTP 403 via REST)
//   - Blocked if read_only=true (returns HTTP 403 via REST)
//
// Behavior:
//   - Returns error if server name is empty
//   - Returns error if server not found in configuration
//   - Returns error if server doesn't support OAuth
//   - Launches browser for user authentication
//   - Emits "servers.changed" event on successful OAuth completion
//
// Side Effects:
//   - Opens default browser with OAuth authorization URL
//   - Starts local callback server to receive OAuth code
//   - Updates server authentication state in runtime
//   - Emits event to notify UI of state change
//
// Performance:
//   - Method returns immediately after starting OAuth flow
//   - Actual OAuth completion is asynchronous
//
TriggerOAuthLogin(ctx context.Context, name string) error
```

## Data Flow Diagrams

### GetServerTools Flow

```
REST API Request
    ↓
handleGetServerTools (internal/httpapi/server.go:1155)
    ↓
controller.GetManagementService()
    ↓
ManagementService.GetServerTools(ctx, serverID)
    ↓
runtime.GetServerTools(serverID)
    ↓
supervisor.StateView().Snapshot().Servers[serverID].Tools
    ↓
Return []map[string]interface{} (tool metadata)
    ↓
REST API Response (JSON)
```

### TriggerOAuthLogin Flow

```
REST API Request
    ↓
handleServerLogin (internal/httpapi/server.go:1050)
    ↓
controller.GetManagementService()
    ↓
ManagementService.TriggerOAuthLogin(ctx, serverID)
    ↓
Check config gates (disable_management, read_only)
    ↓
upstreamManager.StartManualOAuth(serverID, inProcess=true)
    ↓
Launch browser with OAuth URL
    ↓
[User authenticates in browser]
    ↓
Callback server receives OAuth code
    ↓
Exchange code for access token
    ↓
Update server authentication state
    ↓
EventEmitter.EmitServersChanged("oauth_completed", {...})
    ↓
SSE /events → Tray UI update
    ↓
REST API Response (success)
```

## Configuration Gates

**Enforcement Location**: ManagementService implementation (centralized)

**Gates**:
1. **disable_management**: Blocks all write operations including `TriggerOAuthLogin()`
2. **read_only**: Blocks configuration changes (not applicable to these methods, but checked for consistency)

**Error Handling**:
- Gate violation returns `fmt.Errorf("operation blocked: management disabled")`
- REST handlers map this to HTTP 403 Forbidden
- CLI commands display user-friendly error messages

## Event Emissions

**Event Type**: `servers.changed`

**Emitted By**: `TriggerOAuthLogin()` on successful OAuth completion

**Payload**:
```go
{
    "reason": "oauth_completed",
    "server_name": string,  // Server that completed OAuth
    "timestamp": string,    // ISO 8601 timestamp
}
```

**Consumers**:
- SSE endpoint `/events` (broadcasts to all connected clients)
- Tray application (refreshes server menu state)
- Web UI (updates server authentication badges)

## Error Handling

### GetServerTools Errors

| Error Condition | Error Message | HTTP Status | CLI Display |
|----------------|---------------|-------------|-------------|
| Empty server name | "server name required" | 400 | "Error: Server name required" |
| Server not found | "server not found: {name}" | 404 | "Error: Server '{name}' not found" |
| Server not connected | "server not connected: {name}" | 500 | "Error: Server '{name}' is not connected" |
| Runtime error | "failed to get tools: {err}" | 500 | "Error: Failed to retrieve tools: {err}" |

### TriggerOAuthLogin Errors

| Error Condition | Error Message | HTTP Status | CLI Display |
|----------------|---------------|-------------|-------------|
| Empty server name | "server name required" | 400 | "Error: Server name required" |
| Management disabled | "operation blocked: management disabled" | 403 | "Error: Management operations are disabled" |
| Read-only mode | "operation blocked: read-only mode" | 403 | "Error: System is in read-only mode" |
| Server not found | "server not found: {name}" | 404 | "Error: Server '{name}' not found" |
| No OAuth config | "server does not support OAuth: {name}" | 400 | "Error: Server '{name}' does not support OAuth" |
| OAuth start failed | "failed to start OAuth: {err}" | 500 | "Error: Failed to start OAuth flow: {err}" |

## Validation Rules

### Input Validation

**Server Name**:
- MUST NOT be empty string
- MUST match an existing server in configuration
- SHOULD match pattern `^[a-zA-Z0-9_-]+$` (enforced by config loader)

**Context**:
- MUST NOT be nil
- SHOULD have reasonable timeout (recommended: 30s for GetServerTools, 5m for TriggerOAuthLogin)

### State Validation

**GetServerTools**:
- Server MUST be in "connected" state
- Server MUST have completed initialization
- StateView MUST contain server entry

**TriggerOAuthLogin**:
- Server MUST have OAuth configuration in mcp_config.json
- No other OAuth flow MUST be active for the same server (enforced by upstream manager)

## Performance Characteristics

**GetServerTools**:
- Latency: <10ms (in-memory read from StateView cache)
- No blocking I/O
- No database queries
- Lock-free operation (uses atomic snapshots)

**TriggerOAuthLogin**:
- Latency: <50ms to start OAuth flow (launches goroutine)
- Asynchronous browser launch
- Callback server starts in background
- Method returns before OAuth completes

## Backward Compatibility

**Guarantees**:
- Tool metadata format unchanged (existing `map[string]interface{}` structure)
- REST API response contracts unchanged
- HTTP status codes unchanged
- Event payload structure consistent with existing events

**Migration**:
- No migration required (interface extension only)
- Existing code continues to work
- New methods available immediately after deployment

## Related Entities

**StateView** (existing, from Phase 7.1 refactoring):
- Provides lock-free cached reads of server state
- Contains tool lists, connection status, error messages
- Updated by supervisor actor, read by management service

**UpstreamManager** (existing):
- Manages server lifecycle (connect, disconnect, restart)
- Handles OAuth flows via `StartManualOAuth()`
- Emits connection state changes

**EventBus** (existing):
- Broadcasts `servers.changed` events to subscribers
- Supports multiple concurrent subscribers
- Non-blocking event delivery

## Testing Considerations

**Unit Tests**:
- Mock runtime to return tool lists, test GetServerTools() validation
- Mock upstream manager to simulate OAuth success/failure
- Verify event emissions with mock EventEmitter
- Test config gate enforcement with different config states

**Integration Tests**:
- Start real server with OAuth config, trigger OAuth flow
- Verify browser launch and callback server creation
- Verify event propagation through event bus to SSE endpoint

**E2E Tests**:
- Existing `./scripts/test-api-e2e.sh` should pass without modification
- CLI commands from PR #152 should work correctly
- Tray menu actions should trigger proper management service calls
