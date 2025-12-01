# Quickstart Guide: OAuth Extra Parameters

**Feature**: OAuth Extra Parameters Support
**Audience**: Developers integrating MCPProxy with OAuth providers requiring extra parameters

## Overview

This guide shows you how to configure and use OAuth extra parameters in MCPProxy. You'll learn how to:
1. Add extra_params to your server configuration
2. Test OAuth authentication with Runlayer (or similar providers)
3. Verify the feature is working correctly
4. Troubleshoot common issues

**Time to Complete**: 10-15 minutes

## Prerequisites

- MCPProxy installed (version with OAuth extra params support)
- OAuth provider that requires extra parameters (e.g., Runlayer)
- OAuth client credentials (client_id, client_secret)

## Step 1: Configure Extra Parameters

### Add to Configuration File

Edit your `mcp_config.json` (typically located at `~/.mcpproxy/mcp_config.json`):

```json
{
  "mcpServers": [
    {
      "name": "runlayer-slack",
      "protocol": "streamable-http",
      "url": "https://oauth.runlayer.com/api/v1/proxy/YOUR-UUID-HERE/mcp",
      "oauth": {
        "client_id": "your-client-id",
        "client_secret": "your-client-secret",
        "scopes": ["read", "write"],
        "extra_params": {
          "resource": "https://oauth.runlayer.com/api/v1/proxy/YOUR-UUID-HERE/mcp"
        }
      },
      "enabled": true
    }
  ]
}
```

**Key Points**:
- `extra_params` is a map of string key-value pairs
- `resource` parameter is required by RFC 8707-compliant providers like Runlayer
- Replace `YOUR-UUID-HERE` with your actual Runlayer proxy UUID

### Multiple Extra Parameters

If your provider requires multiple custom parameters:

```json
{
  "oauth": {
    "client_id": "your-client-id",
    "extra_params": {
      "resource": "https://example.com/mcp",
      "audience": "mcp-api",
      "tenant": "tenant-123"
    }
  }
}
```

## Step 2: Validate Configuration

Before starting MCPProxy, verify your config is valid:

```bash
# Syntax check (JSON parser)
cat ~/.mcpproxy/mcp_config.json | jq . > /dev/null && echo "✅ Valid JSON" || echo "❌ Invalid JSON"

# Start MCPProxy in debug mode to see config loading
./mcpproxy serve --log-level=debug
```

**Expected Output** (debug logs):
```
DEBUG Loaded OAuth config for server=runlayer-slack extra_params={"resource":"https://oauth.runlayer.com/..."}
```

**If you see an error like this**:
```
ERROR oauth config validation failed: extra_params cannot override reserved OAuth 2.0 parameters: client_id
```

**Fix**: Remove the reserved parameter from `extra_params`. Reserved parameters include:
- `client_id`, `client_secret`, `redirect_uri`
- `response_type`, `scope`, `state`
- `code_challenge`, `code_challenge_method`
- `grant_type`, `code`, `refresh_token`, `code_verifier`

## Step 3: Trigger OAuth Login

Start the OAuth authentication flow:

```bash
# Trigger OAuth login for your configured server
./mcpproxy auth login --server=runlayer-slack
```

**What Happens**:
1. MCPProxy generates OAuth authorization URL (with extra params)
2. Opens your default browser to the OAuth provider
3. You authorize the application
4. OAuth provider redirects back to MCPProxy
5. MCPProxy exchanges authorization code for access token (with extra params)

**Expected Output**:
```
Opening authorization URL in browser...
Authorization URL: https://provider.com/authorize?...&resource=https%3A%2F%2Foauth.runlayer.com%2F...

Waiting for authorization callback...
✓ OAuth login successful! Access token stored.
```

## Step 4: Verify Extra Params Are Sent

### Check Authorization URL

When the browser opens, inspect the URL. You should see your extra params:

```
https://provider.com/authorize?
  response_type=code&
  client_id=abc123&
  redirect_uri=http://127.0.0.1:8080/callback&
  state=xyz789&
  scope=read+write&
  code_challenge=...&
  code_challenge_method=S256&
  resource=https%3A%2F%2Foauth.runlayer.com%2Fapi%2Fv1%2Fproxy%2FUUID%2Fmcp  ← Extra param!
```

**Tip**: If the URL auto-redirects too fast, check debug logs:
```bash
./mcpproxy auth login --server=runlayer-slack --log-level=debug 2>&1 | grep "Authorization URL"
```

### Check Auth Status

After successful login, verify tokens are stored:

```bash
./mcpproxy auth status
```

**Expected Output**:
```
Server: runlayer-slack
Status: ✓ Authenticated
Scopes: read, write
Extra Params:
  resource: https://oauth.runlayer.com/api/v1/proxy/UUID/mcp
Token Expiry: 2025-12-01 14:30:00 UTC
```

## Step 5: Test MCP Server Integration

Now that OAuth is configured, test the actual MCP server connection:

