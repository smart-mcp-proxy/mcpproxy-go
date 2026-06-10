---
id: MCPX_OAUTH_LOGIN_REQUIRED
title: MCPX_OAUTH_LOGIN_REQUIRED
sidebar_label: LOGIN_REQUIRED
description: The server requires an OAuth sign-in before it can connect (first-time login).
---

# `MCPX_OAUTH_LOGIN_REQUIRED`

**Severity:** warn
**Domain:** OAuth

## What happened

The server is configured for OAuth but has no usable token yet, so mcpproxy
deferred the browser sign-in to you (via the tray menu, web UI, or CLI) instead
of blocking the connection. This is an **expected setup step, not a fault** — the
server is reported as *degraded* (amber), not *unhealthy* (red), and the only
suggested action is to sign in.

## Common causes

- A newly added OAuth server that you have not signed in to yet.
- mcpproxy started headless (daemon/tray mode) and deferred the interactive
  browser flow until you trigger it.

## How to fix

### Sign in

```bash
mcpproxy auth login --server=<server-name>
```

Or click **Sign in** on the server's web UI page, or use the tray menu. The
server moves to healthy once a token is obtained.

## Related

- [`MCPX_OAUTH_REAUTH_REQUIRED`](MCPX_OAUTH_REAUTH_REQUIRED.md) — a previously
  working token broke (red, not amber)
- [`MCPX_OAUTH_REFRESH_EXPIRED`](MCPX_OAUTH_REFRESH_EXPIRED.md) — refresh token expired
- [OAuth Authentication](../features/oauth-authentication.md)
