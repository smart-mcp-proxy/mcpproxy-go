# MCPProxy Manual Testing Guide

This guide provides step-by-step instructions for manually testing the refactored mcpproxy with all new features.

## Prerequisites

1. **Go 1.23+** installed
2. **Node.js** and **npm** for testing MCP servers
3. **jq** for JSON processing
4. **Docker** (optional, for testing Docker isolation)

## Quick Start

### 1. Build MCPProxy

```bash
# Clone and navigate to repository
cd /path/to/mcpproxy-go/.tree/next

# Build the binary
go build -o mcpproxy ./cmd/mcpproxy

# Verify build
./mcpproxy --version
```

### 2. Create Test Configuration

```bash
# Create test data directory
mkdir -p test-data

# Create minimal configuration
cat > test-data/config.json << 'EOF'
{
  "listen": ":8080",
  "data_dir": "./test-data",
  "enable_tray": false,
  "mcpServers": [
    {
      "name": "everything",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true,
      "quarantined": false
    }
  ],
  "features": {
    "enable_observability": true,
    "enable_health_checks": true,
    "enable_metrics": true,
    "enable_tracing": false,
    "enable_web_ui": true,
    "enable_tray": false
  }
}
EOF
```

### 3. Start MCPProxy

```bash
# Start server with debug logging
./mcpproxy serve --config=test-data/config.json --log-level=debug

# In another terminal, verify it's running
curl http://localhost:8080/healthz
```

## Testing Scenarios

### 1. Basic Health & Status

```bash
# Health check endpoint
curl http://localhost:8080/healthz
# Expected: {"status":"ok","timestamp":"2025-09-19T..."}

# Readiness check endpoint
curl http://localhost:8080/readyz
# Expected: {"status":"ok","timestamp":"2025-09-19T..."}

# Prometheus metrics
curl http://localhost:8080/metrics
# Expected: Prometheus metrics format

# Server status via API
curl http://localhost:8080/api/v1/servers | jq .
# Expected: JSON with server list and stats
```

### 2. Server-Sent Events (SSE)

```bash
# Listen to real-time events
curl -N http://localhost:8080/events

# In another terminal, trigger events by enabling/disabling servers
curl -X POST http://localhost:8080/api/v1/servers/everything/disable
curl -X POST http://localhost:8080/api/v1/servers/everything/enable
```

Expected SSE output:
```
event: status
data: {"running":true,"listen_addr":":8080",...}

event: server.state.changed
data: {"server":"everything","enabled":false,...}
```

### 3. Web UI Testing

```bash
# Access Web UI
open http://localhost:8080/ui/

# Or test with curl
curl http://localhost:8080/ui/ | grep -o '<title>.*</title>'
# Expected: <title>MCPProxy Dashboard</title>
```

Web UI should show:
- Server status dashboard
- Real-time updates via SSE
- Server management controls
- Tool search interface

### 4. Tool Discovery & Search

```bash
# Wait for everything server to connect and tools to be indexed
# Check server status
curl http://localhost:8080/api/v1/servers | jq '.data.servers[] | select(.name=="everything")'

# List tools from everything server
curl "http://localhost:8080/api/v1/servers/everything/tools" | jq .

# Search for tools
curl "http://localhost:8080/api/v1/index/search?q=echo" | jq .
curl "http://localhost:8080/api/v1/index/search?q=math&limit=5" | jq .
```

### 5. Tool Execution

```bash
# Call a simple echo tool
./mcpproxy call tool --tool-name=everything:echo \
  --json_args='{"message":"Hello from mcpproxy!"}'

# Call a math tool
./mcpproxy call tool --tool-name=everything:add \
  --json_args='{"a":5,"b":3}'

# Call a file operation tool
./mcpproxy call tool --tool-name=everything:write_file \
  --json_args='{"path":"./test-data/test.txt","content":"Test file content"}'
```

### 6. Port Conflict Recovery (Tray)

1. Hold the default port to simulate a conflict:

   ```bash
   # Terminal A
   python3 -m http.server 8080
   ```

