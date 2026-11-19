# Feature Specification: Per-user upstream credentials (server edition)

**Feature Branch**: `129-per-user-upstream-credentials`
**Created**: 2026-11-19
**Status**: Draft, revised after review round 1. This is the "Phase 2 trigger" that Spec 107 FR-034
deferred. It builds on the Spec 112 per-request header seam.
**Input**: Idea 5 of the LiteLLM MCP Gateway comparison (2026-11-19), and the Gcore deployment request
(2026-09-26). A central mcpproxy-server sits in front of internal MCP servers, including LiteLLM, and
holds one upstream key, so every user collapses into one identity. LiteLLM itself supports per-user BYOK
keys, per-user env vars interpolated into headers, RFC 8693 token exchange (incl. Entra OBO) and ID-JAG
(`litellm/proxy/mcp_server/outbound_credentials/`, `byok_oauth_endpoints.py`, `idp_token_exchange.py`).

## Why

A team runs one mcpproxy-server. Its upstreams (an internal LiteLLM, GitHub, Jira, a data API) each sit
behind one static credential in the server config. Every user's tool call therefore reaches the upstream
as the same principal. The consequences:

- The upstream's own access control, per-user limits, spend tracking and audit trail see one user.
- A user can act with the operator's permissions, which can be broader than their own.
- Teams that need per-user attribution run one mcpproxy per user, which defeats the server edition.

Spec 107 deliberately removed the latent per-user chain (`Router`, `HeaderInjector`, `TokenExchanger`,
per-(user, server) connection keying), because it had no production callers. It also stopped storing
IdP tokens (FR-033: "a long-lived IdP refresh token at rest with no reader is a leak surface"). This
spec does not revert that chain. It adds a smaller design on the seam that exists and is tested today.
Spec 112 attaches headers per HTTP request on the shared upstream connection (`WithHTTPHeaderFunc`).

The Spec 112 seam carries **client-asserted** headers, and its protections are written for that
purpose. It deny-lists `Authorization`, rejects collisions with static headers, and lets an upstream
echo a forwarded value back, because the client already knew it. A credential held by the server, and
which an agent must never be able to read, is a different trust class. It gets its own context and its own
disclosure boundary. The Spec 112 gate, redirect filter and scrub sinks are extended to cover it. The
client-forwarding deny list is not relaxed.

## The Decision

1. **Per-request injection on the shared connection, not per-user connections.** One upstream
   connection per server stays. A user's credential is attached only to that user's `tools/call` HTTP
   requests. This targets upstreams that authenticate every request (LiteLLM, most bearer-token APIs,
   2026-07-28 stateless MCP). Upstreams that bind identity at `initialize` are excluded by validation
   (`session_bound_identity: true`).
2. **A user credential is referenced, never configured.** The server config names a slot with
   `${user:<slot>}` inside a header value. Each user fills their own slot through the credential surfaces
   (Web UI, `mcpproxy credential`, REST).
3. **One credential source per call.** A server in user-credential mode carries no operator credential
   on user calls. The operator's discovery identity lives in a separate `discovery_headers` block, which
   is never sent on a user call (FR-004).
4. **Discovery stays operator-identity, and only background operations use it.** Background operations
   have no user and use `discovery_headers` (or nothing): `initialize`, `tools/list`, `prompts/list`,
   `resources/list`, `ping`, health checks and refresh. The tool index is shared, and per-user tool lists
   are out of scope. A user-triggered request other than `tools/call` (`prompts/get`, `resources/read`,
   `completion/complete`) on a user-credential server is refused for user callers in this phase. It
   would otherwise run with the operator's identity on caller-controlled arguments.
5. **Token exchange is opt-in per server and stores only what it reads.** RFC 8693 / Entra OBO uses a
   *delegation grant*: the IdP access token from one login session, held encrypted, valid no longer than
   that session and that token, and purged when either ends. There is no refresh token and no offline
   access, so FR-033's rationale holds: there is now a reader, and nothing outlives the session.

## Scope Boundary

In scope (server edition only, `//go:build server`):

