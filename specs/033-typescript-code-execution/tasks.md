# Tasks: TypeScript Code Execution Support

**Input**: Design documents from `/specs/033-typescript-code-execution/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Included - the constitution (V. Test-Driven Development) requires tests for all features.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Phase 1: Setup

**Purpose**: Add esbuild dependency and foundational types

- [ ] T001 Add esbuild dependency by running `go get github.com/evanw/esbuild` and verify `go.mod` / `go.sum` are updated
- [ ] T002 Add `ErrorCodeTranspileError` error code constant to `internal/jsruntime/errors.go`
- [ ] T003 Add `Language` field (string, default "javascript") to `ExecutionOptions` struct in `internal/jsruntime/runtime.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core transpilation layer that ALL user stories depend on

- [ ] T004 Create `internal/jsruntime/typescript.go` with `TranspileTypeScript(code string) (string, error)` function using esbuild `api.Transform()` with `Loader: api.LoaderTS`. Return transpiled JS code on success; return `*JsError` with `ErrorCodeTranspileError` on failure, including line/column from esbuild error messages.
- [ ] T005 Create `internal/jsruntime/typescript_test.go` with unit tests: (a) basic type annotation stripping, (b) interfaces removed, (c) generics removed, (d) enums produce valid JS, (e) namespaces produce valid JS, (f) plain JavaScript passes through unchanged, (g) invalid TypeScript returns error with line/column, (h) empty code input, (i) performance benchmark `BenchmarkTranspileTypeScript` verifying <5ms for 10KB code
- [ ] T006 Modify `Execute()` function in `internal/jsruntime/runtime.go` to check `opts.Language`: if `"typescript"`, call `TranspileTypeScript(code)` before passing to `executeWithVM()`. If transpilation fails, return the transpile error. If `"javascript"` or empty, execute as before (no transpilation). If unsupported language value, return error with code `INVALID_ARGS` listing valid options.
- [ ] T007 Add TypeScript execution tests to `internal/jsruntime/runtime_test.go`: (a) TypeScript code with types executes correctly via `Execute()`, (b) JavaScript code with `Language: "javascript"` works unchanged, (c) empty `Language` defaults to JavaScript, (d) invalid language returns error, (e) TypeScript with `call_tool()` works, (f) TypeScript transpilation error returns `TRANSPILE_ERROR` code, (g) transpilation overhead is logged

**Checkpoint**: Transpilation layer is complete and tested. All user stories can now proceed.

---

## Phase 3: User Story 1 - Execute TypeScript Code via MCP Tool (Priority: P1) MVP

**Goal**: AI agents can submit TypeScript code through the `code_execution` MCP tool with `language: "typescript"` and get correct results.

**Independent Test**: Send a `code_execution` MCP tool call with `language: "typescript"` and TypeScript code containing type annotations; verify the result matches expected output.

### Tests for User Story 1

- [ ] T008 [P] [US1] Add test cases to `internal/server/mcp_code_execution_test.go`: (a) TypeScript code with `language: "typescript"` executes correctly, (b) JavaScript code without language parameter works unchanged, (c) invalid language returns error, (d) TypeScript transpilation error returns proper MCP error format, (e) language parameter is passed through to activity log

### Implementation for User Story 1

- [ ] T009 [US1] Add `language` string parameter to the `code_execution` tool schema in `internal/server/mcp.go` (around line 450) using `mcp.WithString("language", mcp.Description("..."), mcp.Enum("javascript", "typescript"))` with description from contracts/mcp-tool-schema.md. Update the tool description to mention TypeScript support.
- [ ] T010 [US1] Modify `handleCodeExecution()` in `internal/server/mcp_code_execution.go` to: (a) parse `language` from `request.GetArguments()` with default `"javascript"`, (b) set `options.Language` before calling `jsruntime.Execute()`, (c) log the language used alongside existing execution logging, (d) include `language` in the activity record arguments map

**Checkpoint**: TypeScript execution works via MCP tool. AI agents can use `language: "typescript"`.

---

## Phase 4: User Story 2 - Execute TypeScript Code via REST API (Priority: P2)

**Goal**: Developers and automation tools can execute TypeScript code through `POST /api/v1/code/exec` with a `language` field.

**Independent Test**: Send a POST request to `/api/v1/code/exec` with `language: "typescript"` and TypeScript code; verify the JSON response.

### Tests for User Story 2

- [ ] T011 [P] [US2] Add test cases to `internal/httpapi/code_exec_test.go`: (a) TypeScript code with `language: "typescript"` returns successful result, (b) request without `language` field defaults to JavaScript, (c) invalid language returns `INVALID_LANGUAGE` error, (d) TypeScript transpilation error returns proper error response

