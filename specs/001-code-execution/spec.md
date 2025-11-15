# Feature Specification: JavaScript Code Execution Tool for MCP Tool Composition

**Feature Branch**: `001-code-execution`
**Created**: 2025-11-15
**Status**: Draft
**Input**: User description: "Specification for adding a code_execution MCP tool to mcpproxy using dop251/goja. The tool lets LLMs send JS, access json_args as input, call upstream tools via call_tool, and return a structured result or error. Focus is on ergonomics, debuggability, and safe defaults."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Basic Multi-Tool Orchestration (Priority: P1)

An LLM agent needs to fetch data from one MCP tool, transform it, and send the result to another tool in a single operation. Instead of making multiple round-trip calls to the model, the agent submits a JavaScript code snippet that orchestrates the entire workflow.

**Why this priority**: This is the core value proposition - enabling multi-step tool workflows without multiple model round-trips. This is the minimum viable product.

**Independent Test**: Can be fully tested by submitting a code_execution request that calls two upstream tools sequentially (e.g., fetch → transform → write) and verifies the final result contains data from both operations.

**Acceptance Scenarios**:

1. **Given** an LLM client connected to mcpproxy with code_execution enabled and two upstream MCP servers available, **When** the client sends a code_execution request with JavaScript that calls both tools via `call_tool()`, **Then** the system returns a success response containing the composed result from both tool calls
2. **Given** a code_execution request that fetches user data from one tool and updates a record in another, **When** the JavaScript executes successfully, **Then** both tools are called in sequence and the final result includes confirmation data from both operations
3. **Given** JavaScript code that processes array data by calling a tool for each item, **When** the code executes with a loop calling `call_tool()` multiple times, **Then** all tool calls succeed and the result contains aggregated data from all calls

---

### User Story 2 - Error Handling and Partial Results (Priority: P1)

When orchestrating multiple tools, one upstream tool call may fail while others succeed. The LLM needs clear error information to decide whether to retry, use partial results, or fail the entire operation.

**Why this priority**: Error handling is critical for production use and directly impacts reliability. Without this, the feature is not production-ready.

**Independent Test**: Can be tested by submitting code that calls a failing tool and a succeeding tool, then verifying the error response includes both the failure details and any partial results collected before the failure.

**Acceptance Scenarios**:

1. **Given** JavaScript code that calls three tools where the second tool fails, **When** the code handles the error and continues execution, **Then** the response includes both the successful first result and error details from the second call
2. **Given** a tool call that times out during code execution, **When** the timeout occurs, **Then** the system returns an error response with code "TIMEOUT" and preserves any results obtained before the timeout
3. **Given** JavaScript that throws an uncaught error, **When** the error occurs, **Then** the response includes the JavaScript error message, stack trace, and indicates which line failed

---

### User Story 3 - Execution Limits and Sandboxing (Priority: P1)

To prevent resource exhaustion and abuse, administrators need to configure execution limits (timeout, max tool calls) that apply globally or per-request. The sandbox must prevent access to filesystem, network, or environment variables.

**Why this priority**: Security and resource protection are non-negotiable for production deployment. This must be part of the MVP.

**Independent Test**: Can be tested by submitting code that attempts to exceed limits (timeout, max_tool_calls) or access restricted APIs (filesystem, require), then verifying the system rejects these attempts with appropriate error messages.

**Acceptance Scenarios**:

1. **Given** a global timeout of 30 seconds is configured, **When** JavaScript code runs for 31 seconds, **Then** the execution is terminated with error "JavaScript execution timed out"
2. **Given** max_tool_calls is set to 5 in request options, **When** JavaScript attempts to make 6 tool calls, **Then** the 6th call fails with error "max tool calls exceeded"
3. **Given** JavaScript code that attempts to use `require()` or access filesystem APIs, **When** the code executes, **Then** the sandbox prevents the operation and returns an error indicating these APIs are not available
4. **Given** a request-specific timeout override of 60 seconds, **When** code runs for 45 seconds, **Then** execution completes successfully, respecting the per-request limit rather than global default

---

### User Story 4 - Parallel Execution for Multiple Clients (Priority: P2)

When multiple LLM clients are connected to mcpproxy simultaneously, each client's code_execution requests must run in isolated sandboxes without blocking other clients or consuming shared resources unfairly.

**Why this priority**: Essential for multi-client deployments but not blocking for single-client testing and validation.

**Independent Test**: Can be tested by submitting concurrent code_execution requests from different clients and verifying each executes independently with isolated state and respects per-client rate limits.

**Acceptance Scenarios**:

