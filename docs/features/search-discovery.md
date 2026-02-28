---
id: search-discovery
title: Tool Search & Discovery
sidebar_label: Search & Discovery
sidebar_position: 5
description: Intelligent tool discovery using BM25 search across all MCP servers
keywords: [search, discovery, bm25, tools, index]
---

# Tool Search & Discovery

MCPProxy provides intelligent tool discovery using BM25 full-text search across all connected MCP servers.

## Overview

When you have multiple MCP servers with dozens or hundreds of tools, finding the right tool becomes challenging. MCPProxy indexes all tools and provides fast, relevant search results.

## How It Works

### Indexing

1. MCPProxy connects to upstream servers
2. Retrieves tool metadata (name, description, parameters)
3. Indexes tools using Bleve full-text search engine
4. Updates index when servers reconnect or tools change

### Search Algorithm

MCPProxy uses **BM25** (Best Matching 25) ranking:

- Considers term frequency in tool descriptions
- Accounts for document length
- Ranks results by relevance score

## Using Search

### Via MCP Protocol

Use the `retrieve_tools` built-in tool:

```json
{
  "query": "create github issue",
  "limit": 5
}
```

Response:

```json
{
  "tools": [
    {
      "name": "github:create_issue",
      "server": "github-server",
      "description": "Create a new issue in a GitHub repository",
      "score": 0.89
    },
    {
      "name": "gitlab:create_issue",
      "server": "gitlab-server",
      "description": "Create an issue in GitLab",
      "score": 0.72
    }
  ]
}
```

### Via REST API

```bash
curl -H "X-API-Key: your-key" \
  "http://127.0.0.1:8080/api/v1/tools?q=create%20file&limit=10"
```

### Via Web UI

1. Open the dashboard
2. Use the search box in the header
3. Type keywords to find tools
4. Click a tool to see details

## Configuration

### Search Settings

```json
{
  "tools_limit": 15
}
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `tools_limit` | integer | `15` | Maximum results per query |

### Index Location

The search index is stored at:

```
~/.mcpproxy/index.bleve/
```

## Search Tips

### Effective Queries

| Query | Finds |
|-------|-------|
| `create file` | Tools for creating files |
| `github issue` | GitHub-specific issue tools |
| `read json` | Tools that read JSON data |
| `sql query database` | Database query tools |

### Query Syntax

- **Multiple words**: Matched as AND (all words must appear)
- **Quoted phrases**: Exact phrase match (`"create issue"`)
- **Server prefix**: Filter by server (`github:create`)

## Index Management

### Manual Rebuild

If the search index becomes corrupted or out of sync:

```bash
# Stop MCPProxy
pkill mcpproxy

# Remove index
rm -rf ~/.mcpproxy/index.bleve

# Restart - index rebuilds automatically
mcpproxy serve
```

### Index Statistics

Check index status:

```bash
mcpproxy doctor
# Shows indexed tool count
```

## Automatic Tool Discovery

### Real-Time Updates via Notifications

MCPProxy supports the MCP `notifications/tools/list_changed` notification protocol. When an upstream MCP server updates its available tools (adds, removes, or modifies), it can send a notification to trigger automatic re-indexing:

1. **Server Support**: Servers that advertise `capabilities.tools.listChanged: true` send notifications when their tools change
2. **Automatic Re-indexing**: MCPProxy receives the notification and triggers `DiscoverAndIndexToolsForServer()` within seconds
3. **No Polling Delay**: Tools are updated immediately instead of waiting for the 5-minute background refresh cycle

### Fallback Behavior

For servers that don't support tool change notifications:
- MCPProxy continues to poll every 5 minutes
- Manual refresh available via Web UI or `mcpproxy upstream restart`

### Logs

When notifications are received:
- `INFO`: "Received tools/list_changed notification from server: {name}"
- `DEBUG`: "Server supports tool change notifications - registered handler"
- `DEBUG`: "Tool discovery triggered by notification"

## Performance

### Index Updates

- **Incremental**: Only changed tools are re-indexed
- **Hash-based**: Tool content hash determines changes
- **Non-blocking**: Indexing runs in background
- **Reactive**: Servers with notification support trigger immediate updates

### Search Speed

- Typical queries: < 10ms
- Large indexes (1000+ tools): < 50ms
- Results are cached for repeated queries

## Troubleshooting

### No Results

1. Verify servers are connected:
   ```bash
   mcpproxy upstream list
   ```

2. Check tool count:
   ```bash
   mcpproxy tools list
   ```

3. Rebuild index if needed

### Stale Results

If search results don't reflect current tools:

1. Restart the problematic server:
   ```bash
   mcpproxy upstream restart server-name
   ```

2. Wait for re-indexing (check logs)

### Index Corruption

```bash
# Remove and rebuild
rm -rf ~/.mcpproxy/index.bleve
mcpproxy serve
```

## Integration with AI Clients

AI clients typically use MCPProxy's search in two ways:

1. **Direct Tool Call**: AI calls `retrieve_tools` to find relevant tools
2. **Automatic Discovery**: MCPProxy returns matching tools based on the task context

The `tools_limit` setting controls how many tools are suggested to the AI, balancing between:
- More tools = better coverage but higher token usage
- Fewer tools = faster responses but may miss relevant tools
