// AutostartSidecarService.swift
// MCPProxy
//
// Spec 044 (T055): exposes the tray's current login-item status to the
// core via a read-only sidecar file at ~/.mcpproxy/tray-autostart.json.
// The core's telemetry.AutostartReader consumes this file with a 1h TTL
// cache. Pragmatic substitute for a tray-hosted HTTP /autostart endpoint —
// same semantics, zero extra listener surface area.
//
// The tray writes on three occasions:
//   1. At app launch (boot), after reading current SMAppService state.
//   2. Immediately after FirstRunDialog enables/skips the login item.
//   3. Whenever the user toggles the "Launch at login" menu item.

import Foundation

/// Writes the tray-owned autostart sidecar consumed by the core's telemetry
/// service.
///
/// Writes are best-effort: if the filesystem call fails (disk full, permission
/// denied, …) the tray logs and moves on. A stale sidecar is preferable to
/// crashing the app over telemetry plumbing.
enum AutostartSidecarService {

    /// Returns `~/.mcpproxy/tray-autostart.json`.
    private static var sidecarURL: URL {
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home
            .appendingPathComponent(".mcpproxy", isDirectory: true)
            .appendingPathComponent("tray-autostart.json", isDirectory: false)
    }

    /// Sidecar JSON schema. Matches `autostartSidecar` in
    /// `internal/telemetry/autostart.go`.
    private struct Sidecar: Encodable {
        let enabled: Bool
        let updated_at: String
    }

    /// Synchronize the sidecar with the current SMAppService state. Safe to
    /// call frequently — cheap stat + write only when value changes.
    static func refresh() {
        let enabled = AutoStartService.isEnabled
        write(enabled: enabled)
    }

    /// Explicitly publish a new enabled value — used right after the user
    /// toggles the setting so the core sees fresh state within the 1h TTL.
    static func write(enabled: Bool) {
        let payload = Sidecar(
            enabled: enabled,
            updated_at: ISO8601DateFormatter().string(from: Date())
        )
        guard let data = try? JSONEncoder().encode(payload) else {
            NSLog("[MCPProxy] AutostartSidecar: encode failed")
            return
        }
        let url = sidecarURL
        do {
            try FileManager.default.createDirectory(
                at: url.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
            try data.write(to: url, options: .atomic)
        } catch {
            NSLog("[MCPProxy] AutostartSidecar: write failed: %@", error.localizedDescription)
        }
    }
}
