# MCP Server Registries

mcpproxy discovers MCP servers through a built-in set of registries. Discovery is
available via the `search_servers` / `list_registries` MCP tools, the
`mcpproxy registry search|list|add` CLI, and the REST `/api/v1/registries` routes.

## Default registries

| ID | Name | Protocol | Key required | Notes |
|----|------|----------|--------------|-------|
| `official` | Official MCP Registry | `modelcontextprotocol/registry` | no | Primary, zero-config aggregator (`registry.modelcontextprotocol.io/v0.1/servers`). |
| `reference` | Reference Servers | `builtin/reference` | no | Curated `@modelcontextprotocol` servers, **shipped in-binary** so the basics work offline. |
| `docker-mcp-catalog` | Docker MCP Catalog | `custom/docker` | no | Signed-container MCP server inventory. |
| `pulse` | Pulse MCP | `custom/pulse` | **yes** | Opt-in; set `MCPPROXY_REGISTRY_PULSE_API_KEY`. |
| `smithery` | Smithery | `modelcontextprotocol/registry` | **yes** | Opt-in; set `MCPPROXY_REGISTRY_SMITHERY_API_KEY`. |

Key-requiring registries are **skipped** (not failed) when no key is configured, so
a default search always succeeds. The API-key env var is
`MCPPROXY_REGISTRY_<ID>_API_KEY` (ID upper-cased, non-alphanumerics → `_`).

User-configured registries in `mcp_config.json` (`registries: [...]`) are **merged**
with these defaults (keyed by ID); a custom entry never drops the shipped set.

## Official v0.1 protocol

The official registry returns a cursor-paginated list of wrapped entries:

```json
{ "servers": [ { "server": { /* server.json */ }, "_meta": { "io.modelcontextprotocol.registry/official": { "status": "active", "isLatest": true } } } ],
  "metadata": { "nextCursor": "..." } }
```

mcpproxy:

- descends into each `.server`, and **skips** entries whose `_meta` status is
  `deleted`/`deprecated` or that are not `isLatest`;
- follows `metadata.nextCursor` (bounded) and passes through `version=latest` and an
  optional `search` query.

## Transport classification (local vs remote)

Classification is **per transport entry** — never "has remotes ⇒ remote" (the fix
for GH #567 / #483):

| server.json | Result |
|---|---|
| `packages[]` present | **stdio**: launch command derived from `runtimeHint` + `runtimeArguments` + `identifier`(`@version`) + `packageArguments`; `environmentVariables[]` become required inputs. No URL. |
| `remotes[]` only | **remote/http**: `type` + `url` become the connection endpoint; `headers[]` become required inputs. |
| both (hybrid) | the **package** is preferred (stdio); the remote endpoint is kept as a fallback connection URL. |

Because every add surface (MCP, REST, CLI) funnels through the same keystone, a
packages-only server is added as stdio and a remotes-only server as http
identically across all surfaces.

## Adding a discovered server

See [registry-add.md](features/registry-add.md). New servers are quarantined by
default until you approve them.
