# Smart MCP Proxy

A smart proxy server for the Model Context Protocol (MCP) that provides intelligent tool discovery, indexing, and routing capabilities with an enhanced system tray interface and **advanced security quarantine protection against Tool Poisoning Attacks (TPAs)**.

## ⚡ Fast Startup & Background Operations

The Smart MCP Proxy features optimized startup with immediate tray appearance and background connection handling:

### 🚀 **Instant Startup**
- **Immediate Tray**: Appears within 1-2 seconds, no waiting for upstream connections
- **Non-blocking**: Connections happen in background while proxy is fully functional
- **Quick Access**: Users can quit, check status, or interact immediately after launch
- **Auto-Configuration**: Creates default configuration file automatically if none exists

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

### 🛑 **Robust Signal Handling**
- **Graceful Termination**: Properly handles SIGTERM and SIGINT signals (Ctrl+C)
- **Background Cleanup**: Stops all background operations cleanly
- **No Hanging Processes**: Exits promptly without requiring force kill
- **Clean Shutdown Logs**: Detailed logging shows exactly what's being stopped

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
- **Enhanced Tool Search**: Consolidated `retrieve_tools` with optional statistics, debug mode, and tool ranking explanations  
- **Semantic Search**: Find relevant tools using natural language queries with BM25 full-text search
- **Tool Aggregation**: Combine tools from multiple upstream servers into a single interface
- **Response Truncation & Caching**: Automatically truncate large tool responses to prevent LLM context bloat
- **Smart Pagination**: Access cached response data through pagination with the `read_cache` tool
- **JSON Structure Analysis**: Intelligent splitting of JSON responses by record arrays
- **HTTP & Stdio Support**: Connect to MCP servers via HTTP or stdio protocols
- **Security Controls**: Read-only mode, management disabling, granular server add/remove permissions
- **Persistent Storage**: Cache tool metadata and connection information
- **Configuration Management**: Flexible JSON-based configuration with environment variable support
- **System Tray Integration**: Native system tray with real-time monitoring and control
- **Auto-updates**: Built-in update checking and installation
- **Cross-platform**: Works on macOS, Windows, and Linux
- **MCP Prompts Support**: Ready for prompt templates and workflow guidance (when mcp-go supports it)
- **Security Quarantine System**: Advanced protection against Tool Poisoning Attacks (TPAs)

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
  "tool_response_limit": 20000,
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

### Security Configuration

Configure security settings to control access in different environments:

```json
{
  "read_only_mode": false,
  "disable_management": false,
  "allow_server_add": true,
  "allow_server_remove": true,
  "enable_prompts": true
}
```

Security options:
- `read_only_mode`: When true, only allows listing servers, blocks all modifications
- `disable_management`: When true, completely disables the `upstream_servers` tool
- `allow_server_add`: Controls whether new servers can be added (includes `add`, `add_batch`, `import_cursor`)
- `allow_server_remove`: Controls whether servers can be removed
- `enable_prompts`: Enable MCP prompts capability for workflow guidance

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
- `--read-only`: Enable read-only mode (security)
- `--disable-management`: Disable server management (security)
- `--allow-server-add`: Allow adding servers (default: true)
- `--allow-server-remove`: Allow removing servers (default: true)
- `--enable-prompts`: Enable prompts capability (default: true)

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

## Response Truncation & Caching

The Smart MCP Proxy includes intelligent response truncation to prevent LLM context bloat while maintaining access to complete data through caching and pagination.

### How It Works

1. **Automatic Truncation**: Tool responses exceeding the configured limit (default: 20,000 characters) are automatically truncated
2. **JSON Analysis**: The proxy analyzes JSON responses to identify record arrays for intelligent splitting
3. **Smart Caching**: Complete responses are cached with 2-hour TTL for pagination access
4. **Fallback Handling**: Non-JSON or unstructured responses get simple truncation

### Configuration

```json
{
  "tool_response_limit": 20000  // Default: 20000 chars, 0 = disabled
}
```

