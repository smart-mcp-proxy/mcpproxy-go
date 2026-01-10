# Data Model: OAuth Login Error Feedback

**Feature**: 020-oauth-login-feedback
**Date**: 2026-01-06

## Overview

This feature enhances the OAuth login response payload to include browser status, auth URL, and correlation ID. All clients (CLI, tray, Web UI) receive the same response structure. No database schema changes are required - these are API response structures.

---

## Entity: OAuthStartResponse

**Purpose**: Response from `POST /api/v1/servers/{id}/login` when OAuth flow is successfully started.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| success | boolean | Always `true` for successful start |
| server_name | string | Name of the server being authenticated |
| correlation_id | string (UUID) | Unique identifier for tracking this flow |
| auth_url | string | Authorization URL (always included for manual use) |
| browser_opened | boolean | Whether browser launch succeeded |
| browser_error | string (optional) | Error message if browser launch failed |
| message | string | Human-readable status message |

**Constraints**:
- `correlation_id` is a UUID generated at flow start
- `auth_url` is always populated for successful OAuth start
- `browser_opened=false` when `HEADLESS=true` or browser launch fails
- `browser_error` is only populated when `browser_opened=false`

**Example (browser opened)**:
```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "auth_url": "https://accounts.google.com/o/oauth2/auth?client_id=...",
  "browser_opened": true,
  "message": "OAuth flow started. Complete authorization in browser."
}
```

**Example (browser failed - headless)**:
```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "auth_url": "https://accounts.google.com/o/oauth2/auth?client_id=...",
  "browser_opened": false,
  "browser_error": "Headless mode - browser not available",
  "message": "OAuth flow started. Open the auth_url manually to complete authorization."
}
```

**Example (browser failed - error)**:
```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "auth_url": "https://accounts.google.com/o/oauth2/auth?client_id=...",
  "browser_opened": false,
  "browser_error": "xdg-open: command not found",
  "message": "OAuth flow started. Open the auth_url manually to complete authorization."
}
```

---

## Entity: OAuthValidationError

**Purpose**: Pre-flight validation failure details before OAuth is attempted. Returned with HTTP 400.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| success | boolean | Always `false` for validation errors |
| error_type | string | Category of validation failure |
| server_name | string | Requested server name |
| message | string | Human-readable error description |
| suggestion | string | Actionable remediation hint |
| available_servers | string[] (optional) | List of valid server names (for server_not_found) |
| correlation_id | string (optional) | Existing flow ID (for flow_in_progress) |

**Error Types**:
- `server_not_found` - Server name doesn't exist in configuration
- `server_disabled` - Server exists but is disabled
- `server_quarantined` - Server is in quarantine pending approval
- `oauth_not_supported` - Server protocol doesn't support OAuth (e.g., stdio)
- `flow_in_progress` - OAuth flow already active for this server

**Example (server not found)**:
```json
{
  "success": false,
  "error_type": "server_not_found",
  "server_name": "google-drvie",
  "message": "Server 'google-drvie' not found in configuration",
  "suggestion": "Check server name spelling. Did you mean 'google-drive'?",
  "available_servers": ["google-drive", "github-server", "slack-server"]
}
```

**Example (server disabled)**:
```json
{
  "success": false,
  "error_type": "server_disabled",
  "server_name": "google-drive",
  "message": "Server 'google-drive' is disabled",
  "suggestion": "Enable it first: mcpproxy upstream enable google-drive"
}
```

**Example (OAuth not supported)**:
```json
{
  "success": false,
  "error_type": "oauth_not_supported",
  "server_name": "local-script",
  "message": "Server 'local-script' uses stdio protocol which does not support OAuth",
  "suggestion": "OAuth is only available for HTTP/SSE servers"
}
```

