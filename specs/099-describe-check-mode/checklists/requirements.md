# Specification Quality Checklist: `describe_tool` Check Mode — In-Band Preflight

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-16
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details beyond what locked decisions require (parameter names, enum reuse, batch caps and disclosure tiers are product contract, because agents and consumers branch on them; internal seam names appear only in Assumptions and FR-003/FR-010 as reuse scope)
- [x] Focused on user value and business needs (an agent can gate its own plan in-band instead of failing one tool call at a time)
- [x] Written for non-technical stakeholders (Stories 1–4 readable standalone)
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain and no requirement is reopened elsewhere — the three input-locked choices are DECIDED in the FRs, with their measured price and rejected alternative recorded under "Priced Alternatives" (non-blocking, owner-override only)
- [x] Requirements are testable and unambiguous (each FR names an observable behavior; FR-015's budget is a measured number, not an adjective)
- [x] Success criteria are measurable (SC-001…SC-007)
- [x] Success criteria are technology-agnostic where they can be (SC-003/SC-005 quote token counts because token cost IS the user-facing currency of this product)
- [x] All acceptance scenarios are defined (Stories 1–4)
- [x] Edge cases are identified (explicit `check:false`, `check:null`/non-boolean, filters/pins without check, orphan and blank pin values, dedup + id normalization, malformed ids, empty batch with mode-accurate wording, degraded runtime, unwritable activity record, unauthenticated `/mcp`, profile-pinned and `set_profile` sessions, batch cost, no side effects)
- [x] Scope is clearly bounded (Non-Goals enumerates every deferred phase and explicitly closes the enum)
- [x] Dependencies and assumptions identified (spec 098 shipped; reporter's 2026-08-13 comment; measurement provenance; no shipped consumer of `invisible`)

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows (in-band gating, plain-mode non-regression, cross-surface parity, absent-surface honesty)
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into user-facing sections

## Feature-Specific Gates

- [x] Every new reason-surface cell has a sabotage-matrix obligation (FR-016) and the reflection gate is extended, not duplicated
- [x] The token-budget change is measured, priced against alternatives, given an explicit new ceiling, and pinned by BOTH a ceiling test and a golden snapshot (FR-015)
- [x] The golden-snapshot update is called out as a deliberate exception to spec 098 FR-015, with the reason it does not weaken that rule (FR-014/FR-015)
- [x] The plain-mode behavior change (`invisible` → `not_found`) is stated explicitly, scoped to exactly one field, justified as a leak fix, and enumerated in the byte-identity test rather than blanket-allowed (FR-011, SC-002)
- [x] Disclosure-tier resolution is fail-closed AND explicitly forbids inferring operator from the admin auth context, because the MCP middleware injects one for unauthenticated requests by design (FR-009, SC-007)
- [x] Evaluation scope is defined against all three existing narrowing inputs (agent-token servers, token profile pin, session active profile) and reuses the 098 composition rather than re-deriving one (FR-009a)
- [x] Cross-surface payload divergences are named field-by-field, not hand-waved: no `hash` in band, `checked_at` excluded from parity (FR-004, FR-017)
- [x] Caller-visible correlation is closed: the response returns the same `request_id` the activity record carries, so SC-006 is achievable from inside the session (FR-004, FR-013)
- [x] No new reason code is introduced; the 15-code enum stays owned by spec 098 (FR-003, Non-Goals)
- [x] One inherited defect found during review is scheduled rather than inherited silently: the 098 `mid_indexing` matrix note contradicts 098 FR-005 and is corrected while the matrix is extended (FR-016)

## Notes

- Reason codes, precedence, disclosure tiers and the pin format are inherited verbatim from spec 098 — this spec is a new surface over an existing contract, and any change to that contract belongs upstream in 098.
- Cross-model review of this spec (opencode / GPT Sol) ran on 2026-08-16: 8 P1 / 6 P2 / 1 P3, verdict REQUEST CHANGES. All 15 findings were judged genuine and folded in (FR-004 `request_id` + no-hash-echo + `checked_at` semantics, FR-006 normalization, FR-008 pin validation, FR-009 fail-closed tier + `AuthTypeUser`, FR-009a scope composition, FR-011 compatibility-break framing, FR-012a strict validation, FR-013 count definition, FR-016 inherited matrix fix, FR-017 excluded fields, SC-005 falsifiability, Story 2 wording, and the Open-Questions→Priced-Alternatives reframe). One finding was scoped OUT deliberately: tightening the REST `disclosureTier` mapping for server-edition `AuthTypeUser` belongs to spec 098 and is flagged there, not fixed here.
- The three Priced Alternatives are recorded decisions with measured costs, not spec gaps; they do not gate planning.
