// APIClientUpdateFailureTests.swift
// MCPProxyTests
//
// Spec 095 FR-009 / FR-010 / FR-016 — the one request this feature puts on the
// wire, asserted byte for byte.
//
// Two properties matter more than the happy path: the body carries a stage and
// NOTHING else (it is the only update-failure information that ever leaves the
// machine), and every failure mode — 404 from an older core, 500, a dead socket
// — is one local log line and no retry.

import XCTest
@testable import MCPProxy

final class APIClientUpdateFailureTests: XCTestCase {

    /// Collects the client's log lines. Locked because the recording runs off
    /// the caller's thread.
    private final class LogSpy: @unchecked Sendable {
        private let lock = NSLock()
        private var lines: [String] = []

        func record(_ line: String) {
            lock.lock()
            defer { lock.unlock() }
            lines.append(line)
        }

        var all: [String] {
            lock.lock()
            defer { lock.unlock() }
            return lines
        }
    }

    private func makeClient(_ spy: LogSpy) -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectStubURLProtocol.self]
        return APIClient(
            session: URLSession(configuration: config),
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil,
            log: { [spy] line in spy.record(line) }
        )
    }

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    // MARK: - The request

    func testTheRequestIsASingleStageOnlyPost() async throws {
        ConnectStubURLProtocol.statusCode = 204
        let spy = LogSpy()
        let client = makeClient(spy)

        await client.recordUpdateFailure(stage: .download)

        XCTAssertEqual(ConnectStubURLProtocol.recorded.count, 1)
        let request = try XCTUnwrap(ConnectStubURLProtocol.recorded.first)
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/telemetry/update-failure")
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.headers["Content-Type"], "application/json")
        let body = try XCTUnwrap(request.body)
        XCTAssertEqual(String(data: body, encoding: .utf8), #"{"stage":"download"}"#,
                       "FR-009: the body is exactly one field — no version, no URL, no message")
    }

    func testEveryStageIsSentAsItsWireValue() async throws {
        for stage in UpdateFailureStage.allCases {
            ConnectStubURLProtocol.reset()
            ConnectStubURLProtocol.statusCode = 204
            let client = makeClient(LogSpy())

            await client.recordUpdateFailure(stage: stage)

            let request = try XCTUnwrap(ConnectStubURLProtocol.recorded.first)
            XCTAssertEqual(request.json["stage"] as? String, stage.rawValue)
            XCTAssertEqual(request.json.keys.count, 1)
        }
    }

    func testASuccessfulRecordingSaysNothing() async {
        ConnectStubURLProtocol.statusCode = 204
        let spy = LogSpy()
        let client = makeClient(spy)

        await client.recordUpdateFailure(stage: .install)

        XCTAssertEqual(spy.all, [], "a working recording is not news")
    }

    // MARK: - Failure modes (FR-010 / FR-016)

    /// An older core has no such route. That is not an error condition worth
    /// anyone's attention — and the 404 body must not be parsed or logged.
    func testAnOlderCoreProducesExactlyOneLine() async throws {
        ConnectStubURLProtocol.statusCode = 404
        ConnectStubURLProtocol.responseBody = Data(#"{"success":false,"error":"route not found"}"#.utf8)
        let spy = LogSpy()
        let client = makeClient(spy)

        await client.recordUpdateFailure(stage: .appcast)

        XCTAssertEqual(spy.all.count, 1)
        let line = try XCTUnwrap(spy.all.first)
        XCTAssertTrue(line.contains("appcast"), line)
        XCTAssertTrue(line.contains("404"), line)
        XCTAssertFalse(line.contains("route not found"),
                       "FR-010: response bodies are never logged — got \(line)")
        XCTAssertFalse(line.contains("127.0.0.1"), "FR-010: no URLs — got \(line)")
        XCTAssertEqual(ConnectStubURLProtocol.recorded.count, 1, "one attempt, no retry")
    }

    func testAServerErrorProducesExactlyOneLine() async throws {
        ConnectStubURLProtocol.statusCode = 500
        let spy = LogSpy()
        let client = makeClient(spy)

        await client.recordUpdateFailure(stage: .other)

        XCTAssertEqual(spy.all.count, 1)
        XCTAssertTrue(try XCTUnwrap(spy.all.first).contains("500"))
    }

    /// A core that is down, or a socket that has gone away: the transport
    /// throws. One line, classified numerically, nothing user-visible.
    func testAnUnreachableCoreProducesExactlyOneLine() async throws {
        ConnectStubURLProtocol.transportFailure = URLError(.timedOut)
        let spy = LogSpy()
        let client = makeClient(spy)

        await client.recordUpdateFailure(stage: .download)

        XCTAssertEqual(spy.all.count, 1)
        let line = try XCTUnwrap(spy.all.first)
        XCTAssertTrue(line.contains("download"), line)
        XCTAssertTrue(line.contains("\(URLError.Code.timedOut.rawValue)"),
                      "the numeric classification is what FR-010 allows — got \(line)")
        XCTAssertFalse(line.lowercased().contains("timed out"),
                       "raw error descriptions are not ours to log — got \(line)")
    }
}
