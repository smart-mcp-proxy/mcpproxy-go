---
id: MCPX_OAUTH_CALLBACK_MISMATCH
title: MCPX_OAUTH_CALLBACK_MISMATCH
sidebar_label: CALLBACK_MISMATCH
description: The OAuth redirect URI used in the callback didn't match the one mcpproxy registered.
---

# `MCPX_OAUTH_CALLBACK_MISMATCH`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy received an OAuth redirect, but the `redirect_uri` parameter in the
authorisation response differs from the one mcpproxy persisted for this server.
Returning a token in this state would violate RFC 8252 / PKCE binding, so the
flow is aborted.

## Common causes

- The OAuth client registered with the provider lists a different redirect URI
  than the one mcpproxy uses.
- The provider was reconfigured between the start of the login and the
  callback (rare).
- mcpproxy's persisted port changed because the saved port was already in use.
- A reverse proxy in front of mcpproxy rewrote the `redirect_uri`.

## How to fix

### Update the provider configuration

mcpproxy uses `http://127.0.0.1:<port>/oauth/callback` (with a per-server
persisted port). Add that exact URI to your OAuth client's allowed redirect
URIs in the provider's developer console.

For most providers wildcards aren't allowed; you'll need to register the exact
port. mcpproxy persists the port in the upstream config — see
[`oauth_redirect_port`](../configuration/upstream-servers.md) — so you can
register a stable URI.

### Re-pin the redirect port

If you previously used a different port and want to restore it, set
`oauth_redirect_port` explicitly:

```json
{
  "name": "my-server",
  "oauth": {
    "client_id": "...",
    "redirect_port": 53412
  }
}
```

Then re-register that exact URI on the provider side.

### If a reverse proxy is in front

Make the proxy preserve the original `redirect_uri` query parameter and avoid
host rewriting on `/oauth/callback`. mcpproxy ships a self-hosted callback —
it doesn't need to be exposed publicly, only locally.

## Related

- [Spec 023 — OAuth state persistence](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/specs/023-oauth-state-persistence/spec.md)
- [OAuth Authentication](../features/oauth-authentication.md)
