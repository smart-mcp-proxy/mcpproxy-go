# Phase 0 — Research: Paperclip Goal Cockpit

**Date**: 2026-04-25
**Status**: Complete — all open questions resolved

This document captures the resolved decisions for unknowns flagged in the spec and design doc. Each entry is **Decision / Rationale / Alternatives**.

## R-1: AGENTS.md template structure for each role

**Decision**: Adapt from Paperclip's `paperclip-create-agent` skill (specifically `references/agents/` and `references/agent-instruction-templates.md` in the paperclipai/paperclip repo). For the six non-CEO agents, use a flat `AGENTS.md` with five sections: *Role*, *Mandate (what you do, what you don't)*, *Inputs (Synapbus channels + wiki articles to read)*, *Outputs (Paperclip ticket comments + PRs)*, *Tools (allowlist)*.

For the CEO, use four files: `AGENTS.md` (role + mandate + tools), `SOUL.md` (decision-making voice + provenance rule), `HEARTBEAT.md` (regular cadence + roadmap freshness sweep), `TOOLS.md` (explicit allowlist of Paperclip MCP + Synapbus MCP tools).

**Rationale**: Paperclip's skill bundles already encode the platform's prompt conventions. Adapting them costs less than inventing a new format. The CEO's multi-file structure mirrors Paperclip's CEO-style root agent observed in the existing default agent dir (`HEARTBEAT.md`, `SOUL.md`, `TOOLS.md` only present on the CEO).

**Alternatives considered**:
- A single `AGENTS.md` per agent with all sections inline — rejected for CEO because the Soul/Heartbeat/Tools concerns evolve at different cadences and a single file becomes harder to edit without merge conflicts.
- Inventing a custom prompt format optimized for mcpproxy domain — rejected because Paperclip's adapter expects its own format, and divergence would silently lose features (e.g., heartbeat scheduling).

## R-2: CEO heartbeat cadence

**Decision**: CEO heartbeat fires every **6 hours**. Each fire performs: (a) sweep Paperclip tickets for stale "in-flight" goals (no progress in >24h → ping the assigned expert), (b) sweep `mcpproxy-roadmap` wiki article for staleness (>7 days since last edit while goals exist → re-render), (c) check Anthropic budget burn vs. cap, post a low-priority `#my-agents-algis` notice if >75% used.

**Rationale**: 6h matches Paperclip's existing heartbeat resolution and is short enough to catch a stuck goal within a working day, long enough to avoid spurious wake-ups. The three sweep actions are exactly the maintenance the CEO is responsible for under FR-007 + FR-014.

**Alternatives considered**:
- Hourly — rejected as over-frequent for a single-user system; spurious noise without offsetting value.
- Daily — rejected because a goal stuck at hour 18 of a multi-stage flow would be discovered too late.
- On-demand only — rejected because the wiki article would drift; an explicit cadence is the only way to enforce SC-006 (24h freshness after ship).

## R-3: Initial Paperclip per-agent budget caps

**Decision**: Conservative day-one caps in USD/day:
- CEO: $5
- Backend / Frontend / macOS / Release Engineer: $3 each
- QA Tester: $4 (long test plans + report generation cost more than implementation tokens)
- Critic: $2 (short adversarial reads, cheap)

Total ceiling: $23/day. Adjusted empirically after the first 5 goals (SC-002, SC-005).

**Rationale**: The first 5 goals are exploratory; runaway is the bigger risk than capped throughput. CEO + QA get the most because they are read-heavy (Synapbus search, test report generation). Critic is cheapest because its job is pure review.

**Alternatives considered**:
- Single $50/day company-wide cap — rejected because a single runaway agent could starve all others.
- No caps initially — rejected on FR-006 grounds; Paperclip's `--budget-cap` is the safety net.
- Higher caps ($20+/agent) — rejected for first phase; can be raised once usage patterns are observed.

## R-4: `mcpproxy-roadmap` initial seed content

**Decision**: Seed from the current `specs/` state, derived from the survey done at brainstorming time (already in conversation context). The article opens with four sections:

- **In flight** — specs with tasks.md showing >0 unchecked tasks AND recent commits (lookback: 30 days). Initial entries: 042 telemetry tier2, 043 linux package repos, 044 diagnostics taxonomy, 044 retention telemetry v3, 037 macos swift tray. Cross-link each as `[[spec-NNN-name]]`.
- **Recently shipped (last 30 days)** — derived from `git log --merges --since="30 days ago" --first-parent main` filtered for merge commits whose subject mentions a spec number. Initial entries from recent commits: spec 044 (diagnostics + telemetry), spec 043 (Linux packaging), spec 040 (server UX subset), and the two bugfix PRs #407 + #408.
- **Planned** — specs with no `tasks.md` or empty tasks: 037, 038, 039.x, 041 (per the brainstorming survey). Each entry is one line.
- **Parked** — empty initially.

Article ends with a footer noting the rewrite cadence (every goal milestone, max one rewrite/day).

