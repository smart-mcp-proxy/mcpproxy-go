---
id: MCPX_OAUTH_REFRESH_403
title: MCPX_OAUTH_REFRESH_403
sidebar_label: REFRESH_403
description: The OAuth provider rejected the refresh token with HTTP 403.
---

# `MCPX_OAUTH_REFRESH_403`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy presented a refresh token to the provider's token endpoint and got an
HTTP 403 (or `invalid_grant`) back. The token was technically still in our
store, but the provider considers it unusable.

## Common causes

- The user revoked the OAuth grant from their account settings.
- The OAuth client (client_id) was deleted or rotated provider-side.
- Refresh-token rotation: the provider already issued a successor and now
  refuses the older one.
- The redirect URI / scopes registered for the client changed.
- Conditional-access / device-trust policy (Microsoft Entra) revoked the session.

## How to fix

### 1. Re-authenticate

```bash
mcpproxy upstream login <server-name>
```

Or click **Log in again** on the server's web UI page. This is the only
deterministic recovery — silent retry will keep failing.

### 2. Verify the client is still valid

If re-login still 403s, the OAuth client itself may be misconfigured:

- Confirm `client_id` / `client_secret` in the upstream config still exist on
  the provider side.
- Verify the redirect URI matches exactly what the provider has registered
  (mcpproxy persists a per-server callback — see
  [`MCPX_OAUTH_CALLBACK_MISMATCH`](MCPX_OAUTH_CALLBACK_MISMATCH.md)).

### 3. Check provider-side audit log

Many providers expose a sign-in / token audit log that says exactly why the
refresh was denied (revoked, scope changed, conditional access, etc.).

## Related

- [`MCPX_OAUTH_REFRESH_EXPIRED`](MCPX_OAUTH_REFRESH_EXPIRED.md) — natural expiry
- [`MCPX_OAUTH_CALLBACK_MISMATCH`](MCPX_OAUTH_CALLBACK_MISMATCH.md) — wrong redirect URI
- [OAuth Authentication](../features/oauth-authentication.md)
