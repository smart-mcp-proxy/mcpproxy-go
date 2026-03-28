// MainWindow.swift
// MCPProxy

import SwiftUI

enum SidebarItem: String, CaseIterable, Identifiable {
    case dashboard = "Dashboard"
    case servers = "Servers"
    case activity = "Activity Log"
    case secrets = "Secrets"
    case config = "Configuration"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .dashboard: return "rectangle.3.group"
        case .servers: return "server.rack"
        case .activity: return "clock.arrow.circlepath"
        case .secrets: return "key.fill"
        case .config: return "gearshape"
        }
    }

}

struct MainWindow: View {
    @ObservedObject var appState: AppState
    @State private var selectedItem: SidebarItem? = .dashboard

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedItem) {
                ForEach(SidebarItem.allCases) { item in
                    Label(item.rawValue, systemImage: item.icon)
                        .tag(item)
                        .accessibilityIdentifier("sidebar-\(item.rawValue)")
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 220)
            .listStyle(.sidebar)
            .accessibilityIdentifier("sidebar-list")
        } detail: {
            VStack(spacing: 0) {
                // Core status banner — shown when not connected
                if appState.coreState != .connected {
                    coreStatusBanner
                }

                // Regular content
                Group {
                    switch selectedItem ?? .dashboard {
                    case .dashboard:
                        DashboardView(appState: appState)
                    case .servers:
                        ServersView(appState: appState)
                    case .activity:
                        ActivityView(appState: appState)
                    case .secrets:
                        SecretsView(appState: appState)
                    case .config:
                        ConfigView(appState: appState)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .accessibilityIdentifier("detail-view")
            // Apply user-adjustable zoom via Dynamic Type (scales text only, not layout)
            .environment(\.dynamicTypeSize, fontScaleToDynamicType(appState.fontScale))
        }
        .frame(minWidth: 800, minHeight: 500)
    }

    // MARK: - Core Status Banner

    @ViewBuilder
    private var coreStatusBanner: some View {
        let isPaused = appState.isPaused
        let bannerColor: Color = isPaused ? .orange : .red
        let bannerIcon: String = isPaused ? "pause.circle.fill" : "exclamationmark.triangle.fill"
        let bannerText: String = {
            if isPaused { return "MCPProxy Core is paused" }
            if case .idle = appState.coreState { return "MCPProxy Core is not running" }
            if case .error(let err) = appState.coreState { return "MCPProxy Core error: \(err.userMessage)" }
            return "MCPProxy Core: \(appState.coreState.displayName)"
        }()

        HStack(spacing: 10) {
            Image(systemName: bannerIcon)
                .font(.title3)
                .foregroundStyle(bannerColor)

            Text(bannerText)
                .font(.subheadline.weight(.medium))

            Spacer()

            if isPaused {
                Button("Resume") {
                    NotificationCenter.default.post(name: .resumeCore, object: nil)
                }
                .buttonStyle(.borderedProminent)
                .tint(.orange)
                .controlSize(.small)
            } else if appState.coreState == .idle || appState.coreState.canLaunch {
                Button("Start") {
                    NotificationCenter.default.post(name: .resumeCore, object: nil)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(bannerColor.opacity(0.15))
    }

    // MARK: - Font Scale to Dynamic Type

    private func fontScaleToDynamicType(_ scale: CGFloat) -> DynamicTypeSize {
        switch scale {
        case ..<0.8: return .xSmall
        case 0.8..<0.9: return .small
        case 0.9..<1.0: return .medium
        case 1.0..<1.1: return .large
        case 1.1..<1.2: return .xLarge
        case 1.2..<1.4: return .xxLarge
        case 1.4..<1.6: return .xxxLarge
        case 1.6..<1.8: return .accessibility1
        default: return .accessibility2
        }
    }
}
