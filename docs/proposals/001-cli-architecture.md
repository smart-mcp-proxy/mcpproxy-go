# RFC-001: MCPProxy CLI Architecture Proposal

**Status**: Draft
**Created**: 2025-12-19
**Related Issues**: #55 (Projects/Profiles), #136 (Code Execution)

---

## Summary

This proposal defines a solid CLI architecture for mcpproxy that serves both human developers and AI agents effectively. The design follows the proven "gh CLI model" with hierarchical command discovery, universal JSON output, and project-aware context.

---

## Design Principles

### 1. CLI-First Architecture

CLI is the primary interface for both humans AND agents. MCP is only for upstream tool aggregation.

```
┌─────────────────────────────────────────────────────────────┐
│ mcpproxy MCP Tools (4 tools, ~400 tokens total):            │
│                                                             │
│ 1. retrieve_tools(query)     - Search upstream MCP tools    │
│ 2. call_tool(server, tool)   - Execute upstream MCP tool    │
│ 3. upstream_servers(action)  - CRUD servers (minimal)       │
│ 4. code_execution(script)    - JS orchestration             │
│                                                             │
│ These are for USING mcpproxy as a tool aggregator.          │
│ NOT for managing mcpproxy itself.                           │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ mcpproxy CLI Commands (for management):                     │
│                                                             │
│ Agents use these via shell (like gh, docker, kubectl)       │
│ Hierarchical discovery via --help-json                      │
└─────────────────────────────────────────────────────────────┘
```

### 2. Hierarchical Discovery (The gh Model)

```bash
# Level 0: What can mcpproxy do? (~150 tokens)
mcpproxy --help-json
{
  "commands": [
    {"name": "upstream", "description": "Manage MCP servers"},
    {"name": "tools", "description": "Discover and search tools"},
    {"name": "call", "description": "Execute tools"},
    {"name": "profile", "description": "Manage profiles"}
  ]
}

# Level 1: What can "upstream" do? (~100 tokens)
mcpproxy upstream --help-json

# Level 2: How do I use "upstream add"? (~150 tokens)
mcpproxy upstream add --help-json
```

**Context cost**: ~400 tokens for one operation (3 help calls)
**MCP equivalent**: ~5000 tokens (full tool list)

### 3. Agent-Friendly by Default

| Aspect | Human Mode (default) | Agent Mode (flags/env) |
|--------|---------------------|------------------------|
| Output | Formatted tables | `-o json` or `--json` |
| Help | Prose descriptions | `--help-json` |
| Prompts | Interactive | `--yes` / `MCPPROXY_NO_PROMPT=1` |
| Colors | Yes | `--no-color` / detect TTY |
| Progress | Spinners | Silent or stderr |
| Errors | Friendly text | Structured JSON with `recovery_command` |

---

## Complete Command Tree (v1.0)