**Rationale**: The survey done during brainstorming gave us the inventory; reusing it avoids re-research. The four-section format matches the design doc's wiki-ownership table.

**Alternatives considered**:
- Empty stub initially — rejected because the wiki's value is being current from day one, and the bootstrap human (user + CC) already has the inventory.
- Generated programmatically by a script — rejected as scope creep; the user can hand-seed once and the CEO maintains thereafter.

## R-5: Whether `docs/agent-cockpit.md` is worth adding to the repo

**Decision**: **Yes, but minimal** — a 30-line page in `docs/` that explains: (a) the cockpit exists, (b) it is configuration outside the repo, (c) link to the design doc and the spec dir. Purpose is discoverability for future contributors who see "Paperclip" mentioned in commit history and wonder what it is.

**Rationale**: SC-007 requires direct CC sessions to remain unchanged; a future contributor stumbling on agent-generated commits without context could misunderstand the workflow. A small pointer page costs little and prevents that.

**Alternatives considered**:
- Skip it entirely — rejected for the discoverability reason above.
- Move the design doc to `docs/` and remove from `docs/superpowers/specs/` — rejected; `docs/superpowers/specs/` is the established home for brainstorm artifacts and breaking that convention costs more than it gains.

## R-6: Synapbus wiki tool surface

**Decision**: Wiki operations are performed via `mcp__synapbus__execute` with action names. Required actions for cockpit:
- `list_articles` — read article inventory
- `read_article` (or equivalent) — fetch current content for editing
- `create_article` — create a new article (used during bootstrap T-003)
- `update_article` — replace article content (used during CEO heartbeat sweeps)

The CEO's `TOOLS.md` enumerates these as the only wiki-edit tools the CEO is allowed to call.

**Rationale**: From the global `~/.claude/CLAUDE.md`, Synapbus tools are exposed via `mcp__synapbus__*` and the catch-all `execute` action handles wiki ops. Listing them in `TOOLS.md` makes the allowlist explicit so the CEO doesn't accidentally use undocumented actions.

**Alternatives considered**:
- Have the user manually maintain wiki articles — rejected because SC-006 (100% roadmap freshness within 24h post-ship) requires automation.
- Use a separate per-action tool (e.g., `mcp__synapbus__update_article` direct call) — viable but unconfirmed at this time; if such direct tools exist, the CEO can use them; if not, fall back to `execute`.

## R-7: How agents call speckit slash commands

**Decision**: Implementation agents (Backend/Frontend/macOS) that handle "big" goals invoke `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`, `/speckit.implement` from inside the Claude Code subprocess that Paperclip spawns in the mcpproxy-go working directory. They do NOT shell out to a CLI; they invoke as skills.

**Rationale**: Paperclip's process adapter spawns Claude Code as a subprocess in a working directory. Inside that subprocess, the speckit skills are available exactly as in interactive CC sessions. No special wiring is needed — agents just need to know the slash commands as their canonical workflow for big features (encoded in their `AGENTS.md`).

**Alternatives considered**:
- Have agents write spec files directly without going through speckit — rejected because the speckit workflow produces the constitutional gate checks, the checklist artifacts, and the implementation plan; bypassing it loses guardrails.
- Have a dedicated "Speckit Agent" — rejected as over-segmentation; any implementation expert can run speckit when its turn comes.

## R-8: How agents detect the CEO routing rule (≥3 file areas etc.)

**Decision**: Encode the routing rule as an explicit **decision tree in the CEO's `SOUL.md`**, with worked examples drawn from real specs:

> Big-feature triggers (route to speckit):
> - Touches code in ≥3 directories under `internal/`, `frontend/src/`, `native/macos/`, `cmd/`
> - Touches `internal/storage/`, `internal/security/`, `oas/`, `internal/auth/`, or `cmd/mcpproxy/exit_codes.go` (data/security/release-impact)
> - User text contains "spec it", "make a spec", "this needs design"
> - Estimated >1 day of focused work
>
> Otherwise: small-feature route (direct PR, no spec).

Worked examples: spec 042 telemetry-tier2 (BIG — touches contracts + telemetry + multi-platform) vs. PR #407 tooltip clipping (SMALL — one CSS line).

**Rationale**: A code-driven rule would require parsing repo state from inside the CEO. A prompt-driven decision tree with examples is the simpler, more inspectable approach and matches how all current speckit specs were classified historically (by judgement against the constitution).

**Alternatives considered**:
- A scripted classifier that runs `git ls-files` against the goal description — rejected as premature; the prompt rule with examples is good enough for v1, and the CEO can refine over the first 5 goals (SC-002).
- No rule, always route to speckit — rejected because trivial bugs would generate spec dirs and pollute `specs/`.
- No rule, never route to speckit — rejected because big features benefit from the constitutional gate and design contract.

## R-9: Critic agent runs on Gemini CLI (gemini-3.1-pro-preview), not Claude Code

