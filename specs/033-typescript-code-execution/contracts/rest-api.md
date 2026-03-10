# REST API Contract: Code Execution

**Feature**: 033-typescript-code-execution

## POST /api/v1/code/exec

### Updated Request Body

```json
{
  "code": "const greeting: string = 'hello'; ({ result: greeting })",
  "language": "typescript",
  "input": {},
  "options": {
    "timeout_ms": 120000,
    "max_tool_calls": 0,
    "allowed_servers": []
  }
}
```

| Field      | Type   | Required | Default        | Description                              |
|------------|--------|----------|----------------|------------------------------------------|
| code       | string | yes      | -              | Source code to execute                   |
| language   | string | no       | "javascript"   | Source language: "javascript", "typescript" |
| input      | object | no       | {}             | Input data                               |
| options    | object | no       | defaults       | Execution options                        |

### Response (unchanged)

Success:
```json
{
  "ok": true,
  "result": { "result": "hello" },
  "stats": {}
}
```

Transpilation error:
```json
{
  "ok": false,
  "error": {
    "code": "TRANSPILE_ERROR",
    "message": "TypeScript transpilation failed: [error details with line/column]"
  }
}
```

Invalid language:
```json
{
  "ok": false,
  "error": {
    "code": "INVALID_LANGUAGE",
    "message": "Unsupported language 'python'. Supported languages: javascript, typescript"
  }
}
```

### Backward Compatibility

- Omitting `language` field: request processed as JavaScript (existing behavior)
- All existing request/response formats unchanged