- `${user:<slot>}` references in a server's `headers` values (streamable-HTTP upstreams).
- `discovery_headers` for the operator's discovery identity on user-credential servers.
- User self-service to set, replace and delete slot values. Administrators see which users have filled
  which slots (never the values) and can delete them.
- `auth_broker.mode: token_exchange` (RFC 8693) and `entra_obo` (OAuth 2.0 JWT-bearer OBO).
- Injection of an `oauth_connect` credential the user already stored (`inject: true`). FR-034's
  "stored, not injected" becomes "injected when the server opts in".
- Attribution, a disclosure boundary for stored credentials, and read-cache isolation per credential.
- The record-store changes these need: namespaced and versioned records, generations, linked purge.

Out of scope:

- Personal-edition behaviour changes. The personal build keeps preserving `auth_broker` blocks opaquely,
  exactly as today (Spec 107). A `${user:…}` reference in a personal-edition config is preserved too,
  and the server is reported `needs_config` ("per-user credentials need the server edition"). The
  placeholder is never sent upstream.
- stdio and SSE upstreams (stdio env is fixed at spawn, and SSE has no per-request header seam today).
- Per-user tool lists or per-user discovery.
- Okta ID-JAG, SigV4, and passthrough of the caller's own `Authorization` header (it stays deny-listed in
  `forward_headers`).
- IdP refresh tokens and offline access (open question).
- Re-encryption on master-key rotation. Rotation remains unsupported, and the docs state that changing the
  key makes every stored user credential unreadable. Users must re-enter slots and reconnect, and the
  store reports those records as `unreadable`.

## Definitions

- **User**: a server-edition principal with a `UserID`. On `/mcp` this is an agent token owned by the
  user (Spec 107 FR-003: sessions and user JWTs never authenticate `/mcp`).
- **User-credential mode**: a server with exactly one of three credential sources: slot references, an
  exchange mode (`token_exchange` / `entra_obo`), or `oauth_connect` with `inject: true`. The three are
  mutually exclusive on one server.
- **Slot**: a named per-server placeholder (`[a-z0-9_]{1,32}`) declared by its use in `${user:<slot>}`.
- **Credential record**: one encrypted record per (user ID, server key, kind, name). The kind is `slot`
  (name = slot), `connect`, `exchanged`, or `grant` (name = session ID). Each record carries a
  **generation**, which increments on every set, replace, delete and reconnect, and a **broker-config
  fingerprint** covering mode, endpoints, audience or resource, scopes and client identity. The server
  key stays name + URL, so re-pointing a server orphans old records instead of leaking them to a new URL.
- **Delegation grant**: the IdP access token captured at one login session. It is valid while that
  session is live *and* the token has not expired. A user has at most one live grant: a new login
  replaces the previous grant, and the previous session stops carrying one. The token's expiry comes
  only from a positive `expires_in` in the IdP token response. When that field is omitted, zero or
  negative, the login still succeeds but creates **no grant**, so exchange fails closed with
  `MCPX_USER_REAUTH_REQUIRED`. An unknown lifetime is never treated as unbounded or session-long.
- **Effective expiry** of an exchanged token: the minimum of its own expiry, its grant's token expiry and
  its grant's session expiry. An exchanged token without a stated expiry is treated as expiring 5
  minutes after issue.
- **Credential source** (attributed on every call): `user_slot`, `token_exchange`, `entra_obo`,
  `oauth_connect`, `discovery` (operator), or `none`.

## User Scenarios & Testing *(mandatory)*

### User Story 1: Each user reaches LiteLLM with their own key (Priority: P1)

The operator configures `litellm` with `headers: {"Authorization": "Bearer ${user:litellm_key}"}` and
`discovery_headers: {"Authorization": "Bearer ${file:/run/secrets/litellm_operator}"}`. Alice opens
Credentials in the Web UI and sees "litellm: key `litellm_key` not set". She pastes her LiteLLM virtual
key and saves. Her agent calls a LiteLLM tool, and LiteLLM's logs and spend show Alice. Bob, who has not
set his key, gets the refusal "this server needs your personal credential `litellm_key`; set it in
MCPProxy → Credentials". No request reaches LiteLLM with an empty, placeholder or operator credential.

