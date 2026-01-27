---
id: intent-declaration
title: Intent Declaration
sidebar_label: Intent Declaration
sidebar_position: 8
description: Fine-grained permission control for AI tool calls with intent-based tool splitting
keywords: [intent, permissions, security, read, write, destructive, ide, cursor]
---

# Intent Declaration

MCPProxy provides **intent-based tool splitting** - a security feature that enables fine-grained permission control in IDEs like Cursor, Claude Desktop, and other AI coding assistants.

## The Problem

With a single `call_tool` command, IDEs can only offer all-or-nothing permission choices:

```
MCPProxy Tools:
  [x] call_tool → Auto-approve ALL operations? (risky!)
  [ ] call_tool → Ask every time? (annoying for reads)
```

This forces users to choose between security (asking for every operation) and convenience (auto-approving everything, including destructive operations).

## The Solution: Tool Splitting

MCPProxy splits tool execution into three variants based on operation type:

| Tool Variant | Purpose | Risk Level |
|-------------|---------|------------|
| `call_tool_read` | Read-only queries | Low |
| `call_tool_write` | Create/update operations | Medium |
| `call_tool_destructive` | Delete/irreversible operations | High |

This enables granular IDE permission settings:

```
MCPProxy Tools:
  [x] call_tool_read        → Auto-approve (safe reads)
  [ ] call_tool_write       → Ask each time
  [ ] call_tool_destructive → Always ask + confirm
```

## How It Works

The tool variant (`call_tool_read` / `write` / `destructive`) **automatically determines** the operation type. Intent metadata is provided as **flat string parameters** (not nested objects) for maximum compatibility with AI models:

```json
{
  "name": "call_tool_destructive",
  "arguments": {
    "name": "github:delete_repo",
    "args_json": "{\"repo\": \"test-repo\"}",
    "intent_data_sensitivity": "private",
    "intent_reason": "User requested repository cleanup"
  }
}
```

The `operation_type` is inferred from the tool variant - agents don't need to specify it explicitly.

### Validation Chain

1. Tool variant determines operation type (`call_tool_destructive` → "destructive")
2. Optional intent fields (`intent_data_sensitivity`, `intent_reason`) are validated if provided
3. Server annotation check → validate against `destructiveHint`/`readOnlyHint`

## Tool Variants

### call_tool_read

Execute read-only operations that don't modify state.

```json
{
  "name": "github:list_repos",
  "args_json": "{\"org\": \"myorg\"}"
}
```

Or with optional metadata:
```json
{
  "name": "github:list_repos",
  "args_json": "{\"org\": \"myorg\"}",
  "intent_reason": "Listing repositories for project analysis"
}
```

**Validation:**
- `operation_type` automatically inferred as "read"
- Rejected if server marks tool as `destructiveHint: true`

### call_tool_write

Execute state-modifying operations that create or update resources.

```json
{
  "name": "github:create_issue",
  "args_json": "{\"title\": \"Bug report\", \"body\": \"Details...\"}",
  "intent_reason": "Creating bug report per user request"
}
```

**Validation:**
- `operation_type` automatically inferred as "write"
- Rejected if server marks tool as `destructiveHint: true`

### call_tool_destructive

Execute destructive or irreversible operations.

```json
{
  "name": "github:delete_repo",
  "args_json": "{\"repo\": \"test-repo\"}",
  "intent": {
  "intent_data_sensitivity": "private",
  "intent_reason": "User confirmed deletion of test repository"
}
```

**Validation:**
- `operation_type` automatically inferred as "destructive"
- Most permissive - allowed regardless of server annotations

## Intent Parameters

Intent metadata is provided as **flat string parameters** for maximum compatibility with AI models (e.g., Gemini):

| Parameter | Required | Values | Description |
|-----------|----------|--------|-------------|
| `intent_data_sensitivity` | No | `public`, `internal`, `private`, `unknown` | Data classification for audit |
| `intent_reason` | No | String (max 1000 chars) | Explanation for audit trail |

The `operation_type` is automatically inferred from the tool variant and cannot be overridden.

### Examples

**Minimal (no intent needed):**
```json
{
  "name": "dataserver:read_data",
  "args_json": "{\"id\": \"123\"}"
}
```

**With optional metadata:**
```json
{
  "name": "dataserver:write_data",
  "args_json": "{\"id\": \"123\", \"value\": \"new\"}",
  "intent_data_sensitivity": "private",
  "intent_reason": "Updating user profile with personal information"
}
```

## Server Annotation Validation

MCPProxy validates agent intent against server-provided annotations:

