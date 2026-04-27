---
id: MCPX_HTTP_403
title: MCPX_HTTP_403
sidebar_label: HTTP 403
description: The MCP server responded 403 Forbidden — credentials valid but lack permission.
---

# `MCPX_HTTP_403`

**Severity:** error
**Domain:** HTTP

## What happened

The upstream MCP server accepted mcpproxy's credentials but rejected the
request: 403 Forbidden. Unlike 401, the identity is recognised — it just isn't
authorised for what was attempted.

## Common causes

- The OAuth scopes granted at login don't include the scopes required for this
  endpoint.
- The user / API token has been moved to a less-privileged role.
- The MCP server enforces resource-level ACLs (e.g. workspace, team).
- Conditional-access policy (Microsoft, Okta) blocks this network/device.

## How to fix

### Re-authorise with broader scopes

If the upstream advertises additional scopes you didn't accept, re-run login
and accept them on the consent screen:

```bash
mcpproxy upstream login <server-name>
```

Some upstreams let you pass scopes explicitly via `oauth.scopes` in the upstream
config. Add the missing scope and log in again.

### Ask an admin

For role-based access, the fix is on the upstream side: an admin needs to grant
the user / token the missing role.

### Check the response body

The response body usually says exactly which scope or role is missing:

```bash
mcpproxy activity show <id>
```

## Related

- [`MCPX_HTTP_401`](MCPX_HTTP_401.md) — credentials missing or invalid
- [OAuth Authentication](../features/oauth-authentication.md)