**Why this priority**: it is the Gcore request and the smallest design that delivers per-user identity.

**Independent Test**: a fixture HTTP MCP server records every request's headers and method. Two users
call through owned agent tokens. Alice's `tools/call` requests carry only Alice's value. Bob is refused
before dispatch. Every non-`tools/call` request carries only the discovery header. The test runs with
`forward_client_headers` both on and off.

**Acceptance Scenarios**:

1. **Given** Alice's slot set and Bob's unset, **When** both call the same tool, **Then** the fixture
   receives exactly one `tools/call`, carrying Alice's value and no operator header. Bob gets
   `MCPX_USER_CREDENTIAL_MISSING`.
2. **Given** background discovery, **When** mcpproxy lists tools, **Then** the request carries
   `discovery_headers` and never a user value.
3. **Given** Alice deletes her value while a call of hers is queued, **When** the queued call
   dispatches, **Then** it is refused: the record generation is checked at dispatch.
4. **Given** the caller is the operator's API key or the socket (no user), **When** it calls the tool,
   **Then** the per-server `when_no_user` policy applies: `refuse` (default) or `discovery` (use
   `discovery_headers`, attributed as `discovery`).

---

### User Story 2: A stored credential never reaches an agent or another user (Priority: P1)

An agent must not read Alice's stored secret, even through an upstream that echoes request headers. Bob
must not receive any result produced under Alice's credential. A server-initiated reply on the shared
connection must never carry Alice's credential.

**Why this priority**: per-user identity without this boundary is worse than none.

**Acceptance Scenarios**:

1. **Given** an echo tool that returns its request headers, **When** Alice calls it, **Then** her raw slot
   value and the rendered header value are replaced with `[REDACTED:user_credential]` in the result, any
   error, the cache payload and every recording sink.
2. **Given** Alice's call produced a `read_cache` entry, **When** Bob (or an administrator reader, or a
   recursive child-page read) redeems that exact cache key, **Then** the read is refused without
   disclosing that the entry exists.
3. **Given** Alice's cache entry, **When** her credential is deleted, replaced or expires, or the server's
   broker config changes, **Then** Alice's own redemption is refused too.
4. **Given** an upstream ping, sampling or elicitation request during Alice's call, **When** mcpproxy
   replies, **Then** the reply carries no user credential, with forwarding both on and off.
5. **Given** a cross-origin redirect of Alice's call, **When** it is followed, **Then** the user
   credential is dropped.

---

### User Story 3: Exchange the user's sign-in for an upstream token (Priority: P2)

The operator sets `auth_broker: {mode: token_exchange, token_endpoint, audience, scopes, client_id,
client_secret}` on `data-api`. Alice signed in to mcpproxy-server with the company IdP, which created her
delegation grant. Her first call exchanges the grant's IdP access token (RFC 8693, `subject_token_type`
access_token) for an upstream token with that audience. The token is cached until its effective expiry
and injected as `Authorization: Bearer`. `entra_obo` does the same with the Entra on-behalf-of grant.

**Why this priority**: it removes per-user key handling for enterprises with one IdP. It is P2 because it
needs IdP cooperation and re-adds token custody.

**Acceptance Scenarios**:

1. **Given** a live grant, **When** Alice calls twice, **Then** one exchange happens, and the second call
   reuses the token because it is before its effective expiry minus 60 s.
2. **Given** the grant's IdP token has expired (while the exchanged token has not), **When** she calls,
   **Then** the call is refused with `MCPX_USER_REAUTH_REQUIRED`.
3. **Given** two live sessions for Alice, **When** she logs in a second time, **Then** the second login
   replaces the grant. Logging out of the first session leaves the new grant valid. Logging out of the
   second session invalidates it and every token exchanged from it, effective at once (the validity check
   runs before every dispatch and cache redemption). Physical deletion follows within one minute.
4. **Given** an exchange is in flight when Alice logs out, **When** it completes, **Then** its result is
   discarded and not stored (generation fence).
