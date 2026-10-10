# Feature Specification: MCP Credential Lifecycle — Admin-Issued Client and Token Credentials over MCP

**Feature Branch**: `115-mcp-credential-lifecycle`
**Created**: 2026-10-10
**Status**: Draft (design complete; implementation pending)
**Input**: GitHub issue [#1552](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1552), "Expose admin client/token credential creation and revocation over MCP". The issue's capability, end-to-end and UI acceptance criteria are binding. This spec restates each one as a numbered requirement (FR/E2E/UI), and the traceability table at the end maps every issue checkbox to them.

**Related**: Spec 108 (profiles v3: client credentials, bindings, the `profiles` admin MCP tool, the FR-008a binding guard, `profile_change` records), Spec 105 (agent-token scope hardening), Spec 107 (credential kinds, token expiry cap), Spec 028 (agent tokens), Spec 109 (navigation, scope URL filters, deep links). Issue #1548 (preflight/dispatch policy parity) is being fixed separately. This spec never uses preflight as evidence of a grant. Grants are verified with `effective_tools`, `explain` and real calls.

## Context and Motivation

v0.70.0 lets a trusted admin agent do most access administration over MCP. Through the `profiles` tool it can create a scenario profile, check `effective_tools`, run `explain`, and move an existing client between profiles. It cannot finish the delegation loop:

`task brief → profile → review assumptions/effective access → create locked client or pinned token → verify worker → inspect results → revoke`

The captured reproduction (issue #1552, v0.70.0 darwin/arm64) and the code at `8282c3865` show these gaps:

| # | Observed | Code today |
|---|---|---|
| G1 | An admin's `tools/list` has no credential-management tool | `profiles` operations are `list, get, create, update, delete, rename, classify, assign, list_clients, effective_tools, explain` (`internal/server/mcp_profiles_tool_schema.go`) |
| G2 | `profiles assign` of a fresh client fails with `no_client_credential` | `ClientsService.SetBinding` only re-binds an existing active credential (`internal/runtime/clients_service.go`) |
| G3 | There is no MCP way to mint an agent token | Token minting is written inline in the REST handler `handleCreateToken` (`internal/httpapi/tokens.go`). There is no runtime service MCP could reuse |
| G4 | There is no MCP way to revoke either kind | Revocation exists only as REST `DELETE /api/v1/tokens/{name}`, `DELETE /api/v1/clients/{id}`, and the CLI |
| G5 | Token create/revoke leave no audit trail and send no live event | `handleCreateToken`/`handleRevokeToken` write no activity record and publish no SSE event. The Tokens view therefore does not update live, and Activity cannot show who issued a token |
| G6 | Client revocation (`forget`) writes a record but publishes no event | `forgetLockedOpt` writes `profile_change` without calling `announce` |
| G7 | A profile with a `read` cap and an explicit write `allow` is labelled "Read-only" | `tierPhrase(max_tier)` in `frontend/src/utils/profiles.ts`. An explicit allow bypasses the cap (`internal/profile/policy.go`) |

Restricted workers already behave correctly. They cannot write past their grant, cannot reach admin operations and cannot switch profile. Reassignment applies on the next request, and CLI revocation returns HTTP 401 on the next request of a live session. The gap is in **provisioning and lifecycle**, not in enforcement. This spec adds the missing lifecycle on top of the existing services and guards. It does not add a parallel authorization model.

## Scope Boundary

| Already exists (reused, not rebuilt) | Added or extended by this spec |
|---|---|
| `ClientsService.Add/Forget/List/Get` (custom client mint, revoke and views), the FR-008a binding guard, `bindingWriteMu` | `runtime.CredentialsService`: one facade over both credential kinds, whose client operations delegate to `ClientsService` |
| Agent-token storage (`storage.Manager.CreateAgentToken/RevokeAgentToken/...`), `auth.ParseTokenExpiry`, `auth.GenerateToken` | Token create/revoke logic moves out of the REST handler into `CredentialsService`. REST and CLI call it, so every surface gets the same audit record and live event |
| The `profiles` admin-tool visibility rule (`profilesToolAccess`: api_key/socket credential, effective profile none or `management_tools: true`) and its per-call `read_only_mode`/`disable_management` refusals | New built-in MCP tool `credentials` under the **same** visibility and gate rules |
| Per-request token validation in `mcpAuthMiddleware` (revoked or expired → 401 on the next request) | Unchanged. MCP revocation reaches it through the same store |
| `profile_change` activity records (actor, surface, profile, client_id, token_name, diff) | Two new change kinds: `issue` and `revoke` |
| SSE `client.binding_changed` and `profiles.changed` | New invalidation event `credentials.changed` |
| Profile resolver: a pin is authoritative and a dangling pin denies all (`profile_resolver_v3.go`) | Unchanged. E2E tests pin it for MCP-created identities |
| Web UI Clients, Tokens, Profiles and Activity views | Show issuer, lease-style expiry, revoked time and stated purpose. Refresh live on `credentials.changed`. Link each client in the profile's "Assigned to" list. Drop the plain "Read-only" tier label when exceptions exist |

**Editions.** The target is the personal edition. In the server edition the `credentials` tool is registered and visible under the same rule. Its token operations behave like the administrator `/api/v1/tokens` routes, which exist in both editions. Its client operations answer `unsupported_edition`, because the server edition has no client credentials (Spec 108). Server-edition per-user tokens (`/api/v1/user/tokens`) are out of scope.

**Transport.** The HTTP MCP endpoints `/mcp`, `/mcp/call`, `/mcp/code` and `/mcp/p/<slug>` carry the tool on the same servers that carry `profiles`. Like `profiles`, it is not reachable from direct mode (`/mcp/all`) or from `code_execution` JavaScript.

## Definitions

- **Admin caller**: an MCP request that is authenticated by the instance API key (`credential_kind = api_key`) or the tray socket (`socket`), is not anonymous, and whose effective profile is none or sets `management_tools: true`. This is exactly the existing `profiles` visibility predicate. Agent tokens, client credentials, anonymous callers and server-edition OAuth users are never admin callers for this tool.
- **Worker identity**: a credential created for a task. It is either a **custom client credential** (`kind = client`, `mcp_cli_` secret, `mode = locked` by default) or a **profile-pinned agent token** (`kind = agent`, `mcp_agt_` secret, `profile_pin` set, scope taken from the profile).
- **Issue**: the creation of a worker identity. **Revoke**: marking it revoked. Both change soft state on the credential record, which is kept for history.
- **One-time delivery response**: the `credentials` `create_client`/`create_token` result returned to the admin caller that requested it. It is the only place the raw secret appears.
- **Lease**: a credential whose lifetime at issue (`expires_at − created_at`) is at most 24 h. Its expiry is a planned task end, not a warning.
- **Stated purpose**: optional free text the admin agent records on the credential, such as the task brief and its assumptions. It is display-only and never enforced.

## User Scenarios & Testing

### User Story 1: Admin agent provisions a locked worker client over MCP (Priority: P1)

A trusted admin agent connected with the API key creates a restrictive profile, then creates a custom client credential locked to it. It hands the one-time secret and the endpoint snippet to the worker. The worker's requests are confined to the profile. When the task ends the agent revokes the client, and the worker's next request on its existing session fails.

**Why this priority**: this is the core gap (G1, G2, G4). Without it the delegation loop needs the CLI.

**Independent Test**: E2E-1 (fresh client path), with administration over MCP only.

**Acceptance Scenarios**:

1. **Given** an admin caller and an existing profile `daily-research`, **When** it calls `credentials` with `{"operation":"create_client","client":"delegated-worker","profile":"daily-research","expires_in":"1h"}`, **Then** the result contains the client's safe view (`mode: locked`, `profile: daily-research`, `expires_at`, `lease: true`), the `credential` secret, a snippet carrying it in `X-API-Key`, and deep links. One `profile_change` record with `change: issue` is written, and it does not contain the secret.
2. **Given** that credential, **When** a worker initializes `/mcp` with it and calls an admitted read tool, **Then** the call dispatches upstream exactly once. Write tools, tools on another server, tools the profile excludes, `profiles`, `credentials`, and `set_profile` to another profile are all refused with zero upstream dispatches.
3. **Given** the worker's live session, **When** the admin calls `{"operation":"revoke","client":"delegated-worker"}`, **Then** the worker's next request on the same session returns HTTP 401 `Agent token invalid: token has been revoked`.

### User Story 2: Admin agent issues a profile-pinned token with a short lease (Priority: P1)

The admin agent creates an agent token pinned to a profile with a short expiry. It verifies the grant with `profiles effective_tools`/`explain` and real calls, then revokes the token over MCP. Separately, the token must stop working when its lease ends, even if nobody revokes it.

**Why this priority**: it closes G3 and G4 for the second identity kind the issue names.

**Independent Test**: E2E-2.

**Acceptance Scenarios**:

1. **Given** an admin caller, **When** it calls `{"operation":"create_token","name":"research-task-42","profile":"daily-research","expires_in":"30m"}`, **Then** it receives the token's safe view (`profile_pin`, `expires_at`, `lease: true`) and the `credential`, once.
2. **Given** that token, **When** a worker calls tools, **Then** each allowed or denied outcome matches `profiles explain token=research-task-42` for that tool and the `effective_tools` rows of the profile.
3. **Given** the worker's live session, **When** the admin revokes the token over MCP, **Then** the next request on that session returns 401.
4. **Given** a token issued with an expiry short enough for the test (`expires_in: "3s"` in Go tests, `"90s"` on the live harness), **When** the expiry passes, **Then** the next request returns 401 `token has expired` with no admin action.

### User Story 3: Live reassignment without reconnecting (Priority: P1)

The admin agent moves an existing MCP-issued client from a research profile to a triage profile that has one exact write exception. The worker's next request on the same session uses the new grant, without reconnecting or rewriting its config. A separate token pinned to research keeps its grant.

**Independent Test**: E2E-3, using the existing `profiles assign` on an identity created by `credentials`.

**Acceptance Scenarios**:

1. **Given** client `w1` locked to `research` and token `t1` pinned to `research`, **When** the admin calls `profiles assign client=w1 profile=triage`, **Then** `w1`'s next call to the triage write exception dispatches once, and `t1`'s call to the same tool is refused with zero dispatches.

### User Story 4: Workers and unauthenticated callers cannot administer credentials (Priority: P1)

A worker client, a worker token, an anonymous caller (`require_mcp_auth` off), and an admin whose session selected a non-management profile can neither see nor call `credentials`. The handler checks authorization on every call, so hiding the tool is not the only protection.

**Independent Test**: E2E-4 negative paths, plus handler unit tests per caller kind.

**Acceptance Scenarios**:

1. **Given** each non-admin caller kind, **When** it lists tools, **Then** `credentials` is absent. **When** it calls `credentials` anyway with a forged `tools/call`, **Then** the result is `unknown tool: credentials`, no record changes, and no audit record names a credential change.
2. **Given** an admin session that `set_profile`d into a profile without `management_tools`, **When** it calls `credentials`, **Then** the call is refused the same way. After `set_profile("")` the tool is visible again, because the admin keeps its authority.
3. **Given** `read_only_mode: true` or `disable_management: true`, **When** an admin calls `create_client`, `create_token` or `revoke`, **Then** the call is refused with `read_only_mode` or `management_disabled` and nothing changes. `list` and `get` still work.

### User Story 5: The user reviews the agent's work in the Web UI (Priority: P2)

The user opens the deep links the agent returned. The Clients and Tokens views show the new identity with:

- its profile and lock or pin
- the issuer ("via MCP · api_key")
- the expiry, presented as a planned lease
- the stated purpose, labelled unenforced
- whether it is active or revoked

The profile editor's **Assigned to** section links to the filtered identity view. Tools → Callable shows the exact callable set, including write exceptions. Activity shows the allowed calls, the blocked policy decisions with reasons and identities, and the lifecycle records (`issue`/`revoke`) without the secret. Open views update without a manual refresh.

**Independent Test**: UI verification (Playwright plus screenshots) against an isolated daemon after E2E-1/E2E-2 provisioning.

**Acceptance Scenarios**:

1. **Given** the Clients view is open, **When** the agent issues, reassigns or revokes over MCP, **Then** the row appears, changes or shows Revoked within one SSE round-trip, with no reload.
2. **Given** the profile editor has unsaved edits, **When** the agent changes that profile or its assignments, **Then** the draft is kept and the existing reload affordance ("Reload") is offered.
3. **Given** a lease credential with 50 minutes left, **When** it is shown, **Then** it reads "Lease ends in 50 min" in neutral styling, with no "expiring soon" warning and no reconnect prompt. After the lease ends it reads "Lease ended", as an outcome. A revoked credential reads "Revoked <time> by <actor>".
4. **Given** a profile `triage` with `max_tier: read` and one explicit write allow, **When** its card or editor is shown, **Then** the tier reads "Read-only + 1 write exception", never plain "Read-only".

### Edge Cases

- **Duplicate identity**: `create_client` for an id that has any record (active, expired or revoked), or `create_token` for a name that has any record, answers `identity_exists` with the existing `state`. Nothing is minted. Revoked identities are never revived, and two credentials' histories never merge. A token name starting with `client-`, or a client id that belongs to a supported (connect-registry) client, answers `reserved_identity`.
- **Invalid or missing profile**: `profile` is required and must name an existing profile. A missing profile answers `missing_argument` (field `profile`), and an unknown one answers `unknown_profile`. The empty string (All servers) is refused with `profile_required`, so nothing silently defaults to full access.
- **Profile deleted after issue**: the pin dangles and the resolver denies everything. E2E-4 checks zero dispatches, and `get` reports `profile_state: dangling`. Deleting a profile that is in use is already refused unless `force` or `reassign_to` is given (Spec 108).
- **Malformed expiry**: `expires_in` is required. A value that does not parse, is not positive, or exceeds 365 days answers `invalid_expiry` (field `expires_in`). This includes day counts whose duration would overflow (`106752d`, `9999999999d`); the parser checks the bound before multiplying (A16).
- **Concurrent conflicting requests**: two `create_*` calls for the same identity serialize on `bindingWriteMu`. Exactly one succeeds, and the other answers `identity_exists`. A `create_*` that races a profile delete either sees the profile, in which case the delete then counts the new pin as a user, or answers `unknown_profile`.
- **Binding guard**: when `require_mcp_auth` is off and the anonymous resolution is wider than the requested profile, both `create_client` and `create_token` are refused with the existing `binding_bypassable_without_auth` body, including fixes. This follows the issue's "server-enforced" requirement: a grant the worker could escape by dropping its credential is not issued.
- **Token cap**: once 100 tokens are stored (`auth.MaxTokens`), creation answers `token_limit_reached`.
- **Revoke idempotence**: revoking an already-revoked identity succeeds with `changed: false` and writes no new record. Revoking an unknown identity answers `identity_not_found`. Revoking an expired identity marks it revoked (`changed: true`).
- **Revoking a supported client** (one connected through connect) is allowed. Its config file is not modified, and the result says `client_config_untouched: true`. If a connect is in progress for that client, the call answers the existing `connect_in_progress`.
- **Lost delivery**: if the response never reaches the agent, the credential still exists and can be listed and revoked. Its secret cannot be recovered, by design.
- **Audit write failure**: the failure is logged at error, and the issue or revoke still stands (the existing `writeChangeRecord` convention). The `internal_tool_call` record still captures the call.

## Requirements

### Functional Requirements: Capability

- **FR-001**: The system MUST expose a built-in MCP tool `credentials` with the operations `list`, `get`, `create_client`, `create_token` and `revoke`. The schema, descriptions and errors are documented in [contracts/mcp-credentials-tool.md](contracts/mcp-credentials-tool.md) and in the docs page `docs/features/mcp-credential-lifecycle.md`.
- **FR-002**: `create_client` MUST mint a custom client credential through `ClientsService`. It takes the `client` id, an optional `display_name`, a required existing `profile`, a `mode`, a required `expires_in`, and an optional `purpose`. `mode` defaults to `locked`; `switchable` is used only when requested explicitly.
- **FR-003**: `create_token` MUST mint an agent token through the new `CredentialsService`. It takes a `name`, a required existing `profile` (stored as `profile_pin`), a required `expires_in`, and an optional `purpose`. Scope comes from the profile only: `allowed_servers` is `["*"]` and all three permissions are granted. The legacy `allowed_servers`/`permissions` arguments are refused as unknown arguments.
- **FR-004**: `create_client` and `create_token` MUST return the raw secret only in their own result, in the delivery format of [contracts/mcp-credentials-tool.md §Delivery](contracts/mcp-credentials-tool.md#one-time-delivery-response). The format mirrors REST `POST /api/v1/clients` (`credential` plus `snippet{generic_http, header_name}`) and adds `delivery` and `links`.
- **FR-005**: `list` and `get` MUST return safe metadata only:
  - kind, id or name, and display name
  - profile, plus the mode or pin source
  - state (`active|expired|revoked`) and `profile_state` (`ok|dangling`)
  - created, expires, revoked and last-used times
  - `lease`, issuer and purpose
  - `token_prefix`, the existing 12-character display prefix

  They MUST never return the raw secret, its HMAC hash, or the pending-rotation hash or prefix.
- **FR-006**: `revoke` MUST take exactly one of `client` or `token`. It marks the credential revoked through the existing store (`ClientsService` for clients, `RevokeAgentToken` for tokens) and stamps `revoked_at`. Revocation MUST make every later request with that secret fail with 401, including requests on MCP sessions initialized before the revocation.
- **FR-007**: No operation may silently widen scope. A missing or invalid profile, a malformed expiry, a duplicate or reserved identity, or a guard refusal MUST fail before anything is written, and leave no credential behind. Each check runs under `bindingWriteMu`, and the mint is a single storage transaction. Nothing after the commit may turn the call into a failure: the delivered view is projected from the committed record (no post-commit store read), and audit, events and links are best-effort. A call either delivers the secret or leaves no active credential and no issuance record (research D6).
- **FR-008**: `CredentialsService` MUST serialize every create and revoke on the runtime's `bindingWriteMu`. `ClientsService` and the guarded config apply already share this mutex, so a profile delete or rename cannot interleave with an issue.
- **FR-009**: Token create and revoke on REST (`POST/DELETE /api/v1/tokens`) and the CLI (`mcpproxy token create|revoke`, which call REST) MUST go through `CredentialsService` and keep their existing request and response shapes. They gain the audit record (FR-021) and the live event (FR-024). They do **not** gain the FR-012 guard refusal (see Assumption A9).
- **FR-010**: REST `POST /api/v1/clients` (custom client add, and CLI `client add`) MUST go through the same `CredentialsService` issue path and write `change: issue` instead of today's `change: assign`. This contract change is deliberate, so that every surface records issuance the same way.

### Functional Requirements: Authorization and Gates

- **FR-011**: Only an admin caller (see Definitions) may see and call the `credentials` tool. The tool filter hides it from everyone else, and the handler re-checks the same predicate on every call. A refused call gets `unknown tool: credentials`, the uniform hidden-tool text. When the refused caller is an attributable administrator credential hidden only by its profile, the handler records a management refusal exactly as `profiles` does.
- **FR-012**: `create_client` and `create_token` MUST run the FR-008a binding guard over the candidate state. For `create_token`, the candidate token counts as a locked named binding to its pin. If the guard reports a delta, the call answers `binding_bypassable_without_auth` with fixes and nothing is minted.
- **FR-012a**: The confinement MUST persist after issuance. An MCP-issued token is stored with `guard_bound: true` and counts as a locked named binding in **every** later guard evaluation, exactly like a client binding: config writes through the API funnel (`MutateConfig`, `GuardedApplyConfig`) that would turn `require_mcp_auth` off or remove or widen a confining `anonymous_profile` are refused with `binding_bypassable_without_auth` naming the token; a file-watcher hot reload doing the same is applied but the per-request anonymous guard then denies every anonymous request while the token is active. The binding stops counting when the token is revoked or expired (data-model §5).
- **FR-012b**: A guarded binding whose pinned profile no longer exists (a dangling pin, left by `profiles delete force=true`) MUST still count as bypassable whenever anonymous access is not itself deny-all. The credentialed request is deny-all, so any anonymous access that grants anything is wider. The API funnel therefore refuses turning `require_mcp_auth` off, or removing or widening `anonymous_profile`, while such a binding is active, and after a hot reload that does either, the anonymous request guard denies every anonymous request until the binding is revoked, expires or is reassigned to an existing profile. No `anonymous_profile` fix is offered for a dangling binding, because the target does not exist; the offered fixes are turning auth on, revoking, or reassigning. This changes the Spec 108 verdict for a locked client with a dangling pin in the same way (a deliberate tightening; data-model §5, research D4).
- **FR-013**: `create_client`, `create_token` and `revoke` MUST be refused per call, reading the live config, in two cases. Under `read_only_mode` the refusal is `code: read_only_mode` with the text `Operation not allowed in read-only mode`. Under `disable_management` it is `code: management_disabled` with the text `Server management is disabled for security`. Both texts are byte-equal to those of `profiles` and `upstream_servers`. `list` and `get` remain available.
- **FR-014**: `require_mcp_auth` and anonymous handling MUST stay unchanged. An anonymous caller is never an admin caller for this tool, whether `require_mcp_auth` is on or off.
- **FR-015**: The tool MUST NOT be reachable from `code_execution` JavaScript or from direct mode (`/mcp/all`). It is registered on exactly the servers that register `profiles`.

### Functional Requirements: Worker Enforcement (Verifying Existing Behaviour)

- **FR-016**: A locked MCP-issued client and a pinned MCP-issued token MUST resolve with source `pin`. Neither may change its effective profile through `set_profile`, URL profile paths (`/mcp/p/<slug>`) or session state. This is existing FR-020 behaviour, pinned here by tests.
- **FR-017**: Unknown or unannotated tools MUST follow the profile's `unannotated` policy, and tools outside the profile's servers MUST be refused. This holds on every routing surface the worker can reach: `/mcp` retrieve plus `call_tool_*`, `/mcp/call`, nested calls under `/mcp/code`, and direct calls under `/mcp/all`. Refusals produce zero upstream dispatches.
- **FR-018**: A dangling pin, left when a profile is deleted with `force`, MUST deny everything (existing behaviour). `get` MUST report `profile_state: dangling`.

### Functional Requirements: Errors

- **FR-019**: Every refusal MUST be a tool result with `isError: true`. Its text is a JSON object `{code, error, field?, ...}` whose code comes from the catalog in [contracts/errors.md](contracts/errors.md). Existing codes (`binding_bypassable_without_auth`, `connect_in_progress`, `no_client_credential`) keep their bodies. New codes are added to `internal/profile/contract.go` and to the enums golden.
- **FR-020**: Error texts MUST NOT include secrets, hashes, storage internals or stack traces. They echo an argument value only when it passed the secret-shaped input screen and its own syntax or enum check; free text (`purpose`, `display_name`) and unparsed values (`expires_in`, an unknown `operation`) are never echoed.
- **FR-020a**: The **whole** argument payload of every `credentials` call MUST pass a secret-shaped input screen before any other check (data-model §8). The screen walks every key and every value at every depth, whatever its JSON type (strings, numbers and booleans as their JSON text, arrays, objects, and the argument keys themselves, known or unknown). It rejects any key or value containing an issued-credential prefix (`mcp_agt_`, `mcp_cli_`), the configured API key, or anything the existing `internal/security` detector flags. A hit answers `secret_in_argument`, persists nothing, and replaces the recorded arguments wholesale with a server-built sanitized summary that contains no caller-supplied key or value other than known argument names and a known `operation` enum value. `field` names a known argument, or the fixed placeholder `"(unknown argument)"`; a caller-supplied key is never echoed. All offending locations are found, not only the first. The handler logs only the sanitized form.

### Functional Requirements: Audit and Secret Handling

- **FR-021**: Each successful issue or revoke MUST write one `profile_change` activity record with `change: issue|revoke`. The record carries:
  - the actor: `actor_kind`, `actor_name` and `surface`
  - `client_id` or `token_name`, and `profile`
  - a `diff` holding `credential_kind`, `expires_at`, `lease`, `token_prefix` and `purpose_set`, plus `mode` for clients or `pin_source: token_pin` for tokens

  The record MUST NOT contain the secret or its hash. A failed attempt writes no `profile_change`. Its `internal_tool_call` record, with status `error` and the structured body, is the audit of the attempt.
- **FR-022**: For a successful `create_*`, the `internal_tool_call` activity record MUST store a server-built safe summary as its response body, with `credential` and `snippet` replaced by `"[REDACTED: one-time credential]"`. It MUST NOT store a redaction of the delivered text, because redaction by key name alone is not trusted here.
- **FR-023**: The raw secret MUST NOT appear in any of these places:
  - activity records or their exports (`/api/v1/activity`, `/export`)
  - SSE payloads (`activity.*`, `credentials.changed`)
  - the main log or per-server logs, at any level including debug
  - MCP notifications or error texts
  - the config file
  - `config.db`, where only its HMAC hash may be stored

  A test MUST search each of these sinks for the full secret, including after the secret has been fed back as input to every free-text argument and to each invalid-input path (FR-020a).

### Functional Requirements: Live Updates

- **FR-024**: Every issue, revoke and client forget MUST publish a `credentials.changed` SSE event `{kind, id, token_name, change, profile}`. The event is an invalidation only and carries no secret and no prefix. Binding changes keep publishing `client.binding_changed`.

### Functional Requirements: Provisioning versus Installation

- **FR-025**: The tool MUST NOT write any client application's config and MUST NOT expose connect. For a custom client, the delivery response carries the endpoint and header snippet, together with a `delivery.install_note` stating that installation is the caller's responsibility. Supported clients keep using connect (preview, commit, staged rotation) from the Web UI, CLI or REST. That flow is unchanged and is out of scope for MCP.

### UI Requirements

- **UI-001**: Rows for custom clients and for tokens MUST show:
  - the profile
  - the lock or pin source ("Locked to X" or "Pinned to X")
  - the issuer ("via MCP · api_key", "via Web UI" and so on)
  - the expiry
  - the state: Active, Expired, or Revoked with `revoked_at`
  - the stated purpose, labelled "Stated purpose — not enforced"
- **UI-002**: The profile editor's **Assigned to** section MUST link each client to `/clients?client=<id>` and each token to `/clients?tab=tokens&token=<name>` (the token link exists today). ProfileCard "Used by" links filter Clients and Tokens by `profile`, as they already do.
- **UI-003**: When a profile admits tools above its cap, its tier label MUST name the exceptions, for example "Read-only + N write/destructive exception(s)" or the equivalent for a write cap. Plain "Read-only" appears only when nothing above read is admitted.
- **UI-004**: Clients, Tokens and the profile editor's Assigned to section MUST refetch on `credentials.changed`, `client.binding_changed` and `profiles.changed`. The profile editor MUST keep unsaved drafts and show its existing Reload affordance.
- **UI-005**: Lease credentials MUST show "Lease ends in …" and, afterwards, "Lease ended", with no warning styling and no reconnect call to action. Non-lease credentials keep today's expiring-soon styling.
- **UI-006**: Activity MUST render `profile_change` `issue`/`revoke` records with readable labels ("Issued client credential", "Revoked token" and so on), the actor and surface, and links to the identity and the profile. These records MUST be filterable by `client`, `token`, `profile` and `type=profile_change`, using filters that already exist.
- **UI-007**: The create result's `links` MUST give deep links to the identity view, the profile editor (Tools tab → Callable), the profile's effective tools, and Activity filtered to the identity. No URL may contain an API key.

### Key Entities

- **Credential record** (`auth.AgentToken`): adds the fields `revoked_at`, `issuer {actor_kind, actor_name, surface}`, `purpose` and, for MCP-issued tokens, `guard_bound` (data-model.md §1, §5).
- **Credential view**: the safe projection that `list`, `get` and REST return (data-model.md §2).
- **Lifecycle record**: a `profile_change` with `change ∈ {issue, revoke}` (data-model.md §3).
- **credentials.changed event**: data-model.md §4.

## Success Criteria

- **SC-001**: The full loop (profile → issue → worker verification → reassign → revoke) runs with all administration over MCP. No step uses the CLI, a REST management call or a direct store mutation (E2E-1 to E2E-3 green).
- **SC-002**: Across every E2E scenario, each denied or revoked call produces zero upstream dispatches according to the fixture's ledger.
- **SC-003**: A substring search for each issued secret across logs, activity, exports, SSE captures, notifications and the config file finds it only in the one-time response (E2E-5).
- **SC-004**: `credentials` is absent from `tools/list` for every non-admin caller kind, and a forged call from each kind is refused with no state change (E2E-4).
- **SC-005**: An open Clients view reflects an issue, reassign or revoke within 2 s with no reload (UI verification with screenshots).
- **SC-006**: The `credentials` definition adds at most 1,200 cl100k tokens to an admin's `tools/list`, measured with the existing token-bench helper and recorded in the commit that updates the goldens.

## Assumptions

- **A1 (edition)**: the personal edition is the target. In the server edition, client operations answer `unsupported_edition` and token operations match `/api/v1/tokens`. Per-user tokens are out of scope.
- **A2 (admin authority)**: "admin" means the api_key or socket credential kinds, as for `profiles`. Server-edition OAuth administrators cannot use the tool in this spec.
- **A3 (write gates cover revoke)**: `read_only_mode` and `disable_management` block `revoke` as well as `create_*`. This matches `profiles`, where every write is refused. These are MCP-level flags, so the human can still revoke from the Web UI, CLI or REST.
- **A4 (explicit expiry)**: MCP requires `expires_in` and has no default. The cap is 365 days (`auth.MaxTokenExpiry`), and the lease threshold is 24 h.
- **A5 (no identity reuse)**: MCP refuses any id or name that already exists, revoked ones included, so agents choose task-scoped ids. REST/CLI `client add` can still re-issue a revoked custom client (unchanged).
- **A6 (best-effort audit)**: as in the existing `writeChangeRecord`, a failed activity write is logged and does not undo the issue or revoke.
- **A7 (header)**: the snippet uses `X-API-Key`, the existing custom-client convention, and never a query parameter. `/mcp` also accepts `Authorization: Bearer`, and the docs say so.
- **A8 (macOS)**: this spec makes no macOS UI change. The tray reads the same REST fields, and showing issuer, lease and purpose there is a follow-up.
- **A9 (REST guard unchanged)**: under `require_mcp_auth: false`, REST/CLI `token create` keeps today's behaviour and issues without the guard. Enforcing the guard there would refuse token creation on most default installs. The MCP path, which issues worker identities for agents, does enforce it, because the issue requires server-enforced grants.
- **A10 (purpose)**: `purpose` is optional, at most 500 characters, stored as given once it passes the secret-shaped input screen (FR-020a), and shown as unenforced prose. Activity metadata records only `purpose_set: true`, never the text.
- **A11 (verification without preflight)**: the E2E suite never asserts on `preflight` or required-tools readiness (issue #1548).
- **A13 (legacy and REST tokens are not standing bindings)**: only tokens minted through the MCP path carry `guard_bound`. Pre-115 tokens and REST/CLI tokens (even with `profile_pin`) do not participate in the guard, consistent with A9, so no existing install changes behaviour or starts refusing config writes on upgrade. A REST/CLI caller that wants the standing guarantee issues the token over MCP. A downgraded pre-115 binary ignores `guard_bound`; that is acceptable because downgrade already drops every Spec 115 guarantee.
- **A14 (secret screen reuses the detector)**: the screen calls the existing `internal/security.Detector` with all categories enabled, independent of the user's `sensitive_data_detection` toggles, because the input is admin-tool metadata rather than upstream traffic. A false positive (for example a high-entropy display name) is refused with `secret_in_argument`; the agent can rephrase. No new dependency or redaction model is introduced.
- **A15 (dangling bindings stay guarded)**: a binding whose pin was force-deleted is deny-all for its credential, so for the anonymous-bypass guard it is treated as the narrowest possible binding rather than as "cannot be bypassed" (FR-012b). This tightens Spec 108 for locked clients with dangling pins too: on an install that already has auth off, an unrestricted anonymous profile and such a client, the request guard starts denying anonymous calls after upgrade until the client is revoked or reassigned. This is accepted because that install is already in the unsafe state the guard exists to prevent; the existing guard notice names the binding and the fixes.
- **A16 (checked expiry arithmetic)**: `auth.ParseTokenExpiry` gains a bound check on the day count before multiplying (`days > MaxTokenExpiry/(24h)` is refused with the existing ">365 days" error), so no input can overflow `time.Duration`. REST and CLI keep their status codes and texts for ordinary inputs; inputs that used to overflow now fail instead of creating an already-expired or wrapped token.
- **A12 (reserved ids)**: connect-registry client ids (`cursor`, `claude-code`, …) cannot be created over MCP. Those clients are provisioned through connect.

## Out of Scope

- Rotating or regenerating either kind over MCP. The boundary is designed so that a later `rotate` operation can reuse `CredentialsService`.
- A unified task review or diff view, which the issue lists as a follow-up.
- Writing or installing any client's config, and exposing connect over MCP.
- Permanently deleting credentials over MCP. Revoked records are kept for history, and permanent delete stays in the Web UI, CLI and REST.
- Bulk revoke, revoke by profile, server-edition per-user tokens, and the macOS UI.
- Any change to how profiles, tiers or resolver tiers are enforced.

## Traceability to Issue #1552

| Issue criterion | Requirements | Tasks / tests |
|---|---|---|
| Native admin MCP operations create a client or token with profile, binding, expiry and name, reusing existing services | FR-001 to FR-003, FR-008 to FR-010 | T010–T024, E2E-1, E2E-2 |
| List/show safe metadata; revoke either kind; same-session invalidation; secret shown once | FR-004 to FR-006, FR-022, FR-023 | T025–T031, E2E-1, E2E-2, E2E-5 |
| Restricted and unconfined callers cannot discover or invoke; check at execution; require_mcp_auth, guard, read_only, disable_management | FR-011 to FR-015 | T032–T037, E2E-4 |
| Server-enforced grants; no silent All-servers default | FR-007, FR-012, FR-012a, FR-012b, FR-016 to FR-018 | T012–T014, T013a, T013b, T038–T040, E2E-1 to E2E-4 |
| Duplicate, invalid, malformed or conflicting requests get structured errors and leave no partial grant | FR-007, FR-019, FR-020, FR-020a | T015–T018, T015a, T038a, E2E-4 |
| Delivery separated from audit; redaction; audit metadata | FR-020a, FR-021 to FR-023 | T038a, T041–T046, E2E-5 |
| Provisioning versus installing client config | FR-025 | T047, docs |
| E2E: fresh client, fresh token with expiry, live reassignment, parity and negatives, secret handling, dispatch proof | E2E-1 to E2E-6 | T050–T056 (quickstart.md) |
| UI: identity, profile, lock or pin, expiry, state; Used by and Assigned to links | UI-001, UI-002 | T060–T063 |
| UI: assumptions versus enforced restrictions; exact callable tools including exceptions | UI-001 (purpose), UI-003 | T064–T065 |
| UI: existing review works with MCP-created credentials; Activity shows reasons and identities | UI-006 | T066, T071 |
| UI: live updates; unsaved edits preserved | FR-024, UI-004 | T067–T068 |
| UI: lease-style expiry | UI-005 | T069 |
| UI: lifecycle audit and deep links | FR-021, UI-006, UI-007 | T070–T071 |