### Truncated Response Format

When a response is truncated, you'll see:

```json
{
  "data": [{"id": 1}, {"id": 2}]  // Partial data...
}

... [truncated by mcpproxy]

Response truncated (limit: 20000 chars, actual: 45000 chars, records: 150)
Use read_cache tool: key="abc123def...", offset=0, limit=50
Returns: {"records": [...], "meta": {"total_records": 150, "total_size": 45000}}
```

### Accessing Complete Data

Use the `read_cache` tool to access paginated data:

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "read_cache",
    "arguments": {
      "key": "abc123def456...",
      "offset": 0,
      "limit": 50
    }
  }
}
```

Response:
```json
{
  "records": [
    {"id": 1, "name": "item1"},
    {"id": 2, "name": "item2"}
    // ... up to 50 records
  ],
  "meta": {
    "key": "abc123def456...",
    "total_records": 150,
    "limit": 50,
    "offset": 0,
    "total_size": 45000,
    "record_path": "data"
  }
}
```

### Cache Management

- **TTL**: Cached responses expire after 2 hours
- **Cleanup**: Automatic cleanup runs every 10 minutes
- **Storage**: Uses the same BBolt database as other proxy data
- **Statistics**: Cache hit/miss rates available through tool stats

## System Tray Usage

### Status Information

The tray tooltip shows comprehensive status in multiple lines:
- **Server Status**: Current phase and listen address
- **Server Connections**: Connected/total upstream servers  
- **Tool Count**: Total tools available from all connected servers

Example tooltip:
```
mcpproxy (Ready) - http://localhost:3001
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

## MCP Tools

The Smart MCP Proxy provides several MCP tools for managing servers and discovering tools:

### `upstream_servers` - Server Management

Comprehensive tool for managing upstream MCP servers with support for multiple operations:

#### Operations

- **`list`** - List all configured upstream servers
- **`add`** - Add a single upstream server
- **`remove`** - Remove an upstream server
- **`update`** - Update an existing server
- **`patch`** - Partially update server configuration

#### Adding Single Server

```json
{
  "operation": "add",
  "name": "github-tools",
  "url": "http://localhost:3001",
  "headers": {
    "Authorization": "Bearer your-token-here"
  },
  "enabled": true
}
```

```json
{
  "operation": "add",
  "name": "sqlite-tools",
  "command": "uvx",
  "args": ["mcp-server-sqlite", "--db-path", "/path/to/db.sqlite"],
  "env": {
    "MCP_SQLITE_PATH": "/path/to/db.sqlite"
  },
  "enabled": true
}
```



#### Patch/Update Server

```json
{
  "operation": "patch",
  "name": "github-tools",
  "enabled": false,
  "headers": {
    "Authorization": "Bearer new-token"
  }
}
```

### `retrieve_tools` - Tool Discovery

Search for tools across all upstream servers:

```json
{
  "query": "github repository",
  "limit": 10
}
```

### `quarantine_security` - Security Management

Manage server quarantine and security review:

```json
{
  "operation": "list_quarantined"
}
```

```json
{
  "operation": "inspect_quarantined",
  "name": "server-name"
}
```

```json
{
  "operation": "quarantine",
  "name": "server-name"
}
```

### `call_tool` - Tool Execution

Execute tools on upstream servers:

```json
{
  "name": "github:create_repository",
  "args": {
    "name": "my-new-repo",
    "private": false
  }
}
```



## Configuration Management & Sync

### 🔄 **Configuration as Single Source of Truth**

The Smart MCP Proxy implements a robust configuration sync system where the **config file serves as the single source of truth**:

#### **Key Features:**
- **Bidirectional Sync**: Changes made via LLM tools are automatically saved to config file
- **Startup Reconciliation**: Config file state overrides database on startup
- **Runtime File Watching**: Manual config file changes trigger automatic resync
- **Orphan Cleanup**: Servers removed from config file are automatically purged from database and search index
- **Hot Reloading**: Live updates without requiring restart

