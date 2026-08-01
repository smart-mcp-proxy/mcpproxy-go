import Foundation
#if canImport(Darwin)
import Darwin
#endif

// MARK: - Unix Domain Socket URL Protocol

/// Custom `URLProtocol` that routes HTTP requests over a Unix domain socket.
///
/// Register on a `URLSessionConfiguration` via
/// `config.protocolClasses = [SocketURLProtocol.self]`
/// to transparently redirect all HTTP traffic through the mcpproxy socket.
///
/// The protocol intercepts requests whose host is `localhost` or `127.0.0.1`
/// and rewrites the transport layer to use the Unix socket at `~/.mcpproxy/mcpproxy.sock`.
final class SocketURLProtocol: URLProtocol {

    /// Default socket path used by mcpproxy core.
    static let socketPath: String = {
        NSHomeDirectory() + "/.mcpproxy/mcpproxy.sock"
    }()

    /// Header carrying an opaque route token identifying the socket a
    /// particular session must use.
    ///
    /// The route travels PER SESSION, not in a global mutable path. It used to
    /// live in a mutable static, which meant creating any second client —
    /// another `CoreProcessManager`, a probe, a concurrent test — silently
    /// redirected every existing client to the newest path. That is a
    /// liveness-detector hazard, not just a test smell: a client could report a
    /// different core's health as its own.
    ///
    /// The value is a TOKEN, not the path. `URLSessionConfiguration`
    /// `httpAdditionalHeaders` are attached to every request the session makes,
    /// including ones this protocol declines to intercept and ones following a
    /// redirect — so whatever goes in here must be safe to disclose. A UUID is;
    /// `/Users/<name>/.mcpproxy/mcpproxy.sock` is not.
    static let routeHeader = "X-MCPProxy-Route"

    /// Header marking a request that must NEVER leave over TCP.
    ///
    /// The unrouted fallback below ("intercept only if the default socket
    /// exists, otherwise let it go out over TCP") is right for reads and wrong
    /// for administrative writes: if the socket disappears mid-session, a
    /// connect/undo/disconnect would silently ride 127.0.0.1:8080 instead, which
    /// is exactly the non-socket case the spec forbids (research D6). A strict
    /// request is intercepted regardless, so it fails loudly instead.
    ///
    /// Like the route header it is a transport hint and never goes on the wire.
    static let strictSocketHeader = "X-MCPProxy-Strict-Socket"

    /// Whether this request refuses a TCP fallback.
    static func isStrictSocket(_ request: URLRequest) -> Bool {
        request.value(forHTTPHeaderField: strictSocketHeader) != nil
    }

    /// The interception rule, as a pure function of the request and whether the
    /// default socket file exists — so it can be tested without a socket.
    static func shouldIntercept(request: URLRequest, defaultSocketExists: Bool) -> Bool {
        // A request that carries a route is PINNED to that socket (see canInit).
        if routedSocketPath(for: request) != nil { return true }
        // A strict request may not fall back to TCP, ever.
        if isStrictSocket(request) { return true }
        return defaultSocketExists
    }

    /// token -> socket path. Written once per session at creation and read on
    /// every request. Not an alias for "the current path": each entry belongs to
    /// exactly one session, which is the whole point.
    private static let routes = RouteTable()

    /// Register a socket path and return the token that routes to it.
    static func makeRoute(to socketPath: String) -> String {
        routes.add(socketPath)
    }

    /// Resolve a route token back to its socket path.
    static func routes(for token: String) -> String? {
        routes.path(for: token)
    }

    /// Socket path this request must be routed over, or nil when the request
    /// carries no route (a client constructed without an explicit path).
    static func routedSocketPath(for request: URLRequest) -> String? {
        guard let token = request.value(forHTTPHeaderField: routeHeader) else { return nil }
        return routes.path(for: token)
    }

    /// Socket path this request must use, falling back to the default.
    static func effectiveSocketPath(for request: URLRequest) -> String {
        routedSocketPath(for: request) ?? socketPath
    }

    /// Thread-safe token table.
    final class RouteTable: @unchecked Sendable {
        private let lock = NSLock()
        private var paths: [String: String] = [:]

