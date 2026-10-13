import XCTest
@testable import MCPProxy

/// Spec 109-i (FR-050, FR-052): the native sidebar has the same names, groups
/// and order as the Web UI (Home, Connect, Protect, Monitor), and the toolbar
/// "+" menu offers Server / Client / Token. The ⌘1…⌘8 shortcut order follows
/// the visual order because both derive from `SidebarItem.allCases`.
///
/// Spec 108-k (T110a): Profiles joins the Connect group and "Profile" the "+"
/// menu. This file is extended IN PLACE so that a rebase over 109-i cannot drop
/// either side's items silently: every 109-i item is still asserted here.
@MainActor
final class NavigationStructureTests: XCTestCase {

    func testSidebarItemsFollowTheVisualOrder() {
        XCTAssertEqual(
            SidebarItem.allCases.map(\.rawValue),
            ["Home", "Clients", "Profiles", "Servers", "Tools", "Review Queue", "Secrets", "Activity"]
        )
    }

    func testSidebarLayoutGroupsMatchTheWebUI() {
        let layout = MainWindow.sidebarLayout
        XCTAssertEqual(layout.map(\.title), [nil, "Connect", "Protect", "Monitor"])
        XCTAssertEqual(layout[0].items, [.home])
        XCTAssertEqual(layout[1].items, [.clients, .profiles, .servers, .tools])
        XCTAssertEqual(layout[2].items, [.review, .secrets])
        XCTAssertEqual(layout[3].items, [.activity])
        // The shortcut order (allCases) is the visual order.
        XCTAssertEqual(layout.flatMap(\.items), SidebarItem.allCases)
    }

    func testSidebarSectionTitles() {
        XCTAssertEqual(SidebarSection.allCases.map(\.title), ["Connect", "Protect", "Monitor"])
    }

    /// Every Spec 109-i item is still present, in 109's relative order (Profiles
    /// only inserts itself; nothing 109-i shipped is moved or dropped).
    func testEverySpec109iItemIsStillPresentInItsOrder() {
        let names = SidebarItem.allCases.map(\.rawValue)
        let spec109i = ["Home", "Clients", "Servers", "Tools", "Review Queue", "Secrets", "Activity"]
        XCTAssertEqual(names.filter { spec109i.contains($0) }, spec109i)
        XCTAssertEqual(names.count, spec109i.count + 1, "Profiles is the only addition")
    }

    func testProfilesHasItsIconAndLivesInConnect() {
        XCTAssertEqual(SidebarItem.profiles.rawValue, "Profiles")
        XCTAssertEqual(SidebarItem.profiles.icon, "person.crop.rectangle.stack")
        XCTAssertTrue(SidebarSection.connect.items.contains(.profiles))
    }

    func testRetiredNamesAreGone() {
        let names = SidebarItem.allCases.map(\.rawValue)
        for gone in ["Dashboard", "Registries", "Agent Tokens", "Activity Log", "Sessions", "Security"] {
            XCTAssertFalse(names.contains(gone), "\(gone) must not be a sidebar item")
        }
    }

    func testAddMenuItemsAndDestinations() {
        XCTAssertEqual(AddMenuItem.allCases.map(\.rawValue), ["Server", "Client", "Token", "Profile"])
        XCTAssertEqual(AddMenuItem.server.destination, .servers)
        XCTAssertEqual(AddMenuItem.client.destination, .clients)
        XCTAssertEqual(AddMenuItem.token.destination, .clients)
        XCTAssertEqual(AddMenuItem.profile.destination, .profiles)
    }

    /// The ⌘ shortcuts follow the visual order, so adding Profiles at the third
    /// position shifts Servers to ⌘4 and everything after it (documented in
    /// docs/development/macos-tray.md).
    func testCommandShortcutsShiftWithTheVisualOrder() {
        let order = SidebarItem.allCases
        XCTAssertEqual(order.firstIndex(of: .profiles).map { $0 + 1 }, 3)
        XCTAssertEqual(order.firstIndex(of: .servers).map { $0 + 1 }, 4)
        XCTAssertEqual(order.firstIndex(of: .activity).map { $0 + 1 }, 8)
    }

