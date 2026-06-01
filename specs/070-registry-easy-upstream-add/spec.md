# Feature Specification: Registry — Make MCP Server Discovery Actual & Easy to Add to Upstream

**Feature Branch**: `070-registry-easy-upstream-add`
**Created**: 2026-05-31
**Status**: Draft
**Lineage**: H2-2026 roadmap PILLAR A (Adopt). Extends the existing registry subsystem (`internal/registries/`) and the unified upstream-add path. Related but distinct: spec 025 (import-config), GitHub #55 / spec 057 (per-client profiles). Paperclip goal `da399902`.
**Input**: Make the MCP server registry current and make adding a discovered server to your upstream config reliable and easy across ALL three surfaces — Web UI, MCP tools, and CLI — each tested. Close the loop so discovery and add are connected everywhere, sharing one path, with quarantine-by-default preserved.

## Clarifications

### Session 2026-05-31

- Q: Is registry search missing? → A: No — search exists for 8 registries (live API + 24h cache) and the add-to-upstream path is unified through one core method that quarantines by default. The work is closing UX gaps + reaching parity, not building search.
- Q: What are the actual gaps? → A: (1) the **CLI has no registry search/add command at all**; (2) the **Web UI can search (Repositories page) but cannot one-click-add** — discovery and the Add-Server form are disconnected; (3) the registry list is **hardcoded and rebuild-only**, and a default registry needing an API key errors when unconfigured.
- Q: Add-from-registry today via MCP? → A: Works, but the agent must hand-construct the upstream config from a search result; a convenience "add from registry result" mode is desired.
- Q: Does quarantine-by-default hold on add? → A: Yes, on every surface (they share the core add path); this MUST be preserved as an invariant.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Add a discovered server to upstream from the Web UI in one flow (Priority: P1)

A user opens the Web UI, searches the registries for a server (e.g. "github"), sees results, and clicks **Add** — the server is added to their upstream config (quarantined for review) without leaving the page or re-typing the command. Today they can search on the Repositories page but must copy a command and re-enter it in a separate Add-Server form.

**Why this priority**: The Web UI is the primary surface for most users, and the broken search→add loop is the biggest friction. Closing it delivers the headline "easy to add from registry" value.

**Independent Test**: In the Web UI, search a registry, click Add on a result; confirm the server appears in the servers list, quarantined, with a correct config (command/args or url, transport), with no manual re-entry.

**Acceptance Scenarios**:

1. **Given** registry search results in the Web UI, **When** the user clicks Add on a result, **Then** the server is added to upstream (quarantined) with a valid config derived from the result.
2. **Given** a just-added server, **When** the user views the servers list, **Then** it appears quarantined and pending approval.
3. **Given** a result that needs required input (e.g. an env var/API key), **When** adding, **Then** the user is prompted for it rather than silently adding a broken server.

---

### User Story 2 — Discover and add from the CLI (Priority: P1)

A user (or a script/agent in a terminal) lists registries, searches for a server, and adds it to upstream — entirely from the CLI. Today none of this exists on the CLI; adding means hand-editing the config file or going through the MCP protocol.

**Why this priority**: The CLI is the automation surface and currently has a total gap (no `search`, no `registry`, no `add-from-registry`). Parity here is the clearest net-new value and serves the user's explicit "test CLI" requirement.

**Independent Test**: Run the new CLI commands to list registries, search, and add a server from a registry; confirm the server then appears in `mcpproxy upstream list`, quarantined.

**Acceptance Scenarios**:

1. **Given** the CLI, **When** the user runs the registry-list command, **Then** the available registries are listed.
2. **Given** a search query, **When** the user runs the search command, **Then** normalized results (name, source, install info) are shown.
3. **Given** a chosen result, **When** the user runs the add-from-registry command, **Then** the server is added to upstream (quarantined) and appears in `upstream list`.

---

### User Story 3 — Add from registry via MCP tools without hand-constructing config (Priority: P2)

An AI agent searches via `search_servers`, then adds a chosen result to upstream by referencing it (registry + server identifier) rather than re-assembling the command/args/url by hand. The backend reconstructs the validated config.

**Why this priority**: Reduces agent error and token cost on the MCP path (which already functions but requires manual translation). P2 because the MCP add path works today; this is an ergonomics upgrade.

**Independent Test**: Via the MCP protocol, search then add-from-result; confirm the resulting upstream entry matches what manual construction would produce, quarantined.

**Acceptance Scenarios**:

1. **Given** a `search_servers` result, **When** the agent adds it by registry+identifier, **Then** the backend builds the correct upstream config and quarantines it.
2. **Given** the same logical server, **When** added via MCP vs Web UI vs CLI, **Then** the resulting upstream entry is identical.

---

### User Story 4 — Keep the registry list current and resilient (Priority: P2)

