import XCTest
@testable import MCPProxy

final class ReviewPayloadTests: XCTestCase {
    func testReviewPayloadDecodesQueueAndToolSelectionFields() throws {
        let queue = try JSONDecoder().decode(ReviewQueueResponse.self, from: Data("""
        {"count":1,"servers":[{"server":"fixture","kind":"tool_review","quarantined":true,"pending":2,"changed":1,"tools_captured":5}]}
        """.utf8))
        XCTAssertEqual(queue.count, 1)
        XCTAssertEqual(queue.servers.first?.server, "fixture")
        XCTAssertEqual(queue.servers.first?.toolsCaptured, 5)

        let review = try JSONDecoder().decode(ServerReviewResponse.self, from: Data("""
        {"server":{"name":"fixture","quarantined":true,"definitions_captured":true,"scan":{"verdict":"dangerous","risk_score":93,"report_id":"scan-1"}},"tools":[{"name":"remove_file","description":"remove a file","input_schema":{"type":"object"},"output_schema":{"type":"string"},"annotations":{"readOnlyHint":false},"tier":"destructive","approval_status":"changed","disabled":false,"scan_verdict":"warnings","previous":{"description":"old description","input_schema":{"type":"null"},"output_schema":{"type":"null"},"annotations":{"readOnlyHint":true}},"diff":{"description":"- old\\n+ new","input_schema":"schema change","output_schema":"output change","annotations":"annotation change"}}]}
        """.utf8))
        XCTAssertTrue(review.server.definitionsCaptured)
        XCTAssertEqual(review.server.scan?.verdict, "dangerous")
        XCTAssertEqual(review.server.scan?.riskScore, 93)
        XCTAssertEqual(review.tools.first?.tier, "destructive")
        XCTAssertEqual(review.tools.first?.approvalStatus, "changed")
        XCTAssertNotNil(review.tools.first?.inputSchema)
        XCTAssertNotNil(review.tools.first?.annotations)
        XCTAssertEqual(review.tools.first?.previous?.description, "old description")
        XCTAssertEqual(review.tools.first?.diff?.description, "- old\n+ new")
    }

    func testReviewQueueUsesDedicatedSidebarAndSheet() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let window = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/MainWindow.swift"))
        let tray = try String(contentsOf: root.appendingPathComponent("MCPProxy/MCPProxyApp.swift"))
        XCTAssertTrue(window.contains("case review = \"Review Queue\""))
        XCTAssertTrue(window.contains("ReviewQueueView(appState: appState)"))
        XCTAssertTrue(tray.contains("showReviewFromMenu"))
    }

    func testReviewSheetHasInformedApprovalAndToolDecisionPaths() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/ReviewQueueView.swift"))
        for expected in [
            "Text(verbatim: definitionText(tool))", "Text(verbatim: diffText(tool))",
            "Baseline scan:", "Fetch tool definitions", "approveSpecificTools",
            "blockSpecificTools", "securityRejectServer", "Reject Server",
            "Text(tool.scanVerdict)", "Dangerous findings detected", "approve(force: true)",
            "NotificationCenter.default.publisher(for: .reviewChanged)",
            "NotificationCenter.default.publisher(for: .scanSettled)",
        ] {
            XCTAssertTrue(source.contains(expected), "Review sheet is missing \(expected)")
        }
    }
}
