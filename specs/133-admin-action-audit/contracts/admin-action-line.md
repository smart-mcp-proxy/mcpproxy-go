# `admin_action` line — contract (schema_version 1, additive)

Binding for PR-1..PR-5 (plan.md). The machine-readable form lands in PR-1 in `docs/schemas/audit-line-v1.schema.json` and its identical contract copy `specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json` (both loaded by `internal/audit/schema_test.go`), same `$id`. The schema change is more than one `allOf` block:

- the **root** enums widen: `event` gains `admin_action`; `caller.kind` gains `none` and `cli_offline`;
- every new key (`action`, `target`, `outcome` values, `changes`, `changes_truncated`, `changes_count`, `trigger`, `policy`, `client.surface`) is declared at the root `properties` with its type, and an `if event == admin_action` block adds the required set, the `iff` rules and the forbidden keys; the other three event blocks forbid the new keys;
- `schema_test.go`'s producer-strict helper becomes event-aware (it closes `properties` per event, so new keys are legal only on `admin_action`), and a test pins that every pre-133 example line still validates unchanged;
- the schema's own `description` drops the claim that a strict consumer holding v1 keeps validating after any additive change (true for keys, false for enum values).

## Unit of record

A line records one **committed effect**: one durable transition of one target (a token row written, a server entry committed, an approval record saved, a keyring entry stored), observed at the place it persisted. It is **not** tied to the HTTP or MCP response:

- a request that commits two effects and then fails writes the line(s) for what persisted (config-to-secret that stores the keyring entry and then fails the config patch: one `secret.set`, no `server.update`);
- a request that reports success but commits nothing writes nothing (a no-op toggle, `EnableServerCtx` on an already-enabled server — the existing activity row for that case invents `previous = !enabled`, `lifecycle.go:2437`; the line must not copy that);
- tool, prompt and skill decisions are grouped: one line per **batch key** = (request or automatic pass) × server × `target.kind` × `action` × `policy`. A security review that approves 10 tools and blocks 2 on one server writes two lines (`tool.approve`, `tool.block`).

## Keys

