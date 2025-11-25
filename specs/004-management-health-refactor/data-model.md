# Data Model: Management Service & Diagnostics

**Feature**: 004-management-health-refactor
**Date**: 2025-11-23

## Overview

This document defines the data entities for the management service layer and health diagnostics system.

## Core Entities

### 1. ManagementService Interface

**Purpose**: Unified interface for all server lifecycle and diagnostic operations

**Methods**:
```go
type Service interface {
    // Server Lifecycle
    ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
    GetServerLogs(ctx context.Context, name string, tail int) ([]contracts.LogEntry, error)
    EnableServer(ctx context.Context, name string, enabled bool) error
    RestartServer(ctx context.Context, name string) error

    // Bulk Operations
    RestartAll(ctx context.Context) (int, error)
    EnableAll(ctx context.Context) (int, error)
    DisableAll(ctx context.Context) (int, error)

    // Diagnostics
    Doctor(ctx context.Context) (*contracts.Diagnostics, error)
    AuthStatus(ctx context.Context, name string) (*contracts.AuthStatus, error)
}
```

**Dependencies**:
- `*server.Manager`: Upstream server connection management
- `*config.Config`: Configuration gates (`disable_management`, `read_only`)
- `*runtime.EventBus`: Event emissions for state changes
- `*logs.Reader`: Server log retrieval
- `*secret.Resolver`: Secret existence checking
- `*zap.SugaredLogger`: Structured logging

**Lifecycle**: Singleton created by runtime, injected into HTTP/CLI/MCP handlers

---

### 2. Diagnostics (NEW)

**Purpose**: Aggregated health information from all system components

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `TotalIssues` | `int` | Count of all detected issues | >= 0 |
| `UpstreamErrors` | `[]UpstreamError` | Connection/runtime errors per server | Non-nil slice |
| `OAuthRequired` | `[]OAuthRequirement` | Servers needing authentication | Non-nil slice |
| `MissingSecrets` | `[]MissingSecret` | Referenced but undefined secrets | Non-nil slice |
| `RuntimeWarnings` | `[]string` | General warnings (deprecated config, etc.) | Non-nil slice |
| `DockerStatus` | `*DockerStatus` | Docker daemon status (if isolation enabled) | Nullable |
| `Timestamp` | `time.Time` | When diagnostics were collected | Valid timestamp |

**State Transitions**: None (immutable snapshot)

**Relationships**:
- Contains: UpstreamError (1:N)
- Contains: OAuthRequirement (1:N)
- Contains: MissingSecret (1:N)
- Contains: DockerStatus (1:0..1)

**Example JSON**:
```json
{
  "total_issues": 3,
  "upstream_errors": [
    {
      "server_name": "github-server",
      "error_message": "connection refused",
      "timestamp": "2025-11-23T10:30:00Z"
    }
  ],
  "oauth_required": [
    {
      "server_name": "sentry-mcp",
      "state": "unauthenticated",
      "expires_at": null,
      "message": "Run: mcpproxy auth login --server=sentry-mcp"
    }
  ],
  "missing_secrets": [
    {
      "secret_name": "GITHUB_TOKEN",
      "used_by": ["github-server", "gh-issues"]
    }
  ],
  "runtime_warnings": [],
  "docker_status": {
    "available": true,
    "version": "24.0.7"
  },
  "timestamp": "2025-11-23T10:35:12Z"
}
```

---

### 3. UpstreamError (NEW)

**Purpose**: Details of an upstream server connection or runtime error

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `ServerName` | `string` | Unique server identifier | Non-empty, matches server config |
| `ErrorMessage` | `string` | Human-readable error description | Non-empty |
| `Timestamp` | `time.Time` | When error occurred | Valid timestamp |

**Example JSON**:
```json
{
  "server_name": "weather-api",
  "error_message": "OAuth token expired",
  "timestamp": "2025-11-23T09:15:30Z"
}
```

---

### 4. OAuthRequirement (NEW)

**Purpose**: OAuth authentication status for a server

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `ServerName` | `string` | Server requiring auth | Non-empty |
| `State` | `string` | Auth state (unauthenticated/expired/refreshing) | Enum value |
| `ExpiresAt` | `*time.Time` | Token expiration (if authenticated) | Nullable, future timestamp |
| `Message` | `string` | Actionable instruction for user | Non-empty |

**State Values**:
- `unauthenticated`: No token, user must login
- `expired`: Token exists but expired, re-login required
- `refreshing`: Token refresh in progress
- `authenticated`: Valid token (not in diagnostics, filtered out)

**Example JSON**:
```json
{
  "server_name": "linear-mcp",
  "state": "expired",
  "expires_at": "2025-11-22T18:00:00Z",
  "message": "Token expired. Run: mcpproxy auth login --server=linear-mcp"
}
```

---

### 5. MissingSecret (NEW)

**Purpose**: Secret referenced in config but not found in env/keyring

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `SecretName` | `string` | Environment variable or keyring key | Non-empty |
| `UsedBy` | `[]string` | Server names referencing this secret | Non-empty slice |

**Example JSON**:
```json
{
  "secret_name": "ANTHROPIC_API_KEY",
  "used_by": ["claude-server", "opus-server"]
}
```

---

### 6. DockerStatus (NEW)

**Purpose**: Docker daemon availability for stdio server isolation

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `Available` | `bool` | Whether Docker daemon is reachable | - |
| `Version` | `string` | Docker version (if available) | Empty if unavailable |
| `Error` | `string` | Error message if unavailable | Empty if available |

**Example JSON**:
```json
{
  "available": false,
  "version": "",
  "error": "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"
}
```

