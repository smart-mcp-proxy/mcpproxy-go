# Tasks: JavaScript Code Execution Tool

**Input**: Design documents from `/specs/001-code-execution/`
**Prerequisites**: plan.md (complete), spec.md (complete), constitution check (passed)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure

- [ ] T001 Add github.com/dop251/goja dependency: `go get github.com/dop251/goja@latest && go mod tidy`
- [ ] T002 Create internal/jsruntime directory: `mkdir -p internal/jsruntime`
- [ ] T003 [P] Create tests/e2e directory for E2E tests: `mkdir -p tests/e2e`
- [ ] T004 [P] Create docs/code_execution directory: `mkdir -p docs/code_execution`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Research Phase 0 Tasks

- [ ] T005 [P] Research: Goja engine evaluation - performance benchmarks and sandbox capabilities (document in specs/001-code-execution/research.md)
- [ ] T006 [P] Research: Alternative engines comparison (otto vs v8go vs goja) - document decision rationale
- [ ] T007 [P] Research: Pool sizing strategy - determine optimal default (recommend 10) based on memory usage
- [ ] T008 [P] Research: Timeout enforcement approach - evaluate watchdog pattern with context cancellation
- [ ] T009 [P] Research: Error serialization strategy - test goja.Exception stack trace extraction
- [ ] T010 [P] Research: Logging integration - design execution_id correlation strategy
- [ ] T011 [P] Research: CLI command design patterns - review existing `mcpproxy tools call` structure
- [ ] T012 [P] Research: Documentation best practices - analyze LLM-friendly examples from similar tools

### Core Error Handling

- [ ] T013 Create internal/jsruntime/errors.go with JsError type and error code constants (SYNTAX_ERROR, RUNTIME_ERROR, TIMEOUT, MAX_TOOL_CALLS_EXCEEDED, SERVER_NOT_ALLOWED, SERIALIZATION_ERROR)

### Configuration

- [ ] T014 Modify internal/config/config.go to add EnableCodeExecution, CodeExecutionTimeoutMs, CodeExecutionMaxToolCalls, CodeExecutionPoolSize fields with defaults
- [ ] T015 Add config validation for new fields (timeout: 1-600000ms, pool_size: 1-100, max_tool_calls >= 0)
- [ ] T016 Update config tests to verify new fields parse correctly and defaults apply

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: [US1] Basic Multi-Tool Orchestration (Priority: P1) 🎯 MVP

**Goal**: Enable LLM agents to orchestrate multiple upstream tools in a single JavaScript request using `call_tool()`

**Independent Test**: Submit code_execution request calling two tools sequentially (e.g., github:get_user → slack:post_message), verify both execute and result combines data from both

### Tests for User Story 1 (TDD - Write First)

> **NOTE: Write these tests FIRST, ensure they FAIL before implementation**

- [ ] T017 [P] [US1] Unit test: Execute simple JS returning value in internal/jsruntime/runtime_test.go
- [ ] T018 [P] [US1] Unit test: Execute JS calling mock call_tool() once in internal/jsruntime/runtime_test.go
- [ ] T019 [P] [US1] Unit test: Execute JS with loop calling call_tool() multiple times in internal/jsruntime/runtime_test.go
- [ ] T020 [P] [US1] Integration test: code_execution tool with two mock upstream servers in internal/server/mcp_code_execution_test.go

### Implementation for User Story 1

- [ ] T021 [P] [US1] Create internal/jsruntime/runtime.go with Execute(ctx, caller, code, input, opts) function signature
- [ ] T022 [US1] Implement Goja VM initialization in Execute() - create runtime, set up sandbox restrictions (no require, no fs, no net)
- [ ] T023 [US1] Implement `input` global variable binding - expose opts.Input as global `input` in VM
- [ ] T024 [US1] Implement `call_tool(serverName, toolName, args)` function binding - bridge to caller.CallTool()
- [ ] T025 [US1] Implement result extraction - serialize final JS expression as JSON and return in Result type
- [ ] T026 [US1] Add basic error handling - catch goja.Exception, extract message/stack, return JsError
- [ ] T027 [US1] Create internal/server/mcp_code_execution.go tool handler - parse json_args (code, input, options)
- [ ] T028 [US1] Integrate with jsruntime.Execute() in tool handler - call Execute(), format response as { ok, value, error }
- [ ] T029 [US1] Modify internal/server/mcp.go to register code_execution tool in listTools() with guard for config.EnableCodeExecution
- [ ] T030 [US1] Add code_execution tool schema to MCP tools list with full description explaining when to use vs direct tools

