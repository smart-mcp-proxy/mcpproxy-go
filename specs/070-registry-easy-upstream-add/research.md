# Phase 0 Research: Registry — Make Discovery Actual & Easy to Add to Upstream

**Feature**: 070-registry-easy-upstream-add · **Date**: 2026-05-31
**Method**: Direct code mapping of the existing registry/search/add subsystems across all three surfaces (Web UI / MCP / CLI) + REST backend, with file:line provenance (doctrine S-1).

## Executive summary — the spec premise is partly stale

The spec (Clarifications session 2026-05-31) was written on the premise that the Web UI cannot one-click-add and the CLI has **zero** registry commands. Direct code inspection shows both are **already partially built**. The real, narrower work is **architectural de-duplication**: the registry-result→upstream-config normalization is implemented three different ways (client-side JS, hand-built MCP args, absent on CLI), which directly threatens the CN-004 consistency invariant. The keystone deliverable is **one backend core operation** that all surfaces call.

This reframing must be ratified at the design gate before implementation (Gate 2).

## What already exists (with provenance)

### Backend / core
- **Registry list IS config-driven** — `SetRegistriesFromConfig(cfg)` loads `cfg.Registries` (`internal/registries/registry_data.go:10-42`); 5 built-in defaults in `internal/config/config.go:866-912` (pulse, docker-mcp-catalog, fleur, azure-mcp-demo, remote-mcp-servers); hardcoded `smithery` only as the no-config fallback (`registry_data.go:29-40`). **No rebuild needed** to add a registry — FR-006 is largely satisfied at the storage level.
- **Search + normalization** — `registries.SearchServers(ctx, registryID, tag, query, limit, guesser)` (`internal/registries/search.go:31-75`); per-protocol parsers extract `InstallCmd`/`SourceCodeURL`; `applyBatchRepositoryGuessing` (`search.go:155-219`) enriches with npm/PyPI install commands. Result type `ServerEntry` (`internal/registries/types.go:18-32`).
- **Unified add (storage)** — `storage.SaveUpstreamServer(*config.ServerConfig)` (`internal/storage/manager.go:83-110`); MCP handler `handleAddUpstream` (`internal/server/mcp.go:3381-3628`). Quarantine-by-default via `cfg.DefaultQuarantineForNewServer()` (`internal/config/config.go:1005-1007`, default true). CN-002 holds.
- **Cache** — `internal/cache/manager.go`; **TTL is 2h, not 24h** (`manager.go:19`); cleanup every 10m; **no manual refresh/invalidate API**; no freshness/age surfaced to results.

### MCP surface
- `search_servers` (`internal/server/mcp.go:703-724`, handler `:864-943`), `list_registries` (`:728-735`, handler `:946-986`), `upstream_servers` (`:629-675`, handler dispatch `:2384` → `handleAddUpstream`).
- `upstream_servers add` requires **hand-constructed** `command`/`args_json`/`url` — no "add from search result by reference" mode (the FR-005 gap).

### REST surface
- `GET /api/v1/registries` → `handleListRegistries` (`internal/httpapi/server.go:3901-3946`).
- `GET /api/v1/registries/{id}/servers?q=&tag=&limit=` → `handleSearchRegistryServers` (`:3964-4040`).
- `POST /api/v1/servers` → `handleAddServer` (`:1277-1361`); quarantine default at `:1317`. Takes already-built fields — **no "add from registry result" body mode**.

### CLI surface
- **`search-servers` ALREADY EXISTS** (`cmd/mcpproxy/main.go:234-349`) with `--list-registries`, `--registry`, `--search`, `--tag`, `--limit`, `-o table|json|yaml`. Runs **standalone, in-process** (loads config, calls `registries.SearchServers` directly — `main.go:286-308`), NOT through the running daemon.
- **The real CLI gap**: results only print; there is **no add-from-result command** — the user must hand-copy into `upstream add`. Closing search→add on the CLI is the genuine net-new work (FR-004 remainder).