1. **Given** three concurrent code_execution requests from different clients, **When** all three execute simultaneously, **Then** each runs in an isolated JavaScript sandbox and returns results independently
2. **Given** Client A's code execution is running for 20 seconds, **When** Client B submits a new code_execution request, **Then** Client B's request starts immediately without waiting for Client A to finish
3. **Given** a pool of 10 JavaScript runtime instances, **When** 15 concurrent requests arrive, **Then** the first 10 execute immediately and the remaining 5 queue with appropriate backpressure indicators

---

### User Story 5 - Configuration and Feature Toggle (Priority: P2)

Administrators need to control when code_execution is available through configuration. The feature must default to disabled and require explicit opt-in to prevent unexpected security exposure.

**Why this priority**: Important for security and gradual rollout, but doesn't block core functionality testing if manually enabled in development.

**Independent Test**: Can be tested by verifying that with `enable_code_execution: false` in config, the code_execution tool is not returned in tools/list and requests are rejected, while with `enable_code_execution: true`, the tool becomes available.

**Acceptance Scenarios**:

1. **Given** mcpproxy starts with default configuration (no enable_code_execution setting), **When** an LLM client requests the tools list, **Then** code_execution is NOT included in the response
2. **Given** configuration with `"enable_code_execution": true`, **When** mcpproxy restarts and a client lists tools, **Then** code_execution appears in the available tools with full schema
3. **Given** configuration includes code_execution defaults (timeout: 45000ms, max_tool_calls: 20), **When** a code_execution request omits these options, **Then** the configured defaults are applied

---

### User Story 6 - Tool Call History and Observability (Priority: P2)

Administrators and developers need visibility into code_execution usage including which tools were called, execution times, and any errors. Each execution should be logged with a unique ID for correlation.

**Why this priority**: Critical for debugging and monitoring production use, but not required for initial feature validation.

**Independent Test**: Can be tested by executing code that calls multiple tools, then verifying log entries include execution ID, tool names called, timing data, and any errors encountered.

**Acceptance Scenarios**:

1. **Given** a code_execution request that calls three different upstream tools, **When** execution completes, **Then** logs include an execution_id, timestamps for start/end, list of tools called with their durations, and final status
2. **Given** JavaScript code that fails with an error, **When** the error occurs, **Then** logs capture the truncated JavaScript code (first 500 chars), error message, stack trace, and which tool call (if any) triggered the failure
3. **Given** multiple concurrent executions, **When** reviewing logs, **Then** each execution has a unique execution_id that appears in all related log entries for easy filtering

---

### User Story 7 - LLM-Friendly Tool Description and Examples (Priority: P2)

LLMs need clear guidance on when to use code_execution versus individual tool calls. The tool description must include examples and best practices to steer LLM behavior toward optimal usage patterns.

**Why this priority**: Impacts LLM effectiveness but doesn't affect core functionality. Can be refined iteratively based on usage patterns.

**Independent Test**: Can be tested by providing the tool description to an LLM and verifying it makes appropriate decisions (using code_execution for multi-step workflows, using direct tools for simple single operations).

**Acceptance Scenarios**:

1. **Given** an LLM receives the code_execution tool schema, **When** the user requests a simple single-tool operation, **Then** the LLM chooses the direct tool call rather than code_execution
2. **Given** an LLM needs to orchestrate 3+ tools with conditional logic, **When** evaluating available tools, **Then** the LLM selects code_execution and structures the request correctly with code, input, and options
3. **Given** the tool description includes examples of error handling, **When** an LLM writes JavaScript for code_execution, **Then** the code includes proper checks for `res.ok` and error handling patterns shown in examples

---

### User Story 8 - Input Data Passing and Result Extraction (Priority: P1)

LLMs need to pass structured input data to JavaScript code and receive structured results back. The `input` global variable should contain any parameters provided in the request, and the final JavaScript expression should be serialized as the result.

**Why this priority**: This is fundamental to the API contract and must work correctly for any useful code execution.

**Independent Test**: Can be tested by submitting a request with complex input data (nested objects, arrays) and verifying the JavaScript can access all fields, and that the returned value preserves structure.

**Acceptance Scenarios**:

1. **Given** a request with `input: { userId: "123", preferences: { theme: "dark" } }`, **When** JavaScript accesses `input.userId` and `input.preferences.theme`, **Then** both values are available and correct
2. **Given** JavaScript code that returns `{ summary: "text", items: [1,2,3], metadata: { count: 3 } }`, **When** execution completes, **Then** the response `value` field contains the exact structure with all nested data
3. **Given** JavaScript that returns a function or circular reference, **When** attempting JSON serialization, **Then** the system returns an error indicating "result must be JSON-serializable"

