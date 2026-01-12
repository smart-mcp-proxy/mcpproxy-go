# Data Model: OAuth Token Refresh Reliability

**Feature**: 023-oauth-state-persistence
**Date**: 2026-01-12

## Entity Overview

This feature primarily extends existing entities rather than creating new ones. The key data structures involved are:

1. **OAuthTokenRecord** (existing) - Token persistence
2. **RefreshSchedule** (existing) - Proactive refresh scheduling
3. **HealthCalculatorInput** (existing) - Health status calculation
4. **OAuth Metrics** (new) - Prometheus metrics for observability

## Entity Definitions

### 1. OAuthTokenRecord (Existing - No Changes)

**Location**: `internal/storage/models.go`

```go
type OAuthTokenRecord struct {
    ServerName    string    `json:"server_name"`    // Storage key (serverName_hash)
    DisplayName   string    `json:"display_name"`   // Actual server name
    AccessToken   string    `json:"access_token"`
    RefreshToken  string    `json:"refresh_token"`  // Required for proactive refresh
    TokenType     string    `json:"token_type"`
    ExpiresAt     time.Time `json:"expires_at"`
    Scopes        []string  `json:"scopes"`
    ClientID      string    `json:"client_id"`      // For DCR
    ClientSecret  string    `json:"client_secret"`  // For DCR
    Created       time.Time `json:"created"`
    Updated       time.Time `json:"updated"`
}
```

**Relationships**:
- Referenced by: RefreshSchedule (via DisplayName)
- Stored in: BBolt `oauth_tokens` bucket

**Validation Rules**:
- `ServerName`: Required, unique per OAuth server
- `AccessToken`: Required when authenticated
- `RefreshToken`: Optional, enables proactive refresh
- `ExpiresAt`: Required for scheduling refresh

### 2. RefreshSchedule (Existing - Extended)

**Location**: `internal/oauth/refresh_manager.go`

```go
type RefreshSchedule struct {
    ServerName       string        // Server identifier
    ExpiresAt        time.Time     // Token expiration time
    ScheduledRefresh time.Time     // When refresh will be attempted
    RetryCount       int           // Number of failed attempts
    LastError        error         // Most recent failure reason
    Timer            *time.Timer   // Scheduled timer (internal)

    // NEW FIELDS for this feature:
    RetryBackoff     time.Duration // Current backoff duration
    MaxBackoff       time.Duration // Maximum backoff (5 minutes)
    LastAttempt      time.Time     // Time of last refresh attempt
    RefreshState     RefreshState  // Current state for health reporting
}

// NEW: Refresh state for health status integration
type RefreshState int

const (
    RefreshStateIdle        RefreshState = iota // No refresh needed
    RefreshStateScheduled                       // Proactive refresh scheduled
    RefreshStateRetrying                        // Failed, retrying with backoff
    RefreshStateFailed                          // Permanently failed (needs re-auth)
)
```

**Lifecycle Invariant**: Schedule exists iff tokens exist for server (created post-auth or when loaded at startup, destroyed when tokens removed).

**State Transitions**:
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  [Token Saved] ──► Scheduled ──► [80% lifetime] ──► Idle   │
│                        │                              │     │
│                        │ [Refresh Failed]             │     │
│                        ▼                              │     │
│                    Retrying ◄─────────────────────────┘     │
│                        │                                    │
│                        │ [invalid_grant]                    │
│                        ▼                                    │
│                     Failed ──► [Re-auth] ──► Scheduled      │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 3. HealthCalculatorInput (Existing - Extended)

**Location**: `internal/health/calculator.go`

```go
type HealthCalculatorInput struct {
    // Existing fields...
    OAuthRequired    bool
    OAuthStatus      string     // "authenticated", "expired", "error", "none"
    TokenExpiresAt   *time.Time
    HasRefreshToken  bool
    UserLoggedOut    bool

    // NEW FIELDS for this feature:
    RefreshState     RefreshState // From RefreshSchedule
    RefreshRetryCount int         // Number of retry attempts
    RefreshLastError  string      // Human-readable error message
    RefreshNextAttempt *time.Time // When next retry will occur
}
```