**Checkpoint**: At this point, basic code execution with call_tool should work for simple sequential scripts

---

## Phase 4: [US2] Error Handling and Partial Results (Priority: P1)

**Goal**: Provide clear error responses with stack traces when tool calls fail, allowing JS to handle errors gracefully

**Independent Test**: Submit code calling failing tool and succeeding tool, verify response includes both error details and partial results

### Tests for User Story 2 (TDD - Write First)

- [ ] T031 [P] [US2] Unit test: JS throws uncaught exception - verify error message and stack trace in internal/jsruntime/runtime_test.go
- [ ] T032 [P] [US2] Unit test: call_tool() returns { ok: false } - verify JS can check res.ok in internal/jsruntime/runtime_test.go
- [ ] T033 [P] [US2] Unit test: JS handles error from first tool, continues execution - verify partial results in internal/jsruntime/runtime_test.go
- [ ] T034 [US2] Integration test: code_execution with failing upstream tool in internal/server/mcp_code_execution_test.go

### Implementation for User Story 2

- [ ] T035 [US2] Enhance call_tool() to return { ok: true, result } on success and { ok: false, error: { message, code } } on failure
- [ ] T036 [US2] Map upstream errors to error codes (NOT_FOUND, UPSTREAM_ERROR, TIMEOUT, INVALID_ARGS, SERVER_NOT_ALLOWED)
- [ ] T037 [US2] Enhance error extraction to include line numbers for syntax errors before execution
- [ ] T038 [US2] Add goja.Exception stack trace parsing - extract full call stack with line numbers
- [ ] T039 [US2] Update tool handler error response format to include detailed error.message, error.stack, error.code

**Checkpoint**: Error handling complete - JS code can gracefully handle tool failures and provide clear debugging info

---

## Phase 5: [US3] Execution Limits and Sandboxing (Priority: P1)

**Goal**: Enforce timeout and max_tool_calls limits to prevent resource exhaustion and abuse

**Independent Test**: Submit code exceeding timeout or max_tool_calls, verify execution terminates with appropriate error

### Tests for User Story 3 (TDD - Write First)

- [ ] T040 [P] [US3] Unit test: Timeout enforcement - code running longer than timeout_ms terminates in internal/jsruntime/runtime_test.go
- [ ] T041 [P] [US3] Unit test: max_tool_calls limit - 6th call fails when max_tool_calls=5 in internal/jsruntime/runtime_test.go
- [ ] T042 [P] [US3] Unit test: Sandbox restrictions - attempt require(), fs access fails in internal/jsruntime/runtime_test.go
- [ ] T043 [P] [US3] Unit test: Per-request timeout override - verify custom timeout_ms honored in internal/jsruntime/runtime_test.go

### Implementation for User Story 3

- [ ] T044 [US3] Implement watchdog timeout enforcement - goroutine with time.After() and context cancellation
- [ ] T045 [US3] Add timeout error handling - detect context cancellation, return TIMEOUT error with appropriate message
- [ ] T046 [US3] Implement max_tool_calls tracking - counter in execution context, increment on each call_tool(), enforce limit
- [ ] T047 [US3] Add MAX_TOOL_CALLS_EXCEEDED error when limit exceeded in call_tool() implementation
- [ ] T048 [US3] Verify sandbox restrictions - test that require(), filesystem, network APIs are undefined/blocked
- [ ] T049 [US3] Add per-request option parsing - extract timeout_ms, max_tool_calls, allowed_servers from options
- [ ] T050 [US3] Merge global config defaults with per-request overrides - apply precedence (request > config > hardcoded defaults)

**Checkpoint**: Execution limits enforced - system protected from runaway code and resource exhaustion

---

## Phase 6: [US8] Input Data Passing and Result Extraction (Priority: P1)

**Goal**: Enable passing structured input data to JS and extracting structured results with JSON serialization validation

