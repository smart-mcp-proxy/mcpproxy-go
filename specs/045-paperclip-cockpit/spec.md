# Feature Specification: Paperclip Goal Cockpit for MCPProxy

**Feature Branch**: `045-paperclip-cockpit`
**Created**: 2026-04-25
**Status**: Draft
**Design source of truth**: [`docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md`](../../docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md)
**Input**: Use the locally-running Paperclip AI instance at http://127.0.0.1:3100 (company "MCPProxy") as a strategic cockpit. User sets goals; CEO agent decomposes; expert agents propose; user approves; implementation flows to either a speckit spec (big features) or a direct PR (small work). Direct Claude Code sessions remain preserved for brainstorming and investigation. Synapbus integration is bidirectional — agents read context before deciding, post summaries after, and CEO maintains wiki articles.

> **This spec is intentionally minimal.** The full design (architecture, agent roles, flow diagrams, synapbus integration, wiki ownership, risks) lives in the design doc above. This spec captures only what's testable: user stories, functional requirements, success criteria, and the bootstrap task list.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — User posts a goal and receives a synthesized proposal for approval (Priority: P1)

User writes a high-level goal (e.g., *"Analyze telemetry data on retention and propose what we can improve"*) into a Paperclip CEO ticket. The CEO queries Synapbus and the wiki for prior context, dispatches 1–3 expert agents to propose options, has the Critic review each, then produces a single synthesized recommendation with options A/B/C and tradeoffs. The user reads the synthesis and either approves, rejects, or requests changes via a Paperclip reaction.

**Why this priority**: This is the core value — without it, the cockpit produces nothing. Approval gating without proposals is empty; proposals without approvals is unsafe.

**Independent Test**: Post one goal text to the CEO ticket. Wait for synthesis. Confirm: (a) at least 1 expert proposal cites a Synapbus message or wiki article ID, (b) the synthesis offers at least 2 options with tradeoffs, (c) the user can react `approve` / `reject` / `request_changes`.

**Acceptance Scenarios**:

1. **Given** an empty Paperclip CEO ticket with a goal text, **When** the CEO is prompted, **Then** within a reasonable time it queries Synapbus, dispatches experts, and posts a synthesis comment with options + tradeoffs + a recommendation.
2. **Given** a synthesis comment exists, **When** the user reacts `approve`, **Then** the next workflow stage activates; **When** they react `reject`, **Then** the ticket is archived with no implementation.
3. **Given** a goal too vague to decompose (e.g., one-word prompt), **When** the CEO processes it, **Then** the CEO posts a clarification request comment instead of proposing — and waits.

---

### User Story 2 — Approved goal flows autonomously to a pull request (Priority: P2)

After the user approves a synthesis, the CEO routes to an implementation expert. For "big" goals (≥3 file areas, or data/security/release impact, or user said "spec it"), the expert runs `/speckit.specify` → `/speckit.plan` → `/speckit.tasks` → `/speckit.implement` and the resulting work lands as a PR linked to a `specs/NNN-name/` spec. For small goals, the expert opens a PR directly with no spec. In both cases, the agent does **not** merge its own PR — human review on GitHub is mandatory.

**Why this priority**: Without autonomous implementation, the cockpit is just a proposal generator. P2 because P1 still delivers value (better-thought-out proposals) even without automated implementation.

**Independent Test**: Approve one synthesis tagged "big feature". Observe: (a) `specs/NNN-name/spec.md` lands in git, (b) a PR opens against `main`, (c) the PR is **not auto-merged**.

**Acceptance Scenarios**:

1. **Given** an approved synthesis judged "big" by the CEO routing rule, **When** implementation begins, **Then** a speckit spec dir appears at `specs/NNN-<short-name>/` and a PR opens with the spec dir name in the description.
2. **Given** an approved synthesis judged "small", **When** implementation begins, **Then** a PR opens directly with no speckit spec.
3. **Given** any agent-opened PR, **When** the agent attempts to merge it, **Then** the merge is blocked (no human approval on GitHub).
4. **Given** an agent's per-agent budget cap is reached mid-implementation, **When** the next API call would exceed it, **Then** the agent auto-pauses and the ticket reflects the paused state.

---

### User Story 3 — QA, Synapbus, and wiki close the loop after ship (Priority: P3)

