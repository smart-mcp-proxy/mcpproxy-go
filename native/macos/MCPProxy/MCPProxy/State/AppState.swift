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

    // MARK: Activity & security (ActivityEntry from Models.swift)

    @Published var recentActivity: [ActivityEntry] = []
    @Published var sensitiveDataAlertCount: Int = 0
    @Published var quarantinedToolsCount: Int = 0

    /// Monotonic counter bumped on each SSE activity event for live updates.
    @Published var activityVersion: Int = 0

    // MARK: Token metrics (from status response)

    @Published var tokenMetrics: TokenMetrics?

    // MARK: API Client (shared with all views via AppState)

    /// The API client for the running core, set once connected.
    /// Views read this instead of receiving it as a parameter,
    /// which avoids the need to replace NSHostingView when the client becomes available.
    @Published var apiClient: APIClient?

    // MARK: Metadata

    @Published var version: String = ""
    @Published var updateAvailable: String? = nil
    @Published var autoStartEnabled: Bool = false

    /// Whether the user has explicitly paused MCPProxy (distinct from idle/error states).
    @Published var isPaused: Bool = false

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
        if isPaused { return "Paused" }
        switch coreState {
        case .connected:
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
        let newIDs = newServers.map(\.id).sorted()
        let oldIDs = servers.map(\.id).sorted()
        let newConnected = newServers.filter { $0.connected }.count
        let newTools = newServers.reduce(0) { $0 + $1.toolCount }
        let newQuarantined = newServers.filter { $0.quarantined }.count

        // Only update the server array if the list actually changed
        if newIDs != oldIDs || newConnected != connectedCount || newTools != totalTools {
            servers = newServers
        }
        if totalServers != newServers.count { totalServers = newServers.count }
        if connectedCount != newConnected { connectedCount = newConnected }
        if totalTools != newTools { totalTools = newTools }
        if quarantinedToolsCount != newQuarantined { quarantinedToolsCount = newQuarantined }
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
