# Research: OAuth Extra Parameters Implementation

**Feature**: OAuth Extra Parameters Support
**Date**: 2025-11-30
**Status**: ✅ COMPLETE

## Overview

This document consolidates all research conducted during the pre-planning phase for OAuth extra parameters support. All technical unknowns have been resolved and documented in `sdk-comparison-analysis.md`.

## Research Questions & Findings

### Q1: Does mcp-go v0.43.1 support OAuth extra parameters natively?

**Finding**: ❌ NO

**Evidence**: Source code review of `github.com/mark3labs/mcp-go v0.43.1`:
- `client/transport/oauth.go:686-712` - `GetAuthorizationURL()` uses hardcoded parameters only
- `client/transport/oauth.go:606-684` - `ProcessAuthorizationResponse()` has no extension mechanism
- Release notes for v0.43.1 focus on session management, no OAuth changes

**Decision**: Upgrade to v0.43.1 for stability, implement wrapper pattern for extra params

**Rationale**: v0.43.1 includes important session fixes for multi-instance deployments, but provides no OAuth improvements relevant to this feature.

---

### Q2: Is mcp-go issue #412 (RFC 8707 support) making progress?

**Finding**: ❌ NO - Zero activity since June 2025

**Evidence**:
- **Created**: June 19, 2025
- **Status**: Open, unassigned
- **Comments**: 0 (no discussion)
- **Commits**: None
- **Timeline**: No milestones or target dates

**Decision**: Do not wait for upstream implementation

**Rationale**: Cannot block Runlayer integration on an inactive issue with no committed maintainer or timeline.

---

### Q3: Would switching to the official modelcontextprotocol/go-sdk provide native extra params support?

**Finding**: ❌ NO - Still requires wrapper pattern

**Evidence**: Official SDK analysis:
- Uses `golang.org/x/oauth2` under the hood
- `oauth2.Config` struct does NOT have ExtraParams field
- Must use `oauth2.SetAuthURLParam()` for each extra param (still a wrapper)
- Migration effort: 3-5 weeks, ~4500 lines of code changes
- No benefit for extra params feature

**Decision**: Stay with mcp-go v0.43.1

**Rationale**: Switching SDKs delivers zero benefit for extra params (both require wrappers) while introducing massive migration risk and delay.

---

### Q4: How should extra params be injected into OAuth flows?

**Finding**: ✅ URL construction time interception

**Approach**:
```go
type OAuthTransportWrapper struct {
    inner       *client.OAuthHandler
    extraParams map[string]string
}

func (w *OAuthTransportWrapper) WrapAuthorizationURL(baseURL string) string {
    u, _ := url.Parse(baseURL)
    q := u.Query()
    for k, v := range w.extraParams {
        q.Set(k, v)
    }
    u.RawQuery = q.Encode()
    return u.String()
}
```

**Alternatives Considered**:
1. **HTTP middleware**: Rejected - URLs already constructed before middleware runs
2. **Modify mcp-go directly**: Rejected - requires fork, maintenance burden
3. **Pre-generate URLs**: Rejected - breaks PKCE (dynamic code challenge)

**Decision**: Wrapper intercepts mcp-go's OAuth handler methods

**Rationale**: Clean separation of concerns, minimal code footprint, easy to remove when native support arrives.

---

### Q5: When should extra params be validated?

**Finding**: ✅ Config load time (fail fast)

**Validation Rules**:
```go
var reservedOAuthParams = map[string]bool{
    "client_id": true, "client_secret": true, "redirect_uri": true,
    "response_type": true, "scope": true, "state": true,
    "code_challenge": true, "code_challenge_method": true,
    "grant_type": true, "code": true, "refresh_token": true,
}
```

**Alternatives Considered**:
1. **Runtime validation**: Rejected - confusing errors during OAuth flow
2. **No validation**: Rejected - security risk (parameter override attacks)

**Decision**: Validate at config load, reject invalid configs immediately

**Rationale**: Configuration errors are developer mistakes, not user errors. Failing at startup provides immediate feedback and prevents runtime surprises.

---

### Q6: How should sensitive params be handled in logs?

