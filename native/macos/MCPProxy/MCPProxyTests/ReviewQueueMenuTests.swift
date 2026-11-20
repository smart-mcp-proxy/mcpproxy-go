import XCTest
@testable import MCPProxy

final class ReviewQueueMenuTests: XCTestCase {
    func testAttentionAndServerReviewRoutesOpenTheReviewSheet() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let home = try String(contentsOf: root.appendingPathComponent("MCPProxy/State/HomeAttentionAction.swift"))
        let servers = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/ServersView.swift"))
        let tray = try String(contentsOf: root.appendingPathComponent("MCPProxy/MCPProxyApp.swift"))
        let state = try String(contentsOf: root.appendingPathComponent("MCPProxy/State/AppState.swift"))
        let window = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/MainWindow.swift"))
        XCTAssertTrue(home.contains("SidebarItem.review.rawValue"))
        XCTAssertTrue(home.contains(".showReview"))
        XCTAssertTrue(servers.contains("SidebarItem.review.rawValue"))
        XCTAssertTrue(servers.contains(".showReview"))
        XCTAssertTrue(tray.contains("#selector(showReviewFromMenu(_:))"))
        let skillRoute = try XCTUnwrap(tray.range(of: "else if attentionItem.kind == "))
        let serverRoute = try XCTUnwrap(tray.range(of: "else if attentionItem.subject.type == \"server\"", range: skillRoute.upperBound..<tray.endIndex))
        let skillBranch = tray[skillRoute.lowerBound..<serverRoute.lowerBound]
        XCTAssertTrue(skillBranch.contains("#selector(showSkillReviewFromMenu(_:))"))
        XCTAssertTrue(skillBranch.contains("item.representedObject = attentionItem.fix.target"))
        XCTAssertTrue(tray.contains("Review Queue…"))
        XCTAssertTrue(tray.contains("appState.reviewQueueCount"), "tray count must be the review queue count, not attention")
        XCTAssertTrue(state.contains("reviewQueueCount"))
        XCTAssertTrue(window.contains("appState.reviewQueueCount"))
    }
}
