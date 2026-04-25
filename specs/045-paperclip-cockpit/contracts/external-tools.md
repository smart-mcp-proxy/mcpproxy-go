# Phase 1 — External Tool Contracts

**Date**: 2026-04-25

Traditional REST/GraphQL contracts do not apply here — this feature ships no API. The de facto contracts are the **MCP tool surfaces** the cockpit's agents depend on. This document enumerates them so future Paperclip / Synapbus changes that break these tools can be detected as a contract change.

## Paperclip MCP Tools (consumed by all agents)

Source: [paperclipai/paperclip `packages/mcp-server`](https://github.com/paperclipai/paperclip/tree/master/packages/mcp-server). The full surface is ~48 tools; the cockpit relies on the subset below.

### Read tools (used by experts before proposing, and by CEO during decomposition)

| Tool | Used by | Purpose |
|---|---|---|
| `paperclipListIssues` | CEO, all experts | enumerate active goals + ad-hoc issues |
| `paperclipGetIssue` | CEO, all experts | fetch full goal context (description, attached docs, comments) |
| `paperclipListIssueComments` | CEO, Critic | read prior synthesis / comments on an existing thread |
| `paperclipListIssueDocuments` | CEO, Critic | enumerate proposals attached to a goal |
| `paperclipGetDocument` | CEO, Critic | fetch a specific proposal's content |
| `paperclipListAgents` | CEO | confirm roster (used during dispatch decisions) |
| `paperclipListProjects` | CEO | scope goal-to-project routing if needed |

### Write tools (used by experts to post proposals, by CEO to post syntheses, by QA to attach reports)

| Tool | Used by | Purpose |
|---|---|---|
| `paperclipUpsertIssueDocument` | Experts, QA Tester | create/update a proposal or test report attached to a goal ticket |
| `paperclipAddComment` | CEO, all experts, Critic | post synthesis, critique, or status update on a goal |
| `paperclipUpdateIssue` | CEO | transition goal state (Decomposing → Proposed → Implementing → Shipped) |
| `paperclipReact` | (read-only by agents) | reactions are user-driven; agents read but never react |

### Tools NOT used (explicit denylist)

| Tool | Reason |
|---|---|
| `paperclipCreateIssue` outside the MCPProxy company | scope discipline |
| `paperclipDeleteIssue` | preserve audit trail |
| `paperclipApiRequest` (escape hatch) | only the user/admin uses this; agents stick to the typed tool surface |

## Synapbus MCP Tools (consumed by CEO + experts for context, by CEO for wiki)

Source: existing Synapbus MCP server on kubic, exposed as `mcp__synapbus__*` per the global `~/.claude/CLAUDE.md`.

### Read tools (provenance gathering)

| Tool | Used by | Purpose |
|---|---|---|
| `mcp__synapbus__my_status` | CEO heartbeat | check inbox at heartbeat time |
| `mcp__synapbus__search` | CEO during decomposition; experts during proposal | semantic recall over channels and #open-brain |
| `mcp__synapbus__get_replies` | Critic | read threaded discussion when a prior decision is referenced |
| `mcp__synapbus__execute` action=`list_articles` | CEO | enumerate wiki articles before update |
| `mcp__synapbus__execute` action=`read_article` | CEO | fetch current wiki content for diff-and-update |

### Write tools (announcements + wiki maintenance)

| Tool | Used by | Purpose |
|---|---|---|
| `mcp__synapbus__send_message` | CEO | post goal-approved / shipped / regression announcements per the spec's anti-spam table |
| `mcp__synapbus__execute` action=`create_article` | CEO (bootstrap T-003 only) | seed initial three wiki articles |
| `mcp__synapbus__execute` action=`update_article` | CEO heartbeat + on-ship | maintain `mcpproxy-roadmap` (full rewrite) and append to `mcpproxy-architecture-decisions` / `mcpproxy-shipped` |

### Channels written to (with priorities, from spec)

| Channel | Event | Priority |
|---|---|---|
| `#my-agents-algis` | Goal approved, PR opened, QA report ready, Shipped | 4–5 |
| `#bugs-mcpproxy` | QA-found regression | 7 |
| `#approvals` | High-stakes approval request only | 8 |

### Tools NOT used (explicit denylist)

| Tool | Reason |
|---|---|
| `mcp__synapbus__execute` action=`delete_article` | wiki articles are append-only or full-rewrite, never deleted |
| Direct messages to other users | the CEO is single-user-scoped; cross-user DMs are out of scope |

## MCPProxy MCP / REST API (used by some experts when goal involves MCPProxy itself)

When an expert needs to inspect mcpproxy's own state (e.g., to draft a proposal about an mcpproxy server's health), the expert uses **existing** mcpproxy tools — no new ones are added by this feature.

