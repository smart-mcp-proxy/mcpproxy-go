# Data Model: TypeScript Code Execution Support

**Feature**: 033-typescript-code-execution
**Date**: 2026-03-10

## Entities

### ExecutionOptions (modified)

The existing `ExecutionOptions` struct in `internal/jsruntime/runtime.go` gains a new `Language` field.

| Field          | Type     | Default        | Description                                      |
|----------------|----------|----------------|--------------------------------------------------|
| Input          | map      | {}             | Input data accessible as global `input` variable |
| TimeoutMs      | int      | 120000         | Execution timeout in milliseconds                |
| MaxToolCalls   | int      | 0 (unlimited)  | Maximum call_tool() invocations                  |
| AllowedServers | []string | [] (all)       | Whitelist of allowed server names                |
| ExecutionID    | string   | auto-generated | Unique execution ID for logging                  |
| **Language**   | string   | "javascript"   | Source language: "javascript" or "typescript"     |

### TranspileResult (new)

Represents the output of TypeScript-to-JavaScript transpilation.

| Field   | Type   | Description                                                 |
|---------|--------|-------------------------------------------------------------|
| Code    | string | Transpiled JavaScript code (empty on error)                 |
| Errors  | []TranspileError | List of transpilation errors (empty on success)   |

### TranspileError (new)

Represents a single transpilation error with source location.

| Field   | Type   | Description                                      |
|---------|--------|--------------------------------------------------|
| Message | string | Human-readable error message                     |
| Line    | int    | 1-based line number in original TypeScript source |
| Column  | int    | 0-based column number in original TypeScript source |

### CodeExecRequest (modified - REST API)

The existing `CodeExecRequest` struct in `internal/httpapi/code_exec.go` gains a new `Language` field.

| Field    | Type            | Default      | Description                          |
|----------|-----------------|--------------|--------------------------------------|
| Code     | string          | required     | Source code to execute               |
| Input    | map             | {}           | Input data                           |
| Options  | CodeExecOptions | defaults     | Execution options                    |
| **Language** | string      | "javascript" | Source language: "javascript" or "typescript" |

## State Transitions

N/A - This feature is stateless. Each request is independently processed:

1. Receive code + language parameter
2. If language == "typescript": transpile to JavaScript
3. Execute JavaScript in goja sandbox
4. Return result

No persistent state changes, no database modifications, no event emissions.

## Validation Rules

- `language` must be one of: `"javascript"`, `"typescript"` (case-sensitive)
- Invalid `language` values return an error with code `INVALID_ARGS` listing valid options
- Empty `language` defaults to `"javascript"`
- When `language` is `"typescript"`, transpilation errors produce `TRANSPILE_ERROR` error code
