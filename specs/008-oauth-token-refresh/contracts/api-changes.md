# API Contracts: OAuth Token Refresh Bug Fixes

**Feature**: 008-oauth-token-refresh
**Date**: 2025-12-04

## Overview

This feature primarily fixes bugs and improves logging. No new API endpoints are required.
The existing REST API endpoints continue to work as before with enhanced OAuth status information.

## Existing Endpoints (Enhanced)

### GET /api/v1/servers

The servers list endpoint already returns OAuth status. This feature ensures the status is accurate.

**Response Enhancement**:

The `oauth` field in server status will now correctly reflect:
- Token expiration status
- Whether refresh is in progress
- Last refresh attempt timestamp

```json
{
  "servers": [
    {
      "name": "oauth-test-server",
      "status": "connected",
      "oauth": {
        "required": true,
        "authenticated": true,
        "token_expires_at": "2025-12-04T11:00:00Z",
        "has_refresh_token": true,
        "last_refresh_at": "2025-12-04T10:00:00Z"
      }
    }
  ]
}
```

### GET /api/v1/servers/{name}

Individual server status with detailed OAuth information.

**Response Enhancement** (same as above, per server):

```json
{
  "name": "oauth-test-server",
  "url": "http://127.0.0.1:9000/mcp",
  "protocol": "streamable-http",
  "status": "connected",
  "oauth": {
    "required": true,
    "authenticated": true,
    "token_expires_at": "2025-12-04T11:00:00Z",
    "has_refresh_token": true,
    "last_refresh_at": "2025-12-04T10:00:00Z",
    "flow_in_progress": false
  }
}
```

## No New Endpoints Required

The bug fixes and logging improvements are internal changes that don't require new API endpoints:

1. **Token Refresh**: Handled automatically by the OAuth transport layer
2. **Correlation IDs**: Logged to files, not exposed via API
3. **Flow Coordination**: Internal state management only
4. **Debug Logging**: Enabled via `--log-level=debug` flag

## SSE Events (Unchanged)

The `/events` SSE endpoint continues to emit `servers.changed` events when OAuth status changes:

```json
{
  "event": "servers.changed",
  "data": {
    "server": "oauth-test-server",
    "reason": "oauth_token_refreshed"
  }
}
```

New event reason values:
- `oauth_token_refreshed`: Token was automatically refreshed
- `oauth_flow_started`: New OAuth flow initiated
- `oauth_flow_completed`: OAuth flow completed successfully
- `oauth_flow_failed`: OAuth flow failed

## Internal Changes Summary

| Component | Change Type | Impact on API |
|-----------|-------------|---------------|
| Token Store | Bug fix | None |
| Flow Coordinator | New component | None |
| Correlation IDs | Logging only | None |
| Debug Logging | Enhanced | None |
| Browser Rate Limiting | Bug fix | None |
