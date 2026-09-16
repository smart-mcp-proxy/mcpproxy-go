---
title: "IdP Subject Token Storage (removed)"
sidebar_label: "IdP Token Storage"
description: "Server edition: the store_idp_tokens feature was removed; the key is accepted and ignored."
---

# IdP Subject Token Storage (removed)

The server edition no longer persists identity-provider access or refresh tokens after login. The feature existed only to feed an on-behalf-of token exchange that was never wired to any upstream call, so a long-lived IdP refresh token at rest had no reader and was a leak surface rather than a capability. `server_edition.store_idp_tokens` is still **accepted** by the config loader for compatibility, but it is a **no-op**: a value of `true` logs one deprecation warning at startup (`server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it`) and nothing is stored. Tokens an earlier release persisted are deleted from `config.db` on the first start after upgrading (logged as `purged legacy IdP subject-token rows`); nothing in the current release could read them anyway. Per-upstream credentials obtained through the `oauth_connect` flow are unaffected — they are still stored encrypted under `credential_encryption_key` / `MCPPROXY_CRED_KEY`, as described in [Auth Broker](./auth-broker.md) and [Credential Commands](../cli/credential-commands.md).