5. **Given** the token endpoint refuses (`invalid_grant`, `unauthorized_client`), **Then** the call is
   refused with `MCPX_USER_CREDENTIAL_REFUSED`, never with the AS response body, and only Alice's status
   changes. The server's shared health does not.
6. **Given** `entra_obo` without a login that requests an access token for mcpproxy's own Entra
   application, **Then** validation rejects the config. With it configured, a fixture AS rejects an
   assertion whose audience is not that application, and the test asserts that rejection.

---

### User Story 4: Use the token the user already connected (Priority: P2)

A server with `auth_broker.mode: oauth_connect` and `inject: true` attaches the user's stored
connect-flow access token to that user's calls. It refreshes the token with the stored refresh token when
it expires, single-flight per (user, server), and publishes the result only when the record's
generation is unchanged.

**Acceptance Scenarios**:

1. **Given** Alice completed the connect flow, **When** she calls, **Then** her token is injected.
2. **Given** two concurrent calls find the token expired, **When** they refresh, **Then** exactly one
   refresh is sent and both calls use its result.
3. **Given** a refresh is blocked in flight, **When** Alice disconnects or reconnects, **Then** the late
   refresh result is discarded. The deleted record is not recreated, the new connection is not
   overwritten, and waiters get the current state, not the obsolete token.
4. **Given** `inject` is unset, **Then** behaviour is unchanged from today (stored, not injected), and
   existing connect records stay readable without migration.

---

### User Story 5: The operator sees who has what (Priority: P3)

The admin Credentials view and `mcpproxy credential list --all-users` show, per server and slot, which
users have a value, when it was set and when it last succeeded or failed. They never show the value. An
admin can delete one user's value or all values for a slot.

### Edge Cases

- **A header mixes static text and a slot** (`"Bearer ${user:k}"`, `"tenant=acme;key=${user:k}"`):
  allowed. At most 8 slots per server.
- **The same slot name on two servers**: independent records (the slot is scoped to the server key).
- **A slot reference in a non-header field** (`url`, `args`, `env`, `oauth`, `discovery_headers`):
  validation error.
- **An agent token owned by a deactivated user**: refused at authentication as today. Deactivation also
  purges the user's grant and exchanged tokens.
- **Profiles and token rules** apply first. Credential construction is the last step before dispatch.
- **`code_execution` nested calls** use the calling user's credential, the same as a direct call.
- **Value limits**: 4 KiB per value, no control characters, no CR/LF (header injection).
- **Transformed echoes**: redaction covers the raw value and the rendered header value. An upstream that
  returns a transformed copy (base64, split, hashed) is not caught. The docs state this limitation
  instead of claiming universal scrubbing.
- **Credential store unavailable** (no master key resolved from `credential_encryption_key` or
  `MCPPROXY_CRED_KEY`): a user-credential server reports health `needs_config`, user calls are refused
  with a fixed message, and discovery still works.

## Requirements *(mandatory)*

### Functional Requirements

**Configuration**

- **FR-001**: In the server edition, a server's `headers` values MUST accept `${user:<slot>}`
  references. They MUST NOT be expanded at client construction. They MUST be expanded per `tools/call`
  request from the calling user's record.
- **FR-002**: Validation MUST reject a `${user:…}` reference in any field other than `headers` values,
  on a stdio or SSE upstream, on a server with `session_bound_identity: true`, or with more than 8
  slots. For a user-credential server it MUST reject each of the following:
  - more than one credential source (slots, exchange, injected connect);
  - its own upstream `oauth` block;
  - a static credential-bearing header in `headers`;
  - a `headers` entry and a `discovery_headers` entry that disagree on whether a header carries a
    credential;
  - a URL with userinfo or any query string;
  - a URL that is not `https`, except a loopback host under an explicit test flag. This is re-checked
    before every dispatch.
- **FR-002a**: A slot MAY appear only in the value of a credential header. These are `Authorization`,
  `X-Api-Key`, `Api-Key`, `X-Auth-Token`, `X-Litellm-Api-Key`, or another `X-` header that is not on the
  Spec 112 deny list (exact names and the `mcp-`, `x-mcpproxy-`, `x-forwarded-`, `proxy-`, `content-`,
  `sec-`, `if-` prefixes) and not hop-by-hop. Names compare case-insensitively. Transport-managed
  headers (`Mcp-Session-Id`, `Mcp-Protocol-Version`, `Content-Type`, `Accept`, framing and routing
  headers) MUST be rejected at validation and again at runtime.
