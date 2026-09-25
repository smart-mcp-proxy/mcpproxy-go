# Contract: MCP built-in tools (Profiles v3)

## `retrieve_tools` (changed output, no argument change)

When the effective profile is non-legacy (sets any of the six policy fields `max_tier`, `unannotated`, `tools`, `code_execution`, `management_tools`, `switchable_to` — data-model §1 `IsLegacy()`; `title`/`description` do not count), the result object gains:

```json
{ "tools": [ ... ], "hidden_by_profile": 3, "profile": "work-readonly" }
```

- `hidden_by_profile`: integer ≥ 0 (FR-011). Present iff the effective profile is non-legacy — including `0` when nothing matched was excluded (e.g. a `max_tier: destructive`-only profile); omitted for no profile and for legacy profiles, whatever their `title`. A dangling base (FR-020 deny-all for a pin, binding or `anonymous_profile` naming a missing profile) and the FR-008a binding guard always count as non-legacy: `hidden_by_profile: 0`. Never accompanied by names, descriptions or per-server breakdowns. Counted by the extended index API (`SearchToolsAdmitted`, data-model §2) over the full match set before the limit — `indexedToolVisible` on the already-cut result cannot do it (FR-011).
- `profile`: effective profile slug **only when the caller selected it** (`profile_source` `url` or `session`); omitted for no profile and for every base the caller did not choose (`pin`, `binding`, `anonymous`, incl. dangling bases and the guard), so discovery never confirms that a caller is pinned or to what (FR-011, research D27).
- Frozen goldens: legacy fixtures unchanged; one new golden `retrieve_tools_profile_v3.golden.json`.

## `describe_tool`

Excluded tools → the existing uniform not-found response (Spec 105 FR-010). No shape change.

## `call_tool_read` / `call_tool_write` / `call_tool_destructive`

Profile gate evaluated after the Spec 105 server-scope gate and before upstream dispatch. Refusal: `isError: true`, text per [refusals.md](refusals.md) (never names the profile). Activity: `status=blocked`, `block_reason`.

## `code_execution`

Absent from `tools/list` and refused at call (`tool not found` uniform shape) when `code_execution: false`. Nested `call_tool` refusals are returned to the script as the same error text and recorded as children (`parent_id`).

## `set_profile` (semantics change only)

Admission per FR-022; every refusal uses the Spec 105 `profileNotSelectable` uniform body. Response unchanged: `{active_profile, servers}` plus `profile_source` (new field, additive), which reports only the caller's own selection — `session` with the selected slug, or `none`/empty `active_profile` after `set_profile("")` even when the next request falls through to a pin, binding or anonymous base (FR-018).

## `profiles` (new, admin only — FR-017, research D12/D14)

Visibility: credential kind `api_key` or `socket`, effective profile none or `management_tools: true`, and neither `disable_management` nor `read_only_mode` for mutating operations (read ops `list`, `get`, `list_clients`, `effective_tools`, `explain` stay available under `read_only_mode`). Annotations: `destructiveHint: true`, `readOnlyHint: false`, `openWorldHint: false`.

Input schema (argument names = REST field names):

| Arg | Type | Used by |
|---|---|---|
| `operation` | enum `list, get, create, update, delete, rename, classify, assign, list_clients, effective_tools, explain` | all |
| `name` | string | get, create, update, delete, rename, classify, effective_tools — for `create` the new profile's slug, for `update` the profile being replaced (= REST path `{name}` = body `name`; there is no second name argument, a rename is only `rename`) |
| `servers`, `title`, `description`, `max_tier`, `unannotated`, `tools` (object `{allow, deny, classify}`), `code_execution` (bool; omitted = inherit), `management_tools` (bool; omitted = inherit), `switchable_to` (array; omitted = unset, `[]` = explicit none) | the `ProfileConfig` fields, same names, types and omission semantics as the REST body (data-model §1) | create, update (full replace; same validator) |
| `new_name` | string | rename |
| `reassign_to` | string | delete |
| `force` | bool | delete |
| `tool` | string `server:tool` | classify, explain |
| `tier` | enum `read, write, destructive, ""` (`""` removes) | classify |
| `client` | string client id | assign, explain, effective_tools |
| `profile` | string (`""` = All servers) | assign, explain, list_clients (optional filter: rows whose current binding is that profile, `-` = All servers — same as REST `GET /clients?profile=`) |
| `mode` | enum `locked, switchable` (optional; omitted = keep current mode, `profile: ""` → switchable — same as REST `PUT /clients/{id}/binding`) | assign (single and bulk form) |
| `from_profile`, `to_profile` | string | assign (bulk form) |
| `token` | string | explain |
| `anonymous` | bool | explain (subject = anonymous caller; exactly one of `client`, `token`, `profile`, `anonymous`) |

