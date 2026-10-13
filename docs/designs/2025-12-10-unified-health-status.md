# Unified Health Status Design

**Date**: 2025-12-10
**Status**: Ready for implementation

## Problem Statement

**Current issues:**
1. **Inconsistent status** - CLI, tray, and web show different health interpretations
2. **Missing OAuth visibility** - Token expiration not shown in tray/web
3. **No actionable guidance** - Users see errors but not how to fix them
4. **Conflated concepts** - Admin state (disabled/quarantined) mixed with health

**Root cause:** Each interface calculates status independently from raw fields, leading to drift. For example:
- CLI reads `oauth_status` and shows "Token Expired"
- Tray only checks HTTP connectivity and shows "Healthy"
- Same server, different conclusions

**Goals:**
- Single source of truth for server health in the backend
- Consistent display across CLI, tray, and web UI
- Traffic light model: healthy (green) / degraded (yellow) / unhealthy (red)
- Every degraded/unhealthy state includes an action to resolve it
- Admin state (enabled/disabled/quarantined) shown separately from health

**Non-goals:**
- Changing OAuth flow mechanics
- Adding new OAuth features
- Redesigning the web UI layout

## Data Model

**New `HealthStatus` struct** (in `internal/contracts/types.go`):

```go
type HealthStatus struct {
    Level      string `json:"level"`       // "healthy", "degraded", "unhealthy"
    AdminState string `json:"admin_state"` // "enabled", "disabled", "quarantined"
    Summary    string `json:"summary"`     // "Connected (5 tools)", "Token expiring in 2h"
    Detail     string `json:"detail"`      // Optional longer explanation
    Action     string `json:"action"`      // "login", "restart", "enable", "approve", "view_logs", ""
}
```

**Added to existing `Server` struct:**

```go
type Server struct {
    // ... existing fields ...
    Health HealthStatus `json:"health"` // New unified health status
}
```

**Level values:**
| Level | Meaning | View convention |
|-------|---------|-----------------|
| `healthy` | Ready to use, no issues | green |
| `degraded` | Works but needs attention soon | yellow |
| `unhealthy` | Broken, can't use until fixed | red |

**Action types:**
| Action | Meaning |
|--------|---------|
| `""` | No action needed (healthy state) |
| `login` | OAuth authentication required |
| `restart` | Server needs restart |
| `enable` | Server is disabled |
| `approve` | Server is quarantined |
| `view_logs` | Check logs for details |

## Health Calculation Logic

**Location:** `internal/runtime/runtime.go` in `GetAllServers()` (or extracted to `internal/health/calculator.go`)

**Priority order** (first match wins):

```
1. Admin state checks (shown instead of health when not enabled)
   - quarantined → AdminState: "quarantined"
   - disabled    → AdminState: "disabled"

2. Unhealthy (red) conditions
   - connection refused/failed     → "unhealthy", Action: "restart"
   - auth failed (bad credentials) → "unhealthy", Action: "login"
   - server crashed                → "unhealthy", Action: "restart"
   - config error                  → "unhealthy", Action: "view_logs"
   - token expired                 → "unhealthy", Action: "login"
   - refresh failed (after retries)→ "unhealthy", Action: "login"
   - user logged out               → "unhealthy", Action: "login"

3. Degraded (yellow) conditions
   - token expiring soon, no refresh token → "degraded", Action: "login"
   - connecting (in progress)              → "degraded", Action: ""

4. Healthy (green)
   - connected + authenticated (OAuth servers)
   - connected (non-OAuth servers)
   - token valid OR auto-refresh working
```

**OAuth-specific logic:**
| Condition | Level | Action |
|-----------|-------|--------|
| Token valid OR auto-refresh working | `healthy` | - |
| Token expiring soon, no refresh token | `degraded` | `login` |
| Token expired | `unhealthy` | `login` |
| Refresh failed (after retries) | `unhealthy` | `login` |
| User logged out | `unhealthy` | `login` |

