import XCTest
@testable import MCPProxy

/// Request-shape tests for the Connect form's five API calls, the transport
/// identity the form gates its mutating controls on, and the strict-socket mode
/// that keeps an admin write from silently riding TCP (spec 091 T014, research D6).
final class APIClientConnectTests: XCTestCase {

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    private var lastRequest: ConnectStubURLProtocol.Recorded {
        get throws { try XCTUnwrap(ConnectStubURLProtocol.recorded.last) }
    }

    private static let statusJSON = """
    {"id":"claude-code","name":"Claude Code","config_path":"/Users/x/.claude.json",
     "exists":true,"connected":true,"supported":true,"icon":"claude-code",
     "server_name":"mcpproxy","access_state":"accessible"}
    """

    private static let resultJSON = """
    {"success":true,"client":"claude-code","config_path":"/Users/x/.claude.json",
     "backup_path":"/Users/x/.claude.json.backup-20260731-180000","server_name":"mcpproxy",
     "action":"updated","message":"connected"}
    """

    // MARK: - Reads

    func testClientDetailRequestsTheSingleClientEndpoint() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.statusJSON)
        let client = ConnectStubURLProtocol.makeClient()

        let detail = try await client.clientDetail("claude-code")

        let request = try lastRequest
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/connect/claude-code")
        XCTAssertEqual(request.method, "GET")
        XCTAssertEqual(detail.accessState, .accessible)
        XCTAssertEqual(detail.serverName, "mcpproxy")
    }

    func testConnectPreviewCarriesTheEntryNameAsAQueryParameter() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"client":"claude-code","config_path":"/Users/x/.claude.json","server_name":"my proxy",
         "entry_text":"…","entry_exists":false,"contains_api_key":false,
         "access_state":"accessible","precondition_token":"tok"}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        let preview = try await client.connectPreview("claude-code", serverName: "my proxy")

        let request = try lastRequest
        XCTAssertEqual(
            request.url,
            "http://127.0.0.1:8080/api/v1/connect/claude-code/preview?server_name=my%20proxy"
        )
        XCTAssertEqual(request.method, "GET")
        XCTAssertEqual(preview.preconditionToken, "tok")
        XCTAssertEqual(preview.changeKind, .add)
    }

    /// Reads may ride TCP when the socket is unavailable (the list and previews
    /// stay useful off-socket); only writes are strict.
    func testReadsAreNotSentInStrictSocketMode() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.statusJSON)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.clientDetail("claude-code")

        XCTAssertNil(try lastRequest.headers[SocketURLProtocol.strictSocketHeader])
    }

    // MARK: - Connect

    func testConnectPostsEntryNameForceAndPreconditionToken() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.resultJSON)
        let client = ConnectStubURLProtocol.makeClient()

        let result = try await client.connect(
            "claude-code", serverName: "mcpproxy", force: true, preconditionToken: "tok-1")

        let request = try lastRequest
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/connect/claude-code")
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.json["server_name"] as? String, "mcpproxy")
        XCTAssertEqual(request.json["force"] as? Bool, true)
        XCTAssertEqual(request.json["precondition_token"] as? String, "tok-1")
        XCTAssertEqual(result.action, "updated")
        XCTAssertEqual(result.backupPath, "/Users/x/.claude.json.backup-20260731-180000")
    }

    /// An add/create sends neither `force` nor an absent token, so a legacy core
    /// keeps behaving exactly as it does today (contracts §2).
    func testConnectOmitsForceAndTokenWhenNeitherApplies() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.resultJSON)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.connect(
            "cursor", serverName: "mcpproxy", force: false, preconditionToken: nil)

        let body = try lastRequest.json
        XCTAssertEqual(body["server_name"] as? String, "mcpproxy")
        XCTAssertNil(body["force"])
        XCTAssertNil(body["precondition_token"])
    }

    func testConnectSurfacesPreconditionFailedAsADiscriminatedConflict() async throws {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,
         "data":{"success":false,"client":"claude-code","action":"precondition_failed",
                 "message":"the config changed since the preview"},
         "error":"the config changed since the preview"}
        """.utf8)
        let client = ConnectStubURLProtocol.makeClient()

        do {
            _ = try await client.connect(
                "claude-code", serverName: "mcpproxy", force: true, preconditionToken: "stale")
            XCTFail("a stale token must not resolve as a successful connect")
        } catch let error as APIClientError {
            guard case .connectConflict(let action, let message) = error else {
                return XCTFail("expected .connectConflict, got \(error)")
            }
            XCTAssertEqual(action, "precondition_failed")
            XCTAssertEqual(message, "the config changed since the preview")
        }
    }

    /// The legacy 409 must stay distinguishable, or the form cannot tell
    /// "re-preview" from "this is a hard failure" (research D9).
    func testConnectSurfacesAlreadyExistsAsItsOwnConflictAction() async throws {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,
         "data":{"success":false,"action":"already_exists","message":"entry already exists"},
         "error":"entry already exists"}
        """.utf8)
        let client = ConnectStubURLProtocol.makeClient()

        do {
            _ = try await client.connect(
                "claude-code", serverName: "mcpproxy", force: false, preconditionToken: "tok")
            XCTFail("a 409 must not resolve as a successful connect")
        } catch let error as APIClientError {
            guard case .connectConflict(let action, _) = error else {
                return XCTFail("expected .connectConflict, got \(error)")
            }
            XCTAssertEqual(action, "already_exists")
        }
    }

    // MARK: - Undo / disconnect

    func testUndoConnectPostsTheBackupIdentityItWasGiven() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.resultJSON)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.undoConnect(
            "claude-code", serverName: "mcpproxy", backupName: ".claude.json.backup-20260731")

        let request = try lastRequest
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/connect/claude-code/undo")
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.json["backup_name"] as? String, ".claude.json.backup-20260731")
        XCTAssertEqual(request.json["server_name"] as? String, "mcpproxy")
    }

    /// A connect that CREATED the file returns no backup; undo then means
    /// "remove the created file", which the core reads from an absent
    /// backup_name — sending an empty string would be a different request.
    func testUndoOfACreatedFileOmitsTheBackupName() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.resultJSON)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.undoConnect("cursor", serverName: "mcpproxy", backupName: nil)

        XCTAssertNil(try lastRequest.json["backup_name"])
    }

    func testDisconnectUsesDeleteAndNamesTheEntry() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"success":true,"client":"claude-code","config_path":"/Users/x/.claude.json",
         "server_name":"mcpproxy","action":"removed","message":"disconnected"}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        let result = try await client.disconnect("claude-code", serverName: "mcpproxy")

        let request = try lastRequest
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/connect/claude-code")
        XCTAssertEqual(request.method, "DELETE")
        XCTAssertEqual(request.json["server_name"] as? String, "mcpproxy")
        XCTAssertEqual(result.action, "removed")
    }

    // MARK: - Transport identity

    func testTransportKindIsTheSocketUnlessTCPWasRequested() {
        XCTAssertEqual(APIClient(socketPath: nil).transportKind, .unixSocket)
        XCTAssertEqual(APIClient(socketPath: "/tmp/mcpproxy-test.sock").transportKind, .unixSocket)
        XCTAssertEqual(APIClient(socketPath: "").transportKind, .tcp,
                       "an empty socket path is the explicit TCP-only mode")
    }

    // MARK: - Strict-socket writes

    func testEveryMutatingCallIsSentInStrictSocketMode() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.resultJSON)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.connect(
            "claude-code", serverName: "mcpproxy", force: false, preconditionToken: "tok")
        _ = try await client.undoConnect("claude-code", serverName: "mcpproxy", backupName: "b")
        _ = try await client.disconnect("claude-code", serverName: "mcpproxy")

        XCTAssertEqual(ConnectStubURLProtocol.recorded.count, 3)
        for request in ConnectStubURLProtocol.recorded {
            XCTAssertEqual(request.headers[SocketURLProtocol.strictSocketHeader], "1",
                           "\(request.method) \(request.url) was not marked strict-socket")
        }
    }

    /// The transport gate is client-side too: with the app on TCP the write must
    /// never leave the process (spec's non-socket edge case).
    func testMutatingCallsRefuseToRideTCPAndSendNothing() async {
        let client = ConnectStubURLProtocol.makeClient(transportKind: .tcp)

        do {
            _ = try await client.connect(
                "claude-code", serverName: "mcpproxy", force: false, preconditionToken: "tok")
            XCTFail("a TCP-transport client must refuse an administrative write")
        } catch let error as APIClientError {
            guard case .socketRequired = error else {
                return XCTFail("expected .socketRequired, got \(error)")
            }
        } catch {
            XCTFail("expected APIClientError, got \(error)")
        }

        XCTAssertTrue(ConnectStubURLProtocol.recorded.isEmpty,
                      "the request must not be attempted at all")
    }

    /// Reads stay available off-socket — the list and previews are what the form
    /// shows while explaining why the actions are disabled.
    func testReadsStillWorkOffSocket() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(Self.statusJSON)
        let client = ConnectStubURLProtocol.makeClient(transportKind: .tcp)

        _ = try await client.clientDetail("claude-code")

        XCTAssertEqual(ConnectStubURLProtocol.recorded.count, 1)
    }

    // MARK: - Strict interception rule

    private func request(strict: Bool, route: String? = nil) -> URLRequest {
        var request = URLRequest(url: URL(string: "http://127.0.0.1:8080/api/v1/connect/x")!)
        if strict {
            request.setValue("1", forHTTPHeaderField: SocketURLProtocol.strictSocketHeader)
        }
        if let route {
            request.setValue(SocketURLProtocol.makeRoute(to: route),
                             forHTTPHeaderField: SocketURLProtocol.routeHeader)
        }
        return request
    }

    /// The hole research D6 found: with the socket gone, an unrouted request
    /// falls through to TCP. A strict request must be intercepted anyway so it
    /// FAILS instead of delivering an admin write over the network.
    func testAStrictRequestIsInterceptedEvenWithNoSocketFile() {
        XCTAssertTrue(
            SocketURLProtocol.shouldIntercept(
                request: request(strict: true), defaultSocketExists: false)
        )
    }

    func testANonStrictRequestStillFallsBackToTCPWithNoSocketFile() {
        XCTAssertFalse(
            SocketURLProtocol.shouldIntercept(
                request: request(strict: false), defaultSocketExists: false)
        )
    }

    func testANonStrictRequestIsInterceptedWhenTheSocketExists() {
        XCTAssertTrue(
            SocketURLProtocol.shouldIntercept(
                request: request(strict: false), defaultSocketExists: true)
        )
    }

    func testARoutedRequestIsPinnedRegardlessOfStrictness() {
        XCTAssertTrue(
            SocketURLProtocol.shouldIntercept(
                request: request(strict: false, route: "/nonexistent/mcpproxy.sock"),
                defaultSocketExists: false)
        )
    }
}
