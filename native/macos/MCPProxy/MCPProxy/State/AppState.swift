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

    // MARK: Metadata

    @Published var version: String = ""
    @Published var updateAvailable: String? = nil
    @Published var autoStartEnabled: Bool = false

    // MARK: Computed properties

    /// Servers that have a health action the user should take.
    var serversNeedingAttention: [ServerStatus] {
        servers.filter { server in
            guard let action = server.health?.action, !action.isEmpty else { return false }
            return true
        }
    }

    /// Aggregate health indicator for the tray icon badge.
    var healthLevel: HealthIndicator {
        guard coreState == .connected else {
            return .disconnected
        }
        if servers.isEmpty {
            return .healthy
        }

        let hasUnhealthy = servers.contains { $0.health?.level == "unhealthy" }
        let hasDegraded = servers.contains { $0.health?.level == "degraded" }

        if hasUnhealthy {
            return .unhealthy
        } else if hasDegraded {
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
