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

### D0. Enforcement architecture: one resolver, one last-hop guard (review round 14)

This section governs the rest of the design. Where an item below names an earlier check, that check exists to fail early with a clear code. The two mechanisms here are what make the rules hold.

- **One policy-aware metadata resolver** (`oauth.ResolveASMetadata(ctx, server, policy) (*Snapshot, error)`). Every consumer of authorization-server metadata goes through it:
  - the preflight (`config.go:1385-1421`, `:2353-2402`);
  - the scope discovery `discoverASScopesCached` (`config.go:1095-1109`, `:2329-2342`; `discovery.go:458-506`);
  - the login metadata validator (`connection_oauth.go:1040`, `:1429`, `:2022`; `discovery.go:725-790`);
  - the configured URL;
  - the transport wrapper's refill.

  Each candidate carries the issuer it was derived from, so nothing is inferred from a request URL. The resolver returns a typed rejection (`*MetadataIssuerMismatchError`) that every caller treats as terminal. mcp-go never runs its own candidate loop. **Discovery has one owner.** The resolver runs **before** the mcp-go handler is created. On a rejection the flow stops there: no handler is created, and there is no authorize URL and no registration. On success the handler is **always** given an explicit `AuthServerMetadataURL`, which selects the branch at `mcp-go@v1.0.0 oauth.go:616-623`. The wrapper answers that URL with the snapshot's document. For an eligible `fallback` (FR-004a) the resolver builds the document from the fallback endpoints. So the library never enters its own candidate loop or its default-endpoint path (`:666-683`, `:738-755`). D3's earlier inference of an issuer from the request URL is withdrawn.
- **`Snapshot`** is immutable and holds:
  - the binding `{issuer, target, source}`;
  - `IssSupported`, `Insecure` and `Verified` (for `configured`: the URL equals a candidate derived from the declared issuer, FR-008);
  - `Skipped` (the metadata comparison was skipped under the opt-out);
  - the exact authorization, token and registration endpoints.
- **Per-send context.** Every credential-bearing request carries `oauth.SendContext{Snapshot, Credentials}` in its `context.Context`. `Credentials` lists each credential the request may carry with its binding: the client id/secret or assertion, the refresh token, the code, and the verifier with the registration captured at authorization time.
  - mcp-go requests: the context is attached by `WithSendContext(ctx)` at the call site.
  - Direct sends (`refreshTokenWithStoredCredentials`, the broker connector): the context is passed explicitly.
- **Last-hop guard.** `oauth.GuardedTransport` wraps three clients:
  - the inner transport of the OAuth wrapper;
  - the direct refresh client in `refreshTokenWithStoredCredentials` (`client.go:989-1010`), built as `&http.Client{Transport: oauth.GuardedTransport(c.Gate().HTTPTransport()), CheckRedirect: oauth.CredentialCheckRedirect}`;
  - the broker's client (`broker/oauth_connector.go:123-130`).

  Operations are **marked, not recognised by URL**. Every token, exchange, refresh and registration call site attaches a `SendContext{Op, …}`. For every **POST** through these clients, before any network I/O, the guard does the following:
  1. requires a `SendContext`. A POST with none is refused, which also covers a URL that matches no known endpoint.
  2. requires the request URL to equal exactly the snapshot's endpoint for that `Op`;
  3. parses the final form **and** the `Authorization` header (client_secret_basic);
  4. checks every credential value found against `Credentials` and their bindings against the snapshot (FR-009, FR-009a);
  5. applies the credential redirect policy (FR-010);
  6. refuses a POST whose URL query carries any class-1 or class-4 key (FR-010b).

  Credential-bearing **GETs** are the authorization redirect and the stale-client probe (`oauth_stale_dcr_client.go:185-189`, `:321-325`; `authorize_probe.go:153-159`). They carry only a `client_id`, which is a public identifier. Both are built from the flow's captured snapshot and registration, never from the handler's current metadata. Before the probe runs and before the URL is handed to the browser, `oauth.CheckAuthorizationURL(u, flow)` requires the URL's endpoint to equal `Snapshot.AuthorizationEndpoint` and its `client_id` to equal the captured registration. After a successful stale-client re-registration (`oauth_stale_dcr_client.go:265-325`), the flow takes a new registration capture against the **same** validated snapshot; the second probe, the browser URL and the code exchange use that capture. Test: `TestAuthorizationURL_EndpointOrClientIDDiffersFromCapture_Refused`.

  The guard is what holds when metadata or the registration changes between a preliminary check and transport. The test `TestGuard_MetadataOrRegistrationChangedBeforeTransport` covers that case. A variant sends to a changed URL that matches no known endpoint and asserts that the inner transport is never called.

### D1. One strict callback parse (FR-001)

- `oauth.ParseCallbackQuery(rawQuery string) (url.Values, error)` parses the query once and rejects:
  - a repeated `code`, `state`, `iss` or `error`;
  - `code` and `error` together;
  - neither `code` nor `error`, or either one present with an empty value (an empty `iss` is passed on to the issuer decision, which treats it as a mismatch);
  - a missing or empty `state`.
