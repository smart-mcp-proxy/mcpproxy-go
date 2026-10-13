// GlanceStubURLProtocol.swift
// MCPProxyTests
//
// A URLProtocol that records every request URL and replays a canned JSON body,
// so APIClient's request building and decoding can be tested without a core.

import Foundation
@testable import MCPProxy

final class GlanceStubURLProtocol: URLProtocol {

    /// Absolute URL strings seen by the stub, in request order.
    static var requestedURLs: [String] = []

    /// Body replayed for every request.
    static var responseBody = Data()

    /// Status code replayed for every request.
    static var statusCode = 200

    static func reset() {
        requestedURLs = []
        responseBody = Data()
        statusCode = 200
    }

    /// An APIClient whose traffic is intercepted by this stub.
    static func makeClient() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [GlanceStubURLProtocol.self]
        return APIClient(
            session: URLSession(configuration: config),
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil
        )
    }

    /// Wrap a payload in the standard `{"success":true,"data":…}` envelope.
    static func envelope(_ json: String) -> Data {
        Data("{\"success\":true,\"data\":\(json)}".utf8)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        if let url = request.url {
            GlanceStubURLProtocol.requestedURLs.append(url.absoluteString)
        }
        let response = HTTPURLResponse(
            url: request.url ?? URL(string: "http://127.0.0.1:8080")!,
            statusCode: GlanceStubURLProtocol.statusCode,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: GlanceStubURLProtocol.responseBody)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
