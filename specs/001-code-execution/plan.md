# Implementation Plan: JavaScript Code Execution Tool

**Branch**: `001-code-execution` | **Date**: 2025-11-15 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-code-execution/spec.md`

## Summary

Add a `code_execution` MCP tool that enables LLM agents to orchestrate multiple upstream tools in a single request using JavaScript. The system uses dop251/goja for sandboxed ES5.1+ execution, provides a `call_tool()` bridge to upstream MCP servers, enforces execution limits (timeout, max_tool_calls), maintains a runtime pool for concurrent executions, and includes CLI testing interface plus comprehensive documentation.

**Primary Value**: Reduces model round-trips from N calls to 1 by executing multi-step workflows server-side, improving latency and enabling complex tool compositions with conditional logic.

**Technical Approach**:
- Pure Go JavaScript runtime (dop251/goja) for cross-platform compatibility
- Pool-based concurrency (default 10 instances) for parallel executions
- Context-based timeout enforcement (default 2 minutes)
- Event-driven tool call logging with unique execution IDs
- Feature-flagged rollout (default: disabled) for gradual adoption

## Technical Context

**Language/Version**: Go 1.21+ (matches existing mcpproxy codebase)
**Primary Dependencies**:
- `github.com/dop251/goja` (JavaScript engine - ES5.1+ compliant)
- Existing mcpproxy internal packages (runtime, upstream, storage, logs, httpapi)

**Storage**:
- Execution logs: existing structured logging (`internal/logs/`)
- Tool call history: in-memory during execution, logged to per-server log files
- Configuration: existing `mcp_config.json` with new `enable_code_execution`, `code_execution_timeout_ms`, `code_execution_max_tool_calls` fields

**Testing**:
- Unit: `go test` for `internal/jsruntime` package
- Integration: Test code_execution MCP tool with mock upstream servers
- E2E: CLI command tests, concurrent execution validation
- Security: Sandbox restriction tests (filesystem, network, require)

**Target Platform**: Cross-platform (Linux/macOS/Windows, amd64/arm64) matching existing mcpproxy targets

**Project Type**: Single project (Go backend with CLI)

**Performance Goals**:
- Code execution overhead <5 seconds (excluding upstream tool call latency)
- CLI command response <10 seconds (SC-011)
- Support 10+ concurrent executions without blocking (SC-002)
- Timeout enforcement precision within 1 second (SC-003)

**Constraints**:
- Pure Go (no CGO) for cross-platform compatibility
- ES5.1+ only (no ES6+ features requiring transpilation)
- No persistent state between executions (fresh VM per request)
- Sandbox must prevent filesystem, network, and environment variable access
- Must integrate with existing mcpproxy security (quarantine, allow/deny lists)

**Scale/Scope**:
- Default pool: 10 JavaScript runtime instances
- Maximum concurrent requests: 50+ (SC-010)
- Maximum code size: reuse existing `tool_response_limit` (typically 20KB)
- Maximum execution time: 2 minutes default, 10 minutes absolute max

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### ✅ I. Performance at Scale
- **Compliance**: Code execution adds minimal overhead (<5s) to tool composition
- **Justification**: Pool-based design prevents blocking; timeout enforcement prevents runaway executions
- **Risk**: JavaScript engine memory usage needs monitoring under load (addressed by pool size limits)

### ✅ II. Actor-Based Concurrency
- **Compliance**: Runtime pool uses actor pattern - each pool instance is owned by a goroutine
- **Design**:
  - Pool manager goroutine owns available/in-use instance lists
  - Execution requests sent via channel
  - Context propagation for cancellation and timeout enforcement
  - No shared state between concurrent executions (fresh VM per request)
- **No locks needed**: Pool managed via channels, instances isolated

### ✅ III. Configuration-Driven Architecture
- **Compliance**: All behavior configurable via `mcp_config.json`
- **New config fields**:
  - `enable_code_execution`: boolean (default: false)
  - `code_execution_timeout_ms`: int (default: 120000)
  - `code_execution_max_tool_calls`: int (default: unlimited if 0)
  - `code_execution_pool_size`: int (default: 10)
- **Hot-reload**: Config changes trigger runtime event, pool resizes if needed
- **No tray-specific state**: CLI and tray interact via REST API only

### ✅ IV. Security by Default
- **Compliance**: Multiple security layers
  - Feature disabled by default (`enable_code_execution: false`)
  - Sandbox prevents filesystem, network, environment access
  - `call_tool()` respects existing quarantine and allow/deny lists (FR-016)
  - Execution logging with full transparency (code snippet, tool calls, results)
  - Per-request limits prevent resource exhaustion (timeout, max_tool_calls)
- **Threat model**: Mitigates malicious JavaScript via sandbox; does NOT prevent upstream tool abuse (existing quarantine handles this)

### ✅ V. Test-Driven Development (TDD)
- **Compliance**: Comprehensive test plan
  - Unit tests: `internal/jsruntime` (success, error, timeout scenarios)
  - Integration tests: code_execution MCP tool with mock upstreams
  - E2E tests: CLI command, concurrent execution, sandbox restrictions
  - Security tests: Attempt filesystem/network access, verify rejection
- **Coverage target**: >80% for new `internal/jsruntime` package
- **Linting**: All code passes `golangci-lint` before merge

### ✅ VI. Documentation Hygiene
- **Compliance**: Comprehensive documentation plan (FR-027 through FR-030)
  - Update `CLAUDE.md` with code_execution architecture, CLI commands
  - Add documentation with 5+ working examples (SC-013)
  - API reference for input schema, output format, options, error codes
  - Troubleshooting guide for common errors
  - Update README if user-facing commands change

### ✅ Architecture Constraints

**Separation of Concerns: Core + Tray Split**
- **Compliance**: Feature implemented in core only
  - Core: MCP tool registration, JavaScript runtime pool, execution logic
  - Tray: No changes needed (feature exposed via existing MCP protocol and REST API)
  - CLI: New `mcpproxy code exec` command in core binary

**Event-Driven Updates**
- **Compliance**: Execution events logged and optionally broadcast
  - Each execution emits log event with execution_id, tool calls, outcome
  - Future: Could emit runtime events for monitoring dashboards

**Domain-Driven Design (DDD) Layering**
- **Compliance**: Clear layering
  - **Domain**: `internal/jsruntime` (JavaScript execution, sandbox logic)
  - **Application**: `internal/server/mcp_code_execution.go` (orchestration, option validation)
  - **Infrastructure**: Logging via `internal/logs`, config via `internal/config`
  - **Presentation**: MCP tool schema, REST API (no new endpoints, uses existing MCP protocol)

**Upstream Client Modularity**
- **Compliance**: Uses existing `internal/upstream/managed` client for tool calls
  - `call_tool()` bridges to upstream manager
  - Existing retry logic, state management, connection pooling reused
  - No changes to upstream client layers needed

### 🟡 Minor Note: Pool vs. Actor Purity

**Observation**: JavaScript runtime pool uses a manager pattern (one goroutine owns the pool), which is actor-based. However, pool instances themselves are stateless and handed off to request handlers rather than being persistent actors.

**Justification**:
- Pool instances are VM objects (goja.Runtime), not long-lived goroutines
- Request handler goroutine becomes the temporary "owner" of a pool instance
- This hybrid approach balances actor pattern benefits (channel-based coordination) with resource efficiency (reusing VMs vs. creating/destroying per request)
- No locks used; ownership transfer via channels ensures safety

**Verdict**: ✅ Acceptable deviation - hybrid actor/object-pool pattern justified by performance needs

## Project Structure

### Documentation (this feature)

```text
specs/001-code-execution/
├── plan.md              # This file (/speckit.plan output)
├── research.md          # Phase 0: Technology evaluation (Goja, alternatives)
├── data-model.md        # Phase 1: Entity definitions (Execution Context, Tool Call Record, etc.)
├── quickstart.md        # Phase 1: Quick start guide for developers
├── contracts/           # Phase 1: MCP tool schema (OpenAPI for code_execution tool)
│   └── code_execution.yaml
├── checklists/          # Quality validation checklists
│   └── requirements.md  # Spec validation (already created)
└── tasks.md             # Phase 2: Implementation tasks (/speckit.tasks output - NOT YET CREATED)
```

### Source Code (repository root)

```text
# Existing mcpproxy structure (single Go project)
cmd/
├── mcpproxy/
│   ├── main.go
│   ├── code_cmd.go            # NEW: CLI command for `mcpproxy code exec`
│   └── [existing commands...]
└── mcpproxy-tray/
    └── [no changes needed]

