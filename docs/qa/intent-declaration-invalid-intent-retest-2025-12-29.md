# Intent Declaration Invalid Cases Retest (2025-12-29)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Scope
Retest invalid intent cases for `call_tool_read` validation using non-destructive obsidian-pilot read tools.

## Test Cases

### TC1: Intent mismatch (write intent with call_tool_read)
Command:
```bash
./mcpproxy call tool --tool-name=call_tool_read \
  --json_args='{"name":"obsidian-pilot:search_by_date_tool","args_json":"{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}","intent":{"operation_type":"write"}}'
```
Expected (specs/018-intent-declaration/quickstart.md):
- Error about intent mismatch with tool variant.
Actual:
```
Error: ... Intent mismatch: tool is call_tool_read but intent declares write
```
Result: Pass.

### TC2: Missing intent
Command:
```bash
./mcpproxy call tool --tool-name=call_tool_read \
  --json_args='{"name":"obsidian-pilot:search_by_date_tool","args_json":"{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}"}'
```
Expected:
- Error about missing intent parameter.
Actual:
```
Error: ... intent parameter is required for call_tool_read
```
Result: Pass.

### TC3: Invalid operation_type
Command:
```bash
./mcpproxy call tool --tool-name=call_tool_read \
  --json_args='{"name":"obsidian-pilot:search_by_date_tool","args_json":"{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}","intent":{"operation_type":"read_only"}}'
```
Expected:
- Error about invalid operation type.
Actual:
```
Error: ... Invalid intent.operation_type 'read_only': must be read, write, or destructive
```
Result: Pass.

### TC4: Invalid data_sensitivity
Command:
```bash
./mcpproxy call tool --tool-name=call_tool_read \
  --json_args='{"name":"obsidian-pilot:search_by_date_tool","args_json":"{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}","intent":{"operation_type":"read","data_sensitivity":"secret"}}'
```
Expected:
- Error about invalid data sensitivity.
Actual:
```
Error: ... Invalid intent.data_sensitivity 'secret': must be public, internal, private, or unknown
```
Result: Pass.

### TC5: intent.reason too long
Command:
```bash
json_args=$(python3 - <<'PY'
import json
reason = "a" * 1001
payload = {
    "name": "obsidian-pilot:search_by_date_tool",
    "args_json": "{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}",
    "intent": {"operation_type": "read", "reason": reason},
}
print(json.dumps(payload))
PY
)
./mcpproxy call tool --tool-name=call_tool_read --json_args="$json_args"
```
Expected:
- Error for reason length > 1000.
Actual:
```
Error: ... intent.reason exceeds maximum length of 1000 characters
```
Result: Pass.

### TC6: intent not an object
Command:
```bash
./mcpproxy call tool --tool-name=call_tool_read \
  --json_args='{"name":"obsidian-pilot:search_by_date_tool","args_json":"{\"date_type\":\"modified\",\"days_ago\":1,\"operator\":\"within\"}","intent":"oops"}'
```
Expected:
- Error stating intent must be an object.
Actual:
```
Error: ... Invalid intent parameter: intent must be an object
```
Result: Pass.

### TC7: Activity log for invalid intent
Command:
```bash
./mcpproxy activity list --status error --limit 5
```
Expected:
- Errors may appear in activity log if invalid intent attempts are recorded.
Actual:
```
No activities found
```
Result: No error records for invalid intent failures (attempts rejected before activity logging).

## Issues / Observations
1) Invalid intent attempts do not appear in activity logs; if auditing is desired, consider logging rejected attempts as error activity entries.
2) Tests require legacy `call tool` to craft invalid intent payloads; CLI variants do not allow mismatched intent by design.

## References
- specs/018-intent-declaration/quickstart.md
- specs/018-intent-declaration/data-model.md
