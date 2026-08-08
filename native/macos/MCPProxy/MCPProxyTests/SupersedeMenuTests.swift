// SupersedeMenuTests.swift
// MCPProxyTests
//
// Spec 092 FR-002 / FR-003 — the two Phase 0 prompts have to be REACHABLE.
// A verdict that only ever reaches a log is the bug (#957) with extra steps:
// the whole complaint is that nothing visible happened.
//
// Driven through the real controller and the real `rebuildMenu()`, via the
// same seams `MenuOpenNetworkTests` uses, so a rename or a reorder of the menu
// slots fails here rather than shipping.

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class SupersedeMenuTests: XCTestCase {

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    private func makeController() -> (AppController, TestMenuHost) {
        let host = TestMenuHost()
        let controller = AppController(
            glanceDataSource: CountingGlanceDataSource(), menuHost: host
        )
        controller.appState.coreState = .connected
        return (controller, host)
    }

    private func titles(_ host: TestMenuHost) -> [String] {
        (host.menu?.items ?? []).map(\.title)
    }

    private func item(_ host: TestMenuHost, containing text: String) -> NSMenuItem? {
        (host.menu?.items ?? []).first { $0.title.contains(text) }
    }

    // MARK: - Steady state

    func testNeitherPromptAppearsWhenVersionsMatch() throws {
        let (controller, host) = makeController()
        controller.rebuildMenu()

        XCTAssertFalse(titles(host).contains { $0.contains("Restart into") },
                       "FR-005: no supersede prompt when there is nothing to supersede")
        XCTAssertFalse(titles(host).contains { $0.contains("Relaunch") },
                       "FR-005: no relaunch prompt when the bundle has not changed")
    }

    // MARK: - FR-002

    func testStaleCorePromptIsRenderedAndActionable() throws {
        let (controller, host) = makeController()
        controller.appState.staleCorePrompt = StaleCorePrompt(
            runningVersion: "0.53.0", bundledVersion: "0.54.0", pid: 4242
        )
        controller.rebuildMenu()

        let restart = try XCTUnwrap(item(host, containing: "Restart into"),
                                    "the consent action must be in the menu")
        XCTAssertEqual(restart.title, "Old core v0.53.0 running — Restart into v0.54.0")
        XCTAssertNotNil(restart.action, "the item must do something when clicked")
        XCTAssertTrue(restart.target === controller)
        XCTAssertTrue(restart.toolTip?.contains("4242") ?? false,
                      "the tooltip should name the process the click will stop")
    }

    /// A core too old to report a pid still gets an item — it just explains
    /// instead of acting (FR-002's "presents instructions" branch).
    func testStaleCorePromptWithoutAPIDStillOffersAnItem() throws {
        let (controller, host) = makeController()
        controller.appState.staleCorePrompt = StaleCorePrompt(
            runningVersion: "0.40.0", bundledVersion: "0.54.0", pid: nil
        )
        controller.rebuildMenu()

        let restart = try XCTUnwrap(item(host, containing: "Restart into"))
        XCTAssertEqual(restart.title, "Old core v0.40.0 running — Restart into v0.54.0")
        XCTAssertNotNil(restart.action)
        XCTAssertTrue(restart.toolTip?.contains("by hand") ?? false)
    }

    // MARK: - FR-003

    func testReplacedBundlePromptIsRenderedAndActionable() throws {
        let (controller, host) = makeController()
        controller.appState.replacedBundleVersion = "0.55.0"
        controller.rebuildMenu()

        let relaunch = try XCTUnwrap(item(host, containing: "Relaunch"),
                                     "a drag-install upgrade must surface a relaunch action")
        XCTAssertEqual(relaunch.title, "MCPProxy was updated to v0.55.0 — Relaunch")
        XCTAssertNotNil(relaunch.action)
        XCTAssertTrue(relaunch.target === controller)
    }

    /// Both conditions can hold at once — a drag-install replaces the bundle
    /// AND leaves the old core running. Neither prompt may hide the other.
    func testBothPromptsCoexist() throws {
        let (controller, host) = makeController()
        controller.appState.replacedBundleVersion = "0.55.0"
        controller.appState.staleCorePrompt = StaleCorePrompt(
            runningVersion: "0.53.0", bundledVersion: "0.55.0", pid: 99
        )
        controller.rebuildMenu()

        XCTAssertNotNil(item(host, containing: "Relaunch"))
        XCTAssertNotNil(item(host, containing: "Restart into"))
    }

    /// Clearing the state must retract the offers: an item pointing at a pid
    /// nobody re-checked is worse than no item at all.
    func testPromptsAreRetractedWhenTheStateClears() throws {
        let (controller, host) = makeController()
        controller.appState.replacedBundleVersion = "0.55.0"
        controller.appState.staleCorePrompt = StaleCorePrompt(
            runningVersion: "0.53.0", bundledVersion: "0.55.0", pid: 99
        )
        controller.rebuildMenu()
        XCTAssertNotNil(item(host, containing: "Restart into"))

        controller.appState.replacedBundleVersion = nil
        controller.appState.staleCorePrompt = nil
        controller.rebuildMenu()

        XCTAssertNil(item(host, containing: "Restart into"))
        XCTAssertNil(item(host, containing: "Relaunch"))
    }
}
