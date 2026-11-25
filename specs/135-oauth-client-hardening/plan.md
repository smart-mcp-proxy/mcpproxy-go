# Implementation Plan: Harden the OAuth Client Role

**Branch**: `135-oauth-client-hardening` | **Date**: 2026-11-25 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/135-oauth-client-hardening/spec.md`

## Summary

Bump `github.com/mark3labs/mcp-go` from v1.0.0 to the latest v1.x (at least v1.4.0, target v1.6.x), then wire the three fixes through mcpproxy's own seams. The mcp-go fixes alone do not engage there, because mcpproxy:

- feeds mcp-go an explicit metadata URL,
- exchanges codes through `ProcessAuthorizationResponse`, which does not check `iss`, and
- refreshes stored credentials with its own HTTP request.

The work comes in four parts:

- an issuer snapshot attached to each pending flow, with an `iss` check owned by mcpproxy at the single code-exchange seam;
- an issuer-equality check in the discovery preflight;
- `issuer` and `issuer_source` fields inside the sealed `OAuthTokenRecord` body, checked before every use of a stored credential, with trust-on-first-use (TOFU) binding of rows written before this change;
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

### D1. Callback delivers `url.Values` (FR-001)

- `CallbackServer.deliver(state, params map[string]string)` (`internal/oauth/config.go`) changes to `deliver(state, query url.Values)`. The waiter channel type changes the same way. Logging stays as it is (first values, scrubbed), with one added `iss_present` bool.
- A new helper `oauth.RejectRepeatedParams(q url.Values) error` rejects a repeated `code`, `state`, `iss` or `error`. It runs in `handleCallbackWithFailureNotification` **before** `deliver`, so a malformed request cannot use up the waiter. The remote callback's inline check (`remote_callback.go:69`) is replaced by the same helper, and its check that `code` and `error` are not both set is kept.
- The three consumers in `connection_oauth.go` (`:1348`, `:1621`, `:2409`) read `q.Get("code")` and `q.Get("state")`, and pass `q` into `exchangeAuthorizationCode`.

### D2. Issuer snapshot and `iss` check (FR-002–FR-005)

- New file `internal/oauth/issuer.go`:
  - `type IssuerSource string` with values `discovered`, `configured`, `synthetic`.
  - `type ExpectedIssuer struct { Issuer string; Source IssuerSource; IssSupported bool }`.
  - `func CheckAuthorizationResponseIssuer(q url.Values, exp ExpectedIssuer, skip bool) (recorded string, err error)`. It returns `*IssuerMismatchError`, which wraps `diagnostics.OAuthIssuerMismatch`.
  - Rules: an empty `iss` counts as absent. `discovered` and `configured` follow FR-003. `synthetic` follows FR-004 and returns the received `iss` as `recorded`. With `skip`, the function logs at WARN and returns the issuer it expected.
- The issuer snapshot is captured when the authorization URL is built. `CreateOAuthConfig…` already resolves `authServerMetadataURL` and `asDoc` (`config.go:1385-1421`). It now also returns `ExpectedIssuer` with these fields:
  - `Issuer`: `asDoc.meta.Issuer`
  - `IssSupported`: `asDoc.meta.AuthorizationResponseIssParameterSupported` (a new field on the local `discovery.go` metadata struct)
  - `Source`: `configured` when `ov.metadataURL != ""`, `synthetic` when the transport wrapper serves a synthetic document (`transport_wrapper.go:608-616`) or when no document was found, `discovered` otherwise.
- The snapshot is stored with the flow state, in the same struct that already holds `state` and `codeVerifier`. That struct is `OAuthStartResult` / the waiter context in `connection_oauth.go`.
- `exchangeAuthorizationCode(ctx, h, cb, q url.Values, exp ExpectedIssuer, verifier)` works in this order:
  1. `CheckAuthorizationResponseIssuer`
  2. the provider `error` (FR-005)
  3. for `discovered`/`configured` without skip: `h.ProcessAuthorizationCallback(ctx, q, verifier)`, so mcp-go runs its own check as well
  4. otherwise: `h.ProcessAuthorizationResponse(ctx, code, state, verifier)`
  5. the issuer used is passed to the token write (D4).

  Research R2 confirms that `ProcessAuthorizationCallback` works with the handler's in-memory transaction on all three paths. If it does not on one path, that path keeps the legacy call. mcpproxy's own check is the requirement either way.
- Callback page and failure record. Today the callback handler writes the success page as soon as it has delivered the query, before the exchange (`config.go:2082`), and there is no step that waits for a verdict. So the check also has to run **in the callback handler**, before the page is written:
  - The waiter registered for a `state` now carries its `ExpectedIssuer` and the server's skip switch (`waiters map[string]*callbackWaiter{ch, exp, skip}`).
  - `handleCallbackWithFailureNotification` takes the waiter (still atomically, once), runs `RejectRepeatedParams` and `CheckAuthorizationResponseIssuer`, and on failure:
    - delivers a `callbackResult{err}` to the flow, so the flow ends with the typed error and does no exchange;
    - writes the FR-016 page;
    - calls `RecordOAuthFailure(server, err)` with the code. On the remote callback this happens only when the request passed FR-001; see R3.
  - The flow repeats the check in `exchangeAuthorizationCode`, as defence in depth and for the broker, which has no callback server.

  Research R3 records this.

### D3. Discovery issuer equality (FR-006, FR-007)

- In `findWorkingMetadataDoc(env, issuerURL, …)` (`internal/oauth/discovery.go`), after a document parses, compare `trimOneSlash(doc.Issuer)` with `trimOneSlash(expectedIssuerFor(candidateURL, issuerURL))`. The expected issuer is the PRM `authorization_servers[0]` value when PRM produced it. Otherwise it is the issuer URL that the candidate well-known URL was built from: `authorizationServerMetadataURLs` builds each candidate from one issuer URL, so the mapping is known.
- On mismatch, return `*MetadataIssuerMismatchError` (`diagnostics.OAuthMetadataIssuerMismatch`) at once. Do not try the next candidate. Do not write the seed into the cache (`preflightMetadataURL` already discards seeds on error). The result is cached with `discoveryFailureTTL`, like any other discovery failure.
- A missing `issuer` is a failure in every source (FR-007), except for synthetic documents, which mcpproxy builds itself.
- `skip_issuer_validation` bypasses the equality compare only (WARN line). Keep `SkipIssuerMetadataValidation` in the mcp-go `OAuthConfig` in step with the server's switch, for the paths where mcp-go fetches metadata itself.

### D4. Issuer-bound rows (FR-008–FR-011)

- `storage.OAuthTokenRecord` gains `Issuer string \`json:"issuer,omitempty"\`` and `IssuerSource string \`json:"issuer_source,omitempty"\``. Both go inside the body that `encodeOAuthRecord` seals, so the AAD does not change and Spec 123 needs no change. Records with empty fields keep producing the same JSON bytes, so the "same bytes without a key" property of Spec 123 still holds.
- Writers:
  - The login write (`WithLoginClient` / `persistCompletedDCRCredentials`, `UpdateOAuthClientCredentials*`) takes the issuer from the exchange (D2).
  - `PersistentTokenStore.SaveToken` copies `Token.Issuer` from mcp-go v1.3+ when it is set. Otherwise it keeps the row's existing issuer and never clears it.
  - `PersistentTokenStore.GetToken` returns `Token.Issuer` from the row, because mcp-go requires the store to preserve it.