A user can add their own/private registry without rebuilding mcpproxy, manually refresh stale registry data, see how fresh results are, and not be blocked by a registry that needs an unconfigured API key (it's skipped/marked, not erroring).

**Why this priority**: "Make registries actual" — a hardcoded, rebuild-only list with silent key failures undermines trust in discovery. P2 because the add-loop (US1–3) is the dominant value; freshness/config makes it durable.

**Independent Test**: Add a registry via config (no rebuild) and confirm it appears in search; trigger a refresh and confirm cache age updates; configure no key for a key-requiring registry and confirm it's skipped/marked rather than failing the whole search.

**Acceptance Scenarios**:

1. **Given** a user-defined registry in config, **When** searching, **Then** that registry is included alongside built-in defaults (no rebuild).
2. **Given** cached registry data, **When** the user refreshes, **Then** fresh data is fetched and cache age is reflected.
3. **Given** a registry requiring an absent API key, **When** searching, **Then** it is skipped/marked unavailable and other registries still return results.

## Requirements *(mandatory)*

### Context & Constraints (locked)

- **CN-001**: Preserve the unified add path — all surfaces MUST funnel into the one core add-upstream operation; do not create surface-specific add logic that could diverge.
- **CN-002**: Quarantine-by-default on add MUST hold on every surface (Constitution IV).
- **CN-003**: Extend the existing `internal/registries/` subsystem; do not build a parallel registry system.
- **CN-004**: A server added via any surface MUST produce an identical, valid upstream config entry (consistency invariant).
- **CN-005**: All three surfaces (Web UI, MCP, CLI) plus the REST backend MUST be tested.

### Functional Requirements

- **FR-001**: Provide a unified "add from registry result" capability in the core: given a registry id + server identifier, the backend reconstructs the validated upstream config (command/args or url, transport, env) via the existing result-normalization and adds it (quarantined).
- **FR-002**: The Web UI MUST connect search → add: a result in the discovery/Repositories flow (or an Add-Server "from registry" tab) has an Add action that calls the unified add path; no manual re-entry of the command.
- **FR-003**: The Web UI MUST prompt for required inputs (e.g. env vars / API keys a result declares) before adding, rather than adding a broken server.
- **FR-004**: The CLI MUST provide: list registries, search registries (by query, with registry/tag/limit filters), and add a server to upstream from a registry result (and a manual add via command/args or url).
- **FR-005**: The MCP `upstream_servers` tool MUST support an "add from search result" mode (registry + identifier) so agents need not hand-construct the config.
- **FR-006**: The registry list MUST be configurable — user-defined registries merge with built-in defaults without a rebuild.
- **FR-007**: The system MUST support manual refresh/invalidation of registry cache and surface cache age/freshness in results.
- **FR-008**: A registry that cannot be queried (missing key, unreachable) MUST be skipped/marked unavailable without failing the overall search.
- **FR-009**: Across all surfaces, an added server MUST be quarantined by default and appear pending approval.
- **FR-010**: The add-from-registry path MUST be covered by tests on all three surfaces (MCP protocol, CLI, Web UI via Playwright) plus a REST/curl backend test, and a regression test asserting identical upstream entries across surfaces (CN-004).

### Key Entities

- **Registry**: a discovery source (id, url, tags, transport hint, optional key requirement); built-in defaults + user-defined, merged.
- **Server search result (normalized)**: name, description, source registry, install info (command/args or url), declared required inputs.
- **Unified add operation**: result (or manual fields) → validated upstream config → quarantined upstream entry.
- **Registry cache**: cached per-registry results with an age/freshness indicator and manual refresh.

## Success Criteria *(mandatory)*

- **SC-001**: A Web UI user can go from registry search to an added (quarantined) upstream server in one flow, no manual command re-entry.
- **SC-002**: A CLI user can list registries, search, and add a server to upstream — commands that do not exist today.
- **SC-003**: An agent can add a searched server via MCP by reference, without hand-building the config.
- **SC-004**: The same logical server added via Web UI, CLI, and MCP yields an identical upstream config entry, quarantined in all three.
- **SC-005**: A user-defined registry appears in search without rebuilding mcpproxy.
- **SC-006**: A registry needing an unconfigured key does not break search; other registries still return results, and its unavailability is visible.
- **SC-007**: Registry results show freshness/cache age and can be manually refreshed.
- **SC-008**: All three surfaces + REST are covered by passing tests, including the cross-surface consistency regression.

## Assumptions

- Registry search exists for ~8 registries with a 24h cache, and the add-to-upstream path is unified through one core method that quarantines by default (confirmed in research).
- The Web UI Repositories page can search and `AddServerModal` is the add form; wiring them is UI work over existing REST endpoints.
- The CLI has `upstream list/logs/restart/inspect/approve` but no registry/search/add-from-registry commands (the gap).
- Result normalization already yields enough to construct a valid upstream config for stdio (command/args) and http (url) servers.
- Frontend changes require `make build` (embedded UI).

## Dependencies

- `internal/registries/` (registries list + search + normalization), `internal/cache/` (24h cache + `read_cache`).
- The unified add path (`internal/server` `AddUpstreamServer`) and its REST/MCP handlers (`internal/httpapi`, `internal/server/mcp.go`: `search_servers`, `list_registries`, `upstream_servers`).
- `cmd/mcpproxy/` (new CLI commands), `frontend/src/views/Repositories.vue` + `components/AddServerModal.vue`.
- Test infra: `scripts/test-api-e2e.sh`, CLI e2e, Playwright Web-UI workflow, mcpproxy-qa skill.

## Out of Scope

- Per-client tool-visibility profiles (GitHub #55 / spec 057) — related, referenced, not merged here.
- Bulk config import (spec 025) — separate.
- Auto-installing/running server packages beyond adding the validated upstream entry.
- Ranking/relevance improvements to registry search results (discovery quality is a separate concern).

## Edge Cases

- **Result missing install info** (neither command nor url derivable): adding is refused with a clear message, not a broken entry.
- **Required key/env not provided**: prompt (UI) / flag-or-error (CLI) / explicit field (MCP); never silently add broken.
- **Duplicate name** already in upstream: handled (reject or disambiguate), consistent across surfaces.
- **Registry unreachable / key absent**: skipped/marked; overall search still succeeds (FR-008).
- **Stale cache**: results show age; manual refresh available (FR-007).
- **Cross-surface drift**: the consistency regression (CN-004/FR-010) guards against surfaces producing different configs.
