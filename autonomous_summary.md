# Autonomous Summary: Fix OAuth Refresh Deadloop (Issue #310)

## Problem

When an OAuth server was removed from configuration or became unavailable, the refresh manager entered an infinite retry loop with 0-second delays, producing 23M+ retries and flooding logs. The only recovery was restarting mcpproxy.

## Root Cause

Three interconnected bugs in `internal/oauth/refresh_manager.go`:

1. **Integer overflow in `calculateBackoff`**: The expression `1 << uint(retryCount)` overflows at retryCount >= 30 on 64-bit systems. At retryCount 30, the shift produces a value that, when multiplied by `RetryBackoffBase` (10s in nanoseconds), overflows `int64` to a negative value. The comparison `backoff > MaxRetryBackoff` fails to catch negative values. At retryCount >= 64, the shift wraps to 0, producing `backoff = 0s` permanently.

2. **"Server not found" classified as retryable**: The error `"server not found: gcw2"` from `upstream/manager.go` was classified as `"failed_other"` — neither terminal nor network-retryable. This caused it to enter the retry path, which eventually hit the overflow bug.

3. **No maximum retry limit**: `DefaultMaxRetries = 0` meant unlimited retries, so once the backoff dropped to 0s, retries looped infinitely with no circuit breaker.

## Changes Made

### `internal/oauth/refresh_manager.go`

1. **Fixed `calculateBackoff` overflow** (lines 800-820):
   - Added `maxBackoffExponent = 25` constant
   - When `retryCount > maxBackoffExponent`, returns `MaxRetryBackoff` immediately
   - Added post-computation guard: if `backoff <= 0`, use `MaxRetryBackoff`

2. **Added terminal error classification** (lines 760-770):
   - New classification `"failed_server_gone"` for "server not found" and "server does not use OAuth" errors
   - These are checked before network/grant errors since they should never be retried

3. **Handle terminal errors in `handleRefreshFailure`** (lines 718-733):
   - `"failed_server_gone"` errors immediately set `RefreshStateFailed` and emit failure event
   - No retry scheduled — the server won't magically reappear

4. **Changed `DefaultMaxRetries` from 0 to 50** (line 25):
   - Circuit breaker: after 50 consecutive failures, stops retrying
   - With exponential backoff (10s base, 5min cap), 50 retries spans ~2+ hours

5. **Check max retries in `handleRefreshFailure`** (lines 735-750):
   - When `retryCount >= maxRetries`, sets `RefreshStateFailed` and emits failure event

6. **Enforced minimum delay in `rescheduleAfterDelay`** (lines 825-828):
   - If `delay < MinRefreshInterval` (5s), uses `MinRefreshInterval`
   - Defense-in-depth against any code path producing 0 or negative delays

### `internal/oauth/refresh_manager_test.go`

Added 6 new test functions:

- `TestRefreshManager_BackoffOverflowProtection`: Verifies backoff is positive and capped at boundaries (retryCount 30, 63, 64, 100, 1000, 23158728) and exhaustively for 0-10000
- `TestRefreshManager_ServerNotFoundIsTerminal`: Verifies "server not found" stops refresh immediately with no retry
- `TestRefreshManager_MaxRetryLimit`: Verifies max retry limit stops retries and emits failure event
- `TestDefaultMaxRetries_IsNonZero`: Verifies the constant is non-zero
- `TestRefreshManager_MinimumDelayEnforced`: Verifies 0 and negative delays are clamped to MinRefreshInterval
- Extended `TestClassifyRefreshError` with 3 new cases for server-gone errors

## Test Results

```
go test -race ./internal/oauth/... -v  →  PASS (17.2s)
go build ./internal/oauth/...          →  OK
go vet ./internal/oauth/...            →  OK
```

All 28 tests in the oauth package pass, including all pre-existing tests (backward compatible).

## Verification

The fix addresses all three failure modes from the issue:
- **Overflow**: `calculateBackoff(23158728)` now returns `5m0s` (was `0s`)
- **Terminal errors**: "server not found" immediately stops (was infinite retry)
- **Circuit breaker**: Even unknown errors stop after 50 retries (was unlimited)