- It runs first in `handleCallbackWithFailureNotification` (`internal/oauth/config.go:2005`), in the remote callback (it replaces the inline check at `remote_callback.go:68-72`), and in the broker callback (`api/credential_handlers.go:307`).
- A rejected request does not take a waiter. The remote callback keeps its current rule that "an invalid public request is not an OAuth failure".
- `CallbackServer.deliver(state, params map[string]string)` becomes `deliver(state, callbackResult{query url.Values, err error})`, and the waiter channel type changes to match. Logging keeps first values only, scrubbed, plus `iss_present`.
- **One waiter per flow.** URL creation registers the waiter, together with its `ExpectedIssuer`, **before** the URL is exposed. The handle it returns is passed to the consumer, including the async goroutine. Today the goroutine registers the state a second time (`connection_oauth.go:2285`, `:2388-2403`), and a callback arriving in that gap is lost (`config.go:1833-1844`, `:1916-1934`). Every consumed outcome is delivered exactly once: success, refusal, or an accepted provider error. The remote handler hands its parsed query on instead of parsing it again (`remote_callback.go:67-75`). Test: `TestCallback_ArrivesBeforeAsyncConsumer`.
- The three consumers in `connection_oauth.go` (`:1348`, `:1621`, `:2409`) return `result.err` when it is set. Otherwise they read `code`/`state` from `result.query` and pass the query to `exchangeAuthorizationCode`.

### D2. Issuer snapshot and the `iss` decision (FR-002–FR-005)

- New file `internal/oauth/issuer.go`:
  - `IssuerSource` (`discovered` | `configured` | `fallback` | `synthetic`). `fallback` is mcp-go's default endpoints when no document and no override exist (FR-004a): `iss` rules as for `discovered` with `IssSupported=false`.
  - `ExpectedIssuer{Issuer, Source, IssSupported, Target}`. `Target` is the discovery target URL (FR-013).
  - `DecideAuthorizationResponseIssuer(q url.Values, exp ExpectedIssuer, skip bool) (protected bool, err error)`, which returns `*IssuerMismatchError` (`diagnostics.OAuthIssuerMismatch`).
  - Rules:
    - `len(q["iss"]) == 1` means present, even when the value is empty. An empty value is a mismatch, which is stricter than mcp-go (R6).
    - For **every** source, `protected` also requires `!Snapshot.Insecure`.
    - `discovered` and `configured` follow FR-003. `protected` is true only when `iss` was present and equal, the snapshot is not `Skipped` and not `Insecure`, and for `configured` the snapshot is `Verified` (FR-008). Otherwise `configured_issuer_unverified` or `issuer_validation_disabled` is surfaced.
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

- **Escaped paths.** Candidates are built from `u.EscapedPath()`, never `u.Path`. `validateIssuerURL` refuses a query, a fragment, `%2F`, `%2E`-encoded or literal dot segments, and any `.well-known` path segment (the OIDC insertion form of `/t/.well-known/openid-configuration` and the suffix form of `/.well-known/openid-configuration/t` would otherwise share a URL). Test: `TestMetadataCandidates_WellKnownSegmentRefused`. When PRM names no authorization server, the target is the candidate's issuer (the MCP URL's issuer form).
- **Candidates carry their issuer.** `BuildRFC8414MetadataURLs` becomes `metadataCandidates(issuerURL) []candidate{url, issuer}`. For a path issuer it builds only path-derived forms: RFC 8414 path insertion, and OIDC `…/{path}/.well-known/openid-configuration` plus insertion. For an origin issuer it builds the origin forms. The no-PRM fallback derives the issuer from the MCP origin.
- `findWorkingMetadataDoc` (`discovery.go:930-958`) validates `trimOneSlash(doc.Issuer) == trimOneSlash(candidate.issuer)` after it parses each document. A mismatch or a missing `issuer` returns `*MetadataIssuerMismatchError` (`diagnostics.OAuthMetadataIssuerMismatch`) at once. No further candidate is tried. `preflightMetadataURL` propagates it as terminal (no seed, failure TTL) and does not leave `authServerMetadataURL` empty for mcp-go to rediscover on its own.
- **Wrapper serves only its captured snapshot.** A live wrapper answers mcp-go's metadata GETs **only** with the document of the snapshot captured for its handler. It never refetches and never rewrites what it serves to an existing handler, so the authorization request, the callback decision and the guard all use one document. A refill happens only through the resolver. It produces a replacement snapshot, which is used by a **new** handler or flow (FR-009b rebuild). Today's refill-and-learn path (`transport_wrapper.go:491-515`, `:573-605`) is reduced to serving the captured document. Test: `TestWrapper_RefillSameIssuerChangedEndpointsOrIssSupported_ExistingHandlerUnaffected`.
- **Library discovery** never runs. D0 always sets an explicit metadata URL, and a rejection stops the flow before the handler exists. `SkipIssuerMetadataValidation` in the mcp-go `OAuthConfig` follows the server's switch, as belt and braces.
- **Per-consumer validation (FR-007).** The document cache keeps raw bytes (`asMetadataDoc.raw`), as it does today, and validation runs on every use. The `metadataPreflight` result cached under `makeDiscoveryKey("metadata-url", …)` gains the policy (strict or skip) in its key, so a strict failure and a skip success never share an entry.
- **Configured URL (FR-008)**: no derivation compare. The declared issuer is used as is, and a missing one fails. `Snapshot.Verified` is true only when the configured URL equals one of `metadataCandidates(declaredIssuer)`, built with the same escaped-path builder as discovery.
- **Opt-out inside the resolver (FR-014)**: under skip, an equality mismatch logs one scrubbed WARN and returns the snapshot with `Skipped=true`. Missing issuers, ambiguous issuers and cross-origin redirects stay terminal in both modes. Callback rules under skip, for every source including `fallback`: a wrong or absent `iss` is accepted with a WARN, and a skipped metadata comparison makes an otherwise matching callback `protected=false`. Test: `TestSkip_MetadataMismatchThenMatchingIss_NotProtected`.
- **Origin of the document (FR-006a).** A new redirect policy `metadataCheckRedirect` allows only same-origin hops. Unlike `oauthCheckRedirect`, it also blocks cross-origin GETs. It is installed on the resolver's network fetch, `httpFetchRaw` (`discovery_cache.go:310-328`). The wrapper refill client (`transport_wrapper.go:496`) goes away with the refill path, because a live wrapper no longer fetches. A blocked redirect is returned as `*MetadataIssuerMismatchError`, and `skip_issuer_validation` does not lift it (FR-014). `insecure_issuer` is computed when the snapshot is built: an `http` scheme on the issuer or any endpoint, with a non-loopback host (`config.IsLoopbackHost`). It is carried on `ExpectedIssuer.Insecure` and surfaced in D7.

