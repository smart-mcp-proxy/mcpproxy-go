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

    /// Allow override for testing.
    static var overrideSocketPath: String?

    private static var effectiveSocketPath: String {
        overrideSocketPath ?? socketPath
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
        // Only intercept if the socket exists.
        return FileManager.default.fileExists(atPath: effectiveSocketPath)
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
        let path = Self.effectiveSocketPath
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
    static func makeURLSession(socketPath: String? = nil) -> URLSession {
        if let path = socketPath {
            SocketURLProtocol.overrideSocketPath = path
        }

        let config = URLSessionConfiguration.default
        config.protocolClasses = [SocketURLProtocol.self]
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 300
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never

        return URLSession(configuration: config)
    }

    /// Create a standard TCP-based `URLSession` (no socket override).
    static func makeTCPSession() -> URLSession {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 300
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never
        return URLSession(configuration: config)
    }

    /// Check whether the mcpproxy Unix socket file exists and is connectable.
    static func isSocketAvailable(path: String? = nil) -> Bool {
        let socketPath = path ?? SocketURLProtocol.socketPath

        guard FileManager.default.fileExists(atPath: socketPath) else {
            return false
        }

        // Attempt a quick connect to verify the socket is alive
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return false }
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
        return result == 0 || errno == EINPROGRESS
    }
}
