# Phase 1 — Data Model: Paperclip Goal Cockpit

**Date**: 2026-04-25

This is a **logical model**. Most entities live in external systems (Paperclip's embedded Postgres, Synapbus's DB, git). Nothing new is persisted inside mcpproxy-go.

## Entity Map (where each entity lives)

| Entity | Lives in | Created by | Lifecycle |
|---|---|---|---|
| **Goal** | Paperclip ticket | User | Open → Decomposing → Proposed → Approved/Rejected → Implementing → Shipped/Cancelled |
| **Proposal** | Paperclip document attached to ticket | Expert agent | Drafted → Reviewed (by Critic) → Included-in-synthesis |
| **Synthesis** | Paperclip ticket comment | CEO agent | Drafted → Awaiting-approval → Approved/Rejected/Changes-requested |
| **Spec Reference** | string field on Paperclip ticket | CEO agent | Created when goal is "big"; immutable thereafter (points at git path) |
| **Speckit Spec** | git repo (`specs/NNN-<short>/`) | Implementation agent via `/speckit.*` | Standard speckit lifecycle (independent of Paperclip) |
| **PR** | GitHub | Implementation agent | Standard GitHub PR lifecycle (mandatory human merge) |
| **Test Plan** | Paperclip document attached to ticket | QA Tester agent | Drafted → Reviewed (by Critic) → Executed → Reported |
| **Test Report** | HTML attachment on Paperclip ticket | QA Tester agent | Generated when QA execution completes; immutable |
| **Wiki Article** | Synapbus DB (slugged article) | CEO agent | Three articles, all maintained by CEO heartbeat + on-ship updates |
| **Provenance Citation** | inline reference in Paperclip document | Any agent | Created on every proposal claim; immutable |
| **Synapbus Channel Post** | Synapbus channel message | CEO / experts | Append-only |
| **Agent** | Paperclip agent record | User (one-time bootstrap) | Created during T-001; updated only via board approval (FR-011) |
| **Agent Instructions** | filesystem (`~/.paperclip/.../instructions/`) | User (T-002) | Updated by user (or by CEO via dedicated meta-goal) |

## Entity Schemas (logical)

### Goal

| Field | Type | Notes |
|---|---|---|
| ticket_id | int (Paperclip-issued) | Primary identifier in Paperclip |
| title | string | Short user-supplied title |
| description | text | Full goal text |
| state | enum | Open, Decomposing, Proposed, Approved, Rejected, Implementing, Shipped, Cancelled |
| created_at | timestamp | |
| spec_ref | string \| null | When set, looks like `specs/045-paperclip-cockpit/`; null for small-route goals |
| pr_url | string \| null | Set when implementation opens a PR |
| budget_consumed_usd | decimal | Rolled up from Paperclip's per-agent metering |

### Proposal

| Field | Type | Notes |
|---|---|---|
| document_id | uuid | Paperclip document ID |
| ticket_id | int | Foreign key to Goal |
| author_agent | enum | One of: Backend, Frontend, macOS, QA, Release |
| content_md | text | Markdown body, must include cited sources |
| critique | text \| null | Critic's adversarial response (inline reply or sibling document) |
| created_at | timestamp | |

### Synthesis

| Field | Type | Notes |
|---|---|---|
| comment_id | int | Paperclip ticket comment ID |
| ticket_id | int | Foreign key to Goal |
| options | array of {label, description, tradeoffs} | Typically A / B / C |
| recommendation | string | Which option, with rationale |
| user_decision | enum \| null | approve / reject / request_changes; null until user reacts |
| user_decided_at | timestamp \| null | |

### Wiki Article (`mcpproxy-roadmap`)

| Section | Content shape |
|---|---|
| In flight | bulleted list: `- [[spec-NNN-name]] — short description (Paperclip ticket #N)` |
| Recently shipped (30d) | bulleted list: `- [[spec-NNN-name]] — short description — shipped YYYY-MM-DD (PR #NNN)` |
| Planned | bulleted list of approved goals not yet started |
| Parked | bulleted list of explicitly-deprioritized goals |

The article is **rewritten in full** each update — never appended (per FR-014).

### Wiki Article (`mcpproxy-architecture-decisions`)

Append-only list of entries. Each entry:

```markdown
## YYYY-MM-DD — <decision title>

**Goal**: <Paperclip ticket #N>
**Options considered**: A — <…>, B — <…>, C — <…>
**Decision**: B
**Rationale**: <why>
**Sources**: [[…]] cross-links
```

### Wiki Article (`mcpproxy-shipped`)

Append-only log. Each entry:

```markdown
## YYYY-MM-DD — <PR title>

**PR**: #NNN
**Spec**: [[spec-NNN-name]] (or "no spec — small route")
**Summary**: one paragraph "what changed and why"
```

### Provenance Citation

Inline within proposal body. Format:

```markdown
... according to [synapbus message #12345](synapbus://msg/12345) the retention drop ...
... per [[mcpproxy-architecture-decisions]] entry from 2026-03-15 ...
```

This is text-only; not a separate entity. Listed here because FR-003 makes it required.

## State Transitions

### Goal lifecycle

```
Open → Decomposing → Proposed → Approved → Implementing → Shipped
          │              │           │            │
          │              │           ↓            ↓
          │              ↓        Cancelled    Cancelled
          │           Rejected       (user)     (QA-blocking regression
          │           (user)                    that won't be fixed)
          ↓
       Cancelled (CEO unable to decompose)
```

### Synthesis approval

```
Drafted → Awaiting-approval → Approved
                            ↘ Rejected (Goal → Cancelled)
                            ↘ Changes-requested (back to CEO; new synthesis drafted)
```

## Validation Rules

These are derived from Functional Requirements:

| Rule | From | Enforced where |
|---|---|---|
| Goal MUST have a non-empty description before decomposition | FR-001 | CEO checks at start of decomposition; if empty, the goal is auto-cancelled |
| Proposal MUST contain at least one provenance citation | FR-003 | Critic refuses to mark "reviewed" if no citation; CEO refuses to include in synthesis |
| Synthesis MUST contain ≥2 options | spec User Story 1 acceptance | CEO drafts ≥2 options or asks user for clarification |
| Goal cannot transition Proposed → Implementing without `user_decision == approve` | FR-002 | Implementation agent checks before starting |
| PR cannot be merged by the agent that opened it | FR-005 | Branch protection on GitHub: required reviews from a human |
| Adding new agent (beyond initial 7) requires board approval | FR-011 | Paperclip's `requireBoardApprovalForNewAgents` (already on) |
| Paperclip MUST stay loopback-bound | FR-012 | User responsibility; verified periodically via SC-008 |

## Relationships

```
Goal (1) ─── (0..N) Proposal ─── (0..1) Critique
  │
  ├─ (0..1) Synthesis (carries user-approval gate)
  │
  ├─ (0..1) Spec Reference ─→ Speckit Spec in git
  │
  ├─ (0..N) PR
  │
  ├─ (0..N) Test Plan ─── (0..N) Test Report
  │
  └─ (0..N) Synapbus Channel Post (announcements only — read-only)

Wiki Article × 3 (independent of any single Goal; updated by CEO across goals)
```

## Anti-entities (explicitly NOT modeled)

- **No mcpproxy-go database tables**: nothing is persisted to BBolt or any new MCPProxy storage for this feature.
- **No JWT / session for agents**: Paperclip handles its own agent JWT; the cockpit does not issue or consume MCPProxy-side tokens.
- **No new MCPProxy REST endpoints**: agents talk to mcpproxy via existing endpoints (when they need to inspect MCPProxy itself); no new API is added.