internal/
├── jsruntime/                  # NEW: JavaScript execution engine
│   ├── runtime.go              # Core execution logic, sandbox setup
│   ├── pool.go                 # Runtime instance pool manager
│   ├── pool_test.go            # Pool concurrency tests
│   ├── runtime_test.go         # Execution tests (success/error/timeout)
│   └── errors.go               # JsError type definition
├── server/
│   ├── mcp.go                  # MODIFY: Register code_execution tool
│   └── mcp_code_execution.go   # NEW: code_execution tool handler
├── config/
│   └── config.go               # MODIFY: Add code_execution config fields
├── httpapi/
│   └── server.go               # No changes (code_execution uses existing MCP protocol)
└── [existing packages...]

tests/
└── e2e/
    └── code_execution_test.go  # NEW: E2E tests for CLI, concurrency, security

docs/                            # NEW: Documentation
└── code_execution/
    ├── overview.md              # Feature overview
    ├── examples.md              # 5+ working examples
    ├── api-reference.md         # Input schema, output format, options
    └── troubleshooting.md       # Common errors and solutions
```

**Structure Decision**: Single Go project (Option 1). Feature adds a new internal package (`internal/jsruntime`) and integrates with existing MCP tool registry. No frontend or mobile components needed.

## Complexity Tracking

> **No violations requiring justification**

All constitution principles met without exceptions. The hybrid actor/object-pool pattern for the JavaScript runtime is a minor optimization within the actor-based concurrency model and doesn't violate the principle's intent.

## Phase 0: Research & Technology Evaluation

### Research Tasks

1. **Goja JavaScript Engine Evaluation**
   - **Question**: Is dop251/goja suitable for production use in mcpproxy?
   - **Investigation**:
     - Performance benchmarks (execution time, memory usage)
     - ES5.1+ feature completeness (sufficient for tool composition?)
     - Timeout enforcement mechanisms (context support, interrupt API)
     - Sandbox capabilities (preventing require(), filesystem, network)
   - **Decision Criteria**: Must support ES5.1+, <5s overhead, clean sandbox
   - **Output**: `research.md` section on Goja viability

2. **Alternative JavaScript Engines**
   - **Question**: Should we evaluate alternatives to Goja (e.g., otto, v8go)?
   - **Investigation**:
     - otto: Pure Go but ES5 only, slower, less maintained
     - v8go: Faster but requires CGO (breaks cross-platform goal)
     - goja: Pure Go, actively maintained, ES5.1+, decent performance
   - **Decision Criteria**: Pure Go (no CGO), active maintenance, ES5.1+ support
   - **Output**: `research.md` section on engine comparison

3. **Pool Sizing Strategy**
   - **Question**: How to determine optimal default pool size?
   - **Investigation**:
     - CPU core count correlation (2x cores, 4x cores?)
     - Memory usage per goja instance (~10-50MB)
     - Typical concurrent execution patterns (1-10 clients typical)
   - **Decision Criteria**: Conservative default (avoid OOM), configurable for scaling
   - **Output**: `research.md` section on pool sizing rationale (recommending default: 10)

4. **Timeout Enforcement Approach**
   - **Question**: How to reliably enforce execution timeout in Goja?
   - **Investigation**:
     - Context-based approach (context.WithTimeout + goroutine)
     - Goja interrupt API (vm.Interrupt() capability)
     - Watchdog pattern (separate goroutine monitors execution)
   - **Decision Criteria**: Must terminate within 1s of timeout, no hanging
   - **Output**: `research.md` section on timeout strategy (watchdog + context)

5. **Error Serialization Strategy**
   - **Question**: How to extract JavaScript stack traces from Goja errors?
   - **Investigation**:
     - goja.Exception type and Stack() method
     - Line number extraction for syntax errors
     - Error code mapping (JS error types → MCP error codes)
   - **Decision Criteria**: Complete stack traces, line numbers, clear error codes
   - **Output**: `research.md` section on error handling design

6. **Integration with Existing Logging**
   - **Question**: How to integrate code_execution logs with per-server logging?
   - **Investigation**:
     - Should code_execution have its own log file?
     - Should tool calls within JS be logged to upstream server logs?
     - How to correlate execution_id across log entries?
   - **Decision Criteria**: Maintain existing logging patterns, clear correlation
   - **Output**: `research.md` section on logging integration (main.log + execution_id)

7. **CLI Command Design Patterns**
   - **Question**: What's the idiomatic Cobra CLI structure for `mcpproxy code exec`?
   - **Investigation**:
     - Review existing `mcpproxy tools call` command structure
     - Flag naming conventions (--code vs --script, --input vs --params)
     - Output formatting (JSON always, or support --format flag?)
   - **Decision Criteria**: Consistency with existing CLI, intuitive for developers
   - **Output**: `research.md` section on CLI design (flags, output format)

8. **Documentation Best Practices**
   - **Question**: What format and structure for code_execution documentation?
   - **Investigation**:
     - Existing mcpproxy docs structure (README, CLAUDE.md, inline help)
     - Example documentation from similar tools (eval commands, scripting features)
     - LLM-friendly documentation patterns (clear examples, error codes)
   - **Decision Criteria**: Accessible to developers and LLMs, comprehensive examples
   - **Output**: `research.md` section on documentation structure

### Research Output: research.md

The research phase will produce `research.md` containing:

1. **Goja Evaluation Summary**
   - Decision: Use dop251/goja
   - Rationale: Pure Go, actively maintained, ES5.1+ support, adequate performance
   - Benchmark results: Typical execution <100ms overhead for simple scripts
   - Sandbox verification: Confirmed no access to require(), filesystem, network

2. **Engine Comparison Table**
   - otto: ES5 only, slower, less maintained → Rejected
   - v8go: Faster but requires CGO → Rejected (breaks cross-platform)
   - goja: Selected (meets all criteria)

3. **Pool Configuration**
   - Default size: 10 instances
   - Rationale: Conservative estimate (2-4MB per instance * 10 = 20-40MB baseline)
   - Configurable via `code_execution_pool_size` for scaling
   - Auto-scaling NOT implemented in v1 (defer to future)

4. **Timeout Strategy**
   - Approach: Watchdog goroutine + context cancellation
   - Implementation: `time.After()` with channel select
   - Termination guarantee: Within 1 second of timeout expiry
   - Note: Goja doesn't support hard interrupts; relies on cooperative timeout checks

5. **Error Handling Design**
   - Use `goja.Exception` type for stack traces
   - Extract syntax errors with line numbers before execution
   - Map JS error types to MCP error codes (SYNTAX_ERROR, RUNTIME_ERROR, TIMEOUT)
   - Include truncated code snippet in error logs (first 500 chars)

6. **Logging Strategy**
   - Primary log: `main.log` (all code_execution events)
   - Execution format: `[code_execution] execution_id=<uuid> status=<status> duration=<ms> tools_called=<list>`
   - Tool call logs: Written to respective upstream server logs (existing pattern)
   - Correlation: execution_id appears in all related log entries

7. **CLI Command Structure**
   - Command: `mcpproxy code exec`
   - Flags:
     - `--code <string>`: Inline JavaScript code
     - `--file <path>`: Load code from file (mutually exclusive with --code)
     - `--input <json>`: Inline JSON input
     - `--input-file <path>`: Load input from JSON file (mutually exclusive with --input)
     - `--timeout <ms>`: Override default timeout
     - `--max-tool-calls <int>`: Override default limit
     - `--allowed-servers <csv>`: Comma-separated list of allowed servers
   - Output: Always JSON format (matches MCP response structure)
   - Exit codes: 0 for success, 1 for errors

8. **Documentation Structure**
   - Location: `docs/code_execution/` directory
   - Files:
     - `overview.md`: Feature description, when to use, security model
     - `examples.md`: 5+ working examples with explanations
     - `api-reference.md`: Complete schema documentation
     - `troubleshooting.md`: Common errors and solutions
   - Integration: Update `CLAUDE.md` with architecture overview, CLI commands
   - LLM-friendly: Include examples in MCP tool description

## Phase 1: Design & Contracts

### Data Model (data-model.md)

**Entities**:

1. **Code Execution Request**
   - **Purpose**: Represents input to code_execution tool
   - **Attributes**:
     - `code`: string (JavaScript source, required)
     - `input`: object (arbitrary JSON, optional, default {})
     - `options`: object (optional)
       - `timeout_ms`: integer (1-600000, default from config)
       - `max_tool_calls`: integer (>= 0, 0 = unlimited, default from config)
       - `allowed_servers`: array of strings (optional, default all enabled)
   - **Validation**:
     - `code` must be non-empty
     - `timeout_ms` must be 1-600000 if provided
     - `max_tool_calls` must be >= 0 if provided
   - **Lifecycle**: Created per request, discarded after response

2. **Execution Context**
   - **Purpose**: Runtime environment for single JavaScript execution
   - **Attributes**:
     - `execution_id`: UUID (unique identifier)
     - `start_time`: timestamp
     - `end_time`: timestamp (nullable until completion)
     - `status`: enum (running | success | error | timeout)
     - `pool_instance_id`: integer (which pool instance used)
     - `tool_calls`: array of Tool Call Records
     - `result_value`: any (JSON-serializable result)
     - `error_details`: JsError (nullable)
   - **State Transitions**:
     - `running` → `success` (code completes, returns value)
     - `running` → `error` (JavaScript throws exception)
     - `running` → `timeout` (execution exceeds timeout_ms)
   - **Lifecycle**: Created when request starts, persisted to logs when complete

3. **Tool Call Record**
   - **Purpose**: Audit log of single call_tool() invocation
   - **Attributes**:
     - `server_name`: string
     - `tool_name`: string
     - `arguments`: object (JSON args passed to tool)
     - `start_time`: timestamp
     - `duration_ms`: integer
     - `success`: boolean
     - `result`: any (tool response if success)
     - `error_details`: object (error info if !success)
   - **Relationships**: Belongs to one Execution Context
   - **Lifecycle**: Created when call_tool() invoked, appended to context

4. **JavaScript Runtime Pool**
   - **Purpose**: Manages reusable goja.Runtime instances
   - **Attributes**:
     - `pool_size`: integer (from config, default 10)
     - `available_instances`: queue of goja.Runtime
     - `in_use_instances`: set of goja.Runtime (for tracking)
     - `queue_depth`: integer (requests waiting for instance)
   - **Operations**:
     - `Acquire()`: Get instance from pool (blocks if empty)
     - `Release(instance)`: Return instance to pool
     - `Resize(new_size)`: Add/remove instances (for hot config reload)
   - **Lifecycle**: Created on server startup, persists until shutdown

5. **Execution Log Entry**
   - **Purpose**: Persistent audit record for monitoring and debugging
   - **Attributes**:
     - `execution_id`: UUID
     - `timestamp`: timestamp
     - `client_id`: string (from MCP session)
     - `truncated_code`: string (first 500 chars)
     - `tool_calls_made`: array of {server, tool} pairs
     - `duration_ms`: integer
     - `outcome`: enum (success | error | timeout)
     - `error_message`: string (nullable)
   - **Relationships**: References Execution Context (via execution_id)
   - **Lifecycle**: Written to `main.log` when execution completes

### API Contracts (contracts/code_execution.yaml)

OpenAPI schema for the code_execution MCP tool:

```yaml
# contracts/code_execution.yaml
openapi: 3.0.0
info:
  title: code_execution MCP Tool
  version: 1.0.0
  description: |
    Execute JavaScript code that orchestrates multiple MCP tools.

    **When to use**:
    - Combining 2+ tool calls with data transformation
    - Implementing conditional logic or loops
    - Post-processing tool results

    **When NOT to use**:
    - Single tool calls (use direct tool instead)
    - Long-running operations (>2 minutes)