**Independent Test**: Submit request with nested input object and verify JS can access all fields; return complex object and verify structure preserved

### Tests for User Story 8 (TDD - Write First)

- [ ] T051 [P] [US8] Unit test: Complex input data (nested objects, arrays) accessible via `input` global in internal/jsruntime/runtime_test.go
- [ ] T052 [P] [US8] Unit test: Complex result serialization (nested objects, arrays) in internal/jsruntime/runtime_test.go
- [ ] T053 [P] [US8] Unit test: Non-serializable result (function, circular ref) returns SERIALIZATION_ERROR in internal/jsruntime/runtime_test.go

### Implementation for User Story 8

- [ ] T054 [US8] Enhance input binding to support deeply nested objects and arrays - use goja's ToValue() conversion
- [ ] T055 [US8] Add result serialization validation - detect functions, circular references before JSON marshaling
- [ ] T056 [US8] Return SERIALIZATION_ERROR with clear message when result contains non-JSON types
- [ ] T057 [US8] Add integration test verifying end-to-end input → JS → result flow with complex data structures

**Checkpoint**: MVP COMPLETE - All P1 user stories implemented (US1, US2, US3, US8). Core code_execution functionality ready for testing

---

## Phase 7: [US4] Parallel Execution for Multiple Clients (Priority: P2)

**Goal**: Support concurrent executions via runtime pool without blocking

**Independent Test**: Submit 10 concurrent requests, verify all execute without blocking (assuming pool_size >= 10)

### Tests for User Story 4 (TDD - Write First)

- [ ] T058 [P] [US4] Unit test: Pool Acquire() returns instance in internal/jsruntime/pool_test.go
- [ ] T059 [P] [US4] Unit test: Pool Release() returns instance to pool in internal/jsruntime/pool_test.go
- [ ] T060 [P] [US4] Unit test: Pool Acquire() blocks when all instances in use in internal/jsruntime/pool_test.go
- [ ] T061 [P] [US4] Unit test: Pool Resize() adds/removes instances in internal/jsruntime/pool_test.go
- [ ] T062 [US4] E2E test: 10 concurrent executions complete successfully in tests/e2e/code_execution_test.go

### Implementation for User Story 4

- [ ] T063 [US4] Create internal/jsruntime/pool.go with Pool struct and channels for available instances
- [ ] T064 [US4] Implement NewPool(size) constructor - pre-allocate goja.Runtime instances
- [ ] T065 [US4] Implement Acquire(ctx) method - channel receive with context timeout support
- [ ] T066 [US4] Implement Release(instance) method - channel send to return instance to pool
- [ ] T067 [US4] Implement Resize(new_size) for hot config reload - add/remove instances safely
- [ ] T068 [US4] Integrate pool with tool handler - Acquire() before Execute(), defer Release()
- [ ] T069 [US4] Initialize pool in server startup with config.CodeExecutionPoolSize
- [ ] T070 [US4] Add pool shutdown logic - gracefully close all instances on server shutdown

**Checkpoint**: Concurrent execution supported - multiple clients can execute code simultaneously

---

## Phase 8: [US5] Configuration and Feature Toggle (Priority: P2)

**Goal**: Make code_execution availability controllable via config, default to disabled

**Independent Test**: Verify with enable_code_execution=false, tool not in list and requests rejected; with true, tool available

### Tests for User Story 5

- [ ] T071 [P] [US5] Integration test: enable_code_execution=false excludes tool from tools/list in internal/server/mcp_test.go
- [ ] T072 [P] [US5] Integration test: enable_code_execution=false rejects code_execution requests in internal/server/mcp_test.go
- [ ] T073 [P] [US5] Integration test: enable_code_execution=true includes tool in tools/list in internal/server/mcp_test.go
- [ ] T074 [US5] Integration test: Config defaults apply when options omitted in internal/server/mcp_code_execution_test.go

### Implementation for User Story 5

- [ ] T075 [US5] Verify config defaults: EnableCodeExecution=false, CodeExecutionTimeoutMs=120000, CodeExecutionMaxToolCalls=0, CodeExecutionPoolSize=10
- [ ] T076 [US5] Add feature toggle check in listTools() - only include code_execution if EnableCodeExecution=true
- [ ] T077 [US5] Add feature toggle check in tool handler - return error if disabled
- [ ] T078 [US5] Test hot config reload - verify pool resize and timeout updates apply without restart

