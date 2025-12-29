# Research: Intent Declaration with Tool Split

**Feature**: 018-intent-declaration | **Date**: 2025-12-28

## 1. MCP Tool Handler Architecture

### Decision
Extend existing `handleCallTool()` pattern to create three new handlers: `handleCallToolRead()`, `handleCallToolWrite()`, `handleCallToolDestructive()`.

### Rationale
- Existing `handleCallTool()` in `internal/server/mcp.go` (line 762) provides proven pattern
- Tool registration via `proxy.registerTools()` (line 219) supports adding multiple tools
- Built-in proxy tools use `proxyTools` map for routing (line 798)
- Parameter extraction via `request.RequireString()` / `request.GetString()` is well-established

### Alternatives Considered
1. **Single handler with variant parameter**: Rejected - defeats IDE permission model (single tool name)
2. **Middleware validation**: Rejected - adds complexity without benefit
3. **Decorator pattern**: Rejected - Go idiom favors explicit handlers

### Key Files
- `internal/server/mcp.go:762` - Current handleCallTool implementation
- `internal/server/mcp.go:219` - Tool registration pattern

---

## 2. Intent Validation Strategy

### Decision
Implement two-phase validation:
1. **Phase 1 (Two-Key Match)**: Validate `intent.operation_type` matches tool variant
2. **Phase 2 (Server Annotation)**: Validate against server's `destructiveHint`/`readOnlyHint`

### Rationale
- Two-key model ensures agent explicitly declares intent twice (tool choice + parameter)
- Server annotation validation prevents intent spoofing via auto-approved channels
- Configurable strict mode allows flexibility for trusted environments

### Validation Matrix (from spec)
| Tool Variant | intent.operation_type | Server Annotation | Result |
|--------------|----------------------|-------------------|--------|
| call_tool_read | "read" | destructiveHint=true | REJECT |
| call_tool_read | "read" | readOnlyHint=true | ALLOW |
| call_tool_read | "read" | no annotation | ALLOW |
| call_tool_read | "write" | any | REJECT (mismatch) |
| call_tool_write | "write" | destructiveHint=true | REJECT |
| call_tool_write | "write" | readOnlyHint=true | WARN + ALLOW |
| call_tool_write | "write" | no annotation | ALLOW |
| call_tool_destructive | "destructive" | any | ALLOW (most permissive) |
| any | missing | any | REJECT |

### Key Files
- `internal/config/config.go:358-365` - Existing ToolAnnotations struct
- `internal/contracts/converters.go:493-511` - Annotation extraction

---

## 3. Activity Log Integration

### Decision
Store intent in existing `ActivityRecord.Metadata` field as structured object.

### Rationale
- `Metadata map[string]interface{}` already exists in ActivityRecord (spec 016)
- No schema migration needed - just populate the field
- Consistent with extensibility pattern already in place

### Storage Format
```go
Metadata: map[string]interface{}{
    "intent": map[string]interface{}{
        "operation_type":   "read|write|destructive",
        "data_sensitivity": "public|internal|private|unknown",  // optional
        "reason":           "user provided reason",              // optional
    },
    "tool_variant": "call_tool_read|call_tool_write|call_tool_destructive",
}
```

### Key Files
- `internal/storage/activity_models.go` - ActivityRecord struct
- `internal/server/mcp.go:243-257` - Activity event emission

---

## 4. CLI Command Structure

### Decision
Add three subcommands under `mcpproxy call`: `tool-read`, `tool-write`, `tool-destructive`.

### Rationale
- Matches existing `call tool` pattern in `cmd/mcpproxy/call_cmd.go`
- Cobra subcommand structure is idiomatic
- Auto-populates `intent.operation_type` based on command used

### Command Signatures
```bash
mcpproxy call tool-read <server:tool> [--args JSON] [--reason TEXT] [--sensitivity LEVEL]
mcpproxy call tool-write <server:tool> [--args JSON] [--reason TEXT] [--sensitivity LEVEL]
mcpproxy call tool-destructive <server:tool> [--args JSON] [--reason TEXT] [--sensitivity LEVEL]
```

### Alternatives Considered
1. **Single command with --intent flag**: Rejected - doesn't match MCP tool split
2. **Separate mcpproxy read/write/delete commands**: Rejected - breaks existing structure

