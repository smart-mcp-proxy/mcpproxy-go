---
id: MCPX_OAUTH_DISCOVERY_FAILED
title: MCPX_OAUTH_DISCOVERY_FAILED
sidebar_label: DISCOVERY_FAILED
description: mcpproxy could not fetch the OAuth metadata document from the upstream server.
---

# `MCPX_OAUTH_DISCOVERY_FAILED`

**Severity:** error
**Domain:** OAuth

## What happened

When connecting to an HTTP MCP server that requires OAuth, mcpproxy first
discovers the authorisation endpoint by fetching one of:

- `<server>/.well-known/oauth-authorization-server`
- `<server>/.well-known/openid-configuration`

The fetch failed (network error, 404, malformed JSON, missing required fields,
or no advertised auth metadata at all).

## Common causes

- The MCP server doesn't actually require OAuth (mcpproxy auto-detected wrongly).
- The server requires OAuth but doesn't expose `.well-known` discovery.
- The discovery document is behind a corporate proxy / VPN that mcpproxy can't reach.
- The discovery URL responded with an HTML login page instead of JSON.

## How to fix

### Test discovery manually

```bash
curl -sS <server-url>/.well-known/oauth-authorization-server | jq .
curl -sS <server-url>/.well-known/openid-configuration       | jq .
```

If both return non-JSON or 404, the server doesn't publish auto-discoverable
metadata.

### Configure OAuth manually

Set the OAuth fields explicitly in the upstream config so mcpproxy doesn't have
to discover them:

```json
{
  "name": "my-server",
  "url": "https://example.com/mcp",
  "oauth": {
    "authorization_endpoint": "https://example.com/oauth/authorize",
    "token_endpoint": "https://example.com/oauth/token",
    "client_id": "...",
    "scopes": ["openid", "profile"]
  }
}
```

### Network reachability

If discovery is blocked at the network layer:

```bash
curl -v <server-url>/.well-known/oauth-authorization-server
env | grep -i proxy
```

See [`MCPX_NETWORK_PROXY_MISCONFIG`](MCPX_NETWORK_PROXY_MISCONFIG.md) if your
proxy variables are wrong.

## Related

- [OAuth Authentication](../features/oauth-authentication.md)
- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md)
- [`MCPX_HTTP_TLS_FAILED`](MCPX_HTTP_TLS_FAILED.md)
