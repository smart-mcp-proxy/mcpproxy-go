# MCPProxy – Smart Proxy for AI Agents

<a href="https://mcpproxy.app" target="_blank" rel="noopener">🌐 Visit mcpproxy.app</a>

<div align="center">
  <a href="https://mcpproxy.app/images/menu_upstream_servers.png" target="_blank">
    <img src="https://mcpproxy.app/images/menu_upstream_servers.png" alt="System Tray - Upstream Servers" width="250" />
  </a>
  &nbsp;&nbsp;&nbsp;&nbsp;
  <a href="https://mcpproxy.app/images/menu_security_quarantine.png" target="_blank">
    <img src="https://mcpproxy.app/images/menu_security_quarantine.png" alt="System Tray - Quarantine Management" width="250" />
  </a>
  <br />
  <em>System Tray - Upstream Servers &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp; System Tray - Quarantine Management</em>
</div>

**MCPProxy** is an open-source desktop application that super-charges AI agents with intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

## Why MCPProxy?

- **Scale beyond API limits** – Federate hundreds of MCP servers while bypassing Cursor's 40-tool limit and OpenAI's 128-function cap.
- **Save tokens & accelerate responses** – Agents load just one `retrieve_tools` function instead of hundreds of schemas. Research shows ~99 % token reduction with **43 % accuracy improvement**.
- **Advanced security protection** – Automatic quarantine blocks Tool Poisoning Attacks until you manually approve new servers.
- **OAuth & authentication support** – Built-in OAuth 2.0 flows including Device Code Flow for remote/headless deployments.
- **Multiple transport protocols** – HTTP, SSE, Streamable HTTP, and stdio with custom headers support.
- **Works offline & cross-platform** – macOS, Windows, and Linux binaries with a native system-tray UI.

---

## Quick Start

### 1. Install

**macOS (Recommended - DMG Installer):**

Download the latest DMG installer for your architecture:
- **Apple Silicon (M1/M2):** [Download DMG](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest) → `mcpproxy-*-darwin-arm64.dmg`
- **Intel Mac:** [Download DMG](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest) → `mcpproxy-*-darwin-amd64.dmg`

**Alternative install methods:**

macOS (Homebrew):
```bash
brew install smart-mcp-proxy/mcpproxy/mcpproxy
```

Anywhere with Go 1.22+:
```bash
go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest
```

### 2. Run

```bash
mcpproxy                # starts HTTP server on :8080 and shows tray
```

### 3. Add servers

