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

**Windows (Recommended - Installer):**

Download the latest Windows installer for your architecture:
- **x64 (64-bit):** [Download Installer](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest) → `mcpproxy-setup-*-amd64.exe`
- **ARM64:** [Download Installer](https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest) → `mcpproxy-setup-*-arm64.exe`

The installer automatically:
- Installs both `mcpproxy.exe` (core server) and `mcpproxy-tray.exe` (system tray app) to Program Files
- Adds MCPProxy to your system PATH for command-line access
- Creates Start Menu shortcuts
- Supports silent installation: `.\mcpproxy-setup.exe /VERYSILENT`

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
  "listen": "127.0.0.1:8080",   // Localhost-only by default for security
  "data_dir": "~/.mcpproxy",
  "enable_tray": true,

  // Search & tool limits
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,

  // Optional HTTPS configuration (disabled by default)
  "tls": {
    "enabled": false,          // Set to true to enable HTTPS
    "require_client_cert": false,
    "hsts": true
  },

  "mcpServers": [
    { "name": "local-python", "command": "python", "args": ["-m", "my_server"], "protocol": "stdio", "enabled": true },
    { "name": "remote-http", "url": "http://localhost:3001", "protocol": "http", "enabled": true }
  ]
}
```

### Key parameters

| Field | Description | Default |
|-------|-------------|---------|
| `listen` | Address the proxy listens on | `127.0.0.1:8080` |
| `data_dir` | Folder for config, DB & logs | `~/.mcpproxy` |
| `enable_tray` | Show native system-tray UI | `true` |
| `top_k` | Tools returned by `retrieve_tools` | `5` |
| `tools_limit` | Max tools returned to client | `15` |
| `tool_response_limit` | Auto-truncate responses above N chars (`0` disables) | `20000` |
| `tls.enabled` | Enable HTTPS with local CA certificates | `false` |
| `tls.require_client_cert` | Enable mutual TLS (mTLS) for client authentication | `false` |
| `tls.certs_dir` | Custom directory for TLS certificates | `{data_dir}/certs` |
| `tls.hsts` | Send HTTP Strict Transport Security headers | `true` |
| `docker_isolation` | Docker security isolation settings (see below) | `enabled: false` |

### CLI Commands

**Main Commands:**
```bash
mcpproxy serve                      # Start proxy server with system tray
mcpproxy tools list --server=NAME  # Debug tool discovery for specific server
mcpproxy trust-cert                 # Install CA certificate as trusted (for HTTPS)
```

**Serve Command Flags:**
```text
mcpproxy serve --help
  -c, --config <file>              path to mcp_config.json
  -l, --listen <addr>              listen address for HTTP mode
  -d, --data-dir <dir>             custom data directory
      --log-level <level>          trace|debug|info|warn|error
      --log-to-file                enable logging to file in standard OS location
      --read-only                  enable read-only mode
      --disable-management         disable management features
      --allow-server-add           allow adding new servers (default true)
      --allow-server-remove        allow removing existing servers (default true)
      --enable-prompts             enable prompts for user input (default true)
      --tool-response-limit <num>  tool response limit in characters (0 = disabled)
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

## 🔄 Docker Recovery

MCPProxy includes **intelligent Docker recovery** that automatically detects and handles Docker engine outages:

### ✨ **Key Features**
- **Automatic Detection**: Monitors Docker health every 2-60 seconds with exponential backoff
- **Graceful Reconnection**: Automatically reconnects all Docker-based servers when Docker recovers
- **System Notifications**: Native notifications keep you informed of recovery progress
- **Container Cleanup**: Removes orphaned containers on shutdown
- **Zero Configuration**: Works out-of-the-box with sensible defaults

### 🔧 **How It Works**
1. **Health Monitoring**: Continuously checks Docker engine availability
2. **Failure Detection**: Detects when Docker becomes unavailable (paused, stopped, crashed)
3. **Exponential Backoff**: Starts with 2-second checks, backs off to 60 seconds to save resources
4. **Automatic Reconnection**: When Docker recovers, all affected servers are reconnected
5. **User Notification**: System notifications inform you of recovery status

### 📢 **Notifications**

MCPProxy shows native system notifications during Docker recovery:

| Event | Notification |
|-------|-------------|
| **Recovery Started** | "Docker engine detected offline. Reconnecting servers..." |
| **Recovery Success** | "Successfully reconnected X server(s)" |
| **Recovery Failed** | "Unable to reconnect servers. Check Docker status." |
| **Retry Attempts** | "Retry attempt X. Next check in Y" |

### 🐛 **Troubleshooting Docker Recovery**

**Servers don't reconnect after Docker recovery:**
```bash
# 1. Check Docker is running
docker ps

# 2. Check mcpproxy logs
cat ~/.mcpproxy/logs/main.log | grep -i "docker recovery"

# 3. Verify container labels
docker ps -a --filter label=com.mcpproxy.managed

# 4. Force reconnect via system tray
# System Tray → Force Reconnect All Servers
```

**Containers not cleaned up on shutdown:**
```bash
# Check for orphaned containers
docker ps -a --filter label=com.mcpproxy.managed=true

# Manual cleanup if needed
docker ps -a --filter label=com.mcpproxy.managed=true -q | xargs docker rm -f
```

**Docker recovery taking too long:**
- Docker recovery uses exponential backoff (2s → 60s intervals)
- This is intentional to avoid wasting resources while Docker is offline
- You can force an immediate reconnect via the system tray menu

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

## 🔐 Optional HTTPS Setup

MCPProxy works with HTTP by default for easy setup. HTTPS is optional and primarily useful for production environments or when stricter security is required.

**💡 Note**: Most users can stick with HTTP (the default) as it works perfectly with all supported clients including Claude Desktop, Cursor, and VS Code.

### Quick HTTPS Setup

**1. Enable HTTPS** (choose one method):
```bash
# Method 1: Environment variable
export MCPPROXY_TLS_ENABLED=true
mcpproxy serve

# Method 2: Config file
# Edit ~/.mcpproxy/mcp_config.json and set "tls.enabled": true
```

**2. Trust the certificate** (one-time setup):
```bash
mcpproxy trust-cert
```

**3. Use HTTPS URLs**:
- MCP endpoint: `https://localhost:8080/mcp`
- Web UI: `https://localhost:8080/ui/`

### Claude Desktop Integration

For Claude Desktop, add this to your `claude_desktop_config.json`:

**HTTP (Default - Recommended):**
```json
{
  "mcpServers": {
    "mcpproxy": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "http://localhost:8080/mcp"
      ]
    }
  }
}
```

**HTTPS (With Certificate Trust):**
```json
{
  "mcpServers": {
    "mcpproxy": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "https://localhost:8080/mcp"
      ],
      "env": {
        "NODE_EXTRA_CA_CERTS": "~/.mcpproxy/certs/ca.pem"
      }
    }
  }
}
```

### Certificate Management

- **Automatic generation**: Certificates created on first HTTPS startup
- **Multi-domain support**: Works with `localhost`, `127.0.0.1`, `::1`
- **Trust installation**: Use `mcpproxy trust-cert` to add to system keychain
- **Certificate location**: `~/.mcpproxy/certs/` (ca.pem, server.pem, server-key.pem)

### Troubleshooting HTTPS

**Certificate trust issues**:
```bash
# Re-trust certificate
mcpproxy trust-cert --force

# Check certificate location
ls ~/.mcpproxy/certs/

# Test HTTPS connection
curl -k https://localhost:8080/api/v1/status
```

**Claude Desktop connection issues**:
- Ensure `NODE_EXTRA_CA_CERTS` points to the correct ca.pem file
- Restart Claude Desktop after config changes
- Verify HTTPS is enabled: `mcpproxy serve --log-level=debug`

## Learn More

* Documentation: [Configuration](https://mcpproxy.app/docs/configuration), [Features](https://mcpproxy.app/docs/features), [Usage](https://mcpproxy.app/docs/usage)
* Website: <https://mcpproxy.app>
* Releases: <https://github.com/smart-mcp-proxy/mcpproxy-go/releases>

## Contributing 🤝

We welcome issues, feature ideas, and PRs! Fork the repo, create a feature branch, and open a pull request. See `CONTRIBUTING.md` (coming soon) for guidelines. 