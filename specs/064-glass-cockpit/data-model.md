# Phase 1 Data Model: Glass Cockpit

This feature introduces **no new persistent storage**. It maps the spec's logical entities onto **existing Paperclip objects** (owned by Paperclip's embedded Postgres) and onto **version-controlled instruction/config files**. Below: each logical entity, its Paperclip backing, key fields, and state transitions.

## Entity map

### 1. Goal
- **Backing**: Paperclip `goals` (the high-level objective) + a root `issue` under it.
- **Key fields**: `id`, `title`, `description`, `level` (`company|team|agent|task`), `status` (`planned|active|achieved|cancelled`), `parentId`, `ownerAgentId`.
- **Glass Cockpit use**: the user posts one goal; the dry-run goal = "Add a 'Running the test suite' note to CONTRIBUTING.md". A **fresh** goal is created per run (CN-002 non-destructive).
- **Transitions**: `planned → active → achieved|cancelled`.

### 2. Plan-of-attack proposal
- **Backing**: a **plan `document`** (+ `document_revisions`) on the root issue, plus an `issue_thread_interactions` row of kind `request_confirmation` or `suggest_tasks` whose target references the plan-doc revision. Acceptance materializes children via `issue_plan_decompositions` (linking `acceptedPlanRevisionId` → created child issue IDs).
- **Key fields (interaction)**: `id`, `issueId`, `kind`, `status` (`pending|accepted|rejected|answered|cancelled|expired|failed`), `payload` (proposed children + rationale), `result` (user's edits/decision), `continuationPolicy`, `supersedeOnUserComment`.
- **Key fields (decomposition)**: `acceptedPlanRevisionId`, `requestedChildren[]` (each: `title`, `description`, `acceptanceCriteria[]`, `blockedByIssueIds[]`, `blockParentUntilDone`), `childIssueIds[]`, `status` (`in_flight|completed`), `requestFingerprint` (idempotent).
- **Glass Cockpit use**: **Gate 1**. CEO MUST raise the interaction and WAIT for `accepted` before calling `accepted-plan-decompositions`. The user edits the selectable tree (drop/keep/split) or rejects-with-reason (→ revised proposal).
- **Transitions**: `pending → accepted` (→ create children) | `pending → rejected` (→ re-plan) | `pending → expired` (superseded by a user comment).

### 3. Spec work item
- **Backing**: Paperclip `issue` (one node in the tree; `parentId` for hierarchy, `issue_relations` type `blocks` for dependencies).
- **Key fields**: `id`, `identifier` (e.g. `MCP-700`), `title`, `description`, `status` (`backlog|todo|in_progress|in_review|done|blocked|cancelled`), `workMode` (`standard|planning`), `assigneeAgentId|assigneeUserId`, `goalId`, `parentId`, `executionPolicy`, `executionState`, `planDocument`, `blockedBy[]`/`blocks[]`, provenance (`createdByAgentId/UserId`, `originKind`, `originRunId`).
- **Glass Cockpit use**: each spec the fleet drafts is an issue carrying an `executionPolicy` (Gate 2 + Gate 3 stages). Implementation may not start until Gate 2 clears.
- **Transitions**: `todo → in_progress → in_review` (gate) `→ done` (after approval + merge) | any `→ blocked` | `→ cancelled`.

### 4. Gate / approval (execution-policy stage)
- **Backing**: `issue.executionPolicy` (config: ordered `stages[]`, each `{type: review|approval, participants:[{type: user|agent, userId|agentId}]}`) + `issue.executionState` (runtime: `status idle|pending|changes_requested|completed`, `currentStageId`, `currentParticipant`) + `issue_execution_decisions` (audit: `stageId`, `stageType`, `outcome approved|changes_requested`, `body` [required rationale], `actor`, `createdByRunId`).
- **Glass Cockpit use**:
  - **Gate 2 (per-spec design)**: stage `{type:"approval", participants:[{type:"user", userId:<board>}]}`. On engineer "ready for review", issue → `in_review`, blocks; user `approved` → proceed; `changes_requested` (with `body`) → bounce to `returnAssignee`.
  - **Gate 3 (pre-merge)**: terminal `approval` stage on the deliverable issue, paired with GitHub branch protection.
  - **Adversarial review (FR-011)**: a `{type:"review", participants:[{type:"agent", agentId:<Critic>}]}` stage before the user gate; FR-011a waiver = a user `approved` decision substituting for the missing review, recorded in `issue_execution_decisions`.
- **Transitions (executionState)**: `idle → pending` (enter stage) `→ completed` (approved → next stage or done) | `pending → changes_requested` (→ returnAssignee → back to `pending` after revision).

### 5. Reasoning + provenance
- **Backing**: plan `document` body; `issue_comments` (agent rationale, `createdByRunId`); `issue_execution_decisions.body` (required at each decision); run `promptMetrics` + recovery/liveness fields; `activity_log`.
- **Glass Cockpit use**: surfaced at each gate (FR-006). Instruction-level rule (FR-014): every decision-driving claim cites a source (a comment/run/`[[wiki]]`); uncited material must not silently drive decisions.
- **Invariant**: a gate presented to the user MUST have an associated rationale + ≥1 citation reachable from the approval surface.

### 6. Hold (pause / cancel)
- **Backing**: `issue_tree_holds` (`mode pause|resume|cancel|restore`, `status active|released`, `releasePolicy.strategy manual|after_active_runs_finish`). `getActivePauseHoldGate` walks ancestors so a hold on a root freezes the whole subtree.
- **Glass Cockpit use**: FR-012 freeze/cancel any subtree, with a dry-run **preview** of affected issues/agents/runs before applying; resume later.
- **Transitions**: `active → released`; `pause`/`cancel` cancel in-flight runs + unclaimed wakeups; `restore` brings statuses back.

### 7. Audit / wiki entry
- **Backing**: SynapBus channel message (append-only) + wiki article — **best-effort, non-blocking** (CN-003). Authoritative audit remains Paperclip's `activity_log` + `issue_execution_decisions`.
- **Glass Cockpit use**: one-line audit per gate decision + milestone; a wiki article on the cockpit pattern. If SynapBus is down, entries are skipped/deferred; the pipeline proceeds (SC-008).

### 8. Agent roles (instruction bundles)
- **Backing**: Paperclip `agents` rows (`role`, `adapterType`, `reportsTo`, `cwd`, `budgetMonthlyCents`, `status`, `adapterConfig.instructionsFilePath`) + the managed `AGENTS.md`/`GEMINI.md` bundles under `~/.paperclip/.../agents/<id>/instructions/`. Canonical source: `specs/064-glass-cockpit/agent-instructions/`.
- **Roster (live, verified)**: CEO (`ceo`, `claude_local`), BackendEngineer/FrontendEngineer/MacOSEngineer (`engineer`), ReleaseEngineer (`devops`), QATester (`qa`) — all `claude_local`; Critic (`general`, **`gemini_local`**). CTO/PM/CMO paused (left paused).
- **Glass Cockpit use**: rewritten instructions encode the plan-first/gate/no-self-merge/provenance behavior. Activated on-demand per run (D-08).

## Cross-entity invariants (testable)

- **INV-1**: No child issue exists for a goal until its plan-of-attack interaction is `accepted` (Gate 1). *(Probe: post goal; assert child count 0 while interaction `pending`.)*
- **INV-2**: No spec issue is `in_progress` past its design stage until `executionState=completed` for that stage (Gate 2). *(Probe: assert issue stays `in_review` until user approves.)*
- **INV-3**: No issue reaches `done` without (a) a passing QA evidence record and (b) a human merge of its PR (Gate 3). *(Probe: assert issue blocked while PR open/unmerged; no agent merge in git log.)*
- **INV-4**: Every `issue_execution_decision` has a non-empty `body` (rationale). *(Probe: assert decisions carry rationale.)*
- **INV-5**: Pipeline completes even with SynapBus unreachable. *(Probe: block SynapBus; run dry-run; assert completion.)*
- **INV-6**: The waiting-view count == actual pending items (sidebar-badges + blocked-inbox + approvals). *(Probe: compare counts.)*
