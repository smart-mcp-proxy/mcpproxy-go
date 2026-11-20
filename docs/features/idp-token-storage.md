---
title: "IdP Subject Token Storage (removed)"
sidebar_label: "IdP Token Storage"
description: "Server edition: the store_idp_tokens feature was removed; the key is accepted and ignored."
---

# IdP Subject Token Storage (removed)

The server edition no longer persists identity-provider access or refresh tokens after login. The feature existed only to feed an on-behalf-of token exchange that was never wired to any upstream call, so a long-lived IdP refresh token at rest had no reader and was a leak surface rather than a capability. `server_edition.store_idp_tokens` is still **accepted** by the config loader for compatibility, but it is a **no-op**: a value of `true` logs one deprecation warning at startup (`server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it`) and nothing is stored. After upgrading, each start with `server_edition.enabled: true` attempts to delete tokens an earlier release persisted in `config.db`, before other server-edition setup and without needing an encryption key. A successful purge logs `purged legacy IdP subject-token rows`; if scanning or deletion fails, a warning is logged and startup continues, so that attempt may leave rows behind. Nothing in the current release could read the legacy tokens anyway. Per-upstream credentials obtained through the `oauth_connect` flow are unaffected — they are still stored encrypted under `credential_encryption_key` / `MCPPROXY_CRED_KEY`, as described in [Auth Broker](./auth-broker.md) and [Credential Commands](../cli/credential-commands.md).
