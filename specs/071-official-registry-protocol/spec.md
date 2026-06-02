# Feature Spec — Official MCP registry v0.1 protocol + standardized default registries

**Spec:** 071-official-registry-protocol
**Issue:** [MCP-865](/MCP/issues/MCP-865) (keystone backend) under goal [MCP-856](/MCP/issues/MCP-856)
**Status:** Implemented (design board-decided 2026-06-02 in [MCP-856 plan](/MCP/issues/MCP-856#document-plan); Glass-Cockpit Gate 2 satisfied at parent level)
**Resolves by design:** GH #566 (Pulse 410), GH #567 (Fleur all-remote misclassification)

## Why

mcpproxy's registry discovery shipped a stale, bespoke set of registries with hand-written per-registry parsers and URL synthesis. Two production bugs trace directly to that design:

- **#566** — Pulse's `v0beta` endpoint is 410-dead.
- **#567** — a server with a non-empty `remotes[]` was classified as *remote* even when it also had local `packages[]`, so the add path synthesized a bogus `docker://`/`npx://` HTTP transport (the #483 failure class).

The official Model Context Protocol registry (`registry.modelcontextprotocol.io/v0.1/servers`) is now the canonical aggregator. Adopting its protocol replaces most bespoke parsers and fixes the classification bug at the root.

## User scenarios

1. **Zero-config discovery.** A fresh install can `retrieve_tools`/`registry search` against the official registry with no API key.
2. **Correct transport per server.** A server published as an npm/pypi/oci package is added as a **stdio** server (install command, no URL); a server published only as a remote endpoint is added as an **http** server. A hybrid server prefers its local package.
3. **Offline basics.** The `@modelcontextprotocol` reference servers (filesystem, fetch, memory, time, git, sequentialthinking, everything) are discoverable even with no network, because they currently 404 on the official registry (modelcontextprotocol/servers#3047).
4. **Opt-in proprietary registries.** Pulse and Smithery remain available but require an API key (`RequiresKey`), so they never break a default search.

## Functional requirements

- **FR-1 Official v0.1 parser.** Parse the wrapped `{ "server": <server.json>, "_meta": {...} }` envelope. Read `_meta["io.modelcontextprotocol.registry/official"].status`/`.isLatest`; **skip `deleted`/`deprecated`/non-latest** by default. Tolerate camelCase wire fields (`registryType`, `runtimeHint`, `nextCursor`, `isLatest`) and snake_case fallbacks.
- **FR-2 Cursor pagination.** Follow `metadata.nextCursor` (opaque) with `cursor`+`limit`, bounded by a page cap (`officialMaxPages`). Pass through `version=latest` and an optional `search` query.
- **FR-3 Per-entry classification (the #567 fix).** Classify per transport entry, never "remotes present ⇒ remote":
  - `server.packages[]` → **LOCAL/stdio**: command = `runtimeHint` + `runtimeArguments[]` + `identifier`(`@version` for npm) + `packageArguments[]`; set `InstallCmd`, leave `URL` empty; `environmentVariables[]` → `RequiredInputs`.
  - `server.remotes[]` → **REMOTE**: `type`+`url` → `URL`; `headers[]` → `RequiredInputs`.
  - **Hybrid** → prefer the package for stdio, keep the remote as `ConnectURL`.
- **FR-4 Default-registry rewire.** Ship: Official (primary, no key), built-in Reference, Docker MCP catalog (kept). Demote Pulse + Smithery to opt-in (`RequiresKey`). Drop Azure-demo (`mcp/v0`), mcp.run, mcpstore, Fleur, remote-mcp-servers. Remove their bespoke parsers and all `constructServerURL` URL synthesis.
- **FR-5 Curated reference source.** Built-in `builtin/reference` registry served in-binary (no network), all stdio.
- **FR-6 Cross-surface consistency.** A packages-only server reads `stdio` and a remotes-only server reads `remote` identically across MCP, REST, and CLI add surfaces (mirrors spec 070).

## Out of scope (delegated)

- User-added registries + provenance/trust model + `registry add-source` CLI → [MCP-866](/MCP/issues/MCP-866) (blocked by this).

## Acceptance

- Official registry shipped + searchable zero-config; `registry search/list/add` work against it.
- packages[]→stdio, remotes[]→remote across all surfaces (regression-tested).
- Dropped/demoted registries handled per the board-decided §4 set.
- Reference set discoverable offline.
- #566 + #567 resolved by design.
