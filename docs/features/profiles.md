---
title: "Profiles"
sidebar_label: "Profiles"
description: "Named subsets of upstream servers addressable as permanent URLs, selectable statefully via the set_profile tool."
---

# In-Proxy Profiles (Spec 057 · Profiles v2)

> Profiles v1 (Spec 057) is **stateless, URL-based**: a request to `/mcp/p/<slug>` is scoped to that profile for that request. Profiles v2 adds **stateful** selection via the `set_profile` tool, a **shared resolver** with a clear precedence, and a **REST surface** for UI clients.

Profiles are named, stateless subsets of upstream servers addressable as permanent URLs at `/mcp/p/<slug>`.

## Quick start

```json
{
  "profiles": [
    { "name": "research", "servers": ["arxiv", "wikipedia"] },
    { "name": "deploy",   "servers": ["k8s", "github"] }
  ]
}
```

Each profile gets a permanent MCP endpoint:

| URL | Effective servers |
|-----|------------------|
| `/mcp` | All configured servers (unchanged) |
| `/mcp/p/research` | `arxiv`, `wikipedia` only |
| `/mcp/p/deploy` | `k8s`, `github` only |

## Rules

- **Slug format**: `^[a-z0-9][a-z0-9_-]{0,62}$` (max 63 chars, lowercase)
- **Reserved slugs**: `all`, `code`, `call`, `p` — cannot be used as profile names
- **Duplicate names**: rejected at load time (fatal validation error)
- **Unknown server references**: warn-and-skip (non-fatal); the server is excluded from the effective set
- **Empty server list**: legal "deny everything" profile (no tools exposed)

## Behaviour

- `retrieve_tools` returns only tools from the profile's servers
- `call_tool_read/write/destructive` into an out-of-profile server is rejected with:
  `server '<name>' is not in profile '<slug>'`
- `upstream_servers list` at a profile URL excludes out-of-profile servers
- `code_execution` at a profile URL runs with the profile-intersected server set
- Per-server `enabled_tools`/`disabled_tools` continue to apply inside a profile (no profile-level tool overrides)

## Scope composition

Profile filtering is independent of agent-token scope. An unauthenticated connection at `/mcp/p/<slug>` is still profile-filtered. When both a profile and an agent token are present, the effective server set is their intersection.

Error attribution:
- Out-of-profile server → `server '<s>' is not in profile '<slug>'`
- Out-of-token server   → `Server '<s>' is not in scope for this agent token`

## Stateful selection — `set_profile` (Profiles v2)

The `set_profile` MCP tool switches the active profile **inside a live session** — no reconnect, no re-index:

```jsonc
// request
{ "name": "set_profile", "arguments": { "profile": "research" } }
// result
{ "active_profile": "research", "servers": ["research-srv"] }
```

