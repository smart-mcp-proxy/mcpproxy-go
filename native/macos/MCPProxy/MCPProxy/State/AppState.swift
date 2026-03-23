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
import Observation

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
/// Server and activity data use the Codable model types from `API/Models.swift`.
/// Core lifecycle state uses the state machine from `Core/CoreState.swift`.
@Observable
final class AppState {

    // MARK: Core lifecycle

    /// Current core process state (uses CoreState from CoreState.swift).
    var coreState: CoreState = .idle

    /// Who owns the core process.
    var ownership: CoreOwnership = .trayManaged

    // MARK: Server inventory (ServerStatus from Models.swift)

    var servers: [ServerStatus] = []
    var connectedCount: Int = 0
    var totalServers: Int = 0
    var totalTools: Int = 0

    // MARK: Activity & security (ActivityEntry from Models.swift)

    var recentActivity: [ActivityEntry] = []
    var sensitiveDataAlertCount: Int = 0
    var quarantinedToolsCount: Int = 0

    // MARK: Metadata

    var version: String = ""
    var updateAvailable: String? = nil
    var autoStartEnabled: Bool = false

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
    @MainActor
    func updateServers(_ newServers: [ServerStatus]) {
        servers = newServers
        totalServers = newServers.count
        connectedCount = newServers.filter { server in
            if let health = server.health {
                return health.level == "healthy"
            }
            return server.connected
        }.count
        totalTools = newServers.reduce(0) { $0 + $1.toolCount }
        quarantinedToolsCount = newServers.filter { $0.quarantined }.count
    }

    /// Replace the recent activity list.
    @MainActor
    func updateActivity(_ entries: [ActivityEntry]) {
        recentActivity = entries
        sensitiveDataAlertCount = entries.filter { $0.hasSensitiveData == true }.count
    }

    /// Transition the core state on the main actor.
    @MainActor
    func transition(to newState: CoreState) {
        coreState = newState
    }
}
