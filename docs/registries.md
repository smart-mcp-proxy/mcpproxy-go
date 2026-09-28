---
title: "MCP Server Registries"
sidebar_label: "Registries"
description: "The built-in set of MCP server registries used by search_servers and list_registries."
---

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

The shipped default set is exactly these **three** official, built-in entries. Earlier
versions also shipped `pulse`, `smithery`, `fleur`, `azure-mcp-demo`, and
`remote-mcp-servers` as defaults; these were removed. They are pruned from an
existing `mcp_config.json` on load (genuinely user-added custom registries are never
touched), so upgrading installs converge to the three above. `pulse` and `smithery`
can still be **added back** as custom sources (see *Adding your own registry source*);
when added they read `MCPPROXY_REGISTRY_PULSE_API_KEY` / `MCPPROXY_REGISTRY_SMITHERY_API_KEY`.

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
| `official` | A shipped, built-in default (the three above). |
| `custom` | Any registry the user added at runtime, or any non-default ID in `mcp_config.json`. |

Trust is **derived, not asserted** — it comes solely from whether the registry ID
is one of the shipped defaults. Writing `"provenance": "official"` into a
custom `mcp_config.json` entry has no effect; mcpproxy recomputes provenance on
every merge. **There is no allowlist a user can add themselves into.**

Provenance is **informational only** (it no longer changes quarantine behavior):

- Servers discovered through **any** registry — official or custom — follow the
  **global quarantine default** like everything else. With quarantine enabled (the
  secure default) a newly added server lands quarantined for review; provenance no
  longer force-quarantines or forbids `skip_quarantine`. A server's origin is still
  recorded on its config as `source_registry_id` / `source_registry_provenance`
  and surfaced in the approval/quarantine view.
- The `list_registries` output (MCP, REST, CLI) includes `provenance` and a
  `trusted` boolean (derived `official == trusted`) so a UI can show a neutral
  **Official / Custom** badge.
- **Migration:** earlier builds persisted the two-word tags `official/trusted` /
  `custom/unverified`; these are normalized to `official` / `custom` on read, so
  an existing `config.db` / `mcp_config.json` keeps working unchanged.

### Adding your own registry source

`mcpproxy registry add-source` adds any https endpoint that implements the official
`modelcontextprotocol/registry` v0.1 protocol (the same protocol Copilot / VS Code /
Azure ship):

```bash
mcpproxy registry add-source https://registry.example.com
mcpproxy registry add-source https://registry.example.com --id acme --name "Acme Corp"
```

The ID is derived from the host when omitted; `--protocol` defaults to
`modelcontextprotocol/registry`. The source is always tagged `custom`.
This requires a running daemon — the registry list is updated copy-on-write on the
runtime config snapshot and persisted to `mcp_config.json`.

**SSRF guard (CWE-918).** Because the daemon fetches a URL you supply, registry
fetches are constrained so a malicious or typo'd source cannot turn the daemon
into a request-forgery vector against internal services:

- The URL must be `http`/`https` (add-source/edit require `https`).
- A source whose host is a **literal IP in a non-routable range** — loopback
  (`127.0.0.0/8`, `::1`), RFC1918 private (`10/8`, `172.16/12`, `192.168/16`),
  IPv6 unique-local (`fc00::/7`), RFC6598 CGNAT (`100.64/10`), or link-local
  including the cloud metadata endpoint `169.254.169.254` — is **rejected up
  front** with `invalid_registry_url`.
- Hostname sources are allowed at add time and re-checked before every fetch by
  an **application-layer resolution check** (resolve the target host, reject if
  any resolved IP is non-routable) *and* at **dial time** (validate the actual
  IP before connect). The dial check alone is bypassable when an
  `HTTP_PROXY`/`HTTPS_PROXY` is configured — the transport then dials the proxy,
  not the target — so the application-layer check is what holds in the proxy
  case; the dial check remains defense-in-depth for the direct path and DNS
  rebinding. The official protocol's cursor-follow pagination is also pinned to
  the configured host so a hostile `nextCursor` cannot redirect the request.

The top-level `allow_private_registry_fetch` flag (default `false`) is a **blanket
opt-out**: setting it `true` disables this guard for **every** non-routable range
at once — loopback, RFC1918/CGNAT private, link-local **and** the
`169.254.169.254` cloud-metadata endpoint. It cannot be scoped to loopback only,
so enabling it for a localhost dev registry also re-opens the cloud-metadata SSRF
vector; enable it only for trusted local/dev use, ideally on hosts with no
cloud-metadata exposure. The change takes effect on daemon (re)start / config
reload. See [Configuration](configuration/config-file.md).

Equivalent surfaces:

- **REST:** `POST /api/v1/registries` with `{ "url": "https://…", "protocol": "…", "id": "…", "name": "…" }`.
- **CLI:** `mcpproxy registry add-source <https-url>`.
- **Web UI:** Settings → **Catalog Sources** has an **Add Registry** button (URL + optional
  protocol/name) and lists every configured source as a
  card with a neutral **Official / Custom** badge (official cards also carry a
  **Built-in** tag). There is no warning gate — adding a custom source goes straight
  through. Custom cards expose a **kebab (⋮) menu** with **Edit** (reuses the
  add dialog, pre-filled, id read-only) and **Delete** (destructive confirmation);
  official cards are read-only. Server discovery itself lives in the catalog-first
  **Add Server** page (`/add-server`, Catalog tab), not here.
