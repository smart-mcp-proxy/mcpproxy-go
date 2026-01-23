# Research: OAuth Token Refresh Reliability

**Feature**: 023-oauth-state-persistence
**Date**: 2026-01-12

## Key Decisions

### 1. Token Refresh Method

**Decision**: Use mcp-go's automatic token refresh via TokenStore pattern, not a direct `RefreshToken()` API call.

**Rationale**: The mcp-go library (v0.43.1) does NOT expose a direct `RefreshToken()` method on the OAuth handler. Token refresh is handled automatically when:
1. A `TokenStore` with a stored refresh token is provided to `OAuthConfig`
2. `client.Start(ctx)` is called
3. mcp-go detects an expired access token and attempts automatic refresh internally

**Alternatives Considered**:
- Direct OAuth2 HTTP calls: Rejected - duplicates mcp-go functionality and bypasses its token store integration
- Patching mcp-go: Rejected - adds maintenance burden and delays implementation

### 2. Exponential Backoff Strategy

**Decision**: Extend existing exponential backoff (base 10s, cap 5min) with unlimited retries until token expiration.

**Rationale**: The spec requires retries until expiration with 10s→20s→40s→80s→5min cap. The existing `RetryBackoffBase = 2s` is too aggressive; will use 10s base per FR-008 (rate limit 1 per 10s per server).

**Backoff Sequence** (10s base, 5min cap):
| Attempt | Wait Before | Cumulative Time |
|---------|-------------|-----------------|
| 1 | 0s | 0s |
| 2 | 10s | 10s |
| 3 | 20s | 30s |
| 4 | 40s | 70s (~1min) |
| 5 | 80s | 150s (~2.5min) |
| 6 | 160s | 310s (~5min) |
| 7 | 300s (cap) | 610s (~10min) |
| 8+ | 300s (cap) | continues until expiration |

**Alternatives Considered**:
- Jitter: Deferred - single-server retries don't cause thundering herd
- Fibonacci backoff: Rejected - exponential is simpler and well-understood

### 3. Misleading Logging Fix

**Decision**: Fix the logging in `connection.go` to accurately report token refresh outcomes, and remove/rename the misleading helper function in `logging.go`.

**Root Cause Analysis**:
- `LogTokenRefreshSuccess()` in `logging.go` is called when `c.client.Start()` succeeds
- This does NOT mean a token refresh occurred - it means the client connected
- The duration logged (`time.Duration(attempt)*backoff`) is retry wait time, not refresh duration

**Fix Locations**:
1. `internal/upstream/core/connection.go:1239` - HTTP connection retry
2. `internal/upstream/core/connection.go:1696` - SSE connection retry
3. `internal/oauth/logging.go:171` - Rename or remove misleading function

**Alternatives Considered**:
- Keep logging but change wording: Rejected - still semantically wrong
- Add separate actual refresh logging: Selected - log actual refresh attempts via RefreshManager

### 4. Health Status Integration

**Decision**: Surface refresh retry state as `degraded` health with actionable detail.

**Existing Health Calculation** (`internal/health/calculator.go`):
- Already distinguishes tokens with/without refresh capability
- Has `OAuthStatus` enum: "authenticated", "expired", "error", "none"
- Has `action` field for suggested user action

**New States to Add**:
| Condition | Level | Summary | Action |
|-----------|-------|---------|--------|
| Refresh retrying | degraded | "Token refresh retry pending" | view_logs |
| Refresh failed (recoverable) | degraded | "Refresh failed - network error" | retry |
| Refresh failed (permanent) | unhealthy | "Refresh token expired" | login |

**Alternatives Considered**:
- New health level: Rejected - "degraded" is semantically correct for retry state
- Separate refresh status field: Rejected - health system already handles this

### 5. Metrics Implementation

**Decision**: Add two Prometheus metrics to existing `MetricsManager`.

**Metrics**:
```go
mcpproxy_oauth_refresh_total{server, result}       // Counter
mcpproxy_oauth_refresh_duration_seconds{server, result}  // Histogram
```

**Labels**:
- `server`: Server name (e.g., "github-mcp")
- `result`: "success", "failed_network", "failed_invalid_grant", "failed_other"

**Integration Point**: RefreshManager's `executeRefresh()` method already has the hook point via `RefreshEventEmitter`.

**Alternatives Considered**:
- Per-error-type counters: Rejected - label-based is more flexible
- Separate expiry warnings counter: Deferred to future work

### 6. Startup Recovery Flow

**Decision**: Enhance RefreshManager.Start() to attempt immediate refresh for expired tokens.

