// MainWindow.swift
// MCPProxy
//
// Root SwiftUI view for the main application window.
// Uses NavigationSplitView with a sidebar for navigation between sections.
// Requires macOS 13+ for NavigationSplitView support.

import SwiftUI

// MARK: - Sidebar Navigation

/// Sidebar items for the main window navigation.
enum SidebarItem: String, CaseIterable, Identifiable {
    case servers = "Servers"
    case activity = "Activity Log"
    case tokens = "Agent Tokens"
    case config = "Configuration"

    var id: String { rawValue }

    /// SF Symbol name for the sidebar icon.
    var icon: String {
        switch self {
        case .servers: return "server.rack"
        case .activity: return "clock.arrow.circlepath"
        case .tokens: return "person.badge.key"
        case .config: return "gearshape"
        }
    }
}

// MARK: - Main Window View

/// The root view hosted inside the main NSWindow.
/// Provides a sidebar with navigation to all major sections of the app.
struct MainWindow: View {
    @ObservedObject var appState: AppState
    let apiClient: APIClient?
    @State private var selectedItem: SidebarItem? = .servers

    var body: some View {
        NavigationSplitView {
            List(SidebarItem.allCases, selection: $selectedItem) { item in
                Label(item.rawValue, systemImage: item.icon)
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
            .navigationTitle("MCPProxy")
        } detail: {
            if let selected = selectedItem {
                detailView(for: selected)
            } else {
                Text("Select an item from the sidebar")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(minWidth: 800, minHeight: 500)
    }

    @ViewBuilder
    private func detailView(for item: SidebarItem) -> some View {
        switch item {
        case .servers:
            ServersView(appState: appState, apiClient: apiClient)
        case .activity:
            ActivityView(appState: appState, apiClient: apiClient)
        case .tokens:
            TokensView(apiClient: apiClient)
        case .config:
            ConfigView(apiClient: apiClient)
        }
    }
}