- **macOS tray:** Settings → **Catalog Sources** lists every configured registry
  with its provenance/trust badge and offers an **Add Registry** affordance;
  server discovery lives in the Add Server sheet's **Catalog** tab (aggregated
  across every enabled source, same as the Web UI).

Errors share a stable code across surfaces: `invalid_registry_url` (400),
`registries_locked` (403), `registry_shadows_builtin` / `duplicate_registry` (409).
The Web UI maps each code to an actionable message.

### Removing a registry source

`mcpproxy registry remove <id>` deletes a custom registry you added earlier. Only
`custom` registries can be removed — the shipped built-in defaults are
refused via the same shadow guard as add-source. Removing a source does not touch
any upstream servers you already added from it.

```bash
mcpproxy registry list             # find the id
mcpproxy registry remove acme      # delete the custom source (aliases: rm, remove-source)
```

Like add-source, this requires a running daemon — the change is applied
copy-on-write on the runtime config snapshot and persisted to `mcp_config.json`.

Equivalent surfaces:

- **REST:** `DELETE /api/v1/registries/{id}` → `{ "registry": { … } }` echoing the removed entry.
- **CLI:** `mcpproxy registry remove <id>`.
- **Web UI:** the custom registry card's kebab (⋮) → **Delete**, behind a destructive confirmation modal.

Errors share a stable code across surfaces: `registry_not_found` (404),
`registry_shadows_builtin` (409, built-in cannot be removed),
`registries_locked` (403).

### Editing a registry source

`mcpproxy registry edit <id>` updates a custom registry you added earlier — its
display name, base URL, or servers-collection URL. Only `custom` registries can be
edited; the shipped built-in defaults are refused via the same shadow guard as
add/remove-source. Omitted flags leave the existing value unchanged. Changing
`--url` re-derives the servers URL unless `--servers-url` is also given.

```bash
mcpproxy registry edit acme --url https://new.acme.example.com   # change the URL
mcpproxy registry edit acme --name "Acme Corp"                   # change the display name
```

Like add/remove-source, this requires a running daemon — the change is applied
copy-on-write on the runtime config snapshot and persisted to `mcp_config.json`.

Equivalent surfaces:

- **REST:** `PUT /api/v1/registries/{id}` with `{ "name": "…", "url": "https://…", "servers_url": "https://…" }` (all optional) → `{ "registry": { … } }` echoing the updated entry.
- **CLI:** `mcpproxy registry edit <id> [--name … --url … --servers-url …]`.
- **Web UI:** the custom registry card's kebab (⋮) → **Edit**, which reuses the add dialog pre-filled with the current name/URL (id shown read-only).

Errors share a stable code across surfaces: `registry_not_found` (404),
`registry_shadows_builtin` (409, built-in cannot be edited),
`invalid_registry_url` (400), `registries_locked` (403).

### Enterprise: `registries_locked` (stub)

Setting `"registries_locked": true` in `mcp_config.json` disables runtime registry
changes (`registry add-source` / `registry remove` and the REST add-source and
remove surfaces return `registries_locked`). Built-in defaults are unaffected.
This is a forward-looking stub for enterprise policy pinning.

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

## Catalog popularity signal

The catalog's `Popular` landing section (and the popularity tiebreak in
search ranking) is backed by two real, source-native signals — no synthetic
scoring:

- **GitHub stars** for any hit whose `source_code_url` resolves to a
  `github.com/<owner>/<repo>` URL (the official registry and reference
  sources both carry one for almost every entry). Fetched from
  `GET https://api.github.com/repos/{owner}/{repo}`, cached (with ETag
  revalidation) for 24h, capped at 4 concurrent requests and a rolling
  budget of 50 requests/hour without a token or 4,000/hour with one.
- **Docker Hub pull counts** (`pull_count` from the `docker-mcp-catalog`
  registry's listing) map to `Installs`. Docker's own `star_count` is never
  used — it lives on a different scale from GitHub stars (single digits vs.
  tens of thousands) and the two are never summed or converted into one
  another.

Popularity fetching never blocks a search beyond a short bounded wait
(800ms by default): a cold miss is queued for a background fetch and simply
shows no stars on the current page, arriving on the next one. GitHub is
never listed in a search's `unavailable[]` — it is not a catalog source.

**`MCPPROXY_GITHUB_TOKEN`** (not the generic `GITHUB_TOKEN`, which is
deliberately never read) raises the GitHub rate-limit budget from 50 to
4,000 requests/hour. Set it if your catalog has many distinct repositories
and stars are taking a while to fill in.

**`MCPPROXY_CATALOG_POPULARITY=false`** (or `0`/`off`) disables all outbound
GitHub requests — useful for air-gapped or offline setups. Docker's
source-native install counts are unaffected either way, since they come from
the registry listing the Docker source already fetches, not from a separate
popularity call.

## Adding a discovered server

See [registry-add.md](features/registry-add.md). New servers are quarantined by
default until you approve them.