- A new guard, `oauth.CheckStoredIssuer(rec, current ExpectedIssuer) (bindNow bool, err error)`, applies these rules in order:
  - an undecryptable row is handled first, as today;
  - `rec.Issuer == ""` with `current.Source` in {discovered, configured} → TOFU, `bindNow = true`;
  - an equal issuer → ok;
  - a synthetic row compared with a synthetic issuer → ok when equal. They cannot differ in practice, because `config.OAuthConfigChanged` (`internal/config/config.go:3422`) already counts every endpoint-override and metadata-URL edit as an OAuth clear: the epoch moves and the old row is never read under the new overrides;
  - otherwise → `*IssuerChangedError` (`diagnostics.OAuthIssuerChanged`).
- Call sites, all **before** any request is sent:
  - `refreshWithStoredCredentials` (`internal/upstream/core/oauth_refresh.go:124`), before `refreshTokenWithStoredCredentials`
  - `oauthRefreshOnce` / `oauthRefreshFunc` (`:66-104`), where the token endpoint is taken
  - `RefreshManager` attempt entry (`internal/oauth/refresh_manager.go`)
  - DCR reuse in `config.go:1189-1196` and `:1505-1514`: an issuer change is treated like the Spec 022 stale-registration case. The stored `client_id` is not used. The row is not deleted here; the next login write replaces it (FR-011).
  - the code-exchange path (a reused registration must match the issuer just checked)
