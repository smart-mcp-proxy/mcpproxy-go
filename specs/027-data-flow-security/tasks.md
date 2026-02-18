# Tasks: Data Flow Security with Agent Hook Integration

**Input**: Design documents from `/specs/027-data-flow-security/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: TDD is mandated by the project constitution. Tests are written FIRST and must FAIL before implementation.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story. User stories are ordered by dependency graph, not strictly by priority label — foundational stories (US2, US8) must come before stories that depend on them (US1, US1b, US4).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2)
- Include exact file paths in descriptions

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create package structure, configuration types, and shared type definitions

- [x] T001 Create `internal/security/flow/` package directory and base types file `internal/security/flow/types.go` with Classification, FlowType, RiskLevel, PolicyAction enums, FlowSession, DataOrigin, FlowEdge, ClassificationResult, PendingCorrelation structs per `contracts/go-types.go`
- [x] T002 [P] Add security configuration types to `internal/config/config.go`: FlowTrackingConfig, ClassificationConfig, FlowPolicyConfig, HooksConfig structs under SecurityConfig, with JSON tags and defaults per `contracts/config-schema.json`
- [x] T003 [P] Add HookEvaluateRequest and HookEvaluateResponse API types to `internal/security/flow/types.go` per `contracts/go-types.go`
- [x] T004 [P] Add `flow.alert` event type constant to `internal/runtime/events.go`

---

## Phase 2: Foundational — US2 Classify Servers and Tools (Priority: P1)

**Purpose**: Classification is the foundation for ALL flow analysis. Without knowing which tools are internal vs external, the system cannot determine flow direction. Every other story depends on this.

**Goal**: Automatically classify upstream MCP servers and agent-internal tools as internal/external/hybrid/unknown using name-based heuristics, with config overrides.

**Independent Test**: Provide server/tool names and verify correct classification output without any data flow.

### Tests for US2 ⚠️

> **Write these tests FIRST, ensure they FAIL before implementation**

- [x] T010 [P] [US2] Write classifier unit tests in `internal/security/flow/classifier_test.go`: table-driven tests for internal tools (Read→internal, Write→internal, Glob→internal, Grep→internal, WebFetch→external, WebSearch→external, Bash→hybrid), server name heuristics (postgres-db→internal, slack-notifications→external, aws-lambda→hybrid, unknown-server→unknown), config overrides (my-private-slack overridden to internal), MCP tool namespacing (mcp__github__get_file looks up github server classification), confidence scores (>= 0.8 for heuristic matches, 1.0 for config overrides), and capability flags (CanReadData, CanExfiltrate)

### Implementation for US2

- [x] T011 [US2] Implement `Classifier` in `internal/security/flow/classifier.go`: NewClassifier(overrides), Classify(serverName, toolName) method, internalToolClassifications map for agent-internal tools, internalPatterns/externalPatterns/hybridPatterns slices for server name heuristics, classifyByName helper, MCP tool namespace extraction (split `mcp__<server>__<tool>` and look up server)

**Checkpoint**: Classification works independently. Can classify any server/tool name. All US2 acceptance scenarios pass.

---

## Phase 3: Foundational — US8 Content Hashing for Flow Detection (Priority: P1)

**Purpose**: Content hashing is the mechanism that makes flow detection work. Without it, the system cannot determine that data has moved between tool calls. All tracking stories depend on this.

**Goal**: Hash tool responses at multiple granularities to detect when data from one tool call appears in another.

**Independent Test**: Hash tool responses and verify matches when the same data appears in subsequent tool arguments.

### Tests for US8 ⚠️

> **Write these tests FIRST, ensure they FAIL before implementation**

- [x] T020 [P] [US8] Write hasher unit tests in `internal/security/flow/hasher_test.go`: HashContent produces 32 hex chars (128 bits), HashContentNormalized matches case-insensitive and trimmed, ExtractFieldHashes extracts per-field hashes for strings >= 20 chars, ExtractFieldHashes skips strings < 20 chars, ExtractFieldHashes handles non-JSON content, normalized hashing catches lightly reformatted data (leading/trailing whitespace, case changes), nested JSON field extraction, array element extraction

### Implementation for US8

- [x] T021 [US8] Implement content hashing in `internal/security/flow/hasher.go`: HashContent (SHA256 truncated to 128 bits), HashContentNormalized (lowercase + trimmed), ExtractFieldHashes (per-field string hashes >= minLength), extractStrings recursive JSON walker

**Checkpoint**: Hashing works independently. Can hash any content, extract field hashes from JSON, detect reformatted matches. All US8 acceptance scenarios pass.

---

## Phase 4: US1 — Detect Exfiltration via MCP Proxy (Priority: P1) 🎯 MVP

**Purpose**: Proxy-only flow detection — works with ANY MCP agent without hooks. This is the universal security layer.

**Goal**: Track data flow at the MCP proxy layer by recording `call_tool_read` response hashes as data origins and checking `call_tool_write`/`call_tool_destructive` arguments against those origins.

**Independent Test**: Send MCP tool call sequences through the proxy and verify flow detection and policy decisions. No hooks needed.

**Dependencies**: Requires US2 (classifier) and US8 (hasher) to be complete.

### Tests for US1 ⚠️

> **Write these tests FIRST, ensure they FAIL before implementation**

- [x] T030 [P] [US1] Write flow tracker unit tests in `internal/security/flow/tracker_test.go`: RecordOrigin stores origins with correct hashes, CheckFlow detects internal→external flows (exfiltration), CheckFlow allows internal→internal flows (safe), CheckFlow allows external→internal flows (safe), CheckFlow with sensitive data escalates to RiskCritical, CheckFlow with no matching origins returns nil, session expiry after timeout, origin eviction when exceeding MaxOriginsPerSession, per-field hash matching (partial data extraction), concurrent session isolation (multiple sessions don't interfere)
- [x] T031 [P] [US1] Write policy evaluator unit tests in `internal/security/flow/policy_test.go`: PolicyAllow for safe flows, PolicyAsk for internal→external without sensitive data in hook_enhanced mode (default), PolicyWarn for internal→external without sensitive data in proxy_only mode (ask degrades to warn), PolicyDeny for sensitive data flowing externally, tool overrides (WebSearch always allow), suspicious endpoint detection (webhook.site always deny), empty edges return PolicyAllow
- [x] T032 [P] [US1] Write FlowService unit tests in `internal/security/flow/service_test.go` for proxy-only mode: full pipeline test — classify→hash→track→policy, proxy-only session keyed by MCP session ID, RecordOriginProxy records from call_tool_read responses, CheckFlowProxy detects exfiltration in call_tool_write arguments, sensitive data detector integration (reuse Spec 026 detector.Scan on response content)

### Implementation for US1

- [x] T033 [US1] Implement `FlowTracker` in `internal/security/flow/tracker.go`: NewFlowTracker(config), RecordOrigin (store content hashes as data origins), CheckFlow (check args against recorded origins, create FlowEdge), getOrCreateSession, getSession, evictOldest, determineFlowType, assessRisk, session expiry goroutine (30min TTL default), per-session sync.RWMutex
- [ ] T033b [US1] Benchmark mutex vs channel pattern for FlowTracker in `internal/security/flow/tracker_bench_test.go`: BenchmarkTrackerMutex and BenchmarkTrackerChannel comparing concurrent RecordOrigin + CheckFlow throughput with 10, 100, 1000 concurrent goroutines. Document results in plan.md Complexity Tracking. Constitution II requires this benchmark before merging. If channel pattern performs within 10% of mutex, refactor to channels.
- [x] T034 [US1] Implement `PolicyEvaluator` in `internal/security/flow/policy.go`: NewPolicyEvaluator(config), Evaluate(edges, mode) returning (PolicyAction, reason string) where mode is "proxy_only" or "hook_enhanced", tool override lookup, suspicious endpoint check, sensitive data escalation, internal_to_external default action from config. When mode is "proxy_only" and computed action is PolicyAsk, degrade to PolicyWarn (no agent-side UI to prompt user — log warning and allow).
- [x] T035 [US1] Implement `FlowService` in `internal/security/flow/service.go`: NewFlowService(classifier, tracker, policy, detector, activity, eventBus), RecordOriginProxy(mcpSessionID, serverName, toolName, responseJSON) — classifies server, scans for sensitive data, hashes response, records origin, CheckFlowProxy(mcpSessionID, serverName, toolName, argsJSON) — hashes args, checks against origins, returns edges, EvaluatePolicy(edges) — delegates to PolicyEvaluator
- [x] T036 [US1] Integrate FlowService into MCP proxy pipeline in `internal/server/mcp.go`: add flowService field to MCPProxyServer, in handleCallToolVariant — after call_tool_read response, call `go flowService.RecordOriginProxy(...)`, before call_tool_write/call_tool_destructive execution, call `flowService.CheckFlowProxy(...)` and enforce policy decision, use MCP session ID for proxy-only mode
- [x] T037 [US1] Wire FlowService creation in application startup: create FlowService with config, inject into MCPProxyServer and HTTP API server, ensure sensitive data detector (Spec 026) is passed to FlowService

**Checkpoint**: Proxy-only exfiltration detection works. MCP tool call sequences through the proxy are tracked. Internal→external flows with sensitive data are blocked. All US1 acceptance scenarios pass. No hooks needed.

---

## Phase 5: US9 — Graceful Degradation Without Hooks (Priority: P1)

**Purpose**: Ensure the system communicates coverage level clearly when no hooks are installed.

**Goal**: Report `security_coverage: "proxy_only"` in status, show recommendations in diagnostics, and prepare nudge infrastructure.

**Independent Test**: Run MCP tool calls through the proxy with hooks disabled; verify proxy-level flow detection works and coverage level is reported correctly.

**Dependencies**: Requires US1 (proxy-only flow tracking) to be complete.

### Tests for US9 ⚠️

- [x] T040 [P] [US9] Write tests for security coverage reporting: `/api/v1/status` response includes `security_coverage: "proxy_only"` when no hooks active, includes `security_coverage: "full"` when hooks active, includes `hooks_active: false/true` boolean
- [x] T041 [P] [US9] Write tests for diagnostics recommendations: `/api/v1/diagnostics` includes Recommendation with ID "install-hooks", category "security", and command `mcpproxy hook install --agent claude-code` when no hooks detected; recommendation absent when hooks are active

### Implementation for US9

- [x] T042 [US9] Add `security_coverage` and `hooks_active` fields to status response in `internal/httpapi/server.go`: query FlowService for active hook session count, return "full" if > 0, "proxy_only" otherwise
- [x] T043 [US9] Add Recommendation type to `internal/contracts/types.go` (ID, Category, Title, Description, Command, Priority fields) and add `Recommendations []Recommendation` to Diagnostics struct
- [x] T044 [US9] Add hook detection and recommendation logic to `internal/management/diagnostics.go`: check FlowService for active hooks, if none detected, append recommendation explaining benefit of hooks with install command
- [x] T045 [US9] Add Security Coverage section to `cmd/mcpproxy/doctor_cmd.go`: display coverage mode (proxy_only/full), if proxy_only show recommendation with install command, if full show active hook count and agent type

**Checkpoint**: Users can see coverage level in status API, diagnostics API, and doctor CLI. Nudge infrastructure is in place. All US9 acceptance scenarios pass.

---

## Phase 6: US4 — Evaluate Tool Calls via Hook Endpoint (Priority: P1)

**Purpose**: The runtime evaluation path — the mechanism through which hook-enhanced security decisions flow.

**Goal**: Expose POST /api/v1/hooks/evaluate endpoint and mcpproxy hook evaluate CLI command.

**Independent Test**: Send JSON payload to the endpoint and verify response structure and decision.

**Dependencies**: Requires US2 (classifier), US8 (hasher), and US1 (FlowService) to be complete.

### Tests for US4 ⚠️

- [x] T050 [P] [US4] Write hook evaluate HTTP handler tests in `internal/httpapi/hooks_test.go`: PreToolUse for Read returns allow (reading is always allowed), PostToolUse for Read records origins and returns allow, PreToolUse for WebFetch with matching content hash returns deny, malformed JSON returns 400, missing required fields return 400, response includes activity_id, evaluation completes within 100ms
- [x] T051 [P] [US4] Write hook evaluate CLI tests in `cmd/mcpproxy/hook_cmd_test.go`: reads JSON from stdin and outputs Claude Code protocol response, fail-open when daemon unreachable (exit 0, decision: approve), maps internal "deny" to Claude Code "block", maps internal "ask" to Claude Code "ask", maps internal "allow" to Claude Code "approve"

### Implementation for US4

- [x] T052 [US4] Implement hook evaluate HTTP handler in `internal/httpapi/hooks.go`: HandleHookEvaluate function, parse HookEvaluateRequest from body, delegate to FlowService.Evaluate(), return HookEvaluateResponse as JSON, log to activity service as hook_evaluation record with metadata including coverage_mode ("full" since hooks are active for this evaluation) per FR-029
- [x] T053 [US4] Register POST /api/v1/hooks/evaluate route in `internal/httpapi/server.go`: add route in setupRoutes() within the authenticated API group
- [x] T054 [US4] Implement FlowService.Evaluate(req) skeleton in `internal/security/flow/service.go`: switch on req.Event, dispatch to evaluatePreToolUse() and processPostToolUse() methods. PreToolUse: classify tool, delegate to tracker.CheckFlow() + policy.Evaluate(), return decision. PostToolUse: return PolicyAllow immediately (origin recording deferred to T061). Register pending correlation for mcp__mcpproxy__* tools. Log evaluation to activity service. Emit flow.alert for high/critical PreToolUse decisions. NOTE: The actual PostToolUse hashing/recording and PreToolUse flow detection logic is implemented in T061/T062 — this task wires up the dispatch, classification, and response formatting only.
- [x] T055 [US4] Implement `mcpproxy hook evaluate` CLI command in `cmd/mcpproxy/hook_cmd.go`: hookCmd parent, hookEvaluateCmd with --event flag, fast startup path (no config loading, no file logger), read JSON from stdin, detect socket path, POST to daemon via Unix socket HTTP, translate response to Claude Code hook protocol (approve/block/ask), fail-open on any error (return approve)
- [x] T056 [US4] Implement Unix socket HTTP client helper in `cmd/mcpproxy/hook_cmd.go`: detectSocketPath() using standard `~/.mcpproxy/mcpproxy.sock` path, postToSocket(path, endpoint, body) using net.Dial("unix", ...) + http.NewRequest

**Checkpoint**: Hook evaluate endpoint works. CLI reads from stdin, communicates via socket, returns Claude Code protocol response. Fail-open verified. All US4 acceptance scenarios pass.

---

## Phase 7: US1b — Detect Exfiltration with Hook Enhancement (Priority: P1)

**Purpose**: Full visibility — catching exfiltration patterns that proxy-only mode cannot see (agent-internal tool chains like Read→WebFetch).

**Goal**: Process hook events to detect internal→external flows across agent-internal tools.

**Independent Test**: Replay deterministic hook event sequences (PostToolUse for Read → PreToolUse for WebFetch with matching content) and verify deny decision.

**Dependencies**: Requires US4 (hook endpoint) to be complete.

### Tests for US1b ⚠️

- [x] T060 [P] [US1b] Write hook-enhanced flow detection tests in `internal/security/flow/service_test.go`: PostToolUse for Read with AWS secret key → PreToolUse for WebFetch with same key → deny decision with risk critical, PostToolUse for Read with DB connection string → PreToolUse for Bash with curl containing string → deny decision, Read→Write (internal→internal) → allow with risk none, hook session isolation (two sessions don't cross-contaminate flows), PostToolUse only records origins (never denies), per-field matching (extract field from JSON response, detect in WebFetch URL)

### Implementation for US1b

- [x] T061 [US1b] Implement processPostToolUse() body in `internal/security/flow/service.go` (skeleton created in T054): parse tool_response from request, call detector.Scan() for sensitive data detection, call classifier.Classify() for source tool, hash response with multi-granularity (full content + per-field via ExtractFieldHashes), record all content hashes as DataOrigins in the flow session tagged with classification and sensitive data flags
- [x] T062 [US1b] Implement evaluatePreToolUse() flow detection body in `internal/security/flow/service.go` (skeleton created in T054): marshal tool_input to JSON for hash matching, classify destination tool, call tracker.CheckFlow() to match argument hashes against session origins, evaluate detected FlowEdges via policy.Evaluate(), return deny/ask/allow decision. T054 already handles classification and response formatting — this task adds the origin-matching and edge-detection logic.

**Checkpoint**: Hook-enhanced exfiltration detection works. Read→WebFetch with sensitive data is blocked. Read→Write (internal→internal) is allowed. All US1b acceptance scenarios pass.

---

## Phase 8: US3 — Install Hook Integration via CLI (Priority: P2)

**Purpose**: Make hook installation a one-command operation for Claude Code users.

**Goal**: `mcpproxy hook install --agent claude-code --scope project` generates and writes hook configuration.

**Independent Test**: Run install command and verify generated settings file.

**Dependencies**: Requires US4 (hook evaluate CLI) to exist.

### Tests for US3 ⚠️

- [x] T070 [P] [US3] Write hook install/uninstall/status CLI tests in `cmd/mcpproxy/hook_cmd_test.go`: install --agent claude-code --scope project creates `.claude/settings.json` with correct PreToolUse and PostToolUse hooks, PreToolUse matcher includes `Read|Glob|Grep|Bash|Write|Edit|WebFetch|WebSearch|Task|mcp__.*`, PostToolUse has `async: true`, no API keys or port numbers in generated config, hooks point to `mcpproxy hook evaluate --event <event>`, uninstall removes hook entries from settings, status shows installed/not-installed state and daemon reachability, install with existing settings merges (doesn't overwrite other settings)

### Implementation for US3

- [x] T071 [US3] Implement `mcpproxy hook install` in `cmd/mcpproxy/hook_cmd.go`: --agent flag (required, validate: "claude-code"), --scope flag (default: "project", options: "project"/"user"), for claude-code: read/create `.claude/settings.json`, merge hook configuration (PreToolUse with matcher, PostToolUse with async:true), write updated settings, print success message with status check command
- [x] T072 [US3] Implement `mcpproxy hook uninstall` in `cmd/mcpproxy/hook_cmd.go`: remove mcpproxy hook entries from Claude Code settings file, preserve other hook entries and settings, print success message
- [x] T073 [US3] Implement `mcpproxy hook status` in `cmd/mcpproxy/hook_cmd.go`: check if hook config exists for detected agent, check if daemon is reachable via Unix socket, display: hooks installed (yes/no), agent type, scope, daemon reachable (yes/no), active session count (if reachable)
- [x] T074 [US3] Register hook subcommands in `cmd/mcpproxy/root.go` or main command setup: hookCmd as parent, hookEvaluateCmd, hookInstallCmd, hookUninstallCmd, hookStatusCmd as children

**Checkpoint**: One-command hook installation works. Settings file is correctly generated. Status shows daemon reachability. All US3 acceptance scenarios pass.

---

## Phase 9: US5 — Correlate Hook Sessions with MCP Sessions (Priority: P2)

**Purpose**: Enrich the flow graph by linking hook sessions to MCP sessions for unified cross-boundary visibility.

**Goal**: Automatically link sessions via argument hash matching (Mechanism A).

**Independent Test**: Send hook PreToolUse for `mcp__mcpproxy__call_tool_read` followed by matching MCP tool call; verify sessions are linked.

**Dependencies**: Requires US4 (hook endpoint) and US1 (proxy-only tracking) to be complete.

### Tests for US5 ⚠️

- [x] T080 [P] [US5] Write correlator unit tests in `internal/security/flow/correlator_test.go`: RegisterPending stores pending correlation with hash, MatchAndConsume returns hook session ID for matching hash, MatchAndConsume returns empty for non-matching hash, pending entries expire after TTL (5s), consumed entries are deleted (no double-match), concurrent RegisterPending + MatchAndConsume safety, multiple simultaneous sessions don't cross-contaminate
- [x] T081 [P] [US5] Write correlation integration tests in `internal/security/flow/service_test.go`: PreToolUse for mcp__mcpproxy__call_tool_read registers pending correlation, subsequent MCP call with matching args links sessions, linked sessions share flow state (origins from hook visible in MCP context), stale correlation (>5s TTL) is ignored

### Implementation for US5

- [x] T082 [US5] Implement `Correlator` in `internal/security/flow/correlator.go`: NewCorrelator(ttl), RegisterPending(hookSessionID, argsHash, toolName), MatchAndConsume(argsHash) returning hookSessionID, cleanup goroutine for expired entries, sync.Map for lock-free concurrent access
- [ ] T082b [US5] Benchmark sync.Map vs channel pattern for Correlator in `internal/security/flow/correlator_bench_test.go`: BenchmarkCorrelatorSyncMap and BenchmarkCorrelatorChannel comparing concurrent RegisterPending + MatchAndConsume throughput. Document results in plan.md Complexity Tracking. Constitution II requires this benchmark before merging.
- [x] T083 [US5] Integrate correlation in FlowService.Evaluate() for PreToolUse in `internal/security/flow/service.go`: when tool_name matches `mcp__mcpproxy__*`, extract inner tool name and args, hash them, call correlator.RegisterPending()
- [x] T084 [US5] Integrate correlation matching in `internal/server/mcp.go` handleCallToolVariant(): compute args hash, call flowService.MatchCorrelation(hash), if match found call flowService.LinkMCPSession(hookSessionID, mcpSessionID), linked sessions share FlowSession state

**Checkpoint**: Hook sessions are automatically linked to MCP sessions. Origins recorded via hooks are visible when checking MCP tool calls. All US5 acceptance scenarios pass.

---

## Phase 10: US6 — Enforce Configurable Flow Policies (Priority: P2)

**Purpose**: Enterprise policy customization for flow decisions.

**Goal**: Configurable policy actions per flow type, per tool, with suspicious endpoint blocking.

**Independent Test**: Configure different policies, replay same flow scenario, verify different decisions.

**Dependencies**: Requires US1 (PolicyEvaluator exists) to be complete.

### Tests for US6 ⚠️

- [x] T090 [P] [US6] Write policy configuration tests in `internal/security/flow/policy_test.go`: policy `internal_to_external: "allow"` returns allow for internal→external, policy `internal_to_external: "deny"` returns deny, policy `sensitive_data_external: "warn"` returns warn (instead of default deny), tool override `WebSearch: "allow"` always returns allow regardless of flow, suspicious endpoint `webhook.site` always returns deny, multiple edges returns highest-severity decision, config hot-reload updates policy behavior

### Implementation for US6

- [x] T091 [US6] Extend PolicyEvaluator in `internal/security/flow/policy.go` with config-driven behavior: support all PolicyAction values from config, suspicious endpoint checking against URL patterns in tool args, tool override lookup before flow-based policy, require_justification flag (future: check for justification field in tool input), config update method for hot-reload
- [x] T092 [US6] Add suspicious endpoint URL extraction in `internal/security/flow/policy.go`: scan tool_input for URL-like strings, check against configured suspicious_endpoints list, return PolicyDeny with specific reason if match found

**Checkpoint**: Policy customization works. All US6 acceptance scenarios pass. Different configurations produce expected different decisions.

---

## Phase 11: US7 — Log Hook Events in Activity Log (Priority: P2)

**Purpose**: Audit trail for security reviews and compliance.

**Goal**: All hook evaluations logged as activity records, filterable by type, flow_type, and risk_level.

**Independent Test**: Send hook events, query activity log for hook_evaluation records.

**Dependencies**: Requires US4 (hook endpoint produces events to log) to be complete.

### Tests for US7 ⚠️

- [x] T100 [P] [US7] Write activity log integration tests: hook evaluation creates activity record of type "hook_evaluation", record metadata includes tool_name, classification, flow_analysis, policy_decision, risk_level, session_id, filter `--type hook_evaluation` returns only hook records, filter `--risk-level high` returns high and critical records, filter `--flow-type internal→external` returns matching records

### Implementation for US7

- [x] T101 [US7] Add hook_evaluation activity type support in `internal/runtime/activity_service.go`: new RecordHookEvaluation method (or extend existing Record method), metadata structure per data-model.md (event, agent_type, hook_session_id, classification, flow_analysis, policy_decision, policy_reason, coverage_mode per FR-029)
- [x] T102 [US7] Add flow_type and risk_level filter parameters to activity list endpoint in `internal/httpapi/activity.go`: parse query params, filter metadata fields, apply to ListActivities query
- [x] T103 [US7] Add flow-related CLI filter flags in `cmd/mcpproxy/activity_cmd.go`: --flow-type and --risk-level flags for `mcpproxy activity list`, pass to API as query params
- [x] T104 [US7] Emit flow.alert SSE event in FlowService when risk is high or critical: publish to event bus in `internal/security/flow/service.go`, include activity_id, session_id, flow_type, risk_level, tool_name, has_sensitive_data

**Checkpoint**: All hook evaluations appear in activity log. Filtering by type, flow_type, and risk_level works. SSE events fire for critical flows. All US7 acceptance scenarios pass.

---

## Phase 12: US10 — Unified Activity Log for Flow Data (Priority: P2)

**Purpose**: Flow session summaries in the unified activity log for post-hoc analysis.

**Goal**: Write flow_summary records on session expiry with aggregate statistics.

**Independent Test**: Create flow sessions, let them expire, query for flow_summary records.

**Dependencies**: Requires US1 (FlowTracker with session expiry) and US7 (activity logging) to be complete.

### Tests for US10 ⚠️

- [x] T110 [P] [US10] Write flow summary tests in `internal/security/flow/tracker_test.go` and `internal/security/flow/service_test.go`: session expiry triggers FlowSummary creation, FlowSummary contains correct aggregate fields (duration, total_origins, total_flows, flow_type_distribution, risk_level_distribution, linked_mcp_sessions, tools_used, has_sensitive_flows), flow_summary activity record written on expiry, filter `--type flow_summary` returns only summary records, filter `--session-id <id>` returns all records for a session (tool_calls, hook_evaluations, flow_summary) in chronological order

### Implementation for US10

- [x] T111 [US10] Add FlowSummary generation to `internal/security/flow/tracker.go`: on session expiry, compute aggregate statistics from FlowSession (duration, origin count, flow count, flow type distribution, risk level distribution, tools used, has_sensitive_flows), return FlowSummary struct
- [x] T112 [US10] Add flow_summary activity record writing in `internal/security/flow/service.go`: register callback for session expiry, write ActivityRecord of type "flow_summary" with FlowSummary as metadata, include session_id, coverage_mode, linked_mcp_sessions
- [x] T113 [US10] Add session-id filter support to activity list in `internal/httpapi/activity.go` and `cmd/mcpproxy/activity_cmd.go`: parse --session-id flag/query param, filter activity records by session_id in metadata

**Checkpoint**: Flow summaries are written on session expiry. Unified log contains tool_call, hook_evaluation, and flow_summary records queryable together. All US10 acceptance scenarios pass.

---

## Phase 13: US11 — Auditor Agent Data Surface (Priority: P3)

**Purpose**: Ensure the data surface supports future auditor agent consumption.

**Goal**: REST API, SSE events, and activity export contain all fields needed for policy refinement, anomaly detection, and incident investigation.

**Independent Test**: Verify REST API and SSE contain required fields.

**Dependencies**: Requires US7 (hook evaluation logging) and US10 (flow summaries) to be complete.

### Tests for US11 ⚠️

- [x] T120 [P] [US11] Write auditor data surface tests: GET /api/v1/activity?type=hook_evaluation,flow_summary returns all flow records with classification, risk_level, flow_type in metadata, SSE /events stream includes flow.alert events for critical flows, GET /api/v1/activity/export?format=json&type=flow_summary returns exportable summaries with all required fields for trend analysis

### Implementation for US11

- [x] T121 [US11] Ensure activity list endpoint supports multi-type filtering in `internal/httpapi/activity.go`: parse comma-separated type values (e.g., `type=hook_evaluation,flow_summary`), return records matching any of the specified types
- [x] T122 [US11] Verify activity export includes flow metadata in `internal/httpapi/activity.go`: ensure export endpoint includes all metadata fields for hook_evaluation and flow_summary record types, support format=json and format=csv
- [x] T123 [US11] Add auditor_finding as reserved activity type constant in `internal/runtime/activity_service.go`: define constant but no implementation yet — placeholder for future auditor agent

**Checkpoint**: Data surface is complete for future auditor. REST API, SSE, and export all contain required fields. All US11 acceptance scenarios pass.

---

## Phase 14: Nudge System (Cross-Cutting, US9 Extension)

**Purpose**: Nudge users to install hooks via doctor CLI and web UI dashboard.

**Dependencies**: Requires US9 (coverage reporting) and US3 (hook install command exists) to be complete.

- [x] T130 [US9] Add hook installation hint to web UI Dashboard in `frontend/src/views/Dashboard.vue`: new hint in CollapsibleHintsPanel when security_coverage is "proxy_only", icon: shield, title: "Improve Security Coverage", content explaining hooks benefit, code block with install command, dismissible, only shown when hooks not active (fetch from /api/v1/status)
- [x] T131 [US9] Add security_coverage to web UI status polling: update status type definitions, pass hooks_active state to CollapsibleHintsPanel

**Checkpoint**: Doctor CLI and web UI both nudge users to install hooks when not present. Nudge disappears when hooks are active.

---

## Phase 15: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, testing hardening, and cross-cutting improvements

- [x] T140 [P] Update CLAUDE.md with new CLI commands (`mcpproxy hook evaluate/install/uninstall/status`), new API endpoint (`POST /api/v1/hooks/evaluate`), new security config section, flow security documentation references
- [x] T141 [P] Add E2E test for full proxy-only flow detection pipeline in `internal/server/e2e_test.go`: configure mock upstream servers (one internal, one external), send call_tool_read to internal, send call_tool_write to external with matching content, verify flow detected and blocked
- [x] T142 [P] Add E2E test for hook-enhanced flow detection in `internal/server/e2e_test.go`: send PostToolUse hook event for Read with sensitive data, send PreToolUse hook event for WebFetch with matching content, verify deny decision returned
- [x] T143 [P] Add race condition tests: run `go test -race ./internal/security/flow/...` to verify no data races in concurrent flow tracking, correlator, and session management
- [x] T144 [P] Update OpenAPI spec `oas/swagger.yaml`: add POST /api/v1/hooks/evaluate endpoint, add security_coverage and hooks_active to status response, add flow_type and risk_level filter params to activity endpoint, add Recommendation to diagnostics response
- [x] T145 Run `./scripts/verify-oas-coverage.sh` to ensure OpenAPI coverage includes new endpoint
- [x] T146 Run `./scripts/test-api-e2e.sh` to verify no regressions in existing API tests (61/71 pass, 10 failures pre-existing on baseline)
- [x] T147 Run `./scripts/run-all-tests.sh` to verify full test suite passes (all unit tests pass; E2E server tests have pre-existing 600s timeout)

---

## Dependencies & Execution Order

### Phase Dependencies

```
Phase 1 (Setup)
  ↓
