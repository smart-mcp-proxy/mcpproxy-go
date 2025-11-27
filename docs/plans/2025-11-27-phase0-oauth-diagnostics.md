# Phase 0: OAuth Diagnostics & Error Reporting

**Status**: Ready for Implementation
**Priority**: P0 (Prerequisite for OAuth extra params)
**Estimated Duration**: 2 days
**Parent Plan**: docs/plans/2025-11-27-oauth-extra-params.md

## Problem Statement

Users cannot diagnose OAuth authentication failures because:

1. ✅ OAuth is configured (`"oauth": {}` in config)
2. ✅ OAuth discovery works (finds auth/token endpoints)
3. ✅ OAuth flow is attempted (logs show retry loop)
4. ❌ **But `auth status` reports no OAuth servers found**
5. ❌ **Error messages are generic: "no valid token available"**
6. ❌ **No indication of which OAuth parameters are missing**

This creates a **visibility gap** where OAuth appears broken but users can't tell why.

## Current Behavior

### auth status Output
```bash
$ ./mcpproxy auth status
ℹ️  No servers with OAuth configuration found.
   Configure OAuth in mcp_config.json to enable authentication.
```

### What's Actually Happening
```bash
# Logs show OAuth is configured and failing:
INFO  | 🌟 Starting OAuth authentication flow | {"scopes": [], "pkce_enabled": true}
ERROR | ❌ MCP initialization failed | {"error": "no valid token available, authorization required"}
INFO  | 🎯 OAuth authorization required during MCP init - deferring OAuth
WARN  | Connection error, will attempt reconnection | {"retry_count": 101}
```

### API Response
```bash
$ ./mcpproxy upstream list --output json | jq '.[] | select(.name == "slack")'
{
  "authenticated": false,
  "name": "slack",
  "oauth": null,  // ← Should contain OAuth config
  "status": "connecting"
}
```

## Root Causes

### Issue 1: OAuth Config Not Serialized
**File**: `internal/contracts/converters.go`
**Line**: ~35

```go
func ToServerContract(cfg *config.ServerConfig, status *upstream.ServerStatus) contracts.Server {
    return contracts.Server{
        Name:          cfg.Name,
        OAuth:         nil, // ← TODO: Convert config.OAuth to contracts.OAuthConfig
        Authenticated: status.Authenticated,
    }
}
```

**Problem**: The conversion function doesn't map `config.OAuth` to `contracts.OAuth`, so the API returns `null`.

### Issue 2: Generic Error Messages
**File**: `internal/upstream/core/connection.go`
**Line**: ~1078

```go
if err != nil {
    return fmt.Errorf("no valid token available, authorization required")
}
```

**Problem**: Error doesn't capture provider-specific requirements like missing `resource` parameter.

### Issue 3: No OAuth Error Diagnostics
**File**: `internal/management/diagnostics.go`
**Line**: ~58

```go
if hasOAuth && !authenticated {
    diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
        ServerName: serverName,
        State:      "unauthenticated",
        Message:    "Run: mcpproxy auth login --server=slack",
    })
}
```

**Problem**: Diagnostics only report "not authenticated" without explaining why authentication failed.

## Desired Behavior

### auth status Output (After Fix)
```bash
$ ./mcpproxy auth status

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔐 OAuth Authentication Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Server: slack
  Status: ❌ Authentication Failed
  Error: OAuth provider requires 'resource' parameter (RFC 8707)
  Auth URL: https://oauth.example.com/.well-known/oauth-authorization-server
  Token URL: https://oauth.example.com/api/v1/oauth/token
  Last Attempt: 2025-11-27 15:45:10
  Retry Count: 101

  💡 Suggestion:
     The OAuth provider requires additional parameters that MCPProxy
     doesn't currently support. This will be fixed in an upcoming release.

     As a workaround, you can try:
     1. Check if the provider has alternative auth methods
     2. Contact the provider about OAuth parameter requirements
     3. Wait for MCPProxy extra_params support (coming soon)

     Technical Details:
     - Missing parameter: resource
     - Expected format: resource=<MCP_ENDPOINT_URL>
     - RFC 8707: https://www.rfc-editor.org/rfc/rfc8707.html
```

