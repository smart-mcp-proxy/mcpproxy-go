# Data Model: Unified Health Status

**Feature**: 012-unified-health-status
**Date**: 2025-12-11

## Entities

### HealthStatus (NEW)

Represents the unified health status of an upstream MCP server.

**Location**: `internal/contracts/types.go`

```go
// HealthStatus represents the unified health status of a server.
// Calculated once in the backend and rendered identically by all interfaces.
type HealthStatus struct {
    // Level indicates the health level: "healthy", "degraded", or "unhealthy"
    Level string `json:"level"`

    // AdminState indicates the admin state: "enabled", "disabled", or "quarantined"
    AdminState string `json:"admin_state"`

    // Summary is a human-readable status message (e.g., "Connected (5 tools)")
    Summary string `json:"summary"`

    // Detail is an optional longer explanation of the status
    Detail string `json:"detail,omitempty"`

    // Action is the suggested fix action: "login", "restart", "enable", "approve", "view_logs", or "" (none)
    Action string `json:"action,omitempty"`
}
```

**Field Definitions**:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `level` | string | Yes | Health level: `healthy`, `degraded`, `unhealthy` |
| `admin_state` | string | Yes | Admin state: `enabled`, `disabled`, `quarantined` |
| `summary` | string | Yes | Human-readable status (max 100 chars) |
| `detail` | string | No | Extended explanation for debugging |
| `action` | string | No | Suggested remediation action |

**Validation Rules**:
- `level` must be one of: `healthy`, `degraded`, `unhealthy`
- `admin_state` must be one of: `enabled`, `disabled`, `quarantined`
- `summary` must be non-empty, max 100 characters
- `action` must be empty or one of: `login`, `restart`, `enable`, `approve`, `view_logs`

**Constants** (in `internal/health/constants.go`):

```go
package health

// Health levels
const (
    LevelHealthy   = "healthy"
    LevelDegraded  = "degraded"
    LevelUnhealthy = "unhealthy"
)

// Admin states
const (
    StateEnabled     = "enabled"
    StateDisabled    = "disabled"
    StateQuarantined = "quarantined"
)

// Actions
const (
    ActionNone     = ""
    ActionLogin    = "login"
    ActionRestart  = "restart"
    ActionEnable   = "enable"
    ActionApprove  = "approve"
    ActionViewLogs = "view_logs"
)
```

---

### Server (MODIFIED)

Extended to include the new `Health` field.

**Location**: `internal/contracts/types.go`

```go
type Server struct {
    // ... existing fields (ID, Name, URL, Protocol, etc.) ...

    // Health is the unified health status calculated by the backend.
    // Always populated for enabled servers; may be minimal for disabled/quarantined.
    Health *HealthStatus `json:"health,omitempty"`
}
```

**Field Definition**:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `health` | *HealthStatus | No | Unified health status; nil only during migration |

**Migration Notes**:
- Field is additive; existing clients that don't read `health` are unaffected
- Backend always populates `health` for new responses
- Old cached responses may lack `health` field (graceful degradation)

---

### HealthCalculatorInput (INTERNAL)

Input struct for the health calculator function.

**Location**: `internal/health/calculator.go`

```go
// HealthCalculatorInput contains all fields needed to calculate health status.
// This struct normalizes data from different sources (StateView, storage, config).
type HealthCalculatorInput struct {
    // Server identification
    Name string

    // Admin state
    Enabled     bool
    Quarantined bool

    // Connection state
    State     string // "connected", "connecting", "error", "idle", "disconnected"
    Connected bool
    LastError string

    // OAuth state (only for OAuth-enabled servers)
    OAuthRequired  bool
    OAuthStatus    string     // "authenticated", "expired", "error", "none"
    TokenExpiresAt *time.Time // When token expires
    HasRefreshToken bool      // True if refresh token exists
    UserLoggedOut  bool       // True if user explicitly logged out

    // Tool info
    ToolCount int
}
```

---

### HealthCalculatorConfig (INTERNAL)

Configuration for health calculation thresholds.

**Location**: `internal/health/calculator.go`

```go
// HealthCalculatorConfig contains configurable thresholds for health calculation.
type HealthCalculatorConfig struct {
    // ExpiryWarningDuration is the duration before token expiry to show degraded status.
    // Default: 1 hour
    ExpiryWarningDuration time.Duration
}

// DefaultHealthConfig returns the default health calculator configuration.
func DefaultHealthConfig() *HealthCalculatorConfig {
    return &HealthCalculatorConfig{
        ExpiryWarningDuration: time.Hour,
    }
}
```

