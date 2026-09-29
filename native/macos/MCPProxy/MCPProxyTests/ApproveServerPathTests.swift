import XCTest
@testable import MCPProxy

/// Spec 109-f T078: the native approval client has exactly one server-release
/// route, the scan-gated security endpoint. The force payload is only sent by
/// the destructive confirmation path in ServerDetailView.
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
        let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Bool])
        XCTAssertEqual(json["force"], true)
    }

    func testDetailViewSourcePresentsForceConfirmationOnlyAfterTheScanGate() throws {
        let path = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ServerDetailView.swift")
        let source = try String(contentsOf: path)
        XCTAssertTrue(source.contains("securityApproveServer(server.id, force: force)"))
        XCTAssertTrue(source.contains("showForceApprovalConfirmation = true"))
        XCTAssertTrue(source.contains("Button(\"Force Approve\", role: .destructive)"))
        XCTAssertFalse(source.contains("unquarantineServer("))
    }

    func testForceConfirmationIsLimitedToDangerousScanRejection() {
        XCTAssertTrue(shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 409, message: "server has 1 dangerous finding")))
        XCTAssertFalse(shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 409, message: "no scan results found; run a scan first")))
        XCTAssertFalse(shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 500, message: "dangerous text is irrelevant")))
    }
}
