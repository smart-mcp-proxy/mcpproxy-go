# Adding Servers from Registries

MCPProxy can discover MCP servers in known registries and add them as upstream
servers without you hand-constructing a `command`/`args`/`url`. You reference a
server by `(registryId, serverId)`; the daemon re-derives the runnable config
from the registry entry and adds it **quarantined** so you can review it before
it is exposed to agents.

The same operation is available from the CLI, the REST API, and the MCP
`upstream_servers` tool. All three surfaces call one server-side core operation
(`AddServerFromRegistry`), so behaviour and error codes are identical everywhere
(spec 070, CN-001).

## Security model

- **The client never sends a config blob.** Only the registry reference plus
  optional overrides (`name`, `env`, `enabled`) cross the wire. The daemon
  re-derives `command`/`args`/`url` from the registry entry, so a caller cannot
  smuggle a different command or pre-approve the server (CN-001 / security
  decision D1).
- **Quarantined by default.** A freshly added server is quarantined until you
  approve it:
  ```bash
  mcpproxy upstream approve <name>
  ```
- **Required inputs are explicit.** If a registry entry declares required inputs
  (e.g. `${GITHUB_TOKEN}`) and you don't supply them, the add fails with the
  stable code `missing_required_input` and the exact input names — no partially
  configured server is persisted (FR-003).

## Discovering servers

Before adding, find the registry id and server id:

```bash
mcpproxy registry list                      # list configured registries + ids
mcpproxy registry search <query> -r <id>    # search one registry
mcpproxy registry search sqlite -r official --limit 5
```

`registry search` flags: `--registry/-r <id>`, `--tag/-t <tag>`,
`--limit/-l <n>` (default 10).

## CLI: `mcpproxy registry add`

```bash
mcpproxy registry add <registryId> <serverId> [flags]
```

Flags:

| Flag | Description |
|------|-------------|
| `--name <name>` | Override the server name |
| `--env KEY=VALUE` | Set an environment variable / required input (repeatable) |
| `--enabled` | Whether the added server is enabled (default `true`) |

Example:

```bash
mcpproxy registry add official io.github.example/github-mcp --env GITHUB_TOKEN=ghp_xxx
# ✅ Added 'github-mcp' (quarantined — approve with: mcpproxy upstream approve github-mcp)
```

Notes:

- `registry add` **requires a running daemon** — the keystone op is server-side.
  If the daemon isn't running you get a `connection_failed` error telling you to
  `mcpproxy serve`.
- Use `-o json` / `-o yaml` for machine-readable output of the added server.

## REST API

Base path: `/api/v1`. Authenticate with `X-API-Key` (see
[REST API](../api/rest-api.md)).

### Add a server from a registry

```
POST /api/v1/registries/{id}/servers/{serverId}/add
```

The registry id and server id come from the URL path. The optional JSON body
carries only overrides — never a config blob:

```json
{
  "name": "github-mcp",          // optional name override
  "env": { "GITHUB_TOKEN": "…" },  // overrides + required-input values
  "enabled": true                  // optional, defaults to true
}
```

Success (`200`):

```json
{
  "success": true,
  "data": {
    "server": {
      "name": "github-mcp",
      "protocol": "stdio",
      "command": "npx",
      "args": ["github-mcp"],
      "enabled": true,
      "quarantined": true
    }
  }
}
```

Failure carries the cross-surface error code:

```json
{
  "success": false,
  "code": "missing_required_input",
  "message": "…",
  "missing_inputs": ["GITHUB_TOKEN"]
}
```

`missing_inputs` is present only for `code == "missing_required_input"`.

### Refresh a registry's cache

Drop a registry's cached server lists so the next discovery re-fetches them
(FR-007):

```
POST /api/v1/registries/{id}/refresh
```

Response:

```json
{ "registry_id": "official", "cleared": 3 }
```

`cleared` is the number of cached entries dropped.

### Browsing endpoints

- `GET /api/v1/registries` — list registries.
- `GET /api/v1/registries/{id}/servers` — search a registry's servers.

## MCP tool

Use the built-in `upstream_servers` tool with `operation: "add_from_registry"`:

| Parameter | Required | Description |
|-----------|----------|-------------|
| `registry` | yes | Registry id (e.g. `official`). Discover via the `list_registries` / `search_servers` tools |
| `id` | yes | Server id within the registry |
| `name` | no | Name override |
| `env_json` | no | JSON object of env / required-input values, e.g. `{"GITHUB_TOKEN":"…"}` |
| `enabled` | no | Defaults to `true`; pass `false` to add disabled |

The server re-derives the runnable config and quarantines it. On a missing
required input the tool returns a structured error result with the same `code`
and `missing_inputs` as the REST and CLI surfaces (CN-001).

## See also

- [CLI management commands](../cli/management-commands.md)
- [Security & quarantine](security-quarantine.md)
- [Search & discovery](search-discovery.md)
- [REST API reference](../api/rest-api.md)