**Checkpoint**: Feature toggle working - safe default (disabled) with opt-in activation

---

## Phase 9: [US6] Tool Call History and Observability (Priority: P2)

**Goal**: Log each execution with unique execution_id, tool calls, timing, and outcome

**Independent Test**: Execute code calling multiple tools, verify logs include execution_id, tool names, durations, status

### Tests for User Story 6

- [ ] T079 [US6] Unit test: Execution context generates unique execution_id (UUID) in internal/jsruntime/runtime_test.go
- [ ] T080 [US6] Integration test: Verify logs contain execution_id, truncated_code, tools_called, duration_ms in internal/server/mcp_code_execution_test.go

### Implementation for User Story 6

- [ ] T081 [US6] Add execution_id generation (UUID) at start of Execute()
- [ ] T082 [US6] Create Execution Context struct with execution_id, start_time, end_time, status, tool_calls
- [ ] T083 [US6] Create Tool Call Record struct with server_name, tool_name, arguments, start_time, duration_ms, success, result/error
- [ ] T084 [US6] Track all call_tool() invocations - append Tool Call Records to Execution Context
- [ ] T085 [US6] Add execution logging - write to main.log with format `[code_execution] execution_id=<uuid> status=<status> duration=<ms> tools_called=<list>`
- [ ] T086 [US6] Truncate logged code to 500 chars to prevent log flooding
- [ ] T087 [US6] Include execution_id in response payload for client-side correlation

**Checkpoint**: Full observability - all executions traceable via logs with execution_id correlation

---

## Phase 10: [US7] LLM-Friendly Tool Description and Examples (Priority: P2)

**Goal**: Provide clear guidance in tool schema on when to use code_execution vs direct tools

**Independent Test**: Review tool description with LLM, verify it makes appropriate decisions (multi-tool workflows use code_execution, single operations use direct tools)

### Implementation for User Story 7

- [ ] T088 [US7] Update code_execution tool description in internal/server/mcp.go with "When to use" and "When NOT to use" sections
- [ ] T089 [US7] Add inline examples in tool schema showing basic tool composition pattern
- [ ] T090 [US7] Add error handling examples in tool description - demonstrate checking res.ok
- [ ] T091 [US7] Include guidance on input/output structure in tool description

**Checkpoint**: Tool description guides LLM behavior - appropriate usage patterns encouraged

---

## Phase 11: [US9] CLI Testing Interface (Priority: P2)

**Goal**: Enable developers to test code_execution via CLI without MCP client setup

**Independent Test**: Run `mcpproxy code exec --code="<js>" --input='<json>'` and verify output matches expected format

### Tests for User Story 9 (TDD - Write First)

- [ ] T092 [P] [US9] CLI test: Inline code execution with --code flag in cmd/mcpproxy/code_cmd_test.go
- [ ] T093 [P] [US9] CLI test: File-based execution with --file flag in cmd/mcpproxy/code_cmd_test.go
- [ ] T094 [P] [US9] CLI test: Input via --input and --input-file in cmd/mcpproxy/code_cmd_test.go
- [ ] T095 [P] [US9] CLI test: Error handling - syntax error returns non-zero exit code in cmd/mcpproxy/code_cmd_test.go
- [ ] T096 [P] [US9] CLI test: Timeout handling - code exceeding timeout in cmd/mcpproxy/code_cmd_test.go

### Implementation for User Story 9

- [ ] T097 [US9] Create cmd/mcpproxy/code_cmd.go with Cobra command definition for `mcpproxy code exec`
- [ ] T098 [US9] Add flags: --code, --file, --input, --input-file, --timeout, --max-tool-calls, --allowed-servers
- [ ] T099 [US9] Implement code loading from file if --file provided, validate mutually exclusive with --code
- [ ] T100 [US9] Implement input loading from file if --input-file provided, validate mutually exclusive with --input
- [ ] T101 [US9] Parse input JSON from flag value or file
- [ ] T102 [US9] Call code_execution internally (not via HTTP) - use jsruntime.Execute() directly
- [ ] T103 [US9] Format output as JSON matching MCP response structure { ok, value, error }
- [ ] T104 [US9] Handle exit codes - 0 for success, 1 for errors
- [ ] T105 [US9] Add CLI help text with usage examples and flag descriptions