#### **Sync Scenarios:**
1. **LLM adds server** → Saved to database → **Automatically written to config file** ✅
2. **User edits config file** → File watcher detects change → **Database updated** ✅  
3. **User removes server from config** → Startup sync → **Database cleaned up** ✅
4. **Config attributes changed** (enabled/quarantined) → **Internal state synchronized** ✅

#### **Automatic Cleanup:**
- **Database purging**: Removes servers not in config file
- **Index cleanup**: Deletes search index entries for removed servers
- **Connection management**: Disconnects removed upstream servers
- **Status synchronization**: Updates enabled/quarantined states

### Configuration Persistence

The proxy automatically saves configuration changes to `~/.mcpproxy/mcp_config.json`. This includes:

- All upstream server configurations
- Server states (enabled/disabled/quarantined)
- Connection parameters (URLs, commands, environment variables)
- Authentication headers

### Configuration File Location

The configuration file location can be customized:

```bash
# Via environment variable
export MCPPROXY_DATA_DIR=/custom/path
./mcpproxy

# Via command line flag
./mcpproxy --data-dir /custom/path
```

### Hot Reloading

The proxy automatically:
- Saves configuration after any server changes via LLM tools
- Watches config file for manual edits and syncs changes
- Attempts to connect to newly added servers
- Updates the tool index when servers are modified
- Purges outdated database entries when servers are removed from config
- Reflects changes in the system tray (if enabled)

## Basic Usage Scenarios

### Scenario 1: Add Individual Servers

1. Use `upstream_servers` with `add` operation
2. Specify either `url` (for HTTP) or `command`/`args` (for stdio)
3. Include authentication headers if needed
4. Server is immediately connected and tools indexed

### Scenario 2: Manage Quarantine Security

1. Use `quarantine_security` tool to manage server security
2. List quarantined servers and inspect their tools
3. Use system tray or manual config to unquarantine if safe

## 🔒 Security Quarantine System

The Smart MCP Proxy implements a comprehensive security model to protect against **Tool Poisoning Attacks (TPAs)** and other MCP security vulnerabilities:

### 🛡️ **Automatic Quarantine Protection**
- **Auto-quarantine**: All newly added servers are automatically quarantined for security review
- **TPA Protection**: Prevents execution of potentially malicious tools until manual approval
- **Security Analysis**: Provides detailed tool descriptions and security prompts for review
- **Safe Defaults**: Security-first approach with manual unquarantining required

### 🔍 **Security Review Tools**
- **`quarantine_security`** tool with operations:
  - **`list_quarantined`**: List all servers in security quarantine
  - **`inspect_quarantined`**: Analyze tool descriptions for security threats
  - **`quarantine`**: Manually quarantine servers for security review
- **Tray Menu Integration**: Security quarantine management via system tray

### ⚠️ **Tool Poisoning Attack Prevention**
Tool Poisoning Attacks embed malicious instructions in tool descriptions that are invisible to users but visible to AI models. Our protection includes:

- **Hidden Instruction Detection**: Security prompts specifically look for malicious patterns
- **Full Description Exposure**: Complete tool descriptions shown for security review
- **Cross-Server Protection**: Quarantine prevents malicious servers from affecting trusted ones
- **Rug Pull Prevention**: Auto-quarantine blocks post-approval server modifications

### Common TPA Patterns Detected
- Instructions to read sensitive files (SSH keys, configs, databases)
- Commands to exfiltrate data while concealing actions  
- Hidden prompts in `<IMPORTANT>` tags or similar markup
- Requests to pass file contents as hidden parameters
- Instructions to override behavior of other trusted tools

### Enterprise Use Cases
- **Compliance**: Meet regulatory requirements for security controls
- **Development Protection**: Safe experimentation with untrusted servers
- **Incident Response**: Immediate quarantine during security incidents
- **Supply Chain Security**: Protection against compromised upstream servers
- **Multi-User Environments**: Centralized security management

See [SECURITY.md](SECURITY.md) for detailed security documentation and best practices. 