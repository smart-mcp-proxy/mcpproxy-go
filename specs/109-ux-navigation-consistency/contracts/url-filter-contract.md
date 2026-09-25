# Contract: URL filter contract and deep links (Web UI · REST · CLI · macOS)

**This spec owns the whole contract** (ownership rule, spec.md Coordination, research D25): the composable, every parameter — `profile`, `client` and `token` included — the behaviour rules, the link map and the macOS `ScopeFilter`. Spec 108's [url-filter-contract.md](../../108-profiles-v3/contracts/url-filter-contract.md) (branch `108-profiles-v3`) is a pointer to this file: Spec 108 owns only the backend meaning of `profile`/`client`/`token` on its REST endpoints (its FR-031) and the availability signal `GET /api/v1/status` → `features.scope_filters: ["profile","client","token"]` (108-e). An earlier revision let each spec restate the contract, and the two copies diverged (codex review round 1); there is now one copy.

## Composable

`frontend/src/composables/useScopeQuery.ts`

```ts
type ParamDef = {
  name: string            // URL name
  rest?: string           // REST query name when different
  sticky?: boolean        // carried across scope-aware pages by in-app links
  pages: PageId[]         // pages that honour it
  parse?: (v: string) => unknown
  requires?: string       // availability feature ("scope_filters"): hidden until GET /api/v1/status features lists it
}
registerScopeParam(def: ParamDef): void          // internal registration API of this spec (109-k registers every row below, incl. profile/client/token)
useScopeQuery(page: PageId): {
  state: Reactive<Record<string, string | undefined>>
  set(patch: Record<string, string | undefined>): void     // router.replace, keeps unknown params
  clear(names?: string[]): void
  toRest(): Record<string, string> | null                    // mapped names, relative times resolved now; null = contradictory parameters (rule 8): the page issues no request
  linkTo(page: PageId, patch?: Record<string, string>): RouteLocationRaw  // carries sticky params
  chips: ComputedRef<Array<{name, label, value, remove(): void}>>
}
```

## Parameters (owned by Spec 109)

