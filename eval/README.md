# MCP Proxy Evaluation Framework

A comprehensive evaluation environment for testing MCP tools quality in mcpproxy using Google ADK and deterministic fake servers.

## 🎯 Overview

This framework provides:
- **10 Fake MCP Servers** with deterministic responses (no external dependencies)
- **Fake MCP Registry** that mimics real registry responses
- **ADK-based Evaluation** for systematic testing
- **Two Key Scenarios** for comprehensive evaluation
- **Parametrizable Test Data** for extensive coverage

## 🏗️ Architecture

```
eval/
├── src/mcpproxy_eval/          # Main Python package
│   ├── servers/                # 10 fake MCP servers using FastMCP
│   ├── registry/               # Fake MCP registry server
│   └── evaluation/             # ADK evaluation tools
├── configs/                    # Configuration files
├── datasets/                   # Test data and scenarios
├── results/                    # Evaluation results
└── Makefile                    # Orchestration commands
```

## 🚀 Quick Start

### 1. Setup Environment

```bash
cd eval
make setup
```

### 2. Start All Services

```bash
make start-all
```

This starts:
- Fake MCP registry on port 8001
- Verifies 10 fake MCP servers are available via stdio (uvx commands)

### 3. Configure and Start mcpproxy

```bash
# In the main project directory
go build
pkill mcpproxy || true
./mcpproxy --config=eval/configs/mcpproxy_eval.json --log-level=debug --tray=false
```

### 4. Start ADK Web UI

```bash
cd eval
make start-adk-web
```

This opens the ADK web interface for interactive evaluation.

## 🎲 Fake MCP Servers

All servers provide deterministic responses for consistent testing and run via stdio:

| Server | Command | Description | Key Tools |
|--------|---------|-------------|-----------|
| **Dice Roller** | `uvx mcp-dice` | Gaming and probability | `roll_dice`, `roll_advantage` |
| **Weather** | `uvx mcp-weather` | Weather information | `get_current_weather`, `get_forecast` |
| **Restaurant** | `uvx mcp-restaurant` | Menu and ordering | `search_restaurants`, `get_menu` |
| **Calculator** | `uvx mcp-calculator` | Mathematical operations | `add`, `multiply`, `factorial` |
| **Translator** | `uvx mcp-translator` | Multi-language translation | `translate_text` |
| **Morse Code** | `uvx mcp-morse` | Morse code encoding/decoding | `text_to_morse`, `morse_to_text` |
| **Time Service** | `uvx mcp-time` | Time and timezone | `get_current_time`, `format_time` |
| **Joke Generator** | `uvx mcp-jokes` | Humor and entertainment | `get_random_joke`, `get_joke_by_category` |
| **Color Palette** | `uvx mcp-color` | Color manipulation | `get_color_info`, `hex_to_rgb` |
| **Random Generator** | `uvx mcp-random` | Random data generation | `random_integer`, `random_choice` |

### Running Individual Servers

```bash
# Run a single server in stdio mode
uvx mcp-dice

# Test server connectivity
uvx mcp-weather
```

**Note**: All servers run in stdio mode by default, which is the standard MCP communication method.

## 📡 Fake Registry

The fake registry server responds to the standard MCP registry API:

- `GET /v0/servers` - List available servers
- `GET /v0/servers/{id}` - Get server details
- `GET /health` - Health check

**Example Response:**
```json
{
  "servers": [
    {
      "id": "01129bff-3d65-4e3d-8e82-6f2f269f818c",
      "name": "dice-roller",
      "url": "stdio://uvx mcp-dice",
      "description": "A dice rolling MCP server for gaming and probability calculations"
    }
  ],
  "metadata": {
    "next_cursor": null,
    "count": 10
  }
}
```

## 🎯 Evaluation Scenarios

### Scenario 1: Adding Server with Quarantine

Tests the agent's ability to:
1. **Add upstream server** via `upstream_servers` tool
2. **Handle failures** with log debugging using `tail_log`
3. **Generate security reports** for quarantined servers using `quarantine_security`

**Example Flow:**
```
User: "Add the server WeatherInfo at stdio://uvx mcp-weather"
Agent: Calls upstream_servers(operation="add", name="WeatherInfo", url="stdio://uvx mcp-weather")
→ Success: Server added but quarantined
Agent: Calls quarantine_security(operation="inspect_quarantined", name="WeatherInfo")
→ Generates security report
```

### Scenario 2: Search and Add Server

Tests the agent's ability to:
1. **List registries** using `list_registries`
2. **Search for servers** using `search_servers` with capability queries
3. **Add found server** using results from search

**Example Flow:**
```
User: "Convert this text to Morse code"
Agent: Calls list_registries() → Gets available registries
Agent: Calls search_servers(search="morse", registry="eval-registry")
→ Finds morse-translator server
Agent: Calls upstream_servers(operation="add", name="morse-translator", url="stdio://uvx mcp-morse")
→ Server added successfully
```

## 🧪 Running Evaluations

### 🌐 Interactive Evaluation with ADK Web UI

**1. Start ADK Web UI**
```bash
make start-adk-web
```

**2. Create Evaluation Cases**
- Open the web interface at http://localhost:8000
- Select the `mcp_proxy_evaluation_agent`
- Interact with the agent to test scenarios
- Navigate to the **Eval** tab
- Click **"Add current session"** to save as evaluation case