        func add(_ path: String) -> String {
            let token = UUID().uuidString
            lock.lock()
            paths[token] = path
            lock.unlock()
            return token
        }

        func path(for token: String) -> String? {
            lock.lock()
            defer { lock.unlock() }
            return paths[token]
        }
    }

    /// Active read task, retained for cancellation.
    private var socketFD: Int32 = -1
    private let socketLock = NSLock()
    private var readThread: Thread?
    private var isCancelled = false

    // MARK: - URLProtocol Overrides

    override class func canInit(with request: URLRequest) -> Bool {
        guard let url = request.url,
              let scheme = url.scheme?.lowercased(),
              (scheme == "http" || scheme == "https"),
              let host = url.host?.lowercased(),
              (host == "localhost" || host == "127.0.0.1") else {
            return false
        }
        // A request that carries a route is PINNED to that socket: intercept it
        // even when the socket is missing, and let it fail. Falling back to TCP
        // there would silently send a client that was told "talk to this core"
        // to whatever happens to be listening on 127.0.0.1:8080 — a different
        // core's health reported as this one's, which is precisely the failure
        // the routing exists to prevent. A strict request refuses the fallback
        // for the same reason. Everything else keeps the legacy behaviour:
        // intercept only if the default socket exists.
        return shouldIntercept(
            request: request,
            defaultSocketExists: FileManager.default.fileExists(atPath: socketPath)
        )
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            let error = NSError(domain: NSPOSIXErrorDomain, code: Int(errno),
                                userInfo: [NSLocalizedDescriptionKey: "Failed to create Unix socket"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }
        socketFD = fd

        // Build sockaddr_un
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let path = Self.effectiveSocketPath(for: request)
        let pathBytes = path.utf8CString
        guard pathBytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(fd)
            let error = NSError(domain: NSPOSIXErrorDomain, code: Int(ENAMETOOLONG),
                                userInfo: [NSLocalizedDescriptionKey: "Socket path too long"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { sunPathPtr in
            sunPathPtr.withMemoryRebound(to: CChar.self, capacity: pathBytes.count) { dest in
                for i in 0..<pathBytes.count {
                    dest[i] = pathBytes[i]
                }
            }
        }

        // Connect
        let connectResult = withUnsafePointer(to: &addr) { addrPtr in
            addrPtr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPtr in
                Darwin.connect(fd, sockaddrPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connectResult == 0 else {
            let connectErrno = errno
            Darwin.close(fd)
            let error = NSError(domain: NSPOSIXErrorDomain, code: Int(connectErrno),
                                userInfo: [NSLocalizedDescriptionKey: "Failed to connect to Unix socket at \(path): \(String(cString: strerror(connectErrno)))"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        // Build HTTP/1.1 request bytes
        let requestData = buildHTTPRequest(from: request)
        NSLog("[SocketURLProtocol] startLoading: %@ %@ (%d bytes request payload)",
              request.httpMethod ?? "GET",
              request.url?.path ?? "/",
              requestData.count)

        // Write request
        var totalWritten = 0
        let count = requestData.count
        let writeResult = requestData.withUnsafeBytes { rawBuffer -> Bool in
            guard let baseAddress = rawBuffer.baseAddress else { return false }
            while totalWritten < count {
                let written = Darwin.write(fd, baseAddress.advanced(by: totalWritten), count - totalWritten)
                if written <= 0 {
                    return false
                }
                totalWritten += written
            }
            return true
        }

        guard writeResult else {
            let writeErrno = errno
            Darwin.close(fd)
            let error = NSError(domain: NSPOSIXErrorDomain, code: Int(writeErrno),
                                userInfo: [NSLocalizedDescriptionKey: "Failed to write to socket"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        // Read response on a background thread to avoid blocking the caller.
        let thread = Thread { [weak self] in
            self?.readResponse(fd: fd)
        }
        thread.qualityOfService = .userInitiated
        thread.name = "SocketURLProtocol-read"
        readThread = thread
        thread.start()
    }

    override func stopLoading() {
        isCancelled = true
        closeSocket()
    }

    /// Thread-safe close of the socket file descriptor.
    /// Prevents double-close race between stopLoading (CFNetwork thread) and readResponse (background thread).
    private func closeSocket() {
        socketLock.lock()
        let fd = socketFD
        socketFD = -1
        socketLock.unlock()
        if fd >= 0 {
            Darwin.close(fd)
        }
    }

    // MARK: - HTTP Request Builder

    private func buildHTTPRequest(from request: URLRequest) -> Data {
        guard let url = request.url else { return Data() }
        let method = request.httpMethod ?? "GET"

        // Request line
        var path = url.path
        if path.isEmpty { path = "/" }
        if let query = url.query, !query.isEmpty {
            path += "?" + query
        }

        var lines = ["\(method) \(path) HTTP/1.1"]

        // Host header (required by HTTP/1.1)
        let host = url.host ?? "localhost"
        if let port = url.port {
            lines.append("Host: \(host):\(port)")
        } else {
            lines.append("Host: \(host)")
        }

        // Forward all headers from the original request
        var hasContentLength = false
        if let allHeaders = request.allHTTPHeaderFields {
            for (key, value) in allHeaders {
                let lowerKey = key.lowercased()
                if lowerKey == "host" { continue } // already added
                // Transport routing hints — never go on the wire.
                if lowerKey == Self.routeHeader.lowercased() { continue }
                if lowerKey == Self.strictSocketHeader.lowercased() { continue }
                if lowerKey == "content-length" { hasContentLength = true }
                lines.append("\(key): \(value)")
            }
        }

        // Body — check both httpBody and httpBodyStream.
        // URLSession may convert httpBody to httpBodyStream internally,
        // so the URLProtocol receives httpBody == nil for POST requests.
        var body = request.httpBody ?? Data()
        NSLog("[SocketURLProtocol] buildHTTPRequest: method=%@, httpBody=%d bytes, httpBodyStream=%@",
              method, body.count, request.httpBodyStream != nil ? "present" : "nil")
        if body.isEmpty, let stream = request.httpBodyStream {
            // Read the entire stream into Data.
            // httpBodyStream from URLSession is memory-backed, so hasBytesAvailable
            // is reliable. We also guard against read() returning -1 (error) or 0 (EOF).
            stream.open()
            var streamData = Data()
            let bufSize = 16384
            let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: bufSize)
            defer {
                buf.deallocate()
                stream.close()
            }
            while stream.hasBytesAvailable {
                let bytesRead = stream.read(buf, maxLength: bufSize)
                if bytesRead > 0 {
                    streamData.append(buf, count: bytesRead)
                } else if bytesRead == 0 {
                    // EOF
                    break
                } else {
                    // Error — log and stop
                    NSLog("[SocketURLProtocol] httpBodyStream read error: %@",
                          stream.streamError?.localizedDescription ?? "unknown")
                    break
                }
            }
            NSLog("[SocketURLProtocol] read %d bytes from httpBodyStream", streamData.count)
            body = streamData
        }

        // Always set Content-Length for bodies: either it was missing, or the original
        // header may reference the pre-stream size which could differ.
        if !body.isEmpty {
            if hasContentLength {
                // Replace any existing Content-Length with the actual body size.
                lines = lines.map { line in
                    if line.lowercased().hasPrefix("content-length:") {
                        return "Content-Length: \(body.count)"
                    }
                    return line
                }
            } else {
                lines.append("Content-Length: \(body.count)")
            }
        }

        // Connection close to simplify reading
        lines.append("Connection: close")

        // Blank line terminates headers
        lines.append("")
        lines.append("")

        var data = lines.joined(separator: "\r\n").data(using: .utf8) ?? Data()
        if !body.isEmpty {
            data.append(body)
        }
        return data
    }

    // MARK: - HTTP Response Reader

    private func readResponse(fd: Int32) {
        let bufferSize = 8192
        let buffer = UnsafeMutableRawPointer.allocate(byteCount: bufferSize, alignment: 1)
        defer {
            buffer.deallocate()
            closeSocket()
        }

        // Phase 1: Read until we find the header/body separator (\r\n\r\n)
        var headerData = Data()
        let separator = Data([0x0D, 0x0A, 0x0D, 0x0A]) // \r\n\r\n
        var separatorRange: Range<Data.Index>?

        while !isCancelled {
            let bytesRead = Darwin.read(fd, buffer, bufferSize)
            if bytesRead <= 0 { break }
            headerData.append(buffer.assumingMemoryBound(to: UInt8.self), count: bytesRead)
            if let range = headerData.range(of: separator) {
                separatorRange = range
                break
            }
        }

        guard !isCancelled, let sepRange = separatorRange else {
            if !isCancelled {
                let error = NSError(domain: "SocketURLProtocol", code: -1,
                                    userInfo: [NSLocalizedDescriptionKey: "Failed to read HTTP headers from socket"])
                client?.urlProtocol(self, didFailWithError: error)
            }
            return
        }

        // Parse headers
        let headersEnd = sepRange.lowerBound
        let bodyStart = sepRange.upperBound
        guard let headerString = String(data: headerData[headerData.startIndex..<headersEnd], encoding: .utf8) else {
            let error = NSError(domain: "SocketURLProtocol", code: -1,
                                userInfo: [NSLocalizedDescriptionKey: "Invalid HTTP header encoding"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        let headerLines = headerString.components(separatedBy: "\r\n")
        guard let statusLine = headerLines.first else {
            let error = NSError(domain: "SocketURLProtocol", code: -1,
                                userInfo: [NSLocalizedDescriptionKey: "Missing HTTP status line"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        let statusParts = statusLine.split(separator: " ", maxSplits: 2)
        guard statusParts.count >= 2, let statusCode = Int(statusParts[1]) else {
            let error = NSError(domain: "SocketURLProtocol", code: -1,
                                userInfo: [NSLocalizedDescriptionKey: "Invalid HTTP status line: \(statusLine)"])
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        var headers: [String: String] = [:]
        for i in 1..<headerLines.count {
            let line = headerLines[i]
            guard let colonIdx = line.firstIndex(of: ":") else { continue }
            let key = String(line[line.startIndex..<colonIdx]).trimmingCharacters(in: .whitespaces)
            let value = String(line[line.index(after: colonIdx)...]).trimmingCharacters(in: .whitespaces)
            headers[key] = value
        }

        // Phase 2: Read body based on Content-Length or Transfer-Encoding
        // We already have some body bytes in headerData after the separator
        var bodyData = Data(headerData[bodyStart..<headerData.endIndex])

        let isChunked = headers["Transfer-Encoding"]?.lowercased().contains("chunked") == true

        if let contentLengthStr = headers["Content-Length"], let contentLength = Int(contentLengthStr) {
            // Read exactly Content-Length bytes
            while bodyData.count < contentLength && !isCancelled {
                let remaining = contentLength - bodyData.count
                let toRead = min(remaining, bufferSize)
                let bytesRead = Darwin.read(fd, buffer, toRead)
                if bytesRead <= 0 { break }
                bodyData.append(buffer.assumingMemoryBound(to: UInt8.self), count: bytesRead)
            }
        } else if isChunked {
            // Read chunked until we see the terminal "0\r\n\r\n"
            let terminator = Data([0x30, 0x0D, 0x0A, 0x0D, 0x0A]) // "0\r\n\r\n"
            while !isCancelled && bodyData.range(of: terminator) == nil {
                let bytesRead = Darwin.read(fd, buffer, bufferSize)
                if bytesRead <= 0 { break }
                bodyData.append(buffer.assumingMemoryBound(to: UInt8.self), count: bytesRead)
            }
            bodyData = decodeChunkedBody(bodyData)
        }
        // else: no Content-Length and not chunked — bodyData is whatever we already have

        guard !isCancelled else { return }

        // Deliver to URLProtocol client
        if let httpResponse = HTTPURLResponse(
            url: request.url ?? URL(string: "http://localhost")!,
            statusCode: statusCode,
            httpVersion: "HTTP/1.1",
            headerFields: headers
        ) {
            client?.urlProtocol(self, didReceive: httpResponse, cacheStoragePolicy: .notAllowed)
        }

        if !bodyData.isEmpty {
            client?.urlProtocol(self, didLoad: bodyData)
        }

        client?.urlProtocolDidFinishLoading(self)
    }

    // MARK: - HTTP Response Parser

    private struct ParsedHTTPResponse {
        let statusCode: Int
        let headers: [String: String]
        let body: Data
    }

    private func parseHTTPResponse(_ data: Data) -> ParsedHTTPResponse? {
        // Find the header/body separator: \r\n\r\n
        let crlf2 = Data([0x0D, 0x0A, 0x0D, 0x0A])
        guard let separatorRange = data.range(of: crlf2) else {
            // Try with just \n\n as a fallback
            let lf2 = Data([0x0A, 0x0A])
            guard let altRange = data.range(of: lf2) else {
                return nil
            }
            return parseWithSeparator(data: data, headerEnd: altRange.lowerBound, bodyStart: altRange.upperBound, lineEnding: "\n")
        }
        return parseWithSeparator(data: data, headerEnd: separatorRange.lowerBound, bodyStart: separatorRange.upperBound, lineEnding: "\r\n")
    }

    private func parseWithSeparator(data: Data, headerEnd: Data.Index, bodyStart: Data.Index, lineEnding: String) -> ParsedHTTPResponse? {
        guard let headerString = String(data: data[data.startIndex..<headerEnd], encoding: .utf8) else {
            return nil
        }

        let headerLines = headerString.components(separatedBy: lineEnding)
        guard let statusLine = headerLines.first else { return nil }

        // Parse status line: "HTTP/1.1 200 OK"
        let statusParts = statusLine.split(separator: " ", maxSplits: 2)
        guard statusParts.count >= 2, let statusCode = Int(statusParts[1]) else {
            return nil
        }

        // Parse headers
        var headers: [String: String] = [:]
        for i in 1..<headerLines.count {
            let line = headerLines[i]
            guard let colonIndex = line.firstIndex(of: ":") else { continue }
            let key = String(line[line.startIndex..<colonIndex]).trimmingCharacters(in: .whitespaces)
            let value = String(line[line.index(after: colonIndex)...]).trimmingCharacters(in: .whitespaces)
            headers[key] = value
        }

        // Body
        let body: Data
        if bodyStart < data.endIndex {
            body = data[bodyStart..<data.endIndex]
        } else {
            body = Data()
        }

        // Handle chunked transfer encoding
        if let te = headers["Transfer-Encoding"]?.lowercased(), te.contains("chunked") {
            let decoded = decodeChunkedBody(body)
            return ParsedHTTPResponse(statusCode: statusCode, headers: headers, body: decoded)
        }

        return ParsedHTTPResponse(statusCode: statusCode, headers: headers, body: body)
    }

    /// Minimal chunked transfer encoding decoder.
    private func decodeChunkedBody(_ data: Data) -> Data {
        var result = Data()
        var offset = data.startIndex

        while offset < data.endIndex {
            // Find end of chunk size line
            guard let lineEnd = findCRLF(in: data, from: offset) else { break }

            // Parse chunk size (hex)
            guard let sizeString = String(data: data[offset..<lineEnd], encoding: .ascii),
                  let chunkSize = UInt(sizeString.trimmingCharacters(in: .whitespaces), radix: 16) else {
                break
            }

            if chunkSize == 0 { break } // Terminal chunk

            let chunkStart = lineEnd + 2 // skip \r\n after size
            let chunkEnd = data.index(chunkStart, offsetBy: Int(chunkSize), limitedBy: data.endIndex) ?? data.endIndex
            result.append(data[chunkStart..<chunkEnd])

            // Skip past chunk data + trailing \r\n
            offset = min(chunkEnd + 2, data.endIndex)
        }

        return result
    }

    private func findCRLF(in data: Data, from start: Data.Index) -> Data.Index? {
        var i = start
        while i < data.endIndex {
            let next = data.index(after: i)
            if next < data.endIndex && data[i] == 0x0D && data[next] == 0x0A {
                return i
            }
            i = next
        }
        return nil
    }
}

// MARK: - Socket Transport Helper

/// Factory for creating URLSessions that communicate via Unix domain socket.
enum SocketTransport {

    /// Create a `URLSession` configured to route traffic through the mcpproxy Unix socket.
    /// Falls back to standard networking if the socket is not available.
    ///
    /// - Parameters:
    ///   - socketPath: socket this session must use. Carried per-request in a
    ///     header rather than a process-global, so two clients can talk to two
    ///     different cores without redirecting each other.
    ///   - timeout: per-request timeout. The default is generous because the
    ///     core can be slow under load; liveness probes pass something short.
    static func makeURLSession(socketPath: String? = nil, timeout: TimeInterval = 30) -> URLSession {
        let config = URLSessionConfiguration.default
        config.protocolClasses = [SocketURLProtocol.self]
        if let socketPath {
            config.httpAdditionalHeaders = [
                SocketURLProtocol.routeHeader: SocketURLProtocol.makeRoute(to: socketPath)
            ]
        }
        config.timeoutIntervalForRequest = timeout
        config.timeoutIntervalForResource = 300
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never

        return URLSession(configuration: config)
    }

    /// Create a standard TCP-based `URLSession` (never routed over the socket).
    static func makeTCPSession(timeout: TimeInterval = 30) -> URLSession {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = timeout
        config.timeoutIntervalForResource = 300
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never
        return URLSession(configuration: config)
    }

    /// Why a socket probe failed. "Not connectable" is NOT the same as "the core
    /// is dead", and the difference decides whether the tray may act on it.
    enum SocketProbe: Equatable {
        /// A listener accepted (or is accepting) the connection.
        case connectable
        /// No socket file at all. A running core always owns its socket file,
        /// so this one is unambiguous.
        case absent
        /// The file is there but the connection was refused. Ambiguous: a dead
        /// process leaves a stale file behind, AND a live core with a full
        /// listen backlog refuses connections in exactly the same way.
        case refused
        /// The failure was on OUR side — descriptor exhaustion, out of memory,
        /// permissions. Says nothing whatsoever about the core.
        case localFailure(Int32)
    }

    /// Check whether the mcpproxy Unix socket file exists and is connectable.
    static func isSocketAvailable(path: String? = nil) -> Bool {
        probeSocket(path: path) == .connectable
    }

    /// Probe the socket and classify the outcome.
    static func probeSocket(path: String? = nil) -> SocketProbe {
        let socketPath = path ?? SocketURLProtocol.socketPath

        guard FileManager.default.fileExists(atPath: socketPath) else {
            return .absent
        }

        // Attempt a quick connect to verify the socket is alive
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return .localFailure(errno) }
        defer { Darwin.close(fd) }

        // Set non-blocking for a quick probe
        let flags = fcntl(fd, F_GETFL)
        _ = fcntl(fd, F_SETFL, flags | O_NONBLOCK)

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = socketPath.utf8CString
        withUnsafeMutablePointer(to: &addr.sun_path) { sunPathPtr in
            sunPathPtr.withMemoryRebound(to: CChar.self, capacity: pathBytes.count) { dest in
                for i in 0..<pathBytes.count {
                    dest[i] = pathBytes[i]
                }
            }
        }

        let result = withUnsafePointer(to: &addr) { addrPtr in
            addrPtr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPtr in
                Darwin.connect(fd, sockaddrPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }

        // Non-blocking connect returns 0 on immediate success or EINPROGRESS
        if result == 0 { return .connectable }
        let connectErrno = errno
        if connectErrno == EINPROGRESS { return .connectable }

        switch connectErrno {
        case ENOENT:
            // Unlinked between the stat above and the connect.
            return .absent
        case EMFILE, ENFILE, ENOMEM, ENOBUFS, EACCES, EPERM:
            // Our process ran out of something, or cannot reach the socket.
            // Not evidence about the core.
            return .localFailure(connectErrno)
        default:
            // ECONNREFUSED and friends: a dead process's stale socket looks
            // exactly like a live core whose listen queue is full.
            return .refused
        }
    }
}