---

### User Story 9 - CLI Testing Interface (Priority: P2)

Developers need to test JavaScript code execution directly from the command line without setting up an MCP client connection. A CLI command similar to `mcpproxy tools call` should allow executing code, passing input data, and viewing results for rapid iteration and debugging.

**Why this priority**: Essential for developer productivity and testing, but not required for LLM client functionality.

**Independent Test**: Can be tested by running a CLI command with JavaScript code and input parameters, then verifying the output matches expected format and includes execution results or errors.

**Acceptance Scenarios**:

1. **Given** mcpproxy is configured with code_execution enabled, **When** a developer runs `mcpproxy code exec --code="<js>" --input='{"key":"value"}'`, **Then** the system executes the code and outputs the result in JSON format
2. **Given** JavaScript code in a file `script.js`, **When** developer runs `mcpproxy code exec --file=script.js --input-file=params.json`, **Then** the code executes with input from the JSON file and displays formatted output
3. **Given** code that calls upstream tools, **When** executed via CLI, **Then** the command respects all configuration options (timeout, max_tool_calls, allowed_servers) and shows detailed execution trace
4. **Given** code that fails with an error, **When** executed via CLI, **Then** the command exits with non-zero status code and displays the error message, stack trace, and execution_id for debugging

---

### User Story 10 - Documentation and Examples (Priority: P2)

Users, developers, and LLMs need comprehensive documentation explaining code_execution capabilities, usage patterns, and best practices. Documentation should include practical examples showing common workflows, error handling, and integration with upstream tools.

**Why this priority**: Critical for adoption and correct usage, but can be developed alongside or after core implementation.

**Independent Test**: Can be tested by reviewing documentation completeness against checklist of required topics and validating that examples execute successfully.

**Acceptance Scenarios**:

1. **Given** a new user reads the code_execution documentation, **When** they review the examples section, **Then** they find working examples for: basic tool composition, error handling, loops/conditionals, and partial result handling
2. **Given** an LLM accesses the code_execution tool description, **When** parsing the schema and examples, **Then** the description includes clear guidance on when to use code_execution vs direct tools, how to structure the request, and error handling patterns
3. **Given** documentation includes API reference, **When** developers review it, **Then** they find complete documentation of: input schema, output format, options (timeout_ms, max_tool_calls, allowed_servers), error codes, and sandbox restrictions
4. **Given** documentation includes troubleshooting guide, **When** users encounter common issues, **Then** they find solutions for: timeout errors, max_tool_calls exceeded, sandbox restrictions, JSON serialization errors, and tool discovery

---

### Edge Cases

