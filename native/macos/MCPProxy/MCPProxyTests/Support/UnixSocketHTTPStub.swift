// UnixSocketHTTPStub.swift
// MCPProxyTests/Support
//
// A minimal HTTP/1.1 server bound to a Unix domain socket, used to stand in for
// an externally-managed mcpproxy core.
//
// Why this exists: `CoreProcessManager` discovers a running core purely by
// probing a Unix socket (`SocketTransport.isSocketAvailable` + `GET /ready`).
// To exercise the attach path — and, crucially, what happens when that core
// DISAPPEARS or goes UNRESPONSIVE (GH #926) — a test needs a socket it can
// create, wedge, and destroy at will. Nothing else in the app can fake that:
// the transport is a real `URLProtocol` speaking real HTTP over a real fd.
//
// Deliberately tiny: one request per connection, `Connection: close`, no
// keep-alive, no chunking. That is all `SocketURLProtocol` ever asks for.

import Foundation
#if canImport(Darwin)
import Darwin
#endif

/// What the stub does with a request.
enum StubResponse {
    /// Answer with this status and body.
    case reply(status: Int, body: String)
    /// Accept the request and never answer, for `seconds` — a core that is up
    /// and listening but not responding. The client must time out on its own.
    case hang(seconds: TimeInterval)

    static func json(_ body: String, status: Int = 200) -> StubResponse {
        .reply(status: status, body: body)
    }

    static let notFound = StubResponse.reply(
        status: 404, body: #"{"success":false,"error":"not found"}"#
    )
}

/// An HTTP server on a Unix domain socket.
///
/// `start()`/`stop()` are called from the test thread; the accept loop runs on
/// its own thread. `stop()` closes the listening socket AND any accepted
/// connection (a stub wedged in `.hang` is blocked on one), then waits for the
/// thread to actually exit, so a stopped stub leaves no descriptor or thread
/// behind to race the next test.
final class UnixSocketHTTPStub {

    /// Path of the socket file. Kept short: `sun_path` is 104 bytes.
    let path: String

    /// Maps (method, path) to a response. Anything unmatched gets a 404, which
    /// the manager's refresh helpers treat as non-fatal — exactly as they do
    /// against a real core that lacks an endpoint.
    private let responder: @Sendable (_ method: String, _ path: String) -> StubResponse

    private let stateLock = NSLock()
    private var listenFD: Int32 = -1
    private var clientFD: Int32 = -1
    private var stopped = true
    private var threadRunning = false
    private var requestCounts: [String: Int] = [:]
    private let exited = DispatchSemaphore(value: 0)

    /// - Parameter path: socket path to bind, or `nil` for a fresh temporary one.
    ///   Pass an existing stub's path to simulate the same core coming back up.
    init(
        at path: String? = nil,
        responder: @escaping @Sendable (_ method: String, _ path: String) -> StubResponse
    ) {
        // A path under NSTemporaryDirectory() can exceed sun_path's 104 bytes on
        // some machines; /tmp keeps it comfortably short and is unlink-able.
        self.path = path ?? "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        self.responder = responder
    }

    deinit {
        stop()
    }

    // MARK: - Lifecycle

    /// Bind, listen, and start accepting. Throws if the socket cannot be created.
    func start() throws {
        unlink(path)

        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw StubError.socketFailed(errno) }
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = path.utf8CString
        guard pathBytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(fd)
            throw StubError.pathTooLong(path)
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { sunPathPtr in
            sunPathPtr.withMemoryRebound(to: CChar.self, capacity: pathBytes.count) { dest in
                for i in 0..<pathBytes.count { dest[i] = pathBytes[i] }
            }
        }

        let bindResult = withUnsafePointer(to: &addr) { addrPtr in
            addrPtr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPtr in
                Darwin.bind(fd, sockaddrPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else {
            let err = errno
            Darwin.close(fd)
            throw StubError.bindFailed(err)
        }

        guard Darwin.listen(fd, 16) == 0 else {
            let err = errno
            Darwin.close(fd)
            throw StubError.listenFailed(err)
        }

        stateLock.lock()
        listenFD = fd
        stopped = false
        threadRunning = true
        stateLock.unlock()

        let thread = Thread { [weak self] in
            self?.acceptLoop(fd: fd)
            self?.exited.signal()
        }
        thread.name = "UnixSocketHTTPStub"
        thread.qualityOfService = .userInitiated
        thread.start()
    }

    /// Stop serving and remove the socket file — i.e. "the core died".
    ///
    /// After this returns, `SocketTransport.isSocketAvailable(path:)` is false
    /// and the accept thread has exited.
    func stop() {
        stateLock.lock()
        let alreadyStopped = stopped
        stopped = true
        let listen = listenFD
        let client = clientFD
        listenFD = -1
        clientFD = -1
        let wasRunning = threadRunning
        threadRunning = false
        stateLock.unlock()

        guard !alreadyStopped else { return }

        // Closing both descriptors unblocks accept() and any pending read().
        if listen >= 0 { Darwin.close(listen) }
        if client >= 0 { Darwin.close(client) }
        unlink(path)

        if wasRunning {
            // Bounded: a wedged handler polls `stopped` on a 20ms cadence.
            _ = exited.wait(timeout: .now() + 2.0)
        }
    }

    // MARK: - Observability

    /// How many requests this stub has received for `path`.
    ///
    /// Used to count connect attempts: `connectToCore()` fetches
    /// `/api/v1/info` exactly once, so that count IS the number of
    /// (re)connections — the observable form of "did we attach twice?".
    func requestCount(path: String) -> Int {
        stateLock.lock()
        defer { stateLock.unlock() }
        return requestCounts[path] ?? 0
    }

    private func recordRequest(path: String) {
        stateLock.lock()
        requestCounts[path, default: 0] += 1
        stateLock.unlock()
    }

    private var isStopped: Bool {
        stateLock.lock()
        defer { stateLock.unlock() }
        return stopped
    }

    // MARK: - Accept loop

    private func acceptLoop(fd: Int32) {
        while !isStopped {
            let client = Darwin.accept(fd, nil, nil)
            if client < 0 {
                if isStopped { return }
                if errno == EINTR { continue }
                return
            }
            var on: Int32 = 1
            setsockopt(client, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))

            stateLock.lock()
            let stoppedNow = stopped
            if !stoppedNow { clientFD = client }
            stateLock.unlock()
            if stoppedNow {
                Darwin.close(client)
                return
            }

            handle(client: client)
            closeClient(client)
        }
    }

