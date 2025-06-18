# mcpproxy-go

A high-performance Go implementation of the Smart MCP Proxy - an intelligent tool discovery and proxying server for Model Context Protocol (MCP) servers.

## Features

- **Single Binary**: Self-contained executable with no external dependencies
- **Concurrent Performance**: Built with Go's excellent concurrency primitives
- **Local BM25 Search**: Fast full-text search using Bleve v2 for tool discovery
- **Persistent Storage**: BoltDB for configuration, statistics, and change detection
- **System Tray UI**: Cross-platform system tray integration (planned)
- **HTTP REST API**: RESTful endpoints for integration
- **Tool Discovery**: Automatic tool indexing from upstream MCP servers
- **Change Detection**: SHA-256 based tool change detection for efficient re-indexing

## Architecture

The proxy acts as an intelligent gateway between AI agents and multiple MCP servers:

```
┌────────────┐ HTTP/JSON   ┌──────────────────────────────┐ HTTP/MCP    ┌──────────────┐
│  AI Agent  │ ⇆ :8080 ⇆  │        mcpproxy-go           │ ⇆  Multiple ⇆ │  MCP Servers │
└────────────┘             │                              │             └──────────────┘
                          │  ┌───────────────┐           │
                          │  │ Bleve Index   │───────────┘
                          └──→ BoltDB (cfg, stats, hashes)
```

## Installation

### Build from Source

```bash
git clone <repository-url>
cd mcpproxy-go
go build ./cmd/mcpproxy
```

### Dependencies

The project uses Go modules and will automatically download required dependencies:

- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `go.uber.org/zap` - Structured logging
- `go.etcd.io/bbolt` - Embedded key/value database
- `github.com/blevesearch/bleve/v2` - Full-text search engine
- `github.com/getlantern/systray` - System tray integration (future)

## Configuration

### Configuration File

Create a `mcp_config.json` file:

```json
{
  "listen": ":8080",
  "data_dir": "",
  "enable_tray": true,
  "top_k": 5,
  "tools_limit": 15,
  "mcpServers": {
    "spoonacular": {
      "name": "spoonacular",
      "url": "http://localhost:8091/mcp/",
      "enabled": true,
      "created": "2024-01-01T00:00:00Z"
    },
    "sqlite": {
      "name": "sqlite",
      "command": "uvx",
      "args": ["mcp-server-sqlite", "--db-path", "/path/to/demo.db"],
      "env": {},
      "enabled": true,
      "created": "2024-01-01T00:00:00Z"
    }
  }
}
```

### Environment Variables

All configuration options can be overridden with environment variables using the `MCPP_` prefix:

- `MCPP_LISTEN` - Listen address (default: ":8080")
- `MCPP_DATA_DIR` - Data directory (default: "~/.mcpproxy")
- `MCPP_TRAY` - Enable system tray (default: true)
- `MCPP_TOP_K` - Number of tools to return in search (default: 5)
- `MCPP_TOOLS_LIMIT` - Maximum tools in active pool (default: 15)

## Usage

### Starting the Server

```bash
# Using configuration file
./mcpproxy --config mcp_config.json

# Using command line flags
./mcpproxy --listen :8080 --data-dir ~/.mcpproxy

# Using environment variables
MCPP_LISTEN=:9090 ./mcpproxy

# Adding upstream servers via CLI
./mcpproxy --upstream spoonacular=http://localhost:8091/mcp/
```

### API Endpoints

#### MCP Endpoints

- `GET /` - Server information
- `GET /tools/list` - List available proxy tools
- `POST /tools/call` - Call a tool (MCP format)

#### Proxy Endpoints

- `POST /proxy/retrieve_tools` - Search for tools
- `POST /proxy/call_tool` - Execute a tool on upstream server
- `GET /proxy/upstream_servers` - List upstream servers
- `GET /proxy/tools_stats` - Tool usage statistics

#### Management Endpoints

- `GET /health` - Health check
- `GET /status` - Comprehensive server status

### Example Usage

#### Tool Discovery

