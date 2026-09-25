# Contract: enforcement matrix (the test oracle for FR-010…FR-018, SC-002, SC-009)

## Fixture

Servers (all enabled, approved, connected via in-process fake upstreams):

| Server | Tool | Annotations | Intrinsic tier |
|---|---|---|---|
| `github` | `list_issues` | readOnly=true | read |
| `github` | `create_issue` | readOnly=false | write |
| `github` | `delete_repo` | destructive=true | destructive |
| `github` | `search_code` | none | unannotated |
| `github` | `get_secret_scanning_alert` | readOnly=true | read |
| `notion` | `update_page` | readOnly=false | write |
| `filesystem` | `read_text_file` | readOnly=true | read |

Profiles:

```json
[
  {"name":"work-readonly","title":"Work · Read-only","servers":["github","notion"],"max_tier":"read",
   "tools":{"allow":["notion:update_page","github:get_secret_scanning_alert","filesystem:read_text_file"],"deny":["github:*secret*"]},"code_execution":false,"management_tools":false,
   "switchable_to":["work-full"]},
  {"name":"work-full","servers":["github","notion","filesystem"],"max_tier":"destructive","unannotated":"as_write","code_execution":true},
  {"name":"legacy","servers":["github"]}
]
```

## Expected profile decisions (FR-010)

| Tool | work-readonly | work-full | legacy |
|---|---|---|---|
| github:list_issues | admitted | admitted | admitted |
| github:create_issue | above_tier_cap | admitted | admitted |
| github:delete_repo | above_tier_cap | admitted | admitted |
| github:search_code | unannotated_hidden | admitted (as write) | admitted (legacy read) |
| github:get_secret_scanning_alert | denied_by_rule (matched by **both** an allow and a deny rule: deny beats allow, FR-010 step 2 before step 3) | admitted | admitted |
| notion:update_page | admitted (allow) | admitted | server_not_in_profile |
| filesystem:read_text_file | server_not_in_profile (the allow rule `filesystem:read_text_file` names a server outside `servers`: loaded with the FR-007 warning `profile "work-readonly" rule "filesystem:read_text_file" names server "filesystem" outside the profile; ignored`, never widening — FR-004) | admitted | server_not_in_profile |
| after `classify work-readonly github:search_code read` → github:search_code | admitted | — | — |

## Resolution sources × surfaces

Each cell runs the full decision table above and asserts discovery + call outcome.

| Source \ Surface | `/mcp` retrieve_tools | describe_tool | call_tool_* | code_execution (+nested) | `/mcp/all` list+call | `/mcp/call` | `/mcp/code` | `/mcp/p/<slug>` | REST `POST /api/v1/tools/call`, `/code/exec`, `/tool-calls/{id}/replay` |
|---|---|---|---|---|---|---|---|---|---|
| locked client credential (pin) | ✓ | ✓ | ✓ | ✓ tool absent (readonly) / nested gate (full) | ✓ | ✓ | ✓ | refused unless slug = pin (own base) | n/a — `403` (FR-023) |
| switchable client credential (binding) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | only `switchable_to` (or own base, FR-022) | n/a — `403` (FR-023) |
| legacy pinned agent token | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Spec 105 rules | ✓ (FR-015 "every dispatch path") |
| URL `/mcp/p/<slug>` (API key) | ✓ | ✓ | ✓ | ✓ | n/a | n/a | n/a | ✓ | n/a (no URL tier on REST) |
| `set_profile` session (API key) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | n/a | n/a (no session on REST) |
| anonymous + `anonymous_profile` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | only `switchable_to` (or own base, FR-022) | n/a — REST always requires a key |
| anonymous under the FR-008a binding guard (`require_mcp_auth` off, a client locked to work-readonly, `anonymous_profile` unset or = work-full) | ✓ deny-all: no tools, `hidden_by_profile: 0`, no `profile` field | ✓ uniform not-found | ✓ Spec 105 out-of-scope refusal for every tool | ✓ absent | ✓ | ✓ | ✓ | refused for every slug | n/a |
| none (API key, no profile) | all admitted | — | — | — | — | — | — | — | all admitted |

