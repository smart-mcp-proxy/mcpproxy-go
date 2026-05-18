# Quickstart — Verify Global Tools Page

Prereqs: built `mcpproxy` binary, Node/npm, `jq`, `curl`.

## 1. Stand up a throwaway instance with a couple of servers

```bash
pkill -f 'mcpproxy serve.*18081' 2>/dev/null; sleep 1
rm -rf /tmp/mcpproxy-uitest/{config.db,index.bleve,logs} 2>/dev/null
mkdir -p /tmp/mcpproxy-uitest
cat > /tmp/mcpproxy-uitest/mcp_config.json <<'EOF'
{ "listen": "127.0.0.1:18081", "data_dir": "/tmp/mcpproxy-uitest", "api_key": "uitest",
  "enable_web_ui": true, "enable_socket": true, "telemetry": {"enabled": false},
  "mcpServers": [
    {"name":"everything","command":"npx","args":["-y","@modelcontextprotocol/server-everything"],"protocol":"stdio","enabled":true},
    {"name":"memory","command":"npx","args":["-y","@modelcontextprotocol/server-memory"],"protocol":"stdio","enabled":false}
  ] }
EOF
./mcpproxy serve --config=/tmp/mcpproxy-uitest/mcp_config.json --listen=127.0.0.1:18081 --log-level=info > /tmp/mcpproxy-uitest/server.log 2>&1 &
until curl -sf -H "X-API-Key: uitest" http://127.0.0.1:18081/api/v1/status >/dev/null; do sleep 1; done
```

## 2. API: consolidated listing (curl)

```bash
# Full payload shape + stats
curl -s -H "X-API-Key: uitest" http://127.0.0.1:18081/api/v1/tools | jq '.data.stats, (.data.tools|length), .data.partial'

# A disabled server's tools must still appear
curl -s -H "X-API-Key: uitest" http://127.0.0.1:18081/api/v1/tools \
  | jq '.data.tools | map(select(.server_name=="memory")) | length'   # > 0 expected (server disabled, tools still listed)

# Usage fields present (0 / null until tools are called)
curl -s -H "X-API-Key: uitest" http://127.0.0.1:18081/api/v1/tools | jq '.data.tools[0] | {name,server_name,usage,last_used,disabled,config_denied,approval_status}'
```

Expected: `stats.total == tools|length`; `memory` (disabled server) tools present;
`partial:false`; usage `0` and `last_used` absent before any tool call.

## 3. CLI parity

```bash
mcpproxy tools list                                  # global, all servers (no --server)
mcpproxy tools list -o json | jq '.[0]'
mcpproxy tools list --server everything              # still works (scoped)
mcpproxy tools list --status disabled                # only disabled/config-denied
mcpproxy tools disable everything:echo memory:foo    # batch; per-target summary
echo "exit: $?"                                      # non-zero if any target failed
mcpproxy tools list --server everything | grep echo  # echo now shows disabled
mcpproxy tools enable everything:echo
```

## 4. Web UI

```bash
open "http://127.0.0.1:18081/ui/?apikey=uitest"      # → sidebar WORKSPACE → "Tools" (badge shows total)
```

Check: summary cards (Total/Enabled/Disabled/Pending) match; type in search box →
list narrows incl. disabled tools; server/status/risk/approval dropdowns combine;
click column header → sort toggles; select rows → batch bar → Disable selected →
states + cards update; row click → schema modal. Empty state when `mcpServers: []`.

## 5. Automated

```bash
./scripts/test-api-e2e.sh        # includes GET /api/v1/tools shape+stats assertion
# Playwright sweep → specs/050-global-tools-page/verification/report.html
```
