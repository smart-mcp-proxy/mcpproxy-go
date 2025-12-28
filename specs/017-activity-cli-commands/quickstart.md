# Quickstart: Activity CLI Commands

**Date**: 2025-12-27
**Feature**: 017-activity-cli-commands

## Prerequisites

1. MCPProxy daemon running (`mcpproxy serve` or tray app)
2. At least one upstream server configured
3. Some tool calls made (to generate activity data)

## Quick Examples

### List Recent Activity

```bash
# Show last 50 activities
mcpproxy activity list

# Show last 10 tool calls only
mcpproxy activity list --type tool_call --limit 10

# Show errors from specific server
mcpproxy activity list --server github --status error
```

### Watch Live Activity

```bash
# Stream all activity in real-time
mcpproxy activity watch

# Watch only github server
mcpproxy activity watch --server github

# Press Ctrl+C to stop
```

### View Activity Details

```bash
# Get ID from list command, then show details
mcpproxy activity show 01JFXYZ123ABC

# Include full response body
mcpproxy activity show 01JFXYZ123ABC --include-response
```

### Get Summary Statistics

```bash
# 24-hour summary (default)
mcpproxy activity summary

# Weekly summary
mcpproxy activity summary --period 7d
```

### Export for Auditing

```bash
# Export to JSON Lines file
mcpproxy activity export --output activity.jsonl

# Export to CSV
mcpproxy activity export --format csv --output activity.csv

# Export specific date range
mcpproxy activity export \
  --start-time 2025-01-01T00:00:00Z \
  --end-time 2025-01-31T23:59:59Z \
  --output january.jsonl
```

## Output Formats

All commands support multiple output formats:

```bash
# Table (default, human-readable)
mcpproxy activity list

# JSON (for scripting/AI agents)
mcpproxy activity list -o json
mcpproxy activity list --json  # shorthand

# YAML
mcpproxy activity list -o yaml
```

## Common Workflows

### Debug a Failed Tool Call

```bash
# 1. Find recent errors
mcpproxy activity list --status error --limit 5

# 2. Get details for specific error
mcpproxy activity show <id-from-step-1>
```

### Monitor Agent Behavior

```bash
# Watch what an AI agent is doing in real-time
mcpproxy activity watch

# Or filter to specific session
mcpproxy activity list --session <session-id>
```

### Compliance Export

```bash
# Monthly audit export
mcpproxy activity export \
  --start-time "$(date -u -d '1 month ago' +%Y-%m-%dT00:00:00Z)" \
  --include-bodies \
  --output audit-$(date +%Y-%m).jsonl
```

## Tips

- Use `--json` output for piping to `jq` for complex filtering
- The watch command automatically reconnects on network issues
- Export streams directly to file without loading all records in memory
- Time filters use RFC3339 format: `2025-12-27T10:30:00Z`
