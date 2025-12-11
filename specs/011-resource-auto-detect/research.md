# Research: RFC 8707 Resource Auto-Detection

**Feature**: 011-resource-auto-detect
**Date**: 2025-12-10

## Research Questions

### Q1: Where does resource metadata come from?

**Decision**: Extract from RFC 9728 Protected Resource Metadata `resource` field.

**Rationale**:
- RFC 9728 defines Protected Resource Metadata as a JSON document containing `resource` (the resource identifier), `scopes_supported`, and `authorization_servers`
- MCPProxy already fetches this metadata in `DiscoverScopesFromProtectedResource()` for scope discovery
- The `resource` field is parsed but currently discarded - we just need to return it

**Alternatives Considered**:
1. ❌ Use server URL as resource always - Won't work for proxies like Runlayer where resource URL differs from server URL
2. ❌ Add mandatory config field - Defeats zero-config goal
3. ✅ Extract from metadata, fallback to server URL - Best of both worlds

### Q2: How to inject resource into authorization URL?

**Decision**: Inject after mcp-go constructs the authorization URL by parsing and modifying query parameters.

**Rationale**:
- mcp-go v0.43.1 doesn't support `ExtraParams` in its `OAuthConfig` struct
- The authorization URL is constructed internally by `oauthHandler.GetAuthorizationURL()`
- We can parse the returned URL, add the `resource` parameter, and use the modified URL

**Alternatives Considered**:
1. ❌ Fork mcp-go - High maintenance burden
2. ❌ Wait for upstream support - Blocks feature indefinitely
3. ✅ Post-construction URL injection - Works now, can be removed if upstream adds support

### Q3: How to inject resource into token requests?

**Decision**: Use existing `OAuthTransportWrapper` for token exchange and refresh requests.

**Rationale**:
- The transport wrapper already exists and injects `extra_params` into POST bodies
- Token exchange (`/token`) and token refresh both use POST with form-encoded body
- The wrapper pattern works transparently without mcp-go modifications

**Alternatives Considered**:
1. ❌ Patch mcp-go HTTP client - Invasive and fragile
2. ✅ Transport wrapper - Already implemented and working for `extra_params`

### Q4: How to handle CreateOAuthConfig() signature change?

**Decision**: Return tuple `(*client.OAuthConfig, map[string]string)` and update all 4 call sites.

**Rationale**:
- Go doesn't have optional returns, so we need to change the signature
- All call sites are in `internal/upstream/core/connection.go` (4 locations)
- The change is mechanical and contained within the package

**Alternatives Considered**:
1. ❌ Add separate function - Code duplication
2. ❌ Use context to pass extra params - Unclear ownership
3. ✅ Return tuple - Explicit, clear, Go-idiomatic

### Q5: What if metadata discovery fails?

**Decision**: Fall back to using the server URL as the `resource` parameter.

**Rationale**:
- RFC 8707 allows the resource to be the server URL
- This provides a sensible default that works for most non-proxy cases
- Users can still override with manual `extra_params.resource` if needed

**Alternatives Considered**:
1. ❌ Fail the OAuth flow - Too strict, breaks backward compatibility
2. ❌ Skip resource parameter - Would fail for Runlayer and similar providers
3. ✅ Fallback to server URL - Works for most cases, can be overridden

## Existing Code Analysis

### Current DiscoverScopesFromProtectedResource()

**Location**: `internal/oauth/discovery.go:59-130`

**Current behavior**:
1. Fetches metadata from URL extracted from `WWW-Authenticate` header
2. Parses JSON into `ProtectedResourceMetadata` struct
3. Returns only `metadata.ScopesSupported` (discards `metadata.Resource`)

**Change needed**:
- Add `DiscoverProtectedResourceMetadata()` that returns full struct
- Refactor `DiscoverScopesFromProtectedResource()` to delegate to new function

### Current CreateOAuthConfig()

**Location**: `internal/oauth/config.go:316-590`

**Current signature**: `func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig`

**Change needed**:
- New signature: `func CreateOAuthConfig(...) (*client.OAuthConfig, map[string]string)`
- Add resource detection logic before callback server setup
- Build `extraParams` map with auto-detected and manual values

### Current handleOAuthAuthorization()

**Location**: `internal/upstream/core/connection.go:~1739-1950`