```bash
# List tools from the OAuth-protected MCP server
./mcpproxy tools list --server=runlayer-slack

# Call a tool (example)
./mcpproxy call tool --server=runlayer-slack --tool=example_tool --input='{}'
```

**Expected Output**:
```
Server: runlayer-slack
Tools:
  - slack_send_message
  - slack_list_channels
  - slack_get_user

Total: 3 tools
```

**If you get a 401 Unauthorized error**:
- OAuth token may have expired → Re-run `auth login`
- Extra params missing from config → Verify config and restart MCPProxy

## Common Use Cases

### Runlayer Integration

Runlayer requires RFC 8707 resource indicators:

```json
{
  "oauth": {
    "client_id": "runlayer-client-id",
    "extra_params": {
      "resource": "https://oauth.runlayer.com/api/v1/proxy/YOUR-UUID/mcp"
    }
  }
}
```

### Multi-Tenant OAuth Providers

Some providers require tenant/organization identifiers:

```json
{
  "oauth": {
    "client_id": "tenant-aware-client",
    "extra_params": {
      "resource": "https://api.example.com/mcp",
      "tenant": "tenant-123",
      "organization": "org-456"
    }
  }
}
```

### Audience-Restricted Tokens

Providers using audience-based authorization:

```json
{
  "oauth": {
    "client_id": "audience-client",
    "extra_params": {
      "audience": "mcp-api",
      "resource": "https://mcp.example.com"
    }
  }
}
```

## Troubleshooting

### Issue: Config Validation Error

**Symptom**:
```
ERROR oauth config validation failed: extra_params cannot override reserved OAuth 2.0 parameters: state
```

**Cause**: Attempting to override a reserved OAuth parameter

**Solution**: Remove the reserved parameter from `extra_params`. Check the full list in `internal/config/oauth_validation.go`.

---

### Issue: OAuth Provider Rejects Request

**Symptom**:
```
ERROR OAuth error: invalid_request, description: Required parameter 'resource' is missing
```

**Cause**: Extra param not being sent (wrapper not applied)

**Solution**:
1. Verify `extra_params` is in config: `cat ~/.mcpproxy/mcp_config.json | jq '.mcpServers[0].oauth.extra_params'`
2. Restart MCPProxy to reload config
3. Check debug logs to confirm wrapper is active

---

### Issue: Authorization URL Missing Extra Params

**Symptom**: URL opens but doesn't include your custom parameters

**Cause**: Wrapper not applying params to URL

**Solution**:
1. Enable debug logging: `--log-level=debug`
2. Check for wrapper creation logs: `grep "OAuth wrapper" logs/main.log`
3. Verify params are non-empty and valid JSON strings

---

### Issue: Token Refresh Fails

**Symptom**: OAuth works initially, but fails after token expires

**Cause**: Extra params not included in refresh request

**Solution**: This is a known issue if refresh isn't wrapped. Check logs for:
```
DEBUG OAuth token refresh url=https://provider.com/token
```

Ensure the request includes extra params. If not, file a bug report.

---

### Issue: Backward Compatibility - Existing OAuth Broken

**Symptom**: Servers without `extra_params` stop working after upgrade

**Cause**: Regression in wrapper logic (this should NOT happen)

**Solution**:
1. Add empty `extra_params: {}` to config
2. Report bug with logs and config (redact secrets)
3. Rollback to previous version if critical

## Advanced Topics

### Debugging OAuth Flows

Enable trace-level logging to see all OAuth requests:

```bash
./mcpproxy serve --log-level=trace

# Watch OAuth requests in real-time
tail -f ~/.mcpproxy/logs/main.log | grep -E "(OAuth|authorization|token)"
```

### Custom Redirect URI

If you need to customize the OAuth callback port:

```json
{
  "oauth": {
    "client_id": "custom-client",
    "redirect_uri": "http://127.0.0.1:9000/oauth/callback",
    "extra_params": {
      "resource": "https://example.com/mcp"
    }
  }
}
```

**Note**: MCPProxy uses dynamic port allocation by default. Custom redirect_uri disables this feature.

### Testing with Mock OAuth Server

For development, use a mock OAuth server:

```bash
# Start mock server (requires test dependencies)
go test ./internal/server -run TestOAuthExtraParams_Integration -v
```

Mock server validates that extra params are present in requests.

## Next Steps

- **Production Deployment**: Add extra_params to all OAuth servers requiring them
- **Monitor OAuth Status**: Use `mcpproxy doctor` to check for OAuth issues
- **Security Audit**: Review `extra_params` to ensure no sensitive data in logs
- **Contribute**: Share your OAuth provider config as an example

## References

- **Feature Spec**: [spec.md](spec.md)
- **Data Model**: [data-model.md](data-model.md)
- **API Contract**: [contracts/oauth-wrapper-api.md](contracts/oauth-wrapper-api.md)
- **RFC 8707**: https://www.rfc-editor.org/rfc/rfc8707.html
