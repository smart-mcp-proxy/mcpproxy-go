# Quickstart: RFC 8707 Resource Auto-Detection

**Feature**: 011-resource-auto-detect
**Date**: 2025-12-10

## Overview

This feature enables automatic detection of the RFC 8707 `resource` parameter for OAuth flows. After implementation, users can connect to OAuth providers like Runlayer without any OAuth configuration.

## Usage

### Before (Manual Configuration Required)

```json
{
  "name": "slack",
  "url": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
  "oauth": {
    "scopes": ["mcp"],
    "pkce": true,
    "extra_params": {
      "resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"
    }
  }
}
```

### After (Zero Configuration)

```json
{
  "name": "slack",
  "url": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"
}
```

MCPProxy automatically:
1. Detects OAuth is required when connection returns 401
2. Fetches Protected Resource Metadata from server
3. Extracts `resource` field from metadata
4. Includes `resource` parameter in authorization URL
5. Includes `resource` parameter in token exchange

## Manual Override

If auto-detection produces incorrect results, you can still override:

```json
{
  "name": "slack",
  "url": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
  "oauth": {
    "extra_params": {
      "resource": "https://custom-resource-url.example.com"
    }
  }
}
```

Manual `extra_params` always take precedence over auto-detected values.

## Diagnostics

### Check Auto-Detected Resource

```bash
mcpproxy auth status --server=slack
```

Output will show the detected resource parameter:

```
Server: slack
OAuth: Auto-detected (RFC 9728)
Resource: https://oauth.runlayer.com/api/v1/proxy/UUID/mcp
Status: Authenticated
Token expires: 2025-12-10 15:30:00
```

### Debug OAuth Flow

```bash
mcpproxy auth login --server=slack --log-level=debug
```

Look for log entries:
- `Auto-detected resource parameter from Protected Resource Metadata`
- `Using server URL as resource parameter (fallback)`
- `Added extra OAuth parameters to authorization URL`

## Troubleshooting

### OAuth Still Fails with "Field required"

1. Check if server publishes Protected Resource Metadata:
   ```bash
   curl -I https://your-server-url | grep WWW-Authenticate
   ```

2. If `resource_metadata` URL is present, fetch it:
   ```bash
   curl https://extracted-metadata-url
   ```

3. Verify metadata contains `resource` field

4. If metadata is missing/incorrect, use manual `extra_params.resource`

### Resource Detected But Wrong

Override with manual configuration:

```json
{
  "oauth": {
    "extra_params": {
      "resource": "https://correct-resource-url.example.com"
    }
  }
}
```

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/oauth/discovery.go` | `DiscoverProtectedResourceMetadata()` - returns full metadata |
| `internal/oauth/config.go` | `CreateOAuthConfig()` - auto-detects resource, returns extra params |
| `internal/upstream/core/connection.go` | `handleOAuthAuthorization()` - injects params into auth URL |

## Testing

### Unit Tests

```bash
go test ./internal/oauth/... -v -run TestDiscoverProtectedResourceMetadata
go test ./internal/oauth/... -v -run TestCreateOAuthConfig_AutoDetects
```

### E2E Test

```bash
go test ./internal/server/... -v -run TestE2E_OAuth_ResourceAutoDetect
```

### Manual Test with Runlayer

1. Configure server with only name and URL
2. Run `mcpproxy upstream restart slack`
3. OAuth browser should open
4. Complete authorization
5. Verify connection succeeds
