# Feature Specification: OAuth E2E Testing & Observability

**Feature Branch**: `007-oauth-e2e-testing`
**Created**: 2025-12-02
**Status**: Draft
**Input**: OAuth E2E & Observability Plan - Exercise real HTTP OAuth paths end-to-end inside mcpproxy (no mocks)

## User Scenarios & Testing *(mandatory)*

<!--
  IMPORTANT: User stories should be PRIORITIZED as user journeys ordered by importance.
  Each user story/journey must be INDEPENDENTLY TESTABLE - meaning if you implement just ONE of them,
  you should still have a viable MVP (Minimum Viable Product) that delivers value.

  Assign priorities (P1, P2, P3, etc.) to each story, where P1 is the most critical.
  Think of each story as a standalone slice of functionality that can be:
  - Developed independently
  - Tested independently
  - Deployed independently
  - Demonstrated to users independently
-->

### User Story 1 - Developer Runs OAuth E2E Tests Locally (Priority: P1)

As a developer working on mcpproxy OAuth features, I want to run comprehensive end-to-end tests locally that exercise real HTTP OAuth paths (discovery, auth code + PKCE, token refresh) so that I can verify my changes work correctly before committing.

**Why this priority**: This is the foundational capability that enables all other testing. Without a working local OAuth test server and basic test suite, developers cannot confidently develop or debug OAuth features.

**Independent Test**: Can be fully tested by running `go test ./tests/oauthserver/...` and verifying that auth code + PKCE flow completes successfully with real HTTP calls. Delivers immediate value by catching OAuth regressions before code reaches CI.

**Acceptance Scenarios**:

1. **Given** the test harness is initialized, **When** a test calls `Start(t, opts)`, **Then** the harness starts an HTTP server that responds to OAuth discovery endpoints (`/.well-known/oauth-authorization-server`, `/.well-known/openid-configuration`) with valid metadata including authorization, token, and JWKS endpoints.

2. **Given** the test OAuth server is running, **When** a test performs the auth code + PKCE flow (redirect to `/authorize`, receive callback with code, exchange code at token endpoint), **Then** the flow completes and returns valid JWT access and refresh tokens.

3. **Given** the test OAuth server is running, **When** a test uses a refresh token at the token endpoint, **Then** a new access token is returned while preserving the resource audience claim.

---

### User Story 2 - Developer Tests OAuth Detection Methods (Priority: P1)

As a developer, I want to verify that mcpproxy correctly discovers OAuth requirements from multiple sources (401 WWW-Authenticate header, well-known metadata, explicit config) so that I can ensure broad compatibility with OAuth providers.

**Why this priority**: OAuth detection is critical for mcpproxy to automatically authenticate with upstream servers. Testing all detection methods ensures users don't need manual configuration for standard OAuth providers.

**Independent Test**: Can be tested by running detection-focused tests that configure the harness in different modes (WWW-Authenticate only, discovery-only, explicit config) and asserting mcpproxy extracts endpoints correctly.

**Acceptance Scenarios**:

1. **Given** the OAuth server is configured to return a 401 with `WWW-Authenticate: Bearer realm="test", authorization_uri="..."`, **When** mcpproxy connects to a protected resource, **Then** mcpproxy discovers the authorization endpoint from the header and initiates OAuth flow.

2. **Given** the OAuth server only exposes `/.well-known/oauth-authorization-server`, **When** mcpproxy fetches metadata, **Then** it correctly parses the response and identifies all supported endpoints and grant types.

3. **Given** explicit OAuth endpoints are specified in mcpproxy config, **When** mcpproxy attempts authentication, **Then** it uses the configured endpoints without attempting discovery.

---

### User Story 3 - Developer Tests Browser Login Workflow (Priority: P2)

As a developer, I want automated browser tests that drive the `mcpproxy auth login` flow through a login UI so that I can verify the full user-facing authentication experience including consent and error handling.

**Why this priority**: While auth code flow can be tested programmatically, the browser-based login experience is what users actually see. Playwright tests catch UI regressions and ensure the callback redirect works correctly.

**Independent Test**: Can be tested by running Playwright specs that navigate to the authorization URL, fill credentials, grant consent, and verify tokens are stored and `auth status` reflects authenticated state.

**Acceptance Scenarios**:

1. **Given** the test OAuth server renders a login UI at `/authorize`, **When** Playwright fills valid credentials and approves consent, **Then** the browser redirects to mcpproxy's callback URL with a valid authorization code and state parameter.

2. **Given** Playwright submits an invalid password, **When** the test OAuth server receives the login attempt, **Then** an error page is displayed and no authorization code is issued.

3. **Given** the user denies consent on the consent screen, **When** the OAuth server redirects, **Then** the callback includes `error=access_denied` and mcpproxy surfaces an appropriate error message.