### Web UI surface
- `Repositories.vue` searches (`loadRegistries`/`searchServers`, lines 301-338) and **already renders an "Add to MCP" button** (lines 161-168) → `api.addServerFromRepository(server)` (`frontend/src/services/api.ts:646-678`).
- **`addServerFromRepository` parses `install_cmd` CLIENT-SIDE** (`api.ts:662-667`: `install_cmd.split(' ')`) and calls `upstream_servers add`. This is brittle (issue #483 history: snake_case casing bug) and is **the CN-001 violation** — surface-specific add logic that diverges from MCP/CLI.
- **No prompt for required inputs** (env/API keys) before adding (the FR-003 gap).
- `AddServerModal.vue` collects name/type/command/args/env/url/working_dir/quarantined — but accepts **no pre-fill props** from a registry result.
- **No `data-test` attributes** on either component — must be added for the Playwright regression (FR-010).

## Key decisions

### D1 — Keystone: one backend "add from registry result" core operation (FR-001)
**Decision**: Add a single core function that takes `(registryID, serverID/name, overrides)`, re-runs the existing `registries.SearchServers`/normalization **server-side**, builds a validated `config.ServerConfig` (command/args **or** url, transport, env), and routes through the existing quarantine-by-default add path.
**Rationale**: Eliminates the three divergent normalizations (frontend JS, hand-built MCP, none on CLI). Directly enforces CN-001 (unified path) and CN-004 (identical entry across surfaces). The normalization already exists (`search.go`); we are relocating the *consumer* of it into the core, not rebuilding search.
**Alternatives rejected**: (a) Keep parsing per-surface and add a shared TS+Go duplicate — rejected, guarantees drift. (b) Pass the full normalized result object from client to a generic add endpoint — rejected, lets a malicious/stale client inject arbitrary config; server must re-derive from the authoritative registry fetch.

### D2 — Expose the core op on REST + MCP; repoint Web UI; add CLI add (FR-002/004/005)
**Decision**:
- REST: `POST /api/v1/registries/{registryId}/servers/{serverId}/add` (body: optional `env`, `name` override, `enabled`) → core op.
- MCP: new `upstream_servers` operation `add_from_registry` with params `registry`, `id` (server identifier), optional `env_json`, `name`.
- CLI: `mcpproxy registry list`, `mcpproxy registry search`, `mcpproxy registry add <registry> <serverId> [--env K=V] [--name N]` — a `registry` command group that talks to the **running daemon** via `cliclient` (mirrors `upstream` cmd pattern, `cmd/mcpproxy/upstream_cmd.go`). `search-servers` is retained as a back-compat alias.
- Web UI: change `addServerFromRepository` to call the new REST add endpoint (server derives config); add a required-input prompt in `AddServerModal`/Repositories.
**Rationale**: Each surface calls the same core; the Web UI stops parsing client-side. CLI add goes through the daemon so it shares the live config/registry list (consistency).
**Alternatives rejected**: Reusing the standalone in-process `search-servers` for add — rejected, it bypasses the running daemon's config/quarantine and would re-introduce divergence.

### D3 — Required-input prompting (FR-003)
**Decision**: Normalized result carries a `required_inputs[]` (env var names a result declares). Add refuses (with a clear message) when a required input is absent; Web UI prompts, CLI errors instructing `--env`, MCP returns a structured "missing required input" error.
**Rationale**: "Never silently add a broken server" (edge case in spec). **Open question O1**: today's `ServerEntry` does not model declared required inputs — most registries don't expose them. Scope decision needed at the gate (see Open Questions).

### D4 — Registry list merge + freshness (FR-006/007)
**Decision**: `SetRegistriesFromConfig` currently **replaces** defaults with config. Change to **merge** (defaults ∪ user-defined, user wins on ID collision) so adding one custom registry doesn't drop the 5 built-ins. Add a manual cache-refresh control (REST `POST .../refresh` or a `--refresh` flag) and surface cache age/freshness on search results.
**Rationale**: FR-006 ("merge with built-in defaults") and FR-007 (freshness + manual refresh) are the genuine remaining registry-resilience gaps.

### D5 — Key-absent resilience (FR-008)
**Decision**: None of the 5 default registries require an API key today, so there is no live failure to fix. Model an optional per-registry `requires_key` hint; when set and the key is absent, **skip that registry and mark it unavailable** in the aggregated result rather than failing the whole search. Implement defensively (search already isolates per-registry fetch errors — `search.go:78-107`).
**Rationale**: Satisfies FR-008/SC-006 without inventing a key-requiring default. **Open question O2**: do we ship a key-requiring registry (e.g. Smithery) to exercise this, or just the resilience plumbing? Gate decision.

### D6 — Cross-surface consistency regression (FR-010 / CN-004) — keystone test
**Decision**: A Go integration test that adds the **same** logical registry result via the core op as invoked by (a) the REST handler, (b) the MCP handler, and (c) the CLI add path, then asserts the persisted `config.ServerConfig` entries are byte-identical (modulo timestamp). Plus the three per-surface tests (MCP protocol, CLI e2e, Playwright Web UI) and a REST/curl test.
**Rationale**: This is the single most valuable artifact — it mechanically prevents the divergence that D1 removes from re-appearing.

## Open questions for the design gate (Gate 2)
- **O1 (FR-003 depth)**: Do registries actually declare required env/keys in their result payloads? If not, "required inputs" is best-effort (heuristic from install_cmd `${VAR}` patterns) vs deferred. Recommend: implement the plumbing + heuristic, defer rich per-registry schemas.
- **O2 (FR-008 demo)**: Ship a key-requiring registry to exercise skip-on-missing-key, or just the resilience plumbing + unit test? Recommend: plumbing + unit test, no new key-requiring default.
- **O3 (spec amendment)**: The spec's US1/US2 "gaps" are stale (Web UI add and CLI search/list already exist). Recommend amending spec Clarifications to reflect the architectural reframing (de-dup normalization) so SC/acceptance match reality.
- **O4 (scope cut)**: P1 = D1 (core op) + D2 (REST/MCP/CLI-add/Web-repoint) + D6 (consistency regression). P2 = D3/D4/D5 (required-inputs, merge+freshness, key-skip). Confirm P2 stays in this spec vs a follow-up.

## Constitution alignment
- **I Performance**: no hot-path change; registry fetch is already off the request path (cached). ✅
- **II Actor concurrency**: core op is a synchronous storage write through existing manager; no new locks. ✅
- **III Config-driven**: registry list stays in `mcp_config.json`; merge change keeps hot-reload. ✅
- **IV Security-by-default**: quarantine-by-default preserved on every surface (CN-002); server re-derives config from authoritative registry fetch (no client-injected config). ✅
- **V TDD**: consistency regression + per-surface tests written first. ✅
- **VI Docs**: CLAUDE.md MCP-tool + CLI tables updated; note the 40k-char CI gate (memory) — put detail in this spec, minimal CLAUDE.md delta. ✅
