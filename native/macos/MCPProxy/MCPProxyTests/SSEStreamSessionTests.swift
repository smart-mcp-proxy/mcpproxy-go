import XCTest
import Combine
@testable import MCPProxy

/// WHERE the connection generation is captured, not merely whether it is
/// checked.
///
/// The publish-helper tests in `GlanceReconnectGenerationTests` hand
/// `prependGlanceActivity` a generation the test picked, so they verify the
/// final guard and nothing before it. They pass identically against the
/// implementation that reads the generation when an event ARRIVES — which,
/// after a reconnect, is the new generation, so the guard admits the dead
/// core's row and the whole fix evaporates. These tests drive the stream
/// instead, and force the reconnect into the window between the stream opening
/// and the event being delivered.
///
/// `CoreProcessManagerSSEWiringTests`, below, covers the other half: that
/// production actually routes through the helper. Keeping the body to a single
/// call was not enough — reinstating the arrival-time read directly in
/// `startSSEStream` left every test in this file green.
@MainActor
final class SSEStreamSessionTests: XCTestCase {

    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    private static func entry(id: String) throws -> ActivityEntry {
        let json = """
        {"id":"\(id)","type":"tool_call","status":"success","timestamp":"2026-07-29T11:00:00Z",
         "server_name":"srv","tool_name":"t","request_id":"\(id)"}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(ActivityEntry.self, from: json.data(using: .utf8)!)
    }

    /// Run one session whose stream delivers a single event, with `between`
    /// executed after the stream has been opened and before the event is
    /// yielded. Returns when the session has finished.
    ///
    /// The handshake is deliberately on `open`, NOT on `captureGeneration`.
    /// `open` is called at the same point by every implementation, so the test's
    /// sequencing does not depend on the thing it is testing; hanging the
    /// handshake off the capture makes a wrong implementation fail by timeout
    /// instead of by assertion, which is a much worse diagnostic.
    private func runSession(
        state: AppState,
        entryID: String,
        between: @escaping @MainActor () -> Void
    ) async throws {
        let (stream, continuation) = AsyncStream<SSEEvent>.makeStream()
        let opened = expectation(description: "the session opened its stream")
        let entry = try Self.entry(id: entryID)

        let session = Task {
            await SSEStreamSession.run(
                captureGeneration: { await MainActor.run { state.connectionGeneration } },
                open: {
                    await MainActor.run { opened.fulfill() }
                    return stream
                },
                handle: { _, generation in
                    await MainActor.run {
                        state.prependGlanceActivity(entry, generation: generation)
                    }
                }
            )
        }

        await fulfillment(of: [opened], timeout: 5)
        between()
        continuation.yield(SSEEvent(event: GlanceEvent.upstreamCompleted, data: "{}", retry: nil, id: nil))
        continuation.finish()
        await session.value
    }

    /// The core dies and comes back while the stream is open. The event was read
    /// from the OLD core's stream, so it must not reach the new core's feed —
    /// even though `coreState` is `.connected` again by the time it publishes.
    func testAnEventDeliveredAfterAReconnectDoesNotPublish() async throws {
        let state = connectedState()

        try await runSession(state: state, entryID: "from-the-dead-core") {
            state.coreState = .reconnecting(attempt: 1)
            state.coreState = .connected
        }

        XCTAssertTrue(state.glanceActivity.isEmpty,
                      "an event from the previous connection's stream published into the new one")
    }

    /// Positive control: with no reconnection the same session publishes, so the
    /// test above pins the reconnect rather than a stream that never delivers.
    func testAnEventOnAnUninterruptedStreamPublishes() async throws {
        let state = connectedState()

        try await runSession(state: state, entryID: "live") {}

        XCTAssertEqual(state.glanceActivity.map(\.id), ["live"])
    }

    /// A reconnect DURING the connect itself invalidates the session too: the
    /// generation is captured before the stream is opened, so a session that
    /// loses its core while connecting cannot adopt the replacement.
    func testAReconnectWhileTheStreamIsOpeningInvalidatesTheSession() async throws {
        let state = connectedState()
        let (stream, continuation) = AsyncStream<SSEEvent>.makeStream()
        let entry = try Self.entry(id: "from-the-dead-core")

        let session = Task {
            await SSEStreamSession.run(
                captureGeneration: { await MainActor.run { state.connectionGeneration } },
                open: {
                    await MainActor.run {
                        state.coreState = .reconnecting(attempt: 1)
                        state.coreState = .connected
                    }
                    return stream
                },
                handle: { _, generation in
                    await MainActor.run {
                        state.prependGlanceActivity(entry, generation: generation)
                    }
                }
            )
        }

        continuation.yield(SSEEvent(event: GlanceEvent.upstreamCompleted, data: "{}", retry: nil, id: nil))
        continuation.finish()
        await session.value

        XCTAssertTrue(state.glanceActivity.isEmpty)
    }
}

/// The production wiring, driven end to end through `CoreProcessManager`.
///
/// `SSEStreamSessionTests` pins the rule; this pins that the manager uses it.
/// Both are needed, and the reason is concrete: with the helper left correct,
/// reinstating the arrival-time generation read inside `startSSEStream` passed
/// every other test in the suite. The seam this needs is one internal method on
/// the actor (`installSSEClient`) plus an internal `startSSEStream`.
@MainActor
final class CoreProcessManagerSSEWiringTests: XCTestCase {

    /// A stream source the test drives by hand.
    private actor StubSSEClient: SSEStreaming {
        private let stream: AsyncStream<SSEEvent>
        private let onConnect: @Sendable () -> Void

        init(stream: AsyncStream<SSEEvent>, onConnect: @escaping @Sendable () -> Void) {
            self.stream = stream
            self.onConnect = onConnect
        }

        func connect() async -> AsyncStream<SSEEvent> {
            onConnect()
            return stream
        }

        func disconnect() async {}
    }

    private static func upstreamCompletedPayload(requestID: String) -> String {
        """
        {"payload":{"server_name":"srv","tool_name":"t","status":"success",
         "request_id":"\(requestID)","session_id":"sess-1","duration_ms":5},
         "timestamp":1785340000}
        """
    }

    /// A `status` event, whose handling is deliberately NOT generation-guarded.
    /// It is the test's ordering marker: the manager handles events in stream
    /// order, so once this one's effect is visible the glance event before it
    /// has already been processed. That makes the negative assertion below
    /// deterministic instead of a timeout.
    private static func statusPayload(totalTools: Int) -> String {
        """
        {"upstream_stats":{"total_servers":1,"total_tools":\(totalTools)}}
        """
    }

    /// The core dies and comes back while the manager's stream is open. The row
    /// read from the old core's stream must not reach the new core's feed —
    /// through the real `startSSEStream`, not through the helper directly.
    func testTheManagerCapturesTheGenerationWhenItsStreamOpens() async throws {
        let state = AppState()
        state.coreState = .connected
        let manager = CoreProcessManager(appState: state, notificationService: NotificationService())

        let (stream, continuation) = AsyncStream<SSEEvent>.makeStream()
        let connected = expectation(description: "the manager opened its stream")
        let stub = StubSSEClient(stream: stream) { connected.fulfill() }

        await manager.installSSEClient(stub)
        await manager.startSSEStream()
        await fulfillment(of: [connected], timeout: 5)

        // The core goes away and a new one takes its place.
        state.coreState = .reconnecting(attempt: 1)
        state.coreState = .connected

        continuation.yield(SSEEvent(event: GlanceEvent.upstreamCompleted,
                                    data: Self.upstreamCompletedPayload(requestID: "from-the-dead-core"),
                                    retry: nil, id: nil))
        continuation.yield(SSEEvent(event: "status",
                                    data: Self.statusPayload(totalTools: 77),
                                    retry: nil, id: nil))

        // Wait for the marker, which proves the glance event was handled first.
        let marker = expectation(description: "the status event was handled")
        let sink = state.$totalTools.sink { if $0 == 77 { marker.fulfill() } }
        await fulfillment(of: [marker], timeout: 5)
        sink.cancel()

        XCTAssertTrue(state.glanceActivity.isEmpty,
                      "the manager published a row from the previous connection's stream")

        // Leave `.connected` before the stream ends, so the manager's
        // disconnect path returns immediately instead of trying to reconnect.
        state.coreState = .idle
        continuation.finish()
    }

    /// Positive control through the same path: with no reconnection the row
    /// arrives, so the test above pins the reconnect rather than a stream the
    /// manager never reads.
    func testTheManagerPublishesRowsFromItsOwnConnection() async throws {
        let state = AppState()
        state.coreState = .connected
        let manager = CoreProcessManager(appState: state, notificationService: NotificationService())

        let (stream, continuation) = AsyncStream<SSEEvent>.makeStream()
        let connected = expectation(description: "the manager opened its stream")
        let stub = StubSSEClient(stream: stream) { connected.fulfill() }

        await manager.installSSEClient(stub)
        await manager.startSSEStream()
        await fulfillment(of: [connected], timeout: 5)

        continuation.yield(SSEEvent(event: GlanceEvent.upstreamCompleted,
                                    data: Self.upstreamCompletedPayload(requestID: "live"),
                                    retry: nil, id: nil))

        let published = expectation(description: "the row reached the feed")
        let sink = state.$glanceActivity.sink { if !$0.isEmpty { published.fulfill() } }
        await fulfillment(of: [published], timeout: 5)
        sink.cancel()

        XCTAssertEqual(state.glanceActivity.map(\.requestId), ["live"])

        state.coreState = .idle
        continuation.finish()
    }
}