| Key | Req | Notes |
|---|---|---|
| `schema_version` | ✔ | `1` |
| `ts` | ✔ | Commit time of the effect, same fixed nine-digit UTC layout as every line |
| `event` | ✔ | `admin_action` |
| `origin` | ✔ | As today (`local` \| `socket`); a line with no request uses `local` |
| `source` | ✔ | `api` (REST, so CLI/Web UI/tray), `mcp` (built-in admin tools), `internal` (no request). From the mount tag `TagRequestMeta` stores, never a header. A server-edition route mounted outside the core `/api/v1` group must be tagged too (PR-4); an untagged context is a bug the door table test catches, not a default |
| `caller` | ✔ | See [Caller](#caller) |
| `action` | ✔ | One value of the [action list](#actions) |
| `target` | ✔ | See [Target](#target) |
| `outcome` | ✔ | `success` — the effect committed. There is no other value in this feature |
| `changes` | opt | Array of [change entries](#changes); omitted when the action is the whole change (`token.revoke`) |
| `changes_truncated`, `changes_count` | opt | Present together when `changes` was capped (see [Size](#size)); `changes_count` is the full count |
| `request_id` | ✔ iff the effect has a request behind it | The **proxy-side** id of the request: the activity id the funnel stamps on the matching activity row, minted once at each admin ingress and carried in the mutation context (PR-1 makes the MCP admin tools and Connect do this; today they mint a local id that never reaches the mutation, `mcp_credentials_tool.go:174-180`, `clients_service_connect.go:333`). Omitted — never `""` — when there is no request. Passes through `maskCredential` like every caller-influenced string |
| `transport_request_id` | opt | REST only, as today (the `X-Request-Id` value, caller-supplied, validated `^[a-zA-Z0-9_-]{1,256}$`, `reqcontext/requestid.go`), masked |
| `trigger` | ✔ iff `caller.kind` ∈ {`none`, `internal`} | `config_file` (`none` only) · `admission` · `policy` · `startup` · `schedule` |
| `policy` | ✔ iff `trigger: policy` | `trusted_baseline` · `scan_clean` · `auto_approve_tool_changes` · `hash_restored` · `skills_auto` · `login_replaced` |
| `client` | opt | `{name, version, ip}` as today, plus `surface` (`web` \| `macos` \| `cli` \| `mcp` \| `api`) — the #1842 `surface`, caller-asserted (`X-MCPProxy-Surface`), untrusted |
| `profile` | opt | The caller's active profile, as today |
| Forbidden | — | `surface`, `operation`, `decision`, `disclosed`, `reason`, `server`, `tool`, `args_sha256`, `args_bytes`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags`, `parent_id` |

## Caller

The trusted request context is normative: `caller` is built by the same derivation as `authz` lines (`auditCallerFromContext`, `internal/server/audit_funnel.go:231-287`: AuthContext type first, then the anonymous bit, then the connection source). The activity `actor_kind` string is **not** an input; the table shows how the two vocabularies line up so one query can join them.

| Request context | `caller.kind` | Identity keys (omitted when empty) |
|---|---|---|
| API key over TCP | `api_key` | — |
| API key over the tray socket / named pipe | `socket` | — |
| Native stdio transport (administrator, no listener) | `stdio` | — |
| `mcp_agt_` / `mcp_cli_` token, or a Spec 132 `mcp_oat_` browser login (both `AuthTypeAgent`) | `agent_token` | `token_name`, `token_prefix`, `profile_pin`; owner `user_id`/`user_email`/`role`/`provider` all-or-none |
| Administrator session (cookie or bearer JWT, server edition) | `session_admin` | `user_id`, `user_email`, `role: admin`, `provider` |
| Tenant session (server edition, acting on their own servers/tokens/credentials) | `session_user` | `user_id`, `user_email`, `role: user`, `provider` |
| `require_mcp_auth: false`, no credential | `anonymous` | — |
| Offline CLI, core stopped | `cli_offline` (new) | — |
| No request; the proxy decided (policy, admission, startup reconcile, schedule) | `internal` | — |
| No request; the proxy observed a change it did not make (config file edit) | `none` (new) | — |

Activity `actor_kind` alignment: `api_key`→`api_key`; `socket`→`socket` (the activity layer also says `socket` for an agent token on the tray socket, `activity_actor.go:42-44`; the audit derivation keeps `agent_token`, which is more specific); `agent_token`/`oauth`→`agent_token`; `cookie`/`bearer_jwt`→`session_admin` or `session_user` by role; `anonymous`→`anonymous`; `cli_offline`→`cli_offline`; `system`→`internal`; no actor→`none` or `internal`. Native stdio has no activity spelling of its own (activity falls back to `api_key`); the audit line says `stdio`.

`session_user` is already legal on `auth_event`; this feature makes it legal on `admin_action` too (it stays forbidden on `authz`/`tool_call`). `none` and `cli_offline` are legal only on `admin_action`. A background line (`internal`, `none`, `cli_offline`) carries no identity key.

## Target

| `target.kind` | Keys |
|---|---|
| `agent_token` | `name` (token name); `owner_user_id` when the token is tenant-owned (server edition: names are unique per owner only, `storage/agent_tokens.go:318-335`) |
| `client` | `name` (client id) |
| `login` | `name` (`<client-id>/<grant-id>`; a pending authorization uses its authorization id) |
| `user_jwt` | `owner_user_id` (server edition `POST /auth/token`) |
| `secret` | `name` (the keyring reference name, never the value) |
| `server` | `name` |
| `tool`, `prompt`, `skill` | `server`, `count` (full count in the batch), `names` (≤64, sorted), `truncated` |
| `profile` | `name` |
| `config` | `name: global` |
| `registry`, `scanner` | `name` (id) |
| `tpa_bundle` | `name` (`publisher` \| `operator` authority), `digest_prefix` (first 12 hex of the bundle digest — a public artifact, not a secret) |
| `user` | `owner_user_id` (server edition) |
| `user_credential` | `owner_user_id`, `server` (server edition) |

Every string goes through `maskCredential` and its length cap. PR-1 extends the fixed credential patterns (`internal/audit/redact.go:38`) with the proxy's own token shapes — `mcp_(agt|cli|oat|ort)_[A-Za-z0-9_-]{16,}`, long enough that a display `token_prefix` (8–16 characters in all) never matches — which today are not on the list; that change also protects `authz`/`tool_call` lines and the whole-line sanitizer pass, so a token value pasted into a name or an `X-Request-Id` is masked and counted. Masking is a pattern match: a free-text label that is a secret with no recognisable shape is not detectable, which is why labels never come from values (only from names an operator chose for a server, token, profile, registry or keyring entry).

## Changes

`{field, before?, after?, withheld?}` — either a literal (`before`/`after`, a side that does not exist is omitted) or `withheld: "secret" | "value"` with no value and **no hash**.

- `field` is a dotted path from a fixed schema (`enabled`, `env.GITHUB_TOKEN`, `profiles.dev.tier`), never built from a value; map keys under `env`/`headers` are operator-chosen names and are masked.
- **Literal** (value verbatim) — only fields whose Go type is one of: `bool`; a closed enum type (`protocol`, `trust_mode`, profile `mode`, tier caps, permission tiers); a bounded integer (rate limits, `max_*`, `*_days`, ports); a timestamp (`expires_at`); a display prefix (`token_prefix`, already shown in every token list); a public content-hash prefix (first 12 hex of a tool or skill approval hash — computed over the definition the upstream publishes to every connected agent); a list of server or profile **names** (`allowed_servers`, profile server lists), capped like `target.names`; `listen`.
- **Withheld `secret`** — `env.*`, `headers.*`, `api_key`, `oauth.*` secrets, `oauth_token_encryption_key`, keyring values, and any value that is or resolves from `${env:}`, `${file:}`, `${keyring:}`.
- **Withheld `value`** — everything else: `url`, `command`, `args`, `working_dir`, descriptions, schemas, rule patterns, registry URLs. They can carry a credential with no recognisable shape (a positional password in `args`), so they get neither the value nor a fingerprint; the line says the field changed, and the activity row (admin-only, redacted best-effort) keeps the detail.
- The classifier is one table keyed by field path; an unknown path is `withheld: "value"`, a path under a secret-class prefix is `withheld: "secret"`.

## Size

A serialized line is at most **16 KiB**. Deterministic truncation, in order: `target.names` and literal name lists to 64 entries each; `changes` to 128 entries sorted by `field`; then drop `changes` entries from the end until the line fits. `changes_count` / `target.count` always report the full set.

## Actions

Priority order of the issue; "Today" is the activity record before this feature (inventory.md). Every operation branch of a door that commits a distinct effect is listed; the registry (FR-022) keys on (door, branch).

### P1 — tokens and credentials (PR-1)

| `action` | Doors | Today |
|---|---|---|
| `token.create` | `POST /tokens`; MCP `credentials create_token`; CLI `token create` | `profile_change issue` (sync, actor) |
| `token.rotate` | `POST /tokens/{name}/regenerate`; CLI `token regenerate` (MCP `credentials` has no rotate operation) | `profile_change rotate` (#1842) |
| `token.revoke` | `DELETE /tokens/{name}`; MCP `credentials revoke` with `token`; CLI `token revoke` | `profile_change revoke` |
| `token.delete` | `DELETE /tokens/{name}/permanent`; CLI `token delete` | **none** |
| `client.create` | `POST /clients`; MCP `credentials create_client`; Connect mint (`POST /connect/{client}`, `POST /clients/upgrade-admin-key-holders`) | `profile_change issue/assign` |
| `client.bind` | Connect's binding written in the same commit as the mint; Connect undo restoring a binding (`POST /connect/{client}/undo`) | `profile_change assign/restore` |
| `client.key_revoke` | `DELETE /clients/{client}/api-key`; MCP `credentials revoke` with `client` and no `grant` (revokes the key; each login it cascades to gets its own `login.revoke`, same `request_id`) | `profile_change revoke` |
| `client.rotate_stage` | `POST /clients/{client}/rotate` and the Connect rotation path (`client_bindings.go:307-344`, `clients_service_connect.go:217-233`) | **none** (a new secret exists from this moment) |
| `client.rotate_finalize`, `client.rotate_rollback` | `POST /clients/{client}/rotate/finalize`; rollback; startup reconcile (`internal`, `trigger: startup`); reconcile-on-read from `GET /clients…` / `GET /connect…` (`internal`, `trigger: policy`, see Exempt) | `profile_change rotate` |
| `client.forget` | `DELETE /clients/{client}`; `DELETE /connect/{client}` with a credential; Connect undo that revokes a minted credential | `profile_change forget` |
| `login.approve`, `login.deny` | `POST /oauth/authorizations/{id}/approve|deny` | `profile_change approve/deny` |
| `login.revoke` | `DELETE /clients/{client}/logins/{grant_id}`; MCP `credentials revoke` with `client` + `grant`; CLI `client revoke-login`; cascade of `client.key_revoke`; replacement login at token exchange (`internal`, `trigger: policy`, `policy: login_replaced`) | `profile_change revoke` |
| `secret.set`, `secret.delete` | `POST /secrets`, `DELETE /secrets/{name}`, `POST /servers/{id}/config-to-secret` (`secret.set`, then `server.update` only if the patch commits); CLI `secrets set|del` (offline: `cli_offline`); CLI `upstream add` keyring flags (written before the backend split, `upstream_cmd.go:1579-1601`) | **none** |
| `config.update` (`field: api_key`, withheld) | CLI `status --reset-key` (`status_cmd.go:150-155,560-572`) | **none** |

### P2 — approvals (PR-2)

| `action` | Doors | Today |
|---|---|---|
| `server.quarantine`, `server.unquarantine` | `POST /servers/{id}/quarantine|unquarantine`; MCP `quarantine_security quarantine_server` and its legacy alias `quarantine` (`mcp.go:5001-5004`) | `quarantine_change` (bus) |
| `server.review_approve` | `POST /servers/{id}/security/approve`; CLI `security approve`, `review approve` — the tools it approves or blocks get their own `tool.*` lines, same `request_id` | `quarantine_change` + `tool_quarantine_change` (no actor on tool rows) |
| `server.review_reject` | `POST /servers/{id}/security/reject` — one line when the reject is accepted; its cleanup failures (`scanner/service.go:2641-2661`) do not change that | **none** |
| `tool.approve`, `tool.block` | `POST /servers/{id}/tools/approve|block` (incl. the reviewed path `tool_approval_review.go:76-187`); MCP `approve_tool`, `approve_all_tools`, `block_tool`, `block_all_tools`; CLI `upstream approve`, `tools approve` | `tool_quarantine_change` (bus, **no actor**) |
| `tool.approve` (automatic) | baseline, scan-clean, `auto_approve_tool_changes`, hash restored (`tool_quarantine.go:1342-1377,1516-1545` save without an activity event today) — `internal`, `trigger: policy` | `tool_quarantine_change tool_auto_approved` / none |
| `tool.enable`, `tool.disable` | `POST /servers/{id}/tools/{tool}/enabled`, `enable_all|disable_all`; MCP `enable_tool|disable_tool` | `tool_quarantine_change` (no actor) |
| `prompt.approve` | MCP `approve_prompt`, `approve_all_prompts` | **none** |
| `skill.approve`, `skill.block`, `skill.unblock` | `POST /servers/{id}/skills/approve|block|unblock`; CLI `skills approve|block|unblock`; status transitions persisted by `POST /servers/{id}/skills/refresh` or the scheduled refresh (`internal`, `policy: skills_auto`; the refresh request itself is not a decision) | `skill_quarantine_change` (bus) |
| `bundle.rollback`, `bundle.unpin`, `bundle.reload` | `POST /security/bundle/rollback|unpin|reload`; CLI `security bundle …` — PR-2 passes the request context into these calls (today they get none, `security_bundle.go:142-146,184,207`) | **none** |
| `bundle.activate` | the refresh cycle that activates a new bundle, written once by that cycle with its initiator (`POST /security/bundle/refresh` caller, or `internal`/`schedule`); a manual refresh that joins a running cycle adds no second line; the activation observers (detached goroutines, also fired on rollback) are not the hook | **none** |
| `scanner.enable`, `scanner.disable`, `scanner.configure` | `POST /security/scanners/{id}/enable` and its alias `/install`; `…/disable` and its alias `DELETE /security/scanners/{id}`; `PUT …/config` | **none** |

### P3 — config (PR-3)

| `action` | Doors | Today |
|---|---|---|
| `server.add`, `server.remove` | `POST /servers`, imports (one per server), registry add, `DELETE /servers/{id}`; MCP `upstream_servers add|remove|add_from_registry`; CLI daemon mode; offline `upstream add|add-json|remove|import` (`cli_offline`); `POST /config/apply` and file reload adding/removing servers | `config_change` (bus); offline, apply and reload **none** |
| `server.update` | `PATCH /servers/{id}` (`PatchServer`); MCP `upstream_servers update|patch`; apply; file reload | `config_change server_updated`; MCP update/patch **none** |
| `server.enable`, `server.disable` | `POST /servers/{id}/enable|disable`, `enable_all|disable_all` (one line per server whose state changed); MCP | `config_change` for single; bulk **none** |
| `server.quarantine` (admission) | the admission gate **outside** a caller's commit (file reload, startup) — `internal`, `trigger: admission`; inside a caller's commit it is a `quarantined` change on that commit's `server.add` | `quarantine_change` from a detached goroutine before persistence (`config_load_admission_gate.go:253-270`) |
| `config.update` | `POST /config/apply`, `PATCH /config`, `PATCH /config/docker-isolation`, CLI `telemetry enable|disable`, file reload — one line per commit over global keys (servers and profiles in the same commit get their own lines); includes `audit_log.*` (see spec FR-016a) | **none** (only `profile_change` for profile diffs) |
| `registry.add`, `registry.update`, `registry.remove` | `/registries` | **none** |
| `profile.create|update|delete|rename|classify|set_anonymous` | `/profiles…`; MCP `profiles`; CLI `profile …`; apply / reload | `profile_change` (sync) |
| `profile.activate` | `PUT /profiles/active` | **none** (in-memory) |
| `client.bind` | `PUT /clients/{client}/binding`, `POST /clients/bulk-assign` (one line per client); MCP `profiles assign` | `profile_change` |

### P3 — server edition (PR-4)

| `action` | Doors | Today |
|---|---|---|
| `token.create`, `token.revoke`, `token.rotate`, `token.delete` (`owner_user_id` set) | `POST /user/tokens`, `DELETE /user/tokens/{name}`, `POST /user/tokens/{name}/regenerate`, `DELETE /user/tokens/{name}/permanent`, `POST /admin/users/{id}/tokens/revoke`, `…/tokens/{name}/revoke` | **none** |
| `jwt.issue` | `POST /auth/token` (and its prefixed form): stateless — no commit to observe, so the line is written once the JWT is signed, before it is returned; `target: {kind: user_jwt, owner_user_id}`, `changes` = `expires_at` | **none** |
| `user.provision`, `user.role_change` | SSO callback creating a user record or changing its role/entitlement (`oauth_handler.go:461-474,511-525`); `caller.kind: session_user`/`session_admin` of the user signing in. Session creation itself stays on `auth_event` | **none** |
| `user.disable`, `user.enable` | `POST /admin/users/{id}/disable|enable` (tokens revoked as a side effect get their own `token.revoke` lines, same `request_id`) | **none** |
| `server.share` | `POST /admin/servers/{name}/shared` | **none** |
| `server.enable`, `server.disable` | `POST /admin/servers/{name}/enable|disable` (same funnel as PR-3) | `config_change` |
| `user_server.add`, `user_server.update`, `user_server.remove`, `user_server.enable` | `/user/servers…` (`session_user`) | **none** |
| `user_credential.connect` | the OAuth **callback** that persists the brokered credential, `GET /user/credentials/{server}/callback` (`credential_handlers.go:280-331`); the connect redirect only starts the flow | `credential_broker` (async, no actor) |
| `user_credential.delete` | `DELETE /user/credentials/{server}` | **none** |

### Exempt (no line; the registry records the reason)

| Door | Reason |
|---|---|
| `restart`, `restart_all`, `reconnect`, `refresh`, `discover-tools`, MCP `restart`/`refresh` | Runtime action; changes no rule |
| `POST /servers/{id}/login|logout`, CLI `auth login|logout` (upstream OAuth, daemon or standalone) | Upstream credential, not an access rule — open question Q3 in plan.md |
| `scan`, `scan-all`, cancel, MCP `scan_server` | Produces a report; the approval is the rule change |
| `POST /secrets/migrate`, CLI `secrets migrate` | Dry run only (`server.go:5113-5127`; the CLI stops at "not yet implemented", `secrets_cmd.go:390-407`) |
| `POST /onboarding/mark`, `/telemetry/update-failure`, `/feedback` | Not a rule |
| `POST /diagnostics/fix` | Each fixer calls a funnel above, which writes the line |
| `set_profile` (MCP; registered without an operation switch, `mcp.go:1714`) | Per-session selection inside the token's binding; not persisted |
| `POST /api/v1/auth/logout` (server edition) | Recorded as `auth_event surface: logout` |
| `db compact`, `auth tokens encrypt|decrypt`, `trust-cert` | Storage maintenance / OS trust store — open question Q1 |
| Read-only doors (`GET` without side effects, `/preview`, `/profiles/try`, `/config/validate`) | No change. A `GET` that persists something is never blanket-exempt: the registry lists each one (`GET /user/credentials/{server}/callback` → `user_credential.connect`; `GET /clients…` / `GET /connect…` reconcile-on-read → `client.rotate_finalize|rollback` with `internal`, `trigger: policy`) |
| Execution doors (`/tools/call`, `/code/exec`, `/preflight`, replay) | Covered by `authz`/`tool_call` |

## Count invariant

`#admin_action == #committed effects` over the action list, counted in batch-key units for `tool`/`prompt`/`skill` targets and one per signed JWT for `jwt.issue`. A line exists only if the enabled sink's write succeeded; a failed write is counted (`WriteFailures`) instead. Holds with the event bus saturated.

## Example

```json
{"schema_version":1,"ts":"2026-11-24T10:15:02.123456789Z","event":"admin_action","origin":"socket","source":"api","request_id":"01JD5Q…","caller":{"kind":"socket"},"client":{"name":"cli/0.77.0","surface":"cli"},"action":"token.rotate","target":{"kind":"agent_token","name":"ci-bot"},"outcome":"success","changes":[{"field":"token_prefix","before":"mcp_agt_a1b2","after":"mcp_agt_9f8e"}]}
{"schema_version":1,"ts":"2026-11-24T10:16:40.000000001Z","event":"admin_action","origin":"local","source":"internal","caller":{"kind":"none"},"trigger":"config_file","action":"server.update","target":{"kind":"server","name":"ast-grep"},"outcome":"success","changes":[{"field":"args","withheld":"value"},{"field":"enabled","before":true,"after":false},{"field":"env.GITHUB_TOKEN","withheld":"secret"}]}
```
