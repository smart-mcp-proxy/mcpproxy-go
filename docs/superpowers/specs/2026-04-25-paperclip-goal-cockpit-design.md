# Paperclip Goal Cockpit for MCPProxy — Design

**Date:** 2026-04-25
**Status:** Brainstorm complete, ready for speckit spec
**Implementation tracking:** `specs/NNN-paperclip-goal-cockpit/` (to be created via `/speckit.specify` against this doc)

## Goal

Use Paperclip AI as a strategic cockpit for MCPProxy development. The user sets high-level goals; agents decompose, propose, and implement under approval gates. Direct Claude Code (CC) sessions remain the daily tactical tool. Speckit remains the formal spec format for substantive features.

## Non-goals

- Replacing direct CC sessions for brainstorming and investigation
- Replacing GitHub PR review for code merge
- Two-way sync between speckit specs (in git) and Paperclip issues
- Using `/superpowers:writing-plans` (replaced by `/speckit.plan`)
- Marketing/PM agents on day one
- Paperclip ticket for bugs trivial enough for one CC session

## Architecture

Three entry points feed one cockpit (Paperclip "MCPProxy" company), producing two output streams: PRs to git, and summaries+wiki updates to Synapbus.

```
                    ┌─────────────────────────────────┐
  HIGH-LEVEL GOAL → │ Paperclip CEO ticket            │ → autonomous flow
                    └─────────────────────────────────┘
                    ┌─────────────────────────────────┐
  BRAINSTORM /      │ Direct Claude Code session      │ → interactive
  INVESTIGATE   →   │ (/superpowers:brainstorming +   │
                    │  manual subagent dispatch)      │
                    └─────────────────────────────────┘
                    ┌─────────────────────────────────┐
  KNOWN AD-HOC →    │ Paperclip ad-hoc issue          │ → direct to expert
                    └─────────────────────────────────┘
```

### Why three separate entry points

Each captures a different mental mode. CC sessions handle exploration and trivial fixes (zero overhead). Paperclip CEO tickets handle goal-shaped work needing multi-agent decomposition. Paperclip ad-hoc tickets handle known-scoped work that doesn't need decomposition (e.g., a marketing task with a clear ask, or a bug pre-classified by a human). Forcing all three through one funnel either burns budget on trivial fixes or undermines the autonomous flow's value.

## Agent Roster (initial 7)

Per-role agent instruction files live under `~/.paperclip/instances/default/companies/<company-id>/agents/<agent-id>/instructions/`.

| Agent | Mandate | Instruction files |
|---|---|---|
| **CEO** | Decompose goals, dispatch experts, synthesize, route spec-vs-no-spec, maintain wiki | `AGENTS.md`, `SOUL.md`, `HEARTBEAT.md`, `TOOLS.md` |
| **Backend Engineer** | Go in `internal/`, `cmd/` | `AGENTS.md` |
| **Frontend Engineer** | Vue/TS in `frontend/` | `AGENTS.md` |
| **macOS Engineer** | Swift in `native/macos/` | `AGENTS.md` |
| **QA Tester** | Test plans, runs via `mcpproxy-ui-test` MCP + Chrome ext, HTML reports | `AGENTS.md` |
| **Critic** | Adversarial review of proposals, test plans, PRs | `AGENTS.md` |
| **Release Engineer** | nfpm packaging, CI, R2 distribution, prerelease cuts | `AGENTS.md` |

Marketing / PM agents deferred until a marketing goal is in flight.

CEO is the only multi-file agent because routing/synthesis benefits from a "soul" (consistent voice), heartbeat (regular roadmap freshness sweep), and explicit tool catalog (Synapbus search, wiki edit, `paperclipUpsertIssueDocument`, etc.).

### Provisioning

Paperclip's `requireBoardApprovalForNewAgents` stays **on**. The initial roster of 7 is approved together as a one-time setup. Adding a new agent type later (e.g., Marketing) is a deliberate board-approval event, not casual.

## Goal → Ship Flow

```
User posts goal text to Paperclip CEO ticket
  │
  ↓ CEO queries Synapbus + wiki for context
  │   (search_messages, list_articles, #news-mcpproxy, #open-brain)
  │
CEO chooses 1–3 experts based on goal scope
  │
  ↓ Experts query Synapbus for domain context (cite sources)
  │
Experts post proposals as Paperclip documents on the ticket
  (paperclipUpsertIssueDocument)
  │
Critic reviews each proposal — adversarial pass, comments
  │
CEO synthesizes ONE recommendation: options A/B/C + tradeoffs + recommended option
  │
👤 USER reacts approve / reject / request_changes  ← only mandatory human gate
  │
CEO routing rule: is this big?
  (≥3 file areas OR data/security/release impact OR user said "spec it")
  ├── YES → expert runs /speckit.specify → /speckit.plan → /speckit.tasks → /speckit.implement
  │           one Paperclip issue holds the spec dir name as a string ref
  │           speckit spec lives in git under specs/NNN-name/
  └── NO  → expert opens PR directly, no spec
  │
QA Tester auto-triggers when PR opens:
  drafts plan → Critic reviews → Tester runs (ui-test MCP / Chrome ext) → HTML report attached
  │
CEO updates Synapbus wiki:
  - mcpproxy-roadmap (in-flight → recently-shipped)
  - mcpproxy-architecture-decisions (if synthesis chose B over A/C)
  - mcpproxy-shipped (append-only log)
  │
Synapbus #my-agents-algis: shipped summary
  │
QA-found regressions → new Paperclip ad-hoc issues, routed to same expert
```

