# Specification Quality Checklist: Auto Routing Mode

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-07
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — existing mcpproxy concepts (rungs, endpoints, `describe_tool`, `doctor`) are named; no packages, types or library calls
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain (the default-flip threshold is deferred to a follow-up spec under Assumptions)
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified (settled catalog, tokenizer unavailable, session identity, serialization settings, scope edits, reload churn, who may read records, stdio)
- [x] Scope is clearly bounded (Scope Boundary + Out of Scope)
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Validation pass 1 (2026-09-07): all items pass on the first draft.
- Cross-model review round 1 (opencode `github-copilot/gpt-6-astra`, 2026-09-07): verdict *fix-then-plan*, 24 findings (2 blockers). All applied in the same day:
  - Blockers: agent-token callers now see only their own-scope verdict and never the unscoped one (FR-018, FR-021 projection matrix); session endpoints stay administrator-only. Measurement identity now includes caller kind and permission tiers, not just profile/servers (Definitions, FR-008, FR-030).
  - Majors: cross-session test rewritten to "its own request scope" (US4); `set_profile` limited to the retrieve rung; "catalog generation" replaced by a measurement revision covering every visibility and rendering input; fallback verdicts made transient with recovery rules; tokenizer availability contract stated (configured-off vs init failure); measured object defined; SC-002 restricted to successful verdicts; ladder is a preference order; hysteresis transition table added (FR-007); settlement defined per server; parity defined against fixed endpoints in the corresponding serialization with listing-affecting settings enumerated; instruction composition rule (FR-019); legacy aliases follow `/mcp`; missing/unknown session handled by transport validation, no accepted no-session path; retention bounds split (FR-017); projection matrix (FR-021); SC-008 permits the caller's own profile slug locally; telemetry event unit defined (FR-026); release shape restated so authorization ships in P1.
  - Minors: FR-012 named as the one compatibility exception to FR-004; CLI state table (FR-022); validation matrix (FR-002); benchmark conditions (SC-004); reproducible baseline (FR-024); mechanism prescriptions removed where possible (the Commit Message Conventions section is mandated by the repo template and stays); tokenizer wording corrected.
- Validation pass 2 (2026-09-07): all checklist items still pass; zero clarification markers.
- Cross-model review round 2 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 15 findings (1 blocker). All applied:
  - Blocker: `read_cache` serves cached responses without checking the requesting credential — now prerequisite correction FR-016a (all surfaces) and a standalone bug task.
  - Majors: `set_profile` response disclosure → FR-016b (all surfaces) and a standalone bug task; settlement made per scope; live-session idle expiry (30 min) defined separately from persisted-row cleanup; `disable_management`/`read_only_mode` declared startup-pinned; discovery-vs-execution asymmetry preserved and stated (read-only token case has one expected result); exact hysteresis comparisons and the skipped-lower-rung case stated; no-decision states per projection (`no_session_evaluated`, `no_current_authorized_verdict`); FR-022 state table with exit codes and a daemon-discovery correction; effective budget under an environment override; invalid-local-config-with-live-daemon state; telemetry typed domains and bucket enums; in-flight loss window accepted, SC-012 restricted to quiescent snapshots; release shape: every FR/SC required before public enablement.
