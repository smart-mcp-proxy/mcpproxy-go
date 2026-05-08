# Quickstart — Tray Refetch Elimination

## Build

```bash
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy -emit-executable -O \
  -o /tmp/MCPProxy-048 \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')
```

## Run XCTests

```bash
# From the MCPProxy package root
swift test --target MCPProxyTests --filter "SSEHandler|SafetyNet"
```

## Live verification — count `/api/v1/servers` GETs at idle

Reproduces the spec 047 harness with the swap-in bundle. Replace the tray binary inside a clone of `/Applications/MCPProxy.app`, launch, watch `http.log`.

```bash
pkill -f "MCPProxy-048" 2>/dev/null
rm -rf /tmp/MCPProxy-048.app
cp -R /Applications/MCPProxy.app /tmp/MCPProxy-048.app
cp /tmp/MCPProxy-048 /tmp/MCPProxy-048.app/Contents/MacOS/MCPProxy
codesign --force --deep --sign - /tmp/MCPProxy-048.app
open /tmp/MCPProxy-048.app

# Wait for steady state
until curl -sf -H "X-API-Key: $(jq -r .api_key ~/.mcpproxy/mcp_config.json)" http://127.0.0.1:8080/api/v1/status >/dev/null; do sleep 1; done
sleep 30   # let the tray finish startup fetches

# Sample 60 s of idle traffic
START=$(date +%s)
NOW=$START
while [ $((NOW-START)) -lt 60 ]; do sleep 5; NOW=$(date +%s); done

LOG=~/Library/Logs/mcpproxy/http.log
COUNT=$(grep '"path": "/api/v1/servers"' "$LOG" | awk -F'|' '{print $1}' | awk '{print $1}' | awk -v START=$(date -v-60S +%FT%T) '$0 >= START' | wc -l | tr -d ' ')
echo "GET /api/v1/servers in last 60 s of idle: $COUNT"
```

**Acceptance**: `$COUNT` should be `0` or `1` (the latter only if the test happened to span a 5-minute safety-net boundary). Before this PR: ~8.

## Live verification — UI reactivity unchanged

Use the same `mcpproxy-ui-test` MCP harness from spec 047:

```bash
BUNDLE=com.smartmcpproxy.mcpproxy
K=$(jq -r .api_key ~/.mcpproxy/mcp_config.json)

# Toggle a server, observe the tray reacting via SSE
curl -H "X-API-Key: $K" -X POST http://127.0.0.1:8080/api/v1/servers/context7/disable
sleep 2
echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_menu_items","arguments":{"path":["Servers (30)"]}}}' \
  | /tmp/mcpproxy-ui-test --bundle-id "$BUNDLE" 2>/dev/null \
  | grep '^{' | head -1 \
  | jq -r '.result.content[0].text' \
  | jq '.items[] | select(.title | test("context7"; "i")) | .children[0:3]'
```

Expected: `Disabled / Enable` within 2 s. Re-enable → `Connected (2 tools) / Disable` within 5 s.

Same as spec 047, but the verification now also asserts that no `/api/v1/servers` GET appeared in `http.log` during the toggle (only the SSE-driven update path is exercised).