> **Note on common flags**: Most commands support `-o/--output` for format selection (table, json, yaml, csv). The `--json` flag is a convenient alias for `-o json`. These are omitted from the tree below for brevity. See [Global Flags](#global-flags-all-commands) section.

```
mcpproxy
├── version                           # Version info
├── serve                             # Start daemon
│   ├── --listen <addr>               # Listen address (default: 127.0.0.1:8080)
│   ├── --log-level <level>           # Log level (debug, info, warn, error)
│   └── --config <path>               # Config file path
│
├── doctor                            # [P0] Health diagnostics
│   └── --fix                         # Auto-fix issues where possible
│
├── completion                        # [P0] Shell completion
│   ├── bash                          # Bash completion script
│   ├── zsh                           # Zsh completion script
│   ├── fish                          # Fish completion script
│   └── powershell                    # PowerShell completion script
│
├── upstream                          # [P0] Server management
│   ├── list                          # List servers
│   │   ├── --jq <expr>               # JQ filter expression
│   │   └── --names-only              # Just server names (newline-delimited)
│   │
│   ├── add                           # Add server (Claude-style notation)
│   │   │                             # HTTP:  mcpproxy upstream add <name> <url>
│   │   │                             # Stdio: mcpproxy upstream add <name> -- <cmd> [args...]
│   │   ├── --transport <type>        # Transport: http, stdio (default: infer from args)
│   │   ├── --env KEY=value           # Environment variable (repeatable)
│   │   ├── --header "Name: value"    # HTTP header (repeatable, http only)
│   │   ├── --working-dir <path>      # Working directory (stdio only)
│   │   ├── --scope <scope>           # Scope: global, project (default: global)
│   │   └── --if-not-exists           # No error if server exists
│   │
│   ├── add-json <name> '<json>'      # Add server from JSON config
│   │   └── --scope <scope>           # Scope: global, project
│   │
│   ├── remove <name>                 # Remove server
│   │   ├── --scope <scope>           # Scope: global, project
│   │   ├── --if-exists               # No error if server missing
│   │   └── --yes                     # Skip confirmation
│   │
│   ├── enable <name>                 # Enable server
│   ├── disable <name>                # Disable server
│   ├── restart <name>                # Restart server
│   │   └── --all                     # Restart all servers
│   │
│   ├── quarantine <name>             # Quarantine server
│   ├── approve <name>                # Unquarantine/approve server
│   │
│   └── logs <name>                   # View server logs
│       ├── --tail <n>                # Last N lines (default: 100)
│       └── --follow                  # Stream new logs
│
├── tools                             # [P0] Tool discovery
│   ├── list                          # List tools
│   │   ├── --server <name>           # Filter by server
│   │   ├── --all-servers             # Include all servers
│   │   └── --names-only              # Just tool names
│   │
│   ├── search <query>                # BM25 search
│   │   └── --limit <n>               # Max results (default: 10)
│   │
│   └── info <server:tool>            # Tool details + schema
│
├── call                              # [P0] Tool execution
│   └── tool <server:tool>            # Execute tool
│       ├── --args <json>             # Tool arguments as JSON
│       ├── --args-file <path>        # Arguments from file
│       └── --dry-run                 # Validate without executing
│
├── code                              # [P0] Code execution
│   └── exec                          # Run JavaScript
│       ├── --code <js>               # Inline JavaScript code
│       ├── --file <path>             # JavaScript file
│       └── --input <json>            # Input data as JSON
│
├── auth                              # [P0] Authentication
│   ├── login                         # OAuth login
│   │   ├── --server <name>           # Target server
│   │   ├── --no-browser              # Don't open browser
│   │   └── --token <token>           # Use token directly
│   │
│   ├── logout                        # Clear tokens
│   │   ├── --server <name>           # Target server
│   │   └── --all                     # Clear all tokens
│   │
│   └── status                        # Check auth status
│       └── --server <name>           # Target server
│
├── secrets                           # Secret management
│   ├── set <key> <value>             # Set secret
│   ├── get <key>                     # Get secret
│   ├── del <key>                     # Delete secret
│   ├── list                          # List secrets
│   └── migrate                       # Migrate plaintext secrets
│
├── project                           # [WIP] Project management (optional)
│   ├── init [path]                   # Initialize .mcpproxy/ directory
│   │   ├── --template <name>         # Use predefined template
│   │   └── --from-global             # Copy servers from global config
│   │
│   ├── current                       # Show current project context
│   ├── list                          # List known projects
│   │
│   └── config                        # Project config management
│       ├── show                      # Show project config
│       │   └── --effective           # Merged global + project
│       └── validate                  # Validate project config
│
├── profile                           # [WIP] Profile management (optional)
│   ├── list                          # List profiles
│   ├── create <name>                 # Create profile
│   │   └── --servers <names>         # Comma-separated server names
│   │
│   ├── switch <name>                 # Switch active profile
│   │   └── --dry-run                 # Preview changes
│   │
│   ├── delete <name>                 # Delete profile
│   │   └── --yes                     # Skip confirmation
│   │
│   └── current                       # Show current profile
│
├── config                            # [P2] Configuration management
│   ├── get <key>                     # Get config value
│   ├── set <key> <value>             # Set config value
│   │   └── --validate                # Validate before applying
│   │
│   ├── edit                          # Open in $EDITOR
│   ├── validate                      # Validate configuration
│   ├── where                         # Show config file paths
│   ├── export                        # Export configuration
│   │   └── --output <path>           # Output file path
│   │
│   └── import                        # Import configuration
│       └── --input <path>            # Input file path
│
├── run <script>                      # [P2] Execute saved script
│   ├── --input <json>                # Input data as JSON
│   └── --list                        # List available scripts
│
├── watch                             # [P3] Live monitoring
│   ├── servers                       # Watch server status changes
│   ├── calls                         # Watch tool calls in real-time
│   │   └── --server <name>           # Filter by server
│   │
│   └── events                        # Watch all SSE events
│
├── activity                          # [NEW] Activity log & audit trail (RFC-003)
│   ├── list                          # List recent activity
│   │   ├── --type <type>             # Filter: tool_call, policy_decision, quarantine
│   │   ├── --server <name>           # Filter by server
│   │   ├── --session <id>            # Filter by session
│   │   ├── --status <status>         # Filter: success, error, blocked
│   │   ├── --risk <level>            # Filter: low, medium, high, critical
│   │   ├── --has-pii                 # Only entries with PII detected
│   │   ├── --start-time <RFC3339>    # After this time
│   │   ├── --end-time <RFC3339>      # Before this time
│   │   └── --limit <n>               # Max results (default: 100)
│   │
│   ├── watch                         # Stream live activity (like tail -f)
│   │   ├── --type <type>             # Filter by activity type
│   │   └── --server <name>           # Filter by server
│   │
│   ├── show <id>                     # Show activity details
│   │   └── --include-response        # Include full request/response
│   │
│   ├── summary                       # Risk/activity summary dashboard
│   │   ├── --period <duration>       # Time period: 1h, 24h, 7d (default: 24h)
│   │   └── --by <group>              # Group by: server, tool, status
│   │
│   └── export                        # Export for compliance/audit
│       ├── --output <path>           # Output file
│       ├── --format <fmt>            # json, csv
│       ├── --start-time <RFC3339>    # Start time
│       ├── --end-time <RFC3339>      # End time
│       └── --include-bodies          # Include request/response bodies
│
└── integrate                         # [NEW] MCP client integration
    ├── import <client>               # Import servers from client config
    │   ├── --config <path>           # Custom config path
    │   └── --clients                 # Supported: claude, cursor, goose
    │
    └── export <client>               # Export mcpproxy config for client
        └── --output <path>           # Output config path
```

---

## Global Flags (All Commands)

| Flag | Short | Description | Environment Variable |
|------|-------|-------------|---------------------|
| `--output <format>` | `-o` | Output format: table, json, yaml, csv | `MCPPROXY_OUTPUT` |
| `--json` | | Shorthand for `-o json` | `MCPPROXY_JSON=1` |
| `--help-json` | | Machine-readable help | |
| `--jq <expr>` | | Filter JSON output | |
| `--quiet` | `-q` | Minimal output | `MCPPROXY_QUIET=1` |
| `--yes` | `-y` | Skip confirmations | |
| `--no-color` | | Disable colors | `NO_COLOR=1` |
| `--config <path>` | `-c` | Config file path | `MCPPROXY_CONFIG` |
| `--project <path>` | `-p` | Project context *(WIP)* | `MCPPROXY_PROJECT` |
| `--profile <name>` | | Use specific profile *(WIP)* | `MCPPROXY_PROFILE` |

### Output Format Design Rationale

The `-o/--output` flag follows the **kubectl/AWS CLI pattern** for extensibility, while `--json` provides a convenient alias following the **gh CLI pattern**:

| Pattern | Tools Using It | Pros | Cons |
|---------|---------------|------|------|
| `-o <format>` | kubectl, AWS, Azure, gcloud, Helm | Extensible, one flag for all formats | Slightly more typing |
| `--json` | gh, Terraform | Ergonomic for common case | Poor extensibility (need `--yaml`, `--csv`, etc.) |

**Hybrid approach**: Use `-o` as primary for extensibility, `--json` as alias for ergonomics.

```go
// Implementation pattern
cmd.Flags().StringVarP(&outputFormat, "output", "o", "table",
    "Output format: table, json, yaml, csv")
cmd.Flags().BoolVar(&jsonOutput, "json", false,
    "Shorthand for -o json")
cmd.MarkFlagsMutuallyExclusive("output", "json")
```

This allows future format additions without new flags:
```bash
mcpproxy activity list -o json      # Primary pattern
mcpproxy activity list --json       # Convenience alias
mcpproxy activity list -o csv       # Export format
mcpproxy activity list -o jsonl     # Streaming format
mcpproxy activity list -o yaml      # Config-friendly format
```

---

## Environment Variables

```bash
# Core settings
MCPPROXY_CONFIG=/path/to/config.json    # Config file path
MCPPROXY_DATA_DIR=~/.mcpproxy           # Data directory
MCPPROXY_API_KEY=xxx                    # API key for REST API

# WIP: Project/Profile support (optional, not yet implemented)
MCPPROXY_PROJECT=/path/to/project       # Project context
MCPPROXY_PROFILE=work                   # Active profile

# Agent-friendly modes
MCPPROXY_NO_PROMPT=1                    # Disable all prompts
MCPPROXY_JSON=1                         # Default to JSON output
MCPPROXY_QUIET=1                        # Minimal output
NO_COLOR=1                              # Disable colors

# CI/Automation detection
CI=true                                 # Auto-detected, disables interactive
MCPPROXY_HEADLESS=1                     # No browser for OAuth

# Debug
MCPPROXY_DEBUG=1                        # Verbose logging
MCPPROXY_LOG_LEVEL=debug                # Log level
```

---

## Exit Codes

| Code | Meaning | Agent Action |
|------|---------|--------------|
| 0 | Success | Continue |
| 1 | General error | Report/retry |
| 2 | Usage error (bad args) | Fix command syntax |
| 3 | Auth required | Run `auth login` |
| 4 | Server not found | Check `upstream list` |
| 5 | Tool not found | Check `tools list` |
| 6 | Network error | Retry with backoff |
| 7 | Rate limited | Wait and retry |
| 10 | Config error | Run `config validate` |

---

## Structured Error Output

When `-o json` or `--json` is used, errors include recovery hints:

```json
{
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Not authenticated for server 'github'",
    "guidance": "Run 'mcpproxy auth login --server github'",
    "recovery_command": "mcpproxy auth login --server github",
    "docs_url": "https://docs.mcpproxy.dev/errors/AUTH_REQUIRED",
    "context": {
      "server": "github",
      "working_directory": "/Users/user/project",
      "active_profile": "work"
    }
  }
}
```

---

## Server Management (Claude-Style Notation)

The `upstream add` command follows the [Claude Code MCP CLI](https://docs.anthropic.com/en/docs/claude-code/mcp) notation for consistency across the MCP ecosystem.

### Adding HTTP/SSE Servers

```bash
# Basic HTTP server
mcpproxy upstream add notion https://mcp.notion.com/sse

# With custom headers (repeatable)
mcpproxy upstream add github https://api.github.com/mcp \
  --header "Authorization: Bearer $GITHUB_TOKEN" \
  --header "X-Custom-Header: value"

# Explicit transport type
mcpproxy upstream add --transport http myapi https://api.example.com/mcp
```

### Adding Stdio Servers

The `--` separator divides mcpproxy flags from the server command:

```bash
# Basic stdio server
mcpproxy upstream add filesystem -- npx -y @anthropic/mcp-server-filesystem

# With environment variables (repeatable)
mcpproxy upstream add github-mcp \
  --env GITHUB_TOKEN=ghp_xxx \
  --env GITHUB_ORG=myorg \
  -- npx -y @anthropic/mcp-server-github

# With working directory
mcpproxy upstream add project-tools \
  --working-dir /path/to/project \
  -- node ./tools/mcp-server.js

# Complex command with arguments
mcpproxy upstream add postgres \
  --env DATABASE_URL=postgres://user:pass@localhost/db \
  -- npx -y @anthropic/mcp-server-postgres --readonly
```

### Adding from JSON

For complex configurations, use `add-json`:

```bash
# HTTP server with full config
mcpproxy upstream add-json weather-api '{
  "url": "https://api.weather.com/mcp",
  "protocol": "http",
  "headers": {
    "Authorization": "Bearer token",
    "X-API-Version": "2"
  }
}'

# Stdio server with full config
mcpproxy upstream add-json db-tools '{
  "command": "npx",
  "args": ["-y", "@anthropic/mcp-server-postgres"],
  "protocol": "stdio",
  "env": {
    "DATABASE_URL": "postgres://localhost/mydb"
  },
  "working_dir": "/app"
}'
```

### Project-Scoped Servers

Use `--scope project` to add servers to `.mcpproxy/config.json` in the current project:

```bash
# Add to project config (committed to git, shared with team)
mcpproxy upstream add project-db \
  --scope project \
  --env DATABASE_URL=\${DATABASE_URL} \
  -- npx -y @anthropic/mcp-server-postgres

# Add to global config (default, user-specific)
mcpproxy upstream add personal-api \
  --scope global \
  -- node ~/tools/my-mcp-server.js
```

### Removing Servers

```bash
# Remove from global config (default)
mcpproxy upstream remove github

# Remove from project config
mcpproxy upstream remove project-db --scope project

# Idempotent remove (no error if missing)
mcpproxy upstream remove old-server --if-exists

# Skip confirmation prompt
mcpproxy upstream remove github --yes
```

### Command Notation Summary

| Pattern | Example |
|---------|---------|
| HTTP server | `mcpproxy upstream add <name> <url>` |
| Stdio server | `mcpproxy upstream add <name> -- <cmd> [args...]` |
| With env vars | `--env KEY=value` (repeatable) |
| With headers | `--header "Name: value"` (repeatable) |
| Working dir | `--working-dir /path` |
| Project scope | `--scope project` |
| From JSON | `mcpproxy upstream add-json <name> '<json>'` |

---

## Idempotent Operations

Commands should be safe to run multiple times:

```bash
mcpproxy upstream add github https://api.github.com/mcp --if-not-exists
mcpproxy upstream remove github --if-exists
mcpproxy upstream enable github                # No-op if already enabled
```

---

## Output Formatting Options

```bash
# JSON output (machine-readable) - two equivalent ways
mcpproxy upstream list -o json       # Primary (kubectl/AWS style)
mcpproxy upstream list --json        # Alias (gh style)

# Other formats (extensible)
mcpproxy upstream list -o table      # Default human-readable
mcpproxy upstream list -o yaml       # Config-friendly
mcpproxy activity export -o csv      # Spreadsheet export
mcpproxy activity list -o jsonl      # Streaming/line-delimited JSON

# JQ filtering (built-in, no external jq needed)
mcpproxy upstream list -o json --jq '.[] | select(.health.level == "unhealthy")'

# Names only (minimal output)
mcpproxy upstream list --names-only
# github
# jira
# confluence

# Quiet mode (just data, no headers/decorations)
mcpproxy upstream list --quiet
```

---

## Anti-Patterns to Avoid

| Anti-Pattern | Problem | Solution |
|--------------|---------|----------|
| Interactive pagers | Agents hang waiting | Auto-disable when no TTY |
| ASCII art/banners in data output | Token waste, parsing issues | Only in `--help` |
| Emoji in JSON fields | Parsing issues | Emoji only in human output |
| Verbose success messages | Token waste | Silent success (`exit 0`) |
| Changing JSON field names | Breaks agent scripts | Version schemas, never remove fields |
| Inconsistent casing | Unpredictable | Always `snake_case` in JSON |
| Mixing stdout/stderr | Breaks pipes | Data→stdout, logs→stderr |

---

## Implementation Notes

### JQ Library

For built-in `--jq` support, use: [gojq](https://github.com/itchyny/gojq)

```go
import "github.com/itchyny/gojq"

func filterJSON(data interface{}, expr string) (interface{}, error) {
    query, err := gojq.Parse(expr)
    if err != nil {
        return nil, err
    }
    iter := query.Run(data)
    // ...
}
```

### Shell Completion

Cobra provides built-in completion generation:

```go
rootCmd.AddCommand(&cobra.Command{
    Use:   "completion [bash|zsh|fish|powershell]",
    RunE: func(cmd *cobra.Command, args []string) error {
        switch args[0] {
        case "bash":
            return rootCmd.GenBashCompletion(os.Stdout)
        // ...
        }
    },
})
```

---

## Priority Matrix

| Priority | Features |
|----------|----------|
| **P0** | Universal `-o/--output` with `--json` alias, `--help-json`, non-interactive mode, completion, upstream/tools/call/code/auth, doctor |
| **P1** | `--jq` filtering, structured errors, yaml/csv output formats |
| **P2** | config commands, run scripts, activity commands (RFC-003), integrate commands |
| **P3** | watch commands, registry operations |
| **WIP** | project management, profile management *(design TBD)* |

---

## Discussion Questions for Community

1. **`mcpproxy api` command**: Should we add a direct REST API access command like `gh api`? Or is CLI coverage sufficient?

2. **Profile vs Project**: Are these distinct concepts or should we merge them? Current proposal: Projects are directory-based, Profiles are named server groups.

3. **Activity log & security**: How much audit/security detail should be exposed via CLI? See RFC-003 (Activity Log) and RFC-004 (Security & Attack Detection).

4. **Integration commands**: Which MCP clients should we prioritize for import/export? (Claude, Cursor, Goose, etc.)

---

## References

- [Claude Code MCP CLI](https://docs.anthropic.com/en/docs/claude-code/mcp) - Server management notation
- [GitHub CLI Patterns](https://cli.github.com/manual/)
- [Cobra CLI Framework](https://cobra.dev/)
- [12 Factor CLI Apps](https://medium.com/@jdxcode/12-factor-cli-apps-dd3c227a0e46)
- [kubectl Output Formatting](https://kubernetes.io/docs/reference/kubectl/#output-options)
- [AWS CLI Output Format](https://docs.aws.amazon.com/cli/latest/userguide/cli-usage-output-format.html)
- [Command Line Interface Guidelines](https://clig.dev/)
- Issue #55: Projects/Profiles Support
- Issue #136: Code Execution with MCP