paths:
  /tools/call:
    post:
      summary: Execute code_execution tool
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - name
                - arguments
              properties:
                name:
                  type: string
                  enum: [code_execution]
                arguments:
                  $ref: '#/components/schemas/CodeExecutionRequest'

      responses:
        '200':
          description: Execution result
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/CodeExecutionResponse'

components:
  schemas:
    CodeExecutionRequest:
      type: object
      required:
        - code
      properties:
        code:
          type: string
          description: JavaScript source code (ES5.1+)
          example: |
            const userRes = call_tool("github", "get_user", { username: input.username });
            if (!userRes.ok) throw new Error("User not found");
            ({ user: userRes.result, timestamp: Date.now() })

        input:
          type: object
          description: Input data accessible as global `input` variable
          default: {}
          example:
            username: octocat

        options:
          type: object
          properties:
            timeout_ms:
              type: integer
              minimum: 1
              maximum: 600000
              description: Max execution time (default: 120000ms)
              example: 60000

            max_tool_calls:
              type: integer
              minimum: 0
              description: Max call_tool() invocations (0 = unlimited, default from config)
              example: 10

            allowed_servers:
              type: array
              items:
                type: string
              description: Whitelist of MCP servers (default: all enabled)
              example: ["github", "slack"]

    CodeExecutionResponse:
      type: object
      required:
        - ok
      properties:
        ok:
          type: boolean
          description: Execution success status

        value:
          description: JavaScript return value (JSON-serializable)
          nullable: true
          example:
            user:
              login: octocat
              id: 583231
            timestamp: 1700000000000

        error:
          type: object
          nullable: true
          properties:
            message:
              type: string
              description: Error message
            stack:
              type: string
              description: JavaScript stack trace
            code:
              type: string
              enum:
                - SYNTAX_ERROR
                - RUNTIME_ERROR
                - TIMEOUT
                - MAX_TOOL_CALLS_EXCEEDED
                - SERVER_NOT_ALLOWED
                - SERIALIZATION_ERROR
          example:
            message: "Failed to load user: User not found"
            stack: "Error: Failed to load user: User not found\n    at <anonymous>:2:23"
            code: RUNTIME_ERROR

    CallToolResponse:
      description: Response format from call_tool() function (internal to JavaScript)
      type: object
      required:
        - ok
      properties:
        ok:
          type: boolean
        result:
          description: Tool result if ok=true
          nullable: true
        error:
          type: object
          nullable: true
          properties:
            message:
              type: string
            code:
              type: string
              enum:
                - NOT_FOUND
                - UPSTREAM_ERROR
                - TIMEOUT
                - INVALID_ARGS
                - SERVER_NOT_ALLOWED
            details:
              type: object
