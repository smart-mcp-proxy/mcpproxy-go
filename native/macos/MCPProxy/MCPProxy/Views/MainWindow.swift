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

    var color: Color {
        switch self {
        case .dashboard: return .purple
        case .servers: return .blue
        case .activity: return .orange
        case .secrets: return .yellow
        case .config: return .gray
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
                    Label {
                        Text(item.rawValue)
                            .font(.body)
                    } icon: {
                        Image(systemName: item.icon)
                            .font(.system(size: 14))
                            .foregroundStyle(.white)
                            .frame(width: 28, height: 28)
                            .background(item.color)
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                    }
                    .tag(item)
                    .accessibilityIdentifier("sidebar-\(item.rawValue)")
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 220)
            .listStyle(.sidebar)
            .accessibilityIdentifier("sidebar-list")
        } detail: {
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
            .accessibilityIdentifier("detail-view")
        }
        .frame(minWidth: 800, minHeight: 500)
    }
}
