# `admin_action` line — contract (schema_version 1, additive)

Binding for PR-1..PR-5 (plan.md). The machine-readable form lands in PR-1 in `docs/schemas/audit-line-v1.schema.json` and its identical contract copy `specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json` (both loaded by `internal/audit/schema_test.go`), same `$id`. The schema change is more than one `allOf` block:

- the **root** enums widen: `event` gains `admin_action`; `caller.kind` gains `none` and `cli_offline`;
- every new key (`action`, `target`, `changes`, `changes_truncated`, `changes_count`, `trigger`, `policy`, `client.surface`) is declared at the root `properties` with its type; `outcome` stays the shared root property `tool_call` already uses, and the `admin_action` block restricts it to `success`; an `if event == admin_action` block adds the required set, the `iff` rules and the forbidden keys;
- the **published** schema stays consumer-tolerant: it does not forbid the new keys on the other three kinds (the old schema tolerates extension keys at the root and in `client`, `docs/schemas/audit-line-v1.schema.json:7,64-71`, and narrowing that would itself be a breaking change). Exclusivity is enforced only by `schema_test.go`'s producer-strict check, which becomes event-aware (it closes `properties` per event, so the proxy can never emit an `admin_action` key on another kind);
- compatibility, case by case: an old shipped line validates under the new schema unchanged (tested over every pre-133 example); a cached old published validator accepts new keys on old kinds but rejects `admin_action` lines (closed `event` and `caller.kind` enums); a producer-strict copy of the old schema rejects both — it was never meant for consumers;
- the schema's own `description` drops the claim that a strict consumer holding v1 keeps validating after any additive change.

## Unit of record

A line records one **committed effect**: one durable transition of one target (a token row written, a server entry committed, an approval record saved, a keyring entry stored), observed at the place it persisted. It is **not** tied to the HTTP or MCP response:

- a request that commits two effects and then fails writes the line(s) for what persisted (config-to-secret that stores the keyring entry and then fails the config patch: one `secret.set`, no `server.update`);
- a request that reports success but commits nothing writes nothing (a no-op toggle, `EnableServerCtx` on an already-enabled server — the existing activity row for that case invents `previous = !enabled`, `lifecycle.go:2437`; the line must not copy that);
- a few administrative decisions change in-memory state that governs access, not stored state; they are effects at their decision point: `login.approve` / `login.deny` (the pending authorization, `oauth_login_consent.go:843-937` — an approval later abandoned by the client keeps its line), `profile.activate` (the server-wide selector, `internal/httpapi/profiles.go:145-160`, only when it changed), and `jwt.issue` (stateless signing, server edition);
- tool, prompt and skill decisions are grouped: one line per **batch key** = (request or automatic pass) × server × `target.kind` × `action` × `policy`. A security review that approves 10 tools and blocks 2 on one server writes two lines (`tool.approve`, `tool.block`).

## Keys

