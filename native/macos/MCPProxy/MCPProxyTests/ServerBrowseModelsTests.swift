import XCTest
@testable import MCPProxy

/// macOS mirror of Web UI R1 (cross-registry browse). Covers the pure units the
/// ServerBrowseView relies on: transport classification (R2 parity), the
/// search-response decode (incl. the per-registry `unavailable` marker), the
/// add-from-registry error decode (`missing_inputs`), and the
/// encodeURIComponent-equivalent path encoder.
final class ServerBrowseModelsTests: XCTestCase {

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        try JSONDecoder().decode(T.self, from: jsonString.data(using: .utf8)!)
    }

    private func server(install: String? = nil, url: String? = nil) -> RepositoryServer {
        RepositoryServer(id: "x", name: "x", description: nil, url: url, sourceCodeURL: nil,
                         installCmd: install, connectURL: nil, registry: "r", requiredInputs: nil)
    }

    // MARK: - Transport classification (mirrors Web UI serverTransport)

    func testTransportNpm() {
        XCTAssertEqual(server(install: "npx -y @modelcontextprotocol/server-memory").transport, "stdio:npm")
        XCTAssertEqual(server(install: "npm exec foo").transport, "stdio:npm")
    }

    func testTransportPython() {
        XCTAssertEqual(server(install: "uvx mcp-server-fetch").transport, "stdio:python")
        XCTAssertEqual(server(install: "pipx run mcp-hn").transport, "stdio:python")
        XCTAssertEqual(server(install: "python3 -m server").transport, "stdio:python")
    }

    func testTransportDocker() {
        XCTAssertEqual(server(install: "docker run -i --rm mcp/everything").transport, "stdio:docker")
    }

    func testTransportRemote() {
        XCTAssertEqual(server(install: nil, url: "https://api.example.com/mcp").transport, "remote")
    }

    func testTransportStdioFallbackAndUrlIgnoredWhenInstallPresent() {
        XCTAssertEqual(server(install: "./run.sh").transport, "stdio")
        // A hybrid entry (install cmd + url) prefers the stdio classification.
        XCTAssertEqual(server(install: "uvx foo", url: "https://x/mcp").transport, "stdio:python")
    }

    // MARK: - Search response decode

    func testSearchResponseDecodesServersAndUnavailable() throws {
        let json = """
        {"registry_id":"pulse","servers":[],"total":0,
         "unavailable":{"reason":"registry requires an API key"}}
        """
        let resp = try decode(SearchRegistryServersResponse.self, from: json)
        XCTAssertEqual(resp.registryID, "pulse")
        XCTAssertEqual(resp.servers?.count, 0)
        XCTAssertEqual(resp.unavailable?.reason, "registry requires an API key")
    }

    func testRepositoryServerDecodesSnakeCaseFields() throws {
        let json = """
        {"id":"io.github.x/y","name":"Y","description":"d",
         "install_cmd":"uvx y","source_code_url":"https://github.com/x/y",
         "registry":"Reference Servers",
         "required_inputs":[{"name":"TOKEN","secret":true}]}
        """
        let s = try decode(RepositoryServer.self, from: json)
        XCTAssertEqual(s.installCmd, "uvx y")
        XCTAssertEqual(s.sourceCodeURL, "https://github.com/x/y")
        XCTAssertEqual(s.registry, "Reference Servers")
        XCTAssertEqual(s.requiredInputs?.first?.name, "TOKEN")
        XCTAssertEqual(s.transport, "stdio:python")
    }

    // MARK: - Add-from-registry error decode

    func testAddServerErrorDecodesMissingInputs() throws {
        let json = """
        {"code":"missing_required_input","message":"needs env",
         "missing_inputs":["GITHUB_TOKEN","ORG"]}
        """
        let err = try decode(RegistryAddServerErrorBody.self, from: json)
        XCTAssertEqual(err.code, "missing_required_input")
        XCTAssertEqual(err.missingInputs, ["GITHUB_TOKEN", "ORG"])
    }

    func testAddServerResultFactories() {
        XCTAssertTrue(AddServerResult.ok().success)
        let f = AddServerResult.failure(message: "boom", missingInputs: ["A"])
        XCTAssertFalse(f.success)
        XCTAssertEqual(f.missingInputs, ["A"])
    }

    // MARK: - encodeURIComponent-equivalent path encoder

    func testURIComponentEncodingEscapesSlashesAndSpaces() {
        XCTAssertEqual("io.github.x/y".uriComponentEncoded, "io.github.x%2Fy")
        XCTAssertEqual("a b".uriComponentEncoded, "a%20b")
        XCTAssertEqual("plain-id_1.0".uriComponentEncoded, "plain-id_1.0")
    }
}
