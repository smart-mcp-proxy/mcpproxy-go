# Contract: navigation map (Web UI routes, sidebar, header; macOS sidebar and toolbar)

## Web UI sidebar (personal edition)

```
MCPProxy
[Setup  N]                 ← always pinned: badge and pulse while onboarding is incomplete, a quiet ✓ after (Spec 046 v2; it is also the way back into the wizard)
Home                 (N)   ← attention count
CONNECT
  Clients            (N live)   ← rows of `GET /clients` with `active_sessions > 0`
  Profiles           (N)   ← appears when Spec 108's /profiles route is registered
  Servers            (N)
  Tools              (N)
PROTECT
  Review queue       (N)   ← review count (GET /review `count`, one per server awaiting review)
  Secrets
MONITOR
  Activity
  Usage
─────────
Settings · Docs · Feedback · Theme        ← Docs = https://docs.mcpproxy.app (new tab, rel=noopener)
v0.69.x  ⟳ Check
```

Removed items: Dashboard (→ Home), Agent Tokens (→ Clients tab), Sessions (→ Activity view), Security (→ Review queue + Settings → Security → Scanners), Repositories (→ Servers → Add → Catalog + Settings → Catalog sources), Configuration (→ Settings). Group labels are uppercase section headers. Collapsed-sidebar icons keep `aria-label` = item name (existing a11y rule).

The diagram is the final 109-i grouping. At 109-h, `SidebarNav.vue` adds a minimal personal-edition Clients link to the existing menu so `/clients` is reachable; 109-i then moves it into CONNECT and performs the full regroup/removal. Server-edition navigation does not gain this link because its `/clients` route is absent.

## Routes

| Route | Renders | Notes |
|---|---|---|
| `/` | Home | attention list, topology, usage strip (FR-051) |
| `/overview` | → `/` | redirect, query kept |
| `/usage` | Usage | full page (existing `Usage.vue`) |
| `/clients` | Clients | `?tab=clients|endpoint|tokens` |
| `/tokens` | → `/clients?tab=tokens` | redirect, query kept (Spec 108 links `?token=`, `?profile=` survive) |
| `/servers` | Servers | `?status=&q=` |
| `/add-server` | Add server (top-level, **not** under `/servers/`: a static `/servers/add` would shadow `/servers/:serverName` for a server literally named `add`, which config validation allows — codex round 3) | `?tab=catalog|paste|import|manual` (default `catalog`, FR-016) `&q=&source=<catalog source id>` |
| `/servers/:name` | Server detail | `?tab=tools|config|logs|security|review` (existing ids + `review`) synced (FR-016) |
| `/tools` | Tools | contract params |
| `/review` | Review queue | `?server=&change=`. Before 109-g: interim redirect → `/servers?status=needs_review` (109-a, T026a; `?status=` is ignored until 109-k) |
| `/review/:server` | Review screen | also embedded as server detail `?tab=review`. Before 109-g: interim redirect → `/servers/:server?tab=tools` (109-a, T026a) |
| `/security` | → `/review` | redirect |
| `/security/scans/:jobId` | Scan report | unchanged |
| `/secrets` | Secrets | unchanged |
| `/activity` | Activity | `?view=calls|sessions|system|all` + contract params |
| `/sessions` | → `/activity?view=sessions` | redirect, query kept (Spec 108 acceptance check 6) |
| `/repositories` | → `/add-server?tab=catalog` | redirect, query kept |
| `/settings` | Settings | `?tab=security|general|catalog|advanced|raw` (+ `teams` only in the server edition); `?focus=` unchanged |
| `/search` | → `/tools` | unchanged |
| `/profiles`, `/profiles/:name` | Spec 108 | — |

## Header (≥ 1100 px)

```
[☰] [🔍 Search servers, tools, settings…  ⌘K] [Viewing slot] [● 1 of 2 online · 14 tools · Retrieve] [⚠ 3] [+ Add ▾]
```

