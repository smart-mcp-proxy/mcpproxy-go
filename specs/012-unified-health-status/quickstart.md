# Quickstart: Unified Health Status Implementation

**Feature**: 012-unified-health-status
**Estimated Implementation**: Backend (1-2 days), Frontend (1 day), Testing (1 day)

## Overview

This feature adds a unified health status system that calculates server health once in the backend and displays it consistently across CLI, tray, web UI, and MCP tools.

## Implementation Order

### Phase 1: Backend Core (Day 1)

1. **Add HealthStatus type** (`internal/contracts/types.go`)
   ```go
   type HealthStatus struct {
       Level      string `json:"level"`
       AdminState string `json:"admin_state"`
       Summary    string `json:"summary"`
       Detail     string `json:"detail,omitempty"`
       Action     string `json:"action,omitempty"`
   }
   ```

2. **Create health calculator** (`internal/health/calculator.go`)
   ```go
   func CalculateHealth(input HealthCalculatorInput, cfg *HealthCalculatorConfig) *contracts.HealthStatus
   ```

3. **Integrate into GetAllServers()** (`internal/runtime/runtime.go`)
   - After building server response, call `CalculateHealth()` and assign to `Health` field

4. **Add to MCP list response** (`internal/server/mcp.go`)
   - In `handleListUpstreams()`, include `health` field in each server object

### Phase 2: CLI & Tray (Day 2)

5. **Update CLI display** (`cmd/mcpproxy/upstream_cmd.go`)
   - Change `upstream list` to show emoji + summary from `Health` field
   - Add action hints column

6. **Update auth status** (`cmd/mcpproxy/auth_cmd.go`)
   - Use `Health` field for consistent display

7. **Update tray menu** (`cmd/mcpproxy-tray/`)
   - Use `Health.Level` for status emoji
   - Click action based on `Health.Action`

### Phase 3: Web UI (Day 3)

8. **Update ServerCard** (`frontend/src/components/ServerCard.vue`)
   - Badge color from `health.level`
   - Action button from `health.action`

9. **Update Dashboard** (`frontend/src/views/Dashboard.vue`)
   - "X servers need attention" banner
   - Filter by `health.level !== 'healthy'`

### Phase 4: Testing & Polish (Day 4)

10. **Unit tests** (`internal/health/calculator_test.go`)
11. **Integration tests** (API response validation)
12. **E2E tests** (CLI output verification)
13. **Documentation** (CLAUDE.md, OpenAPI spec)

## Key Files to Modify

| File | Change |
|------|--------|
| `internal/contracts/types.go` | Add `HealthStatus` struct, add `Health` field to `Server` |
| `internal/health/calculator.go` | NEW: Health calculation logic |
| `internal/health/calculator_test.go` | NEW: Unit tests |
| `internal/runtime/runtime.go` | Call `CalculateHealth()` in `GetAllServers()` |
| `internal/server/mcp.go` | Add `health` to `handleListUpstreams()` response |
| `cmd/mcpproxy/upstream_cmd.go` | Update display format |
| `cmd/mcpproxy/auth_cmd.go` | Update display format |
| `frontend/src/components/ServerCard.vue` | Use `health` for display |
| `frontend/src/views/Dashboard.vue` | Add "needs attention" banner |
| `oas/swagger.yaml` | Add `HealthStatus` schema |
| `CLAUDE.md` | Document new health fields |

## Testing Commands

```bash
# Run unit tests for health calculator
go test ./internal/health/... -v

# Run full test suite
./scripts/run-all-tests.sh

# Run API E2E tests
./scripts/test-api-e2e.sh

# Manual CLI verification
./mcpproxy upstream list
./mcpproxy auth status
```

## Verification Checklist

- [ ] `GET /api/v1/servers` includes `health` field for each server
- [ ] `mcpproxy upstream list` shows emoji and action hints
- [ ] Tray menu shows consistent status with CLI
- [ ] Web UI ServerCard shows colored badge
- [ ] Dashboard shows "X servers need attention" when applicable
- [ ] MCP `upstream_servers list` includes `health` field
- [ ] All interfaces show identical status for same server

## Health Calculation Reference

```go
// Priority order (first match wins):

// 1. Admin state (short-circuit)
if !enabled {
    return HealthStatus{Level: "healthy", AdminState: "disabled", Action: "enable"}
}
if quarantined {
    return HealthStatus{Level: "healthy", AdminState: "quarantined", Action: "approve"}
}

// 2. Connection errors → unhealthy
if state == "error" || state == "disconnected" {
    return HealthStatus{Level: "unhealthy", AdminState: "enabled", Action: "restart"}
}

// 3. Connecting → degraded
if state == "connecting" || state == "idle" {
    return HealthStatus{Level: "degraded", AdminState: "enabled", Action: ""}
}

// 4. OAuth checks (only if connected)
if oauthRequired {
    if userLoggedOut || oauthStatus == "expired" {
        return HealthStatus{Level: "unhealthy", AdminState: "enabled", Action: "login"}
    }
    if tokenExpiringSoon && !hasRefreshToken {
        return HealthStatus{Level: "degraded", AdminState: "enabled", Action: "login"}
    }
}

// 5. Healthy
return HealthStatus{Level: "healthy", AdminState: "enabled", Action: ""}
```

## Frontend Color Mapping

```typescript
const levelToColor = {
  healthy: 'green',
  degraded: 'yellow',
  unhealthy: 'red'
}

const adminStateToColor = {
  enabled: null,  // Use level color
  disabled: 'gray',
  quarantined: 'purple'
}
```

## MCP Response Example

```json
{
  "servers": [
    {
      "name": "github",
      "enabled": true,
      "connected": true,
      "health": {
        "level": "healthy",
        "admin_state": "enabled",
        "summary": "Connected (5 tools)"
      }
    }
  ]
}
```
