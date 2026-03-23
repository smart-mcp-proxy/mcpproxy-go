# Research: Native macOS Swift Tray App

**Feature**: 037-macos-swift-tray
**Date**: 2026-03-23

## 1. MenuBarExtra with Dynamic Content

**Decision**: Use SwiftUI `MenuBarExtra` with `isInserted` binding and AppKit `NSViewRepresentable` for custom menu item views (health dots, attributed strings).

**Rationale**: `MenuBarExtra` (macOS 13+) is Apple's official SwiftUI API for menu bar apps. It provides declarative menu content that auto-updates from `@Observable` state. For custom views (colored dots, secondary text), we wrap `NSView` via `NSViewRepresentable` in menu items.

**Limitations discovered**:
- macOS 13: Menu items are limited to Text, Image, Button, Toggle, Divider, and sub-Menus. Custom views in menu items require macOS 14+ or AppKit escape hatches.
- Dynamic submenus work well with `ForEach` over observable arrays.
- `MenuBarExtra` does not support NSPopover-style content on macOS 13 (only menu style).

**Approach**: Use `.menuBarExtraStyle(.menu)` for the tray. For items needing custom rendering (health status dots, dimmed secondary text), use `NSMenuItem` with `NSHostingView` set as the menu item's `view` property via AppKit bridging.

**Alternatives considered**:
- Pure NSStatusItem + NSMenu (AppKit only): More control but loses SwiftUI declarative benefits
- MenuBarExtra with .window style: Not appropriate for a menu — opens a window instead

## 2. Unix Socket HTTP Transport

**Decision**: Implement a custom `URLProtocol` subclass that connects to Unix domain sockets via `NWConnection` (Network.framework) or raw Darwin sockets.

**Rationale**: Foundation's `URLSession` doesn't natively support Unix domain sockets. A custom `URLProtocol` lets us transparently route HTTP requests over the socket while keeping the APIClient using standard `URLSession` patterns.

**Implementation approach**:
1. Subclass `URLProtocol` → `SocketURLProtocol`
2. Override `canInit(with:)` to match requests with a custom URL scheme (e.g., `unix://`)
3. In `startLoading()`: open a Unix socket connection, write HTTP/1.1 request, parse response
4. Register the protocol on a dedicated `URLSession` configuration

**Alternatives considered**:
- Raw socket + custom HTTP parsing: Works but reinvents URLSession's response handling
- NIO/SwiftNIO: Too heavy a dependency for a simple tray app
- Using `curl` via Process: Hacky, adds external dependency

## 3. Sparkle 2.x Integration

**Decision**: Add Sparkle 2.x via Swift Package Manager. Use `SPUStandardUpdaterController` for the standard update UI.

**Rationale**: Sparkle is the de facto standard for macOS app auto-updates outside the Mac App Store. Version 2.x has native Swift support and SPM distribution.

**Setup requirements**:
1. Add SPM dependency: `https://github.com/sparkle-project/Sparkle` (2.x)
2. Generate EdDSA key pair: `./bin/generate_keys` from Sparkle tools
3. Add to Info.plist: `SUFeedURL` (appcast URL), `SUPublicEDKey` (public key)
4. Initialize `SPUStandardUpdaterController` in app lifecycle
5. Wire "Check for Updates" menu item to `updaterController.checkForUpdates(_:)`

**Appcast hosting**: Generate `appcast.xml` in CI using `generate_appcast` tool. Host at `https://mcpproxy.app/appcast.xml`. Each entry references the DMG download URL on GitHub Releases.

**Alternatives considered**:
- Mac App Store updates: Requires App Store distribution, sandboxing constraints
- Custom updater: Unnecessary complexity when Sparkle exists
- GitHub API polling + manual download: No in-place update, poor UX

## 4. SMAppService for Login Items

**Decision**: Use `SMAppService.mainApp` (macOS 13+) for login item registration.

**Rationale**: SMAppService is Apple's modern API for managing login items, replacing the deprecated `SMLoginItemSetEnabled` and `LSSharedFileListInsertItemURL`. It's a one-liner in Swift:

```swift
// Register
try SMAppService.mainApp.register()

// Unregister
try SMAppService.mainApp.unregister()

// Check status
SMAppService.mainApp.status == .enabled
```

**Alternatives considered**:
- LaunchAgent plist: More complex, requires file management
- LSSharedFileList: Deprecated since macOS 13
- SMLoginItemSetEnabled: Deprecated, requires helper bundle

## 5. Notifications with UNUserNotificationCenter

**Decision**: Use `UNUserNotificationCenter` with custom categories and actionable notifications.

