# Spec 035: Enhanced Tool Annotations Intelligence

## Overview

Leverage MCP tool annotations for security-aware routing, quarantine protection, session-level risk analysis, and smarter tool discovery filtering.

## Features

### F1: Annotation Change Detection in Quarantine
Include tool annotations in the SHA-256 hash used for tool-level quarantine change detection. Detects "annotation rug-pulls" where a server flips `destructiveHint` from true→false to trick agents into using a dangerous tool through `call_tool_read`.

**Files**: `internal/runtime/tool_quarantine.go`
- Modify `calculateToolApprovalHash()` to include serialized annotations in hash input
- Format: `toolName|description|schemaJSON|annotationsJSON`
- Nil annotations → empty string (backward compatible, won't invalidate existing approvals unless annotations appear)

### F2: Lethal Trifecta Session Analysis
When `retrieve_tools` is called, analyze ALL connected servers' tool annotations to detect the "lethal trifecta" (Simon Willison's risk model): a session combining (1) access to sensitive/private data, (2) exposure to untrusted content via open-world tools, and (3) ability to write/destroy.

**Files**: `internal/server/mcp.go`
- Add `analyzeSessionRisk()` method on MCPProxyServer
- Scan all tools across all servers via StateView snapshot
- Classify tools into risk categories based on annotations:
  - `has_sensitive`: tools with `readOnlyHint=false` or accessing private data patterns
  - `has_open_world`: tools with `openWorldHint=true` (or nil, since default is true)
  - `has_destructive`: tools with `destructiveHint=true` (or nil, since default is true)
- Add `session_risk` field to retrieve_tools response:
  ```json
  {
    "session_risk": {
      "level": "high|medium|low",
      "has_sensitive_data_tools": true,
      "has_open_world_tools": true,
      "has_destructive_tools": true,
      "lethal_trifecta": true,
      "warning": "Session has tools that combine private data access, untrusted content, and destructive capabilities"
    }
  }
  ```

### F3: openWorldHint Enhanced Scanning
Flag tool call responses from tools with `openWorldHint=true` for enhanced sensitive data scanning. Untrusted content from open-world tools is a primary vector for prompt injection data exfiltration.

**Files**: `internal/runtime/activity_service.go`, `internal/server/mcp.go`
- In `handleCallToolVariant()` and code_execution handler, check if the called tool has `openWorldHint=true`
- If yes, add metadata tag `"content_trust": "untrusted"` to the activity record
- The sensitive data detector already scans responses; this adds context for audit

### F4: Annotation-Based Filtering in retrieve_tools
Add optional filter parameters to `retrieve_tools` for agents to self-restrict tool discovery.

**Files**: `internal/server/mcp.go`, `internal/server/mcp_routing.go`
- New optional parameters:
  - `read_only_only` (bool): Only return tools with `readOnlyHint=true`
  - `exclude_destructive` (bool): Exclude tools with `destructiveHint=true` or nil
  - `exclude_open_world` (bool): Exclude tools with `openWorldHint=true` or nil
- Filter applied after BM25 search, before response building
- Update tool definition in `buildCallToolModeTools()` and `buildCodeExecModeTools()`

### F5: Annotation Coverage Reporting
REST API endpoint and CLI output showing annotation adoption across connected servers.

**Files**: `internal/httpapi/server.go`, `internal/server/mcp.go`
- New endpoint: `GET /api/v1/annotations/coverage`
- Response:
  ```json
  {
    "total_tools": 45,
    "annotated_tools": 12,
    "coverage_percent": 26.7,
    "servers": [
      {
        "name": "github",
        "total_tools": 20,
        "annotated": 8,
        "coverage_percent": 40.0
      }
    ]
  }
  ```
- Also add `annotation_coverage` field to retrieve_tools response when `include_stats=true`

## Testing
- Unit tests for each feature
- E2E test verifying annotation filtering works via MCP protocol
- Verify quarantine hash change detection triggers on annotation changes
