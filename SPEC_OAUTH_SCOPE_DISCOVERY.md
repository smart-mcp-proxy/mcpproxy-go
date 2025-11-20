# Specification: OAuth Scope Auto-Discovery Fix

**Issue**: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/131

## Problem Statement

mcpproxy currently uses hardcoded default OAuth scopes `["mcp.read", "mcp.write"]` which:
1. Are NOT part of the MCP specification
2. Cause failures with standard OAuth providers (Google, GitHub, etc.)
3. Create poor UX - users must manually configure scopes in config files

## Test Case: GitHub MCP Server

### Current Behavior (BROKEN)
```bash
$ ./mcpproxy tools list --server=github --log-level=trace
# Result: OAuth authorization required - deferred for background processing
# Reason: Attempting to use scopes ["mcp.read", "mcp.write"] which GitHub doesn't support
```

### Expected Behavior (FIXED)
```bash
$ ./mcpproxy tools list --server=github --log-level=trace
# Result: Auto-discovers scopes from Protected Resource Metadata
# Uses: ["repo", "user:email"] (or whatever GitHub specifies)
# Result: OAuth flow succeeds
```

## Discovery Flow (MCP Spec Compliant)

### Step 1: Protected Resource Metadata (RFC 9728) - PRIMARY
When server returns `401 Unauthorized`, check `WWW-Authenticate` header:

```http
HTTP/1.1 401 Unauthorized
www-authenticate: Bearer error="invalid_request",
                 error_description="No access token was provided",
                 resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly"
```

Fetch the `resource_metadata` URL:
```json
{
  "resource_name": "GitHub MCP Server",
  "resource": "https://api.githubcopilot.com/mcp/readonly",
  "authorization_servers": [
    "https://github.com/login/oauth"
  ],
  "bearer_methods_supported": ["header"],
  "scopes_supported": [
    "gist", "notifications", "public_repo", "repo",
    "repo:status", "repo_deployment", "user", "user:email",
    "user:follow", "read:gpg_key", "read:org", "project"
  ]
}
```

✅ **Use `scopes_supported` array**

### Step 2: OAuth Authorization Server Metadata (RFC 8414) - FALLBACK
Fetch `{baseURL}/.well-known/oauth-authorization-server`:
```json
{
  "issuer": "https://server.example.com",
  "authorization_endpoint": "https://server.example.com/authorize",
  "token_endpoint": "https://server.example.com/token",
  "scopes_supported": ["openid", "email", "profile", "mcp:tools:read"]
}
```

✅ **Use `scopes_supported` array**

### Step 3: Config-Specified Scopes - MANUAL OVERRIDE
```json
{
  "name": "github",
  "url": "https://api.githubcopilot.com/mcp/readonly",
  "oauth": {
    "scopes": ["repo", "user:email"]
  }
}
```

✅ **Use `oauth.scopes` array**

### Step 4: Empty Scopes - FINAL FALLBACK
```go
scopes := []string{} // Empty scopes are valid OAuth 2.1
```

✅ **Server will specify required scopes via `WWW-Authenticate` header**

### Step 5: NEVER - REMOVE HARDCODED DEFAULTS
❌ **Remove**: `scopes := []string{"mcp.read", "mcp.write"}`

## Implementation Plan

### Phase 1: Add Protected Resource Metadata Discovery

**File**: `internal/oauth/discovery.go` (new file)