**Example (flow in progress)**:
```json
{
  "success": false,
  "error_type": "flow_in_progress",
  "server_name": "google-drive",
  "message": "OAuth flow already in progress for 'google-drive'",
  "suggestion": "Wait for current flow to complete or check browser",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

---

## Entity: OAuthFlowError

**Purpose**: OAuth runtime error that occurs AFTER pre-flight validation passes but BEFORE browser opens. Examples: metadata discovery failure, DCR failure, authorization URL construction failure.

**Attributes**:

| Field | Type | Description |
|-------|------|-------------|
| success | boolean | Always `false` |
| error_type | string | Category of OAuth runtime failure |
| error_code | string | Machine-readable error code (e.g., OAUTH_NO_METADATA) |
| server_name | string | Server that failed OAuth |
| correlation_id | string (UUID) | Flow tracking ID for log correlation |
| request_id | string | HTTP request ID (from PR #237) |
| message | string | Human-readable error description |
| details | OAuthErrorDetails (optional) | Structured discovery/failure details |
| suggestion | string | Actionable remediation hint |
| debug_hint | string | CLI command for log lookup |

**Error Types**:

| error_type | error_code | Condition |
|------------|------------|-----------|
| `oauth_metadata_missing` | `OAUTH_NO_METADATA` | Auth server /.well-known/oauth-authorization-server returns 404 |
| `oauth_metadata_invalid` | `OAUTH_BAD_METADATA` | Metadata is malformed or missing required fields |
| `oauth_resource_mismatch` | `OAUTH_RESOURCE_MISMATCH` | Protected resource URL != MCP server URL (RFC 9728) |
| `oauth_client_id_required` | `OAUTH_NO_CLIENT_ID` | DCR failed (403) and no static client_id configured |
| `oauth_dcr_failed` | `OAUTH_DCR_FAILED` | DCR failed with unexpected error |
| `oauth_flow_failed` | `OAUTH_FLOW_FAILED` | Generic OAuth flow failure (panic recovered) |

**Sub-entity: OAuthErrorDetails**:

| Field | Type | Description |
|-------|------|-------------|
| server_url | string | The MCP server URL |
| protected_resource_metadata | MetadataStatus (optional) | Status of protected resource discovery |
| authorization_server_metadata | MetadataStatus (optional) | Status of auth server discovery |
| dcr_status | DCRStatus (optional) | Status of Dynamic Client Registration |

**Sub-entity: MetadataStatus**:

| Field | Type | Description |
|-------|------|-------------|
| found | boolean | Whether metadata was successfully retrieved |
| url_checked | string | The URL that was queried |
| error | string (optional) | Error message if discovery failed |
| authorization_servers | string[] (optional) | List of discovered auth servers |

**Sub-entity: DCRStatus**:

| Field | Type | Description |
|-------|------|-------------|
| attempted | boolean | Whether DCR was attempted |
| success | boolean | Whether DCR succeeded |
| status_code | int (optional) | HTTP status code from DCR request |
| error | string (optional) | Error message if DCR failed |

**Example (metadata missing - Smithery case)**:
```json
{
  "success": false,
  "error_type": "oauth_metadata_missing",
  "error_code": "OAUTH_NO_METADATA",
  "server_name": "googledrive-smithery",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "request_id": "req-xyz-123",
  "message": "OAuth authorization server metadata not available",
  "details": {
    "server_url": "https://server.smithery.ai/googledrive",
    "protected_resource_metadata": {
      "found": true,
      "url_checked": "https://server.smithery.ai/.well-known/oauth-protected-resource/googledrive",
      "authorization_servers": ["https://auth.smithery.ai/googledrive"]
    },
    "authorization_server_metadata": {
      "found": false,
      "url_checked": "https://auth.smithery.ai/googledrive/.well-known/oauth-authorization-server",
      "error": "HTTP 404 Not Found"
    }
  },
  "suggestion": "The OAuth authorization server is not properly configured. Contact the server administrator.",
  "debug_hint": "For logs: mcpproxy upstream logs googledrive-smithery | grep a1b2c3d4"
}
```

**Example (client_id required - Figma case)**:
```json
{
  "success": false,
  "error_type": "oauth_client_id_required",
  "error_code": "OAUTH_NO_CLIENT_ID",
  "server_name": "Figma",
  "correlation_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "request_id": "req-abc-456",
  "message": "Server requires client_id but Dynamic Client Registration returned 403",
  "details": {
    "server_url": "https://mcp.figma.com/mcp",
    "dcr_status": {
      "attempted": true,
      "success": false,
      "status_code": 403,
      "error": "Forbidden"
    }
  },
  "suggestion": "Register an OAuth app with Figma and configure oauth.client_id in server config.",
  "debug_hint": "For logs: mcpproxy upstream logs Figma | grep b2c3d4e5"
}
```

---

## Multi-Client Response Handling

All clients receive the same `OAuthStartResponse` and handle it according to their UI:

| Client | browser_opened=true | browser_opened=false |
|--------|--------------------|--------------------|
| CLI | Print "Opening browser..." | Print auth_url for manual copy |
| Tray | Show notification | Show notification with clickable URL |
| Web UI | Show "Check browser" toast | Display auth_url in modal |

---

## OAuth Completion Detection

Clients detect OAuth completion via existing mechanisms:

1. **SSE Events**: Subscribe to `/events`, watch for `servers.changed` event
2. **Polling**: Call `GET /api/v1/servers` and check server's OAuth status
3. **Server Status**: Check `oauth_authenticated` field in server response

No new SSE event types are added - the existing `servers.changed` event fires when OAuth completes.

---

## Relationships

```
┌─────────────────────┐
│  Client Request     │
│  POST /login        │
└──────────┬──────────┘
           │
           ▼
