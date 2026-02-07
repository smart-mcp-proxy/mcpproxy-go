# Implementation Plan: Data Flow Security with Agent Hook Integration

**Branch**: `027-data-flow-security` | **Date**: 2026-02-04 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/027-data-flow-security/spec.md`

## Summary

Implement data flow security to detect exfiltration patterns (the "lethal trifecta") by tracking data movement across tool calls at the MCP proxy layer. The system operates in two modes:

1. **Proxy-only mode (default, works with any agent)**: Classifies upstream servers as internal/external, hashes MCP tool responses, detects cross-server data exfiltration within MCP traffic. No agent-side changes required — works with OpenClaw, Goose, Claude Agent SDK, any MCP client.

2. **Hook-enhanced mode (optional, currently Claude Code)**: Adds visibility into agent-internal tools (`Read`, `WebFetch`, `Bash`) via opt-in hooks. The `mcpproxy hook evaluate` CLI communicates with the daemon via Unix socket (no embedded secrets). Session correlation via argument hash matching links hook sessions to MCP sessions.

The system nudges users to install hooks via `mcpproxy doctor` and web UI banners, but never requires them. All test scenarios cover both operating modes.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: BBolt (storage), Chi router (HTTP), Zap (logging), mcp-go (MCP protocol), regexp (stdlib), crypto/sha256 (stdlib), existing `security.Detector`
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord.Metadata extension for hook_evaluation type. Flow sessions are in-memory only (not persisted).
**Testing**: Go testing with testify/assert, table-driven tests, existing E2E infrastructure (`TestEnvironment`, mock upstream servers)
**Target Platform**: Cross-platform (Windows, Linux, macOS) — hook CLI install targets Claude Code on all platforms, Unix socket on macOS/Linux
**Project Type**: Single project (existing mcpproxy codebase)
**Performance Goals**: Hook evaluate CLI completes in <100ms end-to-end (Go binary startup ~10ms, socket connect ~1ms, HTTP POST ~5-10ms); 50+ concurrent evaluations/sec
**Constraints**: No blocking of agent tool calls on PostToolUse (async only), fail-open when daemon unreachable, no LLM calls for classification (deterministic only), max 10,000 origins per session
**Scale/Scope**: ~20 classification patterns, SHA256 truncated to 128 bits, 5-second correlation TTL

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Gate | Status | Notes |
|------|--------|-------|
| I. Performance at Scale | ✅ PASS | Hook evaluate <100ms, in-memory flow sessions, SHA256 hashing is O(n) on payload size, max 10K origins per session with eviction |
| II. Actor-Based Concurrency | ✅ PASS | FlowTracker uses sync.RWMutex on per-session basis (not global lock). PostToolUse processing is async via goroutine. Pending correlations use sync.Map for lock-free concurrent access. Event bus publishes `flow.alert` events. |
| III. Configuration-Driven | ✅ PASS | All settings in `mcp_config.json` under `security.flow_tracking`, `security.classification`, `security.flow_policy`, `security.hooks`. Hot-reload supported via existing config watcher. |
| IV. Security by Default | ✅ PASS | Flow tracking enabled by default. Default policy: `internal_to_external: "ask"`, `sensitive_data_external: "deny"`. Known suspicious endpoints blocked by default. No secrets in hook configuration files. Proxy-only mode works without any agent-side changes. |
| V. Test-Driven Development | ✅ PASS | TDD approach: write deterministic flow scenarios as table-driven tests first, implement to make them pass. All scenarios tested in both proxy-only and hook-enhanced modes. 5+ attack patterns as E2E test scenarios. Unit tests for classifier, hasher, tracker, policy. |
| VI. Documentation Hygiene | ✅ PASS | CLAUDE.md update for new CLI commands and security section. API docs for new endpoint. Code comments for hashing and classification logic. |

**Post-Design Re-check**: All gates still pass. Constitution II exception for mutex/sync.Map usage in FlowTracker and Correlator requires benchmark validation (tasks T033b, T082b). If benchmarks show channels perform within 10% of mutex/sync.Map, the implementation MUST use channels to comply with Constitution II. The exception is conditional on benchmark results, not pre-approved.

## Project Structure

### Documentation (this feature)

```text
specs/027-data-flow-security/
├── plan.md              # This file
├── research.md          # Phase 0 output - 8 research decisions
├── data-model.md        # Phase 1 output - entity model, enums, config
├── quickstart.md        # Phase 1 output - implementation guide
└── contracts/           # Phase 1 output
    ├── hook-evaluate-api.yaml   # OpenAPI for POST /api/v1/hooks/evaluate
    ├── config-schema.json       # JSON Schema for security config section
    └── go-types.go              # Go type definitions (public API surface)
