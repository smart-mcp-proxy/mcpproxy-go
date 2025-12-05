# SDK Comparison & Implementation Options Analysis

**Date**: 2025-11-30
**Purpose**: Evaluate implementation options for OAuth extra_params support
**Scope**: Compare mark3labs/mcp-go vs official modelcontextprotocol/go-sdk

## Executive Summary

**Recommendation**: **Stay with mark3labs/mcp-go v0.42.0** and implement the wrapper pattern. Switching SDKs offers no immediate benefit for the extra_params feature and introduces significant migration risk.

**Key Findings**:
- ✅ **mcp-go v0.43.1**: No OAuth improvements, still requires wrapper pattern
- ❌ **Official go-sdk**: Uses `golang.org/x/oauth2` (no extra params support), requires OAuth handler callback implementation
- ⚠️ **Issue #412**: Open since June 2025 with no activity, not prioritized
- 📊 **Migration Cost**: High (3-5 weeks) vs Wrapper Pattern (1-2 weeks from existing plan)

## Option 1: Upgrade to mcp-go v0.43.1

### Changes in v0.43.1

**Release Notes Analysis**:
- `create StatelessGeneratingSessionIdManager to fix multi-instance deployments`
- `implement SessionWithClientInfo for streamableHttpSession`

**OAuth Impact**: **NONE**

The v0.43.1 release focuses on session management improvements for distributed deployments. No OAuth-related changes are included.

### Verification

**Source Code Review** of `client/transport/oauth.go` (v0.43.1):
```go
// GetAuthorizationURL (lines 686-712) - unchanged from v0.42.0
func (h *OAuthHandler) GetAuthorizationURL(ctx context.Context, state, codeChallenge string) (string, error) {
    // ... same hardcoded params ...
    params.Set("response_type", "code")
    params.Set("client_id", h.config.ClientID)
    params.Set("redirect_uri", h.config.RedirectURI)
    params.Set("state", state)
    // NO mechanism for extra params
}
```

**Conclusion**: Upgrading to v0.43.1 provides no benefit for OAuth extra_params implementation.

## Option 2: Monitor Issue #412

### Issue Status

**Title**: RFC 8707 Resource Indicators Implementation
**URL**: https://github.com/mark3labs/mcp-go/issues/412
**Created**: June 19, 2025
**Status**: Open
**Priority**: Medium
**Assignee**: Unassigned
**Comments**: 0 (zero activity)

### Issue Scope

**Requirements** (from issue description):
1. Add `resource` parameters to OAuth authorization requests
2. Implement server-side validation
3. Add security checks against token misuse
4. Update documentation with examples
5. Create comprehensive security tests

**Files Affected**: `client/oauth.go` and OAuth transport implementations

### Analysis

**Pros**:
- If implemented, would provide native support for RFC 8707
- No wrapper pattern needed
- Official SDK support

**Cons**:
- **Zero activity** since creation (6 months)
- **Unassigned** - no developer committed
- No timeline or milestone
- **Scope creep risk**: Issue asks for full RFC 8707 including server validation, not just client extra_params
- **Blocks Runlayer integration** indefinitely

**Conclusion**: Cannot rely on upstream timeline. Issue #412 is aspirational, not actionable.

## Option 3: Switch to Official modelcontextprotocol/go-sdk

### SDK Overview

**Repository**: https://github.com/modelcontextprotocol/go-sdk
**Maturity**: v1.1.0 (October 2025), 3.2k stars, 511 commits, 64 contributors
**Maintenance**: Official SDK, maintained with Google collaboration
**License**: MIT

### Architecture Differences

#### mark3labs/mcp-go (Current)
```go
// Integrated OAuth implementation
type OAuthConfig struct {
    ClientID     string
    ClientSecret string
    RedirectURI  string
    Scopes       []string
    PKCEEnabled  bool
    TokenStore   TokenStore
}

// Built-in OAuth flow
client.NewOAuthStreamableHttpClient(url, oauthConfig)
```

#### modelcontextprotocol/go-sdk (Official)
```go
// Callback-based OAuth (delegates to golang.org/x/oauth2)
type HTTPTransport struct {
    // Intercepts 401, calls OAuthHandler callback
}

type OAuthHandler func(ctx context.Context, r *http.Request) (oauth2.TokenSource, error)

// User must implement OAuth flow using golang.org/x/oauth2
transport := &auth.HTTPTransport{
    OAuthHandler: func(ctx, r) { /* implement OAuth flow */ }
}
```

