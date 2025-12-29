# MCPProxy CLI Improvements Brainstorm

## Executive Summary

MCPProxy has a unique advantage as a **local-first** MCP middleware solution. Unlike cloud-based alternatives, it runs on the user's machine, enabling powerful CLI interactions for both humans and AI agents. This document explores how to leverage this advantage by learning from successful CLI patterns in `gh` (GitHub CLI) and `wrangler` (Cloudflare).
(NOTE: Goal is to design solid CLI structure)
---

## Part 1: Analysis of Successful CLI Patterns

### 1.1 GitHub CLI (gh) - What Makes It Agent-Friendly

The GitHub CLI has become the gold standard for AI agent interaction. Key patterns:

#### Structural Excellence
| Pattern | Description | Agent Benefit |
|---------|-------------|---------------|
| **Noun-verb hierarchy** | `gh <entity> <action>` (e.g., `gh pr create`) | Predictable command discovery |
| **Consistent subcommands** | `list`, `view`, `create`, `edit`, `delete` across all entities | Minimal learning curve |
| **JSON everywhere** | `--json` flag on all data commands | Machine-readable output |
| **Field selection** | `--json field1,field2` | Reduced token usage |
| **Built-in filtering** | `--jq` flag | No external tools needed |
| **Template support** | `--template` with Go templates | Custom formatting |

#### The Power of `gh api`
```bash
# Direct API access - the escape hatch
gh api repos/{owner}/{repo}/issues --jq '.[].title'
gh api graphql -f query='{ viewer { login }}'
```

**Why this matters**: `gh api` provides complete API coverage when specific commands don't exist. AI agents can use the full GitHub API without needing CLI commands for every operation.

#### Agent-Specific Features
- `GH_PROMPT_DISABLED=1` - Disables all interactive prompts
- Predictable exit codes: 0 (success), 1 (error), 2 (cancelled), 4 (auth)
- Context awareness from `.git` directory
- Field discovery via `--json help` or `--json ?`

### 1.2 Wrangler CLI - Developer Experience Patterns

Cloudflare's Wrangler excels at project-local configuration:

#### Configuration Hierarchy
```
1. Command-line flags (highest priority)
2. Environment variables
3. Project-local wrangler.toml/wrangler.json
4. User defaults
```

#### Key Patterns
| Pattern | Description | Relevance to mcpproxy |
|---------|-------------|----------------------|
| **Project-local config** | `wrangler.toml` in project root | `.mcpproxy/` directory support |
| **Environment overrides** | `[env.staging]`, `[env.production]` | Profile/project switching |
| **Type generation** | `wrangler types` creates TypeScript defs | Schema generation for tools |
| **Live tailing** | `wrangler tail` for real-time logs | `mcpproxy upstream logs --follow` |
| **Local-first dev** | Uses actual `workerd` runtime | MCP server testing locally |

#### Gaps in Wrangler (To Avoid)
- Incomplete `--json` support across all commands
- No formal JSON schema documentation
- Some commands require text parsing (brittle for agents)

---

## Part 2: Current MCPProxy CLI Assessment

### 2.1 Current Command Structure

```
mcpproxy/
├── serve                    # Start the proxy server
├── search-servers           # Search MCP registries
├── tools/
│   └── list                 # List tools from upstream server
├── call/
│   └── tool                 # Call specific tool
├── code/
│   └── exec                 # Execute JavaScript code
├── auth/
│   ├── login                # OAuth authentication
│   ├── status               # Check OAuth status
│   └── logout               # Clear OAuth tokens
├── secrets/
│   ├── set, get, del, list  # Secret management
│   └── migrate              # Migrate plaintext secrets
├── upstream/
│   ├── list                 # List all servers
│   ├── logs                 # View server logs
│   ├── enable, disable      # Toggle server state
│   └── restart              # Restart server
├── doctor                   # Health checks
└── trust-cert               # Install CA certificate
```

### 2.2 Strengths
- Dual mode: Daemon client vs. standalone operation
- Unified health status across all interfaces
- JSON output support on key commands
- Socket communication for local auth bypass
- Actionable CLI hints in output

### 2.3 Gaps Identified
1. **No `mcpproxy api` command** - Direct REST API access like `gh api`
2. **No project-local configuration** - All config is global
3. **Limited output format options** - Missing `--jq`, `--template`
4. **No profile/project switching** - Single global context
5. **Missing batch operations** - Can't pipe/chain commands easily
6. **No completion generation** - Manual shell setup required


(NOTE: Need to genearate a map of full comand structures, old + new not existed yet. To have an overview, also add main params)
---

## Part 3: New Command Ideas

### 3.1 `mcpproxy api` - Direct REST API Access (Reconsidered)

**Inspired by**: `gh api`

```bash
# Direct API calls with authentication handled automatically
mcpproxy api /servers
mcpproxy api /servers/github/tools --jq '.[].name'
mcpproxy api POST /servers/github/restart
```

**Why `gh api` works but `mcpproxy api` may not**:

| Factor | GitHub API | mcpproxy API |
|--------|-----------|--------------|
| In LLM training data | ✅ Extensive | ❌ None |
| API surface | Thousands of endpoints | ~20 endpoints |
| Schema discovery | Agents know from training | Must load OAS (bloat) |
| CLI coverage | Partial (need escape hatch) | Complete (all ops have commands) |

**The Schema Discovery Problem**:
```bash
# Agent wants to POST to /servers
# How does it know the request body format?

# Option 1: Load full OAS spec (~3000 tokens) - BLOAT
# Option 2: Per-endpoint discovery
mcpproxy api /servers --describe   # Returns schema for ONE endpoint
```

