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

    // MARK: Binding an approval to the reviewed definitions (UX-02)

    private func reviewTool(_ name: String, hash: String?) throws -> ReviewTool {
        let field = hash.map { ",\"current_hash\":\"\($0)\"" } ?? ""
        let json = "{\"name\":\"\(name)\",\"description\":\"d\",\"tier\":\"read\",\"approval_status\":\"pending\",\"disabled\":false,\"scan_verdict\":\"clean\"\(field)}"
        return try JSONDecoder().decode(ReviewTool.self, from: Data(json.utf8))
    }

    private func lastBody() throws -> [String: Any] {
        let body = try XCTUnwrap(HomeReviewActionStubURLProtocol.requestBodies.last ?? nil)
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Any])
    }

    func testReviewToolDecodesCurrentHash() throws {
        XCTAssertEqual(try reviewTool("read_file", hash: "abc123").currentHash, "abc123")
        XCTAssertNil(try reviewTool("read_file", hash: nil).currentHash)
    }

    func testExpectedHashesBindEveryReviewedTool() throws {
        let tools = [try reviewTool("a", hash: "h1"), try reviewTool("b", hash: "h2")]
        XCTAssertEqual(ReviewPresentation.expectedHashes(tools), ["a": "h1", "b": "h2"])
    }

    /// An older core reports no current_hash anywhere: the request stays
    /// unbound rather than asking for a binding the core cannot honour.
    func testExpectedHashesStayUnboundOnlyWhenNoToolHasAHash() throws {
        XCTAssertNil(ReviewPresentation.expectedHashes([try reviewTool("a", hash: nil), try reviewTool("b", hash: "")]))
        XCTAssertNil(ReviewPresentation.expectedHashes([]))
    }

    /// One hashless tool on a newer core is left out of the binding, not
    /// allowed to unbind the whole approval (the core then refuses it as not in
    /// the review unless it is blocked) — same as the CLI and the Web screen.
    func testHashlessToolIsLeftOutOfAnOtherwiseBoundApproval() throws {
        let tools = [try reviewTool("a", hash: "h1"), try reviewTool("b", hash: nil)]
        XCTAssertEqual(ReviewPresentation.expectedHashes(tools), ["a": "h1"])
        // Per-tool approval of the hashless tool stays bound (empty) because the review carries hashes.
        XCTAssertEqual(ReviewPresentation.expectedHashes([tools[1]], review: tools), [:])
        XCTAssertEqual(ReviewPresentation.expectedHashes([tools[0]], review: tools), ["a": "h1"])
    }

    func testSecurityApprovalSendsExpectedHashesOnlyWhenBound() async throws {
        let client = HomeReviewActionStubURLProtocol.makeClient()
        try await client.securityApproveServer("srv", force: false, block: ["write_file"], expectedHashes: ["read_file": "h1", "write_file": "h2"])
        var json = try lastBody()
        XCTAssertEqual(json["expected_hashes"] as? [String: String], ["read_file": "h1", "write_file": "h2"])
        XCTAssertEqual(json["block"] as? [String], ["write_file"])

        try await client.securityApproveServer("srv", force: false, block: [])
        json = try lastBody()
        XCTAssertNil(json["expected_hashes"], "an unbound request must not carry the key")
    }

    func testToolApprovalSendsExpectedHashesOnlyWhenBound() async throws {
        let client = HomeReviewActionStubURLProtocol.makeClient()
        try await client.approveSpecificTools("srv", tools: ["read_file"], expectedHashes: ["read_file": "h1"])
        var json = try lastBody()
        XCTAssertEqual(json["tools"] as? [String], ["read_file"])
        XCTAssertEqual(json["expected_hashes"] as? [String: String], ["read_file": "h1"])

        try await client.approveSpecificTools("srv", tools: ["read_file"])
        json = try lastBody()
        XCTAssertNil(json["expected_hashes"])
    }

    func testOutOfDateConflictIsAStaleReviewNotAForcePrompt() async throws {
        HomeReviewActionStubURLProtocol.responseStatus = 409
        HomeReviewActionStubURLProtocol.responseBody = #"{"success":false,"error":"tool review for server 'srv' is out of date (definition changed since review: read_file); nothing was approved — fetch the review again"}"#
        let client = HomeReviewActionStubURLProtocol.makeClient()
        do {
            try await client.securityApproveServer("srv", block: [], expectedHashes: ["read_file": "old"])
            XCTFail("a 409 must throw")
        } catch {
            XCTAssertNotNil(ReviewPresentation.staleReviewMessage(error))
            XCTAssertTrue(ReviewPresentation.staleReviewMessage(error)?.contains("read_file") == true)
        }
        XCTAssertNil(ReviewPresentation.staleReviewMessage(APIClientError.httpError(statusCode: 409, message: "dangerous findings; use force")))
        XCTAssertNil(ReviewPresentation.staleReviewMessage(APIClientError.httpError(statusCode: 500, message: "out of date")))
    }

    func testReviewSheetBindsApprovalsAndReloadsAStaleReview() throws {
        let path = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ReviewQueueView.swift")
        let source = try String(contentsOf: path)
        XCTAssertTrue(source.contains("expected: expectedHashes(review.tools)"))
        XCTAssertTrue(source.contains("expectedHashes: decision.expected"))
        XCTAssertTrue(source.contains("expectedHashes: expected"))
        // UX-02 cross-review r4: the decision is captured at the click, the
        // sheet is recreated per server and only the newest load of THIS
        // server's review is shown.
        XCTAssertTrue(source.contains("ReviewPresentation.approvalDecision(server: serverName, review: review, allowed: allowed, everything: everything)"))
        XCTAssertTrue(source.contains("ReviewPresentation.toolApprovalExpected(server: serverName, tool: tool.name, review: review)"))
        XCTAssertTrue(source.contains(".id(server)"))
        XCTAssertTrue(source.contains(".task(id: serverName)"))
        XCTAssertTrue(source.contains("guard generation == loadGeneration, value.server.name == server else { return }"))
        XCTAssertTrue(source.contains("decision.server == serverName"))
        XCTAssertTrue(source.contains("ReviewPresentation.staleReviewMessage(error)"))
        XCTAssertTrue(source.contains("await load(); staleNotice = stale"))
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
        XCTAssertTrue(source.contains("securityApproveServer(decision.server, force: force, block: decision.block"))
        XCTAssertTrue(source.contains("status == 409"))
        XCTAssertTrue(source.contains("message.localizedCaseInsensitiveContains(\"dangerous\")"))
        XCTAssertTrue(source.contains("showForceApprovalConfirmation = true"))
        XCTAssertTrue(source.contains("approve(decision, force: true)"))
        XCTAssertTrue(source.contains("Button(\"Reject Server\", role: .destructive)"))
        XCTAssertTrue(source.contains("securityRejectServer(serverName)"))
        XCTAssertTrue(source.contains("Text(tool.scanVerdict)"))
        XCTAssertTrue(source.contains("NotificationCenter.default.publisher(for: .reviewChanged)"))
    }
}
