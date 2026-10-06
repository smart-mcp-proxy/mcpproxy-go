# Specification Quality Checklist: Upstream resilience (Spec 113)

**Purpose**: Validate specification completeness, traceability and slice independence before implementation
**Created**: 2026-10-05
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] CHK001 User stories describe user outcomes (no lost grants, usable non-standard providers, explained failures, honest health, invisible session loss, safe caching)
- [x] CHK002 Every gap claim from the audit was re-verified against `origin/main` with file:line evidence (research.md §1); corrections recorded (G6 existing audit vocabulary, G8 404 semantics, G9 default hints already emitted, G10 intentional freeze, new G1+ finding)
- [x] CHK003 Definitions bind the terms used across slices (server key, refresh flight, reactive/proactive, DCR vs static client, session-terminated)
- [x] CHK004 Mandatory sections complete (stories, edge cases, FRs, key entities, success criteria, commit conventions)

## Requirement Completeness

- [x] CHK005 No [NEEDS CLARIFICATION] markers; open choices decided in research.md D1–D12
- [x] CHK006 Requirements are testable: error-class rules are ordered, thresholds are numeric constants, TTLs and bounds are numeric
- [x] CHK007 Success criteria are measurable (SC-001 – SC-008)
- [x] CHK008 Edge cases: cancelled initiator, read-only token probes, 200-with-error AS bodies, active login flow, cache-key collisions, dispatched vs undispatched timeouts, removed servers, modern-protocol 404s
- [x] CHK009 Scope bounded: G4 and G10 deferred with design sketches and named open decisions; over-broad AS scope request noted, unchanged
- [x] CHK010 Backward compatibility: all new JSON fields `omitempty`; old activity records and configs decode unchanged; audit vocabulary frozen with a total mapping; legacy-protocol responses byte-identical (113-f)

## Slice independence

- [x] CHK011 Each slice has its own user story, FR range and task-ID range (T100–T249; deferred T250–T259)
- [x] CHK012 File ownership table (plan.md) assigns every edited file to one slice, with narrowly scoped shared hunks and the expected textual overlaps listed
- [x] CHK013 No slice depends on another slice's merge; 113-d carries a local predicate if 113-c has not merged
- [x] CHK014 No slice edits `specs/113-upstream-resilience/**`; 113-f may tick Spec 058 T063/T064 only

## Repository rules

- [x] CHK015 Config-field checklist applied to the only new config fields (113-b FR-028): change detection, merge, mask decisions, contracts/generated types, swagger, docs; no env override for per-server fields; write-time-only validation
- [x] CHK016 REST/OAS changes require `make swagger` and committed `oas/` (113-b, 113-c)
- [x] CHK017 Both editions: server-tag build, lint and tests in every slice's gates
- [x] CHK018 No new modules (`golang.org/x/sync` already in `go.mod`)
- [x] CHK019 No Claude attribution in commits or PR bodies

## Traceability

| Gap | Verdict | FRs | Slice | Tests |
|---|---|---|---|---|
| G1 refresh not serialized (3 paths) + lost update | Confirmed | FR-001–FR-006, FR-011–FR-014 | 113-a | T102, T104, T106, T109 |
| G1+ RefreshManager reads by display name | New | FR-010 | 113-a | T112 |
| G2 substring classification, `invalid_client` | Confirmed (invalid_grant already terminal) | FR-007–FR-009 | 113-a | T100, T110, T115 |
| G3 no `offline_access` | Confirmed, PRM-sourced scopes only | FR-020, FR-021 | 113-b | T141, T143 |
| G4 no CIMD | Confirmed, deferred | — | — | T250–T254 |
| G5 no overrides, path heuristic, no discovery cache | Confirmed | FR-022–FR-033 | 113-b | T130–T139, T146 |
| G6 unclassified call failures | Confirmed; audit vocabulary exists | FR-040–FR-051 | 113-c | T160–T171 |
| G7 health ignores failure rate | Confirmed | FR-060–FR-068 | 113-d | T190–T200 |
| G8 session-terminated → Error | Confirmed; any 404 maps to it | FR-080–FR-088 | 113-e | T220–T227 |
| G9 cache hints | Partly incorrect: mcp-go emits private/0 by default | FR-090–FR-094 | 113-f | T240–T242 |
| G10 per-user creds unused | Confirmed, intentional (Spec 107 FR-034) | — | — | T255–T259 |
