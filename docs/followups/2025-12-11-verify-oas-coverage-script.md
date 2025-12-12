# Follow-up: verify-oas-coverage.sh Script Issues

**Date**: 2025-12-11
**Related Feature**: 012-unified-health-status
**Priority**: Low (script works but output is misleading)

## Issue

The `scripts/verify-oas-coverage.sh` script has issues that cause misleading output:

1. **Endpoint prefix mismatch**: The script extracts routes without the `/api/v1` prefix, but the OAS file documents them with the full path. This causes false "missing" reports.

2. **Uppercase artifact**: The sed command `\U\1` uppercases the HTTP method but also adds a "U" prefix to the path (e.g., `UGet /config` instead of `GET /config`).

3. **Coverage calculation is incorrect**: Shows "Coverage: 100%" even when listing 37 "missing" endpoints because the comparison logic doesn't properly match routes.

## Example Output (Problematic)

```
❌ Missing OAS documentation for:
  UDelete /{name}
  UGet /config
  ...

📊 Coverage Statistics:
  Total endpoints:     37
  Documented:          37
  Missing:             37
  Coverage:            100.0%    <-- This is wrong
```

## Root Cause

The route extraction from Go files captures relative paths like `/config`, but the OAS file documents them as `/api/v1/config`. The comparison fails to match these.

## Fix Applied (Partial)

Fixed the bash syntax error where inline comments appeared after backslash line continuations (lines 45-47). This was causing "command not found" errors.

## Remaining Work

1. Update the sed pattern to not add "U" prefix to paths
2. Either:
   - Add `/api/v1` prefix to extracted routes, OR
   - Strip `/api/v1` prefix from OAS paths for comparison
3. Fix the coverage calculation logic

## Verification Steps

```bash
# Run the script and verify:
# 1. No "command not found" errors
# 2. Routes show as "GET /api/v1/config" not "UGet /config"
# 3. Coverage percentage matches actual missing count
./scripts/verify-oas-coverage.sh
```

## Files Affected

- `scripts/verify-oas-coverage.sh`