- The selection is keyed by the MCP session id (stable per streamable-HTTP / SSE connection) and persists for the lifetime of that session.
- It applies to subsequent `retrieve_tools`, `call_tool_*`, `code_execution` and direct-mode (`server__tool`) calls on the base `/mcp` endpoint — `retrieve_tools` searches the profile's per-profile index directly.
- Passing an empty string (`""`) clears the selection and returns to all servers. `active_profile` always reports the **stored session selection** — `""` after a clear, even for a token with a [`profile_pin`](./agent-tokens.md#profile-pinning) — while `servers` reports the **effective scope** the session can actually reach after the update: the pin's servers for a pinned token (nothing once the pinned profile has been deleted), the URL profile on a `/mcp/p/<slug>` endpoint, otherwise the selection or every configured server.
- The `servers` list is always bounded by the caller's credential, using the same rule that scopes `retrieve_tools`: for an [agent token](./agent-tokens.md) scoped to specific servers it is the intersection of the effective profile (resolved pin > URL > session, see [Resolution precedence](#resolution-precedence)) with the token's `allowed_servers`, so a token restricted to one server is never told about the others. On a `/mcp/p/<slug>` endpoint the URL still governs the request, so `set_profile("other")` there stores `other` as `active_profile` but reports `<slug> ∩ allowed_servers` in `servers`. API-key and socket callers see the full lists.
- An unknown slug is rejected. An administrator (API key, socket, anonymous back-compat) gets the discovery affordance: `unknown profile '<slug>' (available: research, deploy)`. An agent token gets `unknown profile '<slug>'` with no list at all: it may select only the profiles overlapping its `allowed_servers` (or its pin while the pin still has reach), and a profile entirely outside its reach (an empty profile, a profile whose servers are all outside `allowed_servers`, or the token's own pin once it no longer exists or no longer overlaps the token's servers) is rejected with that same error rather than confirmed as existing. The check looks only at the requested slug (and the token's pin) and tests the token's own `allowed_servers` against that profile's precomputed server set — its cost does not depend on how many other profiles are configured, on how many servers the requested profile declares or on how many servers are configured at all, only on the size of the token's own grant — so a token cannot learn which profiles or servers exist from `set_profile`, by body or by timing. A pinned token asking for any profile other than its pin is stopped by the pin check first (`agent token is pinned to profile '<pin>' and cannot switch to '<slug>'`, see [profile pinning](./agent-tokens.md#profile-pinning)) — that message names the pin the token was minted with, never the requested profile's existence.
- Session state is cleared automatically on session close.

`set_profile` is available on the default `/mcp` server and the `call_tool` / `code_execution` routing-mode servers.

### Resolution precedence

When more than one source could select a profile, the effective profile for a request is resolved highest-wins:

| # | Source | Scope |
|---|--------|-------|
| 1 | Agent-token [`profile_pin`](./agent-tokens.md#profile-pinning) | Server-enforced, immutable for the connection. If the pinned profile has been deleted, the request resolves to a **deny-all** scope rather than falling to the tiers below. |
| 2 | URL `/mcp/p/<slug>` | Explicit and authoritative **for that request** — overrides the session default. |
| 3 | `set_profile` session selection | The default for the base `/mcp` endpoint for the session lifetime. |
| 4 | None | No filtering (admin / all servers). |

So a request that arrives via `/mcp/p/<other>` is scoped to `<other>` even if the session previously ran `set_profile`; a session selection that no longer matches any configured profile is treated as stale and dropped. A stale **token pin** is not dropped the same way — a pin is a restriction an operator applied to a credential, so it fails closed (deny-all) instead of widening back to the token's own scope.

## REST API

For Web UI and tray surfaces:

| Method & path | Description |
|---------------|-------------|
| `GET /api/v1/profiles` | List profiles, each `{ name, servers, tool_count }` (effective servers + indexed tool count). |
| `GET /api/v1/profiles/active` | Read the server-level default active profile (`{ "active_profile": "<slug>" }`; `""` = all servers). |
| `PUT /api/v1/profiles/active` | Set the default active profile. Body `{ "profile": "<slug>" }` (or `""` to clear). Unknown slug → `404`. |

The REST "active profile" is a **server-level default for UI surfaces** — it is independent of, and does not override, a live MCP session's `set_profile` selection (which is per-session). All responses use the standard `{ "success", "data" }` envelope and require the API key.

## Activity logging

Tool-call activity records carry the **effective** profile slug at top-level `metadata["profile"]` — set by a `/mcp/p/<slug>` URL or a `set_profile` session selection. Records with no active profile omit this field.

## Per-profile search index

Each profile gets a physically separate Bleve index so switching profiles is fast and a config reload that changes one profile does not re-index the others.

Layout under the data dir (`~/.mcpproxy/` by default):

```
index.bleve/                 # shared default index — all servers' tools (used by /mcp)
index.bleve/profiles/<slug>/ # one index per profile — only that profile's servers' tools
```

Notes:

- Per-profile indexes live under `index.bleve/profiles/` (not directly under `index.bleve/<slug>/`) so they never collide with Bleve's own internal files and `store/` subdirectory.
- A per-profile index is a derived view: it is (re)built from the shared default index, so the shared index remains the source of truth and the allow-all fallback for `/mcp`.
- `<slug>` is the validated profile name (`^[a-z0-9][a-z0-9_-]{0,62}$`), so the directory name is always filesystem-safe.

Lifecycle:

| Event | Effect |
|-------|--------|
| Profile added / first use | Its index is built lazily from the shared index. |
| A member server's tools change | Only the profiles that include that server are rebuilt. |
| Profile membership changes on reload | Only the affected profile is rebuilt; others are untouched. |
| Profile removed from config | Its index directory is deleted (including orphans left by a prior run). |
| Server disabled / quarantined | Profiles that include it are refreshed so its tools drop out. |

## Hot reload

Profile changes take effect for new connections on the next config reload. In-flight sessions keep their snapshot.

## 404 responses

For API-key, socket and (when `require_mcp_auth` is off) unauthenticated callers:

| Condition | Body |
|-----------|------|
| No profiles configured | `{"error":"no profiles configured"}` |
| Unknown slug | `{"error":"unknown profile '<slug>'","available":["research","deploy"]}` |

An [agent token](./agent-tokens.md) may initialize through `/mcp/p/<slug>` only when that profile is one it could select with `set_profile` — its servers overlap the token's `allowed_servers`, or it is the token's pin and the pin still has reach. Every other request — a missing or deleted slug, a configured profile outside the token's reach, an empty profile, a pin mismatch, the slug-less `/mcp/p` and `/mcp/p/`, and an empty fleet — receives one and the same `404 {"error":"unknown profile '<slug>'"}` with no `available` list, and the check itself looks only at the requested slug (and the token's pin), testing the token's own `allowed_servers` against that profile's precomputed server set — its cost does not depend on how many other profiles are configured, on how many servers the requested profile declares, on how many servers are configured at all, or on whether this is the first request after a reload (the profile index is rebuilt when the configuration changes, not on demand); it scales only with the size of the token's own grant — so a scoped caller cannot learn which profiles or servers exist from the profile URL, by body or by timing. The refusal is silent towards the agent only: each one is logged (`profile URL refused for scoped caller`, with the token name, the requested slug and the remote address) so an operator can spot a token probing the slug space.
