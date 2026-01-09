# Debugging Notes: Tray/CLI/API Consistency Issues

**Date**: 2025-12-17
**Branch**: `013-structured-server-state`

## Problem Statement

The tray shows inconsistent server counts compared to CLI and Web UI. For example:
- **Tray shows**: "8/15 upstream servers"
- **CLI shows**: 13 healthy servers, 2 with issues (total 15)
- **Doctor shows**: 2 issues (github, gmail needing OAuth)

The spec (013-structured-server-state) requires Health to be the single source of truth across all interfaces.

## Investigation Findings

### 1. `connected` Field vs `health.level` Mismatch

The server data contains both a legacy `connected` boolean and a `health` object. These are out of sync:

```json
{
  "name": "buildkite",
  "connected": false,           // <-- WRONG
  "health": {
    "level": "healthy",         // <-- CORRECT (source of truth)
    "summary": "Connected (28 tools)"
  }
}
```

**Affected servers** (showing `connected: false` but `health.level: "healthy"`):
- buildkite
- context7
- datadog
- gcal
- time

### 2. Doctor/Diagnostics Type Assertion Bug (FIXED)

**Problem**: `diagnostics.go` tried to cast `health` to `map[string]interface{}`, but `GetAllServers()` stores it as `*contracts.HealthStatus`. This caused OAuth servers to be incorrectly routed to `upstream_errors` instead of `oauth_required`.

**Fix Applied**: Added `extractHealthFromMap()` helper in `internal/management/diagnostics.go` that handles both struct and map types.

**Result**: Doctor now correctly shows OAuth servers under "🔑 OAuth Authentication Required" section.

### 3. Doctor CLI Display Bug (FIXED)

**Problem**: `doctor_cmd.go` used `getStringArrayField()` for `oauth_required`, expecting `[]string`, but the data is `[]OAuthRequirement` (array of objects).

**Fix Applied**: Changed to `getArrayField()` and parse as objects with `server_name` and `message` fields.

**Result**: Doctor now displays server-specific auth login commands.

### 4. Doctor Output Sorting (FIXED)

**Problem**: Doctor output showed servers in random order.

**Fix Applied**: Added `sortArrayByServerName()` helper in `doctor_cmd.go` to sort all diagnostic arrays alphabetically.

### 5. Tray Connected Count Bug (ATTEMPTED FIX)

**Problem**: Tray counts connected servers using legacy `connected` field instead of `health.level`.

**Attempted Fix**: Updated `UpdateUpstreamServersMenu()` in `internal/tray/managers.go`:
```go
// Use health.level as source of truth
healthLevel := extractHealthLevel(server)
if healthLevel == "healthy" {
    connectedServers++
    continue
}
// Fallback to legacy connected field
if connected, ok := server["connected"].(bool); ok && connected {
    connectedServers++
}
```

Added `extractHealthLevel()` helper to handle both `*contracts.HealthStatus` struct and `map[string]interface{}`.

**Status**: Fix compiles and tests pass, but tray still showing incorrect count. Need to investigate further.

## Potential Remaining Issues

### A. Tray Not Receiving Fresh Data

The tray may be caching stale server data. Possible causes:
1. SSE events not triggering menu refresh
2. `SynchronizationManager` not calling `UpdateUpstreamServersMenu()` with fresh data
3. `ServerStateManager` returning cached data

### B. Health Data Not Reaching Tray

The tray gets server data via `m.server.GetAllServers()`. Need to verify:
1. Does this return the same data as CLI's `upstream list --output=json`?
2. Is the `health` field populated in the tray's data path?

### C. StateView `Connected` Field Not Being Updated

The `connected` field comes from `stateview.ServerStatus.Connected`. This is set from:
- `supervisor/actor_pool.go`: `client.IsConnected()`
- `supervisor/supervisor.go`: Event handling for `EventServerConnected`

The `IsConnected()` method may be returning `false` even when the server is healthy.

## Files Modified

| File | Change |
|------|--------|
| `internal/management/diagnostics.go` | Added `extractHealthFromMap()` helper, fixed Health extraction |
| `cmd/mcpproxy/doctor_cmd.go` | Fixed OAuth array parsing, added sorting, added `sortArrayByServerName()` |
| `internal/tray/managers.go` | Added `extractHealthLevel()` helper, updated connected count logic |

## Verification Commands

```bash
# Check server health vs connected field
./mcpproxy upstream list --output=json | jq '[.[] | {name, connected, health_level: .health.level}]'

# Count healthy servers (should match tray)
./mcpproxy upstream list --output=json | jq '[.[] | select(.health.level == "healthy")] | length'

# Check doctor output
./mcpproxy doctor

# Check diagnostics JSON
./mcpproxy doctor --output=json | jq '.diagnostics | {oauth_required: (.oauth_required | length), upstream_errors: (.upstream_errors | length)}'
```

## Debugging Session 2025-12-18: Still Seeing Inconsistency

### Current Symptom
- Tray shows **8/15** (or similar low count)
- CLI shows **13 healthy** (correct)
- Web UI shows **2 needing login** (correct)
- Debug logs show: `"healthy":0,"legacy_connected":X,"total_connected":X`

