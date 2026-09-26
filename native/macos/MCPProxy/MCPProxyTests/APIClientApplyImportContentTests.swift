// APIClientApplyImportContentTests.swift
// MCPProxyTests
//
// Review round 4 (F-A/F-D): the Paste tab's Add step must re-parse the
// ORIGINAL raw pasted content server-side (`POST /api/v1/servers/import/json`
// with preview=false), never reconstruct a config from the preview's own
// (potentially redacted) url/command/args. This asserts what actually goes
// OUT over the wire: the raw content, the server_names scope, and the
// resolved env/header overrides — not just that the call "succeeds".

import XCTest
@testable import MCPProxy

final class APIClientApplyImportContentTests: XCTestCase {

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    func testPostsRawContentAndScopesToTheOneServerName() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"format":"url","imported":[{"name":"api","protocol":"http"}]}
        """)
        let client = ConnectStubURLProtocol.makeClient()
        let rawURL = "https://api.example.com/mcp?api_key=ghp_verysecrettoken1234"

        _ = try await client.applyImportContent(rawURL, serverName: "api")

        let req = try XCTUnwrap(ConnectStubURLProtocol.recorded.last)
        XCTAssertTrue(req.url.hasSuffix("/api/v1/servers/import/json"))
        XCTAssertFalse(req.url.contains("preview=true"))
        XCTAssertEqual(req.json["content"] as? String, rawURL)
        XCTAssertEqual(req.json["server_names"] as? [String], ["api"])
        // F-E: the Paste tab is the one surface allowed to guess a bare
        // URL/command line — this must be explicit on every apply call.
        XCTAssertEqual(req.json["allow_paste_fallback"] as? Bool, true)
        XCTAssertNil(req.json["env_override"])
        XCTAssertNil(req.json["header_override"])
    }

    // Deliberately no `format` param on applyImportContent — see its doc
    // comment: a preview's own format string can be e.g. "claude_desktop",
    // which the backend's format-hint parser does not accept, so passing it
    // back as a hint would 400 the apply. Re-detection (allow_paste_fallback)
    // avoids the mismatch since detection is a pure function of content.
    func testNeverSendsAFormatHint() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"format":"url","imported":[{"name":"api","protocol":"http"}]}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.applyImportContent("https://api.example.com/mcp", serverName: "api")

        let req = try XCTUnwrap(ConnectStubURLProtocol.recorded.last)
        XCTAssertNil(req.json["format"])
    }

    func testCarriesEnvAndHeaderOverridesWhenPresent() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"format":"command","imported":[{"name":"github","protocol":"stdio"}]}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.applyImportContent(
            "uvx mcp-server-github",
            serverName: "github",
            envOverride: ["GITHUB_TOKEN": "${keyring:github-env-github-token}"],
            headerOverride: [:]
        )

        let req = try XCTUnwrap(ConnectStubURLProtocol.recorded.last)
        XCTAssertEqual(req.json["env_override"] as? [String: String], ["GITHUB_TOKEN": "${keyring:github-env-github-token}"])
        XCTAssertNil(req.json["header_override"])
    }

    func testPreviewAlsoSendsAllowPasteFallback() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"format":"url","imported":[{"name":"api","protocol":"http"}]}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        _ = try await client.previewImportContent("https://api.example.com/mcp")

        let req = try XCTUnwrap(ConnectStubURLProtocol.recorded.last)
        XCTAssertTrue(req.url.contains("preview=true"))
        XCTAssertEqual(req.json["allow_paste_fallback"] as? Bool, true)
    }

    func testEmptyImportedListSurfacesAsFailureToTheCaller() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"format":"url","imported":[]}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        let result = try await client.applyImportContent("https://api.example.com/mcp", serverName: "api")

        XCTAssertTrue(result.imported.isEmpty)
    }
}
