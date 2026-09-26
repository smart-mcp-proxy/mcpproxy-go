---
id: needs-attention
title: Needs Attention
sidebar_label: Needs Attention
sidebar_position: 15
description: The one needs-attention list every MCPProxy surface reads from — sign-in prompts, reviews, errors, missing secrets, configuration problems and unseen clients.
keywords: [attention, needs attention, health, quarantine, review, sign in, dashboard, home, tray, cli, doctor]
---

# Needs Attention

MCPProxy computes **one** needs-attention list from in-memory state — server
health, tool approval counts, connect status, session history — and every
surface renders it verbatim: the Web UI's Home page, the macOS tray's "Needs
Attention" group and Home section, and the CLI's `attention` command, the
first line of `status`, and the first section of `doctor`.

No surface derives its own version of this list. Before this list existed,
the Web UI Dashboard, the macOS tray and the CLI each had their own predicate
over server health, and the three disagreed about what counted as "needs
attention" — a quarantined server with a transport fault, for example, could
show as an error on one surface and a review item on another. This page is
the parity table for how the list is computed, its kinds, ranks and fixes.

## Where it lives

| Surface | Location |
|---|---|
| REST API | `GET /api/v1/attention` |
| Web UI | Home page (`/`) attention list, header pill (hidden at 0), sidebar Home badge |
| macOS tray | "Needs Attention (N)" menu group, Home section (first, above the topology) |
| CLI | `mcpproxy attention`; first line of `mcpproxy status`; first section of `mcpproxy doctor` |
| SSE | `attention.changed` event (`{count, ids}`, narrowed per subscriber) |

## Kinds, ranks and fixes

The list is sorted by rank ascending, then by subject name. Each item's `id`
is stable (`kind:type:subject[:state]`), so a surface can diff successive
lists instead of re-rendering the whole thing on every change.

| Kind | Rank | Condition | Fix |
|---|---|---|---|
| `sign_in_required` | 10 | `health.status == sign_in_required` (includes a quarantined OAuth server, or a failed token refresh) | `login` → opens the server, which offers Sign in |
| `missing_secret` | 20 | `health.status == needs_secret` | `set_secret` → opens the server's secret form |
| `config_error` | 30 | `health.status == needs_config` | `configure`/`edit_url` → opens the server's Configuration tab, field focused |
| `server_error` | 40 | Not quarantined, and `health.status` has been `error` or `connecting` for ≥ 60 seconds | `restart` → the server menu action; logs are one click away |
| `server_review` | 50 | `admin_state == quarantined` (and enabled) | `review` → opens the review location — never a one-click approve |
| `tool_review` | 60 (changed) / 61 (pending) | A trusted server with ≥ 1 tool in that approval state; one item per state, so a server with both is two items | `review` → opens the review location, filtered to that state |
| `client_never_seen` | 70 | A client MCPProxy recorded as connected ≥ 5 minutes ago, with no MCP session observed since | `reload_hint` → shows how to restart the client |

Never an item: a disabled server, a server `connecting` for under 60 seconds,
a `ready` server whatever its proactive `actions` (a login nudge for a token
expiring soon is not a blocking state), and update availability (that has its
own nudge). A quarantined server is *only* a `server_review` item, never also
`server_error` — restarting a server before it is reviewed fixes nothing, and
the review screen already shows the transport error.

Conditions key on `health.status` and `admin_state`, never on the presence of
an entry in `actions` — `actions` also carries proactive nudges on a
perfectly usable server (the same `status`/`usable`/`actions` vocabulary every
surface renders `health` through) and cannot by itself distinguish a blocking
state from a hint.

## Fixes are never a one-click approve

A `review` fix always **opens a screen**; it never calls an approve or
unquarantine endpoint directly from the list. Reviewing a quarantined server
or a changed/pending tool is a decision a person makes on that server's own
review screen, with the diff or the transport error in front of them — not a
button on a summary row. This is the same rule the macOS tray already
enforced for its per-server actions, extended to every surface.

## Live updates

The backend recomputes the list (debounced) whenever the underlying state
changes, and separately arms a timer for the earliest pending time-based
threshold — a server crossing from `connecting` to `server_error` at 60
seconds, or a client crossing into `client_never_seen` at 5 minutes — so the
list is correct even on a quiet instance where nothing else happens to
trigger a recompute. Every surface holding a live connection (the Web UI and
the macOS tray, both over Server-Sent Events) receives an `attention.changed`
notification and refreshes; the CLI and `GET /api/v1/attention` always read
the current computed list.

## Scoped callers

An administrator (the API key, the local socket, or a server-edition admin
user) sees every item. A scoped caller — an agent token, or a non-admin
server-edition user session — sees only items whose subject is a server it
may enumerate, and never sees a client item at all.
