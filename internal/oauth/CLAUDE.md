# OAuth Implementation

## Key Files

| File | Purpose |
|------|---------|
| `coordinator.go` | Per-server flow coordination (prevents races) |
| `correlation.go` | UUID-based tracing for all operations |
| `logging.go` | Automatic token redaction in logs |
| `transport_wrapper.go` | HTTP RoundTripper for extra params |
| `masking.go` | Sensitive data masking |

## OAuth Flow

1. **Dynamic port allocation** for callback server
2. **PKCE** for security (RFC 8252 compliant)
3. **Auto browser launch** for authentication
4. **Token refresh** using refresh_token before re-auth

## Flow Coordinator

Prevents race conditions with per-server coordination:
- One flow per server at a time
- Queued requests wait for active flow
- Timeout handling for stale flows

## Extra Parameters

Supports RFC 8707 resource indicators and custom params:
```json
{
  "oauth": {
    "extra_params": {
      "resource": "https://api.example.com/mcp",
      "audience": "mcp-api"
    }
  }
}
```

Reserved params are rejected: `client_id`, `redirect_uri`, `code`, `state`, etc.

## Debugging

```bash
mcpproxy auth login --server=ServerName --log-level=debug
mcpproxy auth status --server=ServerName
mcpproxy doctor  # Shows OAuth issues
```

## Token Storage

BBolt database: `~/.mcpproxy/config.db`
- `oauth_tokens` bucket - Access/refresh tokens
- `oauth_completion` bucket - Flow completion state

## Global Callback Server

`internal/oauth/callback.go` manages callback servers:
- Prevents port conflicts across concurrent flows
- Auto-cleanup after flow completion