**Health Output Mapping**:

| RefreshState | Health Level | Summary | Action |
|--------------|--------------|---------|--------|
| Idle | healthy | "Connected" | - |
| Scheduled | healthy | "Token refresh scheduled" | - |
| Retrying | degraded | "Token refresh retry pending" | view_logs |
| Failed | unhealthy | "Refresh token expired" | login |

### 4. OAuth Metrics (New)

**Location**: `internal/observability/metrics.go`

```go
// Added to MetricsManager struct
type MetricsManager struct {
    // Existing fields...

    // NEW: OAuth refresh metrics
    oauthRefreshTotal    *prometheus.CounterVec
    oauthRefreshDuration *prometheus.HistogramVec
}
```

**Metric Definitions**:

```go
// Counter: Total refresh attempts
oauthRefreshTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mcpproxy_oauth_refresh_total",
        Help: "Total number of OAuth token refresh attempts",
    },
    []string{"server", "result"},
)

// Histogram: Refresh duration
oauthRefreshDuration = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name:    "mcpproxy_oauth_refresh_duration_seconds",
        Help:    "OAuth token refresh duration in seconds",
        Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
    },
    []string{"server", "result"},
)
```

**Labels**:
- `server`: Server name (e.g., "github-mcp")
- `result`: One of:
  - `success`: Refresh completed successfully
  - `failed_network`: Network error (retryable)
  - `failed_invalid_grant`: Refresh token expired (permanent)
  - `failed_other`: Other failures

## Configuration Constants

**Location**: `internal/oauth/refresh_manager.go`

```go
const (
    // Existing
    DefaultRefreshThreshold = 0.8  // Refresh at 80% of token lifetime
    MinRefreshInterval      = 5 * time.Second

    // MODIFIED for this feature
    RetryBackoffBase = 10 * time.Second  // Was: 2s, Now: 10s (FR-008)
    MaxRetryBackoff  = 5 * time.Minute   // NEW: Cap at 5 minutes (FR-009)
    MaxRetries       = 0                  // NEW: Unlimited (until expiration)
)
```

## Data Flow

### Startup Recovery Flow

```
1. RefreshManager.Start()
   │
   ├─► storage.ListOAuthTokens()
   │
   └─► For each token:
       │
       ├─► If not expired:
       │   └─► scheduleRefreshLocked(80% lifetime)
       │
       └─► If access token expired, refresh token exists:
           └─► executeRefresh() immediately
               │
               ├─► Success: scheduleRefreshLocked(new 80%)
               │
               └─► Failure: rescheduleWithBackoff()
```

### Proactive Refresh Flow

```
1. Timer fires at 80% lifetime
   │
   └─► executeRefresh()
       │
       ├─► runtime.RefreshOAuthToken(serverName)
       │   │
       │   └─► mcp-go client.Start() with TokenStore
       │
       ├─► Success:
       │   ├─► Emit metrics (success)
       │   ├─► Update health status (Idle)
       │   └─► Schedule next refresh
       │
       └─► Failure:
           ├─► Emit metrics (failed_*)
           ├─► Update health status (Retrying/Failed)
           └─► Reschedule with backoff (if retryable)
```

### Health Status Integration Flow

```
1. HealthCalculator.Calculate()
   │
   ├─► Get RefreshSchedule for server
   │
   └─► Map RefreshState to health:
       │
       ├─► Retrying: degraded + "Refresh retry pending"
       │
       ├─► Failed: unhealthy + "Refresh token expired"
       │
       └─► Other: existing logic
```

## Storage Bucket

**Bucket**: `oauth_tokens` (existing)
**Database**: `~/.mcpproxy/config.db` (BBolt)

No schema changes required. The `OAuthTokenRecord` structure is sufficient for all refresh operations.
