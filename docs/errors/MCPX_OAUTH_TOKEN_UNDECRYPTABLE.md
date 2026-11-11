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

mcpproxy keeps the stored token exactly as it is and does **not** start a new
sign-in for this server: its health recommends `configure`, not `login`, and a
sign-in request is refused before any flow starts, because a new sign-in
cannot replace a token mcpproxy cannot read. When the cause is a missing or a
different key, the token works again once the original key is configured and
mcpproxy is restarted. A stored entry that was changed, damaged or copied from
another server is not repaired by any key: restore `config.db` from a backup,
or discard the entry with `auth logout` and sign in again. Other servers keep
working. The Web UI offers the configure guidance for the server instead of a
sign-in button, and `mcpproxy auth login --server=<name>` reads the stored row first:
it explains this error with the reason, the key ids and the next commands, opens no
browser and exits non-zero. `mcpproxy auth status` shows the server as "cannot be
decrypted", never as "log in". This holds for a server that is currently connected too
(for example one that connected anonymously and later needs a sign-in): its
health reports `configure`, never `login`. A login started from the REST API
(`POST /api/v1/servers/{id}/login`), the Web UI or the tray is refused with this
code instead of starting. The HTTP 409 applies to the REST and Web UI requests
only; the tray receives the same error in-process, with no HTTP status.

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
mcpproxy upstream list -o json
```

shows which servers are affected and why, and `mcpproxy doctor` has an OAuth Token
Storage section with the row counts, the reason for each affected server and both
key ids (the row's and the configured one; public fingerprints, never key material).
With mcpproxy stopped, `mcpproxy auth tokens status` prints the same from the
database. The table form (`mcpproxy upstream list`)
shows only the action column; the reason is in the JSON: `health.action` is
`configure`, `health.detail` holds the reason, and `error_code` is
`MCPX_OAUTH_TOKEN_UNDECRYPTABLE` (with the cause in `diagnostic.cause`).

### Discard the stored token, then sign in again

If the original key is gone for good, or the value was damaged, discard the
stored token and sign in again:

```bash
mcpproxy auth logout --server=<server-name>
mcpproxy auth login --server=<server-name>
```

With mcpproxy stopped, `auth logout` works when the key source is missing or
unreadable: it deletes the stored entry and needs no key. A source that is present
but unusable (empty, oversized, malformed, both variables set, or a bad
reference) still stops it with exit 4. For the Linux package, run it as the service
user with the service's paths:

```bash
sudo systemctl stop mcpproxy
sudo -u mcpproxy mcpproxy --config=/etc/mcpproxy/mcp_config.json \
  --data-dir=/var/lib/mcpproxy auth logout --server=<server-name>
sudo systemctl start mcpproxy
```

`auth login` and
starting mcpproxy still need a readable key, so if the original key is gone,
configure a new one first (see
[OAuth Token Encryption](../features/oauth-token-encryption.md)).

Removing the server, or changing its OAuth settings, also discards the stored
token.

## Related

- [`MCPX_OAUTH_REAUTH_REQUIRED`](MCPX_OAUTH_REAUTH_REQUIRED.md) — a readable stored token stopped working
- [OAuth Authentication](../features/oauth-authentication.md)