### Implementation for User Story 2

- [ ] T012 [US2] Add `Language` field (`json:"language"`) to `CodeExecRequest` struct in `internal/httpapi/code_exec.go`
- [ ] T013 [US2] Modify `ServeHTTP()` in `internal/httpapi/code_exec.go` to: (a) pass `req.Language` in the `args` map as `"language"` key when calling `h.toolCaller.CallTool()`, (b) validate language if non-empty (must be "javascript" or "typescript"), returning `INVALID_LANGUAGE` error for unsupported values

**Checkpoint**: TypeScript execution works via REST API. Integrations and automation tools can use `language: "typescript"`.

---

## Phase 5: User Story 3 - Execute TypeScript Code via CLI (Priority: P3)

**Goal**: Developers can run TypeScript code locally via `mcpproxy code exec --language typescript`.

**Independent Test**: Run `mcpproxy code exec --language typescript --code "const x: number = 42; ({ result: x })"` and verify output.

### Implementation for User Story 3

- [ ] T014 [US3] Add `--language` flag (string, default "javascript") to `codeExecCmd` in `cmd/mcpproxy/code_cmd.go` using `codeExecCmd.Flags().StringVar()`. Add validation in `validateOptions()` to reject unsupported language values.
- [ ] T015 [US3] Pass the language flag value in the `args` map in both `runCodeExecStandalone()` and `runCodeExecClientMode()` functions in `cmd/mcpproxy/code_cmd.go`. Update the command description and examples to include TypeScript usage.

**Checkpoint**: TypeScript execution works via CLI. Developers can test TypeScript code locally.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, backward compatibility verification, and build validation

- [ ] T016 [P] Update `docs/code_execution/overview.md` to document TypeScript support: what it is, how to use it, language parameter, limitations (type-stripping only, no type checking)
- [ ] T017 [P] Update `docs/code_execution/api-reference.md` to document the `language` parameter in the MCP tool schema and REST API request body
- [ ] T018 Verify full build succeeds: `go build ./cmd/mcpproxy` (personal edition) and `go build -tags server ./cmd/mcpproxy` (server edition)
- [ ] T019 Run complete test suite: `go test ./internal/jsruntime/... -v -race && go test ./internal/server/... -v -race && go test ./internal/httpapi/... -v -race`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup (Phase 1) completion - BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational (Phase 2) completion
- **User Story 2 (Phase 4)**: Depends on Foundational (Phase 2) completion - can run in parallel with US1
- **User Story 3 (Phase 5)**: Depends on Foundational (Phase 2) completion - can run in parallel with US1/US2
- **Polish (Phase 6)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Phase 2. No dependencies on other stories. **This is the MVP.**
- **User Story 2 (P2)**: Can start after Phase 2. Independent of US1 (uses same foundational transpiler). The REST handler calls the MCP tool internally, so US1 implementation (T009, T010) must be complete first.
- **User Story 3 (P3)**: Can start after Phase 2. Independent of US1/US2 in standalone mode. Client mode relies on daemon having US1/US2 changes, but the CLI flag addition is independent.

### Within Each User Story

- Tests should be written and verified to fail before implementation
- Schema/model changes before handler logic
- Handler logic before integration

### Parallel Opportunities

- T002 and T003 can run in parallel (different files)
- T004 and T005 can be developed together (new file + its tests)
- T008 and T011 can run in parallel (different test files)
- T016 and T017 can run in parallel (different doc files)
- User Stories 1, 2, and 3 can theoretically be developed in parallel after Phase 2 (though US2 depends on US1 for the MCP handler)

---

## Parallel Example: User Story 1

```bash
# Test and implementation can be developed in sequence:
Task T008: "Add TypeScript test cases to internal/server/mcp_code_execution_test.go"
# then
Task T009: "Add language parameter to code_execution tool schema in internal/server/mcp.go"
Task T010: "Modify handleCodeExecution() to parse and pass language parameter"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T007)
3. Complete Phase 3: User Story 1 (T008-T010)
4. **STOP and VALIDATE**: Test TypeScript execution via MCP tool
5. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational -> Transpilation layer ready
2. Add User Story 1 -> TypeScript works via MCP tool (MVP!)
3. Add User Story 2 -> TypeScript works via REST API
4. Add User Story 3 -> TypeScript works via CLI
5. Polish -> Docs updated, full build validated

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable after the foundational phase
- The esbuild dependency adds ~5MB to the binary but provides near-zero transpilation latency
- All tasks preserve backward compatibility - JavaScript execution is never affected