```

### Source Code (repository root)

```text
internal/
├── security/
│   ├── detector.go                # EXISTING: Reused for sensitive data checks
│   └── flow/                      # NEW: Flow security subsystem
│       ├── classifier.go          # Server/tool classification heuristics
│       ├── classifier_test.go     # Unit tests for classification
│       ├── hasher.go              # Content hashing (SHA256 truncated, multi-granularity)
│       ├── hasher_test.go         # Unit tests for hashing
│       ├── tracker.go             # Per-session flow state tracking
│       ├── tracker_test.go        # Unit tests for flow detection
│       ├── policy.go              # Policy evaluation engine
│       ├── policy_test.go         # Unit tests for policy decisions
│       ├── correlator.go          # Pending correlation for session linking
│       ├── correlator_test.go     # Unit tests for correlation
│       ├── service.go             # FlowService: orchestrates classify→hash→track→policy
│       └── service_test.go        # Integration tests for full evaluation pipeline
├── config/
│   └── config.go                  # MODIFY: Add FlowTrackingConfig, ClassificationConfig, etc.
├── contracts/
│   └── types.go                   # MODIFY: Add Recommendation type to Diagnostics
├── management/
│   └── diagnostics.go             # MODIFY: Add hook detection and recommendations
├── runtime/
│   ├── activity_service.go        # MODIFY: Add hook_evaluation, flow_summary activity types
│   └── events.go                  # MODIFY: Add flow.alert event type
├── httpapi/
│   ├── server.go                  # MODIFY: Register POST /api/v1/hooks/evaluate route, add security_coverage to status
│   ├── hooks.go                   # NEW: Hook evaluation HTTP handler
│   └── activity.go                # MODIFY: Add flow_type, risk_level filter params
└── server/
    └── mcp.go                     # MODIFY: Add proxy-only flow tracking + correlation matching in handleCallToolVariant

cmd/mcpproxy/
├── hook_cmd.go                    # NEW: hook evaluate/install/uninstall/status commands
├── hook_cmd_test.go               # NEW: CLI command tests
└── doctor_cmd.go                  # MODIFY: Add Security Coverage section with hook nudge

frontend/src/
└── views/
    └── Dashboard.vue              # MODIFY: Add hook installation hint to CollapsibleHintsPanel
```

**Structure Decision**: New `internal/security/flow/` package within the existing `internal/security/` directory (alongside the existing `detector.go`). This groups all flow security logic (classification, hashing, tracking, policy, correlation) in one cohesive package. The `FlowService` type orchestrates the full evaluation pipeline and is injected into the HTTP handler and MCP server.

## Integration Points

### FlowService (Orchestrator)

```go
// internal/security/flow/service.go
type FlowService struct {
    classifier *Classifier
    tracker    *FlowTracker
    policy     *PolicyEvaluator
    correlator *Correlator
    detector   *security.Detector  // Reuse existing Spec 026 detector
    activity   ActivityLogger       // Interface to activity service
    eventBus   EventPublisher       // Interface to event bus
}

// Evaluate processes a hook event and returns a security decision.
func (s *FlowService) Evaluate(req HookEvaluateRequest) HookEvaluateResponse {
    switch req.Event {
    case "PreToolUse":
        return s.evaluatePreToolUse(req)
    case "PostToolUse":
        s.processPostToolUse(req)
        return HookEvaluateResponse{Decision: PolicyAllow}
    }
}
```

### Proxy-Only Flow Tracking in MCP Pipeline

```go
// internal/server/mcp.go - handleCallToolVariant() additions
func (p *MCPProxyServer) handleCallToolVariant(ctx context.Context, request mcp.CallToolRequest, toolVariant string) (*mcp.CallToolResult, error) {
    // ... existing code ...
    mcpSessionID := getSessionID()

    // NEW: Check for pending hook correlation (hook-enhanced mode)
    if p.flowService != nil && mcpSessionID != "" {
        argsHash := flow.HashContent(toolName + argsJSON)
        if hookSessionID := p.flowService.MatchCorrelation(argsHash); hookSessionID != "" {
            p.flowService.LinkMCPSession(hookSessionID, mcpSessionID)
        }
    }

    // NEW: Pre-call flow check (proxy-only mode)
    // For write/destructive variants, check args against recorded origins
    if p.flowService != nil && (toolVariant == "write" || toolVariant == "destructive") {
        edges := p.flowService.CheckFlowProxy(mcpSessionID, serverName, toolName, argsJSON)
        if decision := p.flowService.EvaluatePolicy(edges); decision == flow.PolicyDeny {
            return nil, fmt.Errorf("blocked: %s", decision.Reason)
        }
    }

    // ... existing tool call logic ...

    // NEW: Post-call origin recording (proxy-only mode)
    // For read variants, hash response as data origin
    if p.flowService != nil && toolVariant == "read" {
        go p.flowService.RecordOriginProxy(mcpSessionID, serverName, toolName, responseJSON)
    }
}
```

### Config Extension

```go
// internal/config/config.go addition
type SecurityConfig struct {
    FlowTracking   *FlowTrackingConfig   `json:"flow_tracking,omitempty"`
    Classification *ClassificationConfig  `json:"classification,omitempty"`
    FlowPolicy     *FlowPolicyConfig     `json:"flow_policy,omitempty"`
    Hooks          *HooksConfig          `json:"hooks,omitempty"`
}
```

### Event Bus Integration

```go
// Emit flow.alert event for real-time Web UI updates
s.eventBus.Publish(Event{
    Type: "flow.alert",
    Data: map[string]interface{}{
        "activity_id":      activityID,
        "session_id":       req.SessionID,
        "flow_type":        edge.FlowType,
        "risk_level":       edge.RiskLevel,
        "tool_name":        req.ToolName,
        "has_sensitive_data": edge.FromOrigin.HasSensitiveData,
    },
})
```

### Nudge Integration (Doctor + Web UI)

```go
// internal/management/diagnostics.go addition
type Recommendation struct {
    ID          string `json:"id"`
    Category    string `json:"category"`
    Title       string `json:"title"`
    Description string `json:"description"`
    Command     string `json:"command,omitempty"`
    Priority    string `json:"priority"` // "optional", "recommended"
}

