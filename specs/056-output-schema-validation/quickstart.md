# Quickstart: Output-Schema Validation

## Enable it

In `~/.mcpproxy/mcp_config.json`:

```json
{
  "output_validation": {
    "mode": "warn",
    "max_bytes": 5242880,
    "max_depth": 64,
    "missing_structured_content": "allow"
  }
}
```

- `warn` (default if the block is absent): violations are forwarded but logged as `policy_decision` activity records. Safe to enable on day one.
- `strict`: violations are blocked with an MCP error returned to the agent.
- `off`: validation disabled entirely.

## See what it caught

```bash
mcpproxy activity list --type policy_decision        # validation warnings + blocks
mcpproxy activity list --status blocked              # strict-mode blocks only
mcpproxy activity show <id>                           # tool, mode, violation detail
```

## How it behaves

| Tool declares outputSchema? | Response has structuredContent? | Conforms? | warn | strict |
|---|---|---|---|---|
| No | — | — | forward (no-op) | forward (no-op) |
| Yes | No (text only) | — | forward (no-op) | forward, unless `missing_structured_content=block` |
| Yes | Yes | Yes | forward unchanged | forward unchanged |
| Yes | Yes | No | forward + audit | **block** + audit |
| Yes | Yes (oversized / too deep) | — | forward + audit (guard) | **block** + audit (guard) |
| Yes | upstream IsError result | — | forward (skip) | forward (skip) |

On the conforming path the `structuredContent` delivered to the agent is **byte-for-byte identical** to what the upstream returned.

## Manual verification (curl, against a running proxy)

```bash
# 1. strict mode, call a tool whose upstream returns schema-violating structuredContent
curl -s -H "X-API-Key: $KEY" -X POST http://127.0.0.1:8080/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"stub:bad_output"}}}' | jq .
# -> expect an error result mentioning "output schema validation failed"

# 2. confirm the audit record
curl -s -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity?type=policy_decision&limit=5" | jq '.activities[] | {tool,decision,reason}'
```

## Tests

```bash
go test ./internal/outputvalidation/... -v -race      # unit: validator + guards
go test ./internal/server/ -run ContentForward -v     # unit: chokepoint hook
./scripts/test-api-e2e.sh                              # e2e: stub upstream w/ outputSchema, strict block + warn forward + activity record
```
