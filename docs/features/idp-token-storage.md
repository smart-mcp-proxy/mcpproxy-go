# IdP Subject Token Storage (Server Edition)

MCPProxy Server edition can persist the IdP (identity-provider) access and refresh
tokens obtained during a user's OAuth login so that downstream services can use them
for on-behalf-of (OBO) token exchange (RFC 8693, spec 074 TokenExchanger). This
feature is **off by default** and requires an encryption key to activate.

## Prerequisites

- Server edition (`go build -tags server`)
- A 32-byte, base64-encoded AES-256 master key

## Configuration

Two settings control the feature, both under the `server_edition` block (configs
that still use the legacy `teams` key are accepted as a back-compat alias):

```json
{
  "server_edition": {
    "enabled": true,
    "store_idp_tokens": true,
    "credential_encryption_key": "<base64-encoded 32-byte AES key>",
    "oauth": { ... }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `store_idp_tokens` | bool | `false` | Enable IdP subject token persistence |
| `credential_encryption_key` | string | `""` | Base64-encoded AES-256 master key for at-rest encryption |

### Environment variable override

`MCPPROXY_CRED_KEY` overrides `credential_encryption_key` at startup and is the
recommended way to supply the key in container or systemd deployments (keeps
secrets out of the config file):

```bash
export MCPPROXY_CRED_KEY="$(openssl rand -base64 32)"
```

The env var takes precedence over the config file value when both are set.

## Key generation

```bash
# Generate a fresh 32-byte key and base64-encode it
openssl rand -base64 32
# Example output: 7h3K...== (44 characters)
```

Store this value in a secret manager (Vault, AWS Secrets Manager, Kubernetes
Secret, etc.) and inject it as `MCPPROXY_CRED_KEY` at runtime.

## Security model

- Tokens are encrypted with **AES-256-GCM** before being written to BBolt
  (`~/.mcpproxy/config.db`).
- The master key is never written to disk by MCPProxy itself; it lives only in
  memory after startup.
- When the master key is absent or empty, `store_idp_tokens` has no effect: the
  credential store is disabled and a warning is logged at each login. No tokens
  are persisted and the feature degrades gracefully to the pre-feature behaviour.
- Stored tokens are scoped per user. One user's credentials cannot be read by
  another user.

## Token lifecycle

1. **Login** — when a user completes the OAuth flow and `store_idp_tokens: true`,
   the provider's `access_token` and `refresh_token` are encrypted and stored.
2. **Use** — `GetValidIDPSubjectToken` returns the stored access token if it is
   valid and not within 60 s of expiry.
3. **Refresh** — when the access token is near-expiry, MCPProxy automatically
   exchanges the refresh token for a new access token using the provider's token
   endpoint. The refreshed token is re-persisted.
4. **Re-auth** — when no refresh token is available, or the refresh fails, the
   user is required to sign in again (`ErrReauthRequired`).

## Per-user credentials REST API (server edition)

Brokered upstreams expose a per-user credential surface under the session/JWT
auth middleware. Every endpoint is scoped to the authenticated caller — a user
can only see and manage their own credentials, never another user's.

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/user/credentials` | List the connection status of every brokered upstream for the caller. |
| `DELETE /api/v1/user/credentials/{server}` | Disconnect (revoke) the caller's credential for an upstream. |
| `GET /api/v1/user/credentials/{server}/connect` | Initiate the per-user OAuth connect flow (Path B); 302-redirects to the upstream authorization server. |
| `GET /api/v1/user/credentials/{server}/callback` | OAuth connect callback; exchanges the code, stores the per-user credential, and redirects back to the Web UI. |

### Connection status (`GET /api/v1/user/credentials`)

The list returns **non-secret metadata only** — access and refresh tokens are
never serialized. Each entry carries a `status`:

- `connected` — a valid, non-expired per-user credential exists.
- `expired` — a credential exists but its access token has expired.
- `not_connected` — no per-user credential exists for this upstream.
- `unavailable` — the credential store is disabled (no encryption key configured).

For `oauth_connect` upstreams that are `not_connected` or `expired`, the entry
includes an actionable `connect_path` pointing at the connect endpoint.

```json
{
  "credentials": [
    {
      "server": "github-shared",
      "mode": "oauth_connect",
      "status": "not_connected",
      "connect_path": "/api/v1/user/credentials/github-shared/connect"
    },
    {
      "server": "internal-api",
      "mode": "token_exchange",
      "status": "connected",
      "token_type": "Bearer",
      "scopes": ["read"],
      "expires_at": "2026-06-15T20:00:00Z"
    }
  ]
}
```

### Connect flow (Path B)

`connect` builds an authorization-code + PKCE URL bound to the authenticated
user and redirects there. After consent, the upstream redirects to `callback`,
which validates the one-time `state`, exchanges the code, and persists the
per-user credential (encrypted, `obtained_via=connect_flow`). The credential is
always stored under the **initiating** user, so the callback cannot be used to
write into another user's record. The browser then lands on `/ui/` with a
`credential_connected` / `credential_error` query flag. The `credential_error`
value is always a coerced, secret-free label (e.g. `access_denied`,
`authorization_denied`); the raw, authorization-server-controlled error string is
never logged or reflected back into the redirect, and token-endpoint failures
surface only the HTTP status plus an allowlisted OAuth error code — never the raw
response body (FR-029 / SC-005).

## Operational notes

- **Key rotation** is not yet supported. Rotating the key requires clearing the
  `user_upstream_credentials` BBolt bucket and asking all users to sign in again.
- If `store_idp_tokens` is disabled after tokens have been stored, the stored data
  remains encrypted in the database but is never read. A future cleanup command
  will be added to purge it.
- The refresh token is only available when the OAuth provider issues one. Google
  and Microsoft both require explicit `offline_access` / `access_type=offline`
  parameters, which MCPProxy adds automatically when `store_idp_tokens: true`.
