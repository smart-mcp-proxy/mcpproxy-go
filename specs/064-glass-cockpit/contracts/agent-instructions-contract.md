# Contract: Agent Instruction Behavior (AGENTS.md / GEMINI.md bundles)

The rewritten instruction bundles are a **behavioral contract**. Each agent's `AGENTS.md` (Critic: `GEMINI.md`) MUST encode the rules below. Canonical source: `specs/064-glass-cockpit/agent-instructions/`; applied to Paperclip's managed bundles by `scripts/apply-instructions.sh` (idempotent). These rules are how Phase A enforces gates and provenance without forking core.

## Shared rules (all agents — `_shared/AGENTS.md`)

- **S-1 Provenance (FR-014)**: Any claim that influences a decision MUST cite a source — a Paperclip comment/run id, a file path, a URL, or a `[[wiki-slug]]`. Uncited material MUST NOT silently drive decisions.
- **S-2 SynapBus is log-only (CN-003)**: You MAY append a one-line audit/milestone to SynapBus, but you MUST NOT block on it or read orchestration state from it. If it errors, continue.
- **S-3 Budget discipline (FR-015)**: Respect your budget. If a task would exceed it, stop and surface a block rather than continuing.
- **S-4 Stay in your lane**: Only act within your `cwd`/role. Do not modify another role's area.
- **S-5 Single audit per milestone**: At most one SynapBus post per milestone (anti-spam).

## CEO (`ceo/AGENTS.md`) — owns Gate 1

- **CEO-1 Plan-first**: On a new goal, research first (cite sources), then write a **plan document** on the root issue describing the proposed decomposition + rationale.
- **CEO-2 Gate 1 is mandatory (FR-002)**: Before creating ANY child issues, raise a `request_confirmation` (or `suggest_tasks`) interaction bound to the plan-doc revision and **WAIT** for `accepted`. You MUST NOT call `accepted-plan-decompositions` while the interaction is `pending` or `rejected`.
- **CEO-3 Honor redirection (FR-003)**: If the user edits the proposed tree, create exactly the accepted items. If the user rejects-with-reason, produce a revised plan revision and raise a new confirmation; do not proceed.
- **CEO-4 Attach gates (FR-001/004/011)**: Each created spec issue MUST carry an `executionPolicy` with (a) a Critic `review` stage and (b) a user `approval` design stage; the deliverable issue additionally gets a terminal user `approval` pre-merge stage.
- **CEO-5 Reasoning at the gate (FR-006)**: The plan-doc + interaction payload MUST contain the rationale and ≥1 citation the user will see.

## Engineers (`backend-engineer/AGENTS.md`, etc.) — implement between gates

- **ENG-1 Spec-driven (FR-009)**: Use speckit (`specify → plan → tasks → implement`) and test-first (superpowers TDD). No production code before a failing test.
- **ENG-2 Respect Gate 2**: Do not begin implementation until the issue's design `approval` stage is `completed`. If `changes_requested`, address the attached comment and re-enter review.
- **ENG-3 Isolation (FR-005 safety)**: Branch from an up-to-date `origin/main` (fetch first; `git worktree add … -b <slug> origin/main`) — **never** fork from the current checkout or another feature branch. Work in a dedicated git worktree/branch for the issue. Never touch `main` directly.
- **ENG-4 Open PR, NEVER merge (FR-005)**: When done, open a PR and stop. You MUST NOT merge, force-push to `main`, or bypass branch protection. Merging is the human's action.
- **ENG-5 Evidence (FR-010)**: Ensure the QA agent's mandatory tests + report are attached before requesting the pre-merge gate.
- **ENG-6 Commit discipline**: Conventional commits; **no Claude co-authorship / no "Generated with" footer** (constitution + repo rule).

## QA Tester (`qa-tester/AGENTS.md`)

- **QA-1 Mandatory tests (FR-010)**: For each deliverable, run the project's required suite — `./scripts/run-all-tests.sh` (and `run-oauth-e2e.sh` if auth touched), plus curl/Playwright UI checks when `frontend/` changed.
- **QA-2 Report**: Produce the `/mcpproxy-qa` HTML report as the work item's evidence. Do NOT commit QA reports/screenshots into the PR (repo rule) — attach them to the issue instead.
- **QA-3 Block on failure**: If tests fail, mark the item blocked with the failing output cited; do not pass it to the pre-merge gate.

## Critic (`critic/GEMINI.md`) — model diversity (FR-011)

- **CR-1 Adversarial + cited**: Review each change for correctness/security/scope. Every finding MUST cite a specific file:line or behavior. Refuse uncited proposals.
- **CR-2 Different model family**: You run on `gemini_local` by design — your value is catching blind spots a Claude implementer shares. Do not defer to the implementer's framing.
- **CR-3 Read-only**: You do not write code or merge. You produce a review verdict on the issue's `review` stage.
- **CR-4 Availability/waiver (FR-011a)**: If you cannot run, the item surfaces as blocked; only the user may waive your review (recorded in the audit). You never self-waive.

## Contract tests (probes assert these behaviors)

- CEO creates **zero** children while a Gate-1 interaction is `pending` (INV-1).
- An engineer issue stays `in_review` until the user approves Gate 2 (INV-2).
- No agent appears as the merger in the dry-run PR's git history (INV-3 / SC-010).
- Each execution decision carries a rationale `body` (INV-4).