When a PR opens, the QA Tester drafts a test plan, the Critic reviews it, the Tester runs it (via `mcpproxy-ui-test` MCP and the Chrome browser extension), and a rich HTML report is attached to the Paperclip ticket. After the human merges the PR, the CEO updates three Synapbus wiki articles (`mcpproxy-roadmap`, `mcpproxy-architecture-decisions` if a non-default option was chosen, `mcpproxy-shipped`) and posts a summary to `#my-agents-algis`. QA-found regressions become new Paperclip ad-hoc issues.

**Why this priority**: Without P3, you ship without verification and lose institutional memory. But P1+P2 already deliver value — P3 is the long-term-health layer.

**Independent Test**: Merge one PR generated by the cockpit. Within 24 hours, confirm: (a) `mcpproxy-roadmap` wiki article moved this work from "in flight" to "recently shipped", (b) `mcpproxy-shipped` has a new entry with a one-paragraph summary, (c) `#my-agents-algis` Synapbus channel has exactly one post about the ship.

**Acceptance Scenarios**:

1. **Given** a PR was opened by an implementation agent, **When** QA auto-triggers, **Then** an HTML test report is attached to the corresponding Paperclip ticket within a reasonable time.
2. **Given** a PR is merged, **When** the CEO is notified, **Then** the three wiki articles are updated and a summary is posted to `#my-agents-algis` (and only that channel — no spam).
3. **Given** QA finds a regression on the PR, **When** it reports the failure, **Then** a new Paperclip ad-hoc issue is created and routed to the same implementation expert.

---

### Edge Cases

- **Synapbus unreachable**: CEO and experts proceed with proposal generation but explicitly mark the proposal as "no Synapbus context — proceed with caution"; user sees this in the synthesis.
- **Goal text contains secrets**: agents must redact obvious secrets (API keys, passwords) from any wiki articles or synapbus posts derived from the goal.
- **Two concurrent goals routed to the same expert**: expert serializes; the second goal's ticket shows "queued behind goal #N".
- **User edits the goal text after experts have started**: experts complete their current work; CEO surfaces the diff in the synthesis and asks the user to confirm or restart.
- **`requireBoardApprovalForNewAgents` blocks an attempted dynamic agent creation**: no agent is added; ticket comment explains why.
- **Speckit `/speckit.implement` fails partway**: the spec dir + plan + tasks remain in git; ticket reflects partial state; user can resume manually or request retry.
- **Wiki edit conflicts with manual user edit**: CEO refuses to overwrite, posts a diff to the user, and asks for a merge decision.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide three distinct entry points for work: (a) Paperclip CEO ticket for high-level goals, (b) direct Claude Code session for brainstorming and investigation, (c) Paperclip ad-hoc issue for known-scoped tasks. Each entry point MUST function without depending on the others.
- **FR-002**: System MUST require explicit user approval (Paperclip reaction `approve`) on the CEO synthesis before any implementation work begins.
- **FR-003**: CEO agent MUST query Synapbus (search messages, list articles) and read relevant wiki articles BEFORE generating a proposal, and MUST cite all sources used (message IDs or `[[slug]]` cross-links). Silent influence is forbidden.
- **FR-004**: CEO agent MUST classify each goal as "big" or "small" using the routing rule defined in the design doc (≥3 file areas OR data/security/release impact OR user explicitly said "spec it").
- **FR-005**: Implementation agents MUST NOT merge their own pull requests. Merging requires human review on GitHub.
- **FR-006**: System MUST enforce per-agent Paperclip budget caps that auto-pause work on overspend, without losing in-progress state.
- **FR-007**: CEO agent MUST keep three Synapbus wiki articles consistent: `mcpproxy-roadmap` (rewritten in full each goal milestone), `mcpproxy-architecture-decisions` (one entry per non-default routing decision), `mcpproxy-shipped` (append-only).
- **FR-008**: System MUST preserve existing direct Claude Code session workflows. Specifically: `/superpowers:brainstorming` continues to work and outputs to `docs/superpowers/specs/`; `/superpowers:writing-plans` is NOT used (replaced by `/speckit.specify` against the brainstorm output); free-form subagent investigation continues unchanged.
- **FR-009**: Initial agent roster MUST consist of exactly seven roles: CEO, Backend Engineer, Frontend Engineer, macOS Engineer, QA Tester, Critic, Release Engineer. Marketing/PM agents are explicitly out of scope.
- **FR-010**: Initial setup MUST be bootstrap-by-hand. The CEO cannot self-provision; the user (with optional CC assistance) creates the agents, instruction files, wiki articles, and budget caps as a one-time event.
- **FR-011**: Adding any new agent role after initial roster MUST require board approval via Paperclip's `requireBoardApprovalForNewAgents` mechanism.
- **FR-012**: Paperclip MUST remain bound to loopback (`127.0.0.1:3100`) and run in `local_trusted` mode unless production hardening (rotation of `BETTER_AUTH_SECRET` and `PAPERCLIP_AGENT_JWT_SECRET`) is performed first.
- **FR-013**: Each agent's instruction directory MUST live at `~/.paperclip/instances/default/companies/<company-id>/agents/<agent-id>/instructions/`. CEO has four files (`AGENTS.md`, `SOUL.md`, `HEARTBEAT.md`, `TOOLS.md`); other agents have only `AGENTS.md`.
- **FR-014**: System MUST anti-spam Synapbus and the wiki: at most one synapbus channel post per goal milestone (approval, ship, regression) and at most one wiki article update per shipped goal.
- **FR-015**: The Critic agent MUST run via **Gemini CLI** (`gemini-3.1-pro-preview`), not Claude Code. Rationale: model diversity for adversarial review surfaces blind spots a single-model team would miss. Other agents continue to run on Claude Code as their primary CLI.
- **FR-016**: For Synapbus context that exceeds a small threshold (>5 messages from a search OR >10 replies in a thread), agents MUST compress via the **opencode CLI with the `kimi2.5-gcore` model** before reasoning over it. This offloads long-form reading from each agent's primary CLI context window. The compression prompt preserves message IDs as inline citations so the provenance rule (FR-003) still holds.