Results (JSON text content): same `data` objects as the REST routes (`ProfileView`, `ClientView` without `config_path`, `EffectiveTool[]`, `AccessExplanation`), plus `warnings[]` on `assign` and `create/update`. Errors (`isError: true`, JSON text content): **the REST error body verbatim** — `{error, code?, field?, used_by?, bindings?, fixes?, conflicting_token?, skipped?}` — so `profile_in_use` / `profile_is_anonymous_profile` (`code`, `used_by`), `no_client_credential` (`code`), `binding_bypassable_without_auth` (`code`, `bindings`, `fixes`) and validator errors (`field`) carry the same structure and text as on REST and an admin agent can offer the same remediation (FR-051). An earlier revision took a single opaque `profile_json` string for `create`/`update` beside a separate `name`, which broke FR-037's identical-argument rule and left the precedence of two names unspecified; and it returned only `{error, field?}` (codex round 3, research D29). Every mutating op writes `profile_change` with `surface=mcp`.

Registration: **not** through `buildManagementTools()` — that function returns no tools at all when `read_only_mode` or `disable_management` is on (`internal/server/mcp.go`), which would remove the read operations this contract keeps available. `profiles` is registered unconditionally on the retrieve, call-tool and code-exec servers by its own `buildProfilesTool()`; per-session visibility via `WithToolFilter` (re-run at `tools/call`) implements FR-017's credential/profile rule; inside the handler each operation re-checks visibility (defence in depth, Spec 105 pattern) and mutating operations (`create, update, delete, rename, classify, assign`) are refused with the existing read-only / management-disabled error when either global gate is on, while `list, get, list_clients, effective_tools, explain` run. A tool-surface golden pins its presence under `read_only_mode` (T088). Not added to `CallToolDirect`'s dispatch, so REST `POST /api/v1/tools/call {tool_name:"profiles"}` answers unknown-tool (REST has its own profile routes). Added to the tool-surface goldens (`internal/server/testdata/toolslist_goldens/`) (the only intended golden additions besides `retrieve_tools_profile_v3`).

## Management tools under a profile (FR-016)

| Tool | No profile, or `management_tools` unset (legacy and v3) | `management_tools: false` | `management_tools: true` | Client credential (profile without `true`, incl. All servers and unset) | Client credential under `true` |
|---|---|---|---|---|---|
| `upstream_servers` | pre-108 (anonymous confined by `anonymous_profile`: hidden + refused) | hidden + refused | pre-108 gates per credential, **except** an anonymous caller confined by `anonymous_profile`: `list`, `tail_log` on in-scope servers only, every mutating op refused (`AuthorizeServerOp` evaluated as non-admin, FR-016) | hidden + refused | Spec 028 `AuthorizeServerOp` policy, identical to every agent token: `list`, `tail_log` on in-scope servers; `add`, `add_from_registry`, `remove`, `update`, `patch`, `enable`, `disable`, `restart`, `refresh` refused (FR-016, research D14) |
| `quarantine_security` | pre-108 (anonymous confined: hidden + refused) | hidden + refused | administrator only (unchanged; never anonymous-confined) | hidden + refused | hidden + refused |
| `profiles` | admin kinds only when there is **no** profile; hidden + refused under a profile with `management_tools` unset (FR-017 has no legacy) | hidden + refused | admin kinds only | hidden + refused | hidden + refused |

## REST `POST /api/v1/tools/call` (existing route, FR-015)

`handleCallTool` → `CallToolDirect` dispatches `upstream_servers`, `quarantine_security`, `code_execution`, `retrieve_tools`, `call_tool_*` by name, bypassing every mcp-go `WithToolFilter`. Every gate in this document is therefore enforced **inside those handlers** from the request's `ProfileResolution` (a pinned `kind=agent` token resolves its pin on REST exactly as on MCP; client credentials never reach the route, FR-023): excluded upstream tool → `403` + refusals.md text + blocked activity record; `code_execution` or a management tool hidden by the profile → the route's existing unknown-tool error, from the same `unknown tool: <name>` formatter as a nonexistent name (same status and body shape; only the echoed name and per-request identifiers differ); `retrieve_tools` → same filtering and `hidden_by_profile`. The route is a column of the enforcement matrix. Two sibling REST routes share the gates: `POST /api/v1/code/exec` reaches `handleCodeExecution` through `CallToolDirect("code_execution")`, and the typed profile-hidden error (whose text is the `unknown tool: code_execution` formatter output everywhere else) is mapped by `classifyCodeExecError` to `403 PROFILE_BLOCKED` ([refusals.md](refusals.md)); `POST /api/v1/tool-calls/{id}/replay` bypasses every built-in handler, so it runs the FR-010 decision itself before its direct upstream call ([rest-api.md](rest-api.md)).

## Notifications

`notifications/tools/list_changed` sent via `SendNotificationToSpecificClient` on the mcp-go server instance that owns the session (retrieve, direct, call or code server) for: binding change of the session's credential (108-c); edit/rename/delete of the session's latest effective profile or base — detected by a per-profile (fingerprint, servers, existence) diff between consecutive published config snapshots, so service writes and hand edits are both covered (108-f); an `anonymous_profile` change, to every anonymous session (108-f). FR-027.
