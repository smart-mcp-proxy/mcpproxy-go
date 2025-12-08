# Data Model: Proactive OAuth Token Refresh & UX Improvements

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)
**Date**: 2025-12-07

## Overview

This document defines the data structures and entities for implementing proactive OAuth token refresh, logout functionality, and UX improvements. The design reuses existing storage infrastructure where possible.

---

## Existing Entities (No Changes)

### OAuthTokenRecord

**Location**: `internal/storage/models.go:64-78`

```go
// OAuthTokenRecord represents stored OAuth tokens for a server
type OAuthTokenRecord struct {
    ServerName   string    `json:"server_name"`
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    TokenType    string    `json:"token_type"`
    ExpiresAt    time.Time `json:"expires_at"`
    Scopes       []string  `json:"scopes,omitempty"`
    Created      time.Time `json:"created"`
    Updated      time.Time `json:"updated"`
    ClientID     string    `json:"client_id,omitempty"`
    ClientSecret string    `json:"client_secret,omitempty"`
}
```

**Usage**: Used by `RefreshManager` to track token expiration and schedule proactive refresh.

---

## New Entities

### OAuthStatus (Enum)

**Location**: `internal/oauth/status.go` (NEW)

```go
// OAuthStatus represents the current authentication state of an OAuth server.
type OAuthStatus string

const (
    // OAuthStatusNone indicates the server does not use OAuth.
    OAuthStatusNone OAuthStatus = "none"

    // OAuthStatusAuthenticated indicates valid OAuth token is available.
    OAuthStatusAuthenticated OAuthStatus = "authenticated"

    // OAuthStatusExpired indicates OAuth token has expired.
    OAuthStatusExpired OAuthStatus = "expired"

    // OAuthStatusError indicates OAuth authentication error.
    OAuthStatusError OAuthStatus = "error"
)

// CalculateOAuthStatus determines the OAuth status for a server.
func CalculateOAuthStatus(token *OAuthTokenRecord, lastError string) OAuthStatus {
    if token == nil {
        return OAuthStatusNone
    }
    if lastError != "" && strings.Contains(lastError, "OAuth") {
        return OAuthStatusError
    }
    if time.Now().After(token.ExpiresAt) {
        return OAuthStatusExpired
    }
    return OAuthStatusAuthenticated
}
```

### RefreshSchedule

**Location**: `internal/oauth/refresh_manager.go` (NEW)

```go
// RefreshSchedule tracks the proactive refresh state for a single server.
type RefreshSchedule struct {
    ServerName       string        // Unique server identifier (from OAuthTokenRecord.ServerName)
    ServerKey        string        // Hash key for storage lookup (GenerateServerKey output)
    ExpiresAt        time.Time     // When the current token expires
    ScheduledRefresh time.Time     // When proactive refresh is scheduled (80% of lifetime)
    RetryCount       int           // Number of refresh retry attempts (0-3)
    LastError        string        // Last refresh error message
    Timer            *time.Timer   // Background timer for scheduled refresh
}
```

### RefreshManager

**Location**: `internal/oauth/refresh_manager.go` (NEW)

```go
// RefreshManager coordinates proactive OAuth token refresh across all servers.
type RefreshManager struct {
    storage      *storage.BoltDB           // For loading/saving tokens
    coordinator  *OAuthFlowCoordinator     // For per-server mutex coordination
    runtime      RefreshRuntimeOperations  // For triggering token refresh
    eventEmitter RefreshEventEmitter       // For emitting SSE events
    schedules    map[string]*RefreshSchedule
    threshold    float64                   // Refresh at this percentage of lifetime (0.8)
    maxRetries   int                       // Maximum retry attempts (3)
    mu           sync.RWMutex
    logger       *zap.Logger
    ctx          context.Context
    cancel       context.CancelFunc
}

// RefreshRuntimeOperations defines runtime methods needed by RefreshManager.
type RefreshRuntimeOperations interface {
    RefreshOAuthToken(serverName string) error
    DisconnectServer(serverName string) error
}

// RefreshEventEmitter defines event emission methods.
type RefreshEventEmitter interface {
    EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time)
    EmitOAuthRefreshFailed(serverName string, errorMsg string)
}
```

