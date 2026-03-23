# Data Model: Native macOS Swift Tray App

**Feature**: 037-macos-swift-tray
**Date**: 2026-03-23

## Overview

The Swift tray app is a stateless UI client. It holds no persistent storage — all state is fetched from the MCPProxy core via REST API and kept current via SSE events. The models below represent the in-memory state used to drive the SwiftUI menu.

## Entities

### AppState (Root Observable)

The single source of truth for the tray menu. Observed by SwiftUI for automatic re-rendering.

| Field | Type | Description |
|-------|------|-------------|
| coreState | CoreState | Current state of core process lifecycle |
| servers | [ServerStatus] | All configured upstream servers |
| recentActivity | [ActivityEntry] | Last 3 activity entries |
| sensitiveDataAlerts | Int | Count of sensitive data findings in last hour |
| quarantinedToolsCount | Int | Total tools pending approval across all servers |
| serversNeedingAttention | [ServerStatus] | Servers with health.action set |
| version | String | MCPProxy version (e.g., "0.21.3") |
| connectedCount | Int | Number of connected servers |
| totalServers | Int | Total configured servers |
| totalTools | Int | Total available tools |
| updateAvailable | String? | New version string, nil if current |
| autoStartEnabled | Bool | Login item registration state |

### CoreState (State Machine)

| State | Description | Transitions To |
|-------|-------------|----------------|
| launching | Starting core binary | waitingForCore, error |
| waitingForCore | Polling for socket readiness | connected, error |
| connected | SSE streaming, fully operational | reconnecting, shuttingDown |
| reconnecting | Lost SSE/socket, retrying | connected, error |
| error(ErrorKind) | Specific failure with remediation | launching (retry), shuttingDown |
| shuttingDown | Graceful shutdown in progress | (terminal) |

### ErrorKind

| Value | Exit Code | Description |
|-------|-----------|-------------|
| portConflict | 2 | Port already in use |
| databaseLocked | 3 | Another instance holds DB |
| configError | 4 | Invalid configuration |
| permissionError | 5 | Data directory permissions |
| general | other | Unexpected failure |

### ServerStatus

Decoded from `GET /api/v1/servers` response.

| Field | Type | Description |
|-------|------|-------------|
| id | String | Server identifier |
| name | String | Display name |
| protocol | String | "stdio" or "http" |
| url | String? | Endpoint URL (http servers) |
| enabled | Bool | Administrative state |
| connected | Bool | Connection state |
| quarantined | Bool | Quarantine state |
| toolCount | Int | Number of available tools |
| pendingApprovalCount | Int | Tools awaiting approval |
| health | HealthStatus | Unified health info |
| oauthStatus | OAuthStatus? | OAuth state if applicable |

### HealthStatus

| Field | Type | Description |
|-------|------|-------------|
| level | HealthLevel | healthy, degraded, unhealthy |
| adminState | AdminState | enabled, disabled, quarantined |
| summary | String | Human-readable status |
| detail | String? | Additional context |
| action | HealthAction? | Suggested remediation |

### HealthLevel (Enum)
`healthy` | `degraded` | `unhealthy`

### AdminState (Enum)
`enabled` | `disabled` | `quarantined`

### HealthAction (Enum)
`login` | `restart` | `enable` | `approve` | `viewLogs` | `setSecret` | `configure`

### OAuthStatus

| Field | Type | Description |
|-------|------|-------------|
| authenticated | Bool | Has valid token |
| expiresAt | Date? | Token expiration |
| provider | String? | OAuth provider name |

### ActivityEntry

Decoded from `GET /api/v1/activity?limit=3` response.

| Field | Type | Description |
|-------|------|-------------|
| id | String | Activity record ID |
| type | String | tool_call, quarantine_change, etc. |
| serverName | String | Upstream server name |
| toolName | String? | Tool name (for tool_call type) |
| status | ActivityStatus | success, error, blocked |
| duration | Int? | Duration in milliseconds |
| timestamp | Date | When the event occurred |
| hasSensitiveData | Bool | Sensitive data detected flag |

### ActivityStatus (Enum)
`success` | `error` | `blocked`

### SSEEvent

Parsed from the `/events` SSE stream.

| Variant | Payload | Action |
|---------|---------|--------|
| status | StatusUpdate | Update version, listen addr, upstream stats |
| serversChanged | none | Trigger full server list refresh |
| configReloaded | none | Trigger full state refresh |
| ping | timestamp | No-op (keepalive) |

### StatusUpdate

| Field | Type | Description |
|-------|------|-------------|
| running | Bool | Core is operational |
| listenAddr | String | Current listen address |
| upstreamStats | UpstreamStats | Server/tool counts |

### UpstreamStats

| Field | Type | Description |
|-------|------|-------------|
| total | Int | Total configured servers |
| connected | Int | Currently connected |
| tools | Int | Total available tools |

### NotificationEvent

Internal model for notification rate limiting.

| Field | Type | Description |
|-------|------|-------------|
| type | NotificationType | Event category |
| serverName | String | Source server |
| message | String | Notification body |
| timestamp | Date | When the event occurred |
| action | NotificationAction? | Actionable button |

### NotificationType (Enum)
`sensitiveData` | `quarantine` | `oauthExpiring` | `coreCrash` | `updateAvailable` | `updateInstalled` | `connectionLost`

### NotificationAction (Enum)
`reviewTools` | `reAuthenticate` | `restart` | `installUpdate` | `viewDetails`

## Relationships

```
AppState
  ├── CoreState (1:1)
  ├── ServerStatus (1:N)
  │   ├── HealthStatus (1:1)
  │   └── OAuthStatus (0:1)
  ├── ActivityEntry (1:N, max 3)
  └── NotificationEvent (tracked for rate limiting)

SSEEvent → triggers AppState updates
CoreProcessManager → manages CoreState transitions
APIClient → fetches ServerStatus[], ActivityEntry[]
NotificationService → consumes NotificationEvent, enforces rate limits
```

## State Transitions

### Core Process Lifecycle

```
[launch] → launching
launching → waitingForCore (process started)
launching → error (process failed to start, max 3 retries)
waitingForCore → connected (socket responds + SSE connected)
waitingForCore → error (90s timeout)
connected → reconnecting (SSE drops / socket error)
connected → shuttingDown (user quits)
reconnecting → connected (SSE reconnected)
reconnecting → error (max 10 retries)
error → launching (user clicks retry / auto-retry)
error → shuttingDown (user quits)
shuttingDown → (exit)
```