Edit `mcp_config.json` (see below). Or ask LLM to add servers (see [doc](https://mcpproxy.app/docs/configuration#adding-servers)).

## Add proxy to Cursor

### One-click install into Cursor IDE

[![Install in Cursor IDE](https://img.shields.io/badge/Install_in_Cursor-3e44fe?logo=data:image/svg+xml;base64,PHN2ZyB2aWV3Qm94P…&style=for-the-badge)](https://mcpproxy.app/cursor-install.html)

### Manual install


1. Open Cursor Settings
2. Click "Tools & Integrations"
3. Add MCP server
```json
    "MCPProxy": {
      "type": "http",
      "url": "http://localhost:8080/mcp/"
    }
```

---

## Minimal configuration (`~/.mcpproxy/mcp_config.json`)

```jsonc
{
  "listen": ":8080",
  "data_dir": "~/.mcpproxy",
  "enable_tray": true,

  // Search & tool limits
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,

  "mcpServers": [
    { "name": "local-python", "command": "python", "args": ["-m", "my_server"], "protocol": "stdio", "enabled": true },
    { "name": "remote-http", "url": "http://localhost:3001", "protocol": "http", "enabled": true }
  ]
}
```

## Server Configuration Options

### Basic Server Types

#### Stdio Servers (Local Process)
```jsonc
{
  "name": "local-python",
  "command": "python",
  "args": ["-m", "my_server"],
  "protocol": "stdio",
  "env": { "API_KEY": "your_key" },
  "enabled": true
}
```

#### HTTP/HTTPS Servers
```jsonc
{
  "name": "remote-api",
  "url": "https://api.example.com/mcp",
  "protocol": "http",
  "headers": { 
    "Authorization": "Bearer your_token",
    "X-API-Key": "your_key"
  },
  "enabled": true
}
```

#### Server-Sent Events (SSE) Servers
```jsonc
{
  "name": "sse-server",
  "url": "https://api.example.com/sse",
  "protocol": "sse",
  "headers": { "Authorization": "Bearer your_token" },
  "enabled": true
}
```

#### Streamable HTTP Servers
```jsonc
{
  "name": "streaming-server",
  "url": "https://api.example.com/stream",
  "protocol": "streamable-http",
  "headers": { "Authorization": "Bearer your_token" },
  "enabled": true
}
```

### OAuth-Enabled Servers

MCPProxy supports OAuth 2.0 authentication with automatic token management. For complete OAuth documentation, see [OAuth Authentication](docs/oauth.md).

#### OAuth Device Code Flow (Recommended for Remote/Headless)
```jsonc
{
  "name": "oauth-server",
  "url": "https://api.example.com/mcp",
  "protocol": "http",
  "oauth": {
    "flow_type": "device_code",
    "client_id": "your_client_id",
    "device_endpoint": "https://api.example.com/oauth/device",
    "token_endpoint": "https://api.example.com/oauth/token",
    "scopes": ["read", "write"],
    "device_flow": {
      "poll_interval": "5s",
      "enable_notification": true
    }
  },
  "enabled": true
}
```

#### OAuth Authorization Code Flow
```jsonc
{
  "name": "oauth-server",
  "url": "https://api.example.com/mcp",
  "protocol": "http",
  "oauth": {
    "flow_type": "authorization_code",
    "client_id": "your_client_id",
    "client_secret": "your_client_secret",
    "authorization_endpoint": "https://api.example.com/oauth/authorize",
    "token_endpoint": "https://api.example.com/oauth/token",
    "scopes": ["read", "write"]
  },
  "enabled": true
}
```

> **Note**: OAuth tokens are automatically refreshed and stored securely. For remote deployments, use `device_code` flow - users authenticate on their own devices while mcpproxy runs headless.

### Key parameters

| Field | Description | Default |
|-------|-------------|---------|
| `listen` | Address the proxy listens on | `:8080` |
| `data_dir` | Folder for config, DB & logs | `~/.mcpproxy` |
| `enable_tray` | Show native system-tray UI | `true` |
| `top_k` | Tools returned by `retrieve_tools` | `5` |
| `tools_limit` | Max tools returned to client | `15` |
| `tool_response_limit` | Auto-truncate responses above N chars (`0` disables) | `20000` |

### Server Configuration Fields

| Field | Required | Description | Example |
|-------|----------|-------------|---------|
| `name` | Yes | Unique server identifier | `"my-server"` |
| `protocol` | Yes | Transport protocol | `"stdio"`, `"http"`, `"sse"`, `"streamable-http"` |
| `command` | stdio only | Command to execute | `"python"` |
| `args` | stdio only | Command arguments | `["-m", "my_server"]` |
| `env` | stdio only | Environment variables | `{"API_KEY": "value"}` |
| `url` | http/sse only | Server endpoint URL | `"https://api.example.com/mcp"` |
| `headers` | http/sse only | HTTP headers for authentication | `{"Authorization": "Bearer token"}` |
| `oauth` | Optional | OAuth 2.0 configuration | See OAuth examples above |
| `enabled` | Optional | Enable/disable server | `true` |

### CLI flags

```text
mcpproxy --help
  -c, --config <file>          path to mcp_config.json
  -l, --listen <addr>          listen address (":8080")
  -d, --data-dir <dir>         custom data directory
      --tray                   enable/disable system tray (default true, use --tray=false to disable)
      --log-level <level>      debug|info|warn|error
      --read-only              forbid config changes
      --disable-management     disable upstream_servers tool
      --allow-server-add       allow adding servers (default true)
      --allow-server-remove    allow removing servers (default true)
```

---

## Learn More

* Documentation: [Configuration](https://mcpproxy.app/docs/configuration), [OAuth Authentication](docs/oauth.md), [Logging](docs/logging.md), [Features](https://mcpproxy.app/docs/features), [Usage](https://mcpproxy.app/docs/usage)
* Website: <https://mcpproxy.app>
* Releases: <https://github.com/smart-mcp-proxy/mcpproxy-go/releases>

## Contributing 🤝

We welcome issues, feature ideas, and PRs! Fork the repo, create a feature branch, and open a pull request. See `CONTRIBUTING.md` (coming soon) for guidelines. 