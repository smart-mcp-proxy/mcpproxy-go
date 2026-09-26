import XCTest
@testable import MCPProxy

/// Pure-function seam for the Paste tab (Spec 109 FR-064/065): turning an
/// import preview into the toggle list (`buildFields`) and turning the
/// resolved fields into the `POST /api/v1/servers` body (`makeServerConfig`).
/// Unit-testable without a running app or API client, mirroring
/// `ManualServerForm`'s `makeServerConfig` seam.
final class PasteServerFieldsTests: XCTestCase {

    // MARK: - buildFields

    func testBuildFieldsDefaultsSecretLikeEnvVarsToSecretMode() {
        let server = ImportPreviewServer(
            name: "fs", protocol: "stdio", url: nil, command: "npx", args: ["-y"],
            summary: nil, tags: nil,
            env: [ImportPreviewField(name: "GITHUB_TOKEN", secretLike: true, emptyOrPlaceholder: false),
                  ImportPreviewField(name: "PORT", secretLike: false, emptyOrPlaceholder: false)],
            headers: nil
        )
        let fields = PasteServerView.buildFields(from: server)
        XCTAssertEqual(fields.count, 2)
        XCTAssertEqual(fields.first { $0.name == "GITHUB_TOKEN" }?.mode, .secret)
        XCTAssertEqual(fields.first { $0.name == "PORT" }?.mode, .value)
        XCTAssertTrue(fields.allSatisfy { $0.kind == .env })
    }

    func testBuildFieldsMapsHeadersToHeaderKind() {
        let server = ImportPreviewServer(
            name: "remote", protocol: "http", url: "https://api.example.com/mcp", command: nil, args: nil,
            summary: nil, tags: nil, env: nil,
            headers: [ImportPreviewField(name: "Authorization", secretLike: true, emptyOrPlaceholder: false)]
        )
        let fields = PasteServerView.buildFields(from: server)
        XCTAssertEqual(fields.count, 1)
        XCTAssertEqual(fields.first?.kind, .header)
        XCTAssertEqual(fields.first?.mode, .secret)
    }

    func testBuildFieldsHandlesBothEnvAndHeaders() {
        let server = ImportPreviewServer(
            name: "hybrid", protocol: "http", url: "https://x/mcp", command: nil, args: nil,
            summary: nil, tags: nil,
            env: [ImportPreviewField(name: "API_KEY", secretLike: true, emptyOrPlaceholder: false)],
            headers: [ImportPreviewField(name: "API_KEY", secretLike: true, emptyOrPlaceholder: false)]
        )
        let fields = PasteServerView.buildFields(from: server)
        // Same NAME, different kind — must not collapse to one row.
        XCTAssertEqual(fields.count, 2)
        XCTAssertEqual(Set(fields.map(\.id)).count, 2)
    }

    // MARK: - makeServerConfig

    func testMakeServerConfigForStdioIncludesCommandArgsAndEnv() {
        let preview = ImportPreviewServer(
            name: "fs", protocol: "stdio", url: nil, command: "npx", args: ["-y", "server-filesystem"],
            summary: nil, tags: nil, env: nil, headers: nil
        )
        let resolved = ResolvedSecretFields(env: ["GITHUB_TOKEN": "${keyring:fs-env-github-token}"], headers: [:], writtenRefs: ["fs-env-github-token"])
        let config = PasteServerView.makeServerConfig(preview: preview, resolved: resolved)
        XCTAssertEqual(config["name"] as? String, "fs")
        XCTAssertEqual(config["protocol"] as? String, "stdio")
        XCTAssertEqual(config["command"] as? String, "npx")
        XCTAssertEqual(config["args"] as? [String], ["-y", "server-filesystem"])
        XCTAssertEqual((config["env"] as? [String: String])?["GITHUB_TOKEN"], "${keyring:fs-env-github-token}")
        XCTAssertNil(config["url"])
        XCTAssertNil(config["headers"])
    }

    func testMakeServerConfigForURLIncludesHeadersNotEnv() {
        let preview = ImportPreviewServer(
            name: "remote", protocol: "http", url: "https://api.example.com/mcp", command: nil, args: nil,
            summary: nil, tags: nil, env: nil, headers: nil
        )
        let resolved = ResolvedSecretFields(env: [:], headers: ["Authorization": "${keyring:remote-header-authorization}"], writtenRefs: ["remote-header-authorization"])
        let config = PasteServerView.makeServerConfig(preview: preview, resolved: resolved)
        XCTAssertEqual(config["url"] as? String, "https://api.example.com/mcp")
        XCTAssertEqual((config["headers"] as? [String: String])?["Authorization"], "${keyring:remote-header-authorization}")
        XCTAssertNil(config["command"])
        XCTAssertNil(config["env"])
    }

    func testMakeServerConfigOmitsEmptyEnvAndHeaders() {
        let preview = ImportPreviewServer(
            name: "plain", protocol: "stdio", url: nil, command: "true", args: nil,
            summary: nil, tags: nil, env: nil, headers: nil
        )
        let resolved = ResolvedSecretFields(env: [:], headers: [:], writtenRefs: [])
        let config = PasteServerView.makeServerConfig(preview: preview, resolved: resolved)
        XCTAssertNil(config["env"])
        XCTAssertNil(config["headers"])
    }
}
