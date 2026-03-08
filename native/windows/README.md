# MCPProxy Windows Tray Application

Native Windows system tray application built with C# and WPF.

## Status

Placeholder — not yet implemented. The current tray app is at `cmd/mcpproxy-tray/` (Go + systray).

## Requirements

- .NET 8+ SDK
- Visual Studio 2022+ or VS Code with C# extension
- Windows 10+

## Architecture

The native tray app replaces `cmd/mcpproxy-tray/` with a platform-native experience:
- Manages the core `mcpproxy` server lifecycle
- Communicates via named pipe (`\\.\pipe\mcpproxy-<username>`) and REST API
- Subscribes to SSE (`/events`) for real-time status updates
- Provides native Windows notification area integration