| Key | Req | Notes |
|---|---|---|
| `schema_version` | ✔ | `1` |
| `ts` | ✔ | Commit time of the effect (for a batch, the time of its last save), same fixed nine-digit UTC layout as every line |
| `event` | ✔ | `admin_action` |
| `origin` | ✔ | As today (`local` \| `socket`); a line with no request uses `local` |
| `source` | ✔ | `api` (REST, so CLI/Web UI/tray), `mcp` (built-in admin tools, including over native stdio, which installs the stdio connection source but no HTTP mount tag, `internal/server/server.go:1510-1516,1564-1566` — PR-1 tags it), `internal` (no request). From the mount tag `TagRequestMeta` stores, never a header. A server-edition route mounted outside the core `/api/v1` group must be tagged too (PR-4); an untagged context is a bug the door table test catches, not a default |
| `caller` | ✔ | See [Caller](#caller) |
| `action` | ✔ | One value of the [action list](#actions) |
| `target` | ✔ | See [Target](#target) |
| `outcome` | ✔ | `success` — the effect committed. There is no other value in this feature |
| `changes` | opt | Array of [change entries](#changes); omitted when the action is the whole change (`token.revoke`) |
| `changes_truncated`, `changes_count` | opt | Present together when `changes` was capped (see [Size](#size)); `changes_count` is the full count |
| `request_id` | ✔ iff a request caused the effect | The id the matching activity row carries — a **join key, not a uniqueness guarantee**. On REST it is today's request id: the validated `X-Request-Id` when the caller sent one (`^[a-zA-Z0-9_-]{1,256}$`, `internal/httpapi/middleware.go:118-139`), else proxy-minted; a caller can reuse a value, exactly as for activity rows and `authz` lines. On MCP and Connect it is the id the door mints, which PR-1 installs in the mutation context (today it never reaches the mutation, `mcp_credentials_tool.go:174-180`, `clients_service_connect.go:333`). Present on an `internal` line when a request caused it (reconcile-on-read); never on `none` or `cli_offline`. Omitted — never `""` — when absent. Masked like every caller-influenced string |
| `trigger` | ✔ iff `caller.kind` ∈ {`none`, `internal`} | `config_file` (`none` only) · `admission` · `policy` · `startup` · `schedule` |
| `policy` | ✔ iff `trigger: policy` | `trusted_baseline` · `scan_clean` · `auto_approve_tool_changes` · `hash_restored` · `skills_auto` · `login_replaced` · `rotation_reconcile` |
| `client` | opt | `{name, version, ip}` as today, plus `surface` (`web` \| `macos` \| `cli` \| `mcp` \| `api`) — the #1842 `surface`, caller-asserted (`X-MCPProxy-Surface`), untrusted |
| `profile` | opt | The caller's active profile, as today |
| Forbidden | — | `surface`, `operation`, `decision`, `disclosed`, `reason`, `server`, `tool`, `args_sha256`, `args_bytes`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags`, `parent_id`, `transport_request_id` (caller-chosen text with no join value beyond `request_id`) |

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
| SSO callback provisioning a user (server edition; public route, no session yet, `setup.go:275-279`) | `session_user` / `session_admin` by the role derived at that callback | from the **verified** identity the callback just committed (`user_id` of the stored record), never from the empty AuthContext, which the shared helper would call `anonymous` |
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
| `tool`, `prompt`, `skill` | `server`, `count` (full count in the batch), `names` (≤64, sorted, each ≤64 characters), `truncated` |
| `profile` | `name` |
| `config` | `name: global` |
| `registry`, `scanner` | `name` (id) |
| `tpa_bundle` | `name` (`publisher` \| `operator` authority), `digest_prefix` (first 12 hex of the bundle file's digest — a signed artifact the operator or publisher distributes, verified against its `.sig`; it is not built from configuration) |
| `user` | `owner_user_id` (server edition) |
| `user_credential` | `owner_user_id`, `server` (server edition) |

Every string goes through `maskCredential` and its length cap. PR-1 extends the fixed credential patterns (`internal/audit/redact.go:38`) with the proxy's own token shapes — `mcp_(agt|cli|oat|ort)_[A-Za-z0-9_-]{16,}`, long enough that a display `token_prefix` (8–16 characters in all) never matches — which today are not on the list; that change also protects `authz`/`tool_call` lines and the whole-line sanitizer pass, so a token value pasted into a name or an `X-Request-Id` is masked and counted. Masking is a pattern match and catches only recognisable shapes: a generated 64-hex API key pasted into a name or an `X-Request-Id` is not masked (`internal/config/config.go:1987-1998`). The guarantee is therefore scoped: **values** never enter a line (they have no path in); **labels** — names an administrator chose for a server, token, profile, registry or keyring entry, and the request id an administrator sent — are recorded as chosen, pattern-masked, and an administrator who puts a secret into a label puts it into the audit file, as they already do into the activity log and `authz` lines.

## Changes

`{field, before?, after?, withheld?}` — either a literal (`before`/`after`, a side that does not exist is omitted) or `withheld: "secret" | "value"` with no value and **no hash**.

- `field` is a dotted path from a fixed schema (`enabled`, `env.GITHUB_TOKEN`, `profiles.dev.tier`), never built from a value; map keys under `env`/`headers` are operator-chosen names and are masked.
- **Literal** (value verbatim) — only fields whose Go type is one of: `bool`; a closed enum type (`protocol`, `trust_mode`, profile `mode`, tier caps, permission tiers); a bounded integer (rate limits, `max_*`, `*_days`, the **port** of `listen` — the host part is free text); a timestamp (`expires_at`); a display prefix (`token_prefix`, already shown in every token list); a count (`logins` removed by `client.forget`); a list of server or profile **names** (`allowed_servers`, profile server lists), capped like `target.names`.
- Tool and skill approval hashes are **not** literal: they cover upstream-controlled descriptions, schemas and defaults (`internal/runtime/tool_quarantine.go:47-66`) and skill files (`internal/skills/hash.go:11-66`), which nothing proves secret-free. A `tool.approve` line names the tools and says nothing about their hashes; the activity row keeps them.
- **Withheld `secret`** — `env.*`, `headers.*`, `api_key`, `oauth.*` secrets, `oauth_token_encryption_key`, keyring values, and any value that is or resolves from `${env:}`, `${file:}`, `${keyring:}`.
- **Withheld `value`** — everything else: `url`, `command`, `args`, `working_dir`, the host of `listen`, descriptions, schemas, rule patterns, registry URLs. They can carry a credential with no recognisable shape (a positional password in `args`), so they get neither the value nor a fingerprint; the line says the field changed, and the activity row (admin-only, redacted best-effort) keeps the detail.
- The classifier is one table keyed by field path; an unknown path is `withheld: "value"`, a path under a secret-class prefix is `withheld: "secret"`.

## Size

A serialized line is at most **16 KiB**, not counting the newline the sink appends. Every name, field path and label is first capped at 64 characters (the mask's own 256-rune cap is the ceiling for other strings). Then, in order, until the line fits: `target.names` and literal name lists to 64 entries each; `changes` to 128 entries sorted by `field`; drop `changes` entries from the end; drop `target.names` entirely. `truncated` / `changes_truncated` are set whenever anything was dropped; `target.count` / `changes_count` always report the full set. With every list dropped, a line is bounded by its fixed keys and capped labels, well under the budget. The test asserts the final serialized byte length.

## Actions

Priority order of the issue; "Today" is the activity record before this feature (inventory.md). Every operation branch of a door that commits a distinct effect is listed; the registry (FR-022) keys on (door, branch).

### P1 — tokens and credentials (PR-1)

| `action` | Doors | Today |
|---|---|---|
| `token.create` | `POST /tokens`; MCP `credentials create_token`; CLI `token create` | `profile_change issue` (sync, actor) |
| `token.rotate` | `POST /tokens/{name}/regenerate`; CLI `token regenerate` (MCP `credentials` has no rotate operation) | `profile_change rotate` (#1842) |
| `token.revoke` | `DELETE /tokens/{name}` for an agent token; MCP `credentials revoke` with `token`; CLI `token revoke`. `DELETE /tokens/client-<id>` addresses a client credential and takes the client branch below (`credentials_service.go:1045-1071`) | `profile_change revoke` |
| `token.delete` | `DELETE /tokens/{name}/permanent`; CLI `token delete` | **none** |
| `client.create` | `POST /clients`; MCP `credentials create_client`; Connect mint (`POST /connect/{client}`, `POST /clients/upgrade-admin-key-holders`); offline `connect` backend (`cli_offline`, `connect_credential.go:247-288`) | `profile_change issue/assign` |
| `client.bind` | Connect's binding change; Connect undo restoring a binding (`POST /connect/{client}/undo`); Connect's compensating restore after a failed finalize | `profile_change assign/restore` |
| `client.key_revoke` | `DELETE /clients/{client}/api-key`; MCP `credentials revoke` with `client` and no `grant`; legacy `DELETE /tokens/client-<id>`. One line only when a live key was actually revoked (an absent or already-revoked key writes none); each login the client-wide branch revokes gets its own `login.revoke`, same `request_id`. A login-only client writes only the `login.revoke` lines. The identities come from what the transaction changed, not from the request or a post-commit list | `profile_change revoke` |
| `client.rotate_stage` | `POST /clients/{client}/rotate` and the Connect rotation path (`client_bindings.go:307-344`, `clients_service_connect.go:217-233`) | **none** (a new secret exists from this moment) |
| `client.rotate_finalize`, `client.rotate_rollback` | `POST /clients/{client}/rotate/finalize`; rollback; startup reconcile (`internal`, `trigger: startup`); reconcile-on-read from `GET /clients…`, `GET /connect…` and MCP `profiles list_clients` (`internal`, `trigger: policy`, `policy: rotation_reconcile`, with the initiating `request_id`) | `profile_change rotate` |
| `client.forget` | `DELETE /clients/{client}`; `DELETE /connect/{client}` with a credential; Connect undo that revokes a minted credential; offline `disconnect` (`cli_offline`, `connect_credential.go:293-307`). One line: forget deletes the client's grants and OAuth binding (`storage/client_binding.go:275-352`); they are folded in as `changes` `{field: logins, before: n, after: 0}`, not separate `login.revoke` lines | `profile_change forget` |
| `login.approve`, `login.deny` | `POST /oauth/authorizations/{id}/approve|deny` | `profile_change approve/deny` |
| `login.revoke` | `DELETE /clients/{client}/logins/{grant_id}`; MCP `credentials revoke` with `client` + `grant`; CLI `client revoke-login`; cascade of `client.key_revoke`; replacement login at token exchange (`internal`, `trigger: policy`, `policy: login_replaced`) | `profile_change revoke` |
| `secret.set`, `secret.delete` | `POST /secrets`, `DELETE /secrets/{name}`, `POST /servers/{id}/config-to-secret` (`secret.set`, then `server.update` only if the patch commits); CLI `secrets set|del` (offline: `cli_offline`); CLI `upstream add` keyring flags (written before the backend split, `upstream_cmd.go:1579-1601`) | **none** |
| `config.update` (`field: api_key`, withheld) | CLI `status --reset-key` (`status_cmd.go:150-155,560-572`) — see [CLI ownership](#cli-ownership) | **none** |

Connect is a multi-step writer (`clients_service_connect.go:217-434`): `Issue` persists the fresh credential or stages a rotation, the config file is written, `Commit` records the mint or finalizes, and `Abort` rolls back or revokes the fresh credential. Each step that persists writes its own line at its own persistence point with the original `request_id` — `client.create` or `client.rotate_stage` at `Issue`, `client.bind` at the binding write, `client.rotate_finalize` at finalize, `client.rotate_rollback` or `client.forget` at `Abort`, `client.bind` for a compensating restore — so a config-write failure, a finalize failure and a failed compensation each leave exactly the lines of what persisted (tested).

### P2 — approvals (PR-2)

| `action` | Doors | Today |
|---|---|---|
| `server.quarantine`, `server.unquarantine` | `POST /servers/{id}/quarantine|unquarantine`; MCP `quarantine_security quarantine_server` and its legacy alias `quarantine` (`mcp.go:5001-5004`) | `quarantine_change` (bus) |
| `server.review_approve` | `POST /servers/{id}/security/approve`; CLI `security approve`, `review approve`. A review persists in steps (`tool_approval_review.go:300-395`): baseline and tool blocks are saved, then tools are promoted, then the server is unquarantined in a config commit. Each step writes at its own persistence point: `tool.block` / `tool.approve` lines when those records are saved, `server.review_approve` only when the unquarantine commit succeeds. A review that fails after saving blocks leaves the `tool.block` line and no `server.review_approve` (tested) | `quarantine_change` + `tool_quarantine_change` (no actor on tool rows) |
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
| `user.provision` | SSO callback creating a user record (`oauth_handler.go:461-474,511-525`), attributed as in [Caller](#caller). There is no `user.role_change`: the store persists groups, not a role, and the role is derived at each login from the live `admin_emails` (`users/models.go:14-36`, `oauth_handler.go:827-834`), so a role change is a `config.update` of `admin_emails`. Session creation stays on `auth_event` | **none** |
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
| `POST /servers/{id}/security/reject` | Discards review artifacts (scan reports, jobs, baseline, snapshot image); the server stays quarantined, no access rule changes, and the call succeeds even when cleanup fails (`scanner/service.go:2641-2661`) |
| `POST /secrets/migrate`, CLI `secrets migrate` | Dry run only (`server.go:5113-5127`; the CLI stops at "not yet implemented", `secrets_cmd.go:390-407`) |
| `POST /onboarding/mark`, `/telemetry/update-failure`, `/feedback` | Not a rule |
| `POST /diagnostics/fix` | Each fixer calls a funnel above, which writes the line |
| `set_profile` (MCP; registered without an operation switch, `mcp.go:1714`) | Per-session selection inside the token's binding; not persisted |
| `POST /api/v1/auth/logout` (server edition) | Recorded as `auth_event surface: logout` |
| `db compact`, `auth tokens encrypt|decrypt`, `trust-cert` | Storage maintenance / OS trust store — open question Q1 |
| Read-only doors (`GET` without side effects, `/preview`, `/profiles/try`, `/config/validate`) | No change. Every route, `GET` included, is classified by name in the registry; a `GET` that persists something is mapped (`GET /user/credentials/{server}/callback` → `user_credential.connect`; `GET /clients…` / `GET /connect…` reconcile-on-read → `client.rotate_finalize|rollback`) |
| Execution doors (`/tools/call`, `/code/exec`, `/preflight`, replay) | Covered by `authz`/`tool_call` |

## CLI ownership

Exactly one writer owns each effect, and a CLI never opens the daemon's rotating sink while the daemon runs (two processes rotating one file would corrupt it; the sink mutex is process-local, `internal/audit/sink.go:61-65`).

- **Through REST** (most commands): the daemon owns the line.
- **Offline commands that refuse while the core runs** (offline `connect`/`disconnect`, offline `upstream add|add-json|remove|import`): the CLI owns it, `cli_offline`, via its own file sink (spec FR-021).
- **Commands that edit the config file directly whatever the daemon's state** (`status --reset-key`, `telemetry enable|disable`; they have no stopped-core guard, `status_cmd.go:150-155,560-572`, `telemetry_cmd.go:238-283`) and keyring writes before the backend split (`upstream add` keyring flags): when the daemon is running (socket reachable), the CLI writes no line — the watcher's reload owns the config effect as `caller.kind: none`, `trigger: config_file`, which loses the caller (documented); keyring writes with a running daemon are routed through `POST /secrets` (PR-1). When the daemon is not running, the CLI owns the line as `cli_offline`.

## Count invariant

`#admin_action == #committed effects` over the action list, counted in batch-key units for `tool`/`prompt`/`skill` targets and one per signed JWT for `jwt.issue`. A line exists only if the enabled sink's write succeeded; a failed write is counted (`WriteFailures`) instead. Holds with the event bus saturated.

## Example

```json
{"schema_version":1,"ts":"2026-11-24T10:15:02.123456789Z","event":"admin_action","origin":"socket","source":"api","request_id":"01JD5Q…","caller":{"kind":"socket"},"client":{"name":"cli/0.77.0","surface":"cli"},"action":"token.rotate","target":{"kind":"agent_token","name":"ci-bot"},"outcome":"success","changes":[{"field":"token_prefix","before":"mcp_agt_a1b2","after":"mcp_agt_9f8e"}]}
{"schema_version":1,"ts":"2026-11-24T10:16:40.000000001Z","event":"admin_action","origin":"local","source":"internal","caller":{"kind":"none"},"trigger":"config_file","action":"server.update","target":{"kind":"server","name":"ast-grep"},"outcome":"success","changes":[{"field":"args","withheld":"value"},{"field":"enabled","before":true,"after":false},{"field":"env.GITHUB_TOKEN","withheld":"secret"}]}
```
