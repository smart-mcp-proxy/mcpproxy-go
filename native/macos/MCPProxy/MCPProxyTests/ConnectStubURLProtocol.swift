// ConnectStubURLProtocol.swift
// MCPProxyTests
//
// A URLProtocol that records the full shape of every request — URL, method,
// headers and body — and replays a canned JSON body. The Connect form's API
// surface is mutating, so the tests must assert what goes OUT, not only what
// comes back.

import Foundation
@testable import MCPProxy

final class ConnectStubURLProtocol: URLProtocol {

    struct Recorded {
        let url: String
        let method: String
        let headers: [String: String]
        let body: Data?

        /// The request body decoded as a JSON object, for field-level asserts.
        var json: [String: Any] {
            guard let body,
                  let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any]
            else { return [:] }
            return object
        }
    }

    /// Requests seen by the stub, in order.
    static var recorded: [Recorded] = []

    /// Body replayed for every request.
    static var responseBody = Data()

    /// Status code replayed for every request.
    static var statusCode = 200

    /// When set, the request fails at the transport instead of answering — a
    /// core that is not running, or a socket that has gone away.
    static var transportFailure: Error?

    static func reset() {
        recorded = []
        responseBody = Data()
        statusCode = 200
        transportFailure = nil
    }

    /// An APIClient whose traffic is intercepted by this stub.
    ///
    /// `transportKind` is explicit because the mutating calls refuse to ride
    /// anything but the private socket, and a stubbed session has no transport
    /// identity of its own.
    static func makeClient(transportKind: APIClient.TransportKind = .unixSocket) -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectStubURLProtocol.self]
        return APIClient(
            session: URLSession(configuration: config),
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil,
            transportKind: transportKind
        )
    }

    /// Wrap a payload in the standard `{"success":true,"data":…}` envelope.
    static func envelope(_ json: String) -> Data {
        Data("{\"success\":true,\"data\":\(json)}".utf8)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        ConnectStubURLProtocol.recorded.append(
            Recorded(
                url: request.url?.absoluteString ?? "",
                method: request.httpMethod ?? "",
                headers: request.allHTTPHeaderFields ?? [:],
                body: Self.body(of: request)
            )
        )
        if let failure = ConnectStubURLProtocol.transportFailure {
            client?.urlProtocol(self, didFailWithError: failure)
            return
        }
        let response = HTTPURLResponse(
            url: request.url ?? URL(string: "http://127.0.0.1:8080")!,
            statusCode: ConnectStubURLProtocol.statusCode,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: ConnectStubURLProtocol.responseBody)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    /// URLSession converts `httpBody` into `httpBodyStream` before a custom
    /// protocol sees the request, so reading only `httpBody` records nil for
    /// every POST — exactly the requests these tests exist to inspect.
    private static func body(of request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        let size = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: size)
        defer { buffer.deallocate() }
        while stream.hasBytesAvailable {
            let read = stream.read(buffer, maxLength: size)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return data.isEmpty ? nil : data
    }
}
