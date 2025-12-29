# Quickstart: Intent Declaration with Tool Split

**Feature**: 018-intent-declaration | **Date**: 2025-12-28

This guide shows how to use the new intent-based tool calling system in MCPProxy.

## Overview

MCPProxy now uses three tool variants instead of a single `call_tool`:

| Tool | Use For | Example Operations |
|------|---------|-------------------|
| `call_tool_read` | Read-only queries | List repos, get user info, search |
| `call_tool_write` | State modifications | Create issue, update file, send message |
| `call_tool_destructive` | Irreversible actions | Delete repo, revoke access, drop table |

## Two-Key Security Model

Every tool call requires matching intent in **two places**:

1. **Tool selection**: Choose the right variant (`call_tool_read/write/destructive`)
2. **Intent parameter**: Declare `intent.operation_type` matching the tool

```json
{
  "name": "call_tool_destructive",
  "arguments": {
    "name": "github:delete_repo",
    "args_json": "{\"repo\": \"test-repo\"}",
    "intent": {
      "operation_type": "destructive",
      "data_sensitivity": "private",
      "reason": "User requested cleanup"
    }
  }
}
```

## MCP Client Usage

### 1. Discover Tools with Annotations

```json
// Request
{
  "name": "retrieve_tools",
  "arguments": {
    "query": "delete repository",
    "limit": 5
  }
}

// Response
{
  "tools": [
    {
      "name": "github:delete_repo",
      "description": "Delete a GitHub repository",
      "inputSchema": { ... },
      "score": 0.95,
      "server": "github",
      "annotations": {
        "destructiveHint": true
      },
      "call_with": "call_tool_destructive"
    }
  ],
  "usage_instructions": "Use call_tool_read for read-only operations..."
}
```

### 2. Call Tool with Matching Intent

```json
// Use the recommended call_with variant
{
  "name": "call_tool_destructive",
  "arguments": {
    "name": "github:delete_repo",
    "args_json": "{\"repo\": \"test-repo\"}",
    "intent": {
      "operation_type": "destructive",
      "reason": "User confirmed deletion"
    }
  }
}
```

## CLI Usage

### Read Operations
```bash
# List repositories (read-only)
mcpproxy call tool-read github:list_repos --args '{}'

# Get user info
mcpproxy call tool-read github:get_user --args '{"username":"octocat"}'
```

### Write Operations
```bash
# Create issue
mcpproxy call tool-write github:create_issue \
  --args '{"repo":"test","title":"Bug report"}' \
  --reason "Filing bug from user feedback"

# Update file with sensitivity
mcpproxy call tool-write github:update_file \
  --args '{"path":"config.json","content":"..."}' \
  --sensitivity private
```

### Destructive Operations
```bash
# Delete repository (requires explicit destructive variant)
mcpproxy call tool-destructive github:delete_repo \
  --args '{"repo":"old-project"}' \
  --reason "Project deprecated, user confirmed"
```

## Activity Monitoring

### View Recent Activity with Intent
```bash
# List all activity
mcpproxy activity list

# Output includes Intent column:
# ID          TIME                    TYPE       SERVER    TOOL           INTENT    STATUS
# 01ABC...    2025-12-28 10:30:00    tool_call  github    list_repos     read      success
# 01DEF...    2025-12-28 10:31:00    tool_call  github    create_issue   write     success
# 01GHI...    2025-12-28 10:32:00    tool_call  github    delete_repo    destruct  success
```

### Filter by Intent Type
```bash
# Show only destructive operations (security audit)
mcpproxy activity list --intent-type destructive

# Show failed write operations
mcpproxy activity list --intent-type write --status error
```

### REST API Filtering
```bash
# Filter via API
curl -H "X-API-Key: $KEY" \
  "http://127.0.0.1:8080/api/v1/activity?intent_type=destructive"
```

## IDE Configuration

Configure your IDE (Cursor, Claude Desktop) for per-variant permissions:

```
MCPProxy Tools:
  [x] call_tool_read        → Auto-approve
  [ ] call_tool_write       → Ask each time
  [ ] call_tool_destructive → Always ask + confirm
```

This enables safe auto-approval of read operations while requiring human confirmation for modifications.

## Validation Errors

### Intent Mismatch
```
Error: Intent mismatch: tool is call_tool_read but intent declares write
```
**Fix**: Use `call_tool_write` with `intent.operation_type: "write"`

### Missing Intent
```
Error: intent parameter is required for call_tool_read
```
**Fix**: Add `intent` object with at least `operation_type`

### Server Annotation Mismatch
```
Error: Tool 'github:delete_repo' is marked destructive by server, use call_tool_destructive
```
**Fix**: Check `call_with` in retrieve_tools response and use recommended variant

### Legacy call_tool
```
Error: Tool 'call_tool' not found. Use call_tool_read, call_tool_write, or call_tool_destructive
```
**Fix**: The legacy `call_tool` has been removed. Use the new variants.

## Configuration

### Strict Server Validation (default)

```json
{
  "intent_declaration": {
    "strict_server_validation": true
  }
}
```

When `true` (default): Rejects calls where intent conflicts with server annotations.

When `false`: Logs warning but allows call (for trusted environments).

## Migration from call_tool

If you're upgrading from a version with the legacy `call_tool`:

1. **Update tool discovery**: Check `call_with` field in retrieve_tools response
2. **Split calls by operation type**:
   - Queries → `call_tool_read`
   - Creates/Updates → `call_tool_write`
   - Deletes → `call_tool_destructive`
3. **Add intent parameter**: Include `operation_type` matching the variant
4. **Update IDE permissions**: Configure per-variant approval rules

## Example: Complete Workflow

```bash
# 1. Search for tools
mcpproxy call tool github:retrieve_tools --args '{"query":"file operations"}'

# 2. Read a file (auto-approved in IDE)
mcpproxy call tool-read github:get_file --args '{"path":"README.md"}'

# 3. Update a file (IDE prompts for approval)
mcpproxy call tool-write github:update_file \
  --args '{"path":"README.md","content":"# Updated"}' \
  --reason "Updating documentation"

# 4. Delete a file (IDE shows warning + confirmation)
mcpproxy call tool-destructive github:delete_file \
  --args '{"path":"old-file.txt"}' \
  --reason "Cleaning up deprecated file"

# 5. Review activity
mcpproxy activity list --limit 5
```
