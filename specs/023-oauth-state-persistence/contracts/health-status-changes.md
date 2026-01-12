# Contract Changes: Health Status Extension

**Feature**: 023-oauth-state-persistence
**Date**: 2026-01-12

## Overview

This feature does NOT add new REST API endpoints. It extends the existing health status response format to include refresh retry state information.

## Existing Endpoint

**Endpoint**: `GET /api/v1/servers`
**Response**: Array of server objects with `health` field

## Health Field Extension

### Current Schema (unchanged fields)

```json
{
  "health": {
    "level": "healthy|degraded|unhealthy",
    "admin_state": "enabled|disabled|quarantined",
    "summary": "string",
    "detail": "string",
    "action": "login|restart|enable|approve|view_logs|"
  }
}
```

### Extended `detail` Values

The `detail` field will include refresh-specific information when applicable:

| Condition | level | summary | detail | action |
|-----------|-------|---------|--------|--------|
| Refresh scheduled | healthy | Connected | Token refresh scheduled for {time} | - |
| Refresh retrying | degraded | Token refresh pending | Refresh retry {n} scheduled for {time}: {error} | view_logs |
| Refresh failed (network) | degraded | Refresh failed - network error | Network error: {details}. Retry in {time} | retry |
| Refresh failed (permanent) | unhealthy | Refresh token expired | Re-authentication required: {error} | login |

### Example Responses

**Healthy with scheduled refresh**:
```json
{
  "health": {
    "level": "healthy",
    "admin_state": "enabled",
    "summary": "Connected (5 tools)",
    "detail": "Token refresh scheduled for 2026-01-12T15:30:00Z",
    "action": ""
  }
}
```

**Degraded during retry**:
```json
{
  "health": {
    "level": "degraded",
    "admin_state": "enabled",
    "summary": "Token refresh pending",
    "detail": "Refresh retry 3 scheduled for 2026-01-12T14:05:40Z: connection timeout",
    "action": "view_logs"
  }
}
```

**Unhealthy (permanent failure)**:
```json
{
  "health": {
    "level": "unhealthy",
    "admin_state": "enabled",
    "summary": "Refresh token expired",
    "detail": "Re-authentication required: invalid_grant",
    "action": "login"
  }
}
```

## Metrics Endpoint

**Endpoint**: `GET /metrics` (Prometheus format)

### New Metrics

```prometheus
# HELP mcpproxy_oauth_refresh_total Total number of OAuth token refresh attempts
# TYPE mcpproxy_oauth_refresh_total counter
mcpproxy_oauth_refresh_total{server="github-mcp",result="success"} 42
mcpproxy_oauth_refresh_total{server="github-mcp",result="failed_network"} 3
mcpproxy_oauth_refresh_total{server="atlassian-mcp",result="failed_invalid_grant"} 1

# HELP mcpproxy_oauth_refresh_duration_seconds OAuth token refresh duration in seconds
# TYPE mcpproxy_oauth_refresh_duration_seconds histogram
mcpproxy_oauth_refresh_duration_seconds_bucket{server="github-mcp",result="success",le="0.5"} 38
mcpproxy_oauth_refresh_duration_seconds_bucket{server="github-mcp",result="success",le="1"} 41
mcpproxy_oauth_refresh_duration_seconds_bucket{server="github-mcp",result="success",le="+Inf"} 42
mcpproxy_oauth_refresh_duration_seconds_sum{server="github-mcp",result="success"} 18.5
mcpproxy_oauth_refresh_duration_seconds_count{server="github-mcp",result="success"} 42
```

### Label Values

**`result` label**:
- `success`: Token refresh completed successfully
- `failed_network`: Network error (timeout, DNS, connection refused)
- `failed_invalid_grant`: Refresh token expired or revoked
- `failed_other`: Other OAuth errors (server_error, etc.)

## SSE Events

**Endpoint**: `GET /events`

No new event types. Existing `servers.changed` event will be emitted when health status changes due to refresh state transitions.

## Backward Compatibility

All changes are additive:
- `detail` field already exists, just gains more specific content
- New metrics don't affect existing metrics
- No schema breaking changes
