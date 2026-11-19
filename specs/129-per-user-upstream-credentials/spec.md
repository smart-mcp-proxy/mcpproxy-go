# Feature Specification: Per-user upstream credentials (server edition)

**Feature Branch**: `129-per-user-upstream-credentials`
**Created**: 2026-11-19
**Status**: Draft. It is the "Phase 2 trigger" that Spec 107 FR-034 deferred, and it builds on the
Spec 112 per-request header seam.
**Input**: Idea 5 of the LiteLLM MCP Gateway comparison (2026-11-19), and the Gcore deployment request
(2026-09-26). A central mcpproxy-server in front of internal MCP servers, including LiteLLM, holds one
upstream key, so every user collapses into one identity. LiteLLM itself supports per-user BYOK keys,
per-user env vars interpolated into headers, RFC 8693 token exchange (incl. Entra OBO) and ID-JAG
(`litellm/proxy/mcp_server/outbound_credentials/`, `byok_oauth_endpoints.py`, `idp_token_exchange.py`).

## Why

A team runs one mcpproxy-server. Its upstreams (an internal LiteLLM, GitHub, Jira, a data API) each sit
behind one static credential in the server config. Every user's tool call therefore reaches the upstream
as the same principal. The consequences:

- The upstream's own access control, per-user rate limits, spend tracking and audit trail see one user.
- A user can act with the operator's permissions, which can be broader than their own.
- Teams that need per-user attribution have to run one mcpproxy per user. That defeats the server edition.

Spec 107 deliberately removed the latent per-user chain (`Router`, `HeaderInjector`, `TokenExchanger`,
per-(user, server) connection keying) because it had no production callers and no tests of the
behaviour users would rely on. It also stopped storing IdP tokens (FR-033: "a long-lived IdP refresh
token at rest with no reader is a leak surface"). This spec does not revert that chain. It adds a
smaller design on the seam that now exists and is tested. Spec 112 attaches headers per request on the
shared upstream connection (`WithHTTPHeaderFunc`), already strips them from anything that is not a
`tools/call`, drops them on cross-origin redirects, and scrubs them from every sink.

## The Decision

1. **Per-request injection on the shared connection, not per-user connections.** One upstream
   connection per server stays. A user's credential is attached to that user's `tools/call` HTTP
   request only. This works for upstreams that authenticate every request (LiteLLM, most bearer-token
   APIs, 2026-07-28 stateless MCP). Upstreams that bind identity at `initialize` are not supported, and
   validation says so for servers marked `session_bound_identity: true`.
2. **A user credential is referenced, never configured.** The server config names a slot with
   `${user:<slot>}` inside a header value. Each user fills their own slot through the existing
   credential surfaces (Web UI, `mcpproxy credential`, REST). Values live in the existing AES-256-GCM
   `user_upstream_credentials` store.
3. **Discovery stays operator-identity.** Connect, `tools/list`, health checks and background refresh have
   no user. They keep using the server's static credential. The tool index is shared. Per-user tool lists
   are out of scope.
4. **Token exchange is opt-in per server and stores only what it reads.** RFC 8693 / Entra OBO uses the
   user's IdP access token captured at login. It is held encrypted for the session's lifetime and purged
   at logout or expiry. There is no refresh token and no offline access, so the FR-033 rationale holds: there is
   now a reader, and nothing outlives the session.

## Scope Boundary

In scope (server edition only, `//go:build server`):

- `${user:<slot>}` references in a server's `headers` values (streamable-HTTP upstreams).
- User self-service to set, replace and delete their slot values. Administrators see which users have
  filled which slots (never the values) and can delete a user's value.
- `auth_broker.mode: token_exchange` (RFC 8693) and `entra_obo` (OAuth 2.0 JWT-bearer OBO), each producing
  a per-(user, server) upstream access token injected as `Authorization: Bearer …` on that user's calls.
- Injection of an `oauth_connect` credential the user already stored (FR-034's "stored, not injected"
  becomes "injected when the server opts in").
- Attribution: activity, audit and read-cache isolation per credential.

Out of scope:

- The personal edition. A `${user:…}` reference or an exchange mode in a personal-edition config is a
  load-time validation error, and the field is preserved opaquely (Spec 107 FR-040 pattern).
- stdio and SSE upstreams (stdio env is fixed at spawn, and SSE has no per-request header seam today).
- Per-user tool lists or per-user discovery.
- Okta ID-JAG, SigV4 and inbound token passthrough of the caller's own `Authorization` header (it stays
  deny-listed in `forward_headers`).
