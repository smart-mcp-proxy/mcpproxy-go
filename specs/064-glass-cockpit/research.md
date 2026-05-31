# Phase 0 Research: Glass Cockpit

All findings below were established by reading the **installed** Paperclip package (`paperclipai@2026.529.0`, unminified `dist/*.js` + `.d.ts`) and confirmed by **read-only** REST calls against the live instance (`127.0.0.1:3100`, company `16edd8ed-8691-4a89-aa30-74ab6b931663`). Mutating flows were **not** exercised yet — derisking them is the first implementation task (see D-09).

## Decision summary

| # | Decision | Rationale | Alternatives rejected |
|---|---|---|---|
| D-01 | **Reuse the running "MCPProxy" company**; do not fork or stand up a new instance for Phases A/B | User directive; the spec-045 roster (CEO/engineers/QA/Gemini Critic) already exists and matches the design | New dev instance (062 approach) — extra setup, port/DB collisions, no benefit; fork — premature (Phase C only) |
| D-02 | **Per-spec design gate + pre-merge gate = native `executionPolicy` `approval` stages** with `participant = {type:"user"}` | Runtime-enforced: the issue parks in `in_review` and only the participant can advance it. Not prompt-dependent | Convention-only "ask before coding" — bypassable; board `approvals` table — coarser, not per-issue |
| D-03 | **Plan-of-attack gate = `request_confirmation` (or `suggest_tasks`) interaction** bound to the plan-doc revision, blocking the CEO before `accepted-plan-decompositions` | `suggest_tasks` renders a natively **selectable/editable** task tree (structured redirection for free); `supersedeOnUserComment` gives reject-with-reason | A chat-parsed `DROP 4`/`SPLIT 3` command grammar (review suggestion) — brittle NLP, unnecessary given the native editable tree |
| D-04 | **"Waiting on you" = native surfaces in Phase A, fused panel in Phase B** | Live API already returns `sidebar-badges` (`{inbox,approvals,failedRuns,joinRequests}`), a `attention=blocked&includeBlockedInboxAttention=true` issue filter, and an approvals page. Phase B fuses them via the plugin platform | A CEO-maintained wiki to-do page (review suggestion) — strictly worse than native (stale, hand-maintained) |
| D-05 | **Pre-merge gate = GitHub branch protection + never-self-merge instruction**, agents open PRs only | The one hard, external, non-bypassable gate | Paperclip-internal merge approval — agents have shell + git; only the git host can truly fence merge |
| D-06 | **SynapBus = append-only audit log + wiki, best-effort, never blocking** | It is beta (user directive); Paperclip already stores the authoritative audit (`activity_log`, `issue_execution_decisions`) | SynapBus as task store / coordination backbone — beta risk on the critical path |
| D-07 | **Conservative per-agent budgets; manual cost watch** | The package tracks `spentMonthlyCents=0` (no real cost accounting) — budgets are the only guard | Relying on platform cost enforcement — not implemented in this build |
| D-08 | **Revival is non-destructive**: un-pause only the agents a run needs; new work under a fresh goal; leave the 49 historical issues untouched | CN-002; avoids disturbing prior state | Wiping/rebuilding the company — destructive, loses history, unnecessary |
| D-09 | **First implementation task is a primitive spike** on a throwaway issue: exercise each mutate flow (attach policy → block → approve; raise confirmation → block → accept/edit; tree-hold pause/preview/release) and capture exact request/response | Mutation flows are documented from package source but unproven live; derisk before wiring the real pipeline (TDD: probes first) | Assume the flows work and build the pipeline — risks late discovery of shape mismatches |

## Verified primitive → requirement mapping

