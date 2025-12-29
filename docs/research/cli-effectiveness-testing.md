# CLI Effectiveness Testing for AI Agents

**Status**: Research
**Created**: 2025-12-19
**Related**: RFC-001 CLI Architecture, mcp-eval framework

---

## Overview

This document describes how to test CLI effectiveness for AI agent usage. Traditional CLI testing verifies correctness ("does command X produce output Y?"), but agent CLI testing must also verify **effectiveness** ("can an agent discover and use commands efficiently?").

---

## The Testing Challenge

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

---

## Leveraging mcp-eval Patterns

The existing `/Users/user/repos/mcp-eval/` project provides a sophisticated evaluation framework that can be adapted for CLI testing.

### Key mcp-eval Concepts

| Concept | Description | CLI Testing Application |
|---------|-------------|------------------------|
| **Scenario** | YAML-defined test case with user intent | CLI task specification |
| **Expected Trajectory** | Sequence of expected tool calls | Expected command sequence |
| **Similarity Scoring** | 0.0-1.0 score comparing actual vs expected | Command sequence similarity |
| **Baseline Recording** | Capture "golden" execution for regression | Record optimal CLI usage |
| **Judge Agent** | LLM-based analysis of divergence | Analyze why agent used wrong commands |

### Adapted Scenario Format for CLI Testing

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

### Trajectory Similarity Scoring

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

---

## TextGrad Feedback Loop for CLI Optimization

TextGrad enables automatic optimization of CLI design through textual gradients.

### Architecture

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

### What Can Be Optimized

| Component | Variable Type | Optimization Goal |
|-----------|--------------|-------------------|
| `--help` text | String | Clearer descriptions for agent discovery |
| Error messages | String | More actionable guidance |
| Command names | String | More intuitive naming |
| Flag names | String | More discoverable flags |
| JSON output schema | Object | Easier parsing |
| Exit codes | Integer mapping | Better error recovery signals |

### Implementation Example

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

### Example Optimization Trajectory

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

---

## Agent Dialog Engine for CLI Testing

### Architecture: Executor-Judge Pattern

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

### Sandboxed Execution Environment

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

### ReAct Agent Implementation

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

---

## Effectiveness Metrics

### Core Metrics

| Metric | Formula | Target | Description |
|--------|---------|--------|-------------|
| **Task Completion Rate** | `completed / total` | ≥95% | Basic success rate |
| **Command Efficiency** | `optimal_cmds / actual_cmds` | ≥0.8 | 1.0 = optimal path |
| **Discovery Efficiency** | `1 / help_calls` | ≥0.5 | Fewer help calls = better |
| **Token Efficiency** | `baseline_tokens / actual_tokens` | ≥0.8 | Token usage vs baseline |
| **Error Recovery Rate** | `recovered / errors` | ≥90% | Errors gracefully handled |
| **First-Try Success** | `first_try_success / total` | ≥70% | No retries needed |

### Scoring Rubric

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

### Trajectory Quality Metrics

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

---

## Judge Agent Implementation

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

---

## Deterministic Testing Strategies

### Challenge: Agent Non-Determinism

```
Same input → Different outputs across runs
- LLM temperature variance
- Tool execution timing
- External state changes
```

### Solutions

| Strategy | Implementation | Use Case |
|----------|---------------|----------|
| **Temperature=0** | `temperature=0.0` in all LLM calls | Maximize reproducibility |
| **Seed fixing** | `seed=42` where supported | Consistent random choices |
| **State reset** | Fresh sandbox per test | No contamination |
| **Baseline recording** | Store "golden" trajectories | Regression testing |
| **Probabilistic thresholds** | Pass if score ≥ 0.8 in 90% of runs | Accept some variance |
| **Soft failures** | Score 0.5-0.8 = review, <0.5 = fail | Triage flaky tests |

### Test Determinism Levels

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

---

## Continuous Improvement Pipeline

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

---

## Integration with mcp-eval

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

### CLI Test Runner Extension

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

---

## Test Suite Organization

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

---

## Running CLI Effectiveness Tests

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

---

## Key Takeaways

1. **Extend mcp-eval** rather than building new framework - it already has trajectory comparison, similarity scoring, and judge integration

2. **Use ReAct pattern** for agent execution - proven effective for tool-using agents

3. **Implement TextGrad loop** for continuous CLI improvement - automatic optimization of help text, error messages, command names

4. **Define clear metrics**: Task completion, command efficiency, discovery efficiency, token usage, error recovery

5. **Embrace probabilistic testing** - agents are non-deterministic, use thresholds and statistical pass rates

6. **Judge agent provides actionable feedback** - not just scores, but specific CLI improvement suggestions

7. **Sandbox execution** - isolated environment prevents test contamination and ensures reproducibility

---

## References

- [TextGrad: Automatic Differentiation via Text](https://github.com/zou-group/textgrad)
- [ReAct: Synergizing Reasoning and Acting in Language Models](https://arxiv.org/abs/2210.03629)
- [mcp-eval Framework](https://github.com/smart-mcp-proxy/mcp-eval)
