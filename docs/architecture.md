# MCPProxy Architecture

This document describes the internal architecture of MCPProxy, including the management service, runtime lifecycle, and event system.

## Management Service Architecture

The management service (`internal/management/`) provides a centralized business logic layer for upstream server management operations, eliminating code duplication across CLI, REST API, and MCP interfaces.

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    Client Interfaces                         │
├───────────────┬─────────────────┬───────────────────────────┤
│  CLI Commands │   REST API      │   MCP Protocol            │
│  (upstream)   │   (/api/v1/*)   │   (upstream_servers tool) │
└───────┬───────┴────────┬────────┴───────────┬───────────────┘
        │                │                    │
        └────────────────┼────────────────────┘
                         │
                         ▼
              ┌─────────────────────┐
              │  Management Service │
              │  (internal/mgmt/)   │
              └──────────┬──────────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
  ┌──────────┐    ┌──────────┐    ┌──────────┐
  │ Runtime  │    │  Config  │    │  Events  │
  │Operations│    │  Gates   │    │  Emitter │
  └──────────┘    └──────────┘    └──────────┘
```

### Key Components

- **Service Interface** (`service.go:16-102`): Defines all management operations
  - Single-server: `RestartServer()`, `EnableServer()`, `DisableServer()`
  - Bulk operations: `RestartAll()`, `EnableAll()`, `DisableAll()`
  - Diagnostics: `GetServerHealth()`, `RunDiagnostics()`
  - Server CRUD: `AddServer()`, `RemoveServer()`, `QuarantineServer()`
  - Tool operations: `GetServerTools()`, `TriggerOAuthLogin()`

- **Configuration Gates**: All operations respect centralized configuration guards
  - `disable_management`: Blocks all write operations when true
  - `read_only_mode`: Blocks all configuration modifications

- **Bulk Operations** (`service.go:243-388`): Efficient multi-server management
  - Sequential execution with partial failure handling
  - Returns `BulkOperationResult` with success/failure counts
  - Collects per-server errors in results map
  - Continues on individual failures, reports aggregate results

- **Event Integration**: All operations emit events through event bus
  - `servers.changed`: Notifies UI of server state changes
  - Triggers SSE updates to web UI and tray application
  - Enables real-time synchronization across interfaces

### Benefits

- **Code Deduplication**: 40%+ reduction in duplicate code across interfaces
- **Consistent Behavior**: All interfaces use identical business logic
- **Centralized Validation**: Configuration gates enforced in one place
- **Easier Testing**: Unit tests cover all interfaces through service layer
- **Future Extensibility**: New interfaces can reuse existing service methods

### Usage Examples

```go
// CLI usage (cmd/mcpproxy/upstream_cmd.go:547-636)
result, err := client.RestartAll(ctx)
fmt.Printf("  Total servers:      %d\n", result.Total)
fmt.Printf("  ✅ Successful:      %d\n", result.Successful)
fmt.Printf("  ❌ Failed:          %d\n", result.Failed)

// REST API usage (internal/httpapi/server.go:772-866)
mgmtSvc := s.controller.GetManagementService().(ManagementService)
result, err := mgmtSvc.RestartAll(r.Context())
s.writeSuccess(w, result)

// MCP protocol usage (future integration)
result, err := mgmtService.RestartAll(ctx)
return mcpResponse(result)
```

## Runtime Architecture

The runtime package (`internal/runtime/`) provides core non-HTTP lifecycle management, separating concerns from the HTTP server layer.

### Runtime Package Components

- **Configuration Management**: Centralized config loading, validation, and hot-reload
- **Background Services**: Connection management, tool indexing, and health monitoring
- **State Management**: Thread-safe status tracking and upstream server state
- **Event System**: Real-time event broadcasting for UI and SSE consumers

### Event Bus System

The event bus enables real-time communication between runtime and UI components:

**Event Types**:
- `servers.changed` - Server configuration or state changes
- `config.reloaded` - Configuration file reloaded from disk

**Event Flow**:
1. Runtime operations trigger events via `emitServersChanged()` and `emitConfigReloaded()`
2. Events are broadcast to subscribers through buffered channels
3. Server forwards events to tray UI and SSE endpoints
4. Tray menus refresh automatically without file watching
5. Web UI receives live updates via `/events` SSE endpoint

**SSE Integration**:
- `/events` endpoint streams both status updates and runtime events
- Automatic connection management with proper cleanup
- JSON-formatted event payloads for easy consumption

### Runtime Lifecycle

**Initialization**:
1. Runtime created with config, logger, and manager dependencies
2. Background initialization starts server connections and tool indexing
3. Status updates broadcast through event system

**Background Services**:
- **Connection Management**: Periodic reconnection attempts with exponential backoff
- **Tool Indexing**: Automatic discovery and search index updates every 15 minutes
- **Configuration Sync**: File-based config changes trigger runtime resync

**Shutdown**:
- Graceful context cancellation cascades to all background services
- Upstream servers disconnected with proper Docker container cleanup
- Resources closed in dependency order (upstream → cache → index → storage)

## Tray Application Architecture

The tray application uses a robust state machine architecture for reliable core management.

### State Machine States

- Normal flow: `StateInitializing` → `StateLaunchingCore` → `StateWaitingForCore` → `StateConnectingAPI` → `StateConnected`
- Error states: `StateCoreErrorPortConflict`, `StateCoreErrorDBLocked`, `StateCoreErrorGeneral`, `StateCoreErrorConfig`
- Recovery states: `StateReconnecting`, `StateFailed`, `StateShuttingDown`

### Key Components

- **Process Monitor** (`cmd/mcpproxy-tray/internal/monitor/process.go`): Monitors core subprocess lifecycle
- **Health Monitor** (`cmd/mcpproxy-tray/internal/monitor/health.go`): Performs socket-aware HTTP health checks on core API (`/healthz`, `/readyz`)
- **State Machine** (`cmd/mcpproxy-tray/internal/state/machine.go`): Manages state transitions and automatic retry logic

### Error Classification

Core process exit codes are mapped to specific state machine events:
- Exit code 2 (port conflict) → `EventPortConflict`
- Exit code 3 (database locked) → `EventDBLocked`
- Exit code 4 (config error) → `EventConfigError`
- Exit code 5 (permission error) → `EventPermissionError`
- Other errors → `EventGeneralError`

### Automatic Retry Logic

Error states automatically retry core launch with exponential backoff:
- `StateCoreErrorGeneral`: 2 retries with 3s delay (3 total attempts)
- `StateCoreErrorPortConflict`: 2 retries with 10s delay
- `StateCoreErrorDBLocked`: 3 retries with 5s delay
- After max retries exceeded → transitions to `StateFailed`

### Development Environment Variables

- `MCPPROXY_TRAY_SKIP_CORE=1` - Skip core launch (for development)
- `MCPPROXY_CORE_URL=http://localhost:8085` - Custom core URL
- `MCPPROXY_TRAY_PORT=8090` - Custom tray port

## Related Documentation

- [Socket Communication](socket-communication.md) - Tray-core IPC details
- [CLI Management Commands](cli-management-commands.md) - CLI reference
- [Configuration](configuration.md) - Full configuration reference