---

## State Transitions

### Health Level Transitions

Health level is stateless; it's recalculated on every request based on current state.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Health Calculation Flow                       │
└─────────────────────────────────────────────────────────────────┘

                    ┌─────────────────┐
                    │  Start Check    │
                    └────────┬────────┘
                             │
            ┌────────────────┼────────────────┐
            │                │                │
            ▼                ▼                ▼
     ┌──────────┐     ┌──────────┐     ┌──────────┐
     │ Disabled │     │Quarantine│     │ Enabled  │
     └────┬─────┘     └────┬─────┘     └────┬─────┘
          │                │                │
          ▼                ▼                ▼
    AdminState:      AdminState:      Check Connection
    "disabled"       "quarantined"    & OAuth State
    Action:"enable"  Action:"approve"       │
                                            │
                          ┌─────────────────┼─────────────────┐
                          │                 │                 │
                          ▼                 ▼                 ▼
                    ┌──────────┐     ┌──────────┐     ┌──────────┐
                    │  Error   │     │Connecting│     │Connected │
                    └────┬─────┘     └────┬─────┘     └────┬─────┘
                         │                │                │
                         ▼                ▼                ▼
                   "unhealthy"       "degraded"      Check OAuth
                   Action:*              │                │
                                         │     ┌──────────┼──────────┐
                                         │     │          │          │
                                         │     ▼          ▼          ▼
                                         │  Expired   Expiring   Valid/
                                         │     │      Soon       Refreshing
                                         │     │          │          │
                                         │     ▼          ▼          ▼
                                         │"unhealthy" "degraded" "healthy"
                                         │  "login"    "login"      ""
                                         │
                                         └───────────────────────────┘
```

### Admin State Priority

Admin state is checked first and short-circuits health calculation:

1. If `!enabled` → return immediately with `AdminState: "disabled"`
2. If `quarantined` → return immediately with `AdminState: "quarantined"`
3. Otherwise → proceed to connection/OAuth health checks

---

## Relationships

```
┌─────────────────────────────────────────────────────────────────┐
│                      Entity Relationships                        │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  contracts.Server                                                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ ID, Name, URL, Protocol, Command, Args, Env, Headers        ││
│  │ Enabled, Quarantined, Connected, Connecting, Status         ││
│  │ OAuthStatus, TokenExpiresAt, ToolCount, ...                 ││
│  │                                                             ││
│  │ ┌─────────────────────────────────────────────────────────┐ ││
│  │ │ Health *HealthStatus (NEW)                              │ ││
│  │ │   - Level: healthy/degraded/unhealthy                   │ ││
│  │ │   - AdminState: enabled/disabled/quarantined            │ ││
│  │ │   - Summary: "Connected (5 tools)"                      │ ││
│  │ │   - Action: login/restart/enable/approve/view_logs      │ ││
│  │ └─────────────────────────────────────────────────────────┘ ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ Calculated by
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  health.Calculator                                               │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ CalculateHealth(input HealthCalculatorInput) HealthStatus   ││
│  │                                                             ││
│  │ Uses:                                                       ││
│  │   - HealthCalculatorConfig (thresholds)                     ││
│  │   - Input fields from Server/StateView                      ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

---

## API Impact

### REST API

**Endpoint**: `GET /api/v1/servers`

**Response Change**: Each server in the response now includes a `health` field.

Before:
```json
{
  "servers": [
    {
      "id": "abc123",
      "name": "github",
      "enabled": true,
      "connected": true,
      "oauth_status": "authenticated",
      "tool_count": 5
    }
  ]
}
```

After:
```json
{
  "servers": [
    {
      "id": "abc123",
      "name": "github",
      "enabled": true,
      "connected": true,
      "oauth_status": "authenticated",
      "tool_count": 5,
      "health": {
        "level": "healthy",
        "admin_state": "enabled",
        "summary": "Connected (5 tools)",
        "action": ""
      }
    }
  ]
}
```

### MCP Protocol

**Tool**: `upstream_servers` with `operation: list`

**Response Change**: Each server includes a `health` field (same structure as REST API).

---

## Storage Impact

**No database changes required.**

The `HealthStatus` is calculated at runtime from existing stored fields. No new tables, buckets, or indices are needed.
