# MCPProxy Setup Guide

A comprehensive guide to connect mcpproxy (http-streamable) to popular MCP clients: Cursor IDE, VS Code, Claude Desktop, and Goose.

## What is MCPProxy?

MCPProxy is a smart Model Context Protocol (MCP) proxy that provides intelligent tool discovery and proxying for MCP servers. It runs as an HTTP server that aggregates multiple upstream MCP servers into a single endpoint, making it easy to connect multiple AI tools and services to your favorite IDE or AI assistant.

**Key Features:**
- **HTTP Streamable**: Uses MCP's streamable HTTP transport for efficient communication
- **Smart Tool Discovery**: Automatically indexes and searches tools from multiple upstream servers
- **Unified Interface**: Single endpoint for multiple MCP servers
- **OAuth Support**: Built-in authentication for secure services
- **Cross-Platform**: Works on macOS, Windows, and Linux

## Quick Start

### 1. Install MCPProxy

**macOS (Recommended - DMG Installer):**
Download the DMG installer from [GitHub Releases](https://github.com/smart-mcp-proxy/mcpproxy-go/releases) for the easiest installation experience.

**macOS (Homebrew):**
```bash
brew install smart-mcp-proxy/mcpproxy/mcpproxy
```

**Go Install:**
```bash
go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest
```

### 2. Run MCPProxy

**From Terminal:**
```bash
mcpproxy serve
```

**macOS (DMG Install):** Use Launchpad or Spotlight search to find and launch MCPProxy.

This starts MCPProxy on the default port `:8080` with HTTP endpoint at `http://localhost:8080/mcp/`

**📝 Note:**
- MCPProxy starts with **HTTP by default** for immediate compatibility
- HTTPS is available optionally for enhanced security (see [HTTPS Setup](#optional-https-setup) below)
- At first launch, MCPProxy will automatically generate a minimal configuration file if none exists

### 3. Check if Port is Available

**Check if port 8080 is already in use:**

**macOS/Linux:**
```bash
lsof -i :8080
# or
netstat -an | grep 8080
```

**Windows:**
```bash
netstat -an | findstr 8080
```

**Change Default Port:**
```bash
mcpproxy serve --listen :8081
# or set in config file
```

## Configuration Paths

MCPProxy looks for configuration in these locations (in order):

| OS | Config Location |
|---|---|
| **macOS** | `~/.mcpproxy/mcp_config.json` |
| **Windows** | `%USERPROFILE%\.mcpproxy\mcp_config.json` |
| **Linux** | `~/.mcpproxy/mcp_config.json` |

**Sample Configuration:**
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

**📝 Note:** At first launch, MCPProxy will automatically generate a minimal configuration file if none exists.

## Client Setup Instructions

### 🎯 Cursor IDE

**Method 1: One-Click Install**
1. Visit: https://mcpproxy.app/cursor-install.html
2. Click "Install in Cursor IDE"

**Method 2: Manual Setup**
1. Open Cursor Settings (`Cmd/Ctrl + ,`)
2. Click "Tools & Integrations"
3. Add MCP Server with this configuration:

```json
{
  "MCPProxy": {
    "type": "http",
    "url": "http://localhost:8080/mcp/"
  }
}
```

**Verification:**
- **Option 1:** Restart Cursor completely
- **Option 2:** Disable and re-enable the MCP server in Cursor Settings > Tools & Integrations
- **⚠️ Important:** Make sure MCPProxy is running (check for tray icon if enabled)
- Open chat and ask: "What tools do you have available?"

---

### 🛠️ VS Code

VS Code has built-in MCP support starting from version 1.102.

**Setup Steps:**
1. Install **GitHub Copilot** and **Copilot Chat** extensions
2. Open VS Code Settings (`Cmd/Ctrl + ,`)
3. Search for "mcp" in settings
4. Click "Edit in settings.json" in the MCP section
5. Add this configuration:

```json
{
  "chat.mcp.discovery.enabled": true,
  "mcp": {
    "servers": {
      "mcpproxy": {
        "type": "http",
        "url": "http://localhost:8080/mcp/"
      }
    }
  }
}
```

**Alternative: Workspace Configuration**
Create `.vscode/mcp.json` in your workspace:

```json
{
  "servers": {
    "MCPProxy": {
      "type": "http", 
      "url": "http://localhost:8080/mcp/"
    }
  }
}
```

**Usage:**
1. Open Copilot Chat
2. Select **Agent Mode** 
3. Click Tools icon to see available tools
4. MCPProxy tools will be listed

**📚 Reference:** [VS Code MCP Documentation](https://code.visualstudio.com/docs/copilot/chat/mcp-servers)

---

### 🤖 Claude Desktop

Claude Desktop supports two different approaches depending on your plan:

**A) Free Plan — Local JSON Configuration**
Run mcpproxy as a local process and register it in the JSON configuration file. This method uses stdio transport with a bridge package.

**B) Paid Plans — Remote Custom Connector**
Add mcpproxy as a remote MCP server via Settings → Connectors → Add Custom Connector. This method connects directly via HTTP.

---

**Configuration Paths:**

| OS | Claude Desktop Config Path |
|---|---|
| **macOS** | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| **Windows** | `%APPDATA%\Claude\claude_desktop_config.json` |
| **Linux** | `~/.config/Claude/claude_desktop_config.json` |

**Setup Steps:**

#### Option A: Free Plan — JSON Configuration

1. Create the config file if it doesn't exist:

**macOS:**
```bash
mkdir -p ~/Library/Application\ Support/Claude/
touch ~/Library/Application\ Support/Claude/claude_desktop_config.json
```

**Windows:**
```bash
mkdir "%APPDATA%\Claude"
type nul > "%APPDATA%\Claude\claude_desktop_config.json"
```

**Linux:**
```bash
mkdir -p ~/.config/Claude/
touch ~/.config/Claude/claude_desktop_config.json
```

2. Add this configuration:

```json
{
  "mcpServers": {
    "mcpproxy": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://localhost:8080/mcp"]
    }
  }
}
```

**📝 Note:** This approach uses `mcp-remote` to bridge HTTP to stdio transport, which is required for the JSON configuration method.

3. Restart Claude Desktop
4. Look for MCP tools in the conversation interface

#### Option B: Paid Plans — Remote Custom Connector

1. Open Claude Desktop
2. Go to **Settings** → **Connectors**
3. Click **Add Custom Connector**
4. Enter the URL: `http://localhost:8080/mcp`
5. Save the configuration

**📝 Note:** Remote servers defined in the JSON configuration are NOT used by Claude Desktop for paid plans. You must add them through the UI, and this feature is gated by your subscription plan.

**📚 Reference:** [Claude Desktop MCP Setup](https://docs.anthropic.com/claude/docs/mcp)

---

### 🪿 Goose

Goose is a command-line AI agent that supports MCP servers through its extension system.

**Prerequisites:**
- Python 3.8+ or Go 1.19+
- Goose installed: https://github.com/block/goose

**Setup via CLI:**
```bash
# Configure Goose
goose configure

# Choose: Add Extension
# Choose: Remote Extension  
# Name: MCPProxy
# URL: http://localhost:8080/mcp/
# Timeout: 300 (default)
```

**Setup via Configuration File:**
Edit `~/.config/goose/config.yaml`:

```yaml
extensions:
  mcpproxy:
    type: "remote"
    url: "http://localhost:8080/mcp/"
    timeout: 300
```

**Usage:**
```bash
# Start Goose session
goose

# Check available tools
goose> What tools do you have?

# Use MCPProxy tools
goose> Help me search for files related to authentication
```

**📚 Reference:** [Goose Documentation](https://block.github.io/goose/docs/tutorials/custom-extensions/)

---

## Optional HTTPS Setup

MCPProxy supports secure HTTPS connections with automatic certificate generation. **HTTP is enabled by default** for immediate compatibility, but HTTPS provides enhanced security for production use.

### Why Use HTTPS?

- 🔒 **Encrypted Communication**: All data between clients and MCPProxy is encrypted
- 🛡️ **Production Ready**: Secure for network-exposed deployments
- 🔑 **Certificate Authentication**: Prevents man-in-the-middle attacks
- 🌐 **Standard Compliance**: Follow web security best practices

### Quick HTTPS Setup

**Step 1: Install Certificate (One-time setup)**
```bash
# Trust the mcpproxy CA certificate
mcpproxy trust-cert
```
This command will:
- Generate a local CA certificate if needed
- Install it to your system's trusted certificate store
- Prompt for your password once (required for keychain access)

**Step 2: Enable HTTPS**

Choose one of these methods:

**Option A: Environment Variable (Temporary)**
```bash
export MCPPROXY_TLS_ENABLED=true
mcpproxy serve
```

**Option B: Configuration File (Permanent)**
Edit `~/.mcpproxy/mcp_config.json`:
```json
{
  "listen": ":8080",
  "tls": {
    "enabled": true,
    "require_client_cert": false,
    "hsts": true
  }
}
```

**Step 3: Update Client Configurations**

After enabling HTTPS, update your client configurations to use `https://` URLs:

**Cursor IDE:**
```json
{
  "MCPProxy": {
    "type": "http",
    "url": "https://localhost:8080/mcp/"
  }
}
```

**VS Code:**
```json
{
  "mcp": {
    "servers": {
      "mcpproxy": {
        "type": "http",
        "url": "https://localhost:8080/mcp/"
      }
    }
  }
}
```

**Claude Desktop (with certificate trust):**
```json
{
  "mcpServers": {
    "mcpproxy": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://localhost:8080/mcp"],
      "env": {
        "NODE_EXTRA_CA_CERTS": "~/.mcpproxy/certs/ca.pem"
      }
    }
  }
}
```

### HTTPS Configuration Options

**Basic HTTPS (Recommended):**
```json
{
  "tls": {
    "enabled": true
  }
}
```

**Advanced HTTPS with mTLS:**
```json
{
  "tls": {
    "enabled": true,
    "require_client_cert": true,
    "certs_dir": "~/.mcpproxy/certs",
    "hsts": true
  }
}
```

**Configuration Options:**
- `enabled`: Enable/disable HTTPS (default: `false`)
- `require_client_cert`: Enable mutual TLS (mTLS) for client authentication
- `certs_dir`: Custom directory for certificates (default: `{data_dir}/certs`)
- `hsts`: Enable HTTP Strict Transport Security headers

### Certificate Management

**Certificate Locations:**
- **CA Certificate**: `~/.mcpproxy/certs/ca.pem`
- **Server Certificate**: `~/.mcpproxy/certs/localhost.pem`
- **Private Keys**: `~/.mcpproxy/certs/*.key` (automatically secured)

**View Certificate Details:**
```bash
# View CA certificate info
openssl x509 -in ~/.mcpproxy/certs/ca.pem -text -noout

# Verify certificate chain
openssl verify -CAfile ~/.mcpproxy/certs/ca.pem ~/.mcpproxy/certs/localhost.pem
```

**Regenerate Certificates:**
```bash
# Remove existing certificates
rm -rf ~/.mcpproxy/certs

# Start mcpproxy with HTTPS (will generate new certificates)
MCPPROXY_TLS_ENABLED=true mcpproxy serve

# Trust the new certificate
mcpproxy trust-cert
```

### Troubleshooting HTTPS

**Certificate Trust Issues:**

If you get SSL/TLS errors, verify certificate trust:
```bash
# Test certificate trust
curl -f https://localhost:8080/health

# If it fails, re-trust the certificate
mcpproxy trust-cert --force
```

**Claude Desktop Certificate Issues:**

If Claude Desktop shows certificate errors:
1. Ensure `NODE_EXTRA_CA_CERTS` points to the correct certificate path
2. Use absolute path: `/Users/yourusername/.mcpproxy/certs/ca.pem`
3. Restart Claude Desktop after configuration changes

**Browser Certificate Warnings:**

When accessing the Web UI at `https://localhost:8080/ui/`:
1. Click "Advanced" on the certificate warning
2. Click "Proceed to localhost (unsafe)"
3. This is expected for self-signed certificates

### Security Notes

- 🔒 **Local Development**: Self-signed certificates are perfect for local development
- 🏢 **Production**: Consider using proper CA-signed certificates for production deployments
- 🔑 **Certificate Rotation**: Certificates are valid for 10 years but can be regenerated anytime
- 🛡️ **mTLS**: Enable `require_client_cert: true` for maximum security in sensitive environments

---

## Port Management

### Check Current Port Usage

**Find MCPProxy Process:**
```bash
# macOS/Linux
ps aux | grep mcpproxy
lsof -i :8080

# Windows  
tasklist | findstr mcpproxy
netstat -ano | findstr :8080
```

### Change Default Port

**Command Line:**
```bash
mcpproxy serve --listen :8081
mcpproxy serve --listen :9000
mcpproxy serve --listen 127.0.0.1:8080  # Bind to specific interface
```

**Configuration File:**
```json
{
  "listen": ":8081",
  // ... rest of config
}
```

**Environment Variable:**
```bash
export MCPP_LISTEN=":8081"
mcpproxy serve
```

**📝 Note:** Environment variables are prefixed with `MCPP_`. For example, `MCPP_LISTEN` controls the listen address.

### Multiple Instances

Run multiple MCPProxy instances on different ports:

```bash
# Instance 1 - Development  
mcpproxy serve --config dev_config.json --listen :8080

# Instance 2 - Production
mcpproxy serve --config prod_config.json --listen :8081
```

## Troubleshooting

### Common Issues

**1. Port Already in Use**
```bash
# Kill process using port 8080
lsof -ti:8080 | xargs kill -9  # macOS/Linux
netstat -ano | findstr :8080   # Windows - note PID, then:
taskkill /PID <PID> /F          # Windows
```

**2. MCPProxy Not Starting**
```bash
# Check logs
mcpproxy serve --log-level debug

# Check configuration
mcpproxy serve --config ~/.mcpproxy/mcp_config.json --log-level debug
```

**3. Client Connection Issues**
- Verify MCPProxy is running: Check process with `ps aux | grep mcpproxy`
- Check firewall settings
- Ensure correct URL in client config
- Try different port: `mcpproxy serve --listen :8081`
- Check tray icon (if enabled) for status

**4. Tools Not Appearing**
- Check MCPProxy upstream server configuration
- Verify upstream servers are running
- Check MCPProxy logs for errors
- Use the `retrieve_tools` tool in your MCP client to test tool discovery
- Use `mcpproxy tools list --server=SERVER_NAME` to test individual servers

**5. Server Connection Problems**
- Test individual servers: `mcpproxy tools list --server=SERVER_NAME --log-level=trace`
- Check authentication: Look for OAuth URLs in console output
- Verify server configuration: Ensure URL, command, and protocol are correct
- Check environment: For stdio servers, verify command and arguments are correct

### Debug Commands

**Test MCPProxy Status:**
```bash
# Check if MCPProxy is running
ps aux | grep mcpproxy

# Check port usage
lsof -i :8080

# View logs (with debug mode)
mcpproxy serve --log-level debug
```

**Debug Individual Servers:**
```bash
# List tools from a specific server with detailed debugging
mcpproxy tools list --server=github-server --log-level=trace

# Test slow servers with extended timeout
mcpproxy tools list --server=slow-server --timeout=60s

# Output tools in machine-readable format
mcpproxy tools list --server=weather-api --output=json
```

**📝 Note:** The `mcpproxy tools list` command is perfect for debugging connection issues, authentication problems, or verifying that a server is working correctly. It connects directly to the server, bypasses the proxy's cache, and shows detailed logging.

**📝 Note:** MCPProxy uses the MCP protocol over HTTP, not simple REST endpoints. Use MCP clients to interact with the server, not direct curl commands.

**View Logs:**
```bash
# Real-time logs (macOS/Linux)
tail -f ~/Library/Logs/mcpproxy/main.log

# Windows
Get-Content -Path "$env:LOCALAPPDATA\mcpproxy\logs\main.log" -Wait

# Filter logs for specific server debugging
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(github-server|oauth|error)"
```

## Advanced Configuration

### Security Settings

```json
{
  "listen": "127.0.0.1:8080",    // Bind to localhost only
  "read_only_mode": true,        // Prevent configuration changes
  "disable_management": true,     // Disable server management tools
  "allow_server_add": false,     // Prevent adding new servers
  "allow_server_remove": false   // Prevent removing servers
}
```

### Performance Tuning

```json
{
  "top_k": 10,                   // More search results
  "tools_limit": 25,             // More tools per request
  "tool_response_limit": 50000   // Larger response limit
}
```

### OAuth Configuration

For servers requiring authentication:

```json
{
  "mcpServers": [
    {
      "name": "github",
      "url": "https://api.github.com/mcp/",
      "type": "http",
      "oauth": {
        "scopes": ["repo", "user"]
      },
      "enabled": true
    }
  ]
}
```

## Next Steps

1. **Add Upstream Servers**: Configure MCPProxy to connect to your MCP servers
2. **Explore Tools**: Use your AI assistant to discover available tools
3. **Customize**: Adjust settings for your workflow
4. **Share**: Use workspace configs to share setups with your team

## Additional Resources

- **MCPProxy Website**: https://mcpproxy.app
- **Documentation**: https://mcpproxy.app/docs
- **GitHub Repository**: https://github.com/smart-mcp-proxy/mcpproxy-go
- **MCP Specification**: https://modelcontextprotocol.io
- **Available MCP Servers**: https://github.com/modelcontextprotocol/servers

---

*Need help? Join our community or open an issue on GitHub.* 