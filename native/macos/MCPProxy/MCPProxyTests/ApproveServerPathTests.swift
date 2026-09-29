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

    func testDisconnectedCoreFailsApprovalBeforeSuccessCanBeReported() async {
        do {
            try await ServerDetailView.performSecurityApproval(apiClient: nil, serverID: "filesystem", force: false)
            XCTFail("approval without a connected API client must fail")
        } catch {
            XCTAssertEqual(error.localizedDescription, "Core is not ready")
        }
        XCTAssertTrue(HomeReviewActionStubURLProtocol.requests.isEmpty)
    }

    func testDetailViewSourcePresentsForceConfirmationOnlyAfterTheScanGate() throws {
        let path = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ServerDetailView.swift")
        let source = try String(contentsOf: path)
        XCTAssertTrue(source.contains("apiClient.securityApproveServer(serverID, force: force)"))
        XCTAssertTrue(source.contains("try await Self.performSecurityApproval(apiClient: apiClient"), "success is only reported after approval completes")
        XCTAssertFalse(source.contains("apiClient?.securityApproveServer"), "optional chaining can report success without approving")
        XCTAssertTrue(source.contains("showForceApprovalConfirmation = true"))
        XCTAssertTrue(source.contains("Button(\"Force Approve\", role: .destructive)"))
        XCTAssertFalse(source.contains("unquarantineServer("))
    }

    func testForceConfirmationIsLimitedToDangerousScanRejection() {
        XCTAssertTrue(ServerDetailView.shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 409, message: "server has 1 dangerous finding")))
        XCTAssertFalse(ServerDetailView.shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 409, message: "no scan results found; run a scan first")))
        XCTAssertFalse(ServerDetailView.shouldConfirmForcedSecurityApproval(
            APIClientError.httpError(statusCode: 500, message: "dangerous text is irrelevant")))
    }
}
