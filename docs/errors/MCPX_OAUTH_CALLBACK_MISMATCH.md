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
port. When `oauth.redirect_uri` is not set, mcpproxy allocates a loopback port
on the first login and persists it, reusing it on subsequent logins — so the
callback URL is usually stable, but it is not guaranteed if that port is taken
later.

### Pin the redirect URI

If the provider requires an exact callback URL, pin it with `oauth.redirect_uri`:

```json
{
  "name": "my-server",
  "oauth": {
    "client_id": "...",
    "redirect_uri": "http://127.0.0.1:53412/oauth/callback"
  }
}
```

mcpproxy binds that exact port and sends that exact string to the provider. The
value must be an RFC 8252 loopback redirect: `http` scheme, a loopback host, an
explicit port, and the `/oauth/callback` path. A malformed value, or a pinned
port already in use, fails the login with an explicit error naming
`redirect_uri` rather than falling back to a random port.

Then re-register that exact URI on the provider side.

### If a reverse proxy is in front

Make the proxy preserve the original `redirect_uri` query parameter and avoid
host rewriting on `/oauth/callback`. mcpproxy ships a self-hosted callback —
it doesn't need to be exposed publicly, only locally.

## Related

- [Spec 023 — OAuth state persistence](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/specs/023-oauth-state-persistence/spec.md)
- [OAuth Authentication](../features/oauth-authentication.md)
