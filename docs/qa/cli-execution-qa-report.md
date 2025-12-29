# CLI Execution QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Scope
Tool execution and orchestration: `call tool` and `code exec`.
Docs referenced: docs/cli/command-reference.md.

## Test Cases

### TC1: Call tool help
Command:
```bash
./mcpproxy call tool --help
```
Expected (docs/cli/command-reference.md):
- `call tool` accepts JSON input via `--input`.
Actual:
- CLI expects `--json_args` instead of `--input`.
- Output format options are `pretty` or `json`.
Result: Doc mismatch.

### TC2: Call built-in tool
Command:
```bash
./mcpproxy call tool --tool-name=list_registries --json_args='{}'
```
Expected:
- Successful tool call and structured output.
Actual:
- Command succeeds; output is a wrapper object with `content` array containing a `text` field that itself encodes JSON as a string.
Result: Pass (functionality), but output shape is nested and not directly machine-friendly.

UX notes:
- Consider a `--raw` or `--unwrap` flag to extract `content[0].text` when it is JSON.

### TC3: Code exec
Command:
```bash
./mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value":21}'
```
Expected (docs/cli/command-reference.md):
- Executes JS and returns JSON result.
Actual:
```
{ "ok": true, "result": { "result": 42 } }
```
Result: Pass.

## Issues / Observations
1) Doc mismatch for `call tool` input flag: docs specify `--input`, CLI uses `--json_args`.
2) `call tool` output is wrapped in a content envelope, which adds extra parsing for CLI users.

## References
- docs/cli/command-reference.md
