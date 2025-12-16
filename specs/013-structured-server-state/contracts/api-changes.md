# API Contract Changes: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-13
**Updated**: 2025-12-16

## Implementation Status

| Change | Status | Notes |
|--------|--------|-------|
| `health` field on Server | ✅ DONE | Merged in #192 |
| `oauth_state` field on Server | ❌ TODO | Proposed in this doc |
| `connection_state` field on Server | ❌ TODO | Proposed in this doc |
| Diagnostics aggregation from Health | ❌ TODO | Doctor() still uses raw fields |

## Overview

This document describes the API changes for the structured server state feature. All changes are **additive** - existing fields remain unchanged for backwards compatibility.

## Affected Endpoints

| Endpoint | Change Type | Description |
|----------|-------------|-------------|
| `GET /api/v1/servers` | Additive | Add `oauth_state`, `connection_state` to each server |
| `GET /api/v1/diagnostics` | Modified | Aggregate from `server.Health` instead of separate logic |
| `GET /events` (SSE) | No change | Existing `servers.changed` events include updated data |

## Schema Changes

### Server Object (Additive)

**New Fields**:

```yaml
Server:
  type: object
  properties:
    # ... existing properties unchanged ...

    oauth_state:
      $ref: '#/components/schemas/OAuthState'
      description: OAuth authentication state (only present if OAuth configured)
      nullable: true

    connection_state:
      $ref: '#/components/schemas/ConnectionState'
      description: Connection state (always present)
```

### New Schema: OAuthState

```yaml
OAuthState:
  type: object
  required:
    - status
    - retry_count
    - user_logged_out
    - has_refresh_token
  properties:
    status:
      type: string
      enum: [authenticated, expired, error, none]
      description: Current OAuth authentication status
    token_expires_at:
      type: string
      format: date-time
      description: When the access token expires (ISO 8601)
    last_attempt:
      type: string
      format: date-time
      description: When the last OAuth flow was attempted
    retry_count:
      type: integer
      minimum: 0
      description: Number of OAuth retry attempts
    user_logged_out:
      type: boolean
      description: True if the user explicitly logged out
    has_refresh_token:
      type: boolean
      description: True if auto-refresh is possible
    error:
      type: string
      description: Last OAuth error message (if any)
```

### New Schema: ConnectionState

```yaml
ConnectionState:
  type: object
  required:
    - status
    - retry_count
    - should_retry
  properties:
    status:
      type: string
      enum: [disconnected, connecting, ready, error]
      description: Current connection status
    connected_at:
      type: string
      format: date-time
      description: When the connection was established
    last_error:
      type: string
      description: Last connection error message
    retry_count:
      type: integer
      minimum: 0
      description: Number of connection retry attempts
    last_retry_at:
      type: string
      format: date-time
      description: When the last retry was attempted
    should_retry:
      type: boolean
      description: True if a retry is pending based on backoff
```

## Example Response

### GET /api/v1/servers

```json
{
  "success": true,
  "data": {
    "servers": [
      {
        "id": "abc123",
        "name": "github-server",
        "enabled": true,
        "quarantined": false,

        "oauth_state": {
          "status": "authenticated",
          "token_expires_at": "2025-12-14T12:00:00Z",
          "last_attempt": "2025-12-13T10:00:00Z",
          "retry_count": 0,
          "user_logged_out": false,
          "has_refresh_token": true,
          "error": null
        },

        "connection_state": {
          "status": "ready",
          "connected_at": "2025-12-13T10:01:00Z",
          "last_error": null,
          "retry_count": 0,
          "last_retry_at": null,
          "should_retry": false
        },

        "health": {
          "level": "healthy",
          "admin_state": "enabled",
          "summary": "Connected (5 tools)",
          "detail": null,
          "action": ""
        },

        "authenticated": true,
        "oauth_status": "authenticated",
        "connected": true,
        "last_error": "",
        "tool_count": 5
      }
    ],
    "stats": { ... }
  }
}
```

### GET /api/v1/diagnostics

**Response structure unchanged** - Diagnostics is populated by aggregating `server.Health`:

```json
{
  "success": true,
  "data": {
    "total_issues": 2,
    "upstream_errors": [
      {
        "server_name": "failing-server",
        "error_message": "Connection refused",
        "timestamp": "2025-12-13T11:00:00Z"
      }
    ],
    "oauth_required": [
      {
        "server_name": "oauth-server",
        "state": "expired",
        "message": "Run: mcpproxy auth login --server=oauth-server"
      }
    ],
    "oauth_issues": [],
    "missing_secrets": [],
    "runtime_warnings": [],
    "docker_status": {
      "available": true,
      "version": "24.0.5"
    },
    "timestamp": "2025-12-13T11:30:00Z"
  }
}
```

## Backwards Compatibility

| Existing Field | Status | Notes |
|----------------|--------|-------|
| `authenticated` | ✅ KEPT | Returns same value as `oauth_state.status == "authenticated"` |
| `oauth_status` | ✅ KEPT | Returns same value as `oauth_state.status` |
| `connected` | ✅ KEPT | Returns same value as `connection_state.status == "ready"` |
| `connecting` | ✅ KEPT | Returns same value as `connection_state.status == "connecting"` |
| `last_error` | ✅ KEPT | Returns same value as `connection_state.last_error` |
| `reconnect_count` | ✅ KEPT | Returns same value as `connection_state.retry_count` |
| `should_retry` | ✅ KEPT | Returns same value as `connection_state.should_retry` |
| `health` | ✅ IMPLEMENTED | Added in #192, fully functional |

## Migration Path

1. **Phase 1**: Add new fields alongside existing (this feature)
2. **Phase 2** (future): Deprecation warnings in API docs
3. **Phase 3** (future): Remove flat fields in major version bump