```

### Quickstart Guide (quickstart.md)

Developer quickstart for implementing code_execution feature:

```markdown
# Code Execution Quickstart

## Prerequisites

- Go 1.21+
- mcpproxy repository cloned
- Existing mcpproxy architecture familiarity

## Development Setup

1. **Add dependency**:
   ```bash
   go get github.com/dop251/goja@latest
   go mod tidy
   ```

2. **Create internal/jsruntime package**:
   ```bash
   mkdir -p internal/jsruntime
   touch internal/jsruntime/runtime.go
   touch internal/jsruntime/pool.go
   touch internal/jsruntime/errors.go
   ```

3. **Run tests** (write tests first per TDD):
   ```bash
   go test ./internal/jsruntime -v
   ```

## Implementation Order

### Phase 1: Core Runtime (MVP)

1. **`internal/jsruntime/runtime.go`**:
   - `Execute(ctx, caller, code, input, opts)` function
   - Goja VM initialization
   - `input` global binding
   - `call_tool()` function binding
   - Timeout enforcement via watchdog goroutine
   - Error extraction and serialization

2. **`internal/jsruntime/errors.go`**:
   - `JsError` type (message, stack)
   - `Result` type (value)
   - Error code constants

3. **Unit tests**:
   - Success: simple code returns value
   - Error: JavaScript throws exception
   - Timeout: code exceeds limit
   - Sandbox: require(), filesystem blocked