**3. Run Evaluations**
- Select evaluation cases from your evalset
- Click **Run Evaluation**
- Configure metrics using sliders (tool trajectory, response match)
- Analyze results and failures

**4. Debug with Trace View**
- Use the **Trace** tab to inspect agent execution
- Hover over trace rows to see corresponding messages
- Click rows for detailed inspection (Event, Request, Response, Graph)

### 🤖 CLI Evaluation (for automation)
```bash
make run-eval-cli
```

### 📊 Architecture Flow
```
MCP Servers (stdio) → MCP Registry → mcpproxy → StreamableHTTPConnectionParams → ADK Agent
```

## 📊 Evaluation Results

### ADK Web UI Results

The web interface provides comprehensive evaluation results:

- **Pass/Fail Status**: Clear indicators for each evaluation case
- **Metric Scores**: Tool trajectory accuracy and response match scores
- **Side-by-side Comparison**: Actual vs. expected output for failures
- **Interactive Analysis**: Click on results to see detailed breakdowns
- **Evaluation History**: Track metrics and results over time

### CLI Results

CLI evaluations output detailed JSON results with:
- Tool trajectory comparisons
- Response similarity scores (ROUGE metrics)
- Individual case pass/fail status
- Overall evaluation statistics

## 🔧 Configuration

### Environment Variables

Create `.env` file in the eval directory:
```bash
GOOGLE_AI_API_KEY=your_api_key_here
MCPPROXY_URL=http://localhost:8080
```

### mcpproxy Configuration

The evaluation uses a special config file at `eval/configs/mcpproxy_eval.json` with:
- Registry pointing to localhost:8001
- Debug logging enabled
- Quarantine security enabled
- All evaluation features enabled

## 🛠️ Development

### Setup Development Environment
```bash
make dev-setup
```

### Run Tests
```bash
make dev-test
```

### Check Service Status
```bash
make status
```

### View Logs
```bash
make show-logs
make debug-logs
```

### Stop All Services
```bash
make stop-all
```

## 📝 Using ADK Web UI for Evaluation

### Step-by-Step Workflow

**1. Start the Environment**
```bash
cd eval
make setup
make start-all
# In another terminal: make start-mcpproxy  
make start-adk-web
```

**2. Create Evaluation Cases**
- Select the `mcp_proxy_evaluation_agent` in the web UI
- Test the two key scenarios:
  - **Scenario 1**: "Add the server WeatherInfo at stdio://uvx mcp-weather"
  - **Scenario 2**: "Convert this text to Morse code"
- Navigate to **Eval** tab and save sessions as evaluation cases

**3. Configure and Run Evaluations**
- Select saved evaluation cases
- Configure evaluation metrics:
  - **Tool trajectory avg score**: 1.0 (100% tool match)
  - **Response match score**: 0.8 (80% response similarity)
- Run evaluations and analyze results

**4. Debug and Iterate**
- Use **Trace** tab for detailed execution analysis
- Inspect failures with side-by-side comparisons
- Refine agent instructions and re-evaluate

## 🧩 Adding New Test Scenarios

### 1. Create New Server Variations

Edit `eval/datasets/scenarios/` files to add new test cases:

```json
{
  "name": "new_test_case",
  "server_name": "test-server",
  "server_url": "stdio://uvx mcp-test",
  "expected_outcome": "success_with_quarantine"
}
```

### 2. Create New Fake Servers

Add new servers in `eval/src/mcpproxy_eval/servers/`:

```python
from fastmcp import FastMCP

mcp = FastMCP("My Test Server 🧪")

@mcp.tool
def my_tool(param: str) -> dict:
    """My deterministic tool"""
    return {"result": f"deterministic_response_for_{param}"}

def main():
    mcp.run()
```

### 3. Update Configuration

Add the new server to:
- `pyproject.toml` scripts section
- `Makefile` start-servers target
- Fake registry data in `registry/server.py`

## 🔍 Debugging

### Common Issues

1. **Services not starting**
   ```bash
   make check-deps  # Verify dependencies
   make status      # Check service status
   ```

2. **mcpproxy connection issues**
   ```bash
   make test-connection
   ```

3. **Evaluation failures**
   ```bash
   make show-logs      # View recent logs
   make debug-logs     # Monitor filtered logs
   ```

### Log Monitoring

Real-time filtered logging:
```bash
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(error|fail|success|quarantine|registry)"
```

## 🚦 CI/CD Integration

The evaluation can be integrated into CI/CD pipelines:

```yaml
# Example GitHub Actions
- name: Run MCP Evaluation
  run: |
    cd eval
    make setup
    make start-all
    # Start mcpproxy in background
    make run-eval
```

## 📈 Extending for Textgrad

The framework is designed to support future textgrad optimization:

1. Current ADK evaluation establishes baseline metrics
2. Textgrad can optimize based on evaluation results
3. Same deterministic servers ensure consistent comparison

## 🤝 Contributing

1. Add new fake servers with deterministic responses
2. Extend evaluation scenarios for comprehensive coverage
3. Improve ADK integration and reporting
4. Add support for more complex multi-turn conversations

## 📜 License

This evaluation framework follows the same license as the main mcpproxy project. 