---

### User Story 4 - Developer Tests RFC 8707 Resource Indicator (Priority: P2)

As a developer, I want to test that mcpproxy correctly sends the `resource` parameter during OAuth flows (RFC 8707) and that tokens contain the appropriate audience claim, so that multi-tenant OAuth scenarios work correctly.

**Why this priority**: Resource indicators are essential for OAuth deployments with multiple protected resources. Verifying this flow ensures mcpproxy can work with enterprise OAuth providers that require audience-restricted tokens.

**Independent Test**: Can be tested by configuring the harness with resource indicator support enabled and verifying that authorize/token requests include the `resource` parameter and issued tokens have the correct `aud` claim.

**Acceptance Scenarios**:

1. **Given** mcpproxy is configured with a resource indicator value, **When** the authorization request is sent, **Then** the `resource` parameter is included in the authorization URL.

2. **Given** a token request includes the `resource` parameter, **When** the OAuth server issues tokens, **Then** the JWT access token contains an `aud` claim matching the resource value.

3. **Given** a refresh token is used with the same resource indicator, **When** the token endpoint issues new tokens, **Then** the resource parameter is sent and the new access token preserves the audience claim.

---

### User Story 5 - Developer Tests Dynamic Client Registration (Priority: P2)

As a developer, I want to test Dynamic Client Registration (DCR) flows where mcpproxy registers itself with an OAuth server and then authenticates using the dynamically issued credentials.

**Why this priority**: DCR enables zero-configuration OAuth setup for users. Testing this flow ensures mcpproxy can work with providers that require dynamic registration before authentication.

**Independent Test**: Can be tested by running DCR-focused tests that POST to `/registration`, receive client credentials, and then perform auth code flow with those credentials.

**Acceptance Scenarios**:

1. **Given** the OAuth server supports DCR at `/registration`, **When** mcpproxy registers with redirect URIs and requested scopes, **Then** the server returns `client_id`, `client_secret`, and registered metadata.

2. **Given** mcpproxy has dynamically registered credentials, **When** it performs the auth code flow, **Then** authentication succeeds using the issued `client_id` and `client_secret`.

3. **Given** DCR registration fails (e.g., unsupported scope), **When** mcpproxy attempts registration, **Then** an appropriate error is surfaced with the OAuth error response body.

---

### User Story 6 - Developer Tests Device Code Flow (Priority: P2)

As a developer, I want to test the device authorization grant (device code flow) for headless/CLI scenarios, including polling behavior and approval/denial states.

**Why this priority**: Device code flow is the recommended pattern for CLI tools where opening a browser for callback isn't feasible. Testing this ensures mcpproxy works correctly on headless servers.

**Independent Test**: Can be tested by running device code tests that initiate the flow, poll for approval status, and verify tokens are issued after simulated user approval.

**Acceptance Scenarios**:

1. **Given** the OAuth server supports device authorization at `/device_authorization`, **When** mcpproxy requests a device code, **Then** it receives `device_code`, `user_code`, `verification_uri`, and `interval` values.

2. **Given** the device code is pending approval, **When** mcpproxy polls the token endpoint, **Then** it receives `authorization_pending` and retries after the specified interval.

3. **Given** the user approves the device code via `/device_verification`, **When** mcpproxy polls again, **Then** it receives valid access and refresh tokens.

4. **Given** the user denies the device code, **When** mcpproxy polls, **Then** it receives `access_denied` and stops polling.

---

### User Story 7 - Developer Tests OAuth Error Handling (Priority: P2)

As a developer, I want to test OAuth error scenarios (invalid credentials, wrong PKCE verifier, expired tokens, token endpoint errors) so that I can verify mcpproxy surfaces actionable error messages and handles failures gracefully.

**Why this priority**: OAuth errors are common in production. Testing error paths ensures users receive clear diagnostics rather than cryptic failures, reducing support burden.

**Independent Test**: Can be tested by configuring the harness to return specific error responses and asserting mcpproxy logs and CLI output contain actionable error information.

**Acceptance Scenarios**:

1. **Given** the token endpoint returns `invalid_client`, **When** mcpproxy exchanges an authorization code, **Then** logs include the error code and description, and CLI output suggests checking credentials.

2. **Given** the code verifier doesn't match the code challenge, **When** mcpproxy attempts token exchange, **Then** the `invalid_grant` error is surfaced with PKCE-related context.

3. **Given** the token endpoint returns 500 errors, **When** mcpproxy attempts token operations, **Then** it retries with appropriate backoff and logs retry attempts with timing.

4. **Given** a refresh token is invalid or revoked, **When** mcpproxy attempts refresh, **Then** it surfaces an error indicating re-authentication is required without entering an infinite retry loop.

