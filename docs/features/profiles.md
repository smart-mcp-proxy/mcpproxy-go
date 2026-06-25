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
- It applies to subsequent `retrieve_tools`, `call_tool_*`, and `code_execution` calls on the base `/mcp` endpoint — `retrieve_tools` searches the profile's per-profile index directly.
- Passing an empty string (`""`) clears the selection and returns to all servers (the result lists every configured server).
- An unknown slug is rejected: `unknown profile '<slug>' (available: research, deploy)`.
- Session state is cleared automatically on session close.

`set_profile` is available on the default `/mcp` server and the `call_tool` / `code_execution` routing-mode servers.

### Resolution precedence

When more than one source could select a profile, the effective profile for a request is resolved highest-wins:

| # | Source | Scope |
|---|--------|-------|
| 1 | Agent-token `profile_pin` | Server-enforced, immutable for the connection. *(Hook reserved for Profiles v2 T3; inert until then.)* |
| 2 | URL `/mcp/p/<slug>` | Explicit and authoritative **for that request** — overrides the session default. |
| 3 | `set_profile` session selection | The default for the base `/mcp` endpoint for the session lifetime. |
| 4 | None | No filtering (admin / all servers). |

So a request that arrives via `/mcp/p/<other>` is scoped to `<other>` even if the session previously ran `set_profile`; a session selection that no longer matches any configured profile is treated as stale and dropped.

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

| Condition | Body |
|-----------|------|
| No profiles configured | `{"error":"no profiles configured"}` |
| Unknown slug | `{"error":"unknown profile '<slug>'","available":["research","deploy"]}` |