| Tool | Used by | Purpose |
|---|---|---|
| `mcp__mcpproxy__upstream_servers` | Backend, QA | list configured upstream servers when goal touches connectivity |
| `mcp__mcpproxy__retrieve_tools` | All | search mcpproxy's tool index when goal references "tool X" |
| `mcp__mcpproxy__quarantine_security` | Backend, QA | inspect quarantine state when goal touches security |
| `mcp__mcpproxy__read_cache` | Backend | inspect activity log when goal references prior tool calls |

**No new MCPProxy endpoints or tools are introduced by this feature.**

## Agent Instruction File Contract

Each agent's instruction directory (`~/.paperclip/instances/default/companies/<id>/agents/<id>/instructions/`) must contain at minimum one `AGENTS.md` with the following sections (top-down):

```markdown
# Role: <agent name>

## Mandate
<what you do, what you don't>

## Inputs
- Synapbus channels: <list>
- Synapbus wiki articles to consult: <list>
- Paperclip ticket fields: <list>

## Outputs
- Paperclip documents/comments: <when, format>
- PRs (if applicable): <branch naming, commit conventions>

## Tools (allowlist)
- Paperclip MCP: <subset of the table above>
- Synapbus MCP: <subset of the table above>
- (CEO only) MCPProxy MCP: <subset>

## Provenance rule
Every claim in a proposal/synthesis cites at least one Synapbus message ID or wiki [[slug]].
```

The CEO additionally has `SOUL.md`, `HEARTBEAT.md`, `TOOLS.md`. These are loaded by Paperclip's process adapter alongside `AGENTS.md`.

**Contract**: changes to this format must be backward-compatible with Paperclip's adapter. Verify by checking `paperclip-create-agent` skill in upstream paperclipai/paperclip.

## External CLI Dependencies

The cockpit is multi-LLM by design. Three CLIs are runtime dependencies:

### Claude Code (default for CEO + 5 implementation experts + QA Tester)

- **CLI**: `claude` (Claude Code)
- **Models**: Anthropic Opus / Sonnet / Haiku (configured per Paperclip's adapter)
- **Instruction file**: `AGENTS.md` (also reads `CLAUDE.md` if present in working dir)
- **MCP support**: full — all `mcp__*` tools enumerated in this contract are reachable

### Gemini CLI (Critic agent only — FR-015, R-9)

- **CLI**: `gemini --yolo --model gemini-3.1-pro-preview`
- **Model**: `gemini-3.1-pro-preview` (Google)
- **Instruction file**: `GEMINI.md` (primary), `AGENTS.md` (mirror for Paperclip adapter compatibility)
- **MCP support**: yes, but the registered tool surface may differ from Claude's. Verify via `gemini mcp list` after configuring the Critic agent. Required tools: `paperclipGetDocument`, `paperclipListIssueDocuments`, `mcp__synapbus__search`, `mcp__synapbus__get_replies`.
- **Why structurally different model**: model diversity for adversarial review (R-9 rationale).

### opencode CLI (Synapbus context summarization — FR-016, R-10)

- **CLI**: `opencode run --model kimi2.5-gcore`
- **Model**: `kimi2.5-gcore` (Moonshot Kimi K2 line, hosted on Gcore)
- **Used by**: every agent (CEO, 5 experts, QA Tester, Critic) when Synapbus content exceeds threshold
- **Invocation**: subprocess from inside the agent's primary CLI session, with raw Synapbus search/thread JSON as input + canonical summarization prompt template (see research.md R-10)
- **Output contract**: ≤300-word summary that preserves Synapbus message IDs as inline citations (so provenance rule FR-003 still holds for proposals built on summaries)
- **Not metered in Paperclip budget caps** — opencode runs outside Paperclip's process adapter; track separately via opencode/Gcore billing.

### CLI tool denylist

- No Critic-via-Claude (defeats the model-diversity purpose of FR-015)
- No raw Synapbus reading >5 messages without opencode summarization (defeats the context-window economics of FR-016)
- No alternative summarization models (e.g., GPT-class long-context) without amending R-10

## Versioning

This contract document is versioned with the spec. Breaking changes (e.g., Paperclip removes `paperclipUpsertIssueDocument`, Gemini removes the `--yolo` flag, opencode drops `kimi2.5-gcore`) require a new spec or amendment.
