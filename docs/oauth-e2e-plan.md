# OAuth E2E & Observability Plan

## Goals
- Exercise real HTTP OAuth paths end-to-end inside mcpproxy (no mocks), covering discovery, auth code + PKCE, device code, client credentials, refresh, RFC 8707 resource, and dynamic client registration (DCR).
- Detect regressions quickly with automated Go + Playwright/API suites.
- Provide actionable diagnostics to users (CLI + logs) to debug OAuth issues.

## References to study (go-sdk parity)
- We stay on `mark3labs/mcp-go` for mcpproxy, but use go-sdk tests as a primer for expected OAuth flows and fixtures.
- Key go-sdk files worth mirroring for behaviors and test shapes:
  - Auth code + PKCE flow helpers: `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/oauth2.go` and tests `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/oauth2_test.go`.
  - Discovery/resource metadata handling: `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/auth_meta.go`, `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/resource_meta.go`, tests `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/auth_meta_test.go`.
  - Dynamic client registration flow: `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/dcr.go`, tests `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/dcr_test.go`.
  - Package orchestrator and shared fixtures: `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/oauthex.go`, tests `https://github.com/modelcontextprotocol/go-sdk/blob/main/oauthex/oauthex_test.go`, plus `https://github.com/modelcontextprotocol/go-sdk/tree/main/oauthex/testdata` (auth metadata samples like Google/client-auth).
  - (If device code coverage exists) scan `oauthex` tests for device grant handling patterns to align our local test server endpoints.
- Mirror token minting and discovery shapes so our e2e harness matches SDK expectations (authorization_endpoint, token_endpoint, jwks_uri, registration_endpoint, device_authorization_endpoint, resource handling).

## Deliverables
- Reusable local OAuth test server package (Go) with toggles for flows and error modes.
- Go integration tests using that server (detection, resource indicator, DCR, device code, refresh, JWKS rotation, failure cases).
- E2E script wiring (bash + Playwright/API) to run the server, start mcpproxy, and assert UI/CLI behavior.
- Observability enhancements verified by tests: richer logs, `auth status` / `auth login` surfaces, doctor checks.

## Local OAuth test server design
- Serve discovery: `/.well-known/openid-configuration` and `/.well-known/oauth-authorization-server` (authorization, token, jwks, registration, device endpoints, supported scopes/grants).
- Auth code flow: `/authorize` accepts PKCE + optional `resource`; redirects with `code` + `state`.
- Token endpoint: supports `authorization_code`, `refresh_token`, `client_credentials`, `urn:ietf:params:oauth:grant-type:device_code`; emits JWT access tokens and optional refresh; echo `resource` into `aud`.
- DCR: `/registration` issues client_id/client_secret, remembers allowed redirects/scopes.
- Device code: `/device_authorization` + `/device_verification` with toggles for pending/approved/denied.
- Introspection and error toggles: configurable responses (invalid_client, invalid_scope, invalid_grant, slow/500) plus JWKS rotation for key churn.
- Configurable hints for detection: `WWW-Authenticate` on protected resource endpoint, discovery-only mode, or explicit endpoints.

## Test matrix (prioritized)
- Detection: mcpproxy discovers OAuth from 401 `WWW-Authenticate`, from well-known metadata, and from explicit config.
- Auth code + PKCE happy path: token stored, refresh path exercised.
- Resource indicator (RFC 8707): `resource` sent on authorize/token; token carries correct audience; propagated to upstream calls.
- Dynamic client registration: register, then perform auth code with issued credentials.
- Device code: poll behavior, approval/denial, timeout handling.
- Client credentials: direct token fetch with resource and scopes.
- JWKS rotation: old kid rejected, new kid accepted.
- Error handling: wrong code_verifier, invalid_client, unsupported_grant, expired tokens, slow/500 token endpoint with retries/telemetry.
- Logging/observability: logs include auth URL (sans secrets), token request shape (masked), resource indicator, scopes, PKCE flag; CLI surfaces show config/status; doctor reports misconfig/errors.

### Token refresh coverage
- Automatic refresh before expiry: ensure mcpproxy refreshes when access token is near expiration and continues to serve requests without user interaction.
- Refresh failure paths: simulate invalid_refresh_token/invalid_client and confirm mcpproxy surfaces actionable errors and does not loop endlessly.
- Refresh retry/backoff: validate behavior when token endpoint is temporarily 500/slow; assert telemetry/log fields.
- Refresh + resource indicator: verify `resource` persists on refresh requests and audience in new token.

## Automation wiring
- Go helper `tests/oauthserver` exporting `Start(t *testing.T, opts Options)` returning issuer URL, client creds, JWKS. Options cover flow toggles and error modes.
- Go tests in `internal/httpapi` and `internal/runtime` using helper to hit real HTTP handlers (server login, callback, token refresh).
- E2E bash entrypoint (e.g., `scripts/run-oauth-e2e.sh`): start OAuth server, launch mcpproxy with config pointing at it, run Playwright/API suites for login, device approval, resource handling, DCR.
- CI job to run OAuth suite (behind flag to keep runtime reasonable).

## Browser login workflow (auth code UI)
- Test OAuth server renders a login/consent UI when `/authorize` is hit without prompt=none: username/password fields + consent checkbox, with toggles for failure states (bad password, consent denied, MFA placeholder).
- Playwright spec drives the page opened by `mcpproxy auth login` (or API-triggered login): fill credentials, approve consent, submit, and assert redirect lands on the mcpproxy callback with code/state intact.
- Validate mcpproxy stores tokens and `auth status` reflects authenticated state; also assert logs emitted auth URL preview and PKCE/resource parameters.
- Negative paths: wrong password (expect error page and no token), consent denied (expect error=access_denied), abandoned flow timeout.

## Observability tasks to verify via tests
- `mcpproxy auth status`: shows endpoints, scopes, resource, PKCE, expiry, last refresh; masks secrets.
- `mcpproxy auth login`: prints authorization URL preview with extra params (resource) before browser open.
- Logging: structured fields for provider URL, resource, scopes, grant type, PKCE, DCR outcomes, token expiry; errors include provider error bodies.
- `mcpproxy doctor`: OAuth check that validates config, discovery reachability, and emits actionable hints.

## Milestones
1) Map go-sdk OAuth test shapes and align server responses.
2) Implement `tests/oauthserver` harness with feature toggles and deterministic keys.
3) Add Go integration tests for detection, resource, DCR, device, and failure cases.
4) Wire e2e script + Playwright/API assertions using the harness.
5) Add/verify observability outputs; backfill tests for logs/CLI surfaces.
6) CI job + docs: add run instructions to MANUAL_TESTING.md and scripts README.
