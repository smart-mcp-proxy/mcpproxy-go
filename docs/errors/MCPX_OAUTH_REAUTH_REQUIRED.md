---
id: MCPX_OAUTH_REAUTH_REQUIRED
title: MCPX_OAUTH_REAUTH_REQUIRED
sidebar_label: REAUTH_REQUIRED
description: A previously working OAuth token is no longer valid; sign in again.
---

# `MCPX_OAUTH_REAUTH_REQUIRED`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy had a stored OAuth token for this server, but it stopped working — for
example the server returned an error while the token was presented, so mcpproxy
cleared the broken token and now needs you to sign in again. Unlike
[`MCPX_OAUTH_LOGIN_REQUIRED`](MCPX_OAUTH_LOGIN_REQUIRED.md), the server *was*
working, so this is reported as **unhealthy** (red): it is a regression you
should act on, not a routine first-time setup step.

## Common causes

- The provider returned a 5xx/auth error while the stored token was in use, so
  the token was discarded as likely invalid.
- The token was revoked, rotated, or otherwise invalidated provider-side.

## How to fix

### Sign in again

```bash
mcpproxy auth login --server=<server-name>
```

Or click **Sign in again** on the server's web UI page, or use the tray menu.

If re-login keeps failing, the OAuth client itself may be misconfigured — see
[`MCPX_OAUTH_REFRESH_403`](MCPX_OAUTH_REFRESH_403.md) for client-validation steps.

## Related

- [`MCPX_OAUTH_LOGIN_REQUIRED`](MCPX_OAUTH_LOGIN_REQUIRED.md) — first-time sign-in (amber)
- [`MCPX_OAUTH_REFRESH_403`](MCPX_OAUTH_REFRESH_403.md) — provider rejected refresh
- [OAuth Authentication](../features/oauth-authentication.md)
