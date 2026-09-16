---
id: auth-broker
title: Per-User Auth Broker
sidebar_label: Auth Broker
sidebar_position: 11
description: Server-edition per-user OAuth connect flow — credentials are stored encrypted per user for a future broker and are not injected into upstream calls
keywords: [auth broker, oauth connect, per-user, credential store, server edition, spec 074, spec 107]
---

# Per-User Auth Broker

**A stored credential is kept for a future broker and is NOT injected into upstream calls in this release.** The `auth_broker` block lets a user of the server edition connect their *own* OAuth credential for a shared upstream through a per-user consent flow; the proxy persists that credential encrypted, per user, and shows its status through the REST API, the CLI and the Web UI. Nothing on the tool-call path reads it: a proxied request to that upstream still carries whatever the upstream's own `headers` / `oauth` configuration says, exactly as it would without the block.

:::info Server edition only
The auth broker is part of the **server edition** (`//go:build server`). It is opt-in **per upstream** — servers without an `auth_broker` block behave exactly as before. The connect flow applies only to HTTP-family upstreams (`http`, `sse`, `streamable-http`); configuring it on a `stdio` upstream is rejected at config validation. The personal edition keeps the block in the config file as opaque JSON so a file shared between editions round-trips unchanged; it never validates or acts on it.
:::

## Modes

`auth_broker.mode` accepts one value:

| Mode | Description |
|------|-------------|
| `oauth_connect` | **Path B** — a per-user authorization-code + PKCE *connect* flow against the upstream's authorization server. The user is redirected to the upstream's consent screen once; the resulting per-user credential is persisted encrypted. |

Earlier releases also validated the modes `token_exchange` (RFC 8693) and `entra_obo` (Microsoft On-Behalf-Of). Neither was ever performed by any code path, and they are no longer accepted: a config file that still names one loads with **that server's whole `auth_broker` block ignored** and one warning (`auth_broker.mode "token_exchange" was never implemented; the auth_broker block for server "<name>" was ignored`), and `PATCH /api/v1/config` / `/config/apply` refuse it with the same text. The same applies to the removed `header` and `header_format` keys (`auth_broker.header is no longer supported and was ignored`).

## Configuration

The block lives under a server entry in the config file:

```json
{
  "mcpServers": [
    {
      "name": "github-enterprise",
      "url": "https://ghe.example.com/mcp",
      "protocol": "streamable-http",
      "auth_broker": {
        "mode": "oauth_connect",
        "authorization_endpoint": "https://ghe.example.com/login/oauth/authorize",
        "token_endpoint": "https://ghe.example.com/login/oauth/access_token",
        "client_id": "Iv1.0123456789abcdef",
        "client_secret": "GHE-secret",
        "scopes": ["repo", "read:user"],
        "resource": "https://ghe.example.com/mcp"
      }
    }
  ]
}
```

### Fields

| Key | Required | Description |
|-----|----------|-------------|
| `mode` | yes | Must be `oauth_connect`. |
| `token_endpoint` | yes | Upstream authorization-server token endpoint where the authorization code is exchanged. |
| `authorization_endpoint` | yes | Upstream authorization-server *authorize* URL the user is redirected to for consent. |
| `resource` | no | RFC 8707 audience the resulting token is scoped to. |
| `scopes` | no | Scopes requested for the upstream credential. |
| `client_id` | no¹ | Identifies the gateway to the token/authorization endpoint. |
| `client_secret` | no | Authenticates a confidential client. A public client may omit it — PKCE still protects the code exchange. Supports `${env:VAR}` so the secret stays out of the file. |

¹ `client_id` is required at runtime for the connect flow (the connector rejects an empty client ID); it is validated when the connect flow is assembled.

:::warning `authorization_endpoint` is mandatory
Config validation fails with `auth_broker.authorization_endpoint is required for mode "oauth_connect"` if the key is missing.
:::

## Credential storage and the encryption key

Per-user credentials are encrypted with **AES-256-GCM** before they are written to BBolt (`config.db`), keyed by `server_edition.credential_encryption_key` — a base64-encoded 32-byte key. The environment variable **`MCPPROXY_CRED_KEY`** is the recommended way to provide it in container or systemd deployments, and **it takes precedence**: when both are set the variable is used and the config value is ignored (`broker.ResolveMasterKey`), so a credential encrypted under one key is unreadable if the other is later put in front of it. Set exactly one.

```bash
# Generate a fresh 32-byte key and base64-encode it
export MCPPROXY_CRED_KEY="$(openssl rand -base64 32)"
```

Store the value in a secret manager (Vault, AWS Secrets Manager, a Kubernetes Secret, …) and inject it at runtime. The key is never written to disk by MCPProxy itself. When no key is configured the credential store is **disabled**: every credential reports `unavailable` and nothing is persisted. Key rotation is not supported; rotating the key means clearing the `user_upstream_credentials` bucket and asking users to connect again.

Stored credentials are scoped per user — one user's credential is never visible to another — and secret values (access/refresh tokens) are never serialised by any REST, CLI or UI surface.

## The `oauth_connect` flow (Path B)

1. The gateway builds an authorize URL from `authorization_endpoint` with a per-user opaque `state` and a PKCE `S256` challenge, and redirects the user there.
2. On the upstream's callback, `state` is validated as a **known, unexpired, single-use** pending flow (10-minute TTL) bound to the initiating user — confused-deputy / replay hardening.
3. The authorization code is exchanged at `token_endpoint` using the bound PKCE verifier; the resulting credential is stored **encrypted, per user**, tagged `ObtainedVia=connect_flow`.
4. That is where the flow ends. The credential is **not** refreshed and **not** injected into any request. Once its access token expires the status becomes `expired`; the user runs the connect flow again to store a fresh one.

A denied consent (`error=access_denied`) clears the pending flow and stores nothing.

## Status surfaces

| Surface | Reference |
|---------|-----------|
| REST | `GET /api/v1/user/credentials`, `DELETE /api/v1/user/credentials/{server}`, `GET /api/v1/user/credentials/{server}/connect`, `GET /api/v1/user/credentials/{server}/callback` |
| CLI | [`mcpproxy credential list\|status\|connect\|rm`](../cli/credential-commands.md) |

Status values are `connected` (a valid, non-expired credential is stored), `expired`, `not_connected` and `unavailable` (no encryption key, store disabled). `connected` describes the *stored* credential only; it does not mean any upstream call carries it.

Connect-flow consent and callback outcomes are recorded as `credential_broker` activity records (see [Activity Log](./activity-log.md)).

## See also

- [OAuth Authentication](./oauth-authentication.md) — upstream OAuth for the personal edition.
- [Server Multi-User Authentication](../development/server-edition-multiuser-auth.md) — the server-edition login front door.
