# Quickstart: OAuth E2E Testing & Observability

**Feature**: 007-oauth-e2e-testing
**Date**: 2025-12-02

## Overview

This feature adds comprehensive OAuth end-to-end testing infrastructure to mcpproxy, enabling developers to:

1. **Run OAuth tests locally** without external dependencies
2. **Test all OAuth flows** (auth code, device code, DCR, client credentials)
3. **Verify observability** (logs, CLI output, doctor checks)
4. **Run browser-based tests** with Playwright

## Prerequisites

- Go 1.24.0+
- Node.js 18+ (for Playwright tests)
- mcpproxy built: `go build -o mcpproxy ./cmd/mcpproxy`

## Quick Start

### Run All OAuth E2E Tests

```bash
# Full OAuth E2E suite (Go + Playwright)
./scripts/run-oauth-e2e.sh

# Go integration tests only
go test ./tests/oauthserver/... -v

# Playwright browser tests only
cd e2e/playwright && npx playwright test
```

### Run Specific Test Scenarios

```bash
# Auth code + PKCE flow
go test ./tests/oauthserver -run TestAuthCodePKCE -v

# Device code flow
go test ./tests/oauthserver -run TestDeviceCode -v

# Dynamic client registration
go test ./tests/oauthserver -run TestDCR -v

# Error handling
go test ./tests/oauthserver -run TestErrorHandling -v

# JWKS rotation
go test ./tests/oauthserver -run TestJWKSRotation -v
```

## Using the Test Server Directly

### Basic Usage

```go
package mytest

import (
    "testing"
    "github.com/your-org/mcpproxy-go/tests/oauthserver"
)

func TestMyOAuthFeature(t *testing.T) {
    // Start server with defaults
    server := oauthserver.Start(t, oauthserver.Options{})
    defer server.Shutdown()

    // Use server.IssuerURL, server.ClientID, etc.
    t.Logf("OAuth server running at %s", server.IssuerURL)

    // Configure mcpproxy or make HTTP requests
}
```

### Custom Configuration

```go
func TestWithCustomOptions(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        // Disable features not needed
        EnableDeviceCode: false,
        EnableDCR:        false,

        // Fast token expiry for refresh testing
        AccessTokenExpiry: 5 * time.Second,

        // Custom scopes
        SupportedScopes: []string{"read", "write", "admin", "custom"},

        // Test credentials
        ValidUsers: map[string]string{
            "alice": "password123",
            "bob":   "secret456",
        },
    })
    defer server.Shutdown()
}
```

### Error Injection

```go
func TestErrorHandling(t *testing.T) {
    server := oauthserver.Start(t, oauthserver.Options{
        ErrorMode: oauthserver.ErrorMode{
            TokenInvalidClient: true,  // Force invalid_client errors
        },
    })
    defer server.Shutdown()

    // Test that mcpproxy handles the error correctly
}
```

## Test Server Endpoints

| Endpoint | Description |
|----------|-------------|
| `/.well-known/oauth-authorization-server` | OAuth metadata (RFC 8414) |
| `/.well-known/openid-configuration` | OIDC discovery (alias) |
| `/jwks.json` | Public keys for JWT verification |
| `/authorize` | Authorization endpoint with login UI |
| `/token` | Token endpoint (all grant types) |
| `/registration` | Dynamic client registration |
| `/device_authorization` | Device code initiation |
| `/device_verification` | Device code approval UI |
| `/protected` | Returns 401 for detection testing |

## Testing OAuth Detection

### Well-Known Discovery

```go
server := oauthserver.Start(t, oauthserver.Options{
    DetectionMode: oauthserver.Discovery,
})
// mcpproxy will fetch /.well-known/oauth-authorization-server
```

### WWW-Authenticate Header

```go
server := oauthserver.Start(t, oauthserver.Options{
    DetectionMode: oauthserver.WWWAuthenticate,
})
// Hit /protected to get 401 with WWW-Authenticate
resp, _ := http.Get(server.ProtectedResourceURL)
// Parse WWW-Authenticate header for authorization_uri
```

## Observability Testing

### Test `auth status` Output

```bash
# After OAuth flow completes
./mcpproxy auth status --server=test-server

# Expected output includes:
# - Authorization endpoint URL
# - Token endpoint URL
# - Scopes
# - Token expiry
# - PKCE status
```

### Test `auth login` Preview

```bash
# Should print authorization URL before opening browser
./mcpproxy auth login --server=test-server

# Look for:
# "Opening browser to: http://127.0.0.1:XXXXX/authorize?..."
# with resource, scopes, and PKCE parameters visible
```

### Test `doctor` OAuth Checks

```bash
# With misconfigured OAuth
./mcpproxy doctor

# Expected output:
# - OAuth configuration issues
# - Discovery endpoint reachability
# - Actionable hints
```

## Playwright Browser Tests

### Running Tests

```bash
cd e2e/playwright

# Install browsers (first time)
npx playwright install chromium

# Run all OAuth tests
npx playwright test oauth-login.spec.ts

# Run with UI (debugging)
npx playwright test --ui

# Run headed (see browser)
npx playwright test --headed
```

### Test Scenarios

1. **Happy Path Login**: Fill credentials, approve consent, verify redirect
2. **Invalid Password**: Submit wrong password, verify error page
3. **Denied Consent**: Uncheck consent, verify error=access_denied
4. **Timeout**: Close browser mid-flow, verify cleanup

## CI Integration

OAuth E2E tests run automatically on:
- Push to `main` or `next` branches
- PRs labeled with `test-oauth`

To trigger manually:
```bash
# Add label to PR
gh pr edit --add-label test-oauth
```

## Troubleshooting

### Server Not Starting

```bash
# Check for port conflicts
lsof -i :8080

# Use verbose logging
go test ./tests/oauthserver -v -run TestBasic
```

### Playwright Issues

```bash
# Reinstall browsers
npx playwright install --force

# Run with debug logging
DEBUG=pw:api npx playwright test
```

### Token Verification Failures

```bash
# Check JWKS is accessible
curl http://127.0.0.1:XXXXX/jwks.json | jq

# Verify token claims
# Decode JWT at jwt.io
```

## Key Files

| File | Purpose |
|------|---------|
| `tests/oauthserver/server.go` | Main test server |
| `tests/oauthserver/options.go` | Configuration |
| `tests/oauthserver/jwt.go` | JWT generation |
| `tests/oauthserver/server_test.go` | Server unit tests |
| `scripts/run-oauth-e2e.sh` | E2E orchestration |
| `e2e/playwright/oauth-login.spec.ts` | Browser tests |

## Next Steps

After running tests successfully:

1. Add new test cases as needed for specific scenarios
2. Update `CLAUDE.md` if adding new test commands
3. Update `MANUAL_TESTING.md` with OAuth test procedures