This means `extractHealthLevel()` is returning empty string for ALL servers - the health field is not present in the data reaching the tray.

### Data Flow Analysis

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ DATA FLOW: Core → HTTP API → Tray                                           │
└─────────────────────────────────────────────────────────────────────────────┘

1. internal/server/server.go:GetAllServers()
   └─→ Returns []map[string]interface{} with "health": *contracts.HealthStatus
   └─→ VERIFIED: Health calculation added (2025-12-17)

2. internal/management/service.go:ListServers()
   └─→ Extracts health from map: srvRaw["health"].(*contracts.HealthStatus)
   └─→ VERIFIED: Line 293-294 extracts health pointer

3. internal/httpapi/server.go:handleGetServers()
   └─→ Uses management.ListServers() OR contracts.ConvertGenericServersToTyped()
   └─→ Returns contracts.GetServersResponse with []contracts.Server
   └─→ QUESTION: Does contracts.Server.Health get serialized to JSON?

4. HTTP Response (JSON)
   └─→ Should contain: {"servers": [{"name": "...", "health": {...}, ...}]}
   └─→ NEEDS VALIDATION: Does /api/v1/servers actually return health in JSON?

5. cmd/mcpproxy-tray/internal/api/client.go:GetServers()
   └─→ Manually extracts fields from JSON map (lines 488-516)
   └─→ FIXED (2025-12-18): Added health extraction at lines 505-514
   └─→ NEEDS VALIDATION: Is the fix actually being used?

6. cmd/mcpproxy-tray/internal/api/adapter.go:GetAllServers()
   └─→ Converts Server struct to map, includes health if present
   └─→ FIXED (2025-12-17): Added health to output map

7. internal/tray/managers.go:UpdateUpstreamServersMenu()
   └─→ Calls extractHealthLevel() to get health.level
   └─→ Debug logs show healthy=0, meaning health field is missing
```

### Validation Steps

To identify where health is being lost:

**Step 1: Verify HTTP API returns health**
```bash
# Get API key from config
API_KEY=$(jq -r '.api_key' ~/.mcpproxy/mcp_config.json)

# Query the API directly
curl -s -H "X-API-Key: $API_KEY" "http://127.0.0.1:8080/api/v1/servers" | \
  jq '.servers[0] | {name, connected, has_health: (.health != null), health}'
