# Audit line — per-event contract (schema_version 1)

Machine-readable form: [`audit-line.schema.json`](audit-line.schema.json) (draft 2020-12; its `examples` validate and the forbidden-key cases reject — checked in PR-D by `internal/audit/schema_test.go`, which loads this file and the US3 fixture lines). PR-D copies the schema to `docs/schemas/audit-line-v1.schema.json` and `docs/features/audit-log.md` embeds this table.

## Common keys (every event)

| Key | Req | Source |
|---|---|---|
| `schema_version` | ✔ | const `1` |
| `ts` | ✔ | `time.Now().UTC()` formatted with the fixed layout `2006-01-02T15:04:05.000000000Z` (nine fractional digits, always `Z`; **not** `RFC3339Nano`, which trims trailing zeros) |
| `event` | ✔ | `authz` \| `tool_call` \| `auth_event` |
| `request_id` | ✔ | `audit.Attempt.RequestID` (activity id, `mcp.go:837` on `/mcp`; transport id on REST direct dispatch, `mcp_routing.go:412-414`); for `auth_event` the request id shown on the refusal page |
| `origin` | ✔ | `transport.GetConnectionSource(ctx)`: `tcp`→`local`, `tray`→`socket`, `stdio`→`local`; `remote` reserved |
| `source` | ✔ | mount point: `/mcp*`→`mcp`, `/api/v1/*`→`api`, proxy-originated→`internal`. **Never** `reqcontext.GetRequestSource` (rewritten from `X-MCPProxy-Client`, `concurrency_shed.go:12-22`) |
| `caller` | ✔ | `auth.AuthContextFromContext(ctx)` + `CredentialKind` + `Anonymous` bit; see kinds below |
| `client` | opt | `name`/`version` caller-asserted (MCP `clientInfo` / `X-MCPProxy-Client`), `ip` via `config.ForwardedHeaders(r, trusted_proxies)` |
| `transport_request_id` | opt | REST only |
| `session_id`, `work_session_id` | opt | activity resolver values (`activity_service.go:168`) |

`caller.kind` derivation (FR-013): `AuthContext.Type==admin && Anonymous` → `anonymous`; `Type==admin && source==tray` → `socket`; `Type==admin && source==stdio` → `stdio` (the native stdio transport installs `auth.AdminContext()` with no listener, `server.go:1039`; `stdioAuthContext` tags the new `transport.ConnectionSourceStdio` — without it `GetConnectionSource` defaults to TCP, `transport/context.go:25`, and the line would say `api_key`); `Type==admin` (API key) → `api_key`; `Type==agent` → `agent_token` (+ `user_id`, `user_email`, `role`, `provider` from the owner resolution when owned; `token_name`, `token_prefix`); `Type==admin_user` with `CredentialKind∈{cookie,bearer_jwt}` → `session_admin`; `Type==user` → `session_user` (auth_event only — no dispatch door admits it, FR-002/FR-003); proxy-originated → `internal`.

## `authz` — one per pre-dispatch decision