- IdP refresh tokens and offline access (open question).

## Definitions

- **User**: a server-edition principal with a `UserID`. On `/mcp` that means an agent token owned by
  the user (Spec 107 FR-003: sessions and user JWTs never authenticate `/mcp`).
- **Slot**: a named per-server placeholder (`[a-z0-9_]{1,32}`) declared by its use in `${user:<slot>}`.
- **User credential**: one user's value for one (server, slot), or one user's exchanged or connected
  token for one server. Encrypted at rest. Keyed by user ID and the server key (name + URL), so renaming
  or re-pointing a server orphans old values instead of leaking them to a new URL.
- **Credential source** (attributed on every call): `user_slot`, `token_exchange`, `entra_obo`,
  `oauth_connect`, `static` (operator) or `none`.

## User Scenarios & Testing *(mandatory)*

### User Story 1: Each user reaches LiteLLM with their own key (Priority: P1)

The operator configures the `litellm` upstream with
`headers: {"Authorization": "Bearer ${user:litellm_key}"}`. Alice opens Credentials in the Web UI, sees
"litellm: key `litellm_key` not set", pastes her LiteLLM virtual key and saves. Her agent calls a
LiteLLM tool, and LiteLLM's logs and spend show Alice. Bob, who has not set his key, gets the refusal
"this server needs your personal credential `litellm_key`; set it in MCPProxy → Credentials". No request
reaches LiteLLM with an empty or operator key.

**Why this priority**: it is the Gcore request in full and the smallest design that delivers per-user
identity.

**Independent Test**: a fixture HTTP MCP server that echoes the received `Authorization` per call. Two
users with owned agent tokens. Alice's calls carry Alice's value, Bob's call is refused before dispatch,
and the operator's discovery `tools/list` carries the static header.

**Acceptance Scenarios**:

1. **Given** Alice's slot set and Bob's unset, **When** both call the same tool, **Then** the fixture
   receives exactly one call, with Alice's value, and Bob gets error code
   `MCPX_USER_CREDENTIAL_MISSING`.
2. **Given** the server's discovery, **When** mcpproxy lists tools in the background, **Then** the request
   carries the static fallback configured for discovery (FR-006) or no `Authorization`, never any user's value.
3. **Given** Alice deletes her value, **When** she calls again, **Then** the call is refused. No cached
   copy of her value is used.
4. **Given** the caller is the operator's API key or the socket (no user), **When** it calls the tool,
   **Then** the per-server `when_no_user` policy applies: `refuse` (default) or `static`.

---

### User Story 2: One user's results are never served to another (Priority: P1)

Alice and Bob call the same read tool with the same arguments. Bob must not receive Alice's cached
result, and a server-initiated reply (ping, sampling, elicitation) on the shared connection must never
carry Alice's credential.

**Why this priority**: this is the isolation gap the Spec 112 work found. Per-user identity without it is
worse than none.

**Acceptance Scenarios**:

1. **Given** Alice's result is in the read cache, **When** Bob issues an identical call, **Then** it is a
   cache miss, because the cache entry is bound to Alice's credential digest and principal.
2. **Given** an upstream ping or elicitation request arrives while Alice's call is in flight, **When**
   mcpproxy replies, **Then** the reply carries no user credential header.
3. **Given** the upstream redirects Alice's call cross-origin, **When** mcpproxy follows it, **Then** the
   user credential is dropped.

---

### User Story 3: Exchange the user's sign-in for an upstream token (Priority: P2)

The operator sets `auth_broker: {mode: token_exchange, token_endpoint, audience, scopes, client_id,
client_secret}` on the `data-api` upstream. Alice signed in to mcpproxy-server with the company IdP. Her
first call to `data-api` exchanges her captured IdP access token (RFC 8693, `subject_token_type`
access_token) for an upstream token with that audience. The token is cached for her until it expires and
injected as `Authorization: Bearer`. The `entra_obo` mode does the same with the Entra on-behalf-of grant.

**Why this priority**: it removes per-user key handling for enterprises with one IdP. It is P2 because it
needs IdP cooperation and re-adds token custody that Spec 107 removed.

**Acceptance Scenarios**:

1. **Given** Alice's session is live, **When** she calls, **Then** one exchange happens and the result is
   reused until 60 s before its expiry.
2. **Given** Alice's captured IdP token has expired, **When** she calls, **Then** the call is refused with
   `MCPX_USER_REAUTH_REQUIRED` ("sign in to MCPProxy again"). No refresh token exists to fall back on.
