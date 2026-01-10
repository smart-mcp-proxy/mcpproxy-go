---
id: oauth-authentication
title: OAuth Authentication
sidebar_label: OAuth Authentication
sidebar_position: 2
description: Configure OAuth 2.1 authentication for MCP servers
keywords: [oauth, authentication, security, tokens]
---

# OAuth Authentication

MCPProxy supports OAuth 2.1 authentication with PKCE for secure authentication to remote MCP servers.

## Overview

OAuth authentication is used when connecting to MCP servers that require authorization, such as:

- GitHub MCP servers
- Enterprise API gateways
- Third-party service integrations

## Configuration

### Basic OAuth Setup

```json
{
  "mcpServers": [
    {
      "name": "github-server",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "oauth": {
        "client_id": "your-client-id",
        "scopes": ["repo", "user"]
      },
      "enabled": true
    }
  ]
}
```

### OAuth Configuration Options

| Option | Type | Description |
|--------|------|-------------|
| `client_id` | string | OAuth client identifier (uses Dynamic Client Registration if empty) |
| `client_secret` | string | OAuth client secret (optional, can reference secure storage) |
| `redirect_uri` | string | OAuth redirect URI (auto-generated if not provided) |
| `scopes` | array | Requested OAuth scopes |
| `pkce_enabled` | boolean | PKCE is always enabled for security; this flag is currently ignored |
| `extra_params` | object | Additional authorization parameters (e.g., RFC 8707 resource) |

**Note:** OAuth authorization and token endpoints are automatically discovered from the server's OAuth metadata (RFC 8414 `.well-known/oauth-authorization-server`), not configured manually.

### Advanced Configuration

For OAuth providers that require additional parameters (like RFC 8707 resource indicators):

```json
{
  "oauth": {
    "client_id": "your-client-id",
    "client_secret": "your-client-secret",
    "scopes": ["read", "write"],
    "extra_params": {
      "audience": "https://api.example.com",
      "resource": "https://api.example.com"
    }
  }
}
```

## Authentication Flow

### Initial Authentication

1. MCPProxy detects OAuth requirement on first connection
2. Opens browser to authorization URL
3. User completes authentication
4. MCPProxy receives callback with authorization code
5. Exchanges code for access token
6. Stores tokens securely in system keyring

### Token Refresh

MCPProxy automatically refreshes tokens before expiration:

1. Checks token expiration before each request
2. Uses refresh token to get new access token
3. Falls back to browser re-authentication if refresh fails

## CLI Commands

### Start Authentication

```bash
mcpproxy auth login --server=github-server
```

### Check Status

```bash
mcpproxy auth status
```

### Debug Authentication

```bash
mcpproxy auth login --server=github-server --log-level=debug
```

## RFC 8707 Resource Parameter

MCPProxy supports automatic RFC 8707 resource parameter detection for OAuth providers that require it:

```json
{
  "oauth": {
    "client_id": "your-client-id",
    "extra_params": {
      "resource": "https://api.example.com"
    }
  }
}
```

For providers like Runlayer, MCPProxy can auto-detect the resource parameter from the server's OAuth metadata.

## Token Storage

Tokens are stored securely using the system keyring:

| Platform | Storage |
|----------|---------|
| macOS | Keychain |
| Windows | Credential Manager |
| Linux | Secret Service (libsecret) |

## Error Handling

MCPProxy provides structured error responses for OAuth failures, making it easier to diagnose and fix authentication issues.

### Error Types

| Error Type | Description | Common Causes |
|------------|-------------|---------------|
| `client_id_required` | OAuth client ID is missing | Server requires pre-registered client, DCR not supported |
| `dcr_failed` | Dynamic Client Registration failed | Server rejected registration, permission denied |
| `metadata_discovery_failed` | Could not discover OAuth metadata | Server unreachable, missing `.well-known` endpoints |
| `code_flow_failed` | Authorization code flow failed | User denied, invalid redirect, network issues |

### CLI Error Output

When OAuth fails, the CLI displays rich error information:

```
❌ OAuth Error: dcr_failed

Server: github-server
Message: Dynamic Client Registration failed: 403 Forbidden
Suggestion: Check if the OAuth server requires pre-registered clients

🔍 Debug:
   Server logs: mcpproxy upstream logs github-server
   Activity log: mcpproxy activity list --request-id req-xyz-123
   Request ID: req-xyz-123
   Correlation ID: a1b2c3d4e5f6789012345678
```

### API Error Response

The REST API returns structured `OAuthFlowError` responses:

```json
{
  "success": false,
  "error_type": "dcr_failed",
  "server_name": "github-server",
  "message": "Dynamic Client Registration failed: 403 Forbidden",
  "suggestion": "Check if the OAuth server requires pre-registered clients",
  "correlation_id": "a1b2c3d4e5f6789012345678",
  "request_id": "req-xyz-123",
  "details": {
    "metadata": {
      "status": "ok",
      "protected_resource_url": "https://api.example.com/.well-known/oauth-protected-resource"
    },
    "dcr": {
      "attempted": true,
      "status": "failed",
      "error": "403 Forbidden"
    }
  }
}
```

### Log Correlation

Use the `request_id` from error responses to find related logs:

```bash
# Find activity records for a specific request
mcpproxy activity list --request-id req-xyz-123

# View server-specific logs
mcpproxy upstream logs github-server --tail 50
```

## Troubleshooting

### Browser Doesn't Open

```bash
# Run in headless mode with manual URL copy
HEADLESS=true mcpproxy auth login --server=github-server
```

### Token Refresh Fails

1. Check token expiration in `auth status`
2. Verify refresh token is stored
3. Re-authenticate if needed

### Invalid Scope Errors

Verify the requested scopes are valid for the OAuth provider:

```bash
mcpproxy auth login --server=github-server --log-level=debug
```

### Connection Errors

Ensure the OAuth server URLs are accessible:

```bash
curl -I https://auth.example.com/.well-known/oauth-authorization-server
```

## Security Best Practices

1. **Use PKCE**: MCPProxy uses PKCE by default for public clients
2. **Minimal Scopes**: Request only necessary permissions
3. **Token Storage**: Tokens are encrypted in system keyring
4. **Callback Security**: Uses dynamic port allocation to prevent port conflicts
