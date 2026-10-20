# Specification Quality Checklist: Profile Pin as the Visibility Boundary for Agent Tokens

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-10-20
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details beyond the code anchors this repo's specs carry by convention (file:line pointers to the doors and predicates; no new types or algorithms are prescribed)
- [x] Focused on user value and business needs (operator restriction honoured; no inventory disclosure)
- [x] Written for non-technical stakeholders (Context, Decision and Scope Boundary read without the code)
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain (the maintainer decided Option A; the three open points from the issue are closed by FR-002, FR-012 and FR-014)
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (SC-006 counts decision sites, which is a property of the codebase, not of a framework)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification beyond the convention above

## Notes

- Validated 2026-10-20 against the issue table (L1 to L7) and the MCP-side resolver, which already applies the pin; parity (FR-013) is the check that REST catches up rather than diverging again.
- Amended 2026-10-20 after a first reader pass (Sonnet reader, findings checked against `origin/main` 4e02c36a8): O1 pinned token may select only its own pin on `?profile=` and the profile doors (MCP parity; FR-009, FR-012, A6); O2 `/profiles/{name}/effective-tools` and `/profiles/active` added (L6); O3 grant-as-filter doors and usage cache (L8, FR-019) and the config seam (FR-020); O4 `/diagnostics`, `/doctor`, `/catalog/search`, security scan routes (L7, L9, L10); O5 FR-005 rewritten from a cost claim to a same-code-path refusal rule; O6 FR-014 demoted to a design note with the `profile_mode` caveat.
- A parallel chunked Sol 6.1 pass (four sequential chunks: predicate; SSE and list doors; profile doors and MCP parity; door completeness and consistency) ran in a second session at the same time as rounds 1 and 2 above and returned 17 findings (0 high, 14 medium, 3 low); the ones rounds 1 and 2 had not already covered were verified against `origin/main` 4e02c36a8 and merged in. Added L0 (`/servers`, `/tools`), L11 (`/index/search`), `active_profile.changed`, activity and tool-call detail reads; FR-002 defines the empty-visible-set contract with the profile-door exception; FR-011/FR-012/FR-013 compare eligibility gates and membership rather than bodies; FR-019 cache key carries a pin discriminator; `/security/overview` stays a round-10 denial; SC-006 narrowed; SC-008 and scenarios 8a, 9a, US4.3 added for traceability.
- Amended 2026-10-20 after opencode Sol 6.1 round 1 (6 verified findings): `/profiles/active` withholding extended to every non-administrator as the one FR-004 exception; "visible reach" defined and made the condition for every own-pin disclosure (list, `?profile=`, active); `?profile=<own pin>` refused non-disclosingly without visible reach; FR-007 reconciled with the coalesced `servers.changed` / `attention.changed` exception, hidden-only frames dropped, timing residual recorded; `/security/overview` and `/security/queue` moved out of L7 to the deployment-wide `403` set; `switchable_to` cleared for a pinned caller.
- Amended again after Sol 6.1 round 2 (five of six resolved; two new contradictions and the SSE residual): the FR-004 exception now carried by Compatibility, SC-003 and FR-018; the active-profile example uses a disjoint profile; FR-007 states the snapshot residual (hidden-only changes still produce a frame while visible rows exist) instead of claiming suppression.
- Ready for `/speckit.plan`.
