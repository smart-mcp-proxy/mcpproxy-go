# Quickstart: TypeScript Code Execution

**Feature**: 033-typescript-code-execution

## Development Setup

```bash
# 1. Switch to feature branch
git checkout 033-typescript-code-execution

# 2. Add esbuild dependency
go get github.com/evanw/esbuild

# 3. Verify build
go build ./cmd/mcpproxy

# 4. Run tests
go test ./internal/jsruntime/... -v -race
go test ./internal/server/... -v -race
go test ./internal/httpapi/... -v -race
```

## Implementation Order

1. **internal/jsruntime/typescript.go** - Transpilation layer (new file)
2. **internal/jsruntime/typescript_test.go** - Unit tests (new file)
3. **internal/jsruntime/errors.go** - Add `ErrorCodeTranspileError`
4. **internal/jsruntime/runtime.go** - Add `Language` to `ExecutionOptions`, call transpiler
5. **internal/jsruntime/runtime_test.go** - TypeScript execution tests
6. **internal/server/mcp.go** - Add `language` to tool schema
7. **internal/server/mcp_code_execution.go** - Parse language parameter
8. **internal/server/mcp_code_execution_test.go** - MCP integration tests
9. **internal/httpapi/code_exec.go** - Add `Language` to request struct
10. **internal/httpapi/code_exec_test.go** - REST API tests
11. **cmd/mcpproxy/code_cmd.go** - Add `--language` flag
12. **docs/** - Update documentation

## Quick Verification

```bash
# Build
go build -o mcpproxy ./cmd/mcpproxy

# Test TypeScript via CLI (requires code_execution enabled in config)
./mcpproxy code exec --language typescript --code "const x: number = 42; ({ result: x })"
# Expected: {"ok": true, "value": {"result": 42}}

# Test JavaScript still works (backward compat)
./mcpproxy code exec --code "({ result: input.value * 2 })" --input='{"value": 21}'
# Expected: {"ok": true, "value": {"result": 42}}
```

## Key Files to Read First

1. `internal/jsruntime/runtime.go` - Current execution flow (understand `Execute()` function)
2. `internal/server/mcp.go` lines 448-466 - Tool schema registration
3. `internal/server/mcp_code_execution.go` - MCP handler (language parameter parsing goes here)
4. `internal/httpapi/code_exec.go` - REST API handler
5. `cmd/mcpproxy/code_cmd.go` - CLI command
