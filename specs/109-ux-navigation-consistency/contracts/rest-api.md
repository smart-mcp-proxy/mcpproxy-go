# Contract: REST additions and changes (Spec 109)

Base `/api/v1`, envelope `{success, data}` / `{success:false, error}` as today. Auth: API key or socket (administrator). Agent tokens on REST get filtered reads (their `allowed_servers`) where noted and the existing `requireServerOp` gates on mutations.

**Caller classes (binding for every new read route).** "Administrator" means `AuthContext.IsAdmin()` (API key, socket, server-edition `admin_user`) or the existing no-`AuthContext` bootstrap passthrough. Every other caller is **scoped**: `auth.IsScopedCaller(ctx)`, i.e. `!IsAdmin()`, which covers agent tokens in both editions **and** a plain server-edition OAuth `user` session. Handlers MUST branch on that predicate (the one `requireAdminRead` and `CanEnumerateServer` use, `internal/httpapi/tokens.go:106`), never on `Type == AuthTypeAgent`, which would fail open for an `AuthTypeUser` session. Per route the rule is one of: **admin-only** (`requireAdminRead`, scoped → `403`) or **filtered** (scoped callers see only servers passing `auth.CanEnumerateServer`, through the package's `canSeeServer`/`visibleServers` helpers in `internal/httpapi/scope.go`, so a user session with no `allowed_servers` sees nothing). Each route below names its rule, and each has a test with an agent token and a non-admin `AuthTypeUser` context (`-tags server`). Every route is documented in `oas/swagger.yaml` (regenerated, `swagger-verify` hook).

## Attention

`GET /attention` — both editions. Rule: **filtered**. Administrators get every item; a scoped caller (agent token or non-admin user session) gets only items whose server passes `CanEnumerateServer`, never a client item, with `count` recomputed from the narrowed list.

```json
{
  "count": 3,
  "generated_at": "2026-09-25T06:12:03Z",
  "items": [
    {
      "id": "sign_in_required:server:github",
      "kind": "sign_in_required",
      "rank": 10,
      "subject": {"type": "server", "id": "github", "name": "github"},
      "summary": "github: sign in required",
      "detail": "OAuth · api.githubcopilot.com",
      "fix": {"verb": "login", "label": "Sign in", "target": "/servers/github"},
      "since": "2026-09-25T06:02:11Z"
    },
    {
      "id": "server_review:server:github",
      "kind": "server_review",
      "rank": 50,
      "subject": {"type": "server", "id": "github", "name": "github"},
      "summary": "github: waiting for review",
      "detail": "Tools can be reviewed once sign-in finishes",
      "fix": {"verb": "review", "label": "Review", "target": "/review/github"},
      "since": "2026-09-25T06:01:40Z"
    },
    {
      "id": "client_never_seen:client:codex",
      "kind": "client_never_seen",
      "rank": 70,
      "subject": {"type": "client", "id": "codex", "name": "Codex CLI"},
      "summary": "Codex CLI: connected, never seen",
      "detail": "Restart Codex to load MCPProxy",
      "fix": {"verb": "reload_hint", "label": "How to restart", "target": "/clients?focus=codex"},
      "since": "2026-09-25T05:50:00Z"
    }
  ]
}
```

| `kind` | rank | Condition (inputs only from the state snapshot) | `fix.verb` → `target` |
|---|---|---|---|
| `sign_in_required` | 10 | `health.status == sign_in_required` (includes quarantined + OAuth and a failed OAuth refresh). A `ready` server whose `actions` is `["login"]` (token expiring soon) is **not** an item: it is usable, and the card's proactive Sign in button covers it | `login` → `/servers/<n>` (surfaces call the OAuth login action) |
| `missing_secret` | 20 | `health.status == needs_secret` | `set_secret` → `/servers/<n>?tab=config&focus=env` |
| `config_error` | 30 | `health.status == needs_config` | `actions[0]` (`configure` or `edit_url`) → `/servers/<n>?tab=config` |
| `server_error` | 40 | `admin_state != quarantined` **and** (`health.status == error` for ≥ 60 s, or `connecting` for ≥ 60 s). A quarantined server whose transport is down yields only `server_review`: restarting it before approval is meaningless, and the review screen shows the transport error | `restart` → server menu action; secondary `view_logs` |
| `server_review` | 50 | `admin_state == quarantined` (and not disabled) | `review` → `/review/<n>` (never one-click approve) |
| `tool_review` | 60 (`changed`) / 61 (`pending`) | trusted server with `pending` or `changed` tools; one item per server per state (so a server with both yields two attention items but one Review queue row; the attention count and the review count are different, separately named numbers, spec.md Definitions) | `review` → `/review/<n>?change=changed|pending` |
| `client_never_seen` | 70 | client `connected` by MCPProxy ≥ 5 min ago and no session since that write | `reload_hint` → `/clients?focus=<id>` (row highlighted, hint expanded) |

Conditions key on `health.status` (and `admin_state` for review), never on `actions` membership: `actions` also carries proactive nudges on usable servers (health-vocabulary.md), so it cannot tell a blocking state from a hint. Never items: disabled servers; `server_error` for a quarantined server (it is a `server_review` item); `ready` servers whatever their `actions`; servers `connecting` for < 60 s; update availability; scan findings on an already approved server (they appear in the review screen and scan history). Ordering: rank ascending, then `subject.name`. `id` is stable (`kind:type:subject`) so surfaces can diff lists.

SSE: `attention.changed` `{count, ids}` emitted when the set of `id`s changes. The runtime event carries the structured item list (`items: [{id, subject_type, subject_id}]`), and `/events` renders the wire payload **per subscriber** (FR-006): an administrator gets the full `{count, ids}`; a scoped caller (agent token, session principal) gets only the ids whose subject is a server it can see (`canSeeServer`), never a client item, with `count` recomputed from that narrowed list, and the frame is suppressed when that subscriber's narrowed id set is unchanged since the last frame it received. The type is classified with `servers.changed` as a per-subscriber-rendered type in `internal/httpapi/sse_scope.go`, never as a no-server-identity type (the existing scalar-field classifier cannot see into `ids`).

**Interim fix targets**: `/review` and `/review/<n>` render real views from 109-g. Until then the Web router (109-a, T026a, the first PR to link to `/review`) redirects `/review/:server` → `/servers/:server?tab=tools` and `/review` → `/servers?status=needs_review`, and macOS opens the server's detail view, so a `fix.target` is never a dead link. Before 109-k, `/servers` ignores `?status=` and shows the unfiltered list (the right page, not yet narrowed). `/clients?focus=<id>` exists from 109-h, which is also the PR that feeds client items (data-model §4).

Spec 108 kinds added in 109-l (rank 4–9, above sign-in (10) because they are security-relevant), named exactly as the Spec 108 clients `warnings[]` codes they come from (its data-model §7): `anonymous_denied_by_binding_guard` (4; Spec 108's binding guard is denying every uncredentialed caller — fix: turn on `require_mcp_auth` or narrow `anonymous_profile`), `client_holds_admin_key` (5; fix: Spec 108's bulk upgrade, then admin-key rotation), `client_token_name_conflict` (6), `profile_missing` (7), `client_rotation_pending` (8), `client_credential_expiring` (9). The older name `lock_bypassable_without_auth` is withdrawn in both specs.

## Health (all server payloads)

`health` gains `status`, `usable`, `actions` ([health-vocabulary.md](health-vocabulary.md)). Affects `GET /servers` (the list; there is no `GET /servers/{id}` read route at `638fa805a` — the `/servers/{id}` subtree registers only `PATCH`/`DELETE` and sub-resources), SSE `servers.changed` payloads, MCP `upstream_servers list`.

## Tools: `tier`

`GET /tools` and `GET /servers/{id}/tools` rows gain `tier` ∈ `read|write|destructive|unannotated` from `contracts.AnnotationTier` (FR-028). No other field changes.

## Review

`GET /review` and `GET /servers/{id}/review` — both editions. Rule: **filtered**. Administrators see every server; a scoped caller (agent token or non-admin user session) sees only servers passing `CanEnumerateServer`, and `GET /servers/{id}/review` is mounted inside the existing `/servers/{id}` route group, whose `scopedServerSubtree` middleware (`internal/httpapi/scope_subtree.go`, applied at `server.go:1007`), which answers a server the caller cannot see with the same `404` body as a missing one, so "not yours" and "not there" are indistinguishable.

One row per server awaiting review: a quarantined server (`kind: server_review`) or a trusted server with ≥ 1 `pending` or `changed` tool (`kind: tool_review`, both counts on the one row). `count` = the number of rows = the **review count** (spec.md Definitions), the number the Review queue sidebar badge (Web and macOS) shows; per-tool numbers appear only as each row's `pending`/`changed`/`tools_captured`.

```json
{
  "count": 2,
  "servers": [
    {"server": "filesystem", "kind": "server_review", "quarantined": true, "tools_captured": 14,
     "tier_counts": {"read": 9, "write": 3, "destructive": 2, "unannotated": 0, "unknown": 0},
     "scan": {"verdict": "clean", "risk_score": 0, "report_id": "…"}, "since": "…"},
    {"server": "github", "kind": "tool_review", "quarantined": false, "pending": 0, "changed": 1, "since": "…"}
  ]
}
```

`GET /servers/{id}/review`:

```json
{
  "server": {
    "name": "filesystem", "transport": "stdio",
    "command": "npx -y @modelcontextprotocol/server-filesystem /tmp", "url": "",
    "quarantined": true, "trust_mode": "manual",
    "source_registry_id": "official", "source_registry_provenance": "…",
    "scan": {"verdict": "clean", "risk_score": 0, "report_id": "…", "scanned_at": "…"},
    "definitions_captured": true
  },
  "tools": [
    {
      "name": "edit_file",
      "description": "Make line-based edits to a text file…",
      "input_schema": {"type": "object"},
      "output_schema": null,
      "annotations": {"destructiveHint": true},
      "tier": "destructive",
      "approval_status": "pending",
      "disabled": false,
      "scan_verdict": "clean",
      "held_reason": "", "held_signals": [],
      "previous": null
    },
    {
      "name": "search_code",
      "description": "…",
      "annotations": null,
      "tier": "unknown",
      "approval_status": "changed",
      "scan_verdict": "warnings",
      "previous": {"description": "…", "input_schema": {}, "annotations": null},
      "diff": {"description": "@@ -1 +1 @@\n-…\n+…", "input_schema": "", "annotations": ""}
    }
  ]
}
```

- `command`, `url` (and any env, header or args values the summary carries): **always redacted** by calling `oauth.RedactServerSecretFields`/`LiveRedaction` (`internal/oauth/serverfields.go`) **unconditionally**, for every caller — administrator or scoped. Unlike `GET /servers` (the list), which skips redaction for an administrator who set `reveal_secret_headers: true` (`redactServerSecrets` → `revealSecrets`, `internal/httpapi/server.go`), the review composer never consults `revealSecrets`/`reveal_secret_headers`: the review screen is not an edit surface and shows untrusted-server context, so no opt-out applies (codex round 3). The review composer is another door on the shared helper (beside the `GET /servers` list and SSE `servers.changed`; there is no `GET /servers/{id}` read route at `638fa805a`); a server whose URL query, header, env value or stdio command line carries a literal secret never has it echoed here (FR-021; parity test T077b, incl. an administrator with `reveal_secret_headers: true`). The example above shows a command with nothing to redact.
- `annotations`/`tier` pairs: `annotations: null` → `tier: "unknown"` (nothing captured — a record from before this spec, as in the `search_code` example above); `annotations: {}` → `tier: "unannotated"` (captured, no hints); otherwise `contracts.AnnotationTier`. For instance `{"name": "list_dir", "annotations": {}, "tier": "unannotated", …}`.
- `source_registry_id`, `source_registry_provenance`: the existing MCP-866 origin fields of the server config, `omitempty` (absent for a manually added server).
- `tier`: `unknown` when the record carries no stored annotations (captured before this spec).
- `scan_verdict` per tool: `dangerous|warnings|clean|not_scanned`, from the latest baseline report's findings for that tool, else the record's `held_verdict`.
- `definitions_captured: false` → `tools: []`. Surfaces offer "Fetch tool definitions" = `POST /servers/{id}/scan` (the existing offline baseline scan under the inspection exemption).
- Descriptions are returned verbatim. Surfaces render them as inert text (research D19).

Review verbs (existing routes; one change):

| Verb | Route | Change |
|---|---|---|
| Approve server | `POST /servers/{id}/security/approve` `{force?: bool, block?: [tool]}` | NEW optional `block`: these tools are blocked in the existing `BlockTools` representation (approval `status=approved`, `disabled=true`; there is no `blocked` status) **in the same storage transaction that writes the integrity baseline, before the server is unquarantined**, so no concurrent call can reach them at any instant (race test T078b). An unknown tool name → `400` before anything changes |
| Reject server | `POST /servers/{id}/security/reject` | none |
| Approve tool(s) | `POST /servers/{id}/tools/approve` `{tools:[…]} or {approve_all:true}` | none |
| Reject tool(s) | `POST /servers/{id}/tools/block` | none |
| (legacy) | `POST /servers/{id}/unquarantine` | no first-party caller (SC-005); kept for API compatibility |

## Clients (personal edition only)

`GET /clients`, `GET /clients/{id}` — Rule: **admin-only**. A scoped caller (agent token) gets `403` through `requireAdminRead`, like `GET /config`: presence rows reveal local config paths and per-client session counts for every client, which no server scope covers. **This spec owns these routes and the row shape** (ownership rule); Spec 108 decorates the rows additively (credential and binding fields, `kind` value `custom`, response-level `warnings[]`), adds the `profile`/`client` filters to these routes, and owns its own mutating routes (`PUT /clients/{id}/binding`, rotate, forget, custom add, bulk) — its data-model §7.

```json
{
  "clients": [
    {
      "id": "claude-code", "display_name": "Claude Code", "kind": "supported", "icon": "claude",
      "state": "connected_seen",
      "installed": true, "connected": true,
      "config_path": "/Users/you/.claude.json", "display_path": "~/.claude.json",
      "last_seen": "2026-09-25T06:10:00Z", "active_sessions": 1, "calls_24h": 38,
      "reload_hint": "Run /mcp in Claude Code (or restart it) to load MCPProxy",
      "sessions": [{"id": "…", "work_session_id": "…", "started_at": "…", "last_activity": "…"}]
    },
    {"id": "cursor", "display_name": "Cursor", "kind": "supported", "icon": "cursor", "state": "installed",
     "installed": true, "connected": false, "connection_unverified": true,
     "config_path": "/Users/you/.cursor/mcp.json", "display_path": "~/.cursor/mcp.json",
     "last_seen": null, "active_sessions": 0, "calls_24h": 0, "reload_hint": "…"},
    {"id": "other:zed", "display_name": "zed", "kind": "other", "state": "other", "installed": false, "connected": false,
     "last_seen": "…", "active_sessions": 0, "calls_24h": 2}
  ],
  "routing": {"routing_mode": "retrieve_tools", "endpoints": {"default": "/mcp", "direct": "/mcp/all", "code_execution": "/mcp/code", "retrieve_tools": "/mcp/call"}, "pending_routing_mode": "", "restart_required": false}
}
```

These field names are the base of Spec 108's `ClientView`, which extends them without renaming, narrowing or dropping any (`icon`, `state`, `config_path`, `display_path` and `reload_hint` included). `kind` is `supported|other` here (`other` = an unrecognised `clientInfo.name`, no credential), and Spec 108 adds `custom` (a user-added client with a credential). `routing` is the `GET /routing` payload verbatim (same keys), so the Endpoint & mode tab and the old header dropdown read one shape. `state` ∈ `connected_seen | connected_never_seen | installed | not_installed | other`. `sessions` is present only on `GET /clients/{id}`. The server edition does not register the route (404). **`connected` without a content read** (FR-030): the list reads `connect.GetAllStatus()`, which never opens a client config (Spec 075 FR-001, no macOS App-Data prompt) and always reports `Connected=false`, so `connected` on the list is MCPProxy's own evidence — a recorded connect write, or a session seen from the client (`last_seen`). A client configured by hand with no session yet is `state: installed`, `connection_unverified: true` (the Cursor row above); `GET /clients/{id}` — the explicit per-client read — additionally calls `connect.GetStatus()` (the Spec 075 FR-002 content read) and reports it `connected` when its config points at MCPProxy.

## Connect (existing routes, additive)

- `ClientStatus` (in `GET /connect`, `GET /connect/{client}`) gains `display_path` (home replaced by `~`) and `reload_hint`. Spec 108-c adds `credential_state` to the same struct in a parallel PR (no edge; the later merge keeps all three fields, tasks.md Dependencies) and, because that field names the clients still holding the admin API key, makes `GET /connect`, `GET /connect/{client}` and `GET /connect/{client}/preview` administrator-only (`requireAdminRead`; Spec 108 FR-025a) — the same rule as `GET /clients` above. **109-h adds the same gate itself** (FR-030a, codex round 3): at `638fa805a` those reads are open to every authenticated caller and return the config path and connected state that `GET /clients` withholds, so the PR that adds the administrator-only `/clients` closes them too; whichever of 109-h / 108-c merges first adds it (same middleware and message: `403 {"error":"Admin credentials required to read client connection status"}`), the other keeps it (T125a, Spec 108 T030b). This spec's consumers (Web UI, tray, CLI, `ClientPresence` via the in-process `connect.GetAllStatus()`) are unaffected.
- `ConnectResult` (from `POST /connect/{client}`) gains `reload_hint` and `display_path`.
- The bulk path (Web UI "Connect selected") calls `GET /connect/{client}/preview` for every selected client and shows the combined diff before the first `POST`. There is no server-side bulk route.

## Scope-filter gate (109-k, FR-080a)

`GET /activity`, `/activity/summary`, `/activity/usage`, `/activity/export`, `/sessions`, `/tools`, `/servers`, `/tokens` and (from 109-h) `/clients` reject `profile`, `client` or `token` that the build does not support with `400 {"error":"scope filter '<param>' is not supported by this server","code":"unsupported_scope_filter","param":"<param>"}`, checked before any other work. Supported = listed in the one variable that also produces `GET /api/v1/status` `features.scope_filters` **and** honoured by that handler. Empty in 109-k; Spec 108-e fills it (108-f adds `/clients`, `/tokens`). `agent` (alias of `token`) is not gated on `GET /activity` and `/activity/export`, which honour it today, and is gated like `token` on `/activity/summary`, `/activity/usage` and `/sessions`, which ignore it at `638fa805a`. The list variable and `rejectUnsupportedScopeFilters` are created together by whichever of 109-k / Spec 108-e merges first. Full rule: [url-filter-contract.md](url-filter-contract.md) "Backend gate".

## Onboarding

`GET /onboarding/state` gains `has_usable_server` (bool) and `usable_servers` (`[name]`, used by Verify prompts). `incomplete_tab_count` uses `has_usable_server` for the Servers step.

## Import preview

`POST /servers/import/json?preview=true` accepts, besides JSON/TOML content, a single `http(s)://` URL or a single command line (format `url` / `command`, detected or passed as `format`). Each proposed server gains:

```json
{"summary": "npx -y @modelcontextprotocol/server-filesystem /tmp", "tags": ["local process"],
 "env": [{"name": "GITHUB_TOKEN", "value_present": true, "secret_like": true, "empty_or_placeholder": false}],
 "headers": [{"name": "Authorization", "secret_like": true, "empty_or_placeholder": false}]}
```

`tags` ⊆ `local process`, `remote`, `needs secret`, `oauth`. Preview never executes a command and never contacts a URL.

## Secrets at add time

No new route: the Web UI and macOS write each secret with the existing `POST /secrets` (keyring) and then add the server with `${keyring:<ref>}` in `env`/`headers`, where `<ref>` is `<server>-env-<name>` or `<server>-header-<name>` (lowercase, runs outside `[a-z0-9-]` → `-`, ≤ 64 characters; FR-065), so an env var and a header with the same name never share an entry. `POST /secrets` and the keyring provider's `Store` overwrite an existing name silently, so before writing the surface reads the existing names (`GET /secrets`) and appends `-2`, `-3`, … to a computed name that is taken — an add never overwrites a secret it did not create. If the add fails after secrets were written, the surface deletes only the secrets it wrote (`DELETE /secrets/{name}`). The naming rule is one function per language, pinned by the shared fixture `internal/secret/testdata/ref_names.json` (incl. the env/header same-name pair and a taken name). Keyring availability: `GET /secrets/config` gains `keyring_available` (bool) and `keyring_reason` from the provider's existing `IsAvailable()` probe (`internal/secret/types.go:32`). `false` → the toggle is disabled with the reason.

## Catalog

`GET /catalog/search?q=&source=&tag=&limit=20` — both editions. Rule: **filtered** on `added` only. Catalog entries are catalog-source data, readable by any authenticated caller exactly like today's `GET /registries/{id}/servers` (no admin gate). `added` is derived from the configured servers, so for a scoped caller it is computed only over servers passing `CanEnumerateServer`: an entry matching an out-of-scope server reads `added: false`, and no field names or reveals a configured server's name, URL or command. The MCP `search_servers` result carries no `added` field at all (contracts/mcp-tools.md).

```json
{
  "query": "github",
  "results": [
    {"source": "official", "id": "io.github.github/github-mcp-server", "title": "GitHub",
     "publisher": "github", "verified": true, "official": true, "popularity": {"stars": 21000},
     "description": "…", "transport": "http", "install": {"url": "https://api.githubcopilot.com/mcp/"},
     "required_inputs": [{"name": "GITHUB_TOKEN", "secret_like": true}], "added": false}   // secret_like = the registry's isSecret OR the research D13 secret-like-name rule (data-model §9)
  ],
  "sections": null,
  "unavailable": [{"source": "smithery", "reason": "timeout after 5s"}]
}
```

- Result objects are the `CatalogResult` **DTO** (data-model §9), built from the internal `CatalogHit` by `toCatalogResult`; the Spec 070 `registries.ServerEntry` JSON (`url`, `installCmd`, `registry`, `required_inputs[].secret`) is not renamed and keeps serving `GET /registries/{id}/servers` and MCP `search_servers` unchanged (codex round 3: embedding `ServerEntry` could not produce this example). Golden test T101a.
- Empty `q` → `results: []`, `sections: {"official": [...], "popular": [...]}` (≤ 12 each).
- Ranking (pure `registries.Rank`): `official` desc, `verified` desc, popularity desc (missing = 0), relevance desc, `title` asc, `id` asc.
- `added: true` when a configured server has `source_registry_id == source` **and** the same install target (`install.url`, or the command + args) as this result → surfaces render "Added ✓ · Open". The config does not store the registry's own server `id`, so `id` is not part of the join (data-model.md §9). A manually added server matches on the install target alone.
- Adding stays `POST /registries/{id}/servers/{serverId}/add` (Spec 070), always quarantined.

## Activity summary (additive)

`GET /activity/summary` gains `per_server: [{name, calls, errors, last_call_at}]` covering every server with tool calls in the period. It is computed in the same counting pass as the existing totals; `top_servers` is unchanged. Used by server-card stats lines (109-e) and the macOS Servers rows.

## Token metrics

`ServerTokenMetrics` gains `estimated` (bool). When no `retrieve_tools` result sizes exist yet, `average_query_result_size` = mean per-tool token size × the default `retrieve_tools` limit, `saved_tokens`/`saved_tokens_percentage` are computed from it, and `estimated: true`.

## Events

| SSE type | Payload | Emitted when |
|---|---|---|
| `attention.changed` | `{count, ids}` | attention item set changes; rendered per subscriber (see Attention, FR-006) |
| `review.changed` | `{server}` | a review item appears/disappears or a review verb completes. Added to `identityBearingEventTypes` (`sse_scope.go`): a scoped subscriber receives it only for a server it can see, and a frame with an empty `server` is dropped |
