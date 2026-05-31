# Specification Quality Checklist: Registry — Make Discovery Actual & Easy to Add

**Created**: 2026-05-31 · **Feature**: [spec.md](../spec.md)

## Content Quality
- [x] No implementation details in requirements (names files only as dependencies)
- [x] Focused on user value (easy add from registry; CLI parity)
- [x] Written for stakeholders
- [x] All mandatory sections completed

## Requirement Completeness
- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements testable and unambiguous
- [x] Success criteria measurable + technology-agnostic
- [x] All acceptance scenarios defined
- [x] Edge cases identified (missing install info, required key, duplicate, unreachable registry, stale cache, cross-surface drift)
- [x] Scope bounded (close the loop + parity; not building search; profiles/import out)
- [x] Dependencies + assumptions identified

## Feature Readiness
- [x] All FRs have acceptance criteria
- [x] User scenarios cover all three surfaces (Web UI, CLI, MCP) + freshness
- [x] Meets measurable outcomes in SC
- [x] No implementation leakage

## Notes
- Scaffolder not used (broken on this repo's fork remotes); branch `070-registry-easy-upstream-add` + artifacts created directly in standard speckit format. 070 confirmed free.
- Framing from research: search + add BOTH exist and unify through `AddUpstreamServer` (quarantine-by-default). Real gaps: CLI has zero registry commands; Web UI Repositories searches but can't one-click-add; registry list hardcoded/rebuild-only; key-less registries error.
- Plan (`/speckit.plan`) should pin: the unified "add from registry result" core signature; exact new CLI command names; whether the Web UI add lives as an AddServerModal tab vs a Repositories Add button; the config-driven registry-list schema (merge with defaults); cache-refresh control; and the cross-surface consistency regression test design.
- Strong consistency invariant (CN-004/FR-010): same server via any surface → identical upstream entry. This is the key regression test.

## Research refinements (2026-05-31, grounding agent)
- CONFIRMED: search works on all 3 surfaces; add-from-result unified on NONE. Only Web UI auto-adds, via a LOSSY client-side `install_cmd.split(' ')` in `frontend/src/services/api.ts:646-678` that drops env/oauth/working_dir and can break on quoted args. This is the core gap FR-001/CN-004 fix.
- The one source of truth to build: a backend `BuildServerConfigFromRegistryEntry()` (ServerEntry→ServerConfig) that REST + MCP `upstream_servers` + CLI `upstream add` all call → identical quarantined entry (the cross-surface regression in FR-010).
- Registry list is static config (`config.go:866-912`, 5 defaults: pulse/docker/fleur/azure-demo/remote); official `modelcontextprotocol/registry` parser EXISTS (`search.go:115`) but is NOT wired as a default — FR for currency should add it. Server data fetched live per search (10s timeout, NO cache) → a down registry errors the whole search (`runtime.go:1506`); FR-008 isolation + a short-TTL cache needed.
- #483 data-contract fragility: camelCase(runtime)→snake_case(REST)→TS three-hop mapping; consistent now but brittle — collapse to one canonical shape (a hardening FR).
- 025-import-config is an EMPTY STUB (dir only); configimport (`internal/configimport/`) is the separate client-import subsystem — keep distinct. Issue #55 = per-client scoping (adjacent, out of scope).
- Test: golden registry fixture → add via REST+MCP+CLI → byte-identical ServerConfig + all quarantined (the spec's core acceptance). Use a stub registry HTTP server.
