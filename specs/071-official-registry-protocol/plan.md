# Implementation Plan — 071 Official MCP registry v0.1 protocol

Authoritative design: [MCP-856 plan, BOARD-DECIDED section](/MCP/issues/MCP-856#document-plan).

## Architecture

All discovery surfaces funnel through `registries.SearchServers` / `registries.FindServerByID` → protocol parser → `[]ServerEntry`, and all add surfaces funnel through `server.AddServerFromRegistryRef` → `buildToServerConfig`. `buildToServerConfig` already derives transport from the entry: `InstallCmd != ""` ⇒ `stdio`, else `URL`/`ConnectURL` ⇒ `http`. Therefore the #567 fix lives entirely in the parser deciding which field to set — every surface inherits the correct classification for free.

## Components

- `internal/registries/official.go` (new) — official v0.1 protocol: `fetchOfficialServers` (cursor follow-loop + versioned User-Agent), `parseOfficialPage` (envelope + status filter + legacy-flat fallback), `officialServerToEntry` (per-entry classification), `buildPackageCommand` (runtime-aware command synthesis), map helpers tolerant of camelCase/snake_case.
- `internal/registries/reference.go` (new) — `builtin/reference` source served in-binary (`referenceServers()`); 7 curated stdio servers.
- `internal/registries/search.go` — `fetchServers` short-circuits official (pagination) and reference (in-binary); query threaded through for server-side `search`; dispatch trimmed; bespoke parsers (`parseMCPRun`, `parseMCPStore`, `parseFleur`, `parseAzureMCPDemoWithoutGuesser`, `parseRemoteMCPServers`, `buildFleurInstallCmd`) and `constructServerURL` synthesis removed.
- `internal/config/config.go` — `DefaultRegistries()` rewired: official + reference + docker (kept) + pulse/smithery (opt-in `RequiresKey`).

## Classification rule (the #567 root fix)

| Input | InstallCmd | URL | ConnectURL | Transport |
|---|---|---|---|---|
| packages[] only | set | empty | — | stdio |
| remotes[] only | empty | set | — | http |
| hybrid | set (package) | empty | set (remote) | stdio |

## Pagination

`fetchOfficialServers` loops up to `officialMaxPages` (20 × `officialPageLimit` 100), following `metadata.nextCursor`, stopping on empty/repeated cursor.

## Test strategy (TDD)

- `official_test.go` — golden fixture (`testdata/official_v0.1_servers.json`): wrapped parse, status/isLatest filter, classification matrix, pagination (httptest two-page), reference offline.
- `server/consistency_official_test.go` — end-to-end #567 guard: packages→stdio / remotes→http identical across REST + CLI keystone.
- Updated `registry_data_test.go` / `integration_test.go` to the new default set.

## Risk

- Smithery (opt-in) uses the official protocol parser as a best-effort default; gated behind `RequiresKey`, so it is skipped without a key. Exact Smithery-shape parsing is a future refinement.
- Legacy flat `modelcontextprotocol/registry` responses still parse via the `parseOpenAPIRegistry` fallback inside `parseOfficialPage`.
