# Quickstart: Upstream Resources

1. Turn it on in `~/.mcpproxy/mcp_config.json` (hot-reloads):

   ```json
   { "aggregate_upstream_resources": true }
   ```

2. Optionally exclude one server: `{"name": "big-files", "expose_resources": false, ...}`.

3. List from any MCP client connected to `http://127.0.0.1:8080/mcp`, or with Inspector:

   ```bash
   npx @modelcontextprotocol/inspector --cli http://127.0.0.1:8080/mcp --method resources/list
   npx @modelcontextprotocol/inspector --cli http://127.0.0.1:8080/mcp --method resources/templates/list
   npx @modelcontextprotocol/inspector --cli http://127.0.0.1:8080/mcp --method resources/read --uri 'mcpproxy://upstream/everything-server/test://static/resource/1'
   ```

4. Check the record:

   ```bash
   mcpproxy activity list --type resource_read
   ```

Expected: each server's resources appear once under `mcpproxy://upstream/<server>/...`; quarantined or disabled servers and `skill://` addresses do not; the read returns the upstream content (after your output policy) and one `resource_read` row appears.
