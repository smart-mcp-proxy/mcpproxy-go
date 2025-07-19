# Repository Detection Feature

## Overview

The repository detection feature automatically identifies whether MCP servers discovered through `search_servers` are available as npm or PyPI packages. This enhances search results with accurate installation commands and package metadata.

## Features

- **Automatic Package Detection**: Queries npm registry and PyPI APIs to detect published packages
- **Smart Install Commands**: Generates `npm install` or `pip install` commands when packages are found
- **Intelligent Caching**: Caches API responses for 6 hours to improve performance
- **Configurable**: Enable/disable via `check_server_repo` configuration parameter
- **Result Limits**: Default 10 results (max 50) for optimal performance

## Configuration

### Enable/Disable Repository Detection

Add to your `mcp_config.json`:

```json
{
  "check_server_repo": true,
  "listen": ":8080"
}
```

- `true` (default): Enable repository detection
- `false`: Disable repository detection (faster but no install commands)

### Default Configuration Template

Repository detection is enabled by default in new configurations:

```json
{
  "listen": ":8080",
  "data_dir": "",
  "enable_tray": true,
  "debug_search": false,
  "check_server_repo": true,
  "mcpServers": [],
  "logging": {
    "level": "info",
    "enable_file": true,
    "enable_console": true
  }
}
```

## Usage Examples

### CLI Usage

```bash
# Basic search with repository detection
mcpproxy search-servers --registry pulse --search weather

# Limit results for faster response
mcpproxy search-servers --registry smithery --search database --limit 5

# Search without limit specification (uses default 10)
mcpproxy search-servers --registry mcprun --search api
```

### MCP Tool Usage

```json
{
  "name": "search_servers", 
  "arguments": {
    "registry": "pulse",
    "search": "weather",
    "limit": 10
  }
}
```

## Output Format

When repository detection finds packages, results include enhanced information:

```json
[
  {
    "id": "weather-mcp",
    "name": "Weather MCP Server",
    "description": "Real-time weather data via MCP",
    "url": "https://weather.example.com/mcp",
    "installCmd": "npm install weather-mcp-server",
    "repository_info": {
      "npm": {
        "type": "npm",
        "package_name": "weather-mcp-server",
        "version": "1.2.3", 
        "description": "Weather MCP server for Node.js",
        "install_cmd": "npm install weather-mcp-server",
        "url": "https://www.npmjs.com/package/weather-mcp-server",
        "exists": true
      },
      "pypi": {
        "type": "pypi",
        "package_name": "weather-mcp",
        "version": "0.5.1",
        "description": "Weather MCP server for Python",
        "install_cmd": "pip install weather-mcp", 
        "url": "https://pypi.org/project/weather-mcp/",
        "exists": true
      }
    },
    "registry": "Example Registry"
  }
]
```

## API Details

### Package Name Extraction

The system intelligently extracts potential package names from:

- Server names
- URL paths and hostnames
- Common MCP naming patterns

**Cleaning Rules:**
- Removes `mcp-`, `mcp_`, `-mcp`, `_mcp` prefixes/suffixes
- Removes `-server`, `_server` suffixes
- Converts to lowercase
- Handles scoped npm packages (`@scope/package`)

### API Endpoints Used

**npm Registry:**
- URL: `https://registry.npmjs.org/{package}`
- Method: GET
- Response: Package metadata with versions, description
- Scoped packages: URL-encoded (`@types/node` → `%40types%2Fnode`)

**PyPI JSON API:**
- URL: `https://pypi.org/pypi/{package}/json`
- Method: GET  
- Response: Package info, releases, metadata

### Caching Strategy

- **Cache Key Format**: `npm:{package}` or `pypi:{package}`
- **TTL**: 6 hours
- **Storage**: Uses existing mcpproxy cache system (BBolt)
- **Cache Miss**: API call → cache result → return
- **Cache Hit**: Return cached result (no API call)

## Performance Considerations

### Result Limits

- **Default**: 10 results
- **Maximum**: 50 results
- **Reason**: Repository detection makes HTTP calls; limits ensure reasonable response times

### Parallel Processing

- npm and PyPI checks run concurrently
- Multiple package names checked in sequence
- Stops at first successful detection per server

### Network Timeouts

- HTTP requests timeout after 10 seconds
- Failed requests don't block other checks
- Errors logged but don't stop search

## Troubleshooting

### Slow Response Times

**Problem**: `search_servers` taking too long

**Solutions**:
1. Reduce limit: `--limit 5`
2. Disable repository detection: `"check_server_repo": false`
3. Check network connectivity to npm/PyPI

### Missing Install Commands

**Problem**: No `installCmd` in results despite packages existing

**Debugging**:
1. Check if `check_server_repo` is enabled
2. Verify package name extraction logic
3. Check cache for existing negative results
4. Test package existence manually:
   ```bash
   curl -s "https://registry.npmjs.org/package-name"
   curl -s "https://pypi.org/pypi/package-name/json"
   ```

### Cache Issues

**Problem**: Outdated repository information

**Solutions**:
1. Wait for cache TTL (6 hours)
2. Restart mcpproxy to clear cache
3. Check cache storage location: `~/.mcpproxy/cache.db`

### API Rate Limits

**Problem**: npm or PyPI returning rate limit errors

**Solutions**:
1. Reduce search frequency
2. Increase cache TTL in code
3. Use smaller result limits

## Development

### Adding New Registry Types

To support additional package registries:

1. Add new `RepositoryType` in `internal/experiments/types.go`
2. Implement checker function in `internal/experiments/guesser.go`
3. Update `GuessRepositoryType` to call new checker
4. Add tests in `internal/experiments/guesser_test.go`

### Testing

Run repository detection tests:

```bash
# Unit tests
go test ./internal/experiments/...

# Integration tests with registries
go test ./internal/registries/... -v

# End-to-end tests
go test ./internal/server/... -run TestSearchServers
```

### Debugging

Enable debug logging to trace repository detection:

```bash
mcpproxy --log-level=debug --tray=false
```

Look for log entries containing:
- `Found npm package`
- `Found PyPI package` 
- `Failed to fetch`
- `Repository guessing`

## Security Considerations

- Only queries public npm and PyPI APIs
- No authentication credentials required
- No sensitive data transmitted
- API responses cached locally only
- Network requests respect timeouts

## Future Enhancements

Potential improvements:

1. **Additional Registries**: Docker Hub, GitHub Packages, etc.
2. **Version Management**: Detect latest/compatible versions
3. **Security Scanning**: Check for known vulnerabilities
4. **Install Verification**: Test installation commands
5. **Dependency Analysis**: Show package dependencies 