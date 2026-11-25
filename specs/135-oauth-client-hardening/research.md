# Research: Harden the OAuth Client Role (Spec 135)

All facts about mcp-go v1.4.0 come from its `client/transport/oauth.go` at tag `v1.4.0` and the GitHub release notes, both read on 2026-11-25. The implementer MUST re-check each one against the version actually pinned (R1) before relying on it.

## R1. Which mcp-go version

- **Decision**: pin the newest v1.x available when PR-1 starts. On 2026-11-25 that is v1.6.0. The floor is v1.4.0.
- **Rationale**:
  - v1.2.0 adds the RFC 8414 issuer check on metadata (#1004) and the token-endpoint auth method (#1005).
  - v1.3.0 binds client credentials to the issuer (#1089): `OAuthConfig.CredentialsIssuer` and `Token.Issuer`.
  - v1.4.0 adds `ProcessAuthorizationCallback` with the RFC 9207 check, `application_type` (#1003), callbacks bound to their transaction (#1006), and fallback endpoints derived from the origin (#1098).
  - v1.6.0 refuses an empty PKCE challenge or verifier (#1151).
  - No release note is marked breaking.
- **Risk**: v1.2.0 also changed refresh handling (shared refresh #1013, rediscovery after failure #1014, tokens without a type treated as bearer #1015). mcpproxy has many refresh tests (refresh_manager_*, refresh_coordinator_*, persistent_token_store_refresh_*), and they are the regression net. Any behaviour change is either adopted or neutralised, and recorded here when PR-1 lands (FR-019).
- **Alternatives**: patch v1.0.0 behaviour inside `internal/oauth` (rejected by the issue and by us: it duplicates upstream code that the next bump would delete); vendor a fork (rejected: maintenance cost).

## R2. Does `ProcessAuthorizationCallback` fit mcpproxy's flows?

- **Fact**: it needs the `AuthorizationTransaction` that the handler records when it builds the authorization URL. It checks state, then `iss` against the transaction's metadata snapshot, then `error`, then `code`. It skips the `iss` check when mcp-go itself guessed the metadata. With no transaction it fails with `no authorization transaction found; restore it with SetAuthorizationTransaction`.
- **mcpproxy**: all three login paths build the URL and exchange the code on the **same** `*OAuthHandler` instance (`connection_oauth.go`: `handleOAuthAuthorization`, `handleOAuthAuthorizationWithResult`, `getAuthorizationURLQuick` → `waitForOAuthCallbackAsync`). So the in-memory transaction is there, and no `Set/GetAuthorizationTransaction` round trip is needed.
- **Decision**: use `ProcessAuthorizationCallback(ctx, q, verifier)` when the source is `discovered` or `configured` and skip is off. Use `ProcessAuthorizationResponse` otherwise. In both cases mcpproxy's own decision has already run in the callback handler (plan D2). mcp-go treats an empty `iss` as absent, and mcpproxy treats it as a mismatch (R6). Because mcpproxy's check runs first, the stricter rule wins.
- **Gap to check in PR-1**: mcp-go's snapshot comes from the document the transport wrapper serves. For a synthetic document mcp-go does **not** know it is guessed, and would compare `iss` with the synthetic issuer. That is why the synthetic source keeps the legacy call. A test pins this.

## R3. Where the check and the failure page live

- **Fact**: `handleCallbackWithFailureNotification` (`internal/oauth/config.go:2005-2085`) writes the success page right after `deliver` succeeds, before the flow exchanges the code. The flow's result never reaches the page.
- **Decision**: the waiter carries `ExpectedIssuer` and the skip switch. The handler runs the strict parse (FR-001) and the issuer decision (FR-003) itself, before it reports a provider error or chooses the page (plan D2). The flow repeats the check.
- **Remote callback**: it keeps its policy that "an invalid public request must not mark a healthy upstream as failed" (`remote_callback.go:73-75`). Repeated parameters are refused without using up the waiter and without `RecordOAuthFailure`. An `iss` mismatch on a request that carried the right `state` **does** use up the waiter and records the failure: only a party that holds the state can produce one, and an `iss` mismatch is exactly the attack being reported.
- **Alternative**: add a step where the page waits for the flow's verdict (rejected for this spec: it changes how long the browser waits on every login and touches the #975 routing).

## R4. `application_type` mapping

- **Fact**: OpenID Connect Dynamic Client Registration 1.0 §2 defines `application_type` as `web` (the default) or `native`. Web clients MUST use `https` redirect URIs and MUST NOT use `localhost`. Native clients use custom schemes or loopback `http`. RFC 7591 does not define the field, and plain RFC 7591 servers ignore unknown metadata (§2).
- **Decision**: send `native` for loopback or `localhost` redirect hosts and `web` otherwise. PR-1 asserts mcp-go's `applicationType(redirectURI)` (its body is not in `oauth.go`; it lives in another file of the package) against this table. If it disagrees, mcpproxy rewrites the field in the registration body, which the transport wrapper already intercepts.
- **Issue reply**: the issue's "`web`" proposal would tell strict OIDC providers to refuse the loopback redirect. The gap is real; the value is `native`.

## R5. Legacy rows: trust on first use or forced re-login

- **Decision**: trust on first use (TOFU), bound at the first use whose discovery source is `discovered` or `configured`.
- **Rationale**: the issuer of a legacy row cannot be proven after the fact. Forcing a re-login would cost every user one sign-in per server and close only one window: a swap that happened before the upgrade and has not been noticed since. mcp-go makes the same choice ("an empty value enables legacy trust on first use").
- **Spec 123**: the issuer goes inside the sealed body. The additional data (kid ‖ bucket ‖ bucket key) does not change, so the binding write re-seals with the same key and needs no migration step or boot sweep. A row with no issuer marshals to the same bytes as before (`omitempty`), so Spec 123's "same bytes without a key" property holds.
- **Downgrade**: an older binary ignores the field (unknown JSON keys are dropped by `json.Unmarshal` into the struct), and a rewrite by that binary loses it. The next upgrade goes back through TOFU. Documented, accepted.

## R6. String comparison rules

- **`iss`**: exact string compare (RFC 9207 §2.4 refers to RFC 3986 §6.2.1 simple string comparison). A parameter that is present with an empty value is a mismatch, not an absence. Only a parameter that is missing altogether counts as absent.
- **Metadata issuer against the fetched-for URL**: ignore one trailing `/` on either side, matching mcp-go's `strings.TrimSuffix(…, "/")`. Real providers differ on the trailing slash between PRM and metadata, and the check exists to catch a different server, not a different spelling of the same one.
- **Stored issuer against the current one**: exact compare. Both values come from metadata documents that passed R6's metadata rule.

## R7. Synthetic and guessed metadata

- **Fact**: with Spec 113 endpoint overrides, the transport wrapper serves a synthetic document whose `issuer` is the origin of the authorization endpoint (`transport_wrapper.go:608-616`). mcp-go v1.4.0 marks its own fallback endpoints as `guessed` and skips the `iss` check for them.
- **Decision**: source `synthetic`. An absent `iss` is accepted. A present `iss` is logged, with a WARN line when it differs from the synthetic issuer. It is **not** persisted as provenance, because it is an unauthenticated claim. The credential issuer is always the synthetic issuer, which is derived from config. The response is not refused on that difference alone, because mcpproxy cannot vouch for an issuer it made up. The operator who set the overrides pinned the endpoints, and that pin is the trust anchor. An override edit is an OAuth clear (`config.OAuthConfigChanged`), so a synthetic row is only ever compared under the overrides it was written with.

## R8. Opt-out shape

- **Decision**: per server, `oauth.skip_issuer_validation: bool`, default false. It covers FR-003, FR-005 and FR-006 only. There is no global switch.
- **Rationale**: one misbehaving provider must not block a user, and must not lower protection for every other server. Credential binding (FR-009) has no legitimate false positive (a changed issuer is a changed server), so it is never skippable. Repeated parameters have no legitimate use either.
- **Binding identity under skip** (FR-013): the discovery target, meaning the PRM `authorization_servers` entry or the configured metadata URL, and not the document's declared `issuer`. With FR-006 off, the declared issuer is an unchecked claim. A replacement server could declare the old string and inherit the credentials.
- **Cache isolation** (FR-007): the document cache already stores raw bytes, so validation runs per use. The preflight result, which is a validated URL plus a document, is keyed by policy as well, so a strict failure never blocks a skip server and a skip success is never served to a strict one.
- **Alternatives**: a three-state `issuer_validation: strict|lenient|off` (rejected: the middle state has no clear meaning); a global flag (rejected).

## R9. Server-edition broker

- **Fact**: `ConnectorConfig` (`internal/serveredition/broker/oauth_connector.go:36-60`) has static endpoints. There is no discovery and no DCR, and the callback reads `state`/`code`/`error` with `q.Get` (`api/credential_handlers.go:307-323`).
- **Decision**: an optional `issuer` in `auth_broker` config. When it is set, the callback applies FR-001 and FR-003 with source `configured` and `IssSupported=false`: a wrong `iss` is refused and a missing one accepted. When it is not set, the callback applies FR-001 only. The broker's credential store (`bbolt_aes.go`) is per user and keyed per server, with operator-pinned endpoints, so FR-009 binding is not added there (follow-up if the broker ever gains discovery).

## R11. Non-HTTPS issuers and metadata redirects

- **Decision**: metadata fetches follow same-origin redirects only (FR-006a). A non-loopback `http` issuer or endpoint is reported as `insecure_issuer`, not refused.
- **Rationale**: an issuer string matched over plain HTTP, or after a hop to another origin, authenticates nothing. Refusing `http` outright would break LAN providers that work today, with no security gain over telling the operator. The spec's protection claims are stated as void for such servers.
- **Alternative**: require HTTPS with a loopback exception (rejected for compatibility; can be revisited behind the same opt-out).

## R10. Mix-up: what `iss` does and does not cover

- **Attack** (RFC 9700 §4.4.1): a flow for issuer A, where A is hostile. A's metadata points its authorization endpoint at honest H, using a client registration at H whose redirect URI is mcpproxy's loopback callback. Loopback redirect URIs belong to nobody, so anyone can register them, and per-server callback ports give no protection. H redirects back with `state_A` and `code_H`. Without `iss`, mcpproxy sends `code_H` **and the PKCE verifier** to A's token endpoint, and A redeems them at H. `state` routing (#975) and PKCE do not stop this.
- **Defence**: FR-003 refuses any `iss` that differs from A. H sends `iss=H`, so the response is refused **whatever A advertises**. The advertised flag matters only for an absent `iss`, which A could fake.
- **Residual**: if H does not send `iss`, the client cannot tell. RFC 9207 lets clients accept an absent `iss` from servers that do not advertise it. The only other client-side defence in RFC 9700 §4.4.2 is per-AS redirect URIs, and it fails here for the reason above. mcpproxy therefore marks such logins `mix_up_protected: false` and shows it (FR-018).
- **Swap without a user** (FR-009/FR-010): covered by issuer binding and the redirect policy, independently of `iss`.