### OAuth Capabilities Comparison

| Feature | mark3labs/mcp-go | Official go-sdk |
|---------|------------------|-----------------|
| **OAuth Integration** | Built-in, batteries included | Callback-based, BYO implementation |
| **OAuth Library** | Custom implementation | Delegates to `golang.org/x/oauth2` |
| **PKCE Support** | Native | Via `golang.org/x/oauth2` |
| **Extra Params Support** | ❌ No (hardcoded) | ❌ No (golang.org/x/oauth2 limitation) |
| **RFC 8707 Support** | ❌ No | ❌ No |
| **Token Management** | Built-in TokenStore | User-provided oauth2.TokenSource |
| **Discovery** | Built-in (RFC 8414, RFC 9728) | Via oauthex package utilities |

### golang.org/x/oauth2 Limitation

The official SDK delegates OAuth to `golang.org/x/oauth2`, which **ALSO does not support arbitrary extra parameters** in the standard API:

```go
// golang.org/x/oauth2 Config
type Config struct {
    ClientID     string
    ClientSecret string
    Endpoint     Endpoint
    RedirectURL  string
    Scopes       []string
    // NO ExtraParams field
}

// AuthCodeURL generates authorization URL
func (c *Config) AuthCodeURL(state string, opts ...AuthCodeOption) string {
    // Only supports AuthCodeOption for specific OAuth extensions
    // No general-purpose extra params mechanism
}
```

**Available AuthCodeOptions**:
- `SetAuthURLParam(key, value)` - **CAN be used for extra params!**

**Example**:
```go
authURL := config.AuthCodeURL(state,
    oauth2.SetAuthURLParam("resource", "https://example.com/mcp"))
```

### Implementation Effort with Official SDK

**Wrapper Still Required**: Even with the official SDK, you need to wrap `golang.org/x/oauth2.Config` to inject extra params via `SetAuthURLParam`.

**New Code Required**:
1. **OAuth Handler Implementation**: ~200-300 lines to replicate current mcp-go behavior
2. **Token Management**: Implement oauth2.TokenSource wrapper for persistence
3. **Discovery Integration**: Integrate oauthex utilities
4. **PKCE Generation**: Use oauth2 PKCE extensions
5. **Extra Params Wrapper**: Same wrapper pattern, different API

**No Advantage**: The wrapper pattern is still required, just with a different underlying library.

### Migration Effort Estimate

#### Files Requiring Changes

**Core Integration** (~3500 lines affected):
- `internal/oauth/config.go` (682 lines) - complete rewrite
- `internal/transport/http.go` (100+ lines) - transport creation logic
- `internal/upstream/core/connection.go` (2304 lines) - OAuth flow handling
- `internal/upstream/core/client.go` (536 lines) - client creation

**Testing** (~1000 lines):
- All OAuth unit tests need rewriting
- E2E tests need OAuth mocking updates
- Integration test adjustments

**Total Code Impact**: ~4500 lines

#### Time Estimate

| Phase | Duration | Effort |
|-------|----------|--------|
| **Phase 1**: SDK familiarization | 3-4 days | Learn official SDK OAuth patterns, understand oauthex utilities |
| **Phase 2**: OAuth handler implementation | 4-5 days | Implement OAuth flow with golang.org/x/oauth2, token management, discovery |
| **Phase 3**: Integration | 5-7 days | Update all client creation, connection management, transport layer |
| **Phase 4**: Testing | 5-7 days | Rewrite tests, verify OAuth flows, regression testing |
| **Phase 5**: Extra params wrapper | 3-4 days | Implement SetAuthURLParam wrapper (same complexity as mcp-go wrapper) |
| **Phase 6**: Documentation | 2-3 days | Update all OAuth documentation, examples, troubleshooting |
| **Total** | **22-30 days** | **3-5 weeks of development time** |

### Risk Assessment

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| **Regression in existing OAuth** | High | Medium | Comprehensive testing, phased rollout |
| **Token persistence issues** | High | Medium | Thorough TokenSource implementation testing |
| **Discovery compatibility** | Medium | Low | Use oauthex utilities as designed |
| **E2E test breakage** | Medium | High | Update all OAuth mocking infrastructure |
| **Production OAuth failures** | Critical | Low | Extensive testing before rollout |
| **Extra params still need wrapper** | Medium | High | No mitigation - inherent SDK limitation |

