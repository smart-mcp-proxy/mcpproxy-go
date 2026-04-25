---
description: "Task list for Paperclip Goal Cockpit bootstrap"
---

# Tasks: Paperclip Goal Cockpit for MCPProxy

**Input**: Design documents from `/specs/045-paperclip-cockpit/`
**Prerequisites**: plan.md (loaded), spec.md (loaded), research.md (8 decisions), data-model.md (13 entities), contracts/external-tools.md (Paperclip + Synapbus + MCPProxy MCP surfaces), quickstart.md (6-step bootstrap)

**Tests**: Test tasks are NOT included in the traditional sense — this is a configuration-only feature with no Go/Vue/Swift code. The "tests" are end-to-end smoke tests that verify each user story's flow through real Paperclip + Synapbus integration. They appear as the **last task in each user story phase** rather than first.

**Organization**: Tasks are grouped by user story. Each user story is independently testable and delivers value on its own.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different agents / different files / no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to ([US1], [US2], [US3])
- File paths use this convention:
  - **Paperclip Web UI** action → cite endpoint, e.g. `http://127.0.0.1:3100/api/companies/<id>/agents` (POST)
  - **Filesystem** → absolute path under `~/.paperclip/instances/default/companies/<COMPANY_ID>/agents/<AGENT_ID>/instructions/`
  - **Synapbus** action → cite the MCP tool, e.g. `mcp__synapbus__execute action=create_article slug=mcpproxy-roadmap`
  - **mcpproxy-go repo** → standard relative path

In tasks below, `<COMPANY_ID>` = the existing "MCPProxy" company id `16edd8ed-…` (already verified at probe time).

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify all external prerequisites are reachable and capture stable identifiers needed by every later phase.

- [X] T001 Verify Paperclip is running and reachable: `curl http://127.0.0.1:3100/api/health` returns `{deploymentMode:"local_trusted", authReady:true, bootstrapStatus:"ready"}`. Capture the version string for the bootstrap log. **DONE 2026-04-25** — version `2026.416.0`, all flags green.
- [X] T002 Verify Synapbus reachable: call `mcp__synapbus__my_status`. Confirm a non-error response. Note any pending DMs with priority ≥ 7 and triage them before proceeding. **DONE 2026-04-25** — agent `claude-home` registered. 816 pending DMs (mostly stale agent acks from Mar/Apr — none priority-≥7 require action for this bootstrap).
- [X] T003 Capture COMPANY_ID for the existing "MCPProxy" company: `curl http://127.0.0.1:3100/api/companies | jq '.[] | select(.name=="MCPProxy") | .id'`. Record into a shell variable / scratch note for use in T009..T039. **DONE 2026-04-25** — `COMPANY_ID=16edd8ed-8691-4a89-aa30-74ab6b931663`.
- [X] T004 Confirm Anthropic API key configured in Paperclip with at least $30 of budget headroom for first day of operation. Verify via Paperclip Web UI → Settings → API Keys (path varies; if missing, fail-fast and ask the user before continuing). **DONE 2026-04-25** — user is on Claude subscription (per-token cost not the constraint); per-agent monthly USD caps set in T012/T024/T033 act as the runaway-safeguard.
- [X] T005 [P] Confirm `mcp__mcpproxy-ui-test__*` tools are reachable from a sample Claude Code session (smoke check; QA Tester depends on this in Phase 5). **DONE 2026-04-25** — tools loaded as deferred MCP tools.
- [X] T005a [P] Verify Gemini CLI installed and authenticated. `gemini --version` succeeds; auth is via Google OAuth token (run `gemini auth` once if not already). The Critic agent runs as Gemini CLI, not Claude Code (per FR-015 + research.md R-9). **DONE 2026-04-25** — CLI installed (v0.38.1); user confirmed OAuth auth is configured (no `GEMINI_API_KEY` env var needed).
- [X] T005b [P] Verify opencode CLI installed + `kimi2.5-gcore` model reachable: `opencode run --model kimi2.5-gcore "echo OK"` succeeds. Used by all agents for Synapbus context summarization (per FR-016 + research.md R-10). **DONE 2026-04-25** — CLI installed (v1.14.25); user confirmed kimi2.5-gcore is free for them, so reachability is not a token-cost concern.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Lock the company-level + repo-level invariants that every user story relies on. Must complete before any agent is created.