---

### User Story 8 - Developer Tests JWKS Rotation (Priority: P3)

As a developer, I want to test that mcpproxy handles JWKS (JSON Web Key Set) rotation correctly, accepting new signing keys while rejecting tokens signed with rotated/removed keys.

**Why this priority**: Production OAuth providers rotate signing keys periodically. Testing this ensures mcpproxy continues working across key rotations without service interruption.

**Independent Test**: Can be tested by issuing a token with key ID "kid-1", rotating JWKS to "kid-2", and verifying the old token is rejected while new tokens work.

**Acceptance Scenarios**:

1. **Given** a token was signed with key ID "kid-1", **When** the JWKS is rotated to only include "kid-2", **Then** validation of the old token fails with a key-not-found error.

2. **Given** JWKS includes both old and new keys during rotation, **When** tokens signed with either key are validated, **Then** both are accepted.

---

### User Story 9 - Developer Verifies OAuth Observability (Priority: P2)

As a developer, I want enhanced CLI and log outputs for OAuth flows so that users can diagnose authentication issues without deep OAuth knowledge.

**Why this priority**: Users frequently struggle to debug OAuth issues. Rich observability reduces support burden and empowers users to self-diagnose.

**Independent Test**: Can be tested by running OAuth flows and asserting that `auth status` output, log entries, and `doctor` checks contain the expected diagnostic information.

**Acceptance Scenarios**:

1. **Given** an OAuth flow is in progress, **When** `mcpproxy auth login` is invoked, **Then** it prints a preview of the authorization URL with parameters (resource, scopes) before opening the browser, with secrets masked.

2. **Given** OAuth tokens are stored, **When** `mcpproxy auth status` is invoked, **Then** it displays endpoints, scopes, resource indicator, PKCE usage, token expiry time, and last refresh time with secrets masked.

3. **Given** OAuth is misconfigured or unreachable, **When** `mcpproxy doctor` is invoked, **Then** it reports specific issues (missing scopes, unreachable discovery endpoint, invalid client credentials) with actionable hints.

4. **Given** any OAuth operation occurs, **When** logs are emitted, **Then** structured fields include provider URL, resource indicator, scopes, grant type, PKCE flag, DCR outcomes, and token expiry. Error logs include the provider's error response body.

---

### User Story 10 - Developer Runs OAuth Suite in CI (Priority: P3)

As a developer, I want the OAuth E2E test suite to run in CI with reasonable execution time so that regressions are caught automatically on every pull request.

**Why this priority**: Automated CI testing is essential for maintaining code quality. Running the OAuth suite in CI catches regressions before they reach production.

**Independent Test**: Can be tested by running the OAuth E2E script in CI mode and verifying it completes within acceptable time limits with clear pass/fail reporting.

**Acceptance Scenarios**:

1. **Given** a PR is submitted, **When** CI runs the OAuth test suite, **Then** it completes within 5 minutes and reports pass/fail status.

2. **Given** the OAuth suite fails, **When** developers review CI logs, **Then** they can identify which specific test case failed and see relevant error context.

---

### Edge Cases

- What happens when the OAuth server is slow to respond (>10 seconds)?
  - Tests should use timeout configurations and verify retry behavior with backoff.

- What happens when network connectivity is lost mid-OAuth-flow?
  - Tests should verify graceful handling and appropriate error messages.

- What happens when the authorization code expires before exchange?
  - Tests should verify `invalid_grant` is surfaced with helpful context.

- What happens when the user abandons the browser login flow (closes tab)?
  - Tests should verify timeout handling and appropriate cleanup.

- What happens when multiple simultaneous OAuth flows are initiated?
  - Tests should verify state parameter isolation prevents cross-flow interference.

- What happens when the OAuth server returns malformed JSON?
  - Tests should verify parsing errors are logged with context and don't crash mcpproxy.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Test harness MUST provide a reusable local OAuth server package with a `Start(t *testing.T, opts Options)` function that returns issuer URL, client credentials, and JWKS reference.

- **FR-002**: Test OAuth server MUST serve discovery metadata at `/.well-known/oauth-authorization-server` and `/.well-known/openid-configuration` containing all standard endpoints (authorization, token, jwks, registration, device_authorization).

- **FR-003**: Test OAuth server MUST implement auth code flow with PKCE support at `/authorize`, including state parameter validation and optional resource indicator.

- **FR-004**: Test OAuth server MUST implement token endpoint supporting `authorization_code`, `refresh_token`, `client_credentials`, and `urn:ietf:params:oauth:grant-type:device_code` grant types.

- **FR-005**: Test OAuth server MUST emit JWT access tokens with configurable expiry and include resource indicator as audience claim when provided.

