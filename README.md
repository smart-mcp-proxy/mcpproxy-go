# MCPProxy – Smart Proxy for AI Agents

**MCPProxy** is an open-source desktop application that super-charges AI agents with intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

[![MCPProxy Demo](https://img.youtube.com/vi/l4hh6WOuSFM/0.jpg)](https://youtu.be/l4hh6WOuSFM)

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


## Why MCPProxy?

- **Scale beyond API limits** – Federate hundreds of MCP servers while bypassing Cursor's 40-tool limit and OpenAI's 128-function cap.
- **Save tokens & accelerate responses** – Agents load just one `retrieve_tools` function instead of hundreds of schemas. Research shows ~99 % token reduction with **43 % accuracy improvement**.
- **Advanced security protection** – Automatic quarantine blocks Tool Poisoning Attacks until you manually approve new servers.
- **Works offline & cross-platform** – Native binaries for macOS (Intel & Apple Silicon), Windows (x64 & ARM64), and Linux (x64 & ARM64) with system-tray UI.

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

Manual download (all platforms):
- **Linux**: [AMD64](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-linux-amd64.tar.gz) | [ARM64](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-linux-arm64.tar.gz)
- **Windows**: [AMD64](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-windows-amd64.zip) | [ARM64](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-windows-arm64.zip)
- **macOS**: [Intel](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-darwin-amd64.tar.gz) | [Apple Silicon](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-darwin-arm64.tar.gz)

Anywhere with Go 1.22+:
```bash
go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest
```

### 2. Run

```bash
mcpproxy serve          # starts HTTP server on :8080 and shows tray
```

### 3. Add servers

Edit `mcp_config.json` (see below). Or **ask LLM** to add servers (see [doc](https://mcpproxy.app/docs/configuration#adding-servers)).

### 4. Connect to your IDE/AI tool

📖 **[Complete Setup Guide](docs/setup.md)** - Detailed instructions for Cursor, VS Code, Claude Desktop, and Goose

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
    { "name": "local-python", "command": "python", "args": ["-m", "my_server"], "type": "stdio", "enabled": true },
    { "name": "remote-http", "url": "http://localhost:3001", "type": "http", "enabled": true }
  ]
}
```

### Key parameters

| Field | Description | Default |
|-------|-------------|---------|
| `listen` | Address the proxy listens on | `:8080` |
| `data_dir` | Folder for config, DB & logs | `~/.mcpproxy` |
| `enable_tray` | Show native system-tray UI | `true` |
| `top_k` | Tools returned by `retrieve_tools` | `5` |
| `tools_limit` | Max tools returned to client | `15` |
| `tool_response_limit` | Auto-truncate responses above N chars (`0` disables) | `20000` |

### CLI flags

```text
mcpproxy serve --help
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

## OAuth Authentication Support

MCPProxy provides **seamless OAuth 2.1 authentication** for MCP servers that require user authorization (like Cloudflare AutoRAG, GitHub, etc.):

### ✨ **Key Features**
- **RFC 8252 Compliant**: Dynamic port allocation for secure callback handling
- **PKCE Security**: Proof Key for Code Exchange for enhanced security
- **Auto Browser Launch**: Opens your default browser for authentication
- **Dynamic Client Registration**: Automatic client registration with OAuth servers
- **Token Management**: Automatic token refresh and storage

### 🔄 **How It Works**
1. **Add OAuth Server**: Configure an OAuth-enabled MCP server in your config
2. **Auto Authentication**: MCPProxy detects when OAuth is required (401 response)
3. **Browser Opens**: Your default browser opens to the OAuth provider's login page
4. **Dynamic Callback**: MCPProxy starts a local callback server on a random port
5. **Token Exchange**: Authorization code is automatically exchanged for access tokens
6. **Ready to Use**: Server becomes available for tool calls immediately

### 📝 **OAuth Server Configuration**

> **Note**: The `"oauth"` configuration is **optional**. MCPProxy will automatically detect when OAuth is required and use sensible defaults in most cases. You only need to specify OAuth settings if you want to customize scopes or have pre-registered client credentials.

```jsonc
{
  "mcpServers": [
    {
      "name": "cloudflare_autorag",
      "url": "https://autorag.mcp.cloudflare.com/mcp",
      "protocol": "streamable-http",
      "enabled": true,
      "oauth": {
        "scopes": ["mcp.read", "mcp.write"],
        "pkce_enabled": true
      }
    }
  ]
}
```

**OAuth Configuration Options** (all optional):
- `scopes`: OAuth scopes to request (default: `["mcp.read", "mcp.write"]`)
- `pkce_enabled`: Enable PKCE for security (default: `true`, recommended)
- `client_id`: Pre-registered client ID (optional, uses Dynamic Client Registration if empty)
- `client_secret`: Client secret (optional, for confidential clients)

### 🔧 **OAuth Debugging**

Enable debug logging to see the complete OAuth flow:

```bash
mcpproxy serve --log-level=debug --tray=false
```

Check logs for OAuth flow details:
```bash
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(oauth|OAuth)"
```

## Learn More

* Documentation: [Configuration](https://mcpproxy.app/docs/configuration), [Features](https://mcpproxy.app/docs/features), [Usage](https://mcpproxy.app/docs/usage)
* Website: <https://mcpproxy.app>
* Releases: <https://github.com/smart-mcp-proxy/mcpproxy-go/releases>

## Contributing 🤝

We welcome issues, feature ideas, and PRs! Fork the repo, create a feature branch, and open a pull request. See `CONTRIBUTING.md` (coming soon) for guidelines. 