**⚠️ CRITICAL**: No agent creation in Phase 3+ until this phase is complete.

- [X] T006 Confirm `requireBoardApprovalForNewAgents` is enabled on the MCPProxy company in Paperclip Web UI → Company Settings (FR-011). If not, enable it. The probe earlier confirmed this is the existing default. **DONE 2026-04-25** — confirmed `requireBoardApprovalForNewAgents: true`, `feedbackDataSharingEnabled: false`.
- [X] T007 Confirm Paperclip remains bound to loopback only: `lsof -nP -i :3100 | grep -v 127.0.0.1` should return nothing (FR-012, SC-008). If LAN-bound, halt and rotate `BETTER_AUTH_SECRET` + `PAPERCLIP_AGENT_JWT_SECRET` per the design-doc Risks section before proceeding. **DONE 2026-04-25** — only loopback listeners present.
- [X] T008 Enable GitHub branch protection on `main` in `smart-mcp-proxy/mcpproxy-go` repo: required reviews from at least one human approver, no auto-merge by bots (FR-005). Verify via `gh api repos/smart-mcp-proxy/mcpproxy-go/branches/main/protection`. **DONE 2026-04-25** — re-verified via `gh api`: required_approving_review_count=1, enforce_admins=false (admins bypass-able). Adequate for FR-005 human-merge gate.

**Checkpoint**: Foundation ready — agent creation in Phase 3 may begin.

---

## Phase 3: User Story 1 — User posts goal and gets synthesis (Priority: P1) 🎯 MVP

**Goal**: User posts a high-level goal to a Paperclip CEO ticket; CEO + at least one expert + Critic produce a synthesized proposal for user approval. This is the MVP.

**Independent Test**: Post one synthetic goal text to the CEO ticket. Confirm: (a) ≥1 expert proposal cites a Synapbus message ID or wiki `[[slug]]`, (b) the synthesis offers ≥2 options with tradeoffs + a recommendation, (c) the user can react `approve` / `reject` / `request_changes` (per spec User Story 1 acceptance scenario).

### Agent creation (Phase 3 minimum: CEO + Backend Engineer + Critic)

- [X] T009 [P] [US1] Create CEO agent in Paperclip via Web UI or `POST http://127.0.0.1:3100/api/companies/<COMPANY_ID>/agents` with role `ceo`, title "Chief Executive Agent", reportsTo null, status `paused`. Record the returned agent UUID as `CEO_ID`.
- [X] T010 [P] [US1] Create Backend Engineer agent. role `engineer-backend`, title "Backend Engineer (Go)", reportsTo `CEO_ID`, status `paused`. Record UUID as `BACKEND_ID`.
- [X] T011 [P] [US1] Create Critic agent. role `critic`, title "Adversarial Reviewer", reportsTo `CEO_ID`, status `paused`. **CLI runtime: Gemini** — configure Paperclip's per-agent CLI to spawn `gemini --yolo --model gemini-3.1-pro-preview` instead of Claude Code (per FR-015, research.md R-9). Record UUID as `CRITIC_ID`.

### Budget caps for the MVP three

- [X] T012 [US1] Set per-agent budget caps in Paperclip Web UI → Agent Settings: CEO=$5/day, Backend=$3/day, Critic=$2/day (per research.md R-3). Verify caps are enforced (auto-pause on overspend per FR-006).

### CEO agent instructions (4 files — sequential because all in CEO_ID dir)