### doctor Output (After Fix)
```bash
$ ./mcpproxy doctor

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📋 System Diagnostics
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🔍 OAuth Configuration Issues (1)

  Server: slack
    Issue: OAuth provider parameter mismatch
    Error: Provider requires 'resource' parameter (RFC 8707)
    Impact: Server cannot authenticate until parameter is provided

    Resolution:
      This requires MCPProxy support for OAuth extra_params.
      Track progress: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/XXX
```

### API Response (After Fix)
```bash
$ ./mcpproxy upstream list --output json | jq '.[] | select(.name == "slack")'
{
  "authenticated": false,
  "name": "slack",
  "oauth": {
    "auth_url": "https://oauth.example.com/api/v1/oauth/authorize",
    "token_url": "https://oauth.example.com/api/v1/oauth/token",
    "scopes": []
  },
  "last_error": "OAuth provider requires 'resource' parameter",
  "status": "connecting"
}
```

## Implementation Plan

### Task 1: Fix OAuth Config Serialization (1 hour)

**File**: `internal/contracts/converters.go`

**Changes**:
```go
func ToServerContract(cfg *config.ServerConfig, status *upstream.ServerStatus) contracts.Server {
    var oauthConfig *contracts.OAuthConfig
    if cfg.OAuth != nil {
        // Get discovered OAuth endpoints from status if available
        authURL := ""
        tokenURL := ""
        if status.OAuthMetadata != nil {
            authURL = status.OAuthMetadata.AuthorizationEndpoint
            tokenURL = status.OAuthMetadata.TokenEndpoint
        }

        oauthConfig = &contracts.OAuthConfig{
            AuthURL:  authURL,
            TokenURL: tokenURL,
            ClientID: cfg.OAuth.ClientID,
            Scopes:   cfg.OAuth.Scopes,
        }
    }

    return contracts.Server{
        Name:          cfg.Name,
        OAuth:         oauthConfig,
        Authenticated: status.Authenticated,
        LastError:     status.LastError,
        // ... other fields ...
    }
}
```

**Test**:
```bash
$ ./mcpproxy upstream list --output json | jq '.[] | select(.name == "slack") | .oauth'
{
  "auth_url": "https://oauth.example.com/authorize",
  "token_url": "https://oauth.example.com/token",
  "scopes": []
}
```

### Task 2: Capture OAuth Metadata in Status (2 hours)

**File**: `internal/upstream/core/connection.go`

**Add struct field**:
```go
type ServerStatus struct {
    // ... existing fields ...
    OAuthMetadata *OAuthMetadata // NEW
}

type OAuthMetadata struct {
    AuthorizationEndpoint string
    TokenEndpoint         string
    Issuer                string
}
```

**Store metadata after discovery**:
```go
func (c *Client) connectWithOAuth(ctx context.Context) error {
    // ... existing OAuth discovery code ...

    // After CreateOAuthConfig succeeds
    if oauthConfig != nil {
        c.status.OAuthMetadata = &OAuthMetadata{
            AuthorizationEndpoint: discoveredAuthURL,
            TokenEndpoint:         discoveredTokenURL,
            Issuer:                discoveredIssuer,
        }
    }

    // ... continue OAuth flow ...
}
```

### Task 3: Parse OAuth Error Responses (3 hours)

**File**: `internal/upstream/core/connection.go`

