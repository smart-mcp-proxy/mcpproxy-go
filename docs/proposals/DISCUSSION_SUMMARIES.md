# GitHub Discussion Summaries

These summaries are ready to post in GitHub Discussions for community feedback.

---

## Discussion 1: RFC-001 CLI Architecture

**Title**: 📋 RFC-001: Solid CLI Architecture for Humans and AI Agents

**Body**:

We're designing a comprehensive CLI architecture for mcpproxy v2.0 that works great for both human developers and AI agents. This follows the proven "gh CLI model" with hierarchical discovery.

### Key Features

🎯 **Hierarchical Discovery via `--help-json`**
```bash
mcpproxy --help-json              # Top-level commands (~150 tokens)
mcpproxy upstream --help-json     # Subcommands (~100 tokens)
mcpproxy upstream add --help-json # Detailed flags (~150 tokens)
```
This is 10x more token-efficient than MCP tool discovery.

🔧 **Universal Output Formatting**
```bash
mcpproxy upstream list --json                    # JSON output
mcpproxy upstream list --json name,health.level  # Field selection
mcpproxy upstream list --jq '.[] | select(...)'  # Built-in jq filtering
mcpproxy upstream list --names-only              # Minimal output
```

🚀 **Agent-Friendly Error Messages**
```json
{
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Not authenticated for server 'github'",
    "recovery_command": "mcpproxy auth login --server github"
  }
}
```

📁 **Project Support** (see RFC-002)
```bash
mcpproxy upstream list --project /path/to/project
mcpproxy upstream add --project . --name ast-grep ...
```

### New Commands

| Command | Description |
|---------|-------------|
| `mcpproxy project init` | Initialize `.mcpproxy/` in current directory |
| `mcpproxy profile switch` | Switch between server groups |
| `mcpproxy history list` | View tool call history with risk scores |
| `mcpproxy integrate import claude` | Import servers from Claude config |
| `mcpproxy completion bash` | Shell completion scripts |

### Questions for Community

1. **`mcpproxy api` command**: Should we add direct REST API access like `gh api`? Or is CLI coverage sufficient?
2. **JQ library**: Should we bundle jq-like filtering (`gojq`) or keep it external?
3. **Exit codes**: Are semantic exit codes (3=auth, 4=server not found) useful for your scripts?

📄 **Full proposal**: `docs/proposals/001-cli-architecture.md`

---

## Discussion 2: RFC-002 Projects Feature

**Title**: 📁 RFC-002: Project-Local Configuration Support

**Body**:

