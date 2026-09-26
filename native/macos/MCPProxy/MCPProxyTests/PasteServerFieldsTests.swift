import XCTest
@testable import MCPProxy

/// Pure-function seam for the Paste tab (Spec 109 FR-064/065): turning an
/// import preview into the toggle list (`buildFields`). Add itself now goes
/// through `APIClient.applyImportContent`, which re-parses the raw pasted
/// text server-side (review round 4 F-A/F-D fix) rather than reconstructing
/// a `POST /api/v1/servers` body from the preview client-side, so there is
/// no more pure `makeServerConfig` seam to test here — see
/// `PasteServerApplyTests` for the apply-call assembly.
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
}
