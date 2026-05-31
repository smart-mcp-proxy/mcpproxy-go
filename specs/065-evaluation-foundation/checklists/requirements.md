# Specification Quality Checklist: Evaluation Foundation (D1+D2)

**Purpose**: Validate specification completeness and quality before planning
**Created**: 2026-05-31
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — names mcp-eval + SUT files as *dependencies*, keeps requirements behavioral
- [x] Focused on user value (measured vs asserted security/discovery) and business need (quiet security = adoption)
- [x] Written for stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable (Recall@5/MRR/nDCG; P/R/F1/FPR; CI fail)
- [x] Success criteria are technology-agnostic
- [x] All acceptance scenarios are defined
- [x] Edge cases identified (corpus drift, restricted license, non-determinism, FPR-vs-recall, multi-relevant)
- [x] Scope clearly bounded (D1+D2 only; D3/D4/D5/D6 out)
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All FRs have acceptance criteria
- [x] User scenarios cover the primary flows (retrieval eval, security eval, CI gate)
- [x] Meets measurable outcomes in Success Criteria
- [x] No implementation leakage into requirements

## Notes

- Scaffolder (`create-new-feature.sh`) not used — its `git fetch --all` + numbering logic breaks on this repo's contributor-fork remotes (same issue hit on 064). Branch `065-evaluation-foundation` + artifacts created directly in standard speckit location/format.
- Number 065: 064 (glass-cockpit) lives on its own branch, 060 is the highest on main; 065 is the next free.
- Plan (`/speckit.plan`) should pin: the exact RetrievalScorer driving path (`index.Manager.Search` vs MCP `retrieve_tools` round-trip — likely both: direct for speed, MCP for realism), the SecurityScorer's detector invocation surface, the dataset schema (JSON), the frozen-corpus snapshot procedure, and which seed corpora are license-clear to vendor.
- Watch item: keep SC phrased generally; they already avoid the dry-run-specific phrasing that 064 had.