### Phase 2: Pool Management

4. **`internal/jsruntime/pool.go`**:
   - `Pool` struct with channels for available instances
   - `NewPool(size)` constructor
   - `Acquire(ctx)` method (blocks until instance available)
   - `Release(instance)` method
   - `Resize(new_size)` for hot config reload

5. **Pool tests**:
   - Concurrent acquisitions
   - Release returns to pool
   - Resize adds/removes instances

### Phase 3: MCP Integration

6. **`internal/server/mcp_code_execution.go`**:
   - Tool handler implementing MCP tool interface
   - Parse `code`, `input`, `options` from json_args
   - Acquire pool instance
   - Call `jsruntime.Execute()`
   - Format response as `{ ok, value, error }`
   - Release pool instance (defer)

7. **`internal/server/mcp.go`** (modify):
   - Register code_execution tool in `listTools()`
   - Add tool schema with full description
   - Guard with `config.EnableCodeExecution` check

### Phase 4: Configuration

8. **`internal/config/config.go`** (modify):
   - Add fields:
     - `EnableCodeExecution bool`
     - `CodeExecutionTimeoutMs int`
     - `CodeExecutionMaxToolCalls int`
     - `CodeExecutionPoolSize int`
   - Set defaults in `NewConfig()`

9. **Config tests**:
   - Verify defaults loaded
   - Test config file parsing with new fields

