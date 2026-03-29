# Feature Specification: Connect Clients & Dashboard Visual Redesign

**Feature Branch**: `039-connect-and-dashboard`
**Created**: 2026-03-28
**Status**: Approved
**Input**: Two related features: (1) "Connect" feature that modifies AI client configs to register MCPProxy as an MCP server, (2) Visual dashboard redesign showing MCPProxy as a central hub connecting clients to upstream servers.

## Assumptions (No Clarification Needed)

1. **Connect modifies user-level configs only** — not workspace-level or project-level configs
2. **Backup before modify** — always create `.bak` timestamped backup before touching any client config
3. **HTTP transport preferred** — MCPProxy exposes `/mcp` endpoint, so we use `url: "http://127.0.0.1:{port}/mcp"` for all clients that support HTTP/SSE
4. **Stdio fallback not needed** — all modern clients support HTTP transport; we won't generate stdio configs
5. **API key included in URL** — if MCPProxy has an API key configured, include it as query param in the URL for clients that don't support headers
6. **Dashboard replaces current view** — the new visual layout replaces the existing Dashboard.vue entirely
7. **No new backend storage** — connect feature uses filesystem operations only; dashboard uses existing API endpoints
8. **Supported clients for connect**: Claude Code, Claude Desktop, Cursor IDE, Windsurf, VS Code (Copilot), Codex CLI, Gemini CLI
9. **Windsurf path**: `~/.codeium/windsurf/mcp_config.json` (same JSON format as Cursor)
10. **VS Code path**: `~/Library/Application Support/Code/User/mcp.json` on macOS (uses `servers` key, not `mcpServers`)

## Feature 1: Connect Clients

### Overview

A single REST API endpoint + CLI command + Web UI button that writes MCPProxy's connection details into a client's MCP configuration file. The inverse of "import" — instead of pulling configs from clients, we push our config to clients.

### Backend: REST API

**Endpoint**: `POST /api/v1/connect/{client}`

**Path parameter**: `client` — one of: `claude-code`, `claude-desktop`, `cursor`, `windsurf`, `vscode`, `codex`, `gemini`

**Request body** (optional):
```json
{
  "server_name": "mcpproxy",
  "force": false
}
```

- `server_name`: name to register as (default: "mcpproxy")
- `force`: overwrite if entry already exists (default: false)

**Response** (success):
```json
{
  "success": true,
  "client": "claude-code",
  "config_path": "/Users/user/.claude.json",
  "backup_path": "/Users/user/.claude.json.bak.20260328-161800",
  "server_name": "mcpproxy",
  "action": "created",
  "message": "MCPProxy registered in Claude Code configuration"
}
```

**Response** (already exists, force=false):
```json
{
  "success": false,
  "error": "already_exists",
  "client": "claude-code",
  "config_path": "/Users/user/.claude.json",
  "server_name": "mcpproxy",
  "message": "MCPProxy is already registered in Claude Code. Use force=true to overwrite."
}
```

**Endpoint**: `GET /api/v1/connect`

Returns status of all supported clients:
```json
{
  "listen_url": "http://127.0.0.1:8080/mcp",
  "clients": [
    {
      "id": "claude-code",
      "name": "Claude Code",
      "config_path": "/Users/user/.claude.json",
      "exists": true,
      "connected": true,
      "icon": "claude-code"
    },
    {
      "id": "cursor",
      "name": "Cursor IDE",
      "config_path": "/Users/user/.cursor/mcp.json",
      "exists": true,
      "connected": false,
      "icon": "cursor"
    }
  ]
}
```

- `exists`: config file exists on disk
- `connected`: MCPProxy is already registered in that config

**Endpoint**: `DELETE /api/v1/connect/{client}`

Removes MCPProxy entry from client config (with backup). Disconnect operation.

### Client Config Formats

| Client | Key | MCPProxy Entry |
|--------|-----|----------------|
| Claude Code | `mcpServers.mcpproxy` | `{"type": "http", "url": "http://127.0.0.1:8080/mcp"}` |
| Claude Desktop | `mcpServers.mcpproxy` | `{"command": "curl", "args": ["http://127.0.0.1:8080/mcp"]}` — **Skip**: Desktop only supports stdio. Mark as "not supported" |
| Cursor | `mcpServers.mcpproxy` | `{"url": "http://127.0.0.1:8080/mcp", "type": "sse"}` |
| Windsurf | `mcpServers.mcpproxy` | `{"serverUrl": "http://127.0.0.1:8080/mcp", "type": "sse"}` |
| VS Code | `servers.mcpproxy` | `{"type": "http", "url": "http://127.0.0.1:8080/mcp"}` |
| Codex | `[mcp_servers.mcpproxy]` | `url = "http://127.0.0.1:8080/mcp"` (TOML) |
| Gemini | `mcpServers.mcpproxy` | `{"httpUrl": "http://127.0.0.1:8080/mcp"}` |

**Claude Desktop note**: Since it only supports stdio, we skip it from the connect feature. The GET endpoint returns it with `supported: false` and reason "stdio only — use import instead".

### Safety

