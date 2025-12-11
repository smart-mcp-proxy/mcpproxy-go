# Research: Unified Health Status

**Feature**: 012-unified-health-status
**Date**: 2025-12-11
**Status**: Complete

## Research Tasks

### 1. OAuth Status Integration

**Question**: How does MCPProxy currently track OAuth token status, and how should it be integrated into health calculation?

**Findings**:

The OAuth system has multiple status indicators stored across different locations:

1. **`contracts.Server.OAuthStatus`** - String field with values: `authenticated`, `expired`, `error`, `none`
2. **`contracts.Server.TokenExpiresAt`** - `*time.Time` indicating when the token expires
3. **`contracts.Server.Authenticated`** - Boolean for simple auth check
4. **`contracts.Server.UserLoggedOut`** - Boolean indicating explicit user logout

OAuth status calculation in `internal/oauth/status.go`:
- `GetOAuthStatus()` returns the current status string
- `IsTokenExpired()` checks expiration against current time
- Token refresh is handled by `internal/oauth/refresh.go` with automatic retry

**Decision**: Use existing OAuth fields directly in health calculation:
- `OAuthStatus == "expired"` → unhealthy
- `TokenExpiresAt` within 1 hour && no refresh token → degraded
- Token valid OR auto-refresh working → healthy

**Rationale**: No new OAuth tracking needed; existing fields provide sufficient information.

**Alternatives Considered**:
- Creating a new consolidated OAuth state struct → Rejected: adds complexity, duplicates data
- Querying OAuth manager directly → Rejected: breaks StateView's lock-free read pattern

---

### 2. Connection State Mapping

**Question**: How do StateView connection states map to health levels?

**Findings**:

`internal/runtime/stateview/stateview.go` defines `ServerStatus.State` with values:
- `idle` - Not started
- `connecting` - Connection in progress
- `connected` - Successfully connected
- `error` - Connection failed
- `disconnected` - Was connected, now disconnected

Additional fields:
- `Connected` (bool) - True when successfully connected
- `LastError` (string) - Error message if in error state
- `RetryCount` (int) - Number of reconnection attempts

**Decision**: Map states to health levels:
| State | Connected | Level | Action |
|-------|-----------|-------|--------|
| `connected` | true | healthy | - |
| `connecting` | false | degraded | - |
| `idle` | false | degraded | - |
| `error` | false | unhealthy | restart |
| `disconnected` | false | unhealthy | restart |

**Rationale**: `connecting` and `idle` are transitional states that will resolve; `error` and `disconnected` require intervention.

**Alternatives Considered**:
- Treating `idle` as unhealthy → Rejected: may occur during normal startup
- Adding more granular states → Rejected: current states are sufficient; YAGNI

---

### 3. Admin State Precedence

**Question**: How should admin state (disabled/quarantined) interact with health status?

**Findings**:

Design document specifies: "Admin state takes precedence - show 'Disabled'" when server is both disabled AND has other issues.

Current codebase:
- `ServerStatus.Enabled` - Boolean, false = disabled
- `ServerStatus.Quarantined` - Boolean, true = quarantined

**Decision**: Check admin state FIRST before health calculation:
```go
// Pseudocode
if !enabled {
    return AdminState: "disabled", Level: "healthy", Action: "enable"
}
if quarantined {
    return AdminState: "quarantined", Level: "healthy", Action: "approve"
}
// Then calculate health from connection/OAuth state
```

**Rationale**: Disabled/quarantined servers shouldn't show as "unhealthy" because their state is intentional. The action tells users how to enable them.

**Alternatives Considered**:
- Showing both admin state AND health → Rejected: confusing ("disabled but also unhealthy")
- Setting Level to "unhealthy" for admin states → Rejected: implies something is broken

---

### 4. Action Types and Hints

**Question**: What action types should be supported and how should they map to interface-specific hints?

**Findings**:

Design document defines actions:
- `""` (empty) - No action needed
- `login` - OAuth authentication required
- `restart` - Server needs restart
- `enable` - Server is disabled
- `approve` - Server is quarantined
- `view_logs` - Check logs for details

