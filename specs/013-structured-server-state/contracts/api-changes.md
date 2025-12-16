# API Contract Changes: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

This document describes API changes for adding structured state objects. All changes are **additive** - existing fields remain unchanged for backwards compatibility.

## Affected Endpoints

| Endpoint | Change Type | Description |
|----------|-------------|-------------|
| `GET /api/v1/servers` | Additive | Add `oauth_state`, `connection_state` to each server |
| `GET /api/v1/diagnostics` | Modified | Aggregate from `server.Health` instead of raw fields |

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
        "name": "github-server",
        "enabled": true,

        "oauth_state": {
          "status": "authenticated",
          "token_expires_at": "2025-12-14T12:00:00Z",
          "last_attempt": "2025-12-13T10:00:00Z",
          "retry_count": 0,
          "user_logged_out": false,
          "has_refresh_token": true
        },

        "connection_state": {
          "status": "ready",
          "connected_at": "2025-12-13T10:01:00Z",
          "retry_count": 0,
          "should_retry": false
        },

        "health": {
          "level": "healthy",
          "admin_state": "enabled",
          "summary": "Connected (5 tools)"
        },

        "authenticated": true,
        "connected": true,
        "tool_count": 5
      }
    ]
  }
}
```

## Backwards Compatibility

All existing flat fields remain unchanged:

| Field | Consistent With |
|-------|-----------------|
| `authenticated` | `oauth_state.status == "authenticated"` |
| `oauth_status` | `oauth_state.status` |
| `connected` | `connection_state.status == "ready"` |
| `connecting` | `connection_state.status == "connecting"` |
| `last_error` | `connection_state.last_error` |
| `reconnect_count` | `connection_state.retry_count` |
| `should_retry` | `connection_state.should_retry` |

## Migration Path

1. **Phase 1** (this feature): Add new fields alongside existing
2. **Phase 2** (future): Deprecation warnings in API docs
3. **Phase 3** (future): Remove flat fields in major version bump
