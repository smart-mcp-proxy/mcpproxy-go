// MainWindow.swift
// MCPProxy

import SwiftUI

enum SidebarItem: String, CaseIterable, Identifiable {
    case dashboard = "Dashboard"
    case servers = "Servers"
    // F16: BM25 tool discovery is the product's headline feature and had no
    // native home — a tray-first user could not answer "which of my 942 tools
    // does X?" without opening a browser.
    case tools = "Tools"
    case registries = "Registries"
    case activity = "Activity Log"
    case secrets = "Secrets"
    // F5: TokensView was a complete, API-complete create/list/revoke UI that
    // nothing instantiated — a headline security feature reachable from the
    // Web UI and the CLI but not from the app the user has open.
    case tokens = "Agent Tokens"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .dashboard: return "rectangle.3.group"
        case .servers: return "server.rack"
        case .tools: return "wrench.and.screwdriver"
        case .registries: return "books.vertical"
        case .activity: return "clock.arrow.circlepath"
        case .secrets: return "key.fill"
        case .tokens: return "key.horizontal"
        }
    }

}

struct MainWindow: View {
    @ObservedObject var appState: AppState
    @State private var selectedItem: SidebarItem?

    /// `initialTab` seeds the sidebar selection for a window created to land
    /// on a specific section (tray "Open Activity…" → Activity). Once the
    /// window exists, later switches arrive as `.switchToSidebarTab`
    /// notifications instead — state is only readable at creation time.
    init(appState: AppState, initialTab: SidebarItem = .dashboard) {
        self.appState = appState
        _selectedItem = State(initialValue: initialTab)
    }

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedItem) {
                ForEach(SidebarItem.allCases) { item in
                    Label(item.rawValue, systemImage: item.icon)
                        .tag(item)
                        .accessibilityIdentifier("sidebar-\(item.rawValue)")
                }
            }
            // Cap the sidebar width so SwiftUI cannot expand it past a
            // sensible upper bound. Without `max:`, the sidebar can grow
            // unbounded after certain layout transitions (e.g. exiting a
            // detail view back to the list), leaving the detail pane
            // squeezed into a sliver on the right. 280pt keeps long
            // labels like "Activity Log" fully readable while leaving
            // the main content area generous space.
            .navigationSplitViewColumnWidth(min: 180, ideal: 220, max: 280)
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
                    case .tools:
                        ToolsView(appState: appState)
                    case .registries:
                        RegistriesView(appState: appState)
                    case .activity:
                        ActivityView(appState: appState)
                    case .secrets:
                        SecretsView(appState: appState)
                    case .tokens:
                        TokensView(appState: appState)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .environment(\.fontScale, appState.fontScale)
            .accessibilityIdentifier("detail-view")
        }
        .frame(minWidth: 800, minHeight: 500)
        .background(sidebarShortcuts)
        .onReceive(NotificationCenter.default.publisher(for: .switchToActivity)) { _ in
            selectedItem = .activity
        }
        .onReceive(NotificationCenter.default.publisher(for: .switchToServers)) { _ in
            selectedItem = .servers
        }
        .onReceive(NotificationCenter.default.publisher(for: .switchToSidebarTab)) { note in
            guard let item = MainWindow.sidebarItem(from: note) else { return }
            selectedItem = item
        }
    }

    /// Decode a `.switchToSidebarTab` notification's payload. The wire form is
    /// the SidebarItem raw value as a String (posted by
    /// `AppController.showMainWindow(tab:)`); anything else — including a
    /// SidebarItem posted as the object itself — is deliberately dropped
    /// rather than crashing a notification handler.
    static func sidebarItem(from note: Notification) -> SidebarItem? {
        guard let raw = note.object as? String else { return nil }
        return SidebarItem(rawValue: raw)
    }

    /// Hidden ⌘1…⌘5 shortcuts to jump straight to each sidebar section. Keeps
    /// keyboard navigation fast for users and lets UI-test automation reach a
    /// section (the sidebar List rows aren't directly clickable via the
    /// accessibility menu API).
    @ViewBuilder
    private var sidebarShortcuts: some View {
        VStack {
            ForEach(Array(SidebarItem.allCases.enumerated()), id: \.element) { index, item in
                Button("") { selectedItem = item }
                    .keyboardShortcut(KeyEquivalent(Character(String(index + 1))), modifiers: .command)
                    .accessibilityIdentifier("sidebar-shortcut-\(item.rawValue)")
            }
        }
        .opacity(0)
        .frame(width: 0, height: 0)
        .accessibilityHidden(true)
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