### Key Files
- `cmd/mcpproxy/call_cmd.go` - Existing call command

---

## 5. retrieve_tools Enhancement

### Decision
Add `annotations` and `call_with` fields to tool response, plus `usage_instructions` in summary.

### Rationale
- Agents need annotation visibility to select correct tool variant
- `call_with` recommendation simplifies agent logic
- `usage_instructions` provides context for new tools

### Response Format
```json
{
  "tools": [
    {
      "name": "github:delete_repo",
      "description": "Delete a GitHub repository",
      "inputSchema": {...},
      "score": 0.95,
      "server": "github",
      "annotations": {
        "readOnlyHint": false,
        "destructiveHint": true
      },
      "call_with": "call_tool_destructive"
    }
  ],
  "usage_instructions": "Use call_tool_read for read-only operations, call_tool_write for modifications, call_tool_destructive for deletions. Intent must match tool variant."
}
```

### call_with Logic
1. If `destructiveHint=true` → `call_tool_destructive`
2. If `readOnlyHint=true` → `call_tool_read`
3. Otherwise → `call_tool_write` (safe default)

### Key Files
- `internal/server/mcp.go:680-720` - Tool list response construction

---

## 6. Configuration Schema

### Decision
Add `intent_declaration` configuration block with `strict_server_validation` boolean.

### Rationale
- Matches existing config patterns in `internal/config/config.go`
- Single toggle for strict/permissive mode
- Default to strict (true) for security by default

### Config Format
```json
{
  "intent_declaration": {
    "strict_server_validation": true
  }
}
```

### Behavior
- `true` (default): Reject calls where intent doesn't match server annotation
- `false`: Log warning but allow call (for trusted environments)

### Key Files
- `internal/config/config.go` - Configuration structs

---

## 7. Error Message Design

### Decision
Use clear, actionable error messages that explain what went wrong and how to fix it.

### Error Templates
| Scenario | Error Message |
|----------|---------------|
| Intent mismatch | `Intent mismatch: tool is call_tool_read but intent declares write` |
| Missing intent | `intent parameter is required for call_tool_read` |
| Missing operation_type | `intent.operation_type is required` |
| Server annotation mismatch | `Tool 'github:delete_repo' is marked destructive by server, use call_tool_destructive` |
| Unknown operation_type | `Invalid intent.operation_type 'unknown': must be read, write, or destructive` |

### Rationale
- Error messages explain the mismatch clearly
- Include the expected correction
- Match existing error patterns in codebase

---

## 8. Breaking Change Strategy

### Decision
Remove legacy `call_tool` entirely in this release. No deprecation period.

### Rationale
- Spec explicitly requires clean break (FR-004)
- Simplifies tool surface
- Eliminates ambiguity
- Forces explicit intent declaration

### Migration Path
1. Agents using `call_tool` will receive "tool not found" error
2. Error message should suggest using new variants
3. Documentation clearly states breaking change

### Suggested Error
```
Tool 'call_tool' not found. Use call_tool_read, call_tool_write, or call_tool_destructive
with matching intent.operation_type. See retrieve_tools for annotations and recommendations.
```

---

## 9. Performance Considerations

### Decision
Intent validation adds minimal overhead (<1ms) through in-memory operations only.

### Analysis
- **Two-key validation**: String comparison, O(1)
- **Annotation lookup**: Already cached in StateView, O(1)
- **Config check**: Single boolean read, O(1)
- **No I/O**: All validation happens before upstream call

### Benchmark Target
- Total validation overhead: <10ms (spec SC-007)
- Expected actual: <1ms

---

## 10. REST API Filter Extension

### Decision
Add `intent_type` query parameter to `GET /api/v1/activity`.

### Rationale
- Matches existing filter patterns (type, status, server, tool)
- Enables security audits of destructive operations
- Simple implementation via existing filter framework

### API Usage
```
GET /api/v1/activity?intent_type=destructive
GET /api/v1/activity?intent_type=read&status=error
```

### Key Files
- `internal/httpapi/server.go` - Activity endpoint handler
- `oas/swagger.yaml` - OpenAPI spec
