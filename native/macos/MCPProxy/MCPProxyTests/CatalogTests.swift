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

    // MARK: - Secret refs decode (FR-065 taken-name check)

    func testDecodesSecretRefsResponse() throws {
        let json = """
        {"refs": [{"type": "keyring", "name": "github-env-api-key", "original": "${keyring:github-env-api-key}"}], "count": 1}
        """
        let resp = try decode(SecretRefsResponse.self, from: json)
        XCTAssertEqual(resp.refs.first?.name, "github-env-api-key")
        XCTAssertEqual(resp.refs.first?.type, "keyring")
    }

    // MARK: - Import preview decode (FR-064, Paste tab)

    func testDecodesImportPreviewWithEnvFields() throws {
        let json = """
        {"format": "url", "imported": [{"name": "fetch", "protocol": "http", "url": "https://api.example.com/mcp",
         "summary": "https://api.example.com/mcp", "tags": ["remote"],
         "headers": [{"name": "Authorization", "secret_like": true, "empty_or_placeholder": false}]}]}
        """
        let resp = try decode(ImportPreviewResponse.self, from: json)
        XCTAssertEqual(resp.format, "url")
        XCTAssertEqual(resp.imported.first?.name, "fetch")
        XCTAssertEqual(resp.imported.first?.url, "https://api.example.com/mcp")
        XCTAssertEqual(resp.imported.first?.headers?.first?.name, "Authorization")
        XCTAssertEqual(resp.imported.first?.headers?.first?.secretLike, true)
        XCTAssertNil(resp.imported.first?.command)
    }

    func testDecodesImportPreviewStdioWithEnvFields() throws {
        let json = """
        {"format": "command", "imported": [{"name": "server-filesystem", "protocol": "stdio",
         "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem"],
         "env": [{"name": "GITHUB_TOKEN", "secret_like": true, "empty_or_placeholder": true}]}]}
        """
        let resp = try decode(ImportPreviewResponse.self, from: json)
        XCTAssertEqual(resp.imported.first?.command, "npx")
        XCTAssertEqual(resp.imported.first?.args, ["-y", "@modelcontextprotocol/server-filesystem"])
        XCTAssertEqual(resp.imported.first?.env?.first?.name, "GITHUB_TOKEN")
        XCTAssertTrue(resp.imported.first?.env?.first?.emptyOrPlaceholder ?? false)
    }

    // MARK: - SecretFieldResolver.buildValues (pure assembly, no network)

    func testBuildValuesPassesThroughValueModeFields() {
        let fields = [SecretFieldInput(name: "PORT", value: "8080", mode: .value)]
        let (env, headers) = SecretFieldResolver.buildValues(fields: fields, written: [:])
        XCTAssertEqual(env["PORT"], "8080")
        XCTAssertTrue(headers.isEmpty)
    }

    func testBuildValuesSubstitutesKeyringPlaceholderForWrittenSecretFields() {
        let fields = [SecretFieldInput(name: "API_KEY", value: "sk-live-secret", mode: .secret)]
        let field = fields[0]
        let (env, _) = SecretFieldResolver.buildValues(fields: fields, written: [field.id: "github-env-api-key"])
        XCTAssertEqual(env["API_KEY"], "${keyring:github-env-api-key}")
        // The raw value never appears in the assembled env map.
        XCTAssertFalse(env.values.contains("sk-live-secret"))
    }

    func testBuildValuesMixesValueAndSecretFields() {
        let fields = [
            SecretFieldInput(name: "REGION", value: "us-east-1", mode: .value),
            SecretFieldInput(name: "API_KEY", value: "sk-live-secret", mode: .secret),
        ]
        let (env, _) = SecretFieldResolver.buildValues(fields: fields, written: [fields[1].id: "svc-env-api-key"])
        XCTAssertEqual(env["REGION"], "us-east-1")
        XCTAssertEqual(env["API_KEY"], "${keyring:svc-env-api-key}")
    }

    func testBuildValuesRoutesHeaderKindToHeadersMap() {
        let fields = [
            SecretFieldInput(name: "Authorization", kind: .header, value: "sk-live-secret", mode: .secret),
            SecretFieldInput(name: "GITHUB_TOKEN", kind: .env, value: "sk-live-secret", mode: .secret),
        ]
        let written = [fields[0].id: "svc-header-authorization", fields[1].id: "svc-env-github-token"]
        let (env, headers) = SecretFieldResolver.buildValues(fields: fields, written: written)
        XCTAssertEqual(headers["Authorization"], "${keyring:svc-header-authorization}")
        XCTAssertEqual(env["GITHUB_TOKEN"], "${keyring:svc-env-github-token}")
        XCTAssertNil(env["Authorization"])
        XCTAssertNil(headers["GITHUB_TOKEN"])
    }

    /// An env field and a header field sharing one NAME must not collide on
    /// one `field.id` — `SecretFieldInput.id` folds in `kind` precisely so
    /// `written` can hold both refs independently (FR-065).
    func testFieldIdentityDistinguishesEnvAndHeaderOfSameName() {
        let env = SecretFieldInput(name: "API_KEY", kind: .env, value: "a", mode: .secret)
        let header = SecretFieldInput(name: "API_KEY", kind: .header, value: "b", mode: .secret)
        XCTAssertNotEqual(env.id, header.id)
    }
}