### OAuthRefreshEvent

**Location**: `internal/runtime/events.go` (MODIFY)

```go
// Event types for OAuth refresh
const (
    EventTypeOAuthTokenRefreshed EventType = "oauth.token_refreshed"
    EventTypeOAuthRefreshFailed  EventType = "oauth.refresh_failed"
)

// OAuthRefreshEventData contains data for OAuth refresh events.
type OAuthRefreshEventData struct {
    ServerName string     `json:"server_name"`
    ExpiresAt  *time.Time `json:"expires_at,omitempty"` // Only for token_refreshed
    Error      string     `json:"error,omitempty"`      // Only for refresh_failed
}
```

---

## Extended Server Response

### Server (Extended)

**Location**: `internal/contracts/server.go` (MODIFY)

```go
// Server represents an upstream MCP server with OAuth status.
type Server struct {
    // ... existing fields ...

    // OAuth authentication status
    OAuthStatus    string     `json:"oauth_status,omitempty"`     // "authenticated", "expired", "error", "none"
    TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"` // ISO 8601 timestamp when authenticated
}
```

---

## Management Service Extension

### Service Interface (Extended)

**Location**: `internal/management/service.go` (MODIFY)

```go
type Service interface {
    // ... existing methods ...

    // TriggerOAuthLogout clears OAuth token and disconnects a specific server.
    // This operation respects disable_management and read_only configuration gates.
    // Emits "servers.changed" event on successful logout.
    // Returns error if server name is empty, server not found, config gates block operation,
    // or server doesn't support OAuth.
    TriggerOAuthLogout(ctx context.Context, name string) error

    // LogoutAllOAuth clears OAuth tokens for all OAuth-enabled servers.
    // Returns BulkOperationResult with success/failure counts.
    LogoutAllOAuth(ctx context.Context) (*BulkOperationResult, error)
}

type RuntimeOperations interface {
    // ... existing methods ...

    // TriggerOAuthLogout clears token and disconnects server.
    TriggerOAuthLogout(serverName string) error
}
```

---

## CLI Client Extension

### Client (Extended)

**Location**: `internal/cliclient/client.go` (MODIFY)

```go
// TriggerOAuthLogout initiates OAuth logout for a server.
func (c *Client) TriggerOAuthLogout(ctx context.Context, serverName string) error {
    // POST /api/v1/servers/{id}/logout
}
```

---

## REST API Models

### LogoutResponse

**Location**: `internal/httpapi/server.go` (inline)

```go
// LogoutResponse represents the response from logout endpoint.
// @Description Response from OAuth logout operation
type LogoutResponse struct {
    Action  string `json:"action" example:"logout"`
    Success bool   `json:"success" example:"true"`
    Server  string `json:"server" example:"sentry"`
}
```

### LogoutErrorResponse

```go
// LogoutErrorResponse represents error response from logout endpoint.
// @Description Error response from OAuth logout operation
type LogoutErrorResponse struct {
    Error string `json:"error" example:"server does not use OAuth"`
}
```

---

## Frontend Types

### ServerResponse (Extended)

**Location**: `frontend/src/types/contracts.ts` (MODIFY)

```typescript
interface ServerResponse {
  // ... existing fields ...

  oauth_status?: 'authenticated' | 'expired' | 'error' | 'none';
  token_expires_at?: string; // ISO 8601 timestamp
}
```

### SSE Event Types (Extended)

**Location**: `frontend/src/types/contracts.ts` (MODIFY)

```typescript
type SSEEventType =
  | 'servers.changed'
  | 'config.reloaded'
  | 'oauth.token_refreshed'
  | 'oauth.refresh_failed';

