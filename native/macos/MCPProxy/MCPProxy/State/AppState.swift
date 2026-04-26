// AppState.swift
// MCPProxy
//
// Root observable state for the MCPProxy tray application.
// All UI views bind to this single source of truth.
//
// Type reuse:
//   - CoreState, CoreError, CoreOwnership, ReconnectionPolicy -> Core/CoreState.swift
//   - ServerStatus, ActivityEntry, HealthStatus, etc.          -> API/Models.swift

import Foundation
import Combine

// MARK: - Health Indicator (tray icon badge)

/// Tray icon badge level, derived from aggregated server health.
enum HealthIndicator: String, Sendable {
    case healthy
    case degraded
    case unhealthy
    case disconnected
}

// MARK: - App State

/// The root observable state object for the entire tray application.
/// All views bind to properties on this object.
///
/// Uses ObservableObject (not @Observable) for macOS 13 compatibility.
/// Server and activity data use the Codable model types from `API/Models.swift`.
/// Core lifecycle state uses the state machine from `Core/CoreState.swift`.
final class AppState: ObservableObject {

    // MARK: Core lifecycle

    /// Current core process state (uses CoreState from CoreState.swift).
    @Published var coreState: CoreState = .idle

    /// Who owns the core process.
    @Published var ownership: CoreOwnership = .trayManaged

    // MARK: Server inventory (ServerStatus from Models.swift)

    @Published var servers: [ServerStatus] = []
    @Published var connectedCount: Int = 0
    @Published var totalServers: Int = 0
    @Published var totalTools: Int = 0

    /// Set to true once the tray has received its first response from
    /// `/api/v1/servers`. Used by `statusSummary` to distinguish "haven't
    /// fetched yet" from "fetched and the list is genuinely empty", so the
    /// menu shows "Loading…" instead of misleading "No servers configured"
    /// during the cold-start window after the core becomes reachable.
    @Published var serversLoaded: Bool = false

    // MARK: Activity & security (ActivityEntry from Models.swift)

    @Published var recentActivity: [ActivityEntry] = []
    @Published var recentSessions: [APIClient.MCPSession] = []
    @Published var sensitiveDataAlertCount: Int = 0
    @Published var quarantinedToolsCount: Int = 0

    /// Monotonic counter bumped on each SSE activity event for live updates.
    @Published var activityVersion: Int = 0

    /// Monotonic counter bumped on SSE servers.changed / config.reloaded for live updates.
    @Published var serversVersion: Int = 0

    // MARK: Token metrics (from status response)

    @Published var tokenMetrics: TokenMetrics?

    // MARK: API Client (shared with all views via AppState)

    /// The API client for the running core, set once connected.
    /// Views read this instead of receiving it as a parameter,
    /// which avoids the need to replace NSHostingView when the client becomes available.
    @Published var apiClient: APIClient?

    // MARK: Security status

    @Published var dockerAvailable: Bool = false
    @Published var quarantineEnabled: Bool = true

    // MARK: Metadata

    @Published var version: String = ""
    @Published var updateAvailable: String? = nil
    @Published var autoStartEnabled: Bool = false

    /// Base URL for the Web UI, populated from /api/v1/info on connect.
    /// Falls back to localhost:8080 until the actual URL is fetched.
    @Published var webUIBaseURL: String = "http://127.0.0.1:8080"

    /// Whether the user has explicitly stopped MCPProxy (distinct from idle/error states).
    @Published var isStopped: Bool = false

    /// User-adjustable font scale (1.0 = default, persisted in UserDefaults).
    /// Standard macOS Cmd+/Cmd- changes this by 0.1 increments.
    @Published var fontScale: CGFloat = UserDefaults.standard.double(forKey: "fontScale") == 0
        ? 1.0 : CGFloat(UserDefaults.standard.double(forKey: "fontScale")) {
        didSet { UserDefaults.standard.set(Double(fontScale), forKey: "fontScale") }
    }

    // MARK: Computed properties