### D4. Issuer-bound rows (FR-009–FR-013)

- **Record.** `storage.OAuthTokenRecord` gains `Issuer`, `DiscoveryTarget`, `IssuerSource` and `MixUpProtected` (all `omitempty`; data-model.md). A row is unbound only when `Issuer`, `DiscoveryTarget` and `IssuerSource` are all empty. A partial binding is refused as corrupt (`MCPX_OAUTH_REAUTH_REQUIRED`), never treated as legacy. They live inside the body that `encodeOAuthRecord` seals (`oauth_binding.go:166`). The additional data does not change, and a row without the fields marshals to the same bytes as before.
- **Binding = `{Issuer, Target, Source}`** (FR-013). `ExpectedIssuer.Binding()` derives it from a snapshot: for `discovered` the validated issuer plus the PRM entry, for `configured` the declared issuer plus the metadata URL, for `synthetic` the synthetic issuer as both values. `Matches(stored, snap, skip)` always compares the target and the source class (`fallback` versus the rest), and compares the issuer only when skip is off. Toggling skip never changes the target, so continuity holds in both directions.
- **Handler-held credentials (FR-009).** In strict mode, every place that puts `ClientID`/`ClientSecret` into an mcp-go `OAuthConfig` (`config.go:1495-1546`) also sets `CredentialsIssuer`. Under skip it stays empty (see below). It is set from the credential's own binding: the row's for persisted DCR, the flow's `exp` for a fresh DCR, the issuer of the first validated snapshot for a static client. mcp-go v1.3+ then refuses any refresh whose metadata names another issuer (`ErrAuthorizationServerChanged`). On the mcpproxy side, `oauthRefreshOnce` takes the secret from the same credential snapshot whose binding it checked, never from a handler whose binding differs. That rules out the warm-handler case where A and B share a `client_id`.
- **Code exchange (FR-009).** The pending flow stores the `ExpectedIssuer` captured at authorization time. Before redemption, `exchangeAuthorizationCode` compares `h.GetServerMetadata()` (the handler's current metadata) with the capture: the same issuer and the same token endpoint. If they differ, it aborts with `ISSUER_CHANGED` and restarts the flow, and the code is never sent. mcp-go v1.0.0 caches metadata once per handler (`sync.Once`), and v1.2+ re-discovers after a failure, so this compare is what catches a rediscovery in between.
- **Snapshot-consistent sends (FR-009).** A new helper `oauth.SendSnapshot(handler) (ExpectedIssuer, *AuthServerMetadata, error)` returns one validated snapshot. Every credential send gets its issuer **and** its endpoint from that one value:
  - `refreshWithStoredCredentials` takes `metadata.TokenEndpoint` from the snapshot, then checks `CheckStoredIssuer(rec, snap)`, then sends.
  - `oauthRefreshOnce` / `oauthRefreshFunc` (`oauth_refresh.go:66-110`) check before calling the library refresh. In strict mode they also set `OAuthConfig.CredentialsIssuer = rec.Issuer` when the row is bound, so mcp-go's v1.3+ refresh refuses a different issuer by its own rule (`ErrAuthorizationServerChanged`, mapped to the same code). Under skip, `CredentialsIssuer` stays empty, which is the library's legacy mode. The last-hop guard then enforces target and source class (FR-013), and stored credentials are never relabelled. Tests: `TestSkip_RefreshChangedDeclaredIssuerSameTarget_Allowed`, `TestSkipOff_SameCase_Refused`.
  - `RefreshManager` attempts go through the same check.
  - Login reuse (`config.go:1189-1196`, `:1505-1514`): a stored `client_id` is reused only when `Binding.Matches(stored, snapshot, skip)` holds, which is the same send-eligibility rule as everywhere else. A **login that will write a new grant** reuses the registration only when `Binding.Equal(stored, snapshot)` holds. Under skip, a registration whose binding `Matches` but is not `Equal` is not reused for a login: the login obtains a new registration (DCR), or uses an operator-configured **public** client id afresh (spec US2), and writes token, registration and binding together under the new binding. Any effective class-4 credential (FR-010b, aliases included) whose provenance check fails refuses the login with `ISSUER_CHANGED` (action `configure`). So a login never stamps new tokens with an old binding, and never relabels a reused registration. `Matches` alone suffices only for continuing an existing grant (refresh). Test: `TestSkip_LoginWithMatchingNotEqualRegistration_RegistersAnew`.
  - Code exchange: the reused registration must match `exp`.
- **Snapshot lifetime (FR-009b).** `metadataPreflight` stores `expiresAt = min(own TTL, PRM entry expiry, document entry expiry)`, and the cache honours it, so a preflight built on an old PRM entry cannot outlive that entry.
- **In-memory store (no database, CLI mode, `config.go:1456-1460`).** The in-memory token store keeps the binding beside each token from the moment it is issued, and refresh carries it forward. A token or refresh record without a binding is never sent (`ErrOAuthAuthorizationRequired`): the CLI signs in again rather than assign provenance at send time. Every return and refresh from the in-memory store runs the same `Matches` check against the current snapshot as the bbolt store (`config.go:218-233`, `persistent_token_store.go:639-650`). Without a database there is no durable provenance, so **no configured class-4 credential is sent** in this mode: a confidential client is refused with `MCPX_OAUTH_REAUTH_REQUIRED` and a message to run the command against the core or with the data directory available. Public clients are unaffected. Test: `TestMemoryStore_ConfiguredSecretNeverSent`. Test: `TestMemoryStore_BindingCarriedThroughRefresh_UnboundRefused`.
- **Fallback (FR-004a).** Used only when the selected issuer has no path. `Binding.Matches` compares the source class too (`fallback` vs the rest).
- **Freshness (FR-009b).** `oauthRefreshOnce`, the refresh manager, login and the 401 handler call `preflightMetadataURL` (cache lookup, re-resolved after the TTL) and, when its validated document differs from `h.GetServerMetadata()`, rebuild the handler when the **effective snapshot** changes, not only when the library's metadata differs. A change counts if any binding field changes (target and source class included) or any endpoint does. Then run the FR-009 compare. The resolved snapshot is authoritative for the send and for `ISSUER_CHANGED`, even when the document is byte-identical. New invalidations: `globalDiscoveryCache.invalidateServer(serverURL)` on `invalid_grant`/`invalid_client` refresh failures and at the start of an explicit login. Freshness is a UX bound, not part of the security invariant (spec "Protection model"). Detection is tied to discovery. Whenever mcpproxy runs discovery for a server (connect, 401 rediscovery, refresh-failure rediscovery, login), the new snapshot replaces the handler's metadata. If the snapshot names another issuer than the row, the server enters `ISSUER_CHANGED`. A warm handler whose cached metadata still names A sends to A, which is safe: A issued the credentials. `PersistentTokenStore.GetToken` returns `ErrOAuthAuthorizationRequired` wrapped with the code while the server is in `ISSUER_CHANGED`, so the access token stops being attached. `GetToken` resolves the snapshot and runs `Matches` **before** entering the refresh coordinator, and passes that snapshot into the flight. Every token the coordinator can serve (its own result, a concurrently stored row, a joined flight's result) is checked against that same snapshot before it is returned; an `*IssuerChangedError` is terminal **for the current attempt only**: it is returned before the coordinator's generation latch and cooldown (`refresh_coordinator.go:546-549`, `:652-692`), is never latched, and is never mapped to `ErrTokenRefreshTransient`, so the next attempt re-evaluates against discovery and recovers once it resolves the bound issuer again (`refresh_coordinator.go:519-554`, `:616-622`). A mismatch found **during** a `GetToken` call (by the reactive refresh) returns a typed `*IssuerChangedError`, and it takes precedence over the still-valid-access-token fallback (`persistent_token_store.go:562-597`): that fallback serves only on a transient refresh failure, never on an issuer mismatch, and any replacement row is re-checked against the snapshot before its token is returned. Test: `TestGetToken_ReactiveRefreshFindsIssuerChange_StillValidTokenNotServed`. The state clears once a discovery resolves the bound issuer again. The discovery cache's success TTL (`discovery_cache.go:21-28`, 1 h) bounds how late a swap at an unchanged PRM URL is noticed. The swap test injects the existing cache clock (`discoveryCache.now`, `discovery_cache.go:89-104`) and advances it past the TTL. A second variant fires each invalidation event instead.
- **Static confidential client (FR-012).** A new bucket `oauth_static_client_bindings` (in the Spec 116 purge set for server delete and replacement only) is keyed by **durable server identity** (the Spec 116 incarnation, not `GenerateServerKey`, which includes the URL) plus each credential's fingerprint. It holds `{issuer, target, source}`, and it is created or compared atomically under the current Spec 116 binding before any send, where `fingerprint = sha256(credential)`, one entry per effective client-authentication credential (`client_secret`, `client_assertion`, …). **One** resolver, `oauth.EffectiveExtras(cfg)`, produces the effective values. It normalises names the same way `ClassifyExtraParam` does, and it feeds injection, provenance writes and removals, and the guard's final check. It replaces `extraParamValue`'s `EqualFold` lookup (`oauth_refresh.go:42-55`). If two aliases of one credential appear in the extras, config validation refuses the config. Extras logging (`transport_wrapper.go:462-466`, `config.go:1466-1470`) fully redacts class 1 and class 4 values through the same classifier, replacing `maskExtraParams`' partial masking (`masking.go:5-74`). Tests: `TestEffectiveExtras_ConflictingAliasesRefused`, `TestExtrasLogging_FullyRedacted`. The effective secret is `oauth.client_secret`, overridden by `oauth.extra_params.client_secret` (matched case-insensitively, as `extraParamValue` does). The client id is not part of the fingerprint, because DCR may change it. Client secrets are high-entropy, and the value is sealed under Spec 123 when a key is set. It is written in its own transaction before the first secret-bearing request. Logout and grant clears do not touch it. A config commit whose effective value of a class-4 field changes deletes that fingerprint's entry **only when no other effective class-4 field still has that value** (entries are per fingerprint, which two fields can share). Test: `TestStaticProvenance_SharedFingerprintRotateOneField_EntryKept`. Server delete or replacement purges them all. A client-id-only edit clears the grant and moves the epoch (`config.go:3433-3445`, `commit.go:1211-1214`), but it does **not** touch this bucket, so the unchanged secret keeps its binding. A URL edit that keeps the server's incarnation keeps it too. Tests: `TestStaticProvenance_{ClientIDOnlyEdit,URLEditSameIncarnation,CompetingFirstUse}`. On `ISSUER_CHANGED` with a configured secret, the login is refused before the authorize redirect with health action `configure`. An epoch move (any OAuth clear, a `client_id` edit included) clears the grant row but **never** resets provenance. An entry goes only when its own credential's effective value changes, or when the server is deleted or replaced.
- **Extras classes (FR-010b, FR-009a).** `oauth.ClassifyExtraParam(key)` normalises the key the same way `logging.go:376-419` does, then checks in precedence order: `flowOwned`, `legacyClientID`, `assertionType`, `clientAuth` (explicit `client_secret`/`client_assertion`/`assertion` and their normalised forms, then the existing normalised matcher), `plain`. The classes are mutually exclusive by construction.
  - `applyExtraParamsToAuthURL` and the wrapper's authorization-query injection pass only `plain` keys.
  - The token-form injection (`transport_wrapper.go:447-455`) passes `plain`, `clientAuth` and `assertionType` keys (`assertionType` carries no provenance). `flowOwned` keys are dropped with a value-free WARN on **every** channel. The authorization-URL paths drop them too, and log the same WARN.
  - `legacyClientID` is folded into the effective registration when the OAuth config is built (`config.go:1491-1503`), so mcp-go puts it in both requests.
  - The FR-009 compare runs in the wrapper's `RoundTrip`, on the final form. When the request goes to a token or registration endpoint, the wrapper looks up the binding of each `clientAuth` value and of the client secret, refresh token and verifier it carries, compares them against the request's snapshot, and refuses the request on any mismatch. This makes the wrapper the single last-hop check. The earlier checks remain, so that failures surface early with a clear code.

- **Refresh persistence.** A refresh flight carries the precondition of the row it read (revision and Spec 116 binding) together with its issuer binding. Both refresh writers (`persistent_token_store.go:334-359`, `oauth_refresh.go:144-159`) check these inside the save transaction, in addition to `GenerationMatches`. `GenerationMatches` alone is not enough: the generation fingerprints only the refresh token and the client id (`refresh_coordinator.go:856-863`), and neither is unique across issuers. A superseded result is discarded. First-use binding (`EnsureBound`) completes **before** a flight captures its precondition, so the flight reads the committed bound row and its own binding write can never supersede it (`refresh_coordinator.go:519-565`). Test: `TestRefresh_LegacyRowFirstUse_BindsThenRotates_NotDiscarded`. A refresh that skip allows while the declared issuer changed at the same target **continues the existing grant under its stored binding**. It writes the new tokens and keeps the stored `{issuer, target, source}`, and it never adopts the unchecked issuer claim. A binding transition happens only on a new registration or login. Test: `TestSkip_RefreshContinuesStoredBinding_ThenStrictRefuses`. Test: `TestRefresh_DelayedAResultAfterBLogin_SameIDAndRefreshToken_Discarded`.
- **Redirects (FR-010), one policy.** A credential-bearing POST (token, exchange, refresh, registration) follows **no** redirect, whether same-origin or cross-origin. That matches the exact-endpoint guard. `oauth.CredentialCheckRedirect` returns `http.ErrUseLastResponse` for these requests, and the redirect response surfaces as a token-endpoint error. This is a deliberate change: today's `oauthCheckRedirect` allows a same-origin POST redirect, and `internal/oauth/transport_wrapper_endpoints_test.go:225-238` tests that. The test is rewritten to assert refusal. Metadata GETs keep same-origin redirects (FR-006a). Tests: `TestGuardedClient_{SameOrigin,CrossOrigin}PostRedirectNotFollowed`.
- **Redirects (FR-010), details.** All three clients use the one recipe from D0, and they use `oauth.CredentialCheckRedirect`, **not** today's `oauthCheckRedirect`:
  - the OAuth wrapper's client, replacing the current install at `config.go:1485-1489`;
  - the direct refresh client in `refreshTokenWithStoredCredentials` (`internal/upstream/core/client.go:989-1010`): `&http.Client{Transport: oauth.GuardedTransport(c.Gate().HTTPTransport()), CheckRedirect: oauth.CredentialCheckRedirect, Timeout: 30 * time.Second}`;
  - the broker client.

  Because no redirect is followed, a 302 or 303 can never turn the follow-up into a GET that would bypass the POST guard. Tests:
  - `TestCredentialClients_{302,303,307,308}_{SameOrigin,CrossOrigin}_DestinationNeverContacted`, for all three clients;
  - `TestDirectRefresh_EndpointDiffersFromSnapshot_InnerTransportNotCalled`;
  - an AST guard asserting that every credential-bearing `http.Client` in `internal/oauth`, `internal/upstream/core` and `internal/serveredition/broker` sets `CredentialCheckRedirect`.
- **Trust on first use (FR-011).** Binding happens at the first use of the row, which includes `PersistentTokenStore.GetToken` returning an unexpired access token (`persistent_token_store.go:129-184`, `config.go:1436-1454`). It runs **before** any credential is returned, for every permitted source (discovered, configured, eligible fallback, synthetic). The CAS checks the Spec 116 incarnation and epoch as well as the revision, and the caller then uses the committed or reloaded row. FR-011a: when `encodeOAuthRecord` returns `tokenseal.ErrPlaintextTooLarge` (`oauth_binding.go:166-179`), the transaction rolls back. The code logs one size-only line, the server reports `MCPX_OAUTH_REAUTH_REQUIRED`, and the next explicit login replaces the row through the ordinary login write. Every path that reads an unbound row calls `oauth.EnsureBound(rec, snap)` **before** it releases a credential. That call commits the binding through `storage.BindOAuthIssuer`. If the commit loses (the revision changed, or a binding appeared), it reloads the row and re-runs FR-009 against its own snapshot: it proceeds only on a match and otherwise aborts without sending. `storage.BindOAuthIssuer(serverKey, binding, observedRevision, b Binding) (bound bool, err error)`:
  - runs in one bbolt transaction;
  - re-reads the row and requires `Revision == observedRevision` and all three binding fields empty;
  - requires that the row is not undecryptable;
  - re-seals with the configured key, or writes plain JSON without one, as today, and in the same transaction creates `oauth_static_client_bindings` for a configured confidential client when it is absent.

  Otherwise it writes nothing. It runs at most once per row, off the hot path, after the use that discovered the issuer.
- **One issuer per row (FR-012).** The bound writer is `SaveOAuthClientIfBound` (`oauth_binding.go:467-501`). The **write-side** comparison is `Binding.Equal` (issuer, target and source class, always). It is separate from the policy-aware send check `Binding.Matches`, which drops the issuer under skip, so a same-target issuer change under skip still clears the old grant. Test: `TestSkip_SameTargetIssuerChange_DCRThenCancel_OnlyNewRegistrationRemains`. Its `IfUnsetOrSame` mode, used by `persistDCRRegistration` (`oauth_stale_dcr_client.go:69-87`), rejects a new client id while the old one remains. It gains an **issuer-transition mode**, guarded by the observed revision and the Spec 116 binding, that atomically replaces the registration and clears the old grant's tokens and `MixUpProtected`. Its callers include `connection_oauth.go:1524` and `oauth_stale_dcr_client.go:284`. Test: `TestDCRWrite_IssuerTransitionDifferentID_Concurrent`. The DCR persistence called before the browser opens (`connection_oauth.go:1177-1228`, `:2218-2244`) passes the issuer. When the row's binding differs from the new one under `Binding.Equal`, the same transaction clears `AccessToken`, `RefreshToken`, `ExpiresAt` and `Scopes`. Those tokens were already refused under FR-009. The login write (`WithLoginClient`) always writes token, registration and issuer together. A failed callback after DCR leaves a registration-only row bound to the new issuer, which is reusable for the next login against that issuer.
- **Opt-out binding (FR-013).** The record always stores issuer, target and source. With skip on, `Binding.Matches` drops **only** the declared-issuer comparison. Target and source class are still compared, so a grant bound to published metadata is never sent to `fallback` endpoints at the same target, in either direction. Tests: `TestSkip_FallbackDiscoveredTransitionsRefused`.
- **Operator endpoint overrides on fetched metadata (recorded decision, round 21).** Today `rewriteMetadata` (`transport_wrapper.go:573-594`) applies operator overrides to discovered documents inside the wrapper. It moves into the resolver: overrides are applied once, before the snapshot and its served document are frozen, and the wrapper stays read-only (D3). An override is operator configuration, the same trust input as the static secret itself. The binding protects against an **upstream** change, which still changes the binding and is refused. An override edit is an OAuth clear (`config.OAuthConfigChanged`). Static provenance deliberately survives it, as it survives other operator edits. This is not endpoint pinning; an operator who points the token endpoint elsewhere has chosen where the secret goes.
- **Synthetic (FR-004).** The stored issuer is the synthetic issuer (the origin of the authorization endpoint). An override edit is an OAuth clear (`config.OAuthConfigChanged`, `internal/config/config.go:3422`): the epoch moves and the old row is never read under new overrides. So synthetic-to-synthetic comparisons only ever meet the same overrides. A switch from synthetic to discovered (overrides removed) is also an OAuth clear.

### D5. DCR `application_type` (FR-016)

- After the bump, a table test pins mcp-go's registration body: `native` for every host that `config.IsLoopbackHost` accepts (`internal/config/oauth_validation.go:192-199`), including `127.0.0.2`, `[::1]` and `localhost`, and `web` otherwise (R4). The assertion and any rewrite ship in the same PR. If mcp-go's mapping differs, a new registration-request interception in the transport wrapper rewrites the field. This is new work, because today the wrapper only injects query and form parameters (`transport_wrapper.go:403-438`).

### D6. Opt-out and broker (FR-014, FR-015, US7)

- `config.OAuthConfig.SkipIssuerValidation bool` (`json:"skip_issuer_validation,omitempty"`) is wired at five points (project config-field rule):
  1. the config struct and validation. It is **excluded** from `config.OAuthConfigChanged`: like `RefreshReplayEnabled` it is request policy, not credential identity, so toggling it never clears a grant or moves the epoch. Today `OAuthConfigChanged` returns true on any nil-to-non-nil transition (`config.go:3422-3445`), and `commitOAuthClearSet` acts on that result (`commit.go:1203-1219`). So an `oauth` block that holds only policy fields must compare equal to nil. The per-server PATCH (`internal/httpapi/server.go:2356-2362`) today rejects any `oauth` block. It is changed to accept an `oauth` object whose only key is `skip_issuer_validation`, merged into the stored block, with every other OAuth key still rejected. An omitted field means unchanged, and an explicit `false` sets the value. Test: `TestFR015_AddOAuthBlockWithOnlySkip_GrantAndEpochSurvive`, in both directions.
  2. a `ServerFieldMaskDecisions` entry (`internal/oauth/serverfields.go:221`, not a secret);
  3. OAS / `make swagger`;
  4. the Web UI server editor;
  5. the macOS server editor.

  It also hot-reloads. The FR-007 cache partition makes a reload back to strict take effect at the next discovery.
- Broker (`//go:build server`): `config.AuthBrokerConfig.Issuer` → `ConnectorConfig.Issuer`. The handler runs D1, and then, when an issuer is set, runs `DecideAuthorizationResponseIssuer` with `{Source: configured, IssSupported: false}` **before** the `error` branch (`credential_handlers.go:315`). A refusal is reported as `connect_failed` and `conn.Deny(state, "issuer_mismatch")`.

### D7. Surfaces (FR-017, FR-018)

- `internal/diagnostics/codes.go` gains `OAuthIssuerMismatch`, `OAuthIssuerChanged` and `OAuthMetadataIssuerMismatch`, with catalog entries and `docs/errors/<CODE>.md` pages. A new assertion checks that each page exists on disk; the existing catalog test only checks URL strings (`internal/diagnostics/catalog_test.go:114-126`).
- Health: `ISSUER_CHANGED` → unhealthy, with action `login`, or `configure` when the refused credential is a bound configured client-authentication credential. Every surface takes the action from one helper, `issuerChangedAction(refusedCredential)`. The action follows the refused credential, not the row type: a configured class-4 credential gives `configure` even when the client id came from DCR. `METADATA_ISSUER_MISMATCH` → unhealthy, action `configure`. `ISSUER_MISMATCH` → recorded OAuth failure, action `login`.
- Doctor: a **sibling** report, `oauth_issuer_status` (`contracts/doctor-issuer-binding.json`), built from live `Runtime.OAuthIssuerStatus`. It does not depend on the Spec 123 storage-state read (`internal/management/diagnostics.go:320-335`). Reach is applied before counting. It is exposed by `mcpproxy doctor` and `GET /api/v1/diagnostics`. Its golden test asserts placement, both actions, scoped counts and the three code-to-doc mappings.
- The existing scoped server-list projection (`GET /api/v1/servers`, `contracts.Server`) gains the fields, and therefore so do `auth status`, Web UI ServerDetail and the macOS detail, which all read it. Together they show the issuer origin, the source and `mix_up_protected` (from the last login). The login write stores `protected` as `MixUpProtected bool` (`json:"mix_up_protected,omitempty"`) in the record, so the flag survives a restart.

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
| `TestDecideIssuer_Matrix` (unit) | FR-003/004/004a/005/014: source (discovered, configured, fallback, synthetic) × flag × iss mode (correct, absent, empty, wrong, repeated) × skip → refused / accepted-unprotected / protected; repeated always refused (FR-001) |
| `TestParseCallbackQuery_Strict` (loopback, remote, broker; incl. `code=` empty) | FR-001; remote waiter not taken and still usable by a following valid callback |
| `TestCallback_DecisionBeforePage` | the page reflects the refusal; `RecordOAuthFailure` carries the code |
| `TestMixUp_HonestIssRefusedInHostileFlow` | US1.5: A's token endpoint receives nothing |
| `TestLogin_IssAbsentNotAdvertised_UnprotectedFlag` | US1.4, FR-018 |
| `TestLogin_ErrorResponseWrongIss` | FR-005 |
| `TestDiscovery_Issuer_{PRMOrigin,PRMPath,OriginOnlyForPath,Fallback,OIDCOnly,Missing}` | FR-006, terminal, no fallback |
| `TestDiscovery_WrapperRefillValidated` / `TestDiscovery_NoLibraryRediscoveryAfterMismatch` | FR-006 "wherever obtained" |
| `TestDiscovery_PolicyPartition_{StrictFirst,SkipFirst}` + hot reload | FR-007, US6.4 |
| `TestSwap_NoCredentialReachesNewAS_{Proactive,Reactive,Stored,WarmHandler,Relogin}` | US2, SC-002 |
| `TestSwap_TokenEndpointCrossOriginRedirectNotFollowed` | FR-010 |
| `TestCredentialClientsHaveCheckRedirect` (AST guard, incl. `internal/serveredition/broker`) / `TestBroker_TokenCrossOriginRedirectNotFollowed` (`-tags server`) | FR-010 |
| `TestGuard_MetadataOrRegistrationChangedBeforeTransport` / `TestGuard_ClientSecretBasicHeaderChecked` | D0, FR-009/009a |
| `TestResolver_ScopeAndLoginValidatorPathsTerminal` / `TestResolver_LibraryNeverRunsOwnCandidateLoop` | D0, FR-006 |
| `TestConfigured_VerifiedURLMarkedProtected` | FR-008 positive case |
| `TestFR015_ConfigRoundTrip_{REST,WebUI,macOS}_EpochUnchanged` | FR-015 |
| `TestDCR_RejectApplicationType_NoRetry` | FR-016 |
| `TestFR002_PerFlowSnapshot_LiveHandlers` (all three login paths) | FR-002 |
| CLI `auth status` + server-detail golden (issuer origin, source, `mix_up_protected`, every warning, `configure` action for confidential static) | FR-017/018 |
| PR-1 gate: `go list -m github.com/mark3labs/mcp-go` ≥ v1.4.0 and the full existing `internal/oauth` + `internal/upstream/core` OAuth suites green | FR-019 |
| `TestDCRWriteForNewIssuerClearsOldTokens` / `TestFailedCallbackAfterDCRLeavesBoundRegistration` | FR-012 |
| `TestSwap_StaticClientIDKept` / `TestSwap_StaticConfidentialSecretNotSentToNewIssuer` | US2.5, US2.10 |
| `TestDCR_CrossOriginRedirectNotFollowed` | FR-010 DCR |
| `TestFallbackSource_WrongIssRefused` / `TestFallback_NotForPathIssuer` / `TestBoundDiscovered_NoSendToFallback` | FR-004a |
| `TestStaticSecretProvenance_SurvivesLogoutAndScopesEdit` / `TestUpgrade_UnexpiredTokenSetsSecretProvenance` | FR-012, FR-011, US2.11, US3.7 |
| `TestSkip_FallbackDiscoveredTransitionsRefused` | FR-013 source class under skip |
| `TestDiscovery_EscapedPathSiblingNotAccepted` / `TestDiscovery_AmbiguousIssuerRefused` | FR-006 |
| `TestBinding_NoPRMTarget_PRMAppears{Same,Different}` | FR-013, US4.7 |
| `TestUpgrade_PathIssuerFallbackRowNotBound` | SC-003, US3.8 |
| `TestBinding_FallbackDiscoveredSameTarget_{Up,Down}` / `TestDCRWrite_SourceClassChangeClearsTokens` | US4.8, US2.12 |
| `TestStaticSecretProvenance_SecretOnlyOverrideSurvivesDCRIDChange` | FR-012 |
| `TestExtraParamsSecretNeverInAuthURL` | FR-010b, US2.13 |
| `TestFinalTokenBody_AssertionAndFlowOwnedOverrides_AtoB` (incl. alias `X-Amz-Security-Token`, fresh assertion with same type at B) | FR-009a, US2.14 |
| `TestUpgrade_NearSealLimitRowFailsClosed` | FR-011a |
| `TestClassifyExtraParam_PrecedenceAndAliases` | FR-010b |
| `TestMixUp_ConfiguredDocClaimsHonestIssuer_{CrossOrigin,SameHostOtherTenant}_NotProtected` | FR-008, US1.5 |
| `TestLegacyExtraParamsClientID_ConsistentBothRequests` | FR-010b, US2.15 |
| `TestStaticSecretProvenance_ExtraParamsSecret` (file-loaded config, swap + logout + re-login) | FR-012 effective credentials |
| provider case: correct issuer behind cross-origin metadata redirect | SC-004 |
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

1. **PR-1 (bump, no new enforcement)**: FR-019, D1 (strict parse), D5 (the DCR `application_type` assertion and, if needed, the registration rewrite), and the fake AS. The only behaviour change is FR-001 and FR-016.
2. **PR-2 (all enforcement, with its escape hatch)**:
   - D0: the resolver, `Snapshot`, and the last-hop guard on all three clients;
   - D3, D2, D4;
   - D6's `skip_issuer_validation` field and resolver branch;
   - the three codes, trust on first use, and the upgrade tests.

   This is the first PR that can refuse a provider that works today, so it ships together with the opt-out.
3. **PR-3 (surfaces, broker, docs)**: D6's broker `issuer`, D7's surfaces and goldens, the Web UI and macOS editor fields, the docs, the e2e script and the provider verification.

Each PR is cross-reviewed (opencode `gpt-6.2-sol`, round cap per the operator rules).

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
