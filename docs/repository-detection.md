# Repository Detection Feature

## Overview

The repository detection feature automatically identifies whether MCP servers discovered through `search_servers` are available as npm or PyPI packages. This enhances search results with accurate installation commands and package metadata.

**Performance Improvements (v2.0):**
- **Batch Processing**: Processes multiple repositories concurrently instead of sequentially
- **Connection Pooling**: Uses persistent HTTP connections to reduce overhead
- **Short Timeouts**: 2-3 second timeout for npm checks to ensure fast responses
- **Graceful Failure**: Failed package lookups are skipped without affecting other results
- **Concurrency Limiting**: Maximum 10 concurrent requests to prevent overwhelming npm registry
- **Result Limits**: Default 10 results (max 50) with batch processing applied only to limited results

## Features

- **Automatic Package Detection**: Queries npm registry with parallel batch requests
- **Smart Install Commands**: Generates `npm install` or `pip install` commands when packages are found
- **Intelligent Caching**: Caches API responses for 6 hours to improve performance
- **Configurable**: Enable/disable via `check_server_repo` configuration parameter
- **High Performance**: Batch processing reduces search time from ~30 seconds to ~3 seconds for 10 servers
- **Error Resilience**: Individual package failures don't affect the overall search results

## Performance Characteristics

### Before (Sequential Processing)
- 10 servers: ~10-30 seconds (1-3 seconds per server)
- 50 servers: ~50-150 seconds
- High latency due to sequential npm API calls
- Single failure could slow down entire batch

### After (Batch Processing) 
- 10 servers: ~2-3 seconds (parallel processing with 3-second timeout)
- 50 servers: ~3-5 seconds (concurrent with rate limiting)
- Low latency due to parallel npm API calls
- Individual failures handled gracefully

## Configuration

### Enable/Disable Repository Detection

Add to your `mcp_config.json`:

```json
{
  "check_server_repo": true,
  "listen": ":8080"
}
```

- `true` (default): Enable repository detection with batch processing
- `false`: Disable repository detection (faster but no install commands)

### Performance Tuning

The batch processor uses these internal settings for optimal performance:

```go
// Configurable constants in internal/experiments/guesser.go
const (
    batchRequestTimeout = 3 * time.Second    // Timeout per npm request
    maxConcurrentRequests = 10               // Max parallel requests
)
```

### Default Configuration Template

Repository detection is enabled by default in new configurations:

```json
{
  "listen": ":8080",
  "data_dir": "",
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
# Basic search with repository detection (uses batch processing)
mcpproxy search-servers --registry pulse --search weather

# Limit results for faster response (batch processing applied to limited set)
mcpproxy search-servers --registry smithery --search database --limit 5

# Search without limit specification (uses default 10, processed in batch)
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
    "id": "weather-service",
    "name": "Weather MCP Server",
    "description": "Provides weather data via MCP",
    "url": "https://github.com/user/weather-mcp-server",
    "installCmd": "npm install @user/weather-mcp-server",
    "repository_info": {
      "npm": {
        "type": "npm",
        "package_name": "@user/weather-mcp-server", 
        "version": "1.2.3",
        "description": "Weather MCP server package",
        "install_cmd": "npm install @user/weather-mcp-server",
        "url": "https://www.npmjs.com/package/@user/weather-mcp-server",
        "exists": true
      }
    },
    "registry": "Example Registry"
  }
]
```

## Technical Implementation

### Batch Processing Architecture

The enhanced repository detection uses a three-phase approach:

1. **Parse Phase**: Extract all server data without repository guessing
2. **Batch Phase**: Collect all GitHub URLs and process them concurrently
3. **Merge Phase**: Apply batch results back to original servers

### Key Components

- `GuessRepositoryTypesBatch()`: Main batch processing method
- `applyBatchRepositoryGuessing()`: Merges batch results with server data
- Connection pooling with `MaxIdleConns=100`, `MaxIdleConnsPerHost=20`
- Semaphore-based concurrency control
- Duplicate URL deduplication for efficiency

### Error Handling

- **Network Timeouts**: 3-second timeout per request
- **Failed Requests**: Logged but don't block other requests
- **Invalid URLs**: Filtered out before processing
- **Rate Limiting**: Controlled by semaphore to respect npm registry limits

## Debugging

### Enable Debug Logging

```bash
mcpproxy --log-level=debug --tray=false
```

### Monitor Batch Processing

```bash
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(batch repository|concurrent request|npm package)"
```

Look for log messages like:
- `Starting batch repository type guessing`
- `Batch repository type guessing completed`
- `Found npm package in cache`
- `Failed to guess repository type` (for debugging failures)

## Migration Notes

### From v1.x to v2.0

- **Backward Compatible**: All existing APIs work unchanged
- **Performance Improvement**: Automatic for all users with `check_server_repo: true`
- **New Capabilities**: Batch processing happens transparently
- **Cache Compatibility**: Existing cache entries remain valid

### Breaking Changes

None. The batch processing is a drop-in performance improvement that maintains full API compatibility.

## Troubleshooting

### Slow Performance

1. Check if `check_server_repo` is enabled (adds npm lookup time)
2. Verify network connectivity to npm registry
3. Consider reducing limit if many servers have GitHub repositories
4. Check debug logs for network timeout issues

### Missing Install Commands

1. Ensure GitHub repository URLs are correctly formatted
2. Verify npm packages exist with scoped naming (`@user/repo`)
3. Check if repository detection is enabled in config
4. Review debug logs for npm API errors

### Rate Limiting

If you encounter npm rate limits:
1. Reduce `maxConcurrentRequests` in code if needed
2. Add delays between batch operations  
3. Consider caching duration adjustments 