    func testPendingAddActionIsConsumedOnlyByMatchingKinds() {
        let appState = AppState()
        appState.pendingAddAction = .server
        XCTAssertNil(appState.consumePendingAddAction(for: [.client, .token]))
        XCTAssertEqual(appState.pendingAddAction, .server, "a Clients view must not eat a Server action")
        XCTAssertEqual(appState.consumePendingAddAction(for: [.server]), .server)
        XCTAssertNil(appState.pendingAddAction)

        appState.pendingAddAction = .token
        XCTAssertEqual(appState.consumePendingAddAction(for: [.client, .token]), .token)
        XCTAssertNil(appState.consumePendingAddAction(for: [.client, .token]))
    }

    /// `.profile` is consumed only by the Profiles view: Clients must not eat it.
    func testProfileAddActionIsOwnedByTheProfilesView() {
        let appState = AppState()
        appState.pendingAddAction = .profile
        XCTAssertNil(appState.consumePendingAddAction(for: [.client, .token]))
        XCTAssertNil(appState.consumePendingAddAction(for: [.server]))
        XCTAssertEqual(appState.pendingAddAction, .profile)
        XCTAssertEqual(appState.consumePendingAddAction(for: [.profile]), .profile)
        XCTAssertNil(appState.pendingAddAction)
    }

    /// A route is consumed only by a view that recognises it, so the Clients
    /// hub cannot swallow a Tools route.
    func testAnAppRouteIsConsumedOnlyByTheViewThatOwnsIt() {
        let appState = AppState()
        appState.navigate(.tools(filter: .forProfile("ro")))
        let asClients: ClientsTab? = appState.consumeRoute { route in
            if case .clients(let tab, _) = route { return tab }
            return nil
        }
        XCTAssertNil(asClients)
        XCTAssertNotNil(appState.pendingRoute, "the route is still pending")
        let asTools: ScopeFilter?? = appState.consumeRoute { route in
            if case .tools(let filter) = route { return .some(filter) }
            return nil
        }
        XCTAssertEqual(asTools, .some(.some(.forProfile("ro"))))
        XCTAssertNil(appState.pendingRoute)
    }

    /// A route names the sidebar section that hosts its destination.
    func testRoutesMapToTheirSidebarSection() {
        XCTAssertEqual(AppRoute.profileEditor(name: "ro", focusTool: nil).sidebarItem, .profiles)
        XCTAssertEqual(AppRoute.clients(tab: .tokens, filter: nil).sidebarItem, .clients)
        XCTAssertEqual(AppRoute.connectSheet(clientId: "cursor").sidebarItem, .clients)
        XCTAssertEqual(AppRoute.tools(filter: nil).sidebarItem, .tools)
        XCTAssertEqual(AppRoute.serverDetail(name: "github").sidebarItem, .servers)
        XCTAssertEqual(AppRoute.reviewQueue.sidebarItem, .review)
        XCTAssertNil(AppRoute.settings(.requireMCPAuth).sidebarItem, "Settings is its own window")
    }

    /// A rebase over Spec 109-i must not drop the "Settings" wording, and
    /// Spec 108-k's "Anonymous callers" picker must be carried by it too.
    func testSettingsKeepsItsWordingAndCarriesTheAnonymousCallersPicker() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let settings = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/SettingsView.swift"))
        XCTAssertTrue(settings.contains("Settings"), "109-i's Settings wording")
        XCTAssertTrue(settings.contains("SecuritySettingsTab"))
        let section = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/AnonymousProfileSection.swift"))
        XCTAssertTrue(section.contains("Anonymous callers"))
        let tab = try String(contentsOf: root.appendingPathComponent("MCPProxy/Settings/ConfigSettingsView.swift"))
        XCTAssertTrue(tab.contains("AnonymousProfileSection"), "the Security tab mounts the picker")
    }

    /// Source guard (the idiom ReviewQueueMenuTests uses): the window carries
    /// the toolbar "+" menu and the badges the contract names.
    func testMainWindowCarriesTheToolbarAddMenuAndBadges() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let window = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/MainWindow.swift"))
        XCTAssertTrue(window.contains(".toolbar"))
        XCTAssertTrue(window.contains("accessibilityIdentifier(\"toolbar-add-menu\")"))
        XCTAssertTrue(window.contains("appState.attention.count"), "Home badge is the attention count")
        XCTAssertTrue(window.contains("appState.reviewQueueCount"), "Review Queue badge")
    }

    /// No dashboard file is recreated (Spec 109-d renamed it to Home).
    func testNoDashboardViewIsRecreated() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let views = root.appendingPathComponent("MCPProxy/Views")
        let files = try FileManager.default.contentsOfDirectory(atPath: views.path)
        XCTAssertFalse(files.contains("DashboardView.swift"))
    }
}
