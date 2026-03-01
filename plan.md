# Implementation Plan: Fix OAuth Refresh Deadloop (Issue #310)

## Overview

Four changes to `internal/oauth/refresh_manager.go` and updates to `internal/oauth/refresh_manager_test.go`, implemented in TDD order.

## Step 1: Fix `calculateBackoff` Integer Overflow

### Test First
Add `TestRefreshManager_BackoffOverflowProtection` to verify:
- `calculateBackoff(30)` returns `MaxRetryBackoff` (not negative/zero)
- `calculateBackoff(63)` returns `MaxRetryBackoff` (not zero)
- `calculateBackoff(64)` returns `MaxRetryBackoff` (not zero)
- `calculateBackoff(1000)` returns `MaxRetryBackoff` (not zero)
- Loop 0..10000 verifying all results > 0 and <= MaxRetryBackoff

### Implementation
In `calculateBackoff`:
- Cap `retryCount` to a safe maximum exponent (e.g., 30) to prevent overflow
- Add post-computation guard: if `backoff <= 0`, set to `MaxRetryBackoff`

## Step 2: Add Terminal Error Classification for "Server Not Found"

### Test First
Add test cases to `TestClassifyRefreshError`:
- `"server not found: gcw2"` -> `"failed_server_gone"`
- `"server does not use OAuth: gcw2"` -> `"failed_server_gone"`

Add `TestRefreshManager_ServerNotFoundIsTerminal` to verify:
- `handleRefreshFailure` with "server not found" error stops immediately
- Schedule transitions to `RefreshStateFailed`
- Failure event is emitted

### Implementation
In `classifyRefreshError`:
- Add new terminal error patterns: `"server not found"`, `"server does not use OAuth"`
- Return `"failed_server_gone"` for these patterns

In `handleRefreshFailure`:
- Handle `"failed_server_gone"` the same as `"failed_invalid_grant"` (immediate stop, no retry)

## Step 3: Add Maximum Retry Limit

### Test First
Add `TestRefreshManager_MaxRetryLimit` to verify:
- After `DefaultMaxRetries` (50) consecutive failures, schedule transitions to `RefreshStateFailed`
- Failure event is emitted when limit is reached

### Implementation
- Change `DefaultMaxRetries` from `0` to `50`
- In `handleRefreshFailure`, after classifying the error and before calculating backoff:
  - Check if `retryCount >= m.maxRetries` (when `m.maxRetries > 0`)
  - If exceeded, log error, set state to `RefreshStateFailed`, emit event, return

Note: Update `NewRefreshManager` to use the new default -- currently `if config.MaxRetries > 0` would override, so 0 still means "use default of 50".

## Step 4: Enforce Minimum Delay Floor in `rescheduleAfterDelay`

### Test First
Add `TestRefreshManager_MinimumDelayEnforced` to verify:
- `rescheduleAfterDelay` with 0 delay uses `MinRefreshInterval` instead
- `rescheduleAfterDelay` with negative delay uses `MinRefreshInterval` instead

### Implementation
In `rescheduleAfterDelay`:
- Add check: `if delay < MinRefreshInterval { delay = MinRefreshInterval }`

## Step 5: Verify and Commit

1. Run `go test -race ./internal/oauth/... -v`
2. Run `go build ./...`
3. Write `autonomous_summary.md`
4. Commit all changes on branch `fix/oauth-refresh-deadloop-310`

## Files Modified

| File | Changes |
|------|---------|
| `internal/oauth/refresh_manager.go` | Fix `calculateBackoff`, add terminal error handling in `handleRefreshFailure`, enforce min delay in `rescheduleAfterDelay`, change `DefaultMaxRetries` |
| `internal/oauth/refresh_manager_test.go` | Add 5+ new test functions covering overflow, terminal errors, max retries, min delay |

## Risk Assessment

- **Low risk**: All changes are defensive additions (caps, guards, new classifications)
- **No behavioral change for normal operations**: Backoff sequence 10s->20s->40s->80s->160s->300s unchanged for retryCount 0-5
- **No API changes**: No public interface modifications
- **Backward compatible**: Existing configs continue to work