- **FR-003**: The personal build MUST preserve `auth_broker` blocks and `${user:…}` references opaquely,
  report such servers `needs_config`, and never send an unresolved placeholder upstream.
- **FR-004**: For a user call on a user-credential server, outbound headers MUST be built by one
  algorithm. Start from `headers`. Then, depending on the server's single credential source, either
  resolve every slot from the caller's records, or inject the exchanged or connected token as
  `Authorization`. Add client-forwarded headers per Spec 112, whose deny list is unchanged. Never add
  `discovery_headers` or any operator credential. If any slot cannot be resolved, or any credential step
  fails, the call MUST be refused before dispatch. A header MUST never be sent with an empty or
  placeholder value.
- **FR-004a**: On a user-credential server, user callers MUST be refused `prompts/get`,
  `resources/read` and `completion/complete` with a fixed error naming the phase limitation.
  Discovery credentials MUST be used only for the background operations listed in The Decision, with one
  explicit exception. An authenticated operator caller (API key or socket, no user) on a server with
  `when_no_user: discovery` uses `discovery_headers` for `tools/call` and for these three methods,
  attributed as `discovery`. With `when_no_user: refuse` (the default), the operator is refused for all
  four. Tests pin both settings for `tools/call` and each named method.
- **FR-005**: The raw-config compatibility layer (`removed_keys.go`) MUST stop treating `token_exchange`
  and `entra_obo` as retired: load normalisation and every write door (PATCH, apply, REST, MCP) MUST keep
  them. Tests MUST pin that boot, reload, PATCH and apply preserve an exchange block and never degrade a
  user-credential server to static calls.
- **FR-006**: The Spec 107 FR-035 guard test MUST keep asserting the absence of the deleted
  `Router`/`ToolFilter`/workspace/`HeaderInjector`/`ConnectionKey` chain and of per-(user, server)
  connection keying, and newly allow the exchange modes and the injection described here. The "stored,
  not injected" doc sentences MUST be rewritten to describe the opt-in injection.

**Transport and disclosure**

- **FR-007**: User credentials MUST travel in a trusted context distinct from the Spec 112 client-header
  snapshot. The non-`tools/call` body gate, the cross-origin redirect filter and every scrub sink MUST
  cover both contexts, and they MUST work with `forward_client_headers` off and with no inbound header
  snapshot. The gate MUST tell a message positively identified as something other than `tools/call`
  apart from a request it cannot inspect (body over the inspection limit, a read failure, a batch). It
  strips trusted credentials from the former. It MUST refuse to dispatch the latter whenever it carries
  a trusted user credential, so a user call never goes out anonymously or under another identity.
  Fixtures cover oversized bodies and body-read failures.
- **FR-008**: The raw stored value and the rendered header value MUST be redacted from client-visible
  results, error texts, `read_cache` payloads, logs, activity rows and audit lines. Activity and audit
  MUST record `credential_source` and the slot name, never a value.
- **FR-009**: `read_cache` entries produced under a user credential MUST be bound to the credential
  owner's user ID, the identity and generation of **every** contributing record (each slot used, or the
  exchanged or connect record), and the broker-config fingerprint, in addition to today's principal and
  forwarding facts. Replacing any one contributing slot MUST invalidate the entry, and a two-slot test
  MUST replace each slot independently. Redemption MUST be refused for any other user, for administrator
  readers, for recursive child pages under another identity, and after deletion, replacement, expiry or
  broker-config change.
- **FR-010**: A user-attributable authentication failure (401/403 from the upstream on a user call, or a
  token-endpoint refusal) MUST be recorded against that user's credential status only. It MUST NOT set
  or clear the server's shared OAuth sign-in or health state. Discovery and transport failures keep
  affecting shared health as today.

**User self-service and admin**

