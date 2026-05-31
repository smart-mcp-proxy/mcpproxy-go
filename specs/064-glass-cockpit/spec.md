# Feature Specification: Glass Cockpit — Transparent & Steerable Agent Cockpit

**Feature Branch**: `064-glass-cockpit`
**Created**: 2026-05-31
**Status**: Planned (spec + plan complete)
**Lineage**: Extends [`045-paperclip-cockpit`](../045-paperclip-cockpit/) (the running cockpit). Supersedes the fresh-dev-instance approach of [`062-mcpproxy-paperclip-mvp-orchestrator`](../062-mcpproxy-paperclip-mvp-orchestrator/) by reusing the already-running instance. Adds a three-gate steerability + transparency model.
**Input**: Make the existing agent cockpit (spec 045, running on the local Paperclip control-plane) transparent and steerable, so the user can set a high-level goal and have the agent fleet research → draft specs → implement → test → QA → open a PR, while being consulted **only** at high-level design-decision boundaries.

## Clarifications

### Session 2026-05-31

- Q: Where should the orchestrator live? → A: Reuse the existing agent cockpit (spec 045) running on the local Paperclip control-plane. No fork or new instance for the first phases.
- Q: What is SynapBus's role? → A: Append-only audit log + wiki only. It is beta and MUST NOT be in the critical path; nothing blocks on it.
- Q: Which checkpoints are mandatory human gates? → A: Three — (1) plan-of-attack / decomposition review, (2) per-spec design sign-off, (3) pre-merge. Everything else runs autonomously.
- Q: How invasive should the work be? → A: Phased — config-only first (the dry-run target), then a control-plane plugin for the transparency UI, then a fork only if the first two fall short.
- Q: What is the first proof? → A: A dry-run synthetic goal — a trivial, reversible repository change ("add a 'Running the test suite' note to CONTRIBUTING.md") that traverses every stage and all three gates to a real PR.
- Q: What was wrong with the prior cockpit (045)? → A: It had a single, late, binary approval gate on a finished synthesis. The human approved a pre-framed conclusion instead of steering the framing. This feature inverts the default from "proceed" to "checkpoint at every design-decision boundary," with structured redirection.

### Session 2026-05-31 (Session 2 — gate model: human-merge → dual-AI auto-merge)

- Q: Paperclip never merges PRs — is that broken? → A: No, it was by design (FR-005 = human is sole merger). But the human merge-click became the throughput bottleneck (a queue of unmerged PRs). Decision: **replace the mandatory human-merge gate with dual-AI-review consensus auto-merge.**
- Q: Where does the human fit now? → A: **Optional third reviewer with veto.** Two AI reviewers accepting + green checks → auto-merge; the human can block/hold any PR at any time. (Amends US3 + FR-005.)
- Q: Who are the two reviewers? → A: **Gemini Critic + a Codex reviewer**, on different model families (model diversity preserved); never the implementer. Gemini pinned to `gemini-2.5-pro` (best available; `auto` was erroring on the empty-prompt adapter bug). A `codex-local` Paperclip adapter exists.
- Q: Blocker? → A: **A separate bot GitHub identity is required** — GitHub forbids a PR author from approving their own PR, and the agents currently act as the human's `gh` identity (Dumbris). Until a bot identity (fine-grained PAT or GitHub App) exists, AI "approvals" cannot gate a merge. This is the prerequisite for true auto-merge; flagged in Assumptions/Dependencies. Interim fallback: 2-AI-review as a required status check with the human still clicking merge.
- Q: Scope? → A: Amend this spec (064) + the agent instructions (draft-PR + reviewer protocol) + GitHub branch protection (require checks + 2 approvals + the review identities).

### Session 2026-05-31 (spec review)

- Q: If the different-model-family reviewer is down or quota-exhausted, should the review gate block forever? → A: No — add a user-initiated, audited waiver (FR-011a). A board decision, not an agent self-bypass.
- Note: Two review suggestions were evaluated against the live platform and **not** adopted as written, because the platform already provides the capability natively (mechanism mapping deferred to plan.md): (1) a "Phase A has no consolidated view, use a CEO-maintained wiki page" suggestion — rejected because the platform exposes native waiting-counts (`sidebar-badges`), a native blocked-attention issue filter, and a native approvals page; Phase B *fuses* these rather than inventing them. (2) a "parse `DROP 4`/`SPLIT 3` chat commands for redirection" suggestion — rejected because the platform's `suggest_tasks` interaction renders a natively selectable/editable task tree; split/reorder beyond it is handled by reject-with-reason → re-plan. No brittle command parser is needed.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Steer the plan of attack before any work begins (Priority: P1)

