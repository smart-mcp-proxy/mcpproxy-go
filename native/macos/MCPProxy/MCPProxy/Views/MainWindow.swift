// MainWindow.swift
// MCPProxy
//
// Root SwiftUI view for the main application window.
// Uses NavigationSplitView with a sidebar for navigation between sections.
//
// The apiClient is read from appState (not passed as a parameter) so that
// the window never needs to be recreated when the client becomes available.

import SwiftUI

// MARK: - Sidebar Navigation

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

// MARK: - Main Window View

struct MainWindow: View {
    @ObservedObject var appState: AppState
    @State private var selectedItem: SidebarItem? = .servers

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedItem) {
                ForEach(SidebarItem.allCases) { item in
                    Label(item.rawValue, systemImage: item.icon)
                        .tag(item)
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
            .listStyle(.sidebar)
        } detail: {
            detailView(for: selectedItem ?? .servers)
        }
        .frame(minWidth: 800, minHeight: 500)
    }

    @ViewBuilder
    private func detailView(for item: SidebarItem) -> some View {
        switch item {
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
}