Additional assertions per cell: `hidden_by_profile` value for queries "issue", "secret", "search", "repo" (work-readonly: 1, 1, 1, 1 — fixture descriptions equal the tool names so BM25 matches are deterministic; work-full: present with value `0` for every query, because it is non-legacy; legacy: field absent — FR-011); `profile` field present only in the URL and `set_profile` rows (FR-011); a `limit: 1` query whose top-scoring hit is policy-excluded still returns the best admitted hit (filter before limit, FR-011); refusal texts identical across every source row (no profile name, FR-013); zero upstream calls for every refused call (counting upstream); activity record fields `profile`, `profile_source`, `client_id`, `token_name`, `block_reason`; explainer verdict equals call outcome (SC-009); Spec 105 two-fixture differential oracle holds with the policy applied.

`/mcp/call` column: the call-tool routing-mode server (`internal/server/server.go` mounts `/mcp/call` separately from `/mcp`) is its own mcp-go instance with its own `WithToolFilter` registration, so each cell asserts on that instance directly: `tools/list` omits `code_execution` and the management tools exactly as the profile decides, `retrieve_tools` returns the same results and `hidden_by_profile` as the `/mcp` cell, and `call_tool_write github:create_issue` under work-readonly is refused with the refusals.md text (T045).

REST column (legacy agent token pinned to each profile, `POST /api/v1/tools/call`): `{tool_name:"call_tool_write", arguments:{name:"github:create_issue"}}` under work-readonly → `403` with the refusals.md text and a `status=blocked`, `block_reason=profile_tier` record; `{tool_name:"upstream_servers", arguments:{operation:"list"}}` and `{tool_name:"code_execution", …}` under work-readonly (`management_tools:false`, `code_execution:false`) → the route's unknown-tool error: same status and body shape as `{tool_name:"no_such_tool"}`, produced by the same `unknown tool: <name>` formatter, differing only in the echoed tool name (and per-request identifiers); `{tool_name:"retrieve_tools", arguments:{query:"issue"}}` → same tools and `hidden_by_profile` as the MCP cell; zero upstream calls for every refusal. Same token on `POST /api/v1/code/exec` (FR-014): under work-readonly → `403` `PROFILE_BLOCKED` with the refusals.md text and a `block_reason=profile_code_execution` record, never `500 EXECUTION_FAILED`; under work-full the script runs; with the global `enable_code_execution` also off, the profile answer still wins. Same token on `POST /api/v1/tool-calls/{id}/replay` of a recorded `github:create_issue` call (recorded under an admin key): under work-readonly → `403` with the refusals.md `profile_tier` text, a blocked record, zero upstream calls; under work-full → replayed; replay of a recorded `filesystem:read_text_file` call (server outside work-readonly's `servers`) → the route's existing non-disclosing `404`, byte-identical to an unknown record id after substituting the id, zero upstream calls (FR-015, T046b). **REST discovery** (FR-015a, T046c): with the work-readonly-pinned token, `GET /api/v1/index/search?q=issue`, `GET /api/v1/tools`, `GET /api/v1/servers/github/tools` and `GET /api/v1/servers/github/tools/export` contain exactly the admitted tools of the decision table (no `create_issue`, `delete_repo`, `search_code`, `get_secret_scanning_alert`, no `filesystem` row), `GET /api/v1/servers/github/tools/create_issue/diff` → unknown-tool `404`, and an unpinned token and the admin key see today's output.

**Binding-guard "wider" cases** (FR-008a, T033a): with `require_mcp_auth` off and a client locked to work-readonly, each of these `anonymous_profile` values makes the binding bypassable and must be refused by every API path / trip the runtime guard on a hand edit: `work-full` (servers, cap); a copy of work-readonly without the `github:*secret*` deny rule (same servers and cap — admits `github:get_secret_scanning_alert`, condition ii); a copy with `unannotated: as_read` (condition i); a copy with `code_execution: true` (condition iii); and `work-readonly` itself — its `switchable_to: ["work-full"]` lets an anonymous caller `set_profile("work-full")`, which a **locked** client cannot (reachability). For a client **switchable** on work-readonly, `anonymous_profile: work-readonly` is not bypassable (the binding reaches work-full too), and for either mode a copy of work-readonly with `switchable_to` unset (other name) is not bypassable. Rows elsewhere in this matrix that need a non-wider `anonymous_profile` next to a locked work-readonly binding use that copy, never `work-readonly` itself.

