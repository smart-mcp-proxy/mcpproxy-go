# Feature Specification: TypeScript Code Execution Support

**Feature Branch**: `033-typescript-code-execution`
**Created**: 2026-03-10
**Status**: Draft
**Input**: User description: "Add TypeScript language support to MCPProxy's code_execution feature. When users submit TypeScript code (detected by .ts file hints, explicit language parameter, or TypeScript-specific syntax like type annotations), automatically transpile it to JavaScript using esbuild's Go API before executing in the goja sandbox. The transpilation should be transparent - users write TypeScript, it gets stripped to JS and executed. Add a language parameter to the code_execution MCP tool schema (values: 'javascript', 'typescript', default: 'javascript'). When language is 'typescript' or auto-detected, run esbuild transform first. Performance target: less than 5ms transpilation overhead."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Execute TypeScript Code via MCP Tool (Priority: P1)

A developer using an AI agent (e.g., Claude, Cursor) wants to write code_execution scripts using TypeScript syntax for type safety and readability. They submit TypeScript code with type annotations, interfaces, and typed parameters through the `code_execution` MCP tool, specifying `language: "typescript"`. The system transparently strips the types and executes the resulting JavaScript in the existing sandbox, returning the same result format as plain JavaScript execution.

**Why this priority**: This is the core value proposition. Without the ability to execute TypeScript through the MCP tool interface, no other feature in this spec delivers value. This is the primary interface used by AI agents.

**Independent Test**: Can be fully tested by sending a `code_execution` MCP tool call with `language: "typescript"` and TypeScript code containing type annotations, and verifying the result matches expected output.

**Acceptance Scenarios**:

1. **Given** code_execution is enabled, **When** a user submits TypeScript code with `language: "typescript"` (e.g., `const x: number = 42; ({ result: x })`), **Then** the system strips type annotations, executes the resulting JavaScript, and returns `{ ok: true, value: { result: 42 } }`.
2. **Given** code_execution is enabled, **When** a user submits TypeScript code with interfaces and typed function parameters, **Then** the types are stripped and the logic executes correctly.
3. **Given** code_execution is enabled, **When** a user submits TypeScript code with syntax errors in the type annotations, **Then** the system returns a clear error indicating the transpilation failed with the specific error location.
4. **Given** code_execution is enabled, **When** a user submits `language: "javascript"` (or omits the language parameter), **Then** the code executes exactly as before with no transpilation step (backward compatible).

---

### User Story 2 - Execute TypeScript Code via REST API (Priority: P2)

A developer or automation tool uses the REST API endpoint (`POST /api/v1/code/exec`) to execute TypeScript code. They include a `language` field in the request body set to `"typescript"`. The system transpiles and executes the code, returning results in the same response format.

**Why this priority**: The REST API is the secondary interface for code execution, used by integrations and the CLI. It must support the same language parameter as the MCP tool.

**Independent Test**: Can be fully tested by sending a POST request to `/api/v1/code/exec` with `language: "typescript"` and TypeScript code, verifying the JSON response.

**Acceptance Scenarios**:

1. **Given** the REST API is available, **When** a user sends a POST to `/api/v1/code/exec` with `{ "code": "const x: number = 42; ({ result: x })", "language": "typescript" }`, **Then** the response contains `{ "ok": true, "result": { "result": 42 } }`.
2. **Given** the REST API is available, **When** a user sends a request without the `language` field, **Then** the system defaults to JavaScript execution (backward compatible).

---

### User Story 3 - Execute TypeScript Code via CLI (Priority: P3)

A developer uses the CLI command `mcpproxy code exec` to run TypeScript code locally, specifying the language via a `--language` flag. This is useful for testing and debugging code_execution scripts.

**Why this priority**: The CLI is a convenience interface primarily for local development and debugging. It adds value but is not the primary use case.

**Independent Test**: Can be fully tested by running `mcpproxy code exec --language typescript --code "const x: number = 42; ({ result: x })"` and checking the output.

**Acceptance Scenarios**:

1. **Given** the CLI is available, **When** a user runs `mcpproxy code exec --language typescript --code "..."`, **Then** the TypeScript code is transpiled and executed, with results printed to stdout.
2. **Given** the CLI is available, **When** a user omits the `--language` flag, **Then** the code is treated as JavaScript (backward compatible).

