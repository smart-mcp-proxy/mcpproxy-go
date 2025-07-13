# MCP Tracing Guide

This guide explains how to enable and use raw JSON-RPC message tracing in mcpproxy to debug MCP communication issues.

## Overview

The tracing feature logs all MCP communication in raw JSON format:

1. **Server-side tracing**: Logs requests from MCP clients to mcpproxy and responses back
2. **Client-side tracing**: Logs requests from mcpproxy to upstream MCP servers and their responses

This allows you to see exactly what messages are being exchanged in both directions, which is crucial for debugging issues like the `tools/list` timeout problem.

## Enabling Tracing

### Method 1: Command Line Flag

```bash
./mcpproxy --enable-tracing
```

### Method 2: Environment Variable

```bash
export MCP_TRACE=1
./mcpproxy
```

### Method 3: Configuration File

Add to your `mcp_config.json`:

```json
{
  "enable_tracing": true,
  "mcpServers": {
    // ... your server configurations
  }
}
```

## Log Output Format

When tracing is enabled, you'll see log entries like:

### Incoming Requests
```
🔍 MCP Request | method=tools/list | id=1 | message={"method":"tools/list","params":{"cursor":null}}
```

### Successful Responses
```
✅ MCP Response | method=tools/list | id=1 | result={"tools":[{"name":"retrieve_tools","description":"..."}],"nextCursor":null}
```

### Error Responses
```
❌ MCP Error | method=tools/call | id=2 | error="tool not found: invalid_tool"
```

## Use Cases

### 1. Debugging Tool Discovery Issues
Enable tracing to see exactly what tools are being requested and returned:

```bash
# Terminal 1: Start mcpproxy with tracing
MCP_TRACE=1 ./mcpproxy

# Terminal 2: Use mcpproxy tools to see traced messages
./mcpproxy --help  # This will trigger MCP calls that get traced
```

### 2. Debugging Client Integration
When integrating with MCP clients like Claude Desktop, enable tracing to see the exact JSON-RPC messages:

```bash
# Add to Claude Desktop config with tracing enabled mcpproxy
{
  "mcpServers": {
    "mcpproxy": {
      "command": "/path/to/mcpproxy",
      "args": ["--enable-tracing"],
      "env": {}
    }
  }
}
```

### 3. Debugging Upstream Server Communication
Trace messages to see how mcpproxy communicates with upstream MCP servers:

```bash
# Start with tracing and debug logs
./mcpproxy --enable-tracing --log-level=debug
```

## Filtering Trace Output

To focus on specific trace messages, use grep:

```bash
# Show only MCP requests
./mcpproxy --enable-tracing 2>&1 | grep "🔍 MCP Request"

# Show only errors
./mcpproxy --enable-tracing 2>&1 | grep "❌ MCP Error"

# Show specific method calls
./mcpproxy --enable-tracing 2>&1 | grep "tools/call"
```

## Performance Considerations

- Tracing adds JSON marshaling overhead for every request/response
- Only enable tracing for debugging purposes, not in production
- Consider using log levels to control verbosity when tracing is enabled

## Troubleshooting Common Issues

### 1. SSE Connection Timeouts
Look for patterns like:
```
🔍 MCP Request | method=tools/list | id=1
❌ MCP Error | method=tools/list | id=1 | error="context deadline exceeded"
```

### 2. OAuth Flow Issues
Trace OAuth-related requests:
```bash
./mcpproxy --enable-tracing 2>&1 | grep -E "(oauth|auth)"
```

### 3. Tool Call Failures
Look for tool call patterns:
```
🔍 MCP Request | method=tools/call | id=2 | message={"method":"tools/call","params":{"name":"server:tool"}}
❌ MCP Error | method=tools/call | id=2 | error="transport error: context deadline exceeded"
```

## Integration with External Tools

### With mcp-debug CLI
You can combine mcpproxy tracing with external MCP debugging tools:

```bash
# Terminal 1: Start mcpproxy with tracing
./mcpproxy --enable-tracing

# Terminal 2: Use mcp-debug to inspect
mcp-debug --endpoint http://localhost:8080/mcp --verbose
```

### With Custom Scripts
Parse trace output for automated analysis:

```bash
./mcpproxy --enable-tracing 2>&1 | \
  grep "🔍 MCP Request" | \
  jq -r '.message' | \
  jq '.method' | \
  sort | uniq -c
```

## Example Trace Session

Here's what a typical trace session looks like:

```bash
$ MCP_TRACE=1 ./mcpproxy
2025-01-11T12:00:00.000Z | INFO | Configuration loaded | enable_tracing=true
2025-01-11T12:00:01.000Z | INFO | 🔍 MCP Request | method=initialize | id=1 | message={"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}
2025-01-11T12:00:01.001Z | INFO | ✅ MCP Response | method=initialize | id=1 | result={"protocolVersion":"2025-03-26","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mcpproxy-go","version":"1.0.0"}}
2025-01-11T12:00:01.002Z | INFO | 🔍 MCP Request | method=tools/list | id=2 | message={"method":"tools/list","params":{}}
2025-01-11T12:00:01.003Z | INFO | ✅ MCP Response | method=tools/list | id=2 | result={"tools":[{"name":"retrieve_tools","description":"🔍 CALL THIS FIRST to discover relevant tools!"}]}
```

This shows the complete MCP handshake and tool discovery process.

## Debugging the tools/list Timeout Issue

If you're experiencing `tools/list` timeouts with Cloudflare servers, use this specific grep pattern to trace the issue:

```bash
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(📤 MCP Request.*tools/list|📥 MCP Response.*tools/list|❌ MCP Request failed.*tools/list|🔗 MCP Transport|✅ MCP Transport|context deadline exceeded|transport error)"
```

### What to Look For:

1. **Successful handshake but no tools/list response**:
   ```
   🔗 MCP Transport starting | server=cloudflare_dns_analytics
   ✅ MCP Transport started successfully | server=cloudflare_dns_analytics
   📤 MCP Request: Client → Server | server=cloudflare_dns_analytics | method=initialize
   📥 MCP Response: Server → Client | server=cloudflare_dns_analytics | method=initialize
   📤 MCP Request: Client → Server | server=cloudflare_dns_analytics | method=tools/list
   [No response for 30+ seconds]
   ❌ MCP Request failed | server=cloudflare_dns_analytics | method=tools/list | error=context deadline exceeded
   ```

2. **Missing Authorization headers**:
   Look for OAuth token debug messages and verify Authorization headers are being sent.

3. **Transport-level issues**:
   Check for transport start/close messages and connection state changes.

### Quick Test Command:

```bash
# Start mcpproxy with tracing and test immediately
MCP_TRACE=1 ./mcpproxy --enable-tracing &
MCPPROXY_PID=$!

# Wait a moment for startup
sleep 2

# Test tools/list directly (this will show in trace output)
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

# Clean up
kill $MCPPROXY_PID
``` 