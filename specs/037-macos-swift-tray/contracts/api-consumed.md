# API Contracts Consumed by Swift Tray App

**Feature**: 037-macos-swift-tray
**Date**: 2026-03-23

## Overview

The Swift tray app is a consumer of the existing MCPProxy REST API. It does not introduce new endpoints. This document lists all API endpoints the tray app calls, grouped by feature area.

## Authentication

All requests go over Unix socket (`~/.mcpproxy/mcpproxy.sock`). Socket connections bypass API key authentication (OS-level auth via file permissions).

Fallback: TCP to `127.0.0.1:8080` with `X-API-Key` header (only for external-attached mode).

## Endpoints Used

### Health & Status

| Method | Path | Purpose | Polling |
|--------|------|---------|---------|
| GET | `/ready` | Check if core is ready (startup) | Poll every 500ms during waitingForCore |
| GET | `/api/v1/status` | Overall status, version, uptime | On SSE reconnect |
| GET | `/api/v1/info` | Version, update availability | On launch + after SSE reconnect |

### Server-Sent Events

| Method | Path | Purpose | Connection |
|--------|------|---------|------------|
| GET | `/events` | Real-time status/event stream | Persistent (reconnect on drop) |

**Event types consumed**:
- `status` → update AppState.version, connectedCount, totalServers, totalTools
- `servers.changed` → trigger `GET /api/v1/servers` refresh
- `config.reloaded` → trigger full state refresh
- `ping` → no-op (keepalive)

### Server Management

| Method | Path | Purpose | Trigger |
|--------|------|---------|---------|
| GET | `/api/v1/servers` | List all servers with health | On launch, on `servers.changed` SSE |
| POST | `/api/v1/servers/{id}/enable` | Enable a server | Menu action |
| POST | `/api/v1/servers/{id}/disable` | Disable a server | Menu action |
| POST | `/api/v1/servers/{id}/restart` | Restart connection | Menu action |
| POST | `/api/v1/servers/{id}/login` | Initiate OAuth flow | Menu action |
| POST | `/api/v1/servers/{id}/quarantine` | Quarantine a server | Menu action |
| POST | `/api/v1/servers/{id}/unquarantine` | Unquarantine a server | Menu action |
| POST | `/api/v1/servers/{id}/tools/approve` | Approve pending tools | Menu action |

### Activity Log

| Method | Path | Purpose | Polling |
|--------|------|---------|---------|
| GET | `/api/v1/activity?limit=3` | Last 3 activity entries | Every 10s when menu is open |
| GET | `/api/v1/activity?sensitive_data=true&limit=1` | Check for sensitive data findings | Every 30s |
| GET | `/api/v1/activity/summary` | 24h statistics | On launch |

### Diagnostics

| Method | Path | Purpose | Trigger |
|--------|------|---------|---------|
| GET | `/api/v1/diagnostics` | Health checks | On error state |

## Response Models

All models are documented in [data-model.md](../data-model.md). Key Codable types:

- `ServerStatus` ← `GET /api/v1/servers` array items
- `HealthStatus` ← nested in ServerStatus.health
- `ActivityEntry` ← `GET /api/v1/activity` array items
- `StatusUpdate` ← SSE `status` event data
- `InfoResponse` ← `GET /api/v1/info`

## Error Handling

All API errors return JSON:
```json
{
  "error": "description",
  "request_id": "uuid"
}
```

The tray app:
- Retries transient errors (5xx, timeouts) up to 3 times with 1s backoff
- Shows user-facing errors for 4xx responses in the menu status line
- Logs all errors to `~/Library/Logs/mcpproxy/tray.log`
