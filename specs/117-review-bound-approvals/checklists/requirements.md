# Specification Quality Checklist: Spec 117

**Purpose**: Validate specification completeness before implementation
**Created**: 2026-10-11
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] Each requirement traces to a failure scenario in #1565, #1566 or #1567 (spec.md Context table G1-G9)
- [x] User stories are independently testable, with priorities and one PR each
- [x] Mandatory sections are complete (scenarios, requirements, success criteria, assumptions, out of scope)

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain. Ambiguities are resolved as Assumptions A1-A10 and Open Questions O1-O4, each with a default
- [x] Requirements are testable (each FR maps to a test task in tasks.md)
- [x] Success criteria are measurable (live repro flips, 409 guarantees, SC-005 timing)
- [x] Edge cases are identified (vanished block target, empty values, hashless records, dangerous+stale, seq 0, re-apply, downgrade)
- [x] Scope is bounded (rug-pull policy, blocks, non-review unquarantine, Tools page, MCP ops and tray code are out)
- [x] Wire contract matches PR #1572 (contracts/rest-approval-binding.md "Client rules"; research D3 a-e)
- [x] Compatibility with unbound clients is specified (FR-010/FR-012, compatibility matrix)

## Feature Readiness

- [x] Failing-first tests are named per PR with the expected failure on main
- [x] Live verification per PR on the qa070 harness (quickstart L1-L3); G5 and G1/G3 reproduced on main at design time
- [x] PR split is declared with scope per PR (tasks.md headers)

## Traceability

| Issue item | Requirement | Test |
|---|---|---|
| #1566 reviewed A, B persisted, B approved | FR-005/006/007 | T205, T207, T211, L2.2 |
| #1566 tool discovered after GET promoted by server approval | FR-005, FR-012 | T207, L2.4/L2.5 |
| #1566 annotations excluded → read-only becomes destructive | FR-001/002 (fingerprint), activation set | T201, T207, L2.2 |
| #1566 empty review `--force` unbound | FR-013/015 (`{}`), FR-023 | T213, T215, T106 |
| #1566 scanner commit-to-unquarantine window | FR-011 | T207 |
| #1567 replaced inventory re-baselines | FR-020-022 | T104(a), L1 |
| #1567 authoritative empty refresh | FR-022 | T104(b), L1.4 |
| #1567 empty review approval leaves no evidence | FR-023 | T106, L1.5 |
| #1565 applyDifferentialToolUpdate publishes whatever was captured | FR-032 | T306(1) |
| #1565 eviction after operator approval | FR-032 | T306(2) |
| #1565 absent-tool removal from an older inventory | FR-032 | T306(3) |
| #1565 stale inventory republished to the StateView | FR-033 | T306(1) |
| #1565 sweep ticket / coalesced ListTools | FR-030/031 | T301, T303 |
| #1565 paused pass re-indexes a quarantined server | FR-034/035 | T306(4), T308 |
| #1565 old endpoint's capture after replacement | FR-036 | T303, T306(7), L3.3 |

## Review log

- Round 1 (opencode gpt-6.1-sol): FINDINGS, 3 high and 3 medium, all accepted and resolved (research.md "Design review round 1").
