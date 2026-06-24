# In-Proxy Profiles (Spec 057)

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

## Activity logging

Tool-call activity records from profile URLs carry `metadata["profile"] = "<slug>"`. Records from `/mcp` omit this field.

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
