---
id: dashboard
title: Home and Navigation
sidebar_label: Home
sidebar_position: 1
description: The MCPProxy web interface - Home and its needs-attention list, the grouped sidebar, the header, the command palette and the Add menu
keywords: [web, ui, home, dashboard, navigation, attention, sidebar, command palette]
---

# Home

MCPProxy includes a web interface for monitoring and configuring your MCP servers. Its landing page is **Home**. (It was called Dashboard before; the page is still served at `/` and the documentation URL did not change.)

## Accessing the Web UI

The Web UI is available at:

```
http://127.0.0.1:8080/ui/
```

If API key authentication is enabled, append the key as a query parameter:

```
http://127.0.0.1:8080/ui/?apikey=your-api-key
```

The tray application opens the Web UI with the API key automatically.

## Home

Home answers one question first: **what needs me?**

- **Needs attention** is the one list MCPProxy computes for every surface: sign-in prompts, servers waiting for review, missing secrets, configuration problems and clients that connected but were never seen. Items are ordered by rank, each with one button that goes to the screen that fixes it (Sign in, Review, Add secret). With nothing to do Home reads "All clear" and the usage strip moves to the top. See [Needs Attention](/features/needs-attention).
- The **usage strip** shows calls today, blocked calls, errors and the estimated tokens kept out of every request; each number links to the matching [Activity](/web-ui/activity-log) view.
- The **topology** shows your clients, MCPProxy and your servers.

The same count appears on the **header pill** (hidden at 0) and as the **badge** on the sidebar's Home entry. `mcpproxy attention` and the macOS tray show the same list; see [Attention Command](/cli/attention-command).

## The sidebar

The sidebar is grouped by what you are doing:

| Group | Items |
|---|---|
| Home | Home, with the needs-attention badge |
| Connect | Clients, Profiles (once profiles are available), Servers, Tools |
| Protect | Review queue (badge = servers awaiting review), Secrets |
| Monitor | Activity, Usage |

Settings, Docs, Feedback and the theme switch sit below the groups. **Clients** is the home for connecting a client later, seeing who is talking to MCPProxy, the **Endpoint & mode** tab and the **Agent tokens** tab. The **Review queue** is where quarantined servers and new or changed tools are reviewed with their full definitions before approval.

## The header

At 1100 px and wider the header shows, left to right: the search field (`⌘K`), the status pill (for example "1 of 2 online, 14 tools, Retrieve"), the attention pill and the **+ Add** menu. Narrower widths collapse each into an icon; nothing clips or scrolls sideways down to 390 px.

- **Search and `⌘K`** (`Ctrl+K` on Windows and Linux; `/` when no input is focused) opens the command palette. It searches pages, servers, tools, settings and actions. Pressing Enter on free text opens the Tools page with that text as the query.
- **+ Add** offers **Server** (the catalog-first Add Server page), **Client** (the connect dialog with the diff preview), **Token** (the create-token dialog) and **Profile** once profiles are available. Connecting a new client is at most two clicks from any page.

## Old addresses

Pages that moved keep working: the old address redirects and keeps the query string.

| Old address | Goes to |
|---|---|
| `/overview` | `/` (Home) |
| `/repositories` | `/add-server?tab=catalog` |
| `/sessions` | `/activity?view=sessions` |
| `/tokens` | `/clients?tab=tokens` |
| `/security` | `/review` |
| `/search` | `/tools` |

## Servers

The Servers page shows each server as a card with **one status word** (Online, Connecting, Sign-in required, Needs review, Secret required, Needs configuration, Error or Disabled) and at most one primary button for the next step. Everything else is in the card's menu or on the server's detail page, whose tabs (Tools, Configuration, Logs, Security, Review) are kept in the address with `?tab=`.

### Add Server

**+ Add → Server** opens one page with four tabs, kept in the address as `?tab=`:

1. **Catalog** (default): search every catalog source at once, or browse Official and Popular. The button reads "Add to MCPProxy" and becomes "Added ✓ · Open"; the server lands quarantined. See [Registry Add](/features/registry-add).
2. **Paste**: paste a URL, a command line or a JSON or TOML snippet; nothing is added until you press Add.
3. **Import**: bring servers in from the clients already configured on this machine.
4. **Manual**: fill in the fields yourself.

Env values and headers whose names look like secrets default to **Secret**, which stores them in the OS keyring.

### Review before approval

Quarantined servers wait on the [Review queue](/features/security-quarantine). The review screen lists every tool read-only with its tier, annotations and scan verdict before you approve, and lets you leave tools out. Approving always goes through the scan gate.

## Tool Search

Use the search field or `⌘K` to find tools across all servers: enter keywords (for example "create file"), see the matching tools with descriptions, and see which server provides each one.

## Filtering by URL

Tools, Activity, Usage, Servers, Clients and Review read their filters from the address (`server`, `tool`, `status`, `from`, `to`, `view`, and so on), apply them before the first request and show each as a removable chip. A link to a filtered view therefore always opens filtered.

## OAuth Status

For OAuth-enabled servers the server's page shows the authentication status, the token expiration and a sign-in or re-authenticate button, and a server that needs sign-in appears first in the needs-attention list.

## Real-time Updates

The interface updates automatically via Server-Sent Events (SSE): server status changes, tool availability, the needs-attention list and the review queue. No page refresh is needed.

## Dark Mode

The interface supports both light and dark themes, matching your system preference.

## Mobile Support

The interface is responsive and works on mobile devices: collapsible navigation, touch-friendly controls and optimised layouts.