| URL | REST | CLI flag | macOS `ScopeFilter` | Meaning | Pages |
|---|---|---|---|---|---|
| `server` | `server` on Activity and Usage; **client-side on Tools and Review** (`GET /tools` parses no query string at `638fa805a` — `handleGetGlobalTools`; Review filters its own queue rows; `toRest()` never sends it there) | `--server` | server | upstream server | Tools, Activity, Usage, Review |
| `tool` | **split**: `toRest()` sends `server=<text before the first ':'>` and `tool=<the rest>` — the Activity record filter (`ActivityFilter.Matches`) and the Usage rollup (`parseUsageParams`) compare `tool` against the bare stored tool name, so sending `server:tool` would match nothing (codex round 4). A `server` also present in the URL is compared with the prefix: **equal** → `server` is sent once; **different** → the two filters are contradictory (their intersection is empty) and REST has one `server` parameter, so no request can express both: `toRest()` returns `null`, the page issues **no request** and renders the empty state "No calls match: server `<a>` and tool `<b>:<t>` name different servers" with both chips marked conflicting (rule 8) — never one value silently dropped, never rows of one server under chips implying both filters applied (zcode review). A value without `:` is sent as the bare `tool` next to any URL `server` | `--tool` (bare name, existing; a `server:tool` value is split into `--server` + `--tool` by the same rule; `--server a --tool b:t` with `a ≠ b` exits 1 with `--server a conflicts with the server in --tool b:t` before any request) | tool | canonical `server:tool` in the URL (links and chips always show it) | Activity, Usage |
| `session` | `work_session_id` when the value starts with `ws-`, otherwise `session_id` (the CLI's existing `sessionQueryParam` rule, `cmd/mcpproxy/activity_cmd.go`, so a legacy session recorded before Spec 082, which has no work session id, still resolves) | `--session` | session chip (existing) | work session (or a raw MCP transport session id for legacy rows) | Activity (all views) |
| `status` | `status` on Activity/Usage only; **client-side on Tools and Servers** (`GET /tools` and `GET /servers` parse no query string at `638fa805a`; `toRest()` never sends it there) | `--status` | status | **page-defined**, never sticky: Activity/Usage = call outcome `success|error|blocked|rejected` (the four values `activity list --status` and the REST validator accept; `rejected` = shed by a concurrency limit, Spec 093), plus the Web-only `other` on Activity (the existing "Other / internal" residual bucket, `OTHER_STATUS`: filtered client-side, never sent to REST, no CLI or macOS equivalent, so `toRest()` omits `status` when it is `other`); Tools = tool state `enabled|disabled|config-denied` (the CLI spelling of `tools list --status`; `config_denied` accepted as an alias); Servers = health status (mirrors `upstream list --status`) | Activity, Usage, Tools, Servers |
| `from`, `to` | Activity: `start_time`, `end_time`. **Usage: `window`** — `GET /activity/usage` accepts only `window=24h|7d|all` (`parseUsageParams`, `internal/httpapi/activity.go`), so `toRest()` maps `from=-24h` (no `to`) → `window=24h`, `from=-7d` (no `to`) → `window=7d`, no `from`/`to` → `window=all`; any other range stays in the URL and renders as a disabled chip "not applied on Usage (24 h, 7 d or all)" (rule 5) and the Usage picker offers only those three presets | `--from/--to` (aliases, 109-k) | time range picker | RFC 3339 or relative `-24h`, `-7d`, `-30d`; **sticky** | Activity, Usage |
| `view` | `type` (set of types) for `calls`/`system`/`all`; none for `sessions` (see below) | `--view` (no `sessions` value on the CLI, see cli.md) | segmented control | Activity view `calls|sessions|system|all` (default `calls`) | Activity |
| `type` | `type` | `--type` | type picker | explicit type set (overrides `view`) | Activity |
| `tab` | — | — | native state | tab of a tabbed page (Add server: `catalog|paste|import|manual`) | Server detail, Settings, Clients, Add server |
| `q` | Catalog: `q` on `GET /catalog/search`; Tools, Servers: client-side (`GET /tools` and `GET /servers` take no query parameters) | Catalog: `catalog search <query>`; Tools, Servers: none (`tools list` and `upstream list` have no query argument; pipe through `grep` or use `-o json`) | search field | free-text search | Tools, Servers, Catalog |
| `tier` (`risk` alias) | client-side | `--tier` (`--risk` alias) | tier picker | `read|write|destructive|unannotated` | Tools |
| `approval` | client-side | `--approval` | approval picker | `approved|pending|changed` | Tools |
| `auth_type` | `auth_type` | `--auth-type` | caller picker | `admin|agent` | Activity |
| `change` | client-side | — | — | `pending|changed` | Review |
| `source` | `source` | `--source` | catalog source picker | catalog source id (e.g. `official`); never an Add-server tab name, which is `tab` | Add server (Catalog tab) |
| `focus` | — | — | native selection | **page-local, never sticky**: the element to scroll to and highlight. One meaning (highlight) with a page-specific value space: Settings = field key (existing), server detail = section (existing endpoint focus; `env` added), Clients = client id, Spec 108 profile editor = `server:tool` | Settings, Server detail, Clients, (108) Profile editor |

| `profile` | `profile` | `--profile` | profile picker | **sticky**; requires `scope_filters`. Meaning per page (backend semantics are Spec 108 FR-031): effective profile **recorded at call time** on Activity (all views) and Usage; profile to **view as** on Tools and Servers; **current** binding on Clients (`GET /clients?profile=`) and current pin on Tokens (`GET /tokens?profile=`); `-` = unattributed / "All servers" / unpinned | Activity, Usage, Tools, Servers, Clients, Tokens |
| `client` | `client` | `--client` | client picker | **sticky**; requires `scope_filters`. Client id (`cursor`, `claude-code`, custom): recorded client on Activity/Usage, view-as subject on Tools (administrator only, Spec 108 FR-032), the selected/highlighted row on Clients (`GET /clients?client=`); `-` = unattributed | Activity, Usage, Tools, Clients |
| `token` | `token` (`agent` is the existing REST alias, honoured today only by `GET /activity` and `/activity/export`) | `--token` (`--agent` alias) | token picker | **sticky**; requires `scope_filters`. Agent-token name: recorded token on Activity/Usage, the selected row on Tokens (`GET /tokens?token=`) | Activity, Usage, Tokens |

`profile`, `client` and `token` are registered by 109-k with `requires: "scope_filters"`. Until Spec 108-e adds that feature to `GET /api/v1/status` they are **hidden**: no control, no chip, never sent to REST, but kept untouched in the URL (so a link shared from a newer instance survives).

**Backend gate (version skew, 109-k)**: callers that are not these UIs (curl, scripts written from this contract, third-party tools) must not get unfiltered rows presented as filtered. 109-k adds `internal/httpapi/scope_filters.go`: one supported-list variable (empty in 109-k) and a gate, called by every handler that Spec 108 FR-031 extends — `GET /activity`, `/activity/summary`, `/activity/usage`, `/activity/export`, `/sessions`, `/tools`, `/servers`, `/tokens`, and `GET /clients` once 109-h adds it — that answers `400 {"error":"scope filter '<param>' is not supported by this server","code":"unsupported_scope_filter","param":"<param>"}` for a `profile`, `client` or `token` query parameter that is not both in the list and in the set the handler itself honours. `agent` (the existing alias of `token`) is not gated on `GET /activity` and `/activity/export`, which honour it at `638fa805a` (`parseActivityFilters`); every other gated handler — `/activity/summary`, `/activity/usage` (`parseUsageParams` reads only `window`, `server`, `tool`, `status`, `top`, `sort`), `/sessions` — ignores it today, so there it is gated exactly like `token` until Spec 108-e makes the handler honour it (codex round 4). `features.scope_filters` is read from the same variable, so a build accepts a scope parameter exactly when it advertises it. Spec 108-e fills the list; 108-f makes `GET /clients` and `GET /tokens` honour the names (until then those two keep the `400`). 109-k and 108-e have no edge: **whichever merges first creates both the list variable and the gate function** (`rejectUnsupportedScopeFilters(w, r, honoured ...string) bool`, this signature and body) and calls the gate first in every handler it touches, so no build ever has a handler that parses a scope parameter without the gate (e.g. 108-e alone must still answer `GET /tools?token=ci-bot` with `400`, not an unfiltered `200`); the second to merge reuses both and only adds its own call sites (Spec 108 tasks T064, this spec's T121a). The profile editor's `focus` value (`server:tool`) is listed under `focus` above.

### `view` → REST mapping

| `view` | Request | Contract parameters that apply |
|---|---|---|
| `calls` | `GET /activity?type=tool_call,internal_tool_call` | all Activity parameters |
| `system` | `GET /activity?type=<every other type>` | all Activity parameters |
| `all` | `GET /activity` (no `type`) | all Activity parameters |
| `sessions` | `GET /sessions` (existing route and its own `limit`/`status` paging; no `/activity` request, no `type`) | `session` selects and highlights that session row; `from`/`to` stay in the URL and render as disabled "not applicable here" chips (rule 5); `server`, `tool`, `status`, `type`, `auth_type` likewise stay in the URL as disabled chips. `profile`, `client` and `token` (once available) **are** sent to `GET /sessions` (Spec 108 FR-031) |

An explicit `type` overrides the `view` mapping for `calls`/`system`/`all` and is ignored (disabled chip) in `sessions`.

## Behaviour (Web UI)

1. A scope-aware page reads its parameters on mount and applies them **before its first fetch** (no unfiltered request; SC-009 asserts it on the network log).
2. Changing a filter calls `router.replace`. Tabs and views also use `replace`. Back leaves the page.
3. In-app links built with `linkTo` carry sticky parameters (`from`, `to`, `profile`, `client`, `token` — the last three only once available). Page-specific parameters travel only when the link sets them.
4. Active filters render as removable chips. Clearing all chips clears every contract parameter the page supports.
5. A page that does not support a sticky parameter keeps it in the URL and shows it as a disabled chip reading "not applicable here". A parameter a page supports only client-side (Tools/Servers `status`) is applied in the page and never sent to REST; a value a page cannot apply (a Usage range other than the three `window` presets) is shown as a disabled chip that says so — no chip ever implies server-side filtering the backend does not perform (codex round 3).
6. Unknown parameters are preserved untouched. Legacy `?session=` links on Activity keep working, and `?risk=` maps to `tier`.
7. A parameter or link row whose `requires` feature is absent from `GET /api/v1/status` `features` is hidden (rule in the parameter section); it becomes visible without a reload when the status refresh lists it.
8. **Contradictory parameters** (today exactly one case: a URL `server` that differs from the `tool` prefix): `toRest()` returns `null`; the page issues no request, shows the conflict empty state and marks both chips; removing either chip resolves it and triggers the normal fetch. The CLI exits 1 before any request and macOS `ScopeFilter.restQuery()` returns `nil` with the same empty state, so no surface shows rows under filters it did not apply. A future parameter pair that can contradict follows this rule; precedence (one value silently winning) is never used.

## Link map (owned by Spec 109)

| From | Link | Opens |
|---|---|---|
| Attention item | fix button | per `fix.target` ([rest-api.md](rest-api.md#attention)) |
| Home usage strip | "calls today", "blocked", "errors" | `/activity?view=calls&from=-24h[&status=blocked|error]` |
| Server card stats line | "last call …", "N errors today" | `/activity?server=<n>&from=-24h[&status=error]` |
| Server card, needs review | primary button Review | `/review/<n>` (before 109-g: the interim redirect to `/servers/<n>?tab=tools`, rest-api.md#attention) |
| Usage chart bar (tool × bucket) | the calls behind the bar | `/activity?view=calls&tool=<server:tool>&from=<bucket start>&to=<bucket end>[&status=<s>]` |
| Tools row | "Calls" | `/activity?view=calls&tool=<server:tool>` |
| Tools row with `pending`/`changed` | "Review" | `/review/<server>?change=<state>` |
| Tools "Needs review" stat | — | `/review` |
| Review queue row | server | `/review/<server>` |
| Review screen | "Scan report" | `/security/scans/<jobId>` |
| Clients row | "N active sessions" | inline expander; each session → `/activity?session=<work_session_id>` |
| Activity row (Sessions view) | session | `/activity?view=calls&session=<work_session_id>` — the row's `work_session_id` field, never its MCP transport `id`; a legacy row without a `work_session_id` links with its `id`, which `toRest()` routes to `session_id` by the `ws-` prefix rule |
| Header status pill | — | `/servers` |
| Header attention pill | "See all" | `/` (Home) |
| Clients row *(requires `scope_filters`)* | Activity · Sessions · Tools it sees · Usage | `/activity?client=<id>` · `/activity?view=sessions&client=<id>` · `/tools?client=<id>` · `/usage?client=<id>` |
| Profile card (Spec 108 Profiles page) *(requires `scope_filters`)* | Tools · Activity · Clients · Tokens | `/tools?profile=<p>` · `/activity?profile=<p>` · `/clients?profile=<p>` · `/clients?tab=tokens&profile=<p>` |
| Activity row chips *(requires `scope_filters`)* | client · profile · token | `/clients?client=<id>` · `/profiles/<p>` · `/clients?tab=tokens&token=<n>` |
| Blocked Activity row *(requires `scope_filters` and the `/profiles` route)* | "Allow in profile…" · "Why?" | `/profiles/<p>?focus=<server:tool>` · Spec 108's access-explainer modal for (the record's `client_id` or `token_name`, tool) |
| Tools row in view-as mode, excluded *(requires `scope_filters`)* | "Why?" | Spec 108's access-explainer modal for (client/profile, tool) |
| Session row (Sessions view) *(requires `scope_filters`)* | Tools it sees | `/tools?client=<id>` |
| Token row (Clients → Agent tokens tab) *(requires `scope_filters`)* | Activity · Usage | `/activity?token=<n>` · `/usage?token=<n>` |

The rows marked *requires* are owned and wired here (109-k) and hidden until the feature (and, where named, the Spec 108 route) exists; Spec 108 supplies their target pages and the modal. 109-l tests that they appear.

## macOS

`AppState.scopeFilter: ScopeFilter` (struct with every parameter above, `profile`, `client` and `token` included and hidden until `features.scope_filters`) replaces `pendingActivitySessionFilter`. The in-app links of the link map (tray glance client row, Clients rows, Token rows, and the Profiles-card links of Spec 108's Profiles view) go through it. Views that accept a filter (Activity, Tools, Servers, Review) read it on appear and clear the pending value once applied. In-app links (tray glance client row, Home attention rows, Tools row → Activity) set it before switching the sidebar selection. `ScopeFilterTests` asserts the REST query mapping equals the tables above — `tool=github:create_issue` → `server=github&tool=create_issue` (also with `server=github`), `server=notion&tool=github:create_issue` → `nil` and no request (rule 8), `session=ws-…` → `work_session_id`, any other `session` → `session_id`, `server` never sent for Tools — including `view=sessions` → a `GET /sessions` request that carries **no** `from`/`to`, `server`, `tool`, `status`, `type` or `auth_type` (the inapplicable parameters of the `view` → REST table) but **does** carry `profile`/`client`/`token` once `features.scope_filters` lists them (identical to the Web rule above and to Spec 108 FR-031, which lists `/sessions`), and `status=other` never reaching macOS (it is Web-only). An earlier revision asserted "no contract-derived parameters" for Sessions, contradicting the table (codex review round 2).

## CLI

Flags as in the table. No navigation. The contract guarantees that a Web URL's parameters translate to the same CLI flags and the same REST query (parity test in 109-m).