```go
package oauth

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"
)

// ProtectedResourceMetadata represents RFC 9728 Protected Resource Metadata
type ProtectedResourceMetadata struct {
    Resource                 string   `json:"resource"`
    ResourceName             string   `json:"resource_name,omitempty"`
    AuthorizationServers     []string `json:"authorization_servers"`
    BearerMethodsSupported   []string `json:"bearer_methods_supported,omitempty"`
    ScopesSupported          []string `json:"scopes_supported,omitempty"`
}

// OAuthServerMetadata represents RFC 8414 OAuth Authorization Server Metadata
type OAuthServerMetadata struct {
    Issuer                            string   `json:"issuer"`
    AuthorizationEndpoint             string   `json:"authorization_endpoint"`
    TokenEndpoint                     string   `json:"token_endpoint"`
    ScopesSupported                   []string `json:"scopes_supported,omitempty"`
    ResponseTypesSupported            []string `json:"response_types_supported"`
    GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
    TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
    RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
    RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
}

// DiscoverScopesFromProtectedResource attempts to discover scopes from Protected Resource Metadata (RFC 9728)
func DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Accept", "application/json")

    client := &http.Client{Timeout: timeout}
    resp, err := client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to fetch metadata: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
    }

    var metadata ProtectedResourceMetadata
    if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
        return nil, fmt.Errorf("failed to parse metadata: %w", err)
    }

    if len(metadata.ScopesSupported) == 0 {
        return nil, fmt.Errorf("no scopes_supported in metadata")
    }

    return metadata.ScopesSupported, nil
}

// DiscoverScopesFromAuthorizationServer attempts to discover scopes from OAuth Server Metadata (RFC 8414)
func DiscoverScopesFromAuthorizationServer(baseURL string, timeout time.Duration) ([]string, error) {
    metadataURL := baseURL + "/.well-known/oauth-authorization-server"

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Accept", "application/json")

    client := &http.Client{Timeout: timeout}
    resp, err := client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to fetch metadata: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
    }

    var metadata OAuthServerMetadata
    if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
        return nil, fmt.Errorf("failed to parse metadata: %w", err)
    }

    if len(metadata.ScopesSupported) == 0 {
        return nil, fmt.Errorf("no scopes_supported in metadata")
    }

    return metadata.ScopesSupported, nil
}

// ExtractResourceMetadataURL parses WWW-Authenticate header to extract resource_metadata URL
func ExtractResourceMetadataURL(wwwAuthHeader string) string {
    // Parse: Bearer resource_metadata="https://..."
    if !strings.Contains(wwwAuthHeader, "resource_metadata") {
        return ""
    }

    parts := strings.Split(wwwAuthHeader, "resource_metadata=\"")
    if len(parts) < 2 {
        return ""
    }

    endIdx := strings.Index(parts[1], "\"")
    if endIdx == -1 {
        return ""
    }

    return parts[1][:endIdx]
}
```

### Phase 2: Modify OAuth Config Creation

**File**: `internal/oauth/config.go:184-203`

```go
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig {
    logger := zap.L().Named("oauth")

    var scopes []string

    // 1. Try config-specified scopes FIRST (highest priority - user override)
    if serverConfig.OAuth != nil && len(serverConfig.OAuth.Scopes) > 0 {
        scopes = serverConfig.OAuth.Scopes
        logger.Info("✅ Using config-specified OAuth scopes",
            zap.String("server", serverConfig.Name),
            zap.Strings("scopes", scopes))
    }

    // 2. Try auto-discovery from Protected Resource Metadata (RFC 9728)
    if len(scopes) == 0 {
        baseURL, err := parseBaseURL(serverConfig.URL)
        if err == nil && baseURL != "" {
            logger.Debug("Attempting Protected Resource Metadata scope discovery",
                zap.String("server", serverConfig.Name),
                zap.String("base_url", baseURL))

            // Make a preflight request to get WWW-Authenticate header
            resp, err := http.Head(serverConfig.URL)
            if err == nil && resp.StatusCode == 401 {
                wwwAuth := resp.Header.Get("WWW-Authenticate")
                if metadataURL := ExtractResourceMetadataURL(wwwAuth); metadataURL != "" {
                    discoveredScopes, err := DiscoverScopesFromProtectedResource(metadataURL, 5*time.Second)
                    if err == nil {
                        scopes = discoveredScopes
                        logger.Info("✅ Auto-discovered OAuth scopes from Protected Resource Metadata (RFC 9728)",
                            zap.String("server", serverConfig.Name),
                            zap.String("metadata_url", metadataURL),
                            zap.Strings("scopes", scopes))
                    } else {
                        logger.Debug("Protected Resource Metadata discovery failed",
                            zap.String("server", serverConfig.Name),
                            zap.Error(err))
                    }
                }
            }
        }
    }

    // 3. Fallback to Authorization Server Metadata (RFC 8414)
    if len(scopes) == 0 {
        baseURL, err := parseBaseURL(serverConfig.URL)
        if err == nil && baseURL != "" {
            logger.Debug("Attempting Authorization Server Metadata scope discovery",
                zap.String("server", serverConfig.Name),
                zap.String("base_url", baseURL))

            discoveredScopes, err := DiscoverScopesFromAuthorizationServer(baseURL, 5*time.Second)
            if err == nil {
                scopes = discoveredScopes
                logger.Info("✅ Auto-discovered OAuth scopes from Authorization Server Metadata (RFC 8414)",
                    zap.String("server", serverConfig.Name),
                    zap.Strings("scopes", scopes))
            } else {
                logger.Debug("Authorization Server Metadata discovery failed",
                    zap.String("server", serverConfig.Name),
                    zap.Error(err))
            }
        }
    }

    // 4. Final fallback: empty scopes (let server specify via WWW-Authenticate)
    if len(scopes) == 0 {
        scopes = []string{}
        logger.Info("Using empty scopes - server will specify required scopes via WWW-Authenticate header",
            zap.String("server", serverConfig.Name))
    }

    // NOTE: Removed hardcoded "mcp.read", "mcp.write" defaults
    // They are not part of the MCP specification and cause OAuth failures

    // ... rest of the function unchanged
}
```

