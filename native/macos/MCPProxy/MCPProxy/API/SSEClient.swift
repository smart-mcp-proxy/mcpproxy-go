import Foundation

// MARK: - SSE Parser

/// Incremental parser for the Server-Sent Events text protocol.
///
/// Feed lines one at a time via `feed(_:)`. When a complete event is assembled
/// (signaled by a blank line), the method returns a non-nil `SSEEvent`.
///
/// Spec reference: https://html.spec.whatwg.org/multipage/server-sent-events.html
struct SSEParser {
    private var eventType: String = ""
    private var dataBuffer: String = ""
    private var lastEventId: String?
    private var retryMs: Int?

    /// Feed a single line (without trailing newline) to the parser.
    /// Returns an `SSEEvent` when a blank line completes a pending event.
    mutating func feed(_ line: String) -> SSEEvent? {
        // Blank line dispatches the event
        if line.isEmpty {
            return dispatchEvent()
        }

        // Lines starting with ':' are comments — ignore
        if line.hasPrefix(":") {
            return nil
        }

        // Split on first ':'
        let field: String
        let value: String
        if let colonIndex = line.firstIndex(of: ":") {
            field = String(line[line.startIndex..<colonIndex])
            let afterColon = line.index(after: colonIndex)
            if afterColon < line.endIndex && line[afterColon] == " " {
                // Skip single leading space after colon
                value = String(line[line.index(after: afterColon)...])
            } else {
                value = String(line[afterColon...])
            }
        } else {
            // Line with no colon: treat entire line as field name with empty value
            field = line
            value = ""
        }

        switch field {
        case "event":
            eventType = value
        case "data":
            if !dataBuffer.isEmpty {
                dataBuffer += "\n"
            }
            dataBuffer += value
        case "id":
            // Per spec, id must not contain null
            if !value.contains("\0") {
                lastEventId = value
            }
        case "retry":
            if let ms = Int(value) {
                retryMs = ms
            }
        default:
            // Unknown fields are ignored per spec
            break
        }

        return nil
    }

    /// Assemble and return the current buffered event, then reset buffers.
    private mutating func dispatchEvent() -> SSEEvent? {
        // If data buffer is empty, no event to dispatch (per spec)
        guard !dataBuffer.isEmpty else {
            // Still reset event type for next event
            eventType = ""
            return nil
        }

        let event = SSEEvent(
            event: eventType.isEmpty ? "message" : eventType,
            data: dataBuffer,
            retry: retryMs,
            id: lastEventId
        )

        // Reset per-event state; id and retry persist across events per spec
        eventType = ""
        dataBuffer = ""
        // Note: retryMs and lastEventId intentionally NOT reset (they persist)

        return event
    }

    /// Reset the parser to its initial state.
    mutating func reset() {
        eventType = ""
        dataBuffer = ""
        lastEventId = nil
        retryMs = nil
    }
}

// MARK: - SSE Client