| Tool Variant | Server Annotation | Result |
|--------------|-------------------|--------|
| `call_tool_read` | `readOnlyHint: true` | ALLOW |
| `call_tool_read` | `destructiveHint: true` | **REJECT** |
| `call_tool_read` | No annotation | ALLOW (trust agent) |
| `call_tool_write` | `readOnlyHint: true` | WARN + ALLOW |
| `call_tool_write` | `destructiveHint: true` | **REJECT** |
| `call_tool_write` | No annotation | ALLOW |
| `call_tool_destructive` | Any | ALLOW (most permissive) |

### Strict Mode

By default, strict validation is enabled. Configure via `mcp_config.json`:

```json
{
  "intent_declaration": {
    "strict_server_validation": true
  }
}
```

When `strict_server_validation: false`, server annotation mismatches log a warning but allow the call.

## Tool Discovery

The `retrieve_tools` response includes annotations and guidance:

```json
{
  "tools": [
    {
      "name": "github:delete_repo",
      "description": "Delete a GitHub repository",
      "inputSchema": {...},
      "annotations": {
        "destructiveHint": true,
        "readOnlyHint": false
      },
      "call_with": "call_tool_destructive"
    },
    {
      "name": "github:list_repos",
      "description": "List repositories",
      "inputSchema": {...},
      "annotations": {
        "readOnlyHint": true
      },
      "call_with": "call_tool_read"
    }
  ]
}
```

The `call_with` field recommends which tool variant to use based on server annotations.

## CLI Commands

Execute tools via CLI with explicit intent:

```bash
# Read operation
mcpproxy call tool-read github:list_repos --args '{"org": "myorg"}'

# Write operation
mcpproxy call tool-write github:create_issue \
  --args '{"title": "Bug", "body": "Details"}' \
  --reason "Creating bug report"

# Destructive operation
mcpproxy call tool-destructive github:delete_repo \
  --args '{"repo": "test"}' \
  --sensitivity private \
  --reason "User confirmed deletion"
```

### CLI Flags

| Flag | Description |
|------|-------------|
| `--args` | Tool arguments as JSON |
| `--reason` | Optional reason for audit trail |
| `--sensitivity` | Data sensitivity: `public`, `internal`, `private`, `unknown` |

## Activity Log Integration

Intent is recorded in every activity log entry:

```bash
mcpproxy activity list
```

```
ID              TIME                 SERVER      TOOL          INTENT  STATUS  DURATION
01JG2...        2025-01-15 10:30:00  github      create_issue  write   success 245ms
01JG2...        2025-01-15 10:29:45  github      list_repos    read    success 123ms
01JG2...        2025-01-15 10:29:30  github      delete_repo   destr   success 567ms
```

Filter by intent type:

```bash
# Show only destructive operations
mcpproxy activity list --intent-type destructive

# REST API
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity?intent_type=destructive"
```

## Error Messages

Clear error messages help agents self-correct:

**Server annotation conflict:**
```
Tool 'github:delete_repo' is marked destructive by server.
Use call_tool_destructive instead of call_tool_read.
```

**Invalid data sensitivity:**
```
Invalid intent.data_sensitivity 'secret': must be public, internal, private, or unknown
```

**Reason too long:**
```
intent.reason exceeds maximum length of 1000 characters
```

## IDE Configuration Examples

### Cursor

In Cursor settings, configure MCP tool permissions:

```
MCPProxy Tools:
  call_tool_read        [Auto-approve]
  call_tool_write       [Ask each time]
  call_tool_destructive [Always ask]
```

### Claude Desktop

In `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "mcpproxy": {
      "permissions": {
        "call_tool_read": "auto",
        "call_tool_write": "ask",
        "call_tool_destructive": "always_ask"
      }
    }
  }
}
```

## Best Practices

1. **Use the right variant**: Match your intent to the appropriate tool variant
2. **Provide reasons**: Help audit trails with clear explanations
3. **Classify sensitivity**: Mark private data operations appropriately
4. **Trust retrieve_tools**: Use the `call_with` recommendation
5. **Configure IDE permissions**: Enable auto-approve for reads, require confirmation for destructive

## Migration from call_tool

The legacy `call_tool` has been removed. Update your integrations:

**Before:**
```json
{
  "name": "call_tool",
  "arguments": {
    "name": "github:create_issue",
    "args_json": "{...}"
  }
}
```

**After:**
```json
{
  "name": "call_tool_write",
  "arguments": {
    "name": "github:create_issue",
    "args_json": "{...}"
  }
}
```

Intent parameters are optional - `operation_type` is automatically inferred from the tool variant. You can add `intent_data_sensitivity` and `intent_reason` for audit purposes.

:::tip Choosing the Right Variant
When unsure, use `call_tool_destructive` - it's the most permissive and will always succeed validation. Then refine based on `retrieve_tools` guidance.
:::