### Phase 3: Add Caching

**File**: `internal/oauth/discovery.go` (continued)

```go
var (
    scopeCache   = make(map[string]cachedScopes)
    scopeCacheMu sync.RWMutex
    scopeCacheTTL = 30 * time.Minute
)

type cachedScopes struct {
    scopes    []string
    timestamp time.Time
}

func DiscoverScopesFromProtectedResourceCached(metadataURL string, timeout time.Duration) ([]string, error) {
    scopeCacheMu.RLock()
    if cached, ok := scopeCache[metadataURL]; ok {
        if time.Since(cached.timestamp) < scopeCacheTTL {
            scopeCacheMu.RUnlock()
            return cached.scopes, nil
        }
    }
    scopeCacheMu.RUnlock()

    scopes, err := DiscoverScopesFromProtectedResource(metadataURL, timeout)
    if err != nil {
        return nil, err
    }

    scopeCacheMu.Lock()
    scopeCache[metadataURL] = cachedScopes{
        scopes:    scopes,
        timestamp: time.Now(),
    }
    scopeCacheMu.Unlock()

    return scopes, nil
}
```

### Phase 4: Improve Error Reporting

**File**: `internal/upstream/core/connection.go:1724`

```go
if regErr != nil {
    // Check if it's an invalid_scope error
    if strings.Contains(regErr.Error(), "invalid_scope") ||
       strings.Contains(regErr.Error(), "scope") {
        return fmt.Errorf("failed to register client: invalid OAuth scopes. "+
            "The server doesn't support the requested scopes %v. "+
            "Try adding 'oauth.scopes' to your server config with the correct scopes, "+
            "or check the server's /.well-known/oauth-protected-resource metadata. "+
            "Original error: %w", scopes, regErr)
    }
    return fmt.Errorf("failed to register client: %w", regErr)
}
```

## Testing

### Test 1: GitHub MCP Server (No DCR, Protected Resource Metadata)
```bash
# Remove any OAuth config
jq 'del(.mcpServers[] | select(.name == "github") | .oauth)' ~/.mcpproxy/mcp_config.json > /tmp/config.json
mv /tmp/config.json ~/.mcpproxy/mcp_config.json

# Test scope discovery
./mcpproxy auth login --server=github --log-level=debug

# Expected: Discovers scopes from https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly
# Expected: Uses ["repo", "user:email", ...] instead of ["mcp.read", "mcp.write"]
```

### Test 2: Manual Config Override
```json
{
  "name": "github",
  "url": "https://api.githubcopilot.com/mcp/readonly",
  "oauth": {
    "scopes": ["repo", "user"]
  }
}
```

```bash
./mcpproxy auth login --server=github --log-level=debug
# Expected: Uses ["repo", "user"] from config (overrides discovery)
```

### Test 3: Empty Scopes Fallback
```json
{
  "name": "test-server",
  "url": "https://server-without-metadata.com/mcp"
}
```

```bash
./mcpproxy auth login --server=test-server --log-level=debug
# Expected: Uses [] empty scopes
# Expected: Server responds with WWW-Authenticate header specifying required scopes
```

## Success Criteria

1. ✅ GitHub MCP server OAuth works without manual config
2. ✅ No hardcoded `["mcp.read", "mcp.write"]` defaults
3. ✅ Config-specified scopes still work (backwards compatible)
4. ✅ Caching prevents repeated metadata fetches
5. ✅ Better error messages when OAuth fails
6. ✅ Logs clearly show which discovery method was used

## Backwards Compatibility

- ✅ Config-specified `oauth.scopes` still works (highest priority)
- ✅ Servers without metadata discovery get empty scopes (valid OAuth 2.1)
- ✅ No breaking changes to existing configurations
