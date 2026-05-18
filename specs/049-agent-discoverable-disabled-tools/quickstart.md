# Quickstart & Local Verification: Agent-Discoverable Disabled Tools

This doubles as the manual verification recipe (curl + live MCP connection)
required for sign-off. Run after implementation.

## 0. Build & stand up a throwaway instance

```bash
cd /Users/user/repos/mcpproxy-go
go build -o mcpproxy ./cmd/mcpproxy
pkill -f 'mcpproxy serve.*18049' 2>/dev/null; sleep 1
rm -rf /tmp/mcpproxy-049/{config.db,index.bleve,logs} 2>/dev/null
mkdir -p /tmp/mcpproxy-049
cat > /tmp/mcpproxy-049/mcp_config.json <<'EOF'
{
  "listen": "127.0.0.1:18049",
  "data_dir": "/tmp/mcpproxy-049",
  "api_key": "v049",
  "enable_web_ui": true,
  "enable_socket": false,
  "telemetry": {"enabled": false},
  "mcpServers": [
    { "name": "everything", "command": "npx",
      "args": ["-y","@modelcontextprotocol/server-everything"],
      "protocol": "stdio", "enabled": true, "skip_quarantine": true,
      "disabled_tools": ["printEnv"] }
  ]
}
EOF
./mcpproxy serve --config=/tmp/mcpproxy-049/mcp_config.json --listen=127.0.0.1:18049 --log-level=info >/tmp/mcpproxy-049/server.log 2>&1 &
until curl -sf -H "X-API-Key: v049" http://127.0.0.1:18049/api/v1/status >/dev/null; do sleep 1; done
```

`disabled_tools: ["printEnv"]` makes `everything:printEnv` config-denied. We also
user-disable a second tool below to exercise `disabled_by_user`.

## 1. curl — `upstream_servers` conditional counts (FR-010 / US3)

```bash
# User-disable one tool so a disabled_by_user count appears
curl -s -X POST -H "X-API-Key: v049" \
  http://127.0.0.1:18049/api/v1/servers/everything/tools/longRunningOperation/enabled \
  -d '{"enabled":false}'

# MCP: list servers — expect a `tools` block on "everything" with
# disabled_by_config>=1 and disabled_by_user>=1; servers with all-callable
# tools must have NO `tools` block.
curl -s -H "X-API-Key: v049" -H 'Content-Type: application/json' \
  http://127.0.0.1:18049/mcp -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
  "params":{"name":"upstream_servers","arguments":{"operation":"list"}}}' | jq '.result'
```

**Pass**: `everything` entry has `tools:{callable:N,disabled_by_config:>=1,disabled_by_user:>=1}`,
zero reasons omitted.

## 2. MCP — `retrieve_tools` default path UNCHANGED (FR-002 / SC-001)

```bash
curl -s -H "X-API-Key: v049" -H 'Content-Type: application/json' \
  http://127.0.0.1:18049/mcp -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
  "params":{"name":"retrieve_tools","arguments":{"query":"print environment"}}}' | jq '.result'
```

**Pass**: no `disabled` array, no `remediation` key; `printEnv` absent.
(Capture this output as the regression baseline.)

## 3. MCP — `retrieve_tools` with `include_disabled` (FR-001/3/4/5 / US1)

```bash
curl -s -H "X-API-Key: v049" -H 'Content-Type: application/json' \
  http://127.0.0.1:18049/mcp -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
  "params":{"name":"retrieve_tools","arguments":{"query":"print environment","include_disabled":true}}}' | jq '.result'
```

**Pass**: callable results first; then `disabled[]` containing
`{name:"printEnv",server:"everything",status:"disabled_by_config"}` and the
user-disabled tool with `status:"disabled_by_user"`; `remediation` map has only
those two keys; `disabled` length ≤ min(limit,10).

## 4. MCP — status-aware TOOL_BLOCKED + discovery pointer (FR-008 / US2)

```bash
curl -s -H "X-API-Key: v049" -H 'Content-Type: application/json' \
  http://127.0.0.1:18049/mcp -d '{"jsonrpc":"2.0","id":4,"method":"tools/call",
  "params":{"name":"call_tool_read","arguments":{"name":"everything:printEnv"}}}' | jq '.result'
```

**Pass**: error text says operator policy / NOT user-overridable / mcp_config.json,
and includes the "retrieve_tools with include_disabled:true" pointer. Distinct
from the user-disabled tool's message (call the user-disabled one to compare).

## 5. MCP — zero-callable-result nudge (FR-009 / US2)

```bash
# Query that only matches the config-denied tool
curl -s -H "X-API-Key: v049" -H 'Content-Type: application/json' \
  http://127.0.0.1:18049/mcp -d '{"jsonrpc":"2.0","id":5,"method":"tools/call",
  "params":{"name":"retrieve_tools","arguments":{"query":"printEnv"}}}' | jq -r '.result.content[0].text'
```

**Pass**: result text contains a one-line note "N relevant tools exist but are
locked; retry with include_disabled:true…" and NO locked entries inline.

## 6. Automated gate

```bash
go test ./internal/runtime/ -run 'ClassifyDisabledTool' -count=1
go test ./internal/server/ -run 'DisabledDiscovery|BlockedToolMessage' -count=1
./scripts/test-api-e2e.sh
./scripts/verify-oas-coverage.sh
```

## Teardown

```bash
pkill -f 'mcpproxy serve.*18049' 2>/dev/null
```