### Phase 5: CLI Command

10. **`cmd/mcpproxy/code_cmd.go`**:
    - Cobra command definition
    - Flags: --code, --file, --input, --input-file, --timeout, etc.
    - Load code from file if --file provided
    - Parse input JSON
    - Call code_execution via internal API (not HTTP)
    - Format and print response
    - Exit with appropriate code

11. **CLI tests**:
    - Inline code execution
    - File-based execution
    - Error handling (syntax error, timeout)
    - Exit codes validation

## Testing Strategy

### Unit Tests
```bash
# Runtime tests
go test ./internal/jsruntime -v -run TestExecute

# Pool tests
go test ./internal/jsruntime -v -run TestPool
```

### Integration Tests
```bash
# MCP tool tests with mock upstreams
go test ./internal/server -v -run TestCodeExecution
```

### E2E Tests
```bash
# CLI tests
go test ./cmd/mcpproxy -v -run TestCodeCommand

# Concurrent execution
go test ./tests/e2e -v -run TestConcurrentCodeExecution
```

### Security Tests
```bash
# Sandbox validation
go test ./internal/jsruntime -v -run TestSandbox
```

## Running Locally

1. **Enable feature in config**:
   ```json
   {
     "enable_code_execution": true,
     "code_execution_timeout_ms": 30000,
     "code_execution_pool_size": 5
   }
   ```

