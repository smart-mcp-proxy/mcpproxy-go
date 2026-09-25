# Feature Specification: Navigation, Scope Filters and Cross-Surface UX Consistency

**Feature Branch**: `109-ux-navigation-consistency`
**Created**: 2026-09-25
**Status**: Draft. Plan and tasks are complete. Cross-model review (zcode GLM-5.3) ran 6 rounds, and the final verdict is CLEAN; codex (gpt-6-sol) review round 1: 8 findings on this spec, 8 applied, plus the maintainer's cross-spec ownership rule with Spec 108 ([checklists/requirements.md](checklists/requirements.md), research D25); codex cross-spec round 2 (research D26); codex round 3: 13 findings on this spec, 13 applied (research D27). Decisions are in [research.md](research.md); there are no open clarifications.
**Input**: UX audit revision 2 (2026-09-25, `v0.69.1 @ 638fa805a`; a maintainer working artifact that is not committed to the repository — each finding used here is restated with a code or UI reference in the FRs and research, which are the traceable basis, as in Spec 108), every finding except S1, O1, S3 and P1–P7: onboarding O2–O6; navigation N1–N8; header H1–H4; servers S2, S4–S7; catalog C1–C2; activity/usage A1–A3; tools T1; the shared URL filter contract and deep-link map; roadmap items M1–M7 and the quick wins. Also covered: the four surface inventories (Web UI, macOS app, CLI, MCP/REST at `638fa805a`) and their contradiction lists. Maintainer intent: "Web UI, macOS app, CLI and MCP features must stay in sync and must not contradict each other (same concepts, same names, same capabilities, same semantics)."

**Related**: Spec 108 (Profiles v3; this spec owns the shared navigation and filter artifacts and integrates 108's APIs, see [Coordination with Spec 108](#coordination-with-spec-108)), Spec 046 (onboarding wizard), Spec 050 (global Tools page), Spec 032 (tool-level quarantine), Spec 077/086 (scan gate, hold evidence), Spec 070 (registry add), Spec 075 (connect payload), Spec 069 (usage aggregate), Spec 044 (diagnostics), earlier audits `docs/qa/ux-audit-webui-2026-08.md` and `docs/qa/ux-audit-macos-tray-2026-08.md` (F-ids; this spec reverses one of their outcomes, #1044's Usage-first landing, see research D3).

**Excluded (handled elsewhere)**: S1 (Log in button on a quarantined OAuth card, fixed in another session), O1 (wizard offers to import MCPProxy itself) and S3 (server detail stale after Approve), both fixed by another session, and P1–P7 (Spec 108). This spec builds on the S1 fix and does not re-specify it. The ordered `health.actions[]` list in FR-012 is the audit's "longer term" S1 follow-up and is compatible with that fix.

## Context & Motivation

The product has two halves: AI clients on one side, MCP servers on the other. The navigation shows only the server half. Clients, the endpoint, the routing mode and agent tokens are spread across a wizard, a Dashboard tab, a Settings banner and the global header. No single place says what needs attention next. The four surface inventories show that the Web UI, the macOS app, the CLI and MCP each answer the same questions with different words, different predicates and sometimes different semantics:

| # | What the inventories found | Surfaces | Consequence |
|---|---|---|---|
| X1 | Three "needs attention" predicates: Web UI Dashboard (`unhealthy`, or `degraded` with an action, quarantined excluded, `Dashboard.vue:910`); macOS (any `health.action` except `enable`, quarantined included, `AppState.swift:306`); CLI `doctor` (diagnostics endpoint issue count, `doctor_cmd.go:367`) | Web, macOS, CLI | The count a user watches differs by surface |
| X2 | "Approve server": the Web UI calls `POST /servers/{id}/security/approve`, which is scan-gated (Spec 077 FR-021), creates the integrity baseline and unquarantines. The macOS app calls `tools/approve` then `POST /unquarantine`, which bypasses the scan gate and the baseline (`ServerDetailView.swift:201-202`) | Web, macOS | Same button, different security semantics |
| X3 | Informed review: macOS shows captured tool definitions, a diff and per-tool Approve (`ServerDetailView.swift:1616-1780`); the Web UI shows "Tools withheld for review" (`ServerDetail.vue:1736`); the CLI has three approve verbs (`upstream approve` and `tools approve|reject` for tools, `security approve|reject` for the server) | Web, macOS, CLI | Approval is blind on one surface and informed on another |
| X4 | The catalog has three names: "Repositories" (Web UI, under System), "Registries" (macOS sidebar), `registry` (CLI, which mixes sources and search) | all | Nobody can say "open the catalog" |
| X5 | Health: a quarantined server reports `level: healthy` (`internal/health/calculator.go:128`) while its tile says "Blocked" | Web, CLI JSON, macOS | Contradictory readings of the same server |
| X6 | Navigation: Web UI sidebar Workspace/Observability/System vs macOS Dashboard/Servers/Tools/Registries/Activity/Secrets/Tokens; Sessions and Security are pages on Web and absent on macOS; Agent Tokens is under Secrets on Web | Web, macOS | Different mental models of the product |
| X7 | Tool review states: Web `Awaiting approval` + `Pending` + `Changed` (overlapping, `Tools.vue:141-144`); CLI `--approval approved|pending|changed`; macOS "Pending Approval" / "Changed (needs re-approval)" | all | Three vocabularies for two states |
| X8 | Connect: no surface tells the user to restart or reload the client after writing its config; each shows a different path form | Web, macOS, CLI | "Done" is not "working" (O4) |
| X11 | Tool tier is computed three ways: Web Tools `getRisk` maps an unannotated tool to `write` (`Tools.vue:802`); the backend `DeriveCallWith` maps it to `read` (`intent.go:223`); CLI `tools list --risk` compares against `annotations.operation_type` (`tools_cmd.go:168`), a field that tool annotations do not carry, so it presumably never matches (109-a's failing test confirms before fixing) | Web, CLI, macOS | The same tool shows as write, read, or missing depending on the surface |

This spec gives the product one information architecture (Home · Connect · Protect · Monitor), one "needs attention" list, one health vocabulary, one review flow, one catalog, one URL filter contract, and a binding terminology and parity matrix for all four surfaces. Every inventory contradiction is either resolved by a requirement or explicitly accepted with a reason ([Contradiction register](#contradiction-register)).

## Scope Boundary *(read before planning)*

| Already exists (reused, not rebuilt) | Changed by this spec |
|---|---|
| Unified `health` object (`level`, `admin_state`, `summary`, `detail`, `action`) from `internal/health/calculator.go` | Gains `status` (one display vocabulary), `usable` and ordered `actions[]`. `level` keeps its current meaning for badge severity (research D4) |
| Tool approval records with captured `current_description`/`current_schema` and previous versions for diffs (`storage.ToolApprovalRecord`) | Gain captured annotations (not part of the approval hash). A review endpoint composes them with tier and scan verdict |
| `POST /servers/{id}/security/approve` (scan-gated) and `/security/reject` | Become the only first-party "Approve server" and "Reject server" path on every surface |
| Connect registry (`internal/connect/clients.go`), `GET /connect`, preview/undo | Gains `display_path` and `reload_hint`; its UI becomes one shared `ClientConnectList`. From 109-h (FR-030a) or Spec 108-c (its FR-025a: they carry `credential_state`), whichever merges first, the three reads (`GET /connect`, `/connect/{client}`, `/connect/{client}/preview`) are administrator-only, the same rule as this spec's `GET /clients`; every consumer here (Web UI, tray, CLI) already uses the API key or socket |
| Sessions store with `client_name` per MCP session; `GET /sessions` | Feeds the Clients page ("last seen", active sessions) and the Activity "Sessions" view |
| Registry search per registry (`internal/registries/search.go`), Spec 070 add-from-registry | Fan-out search across all registries with one ranking function, exposed as the Catalog |
| Import preview `POST /servers/import/json?preview=true` (`internal/configimport`) | Also detects a bare URL or a command line; env/header values can be stored as keyring secrets |
| Usage aggregate (`/activity/usage`) and token savings (`ServerTokenMetrics`) | Adds a catalog-based estimate before the first call |
| Tray "Needs Attention" group, macOS Dashboard sections | Read the shared attention endpoint instead of local predicates |

**Editions.** All UI changes target the personal edition's navigation. The server edition keeps its `/my/*` and `/admin/*` navigation (unchanged, out of scope). REST additions are registered in both editions and require an authenticated caller. Administrators (API key, socket, server-edition `admin_user`) get full reads. Every non-admin caller (`auth.IsScopedCaller`: agent tokens in both editions and plain server-edition OAuth user sessions) gets filtered reads, keyed on `!IsAdmin()` and never on `Type == AuthTypeAgent` (contracts/rest-api.md, caller classes): `/attention`, `/review`: only servers passing `CanEnumerateServer`, no client items; `/catalog/search`: `added` computed only over visible servers; SSE `attention.changed` and `review.changed` narrowed the same way (FR-006). The existing `requireServerOp` gates mutations. `/api/v1/clients` is personal-only (same rule as Spec 108 D22) and administrator-only: a scoped caller gets `403` (FR-030). The Settings "Server Edition" tab is shown only when `/api/v1/status` reports `edition: server`.

## Coordination with Spec 108

Spec 108 deferred the navigation-wide items (its D16 and "Out of Scope": M1 remainder, M2, M3, M4–M7). This spec takes them over. **Ownership rule** (maintainer's lead agent, binding, supersedes the earlier "first to merge creates" rule — research D1, D25): every shared artifact has exactly **one owning spec and PR**; the other spec only integrates through the named extension point and never redefines it. Tasks that were duplicated across the specs were merged into the owner: Spec 108's former scope-filter PR (`useScopeQuery`, link map, macOS `ScopeFilter` channel) into **109-k**, and its Clients page shell into **109-h**; Spec 108 references them.

**Spec 109 owns**: the URL filter contract and `useScopeQuery()` with **every** parameter, including `profile`, `client` and `token` — registered in 109-k, wired but hidden until Spec 108's backend reports them available (`GET /api/v1/status` `features.scope_filters`, 108-e); the deep-link map, including the rows that open Spec 108 pages; the macOS `ScopeFilter`; the Clients page shell, presence rows, `GET /clients`/`GET /clients/{id}`, CLI `client list|show` and the shared `ClientConnectList`/`ImportServers` components; sidebar and header layout (incl. the Viewing-chip slot and the Profiles sidebar entry); Needs-attention; the health vocabulary; the review queue; the catalog.

**Spec 108 owns**: the profile policy model and enforcement; per-client credentials and bindings; the `profile`/`client`/`token` fields and their backend filters on activity, sessions, usage, tools, servers, clients and tokens endpoints; the Profiles page and editor; the profile chip + lock (and credential state, rotate/forget, custom client, bulk move, binding warnings banner) on Clients rows; the token-dialog profile picker; the access explainer; the Viewing chip; profile CLI/MCP/REST; macOS profile views.

| Artifact | Spec 109 owns | Spec 108 adds |
|---|---|---|
| Clients page (`Clients.vue`, macOS `ClientsView.swift`) | **Owner.** Page, tabs (Clients · Endpoint & mode · Agent tokens), presence rows (installed, connected, last seen, sessions, reload hint), the row links Activity · Sessions · Tools it sees · Usage (hidden until `scope_filters`), `ClientConnectList`, and the row/column/banner extension slots (109-h) | Mounts its components in the slots: profile chip, lock, credential state, rotate/forget, custom client, bulk move, admin-key upgrade, binding warnings banner (108-i/108-k); never creates the page |
| Connect flow (`ConnectModal.vue` → `ClientConnectList.vue`; macOS `ConnectClientView`) | **Owner.** `ClientConnectList.vue` replaces `ConnectModal.vue` (109-h): per-client diff, backup notice, reload hint, combined bulk diff. macOS keeps `ConnectClientView` as the one connect view | Profile picker, mode, masked client credential and the "can no longer manage servers" notice (108-c T042, 108-i T098) as fields inside the owner's component: `ConnectModal.vue` while 109-h has not merged (108-c ships the admin-key fix early), `ClientConnectList.vue` after; 109-h carries over any 108 fields already in `ConnectModal.vue` |
| `GET /api/v1/clients`, `GET /clients/{id}` | **Owner** of the routes and the row shape (presence fields, `kind: supported\|other`), personal edition only (109-h) | Decorates rows additively (`credential_state`, binding fields, `kind` value `custom`, `warnings[]`) and adds the `profile`/`client` filters (108-f); owns its own mutating routes (`PUT /clients/{id}/binding`, rotate, forget, custom add, bulk) |
| `mcpproxy client list|show` | **Owner** of the commands and presence columns incl. `CONFIG PATH` (109-h) | Appends binding columns and `--profile`; adds `set-profile|lock|unlock|add|forget|rotate|upgrade-admin-key-holders` (108-g) |
| `useScopeQuery()`, URL filter contract, link map, macOS `ScopeFilter` | **Owner** of the composable, the full parameter table (`server tool session status from to view type tab q tier approval auth_type change source focus` **and** `profile client token`, the last three sticky and hidden until `features.scope_filters`), REST mapping, sticky rules, the link map (incl. client-row, profile-card, token-row, blocked-row and view-as rows that open 108 pages) and the macOS `ScopeFilter` (109-k) | Supplies the backend filters and the `features.scope_filters` signal (108-e) and the target pages; its Viewing chip reads and writes the composable; never redefines parameters or links |
| Header layout | **Owner.** Search, status pill, attention pill, "+ Add ▾" (Profile item shown once the 108 route exists), responsive rules, reserved slot for the Viewing chip; interim H2 hiding of the switcher (109-a, 109-i) | Removes `ProfileSwitcher` and fills the slot with its Viewing chip (108-i, which depends on 109-i) |
| Sidebar | **Owner.** Home · Connect · Protect · Monitor regrouping (109-i), including the **Profiles** entry in Connect, shown once the `/profiles` route exists (supersedes 108 D16's "Workspace" placement) | Registers the `/profiles` routes (108-i); no sidebar edit |
| Needs attention | **Owner.** Endpoint, item kinds, Home list, pills, badges, tray, CLI (109-d); 109-l adds the kinds built from 108's warnings | Emits the warnings (same names on both specs): `anonymous_denied_by_binding_guard`, `client_holds_admin_key`, `client_credential_expiring`, `client_rotation_pending`, `profile_missing`, `client_token_name_conflict` |
| Activity page | **Owner.** Views (Tool calls · Sessions · System events · All), system-event folding, empty-column hiding, tab/URL sync, and the `profile/client/token` filter controls (hidden until `scope_filters`) (109-k) | Backend filters and fields (108-e); attribution chips, Sessions-view columns and blocked-row "Allow in profile…"/"Why?" actions (108-j) |
| Agent tokens | **Owner** of the placement: moves under Clients (`/clients?tab=tokens`, `/tokens` redirect keeps query) (109-h) | Dialog: Profile + Expiry, legacy scope (108-i) |
| `/sessions` | **Owner.** Becomes a redirect to `/activity?view=sessions` that keeps the query, so 108 acceptance check 6 (`/sessions?client=cursor`) still loads filtered (109-k) | Session fields `client_id`, `profile`, `profile_source` (108-e) and their columns (108-j) |
| CLI `--from/--to` time aliases on `activity list|watch|summary|export` | **Owner** (109-k) | Uses them; removed from its own scope |

## Definitions *(binding)*

- **Needs-attention item**: a condition that stops an agent from using something the user set up and needs a person to act on it. It has a `kind`, a `subject` (server, tool, or client), a one-line `summary`, and exactly one `fix` (a verb plus a target). The **attention count** is the number of items. It is the one number every surface shows for "what needs me".
- **Health status**: the one display vocabulary for a server's state: `ready`, `connecting`, `sign_in_required`, `needs_review`, `needs_secret`, `needs_config`, `error`, `disabled`. Every surface renders it with the labels in [contracts/health-vocabulary.md](contracts/health-vocabulary.md). `usable` is true only for `ready`.
- **Primary action**: `health.actions[0]`, the single next step for a server. It is shown as the only button on a server card or row. Everything else goes into the overflow menu.
- **Captured tool definition**: the name, description, input and output schema and annotations MCPProxy recorded for a tool, whether or not the server is approved. It is untrusted content from the upstream.
- **Review item**: a server awaiting review, counted once per server: a quarantined server (`server_review`), or a trusted server with at least one tool whose approval state is `pending` ("New, needs review") or `changed` ("Changed, needs review") (`tool_review`). The **review count** is the number of review items, i.e. `GET /api/v1/review` `count` (one row per server), and is the only number the Review queue badge shows on every surface. Per-tool numbers (`pending`, `changed`, tools captured) are shown on each row, never as the badge. The review count is not the attention count: attention emits one `tool_review` item per server **per state** (ranks 60/61), so a server with both pending and changed tools is two attention items and one review item.
- **Approve server** / **Reject server**: the scan-gated server operation (`security/approve`, `security/reject`). **Approve tool** / **Reject tool**: `tools/approve` and `tools/block` (block = acknowledge + disable). These are the only four review verbs on any surface.
- **Catalog**: the searchable list of MCP servers that can be added, merged from every configured **catalog source** (a registry).
- **Client presence**: what MCPProxy knows about a client without credentials: installed (config found), connected (MCPProxy entry present in its config — on the list, inferred from MCPProxy's own connect write or from a session seen **after** the last disconnect MCPProxy performed for that client; FR-030), last seen (last MCP session whose reported `clientInfo.name` maps to it), active sessions. The identity and profile binding come from Spec 108.
- **Scope-aware page**: a Web UI page that reads and writes the URL filter contract through `useScopeQuery()` ([contracts/url-filter-contract.md](contracts/url-filter-contract.md)).
- **Surface**: Web UI, macOS app (window and tray), CLI, MCP built-in tools. REST is the substrate the first three use and is specified with them.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — "What needs me?" has one answer and one number everywhere (Priority: P1)

A user opens MCPProxy (Web UI, macOS app or CLI) and sees one ordered list of what blocks their agents, each item with its fix. The same count appears in the header pill, the Home sidebar badge, the tray, and `mcpproxy status`.

**Why this priority**: Fixes N6, N8, A3, and inventory contradiction X1. This is the single most-requested orientation aid, and every other story adds items to it.

**Independent Test**: A fixture instance with one OAuth server that needs sign-in and is quarantined, one server with a missing secret, one non-quarantined server with a changed tool, one disabled server, and one client configured by connect but never seen. Assert `GET /api/v1/attention`, the Web UI Home list, header pill and sidebar badge, the macOS tray group and Home section, and `mcpproxy attention -o json` all return the same five items in the same order. The disabled server must not be listed. (The `client_never_seen` item is computed by 109-d's `Compute` and fed live from 109-h's client presence, so the full list is live-verified from 109-h on; FR-002.)

**Acceptance Scenarios**:

1. **Given** the fixture above, **When** the user opens `/` in the Web UI, **Then** Home shows "Needs attention · 5" first, with items ordered by the rank in [contracts/rest-api.md](contracts/rest-api.md#attention) (sign-in before review for the same server), each with one fix button (Sign in, Review, Add secret, Review change, How to restart). The topology and a usage summary strip appear below the list.
2. **Given** the same state, **Then** the header attention pill reads `⚠ 5`, the sidebar Home badge reads `5`, the macOS tray "Needs Attention (5)" group lists the same items, and `mcpproxy status` prints `Needs attention: 5 (run 'mcpproxy attention')`.
3. **Given** the user signs in to the OAuth server, **When** the server reconnects, **Then** within 1 s every open surface drops the sign-in item (SSE `attention.changed`), and the review item stays and now reads "github: waiting for review".
4. **Given** no items, **Then** Home reads "All clear", the pill and badge are hidden, the usage strip moves up, and `mcpproxy attention` prints `All clear` and exits 0.
5. **Given** `mcpproxy doctor`, **Then** its first section is the same attention list and count. Its diagnostics follow under the heading "Diagnostics" and are never called "issues that need attention" (X1).

---

### User Story 2 — Review what you approve, the same way on every surface (Priority: P1)

A user reviewing a quarantined server or a changed tool sees each captured tool's name, description, annotations, risk tier and scan verdict before approving. They can approve or reject the server and each tool. For a changed tool they see a before/after diff. Every surface uses the same four verbs with the same scan gate.

**Why this priority**: Fixes S2, N6, T1 and contradictions X2, X3 and X7. Approving without seeing the tools undermines quarantine, and the macOS path currently bypasses the scan gate.

**Independent Test**: A fixture with a quarantined stdio server (14 tools, 2 destructive, 1 unannotated) whose definitions were captured, and a trusted server with one `changed` tool. Assert that `GET /api/v1/servers/{id}/review`, the Web UI review screen, the macOS review sheet, `mcpproxy review show <server> -o json`, and MCP `quarantine_security inspect_quarantined` return the same tool set with the same `tier`, `annotations` and `scan_verdict` values. Then approve through each surface in turn on fresh fixtures and assert the same resulting state.

**Acceptance Scenarios**:

1. **Given** a quarantined server with captured definitions, **When** the user opens its review screen (from Home, Review queue, the server card, or `/servers/<name>?tab=review`), **Then** every tool is listed read-only with name, description (rendered as inert text and labelled "from the server, not verified"), tier (`read`, `write`, `destructive`, `unannotated`, or `unknown` for a tool whose definition was captured before this spec stored annotations, FR-021/FR-028), annotations and per-tool scan verdict, plus the server's baseline scan verdict and risk score. Nothing is callable by agents.
2. **Given** the review screen, **When** the user unchecks two tools and presses "Approve server (12 tools)", **Then** the scan-gated server approval runs (a dangerous verdict requires the existing force confirmation), the server is unquarantined, the two unchecked tools are blocked (approved and disabled), and one activity record per state change is written. The same outcome results from the macOS sheet and from `mcpproxy review approve <server> --except a,b`.
3. **Given** the macOS app, **When** the user presses "Approve Server", **Then** it calls `security/approve` (never `unquarantine`), shows the same dangerous-verdict confirmation as the Web UI, and results in the same integrity baseline (X2).
4. **Given** a trusted server with a `changed` tool, **When** the user opens the Review queue, **Then** the item shows a description and schema diff (previous → current) with Approve and Reject. Reject blocks the tool (approved and disabled) and never deletes it.
5. **Given** a quarantined server whose definitions were never captured, **Then** the review screen says "Tool definitions not captured yet" and offers "Fetch tool definitions" (runs the offline baseline scan under the existing inspection exemption). Approve stays available, labelled "Approve without seeing tools", behind a confirmation.
6. **Given** the Tools page, **Then** the approval filter offers exactly "Approved", "New, needs review" and "Changed, needs review", and the "Needs review" stat links to `/review`. The CLI `tools list --approval` help and the macOS Tools view use the same labels for `approved|pending|changed` (X7).

---

### User Story 3 — Clients have a home: connect later, see who is talking, find tokens (Priority: P1)

A week after setup, a user installs Cursor and connects it from **Clients** in the sidebar. The Clients page lists every supported client with its state (connected, last seen 2 min ago, not installed, connected but never seen), a copy-paste snippet for any other client, the endpoint and routing mode, and the agent tokens. The same client list component is used by the setup wizard and by the "+ Add ▾ → Client" menu.

**Why this priority**: Fixes N1, N2, N4, N5 and H3. This is the maintainer's flagged scenario, and Spec 108 bindings hang off this page.

**Independent Test**: Sandboxed HOME with Claude Code (connected, seen), Cursor (installed, not connected) and Codex (connected, never seen). Open `/clients`, the wizard's Clients step, and "+ Add ▾ → Client". Assert all three render the same `ClientConnectList` rows with the same states. Connect Cursor from the Clients page, verify the diff preview and backup notice come first, and verify the reload hint afterwards.

**Acceptance Scenarios**:

1. **Given** the fixture, **When** the user opens `/clients`, **Then** rows show Claude Code "connected · last seen 2 min ago · 1 active session", Cursor "installed · not connected" with Connect, Codex "connected · never seen · restart Codex to load MCPProxy", and a final "Other client" row with a copy-paste snippet for the `/mcp` endpoint.
2. **Given** the wizard's Clients step, the Clients page, and the "+ Add ▾ → Client" dialog, **Then** all three are the same component with the same per-client diff preview and backup notice. There is no bulk "Connect all" that writes without showing the combined diff first.
3. **Given** the Clients page tabs, **Then** "Endpoint & mode" shows the MCP endpoints with copy buttons and the routing mode selector with its restart note (moved from the header), and "Agent tokens" lists and creates agent tokens (moved from under Secrets). `/tokens` and `/tokens?…` redirect to `/clients?tab=tokens&…`.
4. **Given** a successful connect on any surface (Web, macOS, `mcpproxy connect cursor`), **Then** the result shows the client's reload hint (for example "Reload the Cursor window (or restart Cursor) to load MCPProxy") and the config path in `~/`-short form, with the full path in a tooltip or `-o json`.
5. **Given** the macOS app, **Then** a "Clients" sidebar item shows the same rows, the Connect flow and the Endpoint & Mode section, and Agent Tokens appears as a Clients tab (no longer a separate sidebar item). `mcpproxy client list` prints the same rows.

---

### User Story 4 — Each server says one thing and offers one next step (Priority: P1)

The Servers list shows, per server, one status line in the shared vocabulary and at most one primary button (Sign in, Review, Add secret, Fix config, Restart). Enable/disable, scan, restart, logs, edit, trust mode and delete are in a ⋯ menu. No surface calls an unusable server "healthy". Detail tabs update the URL.

**Why this priority**: Fixes S4, S5 and S6 and contradiction X5. The server list is where new users get stuck longest.

**Independent Test**: A table-driven fixture covering every `health` input combination (enabled/disabled, quarantined, OAuth required, missing secret, config error, connecting, connected, error). Assert the calculator's `status`, `usable` and `actions`. Then render each fixture through the Web UI card, the macOS row, the tray menu row, and `mcpproxy upstream list`, and assert the same label and the same primary action.

**Acceptance Scenarios**:

1. **Given** a server that is quarantined and needs OAuth sign-in, **Then** `health.actions = ["login","approve"]`, `status = sign_in_required`, `usable = false`. The card shows "Sign in to GitHub to load tools" with one "Sign in" button and a secondary line "Review after sign-in". Every surface labels it the same way.
2. **Given** a healthy connected server, **Then** the card shows "Online · 14 tools · scan clean", no primary button, and a second line "last call 2 min ago · 1 error today" that links to `/activity?server=<name>&status=error&from=-24h`.
3. **Given** a disabled or quarantined server, **Then** no surface renders the word "healthy" for it. The Configuration tab's Health row shows the status label and detail, never the raw `level`.
4. **Given** the ⋯ menu, **When** the user picks Delete, **Then** a confirmation names the server and what is removed. Delete is never a standalone red button on the card. Trust mode is a shield icon with a tooltip and is changed from the menu.
5. **Given** `/servers/github?tab=security`, **When** the user switches to the Configuration tab, **Then** the URL becomes `?tab=config` via `router.replace`, other query parameters are kept, and Back returns to the previous page, not to the previous tab.
6. **Given** server cards in different states, **Then** every card in the grid has the same height (the layout does not jump).

---

### User Story 5 — Add a server the way people find one (Priority: P2)

A user clicks "+ Add ▾ → Server" (or Servers → Add). The flow opens on a catalog search across all catalog sources, ranked official and verified first, with Popular and Official sections before any query. Next to it, one field accepts a pasted JSON/TOML snippet, a URL or a command line. Each env var and header can be stored as a keyring secret with one toggle.

**Why this priority**: Fixes N3, C1, C2, S7 and contradiction X4. It is not blocking (adding works today with effort), but it is the most common growth path.

**Independent Test**: Registry fixtures (the official registry plus a Smithery-style registry containing `ai.smithery/*github*` forks). Search "github" through the Web UI, macOS, `mcpproxy catalog search github -o json`, and MCP `search_servers` without `registry`. Assert the official GitHub server ranks first on every surface with identical order. Paste `npx -y @modelcontextprotocol/server-filesystem /tmp`, `https://api.githubcopilot.com/mcp/`, and a JSON snippet with `GITHUB_TOKEN`. Assert the detected transport, and assert that the token is stored as `${keyring:…}` when "Secret" is on.

**Acceptance Scenarios**:

1. **Given** no query, **When** the Catalog opens, **Then** it shows "Official" and "Popular" sections from all sources, each result with its human title (id as secondary text), publisher, verified badge, and stars or installs when the source provides them.
2. **Given** the query "github", **Then** results from every source are merged and ranked by [contracts/rest-api.md](contracts/rest-api.md#catalog) (official source, then verified publisher, then popularity, then text relevance, then title). Unreachable sources are listed as "unavailable" without failing the search (Spec 070 FR-008).
3. **Given** a result, **Then** its button reads "Add to MCPProxy". After adding, it reads "Added ✓ · Open", and the server lands quarantined (Spec 070 FR-009). The CLI prints "Added <name> to MCPProxy (quarantined for review)", and macOS uses the same labels.
4. **Given** the paste field, **When** the user pastes a URL, a command line, or a JSON/TOML snippet, **Then** the transport is detected and a preview form is filled. Nothing is added until the user presses Add, and a pasted command is never executed during preview.
5. **Given** an env var named like a secret (`*_TOKEN`, `*_KEY`, `*SECRET*`, `*PASSWORD*`), **Then** its row defaults to "Secret". On Add the value goes to the OS keyring and the config stores `${keyring:<server>-env-<var>}` (FR-065 naming; a header of the same name gets its own `<server>-header-<name>` entry, and an existing keyring entry is never overwritten). The value is never written to `mcp_config.json` or echoed back.
6. **Given** the sidebar, **Then** there is no "Repositories" item. `/repositories` redirects to `/add-server?tab=catalog`, catalog sources are managed in Settings → Catalog sources, macOS has no "Registries" sidebar item (Catalog lives in its Add Server sheet), and the CLI's `catalog` group searches while `registry` manages sources.

---

### User Story 6 — Navigate, search and filter the same way everywhere (Priority: P2)

A user finds every area from a sidebar organised as Home · Connect · Protect · Monitor, uses one search box (⌘K) for servers, tools, settings and pages, sees one compact status pill, and uses "+ Add ▾" for anything new. Every grid keeps its filters in the URL, so a copied link reproduces the view, and pages link to each other with filters already set. The header stays usable in a half-screen window.

**Why this priority**: Fixes N7, N8, H1–H4, A1, A2 and the navigation half of X6. Everything here is reachable today, but slowly.

**Independent Test**: Playwright on the smoke binary at widths 1440, 1100, 900 and 390. Assert the sidebar groups and labels, header controls and overflow behaviour, the modal stacking order at the sidebar version row, URL round-trips for every scope-aware page (load URL → state; change state → URL), the redirect table, and the deep links from Home, server cards, Usage chart bars and Tools rows.

**Acceptance Scenarios**:

1. **Given** the personal edition, **Then** the sidebar reads: Home (badge = attention count); **Connect**: Clients, Profiles (once Spec 108 ships), Servers, Tools; **Protect**: Review queue (badge = review count), Secrets; **Monitor**: Activity, Usage; footer: Settings, Docs, Feedback, Theme, version row. The Setup entry stays pinned at the top while onboarding is incomplete. The macOS sidebar has the same groups and names, with the omissions and reasons listed in the parity matrix.
2. **Given** the header at ≥ 1100 px, **Then** it shows: ⌘K search · [Viewing chip slot] · status pill ("● 1 of 2 online · 14 tools · Retrieve") · attention pill · "+ Add ▾" (Server, Client, Token, and Profile once Spec 108 ships). **Given** 900 px or 390 px, **Then** the search collapses to an icon that opens the palette, the status and attention pills collapse to icon + count (`● 1/2`, `⚠ 3`; the attention pill stays hidden at 0), "+ Add ▾" collapses to `+`, and nothing is clipped or scrolls horizontally.
3. **Given** ⌘K (Ctrl+K on Windows/Linux) or a click on search, **Then** a palette opens that searches pages, servers, tools (`/api/v1/index/search`), settings fields and actions. Enter on free text opens `/tools?q=<text>`. There is no separate Search button.
4. **Given** the Add Server modal is open, **Then** the sidebar version row ("⟳ Check") is under the modal backdrop: `elementFromPoint` at its coordinates returns the modal backdrop or the dialog (H4).
5. **Given** the settings page, **Then** it is called "Settings" in the sidebar, route title, page heading and document title. Tabs use line icons, not emoji, and the "Server Edition" tab is absent in the personal edition.
6. **Given** `/activity` on a fresh instance after approving a 14-tool server, **Then** the default view is "Tool calls". Switching to "System events" shows one folded row "filesystem: 14 tools approved" that expands, columns empty for every visible row (Sensitive, Intent) are hidden, and Duration is not clipped.
7. **Given** Usage or the Home usage strip on an instance with 14 indexed tools and no calls yet, **Then** the savings tile reads "14 tools ≈ N tokens kept out of every request (estimate)", count charts use integer ticks, and the "unresolved" label reads "Calls to unknown tools".
8. **Given** any scope-aware page (Tools, Activity, Usage, Servers, Clients, Review), **When** the page is loaded from a URL with contract parameters, **Then** it applies them before its first fetch, shows each as a removable chip, writes changes back with `router.replace`, and a copied URL reproduces the view, including its tab.
9. **Given** a build where `GET /api/v1/status` does not list `features.scope_filters` (109-k merged, Spec 108-e not yet), **When** a script calls `GET /api/v1/activity?profile=work-readonly&client=cursor`, **Then** it gets `400` with `code: "unsupported_scope_filter"` naming the first unsupported parameter, never the unfiltered record set; `?agent=<name>` keeps working on `GET /activity` and `/activity/export` (which honour it today), while `GET /activity/usage?agent=<name>` gets the same `400` naming `agent` instead of silently unfiltered totals; the Web UI and macOS, which do not send hidden parameters, never see the error (FR-080a). **Given** the Sessions view on Web and macOS once the feature is listed, **When** `/activity?view=sessions&client=cursor` is opened on either, **Then** both send `client=cursor` to `GET /sessions` and neither sends `from`/`to`, `server`, `tool`, `status`, `type` or `auth_type` (url-filter-contract `view` → REST table).

---

### User Story 7 — First run ends in something that works (Priority: P2)

A first-time user connects a client and imports servers in the wizard. Import rows show what each server runs. Client rows show short paths. The Servers step completes only when at least one server is usable. Verify tells them to restart their client, and its suggested prompts use a tool that actually works. The telemetry notice does not compete with the wizard.

**Why this priority**: Fixes O2–O6. First impressions matter, but the wizard already works at a basic level.

**Independent Test**: Sandboxed HOME with Claude Code holding two servers (GitHub remote OAuth, filesystem via npx). Walk the wizard in the in-app browser. Assert the row contents, completion states, the Verify hint and prompts, and the banner timing.

**Acceptance Scenarios**:

1. **Given** the Servers step, **Then** each import row shows a second line with the command or URL (`npx -y @modelcontextprotocol/server-filesystem /tmp`, `https://api.githubcopilot.com/mcp/ · OAuth`) and tags `local process`, `remote`, `needs secret`.
2. **Given** the Clients step, **Then** paths render as `~/.claude.json` (full path in a tooltip), and rows carry client icons.
3. **Given** "Import 2 servers" with "Quarantine imported servers for review" checked (default), **Then** the footer has one primary button and Back. Global Docker and quarantine settings appear as a one-line summary linking to Settings, not as controls in the step.
4. **Given** both imported servers are quarantined, **Then** the Servers step is not marked complete. It shows the inline review list (US2 component) with "Approve a server to finish this step". It completes once `has_usable_server` is true.
5. **Given** the Verify step, **Then** it shows the reload hint for each connected client, and suggested prompts reference a tool of a usable server. With no usable server it shows "Approve a server first" instead of prompts.
6. **Given** first load, **Then** the telemetry notice is not rendered behind the wizard backdrop. It appears as one line in the wizard's final step, or as the banner after the wizard closes, and it is dismissed once for all pages.

### Edge Cases

- **A server is both quarantined and needs OAuth sign-in**: two attention items (sign-in first; the review item reads "review after sign-in"). `actions = ["login","approve"]`, and the card primary is Sign in (consistent with the S1 fix).
- **Tool definitions captured before this spec (no annotations stored)**: tier shows `unknown` with "Fetch tool definitions" to refresh. It is never shown as `read`.
- **Descriptions that contain markup, links or prompt-injection text**: rendered as inert text with no Markdown, no HTML and no auto-linking, truncated at 2,000 characters with "Show all". MCP inspect ops keep their existing untrusted-content framing.
- **Attention flapping (a server connecting)**: `connecting` is not an item until it has lasted 60 s. Items are recomputed on events and debounced (250 ms).
- **Client never seen, but the user connected it seconds ago**: `client_never_seen` appears only 5 minutes after the connect write, and its fix is the reload hint.
- **Unknown `clientInfo.name`** (a client not in the registry): it appears on Clients as "Other: <name>" with its sessions. It is never mapped to a supported client by guesswork. Aliases are an explicit list per client. The name is untrusted wire input and is rendered inertly everywhere (research D19: escaped, control/ANSI sequences stripped, capped at 64 characters).
- **Catalog source timeout**: that source is marked unavailable for that query and is not retried within the request. Ranking uses the sources that answered.
- **Pasted snippet with several servers**: the preview lists each one. Add adds only the checked ones, each quarantined.
- **Secret toggle when the keyring is unavailable** (headless Linux): the toggle is disabled with the reason, and the value stays a plain env var only after an explicit confirmation.
- **Old bookmarks**: `/overview`, `/repositories`, `/security`, `/sessions`, `/tokens` redirect, and the query is kept (navigation-map contract).
- **Server edition**: the personal sidebar regrouping does not apply to `/my/*` and `/admin/*`. Attention and review endpoints follow the same caller classes as in the personal edition (FR-007): administrators (`admin_user`, API key) see everything, and a scoped caller (agent token or non-admin OAuth user session) gets the filtered view of the servers it can enumerate, so a user session with no `allowed_servers` sees an empty list. `/clients` is not registered (404).
- **Scoped REST callers** (agent tokens on REST in both editions, and non-admin OAuth user sessions in the server edition; FR-007): attention and review items are filtered to the token's `allowed_servers`, client items are omitted, and mutations follow the existing `requireServerOp` gates.
- **Spec 108 not yet merged**: every 109 PR except 109-l ships and works without 108. The `profile`, `client` and `token` parameters and the link-map rows that need them are registered by 109-k but stay hidden (no control, no chip, not sent to REST, kept untouched in the URL) until `GET /api/v1/status` lists them in `features.scope_filters`; the Profiles sidebar entry and "+ Add → Profile" stay hidden until the `/profiles` route exists.

## Requirements *(mandatory)*

### Functional Requirements

**A. Needs attention (M2, A3, N6, N8, X1)**

- **FR-001**: The core MUST compute one needs-attention list with one function (`internal/runtime/attention.go`) from in-memory state: server health, tool approval counts, connect status, session history. It MUST serve the list at `GET /api/v1/attention` with the kinds, ranks, summaries and fixes defined in [contracts/rest-api.md](contracts/rest-api.md#attention), and emit SSE `attention.changed` `{count, ids}` when the item set changes.
- **FR-002**: The item kinds shipped by this spec are `sign_in_required`, `server_review`, `tool_review`, `server_error`, `missing_secret`, `config_error` and `client_never_seen`. Disabled servers, servers connecting for under 60 s, and update availability are never items. A quarantined server is only ever a `server_review` item (plus `sign_in_required` when it needs OAuth), never `server_error`: restarting a server before approval fixes nothing, and its review screen shows the transport error. 109-d implements every kind in `Compute`, including the `client_never_seen` rule over a minimal client input type it defines; 109-h, which owns client presence and the `/clients?focus=` target, feeds that input from `ClientPresence` (data-model.md §4, §6). Kinds from Spec 108 warnings are added in 109-l (see Coordination). **Time-based conditions** (`connecting` → `server_error` after 60 s, `server_error` persisting 60 s, `client_never_seen` after 5 min) MUST appear at their threshold even when no event fires: besides the event-triggered debounced recompute, the subscriber arms a timer for the **earliest pending threshold crossing** (recomputed on every recompute, capped at 30 s so a clock jump cannot stall it), so `GET /attention` never under-reports on a quiet instance (research D2).
- **FR-003**: Every surface MUST take its attention list and count from FR-001 only: the Web UI Home list, header pill and Home sidebar badge; the macOS tray "Needs Attention" group, Home section and sidebar badge; CLI `mcpproxy attention` and the first line of `mcpproxy status`. Each surface's local predicate (`Dashboard.vue` `serversNeedingAttention`, `AppState.serversNeedingAttention`) MUST be removed or reduced to reading the endpoint.
- **FR-004**: `mcpproxy doctor` MUST print the attention list first, then its diagnostics under "Diagnostics". The phrase "need attention" MUST refer only to the FR-001 count on every surface.
- **FR-005**: Each item's fix MUST open the exact fix screen or run the fix: Sign in → OAuth login; Review → review screen; Add secret → the server's secret form; Fix config → the server's Configuration tab with the field focused; Restart/logs → the server menu action; client reload → the client's reload hint. Quarantine review is never a one-click approve from a list (the tray rule `TrayPresentation.swift:254-265` becomes the rule for every surface). A fix target whose screen ships in a later PR resolves to an interim screen until then, never to a dead link ([contracts/rest-api.md](contracts/rest-api.md#attention), "Interim fix targets").
- **FR-006**: SSE `attention.changed` and `review.changed` MUST be filtered per subscriber with the same rule as `GET /attention` and `GET /review`. `attention.changed` is rendered from its structured item list: a scoped caller receives only ids whose subject is a server it can see, no client ids, a `count` recomputed from that narrowed list, and no frame when its narrowed set has not changed. It MUST be classified in `internal/httpapi/sse_scope.go` as a per-subscriber-rendered type (like `servers.changed`), not as a type without server identity, because the existing classifier reads only scalar fields (`server_name`, `server`, `target_server`, `affected_entity`) and would pass the `ids` array unfiltered. `review.changed` (scalar `server`) MUST be added to `identityBearingEventTypes` so a frame with an empty name is dropped for scoped callers.
- **FR-007**: Every new REST read route (`/attention`, `/review`, `/servers/{id}/review`, `/clients`, `/catalog/search`) MUST classify its caller with `auth.IsScopedCaller` (`!IsAdmin()`), the predicate `requireAdminRead` and `CanEnumerateServer` share, and MUST NOT branch on `Type == AuthTypeAgent`. Scoped callers (agent tokens, plain server-edition OAuth user sessions) get the route's filtered view or `403`, as contracts/rest-api.md states per route. Each route MUST have a test with an agent token and a test with a non-admin `AuthTypeUser` context under `-tags server` (for `/clients`, which the server edition does not register, the `-tags server` test asserts `404`).

**B. Health vocabulary and server surfaces (S4, S5, S6, X5)**

- **FR-010**: The health calculator MUST add `status` (enum per [contracts/health-vocabulary.md](contracts/health-vocabulary.md)), `usable` (bool) and `actions` (ordered list) to `contracts.HealthStatus`, with the invariant `action == actions[0]` (or `""` when `actions` is empty). Existing fields keep their names and values, with one deliberate value change: a quarantined server that also needs OAuth sign-in reports `action: "login"` (was `"approve"`), because its quarantined branch now checks the same OAuth inputs the later OAuth branch uses. This matches the S1 fix, which already renders Sign in for that state, and the tray's `isOAuthLoginRequired` (`health.action == "login"`) starts offering Sign in for it too. `admin_state` stays `quarantined`.
- **FR-011**: No surface may render the `level` value as text. Every surface renders `status` through the one label table. A server with `usable=false` is never labelled "healthy", "online" or "connected".
- **FR-012**: `actions` MUST list every applicable next step in priority order: `login` > `set_secret` > `configure` > `edit_url` > `approve` > `restart` > `view_logs` > `enable`. A quarantined server that needs OAuth yields `["login","approve"]`.
- **FR-013**: The Web UI server card MUST show one status line (`status` label + detail), at most one primary button (`actions[0]`; none when `actions` is empty, which is the normal `ready` case), a second line with last call time and 24 h errors linking to Activity, and a ⋯ menu with Enable/Disable, Scan, Restart, Logs, Edit, Trust mode and Delete (with confirmation). Trust mode is a shield icon with a tooltip. Every card in a grid has the same height.
- **FR-014**: The macOS Servers view row and the tray server submenu MUST use the same primary action and the same status label. Secondary actions stay in the context menu or submenu. The tray executes only `login`, `restart` and `enable` itself (`TrayPresentation.fromHealthAction`); for every other `actions[0]` it MUST still show the same primary label, as an item that **opens the screen that performs it** — `approve` → the server's review location (review sheet on macOS; `/review/<name>` in the Web UI from the Go tray), `set_secret` → the server's secret form, `configure`/`edit_url` → the server's Configuration tab with the field focused, `view_logs` → the server's logs — never a missing item and never a one-click approve (FR-005). The mapping is one pure function (`TrayPresentation.primaryItem(for:)`) table-tested over every `actions` value.
- **FR-015**: `mcpproxy upstream list` MUST print `STATUS` as the status label and `ACTION` as `actions[0]`, and MUST accept `--status <status>` (repeatable; several flags select the **union** of their statuses, and a comma-separated value is equivalent to repeating the flag) with the enum values. JSON output includes `status`, `usable` and `actions`.
- **FR-016**: Every tabbed Web UI view (server detail, Settings, Clients, Add server, Activity views) MUST read its tab from `?tab=` (or `?view=` for Activity) on mount and write it with `router.replace` on change, keeping other query parameters.

**C. Informed review and the Review queue (S2, N6, T1, X2, X3, X7)**

- **FR-020**: Tool approval records MUST store captured annotations (`current_annotations`, `previous_annotations`) when a definition is captured. Annotations stay excluded from the approval hash (`tool_quarantine.go:26`), so no re-quarantine is caused.
- **FR-021**: `GET /api/v1/servers/{id}/review` MUST return the server's review payload: server summary (transport, command or URL — **passed through the shared secret-redaction path** `oauth.RedactServerSecretFields`/`LiveRedaction` (`internal/oauth/serverfields.go`) — the helper the `GET /servers` list and the SSE `servers.changed` payload use, but called **unconditionally**: the review composer never honours the administrator `reveal_secret_headers` opt-out that `GET /servers` does (there is no `GET /servers/{id}` read route at `638fa805a`) — before it is returned to **any** caller, so an inline secret in a URL query, header, env value or stdio command line is never echoed verbatim; `GET /review` rows and the CLI/MCP review reads use the same composer and therefore the same redaction — the review composer is the fourth door on that path, pinned by a redaction-parity test, trust mode, baseline scan verdict, risk score, and the existing catalog origin `source_registry_id`/`source_registry_provenance` when present), and per tool: name, description, input/output schema, annotations, `tier` (`read`|`write`|`destructive`|`unannotated`|`unknown`, computed from annotations; unannotated is never shown as read), `approval_status`, `scan_verdict` and `held_signals`, and for `changed` tools the previous description, schema and a diff. `GET /api/v1/review` MUST return the queue: quarantined servers and servers with pending/changed tools, with counts. Shapes: [contracts/rest-api.md](contracts/rest-api.md#review).
- **FR-022**: The four review verbs MUST be the only first-party review operations: Approve server = `POST /servers/{id}/security/approve` (scan-gated, `force` only after the dangerous-verdict confirmation), optionally with `block: [tools]` for tools unchecked on the review screen — the `block` tools MUST be written as blocked — in the existing `BlockTools` representation (`internal/runtime/tool_quarantine.go`): approval record `status=approved`, `disabled=true`; there is no `blocked` status value (`storage.ToolApprovalStatus*` is `approved|pending|changed`, and FR-027's terminology keeps exactly those three) — **in the same storage transaction that writes the baseline and before the server is unquarantined**, through one new storage method on the scanner's storage seam (`scanner.Storage.SaveIntegrityBaselineWithBlocks(baseline, blocked []ToolApprovalWrite)`, one bbolt update transaction; the seam has no tool-approval operation today), so no instant exists in which the server is callable and a `block` tool is approved; today's `ApproveServer` saves the baseline and then calls `UnquarantineServer` as a separate step (`internal/security/scanner/service.go`), which the block write MUST precede, never follow; Reject server = `POST /servers/{id}/security/reject`; Approve tool = `POST /servers/{id}/tools/approve`; Reject tool = `POST /servers/{id}/tools/block`. The macOS app MUST stop calling `POST /unquarantine` for approval (X2). The Go tray (`internal/tray`, the `mcpproxy-tray` binary built for Windows and macOS: the Windows installer's tray and the tray inside the macOS/Windows release archives, while the macOS DMG/PKG ships the Swift app instead) MUST stop unquarantining on a click in its "Security Quarantine" submenu (X12): the click opens the server's review location in the Web UI instead. The endpoint stays for API compatibility and is used by no first-party surface.
- **FR-023**: The Web UI MUST provide a Review queue page (`/review`) and a review screen (`/review/:server`, also embedded as the server detail "Review" tab) implementing FR-021/FR-022, with per-tool checkboxes, diff rendering for changed tools, and "Fetch tool definitions" when nothing is captured. `/security` redirects to `/review`. Scan history stays reachable from the review screen and `/security/scans/:jobId`. Scanner configuration moves to Settings → Security → Scanners, where the Docker toggle is shown once.
- **FR-024**: The macOS app MUST provide a "Review Queue" sidebar item and a review sheet with the same content and verbs. The existing detail-view per-tool approval is reused, and the tray's "Needs Attention" review rows open the sheet.
- **FR-025**: The CLI MUST provide `mcpproxy review list|show <server>|approve <server> [--tools a,b | --except a,b] [--force]|reject <server> [--tools a,b]` with the FR-022 semantics: `approve` on a quarantined server runs Approve server; on a trusted server it approves pending/changed tools. `upstream approve`, `tools approve|reject` (`tools_approval.go`) and `security approve|reject` remain as aliases whose help text names the `review` equivalent. Their behaviour does not change.
- **FR-026**: MCP `quarantine_security` `inspect_quarantined` and `inspect_tools` MUST return per-tool `tier`, `annotations` and `scan_verdict` with the same field names and values as FR-021. Agents still cannot approve or unquarantine a server (accepted asymmetry, see the contradiction register).
- **FR-027**: Tool review states MUST use one vocabulary: `approved` "Approved", `pending` "New, needs review", `changed` "Changed, needs review", on the Web UI Tools filter (the `awaiting` value is removed; the "Needs review" stat links to `/review`), macOS Tools and review views, CLI `--approval` help, and the review payload.
- **FR-028**: Every tool listing (`GET /tools`, `GET /servers/{id}/tools`, the review payload) MUST carry `tier` computed by one pure Go function (`contracts.AnnotationTier`: `destructiveHint` → destructive, `readOnlyHint=false` → write, `readOnlyHint=true` → read, neither → `unannotated`). The Web Tools page (column and filter relabelled "Tier"; `?risk=` accepted as an alias of `?tier=`), the macOS Tools view and CLI `tools list --tier` (`--risk` kept as an alias) MUST use it and never compute a tier locally (X11). The review payload is the one place with a fifth value: the review composer returns `unknown` when the approval record has no captured annotations (nil, a record from before this spec) and otherwise returns `AnnotationTier` of the stored annotations (a captured tool without hints is stored as `{}` and yields `unannotated`; data-model.md §2–3). That check is part of the one server-side composer, not a surface-side computation. Spec 108's `IntrinsicTier` calls the same function through an exhaustive adapter (its `profile.Tier` is an `int` compared against the cap; unknown values fail closed to destructive; Spec 108 data-model §2), so Spec 108-a merges after this PR's 109-a (edge 108-a ← 109-a, research D1, D28).

**D. Clients hub (M1, N1, N2, N4, N5, H3)**

- **FR-030**: `GET /api/v1/clients` (personal edition) MUST return one presence row per supported client plus one per unrecognised `clientInfo.name` seen in sessions: `id`, `display_name`, `kind`, icon, installed, connected, `display_path`, `config_path`, last seen, active sessions, calls in the last 24 h, `reload_hint` (field names shared with Spec 108's `ClientView`; `calls_24h` = tool calls attributed to the client in the last 24 h, from a per-client rolling counter kept in the in-memory usage aggregate — data-model §6 — never a log scan per request), and a state from `connected_seen | connected_never_seen | installed | not_installed | other`. The mapping from `clientInfo.name` to client id MUST be an explicit alias list on `ClientDef` (`client_info_names`). The routes are administrator-only: a scoped caller (agent token) gets `403`, because presence exposes local config paths and session counts for every client. Spec 108 extends the same rows (Coordination). **`connected` never requires a config-content read on the list** (codex round 3): `connect.GetAllStatus()` is content-read-free by design (Spec 075 FR-001, no macOS App-Data prompt) and always leaves `Connected=false`, so the list derives `connected` from evidence MCPProxy already holds — its own recorded connect write for that client (`client_connected_at`), or a session seen from that client (`client_last_seen`, which also yields `connected_seen`) **later than the last disconnect MCPProxy performed for it** (`client_disconnected_at`, written by the disconnect path in the same onboarding-record transaction that clears `client_connected_at`; a session recorded before that disconnect proves nothing about the current config and only feeds `last_seen`, codex round 4); only `GET /clients/{id}` — an explicit per-client read, the Spec 075 FR-002 path where a privacy prompt is acceptable — additionally calls `connect.GetStatus()` and reports a client configured by hand as connected. A hand-configured client that has neither a recorded connect write nor a session yet is listed as `installed` with `connection_unverified: true`, and its row offers "Check connection" (opens the detail read); it is never shown as `not_installed`.
- **FR-030a** (connect reads, codex round 3): the PR that adds the administrator-only `GET /clients` (109-h) MUST also make the existing `GET /connect`, `GET /connect/{client}` and `GET /connect/{client}/preview` administrator-only with the same `requireAdminRead` middleware and the Spec 108 FR-025a message (`403 {"error":"Admin credentials required to read client connection status"}`, tray/socket passthrough unchanged), because at `638fa805a` they are open to every authenticated caller (`internal/httpapi/server.go`: "Status/preview reads stay open") and return the same presence facts — config path, installed/connected — that FR-030 withholds; otherwise a scoped token denied `GET /clients` would read `GET /connect/cursor` instead. Spec 108-c adds the identical gate (its FR-025a); 109-h and 108-c have no edge, the first to merge adds it and the second keeps it (T125a here, Spec 108 T030b). Every first-party consumer (Web UI, tray, CLI) already uses the API key or socket.
- **FR-031**: The Web UI MUST provide a Clients page (`/clients`) with tabs Clients · Endpoint & mode · Agent tokens. The Clients tab lists FR-030 rows and an "Other client" row with a copy-paste snippet. Endpoint & mode holds the MCP endpoint list (from the header dropdown) and the routing-mode selector (from `ModeSwitcher`, including its restart note). Agent tokens holds the current AgentTokens view.
- **FR-032**: One `ClientConnectList` component MUST implement connect/disconnect with per-client diff preview and backup notice, and MUST be used by the wizard's Clients step, the Clients page and "+ Add ▾ → Client". `ConnectModal.vue` is removed and its entry points route to `/clients`. A bulk connect MUST show the combined diff of every file it will write before writing.
- **FR-033**: One `ImportServers` component MUST implement import from client configs, file, and paste, and MUST be used by the wizard's Servers step, Servers → Add → Import and Home's "Import from client configs".
- **FR-034**: Agent Tokens MUST move from under Secrets to the Clients "Agent tokens" tab on the Web UI (`/tokens` redirects to `/clients?tab=tokens`, keeping the query) and on macOS (the "Agent Tokens" sidebar item becomes a Clients tab).
- **FR-035**: The macOS app MUST provide a "Clients" sidebar item with the FR-030 rows, the shared Connect flow (`ConnectClientView`), an "Endpoint & Mode" section (endpoints + routing mode setting) and the Agent Tokens tab.
- **FR-036**: The CLI MUST provide `mcpproxy client list` and `mcpproxy client show <id>` printing FR-030 rows (`-o json|yaml`, `--help-json`). Spec 108 adds binding columns and the mutating subcommands.
- **FR-037**: Connect results on every surface MUST include `reload_hint` (per-client text on `ClientDef`) and render it after a successful write. Client rows MUST render `display_path` (home-shortened) with the full path available (tooltip, `show`, or JSON).

**E. Onboarding (O2–O6)**

- **FR-040**: Import candidate rows (wizard and `ImportServers`) MUST show a second line with the command + args or the URL + auth type, and tags `local process` | `remote`, plus `needs secret` when an env var or header value is empty or a placeholder. The data comes from the import preview response (`summary`, `tags`).
- **FR-041**: `GET /api/v1/onboarding/state` MUST add `has_usable_server` (≥ 1 enabled, non-quarantined, connected server with ≥ 1 approved tool; once 109-c lands, "connected" is replaced by `health.usable`). The Servers step and the sidebar Setup badge MUST use it instead of `has_configured_server` (which is kept for compatibility).
- **FR-042**: The Verify step MUST show each connected client's reload hint and MUST generate suggested prompts only from tools of usable servers, or "Approve a server first" when there are none.
- **FR-043**: The import footer MUST have one primary "Import N servers", a visible "Quarantine imported servers for review" checkbox (default on; unchecking requires a confirmation), and Back. Global Docker/quarantine controls are replaced in the step by a one-line summary linking to Settings.
- **FR-044**: The telemetry notice MUST NOT render while the wizard is open. It appears as one line in the wizard's final step or as the banner after the wizard closes, and dismissal is persisted once.

**F. Navigation and header (M7, N7, N8, H1–H4, X6)**

- **FR-050**: The personal-edition sidebar MUST follow [contracts/navigation-map.md](contracts/navigation-map.md): Home; Connect (Clients, Profiles*, Servers, Tools); Protect (Review queue, Secrets); Monitor (Activity, Usage); footer (Settings, Docs, Feedback, Theme, version). Badges: Home = attention count, Clients = live clients, Servers = total, Tools = total, Review queue = review count (one per server awaiting review, Definitions). (*Profiles appears when Spec 108's route exists.)
- **FR-051**: `/` MUST render Home: the attention list (FR-003), the topology from today's Overview panel with its actions, and a usage summary strip that moves above the topology when the list is empty. `/usage` is the full Usage page in Monitor. `/overview` redirects to `/`. 109-a ships the interim step: `/` defaults to the Overview panel.
- **FR-052**: The header MUST contain, in order: ⌘K search (one field, no separate button), the Viewing-chip slot (empty until Spec 108), a status pill (online/total servers, tools, read-only routing-mode chip; click → `/servers`), the attention pill (hidden at 0; click → popover with the first 5 items + "See all" → Home), and "+ Add ▾" (Server → `/add-server`, Client → connect dialog, Token → `/clients?tab=tokens&create=1`, Profile → Spec 108 editor once present). ModeSwitcher and the endpoint dropdown leave the header (FR-031).
- **FR-053**: Below 1100 px the header MUST collapse search to an icon button, both pills (status and attention) to icon + count, and "+ Add ▾" to `+`; at 390 px the header is one row holding exactly these collapsed controls plus the drawer toggle ([contracts/navigation-map.md](contracts/navigation-map.md)). At 900 px and 390 px nothing may be clipped and the page may not scroll horizontally (checked by `visual-a11y-sweep`).
- **FR-054**: The ⌘K palette MUST search pages, servers, tools, settings fields and actions, keyboard-first (arrows, Enter, Esc), and Enter on free text opens `/tools?q=`. Tools are queried only for a non-empty (trimmed) input — `GET /api/v1/index/search` answers an empty `q` with `400` — so the empty palette shows pages, actions and servers only (research D16).
- **FR-055**: Stacking MUST follow one z-index scale defined in one place (sidebar < header < dropdown < modal < toast). Modals render in the top layer, so no sidebar element paints over a modal (H4). This covers **every** `<dialog class="modal">` that exists when 109-a merges, named explicitly — including the three `:open`-bound dialogs in `views/Repositories.vue` (required-input, add-source and delete-registry), which live until 109-j retires the page — not only Add Server and Connect.
- **FR-056**: The settings page MUST be named "Settings" in the sidebar, route title, heading and document title. Its tabs MUST use the app's line-icon set, and the "Server Edition" tab MUST show only when `/status` reports `edition: server`.
- **FR-057**: Until Spec 108 removes it, the header ProfileSwitcher MUST be hidden when no profiles exist (H2 interim). The "Create profile" entry point is Spec 108's.
- **FR-058**: The macOS main window sidebar MUST use the same groups and names (Home, Connect: Clients · Profiles* · Servers · Tools, Protect: Review Queue · Secrets, Monitor: Activity), and its toolbar MUST offer a "+" menu with Server, Client and Token (and Profile once Spec 108 ships). "Registries" and "Agent Tokens" are no longer sidebar items (FR-034, FR-062), and "Dashboard" is renamed "Home".

**G. Catalog and adding servers (N3, C1, C2, S7, X4)**

- **FR-060**: `GET /api/v1/catalog/search?q=&source=&tag=&limit=` MUST fan out to every enabled catalog source in parallel (timeout 5 s per source, existing caches reused), merge, de-duplicate by (source, id), rank with one pure function (official source > verified publisher > popularity > text relevance > title), and return `unavailable[]` for sources that failed. An empty `q` returns `official` and `popular` sections.
- **FR-061**: Catalog results MUST carry `title`, `id`, `publisher`, `verified`, `official`, `popularity` (stars or installs when provided), `source` and the Spec 070 add fields. Every surface shows the title first and the id as secondary text.
- **FR-062**: Adding a server MUST start at `/add-server` (outside the `/servers/:serverName` path space, so no server name — `add` included — can be shadowed by a static route; no server name is reserved) with the tabs Catalog (default) · Paste · Import · Manual, selected by `?tab=catalog|paste|import|manual` (FR-016). `?source=` on that page keeps its contract meaning (a catalog source id that narrows the Catalog tab, mapped to REST `source`). `/repositories` redirects to `/add-server?tab=catalog`, and catalog-source management moves to Settings → Catalog sources. macOS: the Add Server sheet gets the same four tabs and the "Registries" sidebar item is removed.
- **FR-063**: The add action MUST be labelled "Add to MCPProxy", and after success "Added ✓ · Open", on Web and macOS. The CLI prints "Added <name> to MCPProxy (quarantined for review)".
- **FR-064**: The Paste source MUST accept JSON/TOML (existing detector), an `http(s)://` URL (→ HTTP server) or a command line (→ stdio command + args), preview through `POST /servers/import/json?preview=true` (extended with `url` and `command` formats), and add nothing until confirmed. A preview never executes anything.
- **FR-065**: Every env var and header value in Paste, Manual and Catalog-with-inputs MUST offer a Value/Secret toggle, defaulting to Secret for secret-like names. Secret stores the value in the OS keyring through the existing secrets API and writes `${keyring:<ref>}`, where `<ref>` is one name per field **kind**: the lowercase, hyphen-normalised `<server>-env-<NAME>` for an env var and `<server>-header-<Name>` for a header (runs of characters outside `[a-z0-9-]` become `-`, trimmed, ≤ 64 characters — the existing Secrets-view convention of `ServerDetail.vue` `suggestSecretName`, which today drops the kind and is switched to the same helper), so an env var and a header with the same name never share a keyring entry. A computed name that already exists in the keyring (`GET /secrets`) gets `-2`, `-3`, … appended: an add never overwrites an existing secret (the keyring `Store` and `POST /secrets` overwrite silently). One rule, pinned by a shared fixture (`internal/secret/testdata/ref_names.json`) that Go, vitest and Swift decode (codex round 4). CLI: `upstream add --secret-env NAME=VALUE` and `--secret-header 'Name: value'`. macOS Add Server sheet: the same toggle.
- **FR-066**: The CLI MUST provide `mcpproxy catalog search <q> [--source] [--tag] [--limit]`, `catalog show <source>/<id>` and `catalog add <source>/<id>` over FR-060/Spec 070. `registry list|add-source|edit|remove` manage sources. `registry search|add` remain as deprecated aliases that print the `catalog` equivalent.
- **FR-067**: MCP `search_servers` MUST make `registry` optional (omitted = all sources through FR-060) and return the FR-061 fields in the FR-060 order. `list_registries` and `upstream_servers add_from_registry` are unchanged except for wording ("catalog source").

**H. Activity and usage (A1, A2, N5)**

- **FR-070**: Activity MUST offer views Tool calls (`tool_call`, `internal_tool_call`) · Sessions · System events (every other type) · All, in the URL as `?view=calls|sessions|system|all`, defaulting to `calls` on Web and macOS. `/sessions` redirects to `/activity?view=sessions`, keeping the query.
- **FR-071**: In System events, consecutive records of the same type and server within 60 s MUST fold into one summary row ("filesystem: 14 tools approved") that expands in place. Folding is presentation only; export and CLI output are unfolded.
- **FR-072**: Activity MUST hide columns that are empty for every visible row, show the block reason on blocked rows (from record metadata today, from Spec 108's `block_reason` once present), and not clip Duration.
- **FR-073**: `ServerTokenMetrics` MUST add `estimated` (bool) and fill `average_query_result_size` from a catalog-based estimate (mean per-tool token size × the default `retrieve_tools` limit) until real calls exist. Web Usage/Home strip, macOS token savings and `mcpproxy status` show the estimate labelled "estimate".
- **FR-074**: Count charts MUST use integer ticks. The usage "unresolved" group MUST be labelled "Calls to unknown tools" on Web and macOS.
- **FR-075**: CLI `activity list|watch` MUST accept `--view calls|system|all` (default `all`, research D11), and `activity list|watch|summary|export` MUST accept `--from/--to` accepting RFC 3339 or relative `-24h`/`-7d`: on `list` and `export` they are aliases of `--start-time/--end-time`; on `watch`, which streams `/events`, they filter the streamed records by timestamp client-side (like its `--server`) and `watch` exits once `--to` has passed; on `summary`, whose endpoint takes only `period`, `--from` accepts exactly the `--period` presets (`-1h`, `-24h`, `-7d`, `-30d`, no `--to`) and any other range exits 1 with `summary supports --from -1h|-24h|-7d|-30d only` — never a silently widened window (the same rule as the Usage `window` mapping).

**I. URL filter contract and deep links**

- **FR-080**: `frontend/src/composables/useScopeQuery.ts` MUST be the only reader/writer of contract parameters on scope-aware pages, implementing [contracts/url-filter-contract.md](contracts/url-filter-contract.md): a parameter registry with REST name mapping (`session` → `work_session_id`, `from`/`to` → `start_time`/`end_time`, relative times resolved at fetch), read before first fetch, `router.replace` on change, sticky parameters carried across scope-aware pages, the `profile`, `client` and `token` parameters registered by this spec (sticky) and **hidden** until `GET /api/v1/status` lists them in `features.scope_filters` (Spec 108-e) — hidden means no control, no chip, not sent to REST, but never dropped from the URL — and unknown parameters preserved. Contradictory parameters (a URL `server` that differs from the `tool` prefix) produce **no request** and a conflict empty state with both chips marked, never a single-server request under chips implying both (contract rule 8; the CLI exits 1, macOS issues no request). Spec 108 does not register parameters; `registerScopeParam` stays as the internal registration API this spec uses.
- **FR-080a** (scope-filter version skew): a REST caller that sends `profile`, `client` or `token` to an endpoint that does not honour it MUST get `400 {error, code: "unsupported_scope_filter", param}`, never unfiltered rows. 109-k adds the gate (`internal/httpapi/scope_filters.go`) on `GET /activity`, `/activity/summary`, `/activity/usage`, `/activity/export`, `/sessions`, `/tools`, `/servers`, `/tokens` (and `GET /clients` from 109-h), keyed on one supported-list variable that also produces `GET /api/v1/status` `features.scope_filters`; the list is empty until Spec 108-e fills it, and each handler additionally passes the names it honours. The variable and the gate function `rejectUnsupportedScopeFilters` are created together by whichever of 109-k / Spec 108-e merges first, so no build parses a scope parameter without the gate. `agent` (the existing alias of `token`) is not gated on `GET /activity` and `/activity/export`, the two handlers that honour it at `638fa805a`; on `/activity/summary`, `/activity/usage` and `/sessions`, which ignore it today, it is gated exactly like `token` (codex round 4). The Web UI and macOS never trigger the gate (hidden parameters are not sent, FR-080). Contract: [contracts/url-filter-contract.md](contracts/url-filter-contract.md) "Backend gate".
- **FR-081**: Tools, Activity (all views), Usage, Servers, Clients and Review MUST be scope-aware with the parameters listed in the contract. Page-local parameters MUST NOT reuse a contract name with a different meaning, except `status`, whose meaning per page is fixed in the contract and mirrors the CLI flag of the same page.
- **FR-082**: The deep links owned by this spec MUST be implemented as listed in the contract's link map: attention items → fix screens; server card stats → Activity; Usage/Home chart bars → the calls behind the bar; Tools row → Activity for that tool and → Review when it needs review; review item → server; client row → sessions (inline); session row → Activity. Links carrying `client`/`profile`/`token` are registered in 109-k — hidden until `features.scope_filters` (and, where named, the Spec 108 route) exists — and 109-l only proves that they appear (its tasks make no link-map edit), per the Coordination table.
- **FR-083**: macOS MUST carry the same filters on Activity (type, server, tool, status, time range, view, auth type) and pass them between views through one `ScopeFilter` value on `AppState`. It generalises today's `pendingActivitySessionFilter` and already carries `profile`, `client` and `token`, hidden until `features.scope_filters` lists them (un-hidden by that data, not by code; Spec 108 never adds fields).

**J. Terminology and parity**

- **FR-090**: Every user-visible name in the [Terminology](#terminology-binding-for-every-surface) table MUST be used verbatim on every surface marked in it. Enumerations (health `status`, attention `kind`, review `tier`, approval states, Activity views, client presence states) MUST have one Go source, be generated into `frontend/src/types/contracts.ts` by `cmd/generate-types`, and be verified by golden fixtures decoded by Go, vitest and Swift tests.
- **FR-091**: Every capability in the parity matrix MUST exist on each surface marked ✓ with the same name and semantics, or carry the stated reason for being out of scope. Verified mechanically by T148b: a checked-in `parity-matrix.json` (row → per-surface implementing identifier) must match the spec's table row for row and cell for cell, every ✓ cell's identifier must resolve (route/component, Swift view/action, CLI command in `--help-json`, MCP tool/field, REST route in `oas/swagger.yaml`) and name at least one test that exercises it, and every — cell must carry a reason.
- **FR-092**: Every contradiction in the contradiction register MUST be closed by its referenced FR or remain accepted with its reason. 109-m verifies the register with the parity test.
- **FR-093**: 109-l MUST integrate Spec 108 once its APIs exist: the Spec 108 warnings as attention kinds (`anonymous_denied_by_binding_guard`, `client_holds_admin_key`, `client_credential_expiring`, `client_rotation_pending`, `profile_missing`, `client_token_name_conflict` — the names Spec 108 emits), and end-to-end tests proving that the parameters, link-map rows, sidebar entry and header slot this spec wired hidden (109-i, 109-k) appear once `features.scope_filters` and the `/profiles` route exist, on the Web UI and on the macOS `ScopeFilter`. The Viewing chip and the Clients-row profile controls are Spec 108's (108-i/108-k), placed in this spec's slots.

### Parity matrix

✓ = must ship; — = out of scope for that surface (reason given); (108) = delivered by Spec 108.

| # | Capability | Web UI | macOS | CLI | MCP | REST | Notes |
|---|---|---|---|---|---|---|---|
| 1 | Needs-attention list + count | ✓ Home, pill, badge | ✓ tray group, Home, badge | ✓ `attention`, `status` line, `doctor` first section | — | `GET /attention`, SSE | MCP: every fix is an operator action (browser sign-in, human review, secret entry); agents already see per-server `health` in `upstream_servers list` |
| 2 | Health status label + `usable` + ordered actions | ✓ | ✓ rows + tray | ✓ `upstream list` | ✓ via `upstream_servers list` (`health` object) | `health.status/usable/actions` | one label table |
| 3 | Single primary action per server | ✓ card | ✓ row + tray | ✓ `ACTION` column | — | `actions[0]` | MCP returns the data; there is no UI to render |
| 4 | Review screen with captured definitions | ✓ | ✓ sheet | ✓ `review show` | ✓ `inspect_quarantined`, `inspect_tools` | `GET /servers/{id}/review` | same fields |
| 5 | Review queue | ✓ `/review` | ✓ Review Queue | ✓ `review list` | ✓ `list_quarantined` + `inspect_tools` | `GET /review` | MCP has no single queue op; the two existing ops cover it, and a new op would only duplicate them |
| 6 | Approve / reject server (scan-gated) | ✓ | ✓ (fixed, X2) | ✓ `review approve|reject` | — | `security/approve|reject` | MCP: agents must never lift quarantine (constitution IV; tool description) |
| 7 | Approve / reject (block) tool | ✓ | ✓ | ✓ `review approve|reject --tools` | ✓ `approve_tool`, `block_tool` | `tools/approve|block` | |
| 8 | Tool review state labels | ✓ | ✓ | ✓ `--approval` help | ✓ `approval_status` values | same enum | X7 |
| 8a | Tool tier from one function | ✓ Tools "Tier" | ✓ Tools | ✓ `tools list --tier` | ✓ `tier` in review/inspect payloads | `tier` on tool listings | X11 |
| 9 | Clients page with presence rows | ✓ | ✓ | ✓ `client list|show` | — (108: `profiles list_clients`) | `GET /clients` | MCP: presence reveals local install paths; 108 adds a path-free operator op |
| 10 | Connect with diff preview + reload hint + short path | ✓ `ClientConnectList` | ✓ `ConnectClientView` | ✓ `connect` | — | `POST /connect/{client}` | MCP: writing local client configs is an operator action (issue #878 class, same as 108 row 10) |
| 11 | Endpoint & mode | ✓ Clients tab | ✓ Clients section | ✓ `status` (endpoints, mode) | — | `GET /routing`, `PATCH /config` | MCP: routing mode is restart-bound operator config |
| 12 | Agent tokens under Clients | ✓ tab | ✓ tab | ✓ `token` (unchanged) | — | `/tokens` | MCP: minting credentials from an agent is refused (Spec 028) |
| 13 | Import servers (shared component) | ✓ | ✓ existing import | ✓ `upstream import` | — | `/servers/import*` | MCP: `upstream_servers add` covers agent adds; bulk import reads local client files (operator action) |
| 14 | Catalog search across sources, ranked | ✓ | ✓ | ✓ `catalog search` | ✓ `search_servers` (registry optional) | `GET /catalog/search` | |
| 15 | Add to MCPProxy (from catalog) | ✓ | ✓ | ✓ `catalog add` | ✓ `add_from_registry` | Spec 070 route | always quarantined |
| 16 | Paste snippet / URL / command | ✓ | ✓ | ✓ `upstream add <name> <url>` / `upstream add <name> -- <cmd…>` (already auto-detects), `add-json`, `import <path>` | — | `import/json?preview` | MCP: agents pass structured args to `upstream_servers add`; parsing free text adds nothing |
| 17 | Secret toggle for env/header values | ✓ | ✓ | ✓ `--secret-env`, `--secret-header` | — | secrets API + ref | MCP: agents must not handle user secrets; `add` stays quarantined, and the user converts values with `config-to-secret` |
| 18 | Catalog sources management | ✓ Settings | ✓ Settings | ✓ `registry …` | ✓ `list_registries` (read) | `/registries` | MCP write: config-level, operator-only |
| 19 | Activity views (calls/sessions/system/all) | ✓ | ✓ | ✓ `--view calls\|system\|all` (default all); no `sessions` value | — | `type` filter | CLI default differs (research D11). CLI has no `sessions` view: there is no CLI sessions listing (Spec 108 parity row 17), and per-session history is `activity list --session <id>` (contracts/cli.md). MCP: agents do not query history (108 row 15) |
| 20 | System-event folding | ✓ | ✓ | — | — | — | CLI and export stay unfolded for scripts |
| 21 | Time range + tool filters | ✓ | ✓ (new) | ✓ `--from/--to`, `--tool` | — | existing params | |
| 22 | Token-savings estimate | ✓ | ✓ | ✓ `status` | — | `ServerTokenMetrics.estimated` | MCP: not agent-facing |
| 22a | Usage page | ✓ `/usage` (Monitor) | — Home "Usage" section | ✓ `activity summary` | — | `/activity/usage` | macOS: the native Home already shows token savings, distribution and calls; a separate native Usage view is a follow-up (navigation-map.md) |
| 23 | URL filter contract + deep links | ✓ | ✓ `ScopeFilter` in-app navigation | — | — | — | CLI/MCP have no navigation |
| 24 | Sidebar IA (Home/Connect/Protect/Monitor) | ✓ | ✓ | n/a | n/a | — | CLI groups are nouns (`client`, `review`, `catalog`, `attention`) matching the same areas |
| 25 | ⌘K palette | ✓ | — | n/a | n/a | — | macOS uses per-view search fields and the standard Find (⌘F); a web-style palette would not match platform conventions |
| 26 | "+ Add" menu | ✓ header | ✓ toolbar "+" | n/a | n/a | — | |
| 27 | Tab/URL sync | ✓ | n/a (native state restoration) | n/a | n/a | — | |
| 28 | Onboarding usable-completion + Verify hints | ✓ wizard | ✓ first-run dialog + Connect result | ✓ `connect` output | — | `onboarding/state.has_usable_server` | |
| 29 | Settings naming, edition-gated tabs | ✓ | ✓ (already "Settings") | n/a | n/a | `status.edition` | |
| 30 | Profile/client/token filters, Viewing chip, profile links | ✓ filters + links (109-k, hidden until 108) · chip (108) | ✓ `ScopeFilter` fields (109-k, hidden until 108) · views (108) | (108) | (108) | (108) backend filters | parameters and links owned here, backend and chip by 108; 109-l tests the un-hiding (FR-093) |

### Terminology *(binding for every surface)*

| Concept | REST / JSON | CLI | Web UI label | macOS label | Retired names |
|---|---|---|---|---|---|
| Landing page | — | — | Home | Home | Dashboard, Overview |
| Needs attention | `attention`, `items[].kind` | `attention` | Needs attention | Needs Attention | "servers need attention", "issues" (doctor) |
| Health display | `health.status` | `STATUS` | label table | label table | raw `level` as text |
| Clients | `clients` | `client` | Clients | Clients | Connect Clients (as a place), AI agents |
| Reload hint | `reload_hint` | printed after connect | "Restart X (or …) to load MCPProxy" | same | — |
| Endpoint & mode | `routing`, `routing_mode` | `status` fields | Endpoint & mode | Endpoint & Mode | Mode (header), MCP (header) |
| Agent tokens | `tokens` | `token` | Agent tokens (Clients tab) | Agent Tokens (Clients tab) | — |
| Sessions | `sessions` | `activity --session` | Sessions (Activity view) | Sessions (Activity view) | MCP Sessions (page) |
| Review queue | `review` | `review` | Review queue | Review Queue | Security (as the review place) |
| Approve server / Reject server | `security/approve`, `security/reject` | `review approve|reject <server>` | Approve server / Reject server | Approve Server / Reject Server | unquarantine (as a UI verb) |
| Approve tool / Reject tool | `tools/approve`, `tools/block` | `review approve|reject <server> --tools` | Approve / Reject | Approve / Reject | Block (UI verb) |
| Tool review states | `approved`, `pending`, `changed` | `--approval approved|pending|changed` | Approved · New, needs review · Changed, needs review | same | Awaiting approval, Pending Approval |
| Tool tier | `tier: read|write|destructive|unannotated|unknown` | `TIER` | Read · Write · Destructive · Unannotated · Unknown | same | "risk" for the tier column (risk stays the scan score) |
| Catalog | `catalog` | `catalog` | Catalog | Catalog | Repositories, Registries (as the browse place) |
| Catalog source | `registries` | `registry` | Catalog source | Catalog Source | registry (in UI copy) |
| Add action | — | "Added … to MCPProxy" | Add to MCPProxy → Added ✓ · Open | same | Add to MCP |
| Secret toggle | `${keyring:…}` | `--secret-env` | Value · Secret | Value · Secret | — |
| Activity views | `view` (UI), `type` (REST) | `--view calls\|system\|all` (no `sessions`) | Tool calls · Sessions · System events · All | same | — |
| Settings | `config` | — | Settings | Settings | Configuration |
| Status pill | — | `status` | "● 1 of 2 online · 14 tools · Retrieve" | tray summary | — |

### Contradiction register

Every contradiction from the four inventories (W = Web UI, M = macOS, C = CLI, R = MCP/REST) plus the ones found while writing this spec (X). **Resolved** = closed by the FR; **108** = closed by Spec 108; **Accepted** = intentional, with the reason.

| ID | Contradiction | Disposition |
|---|---|---|
| W1 | CLI can pin a token to a profile; Web UI cannot | 108 (FR-043) |
| W2 | Header "Profile:" looks like agent scoping but only sets a UI default | 108 (FR-044, removal). Interim: FR-057 hides it when there are no profiles |
| W3 | Tools approval filter: "Awaiting approval", "Pending", "Changed" overlap | Resolved: FR-027 |
| W4 | No client/profile activity filter on any surface | 108 (FR-031/036). Non-profile parameters: FR-075, FR-080–083 |
| W5 | `/settings` vs "Configuration" | Resolved: FR-056 |
| M1 | Tray Profile submenu is as misleading as the Web switcher | 108 (FR-048) |
| M2 | macOS informed review vs Web UI blind approval | Resolved: FR-021–024 (both use one endpoint) |
| M3 | macOS Activity exposes fewer filters than the backend | Resolved: FR-083 (tool, status, time range, view, auth type). Profile/client/token: 108 (FR-049) |
| M4 | macOS token sheet cannot set a profile | 108 (FR-049) |
| M5 | Catalog named Registries / Repositories / registry | Resolved: FR-062, FR-066, terminology |
| C1 | CLI has no listable client entity | Resolved: FR-036 (presence). Binding: 108 (FR-036) |
| C2 | No `profile` command group | 108 (FR-036) |
| C3 | CLI grids lack `--profile/--client`; `upstream list` has no filters | 108 for profile/client. Resolved here: FR-015 (`--status`), FR-075 (`--from/--to`, `--view`) |
| C4 | Three approve verbs (`upstream approve`, `tools approve|reject`, `security approve|reject`; the inventory listed two) | Resolved: FR-025 (`review` group; old verbs become documented aliases) |
| C5 | `activity --agent` names a token, not a client | 108 (FR-031 `token` alias, `client`) |
| C6 | Global `-o/--output` vs `activity export --format` | Accepted: they are different concepts (terminal rendering vs export file format, which includes csv); renaming breaks scripts. Help texts cross-reference each other (T123) |
| R1 | Header groups a cosmetic default with functional controls | Resolved: FR-052 (Mode/endpoints leave the header). Switcher removal: 108 |
| R2 | `upstream_servers` uses `AuthorizeServerOp`; `quarantine_security` uses bare `IsAdmin()` | Accepted for this spec: an authorization-model difference, not a UX one. Spec 108 D14 defines management-tool semantics |
| R3 | "profile" means three things across CLI, REST and MCP | 108 |
| X1 | Three needs-attention predicates | Resolved: FR-001–004 |
| X2 | macOS Approve Server bypasses the scan gate (`unquarantine`) | Resolved: FR-022 (security-relevant; ships in 109-f) |
| X3 | Three approve verbs in the CLI; informed review only on macOS | Resolved: FR-021–026 |
| X4 | Three catalog names | Resolved: FR-062, FR-066, FR-067 |
| X5 | Quarantined server reports `level: healthy` | Resolved: FR-010/011 (display rule + `usable`). `level` stays a severity field, research D4 |
| X6 | Different sidebars on Web and macOS; Sessions/Security pages only on Web; tokens under Secrets | Resolved: FR-050, FR-058, FR-034, FR-070 |
| X7 | Three vocabularies for tool review states | Resolved: FR-027 |
| X8 | No surface gives a reload hint; path forms differ | Resolved: FR-037, FR-042 |
| X9 | Agents can approve tools (`approve_tool`) but not servers | Accepted: server approval lifts quarantine for every tool and must stay a human decision (constitution IV); per-tool approval on an already-trusted server is Spec 032 behaviour |
| X10 | CLI `activity list` defaults to all types, UI to Tool calls | Accepted: script compatibility (research D11); `--view calls` gives the UI default |
| X11 | Tool tier computed three ways (Web `write`, backend `read`, CLI `operation_type`) | Resolved: FR-028 |
| X12 | The Go tray (`internal/tray` via `cmd/mcpproxy-tray`, built for Windows and macOS: the Windows tray and the tray in the macOS archive installs; the DMG ships the Swift app) unquarantines a server on one click in its "Security Quarantine" submenu (`managers.go` → `onServerAction(name, "unquarantine")` → `POST /unquarantine`), bypassing the scan gate and informed review, like X2 on macOS | Resolved: FR-022 (109-f, T084b): the click opens the review location in the Web UI; the tray's `UnquarantineServer` path is deleted (SC-005) |

### Key Entities

- **Attention item** (derived): kind, rank, subject {type, id, name}, summary, detail, fix {verb, target}, since.
- **Health status** (extended): level, admin_state, summary, detail, action, **status**, **usable**, **actions[]**.
- **Review payload** (derived): server summary + tools[] {name, description, schemas, annotations, tier, approval_status, scan_verdict, held_signals, previous_*, diff}.
- **Tool approval record** (extended): + current_annotations, previous_annotations.
- **Client presence row** (derived, owned here): id, display_name, kind (`supported|other`; Spec 108 adds `custom`), icon, state, installed, connected, display_path, config_path, last_seen, active_sessions, calls_24h, reload_hint. Spec 108's `ClientView` extends it additively and never renames or drops a field.
- **Catalog result** (derived): title, id, publisher, verified, official, popularity, source + Spec 070 add fields.
- **Scope filter** (UI state): the contract parameters, identical on Web (`useScopeQuery`) and macOS (`ScopeFilter`).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every in-scope audit finding (O2–O6, N1–N8, H1–H4, S2, S4–S7, C1, C2, A1–A3, T1) maps to at least one FR and one automated test (checklist traceability table), and all acceptance scenarios above are automated (Go, vitest, Playwright, XCTest, or CLI golden). Checked by T148a, which parses the traceability table and fails when a finding id is missing, an FR id does not exist in this spec, or a task id does not resolve to a test file that exists in the repository.
- **SC-002**: For the US1 fixture, the attention count and item order are identical on REST, the Web UI (Home, pill, badge), macOS (tray, Home, badge) and CLI (`attention`, `status`, `doctor`) (parity test).
- **SC-003**: For every health fixture, zero renderers (Web card, detail, macOS row, tray, CLI table) show "healthy", "online" or "connected" for `usable=false` (table test).
- **SC-004**: For a quarantined server with captured definitions, 100% of tools are visible before approval on Web, macOS, CLI and MCP, with identical `tier`, `annotations` and `scan_verdict` values.
- **SC-005**: No first-party surface calls `POST /servers/{id}/unquarantine`: a grep test over the non-test sources in `frontend/src`, `native/macos`, `cmd/mcpproxy`, `cmd/mcpproxy-tray` and `internal/tray` (the Go tray binary) finds no reference to the `/unquarantine` path, because 109-f deletes the then-dead client methods (`APIClient.unquarantineServer`, `stores/servers.ts` `unquarantineServer`, the Go tray's `Client.UnquarantineServer`/`ServerAdapter.UnquarantineServer`) and repoints the Go tray's quarantine submenu (X12) instead of leaving them unused.
- **SC-006**: From any page, a user reaches "connect a new client" in ≤ 2 clicks (sidebar Clients → Connect, or "+ Add ▾" → Client).
- **SC-007**: The header shows every control without clipping or horizontal scroll at 1440, 1100, 900 and 390 px, in both themes (`visual-a11y-sweep`).
- **SC-008**: Catalog search "github" on the fixture ranks the official, verified GitHub server first, with identical order across REST, Web, macOS, CLI and MCP.
- **SC-009**: Every scope-aware page round-trips its URL (URL → state → URL is identity for contract parameters, tab included) and loads filtered without an unfiltered fetch (Playwright network assertion).
- **SC-010**: On the A1 fixture (approve a 14-tool server, then 3 real calls), the default Activity view shows exactly the 3 calls; System events shows 1 folded row.
- **SC-011**: `GET /api/v1/attention` p95 ≤ 20 ms at 100 servers / 1,000 tools (served from the in-memory snapshot); catalog search p95 ≤ 5.5 s with one source timing out.
- **SC-012**: The contradiction register has no item without a disposition, and the terminology parity test (Go enums → `contracts.ts` → Swift fixtures → CLI help) passes.

## Assumptions

- The S1 fix (another session) renders Login from `oauthSignInState` whatever `health.action` says. FR-012's `actions[]` lets that logic read `actions` without a conflict.
- The O1 and S3 fixes (another session) touch the wizard's import candidate filter and ServerDetail refetch. The `ImportServers` component in FR-033 keeps the O1 self-filter.
- Spec 108 may merge before or after any 109 PR except 109-l, which depends on 108-f/108-i/108-j/108-k (108-k un-hides the macOS `ScopeFilter` fields that 109-l asserts). In the other direction Spec 108 integrates into this spec's artifacts: 108-a ← 109-a (`contracts.AnnotationTier`, which Spec 108's `IntrinsicTier` calls, FR-028), 108-f ← 109-h, 108-i ← 109-h + 109-i, 108-j ← 109-k, 108-k ← 109-h + 109-i + 109-k; 108's other backend PRs (108-b…108-e) have no edge to 109. Shared files without an edge (`ClientStatus` with 108-c, the Web connect component with 108-c, `cmd/mcpproxy/connect_cmd.go` and macOS `ConnectClientView.swift` with 108-c, `scope_filters.go` with 108-e — list variable and gate function, created together by whichever merges first, and the administrator-only gate on the three `GET /connect` reads with 108-c — FR-030a, so 109-h never ships an administrator-only `/clients` beside open connect reads) follow the "later merge keeps both" rule in tasks.md Dependencies.
- The macOS app reads the same REST/SSE API (constitution: tray holds no state). All new macOS behaviour is presentation over the endpoints above.
- Catalog sources expose popularity only sometimes. Ranking treats missing popularity as zero, never as a penalty beyond that.

## Out of Scope

- Everything in Spec 108 (profiles, client credentials, scope enforcement, attribution, the backend profile/client/token filters, the Viewing chip, the Profiles page and the Clients-row profile controls). The URL parameters, links and navigation for them are in scope here (Coordination).
- The server-edition navigation (`/my/*`, `/admin/*`) and multi-user flows.
- New scanners or changes to the scan gate. Annotation changes triggering re-review (research D6 records the observation for a follow-up).
- Light-theme redesign and phone layouts beyond "no clipping at 390 px".
- Built-in profile templates (108 out of scope).
- The Go tray (`internal/tray`, the `mcpproxy-tray` binary for Windows and for macOS archive installs) as a presenter of the new areas (Home, attention, Clients, Review queue, Catalog): it is a minimal menu that opens the Web UI, so the parity matrix does not list it. The one exception is the security fix X12 (FR-022, SC-005). Presenter parity for it is a follow-up.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(attention): one needs-attention list for every surface

Related #[issue-number]

Spec 109 PR 109-d: GET /api/v1/attention + SSE attention.changed; Home,
header pill, sidebar badge, tray and `mcpproxy attention` read it.

## Changes
- internal/runtime/attention.go: pure Compute over the state snapshot
- Web UI Home list; macOS tray group reads the endpoint

## Testing
- attention_test.go table; SC-002 parity test green
```