This proposal addresses [Issue #55](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/55) - the need for project-specific MCP server configurations.

### The Problem

When running mcpproxy as a centralized daemon:
- ❌ MCP servers like ast-grep-mcp execute in wrong directory context
- ❌ Private corporate servers are available to all projects
- ❌ No easy way to restrict servers per project

### The Solution

Projects are directories containing `.mcpproxy/`:

```
my-project/
├── .mcpproxy/
│   ├── config.json          # Project-specific servers
│   └── scripts/              # Project-local scripts
│       └── deploy.js
├── src/
└── package.json
```

### Configuration Merge

```json
// .mcpproxy/config.json
{
  "inherit_global": ["github", "jira"],  // Only these from global
  "mcpServers": [
    {
      "name": "ast-grep-project",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "${PROJECT_ROOT}"   // Runs in project directory
    }
  ]
}
```

Result: `[github, jira, ast-grep-project]` - other global servers excluded.

### CLI Support

```bash
mcpproxy project init                    # Create .mcpproxy/
mcpproxy project init --from-global      # Copy servers from global
mcpproxy upstream list --project .       # List project servers
mcpproxy upstream add --project . ...    # Add to project config
```

### API & Web UI

- All endpoints accept `?project=` parameter
- Web UI has project selector dropdown
- SSE events include project context

### Questions for Community

1. **Naming**: Should projects have explicit names, or just use directory path?
2. **Inheritance model**: Is `inherit_global: ["github"]` the right approach? Or exclusion patterns?
3. **Nested projects**: What happens with nested `.mcpproxy/` directories?
4. **Profile vs Project**: Should these be merged concepts?

📄 **Full proposal**: `docs/proposals/002-projects-feature.md`

---

## Discussion 3: RFC-003 Tool Calls History & Security

**Title**: 🔐 RFC-003: Tool Call History with Security Visibility

**Body**:

This proposal improves visibility into what AI agents are doing, addressing the "lethal trifecta" security concerns (Simon Willison).

### The Lethal Trifecta

AI agents become vulnerable when they have:
1. 🔑 **Access to private data**
2. 📥 **Exposure to untrusted content**
3. 📤 **External communication capability**

As an MCP middleware, mcpproxy sees ALL tool calls and can detect suspicious patterns.

### Proposed Features

📊 **Risk Scoring**
```json
{
  "id": "call-123",
  "risk_score": 78,
  "risk_factors": [
    {"type": "pii_in_arguments", "severity": "critical"},
    {"type": "external_data_write", "severity": "high"}
  ],
  "requires_approval": true
}
```

🔐 **PII Detection & Masking**
- Detect: emails, SSNs, credit cards, API keys, phone numbers
- Mask in UI: `j***@e******.com`
- Auto-redact after configurable period

⚠️ **Real-time Alerts**
```json
{
  "event": "tool_call.exfiltration_detected",
  "data": {
    "pattern": "pii_to_external_endpoint",
    "evidence": "Email addresses sent to webhook.site"
  }
}
```

📋 **Audit Trail**
- Who viewed what tool calls
- Approval workflow for high-risk calls
- Compliance exports

### CLI Commands

```bash
mcpproxy history list --risk high        # High-risk calls
mcpproxy history list --has-pii          # Calls with PII detected
mcpproxy history show <id>               # Call details
mcpproxy history export --format csv     # Compliance export
```

### Web UI

- Dashboard widget with risk summary
- Tool call history with filtering
- Approval queue for flagged calls
- Audit trail viewer

### Configuration

```json
{
  "tool_call_history": {
    "pii_detection": {"enabled": true},
    "risk_assessment": {"auto_flag_threshold": 70},
    "alerts": {"exfiltration_detection": true}
  }
}
```

### Questions for Community

1. **Blocking vs Alerting**: Should high-risk calls be blocked, or just flagged?
2. **Real-time blocking**: Would blocking suspicious calls break agent UX too much?
3. **PII retention**: How long should PII be kept before auto-redaction?
4. **Third-party integration**: Need SIEM/Splunk/Datadog integration?

📄 **Full proposal**: `docs/proposals/003-tool-calls-history.md`

---

## Discussion 4: CLI Effectiveness Testing

**Title**: 🧪 Research: Testing CLI Effectiveness for AI Agents

**Body**:

We're exploring how to test CLI effectiveness for AI agent usage. Traditional testing verifies correctness; agent testing must verify **effectiveness**.

### The Challenge

```
Traditional: Command → Output → Assert(match)
Agent:       Task → Agent(discovers) → Trajectory → Evaluate
             - Did agent find right commands?
             - How many attempts/tokens?
             - Did it recover from errors?
```

### Proposed Approach

**1. Extend mcp-eval framework**
```yaml
# scenarios/cli/list_unhealthy_servers.yaml
user_intent: "Show me which MCP servers have problems"
expected_trajectory:
  - command: "mcpproxy upstream --help-json"
  - command: "mcpproxy upstream list --json"
  - command: "mcpproxy upstream list --jq '...'"
metrics:
  max_commands: 5
  max_tokens: 2000
```

**2. TextGrad optimization loop**
- LLM evaluates CLI usability
- Suggests improvements to help text, error messages
- Automated optimization iterations

**3. Judge agent evaluation**
- Correctness: Did it complete the task?
- Efficiency: Optimal command sequence?
- Discovery: Found commands via help?
- Recovery: Handled errors gracefully?

### Effectiveness Metrics

| Metric | Target |
|--------|--------|
| Task Completion Rate | ≥95% |
| Command Efficiency | ≥0.8 (optimal/actual) |
| Token Efficiency | ≥0.8 (baseline/actual) |
| Error Recovery Rate | ≥90% |

### Questions for Community

1. **Worth the complexity?** Is automated CLI testing valuable, or overkill?
2. **Which scenarios?** What CLI workflows should we test?
3. **TextGrad**: Should we invest in automatic help text optimization?

📄 **Full research**: `docs/research/cli-effectiveness-testing.md`

---

## How to Use These Summaries

1. Create GitHub Discussion for each topic
2. Copy the body text above
3. Tag with: `rfc`, `proposal`, `community-input`
4. Link to full proposal documents in repo

Each discussion is designed to:
- Explain the problem clearly
- Present the proposed solution
- Ask specific questions for community input
- Reference full documentation
