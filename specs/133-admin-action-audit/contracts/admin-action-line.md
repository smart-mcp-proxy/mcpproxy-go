# `admin_action` line — contract (schema_version 1, additive)

Binding for PR-1..PR-4. The machine-readable form is an `admin_action` `allOf` block added to `docs/schemas/audit-line-v1.schema.json` and its identical contract copy `specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json` (both are loaded by `internal/audit/schema_test.go`), same `$id`.

## Keys

| Key | Req | Notes |
|---|---|---|
| `schema_version` | ✔ | `1` |
| `ts` | ✔ | Same fixed nine-digit UTC layout as every line |
| `event` | ✔ | `admin_action` |
| `origin` | ✔ | As today (`local` \| `socket`). A line with no request (`caller.kind` `none`/`internal`/`cli_offline`) uses `local` |
| `source` | ✔ | `api` (REST, so CLI/Web UI/tray), `mcp` (built-in admin tools), `internal` (no request) — the mount point, never a header |
| `caller` | ✔ | Built by the same derivation as `authz` lines (`auditCallerFromContext`). Two new kinds: `none`, `cli_offline`. See [Caller](#caller) |
| `action` | ✔ | One value of the [action list](#actions) |
| `target` | ✔ | `{kind, name, server?, tool_count?, tools?, truncated?}` — see [Target](#target) |
| `outcome` | ✔ | `success` (the only value in this feature; a refused or failed change writes no line) |
| `changes` | opt | Array of [change entries](#changes); omitted when the action itself is the whole change (`token.revoke`) |
| `changes_truncated`, `changes_count` | opt | Present together when `changes` was capped at 128 entries (sorted by `field`) |
| `request_id` | ✔ iff `caller.kind` ∉ {`none`, `internal`, `cli_offline`} | Equal to the `request_id` on the change's activity row, when it has one |
| `transport_request_id` | opt | REST only, as today |
| `trigger` | ✔ iff `caller.kind` ∈ {`none`, `internal`} | `config_file` (`none` only) · `admission` · `policy` · `startup` · `schedule` |
| `policy` | ✔ iff `trigger: policy` | `trusted_baseline` · `scan_clean` · `auto_approve_tool_changes` · `hash_restored` · `skills_auto` |
| `client` | opt | `{name, version, ip}` as today, plus `surface` (`web` \| `macos` \| `cli` \| `mcp` \| `api`) — the #1842 `surface`, caller-asserted, untrusted |
| `profile` | opt | The caller's active profile, as today |
| Forbidden | — | `surface`, `operation`, `decision`, `disclosed`, `reason`, `server`, `tool`, `args_sha256`, `args_bytes`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags`, `parent_id` |

## Caller

`caller.kind` on an `admin_action` line, from the #1842 activity `actor_kind`:

| Activity `actor_kind` | `caller.kind` | Identity keys |
|---|---|---|
| `api_key` | `api_key` | — |
| `socket` | `socket` | — |
| `agent_token` | `agent_token` | `token_name`, `token_prefix`, `profile_pin`; owner fields all-or-none (server edition) |
| `oauth` (Spec 132 browser login, `AuthTypeAgent`) | `agent_token` | as above (`token_name` = the client credential name) — what `authz` lines already write |
| `cookie` / `bearer_jwt`, administrator | `session_admin` | `user_id`, `user_email`, `role: admin`, `provider` |
| `cookie` / `bearer_jwt`, tenant (server edition, own servers/tokens/credentials) | `session_user` | `user_id`, `user_email`, `role: user`, `provider` |
| `anonymous` (`require_mcp_auth: false`) | `anonymous` | — |
| `cli_offline` | `cli_offline` (new) | — |
| `system` (startup reconcile, skill transitions) | `internal` | — |
| no actor (file reload, admission gate, sweeps) | `none` (observed) or `internal` (decided) | — |

`session_user` becomes legal on `admin_action` (it stays forbidden on `authz`/`tool_call`). `none` and `cli_offline` are legal only on `admin_action`.

## Target

| `target.kind` | `name` | Extra |
|---|---|---|
| `agent_token` | token name | — |
| `client` | client id | — |
| `login` | `<client-id>/<grant-id>` (a browser-login grant or a pending authorization id) | — |
| `user_jwt` | user id (server edition `POST /auth/token`) | — |
| `secret` | secret reference name (never the value) | — |
| `server` | server name | — |
| `tool` | — | `server`, `tool_count`, `tools` (≤64 names), `truncated` |
| `prompt` | — | `server`, `tool_count`, `tools` (prompt names), `truncated` |
| `skill` | — | `server`, `tool_count`, `tools` (skill ids), `truncated` |
| `profile` | profile name | — |
| `config` | `global` | — |
| `registry` | registry id | — |
| `scanner` | scanner id | — |
| `tpa_bundle` | bundle authority (`publisher` \| `operator`) | — |
| `user` | user id (server edition) | — |
| `user_credential` | `<user-id>/<server>` (server edition) | — |

Every `name`, `server` and `tools[]` entry goes through the existing per-field `maskCredential` and length cap, as `server`/`tool` do on `authz` lines.

## Changes

`{field, before?, after?, before_sha256?, after_sha256?, redacted?}` — exactly one of: (`before`/`after` literal), (`before_sha256`/`after_sha256`), (`redacted: true`). A side that does not exist (an add, a remove) is omitted.

- `field` is a dotted path (`enabled`, `env.GITHUB_TOKEN`, `profiles.dev.tier`, `tools.search.hash`), masked per field.
- **Literal list** (value verbatim): booleans; enums (`protocol`, `trust_mode`, profile `mode`, tier caps, `permissions` items); bounded integers (rate limits, `max_*`, `*_days`); `expires_at`; `token_prefix`; content-hash prefixes (first 12 hex of a tool/skill hash, `sk1-` prefix form); `allowed_servers` and other lists of server or profile names; `listen`.
- **Fingerprint** (everything not literal and not secret): `url`, `command`, `args`, `working_dir`, descriptions, schemas, rule patterns, registry URLs. `*_sha256` = SHA-256 of the RFC 8785 serialisation of the value (`internal/audit/canonical.go`, the `args_sha256` code).
- **Redacted** (`redacted: true`, no value, no hash): `env.*`, `headers.*`, `api_key`, `oauth.client_secret`, `oauth_token_encryption_key`, any key under `secrets`, any value that was or resolves from `${env:}`, `${file:}`, `${keyring:}`, and any field the activity redactor (`redactActivityValue`) would mask. When in doubt the builder redacts: a field is literal or fingerprinted only if it is named on the two lists above.

## Actions

Priority order of the issue. "Doors" names the inventory rows (inventory.md §); "Today" is the activity record before this feature.

### P1 — tokens and credentials (PR-1)

| `action` | Doors | Today |
|---|---|---|
| `token.create` | `POST /tokens`; MCP `credentials` `create_token`; CLI `token create` | `profile_change issue` (sync, actor) |
| `token.rotate` | `POST /tokens/{name}/regenerate`; CLI `token regenerate` | `profile_change rotate` (#1842) |
| `token.revoke` | `DELETE /tokens/{name}`; MCP `credentials revoke`; CLI `token revoke`; `DELETE /clients/{client}/api-key` | `profile_change revoke` |
| `token.delete` | `DELETE /tokens/{name}/permanent`; CLI `token delete` | **none** |
| `client.create` | `POST /clients`; MCP `credentials create_client`; Connect mint (`POST /connect/{client}`, `POST /clients/upgrade-admin-key-holders`) | `profile_change issue/assign` |
| `client.rotate_stage` | `POST /clients/{client}/rotate` | **none** (a new secret exists from this moment) |
| `client.rotate_finalize`, `client.rotate_rollback` | `POST /clients/{client}/rotate/finalize`; rollback; startup reconcile (`internal`, `trigger: startup`) | `profile_change rotate` |
| `client.forget` | `DELETE /clients/{client}`; `DELETE /connect/{client}` with a credential | `profile_change forget` |
| `login.approve`, `login.deny` | `POST /oauth/authorizations/{id}/approve|deny` | `profile_change approve/deny` |
| `login.revoke` | `DELETE /clients/{client}/logins/{grant_id}`; CLI `client revoke-login` | `profile_change revoke` |
| `secret.set`, `secret.delete` | `POST /secrets`, `DELETE /secrets/{name}`, `POST /secrets/migrate` (one `secret.set` per secret), `POST /servers/{id}/config-to-secret`; CLI `secrets set|del|migrate` (offline: `cli_offline`) | **none** |

### P2 — approvals (PR-2)

| `action` | Doors | Today |
|---|---|---|
| `server.quarantine`, `server.unquarantine` | `POST /servers/{id}/quarantine|unquarantine`; MCP `quarantine_security quarantine_server`; admission gate (`internal`, `trigger: admission`) | `quarantine_change` (bus; admission rows without actor) |
| `server.review_approve` | `POST /servers/{id}/security/approve`; CLI `security approve`, `review approve` | `quarantine_change` + `tool_quarantine_change` (no actor on tool rows) — the tools it approves or blocks get their own `tool.*` lines with the same `request_id` |
| `server.review_reject` | `POST /servers/{id}/security/reject` | **none** |
| `tool.approve`, `tool.block` | `POST /servers/{id}/tools/approve|block` (one line per server per request, tools listed); MCP `approve_tool`, `approve_all_tools`, `block_tool`, `block_all_tools`; CLI `upstream approve`, `tools approve` | `tool_quarantine_change` (bus, **no actor**) |
| `tool.approve` (automatic) | baseline, scan-clean, `auto_approve_tool_changes`, hash restored (`internal`, `trigger: policy`, `policy`) | `tool_quarantine_change tool_auto_approved` |
| `tool.enable`, `tool.disable` | `POST /servers/{id}/tools/{tool}/enabled`, `enable_all|disable_all`; MCP `enable_tool|disable_tool` | `tool_quarantine_change` (no actor) |
| `prompt.approve` | MCP `approve_prompt`, `approve_all_prompts` | **none** |
| `skill.approve`, `skill.block`, `skill.unblock` | `POST /servers/{id}/skills/approve|block|unblock`; CLI `skills approve|block|unblock`; automatic transitions (`internal`, `policy: skills_auto`) | `skill_quarantine_change` (bus) |
| `bundle.rollback`, `bundle.unpin`, `bundle.reload` | `POST /security/bundle/rollback|unpin|reload`; CLI `security bundle …` | **none** |
| `bundle.activate` | a refresh that activates a new bundle: `POST /security/bundle/refresh` (caller) or the scheduled cycle (`internal`, `trigger: schedule`) | **none** |
| `scanner.install`, `scanner.remove`, `scanner.configure`, `scanner.enable`, `scanner.disable` | `/security/scanners/…` | **none** |

### P3 — config (PR-3; server-edition doors PR-4)

| `action` | Doors | Today |
|---|---|---|
| `server.add`, `server.remove` | `POST /servers`, imports (one per server), registry add, `DELETE /servers/{id}`; MCP `upstream_servers add|remove|add_from_registry`; CLI daemon mode; offline `upstream add|remove|import` (`cli_offline`); file reload (`none`, `trigger: config_file`) | `config_change` (bus); offline and reload **none** |
| `server.update` | `PATCH /servers/{id}`; MCP `upstream_servers update|patch`; file reload | `config_change server_updated`; MCP update/patch **none** (only `internal_tool_call.config_diff`) |
| `server.enable`, `server.disable` | `POST /servers/{id}/enable|disable`, `enable_all|disable_all` (one line per server changed); MCP; server-edition admin enable/disable | `config_change` for single; bulk **none** |
| `config.update` | `POST /config/apply`, `PATCH /config`, `PATCH /config/docker-isolation`, file reload; one line per apply, `changes` over global keys (servers and profiles inside the same apply get their own `server.*`/`profile.*` lines) | **none** (only `profile_change` for profile diffs) |
| `registry.add`, `registry.update`, `registry.remove` | `/registries` | **none** |
| `profile.create|update|delete|rename|classify|set_anonymous` | `/profiles…`; MCP `profiles`; CLI `profile …` | `profile_change` (sync) |
| `profile.activate` | `PUT /profiles/active` | **none** (in-memory) |
| `client.bind` | `PUT /clients/{client}/binding`, `POST /clients/bulk-assign` (one line per client); MCP `profiles assign` | `profile_change` |
| `token.create|revoke|rotate|delete` (server edition) | `POST /user/tokens`, `DELETE /user/tokens/{name}[/permanent]`, `POST /user/tokens/{name}/regenerate`, `POST /admin/users/{id}/tokens[/{name}]/revoke`; `POST /auth/token` (`target.kind: user_jwt`) | **none** |
| `user.disable`, `user.enable` | `POST /admin/users/{id}/disable|enable` (tokens revoked as a side effect get their own `token.revoke` lines, same `request_id`) | **none** |
| `server.share` | `POST /admin/servers/{name}/shared` | **none** |
| `user_server.add|update|remove|enable` | `/user/servers…` (tenant; `caller.kind: session_user`) | **none** |
| `user_credential.connect|delete` | `/user/credentials/{server}…` | `credential_broker` (async, no actor) |

### Exempt (no line; the registry test requires the reason)

| Door | Reason |
|---|---|
| `restart`, `restart_all`, `reconnect`, `refresh`, `discover-tools`, MCP `restart`/`refresh` | Runtime action; changes no rule |
| `POST /servers/{id}/login|logout` (upstream OAuth) | Upstream credential, not an access rule; candidate follow-up |
| `scan`, `scan-all`, cancel, MCP `scan_server` | Produces a report; approval is the rule change |
| `POST /onboarding/mark`, `/telemetry/update-failure`, `/feedback` | Not a rule |
| `POST /diagnostics/fix` | Each fixer calls a funnel above, which writes the line |
| `set_profile` (MCP) | Per-session selection inside the token's binding; not persisted |
| `db compact`, `auth tokens encrypt|decrypt`, `trust-cert` | Storage maintenance / OS trust store. `auth tokens decrypt` is the one borderline case: recorded as `config.update` with `field: oauth_token_storage` if review asks (Open question Q1 in plan.md) |
| Read-only doors (`GET`, `/preview`, `/profiles/try`, `/config/validate`) | No change |
| Execution doors (`/tools/call`, `/code/exec`, `/preflight`, replay) | Covered by `authz`/`tool_call` |

## Count invariant

`#admin_action == #committed changes` over the action list, where a "change" is one target per request (or per server per pass for `tool`/`prompt`/`skill` targets). Holds with the event bus saturated. No line for a refused, failed or no-op request.

## Example

```json
{"schema_version":1,"ts":"2026-11-24T10:15:02.123456789Z","event":"admin_action","origin":"socket","source":"api","request_id":"01JD…","caller":{"kind":"socket"},"client":{"name":"cli/0.77.0","surface":"cli"},"action":"token.rotate","target":{"kind":"agent_token","name":"ci-bot"},"outcome":"success","changes":[{"field":"token_prefix","before":"mcp_agt_a1b2","after":"mcp_agt_9f8e"}]}
{"schema_version":1,"ts":"2026-11-24T10:16:40.000000001Z","event":"admin_action","origin":"local","source":"internal","caller":{"kind":"none"},"trigger":"config_file","action":"server.update","target":{"kind":"server","name":"ast-grep"},"outcome":"success","changes":[{"field":"enabled","before":true,"after":false},{"field":"args","before_sha256":"3f…","after_sha256":"a9…"},{"field":"env.GITHUB_TOKEN","redacted":true}]}
```
