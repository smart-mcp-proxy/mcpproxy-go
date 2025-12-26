# CLI Output Formatting

MCPProxy CLI supports multiple output formats for machine-readable and human-readable output.

## Output Format Options

### Global Flags

All commands support the following output format options:

| Flag | Description |
|------|-------------|
| `-o, --output <format>` | Output format: `table` (default), `json`, `yaml` |
| `--json` | Shorthand for `-o json` |

### Environment Variable

You can set the default output format using the `MCPPROXY_OUTPUT` environment variable:

```bash
export MCPPROXY_OUTPUT=json
mcpproxy upstream list  # Outputs JSON
```

### Format Precedence

1. Command-line flag (`-o`/`--json`) - highest priority
2. Environment variable (`MCPPROXY_OUTPUT`)
3. Default (`table`)

## Supported Formats

### Table Format (Default)

Human-readable tabular output with aligned columns.

```bash
mcpproxy upstream list
```

```
  NAME             PROTOCOL   TOOLS   STATUS         ACTION
✅ github-server    http       15      Connected
⏸️ ast-grep         stdio      0       Disabled       Enable
```

Features:
- Aligned columns using tabwriter
- Unicode status indicators
- Respects `NO_COLOR=1` environment variable
- Simplified output for non-TTY contexts

### JSON Format

Machine-readable JSON output for scripting and AI agents.

```bash
mcpproxy upstream list -o json
```

```json
[
  {
    "name": "github-server",
    "enabled": true,
    "protocol": "http",
    "connected": true,
    "tool_count": 15,
    "health": {
      "level": "healthy",
      "admin_state": "enabled",
      "summary": "Connected"
    }
  }
]
```

Features:
- Pretty-printed with 2-space indentation
- Empty arrays output as `[]` (not `null`)
- Snake_case field names
- Structured error output with recovery hints

### YAML Format

Human-readable structured output for configuration scenarios.

```bash
mcpproxy upstream list -o yaml
```

```yaml
- name: github-server
  enabled: true
  protocol: http
  connected: true
  tool_count: 15
  health:
    level: healthy
    admin_state: enabled
    summary: Connected
```

## Machine-Readable Help

The `--help-json` flag outputs structured help information for command discovery:

```bash
mcpproxy --help-json
```

```json
{
  "name": "mcpproxy",
  "description": "Smart MCP Proxy - Intelligent tool discovery and proxying",
  "usage": "mcpproxy [flags]",
  "flags": [
    {
      "name": "output",
      "shorthand": "o",
      "description": "Output format: table, json, yaml",
      "type": "string",
      "default": ""
    }
  ],
  "commands": [
    {
      "name": "upstream",
      "description": "Manage upstream MCP servers",
      "usage": "mcpproxy upstream [flags]",
      "has_subcommands": true
    }
  ]
}
```

This enables AI agents to discover available commands and their options programmatically.

## Structured Error Output

When using JSON output format, errors include structured information:

```json
{
  "code": "ERR_SERVER_NOT_FOUND",
  "message": "Server 'foo' not found",
  "guidance": "Check the server name and try again",
  "recovery_command": "mcpproxy upstream list",
  "context": {
    "server_name": "foo"
  }
}
```

Error fields:
- `code`: Machine-readable error code
- `message`: Human-readable error message
- `guidance`: Suggested next steps
- `recovery_command`: Command to help resolve the issue
- `context`: Additional context about the error

## Command Examples

### List servers in different formats

```bash
# Table (default)
mcpproxy upstream list

# JSON for scripting
mcpproxy upstream list -o json

# YAML for readability
mcpproxy upstream list -o yaml

# Using --json alias
mcpproxy upstream list --json
```

### Parse JSON output with jq

```bash
# Get connected server names
mcpproxy upstream list -o json | jq -r '.[] | select(.connected) | .name'

# Count total tools
mcpproxy upstream list -o json | jq '[.[].tool_count] | add'
```

### Discover commands for AI agents

```bash
# Get all available commands
mcpproxy --help-json | jq '.commands[].name'

# Get flags for a specific command
mcpproxy upstream list --help-json | jq '.flags'
```

## NO_COLOR Support

The CLI respects the `NO_COLOR` environment variable to disable colored output:

```bash
NO_COLOR=1 mcpproxy upstream list
```

This affects table format output only (JSON and YAML are always plain text).