**Checkpoint**: CLI interface complete - developers can test code_execution without MCP client

---

## Phase 12: [US10] Documentation and Examples (Priority: P2)

**Goal**: Provide comprehensive documentation with 5+ working examples

**Independent Test**: Review docs completeness against checklist; execute all examples to verify they work

### Implementation for User Story 10

- [ ] T106 [P] [US10] Create docs/code_execution/overview.md - feature description, when to use, security model
- [ ] T107 [P] [US10] Create docs/code_execution/examples.md with 5+ working examples:
  - Example 1: Basic tool composition (fetch → transform → send)
  - Example 2: Error handling with partial results
  - Example 3: Loop with filtering (batch operations)
  - Example 4: Conditional logic (if/else based on tool results)
  - Example 5: Data aggregation from multiple sources
- [ ] T108 [P] [US10] Create docs/code_execution/api-reference.md - complete schema for input, output, options, error codes
- [ ] T109 [P] [US10] Create docs/code_execution/troubleshooting.md - common errors and solutions:
  - Timeout errors
  - max_tool_calls exceeded
  - Sandbox restrictions (require, fs, net)
  - JSON serialization errors
  - Tool discovery issues
- [ ] T110 [US10] Update CLAUDE.md with code_execution architecture overview
- [ ] T111 [US10] Update CLAUDE.md with CLI command examples (`mcpproxy code exec`)
- [ ] T112 [US10] Validate all documentation examples execute successfully
- [ ] T113 [US10] Add docstrings to all public functions in internal/jsruntime package

**Checkpoint**: Documentation complete - 90%+ of developers should be able to use code_execution after reading docs (SC-014)

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] T114 [P] Security hardening: Verify sandbox prevents all restricted APIs (require, fs, net, process, etc.)
- [ ] T115 [P] Security test: Attempt to access environment variables - verify blocked
- [ ] T116 [P] Security test: Attempt to execute OS commands - verify blocked
- [ ] T117 [P] Integration with quarantine: Verify call_tool() respects upstream server quarantine status
- [ ] T118 [P] Integration with allow/deny lists: Verify allowed_servers option honored
- [ ] T119 Code cleanup: Remove debug logging, ensure all error paths covered
- [ ] T120 Code cleanup: Run golangci-lint and fix all warnings
- [ ] T121 Performance testing: Verify SC-001 (<30s latency for 3+ tool composition excluding upstream)
- [ ] T122 Performance testing: Verify SC-002 (10 concurrent requests without blocking)
- [ ] T123 Performance testing: Verify SC-010 (50+ concurrent requests with graceful queueing)
- [ ] T124 Performance testing: Verify SC-011 (CLI response <10s)
- [ ] T125 [P] Update README if user-facing commands changed
- [ ] T126 Run full test suite: `./scripts/run-all-tests.sh`
- [ ] T127 Validate quickstart.md - follow all steps and verify they work

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-12)**: All depend on Foundational phase completion
  - P1 stories (US1, US2, US3, US8) should complete before P2 stories
  - P2 stories (US4-US7, US9-US10) can proceed in parallel after P1 complete
- **Polish (Phase 13)**: Depends on all desired user stories being complete

### User Story Dependencies

**P1 Stories (MVP) - Complete in order**:
- **US1 (Phase 3)**: Basic Multi-Tool Orchestration - FOUNDATIONAL for all other stories
- **US2 (Phase 4)**: Error Handling - depends on US1 call_tool() implementation
- **US3 (Phase 5)**: Execution Limits - depends on US1 Execute() function
- **US8 (Phase 6)**: Input/Output - depends on US1 runtime setup

**P2 Stories - Can start after MVP complete**:
- **US4 (Phase 7)**: Parallel Execution - depends on P1 complete (wraps Execute() with pool)
- **US5 (Phase 8)**: Configuration Toggle - depends on P1 complete (guards tool registration)
- **US6 (Phase 9)**: Observability - depends on P1 complete (adds logging to Execute())
- **US7 (Phase 10)**: LLM Guidance - depends on P1 complete (enhances tool description)
- **US9 (Phase 11)**: CLI Interface - depends on P1 complete (calls Execute() directly)
- **US10 (Phase 12)**: Documentation - depends on all features complete (documents everything)