- Validation pass 3 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 3 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 5 majors. All applied: FR-016c scopes `retrieve_tools` response metadata (usage stats, debug counts, session risk) to the caller's population; FR-016a fails closed on pre-feature cache entries with an upgrade fixture; FR-004 exceptions extended (management/read-only restart gating, all-modes live-session expiry) and `status` keeps exit 0 on no daemon; `no_unscoped_verdict` state distinguished from `no_session_evaluated` with the CLI reading the administrator routing projection; idle telemetry windows retain the budget and latest-measurement buckets.
- Validation pass 4 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 4 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 findings, both applied: `budget_below_all` now applies hysteresis eligibility first (a previously eligible direct candidate inside its band is still served) with an all-over-budget fixture; the auto floor's default instructions mention management tools only when they are listed for that session, tested under `disable_management` and `read_only_mode`.
- Validation pass 5 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 5 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 5 findings (1 high), all applied: per-recipient notification eligibility on the auto endpoint so out-of-scope fleet changes are never signalled (FR-012, FR-029); hysteresis-history expiry invalidates the cached decision, verdicts do not outlive history, clock-controlled reconnect test (FR-008); nested script calls follow the code-execution contract and `/mcp/all` parity is scoped to direct-name dispatch, collision fixture; settlement deadline anchored to admission with retries not extending it; listed vs discoverable count pairs defined per candidate (FR-020).
- Validation pass 6 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 6 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 5 findings (2 P1), all applied: FR-016d direct discovery filtered against its own publication's catalog with an origin-flip fixture; FR-016e notification recipient is the authenticated stream with A-POST/B-stream and narrowing tests; settlement checked before verdict reuse with a delayed-admission fixture (FR-008); cross-scope collision effects are notified as a stated exception with a fixture (FR-012); prior per-candidate eligibility recorded on every decision (FR-020).
- Validation pass 7 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 7 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 4 findings (1 high), all applied: FR-016f target-tier execution permission on retrieve dispatch regardless of the selected `call_tool_*` variant or intent-validation strictness (pre-existing gap, also filed as a standalone task); history-expiry tests re-specified as two seeded cases plus overlapping sessions (FR-008); "latest verdict" defined by binding order (FR-021); idle expiry: in-flight calls defer it, open streams do not, late completions cannot resurrect a session.
- Validation pass 8 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 8 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 findings, both pre-existing scope leaks, both applied: FR-016g aggregated prompts authorized against their canonical owner; FR-016b extended to profile-URL error responses. Filed as a standalone task.
- Validation pass 9 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 9 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 findings (1 high), both applied: FR-016h server-management operations (`tail_log`) authorized against token and profile scope before lookup (pre-existing leak, filed as a standalone task); FR-012/FR-016e recipient authorization extended to prompt-list notifications.
- Validation pass 10 (2026-09-07): all checklist items pass; zero clarification markers.
- Cross-model review round 10 (gpt-6-astra, 2026-09-07; the last under the 10-round cap in CLAUDE.md): verdict *fix-then-plan*, 2 medium definitional findings, both applied without a further re-review: telemetry "verdict decisions" unit defined as fresh decisions (cached-measurement re-decisions count, cached-verdict bindings do not) with the hysteresis counter on the same unit (FR-026, SC-012); `no_retained_verdict` state with a bounded historical last-bound record across API, CLI, UI and telemetry (FR-008, FR-021, FR-022).
- **Status at cap**: 10 rounds run, 79 findings applied, last verdict *fix-then-plan* on two definitional items now fixed. Findings trend: 24 → 15 → 5 → 2 → 5 → 5 → 4 → 2 → 2 → 2; rounds 2–9 each surfaced pre-existing scope leaks in the fixed surfaces.
- **Split (maintainer direction, 2026-09-07)**: the fixed-surface corrections FR-016a–d, f, g, h moved to [Spec 105](../../105-agent-scope-hardening/spec.md); this spec now depends on 105, keeps FR-016 (the invariant) and FR-016e (per-recipient notifications, auto-specific), and no longer lists the corrections as FR-004 exceptions. Cross-model review round 11 (user-authorized, post-split, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: the historical last-bound record is retained per scope label independently of the 20-record rolling history, with a history-cap eviction fixture; telemetry excluded from the historical projection (measurement bucket `none` after expiry).
- Cross-model review round 12 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 2 medium, both applied: FR-012's admission exception extended to prompt collisions and the global prompt cap with scoped fixtures; scope labels are a tagged category plus slug so a profile named `unscoped`/`token_scoped` never collides with the categories in records, projections or historical retention.
- Cross-model review round 13 (gpt-6-astra, 2026-09-07): verdict *fix-then-plan*, 1 medium, applied: no-decision states are defined over sessions on auto-routed endpoints only, with a fixed-endpoint-only fixture (FR-021, FR-022).
- Cross-model review round 14 (gpt-6-astra, 2026-09-07): **VERDICT: ready-for-plan.** No findings. Spec 104 is ready for `/speckit.plan`, contingent on Spec 105 (its prerequisite) reaching the same state.
