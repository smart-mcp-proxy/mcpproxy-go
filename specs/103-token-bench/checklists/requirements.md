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

## Cross-model review round 1 (opencode gpt-5.6-sol, 2026-08-31)

Seven findings, all verified against the tree and all applied. Two were **Critical and
changed the spec's premise**:

1. **Replay cannot answer what the spec asked it to.** The activity record carries tool
   calls, arguments, responses, status and work-session grouping — but no prompt, no
   conversation, no model state and no completion oracle (`internal/storage/activity_models.go`).
   So replaying a recording under a different mode cannot reveal how the agent would have
   *behaved*: which calls it would attempt, whether the first was right, whether it finished.
   The original US1 asked for exactly that and was unsatisfiable.
   **Fix**: added a binding **Replay Boundary** section and split the old US1 into US1
   (deterministic counterfactual cost recomputation over real workload shape, no model) and
   US2 (live agent loop under a pinned model, which owns every success/completion figure).
   FR-004 now forbids inferring behaviour from a recording, and Out of Scope names it.

2. **Reproducibility and privacy were in direct conflict, and the privacy side was absent.**
   The body-inclusive export deliberately returns full unmasked values including detected
   secrets — it is the compliance surface, not a browsing one (`internal/httpapi/activity.go`).
   The spec said session contents must stay out of published artefacts but said nothing about
   protecting them *during* replay, including transmission to model providers.
   **Fix**: new FR-006..FR-009 (sensitive by default, never transmitted to third parties,
   flagged records excluded or reduced, inputs live outside the repo with a documented
   deletion step) plus SC-009. FR-030 now admits that figures from private sessions are not
   independently reproducible and cannot be the sole support for a published claim.

Also applied:

3. **The headline metric did not actually penalise worse completion.** Tokens-per-completed-task
   can *improve* while completion falls, if tokens fall faster — so the old SC-006 was not
   generally true. **Fix**: FR-018 requires completion rate at equal prominence, FR-019 adds a
   completion-regression threshold that overrides any token saving, and SC-007 now requires
   demonstrating this with a deliberately degraded mode.
4. **SC-001 demanded results for "every mode combination"** while FR-017 requires skipping
   invalid ones. **Fix**: scoped to every *valid* combination exercised by the live task set.
5. **Core terms were undefined** ("first attempt", "accepted", "retry", "unit of work",
   "completion", token accounting source). **Fix**: a binding Definitions block, which also
   separates corrective retries from infrastructure retries.
6. **Three FRs restated existing capabilities.** **Fix**: moved to an Inherited Constraints
   block (IC-001..IC-004) so they are satisfied, not rebuilt.
7. **The capability axes were not a testable matrix.** **Fix**: FR-016 states them as binary
   conditions over the routing modes where each is available, with the report enumerating
   applicable rows.

The reviewer independently confirmed the spec's current-state claims: nothing in the harness
replays activity exports, retry rates are literature-derived defaults, first-call success is
not measured, the three axes are not crossed, and the spec-102 figures (29.7% / 34.8% /
38.9% ceiling vs a 70% projection) are accurate.