- **What happens when JavaScript code is syntactically invalid?** System returns error with syntax details and line number before any execution begins
- **What happens when `call_tool()` is called with a non-existent server or tool name?** Returns `{ ok: false, error: { code: "NOT_FOUND", message: "..." } }` to the JavaScript, allowing JS to handle gracefully
- **What happens when execution timeout is reached mid-tool-call?** The in-flight tool call is canceled (via context cancellation), and timeout error is returned
- **What happens when max_tool_calls is set to 0 in options?** Treated as "unlimited" (no limit enforced)
- **What happens when `allowed_servers` list is empty?** All servers are blocked, any `call_tool()` attempt returns SERVER_NOT_ALLOWED error
- **What happens when input contains very large data (multiple MB)?** Execution proceeds but is subject to overall tool_response_limit truncation if result exceeds configured limit
- **What happens when multiple code_execution requests use the same execution pool?** Requests queue if all pool instances are busy; oldest request times out first if queued beyond its timeout
- **What happens when a tool returns non-JSON data?** JavaScript receives the raw result; responsibility is on JS code to handle or transform as needed

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a built-in MCP tool named `code_execution` that accepts JavaScript code as a string parameter
- **FR-002**: System MUST execute JavaScript code in an isolated sandbox with no access to filesystem, network, environment variables, or Node.js modules (ES5.1+ built-ins only)
- **FR-003**: System MUST provide a `call_tool(serverName, toolName, args)` function within the JavaScript sandbox that proxies calls to upstream MCP servers
- **FR-004**: System MUST bind request input data as a global `input` variable accessible to JavaScript code
- **FR-005**: System MUST serialize the final JavaScript expression value as the result and return it in the response
- **FR-006**: System MUST enforce a maximum execution timeout (default: 2 minutes, configurable globally and per-request via `timeout_ms` option)
- **FR-007**: System MUST enforce a maximum number of tool calls per execution (configurable globally and per-request via `max_tool_calls` option)
- **FR-008**: System MUST support restricting which upstream servers can be called via `allowed_servers` option (empty = none allowed)
- **FR-009**: System MUST return structured error responses including JavaScript error message, stack trace, and error code for all failure cases
- **FR-010**: System MUST log each code_execution invocation with unique execution ID, start/end times, tools called, and outcome
- **FR-011**: System MUST maintain a pool of JavaScript runtime instances to support concurrent executions without blocking
- **FR-012**: System MUST make code_execution tool availability controllable via configuration flag `enable_code_execution` (default: false)
- **FR-013**: System MUST return success responses in format `{ ok: true, value: <result> }` and error responses in format `{ ok: false, error: { message, stack, code } }`
- **FR-014**: System MUST provide clear tool description explaining when to use code_execution (multi-tool workflows) versus direct tool calls (single operations)
- **FR-015**: System MUST validate JavaScript code for JSON-serializability of return value before sending response (reject functions, circular refs)
- **FR-016**: System MUST respect existing mcpproxy security features (quarantine, allow/deny lists) when `call_tool()` invokes upstream tools
- **FR-017**: System MUST support both global configuration defaults and per-request option overrides for timeout_ms, max_tool_calls, and allowed_servers
- **FR-018**: System MUST truncate logged JavaScript code to prevent log flooding (e.g., first 500 characters) while preserving full code for execution
- **FR-019**: System MUST include execution_id in both response payload and logs for correlation and debugging
- **FR-020**: System MUST handle JavaScript execution timeout by terminating execution and returning timeout error (not hanging indefinitely)
- **FR-021**: System MUST provide a CLI command (e.g., `mcpproxy code exec`) that executes JavaScript code without requiring an MCP client connection
- **FR-022**: CLI command MUST support both inline code (`--code` flag) and file-based code (`--file` flag) execution
- **FR-023**: CLI command MUST support input data via inline JSON (`--input` flag) or JSON file (`--input-file` flag)
- **FR-024**: CLI command MUST display execution results in JSON format with the same structure as MCP responses (`{ ok, value, error }`)
- **FR-025**: CLI command MUST exit with non-zero status code when execution fails and display error details (message, stack, code)
- **FR-026**: CLI command MUST support all execution options (timeout, max_tool_calls, allowed_servers) via command-line flags
- **FR-027**: System MUST provide comprehensive documentation including: feature overview, usage patterns, best practices, and when to use code_execution vs direct tools
- **FR-028**: Documentation MUST include working examples demonstrating: basic tool composition, error handling, loops/conditionals, and partial result handling
- **FR-029**: Documentation MUST include complete API reference covering: input schema, output format, all options, error codes, and sandbox restrictions
- **FR-030**: Documentation MUST include troubleshooting guide with solutions for common errors (timeout, max_tool_calls, sandbox violations, JSON serialization)

### Key Entities *(include if feature involves data)*

- **Code Execution Request**: Represents a single invocation of the code_execution tool
  - Attributes: code (JavaScript source), input (JSON object), options (timeout_ms, max_tool_calls, allowed_servers)
  - Relationships: Creates one Execution Context, may trigger multiple Tool Calls

- **Execution Context**: Represents the runtime environment for a single JavaScript execution
  - Attributes: execution_id (UUID), start_time, end_time, status (running/success/error/timeout), pool_instance_id
  - Relationships: Contains one JavaScript VM instance, maintains history of Tool Calls made

- **Tool Call Record**: Represents a single call to an upstream MCP tool from within JavaScript
  - Attributes: server_name, tool_name, arguments, start_time, duration_ms, success (boolean), error_details
  - Relationships: Belongs to one Execution Context, references one Upstream MCP Server

- **JavaScript Runtime Pool**: Manages reusable JavaScript VM instances for concurrent executions
  - Attributes: pool_size (configurable), available_instances, in_use_instances, queue_depth
  - Relationships: Contains multiple Execution Contexts, each using one pool instance