    /// Servers that need user intervention — NOT including intentionally disabled servers.
    /// Only: auth required (login), connection errors (restart), quarantine (approve).
    var serversNeedingAttention: [ServerStatus] {
        servers.filter { server in
            guard let action = server.health?.action, !action.isEmpty else { return false }
            // "enable" means disabled by user — intentional, not attention-worthy
            return action != "enable"
        }
    }

    /// Spec 044 — servers that have an attached, classified diagnostic with
    /// warn/error severity. These drive the "Fix issues" menu group and the
    /// tray badge tint.
    var serversWithDiagnostic: [ServerStatus] {
        servers.filter { $0.hasAttentionDiagnostic }
    }

    /// Highest-severity diagnostic across enabled servers. Returns nil when
    /// no diagnostics are attached. Used by TrayIcon to colour the badge.
    var worstDiagnosticSeverity: String? {
        var sawWarn = false
        for srv in servers where srv.enabled {
            guard let d = srv.diagnostic else { continue }
            if d.severity == "error" { return "error" }
            if d.severity == "warn" { sawWarn = true }
        }
        return sawWarn ? "warn" : nil
    }

    /// Aggregate health indicator for the tray icon badge.
    /// Only considers ENABLED servers. Disabled servers are intentional — don't flag them.
    /// Uses majority-based logic: green if most are healthy, yellow if some degraded,
    /// red only if the majority are unhealthy.
    var healthLevel: HealthIndicator {
        guard coreState == .connected else {
            return .disconnected
        }

        let enabled = servers.filter { $0.enabled }
        if enabled.isEmpty {
            return .healthy
        }

        let unhealthyCount = enabled.filter { $0.health?.level == "unhealthy" }.count
        let degradedCount = enabled.filter { $0.health?.level == "degraded" }.count
        let total = enabled.count

        // Red only if more than half of enabled servers are unhealthy
        if unhealthyCount > total / 2 {
            return .unhealthy
        }
        // Yellow if any degraded or unhealthy (but not majority)
        if unhealthyCount > 0 || degradedCount > 0 {
            return .degraded
        }
        return .healthy
    }

    /// Whether the tray is connected to a running core.
    var isConnected: Bool {
        coreState == .connected
    }

    /// One-line summary suitable for display in the menu header.
    var statusSummary: String {
        if isStopped { return "Stopped" }
        switch coreState {
        case .connected:
            if !serversLoaded {
                return "Loading…"
            }
            if totalServers == 0 {
                return "No servers configured"
            }
            return "\(connectedCount)/\(totalServers) servers, \(totalTools) tools"
        case .idle:
            return "Idle"
        default:
            return coreState.displayName
        }
    }

    // MARK: Mutating helpers (called from background actors via MainActor)

    /// Update server list and recompute derived counts.
    /// Only publishes changes when the data actually differs to prevent
    /// MenuBarExtra from duplicating menu items on spurious re-renders.
    @MainActor
    func updateServers(_ newServers: [ServerStatus]) {
        let newConnected = newServers.filter { $0.connected }.count
        let newTools = newServers.reduce(0) { $0 + $1.toolCount }
        let newQuarantined = newServers.filter { $0.quarantined }.count

        // Always update server array on servers.changed events.
        // Health status, connection state, and tool counts can change
        // even when the server list itself hasn't changed.
        servers = newServers
        if totalServers != newServers.count { totalServers = newServers.count }
        if connectedCount != newConnected { connectedCount = newConnected }
        if totalTools != newTools { totalTools = newTools }
        if quarantinedToolsCount != newQuarantined { quarantinedToolsCount = newQuarantined }
        if !serversLoaded { serversLoaded = true }
    }

    /// Replace the recent activity list.
    /// Only publishes changes when the data actually differs.
    @MainActor
    func updateActivity(_ entries: [ActivityEntry]) {
        let newIDs = entries.map(\.id)
        let oldIDs = recentActivity.map(\.id)
        if newIDs != oldIDs {
            recentActivity = entries
        }
        let newSensitive = entries.filter { $0.hasSensitiveData == true }.count
        if sensitiveDataAlertCount != newSensitive { sensitiveDataAlertCount = newSensitive }
    }

    /// Transition the core state on the main actor.
    @MainActor
    func transition(to newState: CoreState) {
        coreState = newState
    }
}