Phase 2 (US2: Classifier) ──────────────────┐
  ↓                                          │
Phase 3 (US8: Hasher) ──────────────────────┐│
  ↓                                         ││
Phase 4 (US1: Proxy-Only Flow) ←────────────┘│
  ↓                                          │
Phase 5 (US9: Graceful Degradation)          │
  ↓                                          │
Phase 6 (US4: Hook Endpoint) ←───────────────┘
  ↓
Phase 7 (US1b: Hook-Enhanced Flow)
  ↓
Phase 8 (US3: Hook Install CLI)
Phase 9 (US5: Session Correlation) ──── can run in parallel with Phase 8
Phase 10 (US6: Configurable Policies) ─ can run in parallel with Phase 8
  ↓
Phase 11 (US7: Activity Logging)
  ↓
Phase 12 (US10: Flow Summaries)
  ↓
Phase 13 (US11: Auditor Data Surface)
  ↓
Phase 14 (Nudge System)
  ↓
Phase 15 (Polish)
```

### Parallel Opportunities

- **Phase 1**: T002, T003, T004 can run in parallel (different files)
- **Phase 2-3**: US2 and US8 are independent — can be implemented in parallel
- **Phase 4**: T030, T031, T032 (tests) can run in parallel
- **Phase 6**: T050, T051 (tests) can run in parallel
- **Phase 8, 9, 10**: US3, US5, US6 can all be worked on in parallel after Phase 7
- **Phase 15**: T140-T144 can all run in parallel

### Within Each User Story

1. Tests MUST be written and FAIL before implementation (TDD)
2. Types/models before services
3. Services before endpoints/CLI
4. Core implementation before integration
5. Story complete before moving to next dependency

### Both Operating Modes

Per SC-009, all test scenarios run in both proxy-only mode (no hooks) and hook-enhanced mode:
- Phase 4 (US1) tests cover proxy-only mode exclusively
- Phase 7 (US1b) tests cover hook-enhanced mode
- Phase 15 E2E tests (T141, T142) cover both modes explicitly

---

## Implementation Strategy

### MVP First (Phases 1-4)

1. Complete Phase 1: Setup (types, config)
2. Complete Phase 2: Classifier (US2)
3. Complete Phase 3: Hasher (US8)
4. Complete Phase 4: Proxy-Only Flow Detection (US1)
5. **STOP and VALIDATE**: Any MCP agent gets exfiltration protection without hooks
6. This is deployable as a standalone security improvement

### Hook Enhancement (Phases 5-7)

7. Complete Phase 5: Coverage reporting (US9)
8. Complete Phase 6: Hook endpoint (US4)
9. Complete Phase 7: Hook-enhanced detection (US1b)
10. **STOP and VALIDATE**: Claude Code users with hooks get full visibility

### Full Feature (Phases 8-15)

11. Complete Phases 8-13: CLI tools, correlation, policies, logging, summaries, data surface
12. Complete Phase 14: Nudge system
13. Complete Phase 15: Polish, E2E, documentation

---

## Notes

- [P] tasks = different files, no dependencies — can run in parallel
- [Story] label maps task to specific user story for traceability
- TDD is mandatory — write failing tests before implementation
- All flow scenarios must be tested in both proxy-only and hook-enhanced modes
- Commit after each task or logical group
- Stop at any checkpoint to validate the story independently
- The `contracts/go-types.go` file defines the public API surface — implementation types should match