    /// Close the accepted connection unless `stop()` already claimed it.
    private func closeClient(_ fd: Int32) {
        stateLock.lock()
        let owned = clientFD == fd
        if owned { clientFD = -1 }
        stateLock.unlock()
        if owned { Darwin.close(fd) }
    }

    private func handle(client: Int32) {
        guard let head = readRequestHead(fd: client) else { return }
        let requestLine = head.components(separatedBy: "\r\n").first ?? ""
        let parts = requestLine.split(separator: " ")
        let method = parts.count > 0 ? String(parts[0]) : "GET"
        let rawPath = parts.count > 1 ? String(parts[1]) : "/"
        let pathOnly = rawPath.components(separatedBy: "?").first ?? rawPath
        recordRequest(path: pathOnly)

        switch responder(method, pathOnly) {
        case .hang(let seconds):
            // Hold the connection open without answering. Poll `stopped` so
            // teardown is never blocked for the full duration.
            let deadline = Date().addingTimeInterval(seconds)
            while Date() < deadline && !isStopped {
                Thread.sleep(forTimeInterval: 0.02)
            }
        case .reply(let status, let body):
            let bodyData = Data(body.utf8)
            var headers = "HTTP/1.1 \(status) \(Self.reason(status))\r\n"
            headers += "Content-Type: application/json\r\n"
            headers += "Content-Length: \(bodyData.count)\r\n"
            headers += "Connection: close\r\n\r\n"
            var out = Data(headers.utf8)
            out.append(bodyData)
            write(fd: client, data: out)
        }
    }

    /// Read until the end of the request headers. Bodies are ignored — the tray
    /// only ever GETs on this path.
    private func readRequestHead(fd: Int32) -> String? {
        var data = Data()
        let bufSize = 4096
        let buf = UnsafeMutableRawPointer.allocate(byteCount: bufSize, alignment: 1)
        defer { buf.deallocate() }
        let separator = Data([0x0D, 0x0A, 0x0D, 0x0A])

        while data.range(of: separator) == nil {
            let n = Darwin.read(fd, buf, bufSize)
            if n <= 0 { break }
            data.append(buf.assumingMemoryBound(to: UInt8.self), count: n)
            if data.count > 64 * 1024 { break }
        }
        return String(data: data, encoding: .utf8)
    }

    private func write(fd: Int32, data: Data) {
        data.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return }
            var written = 0
            while written < data.count {
                let n = Darwin.write(fd, base.advanced(by: written), data.count - written)
                if n <= 0 { return }
                written += n
            }
        }
    }

    private static func reason(_ status: Int) -> String {
        switch status {
        case 200: return "OK"
        case 404: return "Not Found"
        case 500: return "Internal Server Error"
        case 503: return "Service Unavailable"
        default: return "Status"
        }
    }

    enum StubError: Error {
        case socketFailed(Int32)
        case bindFailed(Int32)
        case listenFailed(Int32)
        case pathTooLong(String)
    }
}

// MARK: - A stand-in for a core

/// Lets a test change how `/ready` behaves while the stub keeps running — a
/// core that is still listening but has stopped answering.
final class ReadyBehaviour: @unchecked Sendable {
    private let lock = NSLock()
    private var value: StubResponse = .json(#"{"success":true}"#)

    var current: StubResponse {
        get { lock.lock(); defer { lock.unlock() }; return value }
        set { lock.lock(); value = newValue; lock.unlock() }
    }
}

extension UnixSocketHTTPStub {

    /// A stub that answers the two calls `CoreProcessManager.connectToCore()`
    /// requires — `/ready` and `/api/v1/info` — and 404s everything else.
    ///
    /// `web_ui_url` points at 127.0.0.1:1, a port nothing can be listening on,
    /// so the SSE client (which is deliberately TCP-only) fails fast and retries
    /// in the background instead of reaching a real core on :8080.
    static func healthyCore(
        at socketPath: String? = nil,
        ready: ReadyBehaviour = ReadyBehaviour()
    ) -> UnixSocketHTTPStub {
        UnixSocketHTTPStub(at: socketPath) { _, path in
            switch path {
            case "/ready":
                return ready.current
            case "/api/v1/info":
                return .json("""
                {"success":true,"data":{
                  "version":"0.0.0-test",
                  "web_ui_url":"http://127.0.0.1:1/ui/?apikey=test-api-key",
                  "listen_addr":"127.0.0.1:1",
                  "endpoints":{"http":"http://127.0.0.1:1","socket":"unix"}
                }}
                """)
            default:
                return .notFound
            }
        }
    }
}
