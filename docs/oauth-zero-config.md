# Zero-Config OAuth

MCPProxy automatically detects OAuth requirements and RFC 8707 resource parameters.

## Quick Start

No manual OAuth configuration needed for standard MCP servers:

```json
{
  "name": "slack",
  "url": "https://oauth.example.com/api/v1/proxy/UUID/mcp"
}
```

MCPProxy automatically:
1. Detects OAuth requirement from 401 response
2. Fetches Protected Resource Metadata (RFC 9728)
3. Extracts resource parameter
4. Auto-discovers scopes
5. Launches browser for authentication

## Manual Overrides (Optional)

For non-standard OAuth requirements:

```json
{
  "oauth": {
    "extra_params": {
      "tenant_id": "12345"
    }
  }
}
```

## How It Works

See design document: `docs/designs/2025-11-27-zero-config-oauth.md`

## Server States

OAuth-capable servers in MCPProxy can be in one of these states:

### Connected States
- **`ready`**: Server connected and authenticated (has valid, non-expired OAuth token)
- **`connected`**: Server connected but not OAuth-authenticated (no token or expired token)

### Waiting States
- **`pending_auth`**: OAuth authentication required but deferred (waiting for user action)
  - Occurs when OAuth server detected but user hasn't logged in yet
  - Server shows ⏳ icon in tray UI
  - Use `mcpproxy auth login --server=<name>` to authenticate
  - **Not an error** - this is a normal waiting state

### Transitional States
- **`connecting`**: Server is attempting to connect
- **`authenticating`**: OAuth authentication flow in progress

### Error States
- **`disconnected`**: Server failed to connect (check logs for details)
- **`error`**: Unexpected error occurred (check `last_error` field)

## Checking Authentication Status

Use the `auth status` command to view OAuth authentication state for all servers:

```bash
mcpproxy auth status
```

**Example output:**
```
Server                   Status              Token Expiry           Authenticated
────────────────────────────────────────────────────────────────────────────────
slack-server            ✅ Authenticated     2025-12-01 15:30:00   Yes
github-server           ⏳ Pending Auth      -                      No
sentry-server           ❌ Auth Failed       Token expired          No
```

**Status meanings:**
- **✅ Authenticated**: Valid OAuth token, server ready to use
- **⏳ Pending Authentication**: Waiting for OAuth login (use `auth login`)
- **❌ Authentication Failed**: OAuth token invalid or expired (re-authenticate required)

## Troubleshooting

### Common Issues

#### 1. Server Shows "Pending Auth" State

**Symptoms:**
- Server appears with ⏳ icon in tray UI
- `mcpproxy upstream list` shows `pending_auth` status
- `mcpproxy auth status` shows "⏳ Pending Authentication"

**Cause:** OAuth-capable server detected, but user hasn't authenticated yet.

**Solution:**
```bash
# Option 1: CLI authentication
mcpproxy auth login --server=<server-name>

# Option 2: Tray UI (recommended)
# Right-click tray icon → <server-name> → "Authenticate"
```

**Why this happens:** MCPProxy automatically detects OAuth requirements from server responses. The server is intentionally deferred (not an error) to prevent blocking daemon startup.

#### 2. Authentication Token Expired

**Symptoms:**
- Server was working, now shows "❌ Auth Failed"
- `auth status` shows "Token expired"
- Server `authenticated` field is `false`

**Cause:** OAuth access token expired (typical lifetime: 1-24 hours).

**Solution:**
```bash
# Re-authenticate to get new token
mcpproxy auth login --server=<server-name>
```

**Prevention:** MCPProxy automatically refreshes tokens when possible. If auto-refresh fails, manual re-authentication is required.

#### 3. OAuth Detection Not Working

**Symptoms:**
- Server requires OAuth but shows as disconnected
- No "Pending Auth" state, just repeated connection failures
- Logs show generic connection errors

**Diagnosis:**
```bash
# Check if server is OAuth-capable
mcpproxy doctor

# Enable debug logging to see OAuth detection
mcpproxy upstream logs <server-name> --tail=100

# Test OAuth flow manually with debug logs
mcpproxy auth login --server=<server-name> --log-level=debug
```

**Common causes:**
- Server doesn't return proper 401 with `WWW-Authenticate` header
- Server uses non-standard OAuth endpoints (need manual config)
- Network/firewall blocking OAuth metadata endpoint

**Solution:** Add explicit OAuth configuration:
```json
{
  "name": "custom-server",
  "url": "https://api.example.com/mcp",
  "oauth": {
    "client_id": "your-client-id",
    "client_secret": "your-client-secret",
    "auth_url": "https://oauth.example.com/authorize",
    "token_url": "https://oauth.example.com/token",
    "scopes": ["mcp.read", "mcp.write"]
  }
}
```

#### 4. OAuth Login Opens Browser But Fails

**Symptoms:**
- Browser opens with OAuth prompt
- After approving, shows "Success" page
- But server still shows "Pending Auth" or "Auth Failed"

**Diagnosis:**
```bash
# Check if callback server received authorization code
mcpproxy upstream logs <server-name> --tail=50 | grep -i "oauth\|callback"
```

**Common causes:**
- Callback server port conflict (rare with dynamic allocation)
- OAuth callback timeout (took > 5 minutes to approve)
- Network/firewall blocking loopback connections

**Solution:**
```bash
# Retry with extended timeout and debug logging
mcpproxy auth login --server=<server-name> --log-level=debug

# If persistent, check firewall rules for 127.0.0.1 loopback
```

### Diagnostic Commands

```bash
# Quick OAuth detection check
mcpproxy doctor

# View all OAuth-capable servers and token status
mcpproxy auth status

# Check server connection status
mcpproxy upstream list

# View OAuth flow logs for specific server
mcpproxy upstream logs <server-name> --tail=100 --follow

# Test OAuth authentication with debug output
mcpproxy auth login --server=<server-name> --log-level=debug

# Verify OAuth configuration extraction
mcpproxy upstream list --format=json | jq '.[] | select(.name=="<server-name>") | .oauth'
```
