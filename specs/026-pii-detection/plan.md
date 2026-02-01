# Implementation Plan: Sensitive Data Detection

**Branch**: `026-pii-detection` | **Date**: 2026-01-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/026-pii-detection/spec.md`

## Summary

Implement automatic detection of secrets, credentials, and sensitive file paths in MCP tool call arguments and responses. Detection results are stored in Activity Log metadata, enabling users to audit data exposure risks through Web UI, CLI, and REST API. The system uses pattern-based detection (regex for secrets, glob matching for file paths) with Shannon entropy analysis for high-entropy strings and Luhn validation for credit cards.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: BBolt (storage), Chi router (HTTP), Zap (logging), regexp (stdlib), existing ActivityService
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord.Metadata extension
**Testing**: Go testing with testify/assert, table-driven tests, temp BBolt DBs
**Target Platform**: Cross-platform (Windows, Linux, macOS)
**Project Type**: Single project (existing mcpproxy codebase)
**Performance Goals**: Detection completes in <15ms for typical payloads (<64KB)
**Constraints**: No blocking of tool responses, async detection, no secret values stored
**Scale/Scope**: ~100 built-in patterns, configurable custom patterns

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Gate | Status | Notes |
|------|--------|-------|
| I. Performance at Scale | ✅ PASS | Detection <15ms, async execution, no blocking |
| II. Actor-Based Concurrency | ✅ PASS | Uses goroutines for async detection, no shared state |
| III. Configuration-Driven | ✅ PASS | All settings in mcp_config.json, hot-reload support |
| IV. Security by Default | ✅ PASS | Detection enabled by default, no secrets stored |
| V. Test-Driven Development | ✅ PASS | Unit tests for patterns, integration tests for ActivityLog |
| VI. Documentation Hygiene | ✅ PASS | CLAUDE.md, README updates planned |

## Project Structure

### Documentation (this feature)

```text
specs/026-pii-detection/
├── plan.md              # This file
├── research.md          # Phase 0 output - pattern sources, library research
├── data-model.md        # Phase 1 output - DetectionPattern, DetectionResult
├── quickstart.md        # Phase 1 output - integration guide
└── contracts/           # Phase 1 output - API schemas
    ├── detection-result.schema.json
    └── config-schema.json
```

### Source Code (repository root)

```text
internal/
├── security/                    # NEW: Detection engine
│   ├── detector.go              # SensitiveDataDetector main type
│   ├── detector_test.go         # Unit tests
│   ├── patterns/                # Pattern definitions
│   │   ├── cloud.go             # AWS, GCP, Azure patterns
│   │   ├── tokens.go            # GitHub, Stripe, Slack patterns
│   │   ├── keys.go              # Private key patterns
│   │   ├── files.go             # Sensitive file path patterns
│   │   └── custom.go            # Custom pattern loading
│   ├── entropy.go               # Shannon entropy calculation
│   ├── luhn.go                  # Luhn credit card validation
│   └── paths.go                 # Cross-platform path normalization
├── runtime/
│   └── activity_service.go      # MODIFY: Add detection hook
├── config/
│   └── config.go                # MODIFY: Add detection config
└── httpapi/
    └── activity_handlers.go     # MODIFY: Add filter params

frontend/src/
├── views/
│   └── ActivityLogView.vue      # MODIFY: Add detection filters
└── components/
    └── ActivitySensitiveData.vue  # NEW: Detection details component

cmd/mcpproxy/
└── commands/
    └── activity.go              # MODIFY: Add filter flags
```

**Structure Decision**: Single project extension - new `internal/security/` package with integration into existing ActivityService, config, and CLI modules. No new binaries or major architectural changes.

## Complexity Tracking

> No constitution violations. Simple pattern-based detection with existing infrastructure.

| Aspect | Decision | Rationale |
|--------|----------|-----------|
| No ML/NLP | Pattern-based only | Simpler, deterministic, lower latency |
| No blocking | Async detection | Follows constitution's non-blocking I/O rule |
| Metadata extension | Map field | Leverages existing ActivityRecord.Metadata |

## Integration Points

### ActivityService Hook

```go
// internal/runtime/activity_service.go - handleToolCallCompleted()
func (s *ActivityService) handleToolCallCompleted(event ToolCallCompletedEvent) {
    record := createActivityRecord(event)

    // NEW: Run sensitive data detection asynchronously
    go func() {
        result := s.detector.Scan(event.Arguments, event.Response)
        if result.Detected {
            s.updateActivityMetadata(record.ID, "sensitive_data_detection", result)
        }
    }()

    s.store.SaveActivity(record)
}
```

### Config Extension

```go
// internal/config/config.go
type SensitiveDataDetectionConfig struct {
    Enabled          bool                  `json:"enabled"`
    ScanRequests     bool                  `json:"scan_requests"`
    ScanResponses    bool                  `json:"scan_responses"`
    MaxPayloadSizeKB int                   `json:"max_payload_size_kb"`
    EntropyThreshold float64               `json:"entropy_threshold"`
    Categories       map[string]bool       `json:"categories"`
    CustomPatterns   []CustomPatternConfig `json:"custom_patterns"`
    SensitiveKeywords []string             `json:"sensitive_keywords"`
}
```

### Event Bus Integration

```go
// Emit event on detection for real-time Web UI updates
s.eventBus.Publish(Event{
    Type: "sensitive_data.detected",
    Data: map[string]interface{}{
        "activity_id": record.ID,
        "detections":  result.Detections,
    },
})
```
