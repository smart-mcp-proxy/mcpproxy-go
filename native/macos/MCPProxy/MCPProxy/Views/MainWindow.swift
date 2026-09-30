// MainWindow.swift
// MCPProxy

import SwiftUI

/// Sidebar destinations, declared in VISUAL order: the hidden ⌘1…⌘7 shortcuts
/// iterate `allCases`, so they follow what the user sees (Spec 109-i FR-050).
enum SidebarItem: String, CaseIterable, Identifiable {
    // Spec 109 FR-051/T064: renamed from "Dashboard" — the needs-attention
    // list is now this section's first thing, not a banner buried in it.
    case home = "Home"
    // Connect
    case clients = "Clients"
    case servers = "Servers"
    // F16: BM25 tool discovery is the product's headline feature and had no
    // native home — a tray-first user could not answer "which of my 942 tools
    // does X?" without opening a browser.
    case tools = "Tools"
    // Protect
    case review = "Review Queue"
    case secrets = "Secrets"
    // Monitor. Spec 109-i: "Activity" (was "Activity Log") to match the Web UI
    // sidebar. The raw value is the in-process wire value of
    // `.switchToSidebarTab` and is never persisted. Spec 109 T109: the
    // Registries item is retired (server discovery lives in the Add Server
    // sheet's Catalog tab), and F5's Agent Tokens live under Clients.
    case activity = "Activity"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .home: return "rectangle.3.group"
        case .clients: return "person.2"
        case .servers: return "server.rack"
        case .tools: return "wrench.and.screwdriver"
        case .review: return "checkmark.shield"
        case .secrets: return "key.fill"
        case .activity: return "clock.arrow.circlepath"
        }
    }
}

/// The sidebar groups below Home, named exactly as in the Web UI
/// (contracts/navigation-map.md). Profiles joins Connect with Spec 108-k.
enum SidebarSection: CaseIterable {
    case connect, protect, monitor

    var title: String {
        switch self {
        case .connect: return "Connect"
        case .protect: return "Protect"
        case .monitor: return "Monitor"
        }
    }

    var items: [SidebarItem] {
        switch self {
        case .connect: return [.clients, .servers, .tools]
        case .protect: return [.review, .secrets]
        case .monitor: return [.activity]
        }
    }
}

/// The toolbar "+" menu (Spec 109-i FR-052): Server / Client / Token, the same
/// three the Web UI "+ Add" menu offers. Profile arrives with Spec 108-k.
enum AddMenuItem: String, CaseIterable, Identifiable {
    case server = "Server"
    case client = "Client"
    case token = "Token"

    var id: String { rawValue }

    /// The sidebar section that hosts this action's sheet.
    var destination: SidebarItem {
        switch self {
        case .server: return .servers
        case .client, .token: return .clients
        }
    }

    var systemImage: String {
        switch self {
        case .server: return "server.rack"
        case .client: return "person.crop.circle.badge.plus"
        case .token: return "key"
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
    init(appState: AppState, initialTab: SidebarItem = .home) {
        self.appState = appState
        _selectedItem = State(initialValue: initialTab)
    }

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedItem) {
                ForEach(Array(MainWindow.sidebarLayout.enumerated()), id: \.offset) { _, group in
                    if let title = group.title {
                        Section(title) { sidebarRows(group.items) }
                    } else {
                        sidebarRows(group.items)
                    }
                }
            }
            // Cap the sidebar width so SwiftUI cannot expand it past a
            // sensible upper bound. Without `max:`, the sidebar can grow
            // unbounded after certain layout transitions (e.g. exiting a
            // detail view back to the list), leaving the detail pane
            // squeezed into a sliver on the right. 280pt keeps long
            // labels like "Review Queue" fully readable while leaving
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
                    switch selectedItem ?? .home {
                    case .home:
                        HomeView(appState: appState)
                    case .review:
                        ReviewQueueView(appState: appState)
                    case .servers:
                        ServersView(appState: appState)
                    case .tools:
                        ToolsView(appState: appState)
                    case .activity:
                        ActivityView(appState: appState)
                    case .secrets:
                        SecretsView(appState: appState)
                    case .clients:
                        ClientsView(appState: appState)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .environment(\.fontScale, appState.fontScale)
            .accessibilityIdentifier("detail-view")
        }
        .frame(minWidth: 800, minHeight: 500)
        .toolbar {
            // Spec 109-i FR-052: one place to add things, from any section.
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    ForEach(AddMenuItem.allCases) { item in
                        Button {
                            // Hand the action off BEFORE switching the sidebar:
                            // a view created by the switch consumes it on appear.
                            appState.pendingAddAction = item
                            selectedItem = item.destination
                        } label: {
                            Label(item.rawValue, systemImage: item.systemImage)
                        }
                        .accessibilityIdentifier("toolbar-add-\(item.rawValue)")
                    }
                } label: {
                    Label("Add", systemImage: "plus")
                }
                .accessibilityIdentifier("toolbar-add-menu")
                .help("Add a server, client or agent token")
            }
        }
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

    /// Home on its own, then Connect / Protect / Monitor (contracts/
    /// navigation-map.md). Flattening this equals `SidebarItem.allCases`, so the
    /// ⌘1…⌘7 shortcut order is the visual order.
    static let sidebarLayout: [(title: String?, items: [SidebarItem])] =
        [(title: nil, items: [.home])] + SidebarSection.allCases.map { (title: $0.title, items: $0.items) }

    /// Count shown beside a sidebar row, 0 for none. Home carries the FR-001
    /// needs-attention count; Review Queue is one row per server (GET /review),
    /// deliberately separate from attention.
    private func badgeCount(for item: SidebarItem) -> Int {
        switch item {
        case .home: return appState.attention.count
        case .review: return appState.reviewQueueCount
        default: return 0
        }
    }

    @ViewBuilder
    private func sidebarRows(_ items: [SidebarItem]) -> some View {
        ForEach(items) { item in
            HStack {
                Label(item.rawValue, systemImage: item.icon)
                Spacer()
                if badgeCount(for: item) > 0 {
                    Text("\(badgeCount(for: item))")
                        .font(.caption2.bold())
                        .padding(.horizontal, 5).padding(.vertical, 2)
                        .background(.orange.opacity(0.2)).clipShape(Capsule())
                        .accessibilityIdentifier("sidebar-badge-\(item.rawValue)")
                }
            }
            .tag(item)
            .accessibilityIdentifier("sidebar-\(item.rawValue)")
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

    /// Hidden ⌘1…⌘7 shortcuts to jump straight to each sidebar section, in the
    /// order the sidebar shows them. Keeps keyboard navigation fast for users
    /// and lets UI-test automation reach a section (the sidebar List rows
    /// aren't directly clickable via the accessibility menu API).
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