## Management and switching

The `profiles` column is asserted only from PR 108-h (T086), where the tool is created; 108-d's tests (T047/T048) assert every other column and must not depend on `profiles` existing. Each row runs in its own configuration; rows whose fixture holds a client binding to a named profile set `require_mcp_auth: true` (or a non-wider `anonymous_profile`) so the FR-008a guard does not apply, except the row that tests the guard itself.

| Caller | tools/list contains `profiles` | `upstream_servers` | `set_profile("work-full")` | `set_profile("legacy")` | `set_profile("")` (FR-018/FR-022: always admitted; next request resolves to) |
|---|---|---|---|---|---|
| API key, no profile | yes | yes | ok | ok | ok → none |
| API key, session on work-readonly | no (mgmt false) | no | ok (admin, no base) | ok | ok → none (management tools and `profiles` visible again) |
| socket | yes | yes | ok | ok | ok → none |
| anonymous, no `anonymous_profile`, no named client binding | no | yes (legacy) | ok | ok | ok → none |
| anonymous, no `anonymous_profile`, `require_mcp_auth` off, a client locked to work-readonly (hand-edited config; FR-008a guard) | no | no | uniform refusal | uniform refusal | ok → deny-all (`anonymous`) |
| anonymous, `anonymous_profile=work-readonly` | no | no | ok (in switchable_to) | uniform refusal | ok → work-readonly (`profile_source=anonymous`) |
| anonymous, `anonymous_profile=legacy` (management_tools unset) | no | no (confined anonymous is never legacy) | uniform refusal (`switchable_to` unset = none) | ok (own base, FR-022) | ok → legacy (`anonymous`) |
| client locked to work-readonly | no | no | uniform refusal | uniform refusal | ok → work-readonly (`pin`; no-op) |
| client switchable, base work-readonly | no | no | ok | uniform refusal | ok → work-readonly (`binding`): after `set_profile("work-full")`, `set_profile("")` returns the session to its binding |
| client switchable, base All servers | no | no | ok (legacy) | ok (legacy) | ok → All servers (no profile) |
| legacy agent token, no pin | no | per Spec 105 | ok if selectable | ok if selectable | ok → none (token scope only) |
| legacy agent token pinned to a v3 profile with `management_tools: true` | no (FR-017) | Spec 028 `AuthorizeServerOp` policy, unchanged | uniform refusal (pinned) | uniform refusal | ok → its pin (`pin`; no-op) |
| anonymous, `anonymous_profile` = a v3 profile with `management_tools: true` (`require_mcp_auth` off) | no | `list`/`tail_log` on in-scope servers only; `add`/`patch`/`restart`/every other mutating op refused, no config write, no restart (non-admin `AuthorizeServerOp`, FR-016) | per that profile's `switchable_to` | per that profile's `switchable_to` | ok → that profile (`anonymous`) |
| client locked to a v3 profile with `management_tools: true` | no | Spec 028 `AuthorizeServerOp` policy: `list`/`tail_log` on in-scope servers only; `add`/`patch`/`restart`/every other mutating op refused; out-of-scope server → non-disclosing refusal | uniform refusal | uniform refusal | ok → its pin (`pin`; no-op) |
| client locked to work-readonly, config URL `/mcp/p/work-readonly` (own pin) | no | no | uniform refusal (`set_profile("work-readonly")` and the own-pin URL itself are **admitted** — naming the own base is not a switch, FR-022) | uniform refusal | ok → work-readonly (the URL names the own pin) |
| client switchable, bound profile hand-deleted from config (dangling binding) | no | no | uniform refusal (no `switchable_to` known) | uniform refusal; every tool call resolves deny-all with `profile_source=binding` (FR-020) | ok → still deny-all (`binding`, dangling; never falls through) |
| client switchable (base work-readonly) on `set_profile("work-full")`, then operator locks it to work-readonly | no | no | next request resolves to work-readonly (selection cleared) | uniform refusal | ok → work-readonly (`pin`) |
