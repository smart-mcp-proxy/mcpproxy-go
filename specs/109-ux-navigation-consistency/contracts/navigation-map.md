# Contract: navigation map (Web UI routes, sidebar, header; macOS sidebar and toolbar)

## Web UI sidebar (personal edition)

```
MCPProxy
[Setup  N]                 ← pinned only while onboarding is incomplete (existing Spec 046 badge)
Home                 (N)   ← attention count
CONNECT
  Clients            (N live)
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
Settings · Docs · Feedback · Theme
v0.69.x  ⟳ Check
```

Removed items: Dashboard (→ Home), Agent Tokens (→ Clients tab), Sessions (→ Activity view), Security (→ Review queue + Settings → Security → Scanners), Repositories (→ Servers → Add → Catalog + Settings → Catalog sources), Configuration (→ Settings). Group labels are uppercase section headers. Collapsed-sidebar icons keep `aria-label` = item name (existing a11y rule).

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

- Search: one input. Focus or ⌘K/Ctrl+K opens the palette. Enter on free text → `/tools?q=`.
- Viewing slot: `<slot name="viewing">`, empty until Spec 108 fills it.
- Status pill: `online/total` and the tool count are computed from the server rows every surface already holds (`GET /servers` + SSE `servers.changed`, both carrying `health.usable` from 109-c): online = rows with `health.usable`, total = all rows, tools = sum of `tool_count` over usable rows (tools an agent can reach now). It does **not** read `/status` `upstream_stats`, whose `connected_servers` counts `status.Connected` (a quarantined or sign-in-parked server can be connected and unusable). Routing mode comes from `GET /routing` as a read-only chip. Click → `/servers`. macOS uses the same rule over `AppState.servers`; no REST change.
- Attention pill: hidden at 0; click → popover (first 5 items with fix buttons, "See all" → `/`).
- "+ Add ▾": Server (`/add-server`), Client (opens `ClientConnectList` in a dialog), Token (`/clients?tab=tokens&create=1`), Profile (Spec 108, hidden until present).
- Removed from the header: the separate Search button, the "+ Add Server" button, ModeSwitcher, the MCP endpoints dropdown, the ProfileSwitcher (hidden when there are no profiles from 109-a; removed by Spec 108).

Below 1100 px: search → icon button; status pill → `● 1/2`; attention pill → `⚠ 3`; "+ Add ▾" → `+`. At 390 px the header is one row with exactly: drawer toggle, search icon, collapsed status pill `● 1/2`, collapsed attention pill `⚠ 3` (hidden at 0) and `+` (the Viewing slot, when Spec 108 fills it, collapses to an icon in the same row).

## Stacking scale (FR-055)

`frontend/src/assets/z-index.css` (or Tailwind theme tokens): `--z-sidebar: 30; --z-header: 40; --z-dropdown: 50; --z-modal: 60; --z-toast: 70`. Modals use `<dialog>.showModal()` (top layer) or `<Teleport to="body">`. The sidebar's `drawer-side z-40` is replaced by `var(--z-sidebar)` (the version block has no z-index of its own; it painted over the Add Server modal only because that modal was not in the top layer).

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

- Removed sidebar items: Dashboard (→ Home), Registries (→ Add Server sheet "Catalog" + Settings → Catalog Sources), Agent Tokens (→ Clients tab).
- Usage: shown as the Home "Usage" section (token savings, distribution, calls). A separate native Usage view is out of scope (parity matrix row 22 covers the data).
- Toolbar: "+" menu (Server, Client, Token; Profile with Spec 108).
- Tray menu: "Needs Attention (N)" reads `GET /attention`; a "Review Queue…" item opens the Review Queue view; "Connect Client…" opens Clients → Connect.