The user posts a high-level goal. The fleet's lead agent researches it and proposes how it will break the goal into a set of specs/tasks. **Before any of those tasks are created**, the lead agent stops and presents the proposed breakdown to the user for approval. The user can accept it, or **redirect** it — drop a task, merge two, change the order, or reject the whole framing with a reason and have it re-planned. Only after the user accepts does the fleet create the task tree and begin.

**Why this priority**: This is the keystone fix for the prior cockpit's un-steerability. Catching a wrong decomposition here costs one conversation; catching it after implementation costs hours of wasted agent work. If only this gate existed, the user would already have meaningful control over the most expensive decision.

**Independent Test**: Post a goal; confirm the lead agent produces a visible decomposition proposal and creates **zero** downstream tasks until the user acts; confirm the user can edit/drop/reorder items and that the executed plan reflects the edits; confirm a reject-with-reason produces a revised proposal rather than proceeding.

**Acceptance Scenarios**:

1. **Given** a newly posted goal, **When** the lead agent finishes researching and proposing a breakdown, **Then** the proposal is surfaced to the user and no spec/child tasks are created until the user responds.
2. **Given** a pending decomposition proposal, **When** the user removes one proposed item and accepts the rest, **Then** only the accepted items are created as tasks.
3. **Given** a pending decomposition proposal, **When** the user rejects it with a written reason, **Then** the lead agent produces a revised proposal incorporating that reason, and still does not proceed without acceptance.

---

### User Story 2 — Approve or redirect each spec's design before code is written (Priority: P1)

For each spec the fleet produces, the responsible engineer agent must get the user's sign-off on the **design** before writing any implementation code. The spec parks in a "needs review" state and blocks. The user approves to proceed, or requests changes with a comment, which sends the spec back to the engineer with that feedback.

**Why this priority**: Design is the second-most-expensive decision after decomposition. A blocking, per-spec design gate is exactly the "high-level design decision" the user wants to own, and it prevents the fleet from building the wrong thing correctly.

**Independent Test**: Drive a single spec to the design-review point; confirm it blocks (no code/PR appears) until the user approves; confirm "request changes" returns it to the engineer with the comment attached and it blocks again until re-approved.

**Acceptance Scenarios**:

1. **Given** a drafted spec, **When** the engineer reaches the design-review point, **Then** the spec blocks in a review state and no implementation begins until the user approves.
2. **Given** a spec awaiting design review, **When** the user requests changes with a comment, **Then** the spec returns to the engineer carrying that comment and re-enters the blocking review state after revision.

---

### User Story 3 — Code lands via dual-AI review consensus, with human veto (Priority: P1)

> **Amended 2026-05-31 (Session 2 — see Clarifications).** Original model: agents open PRs, the human is the sole merger. New model: an engineer opens a **draft** PR; once the project's tests pass **and two independent AI reviewers on different model families both accept**, the PR auto-merges. The human is an **optional third reviewer with veto** — they may block or hold any PR at any time, but their merge click is no longer required on the happy path. This trades the mandatory human-merge gate for AI-consensus throughput while keeping a human override.

When an engineer agent finishes implementation, it opens a **draft** PR (not directly mergeable). Two reviewer agents on **different model families** (a Gemini Critic + a Codex reviewer) each review; the implementer never reviews its own work. When tests pass **and both reviewers accept**, the PR is marked ready and **auto-merges**. The human may, at any point, request changes or apply a hold label to freeze auto-merge — that veto is honored over the AI consensus.

**Why this priority**: This is the throughput unlock — it removes the human merge-click bottleneck (the queue of unmerged PRs) while preserving safety through model-diverse review consensus plus a standing human veto. Model diversity (two families) is the safeguard that one model's blind spot does not auto-land.

**Independent Test**: Drive a work item to completion; confirm a *draft* PR opens, that it does NOT merge until both AI reviewers accept and checks are green, that it then auto-merges, and that a human request-changes/hold at any point freezes the merge until cleared.

**Acceptance Scenarios**:

1. **Given** completed implementation, **When** the engineer finishes, **Then** a **draft** PR exists and does not auto-merge while it is draft or while checks are pending.
2. **Given** a draft PR with green checks, **When** both AI reviewers (different model families) accept, **Then** the PR auto-merges without a human merge click.
3. **Given** any open PR, **When** the human requests changes or applies the hold label, **Then** auto-merge is frozen until the human clears it — the human veto overrides AI consensus.
4. **Given** only one AI reviewer has accepted (or a reviewer is erroring/unavailable), **When** checks are green, **Then** the PR does NOT auto-merge (two distinct accepts are required; the human may stand in as the second reviewer if a reviewer is down).

---

### User Story 4 — See the reasoning and everything awaiting you, in one place (Priority: P2)

At every gate, the user can see **why** the agent proposed what it did — the rationale and at least one cited source — without digging through logs. Separately, a single view shows everything currently waiting on the user (pending decompositions, pending design reviews, pending merges, and any frozen subtrees), so the user always knows what, if anything, needs their attention.

**Why this priority**: Steerability is hollow without transparency — approving a proposal you can't understand is just the old binary gate again. The consolidated "waiting on you" view is what lets the user step away and return to a clear, bounded to-do list (the no-babysitting payoff). P2 because the gates (P1) can function with the platform's existing per-item views first; this elevates them into a usable cockpit.

**Independent Test**: At a live gate, confirm the rationale + ≥1 citation are visible from the approval surface itself. With several items pending, confirm a single view lists exactly those items and its count matches reality.

**Acceptance Scenarios**:

1. **Given** any pending gate, **When** the user opens it, **Then** the agent's rationale and at least one cited source are visible without leaving that surface.
2. **Given** N items awaiting the user across different goals, **When** the user opens the consolidated view, **Then** it shows exactly those N items and nothing already resolved.

---

### User Story 5 — Run autonomously between gates (no babysitting) (Priority: P2)

Between the three gates, the fleet works without human input: it researches, writes specs, implements with test-driven rigor, runs the project's mandatory tests, produces a QA report, and gets adversarial review — surfacing to the user only when it reaches a gate, gets genuinely blocked, or finishes.

**Why this priority**: The entire motivation is to stop deciding small things. The gates define where the human IS consulted; this story defines that everywhere else is hands-off. P2 because it depends on the gates (P1) existing to bound the autonomy.

**Independent Test**: For the dry-run goal, count human interactions between gates; confirm it is zero (no tactical approvals, tool prompts, or step-by-step confirmations) — the only human touches are the three gates.

**Acceptance Scenarios**:

1. **Given** an accepted plan of attack, **When** the fleet works toward the next gate, **Then** it completes research/spec/implementation/test/QA with no human inputs other than the defined gates.
2. **Given** mandatory project tests, **When** an engineer completes implementation, **Then** the tests and a QA report were run automatically as part of reaching the pre-merge gate.

---

### User Story 6 — Pause or stop a run at any time (Priority: P3)

The user can freeze or cancel any goal or subtree at any moment, with a preview of what will be affected, and resume later. Frozen work stops immediately; in-flight agent runs are halted.

**Why this priority**: A safety/control affordance for when something looks wrong between gates. P3 because the three gates plus conservative budgets already bound risk; this is the explicit override.

**Independent Test**: With a goal mid-run, trigger a freeze; confirm a preview lists the affected items, work halts, and a later resume continues from where it stopped.

**Acceptance Scenarios**:

1. **Given** a running subtree, **When** the user freezes it, **Then** a preview of affected items is shown and execution halts until resumed.

## Requirements *(mandatory)*

### Context & Constraints (locked)

- **CN-001**: The cockpit MUST reuse the already-running agent control-plane and its existing company/roster (the spec-045 setup). No fork or new instance is created for Phases A and B.
- **CN-002**: Revival of the existing roster MUST be **non-destructive**: existing historical work items are left untouched; new work uses freshly created goals; only the agents needed for a run are activated.
- **CN-003**: The audit/knowledge bus (SynapBus) MUST be used only as an append-only log and wiki, and MUST NOT be on any critical path. The pipeline MUST complete even if it is unavailable.
- **CN-004**: Delivery MUST be phased: (A) configuration + agent-instruction only; (B) a control-plane plugin for the transparency UI; (C) a fork, only if A and B prove insufficient.

### Functional Requirements

