# Implementation Plan: Paperclip Goal Cockpit for MCPProxy

**Branch**: `045-paperclip-cockpit` | **Date**: 2026-04-25 | **Spec**: [spec.md](spec.md) | **Design**: [2026-04-25-paperclip-goal-cockpit-design.md](../../docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md)
**Input**: Feature specification from `/specs/045-paperclip-cockpit/spec.md`

## Summary

This is a **configuration-only feature**: no Go, Vue, or Swift code is added to the mcpproxy-go repository. The deliverable is the bootstrap of seven Paperclip agents (CEO, Backend, Frontend, macOS, QA, Critic, Release) with per-role instruction files at `~/.paperclip/instances/default/companies/<id>/agents/<id>/instructions/`, three Synapbus wiki articles (`mcpproxy-roadmap`, `mcpproxy-architecture-decisions`, `mcpproxy-shipped`), and per-agent budget caps. Most "code" is markdown prompt files and Synapbus article content.

The technical approach: use Paperclip's existing AGENTS.md system + Synapbus's wiki + Synapbus MCP search to wire a "goal cockpit" workflow on top of platforms that already exist. No new mcpproxy-go primitives are needed.

## Technical Context

**Language/Version**: Markdown (agent instruction files, wiki articles); optionally shell or AppleScript helpers for bootstrap idempotency
**Primary Dependencies**: Paperclip AI (paperclipai/paperclip, MIT) running locally on loopback :3100; Synapbus on kubic; **multi-LLM CLI stack** — Claude Code (default for CEO + 5 implementation experts + QA Tester), Gemini CLI with `gemini-3.1-pro-preview` (Critic agent — FR-015, R-9), opencode CLI with `kimi2.5-gcore` (Synapbus context summarization — FR-016, R-10)
**Storage**: Paperclip's embedded Postgres (existing, port 54329); Synapbus DB (existing); no new storage in mcpproxy-go
**Testing**: Synthetic-goal smoke test (T-006) exercises P1 + P2 + P3 end-to-end; no Go/Swift/Vue test surface
**Target Platform**: User's local machine (Paperclip loopback) + kubic.home.arpa (Synapbus on namespace `synapbus`)
**Project Type**: Operations / configuration (no source tree to modify)
**Performance Goals**: Synthesized proposal in under 30 minutes for typical-scope goals (SC-001)
**Constraints**: Paperclip stays bound to 127.0.0.1; per-agent budget caps prevent runaway; no agent self-merges PRs; provenance citation required on every proposal
**Scale/Scope**: 7 agents, 3 wiki articles, ~10 markdown files; ongoing 1–5 goals/week post-bootstrap

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

This feature is configuration-only. Most constitutional principles target mcpproxy-go's runtime code and **do not apply** here.

| Principle | Applies? | Status |
|---|---|---|
| I. Performance at Scale | No (no MCPProxy code) | N/A |
| II. Actor-Based Concurrency | No | N/A |
| III. Configuration-Driven Architecture | No (Paperclip's config, not MCPProxy's) | N/A |
| IV. Security by Default | **Yes** | ✅ FR-012 mandates loopback binding + `local_trusted` mode + secret rotation gate. SC-008 measures empirically. mcpproxy's own security defaults are not weakened. |
| V. Test-Driven Development | **Yes** (adapted) | ✅ T-006 synthetic-goal smoke test is the test. No Go code → no Go test surface. |
| VI. Documentation Hygiene | **Yes** | ✅ Design doc + spec serve as canonical documentation. Optional `docs/agent-cockpit.md` (T-005) is the user-facing pointer. CLAUDE.md does not need updating because this feature changes no MCPProxy commands or architecture. |

**Architecture Constraints** (Core+Tray, Event-Driven, DDD, Upstream Client Modularity) — **N/A**; the feature adds no code that could violate them.

**Result**: GATE PASSES. The constitution did not envision configuration-only features; the principles that do apply are explicitly satisfied. See Complexity Tracking for the explicit exceptions.

**Post-Phase-1 re-check**: see end of plan.

## Project Structure

### Documentation (this feature)

```text
specs/045-paperclip-cockpit/
├── spec.md                       # /speckit.specify output (done)
├── plan.md                       # This file
├── research.md                   # Phase 0 — open questions resolved
├── data-model.md                 # Phase 1 — entities (mostly external to mcpproxy-go)
├── contracts/
│   └── external-tools.md         # Phase 1 — Paperclip MCP + Synapbus MCP tools we depend on (de facto contracts)
├── quickstart.md                 # Phase 1 — step-by-step bootstrap walkthrough
├── checklists/
│   └── requirements.md           # Spec quality checklist (done)
└── tasks.md                      # /speckit.tasks output (NOT created by /speckit.plan)
```

### Implementation Files (mostly OUTSIDE the repo)

```text
~/.paperclip/instances/default/companies/16edd8ed-…/agents/<agent-id>/instructions/
├── AGENTS.md     # one per agent (7 total)
├── SOUL.md       # CEO only
├── HEARTBEAT.md  # CEO only
└── TOOLS.md      # CEO only

Synapbus wiki articles (created via mcp__synapbus__execute action=create_article):
├── mcpproxy-roadmap
├── mcpproxy-architecture-decisions
└── mcpproxy-shipped

Optional, in repo:
docs/agent-cockpit.md             # T-005 — one-page overview pointing at the design doc
```

**Structure Decision**: There is no `src/` because the feature is operational configuration. The "source files" are markdown prompts in `~/.paperclip/` and wiki articles in Synapbus. The mcpproxy-go repo carries only the spec, the design doc, and optionally a one-page pointer.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Feature ships no source code in mcpproxy-go | Cockpit is the integration of two existing external platforms (Paperclip + Synapbus); the agents drive both via MCP tools that already exist | Adding a Go shim to `internal/` would be incidental code with no consumer; the platforms expose the necessary MCP surfaces directly to agents |
| TDD adapted to smoke test only | No Go/Vue/Swift surface to unit-test | A traditional `_test.go` would assert against nothing; the synthetic-goal smoke test (T-006) exercises all three user stories end-to-end and is the meaningful gate |
| Three artifact systems for one feature (git specs, ~/.paperclip prompts, Synapbus wiki) | This *is* the design — each system holds the artifact best suited to its medium (durable design in git, agent prompts in Paperclip, evolving institutional memory in wiki) | Forcing all artifacts into git would mean copying agent prompts back-and-forth on every Paperclip update, and wiki articles would not benefit from cross-link semantics |

## Phase Outputs

- **Phase 0** → `research.md` (Open Questions resolved)
- **Phase 1** → `data-model.md`, `contracts/external-tools.md`, `quickstart.md`
- **Phase 2** (next command) → `tasks.md` via `/speckit.tasks`

## Post-Phase-1 Constitution Re-check

After Phase 1 artifacts (`data-model.md`, `contracts/external-tools.md`, `quickstart.md`) are written, the design holds:

- No new code surfaces in mcpproxy-go (Phase 1 confirms entities live in Paperclip + Synapbus DB)
- No security regressions: contracts confirm we use existing MCP tools without bypassing auth
- No documentation drift: artifacts are self-contained under `specs/045-paperclip-cockpit/`

**Result after Phase 1**: GATE STILL PASSES.
