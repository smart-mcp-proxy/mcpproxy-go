# Data Model: OAuth Token Refresh Bug Fixes and Logging Improvements

**Feature**: 008-oauth-token-refresh
**Date**: 2025-12-04

## Entities

### Existing Entities (No Changes Required)

#### OAuthTokenRecord
**Location**: `internal/storage/models.go`

Stores OAuth tokens in BBolt database.

| Field | Type | Description |
|-------|------|-------------|
| ServerName | string | Unique server identifier (combined with URL hash) |
| AccessToken | string | JWT access token |
| RefreshToken | string | OAuth refresh token (optional) |
| TokenType | string | Token type (usually "Bearer") |
| ExpiresAt | time.Time | Access token expiration timestamp |
| Scopes | []string | Granted OAuth scopes |
| Created | time.Time | Token creation timestamp |
| Updated | time.Time | Last update timestamp |

#### OAuthCompletionEvent
**Location**: `internal/storage/models.go`

Tracks OAuth completion for cross-process notification.

| Field | Type | Description |
|-------|------|-------------|
| ServerName | string | Server that completed OAuth |
| CompletedAt | time.Time | Completion timestamp |
| Processed | bool | Whether event has been processed |

### New Entities

#### OAuthFlowContext
**Location**: `internal/oauth/correlation.go` (new file)

Represents context for a single OAuth authentication flow.

| Field | Type | Description |
|-------|------|-------------|
| CorrelationID | string | UUID linking all log entries for this flow |
| ServerName | string | Target MCP server name |
| StartTime | time.Time | Flow start timestamp |
| State | OAuthFlowState | Current flow state |

**State Transitions**:
```
Initiated → Authenticating → TokenExchange → Completed
                 ↓                 ↓
              Failed           Failed
```

#### OAuthFlowState
**Location**: `internal/oauth/correlation.go` (new file)

Enumeration of OAuth flow states.

| Value | Description |
|-------|-------------|
| FlowInitiated | OAuth flow has started |
| FlowAuthenticating | Browser opened, waiting for callback |
| FlowTokenExchange | Exchanging code for tokens |
| FlowCompleted | Successfully obtained tokens |
| FlowFailed | Flow failed with error |

#### OAuthFlowCoordinator
**Location**: `internal/oauth/coordinator.go` (new file)

Coordinates concurrent OAuth flows per server.

| Field | Type | Description |
|-------|------|-------------|
| activeFlows | map[string]*OAuthFlowContext | Active flows by server name |
| flowLocks | map[string]*sync.Mutex | Per-server mutexes |
| mu | sync.RWMutex | Protects map access |
| logger | *zap.Logger | Logger instance |

**Methods**:
| Method | Description |
|--------|-------------|
| StartFlow(serverName) (*OAuthFlowContext, error) | Start new flow or wait for existing |
| EndFlow(serverName, success bool) | Mark flow as completed or failed |
| IsFlowActive(serverName) bool | Check if flow is in progress |
| WaitForFlow(serverName, timeout) error | Wait for active flow to complete |

### Relationship Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                        OAuth Flow Lifecycle                          │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────┐     ┌──────────────────┐     ┌─────────────────────┐
│  MCP Server     │────▶│  OAuth Flow      │────▶│  Token Store        │
│  Connection     │     │  Coordinator     │     │  (BBolt)            │
└─────────────────┘     └──────────────────┘     └─────────────────────┘
                               │
                               │ creates
                               ▼
                        ┌──────────────────┐
                        │  OAuth Flow      │
                        │  Context         │
                        │  - correlation_id│
                        │  - server_name   │
                        │  - state         │
                        └──────────────────┘
                               │
                               │ logs with
                               ▼
                        ┌──────────────────┐
                        │  Correlation     │
                        │  Logger          │
                        │  (zap + ctx)     │
                        └──────────────────┘
```

## Data Flow

### Token Refresh Flow

```
1. Connection attempts to use token
   ↓
2. PersistentTokenStore.GetToken() retrieves from BBolt
   ↓
3. Token checked: is access_token expired?
   ├── No: Use token directly
   └── Yes: Has refresh_token?
       ├── Yes: Attempt refresh via mcp-go
       │   ├── Success: Save new token, continue
       │   └── Failure: Trigger new OAuth flow
       └── No: Trigger new OAuth flow
```

### OAuth Flow Coordination

```
1. trySSEOAuthStrategy() or tryHTTPOAuthStrategy() called
   ↓
2. OAuthFlowCoordinator.StartFlow(serverName)
   ├── No active flow: Create new OAuthFlowContext
   │   - Generate correlation_id
   │   - Set state = FlowInitiated
   │   - Return context
   └── Active flow exists: WaitForFlow(serverName, timeout)
       ├── Flow completes successfully: Return shared token
       └── Flow fails or timeout: Return error
   ↓
3. Execute OAuth flow with correlation_id in all logs
   ↓
4. OAuthFlowCoordinator.EndFlow(serverName, success)
   - Notify waiting goroutines
   - Clear flow context
```

### Log Entry Structure

Each OAuth log entry includes:

```json
{
  "level": "info",
  "ts": "2025-12-04T10:00:00.000Z",
  "logger": "oauth",
  "msg": "OAuth token exchange completed",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "server": "oauth-test-server",
  "token_type": "Bearer",
  "expires_in_seconds": 3600,
  "scope": "mcp:read mcp:write",
  "has_refresh_token": true,
  "duration_ms": 245
}
```

## Validation Rules

### OAuthFlowContext
- `CorrelationID` must be valid UUID v4
- `ServerName` must be non-empty
- `StartTime` must be set when flow starts
- `State` must follow valid transitions

### Token Refresh
- Only attempt refresh if `RefreshToken` is non-empty
- Retry refresh max 3 times with exponential backoff
- Fall back to new OAuth flow after refresh failures

### Flow Coordination
- Only one active flow per server at any time
- Waiting goroutines timeout after 5 minutes
- Clear stale flows after 10 minutes of inactivity

## Indexes

### Existing (No Changes)

| Bucket | Key Format | Value |
|--------|------------|-------|
| oauth_tokens | `{serverName}_{urlHash16}` | OAuthTokenRecord (JSON) |
| oauth_completion | `{serverName}_{timestamp}` | OAuthCompletionEvent (JSON) |

### New (In-Memory Only)

| Structure | Purpose |
|-----------|---------|
| activeFlows map | Track in-progress OAuth flows by server |
| flowLocks map | Per-server mutexes for coordination |
