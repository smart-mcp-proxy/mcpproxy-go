---
id: MCPX_OAUTH_TOKEN_UNDECRYPTABLE
title: MCPX_OAUTH_TOKEN_UNDECRYPTABLE
sidebar_label: TOKEN_UNDECRYPTABLE
description: The stored OAuth token cannot be decrypted with the configured encryption key.
---

# `MCPX_OAUTH_TOKEN_UNDECRYPTABLE`

**Severity:** error
**Domain:** OAuth

## What happened

mcpproxy can encrypt the upstream OAuth tokens it keeps in `config.db` with a
key you configure (`oauth_token_encryption_key`, or the
`MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` / `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE`
environment variables). The stored token for this server is encrypted, and
the key mcpproxy started with cannot open it.

mcpproxy keeps the stored token exactly as it is and does **not** offer a new
sign-in for this server: a new sign-in cannot replace a token it cannot read,
and the token comes back as soon as the right key is available again. Other
servers keep working.

The server's health detail names the reason:

| Reason | Meaning |
|--------|---------|
| no encryption key configured | The token was encrypted, but mcpproxy started without a key. |
| sealed with a different key | The token was encrypted with another key than the one configured. The key ids are in the server's last error. |
| failed authentication | The encrypted value was changed, or was copied from another server's entry. |
| unsupported envelope version | The token was written by a newer mcpproxy. |
| not a valid stored token | The stored value is neither an encrypted token nor a readable record. |

## How to fix

### Restore the key

If the key was lost from the service environment (a missing secret mount,
an unset environment variable, a changed config reference), make the original
key available again and restart mcpproxy. The key is read only at startup.

```bash
mcpproxy upstream list
```

shows which servers are affected and why.

### Discard the stored token, then sign in again

If the original key is gone for good, or the value was damaged, discard the
stored token and sign in again:

```bash
mcpproxy auth logout --server=<server-name>
mcpproxy auth login --server=<server-name>
```

Removing the server, or changing its OAuth settings, also discards the stored
token.

## Related

- [`MCPX_OAUTH_REAUTH_REQUIRED`](MCPX_OAUTH_REAUTH_REQUIRED.md) — a readable stored token stopped working
- [OAuth Authentication](../features/oauth-authentication.md)