**Decision**: The Critic agent's per-agent CLI runtime in Paperclip is configured to spawn `gemini --yolo --model gemini-3.1-pro-preview` rather than the default Claude Code subprocess. Critic's instructions are authored in `GEMINI.md` (Gemini's native instruction filename) with a mirror copy in `AGENTS.md` for Paperclip adapter compatibility.

**Rationale**: Model diversity surfaces blind spots that any single model misses. The user's prior pattern — dispatching Gemini cross-reviews after spec/PR completion — found 4 P1 bugs and 1 dead-code bug on spec 044 that the project's own Claude-based TDD + E2E testing missed. Making the Critic structurally a different model (rather than a different prompt to the same model) bakes that diversity into the workflow rather than relying on the user remembering to dispatch a manual cross-review.

**Alternatives considered**:
- Run Critic on Claude Code with an "adversarial" prompt — rejected because identical training distribution means many blind spots are shared. The point of a critic is to *not* think the same way as the proposal author.
- Run Critic on a third frontier model (e.g., GPT family) — viable in principle, but the user's existing tooling (`gemini --yolo`) and prior empirical results favor Gemini.
- Use Anthropic's smaller model (Haiku) to economize — rejected because Critic's job is rigor, not speed; a cheaper-but-similar model loses the diversity benefit.

**Implications**:
- Gemini's MCP tool surface differs from Claude's. Each MCP tool the Critic depends on (`paperclipGetDocument`, `paperclipListIssueDocuments`, `mcp__synapbus__search`, `mcp__synapbus__get_replies`) must be verified to register with Gemini via `gemini mcp list` during T011/T017.
- The Critic's prompt voice in `GEMINI.md` should be authored for Gemini's strengths (less self-deprecating hedging, more direct critique) rather than Claude's.
- T020 smoke test verifies the Critic comment originated from a Gemini run by inspecting Paperclip activity log / agent metadata — catches accidental fallback to Claude Code if Gemini config is wrong.

## R-10: Synapbus context summarization via opencode CLI + kimi2.5-gcore

**Decision**: When any agent encounters Synapbus content over a small threshold (>5 messages from a search, >10 replies in a thread), the agent compresses it through `opencode run --model kimi2.5-gcore` with a fixed summarization prompt template before reasoning over it. The procedure is authored in the CEO's `TOOLS.md` (T015) and inherited by all other agents' `AGENTS.md` by reference.

**Prompt template** (canonical, subject to refinement):

```
You are a context summarizer for an AI agent working on goal '<goal text>'.
Compress the following Synapbus messages into ≤300 words preserving message IDs
as inline citations:

<raw search/thread JSON>
```

**Rationale**:
- The `kimi2.5-gcore` model has a long context window optimized for cheap bulk reading, so it digests Synapbus history without consuming a primary-CLI context window.
- Each agent's primary CLI (Claude Code or Gemini) keeps its context lean for reasoning + writing tasks. Bulk reading happens in the cheap layer.
- Provenance is preserved — message IDs in the summary remain the raw source-of-truth pointers, satisfying FR-003 for proposals built on summarized context.
- Centralizing the procedure in CEO `TOOLS.md` (rather than copy-pasted across 7 agent files) means future tweaks (different model, different prompt) update one location.

**Alternatives considered**:
- Have each agent read raw Synapbus output directly — rejected for context-window economics; one rich Synapbus thread can occupy 20k+ tokens of context that would crowd out the agent's actual reasoning.
- Use Anthropic's prompt caching for repeated Synapbus content — viable but solves a different problem (repeat-read economics, not first-read context bloat).
- Build a dedicated "Synapbus Reader" agent in the Paperclip org chart (8th agent) — rejected as over-engineering; the procedure is one CLI call and doesn't need budget tracking, an org-chart slot, or a Paperclip ticket lifecycle.
- Use `kimi2.5-gcore` directly via API rather than through opencode — viable but loses opencode's prompt + tool integration; opencode is the CLI the user already runs locally for this kind of work.

**Implications**:
- T005b verifies opencode + kimi2.5-gcore reachable as a Phase 1 prerequisite.
- The summarization step has its own (small) cost; not factored into per-agent Paperclip budget caps because opencode is invoked outside Paperclip's metering. The user's overall daily LLM spend therefore = Paperclip caps (R-3) + opencode/kimi2.5 calls. Empirical observation in the first 5 goals (SC-002, SC-005) will reveal if a budget guardrail is needed for kimi2.5 too.

## Open Items After Phase 0

None. All design-doc Open Questions plus the multi-LLM strategy decisions (R-9, R-10) are resolved above.

## Inputs to Phase 1

- Phase 1 needs to enumerate the **entities** that the cockpit produces and consumes. These are mostly external to mcpproxy-go (Paperclip ticket DB, Synapbus DB) — `data-model.md` will document them as a logical model with a "lives in" column.
- Phase 1 needs to enumerate the **MCP tool contracts** the cockpit depends on (Paperclip MCP server tools + Synapbus MCP tools). These are de facto contracts — `contracts/external-tools.md` will list them.
- Phase 1 needs a **bootstrap walkthrough** in `quickstart.md` — step-by-step from "no agents exist" to "first synthetic goal completed".
