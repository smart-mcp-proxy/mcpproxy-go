# Feature Specification: Harden the OAuth Client Role

**Feature Branch**: `135-oauth-client-hardening`
**Created**: 2026-11-25
**Status**: Draft
**Input**: GitHub issue [#2044](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/2044) (felipecrs): the client role (mcpproxy signing in to upstream authorization servers) accepts responses more loosely than its own authorization-server role sends them. The issue lists three gaps (no RFC 9207 `iss` validation; client credentials not bound to the issuer; DCR omits `application_type`) and proposes upgrading `github.com/mark3labs/mcp-go` from v1.0.0 to at least v1.4.0, then checking that each fix actually takes effect in mcpproxy.

**Related**: Spec 022 (redirect-URI port persistence and DCR credential hygiene), Spec 113 (OAuth endpoint overrides, synthetic metadata served by the transport wrapper), Spec 116 (incarnation- and epoch-bound OAuth writes), Spec 123 (`enc:v1` token encryption), Spec 129 / `internal/serveredition/broker` (server-edition per-user connect flow, which is also a client role), Spec 132 / #1971 (mcpproxy's own authorization-server role: **out of scope** except for shared code, see Scope).

All code references are to `origin/main` at `661ad51d7`.

## Context: the three claimed gaps, checked against `main`

All three gaps are real. Two need more than the dependency bump, because mcpproxy does not use the code paths in mcp-go where the upstream fixes live.

### Gap 1: no RFC 9207 `iss` check on the authorization response. CONFIRMED

- The loopback callback server copies the query into `map[string]string`, keeping only the **first** value of each parameter (`internal/oauth/config.go:2014-2019`). It logs and routes `code`, `state`, `error` and `error_description`. `iss` is never read.
- The public remote callback (`internal/oauth/remote_callback.go:68-72`) rejects duplicate `state`, `code` and `error`, but does not look at `iss`. A repeated `iss` gets through.
- All three login paths (`internal/upstream/core/connection_oauth.go:1367`, `:1636`, `:2435`) pass `code` and `state` alone to one place, `exchangeAuthorizationCode` (`:1678`), which calls `OAuthHandler.ProcessAuthorizationResponse(ctx, code, state, codeVerifier)` (`:1688`). The callback's `iss` cannot reach the library.
- mcp-go v1.0.0 has no `iss` handling. In v1.4.0, `ProcessAuthorizationResponse` keeps its signature and still does not check `iss`. The check lives only in the new `ProcessAuthorizationCallback(ctx, url.Values, verifier)`. **So upgrading the module alone does not close this gap.**
- The server-edition broker callback (`internal/serveredition/api/credential_handlers.go:307-323`, `broker/oauth_connector.go:224`) reads `state`, `code` and `error` with `q.Get` and never reads `iss`.
- The authorization-server role already sends `iss` (`internal/mcplogin/redirect.go:135-140`), as the issue says.

### Gap 2: stored credentials are not bound to the issuer. CONFIRMED, plus one sub-gap the issue does not name

- A stored row is keyed by `GenerateServerKey(name, url)` = `name_<sha256(name|url)[:16]>` (`internal/oauth/persistent_token_store.go:96-105`). The row (`storage.OAuthTokenRecord`, `internal/storage/models.go:628-657`) holds the access token, refresh token, DCR `client_id`/`client_secret`, callback port, redirect URI, `OAuthEpoch` and `Revision`. **It has no issuer.**
- Spec 116's `CredentialBinding` is `{Incarnation, OAuthEpoch}` (`internal/commitview/commitview.go:47-50`). The epoch moves only when a config commit clears the server's OAuth state (`internal/runtime/configsvc/generations.go:198-221`). If the upstream's Protected Resource Metadata starts naming a different authorization server, or the metadata document changes, no config commit happens, so the binding does not move.
- On the next login the stored DCR `client_id`/`client_secret` are reused against whatever server discovery finds now (`internal/oauth/config.go:1189-1196`, `:1505-1514`).
- Refresh with stored credentials, `refreshWithStoredCredentials` (`internal/upstream/core/oauth_refresh.go:124-139`), takes a fresh `TokenEndpoint` from the handler's metadata and posts the stored **refresh token and client secret** to it. This path belongs to mcpproxy, not mcp-go. **mcp-go's v1.3.0 binding therefore does not cover it.**
- Sub-gap: RFC 8414 §3.3 says the `issuer` in a metadata document must equal the issuer URL the document was fetched for. mcp-go v1.0.0 does not check this; it only checks that the URLs in the document are well formed. mcp-go v1.2.0+ does check it, but only when it fetches metadata from the PRM `authorization_servers[0]` URL itself. mcpproxy always hands mcp-go an explicit `AuthServerMetadataURL` (`internal/oauth/config.go:1385-1421`, `:1545`), found by its own preflight (`preflightMetadataURL`, `config.go:2353-2402`), and mcp-go skips the check on that path. **mcpproxy's preflight must do the check itself.**

### Gap 3: DCR omits `application_type`. CONFIRMED, but the correct value is not the one the issue proposes

- mcp-go v1.0.0 `RegisterClient` (`client/transport/oauth.go:987-1002` in the module) sends `client_name`, `redirect_uris`, `token_endpoint_auth_method`, `grant_types`, `response_types`, `scope`, and optionally `client_uri` and `resource`. It sends no `application_type`. mcpproxy calls it from `internal/upstream/core/connection_oauth.go:1161`, `:1506`, `:2207` and `oauth_stale_dcr_client.go:133`.
- `application_type` is defined by OpenID Connect Dynamic Client Registration 1.0 §2, not RFC 7591. When it is omitted it defaults to **`web`**, not `native`. Under the OIDC rules a `web` client must not use `http` or `localhost` redirect URIs, and a `native` client is the one allowed a loopback `http` redirect. mcpproxy's normal redirect is `http://127.0.0.1:<port>/oauth/callback`, so leaving the field out tells strict OIDC providers to apply the web rules to a loopback redirect. The fix is to send **`native` for a loopback redirect** and `web` only for a non-loopback redirect such as the remote callback (`/oauth/callback/<server>` on a public host). Sending `"web"` for the loopback flow, as the issue proposes, would make things worse. mcp-go v1.4.0 derives the value from the redirect URI (`applicationType(redirectURI)`). The plan checks that its mapping matches this rule.

## Scope

**In scope**: mcpproxy as an OAuth **client** of upstream authorization servers, in both editions. That covers the personal and server-edition upstream login (`internal/oauth`, `internal/upstream/core`), the refresh paths (mcp-go's and mcpproxy's own), the stored rows in `oauth_tokens`, DCR, discovery preflight, health, doctor and docs. It also covers, with lower priority, the server-edition broker connect flow (`internal/serveredition/broker`).

**Out of scope**: mcpproxy's authorization-server role (Spec 132: `internal/server/oauth_login_*.go`, `internal/mcplogin`). That role already sends `iss`. Its only overlap with this spec is the parameter-ordering helper in `mcplogin/redirect.go`, which this spec does not change. Also out of scope: OIDC `id_token` validation (the client role requests no ID token), DPoP, PAR, JWT access-token validation, and key rotation for Spec 123.

## User Scenarios & Testing *(mandatory)*

### User Story 1: A sign-in whose response came from the wrong authorization server is refused (Priority: P1)

A user signs in to an upstream through mcpproxy. The authorization response that arrives at the callback carries an `iss` that does not match the issuer mcpproxy authorized against, or carries no `iss` although that issuer advertises `authorization_response_iss_parameter_supported: true`. mcpproxy does not exchange the code. The browser shows a clear failure page, the server's status shows why, and nothing is stored.

**Why this priority**: this is the RFC 9207 mix-up defence the issue asks for first. It is the only gap where an attacker-controlled authorization server can get an honest server's authorization code exchanged.

**Independent Test**: a fake authorization server with switches for "advertise the flag", "send iss", "send a wrong iss" and "send iss twice". Run a full login against it through the real callback server and assert, for each switch setting, whether a token request reached the token endpoint, what the callback page says, and what code the server's status shows.

**Acceptance Scenarios**:

1. **Given** the issuer's metadata advertises `authorization_response_iss_parameter_supported: true`, **When** the callback carries `iss` equal to that issuer, **Then** the code is exchanged and the login completes as it does today.
2. **Given** the same metadata, **When** the callback carries no `iss`, **Then** no token request is sent, the page says the response could not be confirmed as coming from the expected authorization server, and the server reports `MCPX_OAUTH_ISSUER_MISMATCH`.
3. **Given** any metadata, flag set or not, **When** the callback carries an `iss` that differs from the expected issuer, **Then** the same refusal happens. A wrong `iss` is never ignored.
4. **Given** metadata that does not advertise the flag, **When** the callback carries no `iss`, **Then** the login proceeds as today. This keeps providers that do not implement RFC 9207 working.
5. **Given** any metadata, **When** the callback repeats `iss`, `code`, `state` or `error`, **Then** the callback is rejected before any exchange, on the loopback and the remote callback alike.
6. **Given** an error response (`error=access_denied`) with a mismatching `iss`, **When** it arrives, **Then** it is reported as an issuer mismatch, not as the provider's error. A response that did not come from the expected server cannot speak for it.
7. **Given** a refused callback, **When** the user starts the login again against the correct server, **Then** it succeeds with no manual cleanup.

---

### User Story 2: Stored credentials are never sent to a different authorization server (Priority: P1)

An upstream that was signed in through authorization server A later starts naming authorization server B. This can happen because its Protected Resource Metadata changed, because its metadata document now declares a different issuer, or because of a DNS or hosting change. mcpproxy notices that the stored client registration and tokens were issued by A. It sends none of them (no refresh token, no client secret, no reused DCR `client_id`) to B. The server asks for a fresh sign-in and gives the reason.

**Why this priority**: a refresh token and a client secret are long-lived credentials. Today, a metadata swap sends both to the new server on the next refresh, with no user action.

**Independent Test**: log in against fake AS-A, then switch the fake resource's PRM to name fake AS-B. Trigger a proactive refresh, a reactive refresh after a 401, and a re-login. Assert that AS-B's token and registration endpoints never receive A's refresh token, client id or secret. Assert that the server shows `MCPX_OAUTH_ISSUER_CHANGED` with a login action, and that after an explicit login the row is bound to B.

**Acceptance Scenarios**:

1. **Given** a row bound to issuer A, **When** discovery now resolves issuer B, **Then** every refresh path refuses before sending a request, the access token in the row is no longer used for upstream calls, and the health action is "login" with code `MCPX_OAUTH_ISSUER_CHANGED`. The message names both issuers by origin.
2. **Given** the same state, **When** the user signs in explicitly, **Then** mcpproxy registers a new client at B (DCR), or uses the static client id from config, and writes the new token and registration bound to B. A's registration is replaced in the same write.
3. **Given** a row bound to issuer A, **When** discovery resolves A again (same issuer string), **Then** nothing changes: refresh and reuse work as today.
4. **Given** a static, operator-configured `client_id`, **When** the issuer changes, **Then** the refresh token and access token are still refused (they belong to A), but the configured client id is not cleared. It belongs to the operator.
5. **Given** a row the core cannot decrypt (Spec 123), **When** it is read, **Then** it is reported as undecryptable, as today. The issuer check is never reached, because an undecryptable row cannot be compared.

---

### User Story 3: Upgrading keeps every server signed in (Priority: P1)

A user with several OAuth servers signed in upgrades mcpproxy. None of them is asked to sign in again. Each existing row becomes bound to its issuer the first time it is used after the upgrade, and from then on the US2 rule applies.

**Why this priority**: a forced re-login across every server after an upgrade would be a regression users notice immediately. The issue asks for "migrate existing records on read".

**Independent Test**: seed `config.db` with pre-upgrade rows (no issuer field), both plaintext and `enc:v1`-sealed. Start the new core against fake servers, and assert that the servers stay connected, that the next write of each row records the issuer, and that a sealed row stays sealed.

**Acceptance Scenarios**:

1. **Given** a pre-upgrade row with no issuer, **When** it is first used (connect, refresh or login reuse) and discovery resolves an issuer through a verified path (FR-006), **Then** the row is used as today, and mcpproxy writes that issuer into the row in the same write that the use already makes, or in a dedicated record-only write if the use writes nothing. This is trust on first use.
2. **Given** a pre-upgrade row, **When** discovery for that use fails, **Then** nothing is recorded and the use behaves as it does today when discovery fails. The row stays unbound until a later successful discovery.
3. **Given** a sealed (`enc:v1`) pre-upgrade row, **When** the issuer is recorded, **Then** the row is re-sealed with the same key and the same additional data (key id, bucket, bucket key). The issuer sits inside the sealed body. Nothing is written in plaintext.
4. **Given** a row bound by the new version, **When** an older mcpproxy runs against the same `config.db`, **Then** it reads and uses the row (the new field is ignored), and if it rewrites the row the issuer is lost. On the next upgrade the row goes back through US3.1. This downgrade loss is accepted and documented.

---

### User Story 4: Discovery refuses a metadata document that names another issuer (Priority: P2)

An upstream's PRM names authorization server `https://auth.example.com`, but the metadata document fetched from that server's well-known URL declares `"issuer": "https://evil.example"`. mcpproxy stops before any authorization redirect, registration or token request, and the server status says the metadata is inconsistent.

**Why this priority**: issuer equality makes the expected issuer used by US1 and US2 trustworthy. Without it, an attacker who controls the metadata document could choose the issuer that `iss` is compared against.

**Independent Test**: a fake PRM and AS pair whose `issuer` field can be set independently of the URL it is served at. Cover the PRM path, the fallback path (no PRM: metadata on the MCP origin), and the `openid-configuration` variants.

**Acceptance Scenarios**:

1. **Given** PRM names issuer URL `X`, **When** the document at `X`'s well-known URL declares an issuer equal to `X` (with one trailing `/` ignored on either side), **Then** discovery succeeds as today.
2. **Given** a document whose issuer differs, **When** discovery runs, **Then** it fails with `MCPX_OAUTH_METADATA_ISSUER_MISMATCH`. The health action is "configure", no candidate URL after it is tried, and no login is offered until discovery succeeds. The issuer-equality failure is not cached for longer than the existing discovery failure TTL.
3. **Given** an operator-configured `oauth.auth_server_metadata_url` (Spec 113), **When** the document is fetched, **Then** its issuer is taken as declared (the operator chose the document) and recorded with source `configured`. Equality against a derived URL is not checked.
4. **Given** endpoint overrides that make the transport wrapper serve a synthetic document (Spec 113), **When** a login runs, **Then** the issuer is recorded with source `synthetic`, and US1's strict rules apply only to an `iss` that is present and differs from every issuer mcpproxy can vouch for. See FR-004.

---

### User Story 5: DCR declares the right application type (Priority: P2)

When mcpproxy registers itself with an authorization server, the request says `"application_type": "native"` for a loopback redirect URI and `"web"` for a non-loopback one. Strict OIDC providers then apply the redirect rules that fit mcpproxy's actual redirect.

**Why this priority**: it fixes registrations that strict providers reject today. It does not by itself protect against an attack.

**Independent Test**: a fake registration endpoint records the request body. Run DCR for a loopback redirect, an IPv6 loopback redirect and the remote callback URL, and assert the value each time.

**Acceptance Scenarios**:

1. **Given** a redirect `http://127.0.0.1:<port>/…`, `http://[::1]:<port>/…` or `http://localhost:<port>/…`, **When** DCR runs, **Then** the body has `application_type: "native"`.
2. **Given** the remote callback (a non-loopback host), **When** DCR runs, **Then** the body has `application_type: "web"`.
3. **Given** a provider that rejects the field, **When** it answers `invalid_client_metadata` naming `application_type`, **Then** mcpproxy does **not** retry without it. The existing DCR error surfaces as today, and the docs name the opt-out (FR-012) as the remedy.

---

### User Story 6: An operator can relax issuer checks for one misbehaving provider, and doctor says so (Priority: P2)

A provider publishes metadata whose `issuer` differs from its well-known URL, or sends a mangled `iss`, so a correct sign-in is refused. The operator sets `oauth.skip_issuer_validation: true` on that one server. The login works. `mcpproxy doctor` and the server's detail view warn that issuer checks are off for it.

**Why this priority**: without an escape hatch, one non-compliant provider would block a user completely. Keeping it per server and visible prevents it from becoming a silent global downgrade.

**Independent Test**: point a fake AS that misreports `iss` at two servers, one with the switch and one without. Only the switched server signs in. Doctor lists exactly that server.

**Acceptance Scenarios**:

1. **Given** the switch on, **When** `iss` mismatches or the metadata issuer differs, **Then** the login proceeds, and a WARN log line names the server, the expected issuer and the received issuer.
2. **Given** the switch on, **When** a stored row is bound to issuer A and discovery resolves B, **Then** US2 still applies. The switch does not let credentials cross issuers.
3. **Given** the switch on, **Then** `mcpproxy doctor`, `GET /api/v1/diagnostics` and the server's OAuth detail show a warning `issuer_validation_disabled` for that server.

---

### User Story 7: The server-edition connect flow checks `iss` when the operator names the issuer (Priority: P3)

In the server edition, a brokered upstream is configured with static endpoints and no discovery (`ConnectorConfig`, `broker/oauth_connector.go`). The operator can now also configure `issuer`. When it is set, the connect callback applies the US1 rules, and a mismatching `iss` is refused as `connect_failed`. When it is not set, behaviour is unchanged.

**Why this priority**: the endpoints there are pinned by the operator, so the mix-up surface is small, and the code is separate.

**Independent Test**: a broker unit test with the issuer set and unset, and with `iss` correct, wrong, missing and repeated.

### Edge Cases

- **Trailing slash and case**: `iss` and the stored issuer are compared as exact strings (RFC 9207 §2.4, simple string comparison). Metadata issuer equality (US4) ignores one trailing `/`, as mcp-go does, because providers differ on it. Scheme and host case are not normalised.
- **Several authorization servers in PRM**: only the entry discovery actually uses (the first, as today) counts as the expected issuer.
- **Discovery cache**: the expected issuer for a login is the one recorded with the authorization request (FR-002), not a later cache read. If the cache is refreshed while the browser is open, the login still compares against the issuer it started with.
- **Concurrent logins** (Spec 116 / `WithLoginClient`): the issuer is written in the same transaction as the token and client, so a row cannot be half-bound.
- **Purge and server delete**: the issuer lives in the row, so the existing purge set covers it.
- **Proactive refresh racing an issuer change**: the refresh manager reads the binding and the issuer together. A refresh that started before the change is checked against the issuer of the metadata it is about to use, never a stale one.
- **Non-HTTPS issuer**: unchanged. mcpproxy accepts what discovery accepts today (loopback test servers use `http`).
- **Undecryptable row**: Spec 123 precedence applies. Undecryptable is reported first, and the issuer is unknown.

## Requirements *(mandatory)*

### Functional Requirements

**Authorization response (`iss`, RFC 9207)**

- **FR-001**: Every callback surface of the client role (loopback callback server, remote callback, server-edition broker callback when an issuer is configured) MUST deliver the full multi-valued query to the flow and MUST reject, before any exchange, a response that repeats `code`, `state`, `iss` or `error`.
- **FR-002**: When mcpproxy builds an authorization request, it MUST record with the pending flow the expected issuer and its source (`discovered`, `configured` or `synthetic`) together with the value of `authorization_response_iss_parameter_supported` from the same metadata snapshot.
- **FR-003**: For an expected issuer of source `discovered` or `configured`, the flow MUST refuse to exchange the code when (a) `iss` is present and not exactly equal to the expected issuer, or (b) `iss` is absent and the snapshot advertised the parameter as supported. It MUST accept an absent `iss` when the snapshot did not advertise support. An empty `iss=` counts as absent.
- **FR-004**: For source `synthetic` (Spec 113 endpoint overrides, or mcp-go's fallback endpoints derived from the origin), mcpproxy cannot vouch for the issuer string. The flow MUST accept an absent `iss`. When `iss` is present it MUST be recorded as the issuer of the resulting credentials, with a WARN line if it differs from the synthetic issuer. It MUST NOT refuse on that difference alone.
- **FR-005**: An error response MUST pass the FR-003 check before its `error` is reported. A failing check is reported as the issuer mismatch.

**Expected issuer and discovery (RFC 8414 §3.3)**

- **FR-006**: mcpproxy's discovery preflight MUST reject a metadata document whose `issuer` is not equal (one trailing `/` ignored) to the issuer URL the document was fetched for: the PRM `authorization_servers` entry, or for the fallback path the issuer implied by the well-known URL that answered. It MUST stop trying further candidate URLs on that mismatch, and MUST NOT seed the document cache with the rejected document.
- **FR-007**: A configured `oauth.auth_server_metadata_url` MUST skip FR-006, and its declared issuer MUST be used with source `configured`. A document missing `issuer` MUST fail discovery, as RFC 8414 requires the field.

**Issuer-bound credentials**

- **FR-008**: Every `oauth_tokens` row write that stores a token or a DCR registration MUST record the issuer that issued it and the issuer source, inside the record body (sealed with the rest under Spec 123, so the additional data does not change).
- **FR-009**: Before sending a stored refresh token, client secret or DCR `client_id` to an authorization server, every path (mcp-go refresh through the token store, `refreshWithStoredCredentials`, the refresh manager, login reuse of a DCR registration, the Spec 022 hygiene read) MUST compare the row's issuer with the issuer of the metadata it is about to use. On inequality it MUST NOT send the request, MUST stop using the row's access token for upstream calls, and MUST report `MCPX_OAUTH_ISSUER_CHANGED`.
- **FR-010**: A row with no issuer (written before this feature, or by an older binary after a downgrade) MUST be used as today and bound on first use to the issuer of a successful discovery whose source is `discovered` or `configured` (trust on first use). A row with source `synthetic` MUST be compared only against another synthetic issuer derived from the same overrides.
- **FR-011**: An explicit login after `MCPX_OAUTH_ISSUER_CHANGED` MUST NOT reuse the old DCR registration. It MUST register anew (or use the configured static client id) and replace the row in one write. A configured static `client_id` MUST never be cleared by this feature.

**Strictness and opt-out**

- **FR-012**: A per-server `oauth.skip_issuer_validation` (default `false`) MUST disable FR-003, FR-005 and FR-006 for that server only, with a WARN line on each skipped check. It MUST NOT disable FR-001 (repeated parameters), FR-008 or FR-009. There MUST be no global switch.
- **FR-013**: The field MUST be wired like every other OAuth field: config load and validation, hot reload, OAS/`swagger.yaml`, Web UI server editor, macOS server editor, the redaction field decisions (not a secret). A change to it MUST NOT clear stored OAuth state or move the OAuth epoch.

**DCR**

- **FR-014**: DCR MUST send `application_type: "native"` when the registered redirect URI's host is a loopback address or `localhost`, and `"web"` otherwise. No retry without the field.

**Surfaces**

- **FR-015**: Three diagnostics codes, each with a `docs/errors/<CODE>.md` page: `MCPX_OAUTH_ISSUER_MISMATCH` (US1: response refused, recorded as an OAuth failure, health action "login"), `MCPX_OAUTH_ISSUER_CHANGED` (US2: stored credentials belong to another issuer, health action "login"), `MCPX_OAUTH_METADATA_ISSUER_MISMATCH` (US4: health action "configure").
- **FR-016**: The callback failure page for FR-003/FR-005 MUST say that the response could not be confirmed as coming from the expected authorization server, that nothing was signed in, and how to retry. It MUST NOT echo the received `iss` value (attacker-controlled). Logs MAY carry the received value through the existing upstream-text scrubber.
- **FR-017**: `mcpproxy doctor` and `GET /api/v1/diagnostics` MUST gain, under the OAuth section: servers with `MCPX_OAUTH_ISSUER_CHANGED`, servers with `skip_issuer_validation` on (warning `issuer_validation_disabled`), and a count of rows not yet bound to an issuer. Scoped callers see only servers in reach, as with Spec 123's report.
- **FR-018**: `mcpproxy auth status` and the server's OAuth detail (REST, Web UI, tray) MUST show the bound issuer origin and its source.

**Dependency**

- **FR-019**: `github.com/mark3labs/mcp-go` MUST be at least v1.4.0. The upgrade MUST keep every existing OAuth test green, with any behaviour change from the upgrade either adopted on purpose or neutralised and recorded in `research.md`.

### Key Entities

- **Expected issuer (per pending flow)**: issuer string, source (`discovered` | `configured` | `synthetic`), `iss`-supported flag, metadata snapshot time. It lives in memory with the flow's state and verifier and is never persisted.
- **OAuth token record** (`storage.OAuthTokenRecord`): gains `issuer` and `issuer_source`. They are empty on legacy rows. They sit inside the JSON body, so they are sealed under Spec 123 when a key is set.
- **Issuer-changed state (per server)**: derived at use time from the row and current discovery, and shown through health and doctor. Not stored separately.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the fake-AS suite, 100% of responses with a wrong `iss`, a missing-but-required `iss` or a repeated parameter produce zero token requests, on every callback surface.
- **SC-002**: In the AS-swap scenario, AS-B receives zero requests carrying A's refresh token, client id or client secret, across proactive refresh, reactive refresh, `refreshWithStoredCredentials` and re-login.
- **SC-003**: Upgrading a `config.db` with N signed-in servers (plaintext and sealed) causes zero forced re-logins. After one successful connect each, N of N rows carry an issuer.
- **SC-004**: Every provider in the existing real-provider check list that signs in today still signs in with default settings. Any provider that needs `skip_issuer_validation` is named in the docs with the reason.
- **SC-005**: A user who hits any of the three new codes sees a page, status line or doctor entry that says what happened and what to do, without reading logs.

## Assumptions

Decisions made on the maintainer's behalf (the zero-interruption policy applies). Each one is recorded so that review can overturn it.

1. **Strict by default.** The checks are on for every server. The only opt-out is per server (FR-012), because a global switch would quietly undo the protection for everyone.
2. **Trust on first use for legacy rows** (FR-010). Forcing every server to sign in again was rejected as unavoidable only if no issuer could be known, which is not the case here. Residual risk: a swap that happened *before* the upgrade is bound in, not caught. It is documented.
3. **mcpproxy owns the `iss` check** at its callback seam, rather than relying only on mcp-go's `ProcessAuthorizationCallback`. mcp-go cannot tell a synthetic document from a discovered one, and the broker does not use mcp-go. Where the semantics agree, the plan also passes the query to `ProcessAuthorizationCallback` as a second check.
4. **`application_type` follows OIDC DCR**: `native` for loopback, `web` otherwise. This differs from the issue's `web` proposal, for the reason given in Gap 3.
5. **No new activity-log type and no telemetry change.** Failures go through the existing OAuth failure record, health, the Spec 124 `upstream_event` rows that carry `MCPX_*` codes, and logs.
6. **Trailing slash**: tolerated once for metadata equality (matching mcp-go and real providers), not for `iss` (RFC 9207 is explicit).
7. **The server-edition broker** gets an optional `issuer` field. It has no discovery to derive one from, and making the field required would break existing configs.
8. **No new dependencies.** The mcp-go bump is a version change of an existing dependency.

## Mix-up attack coverage

The attack: a user has servers backed by an honest AS-H and an attacker-controlled AS-A. The attacker gets an authorization response produced for one server delivered to the flow of another, for example by making A's metadata point its authorization endpoint at H. The flow then exchanges H's code at A's token endpoint, or uses A's code under H's identity.

What already holds on `main`: each flow's `state` routes the callback only to the flow that minted it (#975), and PKCE binds the code to that flow's verifier. Together these stop a callback from being replayed into an unrelated flow. What they do not stop is a response that legitimately answers a flow's `state` but was produced by a different issuer than the one the flow trusts. RFC 9207 closes that hole (FR-003). FR-006 keeps the attacker from choosing the trusted issuer. FR-009 closes the related replay that needs no user at all: refreshing at a swapped AS.

## Out of scope / follow-ups

- Spec 132 authorization-server role (already sends `iss`).
- OIDC `id_token`, DPoP, PAR, signed metadata.
- Retrying DCR without `application_type` for providers that reject it (only if real reports show up).
- A global strictness switch.
