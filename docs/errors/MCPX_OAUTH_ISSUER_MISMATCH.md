---
id: MCPX_OAUTH_ISSUER_MISMATCH
title: MCPX_OAUTH_ISSUER_MISMATCH
sidebar_label: ISSUER_MISMATCH
description: A sign-in response could not be confirmed as coming from the authorization server the login was started with.
---

# `MCPX_OAUTH_ISSUER_MISMATCH`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy started a sign-in with one authorization server and recorded that
server's issuer. The browser came back with a response that did not confirm
it:

- the response's `iss` parameter named another issuer (an empty `iss` counts
  as another issuer), or
- the authorization server's metadata says it always sends `iss`
  (`authorization_response_iss_parameter_supported: true`), and the response
  had none.

This is the check RFC 9207 defines against an OAuth mix-up attack, in which
a malicious authorization server tricks a client into sending it a code that
another server issued. mcpproxy refused the response before the
authorization code was exchanged. No token request was sent and nothing was
stored. An error response from the provider is refused the same way, before
its error is reported.

The browser page and mcpproxy's messages show the expected issuer as an
origin only. The value the response carried is never shown, because whoever
sent the response controls it.

## Common causes

- The provider's sign-in page handed the browser to a different
  authorization server, for example after a tenant or region switch.
- The provider sends an `iss` value that differs from the `issuer` in its own
  metadata (a provider bug), or advertises `iss` support and then omits it.
- Someone is attempting a mix-up attack.

## How to fix

### Sign in again

Start the login again from mcpproxy:

```bash
mcpproxy auth login --server=<server-name>
```

### If it fails every time

The provider's `iss` does not match its own metadata. Check the provider's
metadata document (`/.well-known/oauth-authorization-server`) and report the
mismatch to the provider.

If you trust the provider and need the server working meanwhile, turn the
check off for that one server:

```json
{
  "name": "legacy-provider",
  "url": "https://mcp.legacy.example/mcp",
  "oauth": { "skip_issuer_validation": true }
}
```

With `skip_issuer_validation` on, a wrong `iss`, or a missing one the
metadata advertises, is accepted and logged as a warning, and the login is
not protected against mix-up. The
strict parse of the response is still applied. Turn it off again once the
provider is fixed.

## Related

- [OAuth Authentication](../features/oauth-authentication.md)
- [RFC 9207 — OAuth 2.0 Authorization Server Issuer Identification](https://www.rfc-editor.org/rfc/rfc9207)