### Benefits of Official SDK (Non-OAuth)

**Real Benefits**:
1. **Long-term maintenance**: Official SDK has organizational backing
2. **Spec compliance**: Guaranteed to follow MCP spec updates
3. **Community support**: Larger user base (3.2k stars vs mark3labs)
4. **Future-proof**: Likely to receive OAuth extensions eventually

**But**: None of these benefits help with **immediate** extra_params implementation.

## Option 4: Wrapper Pattern with mcp-go v0.42.0 (Recommended)

### Approach

Use the wrapper/decorator pattern as outlined in `docs/plans/2025-11-27-oauth-extra-params.md`:

```go
// internal/oauth/transport_wrapper.go
type OAuthTransportWrapper struct {
    inner       client.Transport
    extraParams map[string]string
}

func (w *OAuthTransportWrapper) WrapAuthorizationURL(baseURL string) string {
    if len(w.extraParams) == 0 {
        return baseURL
    }
    u, _ := url.Parse(baseURL)
    q := u.Query()
    for k, v := range w.extraParams {
        q.Set(k, v)
    }
    u.RawQuery = q.Encode()
    return u.String()
}
```

### Implementation Timeline

**From existing plan** (docs/plans/2025-11-27-oauth-extra-params.md):

| Phase | Duration | Deliverable |
|-------|----------|-------------|
| Phase 1: Config | 2 days | Add ExtraParams to config.OAuthConfig |
| Phase 2: Wrapper | 3 days | OAuth transport wrapper implementation |
| Phase 3: Integration | 3 days | End-to-end OAuth flow with wrapper |
| Phase 4: Testing | 4 days | Unit, integration, E2E tests |
| Phase 5: Documentation | 2 days | User guides, API docs |
| **Total** | **14 days** | **2-3 weeks** |

### Advantages

✅ **Fast time to value**: 2-3 weeks vs 3-5 weeks for SDK switch
✅ **Low risk**: No changes to core OAuth flow, only URL modification
✅ **Backward compatible**: Zero impact on existing configs
✅ **Tested pattern**: Well-understood decorator pattern
✅ **Easy to remove**: When/if mcp-go adds native support, wrapper can be deleted
✅ **Focused scope**: Solves exactly the problem at hand

### Disadvantages

⚠️ **Maintenance burden**: Need to maintain wrapper code
⚠️ **mcp-go coupling**: Tightly coupled to mcp-go OAuth implementation
⚠️ **No upstream contribution**: Doesn't improve mcp-go for others

**Mitigation**: Plan to contribute wrapper functionality to mcp-go upstream after proven in production (Phase 6 of existing plan).

## Decision Matrix

| Criterion | mcp-go v0.43.1 | Wait for #412 | Switch to go-sdk | Wrapper Pattern |
|-----------|----------------|---------------|------------------|-----------------|
| **Time to Ship** | N/A (no benefit) | ∞ (blocked) | 3-5 weeks | 2-3 weeks ✅ |
| **Risk** | N/A | N/A (blocked) | High | Low ✅ |
| **Extra Params Support** | ❌ Still needs wrapper | ⏳ Indefinite wait | ❌ Still needs wrapper | ✅ Immediate |
| **Backward Compat** | ✅ Drop-in | ✅ N/A | ❌ Breaking change | ✅ Additive only |
| **Code Changes** | None | None | ~4500 lines | ~500 lines ✅ |
| **Test Changes** | None | None | ~1000 lines | ~200 lines ✅ |
| **Future-Proofing** | ❓ Unclear roadmap | ⏳ Waiting on maintainer | ✅ Official SDK | ❓ Depends on #412 |
| **Runlayer Integration** | ❌ Blocked | ❌ Blocked | ⏳ 3-5 weeks | ✅ 2-3 weeks |

## Recommendations

### Immediate (Next Sprint)

**✅ RECOMMENDED**: Implement wrapper pattern with mcp-go v0.42.0 (Option 4)

**Rationale**:
1. **Unblocks Runlayer integration** in 2-3 weeks
2. **Lowest risk** approach - no core OAuth changes
3. **Proven pattern** - decorator is well-understood
4. **Fast time to value** - half the time of SDK switch
5. **Easy to remove** when native support arrives

