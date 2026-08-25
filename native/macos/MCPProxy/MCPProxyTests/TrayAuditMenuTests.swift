// TrayAuditMenuTests.swift
// MCPProxyTests
//
// Drives the real `rebuildMenu()` and pins the menu-shaped fixes from the
// 2026-08 macOS tray UX audit:
//
//   F4  a "Needs Attention" row no longer runs an API action on a click that
//       reads as navigation — the verb has its own row
//   F7  one operation, one verb pair (Enable/Disable), stdio included
//   F8a a quarantined server offers a review path
//   F9  the login item is called the same thing here and in Settings; the two
//       "open" rows say where they open
//   F11 a profile that resolves to no servers says so
//   F12 transports are display-normalised
//   F15 the Servers submenu groups by state and folds the disabled tail
//
// Same seams as MenuStructureTests / SupersedeMenuTests, so a regression fails
// here rather than shipping.

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class TrayAuditMenuTests: XCTestCase {

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    private func makeController(servers: [ServerStatus] = [],
                                profiles: [ProfileSummary] = []) -> (AppController, TestMenuHost) {
        let host = TestMenuHost()
        let controller = AppController(
            glanceDataSource: CountingGlanceDataSource(), menuHost: host
        )
        controller.appState.coreState = .connected
        controller.appState.servers = servers
        controller.appState.profiles = profiles
        return (controller, host)
    }

    private func topLevelTitles(_ host: TestMenuHost) -> [String] {
        (host.menu?.items ?? []).map(\.title)
    }

    private func submenu(_ host: TestMenuHost, startingWith prefix: String) throws -> NSMenu {
        let parent = try XCTUnwrap(
            (host.menu?.items ?? []).first { $0.title.hasPrefix(prefix) },
            "expected a top-level item starting with \(prefix); got \(topLevelTitles(host))")
        return try XCTUnwrap(parent.submenu)
    }

    private func serverSubmenu(_ host: TestMenuHost, named name: String) throws -> NSMenu {
        let servers = try submenu(host, startingWith: "Servers (")
        let item = try XCTUnwrap(servers.items.first { $0.title == name },
                                 "no row for \(name) in \(servers.items.map(\.title))")
        return try XCTUnwrap(item.submenu)
    }

    // MARK: - F7 · One verb pair

    /// The audit's Appendix A: four rows, identical admin state, four different
    /// verbs — and a stdio submenu reading "Disabled … Start".
    func testStdioAndHTTPServersUseTheSameEnableDisableVerbs() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "stdio-on", proto: "stdio", enabled: true),
            Self.server(name: "http-on", proto: "http", enabled: true),
            Self.server(name: "stdio-off", proto: "stdio", enabled: false),
            Self.server(name: "http-off", proto: "http", enabled: false),
        ])
        controller.rebuildMenu()

        for name in ["stdio-on", "http-on"] {
            let titles = try serverSubmenu(host, named: name).items.map(\.title)
            XCTAssertTrue(titles.contains("Disable"), "\(name): \(titles)")
            XCTAssertFalse(titles.contains("Stop"), "process-control verbs are a different mental model")
        }
        // Disabled servers are folded (F15) — reach them through the fold when
        // there are enough of them, or inline when there are not.
        for name in ["stdio-off", "http-off"] {
            let titles = try Self.disabledServerSubmenu(host, named: name).items.map(\.title)
            XCTAssertTrue(titles.contains("Enable"), "\(name): \(titles)")
            XCTAssertFalse(titles.contains("Start"))
        }
    }

    // MARK: - F12 · Transport names

    func testTheProtocolLineIsDisplayNormalised() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "everything", proto: "streamable-http", enabled: true)
        ])
        controller.rebuildMenu()

        let titles = try serverSubmenu(host, named: "everything").items.map(\.title)
        XCTAssertTrue(titles.contains("Protocol: HTTP (streamable)"), "\(titles)")
        XCTAssertFalse(titles.contains("Protocol: streamable-http"))
    }

    // MARK: - F8a · A path to the quarantine review

    func testAQuarantinedServerOffersAReview() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "everything", proto: "http", enabled: true, quarantined: true)
        ])
        controller.rebuildMenu()

        let items = try serverSubmenu(host, named: "everything").items
        let review = try XCTUnwrap(items.first { $0.title.hasPrefix("Review quarantine") },
                                   "quarantined server offered only \(items.map(\.title))")
        XCTAssertNotNil(review.action, "a review row with no action is the F14 dead link again")
        XCTAssertTrue(review.target === controller)
        XCTAssertEqual(review.representedObject as? String, "everything")
    }

    func testAHealthyServerHasNoReviewRow() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "github", proto: "http", enabled: true)
        ])
        controller.rebuildMenu()
        let titles = try serverSubmenu(host, named: "github").items.map(\.title)
        XCTAssertFalse(titles.contains { $0.hasPrefix("Review quarantine") })
    }

    // MARK: - F4 · Attention rows do not mutate on a navigation click

    func testAnAttentionRowDoesNotFireItsActionOnClick() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "cloudflare", proto: "http", enabled: true,
                        health: ("degraded", "Sign-in required", "login"))
        ])
        controller.rebuildMenu()

        let attention = try submenu(host, startingWith: "Needs Attention")
        let row = try XCTUnwrap(attention.items.first)
        // An item that owns a submenu is wired by AppKit to its own
        // `submenuAction:` (and targeted at the submenu). What matters is that
        // the row no longer carries the ServerStatus payload the old handler
        // acted on, so no API call is reachable by clicking the row.
        XCTAssertEqual(row.action, NSSelectorFromString("submenuAction:"),
                       "the row itself must do nothing — clicking it used to sign in / restart / ENABLE a server")
        XCTAssertNil(row.representedObject as? ServerStatus)
        let rowMenu = try XCTUnwrap(row.submenu, "the action needs somewhere explicit to live")
        XCTAssertEqual(rowMenu.items.first?.title, "Sign in")
        XCTAssertNotNil(rowMenu.items.first?.action)
        XCTAssertTrue(rowMenu.items.first?.target === controller)
        XCTAssertTrue(rowMenu.items.map(\.title).contains("Open Server Details"))
    }

    /// The security case the audit calls out by name: enabling a server is a
    /// state change and must never be an unlabelled side effect.
    ///
    /// A disabled server is intentional, so it is not in Needs Attention at
    /// all (`AppState.serversNeedingAttention` drops `enable`) — the only way
    /// to enable one from the tray is the explicitly named row under
    /// `Servers ▸ <name> ▸ Enable`.
    func testEnablingAServerIsOnlyEverAnExplicitlyNamedRow() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "demo", proto: "stdio", enabled: false,
                        health: ("degraded", "Disabled", "enable"))
        ])
        controller.rebuildMenu()

        XCTAssertFalse(topLevelTitles(host).contains { $0.hasPrefix("Needs Attention") },
                       "a server the user switched off is not an alert")
        let items = try Self.disabledServerSubmenu(host, named: "demo").items
        let enable = try XCTUnwrap(items.first { $0.title == "Enable" })
        XCTAssertTrue(enable.target === controller)
    }

    /// A row with nothing to run (quarantine, missing secret) stays a plain
    /// clickable disclosure — no submenu for a menu of one navigation.
    func testARowWithNoActionNavigatesDirectly() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "everything", proto: "http", enabled: true, quarantined: true,
                        health: ("degraded", "Quarantined for review", "approve"))
        ])
        controller.rebuildMenu()

        let attention = try submenu(host, startingWith: "Needs Attention")
        let row = try XCTUnwrap(attention.items.first)
        XCTAssertNil(row.submenu)
        XCTAssertNotNil(row.action)
        XCTAssertEqual(row.representedObject as? String, "everything",
                       "navigation keys off the server NAME, which is what .showServerDetail matches")
    }

    // MARK: - F15 · A Servers submenu that fits

    func testDisabledServersFoldIntoTheirOwnSubmenu() throws {
        var servers = [
            Self.server(name: "alive-1", proto: "http", enabled: true),
            Self.server(name: "alive-2", proto: "http", enabled: true),
        ]
        for i in 1...4 {
            servers.append(Self.server(name: "off-\(i)", proto: "http", enabled: false))
        }
        let (controller, host) = makeController(servers: servers)
        controller.rebuildMenu()

        let menu = try submenu(host, startingWith: "Servers (")
        let titles = menu.items.map(\.title).filter { !$0.isEmpty }
        XCTAssertEqual(titles, ["Add Server...", "alive-1", "alive-2", "Disabled (4)"])
        let fold = try XCTUnwrap(menu.items.first { $0.title == "Disabled (4)" }?.submenu)
        XCTAssertEqual(fold.items.map(\.title), ["off-1", "off-2", "off-3", "off-4"])
    }

    func testAFewDisabledServersStayInline() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "alive", proto: "http", enabled: true),
            Self.server(name: "off", proto: "http", enabled: false),
        ])
        controller.rebuildMenu()

        let titles = try submenu(host, startingWith: "Servers (").items.map(\.title).filter { !$0.isEmpty }
        XCTAssertEqual(titles, ["Add Server...", "alive", "off"],
                       "one disabled server behind a fold costs a click and saves nothing")
    }

    func testServersNeedingAttentionSortFirst() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "aaa-fine", proto: "http", enabled: true),
            Self.server(name: "zzz-broken", proto: "http", enabled: true,
                        health: ("unhealthy", "failed to connect", "restart")),
        ])
        controller.rebuildMenu()

        let titles = try submenu(host, startingWith: "Servers (").items.map(\.title).filter { !$0.isEmpty }
        XCTAssertEqual(titles, ["Add Server...", "zzz-broken", "aaa-fine"],
                       "alphabetical order buried the servers that need something")
    }

    // MARK: - F11 · Empty profiles

    func testAProfileWithNoConfiguredServersIsFlaggedInTheMenu() throws {
        let (controller, host) = makeController(
            servers: [Self.server(name: "everything", proto: "http", enabled: true)],
            profiles: [
                Self.profile(name: "research", servers: ["github", "gitlab"], toolCount: 0),
                Self.profile(name: "live", servers: ["everything"], toolCount: 12),
            ])
        controller.rebuildMenu()

        let titles = try submenu(host, startingWith: "Profile:").items.map(\.title)
        XCTAssertTrue(titles.contains("research — no servers"), "\(titles)")
        XCTAssertTrue(titles.contains("live (1 server · 12 tools)"), "\(titles)")
    }

    // MARK: - F9 · Names that say what happens

    func testTheOpenRowsSayWhereTheyOpen() {
        let (controller, host) = makeController(servers: [Self.server(name: "github", proto: "http", enabled: true)])
        controller.rebuildMenu()

        let titles = topLevelTitles(host)
        XCTAssertTrue(titles.contains("Open MCPProxy Window"), "\(titles)")
        XCTAssertTrue(titles.contains("Open Web UI in Browser"), "\(titles)")
    }

    /// The same AutoStartService setting is "Launch at Login" in Settings → App.
    func testTheLoginItemHasOneName() {
        let (controller, host) = makeController(servers: [Self.server(name: "github", proto: "http", enabled: true)])
        controller.rebuildMenu()

        let titles = topLevelTitles(host)
        XCTAssertTrue(titles.contains("Launch at Login"), "\(titles)")
        XCTAssertFalse(titles.contains("Run at Startup"))
    }

    // MARK: - F5 / F16 · The unreachable views

    func testAgentTokensAndToolsAreReachableFromTheSidebar() {
        let items = SidebarItem.allCases.map(\.rawValue)
        XCTAssertTrue(items.contains("Agent Tokens"),
                      "TokensView was 444 lines of finished UI that nothing instantiated")
        XCTAssertTrue(items.contains("Tools"),
                      "BM25 discovery is the headline feature and had no native home")
    }

    func testEverySidebarItemHasAnIcon() {
        for item in SidebarItem.allCases {
            XCTAssertFalse(item.icon.isEmpty, "\(item.rawValue) has no symbol")
        }
    }

    // MARK: - Helpers

    /// Find a disabled server's submenu whether or not it landed behind the fold.
    private static func disabledServerSubmenu(_ host: TestMenuHost, named name: String) throws -> NSMenu {
        let parent = try XCTUnwrap(
            (host.menu?.items ?? []).first { $0.title.hasPrefix("Servers (") }?.submenu)
        if let direct = parent.items.first(where: { $0.title == name })?.submenu {
            return direct
        }
        let fold = try XCTUnwrap(
            parent.items.first { $0.title.hasPrefix("Disabled (") }?.submenu,
            "\(name) is neither inline nor folded: \(parent.items.map(\.title))")
        return try XCTUnwrap(fold.items.first { $0.title == name }?.submenu)
    }

    /// Built through the canonical Codable path so fixtures survive future
    /// field additions on `ServerStatus`.
    private static func server(name: String,
                               proto: String = "http",
                               enabled: Bool = true,
                               quarantined: Bool = false,
                               health: (level: String, summary: String, action: String)? = nil) -> ServerStatus {
        var healthJSON = ""
        if let health {
            healthJSON = """
            , "health": {"level": "\(health.level)", "admin_state": "enabled",
                          "summary": "\(health.summary)", "action": "\(health.action)"}
            """
        }
        let json = """
        {
            "id": "\(name)",
            "name": "\(name)",
            "protocol": "\(proto)",
            "enabled": \(enabled),
            "connected": \(enabled && health == nil),
            "quarantined": \(quarantined),
            "tool_count": 4
            \(healthJSON)
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }

    private static func profile(name: String, servers: [String], toolCount: Int) -> ProfileSummary {
        let list = servers.map { "\"\($0)\"" }.joined(separator: ",")
        let json = """
        {"name": "\(name)", "servers": [\(list)], "tool_count": \(toolCount)}
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ProfileSummary.self, from: json)
    }
}
