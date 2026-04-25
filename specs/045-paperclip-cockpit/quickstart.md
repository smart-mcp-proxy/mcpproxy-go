# Phase 1 — Quickstart: Bootstrapping the Cockpit

**Date**: 2026-04-25
**Audience**: User (one-time bootstrap) and Claude Code assisting the user
**Estimated time**: under 4 hours of focused work (per SC-005)

This walkthrough takes you from "Paperclip is running but has no agents configured for MCPProxy work" to "first synthetic goal completes the P1+P2+P3 flow."

## Prerequisites (verify before starting)

- [ ] Paperclip running on `http://127.0.0.1:3100` and reachable
- [ ] `curl http://127.0.0.1:3100/api/health` returns `{deploymentMode: "local_trusted", authReady: true, ...}`
- [ ] Company "MCPProxy" exists (id `16edd8ed-…`) — verify via `curl http://127.0.0.1:3100/api/companies | jq`
- [ ] Synapbus reachable: `mcp__synapbus__my_status` returns OK
- [ ] Anthropic API key configured in Paperclip with budget headroom (≥$30 for first day)
- [ ] **Gemini CLI installed** and authenticated via Google OAuth (`gemini auth` if not yet logged in). Smoke check: `gemini --model gemini-3.1-pro-preview "respond with the single word OK"` (FR-015, R-9 — Critic agent runs on Gemini, not Claude Code). No `GEMINI_API_KEY` env var needed.
- [ ] **opencode CLI installed** + `kimi2.5-gcore` model reachable. Smoke check: `opencode run --model kimi2.5-gcore "echo OK"` (FR-016, R-10 — Synapbus context summarization).
- [ ] You have read the [design doc](../../docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md) and [spec](spec.md)

## Step 1 — Create the seven Paperclip agents (T-001)

Use the Paperclip Web UI at `http://127.0.0.1:3100` (login: see your local Paperclip config). Create:

| Agent | Role | Title | Reports to | Initial budget USD/day |
|---|---|---|---|---|
| CEO | `ceo` | Chief Executive Agent | (none — root) | $5 |
| Backend Engineer | `engineer-backend` | Backend Engineer (Go) | CEO | $3 |
| Frontend Engineer | `engineer-frontend` | Frontend Engineer (Vue) | CEO | $3 |
| macOS Engineer | `engineer-macos` | macOS Engineer (Swift) | CEO | $3 |
| QA Tester | `qa-tester` | QA Tester | CEO | $4 |
| Critic | `critic` | Adversarial Reviewer | CEO | $2 |
| Release Engineer | `engineer-release` | Release Engineer | CEO | $3 |

For each, set:
- Status: `paused` initially (you'll start them after instructions are written)
- Capabilities: leave default for now
- `requireBoardApprovalForNewAgents`: confirm it's **on** at the company level (FR-011)

Capture each agent's UUID (visible in the Web UI URL or via `curl http://127.0.0.1:3100/api/companies/<company-id>/agents | jq`).

## Step 2 — Write per-agent instruction files (T-002)

For each agent, create the instruction directory if missing:

```bash
COMPANY_ID="16edd8ed-..."   # your real ID
AGENT_ID="<agent-uuid>"
mkdir -p ~/.paperclip/instances/default/companies/$COMPANY_ID/agents/$AGENT_ID/instructions
```

### CEO (4 files)

Adapt from Paperclip's `paperclip-create-agent` skill templates:

```
~/.paperclip/.../<ceo-id>/instructions/
├── AGENTS.md     # Role, Mandate, Inputs (Synapbus + wiki articles), Outputs, Tools (allowlist from contracts/external-tools.md)
├── SOUL.md       # Decision-making voice + provenance rule + spec-vs-no-spec decision tree (with worked examples — see research.md R-8)
├── HEARTBEAT.md  # Every 6h: stale-goal sweep, roadmap freshness sweep, budget burn check (R-2)
└── TOOLS.md      # Explicit allowlist of Paperclip MCP + Synapbus MCP + MCPProxy MCP tools the CEO is allowed to call
```

The decision tree in `SOUL.md` is the most consequential file — make sure it includes:
- The big-feature triggers (≥3 file areas, data/security/release-impact paths, "spec it" phrases, >1 day estimated)
- Worked examples: "spec 042 telemetry-tier2 = BIG", "PR #407 tooltip = SMALL"

### Other six agents (1 file each)

Each gets a single `AGENTS.md` with the five-section format from the contract:

- **Backend Engineer** — mandate covers `internal/`, `cmd/`. Tools: paperclipUpsertIssueDocument, paperclipAddComment, mcp__mcpproxy__* read tools, Synapbus search, GitHub PR via subprocess.
- **Frontend Engineer** — mandate covers `frontend/src/`. Same tool kit minus mcpproxy-internal.
- **macOS Engineer** — mandate covers `native/macos/`. Add `mcp__mcpproxy-ui-test__*` for visual verification.
- **QA Tester** — mandate covers test plans + execution + HTML reports. Add Chrome browser ext + `mcp__mcpproxy-ui-test__*`.
- **Critic** — mandate is adversarial review only. Read-only tool kit. Output: comment with `request_changes` reaction or approve.
- **Release Engineer** — mandate covers `nfpm/`, `scripts/build.sh`, CI, R2 distribution. Add release-specific tools.

Write these as plain markdown. No code; no templating engine needed.

## Step 3 — Seed the three Synapbus wiki articles (T-003)

Use the CEO's tools (since the CEO's `TOOLS.md` mandates wiki ops via the CEO only). Or, for bootstrap convenience, use the user's interactive Synapbus access via Claude Code:

```
mcp__synapbus__execute(action="create_article", slug="mcpproxy-roadmap", content=<R-4 seed content>)
mcp__synapbus__execute(action="create_article", slug="mcpproxy-architecture-decisions", content=<empty stub with format definition>)
mcp__synapbus__execute(action="create_article", slug="mcpproxy-shipped", content=<empty stub>)
```

For the roadmap seed content, use the inventory in [research.md R-4](research.md#r-4-mcpproxy-roadmap-initial-seed-content). Cross-link as `[[spec-042-telemetry-tier2]]`, `[[spec-044-diagnostics-taxonomy]]`, etc.

## Step 4 — Configure budget caps (T-004)

In the Paperclip Web UI, for each agent, set the daily budget cap from the table in Step 1. Verify the company-level `feedbackDataSharingEnabled: false` setting is preserved (privacy default).

## Step 5 — (Optional) Add `docs/agent-cockpit.md` (T-005)

Create a single-page overview in the mcpproxy-go repo:

```bash
cat > docs/agent-cockpit.md <<'EOF'
# Paperclip Agent Cockpit (overview)

A subset of MCPProxy work flows through a Paperclip agent cockpit (CEO + 6 expert agents). The cockpit is configured outside this repo (in `~/.paperclip/` and Synapbus). For details see:

- Spec: [`specs/045-paperclip-cockpit/`](../specs/045-paperclip-cockpit/)
- Design: [`docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md`](superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md)
EOF
```

Commit on the `045-paperclip-cockpit` branch.

## Step 6 — First synthetic goal end-to-end smoke test (T-006)

The smoke goal: **"Draft a one-paragraph release-notes entry for v0.25.0"** (small scope, no implementation needed beyond a markdown comment).

1. Unpause the CEO + Backend Engineer + Critic + QA Tester agents.
2. Create a Paperclip ticket in the MCPProxy company. Title: `release notes v0.25.0`. Description: the goal text above.
3. Tag the CEO on the ticket so it picks it up.
4. **Watch P1**:
   - CEO queries Synapbus for "v0.25.0" / recent ships
   - CEO dispatches Backend Engineer (or Release Engineer if you set them up) to draft a proposal
   - Critic reviews
   - CEO posts a synthesis comment (likely Option A: "concise" / Option B: "detailed")
   - You react `approve`
5. **Watch P2**:
   - CEO classifies as small (no spec)
   - Implementation expert opens a tiny PR (probably to a CHANGELOG draft or a release-notes file)
6. **Watch P3**:
   - QA may auto-trigger (or skip for a doc-only PR — fine)
   - You merge the PR manually
   - CEO updates `mcpproxy-shipped` wiki article and posts to `#my-agents-algis`

If any step doesn't work as expected, edit the relevant agent's `AGENTS.md` and re-run. Iterate until the full flow completes.

## Verification (against spec acceptance criteria)

After T-006 completes successfully:

- [ ] **SC-001** — synthesis arrived in <30 minutes
- [ ] **SC-003** — proposal cited at least one Synapbus message or wiki article
- [ ] **SC-004** — agent did not auto-merge the PR
- [ ] **SC-005** — bootstrap took <4 hours
- [ ] **SC-006** — `mcpproxy-shipped` updated within 24h of merge
- [ ] **SC-007** — your normal CC sessions still work (open a separate CC session and run `/superpowers:brainstorming` on a throwaway topic)
- [ ] **SC-008** — Paperclip still loopback-only (`lsof -i :3100` shows only `127.0.0.1`)

If all checks pass, the cockpit is operational. Continue running it through real goals, refining instruction files based on what surfaces. Re-evaluate budget caps and the spec-vs-no-spec routing rule after the first 5 real goals (per SC-002).

## Common Bootstrap Issues

| Symptom | Likely cause | Fix |
|---|---|---|
| CEO doesn't pick up the ticket | Agent paused; or `reportsTo` mis-set | Unpause; verify org chart |
| Proposals lack citations | `AGENTS.md` doesn't enforce provenance rule clearly | Edit the rule into `SOUL.md` more emphatically with a refusal example |
| Synthesis posted with only one option | CEO's prompt doesn't enforce "≥2 options" | Add explicit instruction to `SOUL.md` |
| Wiki edit conflicts | Two CEO heartbeats overlapping | Increase heartbeat cadence to 12h, or serialize via Paperclip's heartbeat-lock |
| Budget cap reached before first goal completes | Caps too tight | Raise CEO + QA caps first; they're read-heavy |
| Agent merged its own PR | Branch protection not enabled | Enable required-reviews on `main` in GitHub repo settings |
