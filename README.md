# Smart MCP Proxy

A smart proxy server for the Model Context Protocol (MCP) that provides intelligent tool discovery, indexing, and routing capabilities with an enhanced system tray interface.

## ⚡ Fast Startup & Background Operations

The Smart MCP Proxy features optimized startup with immediate tray appearance and background connection handling:

### 🚀 **Instant Startup**
- **Immediate Tray**: Appears within 1-2 seconds, no waiting for upstream connections
- **Non-blocking**: Connections happen in background while proxy is fully functional
- **Quick Access**: Users can quit, check status, or interact immediately after launch

### 🔄 **Smart Connection Management** 
- **Exponential Backoff**: Retry failed connections with 1s, 2s, 4s, 8s... intervals up to 5 minutes
- **Background Retries**: Connection attempts don't block user interface
- **Resilient**: Proxy remains functional even with failed upstream connections
- **Status Updates**: Real-time feedback on connection progress and retry attempts

### 📈 **Real-time Status System**
- **Live Updates**: Status changes broadcast immediately to tray and interfaces
- **Connection Phases**: Shows progression through Initializing → Loading → Connecting → Ready
- **Detailed Feedback**: Connection counts, retry attempts, and error information
- **Transparent Operations**: Users always know what's happening in the background

## Enhanced System Tray Features

The Smart MCP Proxy includes a comprehensive system tray interface with real-time monitoring and control capabilities:

### 🔍 **Real-time Status Display**
- **Dynamic Tooltip**: Shows current proxy status, connection URL, and statistics
- **Live Updates**: Status refreshes every 5 seconds automatically
- **Connection Info**: Displays server URL (e.g., `http://localhost:8080/mcp`)
- **Server Statistics**: Shows connected servers count and total available tools

### 🎛️ **Server Control**
- **Start/Stop Server**: Toggle proxy server directly from the tray menu
- **Instant Feedback**: Status updates immediately after control actions
- **Background Operation**: Server runs in background while tray provides control interface

### 📊 **Upstream Server Monitoring**
- **Server Status Overview**: View connection status of all configured upstream servers
- **Tool Count Display**: See number of tools available from each server
- **Detailed Information**: Hover over menu items for comprehensive server details
- **Connection Health**: Monitor which servers are online/offline

### 🖥️ **System Integration**
- **Native Look**: Adapts to system theme (light/dark mode)
- **Template Icons**: Uses macOS template icons for better integration
- **Menu Bar Presence**: Persistent access from system menu bar
- **Cross-platform**: Works on macOS, Windows, and Linux

## Quick Start with Tray

1. **Build the application**:
   ```bash
   go build -ldflags "-X main.version=v0.3.0-enhanced" ./cmd/mcpproxy
   ```

2. **Create configuration**:
   ```json
   {
     "listen": ":8080",
     "enable_tray": true,
     "mcpServers": [
       {
         "name": "GitHub Tools",
         "url": "http://localhost:3001",
         "type": "http",
         "enabled": true
       }
     ]
   }
   ```

3. **Run with tray enabled**:
   ```bash
   ./mcpproxy --tray=true --config=config.json
   ```

4. **Access the tray**:
   - Look for the MCP Proxy icon in your system tray
   - Click to see the control menu
   - Hover over the icon for detailed status information

## Tray Menu Structure

```
Smart MCP Proxy
├── Status: Running (localhost:8080)     ← Current server status
├── ─────────────────────────────
├── Start/Stop Server                   ← Server control
├── ─────────────────────────────
├── Upstream Servers (2/3)              ← Server monitoring
│   └── [Hover for server details]      ← Individual server status
├── ─────────────────────────────
├── Check for Updates…                  ← Auto-update feature
├── Open Config                         ← Quick config access
├── ─────────────────────────────
└── Quit                               ← Clean shutdown
```

## Features

- **Intelligent Tool Discovery**: Automatically discover and index tools from multiple MCP servers
- **Semantic Search**: Find relevant tools using natural language queries
- **Tool Aggregation**: Combine tools from multiple upstream servers into a single interface
- **HTTP & Stdio Support**: Connect to MCP servers via HTTP or stdio protocols
- **Persistent Storage**: Cache tool metadata and connection information
- **Configuration Management**: Flexible JSON-based configuration with environment variable support
- **System Tray Integration**: Native system tray with real-time monitoring and control
- **Auto-updates**: Built-in update checking and installation
- **Cross-platform**: Works on macOS, Windows, and Linux

## Installation

### From Source

