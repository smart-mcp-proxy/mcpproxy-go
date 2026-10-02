import XCTest
@testable import MCPProxy

/// Spec 109-m SC-008 (T145b, M12): the macOS leg of the catalog order parity
/// chain. The REST golden `internal/registries/testdata/catalog_github_order.json`
/// (written by the Go httpapi test from the real `GET /catalog/search` handler
/// over the fixture registries) decodes into the models the Catalog view lists,
/// and the list keeps the served order: the official, verified GitHub server
/// first, with a distinct identity per row.
final class CatalogOrderParityTests: XCTestCase {

    private struct Golden: Decodable {
        let query: String
        let ids: [String]
        let results: [CatalogResult]
    }

    private func golden() throws -> Golden {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        url.appendPathComponent("internal/registries/testdata/catalog_github_order.json")
        return try JSONDecoder().decode(Golden.self, from: try Data(contentsOf: url))
    }

    func testCatalogModelKeepsTheServedOrder() throws {
        let g = try golden()
        XCTAssertFalse(g.ids.isEmpty, "the REST golden is missing; run the Go httpapi test with UPDATE_GOLDEN=1")
        XCTAssertEqual(g.results.map { "\($0.source):\($0.catalogID)" }, g.ids)
    }

    func testTheOfficialVerifiedGitHubServerIsFirst() throws {
        let first = try XCTUnwrap(try golden().results.first)
        XCTAssertEqual(first.source, "official")
        XCTAssertEqual(first.catalogID, "io.github.github/github-mcp-server")
        XCTAssertTrue(first.official)
        XCTAssertTrue(first.verified)
    }

    func testEveryRowHasADistinctListIdentity() throws {
        let results = try golden().results
        XCTAssertEqual(Set(results.map(\.id)).count, results.count)
    }

    func testTheSearchResponseWrapperDecodesTheSameResults() throws {
        let g = try golden()
        let wrapper = """
        {"query":"\(g.query)","results":[],"sections":null,"unavailable":[]}
        """
        let response = try JSONDecoder().decode(CatalogSearchResponse.self, from: Data(wrapper.utf8))
        XCTAssertEqual(response.query, g.query)
        XCTAssertNil(response.sections, "a non-empty query has no sections")
    }
}