- **FR-011**: Users MUST be able to set, replace and delete their own slot values for servers in their
  entitlement through the Web UI, `mcpproxy credential set|delete`, and REST. All of these MUST be
  session-authenticated and never reachable through an agent token or `/mcp`. Values MUST be write-only
  after saving.
- **FR-012**: Administrators MUST be able to list, per server and slot, which users have a value (set
  time, last success, last failure class) without seeing values, and to delete one or all users' values
  for a slot. Each change MUST write an activity row of the `credential_broker` type
  (administrator-only on every read door).
- **FR-013**: A user-credential server MUST report its readiness in shared health without user specifics
  (e.g. "needs each user's `litellm_key`"). A user's own Credentials view MUST show their status per
  server: `set`, `not_set`, `expired`, `refused` or `unreadable`.

**Exchange and connect**

- **FR-014**: `auth_broker.mode` MUST accept `token_exchange` (RFC 8693 with `audience`/`resource`,
  `scopes`, client authentication `client_secret_basic|client_secret_post`) and `entra_obo`, on
  streamable-HTTP upstreams only.
- **FR-015**: Token, exchange and refresh endpoints MUST be `https`. The one exception is loopback hosts
  under an explicit test flag. These requests MUST NOT follow redirects. Tests MUST prove that a redirect
  or a non-https endpoint never transmits a subject token, refresh token or client secret.
- **FR-016**: Only when at least one server uses an exchange mode, the server edition MUST capture the
  delegation grant at login and store it encrypted as a `grant` record bound to that session. It MUST
  purge the grant and its exchanged tokens at logout of that session, session expiry, grant replacement
  by a newer login, or user deactivation. It MUST NOT request offline access or store an IdP refresh
  token. `store_idp_tokens` stays a no-op.
- **FR-017**: `entra_obo` MUST require that the server-edition login requests the OIDC scopes plus
  **only** mcpproxy's own resource scope (a configured `obo_scope`, e.g. `api://<client-id>/.default`).
  Graph scopes are removed for that deployment, because Entra issues an access token for one resource per
  request. User identity MUST then come from the verified ID token's claims (`email`, falling back to
  `preferred_username`/`upn`), never from Graph `/me` with the OBO assertion. When none of those claims
  is present, login MUST fail with a fixed error. The Microsoft provider subject MUST stay stable across
  the switch. An existing account bound to Graph's `id` MUST be matched through the ID token's `oid`
  claim, which is the same Entra object id. An account bound to the ID token's `sub` keeps matching on
  `sub`. The matched user keeps its user ID, role and credential ownership. A login whose claims match
  neither stored subject follows the existing administrator-armed rebinding path, never an automatic
  rebind. Tests MUST enable OBO with an existing Graph-bound administrator and an existing `sub`-bound
  user, and assert that both log in unchanged. Validation MUST reject `entra_obo` when the login
  provider is not Microsoft or `obo_scope` is unset. Tests MUST assert the actual authorization request's
  scopes, login completion without Graph, and a fixture AS that rejects an assertion with the wrong
  audience.
- **FR-018**: Before every dispatch and every cache redemption, an exchanged token MUST be checked
  against its effective expiry and its grant's validity, independently of physical cleanup. It MUST be
  reused only until its effective expiry minus 60 s.
- **FR-019**: Every exchange and refresh MUST be single-flight per (user, server) and MUST publish and
  persist its result only if the record generation and broker-config fingerprint are unchanged since it
  started. Otherwise the result MUST be discarded, and waiters MUST receive the current state.
- **FR-019a**: A pending `oauth_connect` consent flow MUST be bound, when `/connect` is initiated, to the
  record generation and broker-config fingerprint. Disconnect, a newer connect initiation and a config
  change MUST invalidate older pending flows. A callback MUST persist only if its original fence still
  matches. Tests MUST cover a disconnect before the callback begins, and two consent flows completed in
  reverse order.
- **FR-020**: An `oauth_connect` server with `inject: true` MUST inject the user's stored access token
  and refresh it under FR-019. Without `inject`, behaviour MUST be unchanged, and existing connect
  records MUST remain readable.
