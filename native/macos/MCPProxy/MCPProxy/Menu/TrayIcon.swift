// TrayIcon.swift
// MCPProxy
//
// The label view for the MenuBarExtra. Renders a template icon in the
// menu bar that reflects the aggregate health level of the proxy.

import SwiftUI

struct TrayIcon: View {
    @ObservedObject var appState: AppState

    var body: some View {
        Image(systemName: iconName)
            .symbolRenderingMode(.hierarchical)
    }

    /// SF Symbol name based on current health indicator.
    private var iconName: String {
        switch appState.healthLevel {
        case .healthy:
            return "server.rack"
        case .degraded:
            return "exclamationmark.triangle"
        case .unhealthy:
            return "xmark.circle"
        case .disconnected:
            return "antenna.radiowaves.left.and.right.slash"
        }
    }
}