```bash
git clone https://github.com/your-org/mcpproxy-go
cd mcpproxy-go

# Build with GUI/tray support (default)
go build ./cmd/mcpproxy

# Build for headless/server environments (no GUI dependencies)
go build -tags nogui ./cmd/mcpproxy
```

### Using Go Install

```bash
go install github.com/your-org/mcpproxy-go/cmd/mcpproxy@latest
```

## Configuration

### Basic Configuration

Create a `config.json` file:

```json
{
  "listen": ":8080",
  "data_dir": "~/.mcpproxy",
  "enable_tray": true,
  "top_k": 5,
  "tools_limit": 15,
  "mcpServers": [
    {
      "name": "Local Python Server",
      "command": "python",
      "args": ["-m", "your_mcp_server"],
      "type": "stdio",
      "enabled": true
    },
    {
      "name": "Remote HTTP Server",
      "url": "http://localhost:3001",
      "type": "http",
      "enabled": true
    }
  ]
}
```

### Environment Variables

You can override configuration with environment variables:

```bash
export MCPPROXY_LISTEN=:8080
export MCPPROXY_TRAY=true
export MCPPROXY_DATA_DIR=~/.mcpproxy
./mcpproxy
```

### Command Line Options

```bash
./mcpproxy --help
```

Options:
- `--config, -c`: Configuration file path
- `--listen, -l`: Listen address (default: ":8080")
- `--data-dir, -d`: Data directory path
- `--tray`: Enable system tray (default: true)
- `--log-level`: Log level (debug, info, warn, error)

## Usage

### Running the Proxy

```bash
# With tray interface (recommended)
./mcpproxy --tray=true

# Command line only
./mcpproxy --tray=false

# With custom config
./mcpproxy --config=my-config.json
```

### Connecting Clients

Once running, clients can connect to the proxy:

```bash
# HTTP clients
curl -X POST http://localhost:8080/mcp/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1.0.0"},"capabilities":{}}}'
```

### Tool Discovery

The proxy automatically discovers and indexes tools from configured upstream servers. Tools are available through the unified interface with semantic search capabilities.

## System Tray Usage

### Status Information

The tray tooltip shows:
- **Proxy Status**: Running/Stopped
- **Connection URL**: Where clients can connect
- **Server Statistics**: Connected servers and available tools

Example tooltip:
```
Smart MCP Proxy - Running
URL: http://localhost:8080/mcp
Servers: 2/3 connected
Tools: 15 available
```

### Server Control

- **Start Server**: Starts the proxy server if stopped
- **Stop Server**: Gracefully stops the proxy server
- **Status Updates**: Menu items update in real-time

### Upstream Monitoring

Hover over "Upstream Servers" to see detailed status:
```
• GitHub Tools: Connected (8 tools)
• Weather API: Disconnected (0 tools)  
• File Manager: Connected (5 tools)
```

## API

### Initialize

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "clientInfo": {
      "name": "your-client",
      "version": "1.0.0"
    },
    "capabilities": {}
  }
}
```

### List Tools

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list",
  "params": {}
}
```

### Call Tool

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "server_name:tool_name",
    "arguments": {
      "param1": "value1"
    }
  }
}
```

## Architecture

The Smart MCP Proxy consists of several key components:

- **Server**: HTTP server handling MCP protocol requests
- **Upstream Manager**: Manages connections to upstream MCP servers
- **Index Manager**: Handles tool discovery and semantic search indexing
- **Storage Manager**: Persistent storage for configuration and metadata
- **Tray Manager**: System tray interface for monitoring and control

## Development

### Building

```bash
go build -ldflags "-X main.version=$(git describe --tags)" ./cmd/mcpproxy
```

### Testing

```bash
go test ./...
```

### Running in Development

```bash
go run ./cmd/mcpproxy --config=config-test.json --log-level=debug
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

MIT License - see LICENSE file for details.

## Troubleshooting

### Tray Icon Not Showing

1. **Check icon files exist**:
   ```bash
   ls -la internal/tray/*.png
   ```

2. **Rebuild with embedded icons**:
   ```bash
   go build ./cmd/mcpproxy
   ```

3. **Check system permissions** (macOS):
   - System Settings → Privacy & Security → Accessibility

### Server Not Starting

1. **Check port availability**:
   ```bash
   lsof -i :8080
   ```

2. **Verify configuration**:
   ```bash
   ./mcpproxy --config=config.json --log-level=debug
   ```

3. **Check upstream server connectivity**:
   - Ensure upstream servers are running
   - Verify network connectivity
   - Check firewall settings

For more detailed troubleshooting, see [TRAY_ICON_GUIDE.md](TRAY_ICON_GUIDE.md). 