2. Launch the tray build and allow it to start the core. The status menu displays **Port conflict** with a dedicated submenu.
3. Use **Resolve port conflict → Use available port …** to switch the core to an automatically selected free port. The tray persists the new value in `mcp_config.json` and restarts the server.
4. Alternatively choose **Retry start** once you have released the original port or **Open config directory** to edit the listen address manually.
5. On macOS you can automate the interaction with the new submenu via `osascript`:

   ```applescript
   osascript <<'EOF'
   tell application "System Events"
     tell process "mcpproxy-tray"
       click menu bar item 1 of menu bar 1
       click menu item "Resolve port conflict" of menu 1 of menu bar item 1 of menu bar 1
       delay 0.2
       click menu item "Use available port" of menu 1 of menu item "Resolve port conflict" of menu bar item 1 of menu bar 1
     end tell
   end tell
   EOF
   ```

6. Verify the new port by inspecting the tray tooltip (now shows the bound address) and by calling `curl http://localhost:<new-port>/healthz`.

### 6. Server Management

```bash
# List all servers
curl http://localhost:8080/api/v1/servers | jq '.data.servers'

# Disable a server
curl -X POST http://localhost:8080/api/v1/servers/everything/disable | jq .

# Enable a server
curl -X POST http://localhost:8080/api/v1/servers/everything/enable | jq .

# Restart a server
curl -X POST http://localhost:8080/api/v1/servers/everything/restart | jq .
```

### 7. Logs and Monitoring

```bash
# Get server logs
curl "http://localhost:8080/api/v1/servers/everything/logs?tail=20" | jq .

# Check metrics endpoint
curl http://localhost:8080/metrics | grep mcpproxy

# Monitor log files
tail -f test-data/logs/main.log
tail -f test-data/logs/server-everything.log
```

### 8. Feature Flag Testing

Create a configuration with different feature flags:

```bash
cat > test-data/minimal-config.json << 'EOF'
{
  "listen": ":8081",
  "data_dir": "./test-data-minimal",
  "mcpServers": [
    {
      "name": "everything",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true
    }
  ],
  "features": {
    "enable_observability": false,
    "enable_web_ui": false,
    "enable_tray": false,
    "enable_tracing": false
  }
}
EOF

# Start with minimal features
./mcpproxy serve --config=test-data/minimal-config.json --log-level=debug

# Test that observability endpoints are not available
curl http://localhost:8081/healthz  # Should return 404
curl http://localhost:8081/metrics  # Should return 404
curl http://localhost:8081/ui/      # Should return 404
```

## Advanced Testing Scenarios

### 1. Docker Isolation Testing

**Prerequisites**: Docker installed and running

```bash
cat > test-data/docker-config.json << 'EOF'
{
  "listen": ":8082",
  "data_dir": "./test-data-docker",
  "docker_isolation": {
    "enabled": true,
    "memory_limit": "256m",
    "cpu_limit": "0.5",
    "timeout": "30s"
  },
  "mcpServers": [
    {
      "name": "python-everything",
      "protocol": "stdio",
      "command": "python3",
      "args": ["-c", "print('Hello from Python MCP server')"],
      "enabled": true,
      "isolation": {
        "enabled": true,
        "image": "python:3.11"
      }
    }
  ],
  "features": {
    "enable_docker_isolation": true
  }
}
EOF

# Start with Docker isolation
./mcpproxy serve --config=test-data/docker-config.json --log-level=debug

# Verify containers are created
docker ps | grep mcpproxy

# Test isolated execution
./mcpproxy call tool --tool-name=python-everything:some_tool --json_args='{}'
```

### 2. OAuth Testing

```bash
cat > test-data/oauth-config.json << 'EOF'
{
  "listen": ":8083",
  "data_dir": "./test-data-oauth",
  "mcpServers": [
    {
      "name": "github-test",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "enabled": true,
      "oauth": {
        "client_id": "your-github-client-id",
        "client_secret": "your-github-client-secret",
        "redirect_uri": "http://localhost:8083/oauth/callback",
        "scopes": ["read:user"]
      }
    }
  ]
}
EOF

# Start with OAuth server
./mcpproxy serve --config=test-data/oauth-config.json --log-level=debug

# Trigger OAuth flow
curl -X POST http://localhost:8083/api/v1/servers/github-test/login
```