interface OAuthRefreshEvent {
  server_name: string;
  expires_at?: string;  // For token_refreshed
  error?: string;       // For refresh_failed
}
```

---

## State Transitions

### OAuth Status State Machine

```
                 ┌─────────────┐
                 │    none     │ (no OAuth configured)
                 └─────────────┘
                        │
                        │ OAuth configured
                        ▼
                 ┌─────────────┐
     ┌──────────▶│   error     │◀──────────┐
     │           └─────────────┘           │
     │                  │                  │
     │ OAuth error      │ Login success    │ Refresh failed
     │                  │                  │ (3 retries)
     │                  ▼                  │
     │           ┌─────────────┐           │
     └───────────│authenticated│───────────┘
                 └─────────────┘
                   │         ▲
    Token expired  │         │ Proactive refresh
    (80% lifetime) │         │ or Manual login
                   ▼         │
                 ┌─────────────┐
                 │   expired   │
                 └─────────────┘
```

### Refresh Manager Lifecycle

```
Application Start
        │
        ▼
┌───────────────────┐
│ Load all tokens   │ (ListOAuthTokens)
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ Schedule refresh  │ (for each non-expired token)
│ at 80% lifetime   │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ Wait for timers   │
└───────────────────┘
        │
        │ Timer fires
        ▼
┌───────────────────┐
│ Check coordinator │ (IsFlowActive?)
│ for active flow   │
└───────────────────┘
        │
        │ No active flow
        ▼
┌───────────────────┐
│ Attempt refresh   │
└───────────────────┘
        │
   ┌────┴────┐
   │         │
Success   Failure
   │         │
   ▼         ▼
┌──────┐ ┌──────────┐
│Emit  │ │Retry up  │
│event │ │to 3 times│
└──────┘ └──────────┘
   │         │
   │         │ Max retries
   ▼         ▼
┌──────────────────────┐
│ Reschedule for new   │
│ expiration           │
└──────────────────────┘
```

---

## Validation Rules

### TriggerOAuthLogout

| Field | Rule | Error Message |
|-------|------|---------------|
| serverName | Required, non-empty | "server name is required" |
| serverName | Must exist in config | "server not found" |
| server | Must have OAuth configured | "server does not use OAuth" |

### RefreshSchedule

| Field | Rule | Error Message |
|-------|------|---------------|
| ExpiresAt | Must be in future | Token already expired |
| RetryCount | 0-3 range | Reset on success |

---

## Configuration Options

### Refresh Manager Config

**Location**: `internal/config/config.go` (optional extension)

```go
type Config struct {
    // ... existing fields ...

    // OAuthRefreshThreshold is the percentage of token lifetime at which
    // proactive refresh is triggered. Default: 0.8 (80%)
    OAuthRefreshThreshold float64 `json:"oauth_refresh_threshold,omitempty"`

    // OAuthRefreshMaxRetries is the maximum number of refresh retry attempts.
    // Default: 3
    OAuthRefreshMaxRetries int `json:"oauth_refresh_max_retries,omitempty"`
}
```

---

## Storage Operations

### Required BoltDB Methods

All methods already exist in `internal/storage/bbolt.go`:

| Method | Description | Location |
|--------|-------------|----------|
| `ListOAuthTokens()` | Get all tokens for initialization | Line 449 |
| `GetOAuthToken(serverName)` | Get single token | Line 366 |
| `SaveOAuthToken(record)` | Save refreshed token | Line 352 |
| `DeleteOAuthToken(serverName)` | Clear token on logout | Line 384 |

---

## Error Types

### OAuth-specific Errors

```go
// ErrServerNotOAuth indicates server doesn't use OAuth authentication.
var ErrServerNotOAuth = errors.New("server does not use OAuth")

// ErrTokenExpired indicates OAuth token has expired.
var ErrTokenExpired = errors.New("OAuth token has expired")

// ErrRefreshFailed indicates token refresh failed after retries.
var ErrRefreshFailed = errors.New("OAuth token refresh failed")
```
