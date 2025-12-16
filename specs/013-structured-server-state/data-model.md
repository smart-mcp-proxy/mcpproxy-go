# Data Model: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-13
**Updated**: 2025-12-16

## Implementation Status

| Entity | Status | Location |
|--------|--------|----------|
| `HealthStatus` | ✅ DONE | `internal/contracts/types.go:568-583` |
| `OAuthState` | ❌ TODO | Proposed in this doc |
| `ConnectionState` | ❌ TODO | Proposed in this doc |

## Entity Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                          Server                                  │
├─────────────────────────────────────────────────────────────────┤
│ id: string                                                       │
│ name: string                                                     │
│ enabled: bool                                                    │
│ quarantined: bool                                                │
│                                                                  │
│ // NEW: Structured state objects                                 │
│ oauth_state: OAuthState?          ←───────────────────┐         │
│ connection_state: ConnectionState ←───────────────┐   │         │
│                                                    │   │         │
│ // KEEP: Flat fields (backwards compat)           │   │         │
│ authenticated: bool                               │   │         │
│ oauth_status: string                              │   │         │
│ connected: bool                                   │   │         │
│ last_error: string                                │   │         │
│ ...                                               │   │         │
│                                                   │   │         │
│ // Calculated                                     │   │         │
│ health: HealthStatus ←────────────────────────────┴───┘         │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                      OAuthState                                  │
├─────────────────────────────────────────────────────────────────┤
│ status: string          // authenticated|expired|error|none      │
│ token_expires_at: time? // ISO 8601 timestamp                    │
│ last_attempt: time?     // Last OAuth attempt timestamp          │
│ retry_count: int        // Number of OAuth retry attempts        │
│ user_logged_out: bool   // User explicitly logged out            │
│ has_refresh_token: bool // Can auto-refresh token                │
│ error: string?          // Last OAuth error message              │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    ConnectionState                               │
├─────────────────────────────────────────────────────────────────┤
│ status: string          // disconnected|connecting|ready|error   │
│ connected_at: time?     // When connection established           │
│ last_error: string?     // Last connection error                 │
│ retry_count: int        // Number of retry attempts              │
│ last_retry_at: time?    // Last retry timestamp                  │
│ should_retry: bool      // Whether retry is pending              │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     HealthStatus  ✅ IMPLEMENTED                 │
├─────────────────────────────────────────────────────────────────┤
│ level: string           // healthy|degraded|unhealthy            │
│ admin_state: string     // enabled|disabled|quarantined          │
│ summary: string         // Human-readable status                 │
│ detail: string?         // Extended explanation                  │
│ action: string?         // login|restart|enable|approve|view_logs│
└─────────────────────────────────────────────────────────────────┘
```

## New Types (Go)

### OAuthState

```go
// OAuthState represents the OAuth authentication state for a server.
// Present only on servers with OAuth configured.
type OAuthState struct {
    // Status indicates the OAuth state: "authenticated", "expired", "error", "none"
    Status string `json:"status"`

    // TokenExpiresAt is when the access token expires (ISO 8601)
    TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`

    // LastAttempt is when the last OAuth flow was attempted
    LastAttempt *time.Time `json:"last_attempt,omitempty"`

    // RetryCount is the number of OAuth retry attempts
    RetryCount int `json:"retry_count"`

    // UserLoggedOut is true if the user explicitly logged out
    UserLoggedOut bool `json:"user_logged_out"`

    // HasRefreshToken indicates whether auto-refresh is possible
    HasRefreshToken bool `json:"has_refresh_token"`

    // Error is the last OAuth error message (if any)
    Error string `json:"error,omitempty"`
}
```

### ConnectionState

```go
// ConnectionState represents the connection state for a server.
// Present on all servers.
type ConnectionState struct {
    // Status indicates connection state: "disconnected", "connecting", "ready", "error"
    Status string `json:"status"`

    // ConnectedAt is when the connection was established
    ConnectedAt *time.Time `json:"connected_at,omitempty"`

    // LastError is the last connection error message
    LastError string `json:"last_error,omitempty"`

    // RetryCount is the number of connection retry attempts
    RetryCount int `json:"retry_count"`

    // LastRetryAt is when the last retry was attempted
    LastRetryAt *time.Time `json:"last_retry_at,omitempty"`

    // ShouldRetry indicates whether a retry is pending
    ShouldRetry bool `json:"should_retry"`
}
```

## New Types (TypeScript)

### OAuthState

```typescript
export interface OAuthState {
    status: 'authenticated' | 'expired' | 'error' | 'none';
    token_expires_at?: string;  // ISO 8601
    last_attempt?: string;      // ISO 8601
    retry_count: number;
    user_logged_out: boolean;
    has_refresh_token: boolean;
    error?: string;
}
```

### ConnectionState

```typescript
export interface ConnectionState {
    status: 'disconnected' | 'connecting' | 'ready' | 'error';
    connected_at?: string;      // ISO 8601
    last_error?: string;
    retry_count: number;
    last_retry_at?: string;     // ISO 8601
    should_retry: boolean;
}
```

## Updated Server Type

### Go Addition

```go
type Server struct {
    // ... existing fields ...

    // NEW: Structured state objects
    OAuthState      *OAuthState      `json:"oauth_state,omitempty"`
    ConnectionState *ConnectionState `json:"connection_state,omitempty"`

    // KEEP: Existing flat fields for backwards compatibility
    Authenticated  bool       `json:"authenticated"`
    OAuthStatus    string     `json:"oauth_status,omitempty"`
    // ... etc
}
```

### TypeScript Addition

```typescript
export interface Server {
    // ... existing fields ...

    // NEW: Structured state objects
    oauth_state?: OAuthState;
    connection_state?: ConnectionState;

    // KEEP: Existing flat fields
    authenticated: boolean;
    oauth_status?: string;
    // ... etc
}
```

## State Transitions

### OAuthState.Status

```
none ──→ authenticated ──→ expired ──→ error
  │            │              │          │
  │            └──────────────┴──────────┘
  │                     │
  └─────────────────────┘ (retry/re-auth)
```

### ConnectionState.Status

```
disconnected ──→ connecting ──→ ready
      │              │           │
      │              ▼           │
      └──────────  error  ◀──────┘
                    │
                    └──→ disconnected (on disable/shutdown)
```

## Validation Rules

| Field | Rule |
|-------|------|
| OAuthState.Status | Must be one of: authenticated, expired, error, none |
| OAuthState.RetryCount | Must be >= 0 |
| ConnectionState.Status | Must be one of: disconnected, connecting, ready, error |
| ConnectionState.RetryCount | Must be >= 0 |
| Health.Level | Must be one of: healthy, degraded, unhealthy |
| Health.AdminState | Must be one of: enabled, disabled, quarantined |
| Health.Action | Must be one of: "", login, restart, enable, approve, view_logs |

## Relationships

- **Server → OAuthState**: Optional (1:0..1) - only present if OAuth configured
- **Server → ConnectionState**: Required (1:1) - always present
- **Server → HealthStatus**: Required (1:1) - always calculated
- **OAuthState + ConnectionState → HealthStatus**: Input for calculation