**Current Behavior** (`refresh_manager.go:124-162`):
1. Loads tokens from storage via `m.storage.ListOAuthTokens()`
2. For each non-expired token, schedules proactive refresh at 80% lifetime
3. Does NOT handle already-expired tokens

**New Behavior**:
1. Load tokens from storage
2. For each token:
   - If not expired → schedule proactive refresh at 80% lifetime (unchanged)
   - If access token expired but refresh token exists → attempt immediate refresh
   - If both expired → mark as needing re-auth

**Alternatives Considered**:
- Trigger refresh on first tool call: Rejected - delays user workflow
- Background refresh thread: Already exists (RefreshManager), just needs enhancement

## Technical Findings

### Token Storage Model

**Location**: `internal/storage/models.go`

```go
type OAuthTokenRecord struct {
    ServerName    string    // Storage key: serverName_hash
    DisplayName   string    // Actual server name for RefreshManager lookup
    AccessToken   string
    RefreshToken  string    // Required for proactive refresh
    TokenType     string
    ExpiresAt     time.Time
    Scopes        []string
    ClientID      string    // For DCR
    ClientSecret  string    // For DCR
    Created       time.Time
    Updated       time.Time
}
```

**Key Insight**: `GetServerName()` returns `DisplayName` if available, enabling RefreshManager to look up servers by their config name.

### mcp-go OAuth Integration

**Version**: v0.43.1

**Available Methods**:
```go
client.IsOAuthAuthorizationRequiredError(err error) bool
client.GetOAuthHandler(err error) OAuthHandler
client.GenerateCodeVerifier() (string, error)
client.GenerateCodeChallenge(codeVerifier string) string
```

**Token Store Interface**:
```go
type TokenStore interface {
    GetToken(ctx context.Context) (*Token, error)
    SaveToken(ctx context.Context, token *Token) error
}
```

**No Direct RefreshToken() Method**: Refresh is handled internally by mcp-go when TokenStore provides a refresh token.

### Error Detection

**OAuth Errors** (`internal/oauth/errors.go`):
```go
ErrServerNotOAuth  = errors.New("server does not use OAuth")
ErrTokenExpired    = errors.New("OAuth token has expired")
ErrRefreshFailed   = errors.New("OAuth token refresh failed")
ErrNoRefreshToken  = errors.New("no refresh token available")
```

**String-Based Detection** (`connection.go:1798-1825`):
```go
oauthErrors := []string{
    "invalid_token",
    "invalid_grant",      // Refresh token expired
    "access_denied",
    "unauthorized",
    "401",
    "Missing or invalid access token",
    "no valid token available",
}
```

**Key Insight**: `invalid_grant` specifically indicates the refresh token is invalid/expired, requiring full re-authentication.

### Existing Backoff Implementation

**RefreshManager** (`refresh_manager.go:405-407`):
```go
backoff := RetryBackoffBase * time.Duration(1<<(retryCount-1))
m.rescheduleAfterDelay(serverName, backoff)
```

**Connection Retry** (`connection.go:1232-1265`):
```go
backoff := refreshConfig.InitialBackoff  // 1s
for attempt := 1; attempt <= refreshConfig.MaxAttempts; attempt++ {
    // ...
    backoff = min(backoff*2, refreshConfig.MaxBackoff)  // Cap at 10s
}
```

### Event System

**RefreshEventEmitter Interface** (`refresh_manager.go`):
```go
type RefreshEventEmitter interface {
    EmitTokenRefreshed(serverName string)
    EmitTokenRefreshFailed(serverName string, err error)
}
```

**Integration Point**: RefreshManager already emits events that can be consumed by MetricsManager.

## Files to Modify

| File | Change Type | Description |
|------|-------------|-------------|
| `internal/oauth/refresh_manager.go` | MODIFY | Add exponential backoff with 5min cap, startup refresh for expired tokens |
| `internal/oauth/logging.go` | MODIFY | Fix/remove misleading `LogTokenRefreshSuccess()` |
| `internal/upstream/core/connection.go` | MODIFY | Fix misleading success logs at lines 1239, 1696 |
| `internal/health/calculator.go` | MODIFY | Add refresh retry state to health calculation |
| `internal/observability/metrics.go` | MODIFY | Add OAuth refresh metrics |

## No Changes Required

| File | Reason |
|------|--------|
| `internal/storage/models.go` | Existing `OAuthTokenRecord` is sufficient |
| `internal/oauth/persistent_token_store.go` | Grace period logic already correct |
| `internal/oauth/config.go` | OAuth config creation unchanged |
