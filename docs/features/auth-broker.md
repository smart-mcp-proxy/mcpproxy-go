---
id: auth-broker
title: Per-User Auth Broker
sidebar_label: Auth Broker
sidebar_position: 11
description: Server-edition per-upstream token brokering — token exchange, Entra OBO, and per-user OAuth connect flow
keywords: [auth broker, token exchange, entra obo, oauth connect, per-user, server edition, spec 074]
---

# Per-User Auth Broker

:::info Server edition only
The auth broker is part of the **server edition** (`//go:build server`). It is opt-in **per upstream** — servers without an `auth_broker` block behave exactly as before. Brokering applies only to HTTP-family upstreams (`http`, `sse`, `streamable-http`); configuring it on a `stdio` upstream is rejected at config validation in this phase.
:::

The auth broker lets the gateway acquire an **upstream credential on behalf of the calling user** instead of sharing a single static token. Each upstream server can declare how its credential is obtained via an `auth_broker` block on its server config (spec 074).

## Modes

The `auth_broker.mode` field selects the credential-acquisition strategy:

| Mode | Description |
|------|-------------|
| `token_exchange` | RFC 8693 OAuth 2.0 Token Exchange — swaps the caller's IdP token for an upstream-scoped token at the IdP token endpoint. |
| `entra_obo` | Microsoft Entra **On-Behalf-Of** flow. |
| `oauth_connect` | **Path B** — a per-user authorization-code + PKCE *connect* flow against an upstream authorization server that does not support token exchange. The user is redirected to the upstream's consent screen once; the resulting per-user credential is persisted encrypted and refreshed transparently. |

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
| `mode` | yes | One of `token_exchange`, `entra_obo`, `oauth_connect`. |
| `token_endpoint` | yes | IdP/upstream token endpoint used to mint (and refresh) the upstream credential. |
| `authorization_endpoint` | **only for `oauth_connect`** | Upstream authorization-server *authorize* URL the user is redirected to for consent. Required when `mode` is `oauth_connect`; ignored by `token_exchange` and `entra_obo`. |
| `resource` | no | RFC 8707 audience the resulting token is scoped to. |
| `scopes` | no | Scopes requested for the upstream credential. |
| `client_id` | no¹ | Identifies the gateway to the token/authorization endpoint. |
| `client_secret` | no | Authenticates a confidential client. A public client may omit it — PKCE still protects the `oauth_connect` code exchange. |
| `header` | no | Outbound header the resolved credential is injected into (default `Authorization`). |
| `header_format` | no | Value template; `{token}` is replaced with the resolved credential (default `Bearer {token}`). |

¹ `client_id` is required at runtime for the `oauth_connect` flow (the connector rejects an empty client ID); it is validated when the connect flow is assembled.

:::warning `authorization_endpoint` is mandatory for `oauth_connect`
Config validation fails with `auth_broker.authorization_endpoint is required for mode "oauth_connect"` if the key is missing while `mode` is `oauth_connect`. The other two modes never read it.
:::

## The `oauth_connect` flow (Path B)

1. The gateway builds an authorize URL from `authorization_endpoint` with a per-user opaque `state` and a PKCE `S256` challenge, and redirects the user there.
2. On the upstream's callback, `state` is validated as a **known, unexpired, single-use** pending flow (10-minute TTL) bound to the initiating user — confused-deputy / replay hardening.
3. The authorization code is exchanged at `token_endpoint` using the bound PKCE verifier; the resulting credential is stored **encrypted, per user**, tagged `ObtainedVia=connect_flow`.
4. Tokens are refreshed transparently from the stored refresh token; a non-rotating authorization server keeps its prior refresh token.

A denied consent (`error=access_denied`) clears the pending flow and stores nothing.

## See also

- [OAuth Authentication](./oauth-authentication.md) — upstream OAuth for the personal edition.
- Server multi-user authentication is covered in the project `CLAUDE.md` (Spec 024).