- To set the mcp-go `OAuthConfig.CredentialsIssuer`, use the row's issuer when one is bound, and leave it empty otherwise, which gives mcp-go's legacy TOFU. This lines up mcp-go's own v1.3 refresh check with ours.
- TOFU write: a record-only, binding-conditional write (`storage.BindOAuthIssuer(serverKey, binding, revision, issuer, source)`) that runs only if `Revision` is unchanged and the row is not undecryptable. It re-seals with the configured key. Without a key, it writes plaintext JSON as today. It is never part of a hot read path, and it runs at most once per row.
- An access token from an issuer-changed row is not attached to upstream requests. `PersistentTokenStore.GetToken` returns `ErrOAuthAuthorizationRequired` wrapped with the code, so mcp-go stops and health shows "login".

### D5. DCR `application_type` (FR-014)

- After the bump, assert that mcp-go's `applicationType(redirectURI)` returns `native` for `127.0.0.1`, `[::1]` and `localhost` hosts, and `web` otherwise. Research R4 records the check. If mcp-go's mapping differs, mcpproxy's transport wrapper (which already intercepts registration for Spec 113 overrides, `transport_wrapper.go:408-438`) rewrites the field on the outgoing registration body. A test pins the value either way.

### D6. Opt-out (FR-012, FR-013)

- `config.OAuthConfig.SkipIssuerValidation bool \`json:"skip_issuer_validation,omitempty"\``, wired through these 5 points (project memory: config-field wiring):
  1. config struct and validation, and **excluded** from `config.OAuthConfigChanged`. Like `RefreshReplayEnabled` it is request policy, not credential identity, so toggling it never clears a grant or moves the epoch.
  2. a `ServerFieldMaskDecisions` entry (`internal/oauth/serverconfigfields.go`, non-secret), so the redaction walk test passes
  3. OAS / `make swagger`
  4. Web UI server editor OAuth section
  5. macOS server editor

  It also hot-reloads.
- The broker (`//go:build server`) gets `ConnectorConfig.Issuer string` from `config.AuthBrokerConfig.Issuer`. `Complete` gains a `q url.Values` overload, and the handler calls `RejectRepeatedParams` and then `CheckAuthorizationResponseIssuer` with source `configured` and `IssSupported=false`, so a wrong `iss` is refused and a missing one is accepted.

### D7. Surfaces (FR-015–FR-018)

- `internal/diagnostics/codes.go`: `OAuthIssuerMismatch`, `OAuthIssuerChanged`, `OAuthMetadataIssuerMismatch`, with their catalog entries (title, health level, action) and `docs/errors/<CODE>.md` pages. The pages are checked by the existing docs-coverage test for codes.
- Health mapping (`internal/upstream/types` / the health calculator): `ISSUER_CHANGED` → level unhealthy, action `login` (same as `REAUTH_REQUIRED`). `METADATA_ISSUER_MISMATCH` → action `configure`. `ISSUER_MISMATCH` → recorded OAuth failure, action `login`.
- Doctor: `management.BuildOAuthTokenStorageReport` (Spec 123) gains `issuer_binding {bound, unbound, changed: [server], validation_disabled: [server]}`. Scoped callers get counts limited to their reach.
- `auth status`, `GET /api/v1/servers/{id}` OAuth block, Web UI ServerDetail, tray detail: `issuer` (shown as origin) and `issuer_source`.

## Test plan

**Fake authorization server**: `internal/oauth/oauthtest/fakeas.go` (test-only package). It is one `httptest.Server` that can act as a protected resource (PRM), an AS (RFC 8414 + openid-configuration), and registration, authorize and token endpoints. Its switches are:

- `AdvertiseIss bool`
- `IssMode {Correct, Omit, Wrong, Repeat, Empty}`
- `MetadataIssuer string` (overrides the declared issuer)
- `PRMAuthServers []string`, which can be changed at run time to simulate a swap
- `RejectApplicationType bool`
- a request log of every registration body and every token request (grant type, client id, refresh token)

The authorize endpoint returns a 302 to the redirect URI, so the real callback server is exercised end to end. Two instances (AS-A and AS-B) behind one PRM cover the swap and mix-up cases.