3. **Given** Alice logs out or her session expires, **Then** her captured IdP token and every exchanged
   token derived from it are purged within one minute.
4. **Given** the token endpoint refuses (`invalid_grant`, `unauthorized_client`), **Then** the call is
   refused with the upstream AS error class (never its body), and the attempt is recorded once.

---

### User Story 4: Use the token the user already connected (Priority: P2)

A server with `auth_broker.mode: oauth_connect` and the new `inject: true` attaches the user's stored
connect-flow access token to that user's calls, and refreshes it with the stored refresh token when it
expires. One refresh runs per (user, server) at a time.

**Acceptance Scenarios**:

1. **Given** Alice completed the connect flow, **When** she calls, **Then** her token is injected.
2. **Given** two concurrent calls find Alice's token expired, **When** they refresh, **Then** exactly one
   refresh request is sent and both calls use its result.
3. **Given** `inject` is unset, **Then** behaviour is unchanged from today (stored, not injected).

---

### User Story 5: The operator sees who has what (Priority: P3)

The admin Credentials view and `mcpproxy credential list --all-users` show, per server and slot, which
users have a value, when it was set and when it last succeeded or failed. They never show the value. An
admin can delete one user's value or all values for a slot.

### Edge Cases

- **A header mixes static text and a slot** (`"Bearer ${user:k}"`, `"tenant=acme;key=${user:k}"`): allowed.
  Several slots in one server are allowed, at most 8.
- **The same slot name on two servers**: two independent values (the slot is scoped to the server key).
- **A slot reference in a non-header field** (`url`, `args`, `env`, `oauth`): validation error.
- **The header name is deny-listed for forwarding** (`Authorization`): allowed here, because this value
  comes from the trusted store, not from the client. Client-forwarded headers can never fill a slot.
- **A static header and a slot header with the same name**: validation error (one source per header).
- **An agent token owned by a deactivated user**: refused at authentication as today, so no credential
  lookup happens.
- **Agent tokens pinned to a profile**: profile and token rules apply first. Credential injection is the
  last step before dispatch.
- **`code_execution` nested calls**: they inject the calling user's credential, the same as a direct call.
- **Value limits**: 4 KiB per value, no control characters, no CR/LF (header injection).
- **The master key is unavailable** (`MCPPROXY_CRED_KEY` unset): a server that uses slots or exchange
  reports health `needs_config`, and its user-attributed calls are refused with a fixed message. Static
  discovery still works.
- **Key rotation**: re-encryption follows the existing credential store's rotation procedure. Out of
  scope to change here.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A server's `headers` values MUST accept `${user:<slot>}` references in the server edition.
  They MUST NOT be expanded at client construction, and MUST be expanded per `tools/call` request from
  the calling user's stored value.
- **FR-002**: Validation MUST reject a `${user:…}` reference in the personal edition, in any field other
  than `headers` values, on a stdio or SSE upstream, on a server with `session_bound_identity: true`,
  with more than 8 slots per server, or alongside a static header of the same name.
- **FR-003**: A user credential MUST be attached only to the calling user's `tools/call` HTTP request.
  It MUST be stripped from every other upstream request (initialize, list, ping, replies to
  server-initiated requests, notifications) and from any cross-origin redirect, reusing the Spec 112 gate
  and redirect filter.
- **FR-004**: A call whose server needs a user credential the caller lacks MUST be refused before dispatch
  with `MCPX_USER_CREDENTIAL_MISSING`, naming the server and slot (never another user). A caller with no
  user MUST follow the server's `when_no_user: refuse|static` (default `refuse`).
- **FR-005**: Users MUST be able to set, replace and delete their own slot values for servers in their
  entitlement through the Web UI, `mcpproxy credential set|delete`, and REST, all session-authenticated
  (never through an agent token or `/mcp`). Values MUST be write-only after saving.
- **FR-006**: Discovery and health traffic MUST use the server's static credential (or none) and MUST
  NEVER use a user credential. A server MAY set `discovery_headers` for the operator's discovery
  identity.
- **FR-007**: Read-cache entries produced under a user credential MUST be bound to the principal and an
  HMAC digest of the credential (the Spec 112 `ForwardedDigest` pattern), and MUST NOT be served to any
  other principal or credential.
- **FR-008**: Every tool call MUST record `credential_source` and, for slots, the slot name in the
  activity row and audit line. It MUST NEVER record the value or a token. Values MUST be scrubbed from
  logs, errors and activity by the existing scrub sinks.