**Finding**: ✅ Selective masking (resource URLs visible, others masked)

**Logging Strategy**:
```go
func maskSensitiveParams(params map[string]string) map[string]string {
    masked := make(map[string]string)
    for k, v := range params {
        if strings.HasPrefix(k, "resource") {
            masked[k] = v // Resource URLs are public, show in full
        } else {
            masked[k] = "***" // Mask other params (might be secrets)
        }
    }
    return masked
}
```

**Alternatives Considered**:
1. **Mask everything**: Rejected - breaks debugging (can't verify resource URLs)
2. **Mask nothing**: Rejected - security risk (params might contain tokens)

**Decision**: Mask all params except those starting with "resource"

**Rationale**: Resource URLs are public endpoints (not sensitive), while other custom params (e.g., `api_key`, `token`) might contain secrets.

---

### Q7: What OAuth endpoints need extra params?

**Finding**: ✅ All three: authorization, token exchange, token refresh

**RFC 8707 Requirement** (Section 2.1):
> "The resource parameter is used to indicate the target service or resource to which access is being requested. Its value MUST be an absolute URI... The client MUST include the resource parameter in both the authorization request and the token request."

**Endpoints**:
1. **Authorization** (`GET /authorize`): Query string parameters
2. **Token Exchange** (`POST /token`): Form-encoded body parameters
3. **Token Refresh** (`POST /token`): Form-encoded body parameters

**Decision**: Wrapper must handle all three endpoints

**Rationale**: RFC 8707 compliance requires params in both authorization and token requests. Omitting refresh would break token lifecycle.

---

## Best Practices Applied

### Wrapper Pattern (Gang of Four Decorator)

**Pattern**: Structural design pattern to extend behavior dynamically

**Benefits**:
- Single Responsibility Principle: Wrapper only adds params, mcp-go handles OAuth
- Open/Closed Principle: Extends without modifying mcp-go
- Easy to remove: Delete wrapper when mcp-go adds native support

**Implementation**:
```go
// Decorator wraps component without changing its interface
type OAuthTransportWrapper struct {
    inner *client.OAuthHandler // Delegate to wrapped object
}
```

**References**:
- *Design Patterns* (Gamma et al., 1994), Chapter "Decorator"
- Go idiom: "Composition over inheritance"

---

### Fail-Fast Configuration Validation

**Pattern**: Validate inputs at system boundary (config load)

**Benefits**:
- Immediate feedback to developers
- Prevents runtime errors during OAuth flow
- Clear error messages with actionable guidance

**Implementation**:
```go
func (c *Config) Load() error {
    // Parse config
    // Validate immediately
    if err := c.Validate(); err != nil {
        return fmt.Errorf("config validation failed: %w", err)
    }
    // Use config
}
```

**References**:
- *Release It!* (Nygard, 2018), Chapter "Fail Fast"
- Go proverb: "Errors are values"

---

### URL Encoding Best Practices

**Pattern**: Use stdlib for security-critical operations

**Security**: Proper URL encoding prevents injection attacks

**Implementation**:
```go
import "net/url"

func appendParams(base string, params map[string]string) string {
    u, _ := url.Parse(base)
    q := u.Query()
    for k, v := range params {
        q.Set(k, v) // stdlib handles encoding
    }
    u.RawQuery = q.Encode()
    return u.String()
}
```

**Anti-Pattern**: Manual string concatenation (`base + "&resource=" + value`)

**Why Avoid**: Vulnerable to URL injection if values contain special chars (`&`, `=`, `%`)

**References**:
- OWASP Top 10: Injection vulnerabilities
- Go `net/url` package documentation

---

## Technology Choices

### Go 1.24.0

**Chosen**: Continue using existing Go version

**Rationale**: No new language features required for wrapper pattern

**Alternatives**: Upgrade to Go 1.25 (rejected - no compelling features)

---

### mcp-go v0.43.1

**Chosen**: Upgrade from v0.42.0 to v0.43.1

**Rationale**: Session management fixes for multi-instance deployments

**Alternatives**:
- Stay on v0.42.0: Rejected - missing stability fixes
- Wait for v0.44.0: Rejected - no release timeline

---

### BBolt (Embedded Database)

**Chosen**: Continue using existing storage

**Rationale**: No schema changes required (OAuth tokens already stored)

**Alternatives**: PostgreSQL, SQLite (rejected - unnecessary complexity)

---

### Bleve (Search Index)

**Chosen**: Continue using existing search

**Rationale**: No search changes required for OAuth feature

---

## Performance Analysis

### Wrapper Overhead

**Measurement**: URL manipulation benchmarks

**Expected**:
- WrapAuthorizationURL: <1ms (URL parse + query append + encode)
- WrapTokenRequest: <2ms (body read + form parse + encode + write)

**Acceptable**: <5ms total OAuth overhead (negligible compared to network latency)

**Measurement Plan**: Add benchmarks in `transport_wrapper_test.go`

---

### Memory Impact

**Expected**: Minimal (few hundred bytes per OAuth config)

**Breakdown**:
- `ExtraParams map[string]string`: ~100 bytes (3-5 params × 20 bytes avg)
- `OAuthTransportWrapper struct`: ~50 bytes (2 pointers)
- Total per server: ~150 bytes

**Acceptable**: <1KB per server (MCPProxy targets 1000 servers = ~1MB total)

---

## Security Analysis

### Threat Model

**Threat**: Malicious config attempts to hijack OAuth flow

**Attack Vector**: Override critical parameters via extra_params
```json
{
  "extra_params": {
    "client_id": "attacker-client",
    "redirect_uri": "https://evil.com/steal-tokens"
  }
}
```

**Mitigation**: Reserved parameter validation
```go
var reservedOAuthParams = map[string]bool{
    "client_id": true,
    "redirect_uri": true,
    // ... all critical params
}
```

**Result**: Config load fails with clear error message

---

### Sensitive Data in Logs

**Risk**: Extra params might contain secrets (e.g., `api_key`)

**Mitigation**: Selective masking (resource URLs visible, others masked)

**Example**:
```
DEBUG OAuth extra params server=runlayer extra_params={"resource":"https://...","api_key":"***"}
```

---

## Integration Patterns

### Configuration Loading

**Pattern**: Existing mapstructure-based config loading

**No Changes Required**: mapstructure automatically handles new `extra_params` field

```go
type OAuthConfig struct {
    // ... existing fields ...
    ExtraParams map[string]string `json:"extra_params,omitempty" mapstructure:"extra_params"`
}
```

---

### Event-Driven Updates

**Pattern**: Existing event bus for config changes

**No Changes Required**: OAuth config changes already trigger `servers.changed` event

**Flow**:
1. User edits `mcp_config.json`
2. File watcher detects change
3. Config reloaded and validated
4. `servers.changed` event emitted
5. Tray UI updates via SSE

---

## References

### RFC Documents

- **RFC 8707**: Resource Indicators for OAuth 2.0 (https://www.rfc-editor.org/rfc/rfc8707.html)
- **RFC 6749**: OAuth 2.0 Authorization Framework (https://www.rfc-editor.org/rfc/rfc6749.html)
- **RFC 7636**: Proof Key for Code Exchange (PKCE) (https://www.rfc-editor.org/rfc/rfc7636.html)

### MCP Specification

- **MCP OAuth Spec**: https://modelcontextprotocol.io/specification/draft/basic/authorization
- **MCP Changelog**: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2025-06-18/changelog.mdx

### Code References

- **mcp-go v0.43.1**: https://github.com/mark3labs/mcp-go/tree/v0.43.1
- **mcp-go Issue #412**: https://github.com/mark3labs/mcp-go/issues/412
- **Official go-sdk**: https://github.com/modelcontextprotocol/go-sdk

### Project Documents

- **SDK Comparison**: [sdk-comparison-analysis.md](sdk-comparison-analysis.md)
- **Original Plan**: [docs/plans/2025-11-27-oauth-extra-params.md](../../docs/plans/2025-11-27-oauth-extra-params.md)
- **Feature Spec**: [spec.md](spec.md)
