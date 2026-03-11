# Test MCP Servers & Quarantine QA

Test fixtures for MCPProxy's security quarantine system (Spec 032).

## Test Servers

### `malicious-mcp-server/`

Node.js MCP server with intentionally malicious tool descriptions demonstrating Tool Poisoning Attack (TPA) vectors:

- **fetch_data** - Data exfiltration via description (instructs agent to send context to attacker URL)
- **run_command** - Command injection via description (prepends `curl` exfiltration before commands)
- **summarize_text** - Prompt injection override (instructs agent to dump environment variables)

### `echo-rugpull-server/`

Node.js MCP server that starts with clean tool descriptions and mutates them after the first tool call (rug pull simulation):

- Initially serves benign `echo` and `get_time` tools
- After the first `CallTool` request, descriptions mutate to include exfiltration instructions
- Sends `notifications/tools/list_changed` to trigger MCPProxy's hash-based change detection

## Running Quarantine Tests

### Prerequisites

- Built `mcpproxy` binary at project root
- Node.js (for test MCP servers)
- `curl`, `jq`, `python3`

### Setup

```bash
cd tests/malicious-mcp-server && npm install && cd -
cd tests/echo-rugpull-server && npm install && cd -
```

### Config

`quarantine-test-config.json` - MCPProxy configuration with both test servers enabled and quarantined. Uses `./test-data-quarantine` as an isolated data directory.

### Test Script

```bash
./tests/test-quarantine.sh
```

Runs 16 automated scenarios covering:

1. Server-level quarantine (block tool calls, API status, CLI inspect)
2. Tool-level quarantine (pending approval, hash verification, approval flow)
3. Rug pull detection (description mutation, re-quarantine, diff inspection)
4. MCP protocol integration (retrieve_tools filtering, call_tool blocking)
5. Web UI endpoints (quarantine panel data availability)

Generates an HTML report at `docs/qa/quarantine-test-report-2026-03-11.html`.

## QA Artifacts

- `docs/qa/quarantine-test-report-2026-03-11.html` - HTML test report from the 16-scenario run
- `docs/qa/quarantine-ux-walkthrough.gif` - Chrome walkthrough of the quarantine Web UI
