import XCTest
@testable import MCPProxy

/// Spec 132 (T054): the tray lists browser sign-in requests and opens the
/// consent page; it never decides one.
@MainActor
final class SignInRequestsTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func request(_ id: String, _ label: String = "Cursor", ttl: TimeInterval = 600) -> SignInRequest {
        SignInRequest(id: id, clientLabel: label, expiresAt: now.addingTimeInterval(ttl))
    }

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    // MARK: Event decoding

    func testDecodesPendingEventFromEnvelope() {
        let e = SignInWire.decodePending(#"{"payload":{"request_id":"r1","client_id":"cursor","client_label":"Cursor IDE"},"timestamp":1}"#)
        XCTAssertEqual(e, SignInWire.Pending(requestID: "r1", clientLabel: "Cursor IDE"))
    }

    func testDecodesPendingEventWithoutEnvelopeAndMissingLabel() {
        XCTAssertEqual(SignInWire.decodePending(#"{"request_id":"r2"}"#),
                       SignInWire.Pending(requestID: "r2", clientLabel: ""))
    }

    func testPendingEventWithoutIDOrGarbageIsRejected() {
        XCTAssertNil(SignInWire.decodePending(#"{"payload":{"client_label":"x"}}"#))
        XCTAssertNil(SignInWire.decodePending(#"{"payload":{"request_id":""}}"#))
        XCTAssertNil(SignInWire.decodePending("not json"))
    }

    func testDecodesDecidedEvent() {
        XCTAssertEqual(SignInWire.decodeDecided(#"{"payload":{"request_id":"r1","status":"approved"}}"#),
                       SignInWire.Decided(requestID: "r1", status: "approved"))
        XCTAssertNil(SignInWire.decodeDecided(#"{"payload":{"status":"denied"}}"#))
    }

    // MARK: Reducer

    func testAddDedupesAndReportsOnlyTheFirstArrival() {
        var store = SignInRequestStore()
        XCTAssertTrue(store.add(request("a"), now: now))
        XCTAssertFalse(store.add(request("a", "Cursor 2"), now: now))
        XCTAssertEqual(store.requests.count, 1)
        XCTAssertEqual(store.requests[0].clientLabel, "Cursor 2")
    }

    func testNewestFirst() {
        var store = SignInRequestStore()
        store.add(request("a"), now: now)
        store.add(request("b"), now: now)
        XCTAssertEqual(store.requests.map(\.id), ["b", "a"])
    }

    func testAnExpiredRequestIsNotAdded() {
        var store = SignInRequestStore()
        XCTAssertFalse(store.add(request("old", ttl: -1), now: now))
        XCTAssertTrue(store.isEmpty)
    }

    func testRemoveAndExpire() {
        var store = SignInRequestStore()
        store.add(request("a", ttl: 10), now: now)
        store.add(request("b", ttl: 600), now: now)
        store.expire(now: now.addingTimeInterval(11))
        XCTAssertEqual(store.requests.map(\.id), ["b"])
        store.remove(id: "b")
        store.remove(id: "missing")
        XCTAssertTrue(store.isEmpty)
    }

    func testReplaceAllDropsWhatTheCoreNoLongerListsAndDuplicates() {
        var store = SignInRequestStore()
        store.add(request("gone"), now: now)
        store.replaceAll(with: [request("x"), request("y"), request("x"), request("stale", ttl: -5)], now: now)
        XCTAssertEqual(store.requests.map(\.id), ["x", "y"])
    }

    // MARK: AppState

    func testAppStateAddSanitisesAndNotifiesOnceAndDecidedRemoves() {
        let state = AppState()
        XCTAssertTrue(state.addSignInRequest(id: "r1", rawLabel: "  Cur\u{202E}sor\nIDE  ", now: now))
        XCTAssertFalse(state.addSignInRequest(id: "r1", rawLabel: "Cursor", now: now))
        XCTAssertEqual(state.signInRequests.requests.map(\.clientLabel), ["Cursor"])
        XCTAssertEqual(state.signInRequests.requests.first?.expiresAt,
                       now.addingTimeInterval(SignInPresentation.defaultLifetime))
        state.removeSignInRequest(id: "r1")
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    func testReconnectReloadShowsARequestMadeWhileNotListeningAndDropsExpiredOnes() throws {
        let state = AppState()
        state.addSignInRequest(id: "seen", rawLabel: "Seen", now: now)
        let json = """
        [{"id":"missed","client_label":"Missed App","created_at":"2026-11-23T10:00:00.123Z","expires_at":"2026-11-23T10:10:00.456Z"},
         {"id":"expired","client_label":"Late","created_at":"2020-01-01T00:00:00Z","expires_at":"2020-01-01T00:10:00Z"}]
        """
        let summaries = try JSONDecoder().decode([SignInWire.Summary].self, from: Data(json.utf8))
        let reloadAt = SignInPresentation.parseDate("2026-11-23T10:05:00Z")!
        state.replaceSignInRequests(summaries, now: reloadAt)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["missed"])
        XCTAssertEqual(state.signInRequests.requests[0].clientLabel, "Missed App")
        XCTAssertEqual(state.signInRequests.requests[0].expiresAt, SignInPresentation.parseDate("2026-11-23T10:10:00.456Z"))
    }

    func testClearEmptiesTheListWhenTheFeatureIsOff() {
        let state = AppState()
        state.addSignInRequest(id: "r1", rawLabel: "Cursor", now: Date())
        state.clearSignInRequests()
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    // MARK: Presentation

    func testLabelIsCleanedAndTruncated() {
        XCTAssertEqual(SignInPresentation.sanitizedLabel(""), "an application")
        XCTAssertEqual(SignInPresentation.sanitizedLabel(" \n\t "), "an application")
        XCTAssertEqual(SignInPresentation.sanitizedLabel("A\u{200B}B\u{0007}C"), "ABC")
        XCTAssertEqual(SignInPresentation.sanitizedLabel("<b>Cursor</b> &amp; Co"), "<b>Cursor</b> &amp; Co")
        let long = String(repeating: "x", count: 500)
        XCTAssertEqual(SignInPresentation.sanitizedLabel(long).count, SignInPresentation.labelLimit)
    }

    func testMenuTitle() {
        XCTAssertEqual(SignInPresentation.menuTitle(for: request("a", "Cursor")), "Approve sign-in for Cursor\u{2026}")
    }

    func testTruncatedLabelDoesNotStackEllipses() {
        let label = SignInPresentation.sanitizedLabel(String(repeating: "x", count: 100))
        XCTAssertTrue(SignInPresentation.menuTitle(for: request("a", label)).hasSuffix("x\u{2026}"))
    }

    func testMenuItemOpensTheConsentPageThroughWebUILink() {
        let path = SignInPresentation.webUIPath(requestID: "ab/c d")
        XCTAssertEqual(path, "/authorize?request=ab%2Fc%20d")
        let url = WebUILink.url(base: "http://127.0.0.1:8080/", path: path, apiKey: "k&ey")
        XCTAssertEqual(url?.absoluteString, "http://127.0.0.1:8080/ui/authorize?request=ab%2Fc%20d&apikey=k%26ey")
    }

    // MARK: SSE routing and list reload

    func testSSEEventsAddAndRemoveARequest() async {
        let state = AppState()
        let manager = CoreProcessManager(
            appState: state,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: NSTemporaryDirectory() + "signin-\(UUID().uuidString).sock")
        await manager.handleSSEEvent(
            SSEEvent(event: "oauth.authorization_pending",
                     data: #"{"payload":{"request_id":"r1","client_id":"cursor","client_label":"Cursor"}}"#,
                     retry: nil, id: nil),
            generation: state.connectionGeneration)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["r1"])
        await manager.handleSSEEvent(
            SSEEvent(event: "oauth.authorization_decided",
                     data: #"{"payload":{"request_id":"r1","status":"approved"}}"#, retry: nil, id: nil),
            generation: state.connectionGeneration)
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    func testListRequestReturnsNilWhenTheFeatureIsOff() async throws {
        ConnectStubURLProtocol.statusCode = 404
        ConnectStubURLProtocol.responseBody = Data(#"{"success":false,"error":"browser login is turned off"}"#.utf8)
        let result = try await ConnectStubURLProtocol.makeClient().signInRequests()
        XCTAssertNil(result)
        XCTAssertEqual(ConnectStubURLProtocol.recorded.last?.url, "http://127.0.0.1:8080/api/v1/oauth/authorizations")
    }

    func testListRequestDecodesPendingSummaries() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(
            #"[{"id":"r1","client_label":"Cursor","created_at":"2026-11-23T10:00:00Z","expires_at":"2026-11-23T10:10:00Z"}]"#)
        let result = try await ConnectStubURLProtocol.makeClient().signInRequests()
        XCTAssertEqual(result?.map(\.id), ["r1"])
    }
}
