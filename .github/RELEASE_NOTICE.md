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

- Server edition: a user at 25 tokens gets a `409 Conflict` from `POST /user/tokens` that names *their* quota — permanently deleting one of their unused tokens frees a slot — so one user can no longer take the whole pool. The 100-record deployment cap remains and every stored token still counts toward it, so a deployment whose stored records add up to 100 refuses the next token for everyone until an administrator frees records; a caller who is still under their own quota then gets the `409` that says the limit is shared and points at an administrator (the quota is checked first, so a user already at 25 sees their own-quota message instead). A user who already holds more than 25 tokens keeps them; they cannot create another until they are back under the quota.
- Personal edition: every token is ownerless, so the quota does not apply and the 100-token limit is unchanged.
- No configuration change is needed. Details: [agent tokens](https://docs.mcpproxy.app/features/agent-tokens/).

## Server edition: `/mcp` always requires a credential

When `server_edition.enabled` is true, `/mcp` now behaves as if `require_mcp_auth` were `true` whatever the file says (spec 107, FR-029). Before this release a server-edition deployment with `require_mcp_auth` off (the default) handed every unauthenticated `/mcp` caller an **anonymous administrator** context.

- No credential → `401`. A session cookie or a user JWT on `/mcp` → `401` (they were never valid there; they are no longer silently promoted). Agent tokens (`mcp_agt_…`), the global API key and the Unix socket work exactly as before.
- An explicit `"require_mcp_auth": false` is **not** a validation error, so no deployment fails to boot on upgrade. It logs one notice — `require_mcp_auth: false is overridden to true because server_edition.enabled is true` — and `mcpproxy doctor` reports the same line. Remove the key, or set it to `true`, to silence both.
- Personal edition: unchanged; `require_mcp_auth` keeps its configured value.
- If an AI client reached `/mcp` on a server-edition deployment without any credential, it now needs an agent token: each user mints one from the Web UI or `POST /api/v1/user/tokens` and sends it as `Authorization: Bearer mcp_agt_…`.

## `trusted_proxies` now gates every forwarded header — behind an ingress, set it or `public_url`

`X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto` and `X-Forwarded-Host` used to be believed from **any** peer: a direct client could choose its own session IP, force the OAuth callback to `https`, or move it to another host. They are now honoured only when the request's `RemoteAddr` is inside the new top-level `trusted_proxies` list (CIDRs or addresses; env `MCPPROXY_TRUSTED_PROXIES`, comma-separated; hot-reloadable; both editions), taking the right-most hop that is not itself a trusted proxy as the client IP. The default is empty — **trust nobody** (FR-027).

**What changes behind a reverse proxy or ingress if you do nothing**

- The OAuth `redirect_uri` sent to your IdP is built from the listener's own scheme and `Host` — typically `http://…` — instead of the ingress's `X-Forwarded-Proto: https`. Your IdP's exact-match registration then refuses the callback and every SSO login fails.
- The session's recorded IP, the audit `client.ip`, the connect-flow base URL and the swagger server URL all show the ingress's address, not the user's.

**Fix (either one)**

- Set `server_edition.public_url` to the origin users reach (`https://mcp.example.com`; env `MCPPROXY_PUBLIC_URL`). It becomes the sole source of the callback URL (`<public_url>/api/v1/auth/callback`), the connect-flow base URL and the cookie `Secure` decision; `Host` and `X-Forwarded-*` are ignored for those (FR-025). When it is unset and the listener is not loopback — the published image listens on `0.0.0.0:8080` — boot logs a warning and `mcpproxy doctor` reports it; it is not a validation error.
- Or list your ingress in `trusted_proxies` (`["10.0.0.0/8"]`, `["fd00::/8"]`, a single address). Do both if you also want the real client IP in sessions and audit lines.
- An invalid entry (`trusted_proxies[0] "…" is not a valid CIDR or IP address`) is a validation error at load and on `PATCH /api/v1/config`. No forwarded header ever feeds the local/remote or administrator classification. Details: [reverse proxy](https://docs.mcpproxy.app/operations/reverse-proxy/), [config file](https://docs.mcpproxy.app/configuration/config-file/), [environment variables](https://docs.mcpproxy.app/configuration/environment-variables/).

**Session cookie** — `server_edition.session_cookie_secure` is new: `auto` (default) sets `Secure` when the effective scheme is https (`public_url`, in-process TLS, or `X-Forwarded-Proto: https` from a *trusted* proxy), `true` forces it, `false` disables it. Earlier releases never set `Secure`. Validation refuses `false` together with an `https://` `public_url` or in-process TLS; an explicit `false` elsewhere is honoured with one boot warning and a `mcpproxy doctor` finding. `HttpOnly` and `SameSite=Lax` are unchanged (FR-026).

**Post-login redirect** — `redirect_uri` on `GET /api/v1/auth/login` is accepted only as a same-origin path (a single leading `/`, no scheme, host, `//`, `/\`, backslash or control character); anything else lands on `/ui/` and the login's `auth_event` line carries `redirect_rejected`. The Web UI is unaffected (FR-028).

## Server edition: generic `oidc` identity provider

`server_edition.oauth.provider` accepts `oidc` next to `google`, `github` and `microsoft`, so Okta, Entra ID, Keycloak, Authentik, Auth0 and any other OpenID Connect provider work without provider-specific code (FR-020). The three existing providers behave exactly as before.

- **Configuration**: `issuer_url` (required; `https`, or `http` only for a loopback host **and** `allow_insecure_issuer: true`), `scopes` (default `["openid","profile","email"]`; `openid` is added if missing), `groups_claim` (default `"groups"`), `email_verified_policy` (default `refuse_false`), `display_name` (login-button label; falls back to the provider name). `client_id`/`client_secret` stay `${env:}`-referenced; there is no environment variable for nested keys. `authorization_endpoint`, `token_endpoint`, `jwks_uri` and `userinfo_endpoint` come from `<issuer_url>/.well-known/openid-configuration`, fetched lazily on the first login and cached, so boot and readiness never wait on the IdP.
- **Every ID token is verified before any claim is read**: signature against the issuer's JWKS (RS/PS/ES families only — never `none`, never HS*), exact `iss`, `aud`/`azp`, `exp`/`nbf`/`iat` with 60 s skew, and a per-login `nonce`. Discovered endpoints must be absolute `https` (same loopback exception), and the back-channel client never follows a redirect: the client secret and code are still sent to your configured token endpoint (and a bearer token to your configured userinfo endpoint) as normal, but a 3xx answer from any of the token, JWKS or userinfo endpoints refuses the login instead of being followed — so a compromised or misconfigured endpoint cannot redirect that credential to another host (FR-021).
- **`email_verified_policy`** — `refuse_false` (default) refuses a login whose ID token says `email_verified: false` and admits one where the claim is absent; `require_true` also refuses an absent claim; `ignore` admits both. Pick `require_true` when your IdP always sets the claim; `refuse_false` exists so providers that omit it still work out of the box. `email` itself is required (`email_missing` otherwise).
- **Groups are captured**: on every successful `oidc` login the user record stores the groups claim from the verified ID token (or from `userinfo` when the token lacks it — accepted only when the userinfo `sub` equals the token's `sub`), replacing the previous list wholesale with a `groups_updated_at` timestamp; a missing or malformed claim stores `[]` and logs `groups_claim_missing`. `google`/`github`/`microsoft` logins store `[]`. `GET /api/v1/auth/me` returns your groups; `GET /api/v1/admin/users` shows every user's groups and `groups_updated_at`. Storing groups has no authorisation effect on its own; they are the input to the server-edition `access` grant (FR-008).
- **Subject binding**: a user record now remembers `(provider, provider_subject_id)` and refreshes both on every login. Same provider, same email, **different** subject is refused (`subject_mismatch`), so an IdP email collision cannot take over an existing account; an administrator re-arms the binding for a genuinely re-created IdP account by disabling and re-enabling the user — the next successful login rebinds (FR-023).
- **Refusals are uniform**: every denied login renders one generic `403` page ("Sign-in was not permitted") with a reference id; the reason (`email_unverified`, `subject_mismatch`, `state_invalid`, …) reaches only the server log and the `auth_event` line under that id. IdP-side failures (`discovery_failed`, `provider_error`) and proxy-side failures after verification (`internal_error`) render a `503` "Sign-in is temporarily unavailable" instead, so an outage is never shown as "not permitted" (FR-024).
- **Login page label and edition probe**: public `GET /api/v1/auth/provider` returns only `{"display_name": "…"}` — never the issuer, client id, tenant, scopes or domains — so the Web UI labels the sign-in button and detects the edition before login; the personal build answers `404` (FR-030).
- Guide: [multi-user authentication](https://docs.mcpproxy.app/development/server-edition-multiuser-auth/).
