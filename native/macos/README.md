# MCPProxy macOS Tray Application

Native macOS system tray application built with Swift and SwiftUI.

## Status

Placeholder — not yet implemented. The current tray app is at `cmd/mcpproxy-tray/` (Go + systray).

## Requirements

- Xcode 15+
- macOS 13+ (Ventura)
- Swift 5.9+

## Architecture

The native tray app replaces `cmd/mcpproxy-tray/` with a platform-native experience:
- Manages the core `mcpproxy` server lifecycle
- Communicates via Unix socket (`~/.mcpproxy/mcpproxy.sock`) and REST API
- Subscribes to SSE (`/events`) for real-time status updates
- Provides native macOS menu bar integration