---

### Edge Cases

- What happens when TypeScript code uses features not supported by the sandbox runtime (e.g., `async/await`, ES modules)? The transpiler strips types but the resulting JS must still be valid for the ES5.1+ sandbox. Unsupported JS features should produce a clear runtime error from the sandbox, not the transpiler.
- What happens when the transpilation itself takes longer than expected? The transpilation time counts toward the overall execution timeout, ensuring no separate timeout mechanism is needed.
- What happens when code contains no TypeScript-specific syntax but `language: "typescript"` is specified? The transpiler should handle it gracefully - valid JavaScript is also valid TypeScript, so transpilation succeeds as a no-op type strip.
- What happens with TypeScript `enum` declarations? Enums produce JavaScript output (not just type stripping), so they should work correctly after transpilation.
- What happens with TypeScript `namespace` declarations? These also produce JavaScript output and should be handled by the transpiler.
- What happens when an invalid `language` value is provided (e.g., `"python"`)? The system should return a clear error indicating the language is not supported, listing valid options.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST accept a `language` parameter in the `code_execution` MCP tool schema with allowed values `"javascript"` and `"typescript"`, defaulting to `"javascript"`.
- **FR-002**: System MUST accept a `language` field in the REST API `POST /api/v1/code/exec` request body with the same allowed values and default.
- **FR-003**: System MUST accept a `--language` flag in the `mcpproxy code exec` CLI command with the same allowed values and default.
- **FR-004**: When `language` is `"typescript"`, the system MUST transpile the submitted code from TypeScript to JavaScript before execution in the sandbox.
- **FR-005**: The transpilation MUST strip all TypeScript-specific syntax (type annotations, interfaces, type aliases, generics, enums, namespaces) and produce valid JavaScript.
- **FR-006**: The transpiled JavaScript MUST be executed in the existing sandbox with all existing capabilities (input variable, call_tool function, timeout enforcement, tool call limits).
- **FR-007**: When `language` is `"javascript"` or not specified, the system MUST execute code exactly as before with no transpilation step, preserving full backward compatibility.
- **FR-008**: Transpilation errors MUST be returned to the user with clear error messages including the error location (line and column number) in the original TypeScript source.
- **FR-009**: The system MUST reject unsupported `language` values with an error message listing the supported languages.
- **FR-010**: The transpilation overhead MUST be less than 5 milliseconds for typical code submissions (under 10KB of source code).
- **FR-011**: The system MUST log the language used and transpilation duration in execution logs for observability.
- **FR-012**: The activity log record for code_execution calls MUST include the language parameter used.

### Key Entities

- **Language Parameter**: A string field added to the code execution request indicating the source language. Values: `"javascript"` (default), `"typescript"`. Affects whether a transpilation step runs before sandbox execution.
- **Transpilation Result**: The output of converting TypeScript to JavaScript, containing either the transpiled JavaScript code or error details (message, line, column).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can submit TypeScript code with type annotations through any interface (MCP tool, REST API, CLI) and receive correct execution results within the same response time expectations as JavaScript (less than 5ms additional overhead for transpilation).
- **SC-002**: 100% backward compatibility - all existing JavaScript code execution requests continue to work identically without any changes to client code or configuration.
- **SC-003**: TypeScript transpilation errors provide actionable feedback including the error location (line and column) in the original source, enabling users to fix issues on first attempt in 90% of cases.
- **SC-004**: The `language` parameter is captured in activity logs, enabling operators to track TypeScript vs JavaScript usage patterns.

## Assumptions

- TypeScript transpilation uses type-stripping only (no type checking or semantic validation). This is intentional for performance - the goal is to allow TypeScript syntax, not to provide a full TypeScript compiler.
- The transpiler targets ES5.1+ output compatible with the existing goja sandbox runtime.
- No new configuration options are needed for enabling/disabling TypeScript support - it is always available when code_execution is enabled.
- The `language` parameter is a simple string enum, not a complex object. Auto-detection of TypeScript syntax is not included in this spec to keep the interface explicit and predictable.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