/// Actor that manages a streaming SSE connection to the mcpproxy `/events` endpoint.
///
/// Usage:
/// ```swift
/// let client = SSEClient(baseURL: "http://127.0.0.1:8080")
/// for await event in client.connect() {
///     print(event.event, event.data)
/// }
/// ```
actor SSEClient {
    private var task: Task<Void, Never>?
    private let session: URLSession
    private let baseURL: String
    private let apiKey: String?

    /// Retry interval in seconds. Updated from the SSE `retry:` field.
    private var retryInterval: TimeInterval = 5.0

    /// Last event ID for reconnection (sent as `Last-Event-ID` header).
    private var lastEventId: String?

    /// Whether the client is currently connected.
    private(set) var isConnected: Bool = false

    /// Create an SSE client.
    ///
    /// - Parameters:
    ///   - socketPath: Unix socket path, or `nil` for default, or empty string for TCP-only.
    ///   - baseURL: TCP base URL of the mcpproxy core.
    ///   - apiKey: Optional API key for authentication.
    init(socketPath: String? = nil, baseURL: String = "http://127.0.0.1:8080", apiKey: String? = nil) {
        self.baseURL = baseURL
        self.apiKey = apiKey

        // IMPORTANT: SSE MUST use TCP, not the Unix socket URLProtocol.
        //
        // SocketURLProtocol buffers the entire response before delivering it
        // (reads until EOF in readResponse). SSE is an infinite stream that
        // never sends EOF, so the URLProtocol hangs forever.
        //
        // URLSession.bytes(for:) needs real streaming which only works with
        // the native TCP transport. The core listens on both socket AND TCP,
        // so SSE goes via TCP (127.0.0.1:8080) while API calls use the socket.
        self.session = SSEClient.makeLongLivedSession(useSocket: false, socketPath: nil)
    }

    /// Create an SSE client with an explicit URLSession (for testing).
    init(session: URLSession, baseURL: String = "http://127.0.0.1:8080", apiKey: String? = nil) {
        self.session = session
        self.baseURL = baseURL
        self.apiKey = apiKey
    }

    /// Connect to the SSE stream and return an `AsyncStream` of events.
    ///
    /// The stream automatically reconnects on failure using the retry interval.
    /// Cancel the consuming task or call `disconnect()` to stop.
    func connect() -> AsyncStream<SSEEvent> {
        // Cancel any existing connection
        task?.cancel()

        let (stream, continuation) = AsyncStream<SSEEvent>.makeStream()

        let streamTask = Task { [weak self] in
            guard let self else {
                continuation.finish()
                return
            }

            var reconnectAttempt = 0

            while !Task.isCancelled {
                do {
                    try await self.streamEvents(continuation: continuation)
                    // If streamEvents returns normally, the connection closed gracefully
                    reconnectAttempt = 0
                } catch is CancellationError {
                    break
                } catch {
                    reconnectAttempt += 1
                }

                await self.setConnected(false)

                // Don't reconnect if cancelled
                guard !Task.isCancelled else { break }

                // Wait before reconnecting
                let delay = await self.currentRetryInterval
                let delayNs = UInt64(delay * 1_000_000_000)
                do {
                    try await Task.sleep(nanoseconds: delayNs)
                } catch {
                    break // Cancelled during sleep
                }
            }

            await self.setConnected(false)
            continuation.finish()
        }

        task = streamTask

        continuation.onTermination = { @Sendable _ in
            streamTask.cancel()
        }

        return stream
    }

    /// Disconnect the SSE stream.
    func disconnect() {
        task?.cancel()
        task = nil
        isConnected = false
    }

    // MARK: - Private

    private var currentRetryInterval: TimeInterval {
        retryInterval
    }

    private func setConnected(_ value: Bool) {
        isConnected = value
    }

    /// Stream events from a single SSE connection until it closes or errors.
    private func streamEvents(continuation: AsyncStream<SSEEvent>.Continuation) async throws {
        guard let url = URL(string: baseURL + "/events") else {
            throw SSEError.invalidURL
        }

        var request = URLRequest(url: url)
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")
        request.timeoutInterval = .infinity

        // Send Last-Event-ID for reconnection
        if let lastEventId {
            request.setValue(lastEventId, forHTTPHeaderField: "Last-Event-ID")
        }

        // Attach API key if configured
        if let apiKey, !apiKey.isEmpty {
            request.setValue(apiKey, forHTTPHeaderField: "X-API-Key")
        }

        let (bytes, response) = try await session.bytes(for: request)

        guard let httpResponse = response as? HTTPURLResponse,
              (200...299).contains(httpResponse.statusCode) else {
            let statusCode = (response as? HTTPURLResponse)?.statusCode ?? 0
            throw APIClientError.httpError(statusCode: statusCode, message: "SSE connection failed")
        }

        isConnected = true

        var parser = SSEParser()
        var lineBuffer = ""

        for try await byte in bytes {
            let char = Character(UnicodeScalar(byte))

            if char == "\n" {
                // Process the accumulated line
                if let event = parser.feed(lineBuffer) {
                    // Update retry interval if the event carries one
                    if let retry = event.retry {
                        retryInterval = TimeInterval(retry) / 1000.0
                    }
                    // Track last event ID
                    if let eventId = event.id {
                        lastEventId = eventId
                    }
                    continuation.yield(event)
                }
                lineBuffer = ""
            } else if char == "\r" {
                // Skip CR; we handle LF above (works for both \r\n and \n)
                continue
            } else {
                lineBuffer.append(char)
            }
        }

        // Process any remaining line when the stream ends
        if !lineBuffer.isEmpty {
            if let event = parser.feed(lineBuffer) {
                continuation.yield(event)
            }
            // Dispatch pending event on stream close
            if let event = parser.feed("") {
                continuation.yield(event)
            }
        }

        isConnected = false
    }

    /// Create a URLSession with very long timeouts suitable for SSE.
    private static func makeLongLivedSession(useSocket: Bool, socketPath: String?) -> URLSession {
        let config = URLSessionConfiguration.default

        if useSocket {
            config.protocolClasses = [SocketURLProtocol.self]
            if let socketPath {
                SocketURLProtocol.overrideSocketPath = socketPath
            }
        }

        // SSE connections are long-lived; use very generous timeouts
        config.timeoutIntervalForRequest = 3600   // 1 hour
        config.timeoutIntervalForResource = 86400 // 24 hours
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never

        // Disable caching for streaming
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.urlCache = nil

        return URLSession(configuration: config)
    }
}