**Current signature**: `func (c *Client) handleOAuthAuthorization(ctx context.Context, authErr error, oauthConfig *client.OAuthConfig) error`

**Change needed**:
- New signature: `func (c *Client) handleOAuthAuthorization(ctx context.Context, authErr error, oauthConfig *client.OAuthConfig, extraParams map[string]string) error`
- Add URL injection after `GetAuthorizationURL()` returns

### Call Sites to Update

1. `connection.go:~1108` - `tryOAuthAuth()`
2. `connection.go:~1557` - `trySSEOAuthAuth()`
3. `connection.go:~2436` - `forceHTTPOAuthFlow()`
4. `connection.go:~2495` - `forceSSEOAuthFlow()`

## Implementation Approach

### Step 1: Discovery Layer

```go
// NEW: Returns full metadata struct
func DiscoverProtectedResourceMetadata(metadataURL string, timeout time.Duration) (*ProtectedResourceMetadata, error) {
    // Same logic as current DiscoverScopesFromProtectedResource()
    // But return &metadata instead of metadata.ScopesSupported
}

// MODIFIED: Delegates to new function
func DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error) {
    metadata, err := DiscoverProtectedResourceMetadata(metadataURL, timeout)
    if err != nil {
        return nil, err
    }
    return metadata.ScopesSupported, nil
}
```

### Step 2: Config Layer

```go
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) (*client.OAuthConfig, map[string]string) {
    // ... existing setup ...

    // NEW: Auto-detect resource parameter
    var resourceURL string
    if metadataURL := ExtractResourceMetadataURL(wwwAuth); metadataURL != "" {
        metadata, err := DiscoverProtectedResourceMetadata(metadataURL, 5*time.Second)
        if err == nil && metadata.Resource != "" {
            resourceURL = metadata.Resource
        }
    }
    if resourceURL == "" {
        resourceURL = serverConfig.URL // Fallback
    }

    // ... existing OAuth config creation ...

    // Build extra params
    extraParams := map[string]string{"resource": resourceURL}

    // Merge manual overrides
    if serverConfig.OAuth != nil && serverConfig.OAuth.ExtraParams != nil {
        for k, v := range serverConfig.OAuth.ExtraParams {
            extraParams[k] = v
        }
    }

    return oauthConfig, extraParams
}
```

### Step 3: Connection Layer

```go
func (c *Client) handleOAuthAuthorization(ctx context.Context, authErr error, oauthConfig *client.OAuthConfig, extraParams map[string]string) error {
    // ... existing flow ...

    authURL, authURLErr = oauthHandler.GetAuthorizationURL(ctx, state, codeChallenge)
    if authURLErr != nil {
        return fmt.Errorf("failed to get authorization URL: %w", authURLErr)
    }

    // NEW: Inject extra params into authorization URL
    if len(extraParams) > 0 {
        u, err := url.Parse(authURL)
        if err != nil {
            return fmt.Errorf("failed to parse authorization URL: %w", err)
        }
        q := u.Query()
        for k, v := range extraParams {
            q.Set(k, v)
        }
        u.RawQuery = q.Encode()
        authURL = u.String()

        c.logger.Info("Added extra OAuth parameters to authorization URL",
            zap.String("server", c.config.Name),
            zap.Any("extra_params", extraParams))
    }

    // ... continue with browser launch ...
}
```

## Test Strategy

### Unit Tests

1. `TestDiscoverProtectedResourceMetadata_ReturnsFullStruct` - Verify resource field is returned
2. `TestCreateOAuthConfig_AutoDetectsResource` - Verify resource extraction
3. `TestCreateOAuthConfig_FallsBackToServerURL` - Verify fallback behavior
4. `TestCreateOAuthConfig_ManualOverride` - Verify manual `extra_params` takes precedence

### E2E Test

Modify `tests/oauthserver/` to add a `RequireResource` option that rejects requests missing the `resource` parameter. Then test full OAuth flow with auto-detection.

## References

- RFC 8707: https://www.rfc-editor.org/rfc/rfc8707.html
- RFC 9728: https://www.rfc-editor.org/rfc/rfc9728.html
- Existing implementation in `zero-config-oauth` branch
- `docs/zero-config-oauth-analysis.md` - Detailed analysis
- `docs/plans/2025-12-10-resource-auto-detection.md` - Initial plan
