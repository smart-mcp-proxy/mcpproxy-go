# MCP Tool Schema Contract: code_execution

**Feature**: 033-typescript-code-execution

## Updated Tool Schema

The `code_execution` MCP tool gains a new `language` parameter:

```json
{
  "name": "code_execution",
  "description": "Execute JavaScript or TypeScript code that orchestrates multiple upstream MCP tools...",
  "inputSchema": {
    "type": "object",
    "required": ["code"],
    "properties": {
      "code": {
        "type": "string",
        "description": "JavaScript or TypeScript source code to execute..."
      },
      "language": {
        "type": "string",
        "description": "Source code language. When set to 'typescript', the code is automatically transpiled to JavaScript before execution. Type annotations are stripped, enums and namespaces are converted to JavaScript equivalents.",
        "enum": ["javascript", "typescript"],
        "default": "javascript"
      },
      "input": {
        "type": "object",
        "description": "Input data accessible as global `input` variable"
      },
      "options": {
        "type": "object",
        "description": "Execution options: timeout_ms, max_tool_calls, allowed_servers"
      }
    }
  }
}
```

## New Error Code

```json
{
  "code": "TRANSPILE_ERROR",
  "message": "TypeScript transpilation failed at line 5, column 10: Type 'string' is not assignable...",
  "stack": ""
}
```

## Backward Compatibility

- Omitting `language` parameter: code executes as JavaScript (existing behavior)
- Setting `language: "javascript"`: identical to omitting it
- All existing tool call arguments continue to work unchanged
