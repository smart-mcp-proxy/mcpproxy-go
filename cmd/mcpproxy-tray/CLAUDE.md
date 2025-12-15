# Tray Application

## Architecture

State machine in `internal/state/` manages core server lifecycle.

## State Machine States

```
StateInitializing → StateLaunchingCore → StateWaitingForCore → StateConnectingAPI → StateConnected
```

**Error states**: `StateCoreErrorPortConflict`, `StateCoreErrorDBLocked`, `StateCoreErrorGeneral`, `StateCoreErrorConfig`

**Recovery states**: `StateReconnecting`, `StateFailed`, `StateShuttingDown`

## Key Components

| Directory | Purpose |
|-----------|---------|
| `main.go` | Core process launcher |
| `internal/state/` | State machine |
| `internal/monitor/` | Process + health monitoring |
| `internal/api/` | API client with exponential backoff |

## Exit Code Mapping (`internal/monitor/process.go`)

Core exit codes → state machine events:
- Exit 2 (port conflict) → `EventPortConflict`
- Exit 3 (DB locked) → `EventDBLocked`
- Exit 4 (config error) → `EventConfigError`
- Exit 5 (permission) → `EventPermissionError`

## Automatic Retry Logic

| Error State | Retries | Delay |
|-------------|---------|-------|
| General | 2 | 3s |
| Port conflict | 2 | 10s |
| DB locked | 3 | 5s |

After max retries → `StateFailed`

## Tray-Core Coordination

1. Auto-generates API key if not set
2. Passes `MCPPROXY_API_KEY` to core process
3. Connects via SSE for real-time updates

## Development Environment Variables

```bash
MCPPROXY_TRAY_SKIP_CORE=1           # Skip core launch
MCPPROXY_CORE_URL=http://localhost:8085  # Custom core URL
MCPPROXY_TRAY_PORT=8090             # Custom tray port
```

## Building

```bash
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray
```