// Added to Diagnostics struct:
// Recommendations []Recommendation `json:"recommendations,omitempty"`

// Doctor CLI output (cmd/mcpproxy/doctor_cmd.go):
// 🔒 Security Coverage
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//   Coverage: MCP proxy only
//   💡 Recommended: Install agent hooks for full coverage
//      Hooks provide visibility into agent-internal tools (Read, Bash, WebFetch).
//      Without hooks, flow detection is limited to MCP-proxied tool calls.
//      Run: mcpproxy hook install --agent claude-code
```

```typescript
// frontend/src/views/Dashboard.vue addition (CollapsibleHintsPanel)
// New hint when hooks not active:
// {
//   icon: "shield",
//   title: "Improve Security Coverage",
//   sections: [{
//     title: "Install Agent Hooks",
//     content: "MCPProxy currently monitors MCP tool calls only. Install hooks for visibility into agent-internal tools...",
//     code: "mcpproxy hook install --agent claude-code --scope project"
//   }]
// }
```

### Hook Install Output (Claude Code)

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Read|Glob|Grep|Bash|Write|Edit|WebFetch|WebSearch|Task|mcp__.*",
        "hooks": ["mcpproxy hook evaluate --event PreToolUse"]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Read|Glob|Grep|Bash|Write|Edit|WebFetch|WebSearch|Task|mcp__.*",
        "hooks": ["mcpproxy hook evaluate --event PostToolUse"],
        "async": true
      }
    ]
  }
}
```

## Complexity Tracking

> No constitution violations. Mutex usage justified below.

| Aspect | Decision | Rationale | Constitution II Exception |
|--------|----------|-----------|--------------------------|
| sync.RWMutex in FlowTracker | Per-session mutex | Sessions are independent, read-heavy access pattern (CheckFlow reads, RecordOrigin writes), small critical sections (hash map lookup/insert). Channel-based alternative requires a dedicated goroutine per session plus serialized request/response channels, adding ~3x code complexity for a simple map[string]*DataOrigin. | **Benchmark required** (T033b). Must demonstrate mutex outperforms channel-per-session pattern for concurrent hash map operations before merging. If benchmark shows <10% difference, refactor to channels. |
| sync.Map in Correlator | Lock-free shared map | Pending correlations are write-once-read-once with TTL expiry. sync.Map is optimized for this pattern (keys written once by hook goroutine, read once by MCP goroutine, then deleted). Channel alternative requires a manager goroutine mediating all register/match/expire operations. | **Benchmark required** (T082b). Must demonstrate sync.Map outperforms channel-mediated pattern for the register→match→consume lifecycle under concurrent load. |
| In-memory flow sessions | Not persisted to BBolt | Sessions are ephemeral (30min TTL), high write frequency (every tool call), and persisting would add latency. Acceptable to lose state on daemon restart. | N/A |
| SHA256 for content hashing | Cryptographic hash | Used for security-relevant matching. Non-crypto hashes (xxHash) are faster but don't provide collision resistance guarantees. | N/A |
| Fail-open default | Hook returns "allow" on error | The hook is a security enhancement, not a gatekeeper. Blocking agents when daemon is down is unacceptable UX. Configurable for enterprise users who prefer fail-closed. | N/A |
