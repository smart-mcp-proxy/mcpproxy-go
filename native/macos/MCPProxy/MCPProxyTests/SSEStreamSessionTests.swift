import XCTest
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
/// What they still cannot see: that `CoreProcessManager.startSSEStream`
/// actually routes through `SSEStreamSession`. That body is deliberately a
/// single call so the omission would be visible on sight; the actor has no
/// injection seam for its SSE client, and adding one is a larger change than
/// this pins.
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
