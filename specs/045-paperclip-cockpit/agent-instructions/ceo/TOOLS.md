# Tools: explicit allowlist + denylist

## Paperclip MCP (read)
- `paperclipListIssues`
- `paperclipGetIssue`
- `paperclipListIssueComments`
- `paperclipListIssueDocuments`
- `paperclipGetDocument`
- `paperclipListAgents`

## Paperclip MCP (write)
- `paperclipUpsertIssueDocument` — for synthesis attachments only (proposals are uploaded by experts)
- `paperclipAddComment` — synthesis comment, status updates, clarifications, refusals
- `paperclipUpdateIssue` — state transitions (Decomposing → Proposed → Implementing → Shipped)

## Synapbus MCP (read)
- `mcp__synapbus__my_status` — heartbeat inbox check
- `mcp__synapbus__search` — semantic recall before decomposition
- `mcp__synapbus__get_replies` — threaded discussion when prior decision is referenced
- `mcp__synapbus__execute action=list_articles` — wiki inventory
- `mcp__synapbus__execute action=read_article` — fetch wiki content for diff-and-update

## Synapbus MCP (write)
- `mcp__synapbus__send_message` — channel announcements (anti-spam table in spec FR-014)
- `mcp__synapbus__execute action=update_article` — wiki maintenance (full rewrite of mcpproxy-roadmap; append entries to mcpproxy-architecture-decisions / mcpproxy-shipped)
- `mcp__synapbus__execute action=create_article` — bootstrap T-003 only; should not be needed during normal operation

## MCPProxy MCP (read-only — for context when goal touches MCPProxy state)
- `mcp__mcpproxy__upstream_servers`
- `mcp__mcpproxy__retrieve_tools`
- `mcp__mcpproxy__quarantine_security`
- `mcp__mcpproxy__read_cache`

## Denylist (do not call)

- ❌ `paperclipDeleteIssue` (preserve audit trail)
- ❌ `paperclipApiRequest` (escape hatch — admin/user only)
- ❌ `paperclipCreateIssue` outside MCPProxy company (scope discipline)
- ❌ `mcp__synapbus__execute action=delete_article` (wiki is append-only or full-rewrite, never deleted)
- ❌ DMs to other users (single-user-scoped)
- ❌ Any MCPProxy WRITE tools (no calling tools that mutate mcpproxy state)

## Synapbus context summarization helper (FR-016, R-10) — CANONICAL PROCEDURE

**All agents inherit this procedure from this file.**

When `mcp__synapbus__search` returns >5 messages OR a thread has >10 replies, **DO NOT read the raw output directly**. Instead, run the summarization subprocess:

```bash
opencode run --model kimi2.5-gcore "You are a context summarizer for an AI agent working on goal '<goal text>'. Compress the following Synapbus messages into ≤300 words preserving message IDs as inline citations: <raw search/thread JSON>"
```

Use the summary as your context. The original message IDs in the summary remain valid for provenance citations (FR-003).

Why: long-form Synapbus history bloats your primary CLI's context window; offload bulk reading to a long-context model (`kimi2.5-gcore`) that's optimized for this. Reasoning + writing happens in your primary CLI.
