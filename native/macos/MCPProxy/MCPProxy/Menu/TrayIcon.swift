// TrayIcon.swift
// MCPProxy
//
// The label view for the MenuBarExtra. Renders a template icon in the
// menu bar that reflects the aggregate health level of the proxy.
// Spec 044 adds a small dot overlay keyed off the worst classified
// diagnostic severity across enabled servers — red for error, orange
// for warn — so users see attention-worthy failures at a glance.

import SwiftUI

struct TrayIcon: View {
    @ObservedObject var appState: AppState

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Image(systemName: iconName)
                .symbolRenderingMode(.hierarchical)
            if let badge = diagnosticBadgeColor {
                Circle()
                    .fill(badge)
                    .frame(width: 5, height: 5)
                    .offset(x: 2, y: -2)
                    .accessibilityLabel(
                        appState.worstDiagnosticSeverity == "error"
                            ? "Server errors present"
                            : "Server warnings present"
                    )
            }
        }
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

    /// Spec 044 — colour the badge dot based on the worst classified
    /// diagnostic severity. Returns nil when no diagnostics are attached,
    /// which keeps the plain monochrome icon for healthy users.
    private var diagnosticBadgeColor: Color? {
        switch appState.worstDiagnosticSeverity {
        case "error": return .red
        case "warn":  return .orange
        default:      return nil
        }
    }
}
