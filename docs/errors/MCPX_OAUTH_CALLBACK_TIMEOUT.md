---
id: MCPX_OAUTH_CALLBACK_TIMEOUT
title: MCPX_OAUTH_CALLBACK_TIMEOUT
sidebar_label: CALLBACK_TIMEOUT
description: The OAuth browser callback did not arrive in time.
---

# `MCPX_OAUTH_CALLBACK_TIMEOUT`

**Severity:** warn
**Domain:** OAuth

## What happened

mcpproxy opened the OAuth authorisation URL in the user's browser and started a
local listener on `127.0.0.1:<random-port>` to receive the callback. No callback
arrived before the wait window elapsed, so mcpproxy gave up.

## Common causes

- The user closed the browser tab without completing the consent screen.
- The provider redirected to a different host (e.g. set up to use the public
  domain instead of `localhost`).
- A browser extension or popup blocker swallowed the redirect.
- The provider's consent UI is taking longer than the configured timeout (e.g.
  step-up MFA, password reset, device approval).
- The local loopback port is being filtered by a firewall (rare but possible
  on locked-down corporate machines).

## How to fix

### Retry the login

The fix step **Retry log in** in the error panel restarts the flow. On the CLI:

```bash
mcpproxy upstream login <server-name>
```

Complete the consent screen quickly — most providers have a 5-10 minute
authorisation-code lifetime that mcpproxy mirrors.

### Verify the redirect URI

mcpproxy persists a callback URI per server. If the provider has a different
URI registered, the redirect won't reach mcpproxy. See
[`MCPX_OAUTH_CALLBACK_MISMATCH`](MCPX_OAUTH_CALLBACK_MISMATCH.md) for the fix.

### Check loopback reachability

```bash
# While the login flow is running:
curl -v http://127.0.0.1:<port>/oauth/callback
```

If this fails locally, a firewall is blocking the loopback bind.

## Related

- [`MCPX_OAUTH_CALLBACK_MISMATCH`](MCPX_OAUTH_CALLBACK_MISMATCH.md)
- [OAuth Authentication](../features/oauth-authentication.md)
