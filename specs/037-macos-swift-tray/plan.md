# Implementation Plan: Native macOS Swift Tray App (Spec A)

**Branch**: `037-macos-swift-tray` | **Date**: 2026-03-23 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/037-macos-swift-tray/spec.md`

## Summary

Build a native macOS menu bar application in Swift that replaces the existing Go+systray tray app on macOS. The app uses SwiftUI `MenuBarExtra` (macOS 13+) with AppKit escape hatches for custom menu item views. It manages the MCPProxy core process lifecycle (launch, monitor, shutdown), communicates via Unix socket + REST API, subscribes to SSE for real-time updates, sends native macOS notifications for security events, and integrates Sparkle 2.x for auto-updates from GitHub Releases.

## Technical Context

**Language/Version**: Swift 5.9+ / Xcode 15+
**Primary Dependencies**: SwiftUI, AppKit (escape hatches), Sparkle 2.x (SPM), Foundation (URLSession, Process, UNUserNotificationCenter)
**Storage**: N/A (tray reads all state from core via REST API — no local persistence per Constitution III)
**Testing**: Swift unit tests (XCTest) for state machine, API models, process management logic. Integration tests against a mock HTTP server.
**Target Platform**: macOS 13.0 Ventura and later (arm64 + x86_64 universal binary)
**Project Type**: Native macOS app (single Xcode project)
**Performance Goals**: Menu appears within 200ms of click; state updates within 2s of SSE event; <50MB steady-state memory
**Constraints**: No dock icon (LSUIElement=true); must coexist with existing Go tray (Windows); must work with bundled Go binary
**Scale/Scope**: ~20 Swift source files, single menu bar app

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Tray app is a thin UI client. Performance-critical search/indexing stays in Go core. |
| II. Actor-Based Concurrency | PASS | Swift actors map directly to this principle. CoreProcessManager and APIClient are actors. |
| III. Configuration-Driven | PASS | Tray stores no state — reads/writes core config via REST API. |
| IV. Security by Default | PASS | Socket communication (no API key needed), quarantine display, sensitive data alerts. |
| V. Test-Driven Development | PASS | Unit tests for state machine, models, process management. Integration tests against mock server. |
| VI. Documentation Hygiene | PASS | CLAUDE.md and README.md updated. Build scripts documented. |

| Constraint | Status | Notes |
|------------|--------|-------|
| Core+Tray Split | PASS | Swift app is the tray; Go binary is the core. Separate processes. |
| Event-Driven Updates | PASS | SSE subscription drives all state updates. |
| DDD Layering | N/A | DDD applies to Go core, not the Swift thin client. |
| Upstream Client Modularity | N/A | Applies to Go MCP client layers, not Swift. |

**Gate Result**: PASS — no violations.

## Project Structure

### Documentation (this feature)

```text
specs/037-macos-swift-tray/
├── spec.md              # Feature specification
├── plan.md              # This file
├── research.md          # Phase 0: technology research
├── data-model.md        # Phase 1: state models
├── quickstart.md        # Phase 1: build & run guide
├── contracts/           # Phase 1: API contracts consumed
└── tasks.md             # Phase 2: implementation tasks
```

### Source Code (repository root)

```text
native/macos/MCPProxy/
├── MCPProxy.xcodeproj/          # Xcode project
├── Package.swift                # SPM: Sparkle dependency
├── MCPProxy/
│   ├── MCPProxyApp.swift        # @main entry, MenuBarExtra scene
│   ├── Info.plist               # Bundle config, LSUIElement, Sparkle keys
│   ├── MCPProxy.entitlements    # Network, files, JIT entitlements
│   ├── Assets.xcassets/         # Tray icon (template), app icon
│   │
│   ├── Core/                    # Core process lifecycle
│   │   ├── CoreProcessManager.swift   # Actor: launch/monitor/kill
│   │   ├── CoreState.swift            # State machine enum + transitions
│   │   └── SocketTransport.swift      # Unix socket URLProtocol
│   │
│   ├── API/                     # REST API + SSE clients
│   │   ├── APIClient.swift            # Actor: async/await REST calls
│   │   ├── SSEClient.swift            # SSE stream → AsyncStream
│   │   └── Models.swift               # Codable response types
│   │
│   ├── Menu/                    # Tray menu views
│   │   ├── TrayMenu.swift             # MenuBarExtra content
│   │   ├── ServerSubmenu.swift        # Per-server action submenu
│   │   ├── StatusMenuItem.swift       # NSViewRepresentable for health dots
│   │   └── QuarantineSection.swift    # Quarantined tools section
│   │
│   ├── Services/                # App services
│   │   ├── AutoStartService.swift     # SMAppService login item
│   │   ├── NotificationService.swift  # UNUserNotificationCenter
│   │   ├── UpdateService.swift        # Sparkle SPUStandardUpdaterController
│   │   └── SymlinkService.swift       # /usr/local/bin symlink
│   │
│   └── State/                   # Observable app state
│       ├── AppState.swift             # @Observable root state
│       └── ServerState.swift          # Per-server model
│
├── MCPProxyTests/               # Unit tests
│   ├── CoreStateTests.swift           # State machine transitions
│   ├── ModelsTests.swift              # JSON decoding
│   ├── SSEParserTests.swift           # SSE event parsing
│   └── NotificationServiceTests.swift # Rate limiting logic
│
└── scripts/
    └── build-macos-tray.sh      # Build + sign + notarize script
```

**Structure Decision**: New Xcode project under `native/macos/MCPProxy/` — the existing placeholder directory. This is a standalone macOS app project, not a package. The Go core binary is bundled into `Contents/Resources/bin/` during the build phase (handled by `scripts/create-dmg.sh` adaptation).

## Complexity Tracking

No constitution violations. No complexity justification needed.
