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

List all upstream servers.

**Response:**
```json
{
  "servers": [
    {
      "name": "github-server",
      "protocol": "http",
      "enabled": true,
      "connected": true,
      "quarantined": false,
      "tools_count": 15
    }
  ]
}
```

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

Get application info and version.

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
