# OAuth Test Server Manual Testing Guide

This document describes how to use the OAuth test server for manual testing and E2E test development.

## Quick Start

### 1. Start the OAuth Test Server

```bash
# Basic start on port 9000
go run ./tests/oauthserver/cmd/server -port 9000

# With longer token expiration (recommended for manual testing)
go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=1h
```

### 2. Configure mcpproxy

Add the OAuth test server to your `~/.mcpproxy/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "oauth-test-server",
      "url": "http://127.0.0.1:9000/mcp",
      "protocol": "streamable-http",
      "enabled": true
    }
  ]
}
```

### 3. Trigger OAuth Login

```bash
# Via CLI (opens browser automatically)
mcpproxy auth login --server=oauth-test-server

# Or restart mcpproxy to trigger connection
mcpproxy upstream restart oauth-test-server
```

### 4. Verify Connection

```bash
# Check server status
mcpproxy upstream list

# List available tools
mcpproxy tools list --server=oauth-test-server
```

## Server Startup Output

When started, the server displays all configuration:

```
========================================
OAuth Test Server
========================================
Listening on:      http://localhost:9000
Issuer:            http://127.0.0.1:9000

Endpoints:
  Authorization:   http://127.0.0.1:9000/authorize
  Token:           http://127.0.0.1:9000/token
  JWKS:            http://127.0.0.1:9000/jwks.json
  Discovery:       http://127.0.0.1:9000/.well-known/oauth-authorization-server
  Protected:       http://127.0.0.1:9000/protected
  DCR:             http://127.0.0.1:9000/registration
  Device Auth:     http://127.0.0.1:9000/device_authorization

Features:
  Auth Code:       enabled
  Device Code:     enabled (RFC 8628)
  DCR:             enabled (RFC 7591)
  Client Creds:    enabled
  Refresh Token:   enabled
  PKCE Required:   enabled (RFC 7636)
  Resource Req:    disabled (RFC 8707)
  Detection Mode:  both

Test Credentials:  testuser / testpass
========================================
```

## CLI Options Reference

### Port Configuration

```bash
-port 9000          # Listen port (default: 9000)
```

### Feature Toggles

```bash
-no-auth-code           # Disable authorization code flow
-no-device-code         # Disable device code flow (RFC 8628)
-no-dcr                 # Disable dynamic client registration (RFC 7591)
-no-client-credentials  # Disable client credentials flow
-no-refresh-token       # Disable refresh tokens
```

### Security Settings

```bash
-require-pkce=true      # Require PKCE for auth code flow (default: true)
-require-resource=false # Require RFC 8707 resource indicator (default: false)
-runlayer-mode=false    # Mimic Runlayer strict validation with Pydantic 422 errors
```

### Token Lifetimes

```bash
-access-token-ttl=1h    # Access token expiry (default: 3m for testing)
-refresh-token-ttl=24h  # Refresh token expiry (default: 24h)
```

### Detection Mode

Controls how OAuth discovery works:

```bash
-detection=discovery       # Only via /.well-known/oauth-authorization-server
-detection=www-authenticate # Only via WWW-Authenticate header on /protected
-detection=explicit        # Requires explicit OAuth config (no auto-detection)
-detection=both            # Both discovery and WWW-Authenticate (default)
```

### Error Injection (for testing error handling)

```bash
-token-error=invalid_client  # Inject error on token endpoint
-token-error=invalid_grant
-token-error=invalid_scope
-token-error=server_error

-auth-error=access_denied    # Inject error on authorization endpoint
-auth-error=invalid_request
```

## Example Configurations

### Minimal OAuth Server

```bash
go run ./tests/oauthserver/cmd/server -port 9000 \
  -no-dcr \
  -no-device-code \
  -no-client-credentials
```

### Long-Lived Tokens for Development

```bash
go run ./tests/oauthserver/cmd/server -port 9000 \
  -access-token-ttl=24h \
  -refresh-token-ttl=168h
```

### Strict RFC 8707 Resource Indicator

```bash
go run ./tests/oauthserver/cmd/server -port 9000 \
  -require-resource=true
```

### Runlayer Compatibility Mode (Issue #271)

Mimics Runlayer's strict OAuth validation with Pydantic-style 422 errors:

```bash
go run ./tests/oauthserver/cmd/server -port 9000 -runlayer-mode
```

When the `resource` parameter is missing, returns:
```json
{
  "detail": [
    {
      "type": "missing",
      "loc": ["query", "resource"],
      "msg": "Field required"
    }
  ]
}
```

This mode implies `-require-resource=true`.

### Test Error Handling

```bash
# Test invalid_grant error on token exchange
go run ./tests/oauthserver/cmd/server -port 9000 \
  -token-error=invalid_grant
```

## Auto-Submit Login Page

The login page features automatic submission for E2E testing:

- Pre-filled credentials: `testuser` / `testpass`
- 5-second countdown timer with visual progress bar
- Cancel button to stop auto-submit for manual testing
- Auto-cancels if there's an error displayed

