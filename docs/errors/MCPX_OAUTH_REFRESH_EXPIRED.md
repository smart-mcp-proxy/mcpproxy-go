---
id: MCPX_OAUTH_REFRESH_EXPIRED
title: MCPX_OAUTH_REFRESH_EXPIRED
sidebar_label: REFRESH_EXPIRED
description: The stored OAuth refresh token has expired and cannot be silently renewed.
---

# `MCPX_OAUTH_REFRESH_EXPIRED`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy attempted to refresh an OAuth access token in the background, but the
identity provider reported that the **refresh token itself** has expired. There
is no way to silently recover — the user has to re-authenticate.

## Common causes

- The provider sets a finite lifetime on refresh tokens (Google: ~6 months
  inactivity; some providers: 30 days; some: rotates on every refresh).
- The user revoked the application from their account dashboard.
- The mcpproxy host was offline for longer than the provider allows.
- Time skew on the local machine made the refresh request look replayed.

## How to fix

### Re-authenticate

In the web UI, open the server's detail page and click **Log in again**.
On the CLI:

```bash
mcpproxy upstream login <server-name>
```

This re-runs the OAuth 2.1 + PKCE flow in your default browser and persists
fresh tokens to the local store.

### Reduce future expirations

- Keep mcpproxy running (or auto-start it) so the refresh window doesn't lapse.
- Verify your system clock is correct (`timedatectl` on Linux,
  `sntp -sS time.apple.com` on macOS).

## Related

- [`MCPX_OAUTH_REFRESH_403`](MCPX_OAUTH_REFRESH_403.md) — provider rejected even an unexpired refresh
- [OAuth Authentication](../features/oauth-authentication.md)