| Requirement | Paperclip primitive (verified) | Endpoint(s) |
|---|---|---|
| FR-002 plan-of-attack blocks before tasks exist | `issue_thread_interactions` kind `request_confirmation`/`suggest_tasks`; status `pending` until acted; decomposition only via accepted plan | `POST /api/issues/:id/interactions`, `.../interactions/:iid/accept|reject|respond`; `POST /api/issues/:id/accepted-plan-decompositions` |
| FR-003 structured redirection | `suggest_tasks` editable selectable tree; `request_confirmation` `rejectRequiresReason`; supersede-on-comment | same as above |
| FR-004 per-spec design gate blocks; request-changes returns to engineer | `executionPolicy.stages=[{type:"approval",participants:[{type:"user"}]}]`; issue → `in_review`; decision `changes_requested` bounces to `returnAssignee` | `POST/PATCH /api/companies/:id/issues`, `PATCH /api/issues/:id`; decisions → `issue_execution_decisions` |
| FR-005 no agent self-merge | (external) GitHub branch protection | `gh api ...branches/main/protection` |
| FR-006 reasoning + ≥1 citation at the gate | plan document + `document_revisions`; run `promptMetrics`; recovery/liveness provenance; required decision `body` | `GET /api/issues/:id` (`planDocument`), run transcripts |
| FR-007 consolidated waiting view | `sidebar-badges`; blocked-inbox attention filter; approvals | `GET /api/companies/:id/sidebar-badges`, `GET /api/companies/:id/issues?attention=blocked&includeBlockedInboxAttention=true`, `GET /api/companies/:id/approvals` |
| FR-011/011a adversarial review (diff model) + waiver | Critic agent on `gemini_local`; review as an `executionPolicy` `review` stage or a comment-gated step; waiver = user approval decision recorded | issue execution stages + `issue_execution_decisions` |
| FR-012 pause/cancel + preview + resume | `issue_tree_holds` modes `pause|resume|cancel|restore`, `releasePolicy manual|after_active_runs_finish`; dry-run preview | `POST /api/issues/:id/tree-holds`, `GET /api/issues/:id/tree-control/preview|state` |
| FR-013 audit | SynapBus (log-only) + native `activity_log` | SynapBus MCP; `GET /api/companies/:id/activity` |
| FR-014 provenance | instruction-enforced citation rule + plan-doc/comments | agent instructions; comments |

**Issue status enum (verbatim)**: `backlog, todo, in_progress, in_review, done, blocked, cancelled`.
**Execution stage types**: `review, approval`. **Execution state status**: `idle, pending, changes_requested, completed`. **Decision outcomes**: `approved, changes_requested`. **Interaction kinds**: `request_confirmation, suggest_tasks, ask_user_questions`. **Tree-hold modes**: `pause, resume, cancel, restore`.

## Open risks / decisions needing confirmation during implementation

- **R-01 (pre-merge enforcement vs shared credentials)** — *Important.* If agents use the user's `gh`/git credentials, branch protection cannot distinguish an agent merge from a human merge, so FR-005 degrades to convention. **Recommended**: give the fleet a **scoped GitHub token without merge permission** (fine-grained PAT or bot account excluded from the merge allowlist) so branch protection *hard*-enforces the gate; fall back to convention + required-review + required-CI for the dry-run if a separate identity isn't ready. Resolve in `tasks.md`.
- **R-02 (plan-of-attack convention gap)** — In Phase A the CEO *chooses* to raise the confirmation; nothing in core forces it. Contained by the per-spec design gate (enforced) + Critic. Phase C closes it. Accepted for the dry-run.
- **R-03 (heartbeats disabled)** — All agents have `heartbeat.enabled=false`; officers have `wakeOnDemand=true,intervalSec=300`. The pipeline must advance on **event/on-demand wakes** (assignment, interaction-resolved, approval-approved), not a timer. Confirm wake-on-demand fires after each gate during the D-09 spike.
- **R-04 (plugin cannot block core transitions in-band)** — Phase B's plugin observes via WebSocket and pre-attaches policies / reacts with tree-holds; it cannot synchronously veto a transition. Acceptable: gates are enforced by execution-policy (D-02), not by the plugin. The plugin is transparency + convenience.
- **R-05 (model-diversity reviewer availability)** — Handled by FR-011a user waiver. Confirm the Critic's `gemini_local` adapter has working credentials during the spike; if not, the waiver path is the dry-run fallback.
- **R-06 (instruction drift)** — Paperclip manages instruction bundles in `~/.paperclip/...`; our canonical copies live in the spec. `apply-instructions.sh` must be idempotent and re-runnable so the running copy never silently diverges from version control.

## Out-of-scope confirmations

- No GraphQL exists (`POST /graphql` → "Cannot POST"); REST + WebSocket only.
- No mcpproxy-go binary/source changes in Phases A/B.
- One goal at a time for v1 (concurrent goals deferred).
