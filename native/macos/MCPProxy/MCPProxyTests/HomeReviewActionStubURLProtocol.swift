// HomeReviewActionStubURLProtocol.swift
// MCPProxyTests
//
// A URLProtocol that records every request's method and URL, so
// HomeReviewActionTests can assert an exact negative — no request reached
// the core at all — rather than merely that the app didn't crash.

import Foundation
@testable import MCPProxy

final class HomeReviewActionStubURLProtocol: URLProtocol {

    /// (method, absolute URL) for every request seen by the stub, in order.
    static var requests: [(method: String, url: String)] = []
    /// Request bodies are retained separately so approval-path tests can pin
    /// the force flag without changing the existing navigation assertions.
    static var requestBodies: [Data?] = []

    static func reset() {
        requests = []
        requestBodies = []
    }

    /// An APIClient whose traffic is intercepted by this stub.
    static func makeClient() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HomeReviewActionStubURLProtocol.self]
        return APIClient(
            session: URLSession(configuration: config),
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil
        )
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        if let url = request.url {
            HomeReviewActionStubURLProtocol.requests.append((request.httpMethod ?? "GET", url.absoluteString))
        }
        HomeReviewActionStubURLProtocol.requestBodies.append(requestBody(request))
        let response = HTTPURLResponse(
            url: request.url ?? URL(string: "http://127.0.0.1:8080")!,
            statusCode: 200,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data("{\"success\":true,\"data\":{}}".utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    private func requestBody(_ request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var result = Data()
        var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            result.append(buffer, count: count)
        }
        return result.isEmpty ? nil : result
    }
}