### Bootstrap Tasks (the actual implementation checklist)

The "code" of this feature is mostly configuration outside the mcpproxy-go repo. The bootstrap deliverables are:

- **T-001**: Create the seven Paperclip agents in the existing "MCPProxy" company (id `16edd8ed-…`) with appropriate `role`, `title`, `reportsTo`, `capabilities`, and budget caps.
- **T-002**: Write per-agent instruction files under `~/.paperclip/instances/default/companies/16edd8ed-…/agents/<agent-id>/instructions/`. CEO gets the full set; others get `AGENTS.md` only. Adapt from Paperclip's `paperclip-create-agent` skill templates.
- **T-003**: Seed three Synapbus wiki articles: `mcpproxy-roadmap` (initial snapshot derived from current `specs/` state — list shipped, in-progress, planned, parked), `mcpproxy-architecture-decisions` (empty stub with format definition), `mcpproxy-shipped` (empty stub).
- **T-004**: Configure per-agent Paperclip budget caps (initial conservative values; user adjusts after first 5 goals).
- **T-005**: (Optional) Add `docs/agent-cockpit.md` to the mcpproxy-go repo as a one-page overview pointing at the design doc, so contributors see the cockpit exists.
- **T-006**: First goal exercise: post a small synthetic goal to the CEO ticket (e.g., "draft a one-paragraph release-notes paragraph for v0.25.0") and verify the full P1 + P2 + P3 flow works end-to-end. Refine instruction files based on what surfaces.

### Key Entities