```

Expected: `has_health: true` with health object containing level, admin_state, etc.

**Step 2: Verify tray binary has the fix**
```bash
# Check if the client.go fix is in the binary
strings ./mcpproxy-tray | grep -i "Extract health status"
```

Expected: Should find the comment string if fix is compiled in.

**Step 3: Add more debug logging**
In `client.go:GetServers()`, after extracting health, log what was received:
```go
if healthMap, ok := serverMap["health"].(map[string]interface{}); ok {
    // ... extraction code ...
    c.logger.Debugw("Extracted health", "server", server.Name, "level", server.Health.Level)
} else {
    c.logger.Warnw("No health in server response", "server", server.Name, "keys", keys(serverMap))
}
```

### Hypotheses

**H1: HTTP API is not returning health field**
- The `/api/v1/servers` endpoint may not be including health in the JSON response
- Validate with Step 1 above

**H2: Binary not rebuilt with fixes**
- The running tray binary may not include the latest fixes
- Validate with Step 2 above, or check binary modification time

**H3: Management service path not populating health**
- `management.ListServers()` may not be correctly extracting health
- The health pointer extraction at line 293 only works if health is `*contracts.HealthStatus`

**H4: Converter fallback path doesn't extract health**
- `contracts.ConvertGenericServersToTyped()` doesn't extract health field
- If management service fails, fallback is used which loses health

### Files Modified (Fixes Applied)

| File | Change | Status |
|------|--------|--------|
| `cmd/mcpproxy-tray/internal/api/client.go` | Added `HealthStatus` struct, `Health` field to `Server`, extraction in `GetServers()` | Applied |
| `cmd/mcpproxy-tray/internal/api/adapter.go` | Include health in `GetAllServers()` output map | Applied |
| `internal/server/server.go` | Calculate health in `GetAllServers()` | Applied |
| `internal/tray/managers.go` | Debug logging for health extraction | Applied |

### Next Steps

1. Run validation Step 1 to check if API returns health
2. If API returns health, add logging to client.go to see what's received
3. If API doesn't return health, investigate management service or converter

---

## Root Cause Found & Fixed (2025-12-17)

### Root Cause

The API adapter in `cmd/mcpproxy-tray/internal/api/` was not propagating the `health` field:

1. **`client.go`**: `Server` struct was missing the `Health` field
2. **`adapter.go`**: `GetAllServers()` was not including `health` in the output map
3. **Result**: Tray received server data WITHOUT health, fell back to stale `connected` field

### Fix Applied

**Part 1: API Adapter (for remote tray via HTTP API)**

1. **Added `HealthStatus` struct** to `cmd/mcpproxy-tray/internal/api/client.go`:
   ```go
   type HealthStatus struct {
       Level      string `json:"level"`
       AdminState string `json:"admin_state"`
       Summary    string `json:"summary"`
       Detail     string `json:"detail,omitempty"`
       Action     string `json:"action,omitempty"`
   }
   ```

2. **Added `Health` field** to `Server` struct in `client.go`:
   ```go
   Health *HealthStatus `json:"health,omitempty"`
   ```

3. **Updated `GetAllServers()`** in `adapter.go` to include health in output:
   ```go
   if server.Health != nil {
       serverMap["health"] = map[string]interface{}{
           "level":       server.Health.Level,
           "admin_state": server.Health.AdminState,
           "summary":     server.Health.Summary,
           "detail":      server.Health.Detail,
           "action":      server.Health.Action,
       }
   }
   ```

4. **Updated stats counting** in `GetUpstreamStats()` and `GetStatus()` to use `health.level` as source of truth with fallback to legacy `connected` field.

**Part 2: Embedded Server (for embedded tray)**

5. **Added health calculation** to `internal/server/server.go:GetAllServers()`:
   ```go
   // Calculate unified health status (Spec 013: Health is single source of truth)
   healthInput := health.HealthCalculatorInput{
       Name:        serverStatus.Name,
       Enabled:     serverStatus.Enabled,
       Quarantined: serverStatus.Quarantined,
       State:       status,
       Connected:   connected,
       LastError:   serverStatus.LastError,
       ToolCount:   serverStatus.ToolCount,
       MissingSecret:  health.ExtractMissingSecret(serverStatus.LastError),
       OAuthConfigErr: health.ExtractOAuthConfigError(serverStatus.LastError),
   }
   healthStatus := health.CalculateHealth(healthInput, health.DefaultHealthConfig())
   // ... include in result map
   ```

This was the **critical missing piece** - the embedded tray path via `internal/server/server.go` was not including health, causing 9/15 to be shown instead of 13/15.

### Tests Added

- `internal/tray/managers_test.go`:
  - `TestExtractHealthLevel_*` - Tests health extraction from struct and map
  - `TestConnectedCount_*` - Tests connected count uses health.level
  - `TestHealthConsistency_TrayVsCLI` - Regression test for consistency

- `cmd/mcpproxy-tray/internal/api/adapter_test.go`:
  - `TestServer_HasHealthField` - Verifies Server struct includes health
  - `TestHealthConsistency_AdapterVsRuntime` - Verifies adapter propagates health

### Verification

All tests pass:
- `go test ./internal/tray/...` - 24 tests pass
- `go test ./cmd/mcpproxy-tray/internal/api/...` - 4 tests pass
- `./scripts/test-api-e2e.sh` - 39 tests pass

## Debugging Session 2026-01-06: Validation Script Confirmation

### HTTP API Validation

Created `scripts/validate-health-api.sh` to validate the data flow.

**Results:**
```
=== Spec 013 Health Field Validation ===
✓ Server is running
Response keys: ["data", "success"]
Using servers path: .data.servers
Total servers: 15
Servers with health field: 15
✓ HTTP API is returning health field

=== Health Status Breakdown ===
Healthy servers: 13
Unhealthy servers: 2
Degraded servers: 0

=== Consistency Check: health.level vs connected ===
health.level=healthy: 13
connected=true: 8
enabled + healthy: 13
```

**Conclusion:** The HTTP API `/api/v1/servers` IS returning the health field correctly. The problem was confirmed to be in the tray's API client layer, specifically that the previous fix to extract health from the JSON was not being compiled into the binary the user was running.

### Debug Logging Added

Added granular debug logging to `client.go:GetServers()`:
1. Logs when health is successfully extracted: `"Health extracted" server=name level=healthy`
2. Logs when health field has wrong type: `"Health field present but wrong type"`
3. Logs total counts: `"Server state changed" ... with_health=15 healthy=13`

### Verification Steps

1. **Run validation script:**
   ```bash
   ./scripts/validate-health-api.sh
   ```
   Expected: Shows 13 healthy, 2 unhealthy, all 15 servers have health field

2. **Rebuild and restart tray:**
   ```bash
   go build -o mcpproxy-tray ./cmd/mcpproxy-tray
   # Kill existing tray and restart
   ./mcpproxy-tray
   ```

3. **Check tray logs for health extraction:**
   Look for log messages:
   - `"Server state changed" ... with_health=15 healthy=13` - Good
   - `"Health field present but wrong type"` - Bad (type mismatch)
   - `with_health=0 healthy=0` - Bad (health not reaching client)

4. **Check managers.go logs:**
   Look for `"Connected count calculated"` with `healthy=13`

### Status: Tests Pass, Binary Rebuilt

- All unit tests pass (24 tray tests, 4 adapter tests)
- Binary rebuilt with debug logging
- User needs to restart tray application to verify fix

## Related Spec Requirements

From `specs/013-structured-server-state/spec.md`:
- **FR-006**: Health.Action determines remediation (single source of truth)
- **FR-012**: CLI, Web UI, and Tray MUST show consistent information
- **US-002**: "Checking the status of a troublesome server" - all interfaces should agree
