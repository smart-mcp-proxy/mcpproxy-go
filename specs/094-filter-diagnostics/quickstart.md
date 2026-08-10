# Quickstart: verifying retrieve_tools filter diagnostics locally

## Unit/handler tests (primary loop)

```bash
go test ./internal/server/ -run 'FilterDiagnostics|Annotation' -v
go test -race ./internal/server/
```

## Live reproduction of the field scenario (US1/AS1)

Use an isolated instance (never the real ~/.mcpproxy; see reference_isolated_dev_instance):

Use the repo's `tests/echo-rugpull-server` fixture — its `echo`/`get_time` tools publish NO annotations block (verified in `tests/echo-rugpull-server/index.js`), exactly the field scenario. Do NOT use `cmd/mcpfixture` (explicitly annotated) or public servers like Everything (its echo tool now declares `readOnlyHint: true`).

```bash
mkdir -p /tmp/mcpproxy-094/{data,logs}
go build -o /tmp/mcpproxy-094/mcpproxy ./cmd/mcpproxy
(cd tests/echo-rugpull-server && npm install --silent)   # if node_modules absent
cat > /tmp/mcpproxy-094/config.json <<EOF
{
  "listen": "127.0.0.1:18972",
  "enable_socket": false,
  "mcpServers": [
    {
      "name": "echo-fixture",
      "protocol": "stdio",
      "command": "node",
      "args": ["$PWD/tests/echo-rugpull-server/index.js"],
      "enabled": true,
      "quarantined": false
    }
  ]
}
EOF
/tmp/mcpproxy-094/mcpproxy serve --config /tmp/mcpproxy-094/config.json \
  --data-dir /tmp/mcpproxy-094/data --log-dir /tmp/mcpproxy-094/logs
```

Then via any MCP client (or `curl` to `/mcp` with a JSON-RPC `tools/call`):

1. `retrieve_tools {query: "echo", read_only_only: true}` → expect `total: 0` (or reduced), and `filter_diagnostics` with `read_only_only.missing_annotation >= 1` and an annotations suggestion.
2. Same query without the filter → tools return, NO `filter_diagnostics` key.
3. `retrieve_tools {query: "echo"}` (no filters) → byte-identical shape to pre-feature (no new key).
4. Repeat (1) with `detail: "compact"` → identical `filter_diagnostics` content.

Kill by PID and `rm -rf /tmp/mcpproxy-094` when done.

## What reviewers should check

- `shouldExclude` still delegates to `excludeReason` (parity property test green).
- No `filter_diagnostics` key in any response where filters omitted nothing (SC-002 golden).
- SC-003 size test serializes the maximal fixture and asserts ≤500 bytes + charset.
- All three `retrieve_tools` registrations share `retrieveToolsAnnotationFilterOptions()`.
