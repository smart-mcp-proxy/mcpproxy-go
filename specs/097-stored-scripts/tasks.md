# Tasks: Server-Side Stored Scripts for Code Execution

**Input**: Design documents from `/specs/097-stored-scripts/`
**Prerequisites**: plan.md, research.md, data-model.md, contracts/stored-scripts-api.md, quickstart.md
**Convention**: TDD per constitution — tests first, observed failing.

## Phase 1: Setup

No setup tasks — existing project, no new dependencies.

## Phase 2: Foundational

- [x] T001 Create internal/codescripts package: ValidateName (token rules, no fs — the SC-003 boundary), Resolve per plan step 1 (both-extension Lstat probe, ambiguity, Unix O_NOFOLLOW atomic open / Windows Lstat best-effort split via _unix/_windows files or runtime.GOOS, LimitReader(256KB+1) rejecting the extra byte, one read), List (Entry{Name, Paths, Status ok|ambiguous|invalid, Reason}), DeriveLanguage (.ts→typescript, .js→javascript, explicit-contradiction error); typed errors NotFound{Available ≤20 alphabetical, Total}, Ambiguous{Paths}, Invalid{Reason}, InvalidName. TDD in internal/codescripts/codescripts_test.go: traversal corpus (relative traversal, absolute, separators, dots, Unicode, >64 chars, empty) proving InvalidName BEFORE any fs call (validator is pure — table test), resolve/list/ambiguity/empty/oversize/unreadable cases, freshness (atomic rename picked up next Resolve), symlink cases attempted on every platform and skipped only on symlink-creation privilege failure (Windows reparse-point case included).
- [x] T002 Explicit config-path authority: add configFilePath to MCPProxyServer construction (daemon: from runtime config service via existing GetConfigPath plumbing; CLI in-process: new shared codeConfigFilePath() helper in cmd/mcpproxy/code_cmd.go honoring --config, defaulting ~/.mcpproxy/mcp_config.json); scripts dir = Dir(configFilePath)/scripts with config.GetConfigPath(cfg.DataDir) as documented last-resort fallback. Failing-first tests: helper unit test (default + --config override); server-side test that the handler sees the constructed path.

## Phase 3: US1 — Invoke a stored workflow by name (P1)

- [x] T003 [US1] Failing tests in internal/server (code_execution_options_test.go or new file): script XOR code (both→error, neither→error, exact messages), script resolves from a t.TempDir scripts dir and executes identically to inline (same stub ToolCaller, same result), not-found error lists first 20 ok names + total count, language contradiction rejected, .ts script transpiles (language derived), activity/history args carry script name AND resolved source as code.
- [x] T004 [US1] Implement the handler seam in internal/server/mcp_code_execution.go: hoist script/code parse above the current RequireString, resolve via codescripts using the T002 authority, contradiction check at the effectiveLanguage site, additive "script" key in codeExecRecord.Arguments and codeExecArgs. Make T003 green.
- [x] T005 [US1] Registrations: shared codeExecutionScriptDescription constant in mcp_code_execution.go; optional script param + code no-longer-Required in internal/server/mcp.go and mcp_routing.go live site; disabled stub gains optional script/code-not-required but keeps only the disabled description; failing-first surface test asserting all three schemas.

## Phase 4: US2 — CLI invocation (P2)

- [x] T006 [US2] Failing tests: cmd/mcpproxy/code_cmd_test.go child re-exec — --script over daemon mode (httptest server asserts request body carries script and NOT content), 3-way mutual-exclusion rejections (--script+--code, --script+--file), --language only sent when Flags().Changed; internal/cliclient test for CodeExecOptions.Script body field.
- [x] T007 [US2] Implement: --script flag + exclusion in runCodeExec, CodeExecOptions.Script in internal/cliclient/client.go, standalone mode passes the name through the in-process handler (no CLI-side resolution), ping-failure fallback passes the same name. Make T006 green.

## Phase 5: US3 — Discovery (P2)

- [x] T008 [US3] Failing tests: internal/httpapi/code_scripts_test.go — GET /api/v1/code/scripts returns {success,data:{scripts:[{name,paths,status,reason?}],dir}} incl. ambiguous+invalid entries, auth inherited (401 without key), empty-dir empty list; REST POST /code/exec XOR → 400 {ok:false,error:{code:"INVALID_REQUEST"}} in internal/httpapi/code_exec_test.go.
- [x] T009 [US3] Implement internal/httpapi/code_scripts.go (handleListScripts using s.controller.GetConfigPath(), swagger godoc per handleGetDockerStatus pattern) + route beside /code/exec; Script field + XOR 400 in code_exec.go. Make T008 green.
- [x] T010 [US3] CLI listing: `mcpproxy code scripts list` (cobra codeScriptsCmd + list child) — daemon GET when running else local codescripts.List, -o json|yaml via existing formatter; failing-first child re-exec test for the daemon path and a direct test for the local path.

## Phase 6: US4 — Freshness (P3)

- [x] T011 [US4] Failing test at the handler level: invoke stored script, atomically replace the file (write temp + rename), invoke again → new content executes; add/remove file reflected in next listing. (Package-level freshness already pinned in T001; this pins the end-to-end path.) Implementation should already satisfy — fix if not; no test weakening.

## Phase 7: Polish

- [x] T012 [P] `make swagger` regen (new endpoint + request field); verify swagger-verify clean. (`GET /api/v1/code/scripts` is now in `oas/swagger.yaml` + `oas/docs.go`; regen is idempotent, so `swagger-verify` passes once the artifacts are committed. The `script` request field does NOT appear because `POST /api/v1/code/exec` has never carried swagger annotations — pre-existing gap, `CodeExecRequest` is unreferenced by any `@Router`/`@Param`; annotating that handler is out of this task's oas-only scope.)
- [x] T013 [P] Docs: stored-scripts section (authoring, naming rules, atomic-replace freshness, discovery, 256KB bound, no-write-path) in docs/features/code-execution.md, docs/code_execution/{overview,api-reference,cookbook,troubleshooting}.md, docs/configuration.md pointer; quickstart example from specs/097-stored-scripts/quickstart.md.
- [ ] T014 Full verification: both edition builds; go test -race -count=1 ./internal/... ; server-edition tags; lint v2 (.github/.golangci.yml); ./scripts/test-api-e2e.sh (revert e2e-config churn); optional smoke: REST exec of a stored script against the e2e instance.

## Dependencies

T001→T002 (package before authority wiring) → US1 (T003–T005) → US2/US3 in parallel (different files: cmd+cliclient vs httpapi) → US4 → Polish (T012/T013 parallel; T014 last).

## Implementation Strategy

MVP = T001–T005 (stored scripts invocable over MCP). Single engine agent for T001–T005; a second agent for T006–T011 (CLI+REST+freshness) after; polish agents parallel. Orchestrator runs T014.
