# Quickstart: Schema-Deferred Direct Mode

Build, enable, and verify the deferred direct surface locally. Use an isolated
instance (high port + scratch `--data-dir` and `--config`) — never a shared core.

## 1. Build

```bash
go build -o mcpproxy ./cmd/mcpproxy
go build -tags server -o /dev/null ./cmd/mcpproxy   # server edition still compiles
```

## 2. Enable deferral (opt-in)

`~/.mcpproxy-dev/config.json` (scratch dir):

```json
{
  "listen": "127.0.0.1:18102",
  "direct_tool_response_mode": "deferred",
  "mcpServers": [ { "name": "everything", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-everything"], "protocol": "stdio", "enabled": true } ]
}
```

Equivalents: `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE=deferred` or
`--direct-tool-response-mode=deferred`. Default (`full`) keeps pre-feature bytes.

```bash
./mcpproxy serve --config ~/.mcpproxy-dev/config.json --data-dir ~/.mcpproxy-dev/data
```

## 3. Inspect the deferred listing

```bash
curl -s http://127.0.0.1:18102/mcp/all -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools[] | {name, inputSchema, description}'
```

Expect: every tool listed; `inputSchema == {"type":"object"}`; descriptions ending
with a `(param*:type, …)` signature; `describe_tool` present in the list; the
`initialize` result carrying the convention instructions.

## 4. Recover a schema and trigger self-healing

```bash
# Full schema for a lossy tool (both id forms work):
... method tools/call, name describe_tool, args {"tool_ids":["everything__sampleLLM"]}

# Wrong-guess call → invalid_params error embedding the full input schema + hint:
... method tools/call, name everything__echo, args {}
```

## 5. Verify hot-reload flip (FR-014)

Keep an MCP session open on `/mcp/all`, edit `direct_tool_response_mode` in the
config file, and observe `notifications/tools/list_changed` followed by the next
`tools/list` reflecting the new mode with an identical tool set. An unrelated
config edit must produce no notification.

## 6. Test gates

```bash
go test -race ./internal/toolsig/... ./internal/config/... ./internal/runtime/...
# internal/server locally: use the CI skip regex (unit-tests.yml) — see memory note.
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
```

Golden rules: only `toolslist_goldens/{default_server,retrieve_tools_mode}.json`
are regenerated (once, FR-009 prose); `pre099/`, `code_execution_mode.json`,
`tools_list_prefeature.golden.json`, **both** `retrieve_full_default.golden.json`
and `retrieve_full_stats.golden.json` (one test pins both), and
`describe_plain_corpus/pre099.json` must pass **unregenerated** — the
describe-corpus prose movement is absorbed by adding named substitutions to
`describePlainDelta`, not by rewriting its golden. The new
`direct_mode_builtins.json` gate pins the direct built-ins in both modes and is a
standalone test, not an entry in `toolsListGoldenSurfaces`.

Expected red tests during implementation (enumerated, not regressions):
`TestDescribeTool_RegisteredInRetrieveToolsModeOnly`'s `direct routing mode`
subtest, which asserts the v1 decision this feature reverses.
