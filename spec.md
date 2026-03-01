# Spec: Fix OAuth Refresh Deadloop (Issue #310)

## Problem Statement

When an OAuth server is removed from configuration or becomes unavailable, the refresh manager enters an infinite retry loop with 0-second delays, producing millions of retries per minute and spamming logs. The only recovery is restarting mcpproxy.

### Root Cause Analysis

There are **three distinct bugs** contributing to this issue:

#### Bug 1: Integer Overflow in Backoff Calculation

The `calculateBackoff` function uses `1 << uint(retryCount)` which overflows on 64-bit systems:

- At `retryCount >= 30`: `1 << 30 = 1073741824`, multiplied by `10s` base overflows `time.Duration` (int64 nanoseconds) to a **negative value**. The `backoff > MaxRetryBackoff` check does NOT catch negative values, so the uncapped negative duration is used.
- At `retryCount >= 63`: `1 << 63` overflows to the minimum int64 value, and `1 << 64 = 0`, giving `backoff = 0s`.
- At `retryCount >= 64`: All further shifts produce `0`, giving `backoff = 0s` permanently.

This means after ~30 retries (about 5 minutes of exponential backoff), the delay drops to 0 and retries become infinite tight loops.

#### Bug 2: "Server Not Found" Classified as Retryable

The error `"server not found: gcw2"` from `upstream/manager.go` is classified as `"failed_other"` by `classifyRefreshError`. This classification does NOT trigger the permanent failure path (only `"failed_invalid_grant"` does). Instead, it enters the retry path with backoff, which then hits Bug 1.

When a server is removed from configuration, "server not found" is a **terminal** condition -- no amount of retrying will make the server appear. This should be treated as a permanent failure.

#### Bug 3: No Maximum Retry Limit

The constant `DefaultMaxRetries = 0` means "unlimited retries." Combined with Bug 1 causing 0s delays, this allows the retry count to reach 23M+ without any circuit breaker stopping it.

## Solution

### Fix 1: Overflow-Safe Backoff Calculation

Cap the shift exponent to prevent integer overflow. The maximum useful exponent before hitting `MaxRetryBackoff` (5 minutes = 300s) with `RetryBackoffBase` (10s) is 5 (`10s * 2^5 = 320s > 300s`). Cap the exponent at a safe value (e.g., 30) that prevents overflow while still allowing the backoff cap to apply naturally.

Additionally, add a guard: if the computed backoff is zero or negative, use `MaxRetryBackoff` as a fallback.

### Fix 2: Classify "Server Not Found" as Terminal Error

Add `"server not found"` to the list of terminal (permanent) errors in `classifyRefreshError`. When a server is not in the configuration, retrying is pointless. The same applies to `"server does not use OAuth"`.

Create a new error classification category: `"failed_server_gone"` for errors that indicate the server no longer exists or is not applicable for OAuth refresh.

### Fix 3: Add Maximum Consecutive Retry Limit

Change `DefaultMaxRetries` from `0` (unlimited) to `50`. After 50 consecutive failures, mark the refresh as permanently failed and stop retrying. This acts as a circuit breaker even if Fixes 1 and 2 don't cover an edge case.

The retry limit is per-schedule and resets on success (via `handleRefreshSuccess` which sets `RetryCount = 0`).

### Fix 4: Enforce Minimum Delay Floor

In `rescheduleAfterDelay`, enforce that the delay is never less than `MinRefreshInterval` (5 seconds). This provides defense-in-depth against any code path that computes a 0 or negative delay.

## Assumptions

1. **50 max retries is sufficient**: With exponential backoff (10s, 20s, 40s, ... 300s cap), 50 retries spans approximately 2+ hours. If a transient issue isn't resolved in 2 hours, it's likely permanent. Users can re-trigger refresh via re-login.

2. **"Server not found" is always terminal**: If a server was removed from config while having stored OAuth tokens, the refresh should stop immediately. The stale token record will be cleaned up separately (or via `OnTokenCleared`).

3. **"Server does not use OAuth" is terminal**: Similar to "server not found" -- if the server config changed to remove OAuth, retrying is pointless.

4. **The backoff exponent cap of 30 is safe**: `1 << 30` on a 64-bit platform is `1073741824`, which when multiplied by 10s (`10_000_000_000 ns`) gives `10_737_418_240_000_000_000 ns` -- still within int64 range but will be capped by `MaxRetryBackoff` anyway. We clamp before this point for defense-in-depth.

5. **No configuration change needed**: The max retry limit is a code-level constant, not a user-facing config. This keeps the fix simple and secure by default.

## Functional Requirements

| ID | Requirement |
|----|-------------|
| FR-001 | `calculateBackoff` MUST NOT produce zero or negative durations for any retry count value |
| FR-002 | `classifyRefreshError` MUST classify "server not found" errors as terminal (non-retryable) |
| FR-003 | `classifyRefreshError` MUST classify "server does not use OAuth" errors as terminal |
| FR-004 | `handleRefreshFailure` MUST stop retrying after `DefaultMaxRetries` consecutive failures |
| FR-005 | `rescheduleAfterDelay` MUST enforce a minimum delay of `MinRefreshInterval` (5 seconds) |
| FR-006 | When max retries are exceeded, the schedule MUST transition to `RefreshStateFailed` |
| FR-007 | When a terminal error is detected, the schedule MUST transition to `RefreshStateFailed` immediately |
| FR-008 | A failure event MUST be emitted when retries are exhausted or a terminal error occurs |

## Non-Functional Requirements

| ID | Requirement |
|----|-------------|
| NFR-001 | All changes must pass `go test -race ./internal/oauth/...` |
| NFR-002 | All changes must compile with `go build ./...` |
| NFR-003 | No breaking changes to public APIs or configuration |
| NFR-004 | Backward-compatible: existing exponential backoff behavior preserved for normal retry ranges |

## Testing Requirements

| Test | Description |
|------|-------------|
| T001 | `calculateBackoff` returns correct values at overflow boundaries (retryCount=30, 63, 64, 100, 1000) |
| T002 | `calculateBackoff` never returns zero or negative for any retryCount 0-10000 |
| T003 | `classifyRefreshError` classifies "server not found" as terminal |
| T004 | `classifyRefreshError` classifies "server does not use OAuth" as terminal |
| T005 | `handleRefreshFailure` stops after max retries and emits failure event |
| T006 | `handleRefreshFailure` treats terminal errors same as invalid_grant (immediate stop) |
| T007 | `rescheduleAfterDelay` enforces minimum delay |
| T008 | Integration: server-not-found error stops refresh immediately (no retry) |
