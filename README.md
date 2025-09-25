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

**Prerelease Builds (Latest Features):**

Want to try the newest features? Download prerelease builds from the `next` branch:

1. Go to [GitHub Actions](https://github.com/smart-mcp-proxy/mcpproxy-go/actions)
2. Click the latest successful "Prerelease" workflow run
3. Download from **Artifacts**:
   - `dmg-darwin-arm64` (Apple Silicon Macs)
   - `dmg-darwin-amd64` (Intel Macs)
   - `versioned-linux-amd64`, `versioned-windows-amd64` (other platforms)

> **Note**: Prerelease builds are signed and notarized for macOS but contain cutting-edge features that may be unstable.

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
| `docker_isolation` | Docker security isolation settings (see below) | `enabled: false` |

### CLI Commands

**Main Commands:**
```bash
mcpproxy serve                      # Start proxy server with system tray
mcpproxy tools list --server=NAME  # Debug tool discovery for specific server
```

**Serve Command Flags:**
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

**Tools Command Flags:**
```text
mcpproxy tools list --help
  -s, --server <name>          upstream server name (required)
  -l, --log-level <level>      trace|debug|info|warn|error (default: info)
  -t, --timeout <duration>     connection timeout (default: 30s)
  -o, --output <format>        output format: table|json|yaml (default: table)
  -c, --config <file>          path to mcp_config.json
```

**Debug Examples:**
```bash
# List tools with trace logging to see all JSON-RPC frames
mcpproxy tools list --server=github-server --log-level=trace

# List tools with custom timeout for slow servers
mcpproxy tools list --server=slow-server --timeout=60s

# Output tools in JSON format for scripting
mcpproxy tools list --server=weather-api --output=json
```

---

## 🔐 Secrets Management

MCPProxy provides secure secrets management using your operating system's native keyring to store sensitive information like API keys, tokens, and credentials.

### ✨ **Key Features**
- **OS-native security**: Uses macOS Keychain, Linux Secret Service, or Windows Credential Manager
- **Placeholder expansion**: Automatically resolves `${keyring:secret_name}` placeholders in config files
- **Global access**: Secrets are shared across all MCPProxy configurations and data directories
- **CLI management**: Full command-line interface for storing, retrieving, and managing secrets

### 🔧 **Managing Secrets**

**Store a secret:**
```bash
# Interactive prompt (recommended for sensitive values)
mcpproxy secrets set github_token

# From command line (less secure - visible in shell history)
mcpproxy secrets set github_token "ghp_abcd1234..."

# From environment variable
mcpproxy secrets set github_token --from-env GITHUB_TOKEN
```

**List all secrets:**
```bash
mcpproxy secrets list
# Output: Found 3 secrets in keyring:
#   github_token
#   openai_api_key
#   database_password
```

**Retrieve a secret:**
```bash
mcpproxy secrets get github_token
```

**Delete a secret:**
```bash
mcpproxy secrets delete github_token
```

### 📝 **Using Placeholders in Configuration**

Use `${keyring:secret_name}` placeholders in your `mcp_config.json`:

```jsonc
{
  "mcpServers": [
    {
      "name": "github-mcp",
      "command": "uvx",
      "args": ["mcp-server-github"],
      "protocol": "stdio",
      "env": {
        "GITHUB_TOKEN": "${keyring:github_token}",
        "OPENAI_API_KEY": "${keyring:openai_api_key}"
      },
      "enabled": true
    },
    {
      "name": "database-server",
      "command": "python",
      "args": ["-m", "my_db_server", "--password", "${keyring:database_password}"],
      "protocol": "stdio",
      "enabled": true
    }
  ]
}
```

**Placeholder expansion works in:**
- ✅ Environment variables (`env` field)
- ✅ Command arguments (`args` field)
- ❌ Server names, commands, URLs (static fields)

### 🏗️ **Secret Storage Architecture**

**Storage Location:**
- **macOS**: Keychain Access (`/Applications/Utilities/Keychain Access.app`)
- **Linux**: Secret Service (GNOME Keyring, KDE Wallet, etc.)
- **Windows**: Windows Credential Manager

**Service Name:** All secrets are stored under the service name `"mcpproxy"`

**Global Scope:**
- ✅ Secrets are **shared across all MCPProxy instances** regardless of:
  - Configuration file location (`--config` flag)
  - Data directory (`--data-dir` flag)
  - Working directory
- ✅ Same secrets work across different projects and setups
- ⚠️ **No isolation** - all MCPProxy instances access the same keyring

### 🎯 **Best Practices for Multiple Projects**

If you use MCPProxy with multiple projects or environments, use descriptive secret names:

```bash
# Environment-specific secrets
mcpproxy secrets set prod_database_url
mcpproxy secrets set dev_database_url
mcpproxy secrets set staging_api_key

# Project-specific secrets
mcpproxy secrets set work_github_token
mcpproxy secrets set personal_github_token
mcpproxy secrets set client_a_api_key
```

Then reference them in your configs:
```jsonc
{
  "mcpServers": [
    {
      "name": "work-github",
      "env": {
        "GITHUB_TOKEN": "${keyring:work_github_token}"
      }
    },
    {
      "name": "personal-github",
      "env": {
        "GITHUB_TOKEN": "${keyring:personal_github_token}"
      }
    }
  ]
}
```

### 🔍 **Security Considerations**

- **Encrypted storage**: Secrets are encrypted by the OS keyring
- **Process isolation**: Other applications cannot access MCPProxy secrets without appropriate permissions
- **No file storage**: Secrets are never written to config files or logs
- **Audit trail**: OS keyring may provide access logs (varies by platform)

### 🐛 **Troubleshooting**

**Secret not found:**
```bash
# Verify secret exists
mcpproxy secrets list

# Check the exact secret name (case-sensitive)
mcpproxy secrets get your_secret_name
```

**Keyring access denied:**
- **macOS**: Grant MCPProxy access in `System Preferences > Security & Privacy > Privacy > Accessibility`
- **Linux**: Ensure your desktop session has an active keyring service
- **Windows**: Run MCPProxy with appropriate user permissions

**Placeholder not resolving:**
```bash
# Test secret resolution
mcpproxy secrets get your_secret_name

# Check logs for secret resolution errors
mcpproxy serve --log-level=debug
```

---

## 🐳 Docker Security Isolation

MCPProxy provides **Docker isolation** for stdio MCP servers to enhance security by running each server in its own isolated container:

### ✨ **Key Security Benefits**
- **Process Isolation**: Each MCP server runs in a separate Docker container
- **File System Isolation**: Servers cannot access host file system outside their container
- **Network Isolation**: Configurable network modes for additional security
- **Resource Limits**: Memory and CPU limits prevent resource exhaustion
- **Automatic Runtime Detection**: Detects Python, Node.js, Go, Rust environments automatically

### 🔧 **How It Works**
1. **Runtime Detection**: Automatically detects server type (uvx→Python, npx→Node.js, etc.)
2. **Container Selection**: Maps to appropriate Docker images with required tools
3. **Environment Passing**: Passes API keys and config via secure environment variables
4. **Git Support**: Uses full Docker images with Git for package installations from repositories

### 📝 **Docker Isolation Configuration**

Add to your `mcp_config.json`:

```jsonc
{
  "docker_isolation": {
    "enabled": true,
    "memory_limit": "512m",
    "cpu_limit": "1.0", 
    "timeout": "60s",
    "network_mode": "bridge",
    "default_images": {
      "python": "python:3.11",
      "uvx": "python:3.11",
      "node": "node:20",
      "npx": "node:20",
      "go": "golang:1.21-alpine"
    }
  },
  "mcpServers": [
    {
      "name": "isolated-python-server",
      "command": "uvx",
      "args": ["some-python-package"],
      "env": {
        "API_KEY": "your-api-key"
      },
      "enabled": true
      // Docker isolation applied automatically
    },
    {
      "name": "custom-isolation-server", 
      "command": "python",
      "args": ["-m", "my_server"],
      "isolation": {
        "enabled": true,
        "image": "custom-python:latest",
        "working_dir": "/app"
      },
      "enabled": true
    }
  ]
}
```

### 🎯 **Automatic Runtime Detection**

| Command | Detected Runtime | Docker Image |
|---------|------------------|--------------|
| `uvx` | Python with UV package manager | `python:3.11` |
| `npx` | Node.js with npm | `node:20` |
| `python`, `python3` | Python | `python:3.11` |
| `node` | Node.js | `node:20` |
| `go` | Go language | `golang:1.21-alpine` |
| `cargo` | Rust | `rust:1.75-slim` |

### 🔐 **Security Features**

- **Environment Variables**: API keys and secrets are passed securely to containers
- **Git Support**: Full images include Git for installing packages from repositories
- **No Docker-in-Docker**: Existing Docker servers are automatically excluded from isolation
- **Resource Limits**: Prevents runaway processes from consuming system resources
- **Network Isolation**: Containers run in isolated network environments

### 🐛 **Docker Isolation Debugging**

```bash
# Check which servers are using Docker isolation
mcpproxy serve --log-level=debug --tray=false | grep -i "docker isolation"

# Monitor Docker containers created by MCPProxy
docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"

# View container logs for a specific server
docker logs <container-id>
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

### 📂 **Working Directory Configuration**

Solve project context issues by specifying working directories for stdio MCP servers:

```jsonc
{
  "mcpServers": [
    {
      "name": "ast-grep-project-a",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "/home/user/projects/project-a",
      "enabled": true
    },
    {
      "name": "git-work-repo",
      "command": "npx",
      "args": ["@modelcontextprotocol/server-git"],
      "working_dir": "/home/user/work/company-repo",
      "enabled": true
    }
  ]
}
```

**Benefits**:
- **Project isolation**: File-based servers operate in correct directory context
- **Multiple projects**: Same MCP server type for different projects  
- **Context separation**: Work and personal project isolation

**Tool-based Management**:
```bash
# Add server with working directory
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"add","name":"git-myproject","command":"npx","args_json":"[\"@modelcontextprotocol/server-git\"]","working_dir":"/home/user/projects/myproject","enabled":true}'

# Update existing server working directory
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"update","name":"git-myproject","working_dir":"/new/project/path"}'
```

## Learn More

* Documentation: [Configuration](https://mcpproxy.app/docs/configuration), [Features](https://mcpproxy.app/docs/features), [Usage](https://mcpproxy.app/docs/usage)
* Website: <https://mcpproxy.app>
* Releases: <https://github.com/smart-mcp-proxy/mcpproxy-go/releases>

## Contributing 🤝

We welcome issues, feature ideas, and PRs! Fork the repo, create a feature branch, and open a pull request. See `CONTRIBUTING.md` (coming soon) for guidelines. 