- Search: one input. Focus or ⌘K/Ctrl+K opens the palette (seeded with any typed text; the header field clears and gives up focus so closing the palette does not reopen it). `/` opens it too when no input is focused and no dialog is open. Enter on free text → the palette's default row "Search tools for …" → `/tools?q=`. Tool rows are requested only for non-empty text, debounced 150 ms, `limit=8`, stale responses dropped.
- Viewing slot: `<slot name="viewing">`, empty until Spec 108 fills it. Its fallback content is the interim `ProfileSwitcher` (Spec 109 FR-057: hidden for tenants and while no profiles exist, and hidden below 1100 px), which Spec 108-i deletes together with the fallback when it fills the slot from `App.vue`. An empty slot renders no element.
- Status pill: `online/total` and the tool count are computed from the server rows every surface already holds (`GET /servers` + SSE `servers.changed`, both carrying `health.usable` from 109-c): online = rows with `health.usable`, total = all rows, tools = sum of `tool_count` over usable rows (tools an agent can reach now). It does **not** read `/status` `upstream_stats`, whose `connected_servers` counts `status.Connected` (a quarantined or sign-in-parked server can be connected and unusable). A row with no `health` object (an old core) falls back to `connected && enabled && !quarantined`. Routing mode comes from `GET /routing` as a read-only chip (the same labels the mode switcher uses); a pending restart is named in the chip's title. Click → `/servers`. macOS uses the same rule over `AppState.servers`; no REST change. Tenants see no status pill (their servers store is never loaded, Spec 107 FR-041).
- Attention pill: hidden at 0; click → popover (first 5 items with fix buttons, "See all" → `/`).
- "+ Add ▾": Server (`/add-server`), Client (opens `ClientConnectList` in a dialog), Token (`/clients?tab=tokens&create=1`), Profile (Spec 108: `/profiles?create=1` once a route with the path `/profiles` exists, hidden until then; Spec 108-i must register `/profiles` by that path and strip `create` after opening its editor). Token opens the create dialog and strips `create` from the URL. Server edition admin: a single item, "Personal server" → `/add-server`. Tenant: no menu (Spec 107 FR-041).
- Removed from the header: the separate Search button, the "+ Add Server" button, ModeSwitcher, the MCP endpoints dropdown, the ProfileSwitcher (hidden when there are no profiles from 109-a; removed by Spec 108).

Below 1100 px: search → icon button; status pill → `● 1/2`; attention pill → `⚠ 3`; "+ Add ▾" → `+`. At 390 px the header is one row with exactly: drawer toggle, search icon, collapsed status pill `● 1/2`, collapsed attention pill `⚠ 3` (hidden at 0) and `+` (the Viewing slot, when Spec 108 fills it, collapses to an icon in the same row).

## Stacking scale (FR-055)

`frontend/src/assets/z-index.css`: `--z-header: 30; --z-sidebar: 40; --z-dropdown: 50; --z-modal: 60; --z-toast: 70` (the open mobile drawer must cover the sticky header, so the sidebar sits above the header). Modals use `<dialog>.showModal()` (top layer) or `<Teleport to="body">`. The sidebar's `drawer-side z-40` is replaced by `var(--z-sidebar)` (the version block has no z-index of its own; it painted over the Add Server modal only because that modal was not in the top layer).

## Settings tabs

Security & Access (incl. **Scanners** section moved from Security, Docker toggle shown once, `anonymous_profile` from Spec 108) · General · **Catalog sources** (moved from Repositories) · Advanced · Raw JSON · Server Edition (server edition only). The routing mode field stays searchable in Settings, with a note "Also in Clients → Endpoint & mode". Line icons replace emoji.

## macOS main window

```
Home                    (badge = attention count)
Connect
  Clients               (tabs: Clients · Endpoint & Mode · Agent Tokens)
  Profiles              (Spec 108)
  Servers
  Tools
Protect
  Review Queue          (badge = review count)
  Secrets
Monitor
  Activity              (segments: Tool calls · Sessions · System events · All)
```

- Sidebar names: "Activity" (not "Activity Log"; the `SidebarItem.activity` raw value is the in-process wire value of `.switchToSidebarTab` and is never persisted). Rows are declared in visual order, so the hidden ⌘1…⌘7 shortcuts follow what the user sees (Home, Clients, Servers, Tools, Review Queue, Secrets, Activity). Accessibility ids: `sidebar-<Name>`, `sidebar-badge-<Name>`, `toolbar-add-menu`, `toolbar-add-<Server|Client|Token>`.
- Home carries the attention-count badge, Review Queue the review count.
- Removed sidebar items: Dashboard (→ Home), Registries (→ Add Server sheet "Catalog" + Settings → Catalog Sources), Agent Tokens (→ Clients tab).
- Usage: shown as the Home "Usage" section (token savings, distribution, calls). A separate native Usage view is out of scope (parity matrix row 22 covers the data).
- Toolbar: "+" menu (Server, Client, Token; Profile with Spec 108-k). The toolbar sets `AppState.pendingAddAction` and switches the sidebar; the destination view consumes only the kinds it owns on appear and on change (the `scopeFilter` hand-off shape, which survives a window created by the click). Server → Add Server sheet on Catalog; Client → Connect sheet; Token → Clients → Agent Tokens create sheet. Spec 108-k adds `SidebarItem.profiles` and `AddMenuItem.profile` and extends `NavigationStructureTests`.
- Tray menu: "Needs Attention (N)" reads `GET /attention`; a "Review Queue…" item opens the Review Queue view; "Connect Client…" opens Clients → Connect.
