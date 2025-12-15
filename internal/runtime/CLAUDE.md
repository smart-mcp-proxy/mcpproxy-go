# Runtime

## Purpose

Core non-HTTP lifecycle management. Separates concerns from HTTP server layer.

## Key Files

| File | Purpose |
|------|---------|
| `runtime.go` | Lifecycle, configuration, state management |
| `event_bus.go` | Event system for real-time updates |
| `lifecycle.go` | Background init, connections, indexing |
| `events.go` | Event type definitions |

## Responsibilities

- **Configuration Management**: Loading, validation, hot-reload
- **Background Services**: Connections, indexing, health monitoring
- **State Management**: Thread-safe status tracking
- **Event System**: Real-time broadcasting for UI/SSE

## Event Bus

Event types:
- `servers.changed` - Server config/state changes
- `config.reloaded` - Config file reloaded from disk

Event flow:
1. Runtime operations trigger events
2. Events broadcast via buffered channels
3. Server forwards to tray UI and SSE endpoints
4. Web UI receives via `/events` endpoint

## Lifecycle

**Initialization**:
1. Runtime created with config, logger, manager
2. Background init starts connections and indexing
3. Status updates broadcast through events

**Background Services**:
- Connection management with exponential backoff
- Tool indexing every 15 minutes
- Config sync on file changes

**Shutdown**:
- Graceful context cancellation
- Upstream servers disconnected
- Docker containers cleaned up
- Resources closed in order

## Thread Safety

Uses channels for state updates (actor pattern):
- One goroutine owns each resource
- Communicate via channels, not shared memory
- Context propagation for cancellation
