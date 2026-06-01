# Implementation Plan: Registry — Make Discovery Actual & Easy to Add to Upstream

**Branch**: `070-registry-easy-upstream-add` | **Date**: 2026-05-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/070-registry-easy-upstream-add/spec.md`

## Summary

Close the registry **search → add** loop and reach parity across Web UI, MCP, and CLI by routing every surface through **one backend core operation** that re-derives a validated, quarantined upstream config from a registry result. Research ([research.md](./research.md)) found the loop is *already partially closed* (Web UI has an Add button; CLI already lists+searches) but via **three divergent normalizations** (client-side JS, hand-built MCP args, none on CLI). The plan's keystone is de-duplicating that normalization into the core (FR-001) and guarding it with a cross-surface consistency regression (FR-010/CN-004). Remaining registry-resilience gaps (merge-with-defaults, cache freshness/refresh, key-absent skip) are P2.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10); TypeScript 5.9 / Vue 3.5 (frontend)
**Primary Dependencies**: Cobra (CLI), Chi router (REST), `mark3labs/mcp-go` (MCP), BBolt (storage), Zap (logging), Pinia/Vite (frontend). No new external deps.
**Storage**: BBolt (`~/.mcpproxy/config.db`) upstream bucket — reuses `SaveUpstreamServer`; **no schema change**. Registry list stays in `mcp_config.json` (`Registries []RegistryEntry`).
**Testing**: `go test ./internal/... -race`; `scripts/test-api-e2e.sh` (REST/curl); CLI e2e; Playwright Web-UI workflow (`e2e/playwright`); cross-surface consistency Go integration test.
**Target Platform**: macOS/Linux/Windows desktop (personal edition); server edition unaffected (no build-tagged code touched).
**Project Type**: web (Go backend + embedded Vue frontend).
**Performance Goals**: No regression to the BM25/tool hot path (Constitution I). Registry fetch stays off the request path (cached, 2h TTL).
**Constraints**: Quarantine-by-default invariant (CN-002) on every surface; identical persisted config across surfaces (CN-004); CLAUDE.md 40k-char CI gate (keep delta minimal — detail lives here).
**Scale/Scope**: ~6 registries; result sets ≤50; 4 surfaces (REST/MCP/CLI/Web) + 1 consistency regression.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Note |
|-----------|--------|------|
| I. Performance at Scale | ✅ PASS | No hot-path change; registry fetch cached and off-request. |
| II. Actor-Based Concurrency | ✅ PASS | Core op is a synchronous storage write through the existing manager; no new mutexes. |
| III. Configuration-Driven | ✅ PASS | Registry list stays config-driven; merge-with-defaults preserves hot-reload. |
| IV. Security by Default | ✅ PASS | Quarantine-by-default preserved everywhere (CN-002); server **re-derives** config from the authoritative registry fetch — never trusts a client-supplied config blob. |
| V. TDD | ✅ PASS | Consistency regression + per-surface tests authored before implementation. |
| VI. Documentation Hygiene | ✅ PASS | CLAUDE.md MCP-tool + CLI tables updated minimally; detail in spec. |

**Result**: No violations → Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/070-registry-easy-upstream-add/
├── spec.md              # Feature spec (exists)
├── plan.md              # This file
├── research.md          # Phase 0 — grounded map + the 3 stale-premise discrepancies
├── data-model.md        # Phase 1 — entities (Registry, normalized result, add op)
├── quickstart.md        # Phase 1 — per-surface verification recipe
├── contracts/           # Phase 1 — REST + MCP + CLI contracts
│   └── add-from-registry.md
├── checklists/
│   └── requirements.md  # (exists)
└── tasks.md             # Phase 2 — /speckit.tasks output (not created by plan)
```

### Source Code (repository root) — files this feature touches