### Within Each User Story

- Tests (if included) MUST be written and FAIL before implementation
- Error types (errors.go) before runtime (runtime.go)
- Runtime before pool (pool.go)
- Pool before tool handler (mcp_code_execution.go)
- Tool handler before tool registration (mcp.go)

### Parallel Opportunities

**Phase 1 - All parallel**:
- T001, T002, T003, T004 (different directories)

**Phase 2 - Research tasks parallel, then config**:
- T005-T012 can all run in parallel (research documentation)
- T013 (errors.go)
- T014-T016 (config changes) depend on T013

**Phase 3 (US1) - Tests parallel, then models, then implementation**:
- T017-T020 (tests) can run in parallel
- T021-T022 (runtime.go basics) sequential
- T023-T026 (feature additions to runtime.go) can partially overlap if separate functions
- T027-T030 (tool handler and registration) sequential after runtime complete

**Phase 4-12 (Other user stories)**:
- All P2 stories can be worked on in parallel by different developers after P1 complete
- Within each story, tests can be written in parallel

**Phase 13 - Most tasks parallel**:
- T114-T120 can run in parallel (different concerns)
- T121-T124 performance tests can run in parallel
- T125-T127 final validation sequential

---

## Parallel Example: Foundational Phase Research

```bash
# Launch all research tasks together (different doc sections):
Task T005: "Research: Goja engine evaluation"
Task T006: "Research: Alternative engines comparison"
Task T007: "Research: Pool sizing strategy"
Task T008: "Research: Timeout enforcement approach"
Task T009: "Research: Error serialization strategy"
Task T010: "Research: Logging integration"
Task T011: "Research: CLI command design patterns"
Task T012: "Research: Documentation best practices"
```

---

## Parallel Example: User Story 1 Tests

```bash
# Launch all US1 tests together (TDD - write before implementation):
Task T017: "Unit test: Execute simple JS returning value"
Task T018: "Unit test: Execute JS calling mock call_tool() once"
Task T019: "Unit test: Execute JS with loop calling call_tool() multiple times"
Task T020: "Integration test: code_execution tool with two mock upstream servers"
```

---

## Implementation Strategy

### MVP First (P1 Stories Only)

1. Complete Phase 1: Setup (T001-T004)
2. Complete Phase 2: Foundational (T005-T016) - CRITICAL
3. Complete Phase 3: US1 Basic Orchestration (T017-T030)
4. Complete Phase 4: US2 Error Handling (T031-T039)
5. Complete Phase 5: US3 Execution Limits (T040-T050)
6. Complete Phase 6: US8 Input/Output (T051-T057)
7. **STOP and VALIDATE**: Test all P1 stories independently
8. Run security tests (T114-T118)
9. **MVP READY** - Basic code_execution functional

### Incremental Delivery

1. MVP (P1 complete) → Test independently → Internal demo
2. Add US4 Parallel Execution → Test concurrency → Deploy to staging
3. Add US5 Configuration Toggle → Verify safe defaults → Production flag
4. Add US6 Observability → Monitor production usage
5. Add US7 LLM Guidance → Improve LLM behavior
6. Add US9 CLI Interface → Developer productivity boost
7. Add US10 Documentation → External documentation release
8. Phase 13 Polish → Production hardening
9. **FULL RELEASE**

### Parallel Team Strategy

With multiple developers after P1 complete:

1. **Team completes P1 together** (critical path)
2. **Split P2 stories** after MVP validated:
   - Developer A: US4 (Pool) + US5 (Config)
   - Developer B: US6 (Logs) + US7 (Docs)
   - Developer C: US9 (CLI) + US10 (Docs)
3. **Converge for Phase 13** (polish and validation)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- MVP = P1 stories complete (US1, US2, US3, US8) = ~57 tasks (T001-T057)
- Full feature = All phases complete = 127 tasks
- Verify tests fail before implementing (TDD)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Follow constitution constraints: actor-based pool, config-driven, security-first
