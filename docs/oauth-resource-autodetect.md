# Zero-Config OAuth with Resource Auto-Detection (RFC 8707/9728)

MCPProxy automatically detects and injects the RFC 8707 `resource` parameter for OAuth providers like Runlayer. This enables zero-configuration OAuth for servers advertising Protected Resource Metadata (RFC 9728).

## How It Works

1. MCPProxy sends a preflight HEAD request to the MCP server URL
2. If server returns 401 with `WWW-Authenticate` header containing `resource_metadata` URL, MCPProxy fetches the Protected Resource Metadata
3. The `resource` field from metadata is automatically injected into OAuth authorization URL and token requests
4. If metadata doesn't contain `resource`, MCPProxy falls back to using the server URL

## Zero-Config Example (Runlayer)

```json
{
  "mcpServers": [
    {
      "name": "runlayer-slack",
      "url": "https://oauth.runlayer.com/api/v1/proxy/abc123def/mcp",
      "protocol": "http",
      "enabled": true
      // No OAuth config needed! Resource parameter auto-detected from metadata
    }
  ]
}
```

## Priority Order for Resource Parameter

1. Manual `extra_params.resource` in config (highest priority - preserves backward compatibility)
2. Auto-detected resource from RFC 9728 Protected Resource Metadata
3. Fallback to server URL if metadata unavailable or lacks resource field

## Diagnostic Commands

```bash
# View auto-detected resource parameter
mcpproxy auth status --server=runlayer-slack

# Check for OAuth issues including resource detection
mcpproxy doctor
```

## Related Documentation

- [OAuth Extra Parameters](oauth-extra-params.md) - Manual OAuth parameter configuration
- [OAuth Documentation](mcp-go-oauth.md) - Complete OAuth setup guide
