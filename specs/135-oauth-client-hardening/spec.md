# Feature Specification: Harden the OAuth Client Role

**Feature Branch**: `135-oauth-client-hardening`
**Created**: 2026-11-25
**Status**: Draft
**Input**: GitHub issue [#2044](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/2044) (felipecrs). When mcpproxy signs in to upstream authorization servers (its client role), it accepts responses more loosely than its authorization-server role ought to send them. The issue lists three gaps: no RFC 9207 `iss` validation, client credentials not bound to the issuer, and DCR leaving out `application_type`. It proposes upgrading `github.com/mark3labs/mcp-go` from v1.0.0 to at least v1.4.0 and then checking that each fix actually takes effect.

**Related**: Spec 022 (redirect-URI port persistence and DCR credential hygiene), Spec 113 (OAuth endpoint overrides; synthetic metadata from the transport wrapper), Spec 116 (incarnation- and epoch-bound OAuth writes), Spec 123 (`enc:v1` token encryption), Spec 129 / `internal/serveredition/broker` (the server edition's per-user connect flow, also a client role), Spec 132 / #1971 (mcpproxy's own authorization-server role: **out of scope**, see Scope).

Code references are to `origin/main` at `661ad51d7`. The module copy of mcp-go v1.0.0 is cited as `mcp-go@v1.0.0/client/transport/oauth.go`.

## Context: the three claimed gaps, checked against `main`

All three gaps are real. Two of them need more than the dependency bump, because mcpproxy does not use the parts of mcp-go where the upstream fixes live. One premise in the issue is wrong: mcpproxy's authorization-server role does **not** emit `iss` today (see Gap 1).

### Gap 1: no RFC 9207 `iss` check on the authorization response. CONFIRMED

- The loopback callback server copies the query into a `map[string]string`. It keeps only the **first** value of each parameter (`internal/oauth/config.go:2013-2019`), logs and routes `code`, `state`, `error` and `error_description`, and never reads `iss`.
- The callback server writes the success page, or the provider-error page, straight after delivering the query. That happens before the flow exchanges the code (`config.go:2041-2084`), so the flow's verdict never reaches the browser.
- The public remote callback (`internal/oauth/remote_callback.go:67-75`) rejects a repeated `state`, `code` or `error`, but ignores `iss`.
- All three login paths (`internal/upstream/core/connection_oauth.go:1367`, `:1636`, `:2435`) pass only `code` and `state` to `exchangeAuthorizationCode` (`:1678`). That function calls `OAuthHandler.ProcessAuthorizationResponse(ctx, code, state, codeVerifier)` (`:1688`). The callback's `iss` cannot reach the library.
- mcp-go v1.0.0 has no `iss` handling. In v1.4.0, `ProcessAuthorizationResponse` keeps its signature and still does not check `iss`. The check exists only in the new `ProcessAuthorizationCallback(ctx, url.Values, verifier)`. **Upgrading the module therefore does not close this gap by itself.**
- The server-edition broker parses the query in `internal/serveredition/api/credential_handlers.go:307-324`. It reports `error` before it completes anything, then calls `broker.OAuthConnector.Complete(ctx, state, code)` (`broker/oauth_connector.go:224-243`). It never reads `iss`.
- **Correction to the issue**: the authorization-server role does not send `iss`. `mcplogin.BuildRedirect` (`internal/mcplogin/redirect.go:131-146`) only fixes the position of an `iss` key *if a caller passes one*. No caller in `internal/runtime/oauth_login_*.go` or `internal/server/oauth_login_*.go` does. That belongs to Spec 132 and is recorded as a follow-up, not changed here.

### Gap 2: stored credentials are not bound to the issuer. CONFIRMED, plus two sub-gaps

- A stored row is keyed by `GenerateServerKey(name, url)` = `name + "_" + first 16 hex characters of sha256(name|url)` (`internal/oauth/persistent_token_store.go:96-105`). The row (`storage.OAuthTokenRecord`, `internal/storage/models.go:628-657`) holds the access token, refresh token, DCR `client_id`/`client_secret`, callback port, redirect URI, `OAuthEpoch` and `Revision`. **There is no issuer.**
- Spec 116's `CredentialBinding` is `{Incarnation, OAuthEpoch}` (`internal/commitview/commitview.go:47-50`). Apart from a server's creation or explicit replacement (`internal/runtime/configsvc/generations.go:173-184`), the epoch moves only when a config commit clears that server's OAuth state (`:207-209`). During projection repair (`:198-206`), an unchanged stamped entry keeps its epoch (`:211-213`). An upstream whose Protected Resource Metadata (PRM) starts naming another authorization server, or whose metadata document changes, does not commit config, so the binding does not move.
- The Spec 022 hygiene read (`internal/oauth/config.go:1189-1196`) keeps the stored `client_id`, port and redirect URI. The credential read (`:1505-1514`) loads `client_id` and `client_secret` for reuse against whatever discovery resolves now.
- `refreshWithStoredCredentials` (`internal/upstream/core/oauth_refresh.go:124-139`) takes `TokenEndpoint` from the handler's metadata, which may be cached. It then posts the stored **refresh token and client secret** with mcpproxy's own HTTP request (`internal/upstream/core/client.go:989-1010`). mcp-go's v1.3.0 binding does not cover this path.
- **Sub-gap: redirects.** That request uses `Gate().HTTPClient` (`client.go:1009`, `internal/upstream/dialgate/gate.go:473-477`). Its default redirect policy re-sends a POST body on a 307/308, even to another origin. The OAuth redirect policy `oauthCheckRedirect` (`internal/oauth/discovery_cache.go:363-379`), which refuses a cross-origin POST redirect, is not installed on it.
- **Sub-gap: RFC 8414 §3.3 issuer equality.** The `issuer` in a metadata document must equal the issuer URL the document was fetched for. mcp-go v1.0.0 checks only that the URLs in the document are well formed. v1.2.0+ checks equality, but only on its own fetch from PRM `authorization_servers[0]`. mcpproxy usually hands mcp-go an explicit `AuthServerMetadataURL` (`internal/oauth/config.go:1385-1421`, `:1545`), found by its own preflight (`preflightMetadataURL`, `:2353-2402`, through `findWorkingMetadataDoc`, `internal/oauth/discovery.go:930-958`), and mcp-go skips the check on that path. When the preflight fails, the URL may stay empty, and mcp-go then runs its own discovery. When an explicit URL is set, the transport wrapper can fetch that URL again if its cache is empty (`internal/oauth/transport_wrapper.go:491-525`). The check must therefore apply wherever a document is obtained, not only in the preflight.

### Gap 3: DCR omits `application_type`. CONFIRMED, but the right value is not the one the issue proposes

- mcp-go v1.0.0 `RegisterClient` (`mcp-go@v1.0.0/client/transport/oauth.go:987-1019`) sends `client_name`, `redirect_uris`, `token_endpoint_auth_method`, `grant_types`, `response_types`, `scope`, and, when set, `client_uri` and `resource`. It never sends `application_type`. mcpproxy calls it from `internal/upstream/core/connection_oauth.go:1161`, `:1506`, `:2207` and from `oauth_stale_dcr_client.go:133`.
- `application_type` is defined by OpenID Connect Dynamic Client Registration 1.0 §2, not by RFC 7591. It defaults to **`web`** when omitted. Native clients are the ones expected to use loopback `http` redirect URIs. OIDC states the hard "https only, no localhost" rule for web clients that use the implicit grant, and it does not state it for the code grant, but providers commonly apply native-versus-web redirect policy on this field. mcpproxy's normal redirect is `http://127.0.0.1:<port>/oauth/callback` and it is a native app, so the correct declaration is **`native` for a loopback redirect** and `web` only for a non-loopback redirect such as the remote callback. Sending `"web"` for the loopback flow, as the issue suggests, would declare the wrong type. mcp-go v1.4.0 derives the value from the redirect URI (`applicationType(redirectURI)`), and the plan checks that its mapping matches.

## Scope

**In scope**: mcpproxy as an OAuth **client** of upstream authorization servers, in both editions. That covers:

- the upstream login (`internal/oauth`, `internal/upstream/core`)
- the refresh paths, both mcp-go's and mcpproxy's own
- the stored rows in `oauth_tokens`
- DCR and discovery
- health, doctor and docs
- at a lower priority, the server-edition broker connect flow (`internal/serveredition/broker`)

**Out of scope**:

- mcpproxy's authorization-server role (Spec 132: `internal/server/oauth_login_*.go`, `internal/runtime/oauth_login_*.go`, `internal/mcplogin`), including making it emit `iss` (follow-up)
- OIDC `id_token` validation (the client role requests none)
- DPoP, PAR, and JWT access-token validation
- key rotation for Spec 123

## Protection model (what this spec does and does not claim)

- **Mix-up (RFC 9207, RFC 9700 §4.4)**. Every source except operator-configured `synthetic` refuses a present, mismatching `iss`. A `synthetic` login never compares `iss`, so it is always marked not mix-up protected. In this attack, a flow that mcpproxy started for issuer A receives an authorization response that honest issuer H produced. The attacker controls A's metadata and points A's authorization endpoint at H, under a client registration at H whose redirect URI is mcpproxy's loopback callback for A. Loopback URIs can be registered by anyone, so a per-server redirect URI is **not** a defence here. PKCE does not help either: mcpproxy would send the code *and* the verifier to A's token endpoint.
  - The defence is `iss`. A response whose `iss` differs from the flow's expected issuer is refused (FR-003). This protects a flow whenever the honest H sends `iss`, whatever A's metadata claims, because a mismatch is refused regardless of the advertised flag.
  - When H does not send `iss`, RFC 9207 lets the client accept the response, and mcpproxy does. **Such a flow is not mix-up protected.** Doctor and the server detail say so for each server (FR-018). No other countermeasure is available to a client in that case.
- **Credential replay to a swapped server**. A row bound to issuer A never sends its refresh token, client secret or DCR client id to a different issuer (FR-009). Credential-bearing requests never follow a cross-origin redirect (FR-010). The trust anchor is the issuer's metadata document fetched over TLS from the issuer's well-known URL. Anyone who controls that document controls the issuer itself. This spec does not try to tell a legitimate endpoint change at an unchanged issuer apart from a compromise of that issuer, and a change of who controls an unchanged, validly authenticated URL cannot be detected by any issuer check.
- **What authenticates a metadata document.** A matching `issuer` string proves nothing on its own. The document must also have come from the issuer's origin over HTTPS. Two rules enforce this (FR-006a):
  - A metadata fetch follows only same-origin redirects. A document reached through a cross-origin redirect is treated as an issuer mismatch.
  - An issuer, or a token, registration or authorization endpoint, that uses plain `http` on a non-loopback host is **not refused**, because that would break LAN providers that work today. Instead it is reported as `insecure_issuer` in doctor and in the server detail, and every claim in this section is void for that server. Its logins are therefore never marked mix-up protected.
- **Detection timing.** Detection is a user-experience bound, not part of the security invariant. The invariant: any metadata a credential-bearing request uses was validated, and was compared with the credential's binding in the same snapshot. A handler that still holds issuer A's metadata sends to A, which issued the credentials, and that is safe. How soon a swap is *noticed* depends on discovery (FR-009b). Today the shared discovery cache is invalidated only when a 401 advertises a different PRM URL. A live handler also memoizes its own metadata, so today a swap at an unchanged PRM URL may never be noticed by a warm connection. This feature adds the detection rule of FR-009b.
- **Rows written before this feature** are bound by trust on first use (FR-011). Until a row is bound, it has no replay protection. Binding happens on the row's first use after the upgrade, normally the first connect. A swap that happened before the upgrade, or between the upgrade and the first use, is bound in rather than caught. This is a recorded decision (Assumption 2), not an oversight.

## User Scenarios & Testing *(mandatory)*

### User Story 1: A sign-in whose response came from the wrong authorization server is refused (Priority: P1)

A user signs in to an upstream through mcpproxy. The authorization response that arrives at the callback either carries an `iss` that differs from the issuer the flow authorized against, or carries no `iss` although that issuer advertises `authorization_response_iss_parameter_supported: true`. mcpproxy does not exchange the code. The browser shows a failure page, and the server's status shows why. No token is stored, and the login is not marked complete. A DCR registration that was persisted before the browser opened stays, bound to the flow's expected issuer (FR-012). It belongs to the issuer the user meant to reach, so it is reusable for the next attempt.

**Why this priority**: the RFC 9207 mix-up defence is the first gap the issue names. It is also the only gap where a hostile authorization server can obtain an honest server's authorization code.

**Independent Test**: run two fake authorization servers (honest H, hostile A) with switches for "advertise the flag", "send iss" (correct, absent, empty, wrong, repeated) and "metadata points the authorization endpoint at H". Drive full logins through the real callback server and assert, for every combination: zero or one token request, the callback page, and the code the server reports.

**Acceptance Scenarios**:

1. **Given** the expected issuer advertises the flag, **When** the callback carries `iss` equal to it, **Then** the code is exchanged and the login completes as it does today.
2. **Given** the same, **When** the callback carries no `iss`, **Then** no token request is sent, the page says the response could not be confirmed as coming from the expected authorization server, and the server reports `MCPX_OAUTH_ISSUER_MISMATCH`.
3. **Given** a strict flow of source `discovered`, `configured` or `fallback`, with or without the flag, **When** the callback carries an `iss` that is present and not exactly equal to the expected issuer, including `iss=` with an empty value, **Then** the same refusal happens. Under source `synthetic` (FR-004) and under the opt-out (US6.1), the same response is accepted, and the login is marked not mix-up protected. The broker follows US7.
4. **Given** metadata that does not advertise the flag, **When** the callback carries no `iss` parameter at all, **Then** the login proceeds as today, and the server detail shows that the login was not mix-up protected.
5. **Given** the mix-up set-up (A's metadata sends the browser to H, and H sends `iss=H`), **When** the response arrives in A's flow, **Then** it is refused and A's token endpoint receives nothing. **Given** instead that A is an operator-configured metadata URL whose document declares issuer H, **Then** the login is not refused, it is marked not mix-up protected, and doctor shows `configured_issuer_unverified` (FR-008). The same holds when A and H are different tenants on one host.
6. **Given** any callback surface, **When** the query repeats `code`, `state`, `iss` or `error`, carries both `code` and `error`, or carries neither, **Then** it is rejected before any exchange and before any provider error is shown.
7. **Given** an error response (`error=access_denied`) whose `iss` fails the check, **When** it arrives, **Then** it is reported as an issuer mismatch, not as the provider's error.
8. **Given** a refused callback, **When** the user starts the login again against the correct server, **Then** it succeeds with no manual cleanup.
9. **Given** a refused callback, **Then** the existing grant is unchanged, unless the flow was a login after `ISSUER_CHANGED`, whose pre-browser DCR write already removed the old issuer's tokens (FR-012). Those tokens were already refused.

---

### User Story 2: Stored credentials are never sent to a different authorization server (Priority: P1)

A server was signed in through authorization server A. Later its PRM names authorization server B, or discovery otherwise resolves an issuer other than A. mcpproxy sees that the stored client registration and tokens were issued by A. It sends none of them to B: no refresh token, no client secret, no reused DCR `client_id`. The server asks for a fresh sign-in and says why.

**Why this priority**: a refresh token and a client secret are long-lived. Today a metadata swap hands both to the new server on the next refresh, with no user action.

**Independent Test**: log in against fake AS-A. Switch the fake resource's PRM to fake AS-B at the same PRM URL **while the connection stays up and the handler stays warm**, then advance the injected clock past the discovery TTL, or fire one of the FR-009b invalidation events, before triggering each path. Trigger a proactive refresh, a reactive refresh after a 401, a stored-credential refresh, and a re-login. Assert that AS-B's token and registration endpoints never receive A's refresh token, A's DCR client id or any secret bound to A, and that the server shows `MCPX_OAUTH_ISSUER_CHANGED` with the action US2.1 names. The one permitted send is an operator-configured *public* client id at B, on an explicit login only (US2.5). Repeat the confidential static-client case after a logout and after a scopes edit (US2.10).

**Acceptance Scenarios**:

1. **Given** a row bound to issuer A, **When** any path is about to send a stored credential and the validated metadata snapshot it will use names issuer B, **Then** it sends nothing, and the server reports `MCPX_OAUTH_ISSUER_CHANGED`, with health action "login" (DCR or public static client) or "configure" (confidential static client, US2.10). The message names both issuers by origin.
2. **Given** an issuer change has been detected, **Then** the row's access token is no longer attached to upstream requests. Before detection, an unexpired access token keeps going only to the unchanged resource URL it was always sent to. It never goes to an authorization server, so no extra discovery is added to make detection happen sooner.
3. **Given** the same state, **When** the user signs in explicitly, **Then** mcpproxy registers with B (DCR) or uses the static client id from config. A's tokens are removed in the same transaction that first writes anything bound to B (FR-012), and the completed login writes the row bound to B.
4. **Given** a row bound to issuer A, **When** discovery resolves A again, **Then** nothing changes.
5. **Given** an operator-configured static `client_id` **without** a secret (public client), **When** the issuer changes, **Then** the stored tokens are refused, the configured id is not cleared, and an explicit login may use it at B.
10. **Given** an operator-configured static `client_id` **with** a `client_secret` (confidential client), **When** the issuer changes, **Then** a re-login alone does not send that secret to B. The health action becomes "configure" with `MCPX_OAUTH_ISSUER_CHANGED`, until the operator changes the configured credentials (an OAuth clear that moves the epoch and starts a new binding) or reverts the upstream.
11. **Given** a confidential static client bound to A, **When** the user logs out, a scopes edit clears the grant, or the effective client id changes (operator edit or DCR), and discovery now resolves B, **Then** the next login still does not send the unchanged secret to B.
12. **Given** a row bound under `fallback`, **When** a re-login against the same issuer under `discovered` persists its DCR registration and the login is then cancelled, **Then** the old tokens were removed in the DCR transaction, and none of them is sent.
13. **Given** `oauth.extra_params.client_secret`, `client_assertion`, `Client_Secret` in any case, or an alias credential such as `X-Amz-Signature`, is configured, **When** a login starts, **Then** the final browser URL, the authorization probe and the log lines do not carry it, and it is sent only in the token request that passed the FR-009 checks.
14. **Given** a configured `client_assertion` (or an alias credential such as `X-Amz-Security-Token`) that was used at A, **When** discovery resolves B and the user logs in explicitly, **Then** the final token request body sent to B does not carry it. An extras `refresh_token`, `code_verifier` or `code`, or any normalised alias of one, such as `Refresh-Token`, never replaces the flow's own value. **When** the operator replaces the assertion with a fresh one for B, keeping the same `client_assertion_type`, **Then** the login works.
15. **Given** no `oauth.client_id` and only `extra_params.client_id`, **When** a login runs, **Then** the authorization URL and the token request carry the same client id.
6. **Given** a row that cannot be decrypted (Spec 123), **When** it is read, **Then** it is reported as undecryptable, as today. The issuer check is never reached.
7. **Given** the token endpoint answers a credential-bearing request with a redirect to another origin, **When** any path sends a refresh, code exchange or registration, **Then** the redirect is not followed.
8. **Given** discovery changes between the authorization request and the callback, with no stored row, **When** the code is exchanged, **Then** it is redeemed only at the endpoint captured with the request, or the login is aborted and restarted. It is never redeemed at the new server.
9. **Given** A and B issue the same `client_id` with different secrets, and a handler from A is still warm, **When** the row and the snapshot are B's, **Then** the A-held secret is not sent.

---

### User Story 3: Upgrading keeps every server signed in (Priority: P1)

A user upgrades with several OAuth servers signed in. None of them is asked to sign in again. Each existing row becomes bound to its issuer the first time it is used after the upgrade, and the US2 rule applies from then on.

**Why this priority**: a forced re-login on every server after an upgrade is a regression users notice at once. The issue asks for "migrate existing records on read".

**Independent Test**: seed `config.db` with pre-upgrade rows (no issuer), both plaintext and `enc:v1`. Start the new core against fake servers. Assert that the servers stay connected, that each row records its issuer after first use, and that sealed rows stay sealed.

**Acceptance Scenarios**:

1. **Given** a pre-upgrade row, **When** it is first used and a validated discovery resolves an issuer, or overrides yield a synthetic one, **Then** the binding is committed first and the row is then used as today (trust on first use).
6. **Given** two first uses of the same unbound row that resolve different issuers concurrently, **Then** exactly one binding commits, and the other caller sends nothing unless the committed binding matches its own snapshot.
7. **Given** an upgraded server with a configured confidential client and an unexpired access token, **When** it is bound on first use without any refresh, and discovery later switches to B, **Then** the secret's provenance was already set to A in the binding transaction. The next login gets the "configure" action, and the secret is not sent.
8. **Given** a legacy row for a path issuer whose sign-in relied on origin-level fallback, **When** it is first used, **Then** discovery fails with `MCPX_OAUTH_DISCOVERY_FAILED`, the row is not bound, and none of its credentials are sent.
2. **Given** a pre-upgrade row, **When** discovery for that use fails, **Then** nothing is recorded, the use behaves as a discovery failure does today, and the row stays unbound.
3. **Given** a sealed pre-upgrade row, **When** the issuer is recorded, **Then** the row is re-sealed with the same key and the same additional data (key id, bucket, bucket key), and the issuer sits inside the sealed body.
4. **Given** a pre-upgrade row whose first post-upgrade discovery resolves a *different* server than the one that issued it, **When** it is used, **Then** it is bound to that server. This is the accepted trust-on-first-use risk. The test pins the behaviour so that any later change to it is deliberate.
5. **Given** a row bound by the new version, **When** an older mcpproxy runs on the same `config.db`, **Then** it reads and uses the row. An ordinary token or registration save by the older binary drops the binding, and on the next upgrade the row goes through US3.1 again. Spec 123's envelope-only rewrites (seal or decrypt in place) keep unknown fields, so they keep the binding.

---

### User Story 4: Discovery refuses a metadata document that names another issuer (Priority: P2)

An upstream's PRM names authorization server `https://auth.example.com`, but the document at that server's well-known URL declares `"issuer": "https://evil.example"`. mcpproxy stops before any authorization redirect, registration or token request, and the server status says the metadata is inconsistent.

**Why this priority**: issuer equality is what makes the expected issuer in US1 and US2 trustworthy.

**Independent Test**: use a fake PRM/AS pair whose `issuer` can be set independently of where it is served. Cover the PRM path with an origin issuer and with a path issuer, the no-PRM fallback, OIDC-only providers, a wrapper cache refill, and a resolver rejection after which the library must make no discovery request of its own.

**Acceptance Scenarios**:

1. **Given** PRM names issuer URL `X`, **When** a document fetched from a well-known URL derived from `X` declares an issuer equal to `X` (one trailing `/` ignored), **Then** discovery succeeds.
2. **Given** a declared issuer that differs from the issuer URL its candidate was derived from, **When** discovery runs, **Then** it fails with `MCPX_OAUTH_METADATA_ISSUER_MISMATCH`, no further candidate is tried, mcpproxy does not fall back to synthetic or default endpoints, and the health action is "configure".
3. **Given** PRM names a path issuer (`https://h/tenant`), **Then** only candidates derived from that full issuer are valid. An origin-only document (`https://h/.well-known/oauth-authorization-server` declaring `https://h`) is a mismatch. A document served at a sibling path that decodes the same way (`/a/b` against an issuer of `/a%2Fb`) is never fetched, because such issuers are refused as ambiguous.
4. **Given** any document that mcpproxy or mcp-go acts on, **Then** it has passed this check, wherever it was obtained (resolver or wrapper refill). After a resolver rejection the library runs no discovery of its own. On success it uses only the resolver's snapshot.
5. **Given** an operator-configured `oauth.auth_server_metadata_url` (Spec 113), **Then** its declared issuer is used as is, with source `configured`. A missing `issuer` fails.
6. **Given** endpoint overrides that make the wrapper serve a synthetic document, **Then** the issuer is the synthetic one with source `synthetic` (FR-004).
7. **Given** discovery with no PRM issuer bound a row to the MCP URL's issuer form under source `discovered`, **When** PRM later names that same issuer and the same document validates, **Then** nothing changes. **When** PRM names a different issuer, **Then** it is `ISSUER_CHANGED`.
8. **Given** a row bound under `fallback` to `https://h`, **When** PRM or a published document now yields `https://h` under source `discovered`, or the reverse happens, **Then** reuse is refused with `MCPX_OAUTH_ISSUER_CHANGED`.

---

### User Story 5: DCR declares the right application type (Priority: P2)

When mcpproxy registers itself, the request sends `"application_type": "native"` for a loopback redirect URI and `"web"` for a non-loopback one.

**Why this priority**: it fixes registrations that strict providers reject. It is not an attack defence.

**Independent Test**: a fake registration endpoint records the request body for an IPv4 loopback, IPv6 loopback, `localhost` and remote-callback redirect.

**Acceptance Scenarios**:

1. **Given** an `http` redirect whose host is `127.0.0.1`, another `127/8` address, `[::1]` or `localhost`, **When** DCR runs, **Then** the body has `application_type: "native"`.
2. **Given** a non-loopback redirect host, or an `https` redirect on a loopback host, **When** DCR runs, **Then** the body has `application_type: "web"`.
3. **Given** a provider that rejects the field, **Then** mcpproxy does not retry without it. The existing DCR error surfaces, and the docs name the supported remedy: a pre-registered client, configured as a static `oauth.client_id`.

---

### User Story 6: An operator can relax issuer checks for one misbehaving provider (Priority: P2)

A provider's metadata `issuer` differs from its well-known URL, or it sends a mangled `iss`, so a correct sign-in is refused. The operator sets `oauth.skip_issuer_validation: true` on that one server. The login works. Doctor and the server detail warn that the checks are off.

**Why this priority**: without an escape hatch, one non-compliant provider blocks a user completely. Scoping it to one server and showing it keeps it from becoming a silent downgrade for everyone.

**Independent Test**: two servers share one fake AS that misreports `iss` and `issuer`. One server has the switch; both servers have the same URL and overrides, so they share discovery-cache keys. Only the switched server signs in, in either cache-fill order. A hot reload back to strict refuses the next login.

**Acceptance Scenarios**:

1. **Given** the switch on, **When** `iss` mismatches or the metadata issuer differs, **Then** the login proceeds, with a WARN line naming the server and both values (scrubbed).
2. **Given** the switch on, **Then** credentials are bound to the **discovery target**: the PRM `authorization_servers` entry, or the configured metadata URL. They are not bound to the document's unchecked `issuer` claim, so a replacement server that declares the old issuer string cannot inherit them.
3. **Given** the switch on, **When** a row bound to target A sees target B, **Then** US2 applies. The switch never lets credentials cross targets.
4. **Given** a strict and a switched server that share cache keys, **Then** neither one's validation result affects the other.
5. **Given** the switch on, **Then** doctor, `GET /api/v1/diagnostics` and the server's OAuth detail show `issuer_validation_disabled` for that server.
6. **Given** the switch on and a row bound to target A, **When** A's metadata URL redirects to another origin, **Then** discovery fails and no credential is sent.

---

### User Story 7: The server-edition connect flow checks `iss` when the operator names the issuer (Priority: P3)

A brokered upstream (`ConnectorConfig`, static endpoints, no discovery) can now also be configured with an `issuer`. When it is set, the connect callback applies the US1 rules for a *present* `iss` (a wrong or empty value is refused as `connect_failed`), and accepts an absent `iss`, because static config has no flag that says the server sends it. When no issuer is set, only the strict query rules of FR-001 apply.

**Why this priority**: the operator pins the endpoints, so the mix-up surface is small, and the code is separate.

**Independent Test**: a broker test (`-tags server`) with the issuer set or unset, and `iss` correct, wrong, empty, absent or repeated, plus `error` combined with a wrong `iss`.

### Edge Cases

- **Comparison**: `iss` and the stored issuer are compared as exact strings (RFC 9207 §2.4). Metadata issuer equality ignores one trailing `/`. Case is not normalised.
- **Several authorization servers in PRM**: only the entry discovery uses (the first, as today) counts.
- **Discovery cache**: the expected issuer of a login is the snapshot taken with its authorization request (FR-002). A credential send uses one validated snapshot both for the issuer check and for the endpoint it sends to (FR-009).
- **Concurrent writes**: every issuer write runs inside the existing binding-checked transaction, compares the revision it read (a login compares a per-server write counter that a delete also moves), and never stamps a row that was replaced or deleted meanwhile (FR-011, FR-012).
- **Purge and server delete**: the issuer is inside the row, so the existing purge set covers it.
- **Undecryptable row**: the Spec 123 report comes first, and the issuer is unknown.

## Requirements *(mandatory)*

### Functional Requirements

**Callback (all client callback surfaces: loopback, remote, broker)**

- **FR-001**: Every callback surface MUST parse the query once, strictly, and reject the following before any exchange, provider-error report or success page: a repeated `code`, `state`, `iss` or `error`; both `code` and `error`; neither; a present but empty `code` or `error`. A request rejected here MUST NOT use up a pending flow on the public remote callback (today's behaviour). The multi-valued query MUST reach the issuer decision intact.
- **FR-002**: When mcpproxy builds an authorization request, it MUST record, with the pending flow, the expected issuer, its source (`discovered`, `configured`, `fallback` or `synthetic`) and the `authorization_response_iss_parameter_supported` value from the same validated snapshot.
- **FR-003**: The issuer decision MUST run in the callback handler, bound to the flow that the `state` selects, before the browser page is chosen. For sources `discovered` and `configured`, the flow MUST be refused when `iss` is present and not exactly equal to the expected issuer (an empty value is not equal), or when `iss` is absent and the snapshot advertised support. An absent `iss` MUST be accepted only when support was not advertised, and that login MUST be marked "not mix-up protected".
- **FR-004a**: When no metadata document exists and no override is configured, mcp-go's default endpoints derived from the origin may be used as today, under source `fallback`. The expected issuer is the issuer URL that discovery selected: the PRM entry, or else the MCP origin. A present `iss` that differs from it is refused, exactly as in FR-003. An absent `iss` is accepted, and the login is marked not mix-up protected. The binding is that issuer, used as both issuer and target, with source `fallback`. Fallback MUST NOT be used when the selected issuer has a path component (a PRM path issuer): the origin-level defaults were never authorised by that issuer, so discovery fails instead. A binding's source is part of the comparison: credentials bound under `discovered` or `configured` MUST NOT be sent to `fallback` endpoints, and the reverse holds too.
- **FR-004**: For source `synthetic` (operator-configured Spec 113 endpoint overrides only), the endpoints themselves are the operator's trust anchor. An absent `iss` MUST be accepted. A present `iss` MUST be logged (WARN if it differs from the synthetic issuer) and MUST NOT be persisted as provenance. The credential's issuer is the synthetic issuer.
- **FR-005**: An error response MUST pass the FR-003 decision before its `error` is reported. A failure is reported as the issuer mismatch.

**Discovery (RFC 8414 §3.3)**

- **FR-006a**: Metadata fetches MUST NOT follow a cross-origin redirect. Doing so is an issuer mismatch, under the same code. An `http` (non-loopback) issuer or endpoint MUST be reported as `insecure_issuer` (doctor warning, server detail) and is otherwise left as it is today.
- **FR-006**: Every metadata document that mcpproxy or mcp-go acts on MUST have a declared `issuer` equal (one trailing `/` ignored) to the issuer URL its candidate URL was derived from, wherever it was fetched (preflight, transport-wrapper refill, library discovery). Each candidate MUST carry the issuer it was derived from. For a PRM path issuer, only candidates derived from the full issuer are valid. Candidate derivation MUST work on the escaped path, so that distinct issuers never map to one candidate URL. An issuer with a query, a fragment, an encoded `/` (`%2F`) a dot segment, or a `.well-known` segment in its path MUST be refused as ambiguous (otherwise the OIDC insertion and suffix forms of two issuers can meet at one URL). A mismatch or a missing `issuer` MUST be terminal for that discovery: no further candidates, no synthetic or default-endpoint fallback, nothing cached as a success.
- **FR-007**: Validation MUST be per consumer. The discovery cache MUST NOT let one server's validation policy decide another server's result. Either cache raw documents and validate on use, or partition validated results and failures by policy.
- **FR-008**: A configured `oauth.auth_server_metadata_url` MUST skip the derivation compare of FR-006. Its declared issuer is used with source `configured`, and a missing `issuer` fails. A configured document can name any issuer, and same-origin retrieval authenticates only the host of the document. So a `configured` flow counts as mix-up protected only when the configured metadata URL is **exactly one of the well-known URLs derived from its declared issuer** under FR-006 (RFC 8414 or OIDC forms on the escaped path). In that case the document is authenticated exactly as a discovered one is. Otherwise the login is allowed as today, but it is marked not mix-up protected and doctor warns `configured_issuer_unverified`. Examples: a document at A that declares honest issuer H; tenant A's document on the same host declaring tenant H.

**Issuer-bound credentials**

- **FR-009**: Before sending a refresh token, client secret, DCR `client_id`, authorization code or PKCE verifier to an authorization server, every path MUST compare the binding of the credentials it will send with the validated snapshot it will use for that request, and MUST build the request from that same snapshot. This applies to every credential the request carries, whether it comes from the stored row or is held in a live handler's config. Each handler that holds client credentials MUST carry their binding, and a handler whose binding differs from the snapshot MUST NOT send. A code exchange MUST use the endpoints and client registration captured with its authorization request (FR-002). If the handler's metadata no longer matches that capture, the exchange MUST be aborted with `MCPX_OAUTH_ISSUER_CHANGED` and the login started again. It MUST NOT be redeemed elsewhere. The paths are: library refresh through the token store, `refreshWithStoredCredentials`, the refresh manager, login reuse of a registration, and the code exchange. On inequality the path MUST NOT send, MUST stop attaching the row's access token to upstream requests, and MUST report `MCPX_OAUTH_ISSUER_CHANGED`. An issuer change is detected whenever discovery runs for that server (connect, 401, refresh, login), and its effect clears if discovery later resolves the bound issuer again.
- **FR-009b**: Every refresh attempt, every login and every 401 handling MUST resolve discovery through the shared cache. An entry is reused only within its success TTL, so after expiry discovery is re-resolved. When the result differs from the live handler's metadata, the handler MUST adopt the new validated snapshot, and only then run the FR-009 comparison. This feature also invalidates the server's discovery entries on a refresh that fails with `invalid_grant` or `invalid_client`, and on an explicit login. A resolved snapshot MUST expire no later than the oldest cached input it was derived from (the PRM entry, the metadata document). A swap is therefore noticed no later than the first refresh, login or 401 after one success TTL from the moment the swap became visible upstream. This requirement is separate from the FR-009 send invariant, which holds whatever the freshness.
- **FR-010**: Requests that carry a client secret, refresh token, authorization code or PKCE verifier (token, refresh, registration) MUST NOT follow any redirect, same-origin or cross-origin, on any path. This is a deliberate tightening: today a same-origin POST redirect is followed. A redirect answer surfaces as a token-endpoint error. This includes the `Gate().HTTPClient` used by `refreshTokenWithStoredCredentials`. Dynamic client registration requests MUST NOT follow any redirect either, whether or not they carry a credential. This keeps today's ban on cross-origin POST redirects (`oauthCheckRedirect`, `internal/oauth/discovery_cache.go:363-379`).
- **FR-010b**: Keys in `oauth.extra_params` are matched case-insensitively after the same normalisation the existing sensitive-name matcher uses (`internal/oauth/logging.go:376-419`). Each key falls into exactly one class, checked in this order, and the first match wins. Today the transport wrapper injects extras into the authorization query and the token form just before transport (`internal/oauth/transport_wrapper.go:178-191`, `:403-420`, `:437-455`), and `applyExtraParamsToAuthURL` copies every extra into the authorization URL (`internal/upstream/core/oauth_stale_dcr_client.go:38-61`). Under this feature:
  1. **Flow- or row-owned values**: `code`, `code_verifier`, `code_challenge`, `code_challenge_method`, `refresh_token`, `access_token`, `id_token`, `state`, `redirect_uri`, `grant_type`, `response_type`, `scope`, and any key that normalises to one of them. Extras MUST NOT set or replace these on any channel. They are dropped with a value-free WARN. `scope` stays configurable through `oauth.scopes`.
  2. **Legacy `client_id`**: a supported source of the effective client id (`internal/upstream/core/connection_oauth.go:1259-1267`). It is resolved once into the effective client registration and used consistently in the authorization URL and the token requests. Generic injection no longer sets it.
  3. **`client_assertion_type`**: a public protocol identifier. It is back-channel only and carries no provenance, so a fresh assertion for B works with the same type.
  4. **Client-authentication credentials**: `client_secret`, `client_assertion` and `assertion` (each with its normalised aliases, listed explicitly because the matcher's exact lookup would miss `clientassertion`), plus every remaining key that the normalised sensitive-name matcher classifies as credential-bearing. That includes `password`, `api_key`, `authToken`, `sig`, `signature`, `credential`, `token` and the `X-Amz-*` credential names. Because the class is defined by the matcher, a name the matcher learns later is covered too. These keys are back-channel only: they MUST NOT appear in a browser-facing authorization URL, an authorization probe or a log line. In token requests they keep working, under FR-012 provenance.
  5. **Plain**: everything else. These keys are passed as today.
- **FR-009a**: FR-009 applies to the **final effective request**, after every injection. The credential values the request carries on the wire are the ones whose binding is compared.
- **FR-011**: A row with no binding MUST be used as today and bound on its first use to the snapshot of that use, for every source: `discovered` or `configured` bind to the validated issuer and target, and `synthetic` binds to the synthetic issuer derived from config. The binding MUST be committed **before** any of the row's credentials are released to an authorization server. The write compares the revision it read and an empty binding inside one transaction. When the server has a configured confidential static client, the same transaction also creates that secret's durable provenance (FR-012) with the same binding, unless provenance already exists. When that comparison fails, the caller MUST reload the row and validate it against its own snapshot under FR-009, or abort. It MUST NOT send the credentials it read.
- **FR-011a**: A legacy sealed row whose body plus the new binding fields would exceed Spec 123's seal limit (1 MiB, `internal/tokenseal/tokenseal.go:115-147`) fails closed. It is not bound, its credentials are not released, it is never rewritten in plaintext, and it is reported as `MCPX_OAUTH_REAUTH_REQUIRED` with a log line that names the size. An explicit login replaces it. Real rows are far below this size; the rule exists so that the boundary is defined.
- **FR-012**: Every row write MUST store the issuer that issued what it writes, inside the record body (sealed with the rest under Spec 123, with unchanged additional data). A row MUST never hold a registration from one issuer and tokens from another: the first write whose binding differs from the row's under the full FR-013 comparison (issuer, target or source class), typically the DCR registration persisted before the browser opens, MUST remove the tokens of the old binding in the same transaction. A source-class change at an unchanged issuer counts as a difference. A configured static `client_id` is never cleared. A configured static client means the **effective** client id and client-authentication credentials (FR-010b class 4) that a request would carry after every override. Each such credential gets the provenance rule below, keyed by server and that credential's own fingerprint. That covers `oauth.client_id`/`oauth.client_secret` and the same keys supplied through `oauth.extra_params`, which the transport wrapper injects into token requests (`internal/upstream/core/oauth_refresh.go:36-55`, `internal/oauth/transport_wrapper.go:423-455`). Its secret is bound to the issuer of its first validated use. Because the key is the secret alone, a client id that changes does not reset it. That covers both a DCR-assigned id when only the secret is configured (for example `extra_params.client_secret`) and an operator edit of the id alone. That provenance MUST be durable: it is stored apart from the token row under one key: the server plus a fingerprint of the secret alone. It is committed before the first secret-bearing request. It MUST survive logout and any grant clear that leaves those credentials unchanged (for example a scopes edit). It is removed only when the effective configured **secret** changes, or when the server is deleted. A change to the client id alone, whether configured or assigned by DCR, keeps it. The secret MUST NOT be sent to a different issuer until the operator changes the secret itself, and a re-login does not authorize that.
- **FR-013**: Every binding MUST record both the issuer and the **discovery target**:
  - the PRM `authorization_servers` entry;
  - when PRM names no authorization server, the issuer URL that the successful candidate was derived from (the MCP URL's issuer form, as in FR-006);
  - the configured metadata URL;
  - for `synthetic`, the synthetic issuer.

  So if PRM later appears, disappears or selects another issuer, the comparison is between two well-defined bindings. Continuity requires the full FR-013 comparison to pass, including source class. A same-target move between `fallback` and `discovered` in either direction is `ISSUER_CHANGED`. In strict mode the comparison checks the issuer, the target and the source class (`fallback` versus the rest). With `skip_issuer_validation` on, the declared-issuer comparison is the only one dropped, because the declared issuer is then an unchecked claim. Target and source class are still compared. Toggling the switch therefore keeps continuity in both directions: the target is unchanged, and in strict mode a validated issuer that equals the stored one is required. No row is ever re-bound to a different target without a login.

**Strictness and opt-out**

- **FR-014**: A per-server `oauth.skip_issuer_validation` (default `false`) MUST, for that server only and with a WARN line on each skipped check, accept the callback outcomes below for every issuer source:
  - a present `iss` that differs from the expected issuer (FR-003, FR-004a);
  - an absent `iss` while support was advertised (FR-003);
  - the FR-005 ordering, so the provider's `error` is reported;
  - a metadata issuer that fails FR-006's equality compare.

  Every login where such a check was actually waived is marked not mix-up protected. A refresh that the opt-out allows continues the existing grant under its stored binding and never adopts the unchecked issuer. It MUST NOT disable FR-001, FR-006a (same-origin metadata retrieval stays mandatory, otherwise a redirect from target A to B would yield B's endpoints under A's binding), FR-009, FR-010, FR-011, FR-012 or FR-013's target comparison. There is no global switch.
- **FR-015**: The field MUST be wired like other OAuth fields: config load and validation, hot reload, OAS, Web UI server editor, macOS server editor, and the redaction field decisions (it is not a secret). Changing it MUST NOT clear stored OAuth state or move the OAuth epoch.

**DCR**

- **FR-016**: DCR MUST send `application_type: "native"` when the registered redirect URI is an `http` URI whose host is a loopback address or `localhost`, and `"web"` otherwise, including an `https` URI on a loopback host (OpenID Connect DCR 1.0 §2 allows a native client only loopback `http` or custom-scheme redirect URIs). It MUST NOT retry without the field.

**Surfaces**

- **FR-017**: Three diagnostics codes, each with a `docs/errors/<CODE>.md` page:
  - `MCPX_OAUTH_ISSUER_MISMATCH`: a response was refused. It is recorded as an OAuth failure, with health action "login".
  - `MCPX_OAUTH_ISSUER_CHANGED`: stored credentials belong to another issuer. Health action "login", or "configure" when the credential is a configured static client secret.
  - `MCPX_OAUTH_METADATA_ISSUER_MISMATCH`: health action "configure".
- **FR-018**: The callback failure page for FR-003 and FR-005 MUST say that the response could not be confirmed as coming from the expected authorization server, that nothing was signed in, and how to retry. It MUST NOT echo the received `iss`. Doctor, `GET /api/v1/diagnostics`, `mcpproxy auth status` and the server's OAuth detail MUST show the bound issuer origin, its source, and whether the last login was mix-up protected. Doctor MUST also list servers with `ISSUER_CHANGED`, servers with `skip_issuer_validation`, servers with `insecure_issuer`, and count the unbound rows. Scoped callers see only servers in their reach.

**Dependency**

- **FR-019**: `github.com/mark3labs/mcp-go` MUST be at least v1.4.0. Existing OAuth tests stay green, and every upstream behaviour change is adopted or neutralised on purpose and recorded in `research.md`.

### Key Entities

- **Expected issuer** (per pending flow, in memory): issuer, source, the `iss`-supported flag, and the snapshot it came from.
- **OAuth token record** (`storage.OAuthTokenRecord`): gains `issuer`, `discovery_target`, `issuer_source` and `mix_up_protected`. They are empty on legacy rows and sit inside the sealed body.
- **Issuer-changed state** (per server): derived from the row and the current validated snapshot. Not stored.
- **Binding**: `{issuer, target, source}` stored in the row (`discovery_target` is the new field).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the fake-AS matrix (callback surface × issuer source × advertised flag × `iss` mode × opt-out), every case that must be refused produces zero token requests and the specified page and health result.
- **SC-002**: For rows and configured secrets bound to an issuer, AS-B receives zero requests that carry A's refresh token, A's DCR client id or a secret bound to A. The only exception is an operator-configured public client id sent on an explicit login, across proactive refresh, reactive refresh, stored-credential refresh, a warm-handler swap and re-login, including when the token endpoint answers with a cross-origin redirect.
- **SC-003**: Upgrading a `config.db` with N signed-in servers whose discovery stays valid under this spec (plaintext and sealed, discovered, configured, fallback and synthetic) forces zero re-logins, except for FR-011a rows at the seal limit. After one successful connect each, all other rows carry a binding. A server whose sign-in relied on origin-level fallback for a path issuer (now refused by FR-004a) fails discovery with `MCPX_OAUTH_DISCOVERY_FAILED`. Its row is neither bound nor used to send credentials, and this break is documented.
- **SC-004**: Every provider in the real-provider check list whose metadata and `iss` are consistent, and whose metadata is served without a cross-origin redirect, still signs in with default settings. A provider whose only fault is a metadata issuer-equality or callback `iss` mismatch fails in strict mode with the expected diagnostic, signs in with the per-server opt-out, and is named in the docs with the reason. A provider whose metadata needs a cross-origin redirect fails in both modes (FR-006a), and that too is documented.
- **SC-005**: A user who hits any of the three new codes is told what happened and what to do, without reading logs.

## Assumptions

These are decisions made on the maintainer's behalf (zero-interruption policy). Each is recorded so that review can overturn it.

1. **Strict by default**, with a per-server opt-out only (FR-014).
2. **Trust on first use for legacy rows** (FR-011). Forcing every server to sign in again would close only the window described under "Protection model", at the cost of one sign-in per server for every user. mcp-go makes the same choice. The protection claims (SC-002) are stated for bound rows only.
3. **mcpproxy owns the `iss` decision** in its callback handler. mcp-go cannot tell a synthetic document from a discovered one, and the broker does not use mcp-go. Where the two agree, mcp-go's `ProcessAuthorizationCallback` also runs, as a second check.
4. **`application_type` follows OIDC DCR**: `native` for loopback, `web` otherwise.
5. **No new activity-log type and no telemetry change.** Failures go through the existing OAuth failure record, health, the Spec 124 `upstream_event` rows, and logs.
6. **Trailing slash** is tolerated once for metadata equality and never for `iss`.
7. **Broker**: the `issuer` field is optional, and an absent `iss` is accepted there (US7).
8. **No new dependencies.**
9. **Path-issuer fallback is a deliberate break.** A sign-in that worked only by sending a tenant's credentials to origin-level default endpoints stops working, and the opt-out does not restore it. This is the one case where this spec accepts a forced re-configuration. The mcp-go bump changes the version of an existing dependency.

## Out of scope / follow-ups

- Spec 132: make the authorization-server role emit `iss` and advertise `authorization_response_iss_parameter_supported` (the issue assumed it already does).
- OIDC `id_token`, DPoP, PAR, signed metadata.
- Retrying DCR without `application_type` (only if real reports show up).
- A global strictness switch; forced re-binding of legacy rows.
