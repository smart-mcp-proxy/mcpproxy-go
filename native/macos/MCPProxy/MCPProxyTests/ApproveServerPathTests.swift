import XCTest
@testable import MCPProxy

/// The native approval client uses the scan-gated security endpoint. Server
/// Detail only opens the informed Review queue; force approval lives there.
@MainActor
final class ApproveServerPathTests: XCTestCase {
    override func setUp() {
        super.setUp()
        HomeReviewActionStubURLProtocol.reset()
    }

    func testSecurityApprovalUsesScanGateAndForcePayload() async throws {
        let client = HomeReviewActionStubURLProtocol.makeClient()
        try await client.securityApproveServer("server / one", force: true)

        XCTAssertEqual(HomeReviewActionStubURLProtocol.requests.count, 1)
        let request = try XCTUnwrap(HomeReviewActionStubURLProtocol.requests.first)
        XCTAssertEqual(request.method, "POST")
        XCTAssertTrue(request.url.contains("/api/v1/servers/server%20%2F%20one/security/approve"))
        XCTAssertFalse(request.url.contains("/unquarantine"))

        let body = try XCTUnwrap(HomeReviewActionStubURLProtocol.requestBodies.first ?? nil)
        let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(json["force"] as? Bool, true)
        XCTAssertEqual(json["block"] as? [String], [])
    }

    func testToolApprovalAndServerRejectEscapeTheServerName() async throws {
        let client = HomeReviewActionStubURLProtocol.makeClient()
        try await client.approveSpecificTools("server / one", tools: ["read_file"])
        let toolRequest = try XCTUnwrap(HomeReviewActionStubURLProtocol.requests.first)
        XCTAssertTrue(toolRequest.url.contains("/api/v1/servers/server%20%2F%20one/tools/approve"))

        HomeReviewActionStubURLProtocol.reset()
        try await client.securityRejectServer("server / one")
        let rejectRequest = try XCTUnwrap(HomeReviewActionStubURLProtocol.requests.first)
        XCTAssertTrue(rejectRequest.url.contains("/api/v1/servers/server%20%2F%20one/security/reject"))
    }

    func testServerDetailOnlyNavigatesToInformedReview() throws {
        let path = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ServerDetailView.swift")
        let source = try String(contentsOf: path)
        XCTAssertTrue(source.contains("private func openReview()"))
        XCTAssertTrue(source.contains("SidebarItem.review.rawValue"))
        XCTAssertFalse(source.contains("securityApproveServer("))
        XCTAssertFalse(source.contains("approveSpecificTools("))
        XCTAssertFalse(source.contains("unquarantineServer("))
    }

    func testReviewSheetOffersForceConfirmationAfterTheScanGate() throws {
        let path = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ReviewQueueView.swift")
        let source = try String(contentsOf: path)
        XCTAssertTrue(source.contains("securityApproveServer(serverName, force: force, block:"))
        XCTAssertTrue(source.contains("status == 409"))
        XCTAssertTrue(source.contains("message.localizedCaseInsensitiveContains(\"dangerous\")"))
        XCTAssertTrue(source.contains("showForceApprovalConfirmation = true"))
        XCTAssertTrue(source.contains("approve(force: true)"))
        XCTAssertTrue(source.contains("Button(\"Reject Server\", role: .destructive)"))
        XCTAssertTrue(source.contains("securityRejectServer(serverName)"))
        XCTAssertTrue(source.contains("Text(tool.scanVerdict)"))
        XCTAssertTrue(source.contains("NotificationCenter.default.publisher(for: .reviewChanged)"))
    }
}
