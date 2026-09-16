## Server edition: configuration keys and modes that never did anything are gone

This release removes the server-edition knobs and `auth_broker` modes that were accepted by the validator but had no reader in production. An old `mcp_config.json` still loads; what changes is how the removed keys are treated. Personal-edition users are not affected unless the file carries a `server_edition` or `auth_broker` block.

**Removed keys and modes** (spec 107, FR-032):

- `server_edition.max_user_servers` and `server_edition.workspace_idle_timeout` — never enforced.
- Per-server `auth_broker.header` and `auth_broker.header_format` — no request was ever rewritten with them.
- `auth_broker.mode: token_exchange` and `auth_broker.mode: entra_obo` — never implemented. `oauth_connect` is now the only accepted mode.

**What the server edition (`mcpproxy-server`) does with an old file**

- Loading succeeds. Each removed key is dropped with one warning naming it — `server_edition.max_user_servers is no longer supported and was ignored`, `auth_broker.header is no longer supported and was ignored`, and so on. A server whose `auth_broker.mode` is `token_exchange` or `entra_obo` loses its **whole** `auth_broker` block: `auth_broker.mode "token_exchange" was never implemented; the auth_broker block for server "<name>" was ignored`. The next write-back of the file omits the dropped keys.
- Writing them is refused. `PATCH /api/v1/config` and `/api/v1/config/apply` reject a document that carries any removed key or mode with the same message, so a script that still sends `max_user_servers` now gets a validation error instead of a silent accept.
- Remove the keys from your file, and change any `token_exchange`/`entra_obo` server to `oauth_connect` if you want its connect flow to keep working — otherwise its `auth_broker` block is dropped on the next load.

**What the personal edition (`mcpproxy`) does with the same file**

- Nothing. The `server_edition` block and every server's `auth_broker` block now pass through the personal binary as opaque JSON — every key and value preserved, removed keys included, no warning and no validation. Earlier releases wrote both blocks back as `{}`, so an API-key bootstrap or a `PATCH /api/v1/config` from the personal binary could erase a team's SSO or broker configuration; that is fixed here (FR-040).

## `store_idp_tokens` is now a no-op

`server_edition.store_idp_tokens` no longer stores anything. The identity-provider access and refresh tokens it used to persist at login existed only to feed the never-implemented `token_exchange`/`entra_obo` modes, which left a long-lived IdP refresh token at rest with nothing reading it. The writer, the reader and the offline-access scope and authorization parameters that asked the IdP for a refresh token (`offline_access`, `access_type=offline`) are removed (FR-033), so a fresh login no longer requests a refresh token from the IdP.

- The key is still accepted so an old file loads. `"store_idp_tokens": true` logs one warning at boot — `server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it` — and does nothing else.
- Remove it from your configuration. Nothing in this release reads the IdP tokens an earlier release stored.
- The former [IdP Token Storage](https://docs.mcpproxy.app/features/idp-token-storage/) page is now a tombstone.

## Auth broker: a stored credential is stored, not injected

The `oauth_connect` connect flow, its REST routes, the `mcpproxy credential` commands, the encrypted credential store and `MCPPROXY_CRED_KEY` / `credential_encryption_key` all stay. What changes is the promise attached to them: a credential a user connects through the broker is **kept for a future broker and is not injected into upstream tool calls**. It never was — the injection, resolution and per-user connection-keying code paths that the documentation described had no production caller and are deleted in this release (FR-031, FR-034).

- `mcpproxy credential list` and `mcpproxy credential status` now open with the line `Stored credentials are kept for a future broker and are NOT injected into upstream calls in this release.`
- The [auth broker](https://docs.mcpproxy.app/features/auth-broker/) and [credential commands](https://docs.mcpproxy.app/cli/credential-commands/) pages are rewritten accordingly; the "Credential resolution", "Header injection" and "Per-(user, server) connection keying" sections are gone.
- Upstream calls keep using whatever the server's own configuration provides (static headers, the server's own OAuth). If you deployed the broker expecting per-user credentials on upstream calls, that expectation was never met, and this release says so rather than fixing it.
- Historical `credential_broker` activity rows remain readable and labelled.

## Agent tokens: the cap is now per owner

The 100-token limit on agent tokens is now counted **per owner** instead of across the whole deployment ([#1177](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1177), FR-037). Tokens with the same owner count together; operator tokens with no owner form one owner of their own.

- Server edition: one user reaching 100 tokens no longer blocks every other user from creating theirs, and the `409 Conflict` body from `POST /user/tokens` now refers only to the caller's own count — deleting your own tokens does free a slot.
- Personal edition: every token is ownerless, so the limit is unchanged in practice (100), and the `409` body keeps its meaning.
- No configuration change is needed. Details: [agent tokens](https://docs.mcpproxy.app/features/agent-tokens/).
