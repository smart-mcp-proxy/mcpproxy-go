---
id: config-import
title: Configuration Import
sidebar_label: Config Import
sidebar_position: 8
description: Import MCP server configurations from Claude Desktop, Claude Code, Cursor IDE, Codex CLI, and Gemini CLI
keywords: [import, configuration, migration, claude desktop, claude code, cursor, codex, gemini]
---

# Configuration Import

MCPProxy can import MCP server configurations from popular AI tools, making it easy to migrate your existing setups or consolidate servers from multiple sources.

## Supported Formats

| Source | Format | File Location |
|--------|--------|---------------|
| Claude Desktop | JSON | macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`<br/>Windows: `%APPDATA%\Claude\claude_desktop_config.json`<br/>Linux: `~/.config/Claude/claude_desktop_config.json` |
| Claude Code | JSON | `~/.claude.json` (all platforms) |
| Cursor IDE | JSON | `~/.cursor/mcp.json` (all platforms) |
| Codex CLI | TOML | `~/.codex/config.toml` (all platforms) |
| Gemini CLI | JSON | `~/.gemini/settings.json` (all platforms) |

## Web UI Import

### Quick Import Panel

The Web UI provides a **Quick Import** panel that automatically detects installed AI tools on your system:

1. Open MCPProxy Web UI
2. Click **Add Server** → **Import** tab
3. The **Quick Import - Found Configs** panel shows detected configurations
4. Click **Import** next to any found config to preview servers
5. Select which servers to import and click **Import N Servers**

The panel shows:
- **Found** (green badge): Config file exists, click Import to load
- **Not found** (gray): Config file not present on this system

### Manual File Upload

You can also upload configuration files manually:

1. Click **Add Server** → **Import** → **Upload File**
2. Click **Choose File** and select your config file
3. Format is auto-detected, or select manually from dropdown
4. Preview shows detected servers with details
5. Select servers and click **Import**

### Paste Content

For quick imports without file access:

1. Click **Add Server** → **Import** → **Paste Content**
2. Paste JSON or TOML configuration content
3. Content is validated with line numbers shown for errors
4. Preview and import selected servers

## CLI Import

### Import from File Path

```bash
# Import from Claude Desktop config
mcpproxy import --path ~/Library/Application\ Support/Claude/claude_desktop_config.json

# Import from Claude Code config
mcpproxy import --path ~/.claude.json

# Import with format hint (if auto-detect fails)
mcpproxy import --path config.json --format claude-desktop

# Preview without importing
mcpproxy import --path config.json --preview
```

### Import Specific Servers

```bash
# Import only specific servers by name
mcpproxy import --path config.json --servers "github-server,filesystem"
```

## REST API

### Get Canonical Config Paths

Returns well-known configuration paths for the current OS with existence status:

```bash
curl -H "X-API-Key: your-key" \
  http://127.0.0.1:8080/api/v1/servers/import/paths
```

Response:
```json
{
  "success": true,
  "data": {
    "os": "darwin",
    "paths": [
      {
        "name": "Claude Desktop",
        "format": "claude-desktop",
        "path": "/Users/user/Library/Application Support/Claude/claude_desktop_config.json",
        "exists": true,
        "description": "Claude Desktop app configuration"
      },
      {
        "name": "Claude Code (User)",
        "format": "claude-code",
        "path": "/Users/user/.claude.json",
        "exists": true,
        "description": "Claude Code user-level MCP servers"
      }
    ]
  }
}
```

### Import from Path

Import servers by reading a file from the server's filesystem:

```bash
# Preview import
curl -X POST -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{"path": "/Users/user/.claude.json", "format": "claude-code"}' \
  "http://127.0.0.1:8080/api/v1/servers/import/path?preview=true"

# Actual import
curl -X POST -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{"path": "/Users/user/.claude.json"}' \
  http://127.0.0.1:8080/api/v1/servers/import/path
```

### Import from Content

Import by posting configuration content directly:

```bash
curl -X POST -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{
    "content": "{\"mcpServers\": {\"my-server\": {\"command\": \"npx\", \"args\": [\"-y\", \"@modelcontextprotocol/server-filesystem\"]}}}",
    "format": "claude-desktop"
  }' \
  http://127.0.0.1:8080/api/v1/servers/import/json
```

### Upload File

Import by uploading a multipart form file:

```bash
curl -X POST -H "X-API-Key: your-key" \
  -F "file=@/path/to/config.json" \
  "http://127.0.0.1:8080/api/v1/servers/import?preview=true"
```

## Import Response

All import endpoints return a consistent response format:

```json
{
  "success": true,
  "data": {
    "format": "claude-desktop",
    "format_name": "Claude Desktop",
    "summary": {
      "total": 5,
      "imported": 3,
      "skipped": 1,
      "failed": 1
    },
    "imported": [
      {
        "name": "filesystem",
        "protocol": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem"],
        "source_format": "claude-desktop",
        "original_name": "filesystem"
      }
    ],
    "skipped": [
      {
        "name": "existing-server",
        "reason": "Server with this name already exists"
      }
    ],
    "failed": [
      {
        "name": "invalid-server",
        "reason": "Missing required field: command",
        "original_config": {}
      }
    ],
    "warnings": ["Server 'github' has no tools configured"]
  }
}
```

## Format Detection

MCPProxy auto-detects the configuration format by analyzing:

1. **File extension**: `.toml` → Codex CLI format
2. **JSON structure**:
   - `mcpServers` object → Claude Desktop
   - Top-level `mcpServers` with `env` at server level → Claude Code
   - Root-level server definitions → Cursor IDE
   - `mcpServers` in `tools` section → Gemini CLI

You can override auto-detection with the `format` parameter:
- `claude-desktop`
- `claude-code`
- `cursor`
- `codex`
- `gemini`

## Handling Duplicates

When importing, MCPProxy checks for existing servers by name:

- **Existing servers are skipped** by default
- Skipped servers appear in the `skipped` array with reason
- Use the preview mode to see what will be imported before committing

## Security Considerations

- Imported servers are **quarantined by default** for security review
- Review imported server configurations before enabling them
- Environment variables and secrets are imported as-is; consider using secret references
- File paths are validated to prevent directory traversal attacks
