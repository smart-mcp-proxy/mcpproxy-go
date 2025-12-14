# OAuth Extra Parameters Configuration

MCPProxy supports manual `extra_params` for OAuth providers requiring non-standard parameters. Manual params override auto-detected values.

## Use Cases

- **RFC 8707 Resource Indicators**: Override auto-detected resource for multi-tenant authorization
- **Audience-Restricted Tokens**: Request tokens for specific API audiences
- **Tenant Identification**: Pass tenant/organization identifiers for multi-tenant OAuth
- **Custom Provider Extensions**: Support proprietary OAuth extensions from specific providers

## Configuration Examples

### Example 1: Runlayer MCP Server with Resource Parameter

```json
{
  "mcpServers": [
    {
      "name": "runlayer-slack",
      "url": "https://oauth.runlayer.com/api/v1/proxy/abc123def/mcp",
      "protocol": "http",
      "enabled": true,
      "oauth": {
        "scopes": ["mcp"],
        "pkce_enabled": true,
        "extra_params": {
          "resource": "https://oauth.runlayer.com/api/v1/proxy/abc123def/mcp"
        }
      }
    }
  ]
}
```

### Example 2: Multi-Tenant OAuth with Multiple Parameters

```json
{
  "mcpServers": [
    {
      "name": "enterprise-mcp",
      "url": "https://api.example.com/mcp",
      "protocol": "http",
      "enabled": true,
      "oauth": {
        "scopes": ["mcp:read", "mcp:write"],
        "pkce_enabled": true,
        "extra_params": {
          "resource": "https://api.example.com/mcp",
          "audience": "mcp-api",
          "tenant": "org-456"
        }
      }
    }
  ]
}
```

### Example 3: Azure AD with Resource Parameter

For Azure AD and other providers with custom OAuth endpoints, MCPProxy automatically discovers endpoints via the server's `.well-known/oauth-authorization-server` metadata (RFC 8414).

```json
{
  "mcpServers": [
    {
      "name": "azure-mcp",
      "url": "https://mcp.azure.example.com/api",
      "protocol": "http",
      "enabled": true,
      "oauth": {
        "scopes": ["https://mcp.azure.example.com/.default"],
        "pkce_enabled": true,
        "extra_params": {
          "resource": "https://mcp.azure.example.com"
        }
      }
    }
  ]
}
```

**Note:** OAuth authorization and token URLs are auto-discovered from the server's metadata. If the server doesn't provide discovery metadata, you may need to configure the MCP server itself to expose proper OAuth endpoints.

## Security & Validation

- Reserved OAuth 2.0 parameters (`client_id`, `client_secret`, `redirect_uri`, `code`, `state`, `code_verifier`, `code_challenge`, `code_challenge_method`) are **rejected** at config load time
- Extra parameters are injected into all OAuth 2.0 requests: authorization, token exchange, and token refresh
- Parameter values containing secrets are automatically masked in logs (see `internal/oauth/masking.go`)

## Debugging

```bash
# View OAuth configuration including extra_params
mcpproxy auth status --server=runlayer-slack

# Test OAuth flow with debug logging
mcpproxy auth login --server=runlayer-slack --log-level=debug

# Check for OAuth-related issues
mcpproxy doctor
```

## Implementation Details

- Uses HTTP `RoundTripper` wrapper pattern (RFC 2616) for transparent request interception
- Zero overhead for servers without `extra_params` configured
- Thread-safe concurrent OAuth flows with parameter isolation
- See `internal/oauth/transport_wrapper.go` for wrapper implementation
