# Quickstart: Retention Telemetry v3

**Feature**: 044-retention-telemetry-v3
**Audience**: Developer verifying the v3 payload locally.

## Prerequisites

- Go 1.24+
- macOS 13+ (for tray + autostart verification) OR Linux (for env_kind testing)
- `jq`, `curl`, Node.js + npm (for E2E script)

## 1. Check out the branch and build

```bash
cd /Users/user/repos/mcpproxy-go-retention-telemetry
git checkout 044-retention-telemetry-v3
make build
```

## 2. Run unit tests

```bash
# All telemetry tests, with race detector
go test -race ./internal/telemetry/...

# Just the new v3 tests
go test -race -run "TestEnvKind|TestLaunchSource|TestActivation|TestPayloadV3|TestAnonymityScan" ./internal/telemetry/
```

Expected: all tests pass, including the anonymity scanner.

## 3. Start mcpproxy and inspect v3 payload

Because heartbeats fire over the network, verify locally via `/api/v1/status` which surfaces the same fields.

```bash
./mcpproxy serve --log-level=debug &
MCPPROXY_PID=$!

# Wait for listener
until curl -s http://127.0.0.1:8080/api/v1/status > /dev/null; do sleep 0.5; done

# Inspect activation + env_kind + launch_source
curl -s -H "X-API-Key: $(grep api_key ~/.mcpproxy/mcp_config.json | cut -d'"' -f4)" \
  http://127.0.0.1:8080/api/v1/status | jq '{env_kind, launch_source, autostart_enabled, activation, env_markers}'
```

Expected output shape:

```json
{
  "env_kind": "interactive",
  "launch_source": "cli",
  "autostart_enabled": null,
  "activation": {
    "first_connected_server_ever": false,
    "first_mcp_client_ever": false,
    "first_retrieve_tools_call_ever": false,
    "mcp_clients_seen_ever": [],
    "retrieve_tools_calls_24h": 0,
    "estimated_tokens_saved_24h_bucket": "0",
    "configured_ide_count": 0
  },
  "env_markers": {
    "has_ci_env": false,
    "has_cloud_ide_env": false,
    "is_container": false,
    "has_tty": true,
    "has_display": true
  }
}
```

## 4. Verify env_kind decision tree

### 4a. CI detection

```bash
kill $MCPPROXY_PID
GITHUB_ACTIONS=true ./mcpproxy serve --log-level=debug &
MCPPROXY_PID=$!
# wait, then curl status
curl -s -H "X-API-Key: $KEY" http://127.0.0.1:8080/api/v1/status | jq '.env_kind'
# Expected: "ci"
```

### 4b. Container detection (Linux)

```bash
# Only on Linux where /.dockerenv can be mocked via a container runtime
docker run --rm -v "$PWD:/app" -w /app golang:1.24 ./mcpproxy serve --listen 0.0.0.0:8080 &
# curl env_kind → "container"
```

### 4c. Cloud IDE

```bash
CODESPACES=true ./mcpproxy serve &
curl ... # → "cloud_ide"
```

## 5. Verify activation funnel

```bash
# Start fresh (wipe activation bucket)
rm -f ~/.mcpproxy/config.db
./mcpproxy serve &

# Add and connect a server; watch activation.first_connected_server_ever flip true
mcpproxy upstream add github-mcp '{"url":"https://api.github.com/mcp","protocol":"http"}'
sleep 3
curl -s ... | jq '.activation.first_connected_server_ever'  # → true

# Call retrieve_tools via MCP
curl -X POST http://127.0.0.1:8080/mcp -d '{...retrieve_tools request...}'
curl -s ... | jq '.activation.retrieve_tools_calls_24h'  # → 1
curl -s ... | jq '.activation.first_retrieve_tools_call_ever'  # → true
```

## 6. Verify anonymity invariant

```bash
# Build the anonymity scanner test
go test -race -run "TestAnonymity" -v ./internal/telemetry/
```

Expected: scanner detects and rejects a synthetic payload that contains `/Users/alice/`.

## 7. E2E smoke test

```bash
./scripts/test-api-e2e.sh
# Expected: exit 0
```

## 8. macOS tray verification (manual)

```bash
# Build the tray per CLAUDE.md instructions
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy \
  -emit-executable -O -o /tmp/MCPProxy-new \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')

cp /tmp/MCPProxy-new /tmp/MCPProxy.app/Contents/MacOS/MCPProxy
open /tmp/MCPProxy.app
```

Using the `mcpproxy-ui-test` MCP tools:
- `screenshot_window` → confirm first-run "Launch at login" dialog with checkbox ON by default.
- `click_menu_item` → open tray menu, confirm "Launch at login: On" indicator.
- Restart mac, confirm tray icon returns automatically.

## 9. Installer verification (macOS DMG)

```bash
./scripts/build.sh --dmg
# Install fresh DMG on a throwaway macOS VM / user account
# Confirm tray launches automatically after install
# Confirm first heartbeat carries launch_source="installer"
```

## 10. Cleanup

```bash
kill $MCPPROXY_PID 2>/dev/null
rm -f ~/.mcpproxy/config.db  # if you wiped earlier
```

## Troubleshooting

- `env_kind` is `"unknown"`: process startup fell through the decision tree. Check `MCPPROXY_LOG_LEVEL=debug` output for "env_kind detection" line.
- `autostart_enabled` is `null`: tray is not running OR you're on Linux. Start the tray.
- `activation` is missing from status: check that telemetry is not disabled (`mcpproxy telemetry status`).
- Anonymity scanner rejects legitimate payloads: check for hostnames/usernames in `activation.mcp_clients_seen_ever` — the MCP client is misreporting itself.