**Recommendation**: Consider if `mcpproxy api` is needed at all. (NOTE: it's open question for community)

- mcpproxy's API is small and stable
- All operations should have dedicated CLI commands
- CLI commands are self-documenting via `--help`
- The "escape hatch" benefit is minimal for a small API

**If implemented**, require per-endpoint schema discovery:

```bash
# Discover schema for specific endpoint (minimal context)
mcpproxy api /servers --describe
{
  "GET": {"response": [{"name": "string", "health": {...}}]},
  "POST": {"body": {"name": "string", "url": "string"}, "response": {...}}
}

# Show example for specific operation
mcpproxy api /servers --example POST
# curl -X POST -d '{"name":"github","url":"https://..."}'

# List all endpoints (one line each, minimal)
mcpproxy api --list
# GET  /servers         - List servers
# POST /servers         - Add server
# GET  /servers/{name}  - Get server details
# ...
```

**Alternative**: Skip `mcpproxy api` entirely, ensure CLI covers all operations:
```bash
mcpproxy upstream list --json          # Instead of: mcpproxy api GET /servers
mcpproxy upstream add --name x --url y # Instead of: mcpproxy api POST /servers
```

### 3.2 `mcpproxy init` - Project Initialization

**Inspired by**: `wrangler init`, `npm init`

```bash
# Initialize project-local mcpproxy configuration
mcpproxy init
# Creates .mcpproxy/config.json in current directory

mcpproxy init --template python-dev
# Uses predefined template for Python development

mcpproxy init --from-global
# Copies relevant servers from global config
```

**Creates**:
```
.mcpproxy/
├── config.json          # Project-specific servers
├── scripts/             # Project-local code execution scripts
└── tools/               # Custom tool definitions (future)
```

### 3.3 `mcpproxy profile` - Profile Management

**Inspired by**: `gh auth switch`, Issue #55

```bash
# List profiles
mcpproxy profile list

# Create profile
mcpproxy profile create work --servers github,jira,confluence
mcpproxy profile create personal --servers github,ast-grep

# Switch active profile (for daemon)
mcpproxy profile switch work

# Set working directory for profile
mcpproxy profile set-workdir work /path/to/work/projects

# Show current profile
mcpproxy profile current

# Use specific profile for single command
mcpproxy upstream list --profile work
mcpproxy tools list --profile personal --server ast-grep
```

### 3.4 `mcpproxy run` - Execute Saved Scripts

**Inspired by**: `npm run`, Issue #136

```bash
# Run saved code execution script
mcpproxy run github-stats --input '{"repo": "anthropics/claude"}'

# Run script from file
mcpproxy run --file ./scripts/analyze.js

# List available scripts
mcpproxy run --list

# Run with profile context
mcpproxy run deployment-check --profile work
```

**Script storage**:
- Global: `~/.mcpproxy/scripts/`
- Project-local: `.mcpproxy/scripts/`

### 3.5 `mcpproxy debug` - Enhanced Debugging (NOTE: we don't need this for now)

```bash
# Trace a tool call through the system
mcpproxy debug trace --tool github:get_repo --args '{"owner":"anthropics","repo":"claude"}'

# Show communication between proxy and upstream
mcpproxy debug intercept --server github

# Validate server configuration
mcpproxy debug validate --server github

# Test connectivity without side effects
mcpproxy debug ping --server github

# Show detailed timing breakdown
mcpproxy debug timing --tool github:create_issue --args '{...}'

# Capture and replay MCP messages
mcpproxy debug capture --server github --output capture.json
mcpproxy debug replay --input capture.json
```

### 3.6 `mcpproxy completion` - Shell Completion

**Inspired by**: `gh completion`, `kubectl completion`

```bash
# Generate completion script
mcpproxy completion bash > /etc/bash_completion.d/mcpproxy
mcpproxy completion zsh > ~/.zsh/completions/_mcpproxy
mcpproxy completion fish > ~/.config/fish/completions/mcpproxy.fish
mcpproxy completion powershell > mcpproxy.ps1
```

### 3.7 `mcpproxy config` - Configuration Management (NOTE: good to have backup functions, think of it)

```bash
# Get config value
mcpproxy config get listen
mcpproxy config get mcpServers --json

# Set config value
mcpproxy config set listen "127.0.0.1:9090"
mcpproxy config set enable_code_execution true

# Edit config in $EDITOR
mcpproxy config edit

# Validate configuration
mcpproxy config validate

# Show effective config (merged from all sources) (NOTE: what is merging here, which configs? main + project?)
mcpproxy config show --effective

# Export/import configuration
mcpproxy config export --output backup.json
mcpproxy config import --input backup.json
```

### 3.8 `mcpproxy registry` - Registry Operations

```bash
# List known registries
mcpproxy registry list

# Search across registries
mcpproxy registry search "github" --limit 10

# Add custom registry
mcpproxy registry add mycompany https://mcp.internal.company.com

# Install server from registry
mcpproxy registry install mcp-server-github

# Show server details from registry
mcpproxy registry info mcp-server-github
```

### 3.9 `mcpproxy test` - Testing Commands (NOTE: Don't need for now)

**For developing mcpproxy itself and testing servers**:

```bash
# Test a server configuration before adding
mcpproxy test server --url https://api.github.com/mcp

# Test tool execution (dry-run)
mcpproxy test tool github:get_repo --args '{"owner":"test"}' --dry-run

# Run mcpproxy's own test suite
mcpproxy test self

# Benchmark server response times
mcpproxy test benchmark --server github --iterations 10
```

### 3.10 `mcpproxy watch` - Live Monitoring (NOTE: ok, but low priority)

```bash
# Watch server status changes
mcpproxy watch servers

# Watch tool calls in real-time
mcpproxy watch calls

# Watch with filtering
mcpproxy watch calls --server github --format json (NOTE: good idea)

# Watch events (SSE stream to CLI)
mcpproxy watch events
```

---

## Part 4: Agent-Friendly Enhancements

### 4.1 Universal Output Formatting

Apply to ALL commands:

```bash
# JSON output (machine-readable)
mcpproxy upstream list --json

# JSON with field selection (reduces tokens)
mcpproxy upstream list --json name,health.level,health.action

# JQ filtering (built-in)
mcpproxy upstream list --jq '.[] | select(.health.level == "unhealthy")'

# Go template output
mcpproxy upstream list --template '{{range .}}{{.name}}\t{{.health.summary}}{{end}}'

# CSV output (for spreadsheets/analysis)
mcpproxy upstream list --csv

# Quiet mode (just data, no headers)
mcpproxy upstream list --quiet
```

### 4.2 Non-Interactive Mode

```bash
# Disable all prompts globally
export MCPPROXY_PROMPT_DISABLED=1

# Or per-command
mcpproxy upstream disable-all --force
mcpproxy auth login --server github --no-browser --token $TOKEN
```

### 4.3 Structured Errors

```json
{
  "error": {
    "code": "SERVER_NOT_FOUND",
    "message": "Server 'github' not found",
    "suggestion": "Run 'mcpproxy upstream list' to see available servers",
    "docs_url": "https://docs.mcpproxy.dev/errors/SERVER_NOT_FOUND"
  }
}
```

### 4.4 Command Discovery for Agents

```bash
# List all commands with descriptions
mcpproxy --help-json

# Get command schema (for function calling)
mcpproxy schema upstream list
# Returns JSON schema for flags, args, output

# List all available tool names
mcpproxy tools list --all-servers --names-only
```

### 4.5 Batch Operations (NOTE: no need for now)

```bash
# Accept commands from stdin
echo '{"server": "github", "action": "restart"}' | mcpproxy batch

# Execute multiple commands from file
mcpproxy batch --file commands.jsonl

# Parallel execution
mcpproxy batch --file commands.jsonl --parallel 5
```

---

## Part 5: Project-Local Features

### 5.1 Configuration Hierarchy

```
Priority (highest to lowest):
1. Command-line flags
2. Environment variables (MCPPROXY_*)
3. Project-local: ./.mcpproxy/config.json
4. User config: ~/.mcpproxy/mcp_config.json
5. System defaults
```

### 5.2 Project-Local Scripts

From Issue #136, support project-specific code execution:

```
.mcpproxy/
├── config.json           # Project servers
└── scripts/
    ├── deploy.js         # Custom deployment workflow
    ├── analyze.js        # Code analysis workflow
    └── sync.js           # Data synchronization
```

Usage:
```bash
# Auto-discovered from .mcpproxy/scripts/
mcpproxy run deploy --input '{"env": "staging"}'
```

### 5.3 Project-Scoped Servers

Allow servers to be defined per-project:

```json
// .mcpproxy/config.json
{
  "mcpServers": [
    {
      "name": "ast-grep-project",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "${PROJECT_ROOT}"
    }
  ],
  "inherit_global": ["github", "jira"]
}
```

### 5.4 Project Detection (NOTE: also we need to implement switcher in WebUI, and parametrise REST API with project. Add --project param to relevant CLI calls)

Auto-detect project context:

```bash
# When in /path/to/myproject with .mcpproxy/ directory:
mcpproxy upstream list
# Shows: project servers + inherited global servers

mcpproxy upstream list --global-only
# Shows: only global servers

mcpproxy upstream list --project-only
# Shows: only project servers
```

---

## Part 6: Developer Experience for mcpproxy Development (NOTE: no need for now, remove)

### 6.1 Self-Development Commands

```bash
# Run mcpproxy tests
mcpproxy dev test [--unit | --e2e | --all]

# Run linter
mcpproxy dev lint [--fix]

# Generate code (swagger, mocks, etc.)
mcpproxy dev generate

# Start development server with hot reload
mcpproxy dev serve

# Build for current platform
mcpproxy dev build

# Create release artifacts
mcpproxy dev release --version v1.0.0
```

### 6.2 Documentation Commands  (NOTE: no need for now, remove)

```bash
# Generate CLI documentation
mcpproxy docs generate --output docs/cli/

# Serve documentation locally
mcpproxy docs serve

# Validate documentation
mcpproxy docs validate
```

---

## Part 7: Integration Ideas

### 7.1 MCP Server Development  (NOTE: no need for now, remove)

```bash
# Create new MCP server project
mcpproxy create-server myserver --template typescript

# Test server locally
mcpproxy dev-server ./my-server --watch

# Publish to registry
mcpproxy publish-server ./my-server
```

### 7.2 IDE Integration  (NOTE: no need for now, remove)

```bash
# Generate VS Code settings
mcpproxy ide vscode --output .vscode/

# Generate JetBrains settings
mcpproxy ide jetbrains --output .idea/

# Start language server for IDE
mcpproxy ide lsp
```

### 7.3 CI/CD Integration  (NOTE: no need for now, remove)

```bash
# Validate config for CI
mcpproxy ci validate

# Health check for CI pipelines
mcpproxy ci health --exit-on-unhealthy

# Generate CI workflow files
mcpproxy ci generate --platform github
```


---

(NOTE: add functions for adding mcpproxy as mcp server into configs: claude, cursor, goose etc main mcp clients. User should be able to point config file path. And mpproxy should be able to read and import mcp servers from main mcp clients and add then into own mcp_config.json
Such import, integration functions must be implemented in core and must be avalible viw REST API, WEbUI and CLI)

## Part 8: Summary of Priorities (NOTE: review priorities after updating doc)

### P0 - Critical (Agent Fundamentals)

| Feature | Rationale |
|---------|-----------|
| Universal `--json` | Machine-readable output for all commands |
| Universal `--help-json` | Hierarchical discovery without context bloat |
| Non-interactive mode | `MCPPROXY_NO_PROMPT=1`, `--yes` flags |
| `mcpproxy completion` | Shell completion (bash/zsh/fish/powershell) |

### P1 - High Priority (Core Features)

| Feature | Rationale |
|---------|-----------|
| `--jq` filtering | Built-in output filtering, token efficiency |
| `mcpproxy profile` | Issue #55 - multi-context support |
| `mcpproxy init` | Project-local `.mcpproxy/` config |
| Enhanced exit codes | Semantic codes for agent error recovery |
| Structured errors | JSON errors with `recovery_command` |

### P2 - Medium Priority (Enhanced Workflows)

| Feature | Rationale |
|---------|-----------|
| `mcpproxy run` | Issue #136 - saved scripts |
| `mcpproxy config` | Config management CLI |
| `mcpproxy schema <cmd>` | Detailed command JSON schemas |
| `mcpproxy debug` | Debugging/tracing tools |

### P3 - Lower Priority (Nice-to-Have)

| Feature | Rationale |
|---------|-----------|
| `mcpproxy watch` | Real-time event streaming |
| `mcpproxy registry` | Registry operations |
| `mcpproxy test` | Testing workflows |
| ~~`mcpproxy api`~~ | Optional - CLI covers all operations |

### Rejected

| Feature | Reason |
|---------|--------|
| MCP transport mode | Connection timing breaks dev loop; tool bloat |
| MCP self-description | Same issues as transport mode |

---

## Part 9: Agent Workflow Examples (NOTE: that all these functions must be avalible via mcp tools also, so AI agent can use mcp protocol to repair github upstream server e.g.)

### Example 1: AI Agent Debugging a Failing Server 

```bash
# Check overall health
mcpproxy doctor --json | jq '.issues'

# Find problematic server
mcpproxy upstream list --jq '.[] | select(.health.level == "unhealthy")'

# Get server logs
mcpproxy upstream logs github --tail 100 --json

# Try restarting
mcpproxy upstream restart github

# Verify fix
mcpproxy debug ping --server github (NOTE: remove)
```

### Example 2: AI Agent Setting Up Project

```bash
# Initialize project config
mcpproxy init (NOTE: how we will know project name? or we will identify them by file path only?)

# Add project-specific server
mcpproxy api POST /servers --input '{"name":"ast-grep-project","command":"npx","args":["ast-grep-mcp"]}' (NOTE: need to have parameter - project, so main mcpproxy daemon can distinguish to which project add this server)

# Verify tools available
mcpproxy tools list --server ast-grep-project --json (NOTE: project parameter needed)
```

### Example 3: AI Agent Running Complex Workflow

```bash
# Execute saved workflow script
mcpproxy run analyze-codebase --input '{"path":"./src"}' --json

# Or use code exec directly
mcpproxy code exec --file ./scripts/analyze.js --input '{"path":"./src"}'
```

---

## Part 10: Deep Research Additions (OpenAI Analysis) (NOTE: need to enrich main doc using these additions, except that I will mark to deletion)

### 10.1 MCP Self-Description - The Ultimate Agent Integration

**Key Insight**: Since MCPProxy is itself an MCP middleware, it should **expose its own CLI as an MCP server**. (NOTE: no need, delete)

```bash
# CLI commands become MCP tools that agents can discover
mcpproxy mcp serve-cli  # Expose CLI as MCP endpoint
```

**Benefits**:
- Agents don't need CLI training data - they query capabilities in real-time
- Formal input/output schemas via MCP protocol
- Version-stable interface (MCP handles schema evolution)
- Any MCP-compatible agent can use mcpproxy without custom integration

**Implementation**: Each CLI command becomes an MCP "tool" with:
- `name`: Command path (e.g., `upstream_list`, `tools_search`)
- `description`: Help text
- `inputSchema`: JSON Schema for flags/args
- `outputSchema`: JSON Schema for response

### 10.2 AI-Guidance in Error Messages

**Pattern**: Embed actionable hints directly in error output that guide agents to recovery.

```bash
# Current error
ERROR: Not authenticated

# AI-friendly error
ERROR [AuthRequired]: Not authenticated for server 'github'.
GUIDANCE: Run "mcpproxy auth login --server github" to authenticate.
```

**Implementation**:
```json
{
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Not authenticated for server 'github'",
    "guidance": "Run 'mcpproxy auth login --server github'",
    "recovery_command": "mcpproxy auth login --server github"
  }
}
```

The `recovery_command` field lets agents automatically attempt fixes.

### 10.3 Stateless & Idempotent Operations

**Principle**: Design commands so running them twice has the same effect as once.

| Command | Idempotent? | Notes |
|---------|-------------|-------|
| `upstream list` | ✅ Yes | Read-only |
| `upstream enable github` | ✅ Yes | Already enabled = no-op |
| `upstream add github ...` | ⚠️ Partial | Should check if exists |
| `tools list` | ✅ Yes | Read-only |
| `call tool` | ❌ No | Side effects depend on tool |

**Recommendation**: Add `--if-not-exists` and `--if-exists` flags:
```bash
mcpproxy upstream add github --if-not-exists  # No error if exists
mcpproxy upstream remove github --if-exists    # No error if missing
```

### 10.4 Output Stability & Schema Versioning 

**Problem**: Changing JSON output breaks agent scripts.

**Solution**: Embed schema version in all JSON output:
```json
{
  "schema_version": 1,
  "data": {
    "servers": [...]
  }
}
```

**Practices**:
- Never remove fields without major version bump
- Add new fields as optional
- Document JSON schemas in `oas/cli-schemas/`
- Test output against schemas in CI

### 10.5 Token Efficiency Patterns

AI agents have context limits. Minimize output tokens:

| Strategy | Example |
|----------|---------|
| **Field selection** | `--json name,status` returns only those fields |
| **Quiet mode** | `--quiet` suppresses headers, decorations |
| **Count mode** | `--count` returns just a number |
| **Names only** | `--names-only` returns newline-delimited names |
| **Summary mode** | `--summary` returns condensed overview |

```bash
# Token-heavy (bad for agents)
mcpproxy upstream list
# NAME        PROTOCOL  TOOLS  STATUS
# github      http      45     ✅ Healthy (Connected)
# jira        stdio     12     ⚠️ Degraded (Token expiring)
# ...100 lines of formatted output...

# Token-efficient (good for agents)
mcpproxy upstream list --json name,health.level --quiet
# [{"name":"github","health":{"level":"healthy"}},{"name":"jira","health":{"level":"degraded"}}]

# Ultra-minimal
mcpproxy upstream list --names-only
# github
# jira
```

### 10.6 Anti-Patterns Catalog

**Avoid these patterns that break AI agent usage:**

| Anti-Pattern | Problem | Solution |
|--------------|---------|----------|
| **Interactive pagers** | Agents hang waiting for input | Auto-disable when no TTY |
| **ASCII art/banners** | Wastes tokens, confuses parsing | Only in `--help` | (NOTE: we have it in upstream list)
| **Changing JSON fields** | Breaks parsing | Version schemas |
| **Verbose success messages** | Token waste | Silent success (`exit 0`) |
| **Emoji in data fields** | Parsing issues | Emoji only in human output | (NOTE: we have it in upstream list)
| **Inconsistent casing** | Unpredictable | Always `snake_case` in JSON |
| **Nested JSON** | Hard to query | Flatten where possible |
| **Mixing stdout/stderr** | Breaks pipes | Data→stdout, logs→stderr |

### 10.7 Dry-Run & Validation Flags (NOTE: relevant for some cases like call tool but not for profile switch)

**Pattern**: Let agents preview actions before executing.

```bash
# Validate without executing
mcpproxy call tool github:create_issue --dry-run --args '{...}'
# Output: Would create issue with title "..." in repo "..."

# Validate config before applying
mcpproxy config set listen "0.0.0.0:8080" --validate
# Output: Warning: Binding to all interfaces exposes API to network

# Preview profile switch effects
mcpproxy profile switch work --dry-run
# Output: Would enable: github, jira. Would disable: personal-gitlab
```

### 10.8 Enhanced Exit Codes

**Semantic exit codes help agents decide next steps:**

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

```bash
mcpproxy call tool github:create_issue --args '{}'
# Exit code 3 → Agent knows to run auth login
```

### 10.9 Innovative Ideas 

#### 10.9.1 ~~MCP as CLI Transport~~ (Rejected) (NOTE: remove)

**Original idea**: Expose CLI as MCP server with JSON-RPC over stdin/stdout.

**Why it doesn't work**:

1. **Connection timing**: MCP servers must be configured before conversation starts. Development loop breaks:
   ```
   code change → rebuild → restart agent session → reconnect MCP
                           ↑ breaks flow
   ```
   CLI doesn't have this problem - each command uses current binary.

2. **Tool discovery bloat**: If mcpproxy exposes 50 CLI commands as MCP tools:
   ```
   50 tools × ~100 tokens each = 5000+ tokens just for tool list
   ```
   But agents using CLI discover commands on-demand via `--help`.

3. **gh CLI proves CLI-first works**: gh is successful BECAUSE it's CLI, not despite it. Agents are trained on:
   - Running `--help` to discover commands
   - Parsing JSON output
   - Following noun-verb patterns

**Better approach**: CLI-first with hierarchical discovery (see Section 10.12).

#### 10.9.2 Agent Loop Detection (NOTE: Low priority)

Detect when an AI agent is stuck in a retry loop:

```bash
# After 3 identical failed commands in 60 seconds:
WARNING: Detected repeated failures. Consider running 'mcpproxy doctor'
to diagnose issues, or check 'mcpproxy upstream logs <server>'.
```

#### 10.9.3 Context Breadcrumbs (NOTE: Low priority)

Include context in errors to help "lost" agents:

```bash
ERROR: Command failed
Context:
  Working directory: /Users/user/wrong-project
  Active profile: personal
  Config source: ~/.mcpproxy/mcp_config.json (no project config found)
Hint: Are you in the correct directory? No .mcpproxy/ found.
```

#### 10.9.4 Workflow Templates Library (NOTE: remove)

Pre-built orchestration scripts agents can invoke:

```bash
mcpproxy workflow list
# github-pr-review    Review PR and add comments
# jira-issue-sync     Sync GitHub issues to Jira
# deploy-staging      Run deployment checks

mcpproxy workflow run github-pr-review --input '{"pr": 123}'
```

### 10.10 Implementation Complexity Assessment

| Feature | Complexity | Impact | Priority |
|---------|------------|--------|----------|
| Universal `--json` | Low-Medium | High | P0 |
| `--jq` filtering | Medium | High | P0 |
| Non-interactive mode | Low | Critical | P0 |
| MCP self-description | High | Strategic | P1 |
| Profile management | Medium-High | High | P1 |
| Project-local config | Low-Medium | High | P1 |
| `mcpproxy api` | Medium | Medium | P2 |
| Doctor command | Low-Medium | Medium | P2 |
| Schema versioning | Low | Long-term | P2 |
| Dry-run flags | Low | Medium | P3 |
| Batch operations | Medium-High | Specific | P3 |
| MCP transport mode | High | Innovative | P4 |

**Recommended v2.0 Scope**: P0 + P1 features

### 10.11 Agent-Specific vs Human UX Comparison

| Aspect | Human Preference | Agent Preference |
|--------|------------------|------------------|
| Output verbosity | Descriptive | Minimal |
| Colors/formatting | Yes | No (or detect TTY) |
| Progress indicators | Spinners, bars | Silent or stderr |
| Error messages | Friendly text | Structured + codes |
| Confirmations | Interactive | `--yes` flag |
| Help text | Prose descriptions | JSON schema |
| Learning | Read docs once | Query every time |
| Recovery | Manual investigation | Automated via hints |

**Design Principle**: Default to human-friendly, but make agent-friendly mode easily accessible via flags or environment variables.

### 10.12 CLI-First Architecture (The gh Model)

**Core Principle**: CLI is the primary interface for both humans AND agents. MCP is only for upstream tool aggregation.

#### Why CLI-First Works

```
┌─────────────────────────────────────────────────────────────┐
│ How agents use gh CLI (proven pattern):                     │
│                                                             │
│ 1. Agent knows "gh <entity> <action>" from training         │
│ 2. Runs "gh pr --help" to discover subcommands (~200 tokens)│
│ 3. Runs "gh pr list --json" with known flags                │
│ 4. Parses JSON output                                       │
│                                                             │
│ Context cost: O(1) per command discovered                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ How MCP tool discovery works (problematic):                 │
│                                                             │
│ 1. Agent connects to MCP server                             │
│ 2. Server returns ALL tools with schemas (~5000 tokens)     │
│ 3. Agent picks one tool                                     │
│                                                             │
│ Context cost: O(n) where n = number of tools                │
└─────────────────────────────────────────────────────────────┘
```

#### Hierarchical Discovery Pattern

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
{
  "commands": [
    {"name": "list", "description": "List all servers"},
    {"name": "add", "description": "Add a server"},
    {"name": "logs", "description": "View server logs"}
  ]
}

# Level 2: How do I use "upstream add"? (~150 tokens)
mcpproxy upstream add --help-json
{
  "flags": {
    "--name": {"required": true, "type": "string"},
    "--url": {"required": true, "type": "string"},
    "--protocol": {"default": "http", "enum": ["http", "stdio"]}
  },
  "examples": [
    "mcpproxy upstream add --name github --url https://api.github.com/mcp"
  ]
}
```

**Total context for one operation**: ~400 tokens (3 help calls)
**MCP equivalent**: ~5000 tokens (full tool list)

#### What mcpproxy's MCP Interface Should Expose

Only the **tool aggregation** functions - what mcpproxy is FOR:

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
│ mcpproxy upstream list/add/remove/logs                      │
│ mcpproxy profile list/create/switch                         │
│ mcpproxy config get/set/validate                            │
│ mcpproxy doctor                                             │
│                                                             │
│ Agents use these via shell (like gh, docker, kubectl)       │
└─────────────────────────────────────────────────────────────┘
```

#### Development Loop Comparison

```
┌─────────────────────────────────────────────────────────────┐
│ CLI-based development (works):                              │
│                                                             │
│ 1. Edit code                                                │
│ 2. go build -o mcpproxy ./cmd/mcpproxy                      │
│ 3. ./mcpproxy upstream list --json                          │
│    ↑ Uses new binary immediately                            │
│ 4. Iterate                                                  │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ MCP-based development (broken):                             │
│                                                             │
│ 1. Edit code                                                │
│ 2. go build -o mcpproxy ./cmd/mcpproxy                      │
│ 3. Restart Claude/agent session ← BREAKS FLOW              │
│ 4. Reconnect MCP server                                     │
│ 5. Test                                                     │
└─────────────────────────────────────────────────────────────┘
```

#### Single Meta-Tool Escape Hatch (Optional)

For environments that can ONLY use MCP (can't shell out), provide ONE meta-tool:

```json
{
  "name": "mcpproxy_admin",
  "description": "Execute mcpproxy CLI commands. Run with command='--help-json' to discover available commands.",
  "inputSchema": {
    "properties": {
      "command": {
        "type": "string",
        "description": "CLI command without 'mcpproxy' prefix, e.g., 'upstream list --json'"
      }
    }
  }
}
```

**Context cost**: 1 tool (~100 tokens) instead of 50 tools (~5000 tokens)
**Discovery**: Agent runs `mcpproxy_admin(command="--help-json")` to learn structure

### 10.13 Revised Priority Matrix

Based on CLI-first architecture:

| Feature | Priority | Rationale |
|---------|----------|-----------|
| Universal `--json` output | P0 | Core agent requirement |
| Universal `--help-json` | P0 | Hierarchical discovery |
| Non-interactive mode | P0 | Agent requirement |
| `--jq` filtering | P1 | Token efficiency |
| Profile management | P1 | Issue #55 |
| Project-local config | P1 | Issue #55 |
| `mcpproxy schema <cmd>` | P2 | Detailed command schemas |
| Enhanced exit codes | P2 | Agent error recovery |
| ~~`mcpproxy api`~~ | P3/Optional | CLI commands cover all ops |
| ~~MCP transport mode~~ | Rejected | See 10.9.1 |

---

## Appendix A: Related GitHub Issues

- **Issue #55**: Projects/Profiles Support - "Not All MCP Servers Are Relevant for All Clients"
- **Issue #136**: Code Execution with MCP - Local scripts/tools support

## Appendix B: Research Documents

- [GitHub CLI Patterns for AI Agents](./gh-cli-patterns-for-ai-agents.md)
- [Wrangler CLI Patterns](./research-wrangler-cli-patterns.md)
- [OpenAI Deep Research: CLI Design for AI Agents](./deep_research_ai_cli.txt)

## Appendix C: Environment Variables Reference

```bash
# Core settings
MCPPROXY_CONFIG=/path/to/config.json
MCPPROXY_DATA_DIR=~/.mcpproxy
MCPPROXY_PROFILE=work
MCPPROXY_API_KEY=xxx

# Agent-friendly modes
MCPPROXY_NO_PROMPT=1        # Disable all prompts
MCPPROXY_NO_COLOR=1         # Disable colors
MCPPROXY_JSON=1             # Default to JSON output
MCPPROXY_QUIET=1            # Minimal output

# CI/Automation detection
CI=true                     # Auto-detected, disables interactive
MCPPROXY_HEADLESS=1         # No browser for OAuth

# Debug
MCPPROXY_DEBUG=1            # Verbose logging
MCPPROXY_LOG_LEVEL=debug    # Log level
```

## Appendix D: Proposed Command Tree (v2.0)

```
mcpproxy
├── serve                      # Start daemon
├── upstream                   # [P0] Server management
│   ├── list                   # List servers (--json, --help-json)
│   ├── add                    # Add server
│   ├── remove                 # Remove server
│   ├── enable/disable         # Toggle server
│   ├── restart                # Restart server
│   └── logs                   # View logs (--follow, --tail)
├── tools                      # [P0] Tool discovery
│   ├── list                   # List tools (--json)
│   ├── search                 # BM25 search
│   └── info                   # Tool details + schema
├── call                       # [P0] Tool execution
│   └── tool                   # Execute tool (--dry-run)
├── code                       # [P0] Code execution
│   └── exec                   # Run JavaScript
├── auth                       # [P0] Authentication
│   ├── login                  # OAuth login
│   ├── logout                 # Clear tokens
│   └── status                 # Check auth status
├── profile                    # [P1] Profile management
│   ├── list                   # List profiles
│   ├── create                 # Create profile
│   ├── switch                 # Switch active
│   ├── delete                 # Delete profile
│   └── current                # Show current
├── init                       # [P1] Initialize project .mcpproxy/
├── config                     # [P2] Configuration
│   ├── get/set                # Read/write config
│   ├── edit                   # Open in editor
│   ├── validate               # Validate config
│   └── where                  # Show config path
├── run <script>               # [P2] Execute saved script
├── schema <command>           # [P2] Show command JSON schema (NOTE: remove)
├── doctor                     # [P0] Health diagnostics
├── completion                 # [P0] Shell completion
│   ├── bash
│   ├── zsh
│   ├── fish
│   └── powershell
├── version                    # Version info
└── help                       # Help system

Global flags (all commands):
  --json                       # JSON output
  --help-json                  # Machine-readable help
  --jq <expr>                  # Filter JSON output  (NOTE: need to research which lib to use for it)
  --quiet                      # Minimal output
  --yes                        # Skip confirmations
  --profile <name>             # Use specific profile
  --config <path>              # Config file path

Environment variables:
  MCPPROXY_NO_PROMPT=1         # Disable all prompts  (NOTE: but do we have any prompts now?)
  MCPPROXY_JSON=1              # Default to JSON output
  CI=true                      # Auto-detected, non-interactive
```

**Note**: `mcpproxy api` is intentionally omitted. All operations have dedicated CLI commands with self-documenting `--help-json`. This avoids the schema discovery problem where agents would need to load full OAS spec.

---

## Part 11: Testing CLI Effectiveness for AI Agents

### 11.1 The Testing Challenge

Traditional CLI testing verifies correctness: "does command X produce output Y?"
Agent CLI testing must also verify **effectiveness**: "can an agent discover and use commands efficiently?"

```
┌─────────────────────────────────────────────────────────────┐
│ Traditional Testing:                                        │
│   Input → Command → Output → Assert(output == expected)     │
│                                                             │
│ Agent Effectiveness Testing:                                │
│   Task → Agent(discovers commands) → Trajectory → Evaluate  │
│        → Did agent find right commands?                     │
│        → How many attempts/tokens?                          │
│        → Did it recover from errors?                        │
│        → Was the path efficient?                            │
└─────────────────────────────────────────────────────────────┘
```

### 11.2 Leveraging mcp-eval Patterns

The existing `/Users/user/repos/mcp-eval/` project provides a sophisticated evaluation framework that can be adapted for CLI testing.

#### Key mcp-eval Concepts

| Concept | Description | CLI Testing Application |
|---------|-------------|------------------------|
| **Scenario** | YAML-defined test case with user intent | CLI task specification |
| **Expected Trajectory** | Sequence of expected tool calls | Expected command sequence |
| **Similarity Scoring** | 0.0-1.0 score comparing actual vs expected | Command sequence similarity |
| **Baseline Recording** | Capture "golden" execution for regression | Record optimal CLI usage |
| **Judge Agent** | LLM-based analysis of divergence | Analyze why agent used wrong commands |

#### Adapted Scenario Format for CLI Testing

```yaml
# scenarios/cli/list_unhealthy_servers.yaml
enabled: true
name: "Find Unhealthy Servers"
description: "Agent discovers and lists servers with health issues"

user_intent: "Show me which MCP servers have problems"

# Expected CLI command sequence
expected_trajectory:
  - action: "discover_command"
    command: "mcpproxy upstream --help-json"
  - action: "execute_command"
    command: "mcpproxy upstream list --json"
    args: {}
  - action: "filter_results"
    command: "mcpproxy upstream list --jq '.[] | select(.health.level != \"healthy\")'"

# Success criteria
success_criteria:
  - "upstream list"
  - "health"
  - "json"

# Effectiveness thresholds
metrics:
  max_commands: 5          # Should complete in ≤5 commands
  max_tokens: 2000         # Should use ≤2000 tokens
  max_help_calls: 2        # Should need ≤2 help lookups
  similarity_threshold: 0.8

tags:
  - "discovery"
  - "health_check"
  - "filtering"
```

#### Trajectory Similarity Scoring

```python
def calculate_cli_trajectory_similarity(expected: List[Command], actual: List[Command]) -> float:
    """
    Multi-level similarity scoring for CLI command sequences.

    Levels:
    1. Command name match (exact)
    2. Flag similarity (Jaccard on flag sets)
    3. Argument value similarity (string intersection)
    4. Order penalty (out-of-order commands)
    """
    scores = []

    for exp, act in zip_longest(expected, actual):
        if exp is None or act is None:
            scores.append(0.0)  # Missing command
            continue

        # Command name must match
        if exp.name != act.name:
            scores.append(0.0)
            continue

        # Flag similarity (30% weight)
        flag_sim = jaccard(set(exp.flags), set(act.flags))

        # Argument similarity (70% weight)
        arg_sim = string_intersection(exp.args, act.args)

        scores.append(0.3 * flag_sim + 0.7 * arg_sim)

    # Apply order penalty
    order_penalty = calculate_order_penalty(expected, actual)

    return mean(scores) * (1 - order_penalty)
```

### 11.3 TextGrad Feedback Loop for CLI Optimization

TextGrad enables automatic optimization of CLI design through textual gradients.

#### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    TextGrad Optimization Loop               │
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │  CLI Design  │───▶│ Agent Tests  │───▶│   Evaluate   │  │
│  │  (Variable)  │    │  Execution   │    │   Results    │  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│         ▲                                       │          │
│         │            ┌──────────────┐           │          │
│         └────────────│   Textual    │◀──────────┘          │
│                      │   Gradient   │                      │
│                      └──────────────┘                      │
└─────────────────────────────────────────────────────────────┘
```

#### What Can Be Optimized

| Component | Variable Type | Optimization Goal |
|-----------|--------------|-------------------|
| `--help` text | String | Clearer descriptions for agent discovery |
| Error messages | String | More actionable guidance |
| Command names | String | More intuitive naming |
| Flag names | String | More discoverable flags |
| JSON output schema | Object | Easier parsing |
| Exit codes | Integer mapping | Better error recovery signals |

#### Implementation Example

```python
import textgrad as tg

# Set up TextGrad
tg.set_backward_engine("gpt-4o")  # Critic model

# Define variables to optimize
help_text = tg.Variable(
    """upstream list - List all configured upstream MCP servers

    Flags:
      --json    Output in JSON format
      --profile Select profile""",
    requires_grad=True,
    role_description="CLI help text for upstream list command"
)

error_message = tg.Variable(
    "Error: Server not found",
    requires_grad=True,
    role_description="Error message when server doesn't exist"
)

# Define loss function (evaluation criteria)
loss_fn = tg.TextLoss("""
Evaluate this CLI interface for AI agent usability:

1. DISCOVERABILITY (0-10): Can an agent find the right command from help?
2. CLARITY (0-10): Are descriptions unambiguous?
3. ACTIONABILITY (0-10): Do errors suggest next steps?
4. EFFICIENCY (0-10): Minimal commands needed for common tasks?
5. PARSEABILITY (0-10): Is output easy to parse programmatically?

Provide specific feedback on how to improve each aspect.
""")

# Optimization loop
optimizer = tg.TGD(parameters=[help_text, error_message])

for iteration in range(5):
    # Run agent tests with current CLI design
    test_results = run_agent_cli_tests(help_text.value, error_message.value)

    # Evaluate
    loss = loss_fn.forward(inputs=dict(
        help_text=help_text,
        error_message=error_message,
        test_results=test_results
    ))

    # Backpropagate textual gradients
    loss.backward()

    # Update variables
    optimizer.step()

    print(f"Iteration {iteration}: {loss.value}")
```

#### Example Optimization Trajectory

```
ITERATION 0 (Initial):
─────────────────────
Help text: "upstream list - List servers"
Error: "Error: Server not found"

Agent behavior: 3 failed attempts, 8 commands total
Score: 4.2/10

Textual Gradient:
- "Help text lacks flag descriptions, agent didn't know about --json"
- "Error message doesn't suggest how to find valid server names"

ITERATION 1:
─────────────────────
Help text: "upstream list - List all configured upstream MCP servers
           Use --json for machine-readable output"
Error: "Error: Server 'foo' not found. Run 'mcpproxy upstream list' to see available servers."

Agent behavior: 1 failed attempt, 4 commands total
Score: 7.1/10

Textual Gradient:
- "Good improvement on error message"
- "Help could mention --jq for filtering"

ITERATION 2:
─────────────────────
Help text: "upstream list - List all configured upstream MCP servers
           --json     Machine-readable JSON output
           --jq EXPR  Filter JSON output (e.g., '.[] | select(.health.level==\"unhealthy\")')"
Error: "Error: Server 'foo' not found.
        Available servers: github, jira, confluence
        Run 'mcpproxy upstream list --json' for full details."

Agent behavior: 0 failed attempts, 2 commands total
Score: 9.3/10
```

### 11.4 Agent Dialog Engine for CLI Testing

#### Architecture: Executor-Judge Pattern

```
┌─────────────────────────────────────────────────────────────┐
│                     Test Harness                            │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                   Task Provider                      │   │
│  │  "List all unhealthy servers and restart them"      │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                  │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │               Executor Agent (ReAct)                 │   │
│  │  ┌─────────────────────────────────────────────┐    │   │
│  │  │ Tools:                                       │    │   │
│  │  │  - bash_session(cmd) → Execute mcpproxy CLI │    │   │
│  │  │  - read_file(path)   → Read configs/logs    │    │   │
│  │  │  - parse_json(str)   → Parse JSON output    │    │   │
│  │  │  - submit(result)    → Report completion    │    │   │
│  │  └─────────────────────────────────────────────┘    │   │
│  │                                                      │   │
│  │  Thought → Action → Observation → Thought → ...     │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                  │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                  Trajectory Capture                  │   │
│  │  [cmd1, output1, cmd2, output2, ..., result]        │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                  │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                    Judge Agent                       │   │
│  │  Evaluates:                                          │   │
│  │  - Correctness: Did it complete the task?           │   │
│  │  - Efficiency: Optimal command sequence?            │   │
│  │  - Discovery: Found commands via help?              │   │
│  │  - Recovery: Handled errors gracefully?             │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                  │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                   Feedback Agent                     │   │
│  │  (Optional - for TextGrad loop)                     │   │
│  │  Suggests CLI improvements based on failures        │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

#### Sandboxed Execution Environment

```python
class CLITestSandbox:
    """
    Isolated environment for testing mcpproxy CLI.
    Based on Claude Code sandboxing patterns.
    """

    def __init__(self, config_path: str):
        self.temp_dir = tempfile.mkdtemp()
        self.config_path = config_path
        self.command_log = []
        self.allowed_commands = [
            "mcpproxy",
            "cat",
            "echo",
            "jq",
        ]

    def execute(self, command: str) -> ExecutionResult:
        """Execute command in sandbox with restrictions."""

        # Validate command is allowed
        cmd_name = command.split()[0]
        if cmd_name not in self.allowed_commands:
            return ExecutionResult(
                success=False,
                error=f"Command '{cmd_name}' not allowed in sandbox"
            )

        # Execute with timeout and resource limits
        result = subprocess.run(
            command,
            shell=True,
            capture_output=True,
            text=True,
            timeout=30,
            cwd=self.temp_dir,
            env={
                "MCPPROXY_CONFIG": self.config_path,
                "MCPPROXY_NO_PROMPT": "1",
                "PATH": os.environ["PATH"],
            }
        )

        # Log for trajectory analysis
        self.command_log.append({
            "command": command,
            "stdout": result.stdout,
            "stderr": result.stderr,
            "exit_code": result.returncode,
            "timestamp": datetime.now().isoformat()
        })

        return ExecutionResult(
            success=result.returncode == 0,
            stdout=result.stdout,
            stderr=result.stderr,
            exit_code=result.returncode
        )

    def get_trajectory(self) -> List[Dict]:
        """Return full command trajectory for analysis."""
        return self.command_log
```

#### ReAct Agent Implementation

```python
class MCPProxyCLITestAgent:
    """
    ReAct agent for testing mcpproxy CLI effectiveness.
    """

    SYSTEM_PROMPT = """You are testing the mcpproxy CLI tool.

Your goal is to complete the given task using ONLY the mcpproxy CLI.
You should:
1. Discover available commands using --help or --help-json
2. Execute commands to accomplish the task
3. Parse and validate output
4. Handle errors and retry if needed
5. Submit results when complete

Available tools:
- bash_session(cmd): Execute a shell command
- parse_json(text): Parse JSON string
- submit(success, details): Report task completion

Think step-by-step. Use --json output for easier parsing.
"""

    def __init__(self, sandbox: CLITestSandbox):
        self.sandbox = sandbox
        self.tools = {
            "bash_session": self.sandbox.execute,
            "parse_json": json.loads,
            "submit": self.submit_result,
        }
        self.result = None

    def run(self, task: str, max_iterations: int = 20) -> AgentResult:
        """Execute task using ReAct pattern."""

        messages = [
            {"role": "system", "content": self.SYSTEM_PROMPT},
            {"role": "user", "content": f"Task: {task}"}
        ]

        for i in range(max_iterations):
            # Get next action from LLM
            response = self.llm.chat(messages, tools=self.tools)

            if response.tool_calls:
                for tool_call in response.tool_calls:
                    # Execute tool
                    result = self.tools[tool_call.name](**tool_call.args)

                    # Add observation
                    messages.append({
                        "role": "tool",
                        "content": str(result),
                        "tool_call_id": tool_call.id
                    })

                    # Check if done
                    if tool_call.name == "submit":
                        return AgentResult(
                            success=self.result["success"],
                            trajectory=self.sandbox.get_trajectory(),
                            iterations=i + 1,
                            details=self.result
                        )
            else:
                messages.append({"role": "assistant", "content": response.content})

        return AgentResult(
            success=False,
            trajectory=self.sandbox.get_trajectory(),
            iterations=max_iterations,
            error="Max iterations reached"
        )
```

### 11.5 Effectiveness Metrics

#### Core Metrics

| Metric | Formula | Target | Description |
|--------|---------|--------|-------------|
| **Task Completion Rate** | `completed / total` | ≥95% | Basic success rate |
| **Command Efficiency** | `optimal_cmds / actual_cmds` | ≥0.8 | 1.0 = optimal path |
| **Discovery Efficiency** | `1 / help_calls` | ≥0.5 | Fewer help calls = better |
| **Token Efficiency** | `baseline_tokens / actual_tokens` | ≥0.8 | Token usage vs baseline |
| **Error Recovery Rate** | `recovered / errors` | ≥90% | Errors gracefully handled |
| **First-Try Success** | `first_try_success / total` | ≥70% | No retries needed |

#### Scoring Rubric

```python
class CLIEffectivenessScorer:
    """Score CLI effectiveness for agent use."""

    WEIGHTS = {
        "task_completion": 0.30,
        "command_efficiency": 0.25,
        "discovery_efficiency": 0.15,
        "token_efficiency": 0.15,
        "error_recovery": 0.10,
        "first_try_success": 0.05,
    }

    def score(self, result: AgentResult, baseline: Baseline) -> Score:
        scores = {}

        # Task completion (binary)
        scores["task_completion"] = 1.0 if result.success else 0.0

        # Command efficiency
        optimal = len(baseline.commands)
        actual = len(result.trajectory)
        scores["command_efficiency"] = min(1.0, optimal / actual) if actual > 0 else 0.0

        # Discovery efficiency (penalize excessive help calls)
        help_calls = sum(1 for cmd in result.trajectory if "--help" in cmd["command"])
        scores["discovery_efficiency"] = max(0, 1.0 - (help_calls - 1) * 0.2)

        # Token efficiency
        scores["token_efficiency"] = min(1.0, baseline.tokens / result.tokens) if result.tokens > 0 else 0.0

        # Error recovery
        errors = sum(1 for cmd in result.trajectory if cmd["exit_code"] != 0)
        recovered = result.success  # If succeeded despite errors, recovered
        scores["error_recovery"] = 1.0 if errors == 0 else (0.8 if recovered else 0.0)

        # First-try success
        scores["first_try_success"] = 1.0 if errors == 0 and result.success else 0.0

        # Weighted total
        total = sum(scores[k] * self.WEIGHTS[k] for k in scores)

        return Score(
            total=total,
            breakdown=scores,
            grade=self._grade(total)
        )

    def _grade(self, score: float) -> str:
        if score >= 0.9: return "A"
        if score >= 0.8: return "B"
        if score >= 0.7: return "C"
        if score >= 0.6: return "D"
        return "F"
```

#### Trajectory Quality Metrics (from mcp-eval)

```python
@dataclass
class TrajectoryMetrics:
    """Detailed trajectory analysis."""

    # Similarity to optimal
    trajectory_similarity: float      # 0.0-1.0

    # Command analysis
    total_commands: int
    optimal_commands: int
    redundant_commands: int           # Commands that didn't contribute
    out_of_order_commands: int        # Correct but wrong sequence

    # Discovery analysis
    help_calls: int
    help_efficiency: float            # Found what needed quickly?

    # Error analysis
    errors_encountered: int
    errors_recovered: int
    error_messages_helpful: float     # 0.0-1.0, did errors guide recovery?

    # Token analysis
    input_tokens: int
    output_tokens: int
    tokens_per_command: float
```

### 11.6 Judge Agent Implementation

```python
JUDGE_PROMPT = """You are evaluating an AI agent's use of the mcpproxy CLI.

## Task Given to Agent
{task}

## Expected Optimal Trajectory
{expected_trajectory}

## Agent's Actual Trajectory
{actual_trajectory}

## Evaluation Criteria

Score each criterion 0-10:

### 1. CORRECTNESS (0-10)
- Did the agent complete the task successfully?
- Were the final results accurate?

### 2. EFFICIENCY (0-10)
- Did the agent take the optimal path?
- Were there redundant or unnecessary commands?
- How does command count compare to optimal?

### 3. DISCOVERY (0-10)
- Did the agent effectively discover available commands?
- Did it use --help appropriately (not too much, not too little)?
- Did it understand command structure from help output?

### 4. ERROR HANDLING (0-10)
- Did the agent handle errors gracefully?
- Did error messages help the agent recover?
- Was the recovery path efficient?

### 5. CLI DESIGN FEEDBACK (0-10)
- Was the CLI interface intuitive for the agent?
- What made the agent struggle?
- What would have helped?

## Output Format

```json
{
  "scores": {
    "correctness": X,
    "efficiency": X,
    "discovery": X,
    "error_handling": X,
    "cli_design": X
  },
  "total_score": X.X,
  "pass": true/false,
  "analysis": {
    "what_went_well": ["..."],
    "what_went_wrong": ["..."],
    "agent_struggles": ["..."],
    "cli_improvement_suggestions": ["..."]
  }
}
```
"""

class JudgeAgent:
    """LLM-based judge for CLI effectiveness evaluation."""

    def evaluate(
        self,
        task: str,
        expected: List[Command],
        actual: List[Command],
        result: AgentResult
    ) -> JudgeResult:

        prompt = JUDGE_PROMPT.format(
            task=task,
            expected_trajectory=format_trajectory(expected),
            actual_trajectory=format_trajectory(actual)
        )

        response = self.llm.chat([
            {"role": "system", "content": "You are a CLI usability expert."},
            {"role": "user", "content": prompt}
        ])

        return JudgeResult.parse(response.content)
```

### 11.7 Deterministic Testing Strategies

#### Challenge: Agent Non-Determinism

```
Same input → Different outputs across runs
- LLM temperature variance
- Tool execution timing
- External state changes
```

#### Solutions

| Strategy | Implementation | Use Case |
|----------|---------------|----------|
| **Temperature=0** | `temperature=0.0` in all LLM calls | Maximize reproducibility |
| **Seed fixing** | `seed=42` where supported | Consistent random choices |
| **State reset** | Fresh sandbox per test | No contamination |
| **Baseline recording** | Store "golden" trajectories | Regression testing |
| **Probabilistic thresholds** | Pass if score ≥ 0.8 in 90% of runs | Accept some variance |
| **Soft failures** | Score 0.5-0.8 = review, <0.5 = fail | Triage flaky tests |

#### Test Determinism Levels

```python
class DeterminismLevel(Enum):
    STRICT = "strict"         # Must match exactly
    SIMILARITY = "similarity"  # Must score ≥ threshold
    STATISTICAL = "statistical"  # Must pass X% of N runs
    BEHAVIORAL = "behavioral"  # Must achieve same end state

@dataclass
class TestConfig:
    determinism: DeterminismLevel
    threshold: float = 0.8       # For SIMILARITY
    runs: int = 5                # For STATISTICAL
    pass_rate: float = 0.9       # For STATISTICAL
```

### 11.8 Continuous Improvement Pipeline

```
┌─────────────────────────────────────────────────────────────┐
│              CLI Effectiveness Improvement Loop              │
│                                                             │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐   │
│  │   Define    │────▶│    Run      │────▶│  Evaluate   │   │
│  │  Scenarios  │     │   Tests     │     │   Results   │   │
│  └─────────────┘     └─────────────┘     └─────────────┘   │
│         ▲                                       │          │
│         │                                       ▼          │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐   │
│  │  Implement  │◀────│  Generate   │◀────│   Judge     │   │
│  │  Changes    │     │  Feedback   │     │   Agent     │   │
│  └─────────────┘     └─────────────┘     └─────────────┘   │
│                             │                               │
│                             ▼                               │
│                      ┌─────────────┐                       │
│                      │  TextGrad   │                       │
│                      │  Optimize   │                       │
│                      └─────────────┘                       │
└─────────────────────────────────────────────────────────────┘

Frequency:
- Scenarios: Updated when adding new features
- Tests: Run on every PR
- Evaluation: Automated scoring
- Judge: Weekly deep analysis
- TextGrad: Monthly optimization pass
- Implementation: Based on priority
```

### 11.9 Integration with Existing mcp-eval

Since mcp-eval already exists at `/Users/user/repos/mcp-eval/`, extend it for CLI testing:

```yaml
# mcp-eval/scenarios/cli/upstream_management.yaml
enabled: true
name: "CLI: Upstream Server Management"
description: "Test agent's ability to manage upstream servers via CLI"

# Use CLI executor instead of MCP executor
executor: "cli"
cli_binary: "mcpproxy"

user_intent: "Add a new GitHub MCP server, verify it's quarantined, then approve it"

expected_trajectory:
  - command: "mcpproxy upstream add --name github --url https://api.github.com/mcp --protocol http"
    expect_exit_code: 0
  - command: "mcpproxy upstream list --json"
    expect_output_contains: ["github", "quarantined"]
  - command: "mcpproxy call tool --tool-name upstream_servers --json_args '{\"operation\":\"unquarantine\",\"name\":\"github\"}'"
    expect_exit_code: 0
  - command: "mcpproxy upstream list --json"
    expect_output_contains: ["github", "enabled"]

success_criteria:
  - "github server added"
  - "quarantine state verified"
  - "server approved"

metrics:
  max_commands: 6
  max_tokens: 3000
  similarity_threshold: 0.8
```

#### CLI Test Runner Extension

```python
# mcp-eval/src/mcp_eval/cli_executor.py

class CLIScenarioRunner(ScenarioRunner):
    """Extended runner for CLI-based scenarios."""

    def execute_scenario(self, scenario: Scenario) -> ExecutionResult:
        # Create sandboxed environment
        sandbox = CLITestSandbox(config_path=scenario.config_file)

        # Create ReAct agent
        agent = MCPProxyCLITestAgent(sandbox)

        # Run with deterministic settings
        with deterministic_mode(temperature=0.0):
            result = agent.run(
                task=scenario.user_intent,
                max_iterations=scenario.metrics.get("max_commands", 20) * 2
            )

        # Calculate metrics
        metrics = self.calculate_metrics(
            result=result,
            expected=scenario.expected_trajectory,
            thresholds=scenario.metrics
        )

        return ExecutionResult(
            scenario=scenario.name,
            success=result.success,
            trajectory=result.trajectory,
            metrics=metrics,
            similarity_score=metrics.trajectory_similarity
        )
```

### 11.10 Test Suite Organization

```
mcp-eval/
├── scenarios/
│   ├── mcp/                    # Existing MCP tool tests
│   │   ├── tool_discovery/
│   │   └── tool_execution/
│   └── cli/                    # NEW: CLI effectiveness tests
│       ├── discovery/          # Command discovery tests
│       │   ├── find_list_command.yaml
│       │   ├── find_help_flags.yaml
│       │   └── discover_subcommands.yaml
│       ├── basic_operations/   # Simple command tests
│       │   ├── list_servers.yaml
│       │   ├── enable_disable.yaml
│       │   └── view_logs.yaml
│       ├── complex_workflows/  # Multi-step scenarios
│       │   ├── add_and_approve_server.yaml
│       │   ├── debug_unhealthy_server.yaml
│       │   └── profile_switching.yaml
│       ├── error_recovery/     # Error handling tests
│       │   ├── recover_from_not_found.yaml
│       │   ├── handle_auth_error.yaml
│       │   └── retry_on_timeout.yaml
│       └── efficiency/         # Optimization tests
│           ├── minimal_commands.yaml
│           ├── token_efficiency.yaml
│           └── help_usage.yaml
├── baselines/
│   ├── mcp/
│   └── cli/                    # CLI test baselines
└── reports/
    ├── mcp/
    └── cli/                    # CLI test reports
```

### 11.11 Running CLI Effectiveness Tests

```bash
# Run all CLI tests
mcp-eval test --tag cli

# Run specific category
mcp-eval test --tag cli --tag discovery

# Record new baseline
mcp-eval record --scenario scenarios/cli/list_servers.yaml

# Compare against baseline
mcp-eval compare --scenario scenarios/cli/list_servers.yaml

# Generate TextGrad optimization report
mcp-eval optimize --scenarios scenarios/cli/ --iterations 5

# Full report with judge analysis
mcp-eval test --tag cli --judge --output reports/cli/$(date +%Y%m%d).html
```

### 11.12 Key Takeaways

1. **Extend mcp-eval** rather than building new framework - it already has trajectory comparison, similarity scoring, and judge integration

2. **Use ReAct pattern** for agent execution - proven effective for tool-using agents

3. **Implement TextGrad loop** for continuous CLI improvement - automatic optimization of help text, error messages, command names

4. **Define clear metrics**: Task completion, command efficiency, discovery efficiency, token usage, error recovery

5. **Embrace probabilistic testing** - agents are non-deterministic, use thresholds and statistical pass rates

6. **Judge agent provides actionable feedback** - not just scores, but specific CLI improvement suggestions

7. **Sandbox execution** - isolated environment prevents test contamination and ensures reproducibility