**Decision**: Use these exact action types. Map to hints:

| Action | CLI Hint | Tray Action | Web UI Button |
|--------|----------|-------------|---------------|
| `login` | `auth login --server=%s` | Open login page | "Login" button |
| `restart` | `upstream restart %s` | API call | "Restart" button |
| `enable` | `upstream enable %s` | API call | Toggle switch |
| `approve` | "Approve in Web UI" | Open approve page | "Approve" button |
| `view_logs` | `upstream logs %s` | Open logs page | "View Logs" link |

**Rationale**: Matches design document exactly; each interface adapts the action to its UX idiom.

**Alternatives Considered**:
- Generic "fix" action → Rejected: not actionable
- Including full command in action field → Rejected: mixes concerns; CLI builds its own hints

---

### 5. Token Expiry Warning Threshold

**Question**: What threshold should trigger "expiring soon" degraded status?

**Findings**:

Spec assumption: "Token expiration threshold for 'expiring soon' warning is configurable (default: 1 hour)"

Current config structure in `internal/config/config.go` has no expiry threshold setting.

**Decision**: Add `oauth_expiry_warning_hours` config option with default 1 hour.

Default: 1 hour (3600 seconds)
Range: 0.25 hours (15 minutes) to 24 hours

**Rationale**: 1 hour gives users time to re-authenticate without being annoying. Configurable for different use cases.

**Alternatives Considered**:
- Fixed threshold → Rejected: user feedback may require adjustment
- Per-server threshold → Rejected: over-engineering for initial implementation

---

### 6. Frontend Integration Pattern

**Question**: How should the Vue frontend consume and display health status?

**Findings**:

Current pattern in `frontend/src/`:
- `ServerCard.vue` displays server status using individual fields
- `Dashboard.vue` lists servers but doesn't filter by health
- API responses are fetched via composables/services

**Decision**:
1. Use `health.level` for badge color: healthy=green, degraded=yellow, unhealthy=red
2. Use `health.admin_state` for special styling: disabled=gray, quarantined=purple
3. Show `health.summary` as status text
4. Render action button based on `health.action`

Badge component mapping:
```vue
<Badge :variant="healthLevelToVariant(server.health.level)">
  {{ server.health.summary }}
</Badge>
```

**Rationale**: Frontend becomes a pure renderer; all intelligence is in backend.

**Alternatives Considered**:
- Client-side health calculation → Rejected: defeats the purpose of unified backend calculation
- Multiple API calls to get health → Rejected: health should be embedded in existing endpoints

---

### 7. MCP Tools Response Structure

**Question**: How should health status be structured in MCP `upstream_servers list` responses?

**Findings**:

Current MCP response in `internal/server/mcp.go` `handleListUpstreams()`:
- Returns `[]map[string]interface{}` with server fields
- Consumed by LLMs (Claude Code, Cursor, etc.)

**Decision**: Add `health` field to each server object:
```json
{
  "name": "github",
  "enabled": true,
  "health": {
    "level": "unhealthy",
    "admin_state": "enabled",
    "summary": "Token expired",
    "action": "login"
  }
}
```

**Rationale**: LLMs can use `health.action` directly for next steps without interpreting raw fields.

**Alternatives Considered**:
- Separate `get_server_health` tool → Rejected: requires extra call; less convenient
- Flattening health fields → Rejected: loses semantic grouping

---

## Summary of Decisions

| Area | Decision |
|------|----------|
| OAuth Integration | Use existing `OAuthStatus`, `TokenExpiresAt` fields |
| Connection Mapping | `connected`=healthy, `connecting`=degraded, `error`=unhealthy |
| Admin Precedence | Check disabled/quarantined FIRST, before health |
| Action Types | 6 types: empty, login, restart, enable, approve, view_logs |
| Expiry Threshold | Configurable, default 1 hour |
| Frontend Pattern | Backend calculates; frontend renders with color/action mapping |
| MCP Response | Nested `health` object in server list response |

All research questions resolved. No NEEDS CLARIFICATION items remain.
