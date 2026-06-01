# Contracts: Add-from-Registry across surfaces

**Feature**: 070-registry-easy-upstream-add · **Date**: 2026-05-31
All three surfaces funnel into the single core op `server.AddServerFromRegistry(ctx, req)` (CN-001). Identical input → identical persisted `config.ServerConfig` (CN-004).

## 1. REST (Web UI + curl)

### Add from registry result — **NEW**
```
POST /api/v1/registries/{registryId}/servers/{serverId}/add
Auth: X-API-Key
Body (optional):
  { "name": "github", "enabled": true, "env": { "GITHUB_TOKEN": "..." } }

200 OK:
  { "success": true,
    "data": { "server": { "name": "...", "protocol": "stdio|http",
                          "command": "...", "args": [...], "url": "...",
                          "quarantined": true } },
    "request_id": "..." }

400  no_install_info | missing_required_input | duplicate_name (JSON error + request_id)
404  registry_not_found | server_not_found
```

### Cache refresh — **NEW (FR-007)**
```
POST /api/v1/registries/{registryId}/refresh   → 200 { "refreshed": true, "age_seconds": 0 }
```

### Existing (unchanged, used by the flow)
```
GET /api/v1/registries                                   # list (merge defaults∪config — FR-006)
GET /api/v1/registries/{registryId}/servers?q=&tag=&limit=   # search; response gains age/stale (FR-007)
```
Search response **NEW** fields per registry: `"unavailable": true, "reason": "missing_key"` (FR-008); top-level `"cache": {"age_seconds": N, "stale": bool}`.

## 2. MCP (`upstream_servers` tool) — **NEW operation**

```jsonc
// operation enum gains "add_from_registry"
{ "operation": "add_from_registry",
  "registry": "pulse",            // required — registry id
  "id": "weather-server",         // required — server id within the registry
  "name": "weather",              // optional override
  "env_json": "{\"API_KEY\":\"...\"}" // optional; required if result declares inputs
}
// → standard upstream add result, quarantined: true
// errors: registry_not_found | server_not_found | no_install_info |
//         missing_required_input | duplicate_name (structured MCP error)
```
`search_servers` and `list_registries` tools unchanged in shape; `search_servers` results gain `required_inputs[]` and registry `unavailable` marking.

## 3. CLI — **NEW `registry` command group**

```
mcpproxy registry list                         # alias of: search-servers --list-registries
        [-o table|json|yaml]

mcpproxy registry search <query>               # alias of: search-servers --registry … --search …
        --registry <id>  [--tag <t>] [--limit N] [-o …]

mcpproxy registry add <registryId> <serverId>  # NEW — closes the loop on the CLI
        [--name <n>] [--env KEY=VALUE ...] [--enabled]
        # talks to running daemon via cliclient → POST /api/v1/registries/{id}/servers/{serverId}/add
        # prints: "Added '<name>' (quarantined — approve with: mcpproxy upstream approve <name>)"
        # errors mirror REST: no_install_info / missing_required_input / duplicate_name
```
`search-servers` retained as a back-compat top-level alias (no breakage). New group mirrors the `upstream` cmd pattern (`cmd/mcpproxy/upstream_cmd.go`): config+logger load, daemon detection, `cliclient` call, `internal/cli/output` formatter.

## Cross-surface consistency contract (CN-004 / FR-010)
A regression test (`internal/server/consistency_crosssurface_test.go`) MUST assert that adding the same `(registryId, serverId, env, name)` through the REST handler, the MCP handler, and the CLI add path yields byte-identical persisted `config.ServerConfig` (excluding the `Created` timestamp), all `Quarantined == true`.
