# Research: Data Flow Security with Agent Hook Integration

**Feature**: 027-data-flow-security
**Date**: 2026-02-04

## R1: Session Correlation Mechanism

**Decision**: Argument hash matching (Mechanism A)

**Rationale**: When Claude Code calls MCP tools through mcpproxy, a PreToolUse hook fires _before_ the MCP request arrives. Both carry identical tool name and arguments. By hashing the arguments on the hook side and registering a "pending correlation," mcpproxy can match the subsequent MCP call by hash within a 5-second TTL window. This links the hook `session_id` to the MCP `session_id` permanently after the first match.

**Alternatives considered**:
- **Mechanism B (updatedInput injection)**: Hook modifies tool arguments to inject `_flow_session` field. More explicit but requires echoing entire tool_input back through `updatedInput`, risks corruption of large payloads, and is Claude Code-specific (won't work for Cursor/Gemini CLI).
- **Process-level matching**: Track by subprocess lifetime for stdio MCP servers. Too coarse — can't distinguish multiple sessions.
- **Timestamp proximity only**: Match by time window without hash. Unreliable with concurrent sessions.

## R2: Hook Communication — CLI via Unix Socket vs HTTP Webhook

**Decision**: `mcpproxy hook evaluate` CLI command communicating via Unix socket.

**Rationale**: Embedding API keys and port numbers in static shell scripts is insecure. The CLI approach uses OS-level authentication (Unix socket), requires no secrets in any file, and the `mcpproxy` binary is already in PATH. Optimized fast path (skip config loading, no-op logger) achieves ~15-20ms end-to-end.

**Alternatives considered**:
- **Static shell script with curl**: Simple but embeds API key and port in plaintext. Rejected for security.
- **Named pipe**: Platform-specific complexity. Unix socket already supported.
- **gRPC**: Over-engineered for local IPC. HTTP over Unix socket is simpler.

**Performance budget**:
| Step | Time |
|------|------|
| Go binary startup | ~10ms |
| Socket path detection | ~0.1ms |
| Unix socket connect | ~1ms |
| JSON stdin read | ~1ms |
| HTTP POST to daemon | ~5-10ms |
| JSON stdout write | ~0.5ms |
| **Total** | **~15-20ms** |

## R3: Content Hashing Strategy

**Decision**: SHA256 truncated to 128 bits, multi-granularity (full content + per-field for strings >= 20 chars), with normalized secondary hashes.

**Rationale**: SHA256 provides collision resistance. 128-bit truncation is sufficient for per-session tracking (< 10,000 entries). Multi-granularity catches both exact payload reuse and partial data extraction. The 20-character minimum prevents false positives on short common strings.

**Alternatives considered**:
- **Full SHA256**: Wasteful for hash map keys when collision probability is negligible at this scale.
- **xxHash/FNV**: Faster but not cryptographically secure. Since hashes are used for security-relevant matching, SHA256 is preferred.
- **Fuzzy hashing (ssdeep)**: Better for detecting modified content but adds complexity and dependency. Normalized hashing covers the common reformatting cases.

## R4: Fail-Open vs Fail-Closed

**Decision**: Fail-open when daemon is unreachable.

**Rationale**: The hook evaluator is a security enhancement, not a gatekeeper. If mcpproxy is down, blocking all agent tool calls would make the agent unusable. The hook script returns "allow" and the agent proceeds. When the daemon comes back, new flows are tracked from that point.

**Alternatives considered**:
- **Fail-closed**: More secure but breaks agent functionality when daemon is restarting or has crashed. Unacceptable UX impact.
- **Configurable**: Add a `fail_open` config option (default: true). Included in the spec for enterprise users who prefer fail-closed.

## R5: Classification Heuristics Approach

**Decision**: Name-based pattern matching with configurable overrides.

**Rationale**: Server and tool names are the most reliable signal available without semantic understanding. Patterns like "slack", "email", "webhook" strongly correlate with external communication. Config overrides handle edge cases where heuristics are wrong.

**Alternatives considered**:
- **MCP annotations**: The `readOnlyHint`/`destructiveHint` annotations could inform classification, but they describe operation type, not data flow direction. A "readOnly" Slack tool still sends data externally.
- **URL-based**: Classify by server URL (localhost = internal, external hostname = external). Fragile — many internal services have external URLs.
- **LLM classification**: MCPProxy constitution prohibits internal LLM calls for classification. All detection must be deterministic.

## R6: Testing Strategy

**Decision**: TDD with deterministic flow scenarios as the contract. Write test scenarios first, then implement to make them pass.

**Rationale**: The constitution mandates TDD. Flow detection scenarios have clear inputs (tool call sequences) and outputs (risk level, decision), making them ideal for table-driven tests. The existing e2e test infrastructure (TestEnvironment, mock upstream servers) can be extended.

**Test layers**:
- **Unit tests**: Classification heuristics, content hashing, flow tracking, policy evaluation
- **Integration tests**: Hook evaluate endpoint with flow session state
- **E2E tests**: Full hook→daemon→MCP pipeline with mock upstream servers

## R7: Stacklok Pattern Analysis

**Decision**: Adopt the `mcp__.*` matcher pattern for MCP tools. Do NOT adopt binary allow/deny policy (ours is richer).

**Key learnings from Stacklok**:
- They match `mcp__.*` to filter only MCP tools — same pattern we use
- OpenTelemetry export for enterprise observability (future consideration)
- Pre-filtering blocked tools from context window (aligns with our quarantine system)
- Their policy is binary (allow/deny by server origin). Ours needs flow-awareness, sensitivity-awareness, and justification support.

## R8: Integration with Existing Sensitive Data Detector

**Decision**: Reuse the existing `security.Detector.Scan()` method for checking tool inputs during hook evaluation.

**Rationale**: The detector is already battle-tested with comprehensive patterns (cloud credentials, API tokens, private keys, database credentials, credit cards, high entropy). Integration follows the same async pattern used by ActivityService — call `Scan()` on the arguments and response, check for detections, escalate risk level accordingly.

**Integration point**: The flow detector calls `detector.Scan(argsJSON, "")` during PreToolUse evaluation. If sensitive data is detected AND the flow is `internal→external`, risk is escalated to "critical."

## R9: Proxy-Only vs Hook-Enhanced Operating Modes

**Decision**: Two operating modes. Proxy-only is the default; hooks are an optional enhancement. System MUST be fully functional without hooks.

**Rationale**: Most autonomous MCP agents (OpenClaw, Goose, custom bots) have no hook system. MCPProxy's value as an MCP firewall should not depend on agent-side changes. The proxy layer already sees all MCP tool calls — `call_tool_read` responses can be hashed as data origins, and `call_tool_write`/`call_tool_destructive` arguments can be checked against those origins.

**Coverage comparison**:
| Scenario | Proxy-Only | Hook-Enhanced |
|----------|-----------|---------------|
| MCP server A → MCP server B exfiltration | ✅ Detected | ✅ Detected |
| Agent `Read` → MCP external server | ❌ Not visible | ✅ Detected |
| Agent `Read` → Agent `WebFetch` | ❌ Not visible | ✅ Detected |
| Agent `Bash curl` exfiltration | ❌ Not visible | ✅ Detected |
| MCP server response → same MCP server | ✅ Detected | ✅ Detected |

**Key design choice**: In proxy-only mode, FlowSessions are keyed by MCP session ID. In hook-enhanced mode, they are keyed by hook session ID (with MCP sessions linked via correlation). The FlowTracker handles both seamlessly.

## R10: Agent Ecosystem Analysis

**Decision**: Design for universal MCP proxy compatibility. Hook adapters are per-agent extensions, not core requirements.

**Agents analyzed**:
- **OpenClaw** (150K+ GitHub stars): Uses mcporter for MCP transport (HTTP/SSE/stdio). No hook system. Publicly exposed MCP interfaces found on 1000+ deployments without authentication — exactly the attack vector MCPProxy prevents.
- **Goose** (Block/Linux Foundation): Native MCP with stdio/SSE/HTTP. No hook system. Rust-based, model-agnostic.
- **Claude Agent SDK**: Most mature hook system (PreToolUse/PostToolUse, regex matchers). MCP tools namespaced as `mcp__<server>__<tool>` — same pattern MCPProxy uses.
- **mcp-use**: Python/TypeScript library for connecting any LLM to any MCP server. Built-in access controls.

**Integration patterns**:
- **Universal (HTTP proxy)**: Any agent pointing at MCPProxy's HTTP endpoint gets proxy-only protection. Works with OpenClaw/mcporter, Goose, Claude Agent SDK, mcp-use, any MCP client.
- **Hook-enhanced (per-agent)**: Currently Claude Code only. Claude Agent SDK hooks could also integrate (same protocol). Future: Cursor, Gemini CLI.
- **Future: Stdio bridge**: A lightweight binary for stdio-only agents (Claude Desktop, local Goose) that forwards JSON-RPC to MCPProxy's HTTP endpoint.

## R11: Unified vs Separate Flow Log

**Decision**: Unified activity log with new record types (`hook_evaluation`, `flow_summary`). No separate flow database.

**Rationale**: The existing activity log pattern has been proven across Specs 016, 017, 024, 026. The `ActivityRecord.Metadata` map provides type-specific fields without schema changes. A separate flow log would duplicate infrastructure (pagination, filtering, pruning, export) for minimal benefit.

**Record types in unified log**:
| Type | Trigger | Key Metadata |
|------|---------|-------------|
| `tool_call` | Every MCP tool call | arguments, response, intent, sensitive_data |
| `hook_evaluation` | Each hook evaluate call | classification, flow_analysis, risk_level, policy_decision |
| `flow_summary` | Flow session expiry (30min) | duration, origin_count, flow_count, flow_type_distribution, risk_levels, tools_used |
| `auditor_finding` | Future: auditor analysis | finding_type, severity, evidence, recommendation |

**Why not a separate log**: The auditor needs to correlate tool calls, hook evaluations, and flow summaries by session_id and time range. A unified log makes this a single query. The existing `ListActivities` with filter support handles it. Performance is manageable with the default 7-day / 10K record retention and pruning.

## R12: Auditor Agent Architecture

**Decision**: Three-mode architecture — batch analyst + real-time monitor + MCP server. The auditor is a future feature; the data surface is designed now.

**Architecture**:
- **Batch Analyst**: Exports activity data via REST API, computes behavioral baselines, identifies anomalies, suggests policy refinements. Runs periodically (every 5-15 minutes).
- **Real-Time Monitor**: Subscribes to SSE `flow.alert` events. Detects critical anomalies immediately (sudden spike in external calls, sensitive data exposure).
- **MCP Server**: Exposed as an upstream server in MCPProxy. Tools: `security_report`, `investigate_session`, `suggest_policies`, `anomaly_summary`. Any agent can query the auditor directly via `call_tool_read`.

**Key auditor capabilities**:
- **Policy refinement**: "WebSearch triggered 47 ask decisions, all approved — recommend adding to tool_overrides with action allow."
- **Anomaly detection**: Volume spikes, new tool usage, unusual tool sequences, error bursts, session duration outliers.
- **Classification improvement**: "Server my-custom-api classified as unknown but 95% of its calls send data externally — recommend reclassifying as external."
- **Incident investigation**: Reconstruct tool call chains by session_id, identify data origins, assess whether the pattern indicates prompt injection.

**Data requirements**: All satisfied by the unified activity log + REST API + SSE. No new persistent storage needed. The auditor operates at the pattern level (frequencies, distributions, sequences), not the content hash level.