- **FR-020a**: Only Bearer access tokens may be injected. RFC 8693 requests MUST send
  `requested_token_type=urn:ietf:params:oauth:token-type:access_token`, and MUST require
  `issued_token_type` to equal it and `token_type` to be `Bearer` (case-insensitive). OBO, refresh and
  connect results MUST have `token_type` `Bearer`. Any other result MUST be refused with
  `MCPX_USER_CREDENTIAL_REFUSED` and never injected. Negative fixtures cover ID tokens, refresh tokens
  and `DPoP`/`N_A` token types.
- **FR-021**: Token-endpoint failures MUST be reported to the agent by fixed error class
  (`MCPX_USER_REAUTH_REQUIRED`, `MCPX_USER_CREDENTIAL_REFUSED`), never with the AS response body.

**Store**

- **FR-022**: The credential store MUST hold namespaced records per (user, server key, kind, name), with
  a generation and a broker-config fingerprint. It MUST read today's one-record-per-(user, server)
  connect records as `connect` records without loss. Session revocation, logout, user deactivation and
  server removal MUST purge the linked records, and a startup reconciliation pass MUST remove orphans.
- **FR-023**: Each user-credential use MUST add no upstream round trips beyond the token-endpoint call
  that FR-014 requires on a cache miss or refresh.

### Key Entities

- **Credential record**: see Definitions. Kinds: `slot`, `connect`, `exchanged`, `grant`.
- **Delegation grant**: (user ID, session ID) → encrypted IdP access token, token expiry, session expiry.
  At most one live grant per user.
- **Broker-config fingerprint**: a hash of the server's credential-relevant config. A change invalidates
  `exchanged` and `connect` records and fences their in-flight acquisitions.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a two-user test against a recording fixture, 100% of each user's `tools/call` requests
  carry only that user's credential and no operator credential. 0 non-`tools/call` requests carry any
  user credential, with forwarding on and off.
- **SC-002**: 0 successful cross-identity `read_cache` redemptions across the FR-009 matrix (other user,
  administrator reader, child pages, after delete/replace/expiry/config change).
- **SC-003**: A Gcore-style deployment (one server, LiteLLM upstream, 10 users) shows 10 distinct
  identities in the upstream's logs, without running more than one mcpproxy.
- **SC-004**: Per-call overhead of slot injection is under 1 ms p95. Token exchange adds exactly one
  token-endpoint call per (user, server) per token lifetime.
- **SC-005**: After logout of the grant's session, no call or cache redemption succeeds with a token
  derived from that grant, effective immediately. A raw `config.db` scan one minute later finds no
  grant or exchanged record for it (as in the Spec 123 e2e).
- **SC-006**: Personal-edition behaviour and config compatibility are unchanged. Both editions build, the
  existing e2e suites are green, and the new fields are preserved opaquely.
- **SC-007**: Every race in the FR-019 matrix (refresh against disconnect, reconnect, config change and
  logout) ends with no resurrected or overwritten record.

## Assumptions

- Target upstreams authenticate each HTTP request (true for LiteLLM's MCP gateway with
  `x-litellm-api-key`/`Authorization`, and for typical bearer APIs). Session-bound upstreams are
  excluded by FR-002.
- Users reach `/mcp` only through owned agent tokens (Spec 107 FR-003). A `UserID` is present on every
  user call and absent on operator calls.
- A shared tool index built under the operator's discovery identity is acceptable. Calls that a user's
  upstream identity cannot perform fail on the upstream side.
- Today's AES-256-GCM credential store and master-key resolution (`credential_encryption_key`, then
  `MCPPROXY_CRED_KEY`) are kept. The record layout changes per FR-022.

## Open Questions for the Plan

- Whether to support IdP refresh tokens (offline access) for long-running agents. That would reverse part
  of FR-033 behind an explicit opt-in.
- Whether `${user:…}` should extend to SSE once it gains a per-request header seam.
- Whether to mark a slot `refused` automatically after N consecutive upstream 401s, or only on an explicit
  test.
- Whether ID-JAG (Okta cross-app access) is worth a follow-up mode on the same exchange path.