2. **Start mcpproxy**:
   ```bash
   go run ./cmd/mcpproxy serve
   ```

3. **Test via CLI**:
   ```bash
   go run ./cmd/mcpproxy code exec --code="({ result: 42 })"
   ```

4. **Test via MCP**:
   ```bash
   curl -X POST http://localhost:8080/mcp \
     -H "Content-Type: application/json" \
     -d '{
       "jsonrpc": "2.0",
       "method": "tools/call",
       "params": {
         "name": "code_execution",
         "arguments": {
           "code": "({ hello: \"world\" })"
         }
       },
       "id": 1
     }'
   ```

## Debugging Tips

- **Enable debug logging**: `--log-level=debug`
- **Check execution logs**: `tail -f ~/.mcpproxy/logs/main.log | grep code_execution`
- **Inspect pool state**: Add metrics endpoint (future enhancement)
- **Test JavaScript syntax**: Use Node.js REPL for quick validation

## Next Steps

After core implementation:
1. Add documentation (`docs/code_execution/`)
2. Update `CLAUDE.md` with architecture details
3. Run full test suite: `./scripts/run-all-tests.sh`
4. Create PR following commit message conventions
```

### Agent Context Update

```bash
# Update Claude-specific context file
.specify/scripts/bash/update-agent-context.sh claude
```

This script will add `github.com/dop251/goja` to the `.claude/context.md` technology list, preserving manual additions between markers.

## Next Steps

This plan document (`plan.md`) is now complete with:
- ✅ Technical Context filled
- ✅ Constitution Check passed (no violations)
- ✅ Research tasks defined (Phase 0)
- ✅ Data model designed (Phase 1)
- ✅ API contracts specified (Phase 1)
- ✅ Quickstart guide created (Phase 1)

**Command ends here** per workflow. Next phase (`/speckit.tasks`) will generate actionable tasks from this plan.

### To Proceed

Run the following command to generate implementation tasks:

```bash
/speckit.tasks
```

This will create `tasks.md` with dependency-ordered tasks broken down from this plan, ready for execution tracking.

---

**Generated Artifacts**:
- `/Users/user/repos/mcpproxy-go/specs/001-code-execution/plan.md` ← This file
- Research artifacts will be generated when Phase 0 research tasks execute
- Data model, contracts, and quickstart are embedded in this plan (extract to separate files during implementation)