**Call failure rate (Spec 113-d):** a connected server that would otherwise be
`healthy` is reported `degraded` (`status: ready`, action `view_logs`) when, over
the last 5 minutes, more than 50% of its counted tool calls failed and at least 5
calls were counted. The summary reads "N of M tool calls failed in the last 5
min" and the detail names the most common failure kind (network, timeout, HTTP,
JSON-RPC, session). Only transport-level failures count: `isError` tool results,
proxy refusals (policy, quarantine, concurrency limits, argument validation),
caller cancellation and sign-in-required errors are excluded. Every existing
non-healthy outcome (quarantined, disabled, disconnected, error, OAuth expired,
refresh failed, missing secret, retry stopped) takes precedence. The thresholds
are constants (`health.CallFailureWindow`, `CallFailureMinSamples`,
`CallFailureRatio`), not configuration; the window is in memory and dropped when
the server is removed.

**Key distinction:**
- **Degraded** = works now but will break soon without action
- **Unhealthy** = broken, can't use until fixed

## Interface Display

Each interface renders `HealthStatus` consistently but adapted to its medium.

### CLI

**`mcpproxy upstream list` and `mcpproxy auth status`:**
```
Server           Health                          Action
───────────────────────────────────────────────────────────────────
slack            🟢 Connected (5 tools)
github           🟡 Token expiring in 45m        → auth login --server=github
filesystem       🔴 Connection refused           → upstream restart filesystem
new-server       ⏸️  Quarantined                  → Approve in Web UI
old-server       ⏹️  Disabled                     → upstream enable old-server
```

### Tray Menu

```
🟢 slack
🟡 github - Token expiring
🔴 filesystem - Error
⏸️ new-server (Quarantined)
⏹️ old-server (Disabled)
```

Clicking yellow/red servers opens Web UI to the relevant fix page.

### Web UI

| Location | Shows | Actions |
|----------|-------|---------|
| **Dashboard** | "X servers need attention" banner | Quick-fix buttons per server |
| **ServerCard** | Colored status badge + summary | Login/Restart/Reconnect based on `action` field |
| **ServerDetail** | Full health details | Same actions + logs |

### Action Hint Mapping

Each interface maps the `Action` field to its own UX:

**CLI:**
```
"login"    → "auth login --server=%s"
"restart"  → "upstream restart %s"
"enable"   → "upstream enable %s"
"approve"  → "Approve in Web UI or config"
"view_logs"→ "upstream logs %s"
```

**Tray:**
```
"login"    → opens http://localhost:8080/ui/servers/{name}?action=login
"restart"  → triggers API call directly
"enable"   → triggers API call directly
"approve"  → opens http://localhost:8080/ui/servers/{name}?action=approve
```

**Web UI:**
```
"login"    → Login button
"restart"  → Restart button
"enable"   → Enable toggle
"approve"  → Approve button
```

## Implementation Changes

**Files to modify:**

| File | Change |
|------|--------|
| `internal/contracts/types.go` | Add `HealthStatus` struct |
| `internal/runtime/runtime.go` | Calculate `Health` in `GetAllServers()` |
| `internal/httpapi/server.go` | Ensure `health` field is included in API response |
| `cmd/mcpproxy/upstream_cmd.go` | Update `upstream list` to use `Health` field |
| `cmd/mcpproxy/auth_cmd.go` | Update `auth status` to use `Health` field |
| `internal/tray/managers.go` | Update `getServerStatusDisplay()` to use `Health` field |
| `frontend/src/components/ServerCard.vue` | Use `health` for badge color + show action |
| `frontend/src/views/Dashboard.vue` | Use `health.level` to filter servers needing attention |

**No backward compatibility needed** - all clients (CLI, tray, web) ship together in mcpproxy releases.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Backend (Runtime)                         │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ CalculateHealth() → HealthStatus                        ││
│  │   - Level: healthy/degraded/unhealthy                   ││
│  │   - AdminState: enabled/disabled/quarantined            ││
│  │   - Summary: "Connected (5 tools)"                      ││
│  │   - Action: login/restart/enable/approve/""             ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
                            │
                   GET /api/v1/servers
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
   ┌─────────┐        ┌─────────┐        ┌─────────┐
   │   CLI   │        │  Tray   │        │ Web UI  │
   │         │        │         │        │         │
   │ 🟢/🟡/🔴  │        │ 🟢/🟡/🔴  │        │ badges  │
   │ + hint  │        │ + click │        │ + btns  │
   └─────────┘        └─────────┘        └─────────┘
```

**Key principle:** Backend owns health calculation. Interfaces only render.

## Success Criteria

1. All three interfaces show identical health status for any server
2. Yellow/red states always include actionable guidance
3. OAuth token issues visible in tray and web (not just CLI)
4. Admin state (disabled/quarantined) clearly distinct from health