- **Goal**: high-level user instruction text posted as a Paperclip CEO ticket. Carries scope but not implementation detail.
- **Proposal**: a markdown document attached to a Paperclip ticket via `paperclipUpsertIssueDocument`, produced by one expert agent. Lists options and tradeoffs for one slice of the goal.
- **Synthesis**: the single recommendation produced by the CEO by merging proposals; carries the user-facing approval gate. Hosts the `approve` / `reject` / `request_changes` reactions.
- **Spec**: a speckit feature artifact under `specs/NNN-<short-name>/` (spec.md, plan.md, tasks.md). Created only when the CEO routes a goal as "big". Lives in git.
- **Wiki Article**: Synapbus-hosted markdown maintained by the CEO. Three exist: `mcpproxy-roadmap`, `mcpproxy-architecture-decisions`, `mcpproxy-shipped`.
- **Provenance Citation**: a Synapbus message ID or wiki `[[slug]]` reference attached to a proposal claim. Required by FR-003.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: User can post a goal text and receive a synthesized proposal with options + tradeoffs + recommendation within 30 minutes for typical-scope goals (small/medium feature).
- **SC-002**: After the first five shipped goals, retrospective review shows the spec-vs-no-spec routing decision was correct (in user's judgement) for at least 4 of 5.
- **SC-003**: 100% of expert proposals cite at least one Synapbus message or wiki article (the provenance rule, FR-003).
- **SC-004**: Zero agent-self-merged PRs across the first 10 cockpit-generated PRs (FR-005, verified by GitHub audit log).
- **SC-005**: Initial bootstrap (T-001 through T-005) is completed by the user in under 4 hours of focused work.
- **SC-006**: After the first 5 shipped goals, the `mcpproxy-roadmap` wiki article reflects 100% of those ships, with the timestamp of each ship within 24 hours of PR merge.
- **SC-007**: User's day-to-day Claude Code workflows (brainstorming, investigation, trivial fixes) remain functional and unchanged — verified by the user explicitly testing each on day 1 of cockpit operation.
- **SC-008**: After 30 days of cockpit operation, no Paperclip-runtime secrets (master key, JWT secret) have been exposed beyond loopback (verified via `lsof` on port 3100, or by the absence of LAN-reachability).

## Out of Scope

- Two-way sync between speckit specs (in git) and Paperclip issues — Paperclip references `specs/NNN-name/` by string only.
- `/superpowers:writing-plans` usage — replaced by `/speckit.plan`.
- Paperclip ticket creation for bugs trivial enough to fix in one Claude Code session.
- Marketing or PM agents on day one (deferred until a marketing goal is in flight).
- Code changes to `mcpproxy-go` itself, beyond the optional `docs/agent-cockpit.md` overview.
- Agent-orchestrated brainstorming — brainstorming stays interactive in CC.

## Assumptions

- Paperclip is already running locally and the "MCPProxy" company exists (verified at session start).
- Synapbus is reachable at `http://kubic.home.arpa:30088` (per global CLAUDE.md).
- The user's Anthropic API key is configured in Paperclip and has sufficient budget for the first round of goals.
- Initial conservative budget caps (e.g., $5/agent/day) are acceptable; user will adjust empirically.
- The user accepts that initial bootstrap is manual and the CEO cannot create itself.
- Gemini CLI is installed locally and authenticated via Google OAuth token (`gemini auth`); the `gemini-3.1-pro-preview` model is accessible (FR-015). No `GEMINI_API_KEY` env var is required when OAuth is in use.
- opencode CLI is installed locally with the `kimi2.5-gcore` model configured and reachable (FR-016).
- Paperclip's per-agent CLI configuration supports specifying a non-Claude-Code runtime for the Critic agent. If Paperclip does not expose this, T011 is upgraded to a wrapper-script approach where the Critic's CLI command points at a small shell script that invokes Gemini.

## Dependencies

- **Paperclip AI** running locally (already in place, verified)
- **Synapbus** running on kubic (already in place, verified per global CLAUDE.md)
- **Synapbus MCP server** wired into the user's environment (already in place — `mcp__synapbus__*` tools visible)
- **Paperclip MCP server** (`packages/mcp-server` from paperclipai/paperclip) — needed if agents drive Paperclip from outside; not a blocker for bootstrap if the user configures via the Paperclip web UI directly.
- **Gemini CLI** authenticated via Google OAuth (no `GEMINI_API_KEY` needed) for the Critic agent (FR-015). Model: `gemini-3.1-pro-preview`.
- **opencode CLI** + access to the `kimi2.5-gcore` model for Synapbus context summarization (FR-016).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Special note for this feature

Most "commits" for this feature land in `~/.paperclip/instances/default/companies/<id>/agents/<id>/instructions/` (outside git) or via Synapbus `create_article` / `update_article` (also outside git). The only repo commits expected are:

1. This spec + design doc (committed together to `045-paperclip-cockpit` branch)
2. Optional `docs/agent-cockpit.md` overview

Use the standard project commit conventions for those two.