- **FR-009**: `auth_broker.mode` MUST accept `token_exchange` (RFC 8693 with `audience`/`resource`,
  `scopes`, client authentication `client_secret_basic|client_secret_post`) and `entra_obo`, on
  streamable-HTTP upstreams only. The exchanged token MUST be injected as `Authorization: Bearer` and
  cached per (user, server) until 60 s before expiry.
- **FR-010**: Only when at least one server uses an exchange mode, the server edition MUST capture the
  user's IdP access token at login, store it encrypted in the credential store bound to the session, and
  purge it and all derived exchanged tokens at logout, session expiry or user deactivation. It MUST NOT
  request offline access or store an IdP refresh token. `store_idp_tokens` stays a no-op.
- **FR-011**: An `oauth_connect` server with `inject: true` MUST inject the user's stored access token and
  refresh it single-flight per (user, server). Without `inject`, behaviour MUST be unchanged.
- **FR-012**: Token-endpoint failures MUST be reported to the agent by fixed error class
  (`MCPX_USER_REAUTH_REQUIRED`, `MCPX_USER_CREDENTIAL_REFUSED`), never with the AS response body.
- **FR-013**: Administrators MUST be able to list, per server and slot, which users have a value (set
  time, last success, last failure) without seeing values, and to delete one user's or all users'
  values for a slot. Each change MUST write an activity row of the existing `credential_broker` type
  (administrator-only on every read door).
- **FR-014**: The Spec 107 FR-035 guard test MUST be amended: it keeps asserting the absence of the deleted
  `Router`/`ToolFilter`/workspace/`HeaderInjector`/`ConnectionKey` chain and per-(user, server) connection
  keying, and newly allows the `token_exchange`/`entra_obo` modes and the injection described here. The
  "stored, not injected" doc sentences MUST be rewritten to describe the opt-in injection.
- **FR-015**: Each user-credential use MUST add no upstream round trips beyond the token-endpoint call
  that FR-009 requires on a cache miss.
- **FR-016**: A server with slots or an exchange mode MUST report its credential readiness in its health
  detail without user specifics (e.g. "needs each user's `litellm_key`"), and a user's own
  Credentials view MUST show their per-server status: `set`, `not_set`, `expired` or `refused`.

### Key Entities

- **Slot declaration**: (server, slot name), derived from the config, never stored separately.
- **User credential record**: (user ID, server key, slot or `exchange` or `connect`), encrypted value or
  token, set time, expiry, last success, last failure class.
- **Captured IdP token**: (user ID, session ID), encrypted, with a lifetime no longer than the session's.
  Present only while an exchange-mode server exists.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a two-user test against an echoing fixture, 100% of each user's calls carry only that
  user's credential, and 0 discovery, ping or reply requests carry any user credential.
- **SC-002**: Zero cross-user read-cache hits across a matrix of identical calls by different users and
  credentials.
- **SC-003**: A Gcore-style deployment (one server, LiteLLM upstream, 10 users) shows 10 distinct
  identities in the upstream's logs, without running more than one mcpproxy.
- **SC-004**: Per-call overhead of slot injection is under 1 ms p95. Token exchange adds exactly one
  token-endpoint call per (user, server) per token lifetime.
- **SC-005**: After logout, no captured IdP token or exchanged token for that user remains in storage
  (verified by a raw `config.db` scan, as in the Spec 123 e2e).
- **SC-006**: The personal edition's behaviour and config compatibility are unchanged (both-edition
  build, existing e2e suites green, opaque preservation of the new fields).

## Assumptions

- Target upstreams authenticate each HTTP request (true for LiteLLM's MCP gateway with
  `x-litellm-api-key`/`Authorization`, and for typical bearer APIs). Session-bound upstreams are excluded
  by FR-002.
- The existing credential store's encryption, master-key resolution and purge set are adequate for
  slots, exchanged tokens and captured IdP tokens.
- Users reach `/mcp` only through owned agent tokens (Spec 107 FR-003), so a `UserID` is always present
  on a user call and absent on operator calls.
- A shared tool index built under the operator's discovery identity is acceptable. Calls a user's
  upstream identity cannot perform fail on the upstream side with the upstream's error.

## Open Questions for the Plan

- Whether to support IdP refresh tokens (offline access) for long-running agents, which would reverse
  part of FR-033 behind an explicit opt-in.
- Whether `${user:…}` should later extend to SSE once it gains a per-request header seam.
- The UX for a user whose credential is refused by the upstream (401) mid-session: mark `refused`
  automatically after N consecutive 401s, or only on explicit test.
- Whether ID-JAG (Okta cross-app access) is worth a follow-up mode on the same exchange path.
