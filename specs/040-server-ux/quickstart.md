# Quickstart: Add/Edit Server UX Improvements

## Prerequisites

- macOS 13+ with Xcode 15+
- Go 1.24+ toolchain
- Running MCPProxy instance (`mcpproxy serve`)
- mcpproxy-ui-test binary at `/tmp/mcpproxy-ui-test`

## Build & Test

### Backend (PATCH endpoint)

```bash
# Run PATCH endpoint tests
go test -race ./internal/httpapi/ -run TestPatchServer -v

# Build core with changes
go build -o /tmp/MCPProxy.app/Contents/Resources/bin/mcpproxy ./cmd/mcpproxy

# Verify endpoint
API_KEY=$(jq -r '.api_key' ~/.mcpproxy/mcp_config.json)
curl -s -X PATCH -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}' \
  http://127.0.0.1:8080/api/v1/servers/everything-server
```

### Swift Tray App

```bash
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy -emit-executable -O \
  -o /tmp/MCPProxy-new \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')

# Deploy
cp /tmp/MCPProxy-new /tmp/MCPProxy.app/Contents/MacOS/MCPProxy
```

### UI Verification

```bash
# Screenshot Add Server sheet
mcpproxy-ui-test screenshot_window --bundle-id com.smartmcpproxy.mcpproxy.dev

# List menu items
mcpproxy-ui-test list_menu_items --bundle-id com.smartmcpproxy.mcpproxy.dev

# Test Cmd+N
mcpproxy-ui-test send_keypress --key cmd+n --bundle-id com.smartmcpproxy.mcpproxy.dev
```

## Key Files

| File | Change Summary |
|------|----------------|
| `internal/httpapi/server.go` | PATCH handler for server updates |
| `AddServerView.swift` | Sheet size, validation, connection test, protocols |
| `ServerDetailView.swift` | Editable Config tab |
| `ServersView.swift` | Empty state, status tooltips |
| `APIClient.swift` | updateServer(), import timeout |
| `MCPProxyApp.swift` | Cmd+N keyboard shortcut |