- **FR-006**: Test OAuth server MUST implement dynamic client registration at `/registration` that issues client credentials and remembers allowed redirect URIs and scopes.

- **FR-007**: Test OAuth server MUST implement device authorization flow with `/device_authorization` and `/device_verification` endpoints, supporting pending/approved/denied states.

- **FR-008**: Test OAuth server MUST support configurable error responses (invalid_client, invalid_scope, invalid_grant, slow responses, 500 errors) via Options struct.

- **FR-009**: Test OAuth server MUST support JWKS rotation by allowing key replacement with different key IDs.

- **FR-010**: Test OAuth server MUST render a login/consent UI at `/authorize` for browser-based testing, with username/password fields and consent checkbox.

- **FR-011**: Test harness MUST support configurable detection modes: WWW-Authenticate header on protected resource, discovery-only, or explicit endpoints.

- **FR-012**: Go integration tests MUST cover detection, auth code + PKCE, resource indicator, DCR, device code, client credentials, token refresh, JWKS rotation, and error handling scenarios.

- **FR-013**: E2E bash script (`scripts/run-oauth-e2e.sh`) MUST orchestrate starting the OAuth server, launching mcpproxy with appropriate config, and running Playwright/API test assertions.

- **FR-014**: Playwright tests MUST drive browser login flow, fill credentials, approve/deny consent, and verify redirect behavior and token storage.

- **FR-015**: `mcpproxy auth status` MUST display OAuth endpoints, scopes, resource indicator, PKCE usage, token expiry, and last refresh time with secrets masked.

- **FR-016**: `mcpproxy auth login` MUST print authorization URL preview with parameters before opening browser.

- **FR-017**: OAuth-related logs MUST include structured fields: provider URL, resource indicator, scopes, grant type, PKCE flag, DCR outcomes, token expiry. Errors MUST include provider error response body.

- **FR-018**: `mcpproxy doctor` MUST include an OAuth health check that validates configuration, tests discovery endpoint reachability, and provides actionable hints for common issues.

### Key Entities

- **OAuth Test Server**: Local HTTP server implementing OAuth 2.1 endpoints for testing. Key attributes: issuer URL, signing key pair, registered clients, issued authorization codes, issued tokens.

- **Test Options**: Configuration for test harness. Attributes: enabled flows (auth code, device, DCR, client credentials), error injection toggles, response delays, JWKS configuration.

- **Test Client Credentials**: Dynamically or pre-configured OAuth client. Attributes: client_id, client_secret (optional for public clients), allowed redirect URIs, allowed scopes.

- **Authorization Code**: Ephemeral code issued during auth flow. Attributes: code value, associated client, associated PKCE challenge, associated resource indicator, expiry.

- **Token Set**: Access and refresh tokens issued by test server. Attributes: access token (JWT), refresh token, token type, expiry, scopes, audience (from resource indicator).

## Assumptions

- Tests will use deterministic RSA or ECDSA keys for JWT signing to ensure reproducible test results.
- The test OAuth server will run on localhost with an ephemeral port to avoid port conflicts.
- Playwright tests will run in headless mode for CI compatibility.
- Token expiry times in tests will be short (seconds to minutes) to allow expiry/refresh testing without long waits.
- The go-sdk `oauthex` package will be referenced for expected OAuth flow shapes but not directly imported (mcpproxy uses `mark3labs/mcp-go`).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: OAuth E2E test suite runs in under 5 minutes in CI and provides clear pass/fail results for each test case.

- **SC-002**: All OAuth flows (auth code + PKCE, device code, client credentials, DCR, refresh) have at least one happy-path and one error-path test.

- **SC-003**: `mcpproxy doctor` reports OAuth configuration issues within 2 seconds and provides actionable resolution hints for at least 5 common problems (missing scopes, unreachable provider, invalid credentials, expired tokens, discovery failures).

- **SC-004**: `mcpproxy auth status` displays complete OAuth state information in under 500ms, enabling users to self-diagnose authentication issues without consulting logs.

- **SC-005**: OAuth-related support issues that can be diagnosed by E2E tests are reduced (target: 50% of OAuth bugs are caught by test suite before release).

- **SC-006**: Test suite achieves 90% code coverage on OAuth-related modules (`internal/oauth`, `internal/auth`, related CLI commands).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- **Do NOT include**: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add OAuth test harness with auth code + PKCE flow

Related #[issue-number]

Implements reusable OAuth test server package for E2E testing. The harness
supports configurable flows, error injection, and JWKS rotation.

## Changes
- Add tests/oauthserver package with Start() helper
- Implement discovery, authorize, and token endpoints
- Add PKCE validation and state parameter checks
- Include JWT token generation with configurable claims

## Testing
- All unit tests pass
- Integration tests verify auth code flow end-to-end
- Manual testing confirms token refresh works
```