**Rationale**: UNUserNotificationCenter is the standard macOS notification API. It supports:
- Rich notifications with titles, subtitles, body text
- Actionable buttons (UNNotificationAction) grouped in categories (UNNotificationCategory)
- Delegate for handling user interaction with notification actions

**Category setup**:
- `SENSITIVE_DATA`: actions = [View Details, Dismiss]
- `QUARANTINE`: actions = [Review Tools, Dismiss]
- `OAUTH_EXPIRY`: actions = [Re-authenticate, Dismiss]
- `CORE_CRASH`: actions = [Restart, Quit]
- `UPDATE`: actions = [Install & Relaunch, Later]

**Rate limiting**: Implemented in `NotificationService` using a dictionary of `[String: Date]` keyed by `"{eventType}:{serverName}"`. Suppress duplicates within 5-minute window.

**Alternatives considered**:
- NSUserNotification: Deprecated since macOS 11
- Growl: Dead project, not maintained

## 6. Process Management

**Decision**: Use `Process` class (Foundation) with `DispatchSource.makeProcessSource` for monitoring and `process.terminationHandler` for exit detection.

**Rationale**: Foundation's `Process` is the Swift equivalent of Go's `exec.Cmd`. It supports:
- Setting executable path, arguments, environment
- Capturing stdout/stderr via `Pipe`
- Termination handler callback
- Signal sending via `process.terminate()` (SIGTERM) and `kill(pid, SIGKILL)`

**Exit code parsing**: `process.terminationStatus` maps to MCPProxy exit codes (2=port, 3=DB, 4=config, 5=perms).

**Process monitoring**: `DispatchSource.makeProcessSource(identifier:, flags: .exit, queue:)` fires when the child process exits, even if the tray isn't actively polling.

**Alternatives considered**:
- posix_spawn directly: Lower level, no advantage over Process
- XPC service: Overkill for a simple child process

## 7. SSE (Server-Sent Events) in Swift

**Decision**: Implement SSE parsing on top of `URLSession.bytes(for:)` async sequence (macOS 13+), delivering events via `AsyncStream<SSEEvent>`.

**Rationale**: URLSession's async bytes API provides a natural streaming interface. SSE is a simple text protocol (event:, data:, retry: lines separated by blank lines) that's easy to parse incrementally.

**Implementation approach**:
1. Create a `URLRequest` with `Accept: text/event-stream`
2. Use `URLSession.shared.bytes(for: request)` to get `AsyncBytes`
3. Buffer lines, parse SSE fields, emit parsed events via `AsyncStream`
4. Handle reconnection with exponential backoff
5. Monitor the `retry:` field for server-suggested reconnect intervals

**Alternatives considered**:
- EventSource polyfill libraries (LDSwiftEventSource): Adds dependency, we only need basic SSE
- Polling instead of SSE: Higher latency, more API calls
- WebSocket: Core doesn't expose WebSocket, only SSE

## 8. Template Images for Menu Bar

**Decision**: Use a 22x22pt (44x44px @2x) monochrome template image with badge dot compositing via `NSImage` drawing.

**Rationale**: macOS menu bar icons must be template images (monochrome with alpha) to adapt to light/dark mode automatically. Setting `image.isTemplate = true` lets the system handle color inversion.

**Badge implementation**:
1. Start with base template image
2. Create a composite `NSImage` that draws:
   - The base template image
   - A small filled circle (6x6pt) in the bottom-right corner
3. The badge circle uses `NSColor.systemGreen/Yellow/Red` for health status
4. Set `isTemplate = false` on the composite (since it has colors)
5. Re-composite whenever health status changes

**Alternatives considered**:
- SF Symbols: Good for standard icons but custom MCPProxy branding is preferred
- Multiple pre-rendered icons: Inflexible, doesn't handle all color states
- ASCII/emoji in status item title: Looks unprofessional

## Summary of Key Decisions

| Topic | Decision | Key Dependency |
|-------|----------|---------------|
| Menu framework | MenuBarExtra + AppKit escape hatches | SwiftUI (macOS 13) |
| Socket transport | Custom URLProtocol over Unix socket | Foundation |
| Auto-update | Sparkle 2.x via SPM | sparkle-project/Sparkle |
| Login item | SMAppService.mainApp | ServiceManagement |
| Notifications | UNUserNotificationCenter + categories | UserNotifications |
| Process mgmt | Foundation Process + DispatchSource | Foundation |
| SSE client | URLSession.bytes + AsyncStream | Foundation |
| Tray icon | Template image + NSImage badge compositing | AppKit |
