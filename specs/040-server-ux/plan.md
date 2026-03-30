# Implementation Plan: Add/Edit Server UX Improvements

**Branch**: `040-server-ux` | **Date**: 2026-03-30 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/040-server-ux/spec.md`

## Summary

Improve the macOS tray app's server management UX: fix Add Server sheet (larger, validation, connection feedback, simplified protocols), add Edit Server capability to the Config tab, improve Import flow with preview step, enhance error visibility with log coloring and tooltips, and fix keyboard shortcuts.

## Technical Context

**Language/Version**: Swift 5.9 (macOS tray app), Go 1.24 (backend PATCH endpoint)
**Primary Dependencies**: SwiftUI, AppKit (NSTableView, NSOpenPanel), Chi router (Go)
**Storage**: BBolt (config.db), JSON config file (~/.mcpproxy/mcp_config.json)
**Testing**: mcpproxy-ui-test MCP tool (Swift UI verification), go test (backend)
**Target Platform**: macOS 13+ (arm64/x86_64)
**Project Type**: Desktop app + backend API
**Performance Goals**: Sheet opens <200ms, validation feedback <100ms, connection test feedback within 10s
**Constraints**: Must follow Apple HIG patterns. Tray app must not maintain its own state (constitution III).
**Scale/Scope**: 7 Swift view files, 1 Go API file, ~15 functional requirements

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | UI changes only, no impact on search/indexing |
| II. Actor-Based Concurrency | PASS | Swift async/await for API calls, no new concurrency patterns |
| III. Configuration-Driven | PASS | Edit saves via REST API to core config. Tray has no local state. |
| IV. Security by Default | PASS | New servers still quarantined by default. Edit exposes quarantine toggle. |
| V. TDD | PASS | Go PATCH endpoint tested. Swift verified via mcpproxy-ui-test. |
| VI. Documentation Hygiene | PASS | CLAUDE.md updated if API changes. |

No violations. No complexity tracking needed.

## Project Structure

### Documentation (this feature)

```text
specs/040-server-ux/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0 research
├── data-model.md        # Phase 1 data model
├── quickstart.md        # Phase 1 quickstart
├── contracts/           # Phase 1 API contracts
│   └── patch-server.yaml
└── checklists/
    └── requirements.md
```

### Source Code (repository root)

```text
# Backend (Go) - PATCH endpoint
internal/httpapi/server.go          # Add handlePatchServer handler

# macOS Tray App (Swift)
native/macos/MCPProxy/MCPProxy/
├── Views/
│   ├── AddServerView.swift         # Sheet size, validation, connection test, protocol picker
│   ├── ServerDetailView.swift      # Editable Config tab
│   ├── ServersView.swift           # Empty state, status tooltip
│   └── DashboardView.swift         # Accessibility labels, import button tab param
├── API/
│   ├── APIClient.swift             # updateServer(), import timeout
│   └── Models.swift                # UpdateServerRequest if needed
└── MCPProxyApp.swift               # Cmd+N command group
```

**Structure Decision**: Modifications to existing files only. No new files needed except the PATCH API contract.
