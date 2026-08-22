// MenuStructureTests.swift
// MCPProxyTests
//
// The top-level tray menu had grown long enough to scroll on a normal display,
// and a scrolling status-bar menu is the opposite of a glance. Three rows were
// spent to fix it, and each removal is a claim this file pins:
//
//   - "N quarantined server(s)" was grey, disabled and unclickable, and the
//     Needs Attention submenu already carries the same fact ACTIONABLY.
//   - "Report an Issue…" left with it; the tracker is still reachable through
//     the GitHub link in the About panel.
//   - "Add Server..." moved into the Servers submenu, where it belongs —
//     except when there are no servers and therefore no submenu to hold it.
//
// Driven through the real `rebuildMenu()` via the same seams
// `SupersedeMenuTests` uses, so a reorder that quietly reinstates a row fails
// here rather than shipping.

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class MenuStructureTests: XCTestCase {

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    private func makeController(servers: [ServerStatus] = []) -> (AppController, TestMenuHost) {
        let host = TestMenuHost()
        let controller = AppController(
            glanceDataSource: CountingGlanceDataSource(), menuHost: host
        )
        controller.appState.coreState = .connected
        controller.appState.servers = servers
        return (controller, host)
    }

    private func topLevelTitles(_ host: TestMenuHost) -> [String] {
        (host.menu?.items ?? []).map(\.title)
    }

    private func serversSubmenu(_ host: TestMenuHost) throws -> NSMenu {
        let parent = try XCTUnwrap(
            (host.menu?.items ?? []).first { $0.title.hasPrefix("Servers (") },
            "a proxy with servers must offer the Servers submenu")
        return try XCTUnwrap(parent.submenu)
    }

    // MARK: - Rows that are gone

    func testTheGreyQuarantineCountLineIsGone() {
        let (controller, host) = makeController(servers: [Self.server(name: "github")])
        controller.appState.quarantinedToolsCount = 3
        controller.rebuildMenu()

        XCTAssertFalse(topLevelTitles(host).contains { $0.contains("quarantined server") },
                       "the count is Needs Attention's job, and this row could not be clicked")
    }

    func testReportAnIssueIsGone() {
        let (controller, host) = makeController(servers: [Self.server(name: "github")])
        controller.rebuildMenu()

        XCTAssertFalse(topLevelTitles(host).contains { $0.contains("Report an Issue") })
        XCTAssertTrue(topLevelTitles(host).contains("Documentation"),
                      "the other project links stay — this was a length cut, not a retreat")
        XCTAssertTrue(topLevelTitles(host).contains("About MCPProxy"))
    }

    // MARK: - Add Server moved

    func testAddServerIsTheFirstItemOfTheServersSubmenu() throws {
        let (controller, host) = makeController(servers: [
            Self.server(name: "github"), Self.server(name: "jira")
        ])
        controller.rebuildMenu()

        XCTAssertFalse(topLevelTitles(host).contains { $0.hasPrefix("Add Server") },
                       "it no longer costs a permanent top-level row")

        let submenu = try serversSubmenu(host)
        let first = try XCTUnwrap(submenu.items.first)
        XCTAssertEqual(first.title, "Add Server...")
        XCTAssertTrue(submenu.items.count > 1 && submenu.items[1].isSeparatorItem,
                      "a separator keeps the action off the server list")
        XCTAssertEqual(submenu.items.map(\.title).filter { !$0.isEmpty },
                       ["Add Server...", "github", "jira"])
    }

    /// A submenu item with a nil target falls back to a responder chain the tray
    /// does not have while its menu is open, so the click would silently do
    /// nothing — the exact failure this move could have introduced.
    func testAddServerStaysWiredAndKeepsItsKeyEquivalent() throws {
        let (controller, host) = makeController(servers: [Self.server(name: "github")])
        controller.rebuildMenu()

        let addServer = try XCTUnwrap(try serversSubmenu(host).items.first)
        XCTAssertNotNil(addServer.action)
        XCTAssertTrue(addServer.target === controller)
        XCTAssertEqual(addServer.keyEquivalent, "n")
        XCTAssertEqual(addServer.keyEquivalentModifierMask, .command)
    }

    /// The one case that keeps it at the top level: with no servers there is no
    /// Servers submenu, and the user who most needs to add one would otherwise
    /// have no way to do it from the tray.
    func testAddServerStaysAtTheTopLevelWhenThereAreNoServers() throws {
        let (controller, host) = makeController(servers: [])
        controller.rebuildMenu()

        XCTAssertFalse(topLevelTitles(host).contains { $0.hasPrefix("Servers (") })
        let addServer = try XCTUnwrap(
            (host.menu?.items ?? []).first { $0.title.hasPrefix("Add Server") },
            "adding the first server must never be unreachable")
        XCTAssertNotNil(addServer.action)
        XCTAssertTrue(addServer.target === controller)
    }

    // MARK: - Helpers

    /// Built through the canonical Codable path so the fixture survives future
    /// field additions on `ServerStatus`.
    private static func server(name: String) -> ServerStatus {
        let json = """
        {
            "id": "\(name)",
            "name": "\(name)",
            "protocol": "http",
            "enabled": true,
            "connected": true,
            "quarantined": false,
            "tool_count": 4
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
