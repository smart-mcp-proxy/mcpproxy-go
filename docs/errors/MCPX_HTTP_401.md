---
id: MCPX_HTTP_401
title: MCPX_HTTP_401
sidebar_label: HTTP 401
description: The MCP server responded 401 Unauthorized.
---

# `MCPX_HTTP_401`

**Severity:** error
**Domain:** HTTP

## What happened

The upstream MCP server returned `401 Unauthorized`. mcpproxy either had no
credentials, or the credentials it presented were rejected.

## Common causes

- No OAuth token has been obtained yet for this server.
- The OAuth access token expired and the refresh path also failed (see
  [`MCPX_OAUTH_REFRESH_EXPIRED`](MCPX_OAUTH_REFRESH_EXPIRED.md) /
  [`MCPX_OAUTH_REFRESH_403`](MCPX_OAUTH_REFRESH_403.md)).
- A static bearer token in `headers` was rotated server-side.
- The `Authorization` header is being stripped by an intermediary proxy.

## How to fix

### Re-authenticate via OAuth

```bash
mcpproxy upstream login <server-name>
```

Or click **Log in again** on the server's web UI page.

### Refresh static credentials

If the server uses a static API token (e.g. `headers.Authorization: Bearer …`),
generate a new token in that vendor's console and update the upstream config:

```json
{ "headers": { "Authorization": "Bearer <new-token>" } }
```

Then restart that single server: `mcpproxy upstream restart <server-name>`.

### Confirm the token is sent

```bash
mcpproxy activity list --request-id <id>          # locate the failing call
```

The activity record includes the request ID and (redacted) headers.

## Related

- [OAuth Authentication](../features/oauth-authentication.md)
- [`MCPX_HTTP_403`](MCPX_HTTP_403.md) — authenticated but lacks permission