┌─────────────────────┐
│  Pre-flight         │
│  Validation         │
└──────────┬──────────┘
           │
     ┌─────┴─────┐
     │           │
     ▼           ▼
┌─────────┐  ┌──────────────────┐
│ Error   │  │ Start OAuth      │
│ 400     │  │ Open Browser     │
└─────────┘  └──────────┬───────┘
                        │
                        ▼
             ┌──────────────────┐
             │ OAuthStartResponse│
             │ 200              │
             └──────────┬───────┘
                        │
          ┌─────────────┴─────────────┐
          │                           │
          ▼                           ▼
   browser_opened=true         browser_opened=false
   (Client shows               (Client shows
    "Check browser")            auth_url)
```

---

## Go Implementation

```go
// OAuthStartResponse is returned by POST /api/v1/servers/{id}/login
type OAuthStartResponse struct {
    Success       bool   `json:"success"`
    ServerName    string `json:"server_name"`
    CorrelationID string `json:"correlation_id"`
    AuthURL       string `json:"auth_url,omitempty"`
    BrowserOpened bool   `json:"browser_opened"`
    BrowserError  string `json:"browser_error,omitempty"`
    Message       string `json:"message"`
}

// OAuthValidationError is returned for pre-flight validation failures
type OAuthValidationError struct {
    Success          bool     `json:"success"` // Always false
    ErrorType        string   `json:"error_type"`
    ServerName       string   `json:"server_name"`
    Message          string   `json:"message"`
    Suggestion       string   `json:"suggestion"`
    AvailableServers []string `json:"available_servers,omitempty"`
    CorrelationID    string   `json:"correlation_id,omitempty"` // For flow_in_progress
}

// OAuthFlowError is returned for OAuth runtime failures (after validation passes)
type OAuthFlowError struct {
    Success       bool              `json:"success"`        // Always false
    ErrorType     string            `json:"error_type"`     // e.g., "oauth_metadata_missing"
    ErrorCode     string            `json:"error_code"`     // e.g., "OAUTH_NO_METADATA"
    ServerName    string            `json:"server_name"`
    CorrelationID string            `json:"correlation_id"`
    RequestID     string            `json:"request_id"`     // From PR #237
    Message       string            `json:"message"`
    Details       *OAuthErrorDetails `json:"details,omitempty"`
    Suggestion    string            `json:"suggestion"`
    DebugHint     string            `json:"debug_hint"`
}

// OAuthErrorDetails contains structured discovery/failure details
type OAuthErrorDetails struct {
    ServerURL                   string          `json:"server_url"`
    ProtectedResourceMetadata   *MetadataStatus `json:"protected_resource_metadata,omitempty"`
    AuthorizationServerMetadata *MetadataStatus `json:"authorization_server_metadata,omitempty"`
    DCRStatus                   *DCRStatus      `json:"dcr_status,omitempty"`
}

// MetadataStatus represents the status of OAuth metadata discovery
type MetadataStatus struct {
    Found               bool     `json:"found"`
    URLChecked          string   `json:"url_checked"`
    Error               string   `json:"error,omitempty"`
    AuthorizationServers []string `json:"authorization_servers,omitempty"`
}

// DCRStatus represents the status of Dynamic Client Registration
type DCRStatus struct {
    Attempted  bool   `json:"attempted"`
    Success    bool   `json:"success"`
    StatusCode int    `json:"status_code,omitempty"`
    Error      string `json:"error,omitempty"`
}

// OAuth error type constants
const (
    OAuthErrorMetadataMissing   = "oauth_metadata_missing"
    OAuthErrorMetadataInvalid   = "oauth_metadata_invalid"
    OAuthErrorResourceMismatch  = "oauth_resource_mismatch"
    OAuthErrorClientIDRequired  = "oauth_client_id_required"
    OAuthErrorDCRFailed         = "oauth_dcr_failed"
    OAuthErrorFlowFailed        = "oauth_flow_failed"
)

// OAuth error code constants
const (
    OAuthCodeNoMetadata       = "OAUTH_NO_METADATA"
    OAuthCodeBadMetadata      = "OAUTH_BAD_METADATA"
    OAuthCodeResourceMismatch = "OAUTH_RESOURCE_MISMATCH"
    OAuthCodeNoClientID       = "OAUTH_NO_CLIENT_ID"
    OAuthCodeDCRFailed        = "OAUTH_DCR_FAILED"
    OAuthCodeFlowFailed       = "OAUTH_FLOW_FAILED"
)
```

---

## Storage Notes

- **No database changes**: Response structures are transient
- **Correlation ID**: Generated for logging correlation, not persisted
- **OAuth tokens**: Already persisted via existing mechanisms in upstream client
- **Flow tracking**: Single active flow per server tracked in memory (existing behavior)