### 3. System Tray Testing

```bash
# Start with tray enabled
cat > test-data/tray-config.json << 'EOF'
{
  "listen": ":8084",
  "data_dir": "./test-data-tray",
  "enable_tray": true,
  "mcpServers": [
    {
      "name": "everything",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true
    }
  ],
  "features": {
    "enable_tray": true
  }
}
EOF

# Build and start tray application
go build -o mcpproxy-tray ./cmd/mcpproxy-tray
./mcpproxy serve --config=test-data/tray-config.json --tray=true

# Verify tray icon appears in system tray
# Test tray menu interactions
```

### 4. Performance Testing

```bash
# Add multiple servers for load testing
cat > test-data/load-config.json << 'EOF'
{
  "listen": ":8085",
  "data_dir": "./test-data-load",
  "mcpServers": [
    {
      "name": "everything-1",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true
    },
    {
      "name": "everything-2",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true
    }
  ],
  "features": {
    "enable_observability": true,
    "enable_metrics": true
  }
}
EOF

# Start server
./mcpproxy serve --config=test-data/load-config.json --log-level=info

# Run load test
for i in {1..50}; do
  curl -s "http://localhost:8085/api/v1/index/search?q=test" > /dev/null &
done
wait

# Check metrics for performance data
curl http://localhost:8085/metrics | grep -E "(request_duration|tool_calls)"
```

## Troubleshooting

### Common Issues

1. **Server not starting**:
   ```bash
   # Check configuration syntax
   jq . test-data/config.json

   # Check port availability
   lsof -i :8080

   # Check logs
   tail -f test-data/logs/main.log
   ```

2. **Everything server not connecting**:
   ```bash
   # Test server manually
   npx -y @modelcontextprotocol/server-everything

   # Check Node.js installation
   node --version
   npm --version

   # Check server logs
   tail -f test-data/logs/server-everything.log
   ```

3. **Tools not appearing in search**:
   ```bash
   # Check server status
   curl http://localhost:8080/api/v1/servers | jq '.data.servers[] | select(.name=="everything")'

   # Verify tools are indexed
   curl http://localhost:8080/api/v1/servers/everything/tools | jq '.data.count'

   # Check index status
   ls -la test-data/index.bleve/
   ```

4. **Web UI not loading**:
   ```bash
   # Check if web UI is enabled
   curl http://localhost:8080/ui/

   # Verify frontend assets
   curl http://localhost:8080/ui/assets/

   # Check feature flags
   grep -A 10 '"features"' test-data/config.json
   ```

### Log Analysis

```bash
# Monitor all activity
tail -f test-data/logs/*.log

# Filter for errors
grep -E "(ERROR|WARN)" test-data/logs/main.log

# Check OAuth flows
grep -E "(oauth|OAuth|token)" test-data/logs/main.log

# Monitor tool calls
grep -E "(tool.*call|call.*tool)" test-data/logs/main.log

# Check server connections
grep -E "(connect|disconnect|retry)" test-data/logs/main.log
```

## Testing Checklist

- [ ] Basic server starts and responds to health checks
- [ ] Web UI loads and displays server dashboard
- [ ] SSE events stream correctly for server changes
- [ ] Everything server connects and tools are indexed
- [ ] Tool search returns relevant results
- [ ] Tool execution works via CLI and API
- [ ] Server management (enable/disable/restart) works
- [ ] Observability endpoints return correct data
- [ ] Feature flags correctly enable/disable functionality
- [ ] Docker isolation works (if Docker available)
- [ ] System tray integration works (if enabled)
- [ ] Logs are written to correct locations
- [ ] Configuration validation works

## Test Data Cleanup

```bash
# Clean up test data
rm -rf test-data*
rm -f mcpproxy mcpproxy-tray

# Remove any Docker containers
docker ps -a | grep mcpproxy | awk '{print $1}' | xargs docker rm -f
```

This comprehensive testing guide covers all major functionality of the refactored mcpproxy. Each test scenario validates different aspects of the modular architecture and ensures the system works correctly in various configurations.
