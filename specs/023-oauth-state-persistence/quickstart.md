# Quickstart: OAuth Token Refresh Reliability

**Feature**: 023-oauth-state-persistence
**Date**: 2026-01-12

## Overview

This guide provides the essential information for implementing OAuth token refresh reliability in mcpproxy-go.

## Implementation Summary

### Files to Modify

| File | Changes |
|------|---------|
| `internal/oauth/refresh_manager.go` | Exponential backoff, startup refresh, health state |
| `internal/oauth/logging.go` | Fix misleading success logging |
| `internal/upstream/core/connection.go` | Fix misleading success logs (lines 1239, 1696) |
| `internal/health/calculator.go` | Add refresh state to health calculation |
| `internal/observability/metrics.go` | Add OAuth refresh metrics |

### Key Constants

```go
// internal/oauth/refresh_manager.go
const (
    RetryBackoffBase = 10 * time.Second  // FR-008: min 10s between attempts
    MaxRetryBackoff  = 5 * time.Minute   // FR-009: cap at 5 minutes
)
```

### Core Algorithm: Exponential Backoff

```go
func (m *RefreshManager) calculateBackoff(retryCount int) time.Duration {
    backoff := RetryBackoffBase * time.Duration(1<<uint(retryCount))
    if backoff > MaxRetryBackoff {
        backoff = MaxRetryBackoff
    }
    return backoff
}
```

**Sequence**: 10s → 20s → 40s → 80s → 160s → 300s (cap)

## Testing Approach

### Unit Tests

```go
// internal/oauth/refresh_manager_test.go

func TestExponentialBackoff(t *testing.T) {
    tests := []struct {
        retryCount int
        expected   time.Duration
    }{
        {0, 10 * time.Second},
        {1, 20 * time.Second},
        {2, 40 * time.Second},
        {3, 80 * time.Second},
        {4, 160 * time.Second},
        {5, 300 * time.Second}, // Capped at 5min
        {10, 300 * time.Second}, // Still capped
    }
    // ...
}

func TestStartupRefresh(t *testing.T) {
    // Given: Stored token with expired access but valid refresh
    // When: RefreshManager.Start() is called
    // Then: Immediate refresh attempt is made
}

func TestRefreshHealthState(t *testing.T) {
    // Given: Refresh fails
    // When: Health is calculated
    // Then: State is "degraded" with retry info
}
```

### Integration Tests

```go
// internal/oauth/refresh_integration_test.go

func TestOAuthServerSurvivesRestart(t *testing.T) {
    // 1. Start mcpproxy with OAuth server
    // 2. Authenticate via OAuth
    // 3. Restart mcpproxy
    // 4. Verify server reconnects without browser auth
}
```

### Manual Testing

```bash
# 1. Start mcpproxy with OAuth server
./mcpproxy serve --log-level=debug

# 2. Authenticate an OAuth server (e.g., GitHub MCP)
mcpproxy auth login github-mcp

# 3. Verify token stored
mcpproxy upstream list -o json | jq '.[] | select(.name=="github-mcp") | .health'

# 4. Restart and verify auto-reconnect
pkill mcpproxy
./mcpproxy serve --log-level=debug
# Watch logs for refresh attempts

# 5. Check metrics
curl http://localhost:8080/metrics | grep oauth_refresh
```

## Health Status Examples

### CLI Output

```bash
$ mcpproxy upstream list
NAME          STATUS      HEALTH
github-mcp    connected   healthy (Token refresh scheduled for 14:30)
atlassian-mcp connected   degraded (Refresh retry 2 in 40s: timeout)
jira-mcp      error       unhealthy (Refresh token expired - login required)
```

### API Response

```json
{
  "name": "github-mcp",
  "health": {
    "level": "degraded",
    "summary": "Token refresh pending",
    "detail": "Refresh retry 2 scheduled for 2026-01-12T14:05:40Z: connection timeout",
    "action": "view_logs"
  }
}
```

## Metrics

```bash
# Check refresh success rate
curl -s http://localhost:8080/metrics | grep mcpproxy_oauth_refresh_total

# Output:
mcpproxy_oauth_refresh_total{server="github-mcp",result="success"} 42
mcpproxy_oauth_refresh_total{server="github-mcp",result="failed_network"} 3
```

## Debugging

### Enable Debug Logging

```bash
./mcpproxy serve --log-level=debug 2>&1 | grep -i oauth
```

### Key Log Messages

| Message | Meaning |
|---------|---------|
| `OAuth token refresh scheduled` | Proactive refresh timer set |
| `OAuth token refresh attempt` | Refresh starting |
| `OAuth token refresh succeeded` | Refresh completed (NEW: actual refresh, not connection) |
| `OAuth token refresh failed` | Refresh failed with error details |
| `OAuth token refresh retry scheduled` | Backoff retry queued |

### Common Issues

1. **"invalid_grant" errors**: Refresh token expired on provider side → user must re-authenticate
2. **Rapid retry loops**: Check rate limiting is working (min 10s between attempts)
3. **Health stuck in "retrying"**: Check network connectivity to OAuth provider

## Success Criteria Verification

| Criteria | How to Verify |
|----------|---------------|
| SC-001: Auto-reconnect within 60s | Restart mcpproxy, time until server connected |
| SC-002: 99% proactive refresh | Monitor `mcpproxy_oauth_refresh_total` success rate |
| SC-003: Specific failure reasons | Check health status detail field |
| SC-004: Accurate logging | No "successful" logs for failed refreshes |
| SC-005: Manual re-auth only when needed | Only `invalid_grant` requires browser auth |