**Add error parsing**:
```go
// parseOAuthError extracts structured error information from OAuth provider responses
func parseOAuthError(err error, responseBody []byte) error {
    // Try to parse as FastAPI validation error (Runlayer format)
    var fapiErr struct {
        Detail []struct {
            Type  string   `json:"type"`
            Loc   []string `json:"loc"`
            Msg   string   `json:"msg"`
            Input any      `json:"input"`
        } `json:"detail"`
    }

    if json.Unmarshal(responseBody, &fapiErr) == nil && len(fapiErr.Detail) > 0 {
        for _, detail := range fapiErr.Detail {
            if detail.Type == "missing" && len(detail.Loc) >= 2 {
                if detail.Loc[0] == "query" {
                    paramName := detail.Loc[1]
                    return &OAuthParameterError{
                        Parameter:   paramName,
                        Location:    "authorization_url",
                        Message:     detail.Msg,
                        OriginalErr: err,
                    }
                }
            }
        }
    }

    // Try to parse as RFC 6749 OAuth error response
    var oauthErr struct {
        Error            string `json:"error"`
        ErrorDescription string `json:"error_description"`
        ErrorURI         string `json:"error_uri"`
    }

    if json.Unmarshal(responseBody, &oauthErr) == nil && oauthErr.Error != "" {
        return fmt.Errorf("OAuth error: %s - %s", oauthErr.Error, oauthErr.ErrorDescription)
    }

    // Fallback to original error
    return err
}

// OAuthParameterError represents a missing or invalid OAuth parameter
type OAuthParameterError struct {
    Parameter   string
    Location    string // "authorization_url" or "token_request"
    Message     string
    OriginalErr error
}

func (e *OAuthParameterError) Error() string {
    return fmt.Sprintf("OAuth provider requires '%s' parameter: %s", e.Parameter, e.Message)
}

func (e *OAuthParameterError) Unwrap() error {
    return e.OriginalErr
}
```

**Use in connection flow**:
```go
func (c *Client) handleOAuthAuthorization(ctx context.Context, authErr error, oauthConfig *client.OAuthConfig) error {
    // ... existing code ...

    resp, err := http.Get(authURL)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 400 {
        body, _ := io.ReadAll(resp.Body)
        return parseOAuthError(err, body)
    }

    // ... continue OAuth flow ...
}
```

### Task 4: Enhance auth status Display (2 hours)

**File**: `cmd/mcpproxy/auth_cmd.go`

**Update display logic**:
```go
func runAuthStatusClientMode(ctx context.Context, dataDir, serverName string, allServers bool) error {
    // ... existing server fetching code ...

    hasOAuthServers := false
    for _, srv := range servers {
        name, _ := srv["name"].(string)
        oauth, hasOAuth := srv["oauth"].(map[string]interface{})

        if !hasOAuth {
            continue
        }

        hasOAuthServers = true
        authenticated, _ := srv["authenticated"].(bool)
        lastError, _ := srv["last_error"].(string)

        // Determine status emoji and text
        var status string
        if authenticated {
            status = "✅ Authenticated"
        } else if lastError != "" {
            status = "❌ Authentication Failed"
        } else {
            status = "⏳ Pending Authentication"
        }

        fmt.Printf("Server: %s\n", name)
        fmt.Printf("  Status: %s\n", status)

        if authURL, ok := oauth["auth_url"].(string); ok && authURL != "" {
            fmt.Printf("  Auth URL: %s\n", authURL)
        }

        if tokenURL, ok := oauth["token_url"].(string); ok && tokenURL != "" {
            fmt.Printf("  Token URL: %s\n", tokenURL)
        }

        if lastError != "" {
            fmt.Printf("  Error: %s\n", lastError)

            // Provide suggestions based on error type
            if strings.Contains(lastError, "requires") && strings.Contains(lastError, "parameter") {
                fmt.Println()
                fmt.Println("  💡 Suggestion:")
                fmt.Println("     This OAuth provider requires additional parameters that")
                fmt.Println("     MCPProxy doesn't currently support. Support for custom")
                fmt.Println("     OAuth parameters (extra_params) is coming soon.")
                fmt.Println()
                fmt.Println("     For more information:")
                fmt.Println("     - RFC 8707: https://www.rfc-editor.org/rfc/rfc8707.html")
                fmt.Println("     - Track progress: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/XXX")
            }
        }

        fmt.Println()
    }

    if !hasOAuthServers {
        fmt.Println("ℹ️  No servers with OAuth configuration found.")
        fmt.Println("   Configure OAuth in mcp_config.json to enable authentication.")
    }

    return nil
}
```

### Task 5: Add OAuth Diagnostics to doctor (2 hours)

