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

## Troubleshooting

```bash
./mcpproxy doctor              # Check OAuth detection
./mcpproxy auth status         # View OAuth-capable servers
./mcpproxy auth login --server=myserver --log-level=debug
```
