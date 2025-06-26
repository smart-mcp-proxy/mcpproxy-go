# MCPProxy – Smart Proxy for AI Agents

<a href="https://mcpproxy.app" target="_blank" rel="noopener">🌐 Visit mcpproxy.app</a>

**MCPProxy** is an open-source desktop application that super-charges AI agents with intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

## Why MCPProxy?

- **Scale beyond API limits** – Federate hundreds of MCP servers while bypassing Cursor's 40-tool limit and OpenAI's 128-function cap.
- **Save tokens & accelerate responses** – Agents load just one `retrieve_tools` function instead of hundreds of schemas. Research shows ~99 % token reduction with **43 % accuracy improvement**.
- **Advanced security protection** – Automatic quarantine blocks Tool Poisoning Attacks until you manually approve new servers.
- **Works offline & cross-platform** – macOS, Windows, and Linux binaries with a native system-tray UI.

---

## Quick Start

### 1. Install

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

Click the tray icon → "Upstream Servers" or edit `mcp_config.json` (see below).

Optional: [One-click install into Cursor IDE](cursor://anysphere.cursor-deeplink/mcp/install?name=MCPProxy&config=eyJ0eXBlIjoiaHR0cCIsInVybCI6Imh0dHA6Ly9sb2NhbGhvc3Q6ODA4MC9tY3AvIn0=).

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
mcpproxy --help
  -c, --config <file>          path to mcp_config.json
  -l, --listen <addr>          listen address (":8080")
  -d, --data-dir <dir>         custom data directory
      --tray                   enable/disable system tray (default true)
      --log-level <level>      debug|info|warn|error
      --read-only              forbid config changes
      --disable-management     disable upstream_servers tool
      --allow-server-add       allow adding servers (default true)
      --allow-server-remove    allow removing servers (default true)
```

---

## Learn More

* Documentation: [Configuration](docs/configuration), [Features](docs/features), [Usage](docs/usage)
* Website: <https://mcpproxy.app>
* Releases: <https://github.com/smart-mcp-proxy/mcpproxy-go/releases>

## Contributing 🤝

We welcome issues, feature ideas, and PRs! Fork the repo, create a feature branch, and open a pull request. See `CONTRIBUTING.md` (coming soon) for guidelines. 