## Direct Claude Code Sessions (preserved unchanged)

- **Brainstorming** — `/superpowers:brainstorming` → design doc at `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`. *Replaces `/superpowers:writing-plans` with `/speckit.specify`* against the design doc.
- **Investigation** — interactive Explore / general-purpose subagents from CC, free-form. No Paperclip touch unless the user files a ticket.
- **Direct fixes** — trivial bugs: edit, commit, ship.

## Speckit Usage Rules

| Trigger | Speckit? |
|---|---|
| CEO judges feature is "big" (≥3 file areas OR data/security/release impact OR multi-day) | **YES** |
| User says "spec this" explicitly | **YES** |
| Direct CC brainstorm produces an approved design doc | **YES** — promote with `/speckit.specify` |
| QA-found regression, ad-hoc Paperclip bug, marketing task | **NO** |
| One-file UI fix, typo, doc tweak | **NO** |

## Approval Mechanics

- **Primary** — Paperclip reaction `approve` / `reject` / `request_changes` on the CEO synthesis comment.
- **High-stakes only** (release cut, secret rotation, breaking change, telemetry schema bump) — also posts to Synapbus `#approvals` requiring explicit text reply. Two-key turn.
- **Code review** — standard GitHub PR review. Agents NEVER merge their own PRs without a human green light.
- **Budget caps** — Paperclip's per-agent budget acts as a hard ceiling (auto-pause on overspend).

## Synapbus Integration (bidirectional)

### Read — Paperclip pulls context BEFORE deciding

| Stage | Who | Reads from |
|---|---|---|
| CEO decomposition | CEO | `search_messages` for goal topic; `#news-mcpproxy`; `#open-brain` historical memories |
| Expert proposal | Backend / Frontend / macOS | Domain-relevant search; `#open-brain`; wiki articles via `list_articles` |
| QA test plan | QA Tester | `#bugs-mcpproxy` for prior failure patterns |
| Critic review | Critic | `#open-brain` for "we tried this before" precedents |

**Provenance rule** — every proposal cites the Synapbus messages and wiki articles it drew from (message IDs, `[[slug]]` cross-links). No silent influence.

### Write — Paperclip posts decisions/summaries + wiki

| Event | Channel | Priority |
|---|---|---|
| Goal approved & dispatched | `#my-agents-algis` | 4 |
| PR opened by agent | `#my-agents-algis` | 4 |
| QA HTML report ready | `#my-agents-algis` | 5 |
| Shipped (PR merged) | `#my-agents-algis` | 5 |
| QA-found regression | `#bugs-mcpproxy` | 7 |
| High-stakes approval request | `#approvals` | 8 |

### Wiki articles owned by Paperclip CEO

| Article slug | Updated when | Content |
|---|---|---|
| `mcpproxy-roadmap` | every goal approved + every ship | *In flight*, *Recently shipped (last 30d)*, *Planned*, *Parked*. Cross-links to `[[spec-NNN-name]]` and Paperclip ticket IDs. Rewritten in full each update (not appended). |
| `mcpproxy-architecture-decisions` | each synthesis where CEO chose B over A/C | One entry per decision: context, options considered, choice + reasoning, source links. |
| `mcpproxy-shipped` | each merged PR | Append-only log: date, one-paragraph "what changed and why". |

**Anti-spam** — one Synapbus post per milestone, one wiki update per shipped goal (not per task within).

## Out of Scope

- Two-way sync between speckit specs and Paperclip issues
- `/superpowers:writing-plans` (replaced by `/speckit.plan`)
- Paperclip ticket for bugs trivial enough for one CC session
- Agent-orchestrated brainstorming (stays interactive in CC)
- Marketing/PM agents

## Implementation Notes

Most "code" lives outside the mcpproxy-go repo:

- **Paperclip agent instruction files** — `~/.paperclip/instances/default/companies/<id>/agents/<id>/instructions/*.md`
- **Synapbus wiki articles** — created via Synapbus MCP `create_article`, content seeded by hand from this design
- **Optional repo additions** — `docs/agent-cockpit.md` (overview pointing at this design); no code changes

Initial setup is bootstrap-by-hand (chicken-and-egg: CEO can't set itself up). Once the roster is alive, *future* changes to the cockpit (new agent, prompt revision) can flow through the cockpit itself.

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Paperclip in `local_trusted` mode is unauthenticated on loopback | Don't expose port 3100 on LAN; rotate `BETTER_AUTH_SECRET` and `PAPERCLIP_AGENT_JWT_SECRET` before any LAN access |
| Agent runaway burns Anthropic credit | Per-agent budget cap auto-pauses; review caps in Paperclip UI before first goal |
| CEO bad at spec-vs-no-spec routing | Initially conservative: prefer no-spec for ambiguous cases; iterate the routing rule based on real cases |
| Wiki edits from agents conflict with manual edits | All wiki updates go through CEO only; manual updates done outside CEO's working hours, or via CEO-issued tickets |
| Synapbus search returns stale info | Provenance rule means every cited source is dated and traceable; Critic flags decisions made on stale data |
| Direct CC sessions and Paperclip flow drift apart | Intentional — they serve different modes. Keep them strictly separated; no cross-pollination automation |

## Open Questions for Spec Phase

- Concrete `AGENTS.md` content for each role (templates exist in Paperclip's `paperclip-create-agent` skill — adapt rather than write from scratch)
- CEO heartbeat cadence (every N hours? on-demand only?)
- Wiki article seed content (initial roadmap snapshot derived from current `specs/` state)
- Whether `docs/agent-cockpit.md` is a useful repo addition or redundant with this design doc