- [X] T013 [US1] Write CEO `AGENTS.md` at `~/.paperclip/instances/default/companies/<COMPANY_ID>/agents/<CEO_ID>/instructions/AGENTS.md`. Sections: Role / Mandate (decompose, dispatch, synthesize, route spec-vs-no-spec, maintain wiki) / Inputs (Synapbus channels: news-mcpproxy, open-brain, my-agents-algis; wiki articles: mcpproxy-roadmap, mcpproxy-architecture-decisions, mcpproxy-shipped) / Outputs (synthesis comments, wiki updates) / Tools allowlist (per `contracts/external-tools.md` Paperclip read+write tables, Synapbus read+write tables) / Provenance rule.
- [X] T014 [US1] Write CEO `SOUL.md` at the same dir. Content: decision-making voice (concise, evidence-cited, asks for clarification rather than guessing on ambiguous goals) + provenance rule (every claim cites Synapbus message ID or wiki [[slug]]) + spec-vs-no-spec decision tree from research.md R-8 with worked examples (spec 042 = BIG; PR #407 = SMALL). Include explicit refusal example: "If the proposal contains no citation, return it to the author with a request to add one."
- [X] T015 [US1] Write CEO `TOOLS.md` at the same dir. Explicit allowlist enumerated tool by tool from `contracts/external-tools.md`: Paperclip MCP (paperclipListIssues, paperclipGetIssue, paperclipListIssueComments, paperclipListIssueDocuments, paperclipGetDocument, paperclipListAgents, paperclipUpsertIssueDocument, paperclipAddComment, paperclipUpdateIssue), Synapbus MCP (search, my_status, get_replies, send_message, execute action=list_articles/read_article/update_article), MCPProxy MCP (read-only: upstream_servers, retrieve_tools, quarantine_security, read_cache). Plus a denylist (no paperclipDeleteIssue, no paperclipApiRequest, no Synapbus delete_article, no DMs to other users). **Plus a Synapbus-summarization procedure (FR-016, R-10)**: when `mcp__synapbus__search` returns >5 messages OR a thread has >10 replies, pipe the raw search JSON to `opencode run --model kimi2.5-gcore` with the prompt template `"You are a context summarizer for an AI agent working on goal '<goal>'. Compress the following Synapbus messages into ≤300 words preserving message IDs as inline citations: <raw>"`. The CEO must teach this procedure to all expert agents in their `AGENTS.md` so they don't burn their primary CLI's context on long-form reading.

### Expert + Critic instructions (parallel)

- [X] T016 [P] [US1] Write Backend Engineer `AGENTS.md` at `~/.paperclip/instances/default/companies/<COMPANY_ID>/agents/<BACKEND_ID>/instructions/AGENTS.md`. Sections: Role / Mandate (Go work in `internal/`, `cmd/`; produces proposals for goals that touch backend) / Inputs (open-brain channel, mcpproxy-architecture-decisions wiki, current goal ticket) / Outputs (proposal documents via paperclipUpsertIssueDocument with provenance citations) / Tools allowlist (subset of contracts/external-tools.md: paperclipUpsertIssueDocument, paperclipAddComment, mcp__synapbus__search, mcp__mcpproxy__upstream_servers, mcp__mcpproxy__retrieve_tools).
- [X] T017 [P] [US1] Write Critic instruction files at `~/.paperclip/instances/default/companies/<COMPANY_ID>/agents/<CRITIC_ID>/instructions/`. **Two files (Gemini CLI reads both)**: `GEMINI.md` (primary — Gemini's native instruction filename) and `AGENTS.md` (Paperclip-adapter compatibility, identical content). Sections in each: Role / Mandate (adversarial review of proposals + test plans + PRs; refuses to mark "reviewed" if proposal lacks provenance citation) / Inputs (proposal documents, prior architecture-decisions wiki) / Outputs (paperclipAddComment with `request_changes` reaction or thumbs-up). Read-only tool kit via Gemini's MCP support (paperclipGetDocument, paperclipListIssueDocuments, mcp__synapbus__search, mcp__synapbus__get_replies). Note: Gemini's MCP tool surface differs from Claude's — verify each tool registers via `gemini mcp list` after configuring the agent. Critic also uses opencode/kimi2.5 for Synapbus summarization (same procedure as in CEO TOOLS.md).

### Wiki seed (required for CEO inputs)

- [X] T018 [US1] Seed `mcpproxy-roadmap` wiki article via `mcp__synapbus__execute action=create_article slug=mcpproxy-roadmap`. Content per research.md R-4: four sections (In flight / Recently shipped 30d / Planned / Parked) seeded from current `specs/` inventory. Cross-link as `[[spec-NNN-name]]` for each entry.

### Activation + smoke test for US1

- [X] T019 [US1] Unpause CEO, Backend Engineer, and Critic agents in Paperclip Web UI (depends on T013, T014, T015, T016, T017, T018 — agents need their instructions present before going live).
- [X] T020 [US1] **Smoke test US1** at `http://127.0.0.1:3100/api/companies/<COMPANY_ID>/issues` (POST): create a Paperclip ticket "Synthesis smoke — describe one improvement to the diagnostics taxonomy spec 044" and tag CEO. Verify within 30 minutes (SC-001): (a) CEO posts a synthesis comment with ≥2 options + recommendation, (b) at least one Backend proposal cites a Synapbus message ID or `[[slug]]`, (c) Critic posted a comment **and the Paperclip activity log / agent metadata shows the Critic ran via Gemini CLI** (per FR-015), (d) you can react `approve` to the synthesis. If the test fails, edit the offending agent's instruction file and re-run before proceeding to Phase 4.

**Checkpoint**: User Story 1 fully functional. MVP is shippable: user can submit goals and get syntheses for approval.

---

## Phase 4: User Story 2 — Approved goal flows autonomously to a PR (Priority: P2)

**Goal**: After user approves a synthesis, an implementation expert opens a PR (with a speckit spec for big features, direct PR for small). No agent self-merges.

**Independent Test**: Approve one "big" synthesis → observe `specs/NNN-<short>/` + PR landing; approve one "small" synthesis → observe direct PR with no spec. Verify no agent merges its own PR (SC-004).

### Add the remaining engineering agents

- [X] T021 [P] [US2] Create Frontend Engineer agent in Paperclip. role `engineer-frontend`, title "Frontend Engineer (Vue)", reportsTo `CEO_ID`, status `paused`. Record UUID as `FRONTEND_ID`.
- [X] T022 [P] [US2] Create macOS Engineer agent. role `engineer-macos`, title "macOS Engineer (Swift)", reportsTo `CEO_ID`, status `paused`. Record UUID as `MACOS_ID`.
- [X] T023 [P] [US2] Create Release Engineer agent. role `engineer-release`, title "Release Engineer", reportsTo `CEO_ID`, status `paused`. Record UUID as `RELEASE_ID`.

### Budget caps

- [X] T024 [US2] Set budget caps: Frontend=$3, macOS=$3, Release=$3 per day (research.md R-3).

### Implementation expert instructions (parallel)

- [X] T025 [P] [US2] Write Frontend Engineer `AGENTS.md` at `~/.paperclip/.../<FRONTEND_ID>/instructions/AGENTS.md`. Mandate: Vue/TS in `frontend/src/`. Tools: paperclipUpsertIssueDocument, paperclipAddComment, mcp__synapbus__search, GitHub PR via subprocess. **Speckit invocation rule**: if CEO routes a goal as "big", run `/speckit.specify` → `/speckit.plan` → `/speckit.tasks` → `/speckit.implement` from inside the Claude Code subprocess Paperclip spawns in the mcpproxy-go working dir (research.md R-7).
- [X] T026 [P] [US2] Write macOS Engineer `AGENTS.md` at `~/.paperclip/.../<MACOS_ID>/instructions/AGENTS.md`. Mandate: Swift in `native/macos/`. Tools include `mcp__mcpproxy-ui-test__*` for visual verification (per CLAUDE.md macOS testing flow). Same speckit invocation rule as T025.
- [X] T027 [P] [US2] Write Release Engineer `AGENTS.md` at `~/.paperclip/.../<RELEASE_ID>/instructions/AGENTS.md`. Mandate: nfpm packaging, CI, R2 distribution, prerelease cuts. Tools include knowledge of `scripts/build.sh`, `Makefile` targets `build-server` / `build-docker`. Speckit invocation rule for release-affecting work.
- [X] T028 [P] [US2] **Update** existing Backend Engineer `AGENTS.md` (created in T016) to add the speckit invocation rule (T016 was minimal for US1 and didn't need it). Append a "Speckit invocation" section identical to T025's.

### Activation + smoke test for US2

- [X] T029 [US2] Unpause Frontend, macOS, Release Engineer agents (depends on T024..T028).
- [ ] T030 [US2] **Smoke test US2 — small route**: post a Paperclip ticket "Fix one typo in `README.md` line 12 (replace 'mcp proxy' with 'MCPProxy')". Approve the synthesis. Verify Frontend Engineer (or whichever expert CEO picks) opens a PR with no `specs/NNN-...` spec, against `main`, and CANNOT merge it (SC-004 — branch protection from T008 blocks).
- [ ] T031 [US2] **Smoke test US2 — big route**: post a Paperclip ticket "Add a new REST endpoint `/api/v1/cockpit/health` that returns Paperclip cockpit status (would touch internal/httpapi/, contracts/, oas/, frontend/)". Approve the synthesis. Verify CEO classifies as BIG, Backend Engineer creates `specs/NNN-cockpit-health/` via `/speckit.specify`, opens a PR with the spec dir name in the description.

**Checkpoint**: User Stories 1 AND 2 both work. Cockpit can take a goal from idea to PR autonomously, gated by user approval and human PR merge.

---

## Phase 5: User Story 3 — QA + Synapbus + Wiki close the loop (Priority: P3)

**Goal**: PR auto-triggers QA → HTML test report attached. After human PR merge, CEO updates wiki articles + posts to Synapbus `#my-agents-algis`. Regressions become new ad-hoc Paperclip issues.

**Independent Test**: Merge one cockpit-generated PR. Within 24 hours (SC-006): `mcpproxy-roadmap` moves entry to "Recently shipped"; `mcpproxy-shipped` has a new entry; `#my-agents-algis` channel has exactly one summary post; if synthesis chose a non-default option, `mcpproxy-architecture-decisions` has a new entry.

### QA Tester agent

- [X] T032 [P] [US3] Create QA Tester agent in Paperclip. role `qa-tester`, title "QA Tester", reportsTo `CEO_ID`, status `paused`. Record UUID as `QA_ID`.
- [X] T033 [US3] Set budget cap: QA=$4/day (highest non-CEO because QA is read-heavy + report generation).
- [X] T034 [P] [US3] Write QA Tester `AGENTS.md` at `~/.paperclip/.../<QA_ID>/instructions/AGENTS.md`. Mandate: drafts test plans, gets Critic review, runs tests using `mcp__mcpproxy-ui-test__*` (macOS UI verification) + Chrome browser extension (web UI), generates rich HTML report attached to ticket via `paperclipUpsertIssueDocument`. **Auto-trigger**: when a PR is opened by an implementation expert in this company, QA picks it up — encode this as a heartbeat-pattern check in the AGENTS.md (sweep open Paperclip tickets for "PR opened" state). Inputs: `#bugs-mcpproxy` for prior failure patterns; `mcpproxy-shipped` for recent change context.

### Wiki articles for the loop-closing layer (parallel — different slugs)

- [X] T035 [P] [US3] Seed `mcpproxy-architecture-decisions` wiki article via `mcp__synapbus__execute action=create_article slug=mcpproxy-architecture-decisions`. Content: empty stub with format definition (the entry template from data-model.md — `## YYYY-MM-DD — title`, **Goal:**, **Options considered:**, **Decision:**, **Rationale:**, **Sources:**).
- [X] T036 [P] [US3] Seed `mcpproxy-shipped` wiki article via `mcp__synapbus__execute action=create_article slug=mcpproxy-shipped`. Content: empty stub with format definition (`## YYYY-MM-DD — PR title`, **PR:**, **Spec:**, **Summary:**).

### CEO heartbeat (drives the loop maintenance)

- [X] T037 [US3] Write CEO `HEARTBEAT.md` at `~/.paperclip/.../<CEO_ID>/instructions/HEARTBEAT.md`. Cadence: every 6 hours (research.md R-2). Sweep actions: (1) stale-goal sweep — for each Paperclip ticket in state "Implementing" with no progress in >24h, post a ping comment to the assigned expert; (2) roadmap freshness — if `mcpproxy-roadmap` was last edited >7 days ago and goals exist, regenerate via `mcp__synapbus__execute action=update_article slug=mcpproxy-roadmap`; (3) budget burn — if any agent is >75% of daily cap, post low-priority `#my-agents-algis` notice. Plus on-PR-merge hook: when a PR linked to a Paperclip ticket merges, append entry to `mcpproxy-shipped`, update `mcpproxy-roadmap` (move from "In flight" to "Recently shipped"), and (if synthesis chose non-default option) append to `mcpproxy-architecture-decisions`.

### Activation + smoke test for US3

- [X] T038 [US3] Unpause QA Tester (depends on T034).
- [ ] T039 [US3] **Smoke test US3**: take the small-route PR from T030 and merge it manually. Within 24h verify: (a) QA HTML report attached to the original ticket (or skipped with explanation if doc-only PR — fine), (b) `mcpproxy-shipped` has a new entry naming the PR, (c) `#my-agents-algis` Synapbus channel has exactly one summary post (anti-spam from FR-014), (d) `mcpproxy-roadmap` reflects the merged work, (e) if a non-default option was chosen at synthesis time, `mcpproxy-architecture-decisions` has the entry.

**Checkpoint**: All three user stories work end-to-end. The cockpit is operational.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Repo-side housekeeping + the spec's stated optional deliverables + final validation against success criteria.

- [X] T040 [P] Add `docs/agent-cockpit.md` overview (T-005 from spec). Content: one-page pointer (≤30 lines) explaining the cockpit exists, that it is configured outside the repo, with links to `specs/045-paperclip-cockpit/` and `docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md`. Use the template in quickstart.md Step 5.
- [X] T041 [P] Commit the design doc and spec dir on branch `045-paperclip-cockpit`: `git add docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md specs/045-paperclip-cockpit/ docs/agent-cockpit.md && git commit -m "feat(cockpit): paperclip goal cockpit spec + design (Related #...)"`. Push the branch and open PR (no auto-merge — required reviews from human per FR-005).
- [ ] T042 **End-to-end smoke test (T-006 from spec)**: Post the synthetic goal "Draft a one-paragraph release-notes paragraph for v0.25.0" to the CEO ticket. Walk the full P1 + P2 + P3 flow (synthesis → approval → small-route PR → merge → wiki update + synapbus post). Capture timing for SC-001.
- [ ] T043 [P] Verify success criteria against the smoke-test outcomes: SC-001 (synthesis <30min), SC-003 (provenance citation present), SC-004 (no agent self-merge), SC-005 (bootstrap <4h total — measure since T001), SC-006 (wiki updated within 24h post-merge), SC-007 (your normal CC sessions still work — verify by running `/superpowers:brainstorming` on a throwaway topic in a separate CC session), SC-008 (`lsof -nP -i :3100 | grep -v 127.0.0.1` returns nothing).
- [ ] T044 [P] Refine instruction files based on what surfaced during T020/T030/T031/T039/T042. Common fixes documented in quickstart.md "Common Bootstrap Issues" table — apply as needed and commit small revision bumps to the affected `~/.paperclip/.../instructions/*.md`.
- [ ] T045 [P] Schedule a 5-goal retrospective ~30 days out: review the spec-vs-no-spec routing accuracy (SC-002), per-agent budget burn vs. cap, instruction-file revision count. Use `/schedule` to set a recurring reminder if useful.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately.
- **Foundational (Phase 2)**: Depends on Setup completion. **BLOCKS** all user stories. T006-T008 protect the security + safety invariants.
- **User Story 1 (Phase 3, P1, MVP)**: Depends on Foundational. Can be the entire delivery for v1.
- **User Story 2 (Phase 4, P2)**: Depends on Foundational. Can run in parallel with US1 if you have spare bandwidth, but US1 is gating for MVP.
- **User Story 3 (Phase 5, P3)**: Depends on Foundational. Can run in parallel with US1 + US2.
- **Polish (Phase 6)**: Depends on at least User Story 1 being complete; T042 (end-to-end smoke) depends on US1+US2+US3.

### Within-Phase Dependencies (Phase 3 example)

- T009, T010, T011 (agent creation) can run in parallel.
- T012 (budget caps) depends on T009-T011.
- T013, T014, T015 (CEO instruction files) are **sequential** — same agent's instruction dir, write conflicts otherwise. T013 → T014 → T015.
- T016, T017 (other agents' AGENTS.md) can run in parallel with each other AND with the CEO sequence.
- T018 (wiki seed) is independent — parallel with everything.
- T019 (unpause) depends on T013, T014, T015, T016, T017, T018.
- T020 (smoke test) depends on T019.

### Parallel Opportunities

- All [P] tasks in Setup (T005) can run alongside T001-T004.
- All agent creations across Phase 3 + Phase 4 + Phase 5 (T009-T011, T021-T023, T032) **could** be batched up-front if you want all 7 agents created at once — but I've kept them within their user-story phase to enforce the MVP-first discipline.
- All non-CEO `AGENTS.md` writes (T016, T017, T025, T026, T027, T034) can run in parallel — different files, no shared dependencies.
- All wiki article seeds (T018, T035, T036) can run in parallel — different slugs.
- Phase 6 polish tasks T040, T043, T044, T045 are all parallel.

---

## Parallel Example: User Story 1 setup batch

```bash
# After Phase 2 checkpoint, launch the US1 agent-creation batch in parallel:
Task: T009 — Create CEO agent (paused) via Paperclip API
Task: T010 — Create Backend Engineer agent (paused)
Task: T011 — Create Critic agent (paused)

# Then sequential CEO instruction writes (same dir):
Task: T013 → T014 → T015

# In parallel with the CEO sequence above:
Task: T016 — Backend AGENTS.md
Task: T017 — Critic AGENTS.md
Task: T018 — Seed mcpproxy-roadmap wiki article
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 + Phase 2 (Setup + Foundational): T001-T008. ~30 min.
2. Phase 3 (US1 — CEO + Backend + Critic + roadmap wiki + smoke): T009-T020. ~90 min.
3. **STOP at T020 checkpoint**. You now have a goal-to-synthesis cockpit. Demo: post a synthetic goal, watch a synthesis arrive, decide if quality meets bar. Iterate `SOUL.md` if needed.
4. Decide whether to continue to Phase 4. If first goals reveal that synthesis quality needs more iteration on the CEO's prompt, do that before adding implementation agents.

### Incremental Delivery

1. Setup + Foundational → tools confirmed, invariants locked.
2. US1 → MVP cockpit (proposal generator with approval gate). Ship this on its own.
3. US2 → Add implementation experts. Now goals can autonomously open PRs.
4. US3 → Add QA + wiki maintenance. Now the loop closes — institutional memory accumulates.
5. Polish → Repo-side commit, end-to-end smoke, refinement based on what surfaced.

### Single-Operator Strategy (you, with CC assistance)

You are the only operator. Run sequentially through phases; use CC subagents to accelerate the "write 7 markdown files" step in T013-T017, T025-T028, T034 if you want parallelism. For the wiki seeds (T018, T035, T036), you can use your own Synapbus MCP access from a CC session rather than going through the (paused) CEO agent.

---

## Notes

- This feature ships **no code** in mcpproxy-go beyond `docs/agent-cockpit.md` and the `specs/045-paperclip-cockpit/` artifacts. All "implementation" is markdown agent prompts + wiki content + Paperclip Web UI clicks.
- Tasks are written so each is single-action. If a task seems to do two things (e.g., T012 sets three budget caps), it's because they all live in the same Paperclip Web UI screen and serializing them across three tasks adds clutter without parallelism gain.
- "[P] [USx] Create agent X in Paperclip" tasks are **parallel within the same user story** because they're independent API POSTs, but they cannot run before Phase 2 completes (T006-T008 lock the company invariants).
- Each user story phase ends with a **smoke test task** (T020, T030+T031, T039) that exercises the story's acceptance scenarios from spec.md. Do not skip these — they are the only "tests" this configuration-only feature has.
- The "≥3 file areas" routing rule is acknowledged as fuzzy; SC-002 measures it empirically over the first 5 real goals, and T045 schedules the retrospective.
- **Multi-LLM strategy** (FR-015 + FR-016): the cockpit deliberately uses three different model providers. The **CEO + 5 implementation experts + QA Tester run on Claude Code (Anthropic)** as their primary CLI. The **Critic runs on Gemini CLI** (`gemini-3.1-pro-preview`) — model diversity surfaces blind spots one model misses (a pattern proven by prior gemini cross-reviews catching P1 bugs that Claude TDD+E2E missed). **Synapbus reading + summarization runs on opencode + kimi2.5-gcore** — long-context model offloads bulk reading from primary CLIs, conserving their context windows. Other expert AGENTS.md (T016, T025, T026, T027, T034) inherit the opencode summarization procedure from CEO TOOLS.md (T015) by reference rather than duplicating it.