```bash
curl -X POST http://localhost:8080/proxy/retrieve_tools \
  -H "Content-Type: application/json" \
  -d '{"query": "database sql"}'
```

Response:
```json
{
  "message": "Found 3 tools matching query",
  "tools": [
    {
      "name": "sqlite:execute_query",
      "original_name": "execute_query",
      "server": "sqlite", 
      "description": "Execute SQL query on database",
      "score": 0.95,
      "input_schema": {...}
    }
  ],
  "query": "database sql",
  "total_indexed_tools": 12
}
```

#### Tool Execution

```bash
curl -X POST http://localhost:8080/proxy/call_tool \
  -H "Content-Type: application/json" \
  -d '{
    "name": "sqlite:execute_query",
    "args": {
      "query": "SELECT * FROM users LIMIT 5"
    }
  }'
```

## Data Storage

The proxy stores data in the configured data directory (default: `~/.mcpproxy/`):

- `config.db` - BoltDB database containing:
  - `upstreams` - Server configurations
  - `toolstats` - Tool usage statistics
  - `toolhash` - SHA-256 hashes for change detection
  - `meta` - Schema version and metadata
- `index.bleve/` - Bleve search index directory

## Development

### Running Tests

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/config
go test ./internal/storage
go test ./internal/index

# Run tests with coverage
go test -cover ./...
```

### Project Structure

```
mcpproxy-go/
├── cmd/mcpproxy/          # Main application entry point
├── internal/              # Internal packages
│   ├── config/           # Configuration management
│   ├── storage/          # BoltDB storage layer
│   ├── index/            # Bleve search indexing
│   ├── server/           # HTTP server implementation
│   ├── upstream/         # Upstream MCP client management
│   ├── tray/             # System tray integration
│   └── hash/             # SHA-256 utilities
├── testdata/             # Test fixtures
└── mcp_config.json       # Sample configuration
```

### Adding New Features

1. **Storage**: Add new bucket types in `internal/storage/models.go`
2. **Indexing**: Extend search capabilities in `internal/index/`
3. **Endpoints**: Add new HTTP handlers in `internal/server/mcp.go`
4. **Upstream**: Extend client capabilities in `internal/upstream/`

## Built-in MCP Tools

The proxy exposes these tools to MCP clients:

### `retrieve_tools`

Search and discover tools from upstream servers.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Search query for finding relevant tools"
    }
  },
  "required": ["query"]
}
```

### `call_tool`

Execute a discovered tool on its upstream server.

**Input Schema:**
```json
{
  "type": "object", 
  "properties": {
    "name": {
      "type": "string",
      "description": "Name of the tool to execute (server:tool format)"
    },
    "args": {
      "type": "object",
      "description": "Arguments to pass to the tool"
    }
  },
  "required": ["name", "args"]
}
```

## Performance

- **Concurrent**: Goroutine-based architecture handles multiple requests concurrently
- **Fast Search**: BM25 search typically returns results in <10ms
- **Efficient Storage**: BoltDB provides fast key-value operations
- **Memory Efficient**: Streaming JSON parsing, bounded tool pools
- **Change Detection**: Only re-indexes tools when they actually change

## Troubleshooting

### Server Won't Start

1. Check if port is already in use: `lsof -i :8080`
2. Verify configuration file syntax: `cat mcp_config.json | jq .`
3. Check data directory permissions
4. Review server logs for specific errors

### No Tools Found

1. Verify upstream servers are running and accessible
2. Check server connection status: `curl http://localhost:8080/status`
3. Review upstream server configurations
4. Check if tools were indexed: look for `document_count` in status

### Search Not Working

1. Verify Bleve index exists in data directory
2. Check for indexing errors in logs
3. Try rebuilding index (restart server)
4. Verify search query format

## License

[License information]

## Contributing

[Contributing guidelines]

## Roadmap

- [ ] WebSocket transport support
- [ ] Vector similarity search
- [ ] Distributed indexing
- [ ] Web UI dashboard
- [ ] Auto-update mechanism
- [ ] Docker support
- [ ] Kubernetes operator 