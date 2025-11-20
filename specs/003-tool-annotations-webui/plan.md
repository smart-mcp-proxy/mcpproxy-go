# Implementation Plan: Tool Annotations & MCP Sessions in WebUI

**Branch**: `003-tool-annotations-webui` | **Date**: 2025-11-19 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/003-tool-annotations-webui/spec.md`

## Summary

Add tool annotations transparency to WebUI by displaying MCP tool hints (title, readOnlyHint, destructiveHint, idempotentHint, openWorldHint) on server details pages and in tool call history. Implement MCP session tracking with a dashboard table showing the 10 most recent sessions (status, start time, duration, client name, tool call count, token sum) and add session-based filtering to tool call history.

## Technical Context

**Language/Version**: Go 1.21+, TypeScript/Vue 3
**Primary Dependencies**:
- Backend: mark3labs/mcp-go v0.42.0 (has `ToolAnnotation` struct), chi router, BBolt, zap
- Frontend: Vue 3, TypeScript, TailwindCSS, DaisyUI
**Storage**: BBolt (existing `server_{serverID}_tool_calls` buckets, new `sessions` bucket)
**Testing**: go test, Playwright for E2E
**Target Platform**: Cross-platform desktop (macOS, Linux, Windows)
**Project Type**: Web application (Go backend + Vue frontend)
**Performance Goals**: Dashboard loads in <1s, tool annotations render in <100ms
**Constraints**: Session data limited to 100 most recent, 30s polling interval for active sessions
**Scale/Scope**: Up to 1000 tools across multiple servers, up to 100 stored sessions

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Session queries are bounded (100 max), annotations are cached per-tool |
| II. Actor-Based Concurrency | ✅ PASS | Using existing manager patterns, no new locks needed |
| III. Configuration-Driven | ✅ PASS | No new config needed, uses existing core architecture |
| IV. Security by Default | ✅ PASS | No security changes, annotations are read-only metadata |
| V. Test-Driven Development | ✅ PASS | Will add unit tests for session storage, integration tests for API |
| VI. Documentation Hygiene | ✅ PASS | Will update CLAUDE.md if API endpoints change |

**Architecture Constraints:**
- Core + Tray Split: ✅ All changes in core, tray reads via API
- Event-Driven Updates: ✅ Will use existing SSE for session updates
- DDD Layering: ✅ Storage in internal/storage, API in internal/httpapi
- Upstream Client Modularity: ✅ No changes to upstream client layers

## Project Structure

### Documentation (this feature)

```text
specs/003-tool-annotations-webui/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── contracts/
│   └── types.go              # Add ToolAnnotation, MCPSession types
├── storage/
│   ├── manager.go            # Add session CRUD operations
│   └── server_identity.go    # Update ToolCallRecord with annotations
├── httpapi/
│   └── server.go             # Add session endpoints, update tool endpoints
└── server/
    └── mcp.go                # Capture annotations when recording tool calls

# Frontend (Vue 3 + TypeScript)
frontend/src/
├── types/
│   └── api.ts                # Add ToolAnnotation, MCPSession interfaces
├── services/
│   └── api.ts                # Add session API methods
├── views/
│   ├── Dashboard.vue         # Add sessions table component
│   ├── ServerDetail.vue      # Add annotation badges to tool cards
│   └── ToolCalls.vue         # Add session filter, compact annotations
├── components/
│   ├── AnnotationBadges.vue  # NEW: Reusable annotation display
│   ├── SessionsTable.vue     # NEW: Dashboard sessions component
│   └── SessionFilter.vue     # NEW: Filter dropdown for tool calls

# Tests
internal/
├── storage/
│   └── session_test.go       # NEW: Session storage tests
└── httpapi/
    └── session_test.go       # NEW: Session API tests
```

**Structure Decision**: Web application with Go backend (`internal/`) and Vue frontend (`frontend/src/`). This follows the existing MCPProxy architecture.

## Complexity Tracking

> No violations - design follows existing patterns with minimal new abstractions.

| Aspect | Approach | Why Simple |
|--------|----------|------------|
| Session Storage | Single BBolt bucket | Reuses existing storage patterns |
| Annotation Display | Reusable Vue component | DRY across server details and history |
| Session Filtering | Query parameter | Simple, URL-shareable |
