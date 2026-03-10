# CLI Contract: Code Execution

**Feature**: 033-typescript-code-execution

## mcpproxy code exec

### New Flag

```
--language string   Source code language: javascript, typescript (default "javascript")
```

### Usage Examples

```bash
# Execute TypeScript inline
mcpproxy code exec --language typescript --code "const x: number = 42; ({ result: x })"

# Execute TypeScript from file
mcpproxy code exec --language typescript --file script.ts --input='{"name": "world"}'

# Default behavior unchanged (JavaScript)
mcpproxy code exec --code "({ result: input.value * 2 })" --input='{"value": 21}'
```

### Output

Output format is unchanged. TypeScript transpilation errors are reported via the existing error format:

```json
{
  "ok": false,
  "error": {
    "code": "TRANSPILE_ERROR",
    "message": "TypeScript transpilation failed at line 3, column 5: ..."
  }
}
```

### Exit Codes

No changes to exit codes:
- `0`: Successful execution
- `1`: Execution failed (includes transpilation errors)
- `2`: Invalid arguments or configuration
