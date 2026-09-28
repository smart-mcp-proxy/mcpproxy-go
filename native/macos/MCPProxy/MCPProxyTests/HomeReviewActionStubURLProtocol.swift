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

    static func reset() {
        requests = []
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
}