| Key | Req | Forbidden | Notes |
|---|---|---|---|
| `surface` | ✔ | | `call_tool_read|write|destructive` \| `direct` \| `code_execution` (nested child) \| `rest` |
| `server`, `tool`, `operation` | ✔ | | canonical pair; recorded even when the refusal was non-disclosing |
| `decision` | ✔ | | `allow` written after the last gate and before the upstream call; `deny` at the refusing gate |
| `reason` | ✔ | | `none` iff `allow`; else one pre-dispatch gate: `intent_invalid`, `intent_rejected`, `profile_scope`, `token_scope`, `token_permission`, `server_quarantined`, `tool_pending_approval`, `tool_changed_approval`, `tool_not_callable`, `other` (= `telemetry.BlockReason*` minus `output_sanitisation|output_schema`) |
| `disclosed` | ✔ when `deny` | | `false` for non-disclosing refusals (Spec 105 FR-010 shapes) |
| `args_sha256`, `args_bytes` | ✔ | | from the attempt record |
| `parent_id` | opt | | nested children only |
| `profile`, `caller.profile_pin` | opt | | |
| — | | `outcome`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags`, `caller.email_hash`, `caller.kind: session_user` | |

Gate → reason mapping (all in `handleCallToolVariant` unless noted; anchors shift after PR #1279): intent invalid `mcp.go:2211` → `intent_invalid`; intent rejected `:2227` → `intent_rejected`; profile `:2271` → `profile_scope`; `CanAccessServer` `:2286-2291` → `token_scope` (`disclosed:false`); variant/tier permission `:2305/:2320` → `token_permission`; quarantine `:2396` → `server_quarantined`; approval `:2408/:2417` → `tool_pending_approval|tool_changed_approval`; callability `:2425` → `tool_not_callable`; direct-name dispatch `mcp_routing.go:412-469` same map; nested `jsruntime.checkDispatchGates` (`runtime.go:384`) → `token_scope|token_permission` via the new observer.

## `tool_call` — one per `authz allow`, at completion

| Key | Req | Forbidden | Notes |
|---|---|---|---|
| `surface`, `server`, `tool`, `operation`, `args_sha256`, `args_bytes` | ✔ | | same values as the paired `authz` line |
| `outcome` | ✔ | | `success` \| `error` \| `blocked` (post-dispatch output sanitisation/schema) \| `rejected` (limiter shed, `*limiter.LimitError` returned to the completion path) |
| `reason` | ✔ iff `blocked`/`rejected` | forbidden on `success`/`error` | `output_sanitisation` \| `output_schema` \| `limiter_queue_full` \| `limiter_queue_timeout` |
| `error_class` | ✔ iff `error` | forbidden on every other outcome (`success`, `blocked`, `rejected`) | `upstream_error` \| `upstream_timeout` \| `upstream_unavailable` \| `validation` \| `sanitisation` \| `internal` \| `cancelled` — a bounded class, never message text |
| `duration_ms` | ✔ | | |
| `request_bytes`, `response_bytes` | opt | | as measured by the completion path (`RequestBytes` pre-truncation) |
| `parent_id` | opt | | nested children |
| — | | `decision`, `disclosed`, `flags` | |

## `auth_event` — one per terminal login attempt observed by the proxy, one per logout

| Key | Req | Forbidden | Notes |
|---|---|---|---|
| `surface` | ✔ | | `login` \| `logout` |
| `reason` | ✔ | | `ok`, `logout`, `authorization_denied` (IdP `error=` response on the callback with a valid state — `access_denied` or any RFC 6749 §4.1.2.1 code; 403 page), `id_token_invalid`, `nonce_mismatch`, `audience_mismatch`, `issuer_mismatch`, `token_expired`, `email_missing`, `email_unverified`, `domain_not_allowed`, `subject_mismatch`, `userinfo_subject_mismatch`, `user_disabled`, `state_invalid`, `provider_error`, `discovery_failed`, `internal_error` |
| `flags` | opt | | `provider_rebound`, `redirect_rejected`, `groups_claim_missing`; omitted when empty |
| `caller.user_id` | when the attempt reached the user store and a record exists (`ok`, `logout`, `subject_mismatch`, `user_disabled`, `internal_error`) | forbidden on `provider_error` (the store is never consulted on that path) | |
| `caller.email_hash` | only when the email is verified (verified ID token for `oidc`; back-channel userinfo for legacy providers) **and** the store was not yet consulted: `domain_not_allowed`, `userinfo_subject_mismatch`, `provider_error` raised by the userinfo fetch after a verified ID token (US2.3) | forbidden beside `user_id`/`user_email` | SHA-256 of the normalised email |
| `caller.kind` | ✔ | | `session_user` \| `session_admin` (after role derivation) \| `anonymous` (refused before identity) |
| `client.ip` | opt | | via `trusted_proxies` |
| — | | `server`, `tool`, `operation`, `decision`, `disclosed`, `outcome`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `args_sha256`, `args_bytes`, `parent_id`, `profile` | |

Identity is **stage-dependent, not reason-dependent** (FR-013). Pre-identity refusals (`state_invalid`, `authorization_denied`, `discovery_failed`, `provider_error` from discovery, JWKS or the token exchange, `id_token_invalid`, `nonce_mismatch`, `audience_mismatch`, `issuer_mismatch`, `token_expired`, `email_missing`, `email_unverified`) carry neither `user_id` nor `email_hash` — an unverified claim is never hashed. `provider_error` is the one reason that occurs at two stages: raised by the userinfo fetch **after** a verified ID token it carries `email_hash` of the verified email and never `user_id` (the schema forbids `user_id` on every `provider_error` line and admits `email_hash` only on `auth_event` lines whose reason is `domain_not_allowed`, `userinfo_subject_mismatch` or `provider_error`; T106 covers both stages). An abandoned redirect (pending state never returns) writes no line.

Callback branch → reason (FR-013/FR-024): state lookup fails `oauth_handler.go:152ff` → `state_invalid`; `?error=…` with a valid state (state consumed first; today the missing `code` returns 400 before the state is read, `:163-167`) → `authorization_denied`; discovery/JWKS/token/userinfo transport, 3xx, non-200 or non-JSON → `provider_error` (503 page) / bad discovery document → `discovery_failed` (503 page); verification → `id_token_invalid|nonce_mismatch|audience_mismatch|issuer_mismatch|token_expired`; `:226` no email → `email_missing`; policy → `email_unverified`; `isDomainAllowed` `:362-379` → `domain_not_allowed`; subject check → `subject_mismatch`; userinfo `sub` ≠ token `sub` → `userinfo_subject_mismatch`; `user.Disabled` → `user_disabled`; upsert `:242-247`, JWT `:260-270`, session `:276-289` failures → `internal_error` (503 page); success → `ok`.

## Caller identity rules (encoded in the schema, negative fixtures in T097)

The per-*reason* rules of the `auth_event` table above are encoded too (schema `auth_event` branch `allOf`): `ok|logout|subject_mismatch|user_disabled` ⇒ `caller.kind ∈ {session_user, session_admin}` + `user_id`; `domain_not_allowed|userinfo_subject_mismatch` ⇒ `caller.kind: anonymous` + `email_hash`; the pre-identity reasons ⇒ neither `user_id` nor `email_hash`; `provider_error` ⇒ never `user_id`; `internal_error` is stage-dependent and constrained only by the per-kind rules below.

| `caller.kind` | Required | Forbidden |
|---|---|---|
| `api_key`, `socket`, `stdio`, `anonymous`, `internal` | — | `user_id`, `user_email`, `email_hash`, `role`, `provider`, `token_name`, `token_prefix` — except `anonymous` on an `auth_event` refused **after** a verified email is known and before the store was consulted (`domain_not_allowed`, `userinfo_subject_mismatch`, `provider_error` from the userinfo fetch; `email_unverified` has no verified email by definition, and `subject_mismatch`/`user_disabled` have a record → `user_id`), which may carry `email_hash` only |
| `agent_token` | `token_name`, `token_prefix`; when owned (`user_id` present) **all** of `user_id`, `user_email`, `role`, `provider` (all-or-none, encoded as `user_id` ⇒ the other three) | `email_hash`; ownerless (no `user_id`) also forbids `user_email`, `role`, `provider` |
| `session_user` | `user_id`, `role: user` | `email_hash`, `token_name`, `token_prefix`; `user_email` on `auth_event` |
| `session_admin` | `user_id`, `role: admin` | `email_hash`, `token_name`, `token_prefix`; `user_email` on `auth_event` |

## Count invariants (SC-003, tested under bus saturation)

`#authz == #pre-dispatch decisions`; `#tool_call == #authz(decision=allow)`; no line for a built-in invocation as such; no `authz`/`tool_call` line with `caller.kind: session_user`; no `authz` line with `outcome` or an `output_*` reason; `#auth_event(surface=login) == #terminal login attempts`.

