# API Contract Changes: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

This document describes API changes for making Health the single source of truth. Changes are minimal - only extending the `action` enum in HealthStatus.

## Affected Endpoints

| Endpoint | Change Type | Description |
|----------|-------------|-------------|
| `GET /api/v1/servers` | Extended | Health.action has new values |
| `GET /api/v1/diagnostics` | Internal | Now aggregates from Health (no schema change) |

## Schema Changes

### HealthStatus.action (Extended)

**Current values**: `login`, `restart`, `enable`, `approve`, `view_logs`, `""`

**New values**: `set_secret`, `configure`

```yaml
HealthStatus:
  type: object
  properties:
    action:
      type: string
      enum: [login, restart, enable, approve, view_logs, set_secret, configure, ""]
      description: |
        Suggested action to fix the issue:
        - login: Trigger OAuth authentication
        - restart: Restart the server
        - enable: Enable the disabled server
        - approve: Approve the quarantined server
        - view_logs: View server logs for details
        - set_secret: Navigate to secrets page to set missing secret
        - configure: Navigate to server config to fix configuration
        - "": No action needed (healthy)
```

## New Action Examples

### Missing Secret

```json
{
  "health": {
    "level": "unhealthy",
    "admin_state": "enabled",
    "summary": "Missing secret",
    "detail": "GITHUB_TOKEN",
    "action": "set_secret"
  }
}
```

### OAuth Config Issue

```json
{
  "health": {
    "level": "unhealthy",
    "admin_state": "enabled",
    "summary": "OAuth configuration error",
    "detail": "requires 'resource' parameter",
    "action": "configure"
  }
}
```

## Diagnostics Changes

### Before (Independent Detection)

Doctor() independently detected issues by checking raw server fields.

### After (Aggregated from Health)

Doctor() iterates servers and groups by `Health.Action`:

| Health.Action | Diagnostics Field |
|---------------|-------------------|
| `restart` | `upstream_errors` |
| `login` | `oauth_required` |
| `configure` | `oauth_issues` |
| `set_secret` | `missing_secrets` (grouped by secret name) |

**No schema change** - Diagnostics response structure remains the same. Only the internal implementation changes.

## Backwards Compatibility

- All existing `action` values unchanged
- New `action` values are additive
- Clients that don't handle `set_secret` or `configure` will see them as unknown actions (graceful degradation)
- Diagnostics response schema unchanged

## Frontend Action Mapping

| Action | Button Text | Behavior |
|--------|-------------|----------|
| `login` | Login | Trigger OAuth flow |
| `restart` | Restart | Restart server |
| `enable` | Enable | Enable server |
| `approve` | Approve | Unquarantine server |
| `view_logs` | View Logs | Navigate to `/servers/{name}?tab=logs` |
| `set_secret` | Set Secret | Navigate to `/secrets` |
| `configure` | Configure | Navigate to `/servers/{name}?tab=config` |
