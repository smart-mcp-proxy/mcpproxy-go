# Heartbeat: every 6 hours

## Sweep actions on each fire

### 1. Stale-goal sweep
For each Paperclip ticket in state `Implementing`, `Decomposing`, or `Proposed` with no progress comment in >24h:
- Post a ping comment to the assigned expert: "Status check — last activity NN hours ago. Are you blocked?"
- If no response within next 6h, escalate to user (Synapbus DM to `algis` with priority 6).

### 2. Roadmap freshness sweep
- `mcp__synapbus__execute action=read_article slug=mcpproxy-roadmap`
- If `last_edited_at` is >7 days ago AND there are open goals in the roster, regenerate the article in full and `update_article`.
- Anti-spam: at most one update per 24h per article.

### 3. Budget burn check
- For each agent in the company, query Paperclip `paperclipListAgents` for current daily spend.
- If any agent is >75% of its daily cap, post a low-priority (3) notice to `#my-agents-algis`: "BUDGET WARN: <agent> at NN% of $X cap."

### 4. On PR-merge events (out of band — triggered, not heartbeat)
When a PR linked to a Paperclip ticket merges (detected via webhook, gh-poll, or user notification):
- Append entry to `mcpproxy-shipped` (append-only log).
- Update `mcpproxy-roadmap` (move from "In flight" to "Recently shipped (last 30d)").
- If the synthesis chose option B/C over A (i.e., a non-default choice), append entry to `mcpproxy-architecture-decisions`.
- Post one summary to `#my-agents-algis` (priority 5).

## Anti-spam guardrails

- ≤1 wiki article update per 24h per article (idempotent rewrite is OK).
- ≤1 channel post per milestone.
- ≤1 ping per stale ticket per 24h.
