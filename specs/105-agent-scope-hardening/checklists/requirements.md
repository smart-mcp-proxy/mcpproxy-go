# Specification Quality Checklist: Agent-Token Scope Hardening

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-07
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — surfaces and built-ins are named; no packages or types
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded (agent-token callers only; administrators unchanged)
- [x] Dependencies and assumptions identified (Spec 104 depends on this; five fix sessions in flight)

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Origin: the eight corrections were discovered by the cross-model review of Spec 104 (rounds 2–9, opencode `github-copilot/gpt-6-astra`, 2026-09-07) and split out at the maintainer's direction so Spec 104 can converge without carrying fixed-surface changes.
- Validation pass 1 (2026-09-07): all items pass.
- Cross-model review round 1 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 21 findings (10 high). All applied in a rewrite: effective-authorization provenance with superset redemption (FR-001); legacy and internal entries refused for all callers with durable invalidation (FR-002, named administrator exception); every redemption door and producer covered; target-tier resolution from the exact dispatched identity, fail-closed on unresolvable metadata, paired-name case, permission×tier×variant table with zero-upstream-call oracle (FR-009); scope filtering before limiting with corpus-wide scoring retained and documented (FR-005); publication filtering at every seam, both skew directions, malformed names withheld, built-ins positively identified (FR-008); prompts fail closed on unknown ownership with interleaving tests (FR-006); log-resource ownership for colliding file names (FR-007); MCP-only transport scope with REST policy out of scope; profile selection predicate (intersection) and profile-URL parity (FR-003/FR-004); non-disclosing dispatch and available-server errors (FR-010); one strict-validation boolean; applicability matrix (FR-014); differential two-fixture oracle with sentinels (FR-013, SC-001); reproducible performance bounds (FR-011); retained effects enumerated in the invariant (FR-012); SC-007 restated as a regression-suite requirement. Items where the in-flight fix sessions were briefed more narrowly are marked ⚠ in the spec.
- Validation pass 2 (2026-09-07): all items pass; zero clarification markers.
- Cross-model review round 2 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 findings, all applied: corpus-wide scoring effects (score, ordering, top-K membership) excluded from the differential oracle and "not displaced" defined against the same-corpus filtered top-K with a multi-term fixture; the profile-URL endpoint applies the selectable-profile predicate (FR-004); execution acceptance split into a retrieve table with variants and a direct/nested table without, with permission-disallowed and intent-mismatch cells classified separately (FR-009, SC-002).
- Validation pass 3 (2026-09-07): all items pass; zero clarification markers.
- Cross-model review round 3 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 findings, all applied: `describe_tool` case-suggestion existence oracle closed by one suggestion policy over the authorized corpus, added to the applicability matrix (FR-010, FR-014); permission-disallowed cells include a missing variant tier on retrieve dispatch, classified before intent mismatch (FR-009); retained admission effects exempted from cross-fixture equality with their own assertions (SC-001, FR-013).
- Cross-model review round 4 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 findings, all applied: direct-surface display/canonical resolution over the authorized corpus with a cross-namespace fixture (FR-010); every policy gate (denial, approval, callability) evaluated on the exact dispatched pair with same-tier paired-name tests (FR-009); ranking-derived `explain_tool` fields added to the oracle exclusions (Edge Cases).
- Cross-model review round 5 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: refusal precedence defined (scope violations non-disclosing; tier denials on authorized servers disclose insufficient permission on every dispatch path) in FR-010 and Definitions; native stdio excluded as administrator-only.
- Cross-model review round 6 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: custom initialization instructions and stored-script names/constant output classified as operator-published content outside the semantic guarantee with a documentation warning and fixtures; missing-script errors made non-disclosing for agent tokens; nested calls stay scope-checked (Edge Cases, FR-012, FR-014).
- Cross-model review round 7 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 1 medium, applied: empty profiles exempted from the unrestricted-token compatibility promise (refused to every agent token through both paths; administrators unchanged) with a fixture.
- Cross-model review round 8 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 1 medium, applied: FR-006 no longer treats an unparseable display name (owner beginning with `__`, a valid server name) as unknown ownership — the registration identity records the owner, so unrestricted agents keep that prompt; unknown ownership is only a missing registration identity. Fixture with server `__a` added.
- Cross-model review round 9 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 5 medium, all applied: FR-008 aligned with FR-006 (withhold only for a missing registration identity; `__a` direct-tool fixture); unrestricted-agent compatibility exceptions enumerated (empty profile, exact-identity policy gates, missing registration identity) and mirrored as administrator exceptions in SC-005 with an empty-raw-name fixture; REST direct call path's cache branch named as a narrow exception to REST exclusion (FR-001); one shared retrieve oracle that also excludes truncation/size/pagination consequences of permitted ranking changes.
- Cross-model review round 10 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 findings (1 high), all applied: identity preservation extended to approval creation, baseline capture, index updates and publication with a real-discovery fixture and conservative legacy-approval handling (FR-009); SC-003 aligned with the corrected malformed-display rule; one administrator exception list (SC-005) referenced from Scope Boundary, Edge Cases and FR-012.
- Cross-model review round 11 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 findings (1 high), all applied: REST cache redemption includes the token's profile pin with a pinned-token fixture; the administrator exception list now covers the discovery/index consequences of raw-identity preservation with a positive both-approved fixture; publication dispatch expectations defined by the handler registered at each seam.
- Cross-model review round 12 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 1 medium, applied: the retrieve oracle now derives the per-fixture expectation as authorized hits → ranked window → existing annotation filters, and excludes `total` and the complete filter-diagnostics payload from cross-fixture equality (FR-005, US1.5).
- Cross-model review round 13 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 3 medium, all applied: container-resource ownership in Docker cleanup/logging with an `a` vs `a-b` fixture (FR-007); shared-limiter contention retained as a documented effect with a held-call fixture; internal cache entries added to the administrator exception list with a fresh-entry test (SC-005).
- Cross-model review round 14 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: cross-server security-scan admission retained as a documented effect with a real-discovery fixture and oracle exclusion; FR-007 container-ownership housekeeping outcomes added to the administrator exception list.
- Cross-model review round 15 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: shared prompt-refresh deadline retained as a documented effect with a controlled-order fixture; log-record attribution made uniform and independent of hidden co-owners (per-record attribution, unattributed legacy lines withheld, no whole-file refusal) in FR-007 and US1.7.
- Cross-model review round 16 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: ownership filtering before tail limiting with counts over the authorized tail, and shared log rotation/retention retained as a documented effect with interleaved and forced-rotation fixtures (FR-007); the `code_execution` definition's script-enumeration wording becomes the one narrow golden exception, reworded to administrator-only enumeration (FR-012, SC-005).
- Cross-model review round 17 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: caller-kind-first superset ordering and monotone recursive provenance for cached responses with an admin-narrow-profile → child → pinned-agent fixture (FR-001); subject-bound log routing for shared-service producers (OAuth callback manager) with both interleavings tested and an SC-005 administrator exception (FR-007).
- Cross-model review round 18 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: cache provenance bound to the immutable snapshot that authorized production, with held-call narrowing fixtures (FR-001); scoring defined over the existing selected corpus (profile index or shared index with fallback) instead of "corpus-wide", with a three-scope multi-term fixture (Edge Cases, FR-005, Assumptions).
- Cross-model review round 19 (gpt-6-astra, 2026-09-07): **VERDICT: ready-for-plan.** No findings. 19 rounds, 58 findings applied. Spec 105 is ready for `/speckit.plan`; Spec 104 (ready-for-plan at its round 14) may proceed in parallel since it declares 105 as its prerequisite.
