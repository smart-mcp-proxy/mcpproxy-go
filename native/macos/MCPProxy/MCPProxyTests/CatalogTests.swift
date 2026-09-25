import XCTest
@testable import MCPProxy

/// Spec 109 (FR-060/061/065) — macOS mirror of the Web UI catalog surface.
/// Covers the pure, testable units the Add Server sheet's Catalog tab and
/// secret toggle rely on: `GET /api/v1/catalog/search` JSON decode, the
/// FR-065 keyring ref-name algorithm (pinned against the same shared fixture
/// Go and vitest decode), and the D13 secret-like-name heuristic.
final class CatalogTests: XCTestCase {

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(T.self, from: data)
    }

    // MARK: - CatalogResult decode (contracts/rest-api.md#catalog example)

    func testDecodesTheContractExampleShape() throws {
        let json = """
        {"source": "official", "id": "io.github.github/github-mcp-server", "title": "GitHub",
         "publisher": "github", "verified": true, "official": true, "popularity": {"stars": 21000},
         "description": "…", "transport": "http", "install": {"url": "https://api.githubcopilot.com/mcp/"},
         "required_inputs": [{"name": "GITHUB_TOKEN", "secret_like": true}], "added": false}
        """
        let result = try decode(CatalogResult.self, from: json)
        XCTAssertEqual(result.source, "official")
        XCTAssertEqual(result.id, "io.github.github/github-mcp-server")
        XCTAssertEqual(result.title, "GitHub")
        XCTAssertTrue(result.official)
        XCTAssertTrue(result.verified)
        XCTAssertEqual(result.popularity?.stars, 21000)
        XCTAssertEqual(result.transport, "http")
        XCTAssertEqual(result.install.url, "https://api.githubcopilot.com/mcp/")
        XCTAssertNil(result.install.command)
        XCTAssertEqual(result.requiredInputs?.first?.name, "GITHUB_TOKEN")
        XCTAssertEqual(result.requiredInputs?.first?.secretLike, true)
        XCTAssertFalse(result.added)
    }

    func testDecodesAStdioInstallTarget() throws {
        let json = """
        {"source": "official", "id": "fs", "title": "Filesystem", "verified": false, "official": true,
         "description": "d", "transport": "stdio",
         "install": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]},
         "added": true}
        """
        let result = try decode(CatalogResult.self, from: json)
        XCTAssertEqual(result.install.command, "npx")
        XCTAssertEqual(result.install.args, ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"])
        XCTAssertNil(result.install.url)
        XCTAssertTrue(result.added)
    }

    func testDecodesSearchResponseWithNullSections() throws {
        let json = """
        {"query": "github", "results": [], "sections": null,
         "unavailable": [{"source": "smithery", "reason": "timeout after 5s"}]}
        """
        let resp = try decode(CatalogSearchResponse.self, from: json)
        XCTAssertEqual(resp.query, "github")
        XCTAssertNil(resp.sections)
        XCTAssertEqual(resp.unavailable?.first?.source, "smithery")
    }

    func testDecodesEmptyQuerySections() throws {
        let json = """
        {"query": "", "results": [], "sections": {"official": [], "popular": []}, "unavailable": []}
        """
        let resp = try decode(CatalogSearchResponse.self, from: json)
        XCTAssertNotNil(resp.sections)
        XCTAssertEqual(resp.sections?.official.count, 0)
    }

    // MARK: - SecretRefName (FR-065), pinned against the shared fixture

    func testRefNameBasic() {
        XCTAssertEqual(SecretRefName.compute(server: "github", kind: .env, key: "API_KEY"), "github-env-api-key")
        XCTAssertEqual(SecretRefName.compute(server: "github", kind: .header, key: "API_KEY"), "github-header-api-key")
    }

    func testRefNameEnvHeaderSameNameDistinctRefs() {
        let env = SecretRefName.compute(server: "github", kind: .env, key: "API_KEY")
        let header = SecretRefName.compute(server: "github", kind: .header, key: "API_KEY")
        XCTAssertNotEqual(env, header)
    }

    func testRefNameTakenNameGetsSuffix() {
        let taken: Set<String> = ["github-env-api-key", "github-env-api-key-2"]
        let got = SecretRefName.compute(server: "github", kind: .env, key: "API_KEY", taken: { taken.contains($0) })
        XCTAssertEqual(got, "github-env-api-key-3")
    }

    func testRefNameLongNameTruncated() {
        let longKey = "THIS_IS_A_VERY_VERY_VERY_VERY_VERY_VERY_VERY_LONG_ENV_VAR_NAME"
        let got = SecretRefName.compute(server: "some-really-long-server-name", kind: .env, key: longKey)
        XCTAssertLessThanOrEqual(got.count, 64)
    }

    /// Decodes the same fixture Go (`internal/secret/refname_test.go`) and
    /// vitest (`frontend/tests/unit/secret-ref.spec.ts`) decode, so all three
    /// ports agree on every case (FR-065).
    func testRefNameMatchesSharedFixture() throws {
        struct FixtureCase: Decodable {
            let server: String
            let kind: String
            let key: String
            let taken: [String]
            let want: String
        }
        struct Fixture: Decodable {
            let cases: [FixtureCase]
        }
        // Kept in sync with internal/secret/testdata/ref_names.json /
        // frontend/tests/unit/fixtures/ref_names.json by hand (no shared
        // fixture-loading mechanism between the Go module and this SPM
        // package's test target).
        let fixtureJSON = """
        {
          "cases": [
            {"server": "github", "kind": "env", "key": "API_KEY", "taken": [], "want": "github-env-api-key"},
            {"server": "github", "kind": "header", "key": "API_KEY", "taken": [], "want": "github-header-api-key"},
            {"server": "github", "kind": "env", "key": "GITHUB_TOKEN", "taken": [], "want": "github-env-github-token"},
            {"server": "My Server!", "kind": "env", "key": "GITHUB_TOKEN", "taken": [], "want": "my-server-env-github-token"},
            {"server": "github", "kind": "env", "key": "API_KEY", "taken": ["github-env-api-key"], "want": "github-env-api-key-2"},
            {"server": "github", "kind": "env", "key": "API_KEY", "taken": ["github-env-api-key", "github-env-api-key-2"], "want": "github-env-api-key-3"}
          ]
        }
        """
        let fixture = try decode(Fixture.self, from: fixtureJSON)
        XCTAssertFalse(fixture.cases.isEmpty)
        for c in fixture.cases {
            let takenSet = Set(c.taken)
            guard let kind = SecretRefName.Kind(rawValue: c.kind) else {
                XCTFail("unknown kind \(c.kind)")
                continue
            }
            let got = SecretRefName.compute(server: c.server, kind: kind, key: c.key, taken: { takenSet.contains($0) })
            XCTAssertEqual(got, c.want, "server=\(c.server) kind=\(c.kind) key=\(c.key)")
        }
    }

    // MARK: - SecretLikeName (D13)

    func testLooksSecretFlagsConventionalNames() {
        for name in ["GITHUB_TOKEN", "API_KEY", "PASSWORD", "Authorization", "PRIVATE_KEY"] {
            XCTAssertTrue(SecretLikeName.looksSecret(name), name)
        }
    }

    func testLooksSecretDoesNotFlagOrdinaryNames() {
        for name in ["PORT", "WORKDIR", "PATH", "REGION"] {
            XCTAssertFalse(SecretLikeName.looksSecret(name), name)
        }
    }
}
