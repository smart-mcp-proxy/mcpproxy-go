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
`MCPPROXY_REGISTRY_<ID>_API_KEY` (ID upper-cased, non-alphanumerics → `_`). When a
key is configured it is sent on every request to that registry as an
`Authorization: Bearer <key>` header.

User-configured registries in `mcp_config.json` (`registries: [...]`) are **merged**
with these defaults (keyed by ID); a custom entry never drops the shipped set.

## Trust model & user-added registries

Every registry carries a **provenance** tag:

| Provenance | Meaning |
|---|---|
| `official/trusted` | A shipped, built-in default (the five above). |
| `custom/unverified` | Any registry the user added at runtime, or any non-default ID in `mcp_config.json`. |

Trust is **derived, not asserted** — it comes solely from whether the registry ID
is one of the shipped defaults. Writing `"provenance": "official/trusted"` into a
custom `mcp_config.json` entry has no effect; mcpproxy recomputes provenance on
every merge. **There is no allowlist a user can add themselves into.**

Consequences for `custom/unverified` registries:

- Servers discovered through them are **always quarantined** on add, regardless of
  the global quarantine default — and they can **never** set `skip_quarantine`
  (enforced in config validation *and* at server-add time). A server's origin is
  recorded on its config as `source_registry_id` / `source_registry_provenance`
  and surfaced in the approval/quarantine view.
- The `list_registries` output (MCP, REST, CLI) includes `provenance` and a
  `trusted` boolean so a UI can show a one-time third-party-registry warning.

### Adding your own registry source

`mcpproxy registry add-source` adds any https endpoint that implements the official
`modelcontextprotocol/registry` v0.1 protocol (the same protocol Copilot / VS Code /
Azure ship):

```bash
mcpproxy registry add-source https://registry.example.com
mcpproxy registry add-source https://registry.example.com --id acme --name "Acme Corp"
```

The ID is derived from the host when omitted; `--protocol` defaults to
`modelcontextprotocol/registry`. The source is always tagged `custom/unverified`.
This requires a running daemon — the registry list is updated copy-on-write on the
runtime config snapshot and persisted to `mcp_config.json`.

Equivalent surfaces:

- **REST:** `POST /api/v1/registries` with `{ "url": "https://…", "protocol": "…", "id": "…", "name": "…" }`.
- **CLI:** `mcpproxy registry add-source <https-url>`.
- **Web UI:** the **Repositories** page has an **Add Registry** button (URL + optional
  protocol/name). Each registry in the selector is flagged **Official · trusted** or
  **Third-party · unverified** from its `provenance`, and the first custom add shows a
  one-time third-party-registry warning (the acknowledgement is remembered locally).
- **macOS tray:** the **Registries** sidebar tab lists every configured registry
  with its provenance/trust badge, offers an **Add Registry** affordance,
  and shows a one-time third-party warning before the first custom add.

Errors share a stable code across surfaces: `invalid_registry_url` (400),
`registries_locked` (403), `registry_shadows_builtin` / `duplicate_registry` (409).
The Web UI maps each code to an actionable message.

### Enterprise: `registries_locked` (stub)

Setting `"registries_locked": true` in `mcp_config.json` disables runtime registry
additions (`registry add-source` and the REST/MCP add-source surface return
`registries_locked`). Built-in defaults are unaffected. This is a forward-looking
stub for enterprise policy pinning.

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