**File**: `internal/management/diagnostics.go`

**Add new diagnostic type**:
```go
type OAuthIssue struct {
    ServerName      string   `json:"server_name"`
    Issue           string   `json:"issue"`
    Error           string   `json:"error"`
    MissingParams   []string `json:"missing_params,omitempty"`
    Resolution      string   `json:"resolution"`
    DocumentationURL string  `json:"documentation_url,omitempty"`
}
```

**Update Diagnostics struct**:
```go
type Diagnostics struct {
    Timestamp       time.Time
    UpstreamErrors  []UpstreamError
    OAuthRequired   []OAuthRequirement
    OAuthIssues     []OAuthIssue    // NEW
    MissingSecrets  []MissingSecretInfo
    RuntimeWarnings []string
    DockerStatus    *DockerStatus
    TotalIssues     int
}
```

**Add OAuth issue detection**:
```go
func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
    // ... existing code ...

    // Check for OAuth issues
    diag.OAuthIssues = s.detectOAuthIssues(serversRaw)

    // Update total issues
    diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired) +
        len(diag.OAuthIssues) + len(diag.MissingSecrets) + len(diag.RuntimeWarnings)

    return diag, nil
}

func (s *service) detectOAuthIssues(servers []map[string]interface{}) []contracts.OAuthIssue {
    var issues []contracts.OAuthIssue

    for _, srvRaw := range servers {
        serverName := getStringFromMap(srvRaw, "name")
        hasOAuth := srvRaw["oauth"] != nil
        lastError := getStringFromMap(srvRaw, "last_error")
        authenticated := getBoolFromMap(srvRaw, "authenticated")

        // Skip servers without OAuth or already authenticated
        if !hasOAuth || authenticated {
            continue
        }

        // Check for parameter-related errors
        if strings.Contains(lastError, "requires") && strings.Contains(lastError, "parameter") {
            // Extract parameter name from error
            paramName := extractParameterName(lastError)

            issues = append(issues, contracts.OAuthIssue{
                ServerName:    serverName,
                Issue:         "OAuth provider parameter mismatch",
                Error:         lastError,
                MissingParams: []string{paramName},
                Resolution: fmt.Sprintf(
                    "This requires MCPProxy support for OAuth extra_params. " +
                    "Track progress: https://github.com/smart-mcp-proxy/mcpproxy-go/issues/XXX"),
                DocumentationURL: "https://www.rfc-editor.org/rfc/rfc8707.html",
            })
        }
    }

    return issues
}

func extractParameterName(errorMsg string) string {
    // Extract parameter name from error like "requires 'resource' parameter"
    re := regexp.MustCompile(`'([^']+)' parameter`)
    matches := re.FindStringSubmatch(errorMsg)
    if len(matches) > 1 {
        return matches[1]
    }
    return "unknown"
}
```

**Update doctor command output** (`cmd/mcpproxy/doctor_cmd.go`):
```go
func outputDiagnostics(diag map[string]interface{}, format string) error {
    // ... existing code ...

    // Add OAuth issues section
    if oauthIssues := getArrayField(diag, "oauth_issues"); len(oauthIssues) > 0 {
        fmt.Println()
        fmt.Printf("🔍 OAuth Configuration Issues (%d)\n", len(oauthIssues))
        fmt.Println()

        for _, issue := range oauthIssues {
            issueMap := issue.(map[string]interface{})
            serverName := issueMap["server_name"].(string)
            issueDesc := issueMap["issue"].(string)
            errorMsg := issueMap["error"].(string)
            resolution := issueMap["resolution"].(string)

            fmt.Printf("  Server: %s\n", serverName)
            fmt.Printf("    Issue: %s\n", issueDesc)
            fmt.Printf("    Error: %s\n", errorMsg)
            fmt.Printf("    Impact: Server cannot authenticate until parameter is provided\n")
            fmt.Println()
            fmt.Printf("    Resolution:\n")
            fmt.Printf("      %s\n", resolution)

            if docURL := issueMap["documentation_url"]; docURL != nil {
                fmt.Printf("      Documentation: %s\n", docURL)
            }
            fmt.Println()
        }
    }

    // ... rest of output ...
}
```

## Testing Strategy

### Unit Tests

**Test OAuth Config Serialization**:
```go
func TestToServerContract_WithOAuth(t *testing.T) {
    cfg := &config.ServerConfig{
        Name: "test-server",
        OAuth: &config.OAuthConfig{
            ClientID: "client123",
            Scopes:   []string{"read", "write"},
        },
    }

    status := &upstream.ServerStatus{
        Authenticated: false,
        OAuthMetadata: &upstream.OAuthMetadata{
            AuthorizationEndpoint: "https://oauth.example.com/authorize",
            TokenEndpoint:         "https://oauth.example.com/token",
        },
    }

    contract := converters.ToServerContract(cfg, status)

    require.NotNil(t, contract.OAuth)
    assert.Equal(t, "https://oauth.example.com/authorize", contract.OAuth.AuthURL)
    assert.Equal(t, "https://oauth.example.com/token", contract.OAuth.TokenURL)
    assert.Equal(t, "client123", contract.OAuth.ClientID)
    assert.Equal(t, []string{"read", "write"}, contract.OAuth.Scopes)
}
```

**Test OAuth Error Parsing**:
```go
func TestParseOAuthError_FastAPIValidation(t *testing.T) {
    responseBody := []byte(`{
        "detail": [
            {
                "type": "missing",
                "loc": ["query", "resource"],
                "msg": "Field required",
                "input": null
            }
        ]
    }`)

    err := parseOAuthError(errors.New("validation failed"), responseBody)

    require.Error(t, err)
    var paramErr *OAuthParameterError
    require.True(t, errors.As(err, &paramErr))
    assert.Equal(t, "resource", paramErr.Parameter)
    assert.Equal(t, "authorization_url", paramErr.Location)
    assert.Contains(t, err.Error(), "requires 'resource' parameter")
}
```

### Integration Tests

**Test auth status Output**:
```go
func TestAuthStatus_ShowsOAuthErrors(t *testing.T) {
    // Setup mock server with OAuth config
    // ... setup code ...

    // Run auth status command
    output := captureOutput(func() {
        runAuthStatus(nil, nil)
    })

    // Verify output contains error details
    assert.Contains(t, output, "❌ Authentication Failed")
    assert.Contains(t, output, "requires 'resource' parameter")
    assert.Contains(t, output, "💡 Suggestion:")
}
```

### Manual Testing Checklist

- [ ] Start MCPProxy with Slack server configured (`oauth: {}`)
- [ ] Run `./mcpproxy auth status` - should show slack server with OAuth
- [ ] Verify error message mentions "resource parameter"
- [ ] Run `./mcpproxy doctor` - should list OAuth configuration issue
- [ ] Check `/api/v1/servers` endpoint - should include oauth config
- [ ] Verify logs include structured error information

## Success Criteria

1. ✅ `auth status` shows OAuth-configured servers (not "no servers found")
2. ✅ Error message clearly identifies missing parameter: "requires 'resource' parameter"
3. ✅ Suggestion provides actionable guidance (even if it's "wait for fix")
4. ✅ `doctor` command detects OAuth parameter mismatches
5. ✅ API includes OAuth metadata in server response
6. ✅ Logs capture structured OAuth error information
7. ✅ No regressions in existing OAuth flows

## Rollout Plan

1. **PR 1**: OAuth config serialization (Task 1 + 2)
2. **PR 2**: OAuth error parsing (Task 3)
3. **PR 3**: Enhanced diagnostics (Task 4 + 5)

Each PR can be reviewed and deployed independently.

## Documentation Updates

After implementation, update:
- `docs/runlayer-oauth-investigation.md` - Link to Phase 0 completion
- `README.md` - Mention improved OAuth diagnostics
- `docs/troubleshooting.md` - Add section on OAuth error messages

## Related Issues

- Parent: OAuth Extra Parameters Support (#XXX)
- Upstream: mcp-go ExtraParams support (mark3labs/mcp-go#XXX)
