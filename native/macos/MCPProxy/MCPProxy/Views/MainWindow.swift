// MainWindow.swift
// MCPProxy

import SwiftUI

enum SidebarItem: String, CaseIterable, Identifiable {
    case servers = "Servers"
    case activity = "Activity Log"
    case secrets = "Secrets"
    case config = "Configuration"

    var id: String { rawValue }
    var icon: String {
        switch self {
        case .servers: return "server.rack"
        case .activity: return "clock.arrow.circlepath"
        case .secrets: return "key.fill"
        case .config: return "gearshape"
        }
    }
}

struct MainWindow: View {
    @ObservedObject var appState: AppState
    @State private var selectedItem: SidebarItem? = .servers

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedItem) {
                ForEach(SidebarItem.allCases) { item in
                    Label(item.rawValue, systemImage: item.icon)
                        .tag(item)
                        .accessibilityIdentifier("sidebar-\(item.rawValue)")
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
            .listStyle(.sidebar)
            .accessibilityIdentifier("sidebar-list")
        } detail: {
            Group {
                switch selectedItem ?? .servers {
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