```text
internal/registries/
├── search.go            # SearchServers + normalization (reuse; add FindServerByID helper)
├── registry_data.go     # SetRegistriesFromConfig → MERGE defaults∪config (FR-006)
└── types.go             # ServerEntry → add RequiredInputs[] (FR-003 plumbing)

internal/server/
├── add_from_registry.go # NEW — core op: (registryID, serverID, overrides) → validated quarantined ServerConfig (FR-001) [KEYSTONE]
└── mcp.go               # upstream_servers: new operation "add_from_registry" (FR-005)

internal/httpapi/
└── server.go            # NEW route POST /api/v1/registries/{id}/servers/{serverId}/add (FR-002 backend); optional .../refresh (FR-007)

internal/cache/
└── manager.go           # Manual refresh/invalidate + age surfaced (FR-007)

cmd/mcpproxy/
└── registry_cmd.go      # NEW — `registry list|search|add` group via cliclient→daemon (FR-004); search-servers kept as alias

internal/cliclient/
└── client.go            # NEW methods: SearchRegistry, ListRegistries, AddFromRegistry

frontend/src/
├── services/api.ts      # addServerFromRepository → call new REST add endpoint (stop client-side parse) (FR-002)
├── views/Repositories.vue        # required-input prompt + data-test attrs (FR-003, FR-010)
└── components/AddServerModal.vue # accept registry pre-fill + data-test attrs

tests/ (co-located *_test.go + e2e)
├── internal/server/add_from_registry_test.go      # core op unit tests
├── internal/server/consistency_crosssurface_test.go # KEYSTONE regression (CN-004/FR-010)
├── e2e/cli/registry_add_test.*                     # CLI e2e
└── e2e/playwright/registry-add.spec.ts             # Web UI Playwright
```

**Structure Decision**: Web application (Go backend + embedded Vue). The keystone is a new backend core file `internal/server/add_from_registry.go`; all surfaces (REST handler, MCP operation, CLI command, Web UI) are thin callers of it. This is the structural expression of CN-001/CN-004.

## Phased delivery (maps to user stories / priorities)

**Phase A — P1 keystone (US1+US2+US3 core)**
1. `internal/registries`: add `FindServerByID(ctx, registryID, id)` returning a normalized `ServerEntry` (reuse SearchServers).
2. `internal/server/add_from_registry.go`: `AddServerFromRegistry(ctx, registryID, serverID, overrides)` → validate → `ServerConfig` (command/args **or** url, transport, env) → quarantine-by-default → `SaveUpstreamServer`. Refuse on missing install info / missing required input.
3. Expose: REST `POST /api/v1/registries/{id}/servers/{serverId}/add`; MCP `upstream_servers operation=add_from_registry`; CLI `registry add`.
4. Repoint Web UI `addServerFromRepository` to the REST endpoint (delete client-side `install_cmd.split`).
5. **Consistency regression** (CN-004/FR-010): same result via REST/MCP/CLI → identical persisted config.

**Phase B — P2 resilience (US4 + FR-003 depth)**
6. `SetRegistriesFromConfig`: merge defaults ∪ config (FR-006).
7. Cache manual refresh + freshness on results (FR-007).
8. Key-absent skip/mark-unavailable plumbing (FR-008).
9. Required-input prompting end-to-end (FR-003): Web prompt, CLI `--env`, MCP structured error.

**Phase C — tests + docs (FR-010, Constitution VI)**
10. Per-surface tests: MCP protocol, CLI e2e, Playwright Web UI, REST/curl.
11. CLAUDE.md MCP-tool + CLI table deltas (mind 40k gate); spec amendment per O3.

## Risks / decisions deferred to the design gate (Gate 2)
- **Stale-premise reframing (O3)** — spec US1/US2 describe gaps that are already partly built; the real work is de-dup. Needs human ratification before implementation.
- **FR-003 depth (O1)** — registries may not declare required inputs; heuristic vs deferred.
- **FR-008 demo (O2)** — ship a key-requiring registry or just plumbing.
- **P2 in-scope vs follow-up (O4)** — confirm D3/D4/D5 stay in this spec.

## Complexity Tracking

No constitution violations → no entries required.
