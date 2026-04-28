# Specification Quality Checklist: Adaptive Onboarding Wizard, Extended Connect, & Onboarding Telemetry

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-04-28
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — references existing endpoints (Spec 039) and existing telemetry pipeline (Spec 042 / Spec 044) by name only
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded — does not duplicate Specs 026, 032, 039, 042, 044
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Three priority-ordered user stories, each independently testable:
  - **P1**: Adaptive wizard. Two predicates (any client connected? any server configured?) drive which steps appear. Four state combinations covered.
  - **P2 (a)**: Extends Spec 039's adapter table from 7 to ~20 clients (tier-2: Antigravity, Zed, OpenCode, Amazon Q, Kiro, LM Studio, Cline, Roo Code, Junie, Copilot CLI, Copilot JetBrains, Pi, Amp).
  - **P2 (b)**: Onboarding telemetry — connected-client count + identifier set + wizard funnel record on the daily heartbeat, so we can validate the wizard's retention impact against the 11.8% day-2 baseline.
- 31 functional requirements grouped: Wizard (FR-001..FR-015), Extended Connect (FR-016..FR-023), Onboarding Telemetry (FR-024..FR-031).
- 13 success criteria. SC-001..SC-010 cover wizard + connect; SC-011..SC-013 are post-release retention measurements gated on telemetry being live.
- Background section includes a baseline funnel table from production D1 (n=1,491 installs, 2026-03-23 → 2026-04-28) with the data-driven justification for prioritising client-connect over add-server in the wizard's design.
- Scope explicitly excludes:
  - Rebuilding the connect endpoint or CLI (Spec 039).
  - Rebuilding the add-server flow.
  - Rebuilding quarantine, tool-change detection, or sensitive-data scanning (Specs 032, 026).
  - Building any new telemetry transport — fields ride the existing daily heartbeat (Spec 042 / Spec 044).
  - On-demand security scan UI.
  - mcpscoreboard.com integration.
- Privacy posture inherits Spec 042 / Spec 044: opt-out, anonymous, no user-entered strings, fixed enums for client identifiers, automated privacy test (FR-029).
- Telemetry baseline numbers and post-release retention queries are documented; FR-031 makes the queries part of plan-time artifacts.
