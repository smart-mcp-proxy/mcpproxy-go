# to memory

## MCPProxy Debugging Methodology

### Debugging mcpproxy tool call hanging issues

**Problem**: CLI commands hang when `mcpproxy serve` is running due to DB locking
**Root Cause**: BBolt database locked by serve process, CLI can't access storage

**Debugging Steps**:
1. Add comprehensive debug logs with `DEBUG_HANG:` prefix to trace execution flow
2. Start background log monitoring: `tail -f ~/Library/Logs/mcpproxy/main.log | grep "DEBUG_HANG" &`
3. Start mcpproxy serve: `./mcpproxy serve &`
4. Test via MCP protocol directly using `mcp__mcpproxy__call_tool` instead of CLI
5. Monitor logs to see where execution hangs or succeeds

**Key Discovery**:
- ✅ **MCP Protocol Direct**: Works perfectly, no hanging
- ❌ **CLI Command Path**: Hangs due to DB access conflict
- The core server logic is working correctly
- CLI commands need to be made independent from DB when serve is running

**Commands for testing**:
```bash
# Start log monitoring
tail -f ~/Library/Logs/mcpproxy/main.log | grep "DEBUG_HANG" &

# Start server
./mcpproxy serve &

# Test via MCP protocol (works)
mcp__mcpproxy__call_tool with upstream_servers operation

# Test via CLI (hangs due to DB lock)
./mcpproxy call tool --tool-name=upstream_servers --json_args='{}'
```

**Log locations**:
- Main log: `~/Library/Logs/mcpproxy/main.log`
- Server-specific logs: `~/Library/Logs/mcpproxy/server-<servername>.log`

**TODO**: Make CLI tool calls independent from DB when serve is running, possibly by:
- Using HTTP API calls to running server instead of direct DB access
- Implementing read-only DB access for CLI
- Adding DB connection sharing or pooling