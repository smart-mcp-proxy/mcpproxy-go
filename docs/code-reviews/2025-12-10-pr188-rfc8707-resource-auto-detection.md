# Code Review: PR #188 - Auto-detect RFC 8707 Resource Parameter for OAuth

**Date**: 2025-12-10
**PR**: https://github.com/smart-mcp-proxy/mcpproxy-go/pull/188
**Reviewer**: Claude Code

## Overview

This PR implements automatic detection of the RFC 8707 `resource` parameter from RFC 9728 Protected Resource Metadata, enabling zero-configuration OAuth for providers like Runlayer/AnySource. This is a well-structured feature addition that improves user experience by eliminating manual `extra_params` configuration.

## Summary of Changes

| File | Type | Description |
|------|------|-------------|
| `internal/oauth/config.go` | New API | Added `CreateOAuthConfigWithExtraParams()` and `autoDetectResource()` |
| `internal/oauth/discovery.go` | Enhancement | Added `DiscoverProtectedResourceMetadata()` |
| `internal/upstream/core/connection.go` | Integration | Updated 4 call sites to use new API |
| `cmd/mcpproxy/auth_cmd.go` | UX | Added resource status display |
| `internal/management/diagnostics.go` | UX | Updated doctor resolution message |
| `CLAUDE.md` | Docs | Comprehensive documentation |
| Tests | Verification | Extensive unit and E2E tests |

---

## Positives

### 1. Clean API Design
The new `CreateOAuthConfigWithExtraParams()` function returns both the config and extraParams, enabling the connection layer to inject params into the authorization URL. The backward-compatible `CreateOAuthConfig()` is preserved for existing callers.

### 2. Proper Priority Order
```go
// Resource auto-detection logic (in priority order):
// 1. Manual extra_params.resource from config (highest priority)
// 2. Auto-detected resource from RFC 9728 Protected Resource Metadata
// 3. Fallback to server URL if metadata unavailable
```

This correctly preserves backward compatibility - manual overrides always win.

### 3. Excellent Test Coverage
- Unit tests for `DiscoverProtectedResourceMetadata()` with error cases
- Unit tests for resource auto-detection and manual override
- E2E test validating full metadata discovery flow
- Tests for parameter merging behavior

### 4. Good Documentation
- CLAUDE.md updated with clear explanation and examples
- Inline code comments explain RFC references
- Diagnostic commands documented for troubleshooting

---

## Issues and Suggestions

### 1. **Network Request in Config Creation** (Medium Risk) - FIXED

```go:internal/oauth/config.go
func autoDetectResource(serverConfig *config.ServerConfig, logger *zap.Logger) string {
    resp, err := http.Post(serverConfig.URL, "application/json", strings.NewReader("{}"))
```

**Concern**: The `CreateOAuthConfigWithExtraParams()` function makes a synchronous HTTP POST request during config creation. This can cause:
- Slowdown during startup if the server is slow/unreachable
- Potential timeout issues affecting connection initialization
- No timeout specified on the `http.Post` call

**Fix**: Added 5-second timeout to preflight request client.

### 2. **Fallback Logic Inconsistency** (Low Risk) - FIXED

In `autoDetectResource()`:
- On network error: returns `serverConfig.URL` (fallback)
- On non-401 response: returns `""` (no resource)
- On 401 without metadata URL: returns `serverConfig.URL` (fallback)

The non-401 case returning empty string seems correct (server doesn't require OAuth), but the behavior isn't immediately obvious from the code.

**Fix**: Added clarifying comment explaining why non-401 returns empty.

### 3. **Unused Variable in E2E Test** - FIXED

```go:internal/server/e2e_oauth_zero_config_test.go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
```

The `ctx` variable is created but never passed to the discovery function.

**Fix**: Removed unused context variable and related check.

### 4. **Minor: Unused import check** - FIXED

```go:internal/server/e2e_oauth_zero_config_test.go
// compile-time check to silence unused import warning
var _ = zap.NewNop
```

**Fix**: Removed unused zap import and dummy reference.

---

## Security Considerations

**Positive**: The implementation correctly:
- Respects manual configuration over auto-detected values
- Masks sensitive params in logs (existing `maskExtraParams` function)
- Uses standard HTTP methods for discovery

**Note**: The POST request with empty body `{}` to detect 401 is per MCP spec. Consider documenting this as it might appear suspicious in network logs.

---

## Performance Considerations

The preflight POST request adds latency to connection establishment. For servers that don't advertise RFC 9728 metadata, this is overhead with no benefit. Consider:
- Caching metadata discovery results
- Making discovery async (non-blocking)

However, this is likely acceptable for v1 since it only happens during initial connection.

---

## Verdict

**Approved with minor fixes applied.**

This is a well-implemented feature with good test coverage and documentation. The code follows project conventions and the API design is clean.

### Fixes Applied:
1. Added timeout to `http.Post` call in `autoDetectResource()`
2. Added clarifying comment for non-401 return behavior
3. Removed unused `ctx` variable in E2E test
4. Removed unused zap import workaround in E2E test
