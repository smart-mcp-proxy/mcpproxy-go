import XCTest
@testable import MCPProxy

private final class CallCounter: @unchecked Sendable {
    private let lock = NSLock()
    private(set) var value = 0
    func next() -> Int { lock.lock(); defer { lock.unlock() }; value += 1; return value }
}

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
        let state = connectedState()
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

    // MARK: Review round 1 (P2 x3)

    private func summary(_ id: String, _ label: String = "App", ttl: TimeInterval = 600) -> SignInWire.Summary {
        let iso = ISO8601DateFormatter().string(from: Date().addingTimeInterval(ttl))
        return SignInWire.Summary(id: id, clientLabel: label, expiresAt: iso)
    }

    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    /// P2-1: a reload that was fetched before an SSE decision must not bring
    /// the decided request back.
    func testReloadFetchedBeforeADecisionDoesNotResurrectIt() {
        let state = connectedState()
        state.addSignInRequest(id: "r1", rawLabel: "Cursor")
        let ticket = state.signInReloadTicket()          // fetch starts, sees r1
        state.removeSignInRequest(id: "r1")               // SSE: decided
        XCTAssertEqual(state.applySignInReload([summary("r1")], ticket: ticket), .stale)
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    /// P2-1: ... and must not drop a request that arrived while it was in flight.
    func testReloadFetchedBeforeANewRequestDoesNotDropIt() {
        let state = connectedState()
        let ticket = state.signInReloadTicket()          // fetch starts, sees nothing
        state.addSignInRequest(id: "new", rawLabel: "Fresh") // SSE: pending
        XCTAssertEqual(state.applySignInReload([], ticket: ticket), .stale)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["new"])
    }

    func testReloadWithNoInterveningEventApplies() {
        let state = connectedState()
        let ticket = state.signInReloadTicket()
        XCTAssertEqual(state.applySignInReload([summary("a")], ticket: ticket), .applied)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["a"])
    }

    func testFeatureOffAnswerClearsWhenNothingIntervened() {
        let state = connectedState()
        state.addSignInRequest(id: "r1", rawLabel: "Cursor")
        let ticket = state.signInReloadTicket()
        XCTAssertEqual(state.applySignInReload(nil, ticket: ticket), .applied)
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    func testStaleFeatureOffAnswerDoesNotClearANewerRequest() {
        let state = connectedState()
        let ticket = state.signInReloadTicket()
        state.addSignInRequest(id: "new", rawLabel: "Fresh")
        XCTAssertEqual(state.applySignInReload(nil, ticket: ticket), .stale)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["new"])
    }

    /// The manager retries a stale reload and ends with the core's list.
    func testManagerRefetchesAfterAStaleReload() async {
        let state = connectedState()
        let manager = CoreProcessManager(
            appState: state, notificationService: NotificationService(deliveryEnabled: false),
            socketPath: NSTemporaryDirectory() + "signin-\(UUID().uuidString).sock")
        let counter = CallCounter()
        let fresh = summary("sse", "Via SSE")
        await manager.setSignInRequestsFetcher {
            let n = counter.next()
            if n == 1 {
                // an SSE event lands while the first response is in flight
                await MainActor.run { _ = state.addSignInRequest(id: "sse", rawLabel: "Via SSE") }
                return []
            }
            return [fresh]
        }
        await manager.refreshSignInRequests()
        XCTAssertEqual(counter.value, 2)
        XCTAssertEqual(state.signInRequests.requests.map(\.id), ["sse"])
    }

    /// P2-2: leaving .connected forgets the old core's requests.
    func testLeavingConnectedClearsRequests() {
        let state = connectedState()
        state.addSignInRequest(id: "r1", rawLabel: "Cursor")
        state.coreState = .reconnecting(attempt: 1)
        XCTAssertTrue(state.signInRequests.isEmpty)
        state.coreState = .connected
        XCTAssertTrue(state.signInRequests.isEmpty, "a restarted core must not inherit the old requests")
    }

    /// P2-2: a response issued to the previous connection is dropped.
    func testDelayedOldGenerationReloadIsDropped() {
        let state = connectedState()
        let ticket = state.signInReloadTicket()
        state.coreState = .reconnecting(attempt: 1)
        state.coreState = .connected                      // a new core
        XCTAssertEqual(state.applySignInReload([summary("old")], ticket: ticket), .stale)
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    /// P2-2: an SSE event of an old stream is ignored.
    func testSSEEventFromAnOldGenerationIsIgnored() async {
        let state = connectedState()
        let oldGeneration = state.connectionGeneration
        state.coreState = .reconnecting(attempt: 1)
        state.coreState = .connected
        let manager = CoreProcessManager(
            appState: state, notificationService: NotificationService(deliveryEnabled: false),
            socketPath: NSTemporaryDirectory() + "signin-\(UUID().uuidString).sock")
        await manager.handleSSEEvent(
            SSEEvent(event: "oauth.authorization_pending",
                     data: #"{"payload":{"request_id":"old","client_label":"Old"}}"#, retry: nil, id: nil),
            generation: oldGeneration)
        XCTAssertTrue(state.signInRequests.isEmpty)
    }

    /// P2-3: the menu never lists a request past its deadline, and selecting
    /// one that expired since the menu was built opens nothing.
    func testMenuRowsSkipExpiredRequestsAndSelectionRechecks() {
        let state = connectedState()
        let t0 = Date(timeIntervalSince1970: 1_800_000_000)
        state.addSignInRequest(id: "soon", rawLabel: "Soon", now: t0.addingTimeInterval(-590)) // expires t0+10
        state.addSignInRequest(id: "later", rawLabel: "Later", now: t0)                          // expires t0+600
        let rows = SignInPresentation.menuRows(state.signInRequests, now: t0.addingTimeInterval(20))
        XCTAssertEqual(rows.shown.map(\.id), ["later"])
        XCTAssertEqual(rows.overflow.count, 0)
        XCTAssertNil(state.signInPath(forRequest: "soon", now: t0.addingTimeInterval(20)))
        XCTAssertEqual(state.signInPath(forRequest: "later", now: t0.addingTimeInterval(20)),
                       "/authorize?request=later")
    }

    func testMenuRowsFoldPastTheLimit() {
        var store = SignInRequestStore()
        let t0 = Date(timeIntervalSince1970: 1_800_000_000)
        for i in 0..<7 {
            store.add(SignInRequest(id: "r\(i)", clientLabel: "A\(i)", expiresAt: t0.addingTimeInterval(600)), now: t0)
        }
        let rows = SignInPresentation.menuRows(store, now: t0)
        XCTAssertEqual(rows.shown.count, SignInPresentation.maxMenuRows)
        XCTAssertEqual(rows.overflow.count, 2)
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
