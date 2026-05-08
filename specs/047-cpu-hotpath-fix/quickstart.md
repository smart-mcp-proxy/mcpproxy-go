# Quickstart — CPU Hot-Path Fix

How to build, exercise, and verify this feature locally. Mirrors the workflow used to capture the original profile data.

## Build

```bash
# Personal edition (default)
make build

# Or just the core binary
go build -o ./mcpproxy ./cmd/mcpproxy

# macOS tray (Swift)
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy -emit-executable -O \
  -o /tmp/MCPProxy-new \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')
cp /tmp/MCPProxy-new /tmp/MCPProxy.app/Contents/MacOS/MCPProxy   # if testing in a dev bundle
```

## Run unit tests

```bash
# All Go unit tests, race detector on
go test -race ./internal/...

# Targeted: scanner cache + event bus
go test -race ./internal/security/scanner/... ./internal/runtime/... -v
```

## Reproduce the original scenario

The original measurement used the user's `~/.mcpproxy/mcp_config.json` (30 servers, 1.06 GB BBolt) with the macOS tray driving the core.

```bash
pkill -f 'mcpproxy serve' 2>/dev/null
osascript -e 'quit app "MCPProxy"' 2>/dev/null

# Launch tray pointing at the freshly built binary
open -a /Applications/MCPProxy.app --env MCPPROXY_CORE_PATH=$(pwd)/mcpproxy

# Confirm the tray launched the core
pgrep -lf "mcpproxy serve"
```

## Capture pprof verification

```bash
K=$(jq -r .api_key ~/.mcpproxy/mcp_config.json)

# 60s CPU profile while tray polls
curl -H "X-API-Key: $K" -o /tmp/cpu_post.pb.gz "http://127.0.0.1:8080/debug/pprof/profile?seconds=60"
go tool pprof -top -cum -nodecount=20 /tmp/cpu_post.pb.gz

# Cumulative cputime delta (ground truth)
PID=$(pgrep -f "mcpproxy serve")
T1=$(ps -p $PID -o time= | tr -d ' '); sleep 60
T2=$(ps -p $PID -o time= | tr -d ' ')
echo "CPU time over 60s wall: $T1 -> $T2"
```

Expected after fix:
- `bbolt.(*DB).View` cum < 5% (was 56%).
- `encoding/json.checkValid` flat < 5% (was 13%).
- CPU-time delta < 2 s over 60 s wall (was ~19 s).

## Verify SSE end-to-end

```bash
K=$(jq -r .api_key ~/.mcpproxy/mcp_config.json)

# Watch SSE — confirm `servers.changed` events carry the `servers` array
curl -N -H "X-API-Key: $K" "http://127.0.0.1:8080/events" | grep -A1 "event: servers.changed"

# Trigger a state change to provoke an event
curl -H "X-API-Key: $K" -X POST "http://127.0.0.1:8080/api/v1/servers/<some-server>/disable"
curl -H "X-API-Key: $K" -X POST "http://127.0.0.1:8080/api/v1/servers/<some-server>/enable"
```

The event payload should include a non-empty `servers` array; the tray and Web UI should update state without making any `GET /api/v1/servers` request (visible in the access log / Network tab).

## Web UI Playwright verification

Follows the pattern documented in CLAUDE.md ("Verifying Web UI changes").

```bash
mkdir -p /tmp/uitest-047 && cd /tmp/uitest-047
ln -sfn /Users/user/repos/mcpproxy-go/e2e/playwright/node_modules ./node_modules

# Pinned Chromium 1217 in playwright.config.ts (see CLAUDE.md)
./node_modules/.bin/playwright test --reporter=list
```

Spec asserts: after a server state toggle, the dashboard updates within 100 ms with no fetch call to `/api/v1/servers`.

## macOS tray verification (mcpproxy-ui-test)

```bash
# Build the UI test tool (one-time)
cd native/macos/MCPProxyUITest
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -O -o /tmp/mcpproxy-ui-test Sources/main.swift

# Use mcpproxy-ui-test MCP tools (configured in .claude/settings.json) to:
#  - screenshot_status_bar_menu  (capture tray menu open state)
#  - list_menu_items             (assert server list reflects current state)
#  - click_menu_item             (toggle a server)
#  - screenshot_window           (capture dashboard)
```
