# Terminal UI Research Report

## CLI Structure & Cobra Command Organization

**Location:** `cmd/mcpproxy/main.go`

**Pattern:**
- Root command with subcommands added via `rootCmd.AddCommand()`
- Entry point: `GetXCommand()` functions that return `*cobra.Command`
- Existing commands: `serve`, `upstream`, `activity`, `tools`, `auth`, `code`, `secrets`, `doctor`, `search-servers`, `trust-cert`

**TUI Integration Point:**
- Create `cmd/mcpproxy/tui_cmd.go` with a `GetTUICommand()` function
- Register via `rootCmd.AddCommand(GetTUICommand())`

## REST API Client

**Location:** `internal/cliclient/client.go`

**Key Methods:**
- `GetServers(ctx)` - `GET /api/v1/servers` -> `[]map[string]interface{}`
- `ServerAction(ctx, name, action)` - `POST /api/v1/servers/{name}/{action}`
- `GetServerLogs(ctx, name, tail)` - `GET /api/v1/servers/{name}/logs`
- `ListActivities(ctx, filter)` - `GET /api/v1/activity`
- `GetActivitySummary(ctx, period, groupBy)` - `GET /api/v1/activity/summary`
- `TriggerOAuthLogin(ctx, name)` - `POST /api/v1/servers/{name}/login`
- `TriggerOAuthLogout(ctx, name)` - `POST /api/v1/servers/{name}/logout`
- `Ping(ctx)` - `GET /api/v1/status`
- `GetDiagnostics(ctx)` - `GET /api/v1/diagnostics`
- `GetInfo(ctx)` - `GET /api/v1/info`

**Connection:** Supports Unix sockets, named pipes, and TCP.

## Key Data Models (`internal/contracts/types.go`)

### Server
```go
type Server struct {
    Name, Status, LastError, OAuthStatus string
    Enabled, Quarantined, Connected bool
    ToolCount int
    TokenExpiresAt *time.Time
    Health *HealthStatus
}
```

### HealthStatus
```go
type HealthStatus struct {
    Level      string  // "healthy" | "degraded" | "unhealthy"
    AdminState string  // "enabled" | "disabled" | "quarantined"
    Summary    string  // e.g. "Connected (5 tools)"
    Detail     string
    Action     string  // "login" | "restart" | "enable" | "approve" | ""
}
```

### SSE Events (`internal/runtime/events.go`)
- `EventTypeServersChanged` - Server state change
- `EventTypeConfigReloaded` - Config file reloaded
- `EventTypeOAuthTokenRefreshed` - Token refresh success
- `EventTypeOAuthRefreshFailed` - Token refresh failure
- `EventTypeActivityToolCallStarted/Completed` - Activity

## Socket Detection

```go
socketPath := socket.DetectSocketPath(dataDir)
isAvailable := socket.IsSocketAvailable(socketPath)
```

## Config Loading

```go
cfg, _ := config.LoadFromFile("")
cfg, _ := config.Load()
```