- **FR-001**: The system MUST require explicit human approval at three points before irreversible progress: (a) the goal decomposition (plan of attack), (b) each spec's design, and (c) merging to the main branch.
- **FR-002**: At the plan-of-attack gate, the lead agent MUST present its proposed breakdown and MUST NOT create downstream tasks until the user accepts.
- **FR-003**: Each gate MUST support **structured redirection**, not only accept/reject: at minimum, editing the decomposition (add/drop/reorder/split) and rejecting-with-reason at the plan gate, and request-changes-with-comment at the design and merge gates.
- **FR-004**: A spec at the design gate MUST block (no implementation begins) until approved; "request changes" MUST return it to the responsible agent with the comment attached.
- **FR-005** *(amended 2026-05-31, Session 2)*: Code MUST land via **dual-AI-review consensus auto-merge**, not a mandatory human merge. Specifically: (a) engineers open **draft** PRs; (b) a PR MAY become merge-eligible only when the project's required checks pass AND **two independent AI reviewers on different model families both accept**; (c) the implementer agent MUST NOT be one of the two reviewers (no self-review); (d) merge-eligible PRs **auto-merge**; (e) the human is an **optional reviewer with override** — a human request-changes or hold label MUST freeze auto-merge until cleared; (f) if a reviewer is unavailable, the PR MUST NOT auto-merge on a single accept (the human may serve as the second reviewer). The implementer still MUST NOT merge its own PR, alter branch protection, or push to `main` directly.
- **FR-005a**: The two AI reviewers MUST run on different model families to preserve model-diversity coverage (current roster: a **Gemini** Critic + a **Codex** reviewer). A reviewer's `accept` MUST come from an identity distinct from the PR author so the platform's "no self-approval" rule is satisfiable (requires a bot identity for the agents — see Assumptions).
- **FR-006**: At each gate, the system MUST surface the responsible agent's rationale and at least one cited source on the approval surface itself.
- **FR-007**: The system MUST provide a single consolidated view of all items currently awaiting the user, with an accurate count.
- **FR-008**: Between gates, the fleet MUST operate without human input, surfacing only on a gate, a genuine block, or completion.
- **FR-009**: For any implementation work, the responsible agent MUST follow the project's spec-driven workflow (specify → plan → tasks → implement) and test-first discipline.
- **FR-010**: Before reaching the pre-merge gate, the system MUST run the project's mandatory tests and produce a QA report as part of the work item's evidence.
- **FR-011**: Each produced change MUST receive an adversarial review from a reviewer running on a **different model family** than the implementer (model diversity), and that review MUST cite specifics.
- **FR-011a**: If the different-model-family reviewer is unavailable (down, unreachable, or budget-exhausted), the work item MUST surface as blocked rather than proceed unreviewed; the user (as board) MAY explicitly **waive** the adversarial review for that specific item to unblock it. A waiver is a human decision recorded in the audit trail (FR-013), not an agent-initiated bypass.
- **FR-012**: The user MUST be able to freeze/cancel any goal or subtree at any time, see a preview of affected items before doing so, and resume later.
- **FR-013**: The system MUST record an append-only audit trail of gate decisions and key milestones to the log/wiki bus, on a best-effort basis that never blocks the pipeline (per CN-003).
- **FR-014**: Each agent MUST cite the source of any claim that influences a decision (provenance); uncited material MUST NOT silently drive decisions.
- **FR-015**: Agent spending MUST be bounded by conservative per-agent budgets, given that automated cost tracking is not available in the platform.
- **FR-016**: The feature MUST be demonstrable end-to-end via a dry-run synthetic goal that is trivial and reversible yet produces a real PR, exercising all three gates.

### Key Entities

