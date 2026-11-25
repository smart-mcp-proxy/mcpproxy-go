# Implementation Plan: Harden the OAuth Client Role

**Branch**: `135-oauth-client-hardening` | **Date**: 2026-11-25 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/135-oauth-client-hardening/spec.md`

## Summary

Bump `github.com/mark3labs/mcp-go` from v1.0.0 to the latest v1.x (at least v1.4.0, target v1.6.x), then wire the three fixes through mcpproxy's own seams. The mcp-go fixes alone do not engage there, because mcpproxy:

- feeds mcp-go an explicit metadata URL,
- exchanges codes through `ProcessAuthorizationResponse`, which does not check `iss`,
- writes the callback page before the exchange, and
- refreshes stored credentials with its own HTTP request, which follows redirects.

The work comes in five parts:

- one strict callback parse and an `iss` decision made in the callback handler against the issuer snapshot of the flow that the `state` selects;
- issuer equality on every metadata document mcpproxy or mcp-go acts on, validated per consumer;
- `issuer` / `issuer_source` / `mix_up_protected` inside the sealed `OAuthTokenRecord` body, checked from one validated snapshot before every credential send, with trust-on-first-use (TOFU) binding of rows written before this change, and one issuer per row;
- no cross-origin redirects for credential-bearing requests;
- the right `application_type` sent at registration.

Delivered as three PRs (see "Delivery").

## Technical Context

**Language/Version**: Go 1.26 (backend), TypeScript 5.9 / Vue 3.5 (one Web UI field and a status line), Swift 5.9 (one macOS editor field and a status line)
**Primary Dependencies**: `github.com/mark3labs/mcp-go` (bump only), `go.etcd.io/bbolt`, `go.uber.org/zap`. No new dependencies.
**Storage**: BBolt `oauth_tokens` bucket. The new fields are additive JSON inside the record body. The bucket layout, the bucket key and the Spec 123 additional data (kid ‖ bucket ‖ bucket key) stay the same.
**Testing**: `go test -race ./internal/oauth/... ./internal/upstream/... ./internal/storage/...`; a new fake authorization server under `internal/oauth/oauthtest/`; `-tags server` for the broker; `./scripts/test-api-e2e.sh`; `scripts/test-oauth-token-encryption-e2e.sh` (sealed rows must survive TOFU binding); a new `scripts/test-oauth-client-hardening-e2e.sh`.
**Target Platform**: macOS, Linux, Windows (personal); Docker (server edition)
**Project Type**: single Go module with an embedded Web UI and a native tray
**Performance Goals**: no added network round trips. The issuer comes from metadata that is already fetched, and the comparison is a string compare.
**Constraints**: no forced re-login on upgrade. No plaintext write of a sealed row. One transaction per login write (`WithLoginClient`).
**Scale/Scope**: about 15 files in `internal/oauth`, `internal/upstream/core`, `internal/storage`, `internal/diagnostics`, `internal/management` (doctor), `internal/config`, `internal/serveredition/broker`; 3 error pages; docs.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | PASS | No new I/O. The issuer check reads metadata that is already loaded. |
| II. Actor-Based Concurrency | PASS | The issuer travels with the existing pending-flow waiter (`CallbackServer.deliver`). Rows are written inside existing transactions. No new locks. |
| III. Configuration-Driven | PASS | One per-server field, `oauth.skip_issuer_validation`, wired through every config surface. No tray-only state. |
| IV. Security by Default | PASS | Strict by default. The opt-out is per server, visible in doctor, and does not cover credential binding. |
| V. TDD | PASS | Every task starts with a failing test against the fake AS (see Test plan). |
| VI. Documentation Hygiene | PASS | `docs/features/oauth-authentication.md` section, 3 `docs/errors/*.md` pages, configuration reference, CLAUDE.md "Recent Changes" at merge. |
| Core + Tray split | PASS | The tray only displays the issuer and the warning from REST. |
| 3-layer upstream | PASS | Changes sit in `core` (exchange and refresh). `managed` and `cli` are untouched. |

Post-design re-check: PASS. Nothing in Phase 1 adds a dependency, a lock or tray state.

## Design

### D1. One strict callback parse (FR-001)

- `oauth.ParseCallbackQuery(rawQuery string) (url.Values, error)` parses the query once and rejects:
  - a repeated `code`, `state`, `iss` or `error`;
  - `code` and `error` together;
  - neither `code` nor `error`;
  - a missing or empty `state`.
- It runs first in `handleCallbackWithFailureNotification` (`internal/oauth/config.go:2005`), in the remote callback (it replaces the inline check at `remote_callback.go:68-72`), and in the broker callback (`api/credential_handlers.go:307`).
- A rejected request does not take a waiter. The remote callback keeps its current rule that "an invalid public request is not an OAuth failure".
- `CallbackServer.deliver(state, params map[string]string)` becomes `deliver(state, callbackResult{query url.Values, err error})`, and the waiter channel type changes to match. Logging keeps first values only, scrubbed, plus `iss_present`.
- The three consumers in `connection_oauth.go` (`:1348`, `:1621`, `:2409`) return `result.err` when it is set. Otherwise they read `code`/`state` from `result.query` and pass the query to `exchangeAuthorizationCode`.

### D2. Issuer snapshot and the `iss` decision (FR-002–FR-005)

- New file `internal/oauth/issuer.go`:
  - `IssuerSource` (`discovered` | `configured` | `fallback` | `synthetic`). `fallback` is mcp-go's default endpoints when no document and no override exist (FR-004a): `iss` rules as for `discovered` with `IssSupported=false`.
  - `ExpectedIssuer{Issuer, Source, IssSupported, Target}`. `Target` is the discovery target URL (FR-013).
  - `DecideAuthorizationResponseIssuer(q url.Values, exp ExpectedIssuer, skip bool) (protected bool, err error)`, which returns `*IssuerMismatchError` (`diagnostics.OAuthIssuerMismatch`).
  - Rules:
    - `len(q["iss"]) == 1` means present, even when the value is empty. An empty value is a mismatch, which is stricter than mcp-go (R6).
    - `discovered` and `configured` follow FR-003. `protected` is true only when `iss` was present and equal.
    - `synthetic` follows FR-004, and `protected` is false.
    - With `skip`, mismatches are logged at WARN and accepted.
- **Where the snapshot comes from.** When the authorization URL is built, the OAuth config builder already holds `asDoc` (`config.go:1385-1421`). It now also returns `ExpectedIssuer`:
  - `Issuer` is `asDoc.meta.Issuer`, which D3 has already validated.
  - `IssSupported` is the new `AuthorizationResponseIssParameterSupported` field on the local metadata struct (`discovery.go:27`).
  - `Source` is `configured` when `ov.metadataURL != ""`, `synthetic` when the wrapper served or will serve a synthetic document (`transport_wrapper.go:607-627`), and `discovered` otherwise.
  - If there is no document at all and no override, `Source` is `fallback` with the discovery-selected issuer (FR-004a). A document that fails validation is terminal (D3), never `fallback`.
- **The decision runs in the callback handler** (R3). The waiter for a `state` carries `{ch, exp ExpectedIssuer, skip bool}`. After D1, the handler:
  1. takes the waiter atomically, once;
  2. calls `DecideAuthorizationResponseIssuer`;
  3. on failure, delivers `callbackResult{err}` (no exchange), writes the FR-018 page and calls `RecordOAuthFailure(server, err)`;
  4. on success with `error` set, writes the provider-error page as today (FR-005 order);
  5. otherwise delivers the query and writes the success page.
- `exchangeAuthorizationCode(ctx, h, cb, q url.Values, exp, verifier)` repeats the decision as defence in depth. For `discovered`/`configured` without skip it calls `h.ProcessAuthorizationCallback(ctx, q, verifier)`, so that mcp-go's own RFC 9207 check also runs. Otherwise it calls `h.ProcessAuthorizationResponse`. It passes the issuer and `protected` to the login write (D4).

### D3. Discovery issuer equality on every consumed document (FR-006–FR-008)

- **Escaped paths.** Candidates are built from `u.EscapedPath()`, never `u.Path`. `validateIssuerURL` refuses a query, a fragment, `%2F`, `%2E`-encoded or literal dot segments. When PRM names no authorization server, the target is the candidate's issuer (the MCP URL's issuer form).
- **Candidates carry their issuer.** `BuildRFC8414MetadataURLs` becomes `metadataCandidates(issuerURL) []candidate{url, issuer}`. For a path issuer it builds only path-derived forms: RFC 8414 path insertion, and OIDC `…/{path}/.well-known/openid-configuration` plus insertion. For an origin issuer it builds the origin forms. The no-PRM fallback derives the issuer from the MCP origin.
- `findWorkingMetadataDoc` (`discovery.go:930-958`) validates `trimOneSlash(doc.Issuer) == trimOneSlash(candidate.issuer)` after it parses each document. A mismatch or a missing `issuer` returns `*MetadataIssuerMismatchError` (`diagnostics.OAuthMetadataIssuerMismatch`) at once. No further candidate is tried. `preflightMetadataURL` propagates it as terminal (no seed, failure TTL) and does not leave `authServerMetadataURL` empty for mcp-go to rediscover on its own.
- **Wrapper refill.** `roundTripMetadata` (`transport_wrapper.go:491-525`) validates a refilled document against the `ExpectedIssuer` recorded for the endpoint set: the candidate's issuer, or "as declared" for `configured`. A mismatch answers mcp-go with an error and never with a synthetic document.
- **Library discovery.** Because preflight now treats a mismatch as terminal and serves the URL in every other case, mcp-go never starts PRM discovery on its own while mcpproxy has a URL. For the remaining case (no PRM, no document, no overrides), the wrapper intercepts mcp-go's well-known GETs (they go through the same transport) and applies the same check to the issuer implied by each request URL. `SkipIssuerMetadataValidation` in the mcp-go `OAuthConfig` follows the server's switch.
- **Per-consumer validation (FR-007).** The document cache keeps raw bytes (`asMetadataDoc.raw`), as it does today, and validation runs on every use. The `metadataPreflight` result cached under `makeDiscoveryKey("metadata-url", …)` gains the policy (strict or skip) in its key, so a strict failure and a skip success never share an entry.
- **Configured URL (FR-008)**: no derivation compare. The declared issuer is used as is, and a missing one fails.
- **Origin of the document (FR-006a).** A new redirect policy `metadataCheckRedirect` allows only same-origin hops. Unlike `oauthCheckRedirect`, it also blocks cross-origin GETs. It is installed on `httpFetchRaw` (`discovery_cache.go:310-328`) and on the wrapper refill client (`transport_wrapper.go:496`). A blocked redirect is returned as `*MetadataIssuerMismatchError`, and `skip_issuer_validation` does not lift it (FR-014). `insecure_issuer` is computed when the snapshot is built: an `http` scheme on the issuer or any endpoint, with a non-loopback host (`config.IsLoopbackHost`). It is carried on `ExpectedIssuer.Insecure` and surfaced in D7.

### D4. Issuer-bound rows (FR-009–FR-013)

- **Record.** `storage.OAuthTokenRecord` gains `Issuer` and `IssuerSource` (`omitempty`). They live inside the body that `encodeOAuthRecord` seals (`oauth_binding.go:166`). The additional data does not change, and a row without the fields marshals to the same bytes as before.
- **Binding = `{Issuer, Target, Source}`** (FR-013). `ExpectedIssuer.Binding()` derives it from a snapshot: for `discovered` the validated issuer plus the PRM entry, for `configured` the declared issuer plus the metadata URL, for `synthetic` the synthetic issuer as both values. `Matches(stored, snap, skip)` always compares the target and the source class (`fallback` versus the rest), and compares the issuer only when skip is off. Toggling skip never changes the target, so continuity holds in both directions.
- **Handler-held credentials (FR-009).** Every place that puts `ClientID`/`ClientSecret` into an mcp-go `OAuthConfig` (`config.go:1495-1546`) also sets `CredentialsIssuer` from the credential's own binding: the row's for persisted DCR, the flow's `exp` for a fresh DCR, the issuer of the first validated snapshot for a static client. mcp-go v1.3+ then refuses any refresh whose metadata names another issuer (`ErrAuthorizationServerChanged`). On the mcpproxy side, `oauthRefreshOnce` takes the secret from the same credential snapshot whose binding it checked, never from a handler whose binding differs. That rules out the warm-handler case where A and B share a `client_id`.
- **Code exchange (FR-009).** The pending flow stores the `ExpectedIssuer` captured at authorization time. Before redemption, `exchangeAuthorizationCode` compares `h.GetServerMetadata()` (the handler's current metadata) with the capture: the same issuer and the same token endpoint. If they differ, it aborts with `ISSUER_CHANGED` and restarts the flow, and the code is never sent. mcp-go v1.0.0 caches metadata once per handler (`sync.Once`), and v1.2+ re-discovers after a failure, so this compare is what catches a rediscovery in between.
- **Snapshot-consistent sends (FR-009).** A new helper `oauth.SendSnapshot(handler) (ExpectedIssuer, *AuthServerMetadata, error)` returns one validated snapshot. Every credential send gets its issuer **and** its endpoint from that one value:
  - `refreshWithStoredCredentials` takes `metadata.TokenEndpoint` from the snapshot, then checks `CheckStoredIssuer(rec, snap)`, then sends.
  - `oauthRefreshOnce` / `oauthRefreshFunc` (`oauth_refresh.go:66-110`) check before calling the library refresh. They also set `OAuthConfig.CredentialsIssuer = rec.Issuer` when it is bound, so mcp-go's v1.3+ refresh refuses a different issuer by its own rule (`ErrAuthorizationServerChanged`). It is mapped to the same code.
  - `RefreshManager` attempts go through the same check.
  - Login reuse (`config.go:1189-1196`, `:1505-1514`): a stored `client_id` whose issuer differs from the flow's `ExpectedIssuer` is not used. This is the same branch as a Spec 022 stale registration.
  - Code exchange: the reused registration must match `exp`.
- **Snapshot lifetime (FR-009b).** `metadataPreflight` stores `expiresAt = min(own TTL, PRM entry expiry, document entry expiry)`, and the cache honours it, so a preflight built on an old PRM entry cannot outlive that entry.
- **Fallback (FR-004a).** Used only when the selected issuer has no path. `Binding.Matches` compares the source class too (`fallback` vs the rest).
- **Freshness (FR-009b).** `oauthRefreshOnce`, the refresh manager, login and the 401 handler call `preflightMetadataURL` (cache lookup, re-resolved after the TTL) and, when its validated document differs from `h.GetServerMetadata()`, rebuild the handler with the new snapshot before the FR-009 compare. New invalidations: `globalDiscoveryCache.invalidateServer(serverURL)` on `invalid_grant`/`invalid_client` refresh failures and at the start of an explicit login. Freshness is a UX bound, not part of the security invariant (spec "Protection model"). Detection is tied to discovery. Whenever mcpproxy runs discovery for a server (connect, 401 rediscovery, refresh-failure rediscovery, login), the new snapshot replaces the handler's metadata. If the snapshot names another issuer than the row, the server enters `ISSUER_CHANGED`. A warm handler whose cached metadata still names A sends to A, which is safe: A issued the credentials. `PersistentTokenStore.GetToken` returns `ErrOAuthAuthorizationRequired` wrapped with the code while the server is in `ISSUER_CHANGED`, so the access token stops being attached. The state clears once a discovery resolves the bound issuer again. The discovery cache's success TTL (`discovery_cache.go:21-28`, 1 h) bounds how late a swap at an unchanged PRM URL is noticed. The swap test injects the cache clock (`discoveryCache.now`, added for this) and advances it past the TTL. A second variant fires each invalidation event instead.
- **Static confidential client (FR-012).** A new bucket `oauth_static_client_bindings` (in the Spec 116 purge set for server delete only) maps the server key to `{fingerprint, issuer, target, source}`, where `fingerprint = sha256(client_id || 0x00 || client_secret)`. Client secrets are high-entropy, and the value is sealed under Spec 123 when a key is set. It is written in its own transaction before the first secret-bearing request. Logout and grant clears do not touch it. A config commit whose `client_id`/`client_secret` differ deletes it, as does server delete. On `ISSUER_CHANGED` with a configured secret, the login is refused before the authorize redirect with health action `configure`. Changing `oauth.client_id`/`client_secret` is already an OAuth clear (`config.OAuthConfigChanged`), so the epoch moves and the next use binds afresh.
- **Redirects (FR-010).** `refreshTokenWithStoredCredentials` (`internal/upstream/core/client.go:989-1010`) builds its client as `&http.Client{Transport: c.Gate().HTTPTransport(), CheckRedirect: oauth.CheckRedirect, Timeout: 30s}`, where `CheckRedirect` is the exported `oauthCheckRedirect`. An AST guard test asserts that every `http.Client` in `internal/oauth` and `internal/upstream/core` that sends a form containing `refresh_token`, `client_secret`, `code_verifier` or `code` has a `CheckRedirect` set. mcp-go's own token and registration requests go through the OAuth transport wrapper's client, which already uses `oauthCheckRedirect`. PR-1 confirms this path, and otherwise installs the policy on the `HTTPClient` passed through `OAuthConfig`.
- **Trust on first use (FR-011).** Every path that reads an unbound row calls `oauth.EnsureBound(rec, snap)` **before** it releases a credential to an AS. That call commits the binding through `storage.BindOAuthIssuer`. If the commit loses (the revision changed, or a binding appeared), it reloads the row and re-runs FR-009 against its own snapshot: it proceeds only on a match and otherwise aborts without sending. `storage.BindOAuthIssuer(serverKey, binding, observedRevision, b Binding) (bound bool, err error)`:
  - runs in one bbolt transaction;
  - re-reads the row and requires `Revision == observedRevision && Issuer == ""`;
  - requires that the row is not undecryptable;
  - re-seals with the configured key, or writes plain JSON without one, as today, and in the same transaction creates `oauth_static_client_bindings` for a configured confidential client when it is absent.

  Otherwise it writes nothing. It runs at most once per row, off the hot path, after the use that discovered the issuer.
- **One issuer per row (FR-012).** `UpdateOAuthClientCredentials*` (`oauth_binding.go:467-499`, the DCR persistence called before the browser opens, `connection_oauth.go:1177-1228`, `:2218-2244`) gains an `issuer` argument. When the row's `Issuer` is set and differs, the same transaction clears `AccessToken`, `RefreshToken`, `ExpiresAt` and `Scopes`. Those tokens were already refused under FR-009. The login write (`WithLoginClient`) always writes token, registration and issuer together. A failed callback after DCR leaves a registration-only row bound to the new issuer, which is reusable for the next login against that issuer.
- **Opt-out binding (FR-013).** The record always stores the issuer and the target. With skip on, only the target is compared, so a replacement server that declares the old issuer string is still caught by its different target.
- **Synthetic (FR-004).** The stored issuer is the synthetic issuer (the origin of the authorization endpoint). An override edit is an OAuth clear (`config.OAuthConfigChanged`, `internal/config/config.go:3422`): the epoch moves and the old row is never read under new overrides. So synthetic-to-synthetic comparisons only ever meet the same overrides. A switch from synthetic to discovered (overrides removed) is also an OAuth clear.

### D5. DCR `application_type` (FR-016)

- After the bump, a table test pins mcp-go's registration body: `native` for `127.0.0.1`, `[::1]` and `localhost`, `web` otherwise (R4). If mcp-go's mapping differs, the transport wrapper, which already sees the registration request (`transport_wrapper.go:408-438`), rewrites the field.

### D6. Opt-out and broker (FR-014, FR-015, US7)

- `config.OAuthConfig.SkipIssuerValidation bool` (`json:"skip_issuer_validation,omitempty"`) is wired at five points (project config-field rule):
  1. the config struct and validation. It is **excluded** from `config.OAuthConfigChanged`: like `RefreshReplayEnabled` it is request policy, not credential identity, so toggling it never clears a grant or moves the epoch;
  2. a `ServerFieldMaskDecisions` entry (`internal/oauth/serverconfigfields.go`, not a secret);
  3. OAS / `make swagger`;
  4. the Web UI server editor;
  5. the macOS server editor.

  It also hot-reloads. The FR-007 cache partition makes a reload back to strict take effect at the next discovery.
- Broker (`//go:build server`): `config.AuthBrokerConfig.Issuer` → `ConnectorConfig.Issuer`. The handler runs D1, and then, when an issuer is set, runs `DecideAuthorizationResponseIssuer` with `{Source: configured, IssSupported: false}` **before** the `error` branch (`credential_handlers.go:315`). A refusal is reported as `connect_failed` and `conn.Deny(state, "issuer_mismatch")`.

### D7. Surfaces (FR-017, FR-018)

- `internal/diagnostics/codes.go` gains `OAuthIssuerMismatch`, `OAuthIssuerChanged` and `OAuthMetadataIssuerMismatch`, with catalog entries and `docs/errors/<CODE>.md` pages (covered by the codes-to-docs test).
- Health: `ISSUER_CHANGED` → unhealthy, action `login`. `METADATA_ISSUER_MISMATCH` → unhealthy, action `configure`. `ISSUER_MISMATCH` → recorded OAuth failure, action `login`.
- Doctor: `management.BuildOAuthTokenStorageReport` (Spec 123) gains `issuer_binding` (see `contracts/doctor-issuer-binding.json`), scoped by reach.
- `auth status`, the OAuth block of `GET /api/v1/servers/{id}`, Web UI ServerDetail and the tray detail show the issuer origin, the source and `mix_up_protected` (from the last login). The login write stores `protected` as `MixUpProtected bool` (`json:"mix_up_protected,omitempty"`) in the record, so the flag survives a restart.

## Test plan

**Fake authorization server** `internal/oauth/oauthtest/fakeas.go` (test-only): one `httptest.Server` that serves PRM, RFC 8414 and OIDC metadata, registration, authorize (302 to the redirect URI, so the real callback server is exercised) and token. Its switches:

- `AdvertiseIss`
- `IssMode {Correct, Omit, Empty, Wrong, Repeat}`
- `MetadataIssuer` (declared issuer independent of the serving URL)
- `PathIssuer`
- `AuthorizeRedirectsTo` (mix-up: hand the browser to another instance)
- `PRMAuthServers`, which can change at run time
- `TokenRedirect` (307 to another origin)
- `RejectApplicationType`
- a request log of every registration body and token request (grant, client id, refresh token, verifier)

Two or three instances (honest H, hostile A, replacement B) cover mix-up and swap.

| Test | Covers |
|---|---|
| `TestDecideIssuer_Matrix` (unit) | FR-003/004/005/014: source × flag × iss mode (incl. empty) × skip → refused / protected |
| `TestParseCallbackQuery_Strict` (loopback, remote, broker) | FR-001; remote waiter not taken |
| `TestCallback_DecisionBeforePage` | the page reflects the refusal; `RecordOAuthFailure` carries the code |
| `TestMixUp_HonestIssRefusedInHostileFlow` | US1.5: A's token endpoint receives nothing |
| `TestLogin_IssAbsentNotAdvertised_UnprotectedFlag` | US1.4, FR-018 |
| `TestLogin_ErrorResponseWrongIss` | FR-005 |
| `TestDiscovery_Issuer_{PRMOrigin,PRMPath,OriginOnlyForPath,Fallback,OIDCOnly,Missing}` | FR-006, terminal, no fallback |
| `TestDiscovery_WrapperRefillValidated` / `TestDiscovery_NoLibraryRediscoveryAfterMismatch` | FR-006 "wherever obtained" |
| `TestDiscovery_PolicyPartition_{StrictFirst,SkipFirst}` + hot reload | FR-007, US6.4 |
| `TestSwap_NoCredentialReachesNewAS_{Proactive,Reactive,Stored,WarmHandler,Relogin}` | US2, SC-002 |
| `TestSwap_TokenEndpointCrossOriginRedirectNotFollowed` | FR-010 |
| `TestCredentialClientsHaveCheckRedirect` (AST guard) | FR-010 |
| `TestDCRWriteForNewIssuerClearsOldTokens` / `TestFailedCallbackAfterDCRLeavesBoundRegistration` | FR-012 |
| `TestSwap_StaticClientIDKept` / `TestSwap_StaticConfidentialSecretNotSentToNewIssuer` | US2.5, US2.10 |
| `TestDCR_CrossOriginRedirectNotFollowed` | FR-010 DCR |
| `TestFallbackSource_WrongIssRefused` / `TestFallback_NotForPathIssuer` / `TestBoundDiscovered_NoSendToFallback` | FR-004a |
| `TestStaticSecretProvenance_SurvivesLogoutAndScopesEdit` / `TestUpgrade_UnexpiredTokenSetsSecretProvenance` | FR-012, FR-011, US2.11, US3.7 |
| `TestSkip_FallbackDiscoveredTransitionsRefused` | FR-013 source class under skip |
| `TestDiscovery_EscapedPathSiblingNotAccepted` / `TestDiscovery_AmbiguousIssuerRefused` | FR-006 |
| `TestBinding_NoPRMTarget_PRMAppears{Same,Different}` | FR-013, US4.7 |
| `TestUpgrade_PathIssuerFallbackRowNotBound` | SC-003, US3.8 |
| `TestSnapshotExpiry_StaggeredPRMAndPreflight` | FR-009b |
| `TestRefusedCallback_NoTokenStored_DCRRowBound` | US1 storage, US1.9 |
| `TestSkip_MetadataCrossOriginRedirectStillRefused` | FR-014 / US6.6 |
| `TestDetection_TTLExpiryAndNewInvalidators` | FR-009b |
| `TestUpgrade_LegacyRowsBindOnFirstUse_{Plain,Sealed}` / `TestUpgrade_LegacyRowFirstUseSeesOtherAS` / `TestUpgrade_DiscoveryFailsLeavesUnbound` | US3, FR-011, SC-003 |
| `TestBindOAuthIssuer_RevisionRace` / `TestFirstUse_CompetingIssuers_{Plain,Sealed}` | FR-011: the CAS loser sends nothing unless it matches |
| `TestExchange_DiscoveryChangedSinceAuthorize_Aborts` | FR-009 code exchange |
| `TestWarmHandler_SharedClientIDDifferentSecret` | FR-009 handler-held credentials |
| `TestSkipToggle_ContinuityBothDirections` | FR-013 |
| `TestUpgrade_SyntheticLegacyRowBinds` | FR-011, SC-003 |
| `TestMetadata_CrossOriginRedirectRefused` / `TestInsecureIssuerReported` | FR-006a |
| `TestSkip_BindsToDiscoveryTarget` | FR-013, US6.2/6.3 |
| `TestUndecryptableBeforeIssuer` | US2.6 |
| `TestDCR_ApplicationType_{IPv4,IPv6,Localhost,Remote}` | FR-016 |
| `TestBroker_Iss_{Set,Unset,ErrorWithWrongIss}` (`-tags server`) | US7 |
| `TestRecordJSONUnchangedWithoutIssuer` | Spec 123 byte stability |
| `TestOAuthIssuerChecks_AllExchangePaths` (AST guard) | every `ProcessAuthorization*` call goes through `exchangeAuthorizationCode` |
| Doctor/diagnostics golden + scoped variant | FR-018 |
| `scripts/test-oauth-client-hardening-e2e.sh` | built binary against the fake AS as a helper binary (`internal/oauth/oauthtest/cmd/fakeas`): login, mix-up, swap, upgrade from a seeded previous-version `config.db` |

**Real-provider check** (manual, `verification/providers.md`): Cloudflare, Smithery, GitHub MCP, Notion, Atlassian and Sentry. For each, record the advertised flag, whether `iss` is sent, the declared issuer against its URL, and the DCR answer to `application_type`. SC-004 gate.

## Delivery

1. **PR-1 (bump, callback, `iss`, `application_type`)**: FR-019, D1, D2, D5, `ISSUER_MISMATCH`, the fake AS.
2. **PR-2 (discovery equality, binding, redirects)**: D3, D4, `ISSUER_CHANGED`, `METADATA_ISSUER_MISMATCH`, trust on first use, upgrade tests.
3. **PR-3 (opt-out, broker, surfaces, docs)**: D6, D7, the e2e script, the provider verification.

PR-2 is the first change that can refuse a provider that works today (metadata equality), so PR-3's opt-out ships in the same release. Each PR is cross-reviewed (opencode `gpt-6.2-sol`, round cap per the operator rules).

## Project Structure

### Documentation (this feature)

```text
specs/135-oauth-client-hardening/
├── spec.md
├── plan.md              # this file
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── diagnostics-codes.md
│   ├── doctor-issuer-binding.json
│   └── config-skip-issuer-validation.md
└── checklists/requirements.md
```

### Source Code (repository root)

```text
internal/oauth/          issuer.go (new), config.go (callback, preflight), discovery.go, discovery_cache.go, remote_callback.go,
                         persistent_token_store.go, refresh_manager.go, serverconfigfields.go,
                         transport_wrapper.go (refill validation; R4 rewrite only if needed), oauthtest/ (new, test-only)
internal/upstream/core/  connection_oauth.go (exchange seam, waiters), oauth_refresh.go, client.go (CheckRedirect)
internal/storage/        models.go (4 fields), oauth_binding.go (BindOAuthIssuer, issuer-aware DCR write)
internal/diagnostics/    codes.go + catalog
internal/management/     doctor OAuth section
internal/config/         OAuthConfig.SkipIssuerValidation, AuthBrokerConfig.Issuer
internal/serveredition/  broker/oauth_connector.go, api/credential_handlers.go
frontend/src/            server editor field, ServerDetail issuer line
native/macos/MCPProxy/   server editor field, detail line
docs/                    features/oauth-authentication.md, errors/*.md (3), configuration.md
```

**Structure Decision**: existing single-module layout. The only new package is `internal/oauth/oauthtest`, which is test-only.

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Own `iss` decision in the callback handler in addition to mcp-go's | The page is written before the exchange; mcp-go cannot tell synthetic metadata apart; the broker does not use mcp-go | Relying on mcp-go alone would show success pages for refused logins, refuse Spec 113 override logins, and leave the broker unchecked |
| TOFU binding write | No forced re-login (US3) | Re-login for every server on upgrade was rejected in the spec |
