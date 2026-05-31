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
