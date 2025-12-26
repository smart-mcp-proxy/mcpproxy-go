---
id: security-quarantine
title: Security Quarantine
sidebar_label: Security Quarantine
sidebar_position: 4
description: Protect against Tool Poisoning Attacks with automatic server quarantine
keywords: [security, quarantine, tpa, tool poisoning]
---

# Security Quarantine

MCPProxy includes an automatic quarantine system to protect against Tool Poisoning Attacks (TPA).

## What is a Tool Poisoning Attack?

Tool Poisoning Attacks occur when malicious MCP servers:

1. **Hidden Instructions**: Embed malicious instructions in tool descriptions that AI agents might follow
2. **Data Exfiltration**: Trick AI agents into sending sensitive data to external servers
3. **Credential Theft**: Attempt to extract API keys or tokens
4. **System Manipulation**: Try to execute unauthorized commands

## How Quarantine Works

### Automatic Quarantine

When a new server is added via an AI client (using the `upstream_servers` tool):

1. Server is automatically placed in **quarantine status**
2. Tool calls to quarantined servers return a **security analysis** instead of executing
3. Server remains quarantined until **manually approved**

### Tool Discovery and Search Isolation

**Quarantined servers are completely isolated from the tool discovery and search system:**

| Feature | Quarantined Server | Approved Server |
|---------|-------------------|-----------------|
| Tools indexed | ❌ No | ✅ Yes |
| Tools searchable via `retrieve_tools` | ❌ No | ✅ Yes |
| Tools appear in HTTP API search | ❌ No | ✅ Yes |
| Tool calls allowed | ❌ No (returns security analysis) | ✅ Yes |

This isolation prevents Tool Poisoning Attacks from:
- **Injecting malicious descriptions** into search results that AI agents might read and follow
- **Appearing in tool recommendations** where they could be mistakenly selected
- **Influencing AI agent behavior** through carefully crafted tool metadata

When a server is quarantined:
1. Its tools are **immediately removed** from the search index
2. `retrieve_tools` queries will **never return** tools from that server
3. The server remains visible in the server list (marked as quarantined) for management

When a server is unquarantined (approved):
1. The server connects to discover its tools
2. Tools are **indexed and become searchable**
3. Tool calls are allowed to execute normally

### Security Analysis

When a quarantined server's tool is called, MCPProxy returns:

```json
{
  "status": "quarantined",
  "server": "suspicious-server",
  "analysis": {
    "tool_count": 15,
    "suspicious_patterns": [
      "Tool 'fetch_data' description contains external URL",
      "Tool 'execute' has overly broad permissions"
    ],
    "risk_level": "medium",
    "recommendation": "Review tool descriptions before approving"
  }
}
```

## Managing Quarantine

### View Quarantined Servers

**Web UI:**
1. Open the dashboard
2. Click "Quarantine" in the navigation
3. Review pending servers

**CLI:**
```bash
mcpproxy upstream list
# Shows quarantine status for each server
```

### Approve a Server

**Web UI:**
1. Click on the quarantined server
2. Review the security analysis
3. Click "Approve" to remove from quarantine

**API:**
```bash
curl -X POST \
  -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{"quarantined": false}' \
  http://127.0.0.1:8080/api/v1/servers/server-name/quarantine
```

**Config File:**

Edit `~/.mcpproxy/mcp_config.json` and add `"quarantined": false`:

```json
{
  "mcpServers": [
    {
      "name": "reviewed-server",
      "command": "npx",
      "args": ["@example/mcp-server"],
      "quarantined": false,
      "enabled": true
    }
  ]
}
```

### Re-quarantine a Server

If you need to quarantine a previously approved server:

```bash
curl -X POST \
  -H "X-API-Key: your-key" \
  -H "Content-Type: application/json" \
  -d '{"quarantined": true}' \
  http://127.0.0.1:8080/api/v1/servers/server-name/quarantine
```

## Security Checklist

Before approving a server, verify:

- [ ] **Source**: Is the server from a trusted source?
- [ ] **Code Review**: Have you reviewed the server's code?
- [ ] **Tool Descriptions**: Do tool descriptions look legitimate?
- [ ] **Network Access**: Does the server need network access?
- [ ] **Permissions**: Are requested permissions appropriate?

## Detection Patterns

MCPProxy checks for these suspicious patterns:

| Pattern | Risk Level | Description |
|---------|------------|-------------|
| External URLs in descriptions | Medium | May indicate data exfiltration |
| Credential keywords | High | Mentions of "password", "token", "key" |
| Execution commands | High | Shell execution capabilities |
| Hidden instructions | Critical | Base64 encoded or obfuscated content |
| Overly broad permissions | Medium | Access to all files or network |

## Best Practices

1. **Review All Servers**: Never auto-approve servers added by AI agents
2. **Source Verification**: Only approve servers from known, trusted sources
3. **Minimal Permissions**: Prefer servers with limited, specific capabilities
4. **Regular Audits**: Periodically review approved servers
5. **Network Isolation**: Use Docker isolation with `network_mode: "none"` for untrusted servers

## Disabling Quarantine

**Not recommended**, but you can disable automatic quarantine:

```json
{
  "features": {
    "enable_quarantine": false
  }
}
```

⚠️ **Warning**: Disabling quarantine exposes your system to Tool Poisoning Attacks.
