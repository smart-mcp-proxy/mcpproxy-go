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
| `client_id` | string | OAuth client identifier |
| `scopes` | array | Requested OAuth scopes |
| `authorization_url` | string | Override authorization endpoint |
| `token_url` | string | Override token endpoint |
| `extra_params` | object | Additional authorization parameters |

### Advanced Configuration

```json
{
  "oauth": {
    "client_id": "your-client-id",
    "scopes": ["read", "write"],
    "authorization_url": "https://auth.example.com/authorize",
    "token_url": "https://auth.example.com/token",
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