- **Goal**: A high-level objective the user posts. Owns a tree of work items and traces to the company objective.
- **Plan-of-attack proposal**: The lead agent's proposed decomposition of a goal into specs/tasks, presented for approval before tasks exist; carries rationale + sources and is editable by the user.
- **Spec work item**: A unit of work whose design must be approved before implementation; can be sent back with comments.
- **Gate / approval**: A blocking checkpoint assigned to the user with a decision (approve / request-changes / reject-with-reason) recorded for audit.
- **Reasoning + provenance**: The rationale and cited sources attached to a proposal or work item, visible at the gate.
- **Hold (pause/cancel)**: A freeze applied to a goal/subtree with an impact preview; releasable to resume.
- **Audit/wiki entry**: A best-effort, append-only record of decisions and milestones; never on the critical path.
- **Agent roles**: Lead (decompose/route), Engineers (implement, one per area), QA (mandatory tests + report), Reviewer (adversarial, different model family).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For any new goal, the user is consulted at no fewer than three distinct decision points (decomposition, each spec's design, pre-merge) before any irreversible step.
- **SC-002**: In the dry-run, 100% of gate-protected actions (task creation, implementation start, merge) occur only after explicit human approval — zero occur without it.
- **SC-003**: At every gate, the user can reach the agent's rationale and at least one cited source without leaving the approval surface.
- **SC-004**: At the plan-of-attack gate the user can apply a structural redirection (e.g., reduce the number of specs) and the executed plan reflects the change exactly.
- **SC-005**: For the dry-run goal, the number of human interactions between gates is zero.
- **SC-006**: The dry-run synthetic goal traverses research → plan-of-attack gate → spec → design gate → implement → test → QA → PR → pre-merge gate → merged, with each gate observed to block until the user acted.
- **SC-007**: The consolidated "waiting on you" view's item count matches the actual number of pending items at all times during the dry-run.
- **SC-008**: If the audit/wiki bus is unavailable, the pipeline still completes the dry-run goal end-to-end (audit entries are deferred or skipped, never blocking).
- **SC-009**: The user can freeze a running subtree and see an accurate preview of affected items before execution halts.
- **SC-010**: No pull request is merged by any agent across the dry-run (all merges are performed by the user).
- **SC-011**: When the different-model-family reviewer is unavailable, the affected item does not advance unless the user issues an audited waiver; no item ever advances past review with neither a review nor a waiver.

## Assumptions

- The running control-plane and its company/roster are authoritative and current; the platform's gate, interaction, pause/resume, decomposition, audit, and plugin capabilities behave as documented in its installed version.
- The target repository (mcpproxy-go) has (or will have) branch protection that prevents non-user merges.
- Implementer and reviewer agents have the necessary model credentials (implementers on one model family, the reviewer on a different one).
- Conservative per-agent budgets are acceptable as the interim guard while platform cost tracking is unavailable.
- The dry-run synthetic goal is "add a short 'Running the test suite' note to CONTRIBUTING.md" unless the user substitutes another trivial, reversible task.
- The user is the sole "board" approver for the dry-run; multi-approver workflows are out of scope initially.

## Dependencies

- The running agent control-plane (Paperclip) and its existing MCPProxy company/roster (spec 045).
- The mcpproxy-go repository, its CI, and its existing spec-driven and QA skills/scripts (mandatory tests, QA report generation).
- GitHub branch protection for the pre-merge gate.
- The audit/knowledge bus (SynapBus) — optional and non-blocking (per CN-003).

## Out of Scope

- Forking or patching the control-plane's core (Phase C) — deferred unless Phases A/B prove insufficient.
- Implementing automated cost/token tracking (interim: conservative budgets + manual watch).
- Adding new agent roles beyond the existing roster.
- Concurrent execution of multiple high-level goals (initial focus is one goal at a time, proven via the dry-run).
- Porting Web-UI features to the macOS app or implementing umbrella-spec tracks — those are *future goals to run through* the cockpit, not part of building it.

## Edge Cases

- **Agent skips the plan-of-attack convention** (Phase A enforces it by instruction, not by the platform core): the subsequent per-spec design gate and the adversarial reviewer catch unsanctioned work; Phase C makes the gate enforced by the platform.
- **Audit/wiki bus down**: the pipeline proceeds; audit writes are deferred or skipped (per CN-003 / SC-008).
- **Agent attempts a self-merge / self-review**: refused — the implementer is excluded from the two reviewers (FR-005c) and cannot approve its own PR (bot identity ≠ author); branch protection enforces the required checks + two distinct approvals (FR-005 amended).
- **A reviewer (e.g. Gemini Critic) is down/erroring**: the PR does NOT auto-merge on a single accept; the human may serve as the second reviewer (FR-005f). (This is the live state today — the Gemini adapter empty-prompt bug — so the human-as-second-reviewer fallback matters.)
- **No bot identity provisioned yet**: auto-merge cannot function (GitHub forbids self-approval); the system falls back to 2-AI-review-as-required-check with the human performing the merge click until a bot identity exists.
- **Runaway or looping agent**: bounded by conservative budgets (FR-015) and the user's freeze/cancel control (FR-012).
- **A gate is left pending indefinitely**: the work item stays safely blocked; no progress is made and no timeout auto-approves.
- **Reviewer (different model family) is unavailable**: the work item surfaces as blocked rather than proceeding unreviewed; the user may issue an audited waiver to unblock that specific item (FR-011a). No agent may self-bypass review.