1. **Backup**: Before any write, copy original to `{path}.bak.{YYYYMMDD-HHMMSS}`
2. **Atomic write**: Write to temp file, then rename
3. **Preserve formatting**: Read, parse, modify, marshal with indent
4. **Config file creation**: If config file doesn't exist but parent dir does, create it with minimal valid content
5. **Validation**: After write, re-read and verify the entry exists

### CLI Command

```bash
mcpproxy connect claude-code          # Register in Claude Code
mcpproxy connect --list               # Show all client statuses
mcpproxy connect --all                # Register in all supported clients
mcpproxy disconnect cursor            # Remove from Cursor
```

## Feature 2: Dashboard Visual Redesign

### Layout (matching wireframe)

The dashboard shows MCPProxy as a central hub with three zones:

```
┌─────────────────────────────────────────────────────────────────┐
│  [Telemetry Banner]  [Attention Banner]  (kept as-is)           │
├──────────────┬──────────────────────┬───────────────────────────┤
│              │                      │                           │
│  AI Agents   │    MCPProxy Hub      │   Upstream Servers        │
│  (left)      │    (center)          │   (right)                 │
│              │                      │                           │
│  ┌─────────┐ │   ┌──────────────┐   │  ┌─────────────────────┐ │
│  │ Claude  │→│   │  ◇ MCPProxy  │   │→ │ 20 connected        │ │
│  │ Code    │ │   │   active     │   │  │ 200 tools           │ │
│  └─────────┘ │   │  (uptime 22h)│   │  │ 3 disabled          │ │
│  ┌─────────┐ │   │              │   │  └─────────────────────┘ │
│  │Antigrav │→│   │  96% tokens  │   │  ┌─────────────────────┐ │
│  │ity      │ │   │   saved      │   │→ │ 2 in quarantine     │ │
│  └─────────┘ │   └──────────────┘   │  └─────────────────────┘ │
│              │                      │                           │
│  [connect]   │                      │  [Add server]             │
│  [import]    │  [Activity Log]      │  [Security Scan]          │
│  [sessions]  │  (1279 records)      │  (coming soon)            │
│              │                      │                           │
├──────────────┴──────────────────────┴───────────────────────────┤
│  [Token Savings collapsed / Hints panel]                        │
└─────────────────────────────────────────────────────────────────┘
```

### Left Panel: AI Agents / Clients

- **Connected clients**: Show recent MCP sessions with client name, status badge
- **Action buttons**:
  - "Connect Clients" → opens connect modal (Feature 1 UI)
  - "Import Servers" → opens existing AddServerModal import tab
  - "Recent Sessions" → links to /sessions

### Center Panel: MCPProxy Hub

- **Diamond/hexagon shape** with MCPProxy logo or icon
- **Status**: "active" with green glow, or "stopped" with red
- **Uptime**: calculated from server start time
- **Token savings badge**: "96% tokens saved" in accent circle above
- **Activity log stat**: "Activity Log (N records)" below — links to /activity

### Right Panel: Upstream Servers

- **Stats cards**:
  - Connected count / total tools (from server store)
  - Disabled count
  - Quarantined count (from server store)
- **Action buttons**:
  - "Add Server" → opens AddServerModal
  - "Security Scan (coming soon)" → disabled badge/placeholder

### Data Sources (all existing)

| Data | API Endpoint | Store |
|------|-------------|-------|
| Server counts | `GET /api/v1/servers` | `serversStore.serverCount` |
| Token savings | `GET /api/v1/stats/tokens` | fetched in dashboard |
| Recent sessions | `GET /api/v1/sessions?limit=5` | fetched in dashboard |
| Activity count | `GET /api/v1/activity/summary` | fetched in dashboard |
| Uptime | `GET /api/v1/status` | `systemStore.status` |
| Client connect status | `GET /api/v1/connect` | new fetch in dashboard |

## User Scenarios & Testing

### Story 1 - Connect Claude Code via Web UI (P1)

A user opens the dashboard, sees Claude Code in the clients panel, clicks "Connect", and MCPProxy registers itself in `~/.claude.json`.

**Acceptance**:
1. Given MCPProxy is running, When user clicks Connect on Claude Code, Then `~/.claude.json` is backed up and mcpproxy entry is added
2. Given mcpproxy is already in the config, When user clicks Connect, Then a message says "already connected"
3. Given the config file doesn't exist, When user clicks Connect, Then a new config file is created

### Story 2 - Connect All Clients via CLI (P2)

```bash
mcpproxy connect --all
```

**Acceptance**:
1. Given Claude Code and Cursor configs exist, When `connect --all`, Then both are updated with backup
2. Given VS Code config doesn't exist, When `connect --all`, Then it's created in the correct location

### Story 3 - Dashboard Visual Overview (P1)

User opens the Web UI and sees the hub visualization with live data.

**Acceptance**:
1. Given 20 servers connected with 200 tools, When dashboard loads, Then right panel shows "20 connected / 200 tools"
2. Given 2 servers in quarantine, When dashboard loads, Then quarantine stat is visible
3. Given 3 active sessions, When dashboard loads, Then left panel shows client names

### Story 4 - Disconnect Client (P2)

User clicks disconnect on a connected client.

**Acceptance**:
1. Given mcpproxy is in Claude Code config, When disconnect clicked, Then entry is removed with backup
2. Status updates to show "not connected"