| Test | Covers |
|---|---|
| `TestIssCheck_Matrix` (unit, `issuer_test.go`) | FR-003/004/005: every combination of source × advertised flag × iss mode × skip |
| `TestCallback_RepeatedParamsRejectedBeforeDeliver` (loopback + remote) | FR-001, the waiter is not used up |
| `TestLogin_IssMismatch_NoTokenRequest` (fake AS, real callback, `internal/upstream/core`) | US1.2/1.3, SC-001, page text, failure code |
| `TestLogin_IssAbsentNotAdvertised_Succeeds` | US1.4 |
| `TestLogin_ErrorResponseWrongIss` | US1.6 |
| `TestDiscovery_MetadataIssuerMismatch_{PRM,Fallback,OIDC}` | US4, FR-006, no cache seed |
| `TestDiscovery_ConfiguredMetadataURL_IssuerAsDeclared` | FR-007 |
| `TestSwap_NoCredentialReachesNewAS` (proactive, reactive, stored-credential refresh, re-login) | US2, SC-002 |
| `TestSwap_StaticClientIDKept` | US2.4 |
| `TestUpgrade_LegacyRowsBindOnFirstUse_{Plain,Sealed}` | US3, SC-003, the sealed row stays sealed with the same AAD |
| `TestUpgrade_DiscoveryFailsLeavesUnbound` | US3.2 |
| `TestUndecryptableBeforeIssuer` | US2.5 |
| `TestDCR_ApplicationType_{IPv4,IPv6,Localhost,Remote}` | US5, FR-014 |
| `TestSkipIssuerValidation_DoesNotSkipBinding` | US6.2, FR-012 |
| `TestBroker_Iss_{Set,Unset}` (`-tags server`) | US7 |
| `TestRecordJSONUnchangedWithoutIssuer` | Spec 123 byte stability |
| `TestOAuthIssuerChecks_AllExchangePaths` (AST guard, as Spec 126 does) | every caller of `ProcessAuthorizationResponse` / `ProcessAuthorizationCallback` in `internal/` goes through `exchangeAuthorizationCode` |
| Doctor/diagnostics golden + scoped variant | FR-017 |
| `scripts/test-oauth-client-hardening-e2e.sh` | built binary against the fake AS run as a stand-alone helper (`cmd/fakeas` under `internal/oauth/oauthtest/cmd`, test build only): login, swap, upgrade from a seeded v-previous `config.db` |

**Real-provider check** (manual, recorded in `specs/135-oauth-client-hardening/verification/providers.md`): sign in with default settings against the providers in the existing OAuth docs examples (Cloudflare, Smithery, GitHub-MCP, Notion, Atlassian, Sentry). Record whether each one advertises `iss` support, whether it sends `iss`, its declared issuer against its URL, and its DCR answer to `application_type`. SC-004 is met when none of them needs the opt-out, or each one that does is documented.

## Delivery

1. **PR-1 (bump + `iss` + `application_type`)**: mcp-go bump with regression fixes (FR-019), D1, D2, D5, the `ISSUER_MISMATCH` code, the fake AS. Behaviour change: only callbacks that are actually wrong get refused.
2. **PR-2 (binding + discovery equality)**: D3, D4, `ISSUER_CHANGED` and `METADATA_ISSUER_MISMATCH`, TOFU, the upgrade tests.
3. **PR-3 (surfaces + opt-out + broker)**: D6, D7, docs, e2e script, provider verification.

PR-1 and PR-2 can each ship alone. PR-3's opt-out should land in the same release as PR-2, because PR-2 is the first change that can refuse a provider which works today (metadata equality). Each PR is cross-reviewed (opencode `gpt-6.2-sol`), with the round cap from the operator rules.

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
internal/oauth/          issuer.go (new), config.go (callback, preflight), discovery.go, remote_callback.go,
                         persistent_token_store.go, refresh_manager.go, serverconfigfields.go,
                         transport_wrapper.go (only if R4 needs it), oauthtest/ (new, test-only)
internal/upstream/core/  connection_oauth.go (exchange seam, waiters), oauth_refresh.go
internal/storage/        models.go (2 fields), oauth_binding.go (BindOAuthIssuer)
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
| Own `iss` check in addition to mcp-go's | mcp-go cannot tell synthetic metadata apart, and the broker does not use mcp-go | Relying on mcp-go alone would refuse logins through Spec 113 overrides and leave the broker unchecked |
| TOFU binding write | No forced re-login (US3) | Re-login for every server on upgrade was rejected in the spec |