This enables fully automated OAuth E2E tests without manual interaction.

## Running E2E Tests

### Playwright E2E Tests

The OAuth E2E tests use Playwright to automate browser-based OAuth flows:

```bash
# Run OAuth E2E test suite
./scripts/run-oauth-e2e.sh

# Or run directly with Playwright
OAUTH_SERVER_URL="http://127.0.0.1:9000" \
OAUTH_CLIENT_ID="test-client" \
MCPPROXY_URL="http://127.0.0.1:8085" \
MCPPROXY_API_KEY="test-api-key" \
npx playwright test tests/e2e/oauth.spec.ts
```

### Unit Tests

```bash
# Run OAuth server unit tests
go test ./tests/oauthserver/... -v

# Run integration tests (starts test server automatically)
OAUTH_INTEGRATION_TESTS=1 go test ./tests/oauthserver/... -run TestIntegration -v
```

## Manual OAuth Flow Testing

### 1. Test Discovery Endpoint

```bash
curl -s http://127.0.0.1:9000/.well-known/oauth-authorization-server | jq .
```

### 2. Test Dynamic Client Registration

```bash
curl -s -X POST http://127.0.0.1:9000/registration \
  -H "Content-Type: application/json" \
  -d '{
    "client_name": "Test Client",
    "redirect_uris": ["http://127.0.0.1/callback"],
    "grant_types": ["authorization_code", "refresh_token"]
  }' | jq .
```

### 3. Test Token Endpoint (Client Credentials)

```bash
CLIENT_ID="your-client-id"
CLIENT_SECRET="your-client-secret"

curl -s -X POST http://127.0.0.1:9000/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -u "$CLIENT_ID:$CLIENT_SECRET" \
  -d "grant_type=client_credentials&scope=read write" | jq .
```

### 4. Test MCP Endpoint with Token

```bash
ACCESS_TOKEN="your-access-token"

# Should return 401 without token
curl -s -X POST http://127.0.0.1:9000/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

# Should succeed with token
curl -s -X POST http://127.0.0.1:9000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | jq .
```

### 5. Test MCP Tools

```bash
# List tools
curl -s -X POST http://127.0.0.1:9000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | jq .

# Call echo tool
curl -s -X POST http://127.0.0.1:9000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"Hello"}}}' | jq .

# Call get_time tool
curl -s -X POST http://127.0.0.1:9000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_time","arguments":{}}}' | jq .
```

## Available MCP Tools

The OAuth test server exposes two simple tools for testing:

| Tool | Description | Arguments |
|------|-------------|-----------|
| `echo` | Echoes back the input message | `message` (string, required) |
| `get_time` | Returns current server time | none |

## Troubleshooting

### Server Shows "disconnected" After Login

If the server remains disconnected after successful OAuth login:

1. Check if token expired (default 3-minute TTL)
2. Restart with longer TTL: `-access-token-ttl=1h`
3. Clear stale tokens and re-authenticate

### Browser Doesn't Open

Check these environment variables:
- `HEADLESS=true` prevents browser opening
- `NO_BROWSER=true` prevents browser opening
- `CI=true` prevents browser opening

### PKCE Errors

The server requires PKCE by default. Ensure your client:
- Generates a code_verifier (43-128 characters)
- Sends code_challenge = base64url(sha256(code_verifier))
- Uses code_challenge_method=S256

### Token Validation Fails

- Verify JWKS endpoint is accessible: `curl http://127.0.0.1:9000/jwks.json`
- Check token expiration (exp claim)
- Ensure issuer matches server URL

## Alternative: Python Mock Server

For lightweight testing of issue #271 (RFC 8707 resource parameter), there's also a Python/FastAPI mock server:

### Location

`test/integration/oauth_runlayer_mock.py`

### Quick Start

```bash
# Using uv (recommended)
PORT=9000 uv run --with fastapi --with uvicorn python test/integration/oauth_runlayer_mock.py

# Using pip
pip install fastapi uvicorn
PORT=9000 python test/integration/oauth_runlayer_mock.py
```

### When to Use Each Server

| Scenario | Recommended |
|----------|-------------|
| Full E2E tests with Playwright | Go server |
| Testing OAuth discovery modes | Go server |
| Token refresh/expiry testing | Go server |
| Error injection scenarios | Go server |
| JWKS/key rotation testing | Go server |
| Quick RFC 8707 resource tests | Python or Go with `-runlayer-mode` |
| Issue #271 regression testing | Python (lighter) or Go |
| CI/CD integration tests | Go (embeddable) |

### Issue #271 Integration Test

```bash
./test/integration/test_oauth_resource_injection.sh
```

This script:
1. Starts the Python mock server
2. Starts mcpproxy with test configuration
3. Verifies mcpproxy injects the `resource` parameter correctly
4. Confirms the auth URL is accepted by the mock server