### Short-term (3-6 months)

**Monitor** mcp-go issue #412 and official go-sdk development:
- Watch for activity on #412 (upvote, comment to show demand)
- Track official go-sdk OAuth enhancements
- Evaluate switching when either SDK provides native extra_params support

### Long-term (6-12 months)

**Contribute wrapper to upstream mcp-go**:
- Create PR to mcp-go with ExtraParams field in OAuthConfig
- Reference RFC 8707 use case (Runlayer)
- Provide comprehensive tests and documentation
- Help maintain feature if accepted

**Re-evaluate SDK choice**:
- If mcp-go #412 ships → adopt native implementation, remove wrapper
- If official go-sdk adds extra_params → re-evaluate migration value
- If neither ships → maintain wrapper, contribute more aggressively upstream

## Conclusion

**No silver bullet exists**. All options require a wrapper pattern for extra_params support:

- mcp-go v0.43.1: Still needs wrapper ❌
- Issue #412: Blocked indefinitely ❌
- Official go-sdk: golang.org/x/oauth2 also needs wrapper ❌
- Wrapper pattern: Explicitly designed for this ✅

The wrapper pattern with mcp-go v0.42.0 is the **fastest, lowest-risk path** to enable Runlayer integration while keeping options open for future SDK evolution.

## Appendix: Technical Deep Dive

### golang.org/x/oauth2 Extra Params Support

While `golang.org/x/oauth2` doesn't have a dedicated `ExtraParams` field, it provides `AuthCodeOption` for extending OAuth requests:

```go
// Working example with golang.org/x/oauth2
config := &oauth2.Config{
    ClientID:     "client-id",
    ClientSecret: "client-secret",
    RedirectURL:  "http://localhost:8080/callback",
    Scopes:       []string{"read", "write"},
    Endpoint: oauth2.Endpoint{
        AuthURL:  "https://provider.com/authorize",
        TokenURL: "https://provider.com/token",
    },
}

// Add RFC 8707 resource parameter
authURL := config.AuthCodeURL(state,
    oauth2.SetAuthURLParam("resource", "https://example.com/mcp"),
    oauth2.SetAuthURLParam("audience", "mcp-api"))
```

**Implication**: Official go-sdk could support extra_params by wrapping SetAuthURLParam, but:
1. Still requires wrapper pattern (no built-in ExtraParams config)
2. Only covers authorization URL, not token/refresh requests
3. User must manually construct SetAuthURLParam calls

### mcp-go Wrapper Implementation Preview

**Minimal wrapper example**:

```go
package oauth

import (
    "net/url"
    "github.com/mark3labs/mcp-go/client"
)

// ExtraParamsConfig extends client.OAuthConfig with extra params
type ExtraParamsConfig struct {
    *client.OAuthConfig
    ExtraParams map[string]string
}

// InjectExtraParams wraps an OAuth handler to add extra params
func InjectExtraParams(handler *client.OAuthHandler, extraParams map[string]string) *client.OAuthHandler {
    // Wrap GetAuthorizationURL
    originalGetAuthURL := handler.GetAuthorizationURL
    handler.GetAuthorizationURL = func(ctx context.Context, state, codeChallenge string) (string, error) {
        baseURL, err := originalGetAuthURL(ctx, state, codeChallenge)
        if err != nil || len(extraParams) == 0 {
            return baseURL, err
        }

        u, _ := url.Parse(baseURL)
        q := u.Query()
        for k, v := range extraParams {
            q.Set(k, v)
        }
        u.RawQuery = q.Encode()
        return u.String(), nil
    }

    return handler
}
```

**Complexity**: ~50-100 lines for full implementation (authorization + token + refresh)

## Sources

- [GitHub - modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)
- [MCP Specification - Authorization](https://modelcontextprotocol.io/specification/draft/basic/authorization)
- [RFC 8707: Resource Indicators for OAuth 2.0](https://www.rfc-editor.org/rfc/rfc8707.html)
- [MCP Spec Updates from June 2025 (Auth0 Blog)](https://auth0.com/blog/mcp-specs-update-all-about-auth/)
- [mcp-go Issue #412: RFC 8707 Resource Indicators Implementation](https://github.com/mark3labs/mcp-go/issues/412)
