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
4. Any **pending** (newly-discovered, never-reviewed) tool-approval records for
   the server are **auto-promoted to approved** — approving a server means you
   trust its current tool snapshot (baseline trust). Tools whose description or
   schema later **changes** (`changed`, i.e. rug-pull) are *not* affected and
   stay blocked until you re-approve them explicitly.

### Security Analysis

When a tool from a quarantined server is called, MCPProxy **blocks the call** and
returns a structured security response instead of invoking it — so the tool's
description can be reviewed before it ever runs:

```json
{
  "status": "QUARANTINED_SERVER_BLOCKED",
  "serverName": "suspicious-server",
  "toolName": "fetch_data",
  "message": "🔒 SECURITY BLOCK: Server 'suspicious-server' is currently in quarantine for security review. Tool calls are blocked to prevent potential Tool Poisoning Attacks (TPAs).",
  "instructions": "To use tools from this server, please: 1) Review the server and its tools for malicious content, 2) Use the 'upstream_servers' tool with operation 'list_quarantined' to inspect tools, 3) remove from quarantine if verified safe",
  "toolAnalysis": {
    "name": "fetch_data",
    "description": "…",
    "inputSchema": { "…": "…" },
    "serverName": "suspicious-server",
    "analysis": "SECURITY ANALYSIS: This tool is from a quarantined server. Please carefully review the description and input schema for potential hidden instructions, embedded prompts, or suspicious behavior patterns."
  }
}
```

The actual pattern **detection** — hidden-Unicode smuggling, cross-server
shadowing, decoded shell payloads, injection/exfiltration phrases, and embedded
secrets — is performed by the deterministic offline detect engine that backs the
built-in `tpa-descriptions` scanner. Its findings appear in the scan report
(`mcpproxy security report <server>`), each carrying a `rule_id`, `severity`,
`threat_level`, `confidence`, and the contributing check `signals`. See
[Tool Scanner](/features/tool-scanner) for the full rule reference.

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
  http://127.0.0.1:8080/api/v1/servers/server-name/unquarantine
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

Tool-description analysis is performed by the deterministic, fully-offline
**detect engine** that backs the built-in `tpa-descriptions` scanner. It runs
**seven checks across two tiers** — four **hard** checks that auto-quarantine and
block approval, and three **soft** checks that raise a human-review item:

| Check | Tier | Catches |
|-------|------|---------|
| `unicode.hidden` | hard | Zero-width / bidi / TAG-block / PUA character smuggling |
| `shadowing.cross_server` | hard | Distinctive tool-name collision or cross-server reference |
| `payload.decoded` | hard | base64/hex blob that decodes to a shell/exfil command |
| `phrase.injection` | hard | Curated instruction-override / exfiltration directives |
| `directive.imperative` | soft | Injection directives, secrecy imperatives, instruction overrides |
| `capability.mismatch` | soft | Compute/string tool touching `~/.ssh` etc.; unexplained data-sink param |
| `secret.embedded` | soft | Hardcoded live credential (confidence-scored, placeholders dropped) |

Each check is deterministic and reliability is enforced by a CI eval gate. See
[Tool Scanner](/features/tool-scanner) for the full rule reference, the two-tier
model, normalization, and the eval gate.

## Best Practices

1. **Review All Servers**: Never auto-approve servers added by AI agents
2. **Source Verification**: Only approve servers from known, trusted sources
3. **Minimal Permissions**: Prefer servers with limited, specific capabilities
4. **Regular Audits**: Periodically review approved servers
5. **Network Isolation**: Use Docker isolation with `network_mode: "none"` for untrusted servers

## Tool-Level Quarantine

In addition to server-level quarantine, MCPProxy provides **tool-level quarantine** that detects changes to individual tool descriptions and schemas using SHA256 hashing. This protects against "rug pull" attacks where a previously trusted server silently modifies tool behavior.

See [Tool Quarantine](./tool-quarantine.md) for complete documentation on:
- SHA256 hash-based tool approval
- CLI commands: `mcpproxy upstream inspect` and `mcpproxy upstream approve`
- Configuration: `quarantine_enabled` (global) and `auto_approve_tool_changes` (per-server; deprecates `skip_quarantine`)
- REST API endpoints for tool approval management

### Block (approve + disable)

When reviewing a pending or changed tool you may want to **acknowledge it but
keep it hidden** from MCP clients — for example, dismissing a noisy "changed"
flag for a tool you never intend to use. The **block** operation does this
atomically: it approves the tool (clearing the quarantine flag) **and** disables
it in a single, all-or-nothing server-side write, so a tool is never left in the
approved+enabled state.

- **REST**: `POST /api/v1/servers/{id}/tools/block` with `{"tools":[...]}` or
  `{"block_all": true}`.
- **MCP**: `quarantine_security` operations `block_tool` (with `name` +
  `tool_name`) and `block_all_tools` (with `name`).

A blocked tool can be re-exposed later with the normal enable operation
(`POST /api/v1/servers/{id}/tools/{tool}/enabled` with `{"enabled": true}`).

## Disabling Quarantine

**Not recommended**, but you can opt out of quarantine globally by setting a
single top-level flag in `~/.mcpproxy/mcp_config.json`:

```json
{
  "quarantine_enabled": false
}
```

When `quarantine_enabled` is `false`:

- Servers added dynamically via the `upstream_servers` MCP tool or the
  `POST /api/v1/servers` REST endpoint default to **not quarantined**.
- Tool-level quarantine (per-tool SHA-256 approval of descriptions and
  schemas, see [Tool Quarantine](./tool-quarantine.md)) is skipped.

An explicit `quarantined` field in an add-server request still wins over
the default, so client code can always override on a per-server basis.
Per-server `auto_approve_tool_changes: true` auto-approves all post-baseline tool changes and additions for that server (the deprecated `skip_quarantine: true` is migrated onto it automatically).

Warning: Disabling quarantine exposes your system to Tool Poisoning
Attacks. Only do this on machines where every MCP server you connect to
is already trusted.