---

### 7. ServerStats (EXISTING - Enhanced)

**Purpose**: Aggregate statistics for all configured servers

**Location**: `internal/contracts/server.go`

**Fields** (existing):

| Field | Type | Description |
|-------|------|-------------|
| `Total` | `int` | Total configured servers |
| `Enabled` | `int` | Servers with `enabled: true` |
| `Disabled` | `int` | Servers with `enabled: false` |
| `Connected` | `int` | Servers currently connected |
| `Errors` | `int` | Servers with connection errors |
| `Quarantined` | `int` | Servers in quarantine |

**No changes required** - service computes from manager state

---

### 8. AuthStatus (NEW)

**Purpose**: Detailed OAuth authentication status for a single server

**Location**: `internal/contracts/diagnostics.go`

**Fields**:

| Field | Type | Description | Validation |
|-------|------|-------------|------------|
| `ServerName` | `string` | Server identifier | Non-empty |
| `State` | `string` | Auth state (same as OAuthRequirement) | Enum value |
| `ExpiresAt` | `*time.Time` | Token expiration | Nullable |
| `TokenType` | `string` | OAuth2 token type (Bearer, etc.) | Empty if unauthenticated |
| `Scopes` | `[]string` | Granted OAuth scopes | Empty if unauthenticated |
| `Message` | `string` | Actionable guidance | Non-empty |

**Example JSON**:
```json
{
  "server_name": "github-server",
  "state": "authenticated",
  "expires_at": "2025-11-30T10:00:00Z",
  "token_type": "Bearer",
  "scopes": ["repo", "user"],
  "message": "Authenticated. Token expires in 7 days."
}
```

---

## Data Flow Diagrams

### Doctor Diagnostics Flow

```
┌──────────────────┐
│ CLI/REST/MCP     │
│ Request          │
└────────┬─────────┘
         │
         ▼
┌──────────────────────────────────┐
│ ManagementService.Doctor(ctx)    │
│ - Check upstream errors          │
│ - Check OAuth requirements       │
│ - Check missing secrets          │
│ - Check Docker status            │
└────────┬─────────────────────────┘
         │
         ├──────────────────────────────────┐
         │                                  │
         ▼                                  ▼
┌────────────────┐              ┌──────────────────┐
│ server.Manager │              │ secret.Resolver  │
│ GetAllServers()│              │ ListReferences() │
└────────┬───────┘              └────────┬─────────┘
         │                               │
         ▼                               ▼
┌──────────────────────────────────────────────────┐
│ Aggregate into Diagnostics struct                │
│ - TotalIssues = len(errors + oauth + secrets)    │
│ - Timestamp = now                                │
└────────┬─────────────────────────────────────────┘
         │
         ▼
┌──────────────────┐
│ Return           │
│ *Diagnostics     │
└──────────────────┘
```

### Restart Server Flow (with Events)

```
┌──────────────────┐
│ CLI/REST/MCP     │
│ Request          │
└────────┬─────────┘
         │
         ▼
┌────────────────────────────────────┐
│ ManagementService.RestartServer()  │
│ 1. Check disable_management gate   │
│ 2. Check read_only gate             │
└────────┬───────────────────────────┘
         │
         ▼
┌────────────────────────────────────┐
│ server.Manager.RestartServer()     │
│ - Stop upstream connection          │
│ - Start new connection              │
└────────┬───────────────────────────┘
         │
         ▼
┌────────────────────────────────────┐
│ EventBus.Emit()                    │
│ Type: "servers.changed"             │
│ Data: {server: "name", op: "restart"}│
└────────┬───────────────────────────┘
         │
         ├──────────────────┐
         │                  │
         ▼                  ▼
┌────────────┐    ┌─────────────────┐
│ SSE Stream │    │ Tray UI Refresh │
│ /events    │    │ (via SSE)       │
└────────────┘    └─────────────────┘
```

## Validation Rules

### Service Layer Gates
1. **disable_management**: ALL write operations (enable, disable, restart, bulk) blocked
2. **read_only**: ALL write operations blocked (same as disable_management)
3. Read operations (list, logs, doctor) NEVER blocked by gates

### Diagnostics Constraints
1. `TotalIssues` MUST equal sum of: `len(UpstreamErrors) + len(OAuthRequired) + len(MissingSecrets)`
2. All slice fields MUST be non-nil (empty slice acceptable)
3. `Timestamp` MUST be within 1 minute of request time

### Server Name References
1. All `ServerName` fields MUST reference valid server IDs from config
2. Invalid server names in diagnostics indicate config/state desync (logged as warning)

## Performance Considerations

### Doctor Diagnostics
- **Target**: <3 seconds for 20 servers (SC-002)
- **Strategy**: No external API calls, only local state inspection
- **Timeout**: Context timeout enforced (10s default, configurable)

### List Servers
- **Target**: <100ms (existing performance maintained)
- **Strategy**: Manager caches server state, no I/O needed

### Bulk Operations
- **Target**: Variable (depends on server count)
- **Strategy**: Sequential execution to avoid resource exhaustion
- **Timeout**: Per-server timeout of 5s (total = 5s * server count)

## Migration Notes

### New Types
All new types in `internal/contracts/diagnostics.go`:
- `Diagnostics`
- `UpstreamError`
- `OAuthRequirement`
- `MissingSecret`
- `DockerStatus`
- `AuthStatus`

### Existing Types (Reused)
From `internal/contracts/server.go`:
- `Server`
- `ServerStats`
- `LogEntry`

### Breaking Changes
**NONE** - All new types, no modifications to existing contracts.