## Redaction (FR-015)

Structural: the line builder's inputs are the `audit.Attempt`, the `AuthContext`, the decision/reason/outcome enums, the typed error class and integers — there is no parameter through which an argument value, response fragment, error message, cookie, token or `_auth_*` member can arrive. The line does carry caller/operator-controlled strings — `client.name`/`client.version` (MCP `clientInfo` / `X-MCPProxy-Client`), `caller.token_name`, `profile`, and on a **refused** dispatch `server`/`tool` (the caller may name any `server:tool` pair; on an allowed dispatch they are configured names) — and each is sanitised **per field** at build time (the fixed-prefix secret patterns masked, length-capped; never the high-entropy rule) with the schema validated *after* sanitisation. Defence in depth: every serialised line additionally passes through the **fixed-prefix** credential patterns via `logs.NewStringSanitizer(logs.WithoutHighEntropy())` (the zap-core `NewSecretSanitizer` is not an `io.Writer`) before the write — the generic `high_entropy` rule (`sanitizer.go:104`, any quoted 32+ char `[A-Za-z0-9+/]` run) is excluded because it would mask every `args_sha256` and `email_hash` and break the schema after validation; a fixture proves that `args_sha256`, `email_hash` and a 40-char alphanumeric `server` name survive the pass byte-identical while a planted `AKIA…` in `client.name` and a caller-supplied `AKIA…:ghp_…` target on a refused dispatch are masked **by the per-field pass at build time**, so the writer pass is the **identity on every constructor output** (asserted byte-for-byte on every fixture line). Because every caller- or operator-controlled string is sanitised per field, the writer pass can fire only on a builder bug; a hit is counted in `Sink.SanitizerHits()` (always-on, mirrored to `mcpproxy_audit_sanitizer_hits_total`, surfaced by `doctor`) and logged once per minute, and the masked line is still written. Both passes together are the one documented exception to FR-016's real-name rule (spec FR-016; `docs/features/audit-log.md`): a credential-shaped `server`/`tool`/`token_name` is recorded masked, a configured name that is not credential-shaped is always recorded verbatim. Test: nine distinct high-entropy sentinels — arguments, response, error text, a caller-supplied `_auth_user_email`, `client.name`, `token_name`, `profile`, and a caller-supplied `server` and `tool` on a refused dispatch — are byte-absent from every line.

## Versioning

`schema_version` is bumped only for a removed/renamed key or a narrowed vocabulary (FR-013); **a bump advances three things together** — the `schema_version` const, the `$id` (`…/audit-line-v<N>.schema.json`) and the published filename `docs/schemas/audit-line-v<N>.schema.json` (the v<N-1> file stays published for old consumers) — pinned by the schema-identity sync test of T097. Adding a key or an enum value is a minor change: it ships as an updated copy of the **same** schema file (`$id` unchanged, `docs/schemas/audit-line-v1.schema.json`) with a change-log entry in `docs/features/audit-log.md`. The **published** schema (`docs/schemas/audit-line-v1.schema.json`, identical to the checked-in contract) is **consumer-tolerant**: `additionalProperties: true` at the root and inside `caller`/`client`, so a strict consumer holding the v1 document keeps validating after a minor additive change under the same `$id`. The **exact key set is a producer property proven by test**, not by the published file: `internal/audit/schema_test.go` loads the document, flips every `additionalProperties` to `false` in memory, and validates every constructor's output against that strict variant. Identity rules (which `caller` keys each `kind` requires or forbids) are encoded in the schema itself (see the `allOf` blocks) and covered by negative fixtures.
