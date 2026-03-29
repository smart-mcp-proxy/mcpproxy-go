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
            .environment(\.fontScale, appState.fontScale)
            .accessibilityIdentifier("detail-view")
        }
        .frame(minWidth: 800, minHeight: 500)
        .onReceive(NotificationCenter.default.publisher(for: .switchToActivity)) { _ in
            selectedItem = .activity
        }
        .onReceive(NotificationCenter.default.publisher(for: .switchToServers)) { _ in
            selectedItem = .servers
        }
    }

    // MARK: - Core Status Banner

    @ViewBuilder
    private var coreStatusBanner: some View {
        let isStopped = appState.isStopped
        let bannerColor: Color = isStopped ? .orange : .red
        let bannerIcon: String = isStopped ? "stop.circle.fill" : "exclamationmark.triangle.fill"
        let bannerText: String = {
            if isStopped { return "MCPProxy Core is stopped" }
            if case .idle = appState.coreState { return "MCPProxy Core is not running" }
            if case .error(let err) = appState.coreState { return "MCPProxy Core error: \(err.userMessage)" }
            return "MCPProxy Core: \(appState.coreState.displayName)"
        }()
        let fontScale = appState.fontScale

        HStack(spacing: 10) {
            Image(systemName: bannerIcon)
                .font(.scaled(.title3, scale: fontScale))
                .foregroundStyle(bannerColor)

            Text(bannerText)
                .font(.scaled(.subheadline, scale: fontScale).weight(.medium))

            Spacer()

            if isStopped {
                Button("Start") {
                    NotificationCenter.default.post(name: .startCore, object: nil)
                }
                .buttonStyle(.borderedProminent)
                .tint(.orange)
                .controlSize(.small)
            } else if appState.coreState == .idle || appState.coreState.canLaunch {
                Button("Start") {
                    NotificationCenter.default.post(name: .startCore, object: nil)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(bannerColor.opacity(0.15))
    }

}
