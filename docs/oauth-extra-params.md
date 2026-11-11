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

## Token Response Normalization

The same wrapper also post-processes successful token endpoint responses (code exchange and refresh) so that `mcp-go` can decode them. A `201 Created` is reported as `200 OK`, and an `expires_at` that is not an RFC 3339 string (a numeric Unix epoch, `null`, or any other string such as `""` or `"2026-11-11 10:00:00"`) is dropped from the JSON body, because `mcp-go` decodes `expires_at` into a `time.Time` and fails with `failed to decode token response` otherwise. When `expires_in` is missing or not positive and the dropped `expires_at` was a number or an all-digit string, `expires_in` is derived as the seconds remaining until that timestamp (never negative, capped at 10 years; values above `1e11` are read as milliseconds, and implausibly large values derive nothing). Valid RFC 3339 string `expires_at` values, non-2xx responses, non-JSON bodies and bodies over 1 MiB are passed through unchanged. This applies whether or not `extra_params` is configured.
