## 🔒 Colon-named tools are approved by their exact name — one-time review after upgrade

A tool's approval record, search-index entry and callability are now keyed by the **exact name its server reports**, colons included. Earlier releases filed a namespaced tool such as `ns:erase` under the text after its first colon, so it shared — and silently inherited — the approval of a sibling `erase` on the same server. Every dispatch path (`call_tool_*`, direct-name dispatch on `/mcp/all`, `call_tool()` inside code execution), preflight and `describe_tool` now resolve exactly the `server:tool` pair they dispatch.

**What changes for you**

- **On `manual` (default) and `scan` trust, colon-named tools on a server that already has an approved baseline become pending once, under their own names, on the first discovery after upgrade.** They stay uncallable and out of `retrieve_tools` until you approve them: `mcpproxy upstream inspect <server>` to review, `mcpproxy upstream approve <server> <tool>`, the `quarantine_security` MCP tool, or the Web UI. `trust_mode: auto` servers and installs with `quarantine_enabled: false` auto-approve them; no other tool is affected.
- **Blocks carry over.** A tool you had disabled under the old collapsed name stays disabled under its own name until you enable it there; the log records the carry-over at `WARN` with both names. A tool that was locked pending review is pending under its own name and is unlocked by approving it by that name — nothing else is needed, and nothing is deleted. A namespaced tool you had toggled in the UI keeps its old review lock (with the before/after evidence) under its own name until you approve it; if its old record approved a *different* definition than the server reports now, it is held as changed for review, and if it had no old record it is pending under an active gate — a toggle never approves a definition nobody reviewed.
- **Unresolved names are refused for everyone.** A call to a tool that a connected server's discovered tool set does not contain is refused before any upstream call, for administrators too — while the server's discovery has not completed, retry shortly; afterwards, refresh with `retrieve_tools` and retry with a listed name. Quarantined, disabled and disconnected servers keep their existing answers: a call to a disconnected server still gets the not-connected / `reconnect_on_use` answer, and once the server reconnects and completes discovery, a name that result does not list is refused as unresolved — it is never dispatched.

Details: [Security Quarantine → Namespaced tool names](https://docs.mcpproxy.app/features/security-quarantine#namespaced-tool-names) and [Agent Tokens → Target tool tier](https://docs.mcpproxy.app/features/agent-tokens#target-tool-tier).

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
- Remove it from your configuration. Nothing in this release reads the IdP tokens an earlier release stored, and the first start of `mcpproxy-server` with `server_edition.enabled: true` after upgrading **deletes them** from `config.db` (the rows are removed by key before anything else in the server-edition setup runs, so this happens whether `MCPPROXY_CRED_KEY` is still set, unset or even invalid; the log line `purged legacy IdP subject-token rows` reports the count). Credentials connected through the `oauth_connect` flow are not touched.
- The former [IdP Token Storage](https://docs.mcpproxy.app/features/idp-token-storage/) page is now a tombstone.

## Auth broker: a stored credential is stored, not injected

The `oauth_connect` connect flow, its REST routes, the `mcpproxy credential` commands, the encrypted credential store and `MCPPROXY_CRED_KEY` / `credential_encryption_key` all stay. What changes is the promise attached to them: a credential a user connects through the broker is **kept for a future broker and is not injected into upstream tool calls**. It never was — the injection, resolution and per-user connection-keying code paths that the documentation described had no production caller and are deleted in this release (FR-031, FR-034).

- `mcpproxy credential list` and `mcpproxy credential status` now open with the line `Stored credentials are kept for a future broker and are NOT injected into upstream calls in this release.`
- The [auth broker](https://docs.mcpproxy.app/features/auth-broker/) and [credential commands](https://docs.mcpproxy.app/cli/credential-commands/) pages are rewritten accordingly; the "Credential resolution", "Header injection" and "Per-(user, server) connection keying" sections are gone.
- Upstream calls keep using whatever the server's own configuration provides (static headers, the server's own OAuth). If you deployed the broker expecting per-user credentials on upstream calls, that expectation was never met, and this release says so rather than fixing it.
- Historical `credential_broker` activity rows remain readable and labelled.

## Agent tokens: a per-user quota inside the deployment cap

The server edition now enforces a **25-token quota per signed-in user** on top of the existing 100-record deployment cap ([#1177](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1177)). Revoked tokens keep their slot until they are permanently deleted.

- Server edition: a user at 25 tokens gets a `409 Conflict` from `POST /user/tokens` that names *their* quota — permanently deleting one of their unused tokens frees a slot — so one user can no longer take the whole pool. The 100-record deployment cap remains and every stored token still counts toward it, so a deployment whose stored records add up to 100 refuses the next token for everyone until an administrator frees records; that `409` still says the limit is shared and points at an administrator. A user who already holds more than 25 tokens keeps them; they cannot create another until they are back under the quota.
- Personal edition: every token is ownerless, so the quota does not apply and the 100-token limit is unchanged.
- No configuration change is needed. Details: [agent tokens](https://docs.mcpproxy.app/features/agent-tokens/).
