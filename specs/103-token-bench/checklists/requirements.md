# Specification Quality Checklist: Token-efficiency benchmark — measured savings, published results

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-31
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
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
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

Validation ran over three iterations; the following were fixed rather than waived.

**Iteration 1 — implementation detail leaked into a spec that must stay declarative.**
The draft named concrete files, flags, CLI commands, struct fields and PR numbers
(`bench/session.go`, `armRetryRates`, `ResponseTruncated`, `-corpus-v2`, `#747`). All were
removed from requirements and success criteria. This was the hardest constraint to hold,
because the feature's defining risk — duplicating a harness that already exists — is
naturally expressed in file names. It is instead expressed as the **Scope Boundary** table
in capability terms, which a non-implementer can still act on.

**Iteration 2 — success criteria were not all verifiable without implementation knowledge.**
Rewritten so each states an outcome a reader can check: SC-003 (an outsider reproduces the
numbers), SC-006 (the benchmark detects a false saving), SC-007 (truncated input is never
counted silently).

**Iteration 3 — scope was bounded but the *anti*-scope was not.**
Added Out of Scope, because the likeliest failure of this feature is not under-delivery but
sprawl into optimisation work the measurement merely suggests.

**No [NEEDS CLARIFICATION] markers were used.** Three candidates were resolved by informed
default and recorded in Assumptions instead:
- *Which model to pin for agent-loop runs* — deferred to plan stage as a budget decision,
  and the deterministic half is specified to stand alone without it (FR-015 constrains how
  results are reported, not which model produces them).
- *Whether a completion signal can be derived from recorded sessions* — assumed derivable,
  with an explicit fallback (report sessions lacking the signal rather than assuming
  success). This is the assumption most likely to be wrong and should be validated first at
  plan stage.
- *Which public suite to adopt* — deliberately left as a capability requirement (FR-020:
  "at least one full agent-loop suite") rather than naming one, because the shortlist was
  researched in 2026-08 and needs re-confirmation against the suites' current code before
  being committed to.

**Carried into planning**: the research pass flagged two items as unverified — whether the
candidate suites emit per-task token counts, and whether they can be pointed at a single
proxy endpoint. FR-020 and FR-022 are unsatisfiable if neither holds, so plan stage must
read the suites' code before committing to one.
