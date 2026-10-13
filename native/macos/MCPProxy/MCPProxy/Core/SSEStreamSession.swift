// SSEStreamSession.swift
// MCPProxy
//
// One run of one SSE stream, on behalf of one connection.

import Foundation

/// Consumes an SSE stream and tags every event with the connection it belongs to.
///
/// This exists as its own type for one reason: WHERE the connection generation
/// is captured is a correctness decision, and it was previously made inside a
/// `Task` closure in `CoreProcessManager` that no test could reach. A test that
/// calls the publish helper with an old generation proves the guard works and
/// says nothing about the capture — it passes just as happily against the
/// broken version that reads the generation when the event arrives, which after
/// a reconnect is the NEW generation and sails through the guard.
///
/// The rule this type owns: capture once, before the stream is even opened, and
/// hand that same value to every event the stream ever delivers.
enum SSEStreamSession {

    /// Run one stream to completion.
    ///
    /// - Parameters:
    ///   - captureGeneration: read ONCE, before `open`, so that a reconnect
    ///     during the connect itself invalidates this session rather than being
    ///     silently adopted by it. Returning nil abandons the session (the owner
    ///     has gone away).
    ///   - open: produces the stream. Called after the generation is captured.
    ///   - handle: receives each event together with the generation captured at
    ///     the top. Never the generation current at delivery time.
    static func run(
        captureGeneration: () async -> Int?,
        open: () async -> AsyncStream<SSEEvent>?,
        handle: (SSEEvent, Int) async -> Void
    ) async {
        guard let generation = await captureGeneration() else { return }
        guard let stream = await open() else { return }

        for await event in stream {
            guard !Task.isCancelled else { break }
            await handle(event, generation)
        }
    }
}
