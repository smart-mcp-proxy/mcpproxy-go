---
id: rest-api
title: REST API
sidebar_label: REST API
sidebar_position: 1
description: MCPProxy REST API reference
keywords: [api, rest, http, endpoints]
---

# REST API

MCPProxy provides a REST API for server management and monitoring.

:::tip OpenAPI Specification
Interactive API documentation is available at [http://127.0.0.1:8080/swagger/](http://127.0.0.1:8080/swagger/) when MCPProxy is running. The OpenAPI spec file is also available at [`oas/swagger.yaml`](https://raw.githubusercontent.com/smart-mcp-proxy/mcpproxy-go/refs/heads/main/oas/swagger.yaml).
:::

## Authentication

All `/api/v1/*` endpoints require authentication via API key:

```bash
# Using X-API-Key header (recommended)
curl -H "X-API-Key: your-api-key" http://127.0.0.1:8080/api/v1/servers

# Using query parameter
curl "http://127.0.0.1:8080/api/v1/servers?apikey=your-api-key"
```

**Note:** Unix socket connections bypass API key authentication (OS-level auth).

## Base URL

```
http://127.0.0.1:8080/api/v1
```

## Endpoints

### Status

#### GET /api/v1/status

Get server status and statistics.

**Response:**
```json
{
  "status": "running",
  "version": "0.11.0",
  "uptime": 3600,
  "servers": {
    "total": 5,
    "connected": 4,
    "quarantined": 1
  },
  "tools": {
    "total": 42
  }
}
```

### Servers

#### GET /api/v1/servers

List all upstream servers with unified health status.

**Response:**
```json
{
  "success": true,
  "data": {
    "servers": [
      {
        "name": "github-server",
        "protocol": "http",
        "enabled": true,
        "connected": true,
        "quarantined": false,
        "tool_count": 15,
        "health": {
          "level": "healthy",
          "admin_state": "enabled",
          "summary": "Connected (15 tools)",
          "action": ""
        }
      },
      {
        "name": "oauth-server",
        "protocol": "http",
        "enabled": true,
        "connected": false,
        "quarantined": false,
        "tool_count": 0,
        "health": {
          "level": "unhealthy",
          "admin_state": "enabled",
          "summary": "Token expired",
          "detail": "OAuth access token has expired",
          "action": "login"
        }
      }
    ]
  }
}
```

**Health Object Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `level` | string | Health level: `healthy`, `degraded`, or `unhealthy` |
| `admin_state` | string | Admin state: `enabled`, `disabled`, or `quarantined` |
| `summary` | string | Human-readable status message |
| `detail` | string | Optional additional context about the status |
| `action` | string | Suggested remediation: `login`, `restart`, `enable`, `approve`, `view_logs`, or empty |

#### POST /api/v1/servers/{name}/enable

Enable a server.

#### POST /api/v1/servers/{name}/disable

Disable a server.

#### POST /api/v1/servers/{name}/quarantine

Set quarantine status.

**Request Body:**
```json
{
  "quarantined": true
}
```

#### POST /api/v1/servers/{name}/restart

Restart a server.

### Tools

#### GET /api/v1/tools

Search tools across all servers.

**Query Parameters:**
- `q` - Search query (optional)
- `limit` - Maximum results (default: 15)

**Response:**
```json
{
  "tools": [
    {
      "name": "github:create_issue",
      "server": "github-server",
      "description": "Create a new GitHub issue"
    }
  ]
}
```

#### GET /api/v1/servers/{name}/tools

List tools for a specific server.

### Real-time Updates

#### GET /events

Server-Sent Events (SSE) stream for live updates.

```bash
curl "http://127.0.0.1:8080/events?apikey=your-api-key"
```

Events include:
- `servers.changed` - Server status changed
- `config.reloaded` - Configuration reloaded
- `tools.indexed` - Tool index updated
- `activity.tool_call.started` - Tool call initiated
- `activity.tool_call.completed` - Tool call finished
- `activity.policy_decision` - Tool call blocked by policy

## Error Responses

```json
{
  "error": "error message",
  "code": "ERROR_CODE"
}
```

| Code | Description |
|------|-------------|
| 401 | Unauthorized - Invalid or missing API key |
| 404 | Not Found - Server or resource not found |
| 500 | Internal Server Error |

### Configuration

#### GET /api/v1/config

Get current configuration.

#### POST /api/v1/config/apply

Apply configuration changes.

#### POST /api/v1/config/validate

Validate configuration without applying.

### Diagnostics

#### GET /api/v1/diagnostics

Get system diagnostics.

#### GET /api/v1/doctor

Run health checks (same as `mcpproxy doctor` CLI).

#### GET /api/v1/info

Get application info, version, and update availability.

**Response:**
```json
{
  "success": true,
  "data": {
    "version": "v1.2.3",
    "web_ui_url": "http://127.0.0.1:8080/?apikey=xxx",
    "listen_addr": "127.0.0.1:8080",
    "endpoints": {
      "http": "127.0.0.1:8080",
      "socket": "/Users/user/.mcpproxy/mcpproxy.sock"
    },
    "update": {
      "available": true,
      "latest_version": "v1.3.0",
      "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0",
      "checked_at": "2025-01-15T10:30:00Z",
      "is_prerelease": false
    }
  }
}
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Current MCPProxy version |
| `web_ui_url` | string | URL to access the web control panel |
| `listen_addr` | string | Server listen address |
| `endpoints.http` | string | HTTP API endpoint address |
| `endpoints.socket` | string | Unix socket path (empty if disabled) |
| `update` | object | Update information (may be null if not checked yet) |
| `update.available` | boolean | Whether a newer version is available |
| `update.latest_version` | string | Latest version available on GitHub |
| `update.release_url` | string | URL to the GitHub release page |
| `update.checked_at` | string | ISO 8601 timestamp of last update check |
| `update.is_prerelease` | boolean | Whether the latest version is a prerelease |
| `update.check_error` | string | Error message if update check failed |

:::tip Update Checking
MCPProxy automatically checks for updates every 4 hours. The update information is exposed via this endpoint and used by the tray application and web UI to show update notifications.
:::

### Docker

#### GET /api/v1/docker/status

Get Docker isolation status.

### Secrets

#### GET /api/v1/secrets

List stored secrets.

#### GET /api/v1/secrets/{name}

Get secret metadata (not the value).

### Sessions

#### GET /api/v1/sessions

List active MCP sessions.

#### GET /api/v1/sessions/{id}

Get session details.

### Activity

Track and audit AI agent tool calls. See [Activity Log](../features/activity-log.md) for detailed documentation.

#### GET /api/v1/activity

List activity records with filtering and pagination.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by type: `tool_call`, `policy_decision`, `quarantine_change`, `server_change` |
| `server` | string | Filter by server name |
| `tool` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session ID |
| `status` | string | Filter by status: `success`, `error`, `blocked` |
| `start_time` | string | Filter after this time (RFC3339) |
| `end_time` | string | Filter before this time (RFC3339) |
| `limit` | integer | Max records (1-100, default: 50) |
| `offset` | integer | Pagination offset (default: 0) |

**Response:**
```json
{
  "success": true,
  "data": {
    "activities": [
      {
        "id": "01JFXYZ123ABC",
        "type": "tool_call",
        "server_name": "github-server",
        "tool_name": "create_issue",
        "status": "success",
        "duration_ms": 245,
        "timestamp": "2025-01-15T10:30:00Z"
      }
    ],
    "total": 150,
    "limit": 50,
    "offset": 0
  }
}
```

#### GET /api/v1/activity/{id}

Get full activity record details including request arguments and response data.

#### GET /api/v1/activity/export

Export activity records for compliance and auditing.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `format` | string | Export format: `json` (JSON Lines) or `csv` |
| *(filters)* | | Same filters as list endpoint |

**Example:**
```bash
# Export as JSON Lines
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=json"

# Export as CSV
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=csv"
```

### Bulk Operations

#### POST /api/v1/servers/enable_all

Enable all servers.

#### POST /api/v1/servers/disable_all

Disable all servers.

#### POST /api/v1/servers/restart_all

Restart all servers.

#### POST /api/v1/servers/reconnect

Reconnect all servers.

## OpenAPI Specification

The complete OpenAPI 3.1 specification is available at:
- `/swagger/` - Interactive Swagger UI
- `/swagger/swagger.yaml` - Raw specification

See `oas/swagger.yaml` in the repository for the complete API reference.
