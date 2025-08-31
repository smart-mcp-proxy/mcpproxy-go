# Demo: Repository Detection Feature

This guide shows how to test the new repository detection feature in mcpproxy.

## Setup

1. Build mcpproxy:
```bash
go build
```

2. Kill any existing mcpproxy instance:
```bash
pkill mcpproxy
```

3. Start mcpproxy with debug logging:
```bash
./mcpproxy --log-level=debug --tray=false
```

## Test the CLI

In another terminal, test the CLI with repository detection:

```bash
# Test with pulse registry (should find packages)
./mcpproxy search-servers --registry pulse --search "weather" --limit 5

# Test with smithery registry 
./mcpproxy search-servers --registry smithery --search "database" --limit 3

# List all registries
./mcpproxy search-servers --list-registries
```

## Test via MCP Tools (use in Cursor chat)

Use these tool calls directly in Cursor chat when connected to mcpproxy:

### List available registries
```json
{
  "name": "list_registries",
  "arguments": {}
}
```

### Search servers with repository detection
```json
{
  "name": "search_servers",
  "arguments": {
    "registry": "pulse",
    "search": "weather",
    "limit": 5
  }
}
```

### Search without limit (uses default 10)
```json
{
  "name": "search_servers", 
  "arguments": {
    "registry": "smithery",
    "search": "database"
  }
}
```

### Test limits (max 50)
```json
{
  "name": "search_servers",
  "arguments": {
    "registry": "pulse", 
    "limit": 100
  }
}
```

## Expected Output Features

Look for these new features in the output:

1. **Result Limits**: Results capped at specified limit (default 10, max 50)

2. **Repository Information**: When packages are detected:
```json
{
  "repository_info": {
    "npm": {
      "type": "npm",
      "package_name": "example-package",
      "version": "1.2.3",
      "description": "Package description",
      "install_cmd": "npm install example-package",
      "url": "https://www.npmjs.com/package/example-package",
      "exists": true
    },
    "pypi": {
      "type": "pypi", 
      "package_name": "example_package",
      "version": "0.5.1",
      "description": "Python package description",
      "install_cmd": "pip install example_package",
      "url": "https://pypi.org/project/example_package/",
      "exists": true
    }
  }
}
```

3. **Enhanced Install Commands**: Automatically generated when packages are found:
```json
{
  "installCmd": "npm install package-name"
}
```

## Configuration Testing

### Disable repository detection
Edit `~/.mcpproxy/mcp_config.json`:
```json
{
  "check_server_repo": false,
  "listen": ":8080"
}
```

Restart mcpproxy and test - should be faster but no repository info.

### Enable repository detection (default)
```json
{
  "check_server_repo": true,
  "listen": ":8080" 
}
```

## Debug Logs

Monitor the logs for repository detection activity:

```bash
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(Found npm package|Found PyPI package|Repository guessing|Failed to fetch)"
```

Look for:
- `Found npm package` - successful npm detection
- `Found PyPI package` - successful PyPI detection  
- `Repository guessing` - start of detection process
- `Failed to fetch` - API errors (normal for non-existent packages)

## Performance Testing

Test with different limits to see performance impact:

```json
{"name": "search_servers", "arguments": {"registry": "pulse", "limit": 1}}
{"name": "search_servers", "arguments": {"registry": "pulse", "limit": 5}}
{"name": "search_servers", "arguments": {"registry": "pulse", "limit": 10}}
{"name": "search_servers", "arguments": {"registry": "pulse", "limit": 20}}
```

Smaller limits should be faster due to fewer HTTP calls to npm/PyPI APIs.

## Troubleshooting

### No repository info appearing
1. Check `check_server_repo` is `true` in config
2. Verify network access to npm/PyPI
3. Check debug logs for errors

### Slow responses  
1. Reduce limit: `--limit 3`
2. Disable repository detection temporarily
3. Check network latency to npm/PyPI

### Cache testing
1. First search may be slow (cache miss)
2. Repeat same search should be faster (cache hit)
3. Cache expires after 6 hours 