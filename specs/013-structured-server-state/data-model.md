# Data Model: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

Health is the single source of truth for per-server issues. Diagnostics aggregates from Health.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Health (per-server)                          │
│                    Source of Truth                              │
├─────────────────────────────────────────────────────────────────┤
│ level: healthy | degraded | unhealthy                           │
│ admin_state: enabled | disabled | quarantined                   │
│ summary: "Connected (12 tools)" | "Missing secret" | etc.       │
│ detail: error message, secret name, expiry time                 │
│ action: login | restart | set_secret | configure | ...          │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ aggregate by action
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Diagnostics (system-wide)                      │
│                  Derived from Health                            │
├─────────────────────────────────────────────────────────────────┤
│ upstream_errors: servers where action == "restart"              │
│ oauth_required: servers where action == "login"                 │
│ oauth_issues: servers where action == "configure"               │
│ missing_secrets: grouped by secret name (cross-cutting)         │
│ docker_status: system-level check                               │
└─────────────────────────────────────────────────────────────────┘
```

## Health Actions

### Existing Actions (no change)

| Action | Scenario | Detail Contains |
|--------|----------|-----------------|
| `""` | Healthy, connecting | - |
| `login` | OAuth needed/expired | expiry time (if expiring) |
| `restart` | Connection error | error message |
| `enable` | Server disabled | - |
| `approve` | Server quarantined | - |
| `view_logs` | Needs log inspection | - |

### New Actions

| Action | Scenario | Detail Contains |
|--------|----------|-----------------|
| `set_secret` | Missing secret | secret name (e.g., `GITHUB_TOKEN`) |
| `configure` | OAuth config issue | error message or missing param |

## Health Constants (Go)

```go
// internal/health/constants.go

const (
    // Existing
    ActionNone     = ""
    ActionLogin    = "login"
    ActionRestart  = "restart"
    ActionEnable   = "enable"
    ActionApprove  = "approve"
    ActionViewLogs = "view_logs"

    // New
    ActionSetSecret = "set_secret"
    ActionConfigure = "configure"
)
```

## Health Calculator Input

The calculator needs additional input to detect new scenarios:

```go
type HealthCalculatorInput struct {
    // Existing fields
    Name            string
    Enabled         bool
    Quarantined     bool
    State           string  // disconnected, connecting, ready, error
    LastError       string
    OAuthRequired   bool
    OAuthStatus     string
    TokenExpiresAt  *time.Time
    HasRefreshToken bool
    UserLoggedOut   bool
    ToolCount       int

    // New fields for secret/config detection
    MissingSecret   string  // Secret name if unresolved (e.g., "GITHUB_TOKEN")
    OAuthConfigErr  string  // OAuth config error (e.g., "requires 'resource' parameter")
}
```

## Health Calculation Priority

Updated priority order:

```
1. Admin state (disabled → enable, quarantined → approve)
2. Missing secret (→ set_secret)
3. OAuth config issue (→ configure)
4. Connection state (error/disconnected → restart, connecting → wait)
5. OAuth state (expired/needed → login, expiring → login)
6. Healthy
```

## Diagnostics Aggregation

### Current (Independent Detection)

```go
// BAD: Duplicates health detection logic
for _, srv := range servers {
    if srv.last_error != "" {
        diag.UpstreamErrors = append(...)
    }
    if srv.oauth != nil && !srv.authenticated {
        diag.OAuthRequired = append(...)
    }
}
```

### New (Derived from Health)

```go
// GOOD: Single source of truth
for _, srv := range servers {
    switch srv.Health.Action {
    case "restart":
        diag.UpstreamErrors = append(diag.UpstreamErrors, UpstreamError{
            ServerName:   srv.Name,
            ErrorMessage: srv.Health.Detail,
        })
    case "login":
        diag.OAuthRequired = append(diag.OAuthRequired, OAuthRequirement{
            ServerName: srv.Name,
            State:      "unauthenticated",
        })
    case "set_secret":
        secretName := srv.Health.Detail
        diag.MissingSecrets[secretName] = append(diag.MissingSecrets[secretName], srv.Name)
    case "configure":
        diag.OAuthIssues = append(diag.OAuthIssues, OAuthIssue{
            ServerName: srv.Name,
            Error:      srv.Health.Detail,
        })
    }
}
```

## Frontend Action Handlers

```typescript
// Map action to navigation/behavior
function handleHealthAction(server: Server) {
    switch (server.health?.action) {
        case 'login':
            triggerOAuthFlow(server.name)
            break
        case 'restart':
            restartServer(server.name)
            break
        case 'enable':
            enableServer(server.name)
            break
        case 'approve':
            approveServer(server.name)
            break
        case 'set_secret':
            router.push('/secrets')  // Navigate to secrets page
            break
        case 'configure':
            router.push(`/servers/${server.name}?tab=config`)
            break
        case 'view_logs':
            router.push(`/servers/${server.name}?tab=logs`)
            break
    }
}
```