- **Execution Log Entry**: Audit record of code_execution activity
  - Attributes: execution_id, timestamp, client_id, truncated_code, tool_calls_made, duration_ms, outcome (success/error/timeout), error_details
  - Relationships: References one Execution Context, may reference multiple Tool Call Records

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: LLM agents can orchestrate 3+ upstream tool calls in a single code_execution request with end-to-end latency under 30 seconds (excluding upstream tool call durations)
- **SC-002**: System successfully executes 10 concurrent code_execution requests from different clients without any request blocking on pool availability (assuming pool_size ≥ 10)
- **SC-003**: 100% of timeout violations (code exceeding configured timeout_ms) are terminated within 1 second of timeout expiry and return appropriate error
- **SC-004**: 100% of code_execution attempts with `enable_code_execution: false` configuration are rejected with clear error message
- **SC-005**: All JavaScript execution errors include complete stack traces with line numbers, enabling debugging of code issues
- **SC-006**: System logs capture 100% of code_execution invocations with unique execution_id, tool call details, and timing data for audit and monitoring
- **SC-007**: LLMs successfully structure valid code_execution requests (correct schema, proper error handling) in 90%+ of multi-tool workflow scenarios when given tool description
- **SC-008**: Code execution sandbox prevents 100% of attempts to access filesystem, network, or restricted APIs, returning appropriate errors
- **SC-009**: 95%+ of valid code_execution requests complete successfully when upstream tools are healthy and code is syntactically correct
- **SC-010**: System handles at least 50 concurrent code_execution requests with graceful queueing and timeout behavior (no crashes or deadlocks)
- **SC-011**: Developers can execute and verify code_execution functionality via CLI command in under 10 seconds from command invocation to result display
- **SC-012**: 100% of CLI executions that fail return non-zero exit codes enabling reliable scripting and automation
- **SC-013**: Documentation includes at least 5 working examples covering different use cases (composition, error handling, loops, conditionals, partial results)
- **SC-014**: 90%+ of developers can successfully use code_execution after reading documentation without additional support

## Assumptions

1. **JavaScript Version**: ES5.1 feature set is sufficient for MCP tool composition; no need for ES6+ features (arrow functions, async/await, modules) in initial version
2. **Execution Model**: Fresh VM per request (no persistent state) is acceptable; benefits of stateless execution outweigh performance overhead
3. **Timeout Mechanism**: 2-minute default timeout is appropriate for most multi-tool workflows; longer operations should be split or require explicit override
4. **Pool Size**: Default pool of 10 JavaScript runtime instances provides reasonable concurrency for typical deployment; configurable for high-traffic scenarios
5. **Error Handling**: Returning JavaScript errors (message + stack) to clients is acceptable; no need to sanitize or redact error details in initial version
6. **Tool Discovery**: LLMs will use existing `retrieve_tools` mechanism to discover available upstream tools before writing code_execution scripts
7. **Logging Verbosity**: Debug/info level logging is sufficient; trace-level logging of every JS operation is unnecessary and would create noise
8. **Code Size Limits**: Reusing existing `tool_response_limit` for maximum code size and result size is appropriate; no need for separate code_size_limit
9. **Security Model**: Restricting to call_tool-only access (no direct file/network/env) provides adequate sandboxing for v1; more granular permissions can be added later if needed
10. **LLM Behavior**: With proper tool description and examples, LLMs will learn to use code_execution appropriately without requiring server-side heuristics to block misuse
11. **CLI Command Structure**: Following existing `mcpproxy` CLI patterns (e.g., `mcpproxy tools call`, `mcpproxy auth login`) is acceptable; command should be `mcpproxy code exec` or similar
12. **Documentation Location**: Documentation can be added to existing mcpproxy docs structure (README, docs/ directory, or inline help text); exact location determined during implementation
13. **Example Complexity**: Five comprehensive examples covering main use cases is sufficient for initial release; additional examples can be added based on user feedback

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add code_execution MCP tool with Goja sandbox

Related #[issue-number]

Implements JavaScript code execution for multi-tool composition using dop251/goja.
Enables LLM agents to orchestrate multiple upstream MCP tool calls in a single
request, reducing round-trip latency and enabling complex workflows.

## Changes
- Add internal/jsruntime package with Goja-based sandbox
- Implement code_execution tool in MCP tool registry
- Add call_tool bridge for upstream tool invocation
- Implement execution timeout and max_tool_calls limits
- Add JavaScript runtime pool for concurrent executions
- Add enable_code_execution config flag (default: false)
- Add execution logging with unique execution_id
- Add CLI command `mcpproxy code exec` for testing without MCP client
- Add comprehensive documentation with 5+ working examples
- Add troubleshooting guide and API reference

## Testing
- Unit tests for jsruntime package (success/error/timeout scenarios)
- Integration tests for code_execution tool with mock upstream servers
- Concurrent execution tests validating pool behavior
- Security tests validating sandbox restrictions
- CLI command tests for inline code, file-based execution, and error handling
- Documentation validation (examples execute successfully)
```
