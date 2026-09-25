// ReviewNeverApprovesDirectlySourceGuardTests.swift
// MCPProxy
//
// Review round 2 (109-e medium finding): the "a quarantined server's review
// path never calls approveTools/unquarantine directly" guarantee (FR-005) was
// pinned only against the pure `ServerRowPresentation`/`TrayPrimaryPresentation`
// data functions — never against `MCPProxyApp.swift`'s real
// `showServerDetailFromMenu` handler or the independent quarantine-review
// row's selector wiring, both of which are the tray's equivalent of
// `ServersView.swift`'s `ctxOpenReview` (pinned behaviorally in
// ServerRowDispatchTests). `showServerDetailFromMenu` opens a real window
// (`showMainWindow()`), which the existing test target avoids invoking
// directly (see MainWindowRoutingTests) — so this is a source-level
// regression guard, the same pattern DashboardRoutingTests and
// ServersViewRoutingTests already use for exactly this "can't safely drive
// AppKit window creation from XCTest" constraint.
//
// `APIClient.approveTools` already exists and is already called directly
// elsewhere (e.g. DashboardView.swift) — nothing stops a future edit from
// wiring the tray's review selector to it the same way, and every other test
// in this package would stay green if it did.

import XCTest
@testable import MCPProxy

final class ReviewNeverApprovesDirectlySourceGuardTests: XCTestCase {

    private static let forbidden = ["approveTools(", "unquarantine("]

    func testShowServerDetailFromMenuNeverCallsApproveDirectly() throws {
        let body = try functionBody(named: "showServerDetailFromMenu", in: try mcpProxyAppSource())
        for call in Self.forbidden {
            XCTAssertFalse(body.contains(call),
                           "showServerDetailFromMenu must only navigate (FR-005) — found `\(call)`")
        }
        XCTAssertTrue(body.contains(".showServerDetail"),
                      "showServerDetailFromMenu must still post the navigation notification")
    }

    /// The independent quarantine-review row (round 1's fix, restoring a
    /// review path when `actions[0]` is something other than "approve") must
    /// keep dispatching to the same navigation-only handler.
    func testIndependentQuarantineReviewRowTargetsTheNavigationHandler() throws {
        let source = try mcpProxyAppSource()
        guard let range = source.range(of: "if server.quarantined && !primaryOpensReview {") else {
            XCTFail("could not find the independent quarantine-review row in MCPProxyApp.swift")
            return
        }
        guard let end = source.range(of: "\n        }", range: range.upperBound..<source.endIndex) else {
            XCTFail("could not find the end of the quarantine-review row block")
            return
        }
        let block = String(source[range.upperBound..<end.lowerBound])
        XCTAssertTrue(block.contains("#selector(showServerDetailFromMenu(_:))"),
                      "the review row must dispatch through the navigation-only handler")
        for call in Self.forbidden {
            XCTAssertFalse(block.contains(call), "the review row must never call `\(call)` directly")
        }
    }

    // MARK: - Helpers

    private func functionBody(named name: String, in source: String) throws -> String {
        guard let start = source.range(of: "func \(name)(") else {
            XCTFail("could not find `func \(name)` in MCPProxyApp.swift")
            return ""
        }
        guard let openBrace = source.range(of: "{", range: start.upperBound..<source.endIndex) else {
            XCTFail("could not find the opening brace of `\(name)`")
            return ""
        }
        // Bounded by the next top-level `    @objc` / `    private` / `    func`
        // declaration at the same 4-space indent this file's methods use —
        // fragile only to a reformat of this one function, same trade-off
        // ServersViewRoutingTests' closure-body helper already makes.
        guard let end = source.range(of: "\n    }\n", range: openBrace.upperBound..<source.endIndex) else {
            XCTFail("could not find the end of `\(name)`")
            return ""
        }
        return String(source[openBrace.upperBound..<end.lowerBound])
    }

    private func mcpProxyAppSource() throws -> String {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()   // MCPProxyTests
            .deletingLastPathComponent()   // package root
        let url = packageRoot.appendingPathComponent("MCPProxy/MCPProxyApp.swift")
        XCTAssertTrue(FileManager.default.fileExists(atPath: url.path),
                      "missing source file at \(url.path)")
        return try String(contentsOf: url, encoding: .utf8)
    }
}
